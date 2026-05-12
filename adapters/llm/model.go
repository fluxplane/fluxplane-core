package llm

import (
	"context"

	"github.com/fluxplane/agentruntime/core/agent"
	llmagent "github.com/fluxplane/agentruntime/runtime/agent/llmagent"
)

// Response is the adapter-normalized final provider response before it is
// converted to the runtime llmagent response shape.
type Response struct {
	Message    string                   `json:"message,omitempty"`
	Operations []agent.OperationRequest `json:"operations,omitempty"`
	Completion *agent.Completion        `json:"completion,omitempty"`
}

// ToRuntime converts the adapter response into the runtime LLM-agent response.
func (r Response) ToRuntime() llmagent.Response {
	out := llmagent.Response{
		Operations: append([]agent.OperationRequest(nil), r.Operations...),
		Completion: r.Completion,
	}
	if r.Message != "" {
		out.Message = &agent.Message{Content: r.Message}
	}
	return out
}

// ScriptedModel is a deterministic fake provider adapter for tests and
// examples. It implements the runtime streaming model port.
type ScriptedModel struct {
	Messages []Message
	Tools    []ToolSpec
	Events   []StreamEvent
	Response Response
	Redactor Redactor
	Err      error
}

// Complete returns the scripted final response.
func (m ScriptedModel) Complete(context.Context, llmagent.Request) (llmagent.Response, error) {
	if m.Err != nil {
		return llmagent.Response{}, m.Err
	}
	return m.Response.ToRuntime(), nil
}

// Stream emits the scripted stream after redaction and returns the scripted
// final response plus any streamed completed tool calls.
func (m ScriptedModel) Stream(_ context.Context, _ llmagent.Request, emit llmagent.StreamFunc) (llmagent.Response, error) {
	if m.Err != nil {
		return llmagent.Response{}, m.Err
	}
	assembler := NewToolCallAssembler(m.Tools)
	var operations []agent.OperationRequest
	for _, evt := range m.Events {
		if runtimeEvent, ok := m.Redactor.ToRuntimeStream(evt); ok && emit != nil {
			emit(runtimeEvent)
		}
		completed, err := assembler.Apply(evt)
		if err != nil {
			return llmagent.Response{}, err
		}
		operations = append(operations, completed...)
	}
	resp := m.Response.ToRuntime()
	resp.Operations = append(resp.Operations, operations...)
	return resp, nil
}
