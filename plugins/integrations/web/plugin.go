package web

import (
	"context"
	"fmt"
	fpsystem "github.com/fluxplane/fluxplane-system"
	"mime"
	"strings"
	"time"

	"github.com/fluxplane/fluxplane-core/adapters/content/htmlconvert"
	coredata "github.com/fluxplane/fluxplane-core/core/data"
	"github.com/fluxplane/fluxplane-core/core/operation"
	"github.com/fluxplane/fluxplane-core/core/resource"
	"github.com/fluxplane/fluxplane-core/core/usage"
	"github.com/fluxplane/fluxplane-core/orchestration/pluginhost"
	operationruntime "github.com/fluxplane/fluxplane-core/runtime/operation"
	"github.com/fluxplane/fluxplane-policy"
	"github.com/fluxplane/fluxplane-system/systemkit"
)

const (
	Name         = "web"
	RequestOp    = "web_request"
	SearchOp     = "web_search"
	maxBodyBytes = 5 * 1024 * 1024
)

// Plugin contributes outbound web operations.
type Plugin struct {
	system fpsystem.System
}

var _ pluginhost.Plugin = Plugin{}
var _ pluginhost.OperationContributor = Plugin{}

// New returns a web plugin.
func New(sys fpsystem.System) Plugin { return Plugin{system: sys} }

// Manifest returns plugin metadata.
func (Plugin) Manifest() pluginhost.Manifest {
	return pluginhost.Manifest{Name: Name, Description: "HTTP request operations."}
}

// Contributions returns web specs.
func (Plugin) Contributions(context.Context, pluginhost.Context) (resource.ContributionBundle, error) {
	request := requestSpec()
	search := searchSpec()
	return resource.ContributionBundle{
		OperationSets: []operation.Set{{Name: Name, Description: "Outbound web operations.", Operations: []operation.Ref{request.Ref, search.Ref}}},
		Operations:    []operation.Spec{request, search},
		DataSources:   []coredata.SourceSpec{DataSourceSpec()},
	}, nil
}

// Operations returns executable web operations.
func (p Plugin) Operations(context.Context, pluginhost.Context) ([]operation.Operation, error) {
	if p.system == nil {
		return nil, fmt.Errorf("webplugin: system is nil")
	}
	return []operation.Operation{
		operationruntime.NewTypedResult[requestInput, map[string]any](requestSpec(), p.request(), operationruntime.WithIntent(requestIntent), operationruntime.WithAccess(requestAccess)),
		operationruntime.NewTypedResult[searchInput, searchOutput](searchSpec(), p.search(), operationruntime.WithIntent(searchIntent), operationruntime.WithAccess(searchAccess)),
	}, nil
}

func requestSpec() operation.Spec {
	return operationruntime.WithTypedContract[requestInput, map[string]any](operation.Spec{
		Ref:         operation.Ref{Name: RequestOp},
		Description: "Perform one bounded HTTP request. HTML responses are converted to readable Markdown-like text.",
		Semantics: operation.Semantics{
			Determinism: operation.DeterminismNonDeterministic,
			Effects:     operation.EffectSet{operation.EffectNetwork, operation.EffectReadExternal},
			Idempotency: operation.IdempotencyUnknown,
			Risk:        operation.RiskMedium,
		},
	})
}

type requestInput struct {
	URL       string            `json:"url" jsonschema:"description=Absolute http or https URL.,required"`
	Method    string            `json:"method,omitempty" jsonschema:"description=HTTP method. Defaults to GET."`
	Headers   map[string]string `json:"headers,omitempty" jsonschema:"description=HTTP request headers."`
	Body      string            `json:"body,omitempty" jsonschema:"description=Request body for methods that support one."`
	TimeoutMS int               `json:"timeout_ms,omitempty" jsonschema:"description=Timeout in milliseconds."`
	MaxBytes  int               `json:"max_bytes,omitempty" jsonschema:"description=Maximum response body bytes."`
}

func requestIntent(_ operation.Context, req requestInput) (operation.IntentSet, error) {
	if strings.TrimSpace(req.URL) == "" {
		return operation.IntentSet{}, fmt.Errorf("url is required")
	}
	return operation.IntentSet{Operations: []operation.IntentOperation{{
		Behavior:  operation.IntentNetworkFetch,
		Target:    operation.URLTarget{URL: operation.URL(req.URL)},
		Role:      operation.IntentRoleNetworkTarget,
		Certainty: operation.IntentCertain,
	}}}, nil
}

func requestAccess(_ operation.Context, req requestInput) ([]operationruntime.AccessDescriptor, error) {
	url := strings.TrimSpace(req.URL)
	if url == "" {
		return nil, fmt.Errorf("url is required")
	}
	action := policy.ActionNetworkFetch
	switch strings.ToUpper(strings.TrimSpace(req.Method)) {
	case "", "GET", "HEAD", "OPTIONS":
	default:
		action = policy.ActionNetworkConnect
	}
	return []operationruntime.AccessDescriptor{operationruntime.NetworkDescriptor(url, action)}, nil
}

func (p Plugin) request() operationruntime.TypedResultHandler[requestInput, map[string]any] {
	return func(ctx operation.Context, req requestInput) operation.Result {
		if strings.TrimSpace(req.URL) == "" {
			return operation.Failed("invalid_web_request_input", "url is required", nil)
		}
		method := strings.ToUpper(strings.TrimSpace(req.Method))
		if method == "" {
			method = "GET"
		}
		if !allowedMethod(method) {
			return operation.Rejected("web_request_method_denied", "unsupported HTTP method", map[string]any{"method": method})
		}
		timeout := time.Duration(req.TimeoutMS) * time.Millisecond
		if timeout <= 0 || timeout > 60*time.Second {
			timeout = 30 * time.Second
		}
		maxBytes := req.MaxBytes
		if maxBytes <= 0 || maxBytes > maxBodyBytes {
			maxBytes = 512 * 1024
		}
		resp, err := systemkit.DoHTTP(ctx, p.system.Network(), systemkit.HTTPRequest{
			URL: req.URL, Method: method, Headers: req.Headers, Body: []byte(req.Body),
			Timeout: timeout, MaxBytes: maxBytes, UserAgent: "fluxplane/0.1",
		})
		if err != nil {
			return operation.Failed("web_request_failed", err.Error(), nil)
		}
		body := resp.Body
		contentType := resp.ContentType
		renderedBody := string(resp.Body)
		if isHTML(contentType) {
			renderedBody = htmlconvert.ToMarkdown(renderedBody)
		}
		emitUsage(ctx, resp.FinalURL, usage.DirectionUpload, float64(len(req.Body)))
		emitUsage(ctx, resp.FinalURL, usage.DirectionDownload, float64(len(body)))
		data := map[string]any{
			"url":          resp.URL,
			"final_url":    resp.FinalURL,
			"method":       method,
			"status":       resp.Status,
			"status_code":  resp.StatusCode,
			"headers":      resp.Headers,
			"content_type": contentType,
			"body":         renderedBody,
			"truncated":    resp.Truncated,
			"duration_ms":  resp.Duration.Milliseconds(),
		}
		text := fmt.Sprintf("HTTP %s %s\nStatus: %s\nContent-Type: %s\nBytes: %d truncated=%v\n\n%s", method, resp.URL, resp.Status, contentType, len(body), resp.Truncated, renderedBody)
		return operation.OK(operation.Rendered{Text: strings.TrimSpace(text), Data: data})
	}
}

func allowedMethod(method string) bool {
	switch method {
	case "GET", "HEAD", "POST", "PUT", "PATCH", "DELETE", "OPTIONS":
		return true
	default:
		return false
	}
}

func isHTML(contentType string) bool {
	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		return strings.Contains(strings.ToLower(contentType), "html")
	}
	return mediaType == "text/html" || mediaType == "application/xhtml+xml"
}

func emitUsage(ctx operation.Context, host string, direction usage.Direction, bytes float64) {
	ctx.Events().Emit(usage.Recorded{
		Source: RequestOp,
		Subject: usage.Subject{
			Kind: usage.SubjectNetwork,
			Name: host,
		},
		Measurements: []usage.Measurement{
			{Metric: usage.MetricRequests, Quantity: 1, Unit: usage.UnitRequest},
			{Metric: usage.MetricNetworkBytes, Quantity: bytes, Unit: usage.UnitByte, Direction: direction},
		},
	})
}
