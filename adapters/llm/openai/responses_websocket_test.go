package openai

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/fluxplane/fluxplane-core/core/agent"
	llmagent "github.com/fluxplane/fluxplane-core/runtime/agent/llmagent"
	"github.com/gorilla/websocket"
)

func TestResponsesWebSocketWarmupThenRealRequestUsesPreviousResponseID(t *testing.T) {
	server := newResponsesWebSocketTestServer(t, []string{
		`{"type":"response.completed","response":{"id":"resp-warm","output":[]}}`,
		`{"type":"response.completed","response":{"id":"resp-1","output":[{"type":"message","id":"msg-1","role":"assistant","content":[{"type":"output_text","text":"ok"}]}]}}`,
	})
	defer server.Close()
	model, err := New(Config{
		Model:   "gpt-test",
		BaseURL: server.URL,
		Runtime: ResponsesRuntimeConfig{
			Transport:         ResponsesTransportWebSocket,
			Continuation:      ResponsesContinuationProvider,
			WebSocketWarmup:   ResponsesWebSocketWarmupOn,
			Cache:             ResponsesCacheOff,
			StreamIdleTimeout: time.Second,
		},
		AllowStoreFalseProviderContinuation: true,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	resp, err := model.Stream(context.Background(), llmagent.Request{
		ConversationKey: "thread:test",
		Agent:           agent.Spec{Name: "coder", Inference: agent.InferenceSpec{Model: "gpt-test"}},
		Goal:            "say ok",
	}, nil)
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if resp.Message == nil || fmt.Sprint(resp.Message.Content) != "ok" {
		t.Fatalf("message = %#v, want ok", resp.Message)
	}
	requests := server.Requests()
	if len(requests) != 2 {
		t.Fatalf("requests len = %d, want 2: %#v", len(requests), requests)
	}
	if string(requests[0]["generate"]) != "false" {
		t.Fatalf("warmup request = %s, want generate false", mustMarshal(t, requests[0]))
	}
	if got := string(requests[1]["previous_response_id"]); got != `"resp-warm"` {
		t.Fatalf("real request previous_response_id = %s, want resp-warm; request=%s", got, mustMarshal(t, requests[1]))
	}
	if got := string(requests[1]["input"]); got != "[]" {
		t.Fatalf("real request input = %s, want empty delta", got)
	}
}

func TestResponsesWebSocketRequestPayloadSendsDeltaWhenPrefixMatches(t *testing.T) {
	session := &responsesWebSocketSession{
		lastRequest: &responsesLogicalRequest{
			payload: map[string]json.RawMessage{
				"model": json.RawMessage(`"gpt-test"`),
				"input": json.RawMessage(`[{"type":"message","role":"user","content":"one"}]`),
			},
			input: []json.RawMessage{json.RawMessage(`{"type":"message","role":"user","content":"one"}`)},
		},
		lastResponseID: "resp-1",
		lastOutput:     []json.RawMessage{json.RawMessage(`{"type":"message","id":"msg-1","role":"assistant","content":[{"type":"output_text","text":"two"}]}`)},
	}
	current := &responsesLogicalRequest{
		payload: map[string]json.RawMessage{
			"model": json.RawMessage(`"gpt-test"`),
			"input": json.RawMessage(`[
				{"type":"message","role":"user","content":"one"},
				{"type":"message","id":"msg-1","role":"assistant","content":[{"type":"output_text","text":"two"}]},
				{"type":"message","role":"user","content":"three"}
			]`),
		},
		input: []json.RawMessage{
			json.RawMessage(`{"type":"message","role":"user","content":"one"}`),
			json.RawMessage(`{"type":"message","id":"msg-1","role":"assistant","content":[{"type":"output_text","text":"two"}]}`),
			json.RawMessage(`{"type":"message","role":"user","content":"three"}`),
		},
	}
	payload, err := session.requestPayload(current)
	if err != nil {
		t.Fatalf("requestPayload: %v", err)
	}
	if got := string(payload["previous_response_id"]); got != `"resp-1"` {
		t.Fatalf("previous_response_id = %s, want resp-1", got)
	}
	if got := string(payload["input"]); got != `[{"type":"message","role":"user","content":"three"}]` {
		t.Fatalf("input delta = %s", got)
	}
}

func TestResponsesWebSocketRequestPayloadSendsFullInputWhenRequestShapeChanges(t *testing.T) {
	session := &responsesWebSocketSession{
		lastRequest: &responsesLogicalRequest{
			payload: map[string]json.RawMessage{
				"model":        json.RawMessage(`"gpt-test"`),
				"instructions": json.RawMessage(`"a"`),
				"input":        json.RawMessage(`[{"content":"one"}]`),
			},
			input: []json.RawMessage{json.RawMessage(`{"content":"one"}`)},
		},
		lastResponseID: "resp-1",
	}
	current := &responsesLogicalRequest{
		payload: map[string]json.RawMessage{
			"model":        json.RawMessage(`"gpt-test"`),
			"instructions": json.RawMessage(`"b"`),
			"input":        json.RawMessage(`[{"content":"one"},{"content":"two"}]`),
		},
		input: []json.RawMessage{json.RawMessage(`{"content":"one"}`), json.RawMessage(`{"content":"two"}`)},
	}
	payload, err := session.requestPayload(current)
	if err != nil {
		t.Fatalf("requestPayload: %v", err)
	}
	if _, ok := payload["previous_response_id"]; ok {
		t.Fatalf("payload = %s, want no previous_response_id", mustMarshal(t, payload))
	}
	if got := string(payload["input"]); got != `[{"content":"one"},{"content":"two"}]` {
		t.Fatalf("input = %s, want full input", got)
	}
}

func TestResponsesWebSocketUpgradeRequiredFallsBackToSSE(t *testing.T) {
	completed := `data: {"type":"response.completed","response":{"id":"resp-sse","object":"response","status":"completed","output":[{"id":"msg-1","type":"message","status":"completed","role":"assistant","content":[{"type":"output_text","text":"fallback"}]}]}}` + "\n\n"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Upgrade") == "websocket" {
			http.Error(w, "upgrade required", http.StatusUpgradeRequired)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, completed)
	}))
	defer server.Close()
	model, err := New(Config{
		Model:   "gpt-test",
		BaseURL: server.URL,
		Runtime: ResponsesRuntimeConfig{
			Transport:         ResponsesTransportWebSocket,
			Continuation:      ResponsesContinuationProvider,
			Cache:             ResponsesCacheOff,
			StreamIdleTimeout: time.Second,
		},
		AllowStoreFalseProviderContinuation: true,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	resp, err := model.Stream(context.Background(), llmagent.Request{
		ConversationKey: "thread:fallback",
		Agent:           agent.Spec{Name: "coder", Inference: agent.InferenceSpec{Model: "gpt-test"}},
		Goal:            "hello",
	}, nil)
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if resp.Message == nil || fmt.Sprint(resp.Message.Content) != "fallback" {
		t.Fatalf("message = %#v, want fallback", resp.Message)
	}
}

type responsesWebSocketTestServer struct {
	*httptest.Server
	mu        sync.Mutex
	requests  []map[string]json.RawMessage
	responses []string
}

func newResponsesWebSocketTestServer(t *testing.T, responses []string) *responsesWebSocketTestServer {
	t.Helper()
	s := &responsesWebSocketTestServer{responses: append([]string(nil), responses...)}
	upgrader := websocket.Upgrader{}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade: %v", err)
			return
		}
		defer func() { _ = conn.Close() }()
		for i := 0; ; i++ {
			_, data, err := conn.ReadMessage()
			if err != nil {
				return
			}
			var payload map[string]json.RawMessage
			if err := json.Unmarshal(data, &payload); err != nil {
				t.Errorf("decode request: %v", err)
				return
			}
			s.mu.Lock()
			s.requests = append(s.requests, payload)
			var response string
			if i < len(s.responses) {
				response = s.responses[i]
			}
			s.mu.Unlock()
			if response == "" {
				continue
			}
			if err := conn.WriteMessage(websocket.TextMessage, []byte(response)); err != nil {
				t.Errorf("write response: %v", err)
				return
			}
		}
	}))
	return s
}

func (s *responsesWebSocketTestServer) Requests() []map[string]json.RawMessage {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]map[string]json.RawMessage, len(s.requests))
	copy(out, s.requests)
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
