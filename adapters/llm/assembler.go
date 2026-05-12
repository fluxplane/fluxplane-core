package llm

import (
	"encoding/json"
	"fmt"
	"sort"

	"github.com/fluxplane/agentruntime/core/agent"
	"github.com/fluxplane/agentruntime/core/invocation"
	"github.com/fluxplane/agentruntime/core/operation"
	"github.com/fluxplane/agentruntime/core/tool"
)

// ToolCallAssembler accumulates provider-normalized streamed tool calls and
// converts completed calls into agent operation requests.
type ToolCallAssembler struct {
	tools map[tool.Name]ToolSpec
	calls map[string]*pendingToolCall
}

type pendingToolCall struct {
	id        string
	index     int
	name      tool.Name
	arguments string
	done      bool
}

// NewToolCallAssembler returns a tool-call assembler for the projected tools.
func NewToolCallAssembler(tools []ToolSpec) ToolCallAssembler {
	return ToolCallAssembler{
		tools: toolByName(tools),
		calls: map[string]*pendingToolCall{},
	}
}

// Apply records one stream event and returns any operation requests completed
// by that event.
func (a *ToolCallAssembler) Apply(evt StreamEvent) ([]agent.OperationRequest, error) {
	if a == nil {
		return nil, fmt.Errorf("llm: tool call assembler is nil")
	}
	if a.calls == nil {
		a.calls = map[string]*pendingToolCall{}
	}
	switch evt.Kind {
	case StreamToolCallStart:
		call := a.call(evt)
		call.name = evt.Tool
		call.index = evt.Index
	case StreamToolCallDelta:
		call := a.call(evt)
		if evt.Tool != "" {
			call.name = evt.Tool
		}
		call.arguments += evt.Arguments
	case StreamToolCallDone:
		call := a.call(evt)
		if evt.Tool != "" {
			call.name = evt.Tool
		}
		call.arguments += evt.Arguments
		call.done = true
		req, err := a.operationRequest(*call)
		if err != nil {
			return nil, err
		}
		delete(a.calls, call.id)
		return []agent.OperationRequest{req}, nil
	default:
		return nil, nil
	}
	return nil, nil
}

// Complete returns operation requests for any calls already marked done.
func (a *ToolCallAssembler) Complete() ([]agent.OperationRequest, error) {
	if a == nil || len(a.calls) == 0 {
		return nil, nil
	}
	ids := make([]string, 0, len(a.calls))
	for id := range a.calls {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	var out []agent.OperationRequest
	for _, id := range ids {
		call := a.calls[id]
		if call == nil || !call.done {
			continue
		}
		req, err := a.operationRequest(*call)
		if err != nil {
			return nil, err
		}
		out = append(out, req)
	}
	return out, nil
}

func (a *ToolCallAssembler) call(evt StreamEvent) *pendingToolCall {
	id := evt.ToolCallID
	if id == "" {
		id = fmt.Sprintf("index:%d", evt.Index)
	}
	if existing := a.calls[id]; existing != nil {
		return existing
	}
	call := &pendingToolCall{id: id, index: evt.Index}
	a.calls[id] = call
	return call
}

func (a *ToolCallAssembler) operationRequest(call pendingToolCall) (agent.OperationRequest, error) {
	spec, ok := a.tools[call.name]
	if !ok {
		return agent.OperationRequest{}, fmt.Errorf("llm: unknown tool %q", call.name)
	}
	if spec.Target.Kind != invocation.TargetOperation {
		return agent.OperationRequest{}, fmt.Errorf("llm: tool %q target kind %q is not operation", call.name, spec.Target.Kind)
	}
	if spec.Target.Operation.Name == "" {
		return agent.OperationRequest{}, fmt.Errorf("llm: tool %q operation ref is empty", call.name)
	}
	input, err := decodeArguments(call.arguments)
	if err != nil {
		return agent.OperationRequest{}, fmt.Errorf("llm: tool %q arguments: %w", call.name, err)
	}
	return agent.OperationRequest{Operation: spec.Target.Operation, Input: input, ProviderCallID: call.id}, nil
}

func decodeArguments(raw string) (operation.Value, error) {
	if raw == "" {
		return nil, nil
	}
	var value any
	if err := json.Unmarshal([]byte(raw), &value); err != nil {
		return nil, err
	}
	return value, nil
}
