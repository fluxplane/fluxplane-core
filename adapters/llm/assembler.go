package llm

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

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
	id       string
	callType string
	index    int
	name     tool.Name
	input    string
	done     bool
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
		call.callType = evt.CallType
	case StreamToolCallDelta:
		call := a.call(evt)
		if evt.Tool != "" {
			call.name = evt.Tool
		}
		if evt.CallType != "" {
			call.callType = evt.CallType
		}
		call.input += evt.Arguments
	case StreamToolCallDone:
		call := a.call(evt)
		if evt.Tool != "" {
			call.name = evt.Tool
		}
		if evt.CallType != "" {
			call.callType = evt.CallType
		}
		call.input += evt.Arguments
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
	input, err := decodeArguments(call.input)
	if err != nil {
		return agent.OperationRequest{}, fmt.Errorf("llm: tool %q arguments: %w", call.name, err)
	}
	target, err := operationTarget(spec, input)
	if err != nil {
		return agent.OperationRequest{}, fmt.Errorf("llm: tool %q: %w", call.name, err)
	}
	if target.Operation.Name == "" {
		return agent.OperationRequest{}, fmt.Errorf("llm: tool %q operation ref is empty", call.name)
	}
	return agent.OperationRequest{
		Operation:        target.Operation,
		Input:            input,
		ProviderCallID:   call.id,
		ProviderCallType: call.callType,
	}, nil
}

func operationTarget(spec ToolSpec, input operation.Value) (invocation.Target, error) {
	if spec.Dispatch != nil {
		return dispatchTarget(spec.Dispatch, input)
	}
	if spec.Target.Kind == invocation.TargetOperation {
		return spec.Target, nil
	}
	return invocation.Target{}, fmt.Errorf("target kind %q is not operation", spec.Target.Kind)
}

func dispatchTarget(dispatch *tool.Dispatch, input operation.Value) (invocation.Target, error) {
	actionField := strings.TrimSpace(dispatch.ActionField)
	if actionField == "" {
		return invocation.Target{}, fmt.Errorf("tool dispatch action field is empty")
	}
	inputMap, ok := input.(map[string]any)
	if !ok {
		return invocation.Target{}, fmt.Errorf("tool input must be an object with %q", actionField)
	}
	action, ok := inputMap[actionField].(string)
	action = strings.TrimSpace(action)
	if !ok || action == "" {
		return invocation.Target{}, fmt.Errorf("tool action %q is required; available actions: %s", actionField, strings.Join(dispatchActions(dispatch), ", "))
	}
	for _, candidate := range dispatch.Cases {
		if candidate.Action == action {
			if candidate.Target.Kind != invocation.TargetOperation {
				return invocation.Target{}, fmt.Errorf("tool action %q target kind %q is not operation", action, candidate.Target.Kind)
			}
			return candidate.Target, nil
		}
	}
	return invocation.Target{}, fmt.Errorf("unknown action %q; available actions: %s", action, strings.Join(dispatchActions(dispatch), ", "))
}

func dispatchActions(dispatch *tool.Dispatch) []string {
	if dispatch == nil {
		return nil
	}
	actions := make([]string, 0, len(dispatch.Cases))
	for _, candidate := range dispatch.Cases {
		if candidate.Action != "" {
			actions = append(actions, candidate.Action)
		}
	}
	sort.Strings(actions)
	return actions
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
