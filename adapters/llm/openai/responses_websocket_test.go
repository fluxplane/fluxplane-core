package openai

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fluxplane/fluxplane-core/core/agent"
	coreconversation "github.com/fluxplane/fluxplane-core/core/conversation"
	"github.com/fluxplane/fluxplane-core/core/invocation"
	"github.com/fluxplane/fluxplane-core/core/operation"
	"github.com/fluxplane/fluxplane-core/core/tool"
	"github.com/fluxplane/fluxplane-core/core/usage"
	llmagent "github.com/fluxplane/fluxplane-core/runtime/agent/llmagent"
	conversationruntime "github.com/fluxplane/fluxplane-core/runtime/conversation"
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
		Agent:           agent.Spec{Name: "assistant", Inference: agent.InferenceSpec{Model: "gpt-test"}},
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

func TestResponsesWebSocketOffersCompression(t *testing.T) {
	server := newResponsesWebSocketTestServer(t, []string{
		`{"type":"response.completed","response":{"id":"resp-compression","object":"response","status":"completed","model":"gpt-test","output":[{"type":"message","id":"msg-compression","status":"completed","role":"assistant","content":[{"type":"output_text","text":"ok"}]}]}}`,
	})
	defer server.Close()
	model, err := New(Config{
		Model:   "gpt-test",
		BaseURL: server.URL,
		Runtime: ResponsesRuntimeConfig{
			Transport:         ResponsesTransportWebSocket,
			Continuation:      ResponsesContinuationProvider,
			WebSocketWarmup:   ResponsesWebSocketWarmupOff,
			Cache:             ResponsesCacheOff,
			StreamIdleTimeout: time.Second,
		},
		AllowStoreFalseProviderContinuation: true,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := model.Stream(context.Background(), llmagent.Request{
		ConversationKey: "thread:compression",
		Agent:           agent.Spec{Name: "assistant", Inference: agent.InferenceSpec{Model: "gpt-test"}},
		Goal:            "say ok",
	}, nil); err != nil {
		t.Fatalf("Stream: %v", err)
	}
	handshakes := server.Handshakes()
	if len(handshakes) != 1 {
		t.Fatalf("handshakes len = %d, want 1", len(handshakes))
	}
	if got := strings.ToLower(handshakes[0].Get("Sec-WebSocket-Extensions")); !strings.Contains(got, "permessage-deflate") {
		t.Fatalf("Sec-WebSocket-Extensions = %q, want permessage-deflate offer", got)
	}
}

func TestResponsesWebSocketStreamEmitsUsageIncludingCachedTokens(t *testing.T) {
	server := newResponsesWebSocketTestServer(t, []string{
		`{"type":"response.completed","response":{"id":"resp-1","object":"response","status":"completed","model":"gpt-test","usage":{"input_tokens":10,"input_tokens_details":{"cached_tokens":6},"output_tokens":4,"output_tokens_details":{"reasoning_tokens":2},"total_tokens":14},"output":[{"type":"message","id":"msg-1","status":"completed","role":"assistant","content":[{"type":"output_text","text":"ok"}]}]}}`,
	})
	defer server.Close()
	model, err := New(Config{
		Model:   "gpt-test",
		BaseURL: server.URL,
		Runtime: ResponsesRuntimeConfig{
			Transport:         ResponsesTransportWebSocket,
			Continuation:      ResponsesContinuationProvider,
			WebSocketWarmup:   ResponsesWebSocketWarmupOff,
			Cache:             ResponsesCacheOff,
			StreamIdleTimeout: time.Second,
		},
		AllowStoreFalseProviderContinuation: true,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	resp, err := model.Stream(context.Background(), llmagent.Request{
		ConversationKey: "thread:usage",
		Agent:           agent.Spec{Name: "assistant", Inference: agent.InferenceSpec{Model: "gpt-test"}},
		Goal:            "say ok",
	}, nil)
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	assertUsageQuantity(t, resp.Usage, usage.MetricLLMInputTokens, 4)
	assertUsageQuantity(t, resp.Usage, usage.MetricLLMCachedTokens, 6)
	assertUsageQuantity(t, resp.Usage, usage.MetricLLMOutputTokens, 4)
	assertUsageQuantity(t, resp.Usage, usage.MetricLLMReasoningTokens, 2)
	assertUsageQuantity(t, resp.Usage, usage.MetricLLMTotalTokens, 14)
}

func TestResponsesWebSocketReturnsReadableProviderErrorWhenErrorEventIsEmpty(t *testing.T) {
	server := newResponsesWebSocketTestServer(t, []string{
		`{"type":"error","code":"","message":""}`,
	})
	defer server.Close()
	model, err := New(Config{
		Model:   "gpt-test",
		BaseURL: server.URL,
		Runtime: ResponsesRuntimeConfig{
			Transport:         ResponsesTransportWebSocket,
			Continuation:      ResponsesContinuationProvider,
			WebSocketWarmup:   ResponsesWebSocketWarmupOff,
			Cache:             ResponsesCacheOff,
			StreamIdleTimeout: time.Second,
		},
		AllowStoreFalseProviderContinuation: true,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, err = model.Stream(context.Background(), llmagent.Request{
		ConversationKey: "thread:empty-error",
		Agent:           agent.Spec{Name: "assistant", Inference: agent.InferenceSpec{Model: "gpt-test"}},
		Goal:            "say ok",
	}, nil)
	if err == nil || !strings.Contains(err.Error(), "provider returned an error without code or message") {
		t.Fatalf("Stream error = %v, want readable empty provider error", err)
	}
	if strings.Contains(err.Error(), "openai: :") {
		t.Fatalf("Stream error = %q, want no empty code/message formatting", err.Error())
	}
}

func TestResponsesWebSocketExtractsNestedProviderErrorDetails(t *testing.T) {
	server := newResponsesWebSocketTestServer(t, []string{
		`{"type":"error","error":{"code":"bad_previous_response","message":"previous response is not usable"}}`,
	})
	defer server.Close()
	model, err := New(Config{
		Model:   "gpt-test",
		BaseURL: server.URL,
		Runtime: ResponsesRuntimeConfig{
			Transport:         ResponsesTransportWebSocket,
			Continuation:      ResponsesContinuationProvider,
			WebSocketWarmup:   ResponsesWebSocketWarmupOff,
			Cache:             ResponsesCacheOff,
			StreamIdleTimeout: time.Second,
		},
		AllowStoreFalseProviderContinuation: true,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, err = model.Stream(context.Background(), llmagent.Request{
		ConversationKey: "thread:nested-error",
		Agent:           agent.Spec{Name: "assistant", Inference: agent.InferenceSpec{Model: "gpt-test"}},
		Goal:            "say ok",
	}, nil)
	if err == nil || !strings.Contains(err.Error(), "bad_previous_response: previous response is not usable") {
		t.Fatalf("Stream error = %v, want nested provider error details", err)
	}
}

func TestResponsesWebSocketRetriesWrappedRetryableErrorBeforeProviderEvent(t *testing.T) {
	server := newResponsesWebSocketTestServer(t, []string{
		`{"type":"error","error":{"code":"websocket_connection_limit_reached","message":"too many websocket connections"}}`,
		`{"type":"response.completed","response":{"id":"resp-retry","object":"response","status":"completed","model":"gpt-test","output":[{"type":"message","id":"msg-retry","status":"completed","role":"assistant","content":[{"type":"output_text","text":"ok"}]}]}}`,
	})
	defer server.Close()
	model, err := New(Config{
		Model:   "gpt-test",
		BaseURL: server.URL,
		Runtime: ResponsesRuntimeConfig{
			Transport:         ResponsesTransportWebSocket,
			Continuation:      ResponsesContinuationProvider,
			WebSocketWarmup:   ResponsesWebSocketWarmupOff,
			Cache:             ResponsesCacheOff,
			StreamIdleTimeout: time.Second,
		},
		AllowStoreFalseProviderContinuation: true,
		WebSocketWrappedErrorFunc: func(data []byte) (error, bool) {
			if strings.Contains(string(data), "websocket_connection_limit_reached") {
				return fmt.Errorf("%w: websocket_connection_limit_reached", ErrProviderRetryable), true
			}
			return nil, false
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	resp, err := model.Stream(context.Background(), llmagent.Request{
		ConversationKey: "thread:retryable-wrapped-error",
		Agent:           agent.Spec{Name: "assistant", Inference: agent.InferenceSpec{Model: "gpt-test"}},
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
		t.Fatalf("requests len = %d, want retry after wrapped retryable error: %#v", len(requests), requests)
	}
}

func TestResponsesWebSocketSkipsMalformedTextWhenConfigured(t *testing.T) {
	server := newResponsesWebSocketTestServer(t, []string{
		`not-json
{"type":"response.completed","response":{"id":"resp-skip","object":"response","status":"completed","model":"gpt-test","output":[{"type":"message","id":"msg-skip","status":"completed","role":"assistant","content":[{"type":"output_text","text":"ok"}]}]}}`,
	})
	defer server.Close()
	model, err := New(Config{
		Model:   "gpt-test",
		BaseURL: server.URL,
		Runtime: ResponsesRuntimeConfig{
			Transport:         ResponsesTransportWebSocket,
			Continuation:      ResponsesContinuationProvider,
			WebSocketWarmup:   ResponsesWebSocketWarmupOff,
			Cache:             ResponsesCacheOff,
			StreamIdleTimeout: time.Second,
		},
		AllowStoreFalseProviderContinuation: true,
		WebSocketSkipMalformedEvents:        true,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	resp, err := model.Stream(context.Background(), llmagent.Request{
		ConversationKey: "thread:skip-malformed",
		Agent:           agent.Spec{Name: "assistant", Inference: agent.InferenceSpec{Model: "gpt-test"}},
		Goal:            "say ok",
	}, nil)
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if resp.Message == nil || fmt.Sprint(resp.Message.Content) != "ok" {
		t.Fatalf("message = %#v, want ok", resp.Message)
	}
}

func TestResponsesWebSocketReviewToolResultUsesWireFunctionCallID(t *testing.T) {
	const reviewPrompt = "Use the `assistant-mr-review` skill to review the requested merge request(s).\n\nUser request / focus:"
	const toolArgs = `{\"actions\":[{\"action\":\"activate\",\"skill\":\"assistant-mr-review\"}]}`
	server := newResponsesWebSocketTestServer(t, []string{
		`{"type":"response.output_item.done","output_index":0,"item":{"id":"fc_review","type":"function_call","call_id":"call_review","name":"skill","arguments":"` + toolArgs + `"}}
{"type":"response.completed","response":{"id":"resp-review","object":"response","status":"completed","model":"gpt-test","output":[{"id":"fc_review","type":"function_call","call_id":"call_review","name":"skill","arguments":"` + toolArgs + `"}]}}`,
		`{"type":"response.completed","response":{"id":"resp-reviewed","object":"response","status":"completed","model":"gpt-test","output":[{"type":"message","id":"msg-reviewed","status":"completed","role":"assistant","content":[{"type":"output_text","text":"reviewed"}]}]}}`,
	})
	defer server.Close()
	model, err := New(Config{
		Model:   "gpt-test",
		BaseURL: server.URL,
		Runtime: ResponsesRuntimeConfig{
			Transport:         ResponsesTransportWebSocket,
			Continuation:      ResponsesContinuationProvider,
			WebSocketWarmup:   ResponsesWebSocketWarmupOff,
			Cache:             ResponsesCacheOff,
			Output:            ResponsesOutputStreamItems,
			StreamIdleTimeout: time.Second,
		},
		AllowStoreFalseProviderContinuation: true,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	skillTool := tool.Spec{
		Name:        "skill",
		Description: "Activate a skill.",
		Target: invocation.Target{
			Kind:      invocation.TargetOperation,
			Operation: operation.Ref{Name: "skill"},
		},
		Input: operation.Type{Schema: operation.Schema{Data: json.RawMessage(`{"type":"object","additionalProperties":true}`)}},
	}
	first, err := model.Stream(context.Background(), llmagent.Request{
		ConversationKey: "thread:review-regression",
		Agent:           agent.Spec{Name: "assistant", Inference: agent.InferenceSpec{Model: "gpt-test"}},
		Goal:            reviewPrompt,
		Tools:           []tool.Spec{skillTool},
	}, nil)
	if err != nil {
		t.Fatalf("first Stream: %v", err)
	}
	if len(first.Operations) != 1 || first.Operations[0].ProviderCallID != "call_review" {
		t.Fatalf("first operations = %#v, want call_review skill activation", first.Operations)
	}
	toolResult := coreconversation.Item{
		Kind:     coreconversation.ItemToolResult,
		CallID:   first.Operations[0].ProviderCallID,
		Name:     "skill",
		Content:  `activate "assistant-mr-review": activated` + "\n" + `active skills: assistant, assistant-mr-review`,
		Metadata: map[string]string{"provider_call_type": "function_call"},
	}
	transcript := coreconversation.Transcript{
		Provider: first.Transcript.Provider,
		Items: append([]coreconversation.Item{{
			Kind:    coreconversation.ItemInput,
			Role:    "user",
			Content: promptFromRequest(llmagent.Request{Agent: agent.Spec{Name: "assistant"}, Goal: reviewPrompt}),
		}}, append(first.Transcript.Items, toolResult)...),
		NewItems: []coreconversation.Item{toolResult},
	}
	second, err := model.Stream(context.Background(), llmagent.Request{
		ConversationKey: "thread:review-regression",
		Agent:           agent.Spec{Name: "assistant", Inference: agent.InferenceSpec{Model: "gpt-test"}},
		Transcript:      &transcript,
		Tools:           []tool.Spec{skillTool},
	}, nil)
	if err != nil {
		t.Fatalf("second Stream: %v", err)
	}
	if second.Message == nil || fmt.Sprint(second.Message.Content) != "reviewed" {
		t.Fatalf("second message = %#v, want reviewed", second.Message)
	}
	requests := server.Requests()
	if len(requests) != 2 {
		t.Fatalf("requests len = %d, want 2: %#v", len(requests), requests)
	}
	if got := string(requests[1]["previous_response_id"]); got != `"resp-review"` {
		t.Fatalf("second request previous_response_id = %s, want resp-review; first=%s second=%s", got, mustMarshal(t, requests[0]), mustMarshal(t, requests[1]))
	}
	if input := string(requests[1]["input"]); strings.Contains(input, `"type":"function_call"`) || !strings.Contains(input, `"type":"function_call_output"`) || !strings.Contains(input, `"call_id":"call_review"`) {
		t.Fatalf("second request input = %s, want tool-result delta with call_review", input)
	}
}

func TestResponsesWebSocketTranscriptCoversStreamedToolCallWhenCompletedOutputOmitsIt(t *testing.T) {
	const toolArgs = `{\"path\":\"AGENTS.md\"}`
	server := newResponsesWebSocketTestServer(t, []string{
		`{"type":"response.function_call_arguments.done","output_index":0,"item_id":"fc_edit","call_id":"call_edit","name":"file_edit","arguments":"` + toolArgs + `"}
{"type":"response.completed","response":{"id":"resp-edit","object":"response","status":"completed","model":"gpt-test","output":[]}}`,
	})
	defer server.Close()
	model, err := New(Config{
		Model:   "gpt-test",
		BaseURL: server.URL,
		Runtime: ResponsesRuntimeConfig{
			Transport:         ResponsesTransportWebSocket,
			Continuation:      ResponsesContinuationProvider,
			WebSocketWarmup:   ResponsesWebSocketWarmupOff,
			Cache:             ResponsesCacheOff,
			Output:            ResponsesOutputStreamItems,
			StreamIdleTimeout: time.Second,
		},
		AllowStoreFalseProviderContinuation: true,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	fileEditTool := tool.Spec{
		Name:        "file_edit",
		Description: "Edit a file.",
		Target: invocation.Target{
			Kind:      invocation.TargetOperation,
			Operation: operation.Ref{Name: "file_edit"},
		},
		Input: operation.Type{Schema: operation.Schema{Data: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}},"required":["path"],"additionalProperties":false}`)}},
	}
	resp, err := model.Stream(context.Background(), llmagent.Request{
		ConversationKey: "thread:streamed-tool-transcript",
		Agent:           agent.Spec{Name: "assistant", Inference: agent.InferenceSpec{Model: "gpt-test"}},
		Goal:            "edit AGENTS.md",
		Tools:           []tool.Spec{fileEditTool},
	}, nil)
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if len(resp.Operations) != 1 || resp.Operations[0].ProviderCallID != "call_edit" {
		t.Fatalf("operations = %#v, want call_edit file_edit", resp.Operations)
	}
	if !transcriptHasToolCall(resp.Transcript.Items, "call_edit") {
		t.Fatalf("transcript items = %#v, want assistant tool call call_edit", resp.Transcript.Items)
	}
	follow := append([]coreconversation.Item(nil), resp.Transcript.Items...)
	follow = append(follow, coreconversation.Item{
		Provider: resp.Transcript.Provider,
		Kind:     coreconversation.ItemToolResult,
		CallID:   "call_edit",
		Name:     "file_edit",
		Content:  "ok",
		Metadata: map[string]string{"provider_call_type": "function_call"},
	})
	if err := conversationruntime.ValidateContinuity(follow, conversationruntime.ValidateOptions{Provider: resp.Transcript.Provider}); err != nil {
		t.Fatalf("follow-up transcript continuity: %v", err)
	}
}

func TestResponsesWebSocketReplaysStickyHandshakeHeaderOnReconnect(t *testing.T) {
	server := newResponsesWebSocketTestServer(t, []string{
		`{"type":"response.completed","response":{"id":"resp-1","object":"response","status":"completed","model":"gpt-test","output":[{"type":"message","id":"msg-1","status":"completed","role":"assistant","content":[{"type":"output_text","text":"one"}]}]}}`,
		`{"type":"response.completed","response":{"id":"resp-2","object":"response","status":"completed","model":"gpt-test","output":[{"type":"message","id":"msg-2","status":"completed","role":"assistant","content":[{"type":"output_text","text":"two"}]}]}}`,
	}, withResponsesWebSocketResponseHeader("x-codex-turn-state", "turn-state-1"))
	defer server.Close()
	model, err := New(Config{
		Model:   "gpt-test",
		BaseURL: server.URL,
		Runtime: ResponsesRuntimeConfig{
			Transport:         ResponsesTransportWebSocket,
			Continuation:      ResponsesContinuationProvider,
			WebSocketWarmup:   ResponsesWebSocketWarmupOff,
			Cache:             ResponsesCacheOff,
			StreamIdleTimeout: time.Second,
		},
		AllowStoreFalseProviderContinuation: true,
		WebSocketStickyHeader:               "x-codex-turn-state",
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	req := llmagent.Request{
		ConversationKey: "thread:sticky",
		Agent:           agent.Spec{Name: "assistant", Inference: agent.InferenceSpec{Model: "gpt-test"}},
		Goal:            "say one",
	}
	if _, err := model.Stream(context.Background(), req, nil); err != nil {
		t.Fatalf("first Stream: %v", err)
	}
	sessionKey := strings.Join([]string{req.ConversationKey, responsesWebSocketProviderKey(model.ProviderIdentity(req))}, "\x00")
	session := model.webSocketSessions[sessionKey]
	if session == nil {
		t.Fatalf("missing cached websocket session")
	}
	session.resetRequestState()
	req.Goal = "say two"
	if _, err := model.Stream(context.Background(), req, nil); err != nil {
		t.Fatalf("second Stream: %v", err)
	}
	handshakes := server.Handshakes()
	if len(handshakes) != 2 {
		t.Fatalf("handshakes len = %d, want 2", len(handshakes))
	}
	if got := handshakes[0].Get("x-codex-turn-state"); got != "" {
		t.Fatalf("first handshake turn state = %q, want empty", got)
	}
	if got := handshakes[1].Get("x-codex-turn-state"); got != "turn-state-1" {
		t.Fatalf("second handshake turn state = %q, want turn-state-1", got)
	}
}

func TestResponsesWebSocketSendsResponseProcessed(t *testing.T) {
	server := newResponsesWebSocketTestServer(t, []string{
		`{"type":"response.completed","response":{"id":"resp-processed","object":"response","status":"completed","model":"gpt-test","output":[{"type":"message","id":"msg-1","status":"completed","role":"assistant","content":[{"type":"output_text","text":"ok"}]}]}}`,
	})
	defer server.Close()
	model, err := New(Config{
		Model:   "gpt-test",
		BaseURL: server.URL,
		Runtime: ResponsesRuntimeConfig{
			Transport:         ResponsesTransportWebSocket,
			Continuation:      ResponsesContinuationProvider,
			WebSocketWarmup:   ResponsesWebSocketWarmupOff,
			Cache:             ResponsesCacheOff,
			StreamIdleTimeout: time.Second,
		},
		AllowStoreFalseProviderContinuation: true,
		WebSocketResponseProcessed:          true,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := model.Stream(context.Background(), llmagent.Request{
		ConversationKey: "thread:processed",
		Agent:           agent.Spec{Name: "assistant", Inference: agent.InferenceSpec{Model: "gpt-test"}},
		Goal:            "say ok",
	}, nil); err != nil {
		t.Fatalf("Stream: %v", err)
	}
	requests := server.WaitRequests(t, 2)
	if got := string(requests[1]["type"]); got != `"response.processed"` {
		t.Fatalf("processed type = %s, want response.processed; request=%s", got, mustMarshal(t, requests[1]))
	}
	if got := string(requests[1]["response_id"]); got != `"resp-processed"` {
		t.Fatalf("processed response_id = %s, want resp-processed", got)
	}
}

func TestResponsesWebSocketAnswersPingWhileIdle(t *testing.T) {
	sendPing := make(chan struct{})
	pong := make(chan struct{})
	var closeSendPing sync.Once
	var closePong sync.Once
	defer closeSendPing.Do(func() { close(sendPing) })
	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade: %v", err)
			return
		}
		defer func() { _ = conn.Close() }()
		conn.SetPongHandler(func(payload string) error {
			if payload == "idle-check" {
				closePong.Do(func() { close(pong) })
			}
			return nil
		})
		if _, _, err := conn.ReadMessage(); err != nil {
			return
		}
		if err := conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"response.completed","response":{"id":"resp-idle","object":"response","status":"completed","model":"gpt-test","output":[{"type":"message","id":"msg-idle","status":"completed","role":"assistant","content":[{"type":"output_text","text":"ok"}]}]}}`)); err != nil {
			t.Errorf("write response: %v", err)
			return
		}
		<-sendPing
		readDone := make(chan struct{})
		go func() {
			defer close(readDone)
			_ = conn.SetReadDeadline(time.Now().Add(time.Second))
			for {
				if _, _, err := conn.ReadMessage(); err != nil {
					return
				}
			}
		}()
		if err := conn.WriteControl(websocket.PingMessage, []byte("idle-check"), time.Now().Add(time.Second)); err != nil {
			t.Errorf("write ping: %v", err)
			return
		}
		select {
		case <-pong:
		case <-readDone:
			t.Errorf("websocket reader ended before pong")
		case <-time.After(time.Second):
			t.Errorf("timed out waiting for idle pong")
		}
	}))
	defer server.Close()
	model, err := New(Config{
		Model:   "gpt-test",
		BaseURL: server.URL,
		Runtime: ResponsesRuntimeConfig{
			Transport:         ResponsesTransportWebSocket,
			Continuation:      ResponsesContinuationProvider,
			WebSocketWarmup:   ResponsesWebSocketWarmupOff,
			Cache:             ResponsesCacheOff,
			StreamIdleTimeout: time.Second,
		},
		AllowStoreFalseProviderContinuation: true,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := model.Stream(context.Background(), llmagent.Request{
		ConversationKey: "thread:idle-ping",
		Agent:           agent.Spec{Name: "assistant", Inference: agent.InferenceSpec{Model: "gpt-test"}},
		Goal:            "say ok",
	}, nil); err != nil {
		t.Fatalf("Stream: %v", err)
	}
	closeSendPing.Do(func() { close(sendPing) })
	select {
	case <-pong:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for idle pong")
	}
}

func TestResponsesWebSocketRetriesAfterStaleCachedConnection(t *testing.T) {
	const reviewPrompt = "Use the `assistant-mr-review` skill to review the requested merge request(s).\n\nUser request / focus:"
	const toolArgs = `{\"actions\":[{\"action\":\"activate\",\"skill\":\"assistant-mr-review\"}]}`
	server := newResponsesWebSocketTestServer(t, []string{
		`{"type":"response.output_item.done","output_index":0,"item":{"id":"fc_review","type":"function_call","call_id":"call_review","name":"skill","arguments":"` + toolArgs + `"}}
{"type":"response.completed","response":{"id":"resp-review","object":"response","status":"completed","model":"gpt-test","output":[{"id":"fc_review","type":"function_call","call_id":"call_review","name":"skill","arguments":"` + toolArgs + `"}]}}`,
		`{"type":"response.completed","response":{"id":"resp-reviewed","object":"response","status":"completed","model":"gpt-test","output":[{"type":"message","id":"msg-reviewed","status":"completed","role":"assistant","content":[{"type":"output_text","text":"reviewed"}]}]}}`,
	}, withResponsesWebSocketCloseAfterResponses(0), withResponsesWebSocketResponseHeader("x-codex-turn-state", "turn-state-1"))
	defer server.Close()
	model, err := New(Config{
		Model:   "gpt-test",
		BaseURL: server.URL,
		Runtime: ResponsesRuntimeConfig{
			Transport:         ResponsesTransportWebSocket,
			Continuation:      ResponsesContinuationProvider,
			WebSocketWarmup:   ResponsesWebSocketWarmupOff,
			Cache:             ResponsesCacheOff,
			Output:            ResponsesOutputStreamItems,
			StreamIdleTimeout: time.Second,
		},
		AllowStoreFalseProviderContinuation: true,
		WebSocketStickyHeader:               "x-codex-turn-state",
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	skillTool := tool.Spec{
		Name:        "skill",
		Description: "Activate a skill.",
		Target: invocation.Target{
			Kind:      invocation.TargetOperation,
			Operation: operation.Ref{Name: "skill"},
		},
		Input: operation.Type{Schema: operation.Schema{Data: json.RawMessage(`{"type":"object","additionalProperties":true}`)}},
	}
	first, err := model.Stream(context.Background(), llmagent.Request{
		ConversationKey: "thread:stale-websocket",
		Agent:           agent.Spec{Name: "assistant", Inference: agent.InferenceSpec{Model: "gpt-test"}},
		Goal:            reviewPrompt,
		Tools:           []tool.Spec{skillTool},
	}, nil)
	if err != nil {
		t.Fatalf("first Stream: %v", err)
	}
	if len(first.Operations) != 1 || first.Operations[0].ProviderCallID != "call_review" {
		t.Fatalf("first operations = %#v, want call_review skill activation", first.Operations)
	}
	toolResult := coreconversation.Item{
		Kind:     coreconversation.ItemToolResult,
		CallID:   first.Operations[0].ProviderCallID,
		Name:     "skill",
		Content:  `activate "assistant-mr-review": activated` + "\n" + `active skills: assistant, assistant-mr-review`,
		Metadata: map[string]string{"provider_call_type": "function_call"},
	}
	transcript := coreconversation.Transcript{
		Provider: first.Transcript.Provider,
		Items: append([]coreconversation.Item{{
			Kind:    coreconversation.ItemInput,
			Role:    "user",
			Content: promptFromRequest(llmagent.Request{Agent: agent.Spec{Name: "assistant"}, Goal: reviewPrompt}),
		}}, append(first.Transcript.Items, toolResult)...),
		NewItems: []coreconversation.Item{toolResult},
	}
	second, err := model.Stream(context.Background(), llmagent.Request{
		ConversationKey: "thread:stale-websocket",
		Agent:           agent.Spec{Name: "assistant", Inference: agent.InferenceSpec{Model: "gpt-test"}},
		Transcript:      &transcript,
		Tools:           []tool.Spec{skillTool},
	}, nil)
	if err != nil {
		t.Fatalf("second Stream: %v", err)
	}
	if second.Message == nil || fmt.Sprint(second.Message.Content) != "reviewed" {
		t.Fatalf("second message = %#v, want reviewed", second.Message)
	}
	requests := server.Requests()
	if len(requests) != 2 {
		t.Fatalf("requests len = %d, want 2 after reconnect: %#v", len(requests), requests)
	}
	if _, ok := requests[1]["previous_response_id"]; ok {
		t.Fatalf("second request = %s, want full replay without previous_response_id after stale reconnect", mustMarshal(t, requests[1]))
	}
	if input := string(requests[1]["input"]); !strings.Contains(input, `"type":"function_call"`) || !strings.Contains(input, `"type":"function_call_output"`) || !strings.Contains(input, `"call_id":"call_review"`) {
		t.Fatalf("second request input = %s, want full replay with call and tool result", input)
	}
	handshakes := server.Handshakes()
	if len(handshakes) != 2 {
		t.Fatalf("handshakes len = %d, want reconnect", len(handshakes))
	}
	if got := handshakes[1].Get("x-codex-turn-state"); got != "turn-state-1" {
		t.Fatalf("second handshake turn state = %q, want turn-state-1", got)
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

func TestResponsesWebSocketRequestPayloadSendsDeltaAfterToolCallWithWireCallID(t *testing.T) {
	reviewInput := json.RawMessage(`{"type":"message","role":"user","content":"Agent:\ncoder\n\nGoal:\nUse the ` + "`" + `assistant-mr-review` + "`" + ` skill to review the requested merge request(s).\n\nUser request / focus:"}`)
	toolCall := json.RawMessage(`{"id":"fc_review","type":"function_call","call_id":"call_review","name":"skill","arguments":"{\"actions\":[{\"action\":\"activate\",\"skill\":\"assistant-mr-review\"}]}"}`)
	toolResult := json.RawMessage(`{"type":"function_call_output","call_id":"call_review","output":"activate \"assistant-mr-review\": activated\nactive skills: assistant, assistant-mr-review"}`)
	session := &responsesWebSocketSession{
		lastRequest: &responsesLogicalRequest{
			payload: map[string]json.RawMessage{
				"model": json.RawMessage(`"gpt-test"`),
				"input": mustRawArray(t,
					reviewInput,
				),
			},
			input: []json.RawMessage{reviewInput},
		},
		lastResponseID: "resp-review",
		lastOutput:     []json.RawMessage{toolCall},
	}
	current := &responsesLogicalRequest{
		payload: map[string]json.RawMessage{
			"model": json.RawMessage(`"gpt-test"`),
			"input": mustRawArray(t,
				reviewInput,
				toolCall,
				toolResult,
			),
		},
		input: []json.RawMessage{reviewInput, toolCall, toolResult},
	}
	payload, err := session.requestPayload(current)
	if err != nil {
		t.Fatalf("requestPayload: %v", err)
	}
	if got := string(payload["previous_response_id"]); got != `"resp-review"` {
		t.Fatalf("previous_response_id = %s, want resp-review; payload=%s", got, mustMarshal(t, payload))
	}
	if got := string(payload["input"]); got != `[`+string(toolResult)+`]` {
		t.Fatalf("input = %s, want tool-result delta", got)
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

func TestResponsesWebSocketRequestPayloadSendsFullInputAfterCompactionRewrite(t *testing.T) {
	session := &responsesWebSocketSession{
		lastRequest: &responsesLogicalRequest{
			payload: map[string]json.RawMessage{
				"model": json.RawMessage(`"gpt-test"`),
				"input": json.RawMessage(`[
					{"type":"message","role":"user","content":"older detail"}
				]`),
			},
			input: []json.RawMessage{json.RawMessage(`{"type":"message","role":"user","content":"older detail"}`)},
		},
		lastResponseID: "resp-before-compact",
		lastOutput: []json.RawMessage{
			json.RawMessage(`{"type":"message","id":"msg-before-compact","role":"assistant","content":[{"type":"output_text","text":"older answer"}]}`),
		},
	}
	current := &responsesLogicalRequest{
		payload: map[string]json.RawMessage{
			"model": json.RawMessage(`"gpt-test"`),
			"input": json.RawMessage(`[
				{"type":"message","role":"system","content":"compacted summary"},
				{"type":"message","role":"user","content":"continue"}
			]`),
		},
		input: []json.RawMessage{
			json.RawMessage(`{"type":"message","role":"system","content":"compacted summary"}`),
			json.RawMessage(`{"type":"message","role":"user","content":"continue"}`),
		},
	}
	payload, err := session.requestPayload(current)
	if err != nil {
		t.Fatalf("requestPayload: %v", err)
	}
	if _, ok := payload["previous_response_id"]; ok {
		t.Fatalf("payload = %s, want full replay after compaction rewrite", mustMarshal(t, payload))
	}
	if got := string(payload["input"]); got != string(current.payload["input"]) {
		t.Fatalf("input = %s, want compacted full input %s", got, current.payload["input"])
	}
}

func TestResponsesWebSocketRequestPayloadSendsFullInputWhenPrefixMismatch(t *testing.T) {
	session := &responsesWebSocketSession{
		lastRequest: &responsesLogicalRequest{
			payload: map[string]json.RawMessage{
				"model": json.RawMessage(`"gpt-test"`),
				"input": json.RawMessage(`[{"content":"one"}]`),
			},
			input: []json.RawMessage{json.RawMessage(`{"content":"one"}`)},
		},
		lastResponseID: "resp-1",
		lastOutput:     []json.RawMessage{json.RawMessage(`{"content":"two"}`)},
	}
	current := &responsesLogicalRequest{
		payload: map[string]json.RawMessage{
			"model": json.RawMessage(`"gpt-test"`),
			"input": json.RawMessage(`[{"content":"different"}]`),
		},
		input: []json.RawMessage{json.RawMessage(`{"content":"different"}`)},
	}
	payload, err := session.requestPayload(current)
	if err != nil {
		t.Fatalf("requestPayload: %v", err)
	}
	if _, ok := payload["previous_response_id"]; ok {
		t.Fatalf("payload = %s, want full replay without previous_response_id", mustMarshal(t, payload))
	}
	if got := string(payload["input"]); got != string(current.payload["input"]) {
		t.Fatalf("input = %s, want full input %s", got, current.payload["input"])
	}
}

func TestResponsesWebSocketRequestPayloadAllowsReorderedJSONObjects(t *testing.T) {
	lastInput := json.RawMessage(`{"type":"message","role":"user","content":"one"}`)
	lastOutput := json.RawMessage(`{"type":"message","id":"msg-1","role":"assistant","content":[{"type":"output_text","text":"two"}]}`)
	session := &responsesWebSocketSession{
		lastRequest: &responsesLogicalRequest{
			payload: map[string]json.RawMessage{
				"model":           json.RawMessage(`"gpt-test"`),
				"input":           mustRawArray(t, lastInput),
				"client_metadata": json.RawMessage(`{"x-codex-window-id":"thread:one:0","x-codex-installation-id":"fluxplane"}`),
				"tools":           json.RawMessage(`[{"type":"function","name":"dir_list","parameters":{"type":"object","properties":{"path":{"type":"string"}}}}]`),
			},
			input: []json.RawMessage{lastInput},
		},
		lastResponseID: "resp-1",
		lastOutput:     []json.RawMessage{lastOutput},
	}
	current := &responsesLogicalRequest{
		payload: map[string]json.RawMessage{
			"model":           json.RawMessage(`"gpt-test"`),
			"input":           mustRawArray(t, lastInput, json.RawMessage(`{"id":"msg-1","content":[{"text":"two","type":"output_text"}],"role":"assistant","type":"message"}`), json.RawMessage(`{"type":"function_call_output","call_id":"call-1","output":"ok"}`)),
			"client_metadata": json.RawMessage(`{"x-codex-installation-id":"fluxplane","x-codex-window-id":"thread:one:0"}`),
			"tools":           json.RawMessage(`[{"name":"dir_list","parameters":{"properties":{"path":{"type":"string"}},"type":"object"},"type":"function"}]`),
		},
		input: []json.RawMessage{
			lastInput,
			json.RawMessage(`{"id":"msg-1","content":[{"text":"two","type":"output_text"}],"role":"assistant","type":"message"}`),
			json.RawMessage(`{"type":"function_call_output","call_id":"call-1","output":"ok"}`),
		},
	}
	payload, err := session.requestPayload(current)
	if err != nil {
		t.Fatalf("requestPayload strict reordered JSON: %v", err)
	}
	if got := string(payload["previous_response_id"]); got != `"resp-1"` {
		t.Fatalf("previous_response_id = %s, want resp-1; payload=%s", got, mustMarshal(t, payload))
	}
	if input := string(payload["input"]); !strings.Contains(input, `"function_call_output"`) || strings.Contains(input, `"output_text"`) {
		t.Fatalf("input delta = %s, want only tool-result suffix", input)
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
		Agent:           agent.Spec{Name: "assistant", Inference: agent.InferenceSpec{Model: "gpt-test"}},
		Goal:            "hello",
	}, nil)
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if resp.Message == nil || fmt.Sprint(resp.Message.Content) != "fallback" {
		t.Fatalf("message = %#v, want fallback", resp.Message)
	}
}

func TestResponsesWebSocketSessionFallbackDisablesLaterAttempts(t *testing.T) {
	completed := `data: {"type":"response.completed","response":{"id":"resp-sse","object":"response","status":"completed","output":[{"id":"msg-1","type":"message","status":"completed","role":"assistant","content":[{"type":"output_text","text":"fallback"}]}]}}` + "\n\n"
	var websocketAttempts int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Upgrade") == "websocket" {
			websocketAttempts++
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
		WebSocketSessionFallback:            true,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	req := llmagent.Request{
		ConversationKey: "thread:fallback-disable",
		Agent:           agent.Spec{Name: "assistant", Inference: agent.InferenceSpec{Model: "gpt-test"}},
		Goal:            "hello",
	}
	if _, err := model.Stream(context.Background(), req, nil); err != nil {
		t.Fatalf("first Stream: %v", err)
	}
	if _, err := model.Stream(context.Background(), req, nil); err != nil {
		t.Fatalf("second Stream: %v", err)
	}
	if websocketAttempts != 1 {
		t.Fatalf("websocket attempts = %d, want 1 after session fallback", websocketAttempts)
	}
}

type responsesWebSocketTestServer struct {
	*httptest.Server
	mu              sync.Mutex
	requests        []map[string]json.RawMessage
	handshakes      []http.Header
	responses       []string
	nextResponse    int
	responseHeaders http.Header
	closeAfter      map[int]bool
}

type responsesWebSocketTestServerOption func(*responsesWebSocketTestServer)

func withResponsesWebSocketResponseHeader(key, value string) responsesWebSocketTestServerOption {
	return func(s *responsesWebSocketTestServer) {
		if s.responseHeaders == nil {
			s.responseHeaders = make(http.Header)
		}
		s.responseHeaders.Set(key, value)
	}
}

func withResponsesWebSocketCloseAfterResponses(indices ...int) responsesWebSocketTestServerOption {
	return func(s *responsesWebSocketTestServer) {
		if s.closeAfter == nil {
			s.closeAfter = map[int]bool{}
		}
		for _, index := range indices {
			s.closeAfter[index] = true
		}
	}
}

func newResponsesWebSocketTestServer(t *testing.T, responses []string, opts ...responsesWebSocketTestServerOption) *responsesWebSocketTestServer {
	t.Helper()
	s := &responsesWebSocketTestServer{responses: append([]string(nil), responses...)}
	for _, opt := range opts {
		opt(s)
	}
	upgrader := websocket.Upgrader{}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.handshakes = append(s.handshakes, cloneHeader(r.Header))
		responseHeaders := cloneHeader(s.responseHeaders)
		s.mu.Unlock()
		conn, err := upgrader.Upgrade(w, r, responseHeaders)
		if err != nil {
			t.Errorf("upgrade: %v", err)
			return
		}
		defer func() { _ = conn.Close() }()
		for {
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
			responseIndex := s.nextResponse
			var response string
			if s.nextResponse < len(s.responses) {
				response = s.responses[s.nextResponse]
			}
			s.nextResponse++
			closeAfter := s.closeAfter[responseIndex]
			s.mu.Unlock()
			if response == "" {
				if closeAfter {
					return
				}
				continue
			}
			for _, event := range strings.Split(response, "\n") {
				event = strings.TrimSpace(event)
				if event == "" {
					continue
				}
				if err := conn.WriteMessage(websocket.TextMessage, []byte(event)); err != nil {
					t.Errorf("write response: %v", err)
					return
				}
			}
			if closeAfter {
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

func (s *responsesWebSocketTestServer) WaitRequests(t *testing.T, count int) []map[string]json.RawMessage {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		requests := s.Requests()
		if len(requests) >= count {
			return requests
		}
		time.Sleep(10 * time.Millisecond)
	}
	requests := s.Requests()
	t.Fatalf("requests len = %d, want at least %d: %#v", len(requests), count, requests)
	return nil
}

func (s *responsesWebSocketTestServer) Handshakes() []http.Header {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]http.Header, len(s.handshakes))
	for i, header := range s.handshakes {
		out[i] = cloneHeader(header)
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

func transcriptHasToolCall(items []coreconversation.Item, callID string) bool {
	for _, item := range items {
		for _, ref := range item.ToolCallRefs() {
			if ref.CallID == callID {
				return true
			}
		}
	}
	return false
}

func mustRawArray(t *testing.T, values ...json.RawMessage) json.RawMessage {
	t.Helper()
	data, err := json.Marshal(values)
	if err != nil {
		t.Fatalf("marshal raw array: %v", err)
	}
	return data
}
