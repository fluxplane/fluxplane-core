package codex

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fluxplane/fluxplane-core/adapters/llm/openai"
	"github.com/fluxplane/fluxplane-core/core/agent"
	llmagent "github.com/fluxplane/fluxplane-core/runtime/agent/llmagent"
	"github.com/gorilla/websocket"
	openaigo "github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/responses"
)

func TestCodexMiddlewareIncludesHTTPErrorBody(t *testing.T) {
	req := httptestRequest(t, []byte(`{"store":true,"prompt_cache_retention":"24h"}`))
	req = req.WithContext(openai.ContextWithResponsesProtocolMetadata(req.Context(), openai.ResponsesProtocolMetadata{
		ConversationKey: "thread:test",
		WindowID:        "thread:test:0",
	}))
	middleware := codexMiddleware(staticAuth("token"))
	_, err := middleware(req, func(req *http.Request) (*http.Response, error) {
		body, readErr := io.ReadAll(req.Body)
		if readErr != nil {
			t.Fatalf("ReadAll request body: %v", readErr)
		}
		if !strings.Contains(string(body), "prompt_cache_retention") {
			t.Fatalf("request body = %s, want middleware to leave protocol body untouched", body)
		}
		if got := req.Header.Get("thread-id"); got != "thread:test" {
			t.Fatalf("thread-id = %q, want context metadata", got)
		}
		if got := req.Header.Get("x-codex-window-id"); got != "thread:test:0" {
			t.Fatalf("x-codex-window-id = %q, want context metadata", got)
		}
		return &http.Response{
			StatusCode: http.StatusBadRequest,
			Status:     "400 Bad Request",
			Body:       io.NopCloser(strings.NewReader(`{"error":"bad request detail"}`)),
		}, nil
	})
	if err == nil || !strings.Contains(err.Error(), "bad request detail") {
		t.Fatalf("err = %v, want response body", err)
	}
}

func TestNewRejectsProviderContinuationOnHTTPTransport(t *testing.T) {
	_, err := New(Config{Runtime: openai.ResponsesRuntimeConfig{
		Transport:    openai.ResponsesTransportSSE,
		Continuation: openai.ResponsesContinuationProvider,
	}})
	if err == nil || !strings.Contains(err.Error(), "provider continuation requires websocket transport") {
		t.Fatalf("err = %v, want provider continuation transport error", err)
	}
}

func TestNewAcceptsWebSocketTransport(t *testing.T) {
	model, err := New(Config{
		AuthPath: testAuthPath(t),
		Runtime: openai.ResponsesRuntimeConfig{
			Transport: openai.ResponsesTransportWebSocket,
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if model == nil {
		t.Fatalf("model is nil")
	}
}

func TestCodexModelStreamFailsHardWithoutConversationKey(t *testing.T) {
	model, err := New(Config{
		Model:    "gpt-test",
		AuthPath: testAuthPath(t),
		BaseURL:  "http://127.0.0.1:1",
		Runtime: openai.ResponsesRuntimeConfig{
			Transport:       openai.ResponsesTransportWebSocket,
			WebSocketWarmup: openai.ResponsesWebSocketWarmupOff,
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, err = model.Stream(context.Background(), llmagent.Request{
		Agent: agent.Spec{Name: "coder"},
		Goal:  "say ok",
	}, nil)
	if err == nil || !strings.Contains(err.Error(), "conversation key is required") {
		t.Fatalf("Stream error = %v, want conversation key failure", err)
	}
}

func TestValidateCodexRequestRequiresConversationKey(t *testing.T) {
	err := validateCodexRequest(llmagent.Request{Agent: agent.Spec{Name: "coder"}})
	if err == nil || !strings.Contains(err.Error(), "conversation key is required") {
		t.Fatalf("validateCodexRequest error = %v, want hard conversation key failure", err)
	}
}

func TestCodexProtocolMetadataDerivesWindowID(t *testing.T) {
	meta, err := codexProtocolMetadata(llmagent.Request{
		ConversationKey: "thread:one",
		Protocol:        llmagent.ProtocolMetadata{WindowGeneration: 2, Subagent: "review"},
	})
	if err != nil {
		t.Fatalf("codexProtocolMetadata: %v", err)
	}
	if meta.ConversationKey != "thread:one" || meta.WindowID != "thread:one:2" || meta.Subagent != "review" {
		t.Fatalf("metadata = %#v, want thread/window/subagent", meta)
	}
}

func TestCodexResponseParamsDropsOpenAIOnlyFields(t *testing.T) {
	params := responses.ResponseNewParams{
		Store:                openaigo.Bool(true),
		MaxOutputTokens:      openaigo.Int(4096),
		Temperature:          openaigo.Float(0.7),
		PromptCacheRetention: responses.ResponseNewParamsPromptCacheRetention24h,
		PreviousResponseID:   openaigo.String("resp_old"),
	}
	if err := codexResponseParams(&params, llmagent.Request{ConversationKey: "thread:one"}); err != nil {
		t.Fatalf("codexResponseParams: %v", err)
	}
	raw, err := json.Marshal(params)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	got := string(raw)
	for _, forbidden := range []string{"max_output_tokens", "prompt_cache_retention", "previous_response_id", "temperature"} {
		if strings.Contains(got, forbidden) {
			t.Fatalf("params = %s, still contains %s", got, forbidden)
		}
	}
	if !strings.Contains(got, `"store":false`) {
		t.Fatalf("params = %s, want store false", got)
	}
}

func TestCodexWebSocketPayloadUsesStrictWireFields(t *testing.T) {
	payload := map[string]json.RawMessage{
		"model":                  json.RawMessage(`"gpt-test"`),
		"input":                  json.RawMessage(`[]`),
		"store":                  json.RawMessage("true"),
		"max_output_tokens":      json.RawMessage("1024"),
		"prompt_cache_key":       json.RawMessage(`"thread:one"`),
		"prompt_cache_retention": json.RawMessage(`"24h"`),
		"previous_response_id":   json.RawMessage(`"resp_old"`),
		"reasoning":              json.RawMessage(`{"summary":"auto"}`),
	}
	err := codexWebSocketPayload(llmagent.Request{ConversationKey: "thread:one"}, payload)
	if err != nil {
		t.Fatalf("codexWebSocketPayload: %v", err)
	}
	if _, ok := payload["max_output_tokens"]; ok {
		t.Fatalf("payload = %#v, want max_output_tokens removed", payload)
	}
	if _, ok := payload["previous_response_id"]; ok {
		t.Fatalf("payload = %#v, want previous_response_id removed before websocket request shaping", payload)
	}
	if string(payload["store"]) != "false" {
		t.Fatalf("store = %s, want false", payload["store"])
	}
	for key, want := range map[string]string{
		"stream":      "true",
		"tool_choice": `"auto"`,
		"include":     `["reasoning.encrypted_content"]`,
	} {
		if got := string(payload[key]); got != want {
			t.Fatalf("%s = %s, want %s", key, got, want)
		}
	}
	if got := string(payload["prompt_cache_key"]); got != `"thread:one"` {
		t.Fatalf("prompt_cache_key = %s, want stable cache identity retained", got)
	}
	var metadata map[string]string
	if err := json.Unmarshal(payload["client_metadata"], &metadata); err != nil {
		t.Fatalf("client_metadata: %v", err)
	}
	if metadata["x-codex-installation-id"] != codexInstallationID || metadata["x-codex-window-id"] != "thread:one:0" {
		t.Fatalf("client_metadata = %#v, want installation and window id", metadata)
	}
}

func TestCodexWrappedWebSocketErrorClassifiesRetryableLimit(t *testing.T) {
	err, ok := codexWrappedWebSocketError([]byte(`{"type":"error","error":{"code":"websocket_connection_limit_reached","message":"too many websocket connections"}}`))
	if !ok {
		t.Fatalf("codexWrappedWebSocketError ok = false, want true")
	}
	if !errors.Is(err, openai.ErrProviderRetryable) {
		t.Fatalf("err = %v, want retryable provider error", err)
	}
	if !strings.Contains(err.Error(), "websocket_connection_limit_reached") {
		t.Fatalf("err = %v, want provider code", err)
	}
}

func TestCodexWebSocketHeaderFuncIncludesIdentityHeaders(t *testing.T) {
	headers := make(http.Header)
	if err := codexWebSocketHeaderFunc(staticAuth("token"))(context.Background(), headers); err != nil {
		t.Fatalf("codexWebSocketHeaderFunc: %v", err)
	}
	for key, want := range map[string]string{
		"Authorization":           "Bearer token",
		"originator":              codexOriginator,
		"version":                 codexVersion,
		"OpenAI-Beta":             codexBetaHeader,
		"User-Agent":              codexUserAgent,
		"x-codex-installation-id": codexInstallationID,
	} {
		if got := headers.Get(key); got != want {
			t.Fatalf("%s = %q, want %q", key, got, want)
		}
	}
}

func TestCodexModelStreamSendsCodexProtocolHeadersAndPayload(t *testing.T) {
	handshakeCh := make(chan http.Header, 1)
	requestCh := make(chan map[string]json.RawMessage, 1)
	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handshakeCh <- cloneHeader(r.Header)
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade: %v", err)
			return
		}
		defer func() { _ = conn.Close() }()
		_, data, err := conn.ReadMessage()
		if err != nil {
			t.Errorf("ReadMessage: %v", err)
			return
		}
		var payload map[string]json.RawMessage
		if err := json.Unmarshal(data, &payload); err != nil {
			t.Errorf("decode payload: %v", err)
			return
		}
		requestCh <- payload
		err = conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"response.completed","response":{"id":"resp-codex","object":"response","status":"completed","model":"gpt-test","output":[{"type":"message","id":"msg-codex","status":"completed","role":"assistant","content":[{"type":"output_text","text":"ok"}]}]}}`))
		if err != nil {
			t.Errorf("write response: %v", err)
		}
	}))
	defer server.Close()
	model, err := New(Config{
		Model:    "gpt-test",
		AuthPath: testAuthPath(t),
		BaseURL:  server.URL,
		Runtime: openai.ResponsesRuntimeConfig{
			Transport:         openai.ResponsesTransportWebSocket,
			WebSocketWarmup:   openai.ResponsesWebSocketWarmupOff,
			Continuation:      openai.ResponsesContinuationProvider,
			Cache:             openai.ResponsesCacheMax,
			StreamIdleTimeout: time.Second,
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	resp, err := model.Stream(context.Background(), llmagent.Request{
		ConversationKey: "thread:codex",
		Agent:           agent.Spec{Name: "coder"},
		Goal:            "say ok",
		Protocol: llmagent.ProtocolMetadata{
			Subagent:    "review",
			TraceParent: "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-00",
			ClientMetadata: map[string]string{
				"custom": "value",
			},
		},
	}, nil)
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if resp.Message == nil || fmt.Sprint(resp.Message.Content) != "ok" {
		t.Fatalf("message = %#v, want ok", resp.Message)
	}
	handshake := receiveHeader(t, handshakeCh)
	for key, want := range map[string]string{
		"Authorization":           "Bearer test-token",
		"originator":              codexOriginator,
		"version":                 codexVersion,
		"OpenAI-Beta":             codexBetaHeader,
		"User-Agent":              codexUserAgent,
		"x-codex-installation-id": codexInstallationID,
		"x-client-request-id":     "thread:codex",
		"session-id":              "thread:codex",
		"thread-id":               "thread:codex",
		"x-codex-window-id":       "thread:codex:0",
		"x-openai-subagent":       "review",
	} {
		if got := handshake.Get(key); got != want {
			t.Fatalf("handshake %s = %q, want %q", key, got, want)
		}
	}
	payload := receivePayload(t, requestCh)
	for _, forbidden := range []string{"max_output_tokens", "prompt_cache_retention", "previous_response_id", "temperature"} {
		if _, ok := payload[forbidden]; ok {
			t.Fatalf("payload = %s, did not want %s", mustMarshal(t, payload), forbidden)
		}
	}
	for key, want := range map[string]string{
		"type":             `"response.create"`,
		"store":            "false",
		"stream":           "true",
		"tool_choice":      `"auto"`,
		"prompt_cache_key": `"thread:codex"`,
		"include":          `["reasoning.encrypted_content"]`,
	} {
		if got := string(payload[key]); got != want {
			t.Fatalf("payload %s = %s, want %s; payload=%s", key, got, want, mustMarshal(t, payload))
		}
	}
	var metadata map[string]string
	if err := json.Unmarshal(payload["client_metadata"], &metadata); err != nil {
		t.Fatalf("client_metadata: %v", err)
	}
	for key, want := range map[string]string{
		"x-codex-installation-id":       codexInstallationID,
		"x-codex-window-id":             "thread:codex:0",
		"x-openai-subagent":             "review",
		"ws_request_header_traceparent": "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-00",
		"custom":                        "value",
	} {
		if got := metadata[key]; got != want {
			t.Fatalf("client_metadata[%s] = %q, want %q; metadata=%#v", key, got, want, metadata)
		}
	}
}

func TestCodexModelSSESendsCodexProtocolHeadersAndPayload(t *testing.T) {
	headerCh := make(chan http.Header, 1)
	bodyCh := make(chan map[string]json.RawMessage, 1)
	completed := `data: {"type":"response.completed","response":{"id":"resp-sse","object":"response","status":"completed","model":"gpt-test","output":[{"type":"message","id":"msg-sse","status":"completed","role":"assistant","content":[{"type":"output_text","text":"ok"}]}]}}` + "\n\n"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		headerCh <- cloneHeader(r.Header)
		var payload map[string]json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Errorf("decode request body: %v", err)
			return
		}
		bodyCh <- payload
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, completed)
	}))
	defer server.Close()
	model, err := New(Config{
		Model:    "gpt-test",
		AuthPath: testAuthPath(t),
		BaseURL:  server.URL,
		Runtime: openai.ResponsesRuntimeConfig{
			Transport:         openai.ResponsesTransportSSE,
			Continuation:      openai.ResponsesContinuationReplay,
			Cache:             openai.ResponsesCacheMax,
			StreamIdleTimeout: time.Second,
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	resp, err := model.Stream(context.Background(), llmagent.Request{
		ConversationKey: "thread:sse",
		Agent:           agent.Spec{Name: "coder"},
		Goal:            "say ok",
		Protocol:        llmagent.ProtocolMetadata{Subagent: "compact"},
	}, nil)
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if resp.Message == nil || fmt.Sprint(resp.Message.Content) != "ok" {
		t.Fatalf("message = %#v, want ok", resp.Message)
	}
	headers := receiveHeader(t, headerCh)
	for key, want := range map[string]string{
		"Authorization":           "Bearer test-token",
		"originator":              codexOriginator,
		"version":                 codexVersion,
		"OpenAI-Beta":             codexBetaHeader,
		"User-Agent":              codexUserAgent,
		"x-codex-installation-id": codexInstallationID,
		"x-client-request-id":     "thread:sse",
		"session-id":              "thread:sse",
		"thread-id":               "thread:sse",
		"x-codex-window-id":       "thread:sse:0",
		"x-openai-subagent":       "compact",
	} {
		if got := headers.Get(key); got != want {
			t.Fatalf("header %s = %q, want %q", key, got, want)
		}
	}
	payload := receivePayload(t, bodyCh)
	for _, forbidden := range []string{"max_output_tokens", "prompt_cache_retention", "previous_response_id", "temperature"} {
		if _, ok := payload[forbidden]; ok {
			t.Fatalf("payload = %s, did not want %s", mustMarshal(t, payload), forbidden)
		}
	}
	for key, want := range map[string]string{
		"store":            "false",
		"tool_choice":      `"auto"`,
		"prompt_cache_key": `"thread:sse"`,
	} {
		if got := string(payload[key]); got != want {
			t.Fatalf("payload %s = %s, want %s; payload=%s", key, got, want, mustMarshal(t, payload))
		}
	}
	var metadata map[string]string
	if err := json.Unmarshal(payload["client_metadata"], &metadata); err != nil {
		t.Fatalf("client_metadata: %v", err)
	}
	if metadata["x-codex-installation-id"] != codexInstallationID || metadata["x-codex-window-id"] != "thread:sse:0" || metadata["x-openai-subagent"] != "compact" {
		t.Fatalf("client_metadata = %#v, want Codex identity", metadata)
	}
}

func staticAuth(token string) *auth {
	a := &auth{}
	a.file.Tokens.AccessToken = token
	return a
}

func httptestRequest(t *testing.T, body []byte) *http.Request {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, "https://chatgpt.com/backend-api/codex/responses", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Body = io.NopCloser(bytes.NewReader(body))
	return req
}

func receiveHeader(t *testing.T, ch <-chan http.Header) http.Header {
	t.Helper()
	select {
	case header := <-ch:
		return header
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for handshake")
		return nil
	}
}

func receivePayload(t *testing.T, ch <-chan map[string]json.RawMessage) map[string]json.RawMessage {
	t.Helper()
	select {
	case payload := <-ch:
		return payload
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for payload")
		return nil
	}
}

func cloneHeader(in http.Header) http.Header {
	out := make(http.Header, len(in))
	for key, values := range in {
		out[key] = append([]string(nil), values...)
	}
	return out
}

func mustMarshal(t *testing.T, value any) string {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(data)
}

func testAuthPath(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "auth.json")
	data := []byte(`{"auth_mode":"chatgpt","tokens":{"access_token":"test-token"}}`)
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatalf("write auth: %v", err)
	}
	return path
}
