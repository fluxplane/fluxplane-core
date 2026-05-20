package openrouter

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/openai/openai-go/v3/option"
)

func TestResponsesReliabilityMiddlewareSetsDefaultServiceTierForGPT55(t *testing.T) {
	var captured string
	resp, err := callReliabilityMiddleware(t, `{"model":"openai/gpt-5.5","stream":true}`, func(req *http.Request) (*http.Response, error) {
		body, err := io.ReadAll(req.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}
		captured = string(body)
		return sseHTTPResponse(`data: {"type":"response.completed","response":{"id":"resp_1","object":"response","created_at":1,"model":"openai/gpt-5.5","status":"completed","output":[],"error":null}}` + "\n\ndata: [DONE]\n\n"), nil
	})
	if err != nil {
		t.Fatalf("middleware: %v", err)
	}
	_ = resp.Body.Close()
	if !strings.Contains(captured, `"service_tier":"default"`) {
		t.Fatalf("request body = %s, want service_tier default", captured)
	}
}

func TestResponsesReliabilityMiddlewareRetriesPreOutputResponseFailed(t *testing.T) {
	var attempts int
	resp, err := callReliabilityMiddleware(t, `{"model":"openai/gpt-5.5","stream":true}`, func(req *http.Request) (*http.Response, error) {
		attempts++
		if attempts == 1 {
			return sseHTTPResponse(responseFailedSSE("server_error", "internal stream ended unexpectedly")), nil
		}
		return sseHTTPResponse(responseCompletedSSE("ok")), nil
	})
	if err != nil {
		t.Fatalf("middleware: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if attempts != 2 {
		t.Fatalf("attempts = %d, want 2", attempts)
	}
	if strings.Contains(string(data), "response.failed") || !strings.Contains(string(data), "response.completed") {
		t.Fatalf("body = %s, want retried completed stream", data)
	}
}

func TestResponsesReliabilityMiddlewareRetriesTransientHTTPFailure(t *testing.T) {
	var attempts int
	resp, err := callReliabilityMiddleware(t, `{"model":"openai/gpt-5.5","stream":true}`, func(req *http.Request) (*http.Response, error) {
		attempts++
		if attempts == 1 {
			return &http.Response{
				StatusCode: http.StatusBadGateway,
				Status:     "502 Bad Gateway",
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body:       io.NopCloser(strings.NewReader(`{"error":{"message":"upstream unavailable"}}`)),
			}, nil
		}
		return sseHTTPResponse(responseCompletedSSE("ok")), nil
	})
	if err != nil {
		t.Fatalf("middleware: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if attempts != 2 {
		t.Fatalf("attempts = %d, want 2", attempts)
	}
	if strings.Contains(string(data), "502 Bad Gateway") || !strings.Contains(string(data), "response.completed") {
		t.Fatalf("body = %s, want retried completed stream", data)
	}
}

func TestResponsesReliabilityMiddlewareFallsBackToNonStreaming(t *testing.T) {
	var attempts int
	var fallbackBody string
	resp, err := callReliabilityMiddleware(t, `{"model":"openai/gpt-5.5","stream":true}`, func(req *http.Request) (*http.Response, error) {
		attempts++
		body, err := io.ReadAll(req.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}
		if attempts <= 2 {
			return sseHTTPResponse(responseFailedSSE("server_error", "internal stream ended unexpectedly")), nil
		}
		fallbackBody = string(body)
		return jsonHTTPResponse(`{"id":"resp_fallback","object":"response","created_at":1,"model":"openai/gpt-5.5","status":"completed","output":[],"error":null}`), nil
	})
	if err != nil {
		t.Fatalf("middleware: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if attempts != 3 {
		t.Fatalf("attempts = %d, want 3", attempts)
	}
	if !strings.Contains(fallbackBody, `"stream":false`) {
		t.Fatalf("fallback body = %s, want stream false", fallbackBody)
	}
	if !strings.Contains(string(data), `"type":"response.completed"`) || !strings.Contains(string(data), "resp_fallback") {
		t.Fatalf("body = %s, want synthetic completed SSE", data)
	}
}

func TestResponsesReliabilityMiddlewarePreservesFallbackFailureDetails(t *testing.T) {
	var attempts int
	_, err := callReliabilityMiddleware(t, `{"model":"openai/gpt-5.5","stream":true}`, func(req *http.Request) (*http.Response, error) {
		attempts++
		if attempts <= 2 {
			return sseHTTPResponse(responseFailedSSE("server_error", "internal stream ended unexpectedly")), nil
		}
		return jsonHTTPResponse(`{"id":"resp_failed","object":"response","created_at":1,"model":"openai/gpt-5.5","status":"failed","output":[],"error":{"code":"rate_limit_exceeded","message":"upstream rate limit"}}`), nil
	})
	if err == nil {
		t.Fatal("middleware succeeded, want provider failure")
	}
	if !strings.Contains(err.Error(), "rate_limit_exceeded: upstream rate limit") {
		t.Fatalf("error = %v, want provider failure details", err)
	}
}

func TestResponsesReliabilityMiddlewareDoesNotRetryAfterOutput(t *testing.T) {
	var attempts int
	resp, err := callReliabilityMiddleware(t, `{"model":"openai/gpt-5.5","stream":true}`, func(req *http.Request) (*http.Response, error) {
		attempts++
		return sseHTTPResponse(
			`data: {"type":"response.output_text.delta","output_index":0,"content_index":0,"delta":"hello"}` + "\n\n" +
				responseFailedSSE("server_error", "internal stream ended unexpectedly"),
		), nil
	})
	if err != nil {
		t.Fatalf("middleware: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if attempts != 1 {
		t.Fatalf("attempts = %d, want 1", attempts)
	}
	if !strings.Contains(string(data), "response.output_text.delta") {
		t.Fatalf("body = %s, want original output stream", data)
	}
}

func callReliabilityMiddleware(t *testing.T, body string, next option.MiddlewareNext) (*http.Response, error) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "https://openrouter.test/responses", strings.NewReader(body)).WithContext(context.Background())
	req.Header.Set("Content-Type", "application/json")
	return responsesReliabilityMiddleware()(req, next)
}

func sseHTTPResponse(body string) *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Status:     "200 OK",
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func jsonHTTPResponse(body string) *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Status:     "200 OK",
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func responseFailedSSE(code, message string) string {
	return `data: {"type":"response.failed","response":{"id":"resp_failed","object":"response","created_at":1,"model":"openai/gpt-5.5","status":"failed","output":[],"error":{"code":"` + code + `","message":"` + message + `"}}}` + "\n\ndata: [DONE]\n\n"
}

func responseCompletedSSE(text string) string {
	return `data: {"type":"response.completed","response":{"id":"resp_ok","object":"response","created_at":1,"model":"openai/gpt-5.5","status":"completed","output":[{"id":"msg_1","type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"` + text + `"}]}],"error":null}}` + "\n\ndata: [DONE]\n\n"
}
