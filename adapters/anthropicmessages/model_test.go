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
		if r.URL.RawQuery != "" {
			t.Fatalf("query = %q, want empty for plain Anthropic Messages", r.URL.RawQuery)
		}
		if r.Header.Get("x-api-key") != "test-key" {
			t.Fatalf("x-api-key = %q", r.Header.Get("x-api-key"))
		}
		if r.Header.Get("Anthropic-Beta") != "" || r.Header.Get("Authorization") != "" {
			t.Fatalf("unexpected optional headers: %v", r.Header)
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

func TestMessageRequestDoesNotSynthesizeMissingToolResult(t *testing.T) {
	model, err := New(Config{Model: "claude-test", APIKey: "test-key", BaseURL: "http://example.test"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	assistant := nativeTranscriptItem(t, message{Role: "assistant", Content: []contentBlock{{
		Type:  "tool_use",
		ID:    "toolu_missing",
		Name:  "file_create",
		Input: json.RawMessage(`{"path":"docs/multi-agent.md"}`),
	}}})

	wire, _, _, err := model.messageRequest(llmagent.Request{
		Transcript: &coreconversation.Transcript{Items: []coreconversation.Item{
			assistant,
			{Provider: model.providerIdentity("claude-test"), Kind: coreconversation.ItemInput, Role: "user", Content: "did you write it?"},
		}},
	}, false)
	if err != nil {
		t.Fatalf("messageRequest: %v", err)
	}
	if len(wire.Messages) != 2 {
		t.Fatalf("messages = %#v, want assistant plus user without adapter repair", wire.Messages)
	}
	if wire.Messages[1].Role != "user" || len(wire.Messages[1].Content) != 1 || wire.Messages[1].Content[0].Type == "tool_result" {
		t.Fatalf("second message = %#v, want original user input", wire.Messages[1])
	}
}

func TestMessageRequestGroupsConsecutiveToolResults(t *testing.T) {
	model, err := New(Config{Model: "claude-test", APIKey: "test-key", BaseURL: "http://example.test"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	provider := model.providerIdentity("claude-test")
	assistant := nativeTranscriptItem(t, message{Role: "assistant", Content: []contentBlock{
		{Type: "tool_use", ID: "toolu_1", Name: "read", Input: json.RawMessage(`{}`)},
		{Type: "tool_use", ID: "toolu_2", Name: "stat", Input: json.RawMessage(`{}`)},
	}})

	wire, _, _, err := model.messageRequest(llmagent.Request{
		Transcript: &coreconversation.Transcript{Items: []coreconversation.Item{
			assistant,
			{Provider: provider, Kind: coreconversation.ItemToolResult, CallID: "toolu_1", Name: "read", Content: "one"},
			{Provider: provider, Kind: coreconversation.ItemToolResult, CallID: "toolu_2", Name: "stat", Content: "two"},
		}},
	}, false)
	if err != nil {
		t.Fatalf("messageRequest: %v", err)
	}
	if len(wire.Messages) != 2 {
		t.Fatalf("messages = %#v, want assistant plus grouped tool results", wire.Messages)
	}
	results := wire.Messages[1]
	if results.Role != "user" || len(results.Content) != 2 {
		t.Fatalf("results message = %#v, want two tool_result blocks", results)
	}
	if results.Content[0].ToolUseID != "toolu_1" || results.Content[1].ToolUseID != "toolu_2" {
		t.Fatalf("results message = %#v, want matching tool_use ids", results)
	}
}

func nativeTranscriptItem(t *testing.T, msg message) coreconversation.Item {
	t.Helper()
	raw, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	return coreconversation.Item{
		Provider: coreconversation.ProviderIdentity{Provider: "anthropic", API: "anthropic.messages", Model: "claude-test"},
		Kind:     coreconversation.ItemOutput,
		Role:     "assistant",
		Native:   raw,
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
