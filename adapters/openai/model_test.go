package openaiadapter

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	adapterllm "github.com/fluxplane/agentruntime/adapters/llm"
	"github.com/fluxplane/agentruntime/core/agent"
	coreconversation "github.com/fluxplane/agentruntime/core/conversation"
	"github.com/fluxplane/agentruntime/core/invocation"
	"github.com/fluxplane/agentruntime/core/operation"
	"github.com/fluxplane/agentruntime/core/tool"
	"github.com/openai/openai-go/v3/responses"

	llmagent "github.com/fluxplane/agentruntime/runtime/agent/llmagent"
)

func TestResponseParamsUsesRequestModelAndTools(t *testing.T) {
	model, err := New(Config{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, tools, _, err := model.responseParams(llmagent.Request{
		Agent: agent.Spec{Name: "coder", Inference: agent.InferenceSpec{Model: "gpt-test"}},
		Goal:  "Say hello.",
		Tools: []tool.Spec{{
			Name:        "inspect",
			Description: "Inspect a path.",
			Target: invocation.Target{
				Kind:      invocation.TargetOperation,
				Operation: operation.Ref{Name: "inspect"},
			},
			Input: operation.Type{Schema: operation.Schema{Data: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}}}`)}},
		}},
	})
	if err != nil {
		t.Fatalf("responseParams: %v", err)
	}
	if len(tools) != 1 || tools[0].Name != "inspect" {
		t.Fatalf("tools = %#v, want inspect", tools)
	}
}

func TestResponseParamsRejectsMissingModel(t *testing.T) {
	model, err := New(Config{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, _, _, err = model.responseParams(llmagent.Request{Agent: agent.Spec{Name: "coder"}})
	if !errors.Is(err, ErrModelMissing) {
		t.Fatalf("error = %v, want ErrModelMissing", err)
	}
}

func TestResponseParamsUsesTranscriptItemsAndPreviousResponseID(t *testing.T) {
	model, err := New(Config{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	params, _, sent, err := model.responseParams(llmagent.Request{
		Agent: agent.Spec{Name: "coder", Inference: agent.InferenceSpec{Model: "gpt-test"}},
		Transcript: &coreconversation.Transcript{
			Provider: coreconversation.ProviderIdentity{Provider: "openai", API: "openai.responses", Family: "responses", Model: "gpt-test"},
			Mode:     coreconversation.ProjectionNativeContinuation,
			Continuation: &coreconversation.ContinuationHandle{
				Provider:   coreconversation.ProviderIdentity{Provider: "openai", API: "openai.responses", Family: "responses", Model: "gpt-test"},
				Mode:       coreconversation.ContinuationPreviousResponseID,
				Transport:  coreconversation.TransportHTTPSSE,
				ResponseID: "resp_prev",
			},
			Items: []coreconversation.Item{{
				Kind:    coreconversation.ItemToolResult,
				CallID:  "call_1",
				Content: map[string]any{"ok": true},
			}},
		},
	})
	if err != nil {
		t.Fatalf("responseParams: %v", err)
	}
	raw, err := json.Marshal(params)
	if err != nil {
		t.Fatalf("marshal params: %v", err)
	}
	if !strings.Contains(string(raw), `"previous_response_id":"resp_prev"`) || !strings.Contains(string(raw), `"type":"function_call_output"`) {
		t.Fatalf("params json = %s, want previous_response_id and function_call_output", raw)
	}
	if len(sent) != 1 || sent[0].CallID != "call_1" || len(sent[0].Native) == 0 {
		t.Fatalf("sent = %#v, want native tool output", sent)
	}
}

func TestResponseParamsRecordsOnlyTranscriptNewItems(t *testing.T) {
	model, err := New(Config{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	params, _, sent, err := model.responseParams(llmagent.Request{
		Agent: agent.Spec{Name: "coder", Inference: agent.InferenceSpec{Model: "gpt-test"}},
		Transcript: &coreconversation.Transcript{
			Provider: coreconversation.ProviderIdentity{Model: "gpt-test"},
			Mode:     coreconversation.ProjectionFullReplay,
			Items: []coreconversation.Item{
				{Kind: coreconversation.ItemInput, Role: "user", Content: "old"},
				{Kind: coreconversation.ItemInput, Role: "user", Content: "current"},
			},
			NewItems: []coreconversation.Item{
				{Kind: coreconversation.ItemInput, Role: "user", Content: "current"},
			},
		},
	})
	if err != nil {
		t.Fatalf("responseParams: %v", err)
	}
	raw, err := json.Marshal(params)
	if err != nil {
		t.Fatalf("marshal params: %v", err)
	}
	if !strings.Contains(string(raw), `"text":"old"`) || !strings.Contains(string(raw), `"text":"current"`) {
		t.Fatalf("params json = %s, want full replay input", raw)
	}
	if len(sent) != 1 || sent[0].Content != "current" {
		t.Fatalf("sent = %#v, want only current new item", sent)
	}
	if sent[0].Provider.Provider != "openai" || sent[0].Provider.API != "openai.responses" {
		t.Fatalf("sent provider = %#v, want openai responses", sent[0].Provider)
	}
}

func TestResponseFromOpenAIConvertsText(t *testing.T) {
	resp := mustResponse(t, `{
		"id": "resp_1",
		"object": "response",
		"status": "completed",
		"output": [{
			"id": "msg_1",
			"type": "message",
			"status": "completed",
			"role": "assistant",
			"content": [{"type": "output_text", "text": "hello"}]
		}]
	}`)
	got, err := responseFromOpenAI(resp, nil, openAIProviderIdentity("gpt-test"), false)
	if err != nil {
		t.Fatalf("responseFromOpenAI: %v", err)
	}
	if got.Message == nil || got.Message.Content != "hello" {
		t.Fatalf("message = %#v, want hello", got.Message)
	}
}

func TestResponseFromOpenAIConvertsUsage(t *testing.T) {
	resp := mustResponse(t, `{
		"id": "resp_1",
		"object": "response",
		"model": "gpt-test",
		"status": "completed",
		"usage": {
			"input_tokens": 10,
			"input_tokens_details": {"cached_tokens": 3},
			"output_tokens": 5,
			"output_tokens_details": {"reasoning_tokens": 2},
			"total_tokens": 15
		},
		"output": [{
			"id": "msg_1",
			"type": "message",
			"status": "completed",
			"role": "assistant",
			"content": [{"type": "output_text", "text": "hello"}]
		}]
	}`)
	got, err := responseFromOpenAI(resp, nil, openAIProviderIdentity("gpt-test"), false)
	if err != nil {
		t.Fatalf("responseFromOpenAI: %v", err)
	}
	if len(got.Usage) != 1 {
		t.Fatalf("usage len = %d, want 1", len(got.Usage))
	}
	if got.Usage[0].Subject.Provider != "openai" || got.Usage[0].Subject.Name != "gpt-test" {
		t.Fatalf("usage subject = %#v, want openai/gpt-test", got.Usage[0].Subject)
	}
	if len(got.Usage[0].Measurements) != 5 {
		t.Fatalf("usage measurements = %#v, want 5", got.Usage[0].Measurements)
	}
}

func TestResponseFromOpenAIConvertsMultipleFunctionCalls(t *testing.T) {
	resp := mustResponse(t, `{
		"id": "resp_1",
		"object": "response",
		"status": "completed",
		"output": [
			{"type": "function_call", "call_id": "call_1", "name": "inspect", "arguments": "{\"path\":\"README.md\"}"},
			{"type": "function_call", "call_id": "call_2", "name": "test", "arguments": "{\"package\":\"./...\"}"}
		]
	}`)
	got, err := responseFromOpenAI(resp, []adapterllm.ToolSpec{
		{
			Name: "inspect",
			Target: invocation.Target{
				Kind:      invocation.TargetOperation,
				Operation: operation.Ref{Name: "inspect"},
			},
		},
		{
			Name: "test",
			Target: invocation.Target{
				Kind:      invocation.TargetOperation,
				Operation: operation.Ref{Name: "test"},
			},
		},
	}, openAIProviderIdentity("gpt-test"), true)
	if err != nil {
		t.Fatalf("responseFromOpenAI: %v", err)
	}
	if len(got.Operations) != 2 {
		t.Fatalf("operations = %#v, want two", got.Operations)
	}
	if got.Operations[0].Operation.Name != "inspect" || got.Operations[1].Operation.Name != "test" {
		t.Fatalf("operations = %#v, want inspect then test", got.Operations)
	}
	if got.Operations[0].ProviderCallID != "call_1" || got.Operations[1].ProviderCallID != "call_2" {
		t.Fatalf("provider call IDs = %#v, want OpenAI call IDs", got.Operations)
	}
	if got.Transcript.Continuation == nil || got.Transcript.Continuation.ResponseID != "resp_1" {
		t.Fatalf("continuation = %#v, want resp_1", got.Transcript.Continuation)
	}
}

func TestStreamEventsNormalizeThinkingAndToolCalls(t *testing.T) {
	model := &Model{}
	toolNames := map[int]tool.Name{}

	added := mustStreamEvent(t, `{"type":"response.output_item.added","output_index":0,"item":{"type":"function_call","call_id":"call_1","name":"inspect","arguments":""}}`)
	events := model.streamEvents(added, toolNames)
	if len(events) != 1 || events[0].Kind != adapterllm.StreamToolCallStart || events[0].Tool != "inspect" {
		t.Fatalf("added events = %#v, want inspect start", events)
	}

	delta := mustStreamEvent(t, `{"type":"response.function_call_arguments.delta","output_index":0,"item_id":"call_1","delta":"{\"path\""}`)
	events = model.streamEvents(delta, toolNames)
	if len(events) != 1 || events[0].Kind != adapterllm.StreamToolCallDelta || events[0].Tool != "inspect" {
		t.Fatalf("delta events = %#v, want inspect delta", events)
	}

	thinking := mustStreamEvent(t, `{"type":"response.reasoning_summary_text.delta","output_index":1,"item_id":"rs_1","delta":"checking"}`)
	events = model.streamEvents(thinking, toolNames)
	if len(events) != 1 || events[0].Kind != adapterllm.StreamThinkingDelta || events[0].Text != "checking" {
		t.Fatalf("thinking events = %#v, want thinking delta", events)
	}
}

func TestProviderSpecValidates(t *testing.T) {
	if err := ProviderSpec().Validate(); err != nil {
		t.Fatalf("ProviderSpec Validate: %v", err)
	}
}

func mustResponse(t *testing.T, raw string) responses.Response {
	t.Helper()
	var resp responses.Response
	if err := json.Unmarshal([]byte(raw), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	return resp
}

func mustStreamEvent(t *testing.T, raw string) responses.ResponseStreamEventUnion {
	t.Helper()
	var event responses.ResponseStreamEventUnion
	if err := json.Unmarshal([]byte(raw), &event); err != nil {
		t.Fatalf("unmarshal stream event: %v", err)
	}
	return event
}
