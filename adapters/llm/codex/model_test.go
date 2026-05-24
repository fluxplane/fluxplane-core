package codex

import (
	"bytes"
	"io"
	"net/http"
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

func TestNewRejectsWebSocketTransportUntilImplemented(t *testing.T) {
	_, err := New(Config{Runtime: openai.ResponsesRuntimeConfig{
		Transport: openai.ResponsesTransportWebSocket,
	}})
	if err == nil || !strings.Contains(err.Error(), "websocket transport is not implemented") {
		t.Fatalf("err = %v, want websocket transport error", err)
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
