package anthropicmessages

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/fluxplane/agentruntime/core/agent"
	coreconversation "github.com/fluxplane/agentruntime/core/conversation"
	"github.com/fluxplane/agentruntime/core/invocation"
	"github.com/fluxplane/agentruntime/core/operation"
	coretool "github.com/fluxplane/agentruntime/core/tool"
	"github.com/fluxplane/agentruntime/core/usage"
	llmagent "github.com/fluxplane/agentruntime/runtime/agent/llmagent"
)

func TestTranscriptSystemContextItemMapsToSystemBlocks(t *testing.T) {
	provider := coreconversation.ProviderIdentity{Provider: "anthropic", API: "anthropic.messages"}
	item := coreconversation.Item{
		Provider: provider,
		Kind:     coreconversation.ItemInput,
		Role:     "system",
		Content:  "<system-context>rules</system-context>",
	}
	messages, system, recorded, err := messagesFromTranscript(provider, []coreconversation.Item{item})
	if err != nil {
		t.Fatalf("messagesFromTranscript: %v", err)
	}
	if len(messages) != 0 {
		t.Fatalf("messages = %#v, want no user/assistant messages", messages)
	}
	if len(system) != 1 || system[0].Text != "<system-context>rules</system-context>" {
		t.Fatalf("system = %#v, want context system block", system)
	}
	if len(recorded) != 1 || recorded[0].Role != "system" {
		t.Fatalf("recorded = %#v, want system item", recorded)
	}
}

func TestStreamTextAndUsage(t *testing.T) {
	var gotReq messageRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		if r.Header.Get("x-api-key") != "test-key" {
			t.Fatalf("x-api-key = %q", r.Header.Get("x-api-key"))
		}
		if err := json.NewDecoder(r.Body).Decode(&gotReq); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(strings.Join([]string{
			`event: message_start`,
			`data: {"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","model":"claude-test","content":[],"usage":{"input_tokens":7}}}`,
			``,
			`event: content_block_start`,
			`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
			``,
			`event: content_block_delta`,
			`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"ok"}}`,
			``,
			`event: content_block_stop`,
			`data: {"type":"content_block_stop","index":0}`,
			``,
			`event: message_delta`,
			`data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":2,"cache_read_input_tokens":3}}`,
			``,
			`event: message_stop`,
			`data: {"type":"message_stop"}`,
			``,
		}, "\n")))
	}))
	defer server.Close()
	model, err := New(Config{Model: "claude-test", APIKey: "test-key", BaseURL: server.URL})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	var streamed []llmagent.StreamEvent
	resp, err := model.Stream(context.Background(), llmagent.Request{Goal: "say ok"}, func(event llmagent.StreamEvent) {
		streamed = append(streamed, event)
	})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if gotReq.Model != "claude-test" || !gotReq.Stream || gotReq.MaxTokens != DefaultMaxOutputTokens {
		t.Fatalf("request = %#v", gotReq)
	}
	if resp.Message == nil || resp.Message.Content != "ok" {
		t.Fatalf("message = %#v", resp.Message)
	}
	if len(streamed) != 1 || streamed[0].Text != "ok" {
		t.Fatalf("streamed = %#v", streamed)
	}
	assertUsage(t, resp.Usage, usage.MetricLLMInputTokens, 7)
	assertUsage(t, resp.Usage, usage.MetricLLMCachedTokens, 3)
	assertUsage(t, resp.Usage, usage.MetricLLMOutputTokens, 2)
}

func TestStreamToolUseReturnsOperationAndTranscript(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(strings.Join([]string{
			`event: message_start`,
			`data: {"type":"message_start","message":{"id":"msg_tool","type":"message","role":"assistant","model":"claude-test","content":[]}}`,
			``,
			`event: content_block_start`,
			`data: {"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"toolu_1","name":"lookup","input":{}}}`,
			``,
			`event: content_block_delta`,
			`data: {"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"q\":\"x\"}"}}`,
			``,
			`event: content_block_stop`,
			`data: {"type":"content_block_stop","index":0}`,
			``,
			`event: message_stop`,
			`data: {"type":"message_stop"}`,
			``,
		}, "\n")))
	}))
	defer server.Close()
	model, err := New(Config{Model: "claude-test", APIKey: "test-key", BaseURL: server.URL})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	resp, err := model.Stream(context.Background(), llmagent.Request{
		Goal: "lookup",
		Tools: []coretool.Spec{{
			Name: "lookup",
			Target: invocation.Target{Kind: invocation.TargetOperation, Operation: operation.Ref{
				Name: "lookup_data",
			}},
			Input: operation.Type{Schema: operation.Schema{Data: json.RawMessage(`{"type":"object"}`)}},
		}},
	}, nil)
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if len(resp.Operations) != 1 || resp.Operations[0].ProviderCallID != "toolu_1" {
		t.Fatalf("operations = %#v", resp.Operations)
	}
	if got := resp.Operations[0].Input.(map[string]any)["q"]; got != "x" {
		t.Fatalf("operation input = %#v", resp.Operations[0].Input)
	}
	if len(resp.Transcript.Items) != 1 || !strings.Contains(string(resp.Transcript.Items[0].Native), "tool_use") {
		t.Fatalf("transcript = %#v", resp.Transcript)
	}
}

func TestMessageRequestAppliesThinkingOverride(t *testing.T) {
	model, err := New(Config{
		Model:           "claude-test",
		APIKey:          "test-key",
		BaseURL:         "http://example.test",
		Thinking:        "on",
		ReasoningEffort: "high",
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	wire, _, _, err := model.messageRequest(llmagent.Request{Goal: "think"}, false)
	if err != nil {
		t.Fatalf("messageRequest: %v", err)
	}
	if wire.Thinking == nil || wire.Thinking.Type != "enabled" || wire.Effort != "high" {
		t.Fatalf("request = %#v, want thinking enabled with high effort", wire)
	}
}

func TestMessageRequestOffOverrideSuppressesAgentThinking(t *testing.T) {
	model, err := New(Config{
		Model:    "claude-test",
		APIKey:   "test-key",
		BaseURL:  "http://example.test",
		Thinking: "off",
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	wire, _, _, err := model.messageRequest(llmagent.Request{
		Goal: "think",
		Agent: agent.Spec{Inference: agent.InferenceSpec{
			Thinking:        "enabled",
			ReasoningEffort: "high",
		}},
	}, false)
	if err != nil {
		t.Fatalf("messageRequest: %v", err)
	}
	if wire.Thinking != nil || wire.Effort != "" {
		t.Fatalf("request = %#v, want no thinking", wire)
	}
}

func TestCompleteHTTPErrorIncludesBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":{"message":"bad key"}}`, http.StatusBadRequest)
	}))
	defer server.Close()
	model, err := New(Config{Model: "claude-test", APIKey: "test-key", BaseURL: server.URL})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, err = model.Complete(context.Background(), llmagent.Request{Goal: "x"})
	if err == nil || !strings.Contains(err.Error(), "HTTP 400") || !strings.Contains(err.Error(), "bad key") {
		t.Fatalf("error = %v", err)
	}
}

func assertUsage(t *testing.T, records []usage.Recorded, metric usage.MetricName, quantity float64) {
	t.Helper()
	for _, record := range records {
		for _, measurement := range record.Measurements {
			if measurement.Metric == metric && measurement.Quantity == quantity {
				return
			}
		}
	}
	t.Fatalf("usage records = %#v, want %s=%v", records, metric, quantity)
}
