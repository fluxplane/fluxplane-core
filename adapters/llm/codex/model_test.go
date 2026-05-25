package codex

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fluxplane/fluxplane-core/adapters/llm/openai"
)

func TestMutateBodyDropsOpenAIOnlyFields(t *testing.T) {
	raw := []byte(`{
		"model":"gpt-5.5",
		"store":true,
		"max_output_tokens":4096,
		"prompt_cache_retention":"24h",
		"previous_response_id":"resp_1",
		"text":{},
		"input":"hello"
	}`)
	mutated, ok, err := mutateBody(raw)
	if err != nil {
		t.Fatalf("mutateBody: %v", err)
	}
	if !ok {
		t.Fatalf("mutateBody ok = false, want true")
	}
	got := string(mutated)
	for _, forbidden := range []string{"max_output_tokens", "prompt_cache_retention", "previous_response_id", `"text":{}`} {
		if strings.Contains(got, forbidden) {
			t.Fatalf("mutated body = %s, still contains %s", got, forbidden)
		}
	}
	if !strings.Contains(got, `"store":false`) {
		t.Fatalf("mutated body = %s, want store false", got)
	}
}

func TestCodexMiddlewareIncludesHTTPErrorBody(t *testing.T) {
	req := httptestRequest(t, []byte(`{"store":true,"prompt_cache_retention":"24h"}`))
	middleware := codexMiddleware(staticAuth("token"))
	_, err := middleware(req, func(req *http.Request) (*http.Response, error) {
		body, readErr := io.ReadAll(req.Body)
		if readErr != nil {
			t.Fatalf("ReadAll request body: %v", readErr)
		}
		if strings.Contains(string(body), "prompt_cache_retention") {
			t.Fatalf("request body was not mutated: %s", body)
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

func TestMutateRawPayloadAllowsWebSocketPreviousResponseIDAfterMutation(t *testing.T) {
	payload := map[string]json.RawMessage{
		"store":                json.RawMessage("true"),
		"max_output_tokens":    json.RawMessage("1024"),
		"previous_response_id": json.RawMessage(`"resp_old"`),
	}
	mutateRawPayload(payload)
	if _, ok := payload["max_output_tokens"]; ok {
		t.Fatalf("payload = %#v, want max_output_tokens removed", payload)
	}
	if _, ok := payload["previous_response_id"]; ok {
		t.Fatalf("payload = %#v, want previous_response_id removed before websocket request shaping", payload)
	}
	if string(payload["store"]) != "false" {
		t.Fatalf("store = %s, want false", payload["store"])
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

func testAuthPath(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "auth.json")
	data := []byte(`{"auth_mode":"chatgpt","tokens":{"access_token":"test-token"}}`)
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatalf("write auth: %v", err)
	}
	return path
}
