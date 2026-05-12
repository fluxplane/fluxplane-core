package openaiadapter

import (
	"encoding/json"
	"errors"
	"testing"

	adapterllm "github.com/fluxplane/agentruntime/adapters/llm"
	"github.com/fluxplane/agentruntime/core/agent"
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
	_, tools, err := model.responseParams(llmagent.Request{
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
	_, _, err = model.responseParams(llmagent.Request{Agent: agent.Spec{Name: "coder"}})
	if !errors.Is(err, ErrModelMissing) {
		t.Fatalf("error = %v, want ErrModelMissing", err)
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
	got, err := responseFromOpenAI(resp, nil)
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
	got, err := responseFromOpenAI(resp, nil)
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
	})
	if err != nil {
		t.Fatalf("responseFromOpenAI: %v", err)
	}
	if len(got.Operations) != 2 {
		t.Fatalf("operations = %#v, want two", got.Operations)
	}
	if got.Operations[0].Operation.Name != "inspect" || got.Operations[1].Operation.Name != "test" {
		t.Fatalf("operations = %#v, want inspect then test", got.Operations)
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
