package session

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"text/template"
	"time"

	"github.com/fluxplane/agentruntime/core/agent"
	"github.com/fluxplane/agentruntime/core/channel"
	"github.com/fluxplane/agentruntime/core/command"
	corecontext "github.com/fluxplane/agentruntime/core/context"
	coreconversation "github.com/fluxplane/agentruntime/core/conversation"
	"github.com/fluxplane/agentruntime/core/environment"
	"github.com/fluxplane/agentruntime/core/event"
	"github.com/fluxplane/agentruntime/core/invocation"
	"github.com/fluxplane/agentruntime/core/operation"
	coresession "github.com/fluxplane/agentruntime/core/session"
	corethread "github.com/fluxplane/agentruntime/core/thread"
	coreworkflow "github.com/fluxplane/agentruntime/core/workflow"
	"github.com/fluxplane/agentruntime/orchestration/resourcecatalog"
	"github.com/fluxplane/agentruntime/orchestration/sessioncontrol"
	"github.com/fluxplane/agentruntime/orchestration/sessionenv"
	"github.com/fluxplane/agentruntime/orchestration/subagent"
	workflowruntime "github.com/fluxplane/agentruntime/orchestration/workflow"
	conversationruntime "github.com/fluxplane/agentruntime/runtime/conversation"
	operationruntime "github.com/fluxplane/agentruntime/runtime/operation"
)

// Session is the first orchestration boundary for the observe-decide-apply
// loop. It is intentionally small; lifecycle and persistence will be added only
// after the core loop is stable.
type Session struct {
	Agent             agent.Agent
	Profile           coresession.Spec
	Commands          *command.Registry
	Operations        *operation.Registry
	Resolver          *sessioncontrol.Resolver
	CommandCatalog    CommandCatalog
	OperationCatalog  OperationCatalog
	WorkflowCatalog   resourcecatalog.WorkflowCatalog
	OperationExecutor operationruntime.Executor
	Events            event.Sink
	ThreadStore       corethread.Store
	Thread            corethread.Ref
	Subagents         *sessionenv.SubagentSupervisor
	Delegation        coresession.DelegationPolicy
	StopEvaluator     StopEvaluator
	RunID             string
}

type StopEvaluator = sessioncontrol.StopEvaluator
type StopEvaluationInput = sessioncontrol.StopEvaluationInput
type StopAction = sessioncontrol.StopAction
type StopEvaluation = sessioncontrol.StopEvaluation
type ModelStopEvaluator = sessioncontrol.ModelStopEvaluator

const (
	StopActionStop     = sessioncontrol.StopActionStop
	StopActionContinue = sessioncontrol.StopActionContinue
)

const (
	defaultLLMMaxSteps       = 50
	defaultLLMContinuations  = 3
	defaultGoalContinuations = 10

	defaultCompactContextTokens = 128000
	maxCompactContextTokens     = 200000
	compactTriggerRatio         = 0.85
	compactSafetyMarginTokens   = 4096
	compactLargeItemTokens      = 4096
	compactPreserveRecentItems  = 16
)

var contextCommandSpec = sessioncontrol.ContextCommandSpec
var compactCommandSpec = sessioncontrol.CompactCommandSpec
var goalCommandSpec = sessioncontrol.GoalCommandSpec

var builtInSessionCommands = map[string]sessionCommandBinding{
	contextCommandSpec.Path.String(): {Spec: contextCommandSpec, Handler: Session.executeContextCommand},
	compactCommandSpec.Path.String(): {Spec: compactCommandSpec, Handler: Session.executeCompactCommand},
	goalCommandSpec.Path.String():    {Spec: goalCommandSpec, Handler: Session.executeGoalCommand},
}

var errContextProviderNotFound = errors.New("context provider not found")
var errCompactUnavailable = errors.New("compact is unavailable without a thread store and active thread")

// OperationBinding binds a canonical operation resource to an executable
// implementation.
type OperationBinding struct {
	ID        sessioncontrol.ResourceID `json:"id"`
	Operation operation.Operation       `json:"-"`
}

// OperationCatalog binds canonical operation resource IDs to executable
// implementations.
type OperationCatalog map[string]OperationBinding

// WorkflowCatalog binds canonical workflow resource IDs to workflow specs.
type WorkflowCatalog = resourcecatalog.WorkflowCatalog

// ToolSetBinding binds a projected tool set to its canonical resource identity.
type ToolSetBinding struct {
	ID   sessioncontrol.ResourceID `json:"id"`
	Spec any                       `json:"spec"`
}

// ToolSetCatalog binds canonical tool set resource IDs to tool set specs.
type ToolSetCatalog map[string]ToolSetBinding

// CommandBinding binds a command contribution to its resolved target.
type CommandBinding struct {
	ID          sessioncontrol.ResourceID `json:"id"`
	Spec        command.Spec              `json:"spec"`
	TargetID    sessioncontrol.ResourceID `json:"target_id,omitempty"`
	OperationID sessioncontrol.ResourceID `json:"operation_id,omitempty"`
}

// CommandCatalog binds canonical command resource IDs to command specs.
type CommandCatalog map[string]CommandBinding

type sessionCommandHandler func(Session, context.Context, channel.Inbound, command.Spec, sessioncontrol.PolicyEvaluation) CommandResult

type sessionCommandBinding struct {
	Spec    command.Spec
	Handler sessionCommandHandler
}

type resolvedCommand struct {
	Binding        CommandBinding
	SessionHandler sessionCommandHandler
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
	Agent       agent.StepResult           `json:"agent"`
	Effect      *environment.EffectResult  `json:"effect,omitempty"`
	Effects     []environment.EffectResult `json:"effects,omitempty"`
	Observation *environment.Observation   `json:"observation,omitempty"`
}

// Step runs one observe-decide-apply cycle.
func (s Session) Step(ctx context.Context, req StepRequest) StepResult {
	agentCtx := agentContext{Context: s.withSubagentBaseContext(ensureContext(ctx), "", s.eventSink()), events: s.eventSink()}
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
	if agentResult.Decision.Kind != agent.DecisionOperation || len(agentResult.Decision.Operations) == 0 {
		return out
	}

	for i, request := range agentResult.Decision.Operations {
		effect := s.applyOperation(agentCtx, request.Operation, request.Input, operationCallID("", i+1))
		out.Effects = append(out.Effects, effect)
		out.Effect = &out.Effects[len(out.Effects)-1]
		if effect.Observation.ID != "" || effect.Observation.Kind != "" {
			out.Observation = &effect.Observation
		}
	}
	return out
}

func (s Session) applyOperation(ctx operation.Context, ref operation.Ref, input operation.Value, callID operation.CallID) environment.EffectResult {
	if len(s.OperationCatalog) > 0 {
		binding, err := s.OperationCatalog.Resolve(ref.String(), sessioncontrol.ResourceID{})
		if err != nil {
			return operationEffect(operation.Failed("operation_resolution_failed", err.Error(), map[string]any{
				"operation": ref.String(),
			}))
		}
		return s.executeOperation(ctx, binding.Operation, input, callID)
	}
	if s.Operations == nil {
		return environment.EffectResult{Result: operation.Failed("operation_registry_missing", "operation registry is nil", nil)}
	}
	op, ok := s.Operations.Resolve(ref)
	if !ok {
		return environment.EffectResult{Result: operation.Failed("operation_not_found", "operation not found", map[string]any{
			"operation": ref.String(),
		})}
	}
	return s.executeOperation(ctx, op, input, callID)
}

func (s Session) eventSink() event.Sink {
	if s.Events == nil {
		return event.Discard()
	}
	return s.Events
}

func (s Session) emitLive(payload event.Event) {
	if payload == nil {
		return
	}
	s.eventSink().Emit(payload)
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
	Status CommandStatus                   `json:"status"`
	Spec   command.Spec                    `json:"spec,omitempty"`
	Policy sessioncontrol.PolicyEvaluation `json:"policy,omitempty"`
	Effect *environment.EffectResult       `json:"effect,omitempty"`
	Output any                             `json:"output,omitempty"`
	Error  *CommandError                   `json:"error,omitempty"`
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
	Status   InputStatus                `json:"status"`
	Agent    agent.StepResult           `json:"agent,omitempty"`
	Effect   *environment.EffectResult  `json:"effect,omitempty"`
	Effects  []environment.EffectResult `json:"effects,omitempty"`
	Outbound *channel.Outbound          `json:"outbound,omitempty"`
	Error    *CommandError              `json:"error,omitempty"`
}

type inputExecutionOptions struct {
	Goal             string
	MaxContinuations int
}

// ExecuteInboundInput dispatches a channel message envelope as conversational
// input to the configured agent.
func (s Session) ExecuteInboundInput(ctx context.Context, inbound channel.Inbound) InputResult {
	return s.executeInboundInput(ctx, inbound, inputExecutionOptions{})
}

func (s Session) executeInboundInput(ctx context.Context, inbound channel.Inbound, opts inputExecutionOptions) InputResult {
	if err := inbound.Validate(); err != nil {
		return inputFailed("invalid_input_inbound", err.Error(), nil)
	}
	if inbound.Kind != channel.InboundMessage || inbound.Message == nil {
		return inputFailed("invalid_input_inbound", "inbound envelope does not contain a message", nil)
	}
	history, err := s.historyContext(ctx)
	if err != nil {
		return inputFailed("thread_history_failed", err.Error(), nil)
	}
	if err := s.appendThreadEvents(ctx, coresession.InputReceived{
		RunID:        inbound.ID,
		Message:      *inbound.Message,
		Channel:      inbound.Channel,
		Conversation: inbound.Conversation,
		Caller:       inbound.Caller,
		Trust:        inbound.Trust,
	}); err != nil {
		return inputFailed("thread_append_failed", err.Error(), nil)
	}
	if s.Agent == nil {
		return inputFailed("agent_missing", "agent is nil", nil)
	}
	if err := s.replaySkillEvents(ctx); err != nil {
		return inputFailed("skill_replay_failed", err.Error(), nil)
	}

	baseCtx := ensureContext(ctx)
	var conversationErr error
	var localTranscript []coreconversation.Item
	var localContinuation *coreconversation.ContinuationHandle
	var localContextRecords map[corecontext.ProviderName]corecontext.ProviderRenderRecord
	events := s.conversationEventSink(ctx, inbound.ID, &conversationErr, &localTranscript, &localContinuation)
	observations := []environment.Observation{{
		Source:   "channel",
		Kind:     "channel.message",
		Content:  inbound.Message.Content,
		Metadata: inputObservationMetadata(inbound),
	}}
	goal := strings.TrimSpace(opts.Goal)
	var (
		state   agent.StateRef
		effects []environment.EffectResult
		pending = []coreconversation.Item{inputTranscriptItem(s.providerIdentity(), inbound.Message.Content)}
	)
	for continuation := 0; ; continuation++ {
		inner := s.runInnerTurn(ctx, innerTurnInput{
			Inbound:             inbound,
			BaseContext:         baseCtx,
			History:             history,
			Events:              events,
			ConversationErr:     &conversationErr,
			LocalTranscript:     &localTranscript,
			LocalContinuation:   &localContinuation,
			LocalContextRecords: &localContextRecords,
			Pending:             pending,
			Goal:                goal,
			Observations:        observations,
			State:               state,
			Effects:             effects,
			MaxSteps:            s.maxSteps(),
			FailOnStepLimit:     s.failOnStepLimitForInput(opts),
			ProviderIdentity:    s.providerIdentity(),
			ConversationTurnID:  inbound.ID,
		})
		if inner.Result.Status != "" {
			return s.finalizeInputResult(ctx, inbound, inner.Result)
		}
		state = inner.State
		effects = inner.Effects
		decision := s.evaluateContinuation(ctx, inbound, opts, continuation, inner.AgentResult, effects)
		if decision.Result.Status != "" {
			return s.finalizeInputResult(ctx, inbound, decision.Result)
		}
		if !decision.Continue {
			// When the inner loop exits cleanly at the step budget with a
			// pending operation decision, surface the operation boundary result
			// instead of treating it as a terminal agent decision.
			if inner.AgentResult.Decision.Kind == agent.DecisionOperation {
				return s.finalizeInputResult(ctx, inbound, s.operationBoundaryResult(ctx, inbound, inner.AgentResult, effects))
			}
			return s.finalizeInputResult(ctx, inbound, s.applyTerminalAgentDecision(ctx, inbound, inner.AgentResult, effects))
		}
		instruction := strings.TrimSpace(decision.Instruction)
		if instruction == "" {
			instruction = "Continue."
		}
		pending = []coreconversation.Item{inputTranscriptItem(s.providerIdentity(), instruction)}
		observations = []environment.Observation{{
			Source:  "session",
			Kind:    "session.continuation",
			Content: instruction,
			Metadata: map[string]any{
				"continuation": continuation + 1,
				"reason":       decision.Reason,
			},
		}}
	}
}

type innerTurnInput struct {
	Inbound             channel.Inbound
	BaseContext         context.Context
	History             []corecontext.Block
	Events              event.Sink
	ConversationErr     *error
	LocalTranscript     *[]coreconversation.Item
	LocalContinuation   **coreconversation.ContinuationHandle
	LocalContextRecords *map[corecontext.ProviderName]corecontext.ProviderRenderRecord
	Pending             []coreconversation.Item
	Goal                string
	Observations        []environment.Observation
	State               agent.StateRef
	Effects             []environment.EffectResult
	MaxSteps            int
	FailOnStepLimit     bool
	ProviderIdentity    coreconversation.ProviderIdentity
	ConversationTurnID  string
}

type innerTurnResult struct {
	Result      InputResult
	AgentResult agent.StepResult
	State       agent.StateRef
	Effects     []environment.EffectResult
}

func (s Session) runInnerTurn(ctx context.Context, in innerTurnInput) innerTurnResult {
	pending := append([]coreconversation.Item(nil), in.Pending...)
	observations := append([]environment.Observation(nil), in.Observations...)
	state := in.State
	effects := append([]environment.EffectResult(nil), in.Effects...)
	maxSteps := in.MaxSteps
	if maxSteps <= 0 {
		maxSteps = 1
	}
	var lastAgentResult agent.StepResult
	for step := 0; ; step++ {
		// Check budget BEFORE calling the model — not after.
		if step >= maxSteps {
			if in.FailOnStepLimit {
				return innerTurnResult{
					Result:      s.stepLimitResult(ctx, in.Inbound, lastAgentResult, effects),
					AgentResult: lastAgentResult,
					State:       state,
					Effects:     effects,
				}
			}
			// Clean break: outer loop will call evaluateContinuation.
			return innerTurnResult{AgentResult: lastAgentResult, State: state, Effects: effects}
		}
		contextResult, projectedPending, err := s.materializeContext(ctx, in, pending, observations)
		if err != nil {
			return innerTurnResult{Result: inputFailed("context_render_failed", err.Error(), nil), State: state, Effects: effects}
		}
		transcript, err := s.transcriptForPending(ctx, projectedPending, derefItems(in.LocalTranscript), derefHandle(in.LocalContinuation))
		if err != nil {
			return innerTurnResult{Result: inputFailed("conversation_projection_failed", err.Error(), nil), State: state, Effects: effects}
		}
		modelCtx := sessioncontrol.ContextWithTranscript(in.BaseContext, &transcript)
		modelCtx = s.withSubagentBaseContext(modelCtx, "", in.Events)
		agentCtx := agentContext{Context: modelCtx, events: in.Events}
		agentResult := s.Agent.Step(agentCtx, agent.StepInput{
			Goal:         in.Goal,
			Observations: observations,
			Context:      in.History,
			State:        state,
		})
		if in.ConversationErr != nil && *in.ConversationErr != nil {
			return innerTurnResult{Result: inputFailed("conversation_append_failed", (*in.ConversationErr).Error(), nil), AgentResult: agentResult, State: state, Effects: effects}
		}
		if err := s.appendThreadEvents(ctx, coresession.AgentStepCompleted{RunID: in.Inbound.ID, Result: agentResult}); err != nil {
			return innerTurnResult{Result: inputFailed("thread_append_failed", err.Error(), nil), AgentResult: agentResult, State: state, Effects: effects}
		}
		if agentResult.Status != agent.StatusOK {
			if err := s.persistFailedTurnTranscript(ctx, in.ConversationTurnID, in.ProviderIdentity, pending, in.LocalTranscript, agentErrorMessage(agentResult)); err != nil {
				return innerTurnResult{Result: inputFailed("conversation_append_failed", err.Error(), nil), AgentResult: agentResult, State: state, Effects: effects}
			}
			return innerTurnResult{Result: InputResult{Status: InputStatusFailed, Agent: agentResult, Effect: lastEffect(effects), Effects: effects, Error: agentError(agentResult.Error)}, AgentResult: agentResult, State: state, Effects: effects}
		}
		if err := s.commitContextRender(ctx, contextResult, in.LocalContextRecords); err != nil {
			return innerTurnResult{Result: inputFailed("context_commit_failed", err.Error(), nil), AgentResult: agentResult, State: state, Effects: effects}
		}
		if !stateRefIsZero(agentResult.State.Ref) {
			state = agentResult.State.Ref
		}
		lastAgentResult = agentResult
		if agentResult.Decision.Kind != agent.DecisionOperation {
			return innerTurnResult{AgentResult: agentResult, State: state, Effects: effects}
		}
		if len(agentResult.Decision.Operations) == 0 {
			return innerTurnResult{Result: InputResult{Status: InputStatusUnsupported, Agent: agentResult, Effect: lastEffect(effects), Effects: effects, Error: &CommandError{Code: "operation_missing", Message: "agent operation decision is empty"}}, AgentResult: agentResult, State: state, Effects: effects}
		}
		batch, toolResults, err := s.applyAgentOperations(ctx, agentCtx, in.Inbound, len(effects), agentResult.Decision.Operations)
		if err != nil {
			return innerTurnResult{Result: inputFailed("thread_append_failed", err.Error(), nil), AgentResult: agentResult, State: state, Effects: effects}
		}
		effects = append(effects, batch...)
		// Persist tool results before the budget check at the top of the next
		// iteration fires so they are durably recorded if the loop exits there.
		if step+1 >= maxSteps {
			if err := s.persistPendingTranscriptItems(ctx, in.ConversationTurnID, in.ProviderIdentity, toolResults, in.LocalTranscript); err != nil {
				return innerTurnResult{Result: inputFailed("conversation_append_failed", err.Error(), nil), AgentResult: agentResult, State: state, Effects: effects}
			}
		}
		observations = append(observations, observationsForEffects(batch)...)
		pending = toolResults
	}
}

func (s Session) finalizeInputResult(ctx context.Context, inbound channel.Inbound, result InputResult) InputResult {
	if result.Status == InputStatusOK {
		_ = s.autoCompactAfterTurn(ctx, inbound.ID)
	}
	return result
}

func derefItems(items *[]coreconversation.Item) []coreconversation.Item {
	if items == nil {
		return nil
	}
	return *items
}

func derefHandle(handle **coreconversation.ContinuationHandle) *coreconversation.ContinuationHandle {
	if handle == nil {
		return nil
	}
	return *handle
}

func (s Session) persistPendingTranscriptItems(ctx context.Context, turnID string, provider coreconversation.ProviderIdentity, items []coreconversation.Item, localItems *[]coreconversation.Item) error {
	if len(items) == 0 {
		return nil
	}
	copied := append([]coreconversation.Item(nil), items...)
	if localItems != nil {
		*localItems = append(*localItems, copied...)
	}
	return s.appendConversation(ctx, turnID, provider, copied)
}

func (s Session) persistFailedTurnTranscript(ctx context.Context, turnID string, provider coreconversation.ProviderIdentity, pending []coreconversation.Item, localItems *[]coreconversation.Item, reason string) error {
	base := derefItems(localItems)
	if len(base) == 0 {
		return s.persistPendingTranscriptItems(ctx, turnID, provider, pending, localItems)
	}
	repaired := conversationruntime.RepairToolContinuity(base, conversationruntime.ToolContinuityRepairOptions{
		Provider:            provider,
		RepairOrphanResults: true,
		MissingResultReason: reason,
	})
	if len(repaired.Repairs) == 0 {
		return nil
	}
	return s.persistPendingTranscriptItems(ctx, turnID, provider, repaired.Repairs, localItems)
}

func agentErrorMessage(result agent.StepResult) string {
	if result.Error != nil && strings.TrimSpace(result.Error.Message) != "" {
		return result.Error.Message
	}
	return "Tool call did not complete because the model turn failed before a result could be recorded."
}

func inputObservationMetadata(inbound channel.Inbound) map[string]any {
	out := map[string]any{
		"channel":      inbound.Channel.Name,
		"conversation": inbound.Conversation.ID,
	}
	if inbound.Message != nil {
		for key, value := range inbound.Message.Metadata {
			out[key] = value
		}
	}
	return out
}

func continuationPolicyForInput(opts inputExecutionOptions, spec agent.Spec) agent.ContinuationPolicy {
	goal := strings.TrimSpace(opts.Goal)
	if goal == "" {
		return spec.Turns.Continuation
	}
	contextPolicy := strings.TrimSpace(spec.Turns.Continuation.ContextPolicy)
	switch contextPolicy {
	case "summary", "new":
	default:
		contextPolicy = "summary"
	}
	return agent.ContinuationPolicy{
		MaxContinuations: opts.MaxContinuations,
		ContextPolicy:    contextPolicy,
		StopCondition: agent.StopConditionSpec{
			Type:   "prompt",
			Prompt: goalStopPrompt(goal),
		},
	}
}

func goalStopPrompt(goal string) string {
	return "Goal:\n" + goal + "\n\nStop when the goal is complete, impossible, blocked, or no reasonable next action remains. Continue only when more work is needed, and provide the next concrete instruction for the parent agent."
}

type providerIdentityAgent interface {
	ProviderIdentity() coreconversation.ProviderIdentity
}

type contextProviderAgent interface {
	ContextProviders() []corecontext.Provider
}

type contextPreviewInput struct {
	Fresh bool   `json:"fresh,omitempty" command:"flag=fresh"`
	Key   string `json:"key,omitempty" command:"flag=key"`
}

type goalCommandInput struct {
	Goal                []string `json:"goal,omitempty" command:"arg"`
	Max                 *int     `json:"max,omitempty" command:"flag=max"`
	MaxContinuations    *int     `json:"max_continuations,omitempty" command:"flag=max-continuations"`
	MaxContinuationsAlt *int     `json:"max-continuations,omitempty"`
	DefaultMax          *int     `json:"-" command:"default=10"`
}

type contextPreviewData struct {
	Mode      string   `json:"mode"`
	Key       string   `json:"key,omitempty"`
	Providers []string `json:"providers,omitempty"`
	System    string   `json:"system,omitempty"`
	Developer string   `json:"developer,omitempty"`
	User      string   `json:"user,omitempty"`
}

func (s Session) previewContext(ctx context.Context, input contextPreviewInput) (contextPreviewData, error) {
	providers := s.contextProviders()
	if len(providers) == 0 {
		mode := "diff"
		if input.Fresh {
			mode = "fresh"
		}
		return contextPreviewData{Mode: mode}, nil
	}
	key := strings.TrimSpace(input.Key)
	providerNames := sortedProviderNames(providers)
	if key != "" {
		var filtered []corecontext.Provider
		for _, provider := range providers {
			if provider == nil {
				continue
			}
			if string(provider.Spec().Name) == key {
				filtered = append(filtered, provider)
			}
		}
		if len(filtered) == 0 {
			mode := "diff"
			if input.Fresh {
				mode = "fresh"
			}
			return contextPreviewData{Mode: mode, Key: key, Providers: providerNames}, fmt.Errorf("%w: %q", errContextProviderNotFound, key)
		}
		providers = filtered
	}
	var previous map[corecontext.ProviderName]corecontext.ProviderRenderRecord
	if !input.Fresh {
		var err error
		previous, err = s.loadContextRenderRecords(ctx)
		if err != nil {
			return contextPreviewData{}, err
		}
	}
	result, err := sessionenv.BuildContext(providers, previous, s.contextProviderContext(ctx, nil), corecontext.BuildRequest{
		ThreadID: string(s.Thread.ID),
		BranchID: string(s.Thread.BranchID),
		TurnID:   s.RunID,
		Reason:   corecontext.RenderTurn,
		Previous: previous,
	})
	if err != nil {
		return contextPreviewData{}, err
	}
	mode := "diff"
	if input.Fresh {
		mode = "fresh"
	}
	data := contextPreviewData{Mode: mode, Key: key, Providers: providerNames}
	if text, ok := sessionenv.RenderDiff(result, corecontext.PlacementSystem); ok {
		data.System = text
	}
	if text, ok := sessionenv.RenderDiff(result, corecontext.PlacementDeveloper); ok {
		data.Developer = text
	}
	if text, ok := sessionenv.RenderDiff(result, corecontext.PlacementUser); ok {
		data.User = text
	}
	return data, nil
}

func sortedProviderNames(providers []corecontext.Provider) []string {
	names := make([]string, 0, len(providers))
	for _, provider := range providers {
		if provider == nil {
			continue
		}
		name := strings.TrimSpace(string(provider.Spec().Name))
		if name != "" {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

func (s Session) materializeContext(ctx context.Context, in innerTurnInput, pending []coreconversation.Item, observations []environment.Observation) (corecontext.BuildResult, []coreconversation.Item, error) {
	if err := s.activateTriggeredSkills(pending, observations, in.Events); err != nil {
		return corecontext.BuildResult{}, nil, err
	}
	providers := s.contextProviders()
	if len(providers) == 0 {
		return corecontext.BuildResult{}, append([]coreconversation.Item(nil), pending...), nil
	}
	records, err := s.contextRenderRecords(ctx, in.LocalContextRecords)
	if err != nil {
		return corecontext.BuildResult{}, nil, err
	}
	renderCtx := s.contextProviderContext(in.BaseContext, observations)
	result, err := sessionenv.BuildContext(providers, records, renderCtx, corecontext.BuildRequest{
		ThreadID: string(s.Thread.ID),
		BranchID: string(s.Thread.BranchID),
		TurnID:   in.ConversationTurnID,
		Reason:   contextRenderReason(pending, observations),
		Previous: records,
	})
	if err != nil {
		return corecontext.BuildResult{}, nil, err
	}
	return result, contextPendingItems(in.ProviderIdentity, pending, result), nil
}

func (s Session) activateTriggeredSkills(pending []coreconversation.Item, observations []environment.Observation, sink event.Sink) error {
	return sessionenv.ActivateSkillTriggers(skillTriggerText(pending, observations), s.envConfig(sink))
}

func skillTriggerText(pending []coreconversation.Item, observations []environment.Observation) string {
	var parts []string
	for _, item := range pending {
		if item.Kind == coreconversation.ItemInput {
			if text := valueText(item.Content); text != "" {
				parts = append(parts, text)
			}
		}
	}
	for _, observation := range observations {
		switch observation.Kind {
		case "channel.message", "session.continuation":
			if text := valueText(observation.Content); text != "" {
				parts = append(parts, text)
			}
		}
	}
	return strings.Join(parts, "\n")
}

func (s Session) contextProviders() []corecontext.Provider {
	carrier, ok := s.Agent.(contextProviderAgent)
	if !ok || carrier == nil {
		return nil
	}
	return carrier.ContextProviders()
}

func (s Session) contextProviderContext(ctx context.Context, observations []environment.Observation) context.Context {
	return sessionenv.ContextProviderContext(ctx, s.envConfig(s.eventSink()), observations)
}

func contextRenderReason(pending []coreconversation.Item, observations []environment.Observation) corecontext.RenderReason {
	for _, item := range pending {
		if item.Kind == coreconversation.ItemToolResult {
			return corecontext.RenderToolFollowup
		}
	}
	for _, observation := range observations {
		if observation.Kind == "session.continuation" {
			return corecontext.RenderContinuation
		}
	}
	return corecontext.RenderTurn
}

func (s Session) contextRenderRecords(ctx context.Context, local *map[corecontext.ProviderName]corecontext.ProviderRenderRecord) (map[corecontext.ProviderName]corecontext.ProviderRenderRecord, error) {
	if local != nil && *local != nil {
		return cloneContextRecords(*local), nil
	}
	records, err := s.loadContextRenderRecords(ctx)
	if err != nil {
		return nil, err
	}
	if local != nil {
		*local = cloneContextRecords(records)
	}
	return records, nil
}

func (s Session) loadContextRenderRecords(ctx context.Context) (map[corecontext.ProviderName]corecontext.ProviderRenderRecord, error) {
	if s.ThreadStore == nil || s.Thread.ID == "" {
		return nil, nil
	}
	snapshot, err := s.ThreadStore.Read(ensureContext(ctx), corethread.ReadParams{ID: s.Thread.ID})
	if err != nil {
		if errors.Is(err, corethread.ErrNotFound) {
			return nil, nil
		}
		return nil, err
	}
	records, err := snapshot.EventsForBranch(s.Thread.BranchID)
	if err != nil {
		return nil, err
	}
	var out map[corecontext.ProviderName]corecontext.ProviderRenderRecord
	for _, record := range records {
		switch payload := record.Event.Payload.(type) {
		case corecontext.RenderCommitted:
			out = cloneContextRecords(payload.Records)
		case *corecontext.RenderCommitted:
			if payload != nil {
				out = cloneContextRecords(payload.Records)
			}
		}
	}
	return out, nil
}

func (s Session) commitContextRender(ctx context.Context, result corecontext.BuildResult, local *map[corecontext.ProviderName]corecontext.ProviderRenderRecord) error {
	if len(result.Records) == 0 {
		return nil
	}
	if local != nil {
		*local = cloneContextRecords(result.Records)
	}
	if result.EmptyDiff() {
		return nil
	}
	events := make([]event.Event, 0, len(result.Added)+len(result.Updated)+len(result.Removed)+1)
	for _, block := range append(append([]corecontext.Block(nil), result.Added...), result.Updated...) {
		events = append(events, corecontext.BlockRecorded{
			TurnID:      result.TurnID,
			Provider:    block.Provider,
			Block:       block,
			Fingerprint: sessionenv.BlockFingerprint(block),
		})
	}
	for _, removed := range result.Removed {
		events = append(events, corecontext.BlockRemovedRecorded{TurnID: result.TurnID, Removed: removed})
	}
	events = append(events, corecontext.RenderCommitted{
		TurnID:  result.TurnID,
		Records: cloneContextRecords(result.Records),
	})
	return s.appendThreadEvents(ctx, events...)
}

func contextPendingItems(provider coreconversation.ProviderIdentity, pending []coreconversation.Item, result corecontext.BuildResult) []coreconversation.Item {
	out := append([]coreconversation.Item(nil), pending...)
	if result.EmptyDiff() {
		return out
	}
	var prefix []coreconversation.Item
	if text, ok := sessionenv.RenderDiff(result, corecontext.PlacementSystem); ok {
		prefix = append(prefix, contextTranscriptItem(provider, "system", text))
	}
	if text, ok := sessionenv.RenderDiff(result, corecontext.PlacementDeveloper); ok {
		prefix = append(prefix, contextTranscriptItem(provider, "developer", text))
	}
	if len(prefix) > 0 {
		out = append(prefix, out...)
	}
	if text, ok := sessionenv.RenderDiff(result, corecontext.PlacementUser); ok {
		out = addUserContextDiff(provider, out, text)
	}
	return out
}

func contextTranscriptItem(provider coreconversation.ProviderIdentity, role, content string) coreconversation.Item {
	return coreconversation.Item{
		Provider: provider,
		Kind:     coreconversation.ItemInput,
		Role:     role,
		Content:  content,
		Metadata: map[string]string{"context": "diff"},
	}
}

func addUserContextDiff(provider coreconversation.ProviderIdentity, items []coreconversation.Item, diff string) []coreconversation.Item {
	out := append([]coreconversation.Item(nil), items...)
	for i, item := range out {
		if item.Kind == coreconversation.ItemInput && strings.TrimSpace(item.Role) == "user" {
			item.Content = prependContextDiff(diff, item.Content)
			item.Metadata = cloneMetadata(item.Metadata)
			item.Metadata["context"] = "diff"
			out[i] = item
			return out
		}
	}
	return append(out, contextTranscriptItem(provider, "user", diff))
}

func prependContextDiff(diff string, content any) string {
	body := contextValueText(content)
	if strings.TrimSpace(body) == "" {
		return diff
	}
	return strings.TrimSpace(diff) + "\n\n" + body
}

func contextValueText(value any) string {
	switch typed := value.(type) {
	case nil:
		return ""
	case string:
		return typed
	case []byte:
		return string(typed)
	default:
		data, err := json.Marshal(typed)
		if err != nil {
			return fmt.Sprint(typed)
		}
		return string(data)
	}
}

func cloneMetadata(values map[string]string) map[string]string {
	if values == nil {
		return map[string]string{}
	}
	out := make(map[string]string, len(values)+1)
	for key, value := range values {
		out[key] = value
	}
	return out
}

func cloneContextRecords(records map[corecontext.ProviderName]corecontext.ProviderRenderRecord) map[corecontext.ProviderName]corecontext.ProviderRenderRecord {
	if records == nil {
		return nil
	}
	out := make(map[corecontext.ProviderName]corecontext.ProviderRenderRecord, len(records))
	for key, record := range records {
		copied := record
		if record.Blocks != nil {
			copied.Blocks = make(map[string]corecontext.RenderedBlockRecord, len(record.Blocks))
			for blockID, block := range record.Blocks {
				copied.Blocks[blockID] = block
			}
		}
		out[key] = copied
	}
	return out
}

func (s Session) transcriptForPending(ctx context.Context, pending, localItems []coreconversation.Item, localHandle *coreconversation.ContinuationHandle) (coreconversation.Transcript, error) {
	provider := s.providerIdentity()
	if s.ThreadStore == nil || s.Thread.ID == "" {
		if localHandle != nil && localHandle.SupportsPreviousResponseID() {
			copied := *localHandle
			repaired := conversationruntime.RepairToolContinuity(append(append([]coreconversation.Item(nil), localItems...), pending...), conversationruntime.ToolContinuityRepairOptions{
				Provider: provider,
			})
			items := append([]coreconversation.Item(nil), repaired.Repairs...)
			items = append(items, pending...)
			return coreconversation.Transcript{
				Provider:     provider,
				Items:        items,
				NewItems:     append([]coreconversation.Item(nil), items...),
				Continuation: &copied,
				Mode:         coreconversation.ProjectionNativeContinuation,
			}, nil
		}
		repaired := conversationruntime.RepairToolContinuity(append(append([]coreconversation.Item(nil), localItems...), pending...), conversationruntime.ToolContinuityRepairOptions{
			Provider:            provider,
			RepairOrphanResults: true,
		})
		newItems := append([]coreconversation.Item(nil), repaired.Repairs...)
		newItems = append(newItems, pending...)
		return coreconversation.Transcript{
			Provider: provider,
			Items:    repaired.Items,
			NewItems: newItems,
			Mode:     coreconversation.ProjectionFullReplay,
		}, nil
	}
	snapshot, err := s.ThreadStore.Read(ensureContext(ctx), corethread.ReadParams{ID: s.Thread.ID})
	if err != nil {
		if errors.Is(err, corethread.ErrNotFound) {
			repaired := conversationruntime.RepairToolContinuity(pending, conversationruntime.ToolContinuityRepairOptions{
				Provider:            provider,
				RepairOrphanResults: true,
			})
			newItems := append([]coreconversation.Item(nil), repaired.Repairs...)
			newItems = append(newItems, pending...)
			return coreconversation.Transcript{
				Provider: provider,
				Items:    repaired.Items,
				NewItems: newItems,
				Mode:     coreconversation.ProjectionFullReplay,
			}, nil
		}
		return coreconversation.Transcript{}, err
	}
	projected, err := conversationruntime.Project(conversationruntime.ProjectionInput{
		Thread:   snapshot,
		BranchID: s.Thread.BranchID,
		Provider: provider,
		Pending:  pending,
		Mode:     coreconversation.ProjectionNativeContinuation,
	})
	if err != nil {
		return coreconversation.Transcript{}, err
	}
	return projected.Transcript(provider), nil
}

func (s Session) providerIdentity() coreconversation.ProviderIdentity {
	var identity coreconversation.ProviderIdentity
	if s.Agent != nil {
		if identified, ok := s.Agent.(providerIdentityAgent); ok {
			identity = identified.ProviderIdentity()
		}
		spec := s.Agent.Spec()
		identity.Model = firstNonEmptyString(identity.Model, spec.Inference.Model)
		identity.Provider = firstNonEmptyString(
			identity.Provider,
			spec.Inference.Annotations["provider"],
			spec.Inference.Annotations["llm.provider"],
			spec.Driver.Annotations["provider"],
			stringFromAny(spec.Driver.Config["provider"]),
		)
		identity.API = firstNonEmptyString(
			identity.API,
			spec.Inference.Annotations["api"],
			spec.Inference.Annotations["llm.api"],
			spec.Driver.Annotations["api"],
			stringFromAny(spec.Driver.Config["api"]),
		)
		identity.Family = firstNonEmptyString(
			identity.Family,
			spec.Inference.Annotations["family"],
			spec.Inference.Annotations["llm.family"],
			spec.Driver.Annotations["family"],
			stringFromAny(spec.Driver.Config["family"]),
		)
	}
	identity.Provider, identity.Model = normalizeProviderModel(identity.Provider, identity.Model)
	return identity
}

func normalizeProviderModel(provider, model string) (string, string) {
	provider = strings.TrimSpace(provider)
	model = strings.TrimSpace(model)
	if before, after, ok := strings.Cut(model, "/"); ok && before != "" && after != "" {
		switch {
		case provider == "" && knownModelProviderPrefix(before):
			return before, after
		case provider != "" && before == provider:
			return provider, after
		}
	}
	return provider, model
}

func knownModelProviderPrefix(value string) bool {
	switch value {
	case "openai", "codex", "openrouter", "anthropic", "claudecode", "minimax":
		return true
	default:
		return false
	}
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func stringFromAny(value any) string {
	if value == nil {
		return ""
	}
	text, ok := value.(string)
	if !ok {
		return ""
	}
	return text
}

func inputTranscriptItem(provider coreconversation.ProviderIdentity, content any) coreconversation.Item {
	return coreconversation.Item{
		Provider: provider,
		Kind:     coreconversation.ItemInput,
		Role:     "user",
		Content:  content,
	}
}

func operationResultTranscriptItem(provider coreconversation.ProviderIdentity, opReq agent.OperationRequest, callID operation.CallID, result operation.Result) coreconversation.Item {
	providerCallID := opReq.ProviderCallID
	if providerCallID == "" {
		providerCallID = string(callID)
	}
	content := result.Output
	if rendered, ok := result.Output.(operation.ModelRenderable); ok {
		content = rendered.ModelText()
	}
	if result.IsError() && result.Error != nil {
		content = map[string]any{
			"code":    result.Error.Code,
			"message": result.Error.Message,
			"details": result.Error.Details,
		}
	}
	item := coreconversation.Item{
		Provider: provider,
		Kind:     coreconversation.ItemToolResult,
		CallID:   providerCallID,
		Name:     opReq.Operation.String(),
		Content:  content,
	}
	if opReq.ProviderCallType != "" {
		item.Metadata = map[string]string{"provider_call_type": opReq.ProviderCallType}
	}
	return item
}

func (s Session) conversationEventSink(ctx context.Context, turnID string, errp *error, localItems *[]coreconversation.Item, localHandle **coreconversation.ContinuationHandle) event.Sink {
	live := s.eventSink()
	return event.SinkFunc(func(payload event.Event) {
		if payload == nil {
			return
		}
		live.Emit(payload)
		if errp != nil && *errp != nil {
			return
		}
		switch typed := payload.(type) {
		case coreconversation.ItemsAppended:
			if typed.TurnID == "" {
				typed.TurnID = turnID
			}
			if typed.Provider.Provider == "" {
				typed.Provider = s.providerIdentity()
			}
			if localItems != nil {
				*localItems = append(*localItems, typed.Items...)
			}
			if err := s.appendConversation(ctx, typed.TurnID, typed.Provider, typed.Items); err != nil && errp != nil {
				*errp = err
			}
		case *coreconversation.ItemsAppended:
			if typed == nil {
				return
			}
			copied := *typed
			if copied.TurnID == "" {
				copied.TurnID = turnID
			}
			if copied.Provider.Provider == "" {
				copied.Provider = s.providerIdentity()
			}
			if localItems != nil {
				*localItems = append(*localItems, copied.Items...)
			}
			if err := s.appendConversation(ctx, copied.TurnID, copied.Provider, copied.Items); err != nil && errp != nil {
				*errp = err
			}
		case coreconversation.ContinuationStored:
			if typed.TurnID == "" {
				typed.TurnID = turnID
			}
			if typed.Handle.BranchID == "" {
				typed.Handle.BranchID = s.Thread.BranchID
			}
			if localHandle != nil {
				copied := typed.Handle
				*localHandle = &copied
			}
			if err := s.appendConversation(ctx, typed.TurnID, typed.Handle.Provider, nil, typed.Handle); err != nil && errp != nil {
				*errp = err
			}
		case *coreconversation.ContinuationStored:
			if typed == nil {
				return
			}
			copied := *typed
			if copied.TurnID == "" {
				copied.TurnID = turnID
			}
			if copied.Handle.BranchID == "" {
				copied.Handle.BranchID = s.Thread.BranchID
			}
			if localHandle != nil {
				handle := copied.Handle
				*localHandle = &handle
			}
			if err := s.appendConversation(ctx, copied.TurnID, copied.Handle.Provider, nil, copied.Handle); err != nil && errp != nil {
				*errp = err
			}
		}
	})
}

func (s Session) historyContext(ctx context.Context) ([]corecontext.Block, error) {
	if s.ThreadStore == nil || s.Thread.ID == "" {
		return nil, nil
	}
	snapshot, err := s.ThreadStore.Read(ensureContext(ctx), corethread.ReadParams{ID: s.Thread.ID})
	if err != nil {
		if errors.Is(err, corethread.ErrNotFound) {
			return nil, nil
		}
		return nil, err
	}
	records, err := snapshot.EventsForBranch(s.Thread.BranchID)
	if err != nil {
		return nil, err
	}
	text := conversationHistoryText(records)
	if strings.TrimSpace(text) == "" {
		return nil, nil
	}
	return []corecontext.Block{{
		ID:        "session.history",
		Provider:  corecontext.ProviderName("session.history"),
		Kind:      corecontext.BlockText,
		Title:     "Conversation history",
		Content:   text,
		Freshness: corecontext.FreshnessDynamic,
	}}, nil
}

func conversationHistoryText(records []corethread.Record) string {
	const maxLines = 24
	lines := make([]string, 0, len(records))
	for _, record := range records {
		switch payload := record.Event.Payload.(type) {
		case coresession.InputReceived:
			if text := valueText(payload.Message.Content); text != "" {
				lines = append(lines, "User: "+text)
			}
		case coresession.OutboundProduced:
			if text := valueText(payload.Message.Content); text != "" {
				lines = append(lines, "Agent: "+text)
			}
		case coresession.CommandReceived:
			if text := valueText(payload.Command.Input); text != "" {
				lines = append(lines, "Command "+payload.Command.Path.String()+": "+text)
			}
		}
	}
	if len(lines) > maxLines {
		lines = lines[len(lines)-maxLines:]
	}
	return strings.Join(lines, "\n")
}

func valueText(value any) string {
	switch typed := value.(type) {
	case nil:
		return ""
	case string:
		return truncateText(strings.TrimSpace(typed), 4000)
	default:
		data, err := json.Marshal(typed)
		if err != nil {
			return truncateText(strings.TrimSpace(fmt.Sprint(typed)), 4000)
		}
		return truncateText(string(data), 4000)
	}
}

func truncateText(text string, max int) string {
	if max <= 0 || len(text) <= max {
		return text
	}
	return text[:max]
}

func (s Session) applyTerminalAgentDecision(ctx context.Context, inbound channel.Inbound, agentResult agent.StepResult, effects []environment.EffectResult) InputResult {
	switch agentResult.Decision.Kind {
	case agent.DecisionMessage:
		if agentResult.Decision.Message == nil {
			return InputResult{Status: InputStatusOK, Agent: agentResult, Effect: lastEffect(effects), Effects: effects}
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
		return InputResult{Status: InputStatusOK, Agent: agentResult, Effect: lastEffect(effects), Effects: effects, Outbound: &outbound}
	case agent.DecisionComplete:
		if agentResult.Decision.Complete == nil {
			return InputResult{Status: InputStatusOK, Agent: agentResult, Effect: lastEffect(effects), Effects: effects}
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
		return InputResult{Status: InputStatusOK, Agent: agentResult, Effect: lastEffect(effects), Effects: effects, Outbound: &outbound}
	case agent.DecisionNone, agent.DecisionWait:
		return InputResult{Status: InputStatusOK, Agent: agentResult, Effect: lastEffect(effects), Effects: effects}
	default:
		return InputResult{Status: InputStatusUnsupported, Agent: agentResult, Effect: lastEffect(effects), Effects: effects, Error: &CommandError{Code: "unsupported_agent_decision", Message: "agent decision is not supported by session input dispatch yet", Details: map[string]any{"decision": agentResult.Decision.Kind}}}
	}
}

func (s Session) applyAgentOperations(ctx context.Context, agentCtx operation.Context, inbound channel.Inbound, startIndex int, requests []agent.OperationRequest) ([]environment.EffectResult, []coreconversation.Item, error) {
	effects := make([]environment.EffectResult, 0, len(requests))
	toolResults := make([]coreconversation.Item, 0, len(requests))
	provider := s.providerIdentity()
	for i, opReq := range requests {
		callID := operationCallID(inbound.ID, startIndex+i+1)
		requested := coresession.OperationRequested{
			RunID:     inbound.ID,
			CallID:    callID,
			Operation: opReq.Operation,
			Input:     opReq.Input,
		}
		if err := s.appendThreadEvents(ctx, requested); err != nil {
			return nil, nil, err
		}
		s.emitLive(requested)
		effect := s.applyOperation(agentCtx, opReq.Operation, opReq.Input, callID)
		if effect.Observation.Metadata == nil {
			effect.Observation.Metadata = map[string]any{}
		}
		effect.Observation.Metadata["operation"] = opReq.Operation.String()
		effect.Observation.Metadata["call_id"] = string(callID)
		if opReq.ProviderCallID != "" {
			effect.Observation.Metadata["provider_call_id"] = opReq.ProviderCallID
		}
		effect, replacementErr := replaceOversizedToolResult(agentCtx, effect, opReq.Operation, callID)
		if replacementErr != nil {
			effect = operationEffect(operation.Failed("tool_result_replacement_failed", replacementErr.Error(), map[string]any{
				"operation": opReq.Operation.String(),
				"call_id":   string(callID),
			}))
			effect.Observation.Metadata = map[string]any{
				"operation": opReq.Operation.String(),
				"call_id":   string(callID),
			}
		}
		effects = append(effects, effect)
		toolResult := operationResultTranscriptItem(provider, opReq, callID, effect.Result)
		if replacement, ok := toolResultReplacement(effect.Result); ok {
			if toolResult.Metadata == nil {
				toolResult.Metadata = map[string]string{}
			}
			toolResult.Metadata["replaced"] = "true"
			toolResult.Metadata["replacement"] = replacement.Kind
			toolResult.Metadata["replacement_path"] = replacement.Path
			toolResult.Metadata["replacement_size_bytes"] = fmt.Sprintf("%d", replacement.SizeBytes)
			toolResult.Metadata["replacement_threshold_bytes"] = fmt.Sprintf("%d", replacement.ThresholdBytes)
			if effect.Observation.Metadata == nil {
				effect.Observation.Metadata = map[string]any{}
			}
			effect.Observation.Metadata["replaced"] = true
			effect.Observation.Metadata["replacement"] = replacement
		}
		toolResults = append(toolResults, toolResult)
		completed := coresession.OperationCompleted{
			RunID:     inbound.ID,
			CallID:    callID,
			Operation: opReq.Operation,
			Result:    effect.Result,
		}
		if err := s.appendThreadEvents(ctx, completed); err != nil {
			return nil, nil, err
		}
		s.emitLive(completed)
	}
	return effects, toolResults, nil
}

func replaceOversizedToolResult(ctx operation.Context, effect environment.EffectResult, ref operation.Ref, callID operation.CallID) (environment.EffectResult, error) {
	result, replacement, err := operationruntime.ReplaceLargeResult(ctx, effect.Result, operationruntime.ReplacementOptions{
		Operation: ref,
		CallID:    callID,
	})
	if err != nil || replacement == nil {
		return effect, err
	}
	metadata := effect.Observation.Metadata
	effect = operationEffect(result)
	effect.Observation.Metadata = metadata
	return effect, nil
}

func toolResultReplacement(result operation.Result) (operationruntime.ResultReplacement, bool) {
	if rendered, ok := result.Output.(operation.Rendered); ok {
		if replacement, ok := rendered.Data.(operationruntime.ResultReplacement); ok && replacement.Replaced {
			return replacement, true
		}
	}
	if result.Error == nil || result.Error.Details == nil {
		return operationruntime.ResultReplacement{}, false
	}
	replacement, ok := result.Error.Details["replacement"].(operationruntime.ResultReplacement)
	return replacement, ok && replacement.Replaced
}

func (s Session) stepLimitResult(ctx context.Context, inbound channel.Inbound, agentResult agent.StepResult, effects []environment.EffectResult) InputResult {
	return s.operationBoundaryResult(ctx, inbound, agentResult, effects, innerStepLimitError(s.maxSteps()))
}

func innerStepLimitError(maxSteps int) *CommandError {
	if maxSteps <= 0 {
		maxSteps = 1
	}
	return &CommandError{
		Code:    "step_limit_exceeded",
		Message: fmt.Sprintf("inner loop reached turns.max_steps=%d model decision calls", maxSteps),
		Details: map[string]any{
			"loop":                 "inner",
			"limit":                "turns.max_steps",
			"max_steps":            maxSteps,
			"model_decision_calls": maxSteps,
		},
	}
}

func (s Session) operationBoundaryResult(ctx context.Context, inbound channel.Inbound, agentResult agent.StepResult, effects []environment.EffectResult, limitErr ...*CommandError) InputResult {
	effect := lastEffect(effects)
	var err *CommandError
	if len(limitErr) > 0 {
		err = limitErr[0]
	}
	if effect == nil {
		status := InputStatusOK
		if err != nil {
			status = InputStatusFailed
		}
		return InputResult{Status: status, Agent: agentResult, Error: err}
	}
	message := outboundMessageForOperationResult(effect.Result)
	if err := s.appendThreadEvents(ctx, coresession.OutboundProduced{RunID: inbound.ID, Message: message}); err != nil {
		return inputFailed("thread_append_failed", err.Error(), nil)
	}
	status := InputStatusOK
	if err != nil {
		status = InputStatusFailed
	}
	outbound := channel.Outbound{
		Channel:      inbound.Channel,
		Conversation: inbound.Conversation,
		Kind:         channel.OutboundMessage,
		Message:      &message,
	}
	return InputResult{
		Status:   status,
		Agent:    agentResult,
		Effect:   effect,
		Effects:  effects,
		Outbound: &outbound,
		Error:    err,
	}
}

func (s Session) maxSteps() int {
	if s.Agent == nil {
		return defaultLLMMaxSteps
	}
	spec := s.Agent.Spec()
	if spec.Turns.MaxSteps > 0 {
		return spec.Turns.MaxSteps
	}
	if sessioncontrol.IsLLMDriverKind(spec.Driver.Kind) {
		return defaultLLMMaxSteps
	}
	return 1
}

func (s Session) failOnStepLimit() bool {
	if s.Agent == nil {
		return true
	}
	spec := s.Agent.Spec()
	return sessioncontrol.IsLLMDriverKind(spec.Driver.Kind) || spec.Turns.MaxSteps > 0
}

func (s Session) failOnStepLimitForInput(opts inputExecutionOptions) bool {
	if s.Agent == nil {
		return true
	}
	spec := s.Agent.Spec()
	continuation := continuationPolicyForInput(opts, spec)
	if strings.TrimSpace(continuation.StopCondition.Type) != "" {
		return false
	}
	return s.failOnStepLimit()
}

func (s Session) maxContinuations() int {
	if s.Agent == nil {
		return 0
	}
	spec := s.Agent.Spec()
	if spec.Turns.Continuation.MaxContinuations > 0 {
		return spec.Turns.Continuation.MaxContinuations
	}
	if sessioncontrol.IsLLMDriverKind(spec.Driver.Kind) {
		return defaultLLMContinuations
	}
	return 0
}

func (s Session) maxContinuationsForPolicy(policy agent.ContinuationPolicy) int {
	if policy.MaxContinuations > 0 {
		return policy.MaxContinuations
	}
	return s.maxContinuations()
}

type continuationDecision struct {
	Continue    bool
	Instruction string
	Reason      string
	Result      InputResult
}

func (s Session) evaluateContinuation(ctx context.Context, inbound channel.Inbound, opts inputExecutionOptions, completed int, agentResult agent.StepResult, effects []environment.EffectResult) continuationDecision {
	if agentResult.Status != agent.StatusOK || s.Agent == nil {
		return continuationDecision{}
	}
	spec := s.Agent.Spec()
	continuation := continuationPolicyForInput(opts, spec)
	condition := continuation.StopCondition
	if strings.TrimSpace(condition.Type) == "" {
		return continuationDecision{}
	}
	evalSpec := spec
	evalSpec.Turns.Continuation = continuation
	maxContinuations := s.maxContinuationsForPolicy(continuation)
	evaluation, err := s.evaluateStopCondition(ctx, StopEvaluationInput{
		Agent:            evalSpec,
		Condition:        condition,
		Inbound:          inbound,
		AgentResult:      agentResult,
		Effects:          effects,
		Completed:        completed,
		MaxContinuations: maxContinuations,
	})
	if err != nil {
		return continuationDecision{Result: InputResult{Status: InputStatusFailed, Agent: agentResult, Effect: lastEffect(effects), Effects: effects, Error: &CommandError{Code: "stop_condition_failed", Message: err.Error()}}}
	}
	action := StopAction(strings.TrimSpace(strings.ToLower(string(evaluation.Action))))
	if action != StopActionContinue {
		return continuationDecision{Reason: evaluation.Reason}
	}
	if completed >= maxContinuations {
		return continuationDecision{Result: continuationLimitResult(agentResult, effects, maxContinuations)}
	}
	return continuationDecision{Continue: true, Instruction: evaluation.ContinueInstruction, Reason: evaluation.Reason}
}

func (s Session) evaluateStopCondition(ctx context.Context, input StopEvaluationInput) (StopEvaluation, error) {
	return sessioncontrol.EvaluateStopCondition(ctx, input.Condition, input, s.StopEvaluator)
}

func continuationLimitResult(agentResult agent.StepResult, effects []environment.EffectResult, max int) InputResult {
	return InputResult{Status: InputStatusFailed, Agent: agentResult, Effect: lastEffect(effects), Effects: effects, Error: &CommandError{
		Code:    "continuation_limit_exceeded",
		Message: fmt.Sprintf("outer continuation reached turns.continuation.max_continuations=%d", max),
		Details: map[string]any{
			"loop":              "outer",
			"limit":             "turns.continuation.max_continuations",
			"max_continuations": max,
		},
	}}
}

func observationsForEffects(effects []environment.EffectResult) []environment.Observation {
	observations := make([]environment.Observation, 0, len(effects))
	for _, effect := range effects {
		obs := effect.Observation
		if obs.ID == "" && obs.Kind == "" {
			obs = environment.Observation{
				Source:  "operation",
				Kind:    "operation.result",
				Content: effect.Result,
			}
		}
		observations = append(observations, obs)
	}
	return observations
}

func lastEffect(effects []environment.EffectResult) *environment.EffectResult {
	if len(effects) == 0 {
		return nil
	}
	return &effects[len(effects)-1]
}

func stateRefIsZero(r agent.StateRef) bool {
	return r.Kind == "" && r.URI == "" && r.Digest == ""
}

// ExecuteInboundCommand dispatches a channel command envelope.
func (s Session) ExecuteInboundCommand(ctx context.Context, inbound channel.Inbound) CommandResult {
	if err := inbound.Validate(); err != nil {
		return commandFailed("invalid_command_inbound", err.Error(), nil)
	}
	if inbound.Kind != channel.InboundCommand || inbound.Command == nil {
		return commandFailed("invalid_command_inbound", "inbound envelope does not contain a command", nil)
	}
	resolved, ok, err := s.resolveCommand(inbound.Command.Path)
	if err != nil {
		return commandFailed("command_resolution_failed", err.Error(), map[string]any{
			"path": inbound.Command.Path.String(),
		})
	}
	if s.Profile.Commands != nil && resolved.SessionHandler == nil && !commandPathAllowed(s.Profile.Commands, inbound.Command.Path) {
		return commandFailed("command_not_found", fmt.Sprintf("command %s not found", inbound.Command.Path), map[string]any{
			"path": inbound.Command.Path.String(),
		})
	}
	if err := s.appendThreadEvents(ctx, coresession.CommandReceived{
		RunID:        inbound.ID,
		Command:      *inbound.Command,
		Channel:      inbound.Channel,
		Conversation: inbound.Conversation,
		Caller:       inbound.Caller,
		Trust:        inbound.Trust,
	}); err != nil {
		return commandFailed("thread_append_failed", err.Error(), nil)
	}
	if !ok {
		if s.Commands == nil && len(s.CommandCatalog) == 0 {
			return commandFailed("command_registry_missing", "command registry is nil", nil)
		}
		return commandFailed("command_not_found", fmt.Sprintf("command %s not found", inbound.Command.Path), map[string]any{
			"path": inbound.Command.Path.String(),
		})
	}
	spec := resolved.Binding.Spec
	if spec.Path.String() == "" {
		spec.Path = inbound.Command.Path
	}
	evaluation := sessioncontrol.EvaluateInvocation(spec, inbound.Caller, inbound.Trust)
	switch {
	case sessioncontrol.PolicyDenied(evaluation):
		_ = s.appendThreadEvents(ctx, coresession.CommandRejected{RunID: inbound.ID, Command: *inbound.Command, Reason: evaluation.Reason})
		return CommandResult{Status: CommandStatusRejected, Spec: spec, Policy: evaluation}
	case sessioncontrol.PolicyApprovalRequired(evaluation):
		_ = s.appendThreadEvents(ctx, coresession.CommandRejected{RunID: inbound.ID, Command: *inbound.Command, Reason: evaluation.Reason})
		return CommandResult{Status: CommandStatusApprovalRequired, Spec: spec, Policy: evaluation}
	}

	if sessioncontrol.TargetsSession(spec) {
		if resolved.SessionHandler == nil {
			return CommandResult{
				Status: CommandStatusUnsupported,
				Spec:   spec,
				Policy: evaluation,
				Error: &CommandError{
					Code:    "session_command_handler_missing",
					Message: "session command has no registered handler",
					Details: map[string]any{
						"path": spec.Path.String(),
					},
				},
			}
		}
		return resolved.SessionHandler(s, ctx, inbound, spec, evaluation)
	}

	if spec.Target.Kind == invocation.TargetWorkflow {
		return s.executeWorkflowCommand(ctx, inbound, resolved.Binding, spec, evaluation)
	}
	if spec.Target.Kind == invocation.TargetPrompt {
		return s.executePromptCommand(ctx, inbound, spec, evaluation)
	}

	if !sessioncontrol.TargetsOperation(spec) {
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

	if s.Operations == nil && len(s.OperationCatalog) == 0 {
		return commandFailed("command_registry_missing", "command registry is nil", nil)
	}

	opCtx := operation.NewContext(ensureContext(ctx), s.eventSink())
	callID := operationCallID(inbound.ID, 1)
	requested := coresession.OperationRequested{
		RunID:     inbound.ID,
		CallID:    callID,
		Operation: spec.Target.Operation,
		Input:     inbound.Command.Input,
	}
	if err := s.appendThreadEvents(ctx, requested); err != nil {
		return commandFailed("thread_append_failed", err.Error(), nil)
	}
	s.emitLive(requested)
	effect := s.applyBoundOperation(opCtx, resolved.Binding, spec.Target.Operation, inbound.Command.Input, callID)
	completed := coresession.OperationCompleted{
		RunID:     inbound.ID,
		CallID:    callID,
		Operation: spec.Target.Operation,
		Result:    effect.Result,
	}
	if err := s.appendThreadEvents(ctx, completed, coresession.OutboundProduced{
		RunID:   inbound.ID,
		Message: outboundMessageForOperationResult(effect.Result),
	}); err != nil {
		return commandFailed("thread_append_failed", err.Error(), nil)
	}
	s.emitLive(completed)
	status := CommandStatusOK
	if effect.Result.IsError() {
		status = CommandStatusFailed
	}
	return CommandResult{Status: status, Spec: spec, Policy: evaluation, Effect: &effect}
}

func (s Session) executePromptCommand(ctx context.Context, inbound channel.Inbound, spec command.Spec, evaluation sessioncontrol.PolicyEvaluation) CommandResult {
	content, err := renderPromptCommand(spec, inbound.Command)
	if err != nil {
		return commandFailed("prompt_command_render_failed", err.Error(), map[string]any{
			"path": spec.Path.String(),
		})
	}
	messageInbound := inbound
	messageInbound.Kind = channel.InboundMessage
	messageInbound.Command = nil
	messageInbound.Message = &channel.Message{
		Content: content,
		Metadata: map[string]any{
			"command": spec.Path.String(),
			"target":  string(invocation.TargetPrompt),
		},
	}
	inputResult := s.executeInboundInput(ctx, messageInbound, inputExecutionOptions{})
	result := CommandResult{
		Status: commandStatusForInput(inputResult),
		Spec:   spec,
		Policy: evaluation,
		Output: promptCommandOutput(inputResult),
	}
	if inputResult.Error != nil {
		result.Error = inputResult.Error
	}
	return result
}

func (s Session) executeWorkflowCommand(ctx context.Context, inbound channel.Inbound, binding CommandBinding, spec command.Spec, evaluation sessioncontrol.PolicyEvaluation) CommandResult {
	workflowBinding, err := s.resolveWorkflowBinding(binding, spec)
	if err != nil {
		return commandFailed("workflow_resolution_failed", err.Error(), map[string]any{
			"workflow": string(spec.Target.Workflow),
		})
	}
	result := workflowruntime.Run(ctx, workflowruntime.Config{
		Spec:   workflowBinding.Spec,
		RunID:  coreworkflow.RunID(inbound.ID),
		Input:  firstNonNil(inbound.Command.Input, spec.Target.Input),
		Events: s.eventSink(),
		RunOperation: func(ctx context.Context, step coreworkflow.Step, input operation.Value, callID operation.CallID) (operation.Result, error) {
			return s.runWorkflowOperationStep(ctx, inbound.ID, workflowBinding.ID, step, input, callID)
		},
		RunAgent: func(ctx context.Context, step coreworkflow.Step, input operation.Value) (operation.Value, error) {
			return s.runWorkflowAgentStep(ctx, inbound.ID, step, input)
		},
	})
	if result.Status == coreworkflow.StatusSucceeded {
		commandResult := CommandResult{Status: CommandStatusOK, Spec: spec, Policy: evaluation, Output: result.Output}
		if err := s.appendThreadEvents(ctx, coresession.OutboundProduced{
			RunID:   inbound.ID,
			Message: workflowCommandMessage(commandResult),
		}); err != nil {
			return commandFailed("thread_append_failed", err.Error(), nil)
		}
		return commandResult
	}
	message := "workflow failed"
	code := "workflow_failed"
	if result.Status == coreworkflow.StatusCanceled {
		message = "workflow canceled"
		code = "workflow_canceled"
	}
	if result.Error != nil {
		message = result.Error.Message
		if result.Error.Code != "" {
			code = result.Error.Code
		}
	}
	commandResult := CommandResult{
		Status: CommandStatusFailed,
		Spec:   spec,
		Policy: evaluation,
		Output: result.Output,
		Error: &CommandError{
			Code:    code,
			Message: message,
			Details: map[string]any{
				"workflow": string(workflowBinding.Spec.Name),
			},
		},
	}
	if err := s.appendThreadEvents(ctx, coresession.OutboundProduced{
		RunID:   inbound.ID,
		Message: workflowCommandMessage(commandResult),
	}); err != nil {
		return commandFailed("thread_append_failed", err.Error(), nil)
	}
	return commandResult
}

func (s Session) resolveWorkflowBinding(binding CommandBinding, spec command.Spec) (resourcecatalog.Binding[coreworkflow.Spec], error) {
	if !binding.TargetID.IsZero() {
		if workflowBinding, ok := s.WorkflowCatalog[binding.TargetID.Address()]; ok {
			return workflowBinding, nil
		}
		return resourcecatalog.Binding[coreworkflow.Spec]{}, fmt.Errorf("workflow %q is not bound", binding.TargetID.Address())
	}
	if s.Resolver == nil {
		return resourcecatalog.Binding[coreworkflow.Spec]{}, fmt.Errorf("workflow resolver is nil")
	}
	id, err := s.Resolver.Resolve("workflow", string(spec.Target.Workflow))
	if err != nil {
		return resourcecatalog.Binding[coreworkflow.Spec]{}, err
	}
	workflowBinding, ok := s.WorkflowCatalog[id.Address()]
	if !ok {
		return resourcecatalog.Binding[coreworkflow.Spec]{}, fmt.Errorf("workflow %q is not bound", id.Address())
	}
	return workflowBinding, nil
}

func (s Session) runWorkflowOperationStep(ctx context.Context, runID string, workflowID sessioncontrol.ResourceID, step coreworkflow.Step, input operation.Value, callID operation.CallID) (operation.Result, error) {
	binding, err := s.OperationCatalog.Resolve(step.Operation.String(), workflowID)
	if err != nil {
		return operation.Result{}, err
	}
	requested := coresession.OperationRequested{
		RunID:     runID,
		CallID:    callID,
		Operation: step.Operation,
		Input:     input,
	}
	if err := s.appendThreadEvents(ctx, requested); err != nil {
		return operation.Result{}, err
	}
	s.emitLive(requested)
	effect := s.executeOperation(operation.NewContext(ensureContext(ctx), s.eventSink()), binding.Operation, input, callID)
	completed := coresession.OperationCompleted{
		RunID:     runID,
		CallID:    callID,
		Operation: step.Operation,
		Result:    effect.Result,
	}
	if err := s.appendThreadEvents(ctx, completed); err != nil {
		return operation.Result{}, err
	}
	s.emitLive(completed)
	return effect.Result, nil
}

func (s Session) runWorkflowAgentStep(ctx context.Context, runID string, step coreworkflow.Step, input operation.Value) (operation.Value, error) {
	if s.Subagents == nil {
		return nil, fmt.Errorf("workflow agent step %q requires a sub-agent supervisor", step.ID)
	}
	task := workflowAgentTask(step, input)
	prepared, err := s.Subagents.Prepare(ctx, subagent.SpawnRequest{
		ID:             subagent.ID(string(runID) + ":" + string(step.ID)),
		Agent:          step.Agent,
		Task:           task,
		TaskID:         string(step.ID),
		Policy:         s.Delegation,
		ParentThreadID: s.Thread.ID,
		ParentRunID:    runID,
		ParentCallID:   operation.CallID(string(runID) + ":workflow:" + string(step.ID)),
		Events:         s.eventSink(),
		Approver:       sessionenv.ApproverFromExecutor(s.OperationExecutor),
	})
	if err != nil {
		return nil, err
	}
	prepared.Start()
	result, err := s.Subagents.Wait(ctx, prepared.Handle.ID)
	if err != nil {
		return nil, err
	}
	if result.Error != "" {
		return nil, fmt.Errorf("%s", result.Error)
	}
	return result.Output, nil
}

func workflowAgentTask(step coreworkflow.Step, input operation.Value) string {
	if text, ok := input.(string); ok && strings.TrimSpace(text) != "" {
		return strings.TrimSpace(text)
	}
	if step.ID != "" {
		return "Run workflow step " + string(step.ID)
	}
	return "Run workflow step"
}

func workflowCommandMessage(result CommandResult) channel.Message {
	switch {
	case result.Output != nil:
		return channel.Message{Content: result.Output}
	case result.Error != nil:
		return channel.Message{Content: result.Error.Message}
	default:
		return channel.Message{Content: string(result.Status)}
	}
}

func firstNonNil(values ...any) any {
	for _, value := range values {
		if value != nil {
			return value
		}
	}
	return nil
}

type promptCommandTemplateData struct {
	Path     string
	Args     []string
	Argument string
	Input    any
}

func renderPromptCommand(spec command.Spec, invocation *command.Invocation) (string, error) {
	prompt := strings.TrimSpace(spec.Target.Prompt)
	if prompt == "" {
		return "", fmt.Errorf("prompt command target is empty")
	}
	if invocation == nil {
		return prompt, nil
	}
	data := promptCommandTemplateData{
		Path:     spec.Path.String(),
		Args:     append([]string(nil), invocation.Args...),
		Argument: strings.Join(invocation.Args, " "),
		Input:    invocation.Input,
	}
	if strings.Contains(prompt, "{{") {
		tmpl, err := template.New("prompt-command").Option("missingkey=error").Parse(prompt)
		if err != nil {
			return "", fmt.Errorf("parse prompt command template: %w", err)
		}
		var rendered bytes.Buffer
		if err := tmpl.Execute(&rendered, data); err != nil {
			return "", fmt.Errorf("render prompt command template: %w", err)
		}
		return strings.TrimSpace(rendered.String()), nil
	}
	if len(data.Args) == 0 && data.Input == nil {
		return prompt, nil
	}
	var b strings.Builder
	b.WriteString(prompt)
	if len(data.Args) > 0 {
		b.WriteString("\n\nArguments:\n")
		b.WriteString(data.Argument)
	}
	if data.Input != nil {
		b.WriteString("\n\nInput:\n")
		b.WriteString(promptCommandInputString(data.Input))
	}
	return strings.TrimSpace(b.String()), nil
}

func promptCommandInputString(input any) string {
	if text, ok := input.(string); ok {
		return text
	}
	data, err := json.MarshalIndent(input, "", "  ")
	if err != nil {
		return fmt.Sprint(input)
	}
	return string(data)
}

func commandStatusForInput(result InputResult) CommandStatus {
	switch result.Status {
	case InputStatusOK:
		return CommandStatusOK
	case InputStatusUnsupported:
		return CommandStatusUnsupported
	default:
		return CommandStatusFailed
	}
}

func promptCommandOutput(result InputResult) any {
	if result.Outbound != nil && result.Outbound.Message != nil {
		return result.Outbound.Message.Content
	}
	if result.Agent.Decision.Message != nil {
		return result.Agent.Decision.Message.Content
	}
	return nil
}

func (s Session) executeContextCommand(ctx context.Context, inbound channel.Inbound, spec command.Spec, evaluation sessioncontrol.PolicyEvaluation) CommandResult {
	input, err := parseContextPreviewCommand(*inbound.Command)
	if err != nil {
		return CommandResult{
			Status: CommandStatusFailed,
			Spec:   spec,
			Policy: evaluation,
			Error:  &CommandError{Code: "invalid_context_command_input", Message: err.Error()},
		}
	}
	preview, err := s.previewContext(ctx, input)
	if err != nil {
		details := map[string]any{}
		if len(preview.Providers) > 0 {
			details["providers"] = preview.Providers
		}
		code := "context_preview_failed"
		if errors.Is(err, errContextProviderNotFound) {
			code = "context_provider_not_found"
		}
		return CommandResult{
			Status: CommandStatusFailed,
			Spec:   spec,
			Policy: evaluation,
			Error:  &CommandError{Code: code, Message: err.Error(), Details: details},
		}
	}
	text := renderContextPreview(preview)
	if err := s.appendThreadEvents(ctx, coresession.OutboundProduced{
		RunID:   inbound.ID,
		Message: channel.Message{Content: text},
	}); err != nil {
		return commandFailed("thread_append_failed", err.Error(), nil)
	}
	return CommandResult{
		Status: CommandStatusOK,
		Spec:   spec,
		Policy: evaluation,
		Effect: &environment.EffectResult{Result: operation.OK(text)},
	}
}

func (s Session) executeGoalCommand(ctx context.Context, inbound channel.Inbound, spec command.Spec, evaluation sessioncontrol.PolicyEvaluation) CommandResult {
	input, err := parseGoalCommandInput(*inbound.Command)
	if err != nil {
		return CommandResult{
			Status: CommandStatusFailed,
			Spec:   spec,
			Policy: evaluation,
			Error:  &CommandError{Code: "invalid_goal_command_input", Message: err.Error()},
		}
	}
	messageInbound := inbound
	messageInbound.Kind = channel.InboundMessage
	messageInbound.Command = nil
	messageInbound.Message = &channel.Message{Content: input.Goal}
	result := s.executeInboundInput(ctx, messageInbound, input)
	status := CommandStatusOK
	if result.Status != InputStatusOK {
		status = CommandStatusFailed
	}
	var output any
	if result.Outbound != nil && result.Outbound.Message != nil {
		output = result.Outbound.Message.Content
	}
	return CommandResult{
		Status: status,
		Spec:   spec,
		Policy: evaluation,
		Output: output,
		Error:  result.Error,
	}
}

func parseGoalCommandInput(inv command.Invocation) (inputExecutionOptions, error) {
	input, err := command.Bind[goalCommandInput](inv)
	if err != nil {
		return inputExecutionOptions{}, err
	}
	if len(inv.Args) == 0 && inv.Input != nil {
		structured := structuredGoalCommandInput(inv.Input)
		input = mergeGoalCommandInput(input, structured)
	}
	return validateGoalCommandInput(input)
}

func structuredGoalCommandInput(value any) goalCommandInput {
	values, ok := value.(map[string]any)
	if !ok {
		return goalCommandInput{}
	}
	var input goalCommandInput
	switch goal := values["goal"].(type) {
	case string:
		input.Goal = []string{goal}
	case []string:
		input.Goal = append([]string(nil), goal...)
	case []any:
		for _, item := range goal {
			input.Goal = append(input.Goal, fmt.Sprint(item))
		}
	}
	input.Max = intPointerValue(values, "max")
	input.MaxContinuations = intPointerValue(values, "max_continuations")
	input.MaxContinuationsAlt = intPointerValue(values, "max-continuations")
	return input
}

func intPointerValue(values map[string]any, key string) *int {
	value, ok := values[key]
	if !ok {
		return nil
	}
	parsed := intValue(value)
	return &parsed
}

func intValue(value any) int {
	switch typed := value.(type) {
	case int:
		return typed
	case int64:
		return int(typed)
	case float64:
		return int(typed)
	case string:
		parsed, _ := strconv.Atoi(strings.TrimSpace(typed))
		return parsed
	default:
		return 0
	}
}

func mergeGoalCommandInput(primary, fallback goalCommandInput) goalCommandInput {
	if len(primary.Goal) == 0 {
		primary.Goal = fallback.Goal
	}
	if primary.Max == nil {
		primary.Max = fallback.Max
	}
	if primary.MaxContinuations == nil {
		primary.MaxContinuations = fallback.MaxContinuations
	}
	if primary.MaxContinuationsAlt == nil {
		primary.MaxContinuationsAlt = fallback.MaxContinuationsAlt
	}
	return primary
}

func validateGoalCommandInput(input goalCommandInput) (inputExecutionOptions, error) {
	goal := strings.TrimSpace(strings.Join(input.Goal, " "))
	if goal == "" {
		return inputExecutionOptions{}, fmt.Errorf("goal is required; use /goal \"your goal\"")
	}
	max := 0
	if input.Max != nil {
		max = *input.Max
	} else if input.MaxContinuations != nil {
		max = *input.MaxContinuations
	} else if input.MaxContinuationsAlt != nil {
		max = *input.MaxContinuationsAlt
	} else if input.DefaultMax != nil {
		max = *input.DefaultMax
	} else {
		max = defaultGoalContinuations
	}
	if max <= 0 {
		return inputExecutionOptions{}, fmt.Errorf("max-continuations must be > 0")
	}
	return inputExecutionOptions{Goal: goal, MaxContinuations: max}, nil
}

func parseContextPreviewCommand(inv command.Invocation) (contextPreviewInput, error) {
	input, err := command.Bind[contextPreviewInput](inv)
	if err != nil {
		return contextPreviewInput{}, err
	}
	if inv.Input != nil {
		structured, err := decodeCommandInput[contextPreviewInput](inv.Input)
		if err != nil {
			return contextPreviewInput{}, err
		}
		input = mergeContextPreviewInput(input, structured)
	}
	input.Key = strings.TrimSpace(input.Key)
	return input, nil
}

func mergeContextPreviewInput(primary, fallback contextPreviewInput) contextPreviewInput {
	if !primary.Fresh {
		primary.Fresh = fallback.Fresh
	}
	if primary.Key == "" {
		primary.Key = fallback.Key
	}
	return primary
}

func renderContextPreview(preview contextPreviewData) string {
	if preview.Mode == "" {
		preview.Mode = "diff"
	}
	var b strings.Builder
	b.WriteString("Context preview")
	if preview.Key != "" {
		b.WriteString(" for ")
		b.WriteString(preview.Key)
	}
	b.WriteString(" (")
	b.WriteString(preview.Mode)
	b.WriteString(")\n\n")
	wrote := false
	for _, section := range []struct {
		title string
		text  string
	}{
		{title: "system", text: preview.System},
		{title: "developer", text: preview.Developer},
		{title: "user", text: preview.User},
	} {
		if strings.TrimSpace(section.text) == "" {
			continue
		}
		wrote = true
		b.WriteString("## ")
		b.WriteString(section.title)
		b.WriteString("\n\n```xml\n")
		b.WriteString(strings.TrimSpace(section.text))
		b.WriteString("\n```\n\n")
	}
	if !wrote {
		if len(preview.Providers) == 0 {
			b.WriteString("No context providers are configured.\n")
		} else {
			b.WriteString("No context changes would be sent.\n")
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

type compactCommandInput struct {
	DryRun bool `json:"dry_run,omitempty" command:"flag=dry-run"`
}

type compactReport struct {
	Provider coreconversation.ProviderIdentity
	Mode     coreconversation.ProjectionMode
	Stats    coreconversation.CompactionStats
}

type compactBudget struct {
	ContextTokens  int
	InputBudget    int
	TriggerTokens  int
	OutputReserve  int
	TriggerPercent int
}

func (s Session) executeCompactCommand(ctx context.Context, inbound channel.Inbound, spec command.Spec, evaluation sessioncontrol.PolicyEvaluation) CommandResult {
	input, err := parseCompactCommand(*inbound.Command)
	if err != nil {
		return CommandResult{
			Status: CommandStatusFailed,
			Spec:   spec,
			Policy: evaluation,
			Error:  &CommandError{Code: "invalid_compact_command_input", Message: err.Error()},
		}
	}
	transcript, err := s.compactableTranscript(ctx)
	if err != nil {
		code := "compact_projection_failed"
		if errors.Is(err, errCompactUnavailable) {
			code = "compact_unavailable"
		}
		return CommandResult{
			Status: CommandStatusFailed,
			Spec:   spec,
			Policy: evaluation,
			Error:  &CommandError{Code: code, Message: err.Error()},
		}
	}
	result := conversationruntime.CompactTranscript(transcript, s.compactOptions())
	stats := coreconversation.CompactionStats{
		OriginalItems:     len(transcript.Items),
		CompactedItems:    len(result.Transcript.Items),
		OriginalTokens:    result.OriginalTokens,
		CompactedTokens:   result.CompactedTokens,
		OmittedItems:      result.OmittedItems,
		SummarizedItems:   result.SummarizedItems,
		Compacted:         result.Compacted,
		CheckpointPersist: result.Compacted && !input.DryRun,
	}
	report := compactReport{Provider: transcript.Provider, Mode: transcript.Mode, Stats: stats}
	if stats.CheckpointPersist {
		err := retryThreadAppend(ctx, func(appendCtx context.Context) error {
			return conversationruntime.AppendCompaction(appendCtx, s.ThreadStore, s.Thread, inbound.ID, transcript.Provider, result.Transcript.Items, stats)
		})
		if err != nil {
			return CommandResult{
				Status: CommandStatusFailed,
				Spec:   spec,
				Policy: evaluation,
				Error:  &CommandError{Code: "compact_append_failed", Message: err.Error()},
			}
		}
	}
	text := renderCompactReport(report, input.DryRun)
	if err := s.appendThreadEvents(ctx, coresession.OutboundProduced{
		RunID:   inbound.ID,
		Message: channel.Message{Content: text},
	}); err != nil {
		return commandFailed("thread_append_failed", err.Error(), nil)
	}
	return CommandResult{
		Status: CommandStatusOK,
		Spec:   spec,
		Policy: evaluation,
		Effect: &environment.EffectResult{Result: operation.OK(text)},
	}
}

func (s Session) autoCompactAfterTurn(ctx context.Context, turnID string) error {
	if s.ThreadStore == nil || s.Thread.ID == "" {
		return nil
	}
	transcript, err := s.compactableTranscript(ctx)
	if err != nil {
		return err
	}
	_, budget := s.compactOptionsAndBudget()
	if budget.TriggerTokens <= 0 {
		return nil
	}
	if conversationruntime.EstimateItemsTokens(transcript.Items) <= budget.TriggerTokens {
		return nil
	}
	result := conversationruntime.CompactTranscript(transcript, s.compactOptions())
	if !result.Compacted {
		return nil
	}
	stats := coreconversation.CompactionStats{
		OriginalItems:     len(transcript.Items),
		CompactedItems:    len(result.Transcript.Items),
		OriginalTokens:    result.OriginalTokens,
		CompactedTokens:   result.CompactedTokens,
		OmittedItems:      result.OmittedItems,
		SummarizedItems:   result.SummarizedItems,
		Compacted:         result.Compacted,
		CheckpointPersist: true,
	}
	return retryThreadAppend(ctx, func(appendCtx context.Context) error {
		return conversationruntime.AppendCompaction(appendCtx, s.ThreadStore, s.Thread, turnID, transcript.Provider, result.Transcript.Items, stats)
	})
}

func (s Session) compactOptions() conversationruntime.CompactOptions {
	opts, _ := s.compactOptionsAndBudget()
	return opts
}

func (s Session) compactOptionsAndBudget() (conversationruntime.CompactOptions, compactBudget) {
	contextTokens := s.modelContextTokens()
	outputReserve := s.outputReserveTokens()
	inputBudget := contextTokens - outputReserve
	if inputBudget < compactLargeItemTokens {
		inputBudget = compactLargeItemTokens
	}
	triggerTokens := int(float64(inputBudget) * compactTriggerRatio)
	if triggerTokens < compactLargeItemTokens {
		triggerTokens = compactLargeItemTokens
	}
	return conversationruntime.CompactOptions{
			MaxInputTokens:      triggerTokens + compactSafetyMarginTokens,
			SafetyMarginTokens:  compactSafetyMarginTokens,
			LargeItemTokens:     compactLargeItemTokens,
			PreserveRecentItems: compactPreserveRecentItems,
		}, compactBudget{
			ContextTokens:  contextTokens,
			InputBudget:    inputBudget,
			TriggerTokens:  triggerTokens,
			OutputReserve:  outputReserve,
			TriggerPercent: int(compactTriggerRatio * 100),
		}
}

func (s Session) modelContextTokens() int {
	contextTokens := defaultCompactContextTokens
	if s.Agent != nil {
		spec := s.Agent.Spec()
		if value := intAnnotation(spec.Inference.Annotations, "llm.context_tokens"); value > 0 {
			contextTokens = value
		}
	}
	if contextTokens > maxCompactContextTokens {
		return maxCompactContextTokens
	}
	return contextTokens
}

func (s Session) outputReserveTokens() int {
	return sessioncontrol.OutputReserveTokens(s.Agent, compactSafetyMarginTokens)
}

func (s Session) compactableTranscript(ctx context.Context) (coreconversation.Transcript, error) {
	if s.ThreadStore == nil || s.Thread.ID == "" {
		return coreconversation.Transcript{}, errCompactUnavailable
	}
	snapshot, err := s.ThreadStore.Read(ensureContext(ctx), corethread.ReadParams{ID: s.Thread.ID})
	if err != nil {
		return coreconversation.Transcript{}, err
	}
	provider := s.providerIdentity()
	projected, err := conversationruntime.Project(conversationruntime.ProjectionInput{
		Thread:     snapshot,
		BranchID:   s.Thread.BranchID,
		Provider:   provider,
		Mode:       coreconversation.ProjectionFullReplay,
		AllowEmpty: true,
	})
	if err != nil {
		return coreconversation.Transcript{}, err
	}
	return projected.Transcript(provider), nil
}

func parseCompactCommand(inv command.Invocation) (compactCommandInput, error) {
	input, err := command.Bind[compactCommandInput](inv)
	if err != nil {
		return compactCommandInput{}, err
	}
	if inv.Input != nil {
		structured, err := decodeCommandInput[compactCommandInput](inv.Input)
		if err != nil {
			return compactCommandInput{}, err
		}
		if !input.DryRun {
			input.DryRun = structured.DryRun
		}
	}
	return input, nil
}

func decodeCommandInput[T any](value any) (T, error) {
	var out T
	switch typed := value.(type) {
	case T:
		return typed, nil
	case *T:
		if typed == nil {
			return out, nil
		}
		return *typed, nil
	}
	data, err := json.Marshal(value)
	if err != nil {
		return out, err
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return out, err
	}
	return out, nil
}

func intAnnotation(values map[string]string, key string) int {
	if len(values) == 0 {
		return 0
	}
	value, err := strconv.Atoi(strings.TrimSpace(values[key]))
	if err != nil || value <= 0 {
		return 0
	}
	return value
}

func renderCompactReport(report compactReport, dryRun bool) string {
	title := "Compaction not needed"
	if dryRun {
		title = "Compaction dry run"
	} else if report.Stats.CheckpointPersist {
		title = "Compaction complete"
	}
	var b strings.Builder
	b.WriteString(title)
	b.WriteString("\n\n")
	b.WriteString("Provider: ")
	b.WriteString(providerIdentityLabel(report.Provider))
	b.WriteString("\n")
	b.WriteString("Replay: ")
	if report.Mode == "" {
		b.WriteString(string(coreconversation.ProjectionFullReplay))
	} else {
		b.WriteString(string(report.Mode))
	}
	b.WriteString("\n")
	b.WriteString("Items: ")
	b.WriteString(formatInt(report.Stats.OriginalItems))
	b.WriteString(" -> ")
	b.WriteString(formatInt(report.Stats.CompactedItems))
	b.WriteString("\n")
	b.WriteString("Estimated tokens: ")
	b.WriteString(formatInt(report.Stats.OriginalTokens))
	b.WriteString(" -> ")
	b.WriteString(formatInt(report.Stats.CompactedTokens))
	b.WriteString("\n")
	b.WriteString("Omitted items: ")
	b.WriteString(formatInt(report.Stats.OmittedItems))
	b.WriteString("\n")
	b.WriteString("Summarized large items: ")
	b.WriteString(formatInt(report.Stats.SummarizedItems))
	b.WriteString("\n")
	b.WriteString("Checkpoint: ")
	switch {
	case dryRun && report.Stats.Compacted:
		b.WriteString("would be persisted by /compact")
	case dryRun:
		b.WriteString("would not be persisted")
	case report.Stats.CheckpointPersist:
		b.WriteString("persisted")
	default:
		b.WriteString("not persisted")
	}
	return b.String()
}

func providerIdentityLabel(provider coreconversation.ProviderIdentity) string {
	left := strings.TrimSpace(provider.Provider)
	right := strings.TrimSpace(provider.Model)
	switch {
	case left != "" && right != "":
		return left + " / " + right
	case left != "":
		return left
	case right != "":
		return right
	default:
		return "unknown"
	}
}

func formatInt(value int) string {
	text := fmt.Sprintf("%d", value)
	if len(text) <= 3 {
		return text
	}
	var parts []string
	for len(text) > 3 {
		parts = append([]string{text[len(text)-3:]}, parts...)
		text = text[:len(text)-3]
	}
	parts = append([]string{text}, parts...)
	return strings.Join(parts, ",")
}

func commandPathAllowed(allowed []command.Path, path command.Path) bool {
	key := path.String()
	if key == "" {
		return false
	}
	for _, candidate := range allowed {
		if candidate.String() == key {
			return true
		}
	}
	return false
}

// CommandTargetsSession reports whether a command path needs session-specific
// agent/runtime wiring before dispatch.
func CommandTargetsSession(path command.Path, resolver *sessioncontrol.Resolver, catalog CommandCatalog, registry *command.Registry) (bool, error) {
	resolved, ok, err := resolveCommand(path, resolver, catalog, registry)
	if err != nil || !ok {
		return false, err
	}
	spec := resolved.Binding.Spec
	return sessioncontrol.TargetsSession(spec) || spec.Target.Kind == invocation.TargetPrompt, nil
}

func (s Session) resolveCommand(path command.Path) (resolvedCommand, bool, error) {
	return resolveCommand(path, s.Resolver, s.CommandCatalog, s.Commands)
}

func resolveCommand(path command.Path, resolver *sessioncontrol.Resolver, catalog CommandCatalog, registry *command.Registry) (resolvedCommand, bool, error) {
	if sessionCommand, ok := builtInSessionCommands[path.String()]; ok {
		return resolvedCommand{
			Binding: CommandBinding{
				Spec: sessionCommand.Spec,
			},
			SessionHandler: sessionCommand.Handler,
		}, true, nil
	}
	if binding, ok, err := resolveCommandBinding(path, resolver, catalog); err != nil || ok {
		return resolvedCommand{Binding: binding}, ok, err
	}
	if registry != nil {
		if spec, ok := registry.Resolve(path); ok {
			return resolvedCommand{Binding: CommandBinding{Spec: spec}}, true, nil
		}
	}
	return resolvedCommand{}, false, nil
}

func resolveCommandBinding(path command.Path, resolver *sessioncontrol.Resolver, catalog CommandCatalog) (CommandBinding, bool, error) {
	if resolver == nil || len(catalog) == 0 {
		return CommandBinding{}, false, nil
	}
	ref := commandPathRef(path)
	id, err := resolver.Resolve("command", ref)
	if err != nil {
		return CommandBinding{}, false, err
	}
	binding, ok := catalog[id.Address()]
	return binding, ok, nil
}

func commandPathRef(path command.Path) string {
	if len(path) == 0 {
		return ""
	}
	parts := make([]string, 0, len(path))
	for _, part := range path {
		if part != "" {
			parts = append(parts, part)
		}
	}
	if len(parts) == 0 {
		return ""
	}
	if len(parts) == 1 {
		return parts[0]
	}
	return strings.Join(parts[:len(parts)-1], ":") + ":" + parts[len(parts)-1]
}

func (s Session) applyBoundOperation(ctx operation.Context, binding CommandBinding, fallback operation.Ref, input operation.Value, callID operation.CallID) environment.EffectResult {
	if !binding.OperationID.IsZero() && len(s.OperationCatalog) > 0 {
		if operationBinding, ok := s.OperationCatalog[binding.OperationID.Address()]; ok {
			return s.executeOperation(ctx, operationBinding.Operation, input, callID)
		}
		return operationEffect(operation.Failed("operation_not_bound", "command target operation is not bound to an implementation", map[string]any{
			"operation":   fallback.String(),
			"resource_id": binding.OperationID.Address(),
		}))
	}
	return s.applyOperation(ctx, fallback, input, callID)
}

func (s Session) executeOperation(ctx operation.Context, op operation.Operation, input operation.Value, callID operation.CallID) environment.EffectResult {
	ctx = sessionenv.OperationContext(ctx, s.envConfig(ctx.Events()), callID)
	return operationEffect(s.OperationExecutor.Execute(ctx, op, input))
}

func (s Session) withSubagentBaseContext(ctx context.Context, callID operation.CallID, events event.Sink) context.Context {
	return sessionenv.WithBaseContext(ctx, s.envConfig(events), callID)
}

func (s Session) replaySkillEvents(ctx context.Context) error {
	return sessionenv.ReplaySkillEvents(ctx, s.envConfig(s.eventSink()))
}

func (s Session) envConfig(events event.Sink) sessionenv.Config {
	return sessionenv.Config{
		Agent:             s.Agent,
		Subagents:         s.Subagents,
		Thread:            s.Thread,
		ThreadStore:       s.ThreadStore,
		Delegation:        s.Delegation,
		RunID:             s.RunID,
		OperationExecutor: s.OperationExecutor,
		Events:            events,
	}
}

func operationCallID(runID string, ordinal int) operation.CallID {
	if ordinal < 1 {
		ordinal = 1
	}
	if runID == "" {
		return operation.CallID(fmt.Sprintf("operation:%d", ordinal))
	}
	return operation.CallID(fmt.Sprintf("%s:operation:%d", runID, ordinal))
}

func operationEffect(result operation.Result) environment.EffectResult {
	return environment.EffectResult{
		Result: result,
		Observation: environment.Observation{
			Source:  "operation",
			Kind:    "operation.result",
			Content: result,
			At:      time.Now().UTC(),
		},
	}
}

// Resolve resolves an executable operation binding from catalog.
func (c OperationCatalog) Resolve(ref string, scope sessioncontrol.ResourceID) (OperationBinding, error) {
	if len(c) == 0 {
		return OperationBinding{}, fmt.Errorf("operation catalog is empty")
	}
	index := sessioncontrol.NewResourceIndex()
	for _, binding := range c {
		index.Add(binding.ID)
	}
	resolver := sessioncontrol.NewResolver(index)
	var (
		id  sessioncontrol.ResourceID
		err error
	)
	if scope.IsZero() {
		id, err = sessioncontrol.ResolveResource(resolver, "operation", ref)
	} else {
		id, err = sessioncontrol.ResolveResourceInScope(resolver, "operation", ref, scope)
	}
	if err != nil {
		return OperationBinding{}, err
	}
	binding, ok := c[id.Address()]
	if !ok {
		return OperationBinding{}, fmt.Errorf("resolved operation %q is not bound to an implementation", id.Address())
	}
	return binding, nil
}

func outboundMessageForOperationResult(result operation.Result) channel.Message {
	content := result.Output
	if rendered, ok := result.Output.(operation.ModelRenderable); ok {
		content = rendered.ModelText()
	}
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
	return s.appendThreadRecords(ctx, records...)
}

func (s Session) appendConversation(ctx context.Context, turnID string, provider coreconversation.ProviderIdentity, items []coreconversation.Item, handles ...coreconversation.ContinuationHandle) error {
	return retryThreadAppend(ctx, func(appendCtx context.Context) error {
		return conversationruntime.Append(appendCtx, s.ThreadStore, s.Thread, turnID, provider, items, handles...)
	})
}

func (s Session) appendThreadRecords(ctx context.Context, records ...corethread.AppendRecord) error {
	if s.ThreadStore == nil || s.Thread.ID == "" || len(records) == 0 {
		return nil
	}
	return retryThreadAppend(ctx, func(appendCtx context.Context) error {
		_, err := s.ThreadStore.Append(appendCtx, s.Thread, records...)
		return err
	})
}

func retryThreadAppend(ctx context.Context, append func(context.Context) error) error {
	if append == nil {
		return nil
	}
	var last error
	for attempt := 0; attempt < 8; attempt++ {
		if err := append(persistenceContext(ctx)); err != nil {
			last = err
			if !errors.Is(err, event.ErrAppendConflict) {
				return err
			}
			continue
		}
		return nil
	}
	return last
}

func persistenceContext(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	return context.WithoutCancel(ctx)
}
