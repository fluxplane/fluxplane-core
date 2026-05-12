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

func mustResponse(t *testing.T, raw string) responses.Response {
	t.Helper()
	var resp responses.Response
	if err := json.Unmarshal([]byte(raw), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	return resp
}
