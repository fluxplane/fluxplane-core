package openapi

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	fpsystem "github.com/fluxplane/fluxplane-system"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"

	"github.com/fluxplane/fluxplane-core/core/operation"
	coresecret "github.com/fluxplane/fluxplane-core/core/secret"
	operationruntime "github.com/fluxplane/fluxplane-core/runtime/operation"
	runtimesecret "github.com/fluxplane/fluxplane-core/runtime/secret"
	"github.com/fluxplane/fluxplane-policy"
	"github.com/fluxplane/fluxplane-system/systemkit"
	"github.com/getkin/kin-openapi/openapi3"
)

type openAPIOperation struct {
	network     fpsystem.Network
	environment fpsystem.Environment
	def         operationDefinition
}

func (o openAPIOperation) Spec() operation.Spec { return o.def.Spec }

func (o openAPIOperation) Run(ctx operation.Context, input operation.Value) operation.Result {
	req, err := bindRequestInput(input)
	if err != nil {
		return operation.Failed("invalid_"+o.def.Name+"_input", err.Error(), nil)
	}
	httpReq, err := o.httpRequest(ctx, req)
	if err != nil {
		return operation.Failed(o.def.Name+"_failed", err.Error(), nil)
	}
	client := systemkit.NewHTTPClient(o.network)
	resp, err := client.Do(httpReq)
	if err != nil {
		return operation.Failed(o.def.Name+"_failed", err.Error(), nil)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 10*1024*1024+1))
	if err != nil {
		return operation.Failed(o.def.Name+"_failed", err.Error(), nil)
	}
	if len(body) > 10*1024*1024 {
		return operation.Failed(o.def.Name+"_failed", "response exceeds 10485760 bytes", nil)
	}
	return operation.OK(map[string]any{
		"status":  resp.StatusCode,
		"headers": responseHeaders(resp.Header),
		"body":    decodeResponseBody(resp.Header.Get("Content-Type"), body),
	})
}

func (o openAPIOperation) Access(ctx operation.Context, input operation.Value) ([]operationruntime.AccessDescriptor, error) {
	req, err := bindRequestInput(input)
	if err != nil {
		return nil, err
	}
	target, err := o.requestURL(req)
	if err != nil {
		return nil, err
	}
	action := policy.ActionNetworkFetch
	switch o.def.Method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
	default:
		action = policy.ActionNetworkConnect
	}
	access := []operationruntime.AccessDescriptor{operationruntime.NetworkDescriptor(target, action)}
	for _, scheme := range configuredSecuritySchemes(o.def.Security, o.def.AuthByScheme) {
		ref := coresecret.Plugin(Name, o.def.Instance, scheme)
		access = append(access, operationruntime.AccessDescriptor{
			Resource: policy.ResourceRef{Kind: policy.ResourceSecret, Name: ref.ResourceName()},
			Action:   policy.ActionSecretUse,
			Reason:   "openapi auth scheme " + scheme,
		})
	}
	return access, nil
}

func (o openAPIOperation) httpRequest(ctx operation.Context, in requestInput) (*http.Request, error) {
	if o.network == nil {
		return nil, fmt.Errorf("openapi network is nil")
	}
	target, err := o.requestURL(in)
	if err != nil {
		return nil, err
	}
	body, contentType, err := requestBody(in.Body)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, o.def.Method, target, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	for key, value := range in.Headers {
		if key = strings.TrimSpace(key); key != "" {
			req.Header.Set(key, scalarString(value))
		}
	}
	if contentType != "" && req.Header.Get("Content-Type") == "" {
		req.Header.Set("Content-Type", contentType)
	}
	for key, value := range in.Cookies {
		req.AddCookie(&http.Cookie{Name: key, Value: scalarString(value)})
	}
	if err := o.applyAuth(ctx, req, in); err != nil {
		return nil, err
	}
	return req, nil
}

func (o openAPIOperation) requestURL(in requestInput) (string, error) {
	if strings.TrimSpace(o.def.Server) == "" {
		return "", fmt.Errorf("openapi operation %s has no server URL", o.def.Name)
	}
	baseURL := strings.TrimRight(o.def.Server, "/") + "/"
	base, err := url.Parse(baseURL)
	if err != nil {
		return "", err
	}
	path := o.def.Path
	for key, value := range in.Path {
		path = strings.ReplaceAll(path, "{"+key+"}", url.PathEscape(scalarString(value)))
	}
	rel, err := url.Parse(strings.TrimLeft(path, "/"))
	if err != nil {
		return "", err
	}
	target := base.ResolveReference(rel)
	q := target.Query()
	for key, value := range in.Query {
		q.Set(key, scalarString(value))
	}
	target.RawQuery = q.Encode()
	return target.String(), nil
}

func (o openAPIOperation) applyAuth(ctx operation.Context, req *http.Request, in requestInput) error {
	if len(o.def.Security) == 0 {
		return nil
	}
	schemeNames := configuredSecuritySchemes(o.def.Security, o.def.AuthByScheme)
	if len(schemeNames) == 0 {
		return fmt.Errorf("openapi operation %s requires auth but no configured security scheme matches", o.def.Name)
	}
	if o.environment == nil {
		return fmt.Errorf("openapi auth environment is nil")
	}
	for _, schemeName := range schemeNames {
		method := o.def.AuthByScheme[schemeName]
		broker := runtimesecret.NewBroker(runtimesecret.EnvResolver{Environment: o.environment, Kind: method.Kind})
		resolution, ok, err := broker.UseAvailable(ctx, coresecret.AuthRequest{
			Plugin:   Name,
			Instance: o.def.Instance,
			Purpose:  schemeName,
			Methods:  []coresecret.AuthMethodSpec{method},
		})
		if err != nil {
			return fmt.Errorf("openapi use auth secret: %w", err)
		}
		if !ok || strings.TrimSpace(resolution.Material.Value) == "" {
			return fmt.Errorf("openapi auth secret is not configured for scheme %s", schemeName)
		}
		o.applyAuthMaterial(req, in, schemeName, resolution.Material)
	}
	return nil
}

func (o openAPIOperation) applyAuthMaterial(req *http.Request, in requestInput, schemeName string, material coresecret.Material) {
	scheme := o.def.SecuritySchemes[schemeName]
	value := material.Value
	if scheme == nil {
		return
	}
	if strings.EqualFold(scheme.Type, "http") {
		switch strings.ToLower(scheme.Scheme) {
		case "bearer":
			req.Header.Set("Authorization", "Bearer "+value)
		case "basic":
			req.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(value)))
		}
		return
	}
	if strings.EqualFold(scheme.Type, "apiKey") {
		switch strings.ToLower(scheme.In) {
		case "header":
			req.Header.Set(scheme.Name, value)
		case "query":
			q := req.URL.Query()
			q.Set(scheme.Name, value)
			req.URL.RawQuery = q.Encode()
		case "cookie":
			req.AddCookie(&http.Cookie{Name: scheme.Name, Value: value})
		}
		return
	}
	if strings.EqualFold(scheme.Type, "oauth2") || strings.EqualFold(scheme.Type, "openIdConnect") {
		req.Header.Set("Authorization", "Bearer "+value)
	}
}

type requestInput struct {
	Path    map[string]any `json:"path,omitempty"`
	Query   map[string]any `json:"query,omitempty"`
	Headers map[string]any `json:"headers,omitempty"`
	Cookies map[string]any `json:"cookies,omitempty"`
	Body    any            `json:"body,omitempty"`
}

func bindRequestInput(input any) (requestInput, error) {
	var out requestInput
	if input == nil {
		return out, nil
	}
	data, err := json.Marshal(input)
	if err != nil {
		return out, err
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return out, err
	}
	return out, nil
}

func requestBody(value any) ([]byte, string, error) {
	if value == nil {
		return nil, "", nil
	}
	switch typed := value.(type) {
	case string:
		return []byte(typed), "text/plain", nil
	case []byte:
		return typed, "application/octet-stream", nil
	default:
		data, err := json.Marshal(value)
		return data, "application/json", err
	}
}

func scalarString(value any) string {
	switch typed := value.(type) {
	case nil:
		return ""
	case string:
		return typed
	case fmt.Stringer:
		return typed.String()
	case bool:
		if typed {
			return "true"
		}
		return "false"
	case float64:
		return strings.TrimRight(strings.TrimRight(fmt.Sprintf("%f", typed), "0"), ".")
	case float32:
		return strings.TrimRight(strings.TrimRight(fmt.Sprintf("%f", typed), "0"), ".")
	default:
		return fmt.Sprintf("%v", typed)
	}
}

func configuredSecuritySchemes(requirements openapi3.SecurityRequirements, methods map[string]coresecret.AuthMethodSpec) []string {
	for _, requirement := range requirements {
		if len(requirement) == 0 {
			return nil
		}
		names := make([]string, 0, len(requirement))
		for name := range requirement {
			names = append(names, name)
		}
		for _, name := range names {
			if _, ok := methods[name]; !ok {
				names = nil
				break
			}
		}
		if len(names) > 0 {
			sort.Strings(names)
			return names
		}
	}
	return nil
}

func responseHeaders(headers http.Header) map[string]string {
	out := map[string]string{}
	for key, values := range headers {
		if len(values) > 0 {
			out[key] = values[0]
		}
	}
	return out
}

func decodeResponseBody(contentType string, data []byte) any {
	if strings.Contains(strings.ToLower(contentType), "json") {
		var value any
		if json.Unmarshal(data, &value) == nil {
			return value
		}
	}
	return string(data)
}
