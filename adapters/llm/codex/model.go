package codex

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	adapterllm "github.com/fluxplane/fluxplane-core/adapters/llm"
	"github.com/fluxplane/fluxplane-core/adapters/llm/openai"
	corellm "github.com/fluxplane/fluxplane-core/core/llm"
	"github.com/openai/openai-go/v3/option"
)

// Config configures the Codex Responses backend.
type Config struct {
	Model             string
	AuthPath          string
	BaseURL           string
	ParallelToolCalls bool
	Redactor          adapterllm.Redactor
	HTTPClient        *http.Client
	Runtime           openai.ResponsesRuntimeConfig
	Pricing           []corellm.PricingSpec
	ReasoningEffort   string
	ReasoningSummary  string
}

// New returns a Codex-backed Responses model.
func New(cfg Config) (*openai.Model, error) {
	baseURL := cfg.BaseURL
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	runtime := cfg.Runtime
	if runtime.Transport == "" || runtime.Transport == openai.ResponsesTransportAuto {
		runtime.Transport = openai.ResponsesTransportWebSocket
	}
	if runtime.Cache == "" {
		runtime.Cache = openai.ResponsesCacheMax
	}
	if runtime.Continuation == "" || runtime.Continuation == openai.ResponsesContinuationAuto {
		if runtime.Transport == openai.ResponsesTransportSSE {
			runtime.Continuation = openai.ResponsesContinuationReplay
		} else {
			runtime.Continuation = openai.ResponsesContinuationProvider
		}
	}
	if runtime.Transport == openai.ResponsesTransportSSE && runtime.Continuation == openai.ResponsesContinuationProvider {
		return nil, fmt.Errorf("codex: provider continuation requires websocket transport; HTTP/SSE endpoint rejects previous_response_id")
	}
	if runtime.WebSocketWarmup == "" {
		runtime.WebSocketWarmup = openai.ResponsesWebSocketWarmupAuto
	}
	if runtime.Output == "" {
		runtime.Output = openai.ResponsesOutputStreamItems
	}
	auth, err := loadAuth(cfg.AuthPath, cfg.HTTPClient)
	if err != nil {
		return nil, err
	}
	return openai.New(openai.Config{
		Model:                               cfg.Model,
		ProviderName:                        "codex",
		APIName:                             "codex.responses",
		BaseURL:                             baseURL,
		APIKey:                              "codex-auth-via-middleware",
		HTTPClient:                          cfg.HTTPClient,
		Runtime:                             runtime,
		AllowStoreFalseProviderContinuation: true,
		Pricing:                             cfg.Pricing,
		ReasoningEffort:                     cfg.ReasoningEffort,
		ReasoningSummary:                    cfg.ReasoningSummary,
		ParallelToolCalls:                   cfg.ParallelToolCalls,
		Redactor:                            cfg.Redactor,
		WebSocketHeaderFunc:                 codexWebSocketHeaderFunc(auth),
		PayloadMutator:                      mutateRawPayload,
		RequestOptions: []option.RequestOption{
			option.WithMiddleware(codexMiddleware(auth)),
			option.WithHeader("originator", "codex_cli_rs"),
			option.WithHeader("version", "0.124.0"),
			option.WithHeader("OpenAI-Beta", "responses_websockets=2026-02-06"),
		},
	})
}

func codexMiddleware(auth *auth) option.Middleware {
	return func(req *http.Request, next option.MiddlewareNext) (*http.Response, error) {
		if err := auth.setHeaders(contextForRequest(req), req.Header); err != nil {
			return nil, err
		}
		if err := mutateRequestBody(req); err != nil {
			return nil, err
		}
		req.Header.Set("User-Agent", "codex-cli/0.124.0")
		req.Header.Set("x-codex-installation-id", "fluxplane")
		resp, err := next(req)
		if err != nil {
			return nil, err
		}
		if resp == nil || resp.StatusCode < 400 {
			return resp, nil
		}
		body := readAndReplaceBody(resp)
		return nil, fmt.Errorf("codex: HTTP %s: %s", resp.Status, body)
	}
}

func codexWebSocketHeaderFunc(auth *auth) func(context.Context, http.Header) error {
	return func(ctx context.Context, h http.Header) error {
		if err := auth.setHeaders(ctx, h); err != nil {
			return err
		}
		h.Set("originator", "codex_cli_rs")
		h.Set("version", "0.124.0")
		h.Set("OpenAI-Beta", "responses_websockets=2026-02-06")
		h.Set("User-Agent", "codex-cli/0.124.0")
		h.Set("x-codex-installation-id", "fluxplane")
		return nil
	}
}

func mutateRequestBody(req *http.Request) error {
	if req == nil || req.Body == nil {
		return nil
	}
	raw, err := io.ReadAll(req.Body)
	if err != nil {
		return fmt.Errorf("codex: read request body: %w", err)
	}
	if err := req.Body.Close(); err != nil {
		return fmt.Errorf("codex: close request body: %w", err)
	}
	mutated, ok, err := mutateBody(raw)
	if err != nil {
		return err
	}
	if !ok {
		mutated = raw
	}
	req.Body = io.NopCloser(bytes.NewReader(mutated))
	req.ContentLength = int64(len(mutated))
	req.GetBody = func() (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(mutated)), nil
	}
	return nil
}

func mutateBody(raw []byte) ([]byte, bool, error) {
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, false, nil
	}
	payload["store"] = false
	delete(payload, "max_output_tokens")
	delete(payload, "temperature")
	delete(payload, "top_p")
	delete(payload, "top_k")
	delete(payload, "response_format")
	delete(payload, "prompt_cache_retention")
	delete(payload, "previous_response_id")
	if text, ok := payload["text"].(map[string]any); ok && len(text) == 0 {
		delete(payload, "text")
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return nil, false, fmt.Errorf("codex: encode request body: %w", err)
	}
	return encoded, true, nil
}

func mutateRawPayload(payload map[string]json.RawMessage) {
	if payload == nil {
		return
	}
	payload["store"] = json.RawMessage("false")
	for _, key := range []string{
		"max_output_tokens",
		"temperature",
		"top_p",
		"top_k",
		"response_format",
		"prompt_cache_retention",
		"previous_response_id",
	} {
		delete(payload, key)
	}
	if text, ok := payload["text"]; ok {
		var textPayload map[string]json.RawMessage
		if err := json.Unmarshal(text, &textPayload); err == nil && len(textPayload) == 0 {
			delete(payload, "text")
		}
	}
}

func readAndReplaceBody(resp *http.Response) string {
	if resp == nil || resp.Body == nil {
		return ""
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	_ = resp.Body.Close()
	resp.Body = io.NopCloser(bytes.NewReader(data))
	if err != nil {
		return "read response body: " + err.Error()
	}
	body := strings.TrimSpace(string(data))
	if body == "" {
		return "<empty response body>"
	}
	return body
}

func contextForRequest(req *http.Request) context.Context {
	if req == nil || req.Context() == nil {
		return context.Background()
	}
	return req.Context()
}
