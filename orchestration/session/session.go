package session

import (
	"context"

	"github.com/fluxplane/agentruntime/core/agent"
	"github.com/fluxplane/agentruntime/core/channel"
	"github.com/fluxplane/agentruntime/core/command"
	corecontext "github.com/fluxplane/agentruntime/core/context"
	"github.com/fluxplane/agentruntime/core/environment"
	"github.com/fluxplane/agentruntime/core/event"
	"github.com/fluxplane/agentruntime/core/invocation"
	"github.com/fluxplane/agentruntime/core/operation"
	"github.com/fluxplane/agentruntime/core/policy"
	coresession "github.com/fluxplane/agentruntime/core/session"
	corethread "github.com/fluxplane/agentruntime/core/thread"
	operationruntime "github.com/fluxplane/agentruntime/runtime/operation"
)

// Session is the first orchestration boundary for the observe-decide-apply
// loop. It is intentionally small; lifecycle and persistence will be added only
// after the core loop is stable.
type Session struct {
	Agent             agent.Agent
	Commands          *command.Registry
	Operations        *operation.Registry
	OperationExecutor operationruntime.Executor
	Events            event.Sink
	ThreadStore       corethread.Store
	Thread            corethread.Ref
}

// StepRequest describes one agent step request.
type StepRequest struct {
	Goal         string                    `json:"goal,omitempty"`
	Objective    agent.Objective           `json:"objective,omitempty"`
	Observations []environment.Observation `json:"observations,omitempty"`
	Context      []corecontext.Block       `json:"context,omitempty"`
	State        agent.StateRef            `json:"state,omitempty"`
}

// StepResult describes one orchestrated agent step.
type StepResult struct {
	Agent       agent.StepResult          `json:"agent"`
	Effect      *environment.EffectResult `json:"effect,omitempty"`
	Observation *environment.Observation  `json:"observation,omitempty"`
}

// Step runs one observe-decide-apply cycle.
func (s Session) Step(ctx context.Context, req StepRequest) StepResult {
	agentCtx := agentContext{Context: ensureContext(ctx), events: s.eventSink()}
	if s.Agent == nil {
		return StepResult{Agent: agent.StepResult{
			Status: agent.StatusFailed,
			Error:  &agent.Error{Code: "agent_missing", Message: "agent is nil"},
		}}
	}

	agentResult := s.Agent.Step(agentCtx, agent.StepInput{
		Goal:         req.Goal,
		Objective:    chooseObjective(req.Objective, s.Agent.Spec().Objective),
		Observations: req.Observations,
		Context:      req.Context,
		State:        req.State,
	})

	out := StepResult{Agent: agentResult}
	if agentResult.Status != agent.StatusOK {
		return out
	}
	if agentResult.Decision.Kind != agent.DecisionOperation || agentResult.Decision.Operation == nil {
		return out
	}

	effect := s.applyOperation(agentCtx, agentResult.Decision.Operation.Operation, agentResult.Decision.Operation.Input)
	out.Effect = &effect
	if effect.Observation.ID != "" || effect.Observation.Kind != "" {
		out.Observation = &effect.Observation
	}
	return out
}

func (s Session) applyOperation(ctx operation.Context, ref operation.Ref, input operation.Value) environment.EffectResult {
	if s.Operations == nil {
		return environment.EffectResult{Result: operation.Failed("operation_registry_missing", "operation registry is nil", nil)}
	}
	op, ok := s.Operations.Resolve(ref)
	if !ok {
		return environment.EffectResult{Result: operation.Failed("operation_not_found", "operation not found", map[string]any{
			"operation": ref.String(),
		})}
	}
	result := s.OperationExecutor.Execute(ctx, op, input)
	return environment.EffectResult{
		Result: result,
		Observation: environment.Observation{
			Source:  "operation",
			Kind:    "operation.result",
			Content: result,
		},
	}
}

func (s Session) eventSink() event.Sink {
	if s.Events == nil {
		return event.Discard()
	}
	return s.Events
}

func chooseObjective(requested, fallback agent.Objective) agent.Objective {
	if requested.Role != "" || requested.Instructions != "" || requested.Success != "" {
		return requested
	}
	return fallback
}

func ensureContext(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	return ctx
}

type agentContext struct {
	context.Context
	events event.Sink
}

func (c agentContext) Events() event.Sink {
	if c.events == nil {
		return event.Discard()
	}
	return c.events
}

// CommandStatus classifies the outcome of command dispatch.
type CommandStatus string

const (
	CommandStatusOK               CommandStatus = "ok"
	CommandStatusFailed           CommandStatus = "failed"
	CommandStatusRejected         CommandStatus = "rejected"
	CommandStatusApprovalRequired CommandStatus = "approval_required"
	CommandStatusUnsupported      CommandStatus = "unsupported"
)

// CommandError describes a structured command dispatch failure.
type CommandError struct {
	Code    string         `json:"code,omitempty"`
	Message string         `json:"message"`
	Details map[string]any `json:"details,omitempty"`
}

// CommandResult is the structured outcome of session command dispatch.
type CommandResult struct {
	Status CommandStatus             `json:"status"`
	Spec   command.Spec              `json:"spec,omitempty"`
	Policy policy.Evaluation         `json:"policy,omitempty"`
	Effect *environment.EffectResult `json:"effect,omitempty"`
	Error  *CommandError             `json:"error,omitempty"`
}

// InputStatus classifies the outcome of conversational input dispatch.
type InputStatus string

const (
	InputStatusOK          InputStatus = "ok"
	InputStatusFailed      InputStatus = "failed"
	InputStatusUnsupported InputStatus = "unsupported"
)

// InputResult is the structured outcome of session input dispatch.
type InputResult struct {
	Status   InputStatus               `json:"status"`
	Agent    agent.StepResult          `json:"agent,omitempty"`
	Effect   *environment.EffectResult `json:"effect,omitempty"`
	Outbound *channel.Outbound         `json:"outbound,omitempty"`
	Error    *CommandError             `json:"error,omitempty"`
}

// ExecuteInboundInput dispatches a channel message envelope as conversational
// input to the configured agent.
func (s Session) ExecuteInboundInput(ctx context.Context, inbound channel.Inbound) InputResult {
	if err := inbound.Validate(); err != nil {
		return inputFailed("invalid_input_inbound", err.Error(), nil)
	}
	if inbound.Kind != channel.InboundMessage || inbound.Message == nil {
		return inputFailed("invalid_input_inbound", "inbound envelope does not contain a message", nil)
	}
	if err := s.appendThreadEvents(ctx, coresession.InputReceived{
		RunID:        inbound.ID,
		Message:      *inbound.Message,
		Channel:      inbound.Channel,
		Conversation: inbound.Conversation,
		Caller:       inbound.Caller,
	}); err != nil {
		return inputFailed("thread_append_failed", err.Error(), nil)
	}
	if s.Agent == nil {
		return inputFailed("agent_missing", "agent is nil", nil)
	}

	agentCtx := agentContext{Context: ensureContext(ctx), events: s.eventSink()}
	agentResult := s.Agent.Step(agentCtx, agent.StepInput{
		Observations: []environment.Observation{{
			Source:  "channel",
			Kind:    "channel.message",
			Content: inbound.Message.Content,
			Metadata: map[string]any{
				"channel":      inbound.Channel.Name,
				"conversation": inbound.Conversation.ID,
			},
		}},
	})
	if err := s.appendThreadEvents(ctx, coresession.AgentStepCompleted{RunID: inbound.ID, Result: agentResult}); err != nil {
		return inputFailed("thread_append_failed", err.Error(), nil)
	}
	if agentResult.Status != agent.StatusOK {
		return InputResult{Status: InputStatusFailed, Agent: agentResult, Error: agentError(agentResult.Error)}
	}
	return s.applyAgentDecision(ctx, agentCtx, inbound, agentResult)
}

func (s Session) applyAgentDecision(ctx context.Context, agentCtx operation.Context, inbound channel.Inbound, agentResult agent.StepResult) InputResult {
	switch agentResult.Decision.Kind {
	case agent.DecisionMessage:
		if agentResult.Decision.Message == nil {
			return InputResult{Status: InputStatusOK, Agent: agentResult}
		}
		outbound := channel.Outbound{
			Channel:      inbound.Channel,
			Conversation: inbound.Conversation,
			Kind:         channel.OutboundMessage,
			Message: &channel.Message{
				Content:  agentResult.Decision.Message.Content,
				Metadata: agentResult.Decision.Message.Metadata,
			},
		}
		if err := s.appendThreadEvents(ctx, coresession.OutboundProduced{RunID: inbound.ID, Message: *outbound.Message}); err != nil {
			return inputFailed("thread_append_failed", err.Error(), nil)
		}
		return InputResult{Status: InputStatusOK, Agent: agentResult, Outbound: &outbound}
	case agent.DecisionComplete:
		if agentResult.Decision.Complete == nil {
			return InputResult{Status: InputStatusOK, Agent: agentResult}
		}
		outbound := channel.Outbound{
			Channel:      inbound.Channel,
			Conversation: inbound.Conversation,
			Kind:         channel.OutboundMessage,
			Message:      &channel.Message{Content: agentResult.Decision.Complete.Output},
		}
		if err := s.appendThreadEvents(ctx, coresession.OutboundProduced{RunID: inbound.ID, Message: *outbound.Message}); err != nil {
			return inputFailed("thread_append_failed", err.Error(), nil)
		}
		return InputResult{Status: InputStatusOK, Agent: agentResult, Outbound: &outbound}
	case agent.DecisionOperation:
		if agentResult.Decision.Operation == nil {
			return InputResult{Status: InputStatusUnsupported, Agent: agentResult, Error: &CommandError{Code: "operation_missing", Message: "agent operation decision is nil"}}
		}
		opReq := agentResult.Decision.Operation
		if err := s.appendThreadEvents(ctx, coresession.OperationRequested{
			RunID:     inbound.ID,
			Operation: opReq.Operation,
			Input:     opReq.Input,
		}); err != nil {
			return inputFailed("thread_append_failed", err.Error(), nil)
		}
		effect := s.applyOperation(agentCtx, opReq.Operation, opReq.Input)
		message := outboundMessageForOperationResult(effect.Result)
		if err := s.appendThreadEvents(ctx, coresession.OperationCompleted{
			RunID:     inbound.ID,
			Operation: opReq.Operation,
			Result:    effect.Result,
		}, coresession.OutboundProduced{RunID: inbound.ID, Message: message}); err != nil {
			return inputFailed("thread_append_failed", err.Error(), nil)
		}
		status := InputStatusOK
		if effect.Result.IsError() {
			status = InputStatusFailed
		}
		outbound := channel.Outbound{
			Channel:      inbound.Channel,
			Conversation: inbound.Conversation,
			Kind:         channel.OutboundMessage,
			Message:      &message,
		}
		return InputResult{Status: status, Agent: agentResult, Effect: &effect, Outbound: &outbound}
	case agent.DecisionNone, agent.DecisionWait:
		return InputResult{Status: InputStatusOK, Agent: agentResult}
	default:
		return InputResult{Status: InputStatusUnsupported, Agent: agentResult, Error: &CommandError{Code: "unsupported_agent_decision", Message: "agent decision is not supported by session input dispatch yet", Details: map[string]any{"decision": agentResult.Decision.Kind}}}
	}
}

// ExecuteInboundCommand dispatches a channel command envelope.
func (s Session) ExecuteInboundCommand(ctx context.Context, inbound channel.Inbound) CommandResult {
	if err := inbound.Validate(); err != nil {
		return commandFailed("invalid_command_inbound", err.Error(), nil)
	}
	if inbound.Kind != channel.InboundCommand || inbound.Command == nil {
		return commandFailed("invalid_command_inbound", "inbound envelope does not contain a command", nil)
	}
	if err := s.appendThreadEvents(ctx, coresession.CommandReceived{
		RunID:        inbound.ID,
		Command:      *inbound.Command,
		Channel:      inbound.Channel,
		Conversation: inbound.Conversation,
		Caller:       inbound.Caller,
	}); err != nil {
		return commandFailed("thread_append_failed", err.Error(), nil)
	}
	if s.Commands == nil {
		return commandFailed("command_registry_missing", "command registry is nil", nil)
	}
	spec, ok := s.Commands.Resolve(inbound.Command.Path)
	if !ok {
		return commandFailed("command_not_found", "command not found", map[string]any{
			"path": inbound.Command.Path.String(),
		})
	}
	evaluation := policy.EvaluateInvocation(spec.Policy, inbound.Caller, inbound.Trust)
	switch evaluation.Decision {
	case policy.DecisionDeny:
		_ = s.appendThreadEvents(ctx, coresession.CommandRejected{RunID: inbound.ID, Command: *inbound.Command, Reason: evaluation.Reason})
		return CommandResult{Status: CommandStatusRejected, Spec: spec, Policy: evaluation}
	case policy.DecisionApprovalRequired:
		_ = s.appendThreadEvents(ctx, coresession.CommandRejected{RunID: inbound.ID, Command: *inbound.Command, Reason: evaluation.Reason})
		return CommandResult{Status: CommandStatusApprovalRequired, Spec: spec, Policy: evaluation}
	}

	opCtx := operation.NewContext(ensureContext(ctx), s.eventSink())
	switch spec.Target.Kind {
	case invocation.TargetOperation:
		if err := s.appendThreadEvents(ctx, coresession.OperationRequested{
			RunID:     inbound.ID,
			Operation: spec.Target.Operation,
			Input:     inbound.Command.Input,
		}); err != nil {
			return commandFailed("thread_append_failed", err.Error(), nil)
		}
		effect := s.applyOperation(opCtx, spec.Target.Operation, inbound.Command.Input)
		if err := s.appendThreadEvents(ctx, coresession.OperationCompleted{
			RunID:     inbound.ID,
			Operation: spec.Target.Operation,
			Result:    effect.Result,
		}, coresession.OutboundProduced{
			RunID:   inbound.ID,
			Message: outboundMessageForOperationResult(effect.Result),
		}); err != nil {
			return commandFailed("thread_append_failed", err.Error(), nil)
		}
		status := CommandStatusOK
		if effect.Result.IsError() {
			status = CommandStatusFailed
		}
		return CommandResult{Status: status, Spec: spec, Policy: evaluation, Effect: &effect}
	default:
		return CommandResult{
			Status: CommandStatusUnsupported,
			Spec:   spec,
			Policy: evaluation,
			Error: &CommandError{
				Code:    "unsupported_command_target",
				Message: "command target is not supported by session command dispatch yet",
				Details: map[string]any{
					"target": spec.Target.Kind,
				},
			},
		}
	}
}

func outboundMessageForOperationResult(result operation.Result) channel.Message {
	content := result.Output
	if result.IsError() && result.Error != nil {
		content = result.Error.Message
	}
	return channel.Message{Content: content}
}

func inputFailed(code, message string, details map[string]any) InputResult {
	return InputResult{
		Status: InputStatusFailed,
		Error:  &CommandError{Code: code, Message: message, Details: details},
	}
}

func agentError(err *agent.Error) *CommandError {
	if err == nil {
		return nil
	}
	return &CommandError{Code: err.Code, Message: err.Message, Details: err.Details}
}

func commandFailed(code, message string, details map[string]any) CommandResult {
	return CommandResult{
		Status: CommandStatusFailed,
		Error:  &CommandError{Code: code, Message: message, Details: details},
	}
}

func (s Session) appendThreadEvents(ctx context.Context, events ...event.Event) error {
	if s.ThreadStore == nil || s.Thread.ID == "" || len(events) == 0 {
		return nil
	}
	records := make([]corethread.AppendRecord, 0, len(events))
	for _, payload := range events {
		if payload == nil {
			continue
		}
		records = append(records, corethread.AppendRecord{
			Event: event.Record{
				Name:    payload.EventName(),
				Payload: payload,
				Scope: event.Scope{
					ThreadID: string(s.Thread.ID),
				},
			},
		})
	}
	if len(records) == 0 {
		return nil
	}
	_, err := s.ThreadStore.Append(ensureContext(ctx), s.Thread, records...)
	return err
}
