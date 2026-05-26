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
	llmagent "github.com/fluxplane/fluxplane-core/runtime/agent/llmagent"
	openaigo "github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/packages/param"
	"github.com/openai/openai-go/v3/responses"
)

const (
	codexOriginator      = "codex_cli_rs"
	codexVersion         = "0.124.0"
	codexBetaHeader      = "responses_websockets=2026-02-06"
	codexUserAgent       = "codex-cli/" + codexVersion
	codexInstallationID  = "fluxplane"
	codexTurnStateHeader = "x-codex-turn-state"
	codexWindowIDHeader  = "x-codex-window-id"
	codexParentHeader    = "x-codex-parent-thread-id"
	codexTurnMetaHeader  = "x-codex-turn-metadata"
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
		WebSocketRequestHeaderFunc:          codexWebSocketRequestHeaderFunc(),
		WebSocketStickyHeader:               codexTurnStateHeader,
		WebSocketResponseProcessed:          true,
		WebSocketSessionFallback:            true,
		ValidateRequest:                     validateCodexRequest,
		PrepareRequestContext:               prepareCodexRequestContext,
		PromptCacheKeyFunc:                  codexPromptCacheKey,
		ResponseParamsFunc:                  codexResponseParams,
		WebSocketPayloadFunc:                codexWebSocketPayload,
		WebSocketWrappedErrorFunc:           codexWrappedWebSocketError,
		WebSocketSkipMalformedEvents:        true,
		RequestOptions: []option.RequestOption{
			option.WithMiddleware(codexMiddleware(auth)),
			option.WithHeader("originator", codexOriginator),
			option.WithHeader("version", codexVersion),
			option.WithHeader("OpenAI-Beta", codexBetaHeader),
		},
	})
}

func codexMiddleware(auth *auth) option.Middleware {
	return func(req *http.Request, next option.MiddlewareNext) (*http.Response, error) {
		if err := auth.setHeaders(contextForRequest(req), req.Header); err != nil {
			return nil, err
		}
		req.Header.Set("User-Agent", codexUserAgent)
		req.Header.Set("x-codex-installation-id", codexInstallationID)
		if meta, ok := openai.ResponsesProtocolMetadataFromContext(contextForRequest(req)); ok {
			applyCodexProtocolHeaders(req.Header, meta)
		}
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

func validateCodexRequest(req llmagent.Request) error {
	if strings.TrimSpace(req.ConversationKey) == "" {
		return fmt.Errorf("codex: conversation key is required")
	}
	return nil
}

func prepareCodexRequestContext(ctx context.Context, req llmagent.Request) (context.Context, error) {
	meta, err := codexProtocolMetadata(req)
	if err != nil {
		return nil, err
	}
	return openai.ContextWithResponsesProtocolMetadata(ctx, meta), nil
}

func codexPromptCacheKey(_ string, req llmagent.Request) (string, error) {
	key := strings.TrimSpace(req.ConversationKey)
	if key == "" {
		return "", fmt.Errorf("codex: conversation key is required for prompt cache")
	}
	return key, nil
}

func codexResponseParams(params *responses.ResponseNewParams, req llmagent.Request) error {
	if params == nil {
		return nil
	}
	meta, err := codexProtocolMetadata(req)
	if err != nil {
		return err
	}
	params.Store = openaigo.Bool(false)
	params.MaxOutputTokens = param.Opt[int64]{}
	params.Temperature = param.Opt[float64]{}
	params.TopP = param.Opt[float64]{}
	params.PromptCacheRetention = ""
	params.PreviousResponseID = param.Opt[string]{}
	params.ToolChoice = responses.ResponseNewParamsToolChoiceUnion{
		OfToolChoiceMode: param.NewOpt(responses.ToolChoiceOptionsAuto),
	}
	params.SetExtraFields(map[string]any{
		"client_metadata": codexClientMetadata(meta),
	})
	return nil
}

func codexWebSocketHeaderFunc(auth *auth) func(context.Context, http.Header) error {
	return func(ctx context.Context, h http.Header) error {
		if err := auth.setHeaders(ctx, h); err != nil {
			return err
		}
		h.Set("originator", codexOriginator)
		h.Set("version", codexVersion)
		h.Set("OpenAI-Beta", codexBetaHeader)
		h.Set("User-Agent", codexUserAgent)
		h.Set("x-codex-installation-id", codexInstallationID)
		return nil
	}
}

func codexWebSocketRequestHeaderFunc() func(context.Context, llmagent.Request, http.Header) error {
	return func(_ context.Context, req llmagent.Request, h http.Header) error {
		meta, err := codexProtocolMetadata(req)
		if err != nil {
			return err
		}
		applyCodexProtocolHeaders(h, meta)
		return nil
	}
}

func codexProtocolMetadata(req llmagent.Request) (openai.ResponsesProtocolMetadata, error) {
	threadID := strings.TrimSpace(req.ConversationKey)
	if threadID == "" {
		return openai.ResponsesProtocolMetadata{}, fmt.Errorf("codex: conversation key is required")
	}
	windowID := fmt.Sprintf("%s:%d", threadID, req.Protocol.WindowGeneration)
	meta := openai.ResponsesProtocolMetadata{
		ConversationKey: threadID,
		WindowID:        windowID,
		Subagent:        strings.TrimSpace(req.Protocol.Subagent),
		ParentThreadID:  strings.TrimSpace(req.Protocol.ParentThreadID),
		TurnMetadata:    strings.TrimSpace(req.Protocol.TurnMetadata),
		TraceParent:     strings.TrimSpace(req.Protocol.TraceParent),
		TraceState:      strings.TrimSpace(req.Protocol.TraceState),
		BetaFeatures:    strings.TrimSpace(req.Protocol.BetaFeatures),
		Attestation:     strings.TrimSpace(req.Protocol.Attestation),
		ClientMetadata:  cloneStringMap(req.Protocol.ClientMetadata),
	}
	return meta, nil
}

func applyCodexProtocolHeaders(h http.Header, meta openai.ResponsesProtocolMetadata) {
	if h == nil {
		return
	}
	if meta.ConversationKey != "" {
		h.Set("x-client-request-id", meta.ConversationKey)
		h.Set("session-id", meta.ConversationKey)
		h.Set("thread-id", meta.ConversationKey)
	}
	if meta.WindowID != "" {
		h.Set(codexWindowIDHeader, meta.WindowID)
	}
	if meta.Subagent != "" {
		h.Set("x-openai-subagent", meta.Subagent)
	}
	if meta.ParentThreadID != "" {
		h.Set(codexParentHeader, meta.ParentThreadID)
	}
	if meta.TurnMetadata != "" {
		h.Set(codexTurnMetaHeader, meta.TurnMetadata)
	}
	if meta.BetaFeatures != "" {
		h.Set("x-codex-beta-features", meta.BetaFeatures)
	}
	if meta.Attestation != "" {
		h.Set("x-oai-attestation", meta.Attestation)
	}
}

func codexWebSocketPayload(req llmagent.Request, payload map[string]json.RawMessage) error {
	if payload == nil {
		return nil
	}
	meta, err := codexProtocolMetadata(req)
	if err != nil {
		return err
	}
	payload["store"] = json.RawMessage("false")
	payload["stream"] = json.RawMessage("true")
	payload["tool_choice"] = mustRawJSON("auto")
	delete(payload, "max_output_tokens")
	delete(payload, "temperature")
	delete(payload, "top_p")
	delete(payload, "top_k")
	delete(payload, "response_format")
	delete(payload, "prompt_cache_retention")
	delete(payload, "previous_response_id")
	if _, ok := payload["reasoning"]; ok {
		payload["include"] = mustRawJSON([]string{"reasoning.encrypted_content"})
	} else {
		delete(payload, "include")
	}
	if text, ok := payload["text"]; ok {
		var textPayload map[string]json.RawMessage
		if err := json.Unmarshal(text, &textPayload); err == nil && len(textPayload) == 0 {
			delete(payload, "text")
		}
	}
	metadata := codexClientMetadata(meta)
	if len(metadata) > 0 {
		payload["client_metadata"] = mustRawJSON(metadata)
	}
	return nil
}

func codexClientMetadata(meta openai.ResponsesProtocolMetadata) map[string]string {
	out := cloneStringMap(meta.ClientMetadata)
	if out == nil {
		out = map[string]string{}
	}
	out["x-codex-installation-id"] = codexInstallationID
	if meta.WindowID != "" {
		out[codexWindowIDHeader] = meta.WindowID
	}
	if meta.Subagent != "" {
		out["x-openai-subagent"] = meta.Subagent
	}
	if meta.ParentThreadID != "" {
		out[codexParentHeader] = meta.ParentThreadID
	}
	if meta.TurnMetadata != "" {
		out[codexTurnMetaHeader] = meta.TurnMetadata
	}
	if meta.TraceParent != "" {
		out["ws_request_header_traceparent"] = meta.TraceParent
	}
	if meta.TraceState != "" {
		out["ws_request_header_tracestate"] = meta.TraceState
	}
	return out
}

func codexWrappedWebSocketError(data []byte) (error, bool) {
	var root map[string]json.RawMessage
	if err := json.Unmarshal(data, &root); err != nil {
		return nil, false
	}
	var typ string
	_ = json.Unmarshal(root["type"], &typ)
	if typ != "error" {
		return nil, false
	}
	code, message := codexProviderErrorDetails(data)
	if code == "" && message == "" {
		code = jsonString(root, "code")
		message = jsonString(root, "message")
	}
	err := fmt.Errorf("codex websocket: provider returned an error without code or message")
	if code != "" && message != "" {
		err = fmt.Errorf("codex websocket: %s: %s", code, message)
	} else if code != "" {
		err = fmt.Errorf("codex websocket: %s", code)
	} else if message != "" {
		err = fmt.Errorf("codex websocket: %s", message)
	}
	if code == "websocket_connection_limit_reached" {
		return fmt.Errorf("%w: %v", openai.ErrProviderRetryable, err), true
	}
	return err, true
}

func codexProviderErrorDetails(raw []byte) (string, string) {
	var root map[string]json.RawMessage
	if err := json.Unmarshal(raw, &root); err != nil {
		return "", ""
	}
	code := jsonString(root, "code")
	message := jsonString(root, "message")
	if nestedCode, nestedMessage := codexNestedErrorDetails(root["error"]); nestedCode != "" || nestedMessage != "" {
		if code == "" {
			code = nestedCode
		}
		if message == "" {
			message = nestedMessage
		}
	}
	return code, message
}

func codexNestedErrorDetails(raw json.RawMessage) (string, string) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return "", ""
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return "", strings.TrimSpace(text)
	}
	var nested map[string]json.RawMessage
	if err := json.Unmarshal(raw, &nested); err != nil {
		return "", ""
	}
	code := jsonString(nested, "code")
	if code == "" {
		code = jsonString(nested, "type")
		if code == "error" {
			code = ""
		}
	}
	return code, jsonString(nested, "message")
}

func jsonString(obj map[string]json.RawMessage, key string) string {
	raw := obj[key]
	if len(bytes.TrimSpace(raw)) == 0 {
		return ""
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return ""
	}
	return strings.TrimSpace(value)
}

func cloneStringMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func mustRawJSON(value any) json.RawMessage {
	data, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return data
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
