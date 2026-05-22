package session

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"sort"
	"strconv"
	"strings"
	"sync"
	"text/template"
	"time"

	coreactivation "github.com/fluxplane/engine/core/activation"
	"github.com/fluxplane/engine/core/agent"
	"github.com/fluxplane/engine/core/channel"
	"github.com/fluxplane/engine/core/command"
	corecontext "github.com/fluxplane/engine/core/context"
	coreconversation "github.com/fluxplane/engine/core/conversation"
	coredatasource "github.com/fluxplane/engine/core/datasource"
	"github.com/fluxplane/engine/core/environment"
	coreevidence "github.com/fluxplane/engine/core/evidence"
	"github.com/fluxplane/engine/core/invocation"
	"github.com/fluxplane/engine/core/operation"
	"github.com/fluxplane/engine/core/policy"
	corereaction "github.com/fluxplane/engine/core/reaction"
	coresession "github.com/fluxplane/engine/core/session"
	coreskill "github.com/fluxplane/engine/core/skill"
	coretask "github.com/fluxplane/engine/core/task"
	corethread "github.com/fluxplane/engine/core/thread"
	"github.com/fluxplane/engine/core/tool"
	"github.com/fluxplane/engine/core/user"
	"github.com/fluxplane/engine/orchestration/security"
	"github.com/fluxplane/engine/orchestration/sessionagent"
	"github.com/fluxplane/engine/orchestration/sessioncontrol"
	"github.com/fluxplane/engine/orchestration/sessionenv"
	"github.com/fluxplane/engine/orchestration/sessionworkflow"
	conversationruntime "github.com/fluxplane/engine/runtime/conversation"
	runtimeevidence "github.com/fluxplane/engine/runtime/evidence"
	runtimereaction "github.com/fluxplane/engine/runtime/reaction"
)

// Session is the first orchestration boundary for the observe-decide-apply
// loop. It is intentionally small; lifecycle and persistence will be added only
// after the core loop is stable.
type Session struct {
	Agent                agent.Agent
	Profile              sessionenv.SessionSpec
	Commands             *command.Registry
	Operations           *operation.Registry
	Resolver             *sessioncontrol.Resolver
	CommandCatalog       CommandCatalog
	OperationCatalog     OperationCatalog
	ActivationSets       []coreactivation.Set
	ToolSetCatalog       ToolSetCatalog
	OperationSets        []operation.Set
	Datasources          []coredatasource.Spec
	PostEditChecks       []coresession.PostEditCheckSpec
	ContextProviders     []corecontext.Provider
	WorkflowCatalog      sessionworkflow.WorkflowCatalog
	OperationExecutor    sessionenv.OperationExecutor
	Events               sessionenv.EventSink
	ThreadStore          corethread.Store
	Thread               corethread.Ref
	SessionAgents        *sessionagent.Runner
	Delegation           sessionenv.DelegationPolicy
	StopEvaluator        StopEvaluator
	RunID                string
	TurnTools            []tool.Spec
	StartupObservations  []coreevidence.Observation
	StartupAssertions    []coreevidence.Assertion
	EnvironmentObservers []runtimeevidence.Observer
	AssertionDerivers    []runtimeevidence.AssertionDeriver
	ReactionRules        []corereaction.Rule
	Security             policy.AuthorizationPolicy
	SecurityTrace        bool
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
var envExplainCommandSpec = sessioncontrol.EnvExplainCommandSpec
var surfaceCommandSpec = sessioncontrol.SurfaceCommandSpec
var activateCommandSpec = sessioncontrol.ActivateCommandSpec
var compactCommandSpec = sessioncontrol.CompactCommandSpec
var goalCommandSpec = sessioncontrol.GoalCommandSpec
var whoamiCommandSpec = sessioncontrol.WhoamiCommandSpec

func builtInSessionCommands() map[string]sessionCommandBinding {
	return map[string]sessionCommandBinding{
		contextCommandSpec.Path.String():    {Spec: contextCommandSpec, Handler: Session.executeContextCommand},
		envExplainCommandSpec.Path.String(): {Spec: envExplainCommandSpec, Handler: Session.executeEnvExplainCommand},
		surfaceCommandSpec.Path.String():    {Spec: surfaceCommandSpec, Handler: Session.executeSurfaceCommand},
		activateCommandSpec.Path.String():   {Spec: activateCommandSpec, Handler: Session.executeActivateCommand},
		compactCommandSpec.Path.String():    {Spec: compactCommandSpec, Handler: Session.executeCompactCommand},
		goalCommandSpec.Path.String():       {Spec: goalCommandSpec, Handler: Session.executeGoalCommand},
		whoamiCommandSpec.Path.String():     {Spec: whoamiCommandSpec, Handler: Session.executeWhoamiCommand},
	}
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
type WorkflowCatalog = sessionworkflow.WorkflowCatalog

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

type turnToolAgent interface {
	StepWithTools(agent.Context, agent.StepInput, []tool.Spec) agent.StepResult
}

type resolvedCommand struct {
	Binding        CommandBinding
	SessionHandler sessionCommandHandler
}

// StepRequest describes one agent step request.
type StepRequest struct {
	Goal         string                     `json:"goal,omitempty"`
	Objective    agent.Objective            `json:"objective,omitempty"`
	Observations []coreevidence.Observation `json:"observations,omitempty"`
	Context      []corecontext.Block        `json:"context,omitempty"`
	State        agent.StateRef             `json:"state,omitempty"`
}

// StepResult describes one orchestrated agent step.
type StepResult struct {
	Agent       agent.StepResult           `json:"agent"`
	Effect      *environment.EffectResult  `json:"effect,omitempty"`
	Effects     []environment.EffectResult `json:"effects,omitempty"`
	Observation *coreevidence.Observation  `json:"observation,omitempty"`
}

// Step runs one observe-decide-apply cycle.
func (s Session) Step(ctx context.Context, req StepRequest) StepResult {
	agentCtx := agentContext{Context: s.withBaseContext(ensureContext(ctx), "", s.eventSink()), events: s.eventSink()}
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
	if effect, ok := s.applySurfaceOperation(ctx, ref, input, callID); ok {
		return effect
	}
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

func (s Session) eventSink() sessionenv.EventSink {
	if s.Events == nil {
		return sessionenv.DiscardEvents()
	}
	return s.Events
}

func (s Session) emitLive(payload sessionenv.Event) {
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
	events sessionenv.EventSink
}

func (c agentContext) Events() sessionenv.EventSink {
	if c.events == nil {
		return sessionenv.DiscardEvents()
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

// OperationStatus classifies the outcome of direct operation dispatch.
type OperationStatus string

const (
	OperationStatusOK     OperationStatus = "ok"
	OperationStatusFailed OperationStatus = "failed"
)

// OperationResult is the structured outcome of direct session operation
// dispatch.
type OperationResult struct {
	Status    OperationStatus           `json:"status"`
	Operation operation.Ref             `json:"operation"`
	Effect    *environment.EffectResult `json:"effect,omitempty"`
	Error     *CommandError             `json:"error,omitempty"`
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
	if err := s.appendThreadEvents(ctx, sessionenv.InputReceived{
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
	ctx = security.ContextForInbound(ctx, s.Security, inbound, s.Agent.Spec(), s.SecurityTrace)
	if err := s.replaySkillEvents(ctx); err != nil {
		return inputFailed("skill_replay_failed", err.Error(), nil)
	}
	replayedReactions, err := s.replayReactionEvents(ctx)
	if err != nil {
		return inputFailed("reaction_replay_failed", err.Error(), nil)
	}
	if surface, err := s.surfaceReadModel(ctx); err == nil && surface.Active.Lifetime != "" {
		replayedReactions.Active = mergeActiveState(replayedReactions.Active, s.activeStateFromSurface(surface.Active))
	}

	baseCtx := ensureContext(ctx)
	var conversationErr error
	var localTranscript []coreconversation.Item
	var localContinuation *coreconversation.ContinuationHandle
	var localContextRecords map[corecontext.ProviderName]corecontext.ProviderRenderRecord
	events := s.conversationEventSink(ctx, inbound.ID, &conversationErr, &localTranscript, &localContinuation)
	observations := []coreevidence.Observation{{
		Source:   "channel",
		Kind:     "channel.message",
		Content:  inbound.Message.Content,
		Metadata: inputObservationMetadata(inbound),
	}}
	goal := strings.TrimSpace(opts.Goal)
	var (
		state      agent.StateRef
		effects    []environment.EffectResult
		assertions = append([]coreevidence.Assertion(nil), s.StartupAssertions...)
		reactions  = replayedReactions
		pending    = transcriptPending{Items: []coreconversation.Item{inputTranscriptItem(s.providerIdentity(), inbound.Message.Content)}}
	)
	observations = append(observations, s.StartupObservations...)
	if s.shouldRunSessionOpenPhase(ctx) {
		observations, assertions = s.prepareEnvironmentPhase(ctx, coreevidence.PhaseSessionOpen, observations, assertions)
		var reactionEffects []environment.EffectResult
		reactions, observations, reactionEffects = s.applyTurnReactions(ctx, inbound, assertions, reactions, observations, events)
		effects = append(effects, reactionEffects...)
	}
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
			Assertions:          assertions,
			Reactions:           reactions,
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
		assertions = inner.Assertions
		reactions = inner.Reactions
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
		pending = transcriptPending{Items: []coreconversation.Item{inputTranscriptItem(s.providerIdentity(), instruction)}}
		observations = []coreevidence.Observation{{
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
	Events              *conversationEventSink
	ConversationErr     *error
	LocalTranscript     *[]coreconversation.Item
	LocalContinuation   **coreconversation.ContinuationHandle
	LocalContextRecords *map[corecontext.ProviderName]corecontext.ProviderRenderRecord
	Pending             transcriptPending
	Goal                string
	Observations        []coreevidence.Observation
	Assertions          []coreevidence.Assertion
	Reactions           reactionState
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
	Assertions  []coreevidence.Assertion
	Reactions   reactionState
}

type reactionState struct {
	PreviousAssertions map[string]string
	AppliedKeys        map[string]bool
	Active             sessionenv.ActiveState
	Applied            []corereaction.ActionApplied
}

type transcriptPending struct {
	Items          []coreconversation.Item
	Committed      bool
	CommittedCount int
}

func (s Session) runInnerTurn(ctx context.Context, in innerTurnInput) innerTurnResult {
	pending := transcriptPending{Items: append([]coreconversation.Item(nil), in.Pending.Items...), Committed: in.Pending.Committed, CommittedCount: in.Pending.CommittedCount}
	observations := append([]coreevidence.Observation(nil), in.Observations...)
	assertions := append([]coreevidence.Assertion(nil), in.Assertions...)
	observations, assertions = s.prepareEnvironmentPhase(ctx, coreevidence.PhaseTurn, observations, assertions)
	reactions, observations, reactionEffects := s.applyTurnReactions(ctx, in.Inbound, assertions, in.Reactions, observations, in.Events)
	state := in.State
	effects := append([]environment.EffectResult(nil), in.Effects...)
	effects = append(effects, reactionEffects...)
	maxSteps := in.MaxSteps
	if maxSteps <= 0 {
		maxSteps = 1
	}
	var lastAgentResult agent.StepResult
	lazyPrepared := false
	for step := 0; ; step++ {
		// Check budget BEFORE calling the model — not after.
		if step >= maxSteps {
			if in.FailOnStepLimit {
				return innerTurnResult{
					Result:      s.stepLimitResult(ctx, in.Inbound, lastAgentResult, effects),
					AgentResult: lastAgentResult,
					State:       state,
					Effects:     effects,
					Assertions:  assertions,
					Reactions:   reactions,
				}
			}
			// Clean break: outer loop will call evaluateContinuation.
			return innerTurnResult{AgentResult: lastAgentResult, State: state, Effects: effects, Assertions: assertions, Reactions: reactions}
		}
		if !lazyPrepared && s.shouldRunLazyEnvironment(reactions.Active) {
			observations, assertions = s.prepareEnvironmentPhase(ctx, coreevidence.PhaseLazy, observations, assertions)
			var reactionEffects []environment.EffectResult
			reactions, observations, reactionEffects = s.applyTurnReactions(ctx, in.Inbound, assertions, reactions, observations, in.Events)
			effects = append(effects, reactionEffects...)
			lazyPrepared = true
		}
		contextResult, projectedPending, err := s.materializeContext(ctx, in, pending.Items, observations, reactions.Active)
		if err != nil {
			return innerTurnResult{Result: inputFailed("context_render_failed", err.Error(), nil), State: state, Effects: effects, Assertions: assertions, Reactions: reactions}
		}
		committedCount := pending.CommittedCount
		if pending.Committed && committedCount == 0 {
			committedCount = len(pending.Items)
		}
		projected := transcriptPending{Items: projectedPending, Committed: pending.Committed, CommittedCount: committedCount}
		transcript, err := s.transcriptForPending(ctx, projected, derefItems(in.LocalTranscript), derefHandle(in.LocalContinuation))
		if err != nil {
			return innerTurnResult{Result: inputFailed("conversation_projection_failed", err.Error(), nil), State: state, Effects: effects, Assertions: assertions, Reactions: reactions}
		}
		modelCtx := sessioncontrol.ContextWithTranscript(in.BaseContext, &transcript)
		modelCtx = s.withBaseContext(modelCtx, "", in.Events, reactions.Active)
		agentCtx := agentContext{Context: modelCtx, events: in.Events}
		stepInput := agent.StepInput{
			Goal:         in.Goal,
			Observations: observations,
			Context:      in.History,
			State:        state,
		}
		var agentResult agent.StepResult
		turnTools := s.turnTools(reactions.Active)
		if turnTools != nil {
			if toolAgent, ok := s.Agent.(turnToolAgent); ok {
				agentResult = toolAgent.StepWithTools(agentCtx, stepInput, turnTools)
			} else {
				agentResult = s.Agent.Step(agentCtx, stepInput)
			}
		} else {
			agentResult = s.Agent.Step(agentCtx, stepInput)
		}
		if agentResult.Status != agent.StatusOK {
			return innerTurnResult{Result: InputResult{Status: InputStatusFailed, Agent: agentResult, Effect: lastEffect(effects), Effects: effects, Error: agentError(agentResult.Error)}, AgentResult: agentResult, State: state, Effects: effects, Assertions: assertions, Reactions: reactions}
		}
		stepCompleted := sessionenv.AgentStepCompleted{RunID: in.Inbound.ID, Result: agentResult}
		stepItems := append(derefItems(in.LocalTranscript), in.Events.Items()...)
		if err := s.validateOperationRequests(stepItems, agentResult.Decision.Operations); err != nil {
			return innerTurnResult{Result: inputFailed("conversation_continuity_failed", err.Error(), nil), AgentResult: agentResult, State: state, Effects: effects, Assertions: assertions, Reactions: reactions}
		}
		if err := s.commitContextRender(ctx, contextResult, in.LocalContextRecords); err != nil {
			return innerTurnResult{Result: inputFailed("context_commit_failed", err.Error(), nil), AgentResult: agentResult, State: state, Effects: effects, Assertions: assertions, Reactions: reactions}
		}
		if !stateRefIsZero(agentResult.State.Ref) {
			state = agentResult.State.Ref
		}
		lastAgentResult = agentResult
		if agentResult.Decision.Kind != agent.DecisionOperation {
			if err := conversationruntime.ValidateContinuity(stepItems, conversationruntime.ValidateOptions{Provider: in.ProviderIdentity}); err != nil {
				return innerTurnResult{Result: inputFailed("conversation_continuity_failed", err.Error(), nil), AgentResult: agentResult, State: state, Effects: effects, Assertions: assertions, Reactions: reactions}
			}
			if err := in.Events.Flush(ctx, s, stepCompleted); err != nil {
				return innerTurnResult{Result: inputFailed("conversation_append_failed", err.Error(), nil), AgentResult: agentResult, State: state, Effects: effects, Assertions: assertions, Reactions: reactions}
			}
			return innerTurnResult{AgentResult: agentResult, State: state, Effects: effects, Assertions: assertions, Reactions: reactions}
		}
		if len(agentResult.Decision.Operations) == 0 {
			return innerTurnResult{Result: InputResult{Status: InputStatusUnsupported, Agent: agentResult, Effect: lastEffect(effects), Effects: effects, Error: &CommandError{Code: "operation_missing", Message: "agent operation decision is empty"}}, AgentResult: agentResult, State: state, Effects: effects, Assertions: assertions, Reactions: reactions}
		}
		batch, toolResults, completed, err := s.applyAgentOperations(ctx, agentCtx, in.Inbound, len(effects), agentResult.Decision.Operations, &reactions.Active, in.ProviderIdentity)
		if err != nil {
			return innerTurnResult{Result: inputFailed("thread_append_failed", err.Error(), nil), AgentResult: agentResult, State: state, Effects: effects, Assertions: assertions, Reactions: reactions}
		}
		effects = append(effects, batch...)
		checkBatch, _, err := s.applyPostEditChecks(ctx, agentCtx, in.Inbound, len(effects), batch)
		if err != nil {
			return innerTurnResult{Result: inputFailed("thread_append_failed", err.Error(), nil), AgentResult: agentResult, State: state, Effects: effects, Assertions: assertions, Reactions: reactions}
		}
		if err := conversationruntime.ValidateContinuity(append(stepItems, toolResults...), conversationruntime.ValidateOptions{Provider: in.ProviderIdentity}); err != nil {
			return innerTurnResult{Result: inputFailed("conversation_continuity_failed", err.Error(), nil), AgentResult: agentResult, State: state, Effects: effects, Assertions: assertions, Reactions: reactions}
		}
		flushEvents := make([]sessionenv.Event, 0, 1+len(completed))
		flushEvents = append(flushEvents, stepCompleted)
		for i := range completed {
			flushEvents = append(flushEvents, completed[i])
		}
		if err := in.Events.FlushWithTranscriptItems(ctx, s, toolResults, flushEvents...); err != nil {
			return innerTurnResult{Result: inputFailed("conversation_append_failed", err.Error(), nil), AgentResult: agentResult, State: state, Effects: effects, Assertions: assertions, Reactions: reactions}
		}
		for _, event := range completed {
			s.emitLive(event)
		}
		effects = append(effects, checkBatch...)
		observations = append(observations, observationsForEffects(append(batch, checkBatch...))...)
		observations, assertions = s.prepareEnvironmentPhase(ctx, coreevidence.PhaseToolFollowup, observations, assertions)
		reactions, observations, reactionEffects = s.applyTurnReactions(ctx, in.Inbound, assertions, reactions, observations, in.Events)
		effects = append(effects, reactionEffects...)
		pending = transcriptPending{Items: toolResults, Committed: true, CommittedCount: len(toolResults)}
	}
}

func (s Session) prepareEnvironmentPhase(ctx context.Context, phase coreevidence.ObservationPhase, observations []coreevidence.Observation, assertions []coreevidence.Assertion) ([]coreevidence.Observation, []coreevidence.Assertion) {
	if len(s.EnvironmentObservers) > 0 {
		extra, diagnostics := runtimeevidence.RunObservers(ctx, s.EnvironmentObservers, runtimeevidence.ObservationRequest{
			Phase:        phase,
			Observations: append([]coreevidence.Observation(nil), observations...),
		})
		observations = append(observations, extra...)
		observations = append(observations, environmentDiagnostics("observer", diagnostics)...)
	}
	if len(s.AssertionDerivers) > 0 {
		extra, diagnostics := runtimeevidence.DeriveAssertions(ctx, s.AssertionDerivers, runtimeevidence.AssertionDeriveRequest{
			Observations: append([]coreevidence.Observation(nil), observations...),
		})
		assertions = append(assertions, extra...)
		observations = append(observations, environmentDiagnostics("assertion_deriver", diagnostics)...)
	}
	return observations, assertions
}

func (s Session) shouldRunLazyEnvironment(active sessionenv.ActiveState) bool {
	if !s.hasObserverPhase(coreevidence.PhaseLazy) {
		return false
	}
	return len(s.contextProviders(active)) > 0
}

func (s Session) hasObserverPhase(phase coreevidence.ObservationPhase) bool {
	for _, observer := range s.EnvironmentObservers {
		if observer == nil {
			continue
		}
		spec := observer.Spec()
		if spec.Phase == phase || spec.Phase == "" {
			return true
		}
	}
	return false
}

func (s Session) applyTurnReactions(ctx context.Context, inbound channel.Inbound, assertions []coreevidence.Assertion, state reactionState, observations []coreevidence.Observation, sink sessionenv.EventSink) (reactionState, []coreevidence.Observation, []environment.EffectResult) {
	if len(s.ReactionRules) == 0 || len(assertions) == 0 {
		return state, observations, nil
	}
	plan := runtimereaction.Plan(runtimereaction.Request{
		Rules:       s.ReactionRules,
		Assertions:  assertions,
		Previous:    state.PreviousAssertions,
		AppliedKeys: state.AppliedKeys,
	})
	state.PreviousAssertions = plan.Current
	emitReactionPlanDiagnostics(sink, plan.Diagnostics)
	emitSkippedReactionActions(sink, plan.Skipped)
	observations = append(observations, reactionPlanDiagnostics(plan.Diagnostics)...)
	if len(plan.Planned) == 0 {
		return state, observations, nil
	}
	actions := make([]sessionenv.ReactionAction, 0, len(plan.Planned))
	activationSetActions := make([]sessionenv.ReactionAction, 0, len(plan.Planned))
	operationActions := make([]sessionenv.ReactionAction, 0, len(plan.Planned))
	commandActions := make([]sessionenv.ReactionAction, 0, len(plan.Planned))
	workflowActions := make([]sessionenv.ReactionAction, 0, len(plan.Planned))
	approvalDiagnostics := make([]sessionenv.ReactionDiagnostic, 0)
	for _, planned := range plan.Planned {
		emitReactionEvent(sink, corereaction.ActionPlanned{
			Rule:           planned.Rule,
			Action:         planned.Action.Kind,
			IdempotencyKey: planned.IdempotencyKey,
		})
		action := sessionenv.ReactionAction{
			Rule:           planned.Rule,
			Assertion:      planned.Assertion,
			Action:         planned.Action,
			IdempotencyKey: planned.IdempotencyKey,
		}
		if effectfulReactionAction(planned.Action.Kind) && planned.Action.RequireApproval {
			emitReactionEvent(sink, corereaction.ActionSkipped{
				Rule:           planned.Rule,
				Action:         planned.Action.Kind,
				IdempotencyKey: planned.IdempotencyKey,
				Reason:         "approval_required",
			})
			approvalDiagnostics = append(approvalDiagnostics, sessionenv.ReactionDiagnostic{
				Rule:    planned.Rule,
				Action:  planned.Action.Kind,
				Message: "reaction action requires approval",
			})
			continue
		}
		if planned.Action.Kind == corereaction.ActionRunOperation {
			operationActions = append(operationActions, action)
			continue
		}
		if planned.Action.Kind == corereaction.ActionRunCommand {
			commandActions = append(commandActions, action)
			continue
		}
		if planned.Action.Kind == corereaction.ActionRunWorkflow {
			workflowActions = append(workflowActions, action)
			continue
		}
		if planned.Action.Kind == corereaction.ActionEnableActivationSet {
			activationSetActions = append(activationSetActions, action)
			continue
		}
		actions = append(actions, action)
	}
	emitReactionApplyDiagnostics(sink, approvalDiagnostics)
	observations = append(observations, reactionApplyDiagnostics(approvalDiagnostics)...)
	cfg := s.envConfig(sink)
	cfg.Active = &state.Active
	result := sessionenv.ApplyReactionActions(actions, cfg)
	if len(result.AppliedKeys) > 0 && state.AppliedKeys == nil {
		state.AppliedKeys = map[string]bool{}
	}
	for _, key := range result.AppliedKeys {
		state.AppliedKeys[key] = true
	}
	emitReactionApplyDiagnostics(sink, result.Diagnostics)
	observations = append(observations, reactionApplyDiagnostics(result.Diagnostics)...)
	activationDiagnostics, appliedActivationKeys := s.applyReactionActivationSets(ctx, inbound.ID, activationSetActions, &state.Active, sink)
	reactionEffects, operationDiagnostics, appliedOperationKeys := s.applyReactionOperations(ctx, inbound.ID, operationActions, state.Active, sink)
	commandEffects, commandDiagnostics, appliedCommandKeys := s.applyReactionCommands(ctx, inbound, commandActions, state.Active, sink)
	workflowObservations, workflowDiagnostics, appliedWorkflowKeys := s.applyReactionWorkflows(ctx, inbound.ID, workflowActions, sink)
	reactionEffects = append(reactionEffects, commandEffects...)
	operationDiagnostics = append(operationDiagnostics, activationDiagnostics...)
	operationDiagnostics = append(operationDiagnostics, commandDiagnostics...)
	operationDiagnostics = append(operationDiagnostics, workflowDiagnostics...)
	appliedOperationKeys = append(appliedOperationKeys, appliedActivationKeys...)
	appliedOperationKeys = append(appliedOperationKeys, appliedCommandKeys...)
	appliedOperationKeys = append(appliedOperationKeys, appliedWorkflowKeys...)
	if len(appliedOperationKeys) > 0 && state.AppliedKeys == nil {
		state.AppliedKeys = map[string]bool{}
	}
	for _, key := range appliedOperationKeys {
		state.AppliedKeys[key] = true
	}
	emitReactionApplyDiagnostics(sink, operationDiagnostics)
	observations = append(observations, reactionApplyDiagnostics(operationDiagnostics)...)
	observations = append(observations, observationsForEffects(reactionEffects)...)
	observations = append(observations, workflowObservations...)
	return state, observations, reactionEffects
}

func (s Session) applyReactionActivationSets(ctx context.Context, runID string, actions []sessionenv.ReactionAction, active *sessionenv.ActiveState, sink sessionenv.EventSink) ([]sessionenv.ReactionDiagnostic, []string) {
	if len(actions) == 0 {
		return nil, nil
	}
	var diagnostics []sessionenv.ReactionDiagnostic
	var appliedKeys []string
	for _, planned := range actions {
		name := strings.TrimSpace(planned.Action.ActivationSet)
		if name == "" {
			diagnostics = append(diagnostics, sessionenv.ReactionDiagnostic{Rule: planned.Rule, Action: planned.Action.Kind, Message: "activation set name is empty"})
			continue
		}
		prepared := s.prepareSurfaceWithEmit(ctx, surfacePrepareRequest{
			Terms:    []string{name},
			Lifetime: coreactivation.LifetimeRun,
			Source:   coreactivation.SourceReaction,
			RunID:    runID,
		}, sink.Emit)
		if active != nil {
			*active = mergeActiveState(*active, s.activeStateFromSurface(prepared.Active))
		}
		if len(prepared.Prepared.ActivationSets) == 0 {
			message := "activation set was not prepared"
			if len(prepared.Diagnostics) > 0 {
				message = firstNonEmptyString(prepared.Diagnostics[0].Message, prepared.Diagnostics[0].Reason, message)
			}
			diagnostics = append(diagnostics, sessionenv.ReactionDiagnostic{Rule: planned.Rule, Action: planned.Action.Kind, Message: message})
			continue
		}
		if planned.IdempotencyKey != "" {
			appliedKeys = append(appliedKeys, planned.IdempotencyKey)
			emitReactionEvent(sink, corereaction.ActionApplied{
				Rule:                 planned.Rule,
				Action:               planned.Action.Kind,
				IdempotencyKey:       planned.IdempotencyKey,
				Target:               name,
				Assertion:            planned.Assertion.Kind,
				AssertionTarget:      planned.Assertion.Target,
				AssertionSubjectKind: string(planned.Assertion.Subject.Kind),
				AssertionSubjectName: planned.Assertion.Subject.Name,
				AssertionSubjectID:   planned.Assertion.Subject.ID,
				AssertionScope:       planned.Assertion.Scope,
				AssertionSource:      planned.Assertion.Source,
				ObservationIDs:       append([]string(nil), planned.Assertion.ObservationIDs...),
			})
		}
	}
	return diagnostics, appliedKeys
}

func effectfulReactionAction(kind corereaction.ActionKind) bool {
	return kind == corereaction.ActionRunOperation ||
		kind == corereaction.ActionRunCommand ||
		kind == corereaction.ActionRunWorkflow
}

func (s Session) applyReactionOperations(ctx context.Context, runID string, actions []sessionenv.ReactionAction, active sessionenv.ActiveState, sink sessionenv.EventSink) ([]environment.EffectResult, []sessionenv.ReactionDiagnostic, []string) {
	if len(actions) == 0 {
		return nil, nil, nil
	}
	var effects []environment.EffectResult
	var diagnostics []sessionenv.ReactionDiagnostic
	var appliedKeys []string
	for i, planned := range actions {
		ref := planned.Action.Operation.Operation
		input := planned.Action.Operation.Input
		callID := reactionOperationCallID(runID, planned.IdempotencyKey, i+1)
		requested := sessionenv.OperationRequested{
			RunID:     runID,
			CallID:    callID,
			Operation: ref,
			Input:     input,
		}
		if err := s.appendThreadEvents(ctx, requested); err != nil {
			diagnostics = append(diagnostics, sessionenv.ReactionDiagnostic{Rule: planned.Rule, Action: planned.Action.Kind, Message: err.Error()})
			continue
		}
		s.emitLive(requested)
		base := s.withBaseContext(ensureContext(ctx), callID, sink, active)
		opCtx := operation.NewContext(base, sink)
		effect := s.applyOperation(opCtx, ref, input, callID)
		if effect.Observation.Metadata == nil {
			effect.Observation.Metadata = map[string]any{}
		}
		effect.Observation.Metadata["operation"] = ref.String()
		effect.Observation.Metadata["call_id"] = string(callID)
		effect.Observation.Metadata["reaction_rule"] = planned.Rule
		effect.Observation.Metadata["reaction_action"] = string(planned.Action.Kind)
		effect, replacementErr := replaceOversizedToolResult(opCtx, effect, ref, callID)
		if replacementErr != nil {
			effect = operationEffect(operation.Failed("reaction_result_replacement_failed", replacementErr.Error(), map[string]any{
				"operation": ref.String(),
				"call_id":   string(callID),
			}))
			effect.Observation.Metadata = map[string]any{
				"operation":       ref.String(),
				"call_id":         string(callID),
				"reaction_rule":   planned.Rule,
				"reaction_action": string(planned.Action.Kind),
			}
		}
		completed := sessionenv.OperationCompleted{
			RunID:     runID,
			CallID:    callID,
			Operation: ref,
			Result:    effect.Result,
		}
		if err := s.appendThreadEvents(ctx, completed); err != nil {
			diagnostics = append(diagnostics, sessionenv.ReactionDiagnostic{Rule: planned.Rule, Action: planned.Action.Kind, Message: err.Error()})
			continue
		}
		s.emitLive(completed)
		effects = append(effects, effect)
		if planned.IdempotencyKey != "" {
			appliedKeys = append(appliedKeys, planned.IdempotencyKey)
			emitReactionEvent(sink, corereaction.ActionApplied{
				Rule:                 planned.Rule,
				Action:               planned.Action.Kind,
				IdempotencyKey:       planned.IdempotencyKey,
				Target:               ref.String(),
				Assertion:            planned.Assertion.Kind,
				AssertionTarget:      planned.Assertion.Target,
				AssertionSubjectKind: string(planned.Assertion.Subject.Kind),
				AssertionSubjectName: planned.Assertion.Subject.Name,
				AssertionSubjectID:   planned.Assertion.Subject.ID,
				AssertionScope:       planned.Assertion.Scope,
				AssertionSource:      planned.Assertion.Source,
				ObservationIDs:       append([]string(nil), planned.Assertion.ObservationIDs...),
			})
		}
	}
	return effects, diagnostics, appliedKeys
}

func (s Session) applyReactionCommands(ctx context.Context, inbound channel.Inbound, actions []sessionenv.ReactionAction, active sessionenv.ActiveState, sink sessionenv.EventSink) ([]environment.EffectResult, []sessionenv.ReactionDiagnostic, []string) {
	if len(actions) == 0 {
		return nil, nil, nil
	}
	var effects []environment.EffectResult
	var diagnostics []sessionenv.ReactionDiagnostic
	var appliedKeys []string
	base := s.withBaseContext(ensureContext(ctx), "", sink, active)
	for i, planned := range actions {
		commandInbound := inbound
		commandInbound.ID = reactionCommandRunID(inbound.ID, planned.IdempotencyKey, i+1)
		commandInbound.Kind = channel.InboundCommand
		commandInbound.Command = cloneCommandInvocation(planned.Action.Command)
		commandInbound.Message = nil
		result := s.ExecuteInboundCommand(base, commandInbound)
		switch result.Status {
		case CommandStatusRejected, CommandStatusApprovalRequired:
			message := "reaction command was not executed"
			if result.Policy.Reason != "" {
				message = result.Policy.Reason
			}
			diagnostics = append(diagnostics, sessionenv.ReactionDiagnostic{Rule: planned.Rule, Action: planned.Action.Kind, Message: message})
			continue
		}
		if result.Error != nil && result.Status != CommandStatusFailed {
			diagnostics = append(diagnostics, sessionenv.ReactionDiagnostic{Rule: planned.Rule, Action: planned.Action.Kind, Message: result.Error.Message})
			continue
		}
		if result.Effect != nil {
			effect := *result.Effect
			if effect.Observation.Metadata == nil {
				effect.Observation.Metadata = map[string]any{}
			}
			effect.Observation.Metadata["reaction_rule"] = planned.Rule
			effect.Observation.Metadata["reaction_action"] = string(planned.Action.Kind)
			effects = append(effects, effect)
		}
		if planned.IdempotencyKey != "" {
			appliedKeys = append(appliedKeys, planned.IdempotencyKey)
			emitReactionEvent(sink, corereaction.ActionApplied{
				Rule:                 planned.Rule,
				Action:               planned.Action.Kind,
				IdempotencyKey:       planned.IdempotencyKey,
				Target:               planned.Action.Command.Path.String(),
				Assertion:            planned.Assertion.Kind,
				AssertionTarget:      planned.Assertion.Target,
				AssertionSubjectKind: string(planned.Assertion.Subject.Kind),
				AssertionSubjectName: planned.Assertion.Subject.Name,
				AssertionSubjectID:   planned.Assertion.Subject.ID,
				AssertionScope:       planned.Assertion.Scope,
				AssertionSource:      planned.Assertion.Source,
				ObservationIDs:       append([]string(nil), planned.Assertion.ObservationIDs...),
			})
		}
	}
	return effects, diagnostics, appliedKeys
}

func (s Session) applyReactionWorkflows(ctx context.Context, runID string, actions []sessionenv.ReactionAction, sink sessionenv.EventSink) ([]coreevidence.Observation, []sessionenv.ReactionDiagnostic, []string) {
	if len(actions) == 0 {
		return nil, nil, nil
	}
	var observations []coreevidence.Observation
	var diagnostics []sessionenv.ReactionDiagnostic
	var appliedKeys []string
	for i, planned := range actions {
		workflowName := planned.Action.Workflow.Name
		workflowRunID := reactionWorkflowRunID(runID, planned.IdempotencyKey, i+1)
		spec := command.Spec{
			Path: command.Path{"reaction", "workflow", string(workflowName)},
			Target: invocation.Target{
				Kind:     invocation.TargetWorkflow,
				Workflow: workflowName,
				Input:    planned.Action.Workflow.Input,
			},
		}
		result := sessionworkflow.Execute(ctx, sessionworkflow.Config{
			WorkflowCatalog:   s.WorkflowCatalog,
			Resolver:          s.Resolver,
			OperationExecutor: s.OperationExecutor,
			SessionAgents:     s.SessionAgents,
			Delegation:        s.Delegation,
			Thread:            s.Thread,
			Events:            sink,
			AppendEvents:      s.appendThreadEvents,
			EmitLive:          s.emitLive,
			ResolveOperation: func(ref string, scope sessioncontrol.ResourceID) (operation.Operation, error) {
				binding, err := s.OperationCatalog.Resolve(ref, scope)
				if err != nil {
					return nil, err
				}
				return binding.Operation, nil
			},
		}, workflowRunID, planned.Action.Workflow.Input, sessioncontrol.ResourceID{}, spec)
		if result.Error != nil && result.Status == sessionworkflow.StatusFailed {
			diagnostics = append(diagnostics, sessionenv.ReactionDiagnostic{Rule: planned.Rule, Action: planned.Action.Kind, Message: result.Error.Message})
			continue
		}
		observations = append(observations, coreevidence.Observation{
			Source:  "workflow",
			Kind:    "workflow.result",
			Content: result,
			At:      time.Now().UTC(),
			Metadata: map[string]any{
				"workflow":        string(workflowName),
				"run_id":          workflowRunID,
				"reaction_rule":   planned.Rule,
				"reaction_action": string(planned.Action.Kind),
			},
		})
		if planned.IdempotencyKey != "" {
			appliedKeys = append(appliedKeys, planned.IdempotencyKey)
			emitReactionEvent(sink, corereaction.ActionApplied{
				Rule:                 planned.Rule,
				Action:               planned.Action.Kind,
				IdempotencyKey:       planned.IdempotencyKey,
				Target:               string(workflowName),
				Assertion:            planned.Assertion.Kind,
				AssertionTarget:      planned.Assertion.Target,
				AssertionSubjectKind: string(planned.Assertion.Subject.Kind),
				AssertionSubjectName: planned.Assertion.Subject.Name,
				AssertionSubjectID:   planned.Assertion.Subject.ID,
				AssertionScope:       planned.Assertion.Scope,
				AssertionSource:      planned.Assertion.Source,
				ObservationIDs:       append([]string(nil), planned.Assertion.ObservationIDs...),
			})
		}
	}
	return observations, diagnostics, appliedKeys
}

func environmentDiagnostics(kind string, diagnostics []runtimeevidence.Diagnostic) []coreevidence.Observation {
	if len(diagnostics) == 0 {
		return nil
	}
	out := make([]coreevidence.Observation, 0, len(diagnostics))
	for _, diagnostic := range diagnostics {
		if strings.TrimSpace(diagnostic.Message) == "" {
			continue
		}
		out = append(out, coreevidence.Observation{
			Source:  "runtime/evidence",
			Kind:    "environment.diagnostic",
			Content: diagnostic.Message,
			Metadata: map[string]any{
				"kind": kind,
				"name": diagnostic.Name,
			},
		})
	}
	return out
}

func reactionPlanDiagnostics(diagnostics []runtimereaction.Diagnostic) []coreevidence.Observation {
	if len(diagnostics) == 0 {
		return nil
	}
	out := make([]coreevidence.Observation, 0, len(diagnostics))
	for _, diagnostic := range diagnostics {
		if strings.TrimSpace(diagnostic.Message) == "" {
			continue
		}
		out = append(out, coreevidence.Observation{
			Source:  "runtime/reaction",
			Kind:    "reaction.diagnostic",
			Content: diagnostic.Message,
			Metadata: map[string]any{
				"rule": diagnostic.Rule,
			},
		})
	}
	return out
}

func reactionApplyDiagnostics(diagnostics []sessionenv.ReactionDiagnostic) []coreevidence.Observation {
	if len(diagnostics) == 0 {
		return nil
	}
	out := make([]coreevidence.Observation, 0, len(diagnostics))
	for _, diagnostic := range diagnostics {
		if strings.TrimSpace(diagnostic.Message) == "" {
			continue
		}
		out = append(out, coreevidence.Observation{
			Source:  "orchestration/sessionenv",
			Kind:    "reaction.diagnostic",
			Content: diagnostic.Message,
			Metadata: map[string]any{
				"rule":   diagnostic.Rule,
				"action": string(diagnostic.Action),
			},
		})
	}
	return out
}

func emitSkippedReactionActions(sink sessionenv.EventSink, skipped []runtimereaction.SkippedAction) {
	for _, action := range skipped {
		emitReactionEvent(sink, corereaction.ActionSkipped{
			Rule:           action.Rule,
			Action:         action.Action.Kind,
			IdempotencyKey: action.IdempotencyKey,
			Reason:         action.Reason,
		})
	}
}

func emitReactionPlanDiagnostics(sink sessionenv.EventSink, diagnostics []runtimereaction.Diagnostic) {
	for _, diagnostic := range diagnostics {
		if strings.TrimSpace(diagnostic.Message) == "" {
			continue
		}
		emitReactionEvent(sink, corereaction.Diagnostic{
			Rule:    diagnostic.Rule,
			Message: diagnostic.Message,
		})
	}
}

func emitReactionApplyDiagnostics(sink sessionenv.EventSink, diagnostics []sessionenv.ReactionDiagnostic) {
	for _, diagnostic := range diagnostics {
		if strings.TrimSpace(diagnostic.Message) == "" {
			continue
		}
		emitReactionEvent(sink, corereaction.Diagnostic{
			Rule:    diagnostic.Rule,
			Action:  diagnostic.Action,
			Message: diagnostic.Message,
		})
	}
}

func emitReactionEvent(sink sessionenv.EventSink, payload sessionenv.Event) {
	if payload == nil {
		return
	}
	if sink == nil {
		sink = sessionenv.DiscardEvents()
	}
	sink.Emit(payload)
}

func (s Session) finalizeInputResult(ctx context.Context, inbound channel.Inbound, result InputResult) InputResult {
	if result.Status == InputStatusOK {
		_ = s.autoCompactAfterTurn(ctx, inbound.ID)
	}
	s.expireRunSurface(ctx)
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

func inputObservationMetadata(inbound channel.Inbound) map[string]any {
	scope := contextRequestScope(inbound)
	out := make(map[string]any, len(scope)+2)
	if inbound.Channel.Name != "" {
		out["channel"] = string(inbound.Channel.Name)
	}
	if inbound.Conversation.ID != "" {
		out["conversation"] = inbound.Conversation.ID
	}
	for key, value := range scope {
		out[key] = value
	}
	return out
}

func (s Session) shouldRunSessionOpenPhase(ctx context.Context) bool {
	if s.ThreadStore == nil || s.Thread.ID == "" {
		return true
	}
	snapshot, err := s.ThreadStore.Read(ensureContext(ctx), corethread.ReadParams{ID: s.Thread.ID})
	if err != nil {
		return true
	}
	inboundEvents := 0
	for _, record := range snapshot.Events {
		switch record.Event.Name {
		case coresession.EventInputReceived, coresession.EventCommandReceived:
			inboundEvents++
		}
	}
	return inboundEvents <= 1
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

type surfaceCommandInput struct {
	JSON bool `json:"json,omitempty" command:"flag=json"`
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

type envExplainData struct {
	Observers         []coreevidence.ObserverSpec         `json:"observers,omitempty"`
	AssertionDerivers []coreevidence.AssertionDeriverSpec `json:"assertion_derivers,omitempty"`
	ReactionRules     []string                            `json:"reaction_rules,omitempty"`
	Observations      []envExplainObservation             `json:"observations,omitempty"`
	Assertions        []envExplainAssertion               `json:"assertions,omitempty"`
	Matching          []envExplainReactionMatch           `json:"matching,omitempty"`
	Active            envExplainActive                    `json:"active,omitempty"`
	Applied           []envExplainApplied                 `json:"applied,omitempty"`
	AppliedReactions  int                                 `json:"applied_reactions,omitempty"`
}

type envExplainObservation struct {
	ID          string         `json:"id,omitempty"`
	Kind        string         `json:"kind,omitempty"`
	Source      string         `json:"source,omitempty"`
	Scope       string         `json:"scope,omitempty"`
	Environment string         `json:"environment,omitempty"`
	Content     string         `json:"content,omitempty"`
	Metadata    map[string]any `json:"metadata,omitempty"`
}

type envExplainAssertion struct {
	Kind           string            `json:"kind,omitempty"`
	Target         string            `json:"target,omitempty"`
	Subject        envExplainSubject `json:"subject,omitempty"`
	Scope          string            `json:"scope,omitempty"`
	Source         string            `json:"source,omitempty"`
	Environment    string            `json:"environment,omitempty"`
	Confidence     float64           `json:"confidence,omitempty"`
	ObservationIDs []string          `json:"observation_ids,omitempty"`
	Metadata       map[string]string `json:"metadata,omitempty"`
}

type envExplainSubject struct {
	Kind string `json:"kind,omitempty"`
	Name string `json:"name,omitempty"`
	ID   string `json:"id,omitempty"`
}

type envExplainReactionMatch struct {
	Rule             string            `json:"rule,omitempty"`
	Action           string            `json:"action,omitempty"`
	Target           string            `json:"target,omitempty"`
	Assertion        string            `json:"assertion,omitempty"`
	AssertionTarget  string            `json:"assertion_target,omitempty"`
	AssertionSubject envExplainSubject `json:"assertion_subject,omitempty"`
	ObservationIDs   []string          `json:"observation_ids,omitempty"`
	Status           string            `json:"status,omitempty"`
	Reason           string            `json:"reason,omitempty"`
}

type envExplainActive struct {
	ActivationSets   []string `json:"activation_sets,omitempty"`
	Operations       []string `json:"operations,omitempty"`
	OperationSets    []string `json:"operation_sets,omitempty"`
	Datasources      []string `json:"datasources,omitempty"`
	ContextProviders []string `json:"context_providers,omitempty"`
	InlineContexts   []string `json:"inline_contexts,omitempty"`
}

type envExplainApplied struct {
	Rule             string            `json:"rule,omitempty"`
	Action           string            `json:"action,omitempty"`
	Target           string            `json:"target,omitempty"`
	Assertion        string            `json:"assertion,omitempty"`
	AssertionTarget  string            `json:"assertion_target,omitempty"`
	AssertionSubject envExplainSubject `json:"assertion_subject,omitempty"`
	AssertionScope   string            `json:"assertion_scope,omitempty"`
	AssertionSource  string            `json:"assertion_source,omitempty"`
	ObservationIDs   []string          `json:"observation_ids,omitempty"`
}

func (s Session) executeEnvExplainCommand(ctx context.Context, inbound channel.Inbound, spec command.Spec, evaluation sessioncontrol.PolicyEvaluation) CommandResult {
	state, err := s.replayReactionEvents(ctx)
	if err != nil {
		return commandFailed("reaction_replay_failed", err.Error(), nil)
	}
	if surface, err := s.surfaceReadModel(ctx); err == nil && surface.Active.Lifetime != "" {
		state.Active = mergeActiveState(state.Active, s.activeStateFromSurface(surface.Active))
	}
	observations := append([]coreevidence.Observation(nil), s.StartupObservations...)
	assertions := append([]coreevidence.Assertion(nil), s.StartupAssertions...)
	observations, assertions = s.prepareEnvironmentPhase(ctx, coreevidence.PhaseTurn, observations, assertions)
	plan := runtimereaction.Plan(runtimereaction.Request{
		Rules:       s.ReactionRules,
		Assertions:  assertions,
		AppliedKeys: state.AppliedKeys,
	})
	data := envExplainData{
		Observers:         environmentObserverSpecs(s.EnvironmentObservers),
		AssertionDerivers: assertionDeriverSpecs(s.AssertionDerivers),
		ReactionRules:     reactionRuleNames(s.ReactionRules),
		Observations:      explainObservations(observations),
		Assertions:        explainAssertions(assertions),
		Matching:          explainReactionMatches(plan, s.ReactionRules, assertions),
		Active:            explainActiveState(state.Active),
		Applied:           explainAppliedReactions(state.Applied),
		AppliedReactions:  len(state.AppliedKeys),
	}
	return CommandResult{Status: CommandStatusOK, Spec: spec, Policy: evaluation, Output: operation.Rendered{
		Text: renderEnvExplain(data),
		Data: data,
	}}
}

func (s Session) executeSurfaceCommand(ctx context.Context, inbound channel.Inbound, spec command.Spec, evaluation sessioncontrol.PolicyEvaluation) CommandResult {
	input, err := parseSurfaceCommand(*inbound.Command)
	if err != nil {
		return CommandResult{
			Status: CommandStatusFailed,
			Spec:   spec,
			Policy: evaluation,
			Error:  &CommandError{Code: "invalid_surface_command_input", Message: err.Error()},
		}
	}
	model, err := s.surfaceReadModel(ctx)
	if err != nil {
		return CommandResult{
			Status: CommandStatusFailed,
			Spec:   spec,
			Policy: evaluation,
			Error:  &CommandError{Code: "surface_read_failed", Message: err.Error()},
		}
	}
	text := renderSurfaceReadModel(model)
	if input.JSON {
		data, err := json.MarshalIndent(model, "", "  ")
		if err != nil {
			return CommandResult{
				Status: CommandStatusFailed,
				Spec:   spec,
				Policy: evaluation,
				Error:  &CommandError{Code: "surface_json_failed", Message: err.Error()},
			}
		}
		text = string(data)
	}
	return CommandResult{Status: CommandStatusOK, Spec: spec, Policy: evaluation, Output: operation.Rendered{
		Text: text,
		Data: model,
	}}
}

func parseSurfaceCommand(inv command.Invocation) (surfaceCommandInput, error) {
	input, err := command.Bind[surfaceCommandInput](inv)
	if err != nil {
		return surfaceCommandInput{}, err
	}
	if inv.Input != nil {
		structured, err := decodeCommandInput[surfaceCommandInput](inv.Input)
		if err != nil {
			return surfaceCommandInput{}, err
		}
		if structured.JSON {
			input.JSON = true
		}
	}
	return input, nil
}

func (s Session) surfaceReadModel(ctx context.Context) (coreactivation.ReadModel, error) {
	var model coreactivation.ReadModel
	if s.ThreadStore == nil || s.Thread.ID == "" {
		return model, nil
	}
	snapshot, err := s.ThreadStore.Read(ensureContext(ctx), corethread.ReadParams{ID: s.Thread.ID})
	if err != nil {
		if errors.Is(err, corethread.ErrNotFound) {
			return model, nil
		}
		return model, err
	}
	records, err := snapshot.EventsForBranch(s.Thread.BranchID)
	if err != nil {
		return model, err
	}
	for _, record := range records {
		runtimeEvent, ok := runtimeEmittedPayload(record.Event.Payload)
		if !ok {
			continue
		}
		if err := model.ApplyNamed(runtimeEvent.Name, runtimeEvent.Payload); err != nil {
			return model, err
		}
	}
	return model, nil
}

func runtimeEmittedPayload(payload any) (coresession.RuntimeEmitted, bool) {
	runtimeEvent, ok := payload.(coresession.RuntimeEmitted)
	if ok {
		return runtimeEvent, true
	}
	ptr, ok := payload.(*coresession.RuntimeEmitted)
	if ok && ptr != nil {
		return *ptr, true
	}
	return coresession.RuntimeEmitted{}, false
}

func renderSurfaceReadModel(model coreactivation.ReadModel) string {
	var b strings.Builder
	b.WriteString("Surface\n")
	writeSurfaceFocus(&b, model.Focus)
	writeSurfaceActive(&b, model.Active)
	writeSurfaceDiagnostics(&b, model.Diagnostics)
	writeSurfaceRecent(&b, model.Recent)
	return strings.TrimRight(b.String(), "\n")
}

func writeSurfaceFocus(b *strings.Builder, focus *coreactivation.FocusSummary) {
	b.WriteString("\nFocus\n")
	if focus == nil {
		b.WriteString("  none\n")
		return
	}
	if focus.Objective != "" {
		b.WriteString("  objective: ")
		b.WriteString(focus.Objective)
		b.WriteByte('\n')
	}
	if len(focus.Intents) > 0 {
		b.WriteString("  intents: ")
		b.WriteString(strings.Join(focus.Intents, ", "))
		b.WriteByte('\n')
	}
	if len(focus.Subjects) > 0 {
		b.WriteString("  subjects: ")
		b.WriteString(strings.Join(focus.Subjects, ", "))
		b.WriteByte('\n')
	}
	parts := []string{}
	if focus.Source != "" {
		parts = append(parts, "source="+string(focus.Source))
	}
	if focus.Confidence > 0 {
		parts = append(parts, fmt.Sprintf("confidence=%.2f", focus.Confidence))
	}
	if len(parts) > 0 {
		b.WriteString("  ")
		b.WriteString(strings.Join(parts, " "))
		b.WriteByte('\n')
	}
}

func writeSurfaceActive(b *strings.Builder, active coreactivation.ActiveSurface) {
	b.WriteString("\nActive\n")
	b.WriteString("  activation sets: ")
	b.WriteString(envExplainList(active.ActivationSets))
	b.WriteByte('\n')
	b.WriteString("  operations: ")
	b.WriteString(envExplainList(surfaceOperationRefs(active.Operations)))
	b.WriteByte('\n')
	b.WriteString("  operation sets: ")
	b.WriteString(envExplainList(active.OperationSets))
	b.WriteByte('\n')
	b.WriteString("  context providers: ")
	b.WriteString(envExplainList(surfaceContextRefs(active.ContextProviders)))
	b.WriteByte('\n')
	b.WriteString("  datasources: ")
	b.WriteString(envExplainList(surfaceDatasourceRefs(active.Datasources)))
	b.WriteByte('\n')
	b.WriteString("  skills: ")
	b.WriteString(envExplainList(surfaceSkillRefs(active.Skills)))
	b.WriteByte('\n')
	b.WriteString("  references: ")
	b.WriteString(envExplainList(surfaceReferenceTargets(active.References)))
	b.WriteByte('\n')
	b.WriteString("  inline context: ")
	b.WriteString(envExplainList(active.InlineContexts))
	b.WriteByte('\n')
	if active.Lifetime != "" {
		b.WriteString("  duration: ")
		b.WriteString(string(active.Lifetime))
		b.WriteByte('\n')
	}
}

func writeSurfaceDiagnostics(b *strings.Builder, diagnostics []coreactivation.Diagnostic) {
	b.WriteString("\nDiagnostics\n")
	if len(diagnostics) == 0 {
		b.WriteString("  none\n")
		return
	}
	for _, diagnostic := range diagnostics {
		parts := []string{}
		if diagnostic.Term != "" {
			parts = append(parts, "term="+diagnostic.Term)
		}
		if diagnostic.Target != "" {
			parts = append(parts, "target="+diagnostic.Target)
		}
		if diagnostic.Reason != "" {
			parts = append(parts, "reason="+diagnostic.Reason)
		}
		writeEnvExplainLine(b, firstNonEmptyString(diagnostic.Message, "diagnostic"), parts)
	}
}

func writeSurfaceRecent(b *strings.Builder, recent []coreactivation.TraceEntry) {
	b.WriteString("\nRecent\n")
	if len(recent) == 0 {
		b.WriteString("  none\n")
		return
	}
	for _, item := range recent {
		parts := []string{}
		if item.Source != "" {
			parts = append(parts, "source="+string(item.Source))
		}
		writeEnvExplainLine(b, firstNonEmptyString(item.Summary, string(item.Event)), parts)
	}
}

func surfaceOperationRefs(refs []operation.Ref) []string {
	out := make([]string, 0, len(refs))
	for _, ref := range refs {
		if value := ref.String(); value != "" {
			out = append(out, value)
		}
	}
	return out
}

func surfaceContextRefs(refs []corecontext.ProviderRef) []string {
	out := make([]string, 0, len(refs))
	for _, ref := range refs {
		if ref.Name != "" {
			out = append(out, string(ref.Name))
		}
	}
	return out
}

func surfaceDatasourceRefs(refs []coredatasource.Ref) []string {
	out := make([]string, 0, len(refs))
	for _, ref := range refs {
		if ref.Name != "" {
			out = append(out, string(ref.Name))
		}
	}
	return out
}

func surfaceSkillRefs(refs []coreskill.Ref) []string {
	out := make([]string, 0, len(refs))
	for _, ref := range refs {
		if ref.Name != "" {
			out = append(out, string(ref.Name))
		}
	}
	return out
}

func surfaceReferenceTargets(refs []coreactivation.ReferenceTarget) []string {
	out := make([]string, 0, len(refs))
	for _, ref := range refs {
		if ref.Skill.Name != "" && ref.Path != "" {
			out = append(out, string(ref.Skill.Name)+":"+ref.Path)
		}
	}
	return out
}

func renderEnvExplain(data envExplainData) string {
	var b strings.Builder
	b.WriteString("Environment\n")
	writeEnvExplainObservers(&b, data.Observers)
	writeEnvExplainDerivers(&b, data.AssertionDerivers)
	writeEnvExplainRules(&b, data.ReactionRules)
	writeEnvExplainObservations(&b, data.Observations)
	writeEnvExplainAssertions(&b, data.Assertions)
	writeEnvExplainMatching(&b, data.Matching)
	writeEnvExplainActive(&b, data.Active, data.AppliedReactions)
	writeEnvExplainApplied(&b, data.Applied)
	return strings.TrimRight(b.String(), "\n")
}

func writeEnvExplainObservers(b *strings.Builder, observers []coreevidence.ObserverSpec) {
	b.WriteString("\nObservers\n")
	if len(observers) == 0 {
		b.WriteString("  none\n")
		return
	}
	for _, observer := range observers {
		parts := []string{}
		if observer.Phase != "" {
			parts = append(parts, "phase="+string(observer.Phase))
		}
		if observer.Environment.Name != "" {
			parts = append(parts, "env="+string(observer.Environment.Name))
		}
		if observer.Dynamic {
			parts = append(parts, "dynamic")
		}
		if observer.Disabled {
			parts = append(parts, "disabled")
		}
		if len(observer.ObservableKinds) > 0 {
			parts = append(parts, "kinds="+strings.Join(observer.ObservableKinds, ","))
		}
		writeEnvExplainLine(b, observer.Name, parts)
	}
}

func writeEnvExplainDerivers(b *strings.Builder, derivers []coreevidence.AssertionDeriverSpec) {
	b.WriteString("\nAssertion Derivers\n")
	if len(derivers) == 0 {
		b.WriteString("  none\n")
		return
	}
	for _, deriver := range derivers {
		parts := []string{}
		if len(deriver.ObservationKinds) > 0 {
			parts = append(parts, "from="+strings.Join(deriver.ObservationKinds, ","))
		}
		if len(deriver.Assertions) > 0 {
			parts = append(parts, "assertions="+strings.Join(assertionTemplateNames(deriver.Assertions), ","))
		} else {
			parts = append(parts, "assertions=custom")
		}
		writeEnvExplainLine(b, deriver.Name, parts)
	}
}

func writeEnvExplainRules(b *strings.Builder, rules []string) {
	b.WriteString("\nReaction Rules\n")
	if len(rules) == 0 {
		b.WriteString("  none\n")
		return
	}
	for _, rule := range rules {
		b.WriteString("  - ")
		b.WriteString(rule)
		b.WriteByte('\n')
	}
}

func writeEnvExplainObservations(b *strings.Builder, observations []envExplainObservation) {
	b.WriteString("\nObservations\n")
	if len(observations) == 0 {
		b.WriteString("  none\n")
		return
	}
	for _, observation := range observations {
		parts := []string{}
		if observation.ID != "" {
			parts = append(parts, "id="+observation.ID)
		}
		if observation.Environment != "" {
			parts = append(parts, "env="+observation.Environment)
		}
		if observation.Scope != "" {
			parts = append(parts, "scope="+observation.Scope)
		}
		if observation.Source != "" {
			parts = append(parts, "source="+observation.Source)
		}
		if observation.Content != "" {
			parts = append(parts, "content="+observation.Content)
		}
		writeEnvExplainLine(b, observation.Kind, parts)
	}
}

func writeEnvExplainAssertions(b *strings.Builder, assertions []envExplainAssertion) {
	b.WriteString("\nAssertions\n")
	if len(assertions) == 0 {
		b.WriteString("  none\n")
		return
	}
	for _, assertion := range assertions {
		parts := []string{}
		if assertion.Target != "" {
			parts = append(parts, "target="+assertion.Target)
		}
		if subject := renderEnvExplainSubject(assertion.Subject); subject != "" {
			parts = append(parts, "subject="+subject)
		}
		if assertion.Environment != "" {
			parts = append(parts, "env="+assertion.Environment)
		}
		if assertion.Scope != "" {
			parts = append(parts, "scope="+assertion.Scope)
		}
		if assertion.Source != "" {
			parts = append(parts, "source="+assertion.Source)
		}
		if len(assertion.ObservationIDs) > 0 {
			parts = append(parts, "observations="+strings.Join(assertion.ObservationIDs, ","))
		}
		writeEnvExplainLine(b, assertion.Kind, parts)
	}
}

func writeEnvExplainMatching(b *strings.Builder, matches []envExplainReactionMatch) {
	b.WriteString("\nMatching Reactions\n")
	if len(matches) == 0 {
		b.WriteString("  none\n")
		return
	}
	for _, match := range matches {
		parts := []string{}
		if match.Status != "" {
			parts = append(parts, "status="+match.Status)
		}
		if match.Reason != "" {
			parts = append(parts, "reason="+match.Reason)
		}
		if match.Action != "" {
			parts = append(parts, "action="+match.Action)
		}
		if match.Target != "" {
			parts = append(parts, "target="+match.Target)
		}
		if match.Assertion != "" {
			assertion := match.Assertion
			if match.AssertionTarget != "" {
				assertion += ":" + match.AssertionTarget
			}
			parts = append(parts, "assertion="+assertion)
		}
		if subject := renderEnvExplainSubject(match.AssertionSubject); subject != "" {
			parts = append(parts, "subject="+subject)
		}
		if len(match.ObservationIDs) > 0 {
			parts = append(parts, "observations="+strings.Join(match.ObservationIDs, ","))
		}
		writeEnvExplainLine(b, match.Rule, parts)
	}
}

func writeEnvExplainActive(b *strings.Builder, active envExplainActive, applied int) {
	b.WriteString("\nActive\n")
	b.WriteString("  activation sets: ")
	b.WriteString(envExplainList(active.ActivationSets))
	b.WriteByte('\n')
	b.WriteString("  operations: ")
	b.WriteString(envExplainList(active.Operations))
	b.WriteByte('\n')
	b.WriteString("  operation sets: ")
	b.WriteString(envExplainList(active.OperationSets))
	b.WriteByte('\n')
	b.WriteString("  datasources: ")
	b.WriteString(envExplainList(active.Datasources))
	b.WriteByte('\n')
	b.WriteString("  context providers: ")
	b.WriteString(envExplainList(active.ContextProviders))
	b.WriteByte('\n')
	b.WriteString("  inline context: ")
	b.WriteString(envExplainList(active.InlineContexts))
	b.WriteByte('\n')
	b.WriteString("  applied reactions: ")
	b.WriteString(strconv.Itoa(applied))
	b.WriteByte('\n')
}

func writeEnvExplainApplied(b *strings.Builder, applied []envExplainApplied) {
	b.WriteString("\nApplied Reactions\n")
	if len(applied) == 0 {
		b.WriteString("  none\n")
		return
	}
	for _, item := range applied {
		parts := []string{}
		if item.Action != "" {
			parts = append(parts, "action="+item.Action)
		}
		if item.Target != "" {
			parts = append(parts, "target="+item.Target)
		}
		if item.Assertion != "" {
			assertion := item.Assertion
			if item.AssertionTarget != "" {
				assertion += ":" + item.AssertionTarget
			}
			parts = append(parts, "assertion="+assertion)
		}
		if subject := renderEnvExplainSubject(item.AssertionSubject); subject != "" {
			parts = append(parts, "subject="+subject)
		}
		if len(item.ObservationIDs) > 0 {
			parts = append(parts, "observations="+strings.Join(item.ObservationIDs, ","))
		}
		writeEnvExplainLine(b, item.Rule, parts)
	}
}

func writeEnvExplainLine(b *strings.Builder, name string, parts []string) {
	b.WriteString("  - ")
	b.WriteString(name)
	if len(parts) > 0 {
		b.WriteString(" (")
		b.WriteString(strings.Join(parts, "; "))
		b.WriteByte(')')
	}
	b.WriteByte('\n')
}

func explainAppliedReactions(in []corereaction.ActionApplied) []envExplainApplied {
	out := make([]envExplainApplied, 0, len(in))
	for _, applied := range in {
		out = append(out, envExplainApplied{
			Rule:            applied.Rule,
			Action:          string(applied.Action),
			Target:          applied.Target,
			Assertion:       applied.Assertion,
			AssertionTarget: applied.AssertionTarget,
			AssertionSubject: envExplainSubject{
				Kind: strings.TrimSpace(applied.AssertionSubjectKind),
				Name: strings.TrimSpace(applied.AssertionSubjectName),
				ID:   strings.TrimSpace(applied.AssertionSubjectID),
			},
			AssertionScope:  applied.AssertionScope,
			AssertionSource: applied.AssertionSource,
			ObservationIDs:  append([]string(nil), applied.ObservationIDs...),
		})
	}
	return out
}

func explainObservations(in []coreevidence.Observation) []envExplainObservation {
	out := make([]envExplainObservation, 0, len(in))
	for _, observation := range in {
		kind := strings.TrimSpace(observation.Kind)
		if kind == "" {
			continue
		}
		item := envExplainObservation{
			ID:          strings.TrimSpace(observation.ID),
			Kind:        kind,
			Source:      strings.TrimSpace(observation.Source),
			Scope:       strings.TrimSpace(observation.Scope),
			Environment: strings.TrimSpace(string(observation.Environment.Name)),
			Content:     summarizeEnvExplainContent(observation.Content),
			Metadata:    cloneObservationMetadata(observation.Metadata),
		}
		out = append(out, item)
	}
	return out
}

func explainAssertions(in []coreevidence.Assertion) []envExplainAssertion {
	out := make([]envExplainAssertion, 0, len(in))
	for _, assertion := range in {
		kind := strings.TrimSpace(assertion.Kind)
		if kind == "" {
			continue
		}
		out = append(out, envExplainAssertion{
			Kind:           kind,
			Target:         strings.TrimSpace(assertion.Target),
			Subject:        explainSubject(assertion.Subject),
			Scope:          strings.TrimSpace(assertion.Scope),
			Source:         strings.TrimSpace(assertion.Source),
			Environment:    strings.TrimSpace(string(assertion.Environment.Name)),
			Confidence:     assertion.Confidence,
			ObservationIDs: append([]string(nil), assertion.ObservationIDs...),
			Metadata:       cloneEnvExplainAssertionMetadata(assertion.Metadata),
		})
	}
	return out
}

func explainSubject(subject coreevidence.Subject) envExplainSubject {
	return envExplainSubject{
		Kind: strings.TrimSpace(string(subject.Kind)),
		Name: strings.TrimSpace(subject.Name),
		ID:   strings.TrimSpace(subject.ID),
	}
}

func renderEnvExplainSubject(subject envExplainSubject) string {
	parts := []string{}
	if subject.Kind != "" {
		parts = append(parts, subject.Kind)
	}
	if subject.Name != "" {
		parts = append(parts, subject.Name)
	}
	if subject.ID != "" {
		parts = append(parts, subject.ID)
	}
	return strings.Join(parts, "/")
}

func explainReactionMatches(plan runtimereaction.Result, rules []corereaction.Rule, assertions []coreevidence.Assertion) []envExplainReactionMatch {
	out := make([]envExplainReactionMatch, 0, len(plan.Planned)+len(plan.Skipped)+len(rules))
	matchedRules := map[string]bool{}
	for _, planned := range plan.Planned {
		matchedRules[planned.Rule] = true
		out = append(out, envExplainReactionMatch{
			Rule:             planned.Rule,
			Action:           string(planned.Action.Kind),
			Target:           envExplainReactionActionTarget(planned.Action),
			Assertion:        planned.Assertion.Kind,
			AssertionTarget:  planned.Assertion.Target,
			AssertionSubject: explainSubject(planned.Assertion.Subject),
			ObservationIDs:   append([]string(nil), planned.Assertion.ObservationIDs...),
			Status:           "planned",
		})
	}
	for _, skipped := range plan.Skipped {
		matchedRules[skipped.Rule] = true
		out = append(out, envExplainReactionMatch{
			Rule:             skipped.Rule,
			Action:           string(skipped.Action.Kind),
			Target:           envExplainReactionActionTarget(skipped.Action),
			Assertion:        skipped.Assertion.Kind,
			AssertionTarget:  skipped.Assertion.Target,
			AssertionSubject: explainSubject(skipped.Assertion.Subject),
			ObservationIDs:   append([]string(nil), skipped.Assertion.ObservationIDs...),
			Status:           "skipped",
			Reason:           skipped.Reason,
		})
	}
	for _, rule := range rules {
		if matchedRules[rule.Name] {
			continue
		}
		item := envExplainReactionMatch{
			Rule:             rule.Name,
			Assertion:        rule.When.Assertion,
			AssertionTarget:  rule.When.Target,
			AssertionSubject: explainSubject(rule.When.Subject),
			Status:           "unmatched",
			Reason:           unmatchedReactionReason(rule, assertions),
		}
		out = append(out, item)
	}
	return out
}

func unmatchedReactionReason(rule corereaction.Rule, assertions []coreevidence.Assertion) string {
	if err := rule.Validate(); err != nil {
		return err.Error()
	}
	if len(assertions) == 0 {
		return "no_assertions"
	}
	return "no_matching_assertion"
}

func envExplainReactionActionTarget(action corereaction.Action) string {
	switch action.Kind {
	case corereaction.ActionActivateSkill:
		return string(action.Skill.Name)
	case corereaction.ActionActivateReference:
		if action.Reference.Path == "" {
			return string(action.Reference.Skill.Name)
		}
		return string(action.Reference.Skill.Name) + ":" + action.Reference.Path
	case corereaction.ActionEnableActivationSet:
		return action.ActivationSet
	case corereaction.ActionEnableOperationSet:
		return action.OperationSet
	case corereaction.ActionEnableDatasource:
		return string(action.Datasource.Name)
	case corereaction.ActionEnableContext:
		return string(action.ContextProvider.Name)
	case corereaction.ActionRunWorkflow:
		return string(action.Workflow.Name)
	case corereaction.ActionRunOperation:
		return action.Operation.Operation.String()
	case corereaction.ActionRunCommand:
		return action.Command.Path.String()
	default:
		return ""
	}
}

func summarizeEnvExplainContent(content any) string {
	if content == nil {
		return ""
	}
	switch typed := content.(type) {
	case string:
		return trimEnvExplainContent(typed)
	default:
		data, err := json.Marshal(typed)
		if err != nil {
			return trimEnvExplainContent(fmt.Sprint(typed))
		}
		return trimEnvExplainContent(string(data))
	}
}

func trimEnvExplainContent(value string) string {
	value = strings.TrimSpace(value)
	if len(value) <= 160 {
		return value
	}
	return value[:157] + "..."
}

func cloneObservationMetadata(in map[string]any) map[string]any {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]any, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func cloneEnvExplainAssertionMetadata(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func assertionTemplateNames(templates []coreevidence.AssertionTemplate) []string {
	out := make([]string, 0, len(templates))
	for _, assertion := range templates {
		name := strings.TrimSpace(assertion.Kind)
		if assertion.Target != "" {
			name += ":" + strings.TrimSpace(assertion.Target)
		}
		if name != "" {
			out = append(out, name)
		}
	}
	return out
}

func envExplainList(values []string) string {
	if len(values) == 0 {
		return "none"
	}
	return strings.Join(values, ", ")
}

func (s Session) previewContext(ctx context.Context, input contextPreviewInput, inbound channel.Inbound) (contextPreviewData, error) {
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
		Scope:    contextRequestScope(inbound),
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

func environmentObserverSpecs(observers []runtimeevidence.Observer) []coreevidence.ObserverSpec {
	if len(observers) == 0 {
		return nil
	}
	out := make([]coreevidence.ObserverSpec, 0, len(observers))
	for _, observer := range observers {
		if observer == nil {
			continue
		}
		out = append(out, observer.Spec())
	}
	return out
}

func assertionDeriverSpecs(derivers []runtimeevidence.AssertionDeriver) []coreevidence.AssertionDeriverSpec {
	if len(derivers) == 0 {
		return nil
	}
	out := make([]coreevidence.AssertionDeriverSpec, 0, len(derivers))
	for _, deriver := range derivers {
		if deriver == nil {
			continue
		}
		out = append(out, deriver.Spec())
	}
	return out
}

func reactionRuleNames(rules []corereaction.Rule) []string {
	if len(rules) == 0 {
		return nil
	}
	out := make([]string, 0, len(rules))
	for _, rule := range rules {
		if name := strings.TrimSpace(rule.Name); name != "" {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

func explainActiveState(active sessionenv.ActiveState) envExplainActive {
	out := envExplainActive{
		ActivationSets:   sortedBoolMapKeys(active.ActivationSets),
		Operations:       surfaceOperationRefs(sortedActiveOperationRefs(active.Operations)),
		OperationSets:    sortedBoolMapKeys(active.OperationSets),
		Datasources:      sortedNameBoolMapKeys(active.Datasources),
		ContextProviders: sortedNameBoolMapKeys(active.ContextProviders),
		InlineContexts:   sortedInlineContextMapKeys(active.InlineContexts),
	}
	return out
}

func sortedActiveOperationRefs(values map[operation.Ref]bool) []operation.Ref {
	var out []operation.Ref
	for ref, active := range values {
		if active && strings.TrimSpace(string(ref.Name)) != "" {
			out = append(out, ref)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].String() < out[j].String() })
	return out
}

func sortedInlineContextMapKeys(values map[string]corecontext.Block) []string {
	var out []string
	for id := range values {
		if strings.TrimSpace(id) != "" {
			out = append(out, id)
		}
	}
	sort.Strings(out)
	return out
}

func sortedBoolMapKeys(values map[string]bool) []string {
	if len(values) == 0 {
		return nil
	}
	out := make([]string, 0, len(values))
	for key, active := range values {
		if active && strings.TrimSpace(key) != "" {
			out = append(out, key)
		}
	}
	sort.Strings(out)
	return out
}

func sortedNameBoolMapKeys[K ~string](values map[K]bool) []string {
	if len(values) == 0 {
		return nil
	}
	out := make([]string, 0, len(values))
	for key, active := range values {
		value := strings.TrimSpace(string(key))
		if active && value != "" {
			out = append(out, value)
		}
	}
	sort.Strings(out)
	return out
}

func (s Session) materializeContext(ctx context.Context, in innerTurnInput, pending []coreconversation.Item, observations []coreevidence.Observation, active sessionenv.ActiveState) (corecontext.BuildResult, []coreconversation.Item, error) {
	providers := s.contextProviders(active)
	if len(providers) == 0 {
		return corecontext.BuildResult{}, oneShotUserContextPending(in.Inbound, pending, corecontext.RenderTurn), nil
	}
	records, err := s.contextRenderRecords(ctx, in.LocalContextRecords)
	if err != nil {
		return corecontext.BuildResult{}, nil, err
	}
	renderCtx := s.contextProviderContext(in.BaseContext, observations, active)
	reason := contextRenderReason(pending, observations)
	result, err := sessionenv.BuildContext(providers, records, renderCtx, corecontext.BuildRequest{
		ThreadID:      string(s.Thread.ID),
		BranchID:      string(s.Thread.BranchID),
		TurnID:        in.ConversationTurnID,
		Reason:        reason,
		InputText:     inboundInputText(in.Inbound),
		RecentContext: recentContextExcerpt(derefItems(in.LocalTranscript), pending),
		Scope:         contextRequestScope(in.Inbound),
		Observations:  append([]coreevidence.Observation(nil), observations...),
		Previous:      records,
	})
	if err != nil {
		return corecontext.BuildResult{}, nil, err
	}
	projected := contextPendingItems(in.ProviderIdentity, pending, result)
	projected = oneShotUserContextPending(in.Inbound, projected, reason)
	return result, projected, nil
}

const inboundUserContextMetadataKey = "user_context"

func oneShotUserContextPending(inbound channel.Inbound, pending []coreconversation.Item, reason corecontext.RenderReason) []coreconversation.Item {
	out := append([]coreconversation.Item(nil), pending...)
	if reason != corecontext.RenderTurn || inbound.Message == nil || len(out) == 0 {
		return out
	}
	raw, ok := inbound.Message.Metadata[inboundUserContextMetadataKey]
	if !ok {
		return out
	}
	text := strings.TrimSpace(contextValueText(raw))
	if text == "" {
		return out
	}
	return addUserContextDiff(coreconversation.ProviderIdentity{}, out, text)
}

func inboundInputText(inbound channel.Inbound) string {
	if inbound.Message == nil {
		return ""
	}
	return strings.TrimSpace(contextValueText(inbound.Message.Content))
}

func recentContextExcerpt(local, pending []coreconversation.Item) string {
	items := append(append([]coreconversation.Item(nil), local...), pending...)
	if len(items) == 0 {
		return ""
	}
	const maxItems = 6
	const maxChars = 1200
	if len(items) > maxItems {
		items = items[len(items)-maxItems:]
	}
	var parts []string
	for _, item := range items {
		text := strings.TrimSpace(contextValueText(item.Content))
		if text == "" {
			continue
		}
		role := strings.TrimSpace(item.Role)
		if role == "" {
			role = string(item.Kind)
		}
		parts = append(parts, role+": "+text)
	}
	out := strings.TrimSpace(strings.Join(parts, "\n"))
	if len(out) > maxChars {
		return out[len(out)-maxChars:]
	}
	return out
}

func contextRequestScope(inbound channel.Inbound) map[string]string {
	out := map[string]string{}
	if inbound.Caller.Kind != "" {
		out["caller.kind"] = string(inbound.Caller.Kind)
	}
	if inbound.Caller.Principal.Kind != "" {
		out["caller.principal.kind"] = inbound.Caller.Principal.Kind
	}
	if inbound.Caller.Principal.ID != "" {
		out["caller.principal.id"] = inbound.Caller.Principal.ID
	}
	if inbound.Trust.Level != "" {
		out["trust.level"] = string(inbound.Trust.Level)
	}
	if inbound.Trust.Kind != "" {
		out["trust.kind"] = string(inbound.Trust.Kind)
	}
	if inbound.Trust.Downgraded {
		out["trust.downgraded"] = "true"
	}
	if inbound.Caller.Source != "" {
		out["caller.source"] = inbound.Caller.Source
	}
	if inbound.Actor != nil {
		out["user.resolution"] = string(user.NormalizeResolution(inbound.Actor.Resolution))
		if inbound.Actor.User.ID != "" {
			out["user.id"] = string(inbound.Actor.User.ID)
		}
		if inbound.Actor.User.Username != "" {
			out["user.username"] = inbound.Actor.User.Username
		}
		if inbound.Actor.Identity.Provider != "" {
			out["identity.provider"] = inbound.Actor.Identity.Provider
		}
		if inbound.Actor.Identity.ProviderID != "" {
			out["identity.provider_id"] = inbound.Actor.Identity.ProviderID
		}
		if groups := actorGroupIDs(*inbound.Actor); len(groups) > 0 {
			out["user.groups"] = strings.Join(groups, ",")
		}
		if emails := actorEmailLabels(*inbound.Actor); len(emails) > 0 {
			out["user.email.all"] = strings.Join(emails, ";")
		}
		if identities := actorIdentityLabels(*inbound.Actor); len(identities) > 0 {
			out["identity.all"] = strings.Join(identities, ";")
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func actorIdentityLabels(actor user.Actor) []string {
	seen := map[string]bool{}
	var out []string
	add := func(identity user.Identity) {
		provider := strings.TrimSpace(identity.Provider)
		id := strings.TrimSpace(identity.ProviderID)
		if provider == "" && id == "" {
			return
		}
		label := provider
		if provider != "" && id != "" {
			label = provider + ":" + id
		} else if id != "" {
			label = id
		}
		if label == "" || seen[label] {
			return
		}
		seen[label] = true
		out = append(out, label)
	}
	add(actor.Identity)
	for _, identity := range actor.Identities {
		add(identity)
	}
	for _, identity := range actor.User.Identities {
		add(identity)
	}
	return out
}

func actorEmailLabels(actor user.Actor) []string {
	var primary []string
	var aliases []string
	seen := map[string]bool{}
	add := func(email user.Email) {
		address := strings.ToLower(strings.TrimSpace(email.Address))
		if address == "" || !email.Verified || seen[address] {
			return
		}
		seen[address] = true
		label := address
		if email.Primary {
			label += " primary"
			primary = append(primary, label)
			return
		}
		aliases = append(aliases, label+" alias")
	}
	for _, email := range actor.User.Emails {
		add(email)
	}
	return append(primary, aliases...)
}

func actorGroupIDs(actor user.Actor) []string {
	seen := map[string]bool{}
	var out []string
	add := func(id string) {
		id = strings.TrimSpace(id)
		if id == "" || seen[id] {
			return
		}
		seen[id] = true
		out = append(out, id)
	}
	for _, id := range actor.User.Groups {
		add(string(id))
	}
	for _, group := range actor.Groups {
		add(string(group.ID))
	}
	sort.Strings(out)
	return out
}

func (s Session) contextProviders(active ...sessionenv.ActiveState) []corecontext.Provider {
	carrier, ok := s.Agent.(contextProviderAgent)
	var providers []corecontext.Provider
	if ok && carrier != nil {
		providers = carrier.ContextProviders()
	}
	if len(active) > 0 && len(active[0].InlineContexts) > 0 {
		providers = append(providers, inlineSurfaceContextProvider{blocks: active[0].InlineContexts})
	}
	if len(active) > 0 && s.shouldProjectSurfaceTools(active[0]) {
		providers = append(providers, surfaceSchemaContextProvider{session: s, active: active[0]})
	}
	if len(active) == 0 || len(active[0].ContextProviders) == 0 {
		return providers
	}
	seen := map[corecontext.ProviderName]bool{}
	for _, provider := range providers {
		if provider == nil {
			continue
		}
		seen[provider.Spec().Name] = true
	}
	for _, provider := range s.ContextProviders {
		if provider == nil {
			continue
		}
		name := provider.Spec().Name
		if !active[0].ContextProviders[name] || seen[name] {
			continue
		}
		providers = append(providers, provider)
		seen[name] = true
	}
	return providers
}

func (s Session) contextProviderContext(ctx context.Context, observations []coreevidence.Observation, active ...sessionenv.ActiveState) context.Context {
	cfg := s.envConfig(s.eventSink())
	if len(active) > 0 {
		cfg.Active = &active[0]
	}
	return sessionenv.ContextProviderContext(ctx, cfg, observations)
}

func contextRenderReason(pending []coreconversation.Item, observations []coreevidence.Observation) corecontext.RenderReason {
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
	events := make([]sessionenv.Event, 0, len(result.Added)+len(result.Updated)+len(result.Removed)+1)
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

func (s Session) transcriptForPending(ctx context.Context, pending transcriptPending, localItems []coreconversation.Item, localHandle *coreconversation.ContinuationHandle) (coreconversation.Transcript, error) {
	provider := s.providerIdentity()
	if s.ThreadStore == nil || s.Thread.ID == "" {
		committedCount := pending.CommittedCount
		if pending.Committed && committedCount == 0 {
			committedCount = len(pending.Items)
		}
		if committedCount < 0 {
			committedCount = 0
		}
		if committedCount > len(pending.Items) {
			committedCount = len(pending.Items)
		}
		uncommitted := pending.Items[committedCount:]
		all := append([]coreconversation.Item(nil), localItems...)
		all = append(all, uncommitted...)
		if err := conversationruntime.ValidateContinuity(all, conversationruntime.ValidateOptions{Provider: provider}); err != nil {
			return coreconversation.Transcript{}, err
		}
		newItems := uncommitted
		if localHandle != nil && localHandle.SupportsPreviousResponseID() {
			copied := *localHandle
			return coreconversation.Transcript{
				Provider:     provider,
				Items:        append([]coreconversation.Item(nil), pending.Items...),
				NewItems:     append([]coreconversation.Item(nil), newItems...),
				Continuation: &copied,
				Mode:         coreconversation.ProjectionNativeContinuation,
			}, nil
		}
		return coreconversation.Transcript{
			Provider: provider,
			Items:    all,
			NewItems: append([]coreconversation.Item(nil), newItems...),
			Mode:     coreconversation.ProjectionFullReplay,
		}, nil
	}
	snapshot, err := s.ThreadStore.Read(ensureContext(ctx), corethread.ReadParams{ID: s.Thread.ID})
	if err != nil {
		if errors.Is(err, corethread.ErrNotFound) {
			committedCount := pending.CommittedCount
			if pending.Committed && committedCount == 0 {
				committedCount = len(pending.Items)
			}
			if committedCount < 0 {
				committedCount = 0
			}
			if committedCount > len(pending.Items) {
				committedCount = len(pending.Items)
			}
			canonical := append([]coreconversation.Item(nil), pending.Items[committedCount:]...)
			if err := conversationruntime.ValidateContinuity(canonical, conversationruntime.ValidateOptions{Provider: provider}); err != nil {
				return coreconversation.Transcript{}, err
			}
			newItems := pending.Items[committedCount:]
			return coreconversation.Transcript{
				Provider: provider,
				Items:    canonical,
				NewItems: append([]coreconversation.Item(nil), newItems...),
				Mode:     coreconversation.ProjectionFullReplay,
			}, nil
		}
		return coreconversation.Transcript{}, err
	}
	projected, err := conversationruntime.Project(conversationruntime.ProjectionInput{
		Thread:                snapshot,
		BranchID:              s.Thread.BranchID,
		Provider:              provider,
		Pending:               pending.Items,
		PendingCommitted:      pending.Committed,
		PendingCommittedCount: pending.CommittedCount,
		Mode:                  coreconversation.ProjectionNativeContinuation,
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

type conversationEventSink struct {
	ctx         context.Context
	turnID      string
	errp        *error
	localItems  *[]coreconversation.Item
	localHandle **coreconversation.ContinuationHandle
	live        sessionenv.EventSink
	provider    coreconversation.ProviderIdentity
	thread      corethread.Ref
	items       []coreconversation.Item
	handles     []coreconversation.ContinuationHandle
}

func (s Session) conversationEventSink(ctx context.Context, turnID string, errp *error, localItems *[]coreconversation.Item, localHandle **coreconversation.ContinuationHandle) *conversationEventSink {
	return &conversationEventSink{
		ctx:         ctx,
		turnID:      turnID,
		errp:        errp,
		localItems:  localItems,
		localHandle: localHandle,
		live:        s.eventSink(),
		provider:    s.providerIdentity(),
		thread:      s.Thread,
	}
}

func (c *conversationEventSink) Emit(payload sessionenv.Event) {
	if payload == nil {
		return
	}
	c.live.Emit(payload)
	if c.errp != nil && *c.errp != nil {
		return
	}
	switch typed := payload.(type) {
	case coreconversation.ItemsAppended:
		if typed.TurnID == "" {
			typed.TurnID = c.turnID
		}
		if typed.Provider.Provider == "" {
			typed.Provider = c.provider
		}
		if c.provider.Provider == "" {
			c.provider = typed.Provider
		}
		c.items = append(c.items, typed.Items...)
	case *coreconversation.ItemsAppended:
		if typed == nil {
			return
		}
		copied := *typed
		if copied.TurnID == "" {
			copied.TurnID = c.turnID
		}
		if copied.Provider.Provider == "" {
			copied.Provider = c.provider
		}
		if c.provider.Provider == "" {
			c.provider = copied.Provider
		}
		c.items = append(c.items, copied.Items...)
	case coreconversation.ContinuationStored:
		if typed.TurnID == "" {
			typed.TurnID = c.turnID
		}
		if typed.Handle.BranchID == "" {
			typed.Handle.BranchID = c.thread.BranchID
		}
		if c.provider.Provider == "" {
			c.provider = typed.Handle.Provider
		}
		c.handles = append(c.handles, typed.Handle)
	case *coreconversation.ContinuationStored:
		if typed == nil {
			return
		}
		copied := *typed
		if copied.TurnID == "" {
			copied.TurnID = c.turnID
		}
		if copied.Handle.BranchID == "" {
			copied.Handle.BranchID = c.thread.BranchID
		}
		if c.provider.Provider == "" {
			c.provider = copied.Handle.Provider
		}
		c.handles = append(c.handles, copied.Handle)
	}
}

func (c *conversationEventSink) Flush(ctx context.Context, s Session, events ...sessionenv.Event) error {
	return c.FlushWithTranscriptItems(ctx, s, nil, events...)
}

func (c *conversationEventSink) FlushWithTranscriptItems(ctx context.Context, s Session, extraItems []coreconversation.Item, events ...sessionenv.Event) error {
	if c == nil {
		return s.appendThreadEvents(ctx, events...)
	}
	if c.errp != nil && *c.errp != nil {
		return *c.errp
	}
	if ctx == nil {
		ctx = c.ctx
	}
	items := append([]coreconversation.Item(nil), c.items...)
	items = append(items, extraItems...)
	if err := s.appendConversationAndEvents(ctx, c.turnID, c.provider, items, c.handles, events...); err != nil {
		if c.errp != nil {
			*c.errp = err
		}
		return err
	}
	if c.localItems != nil && len(items) > 0 {
		*c.localItems = append(*c.localItems, items...)
	}
	if c.localHandle != nil && len(c.handles) > 0 {
		copied := c.handles[len(c.handles)-1]
		*c.localHandle = &copied
	}
	c.items = nil
	c.handles = nil
	return nil
}

func (c *conversationEventSink) Items() []coreconversation.Item {
	if c == nil {
		return nil
	}
	return append([]coreconversation.Item(nil), c.items...)
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
		case sessionenv.InputReceived:
			if text := valueText(payload.Message.Content); text != "" {
				lines = append(lines, "User: "+text)
			}
		case sessionenv.OutboundProduced:
			if text := valueText(payload.Message.Content); text != "" {
				lines = append(lines, "Agent: "+text)
			}
		case sessionenv.CommandReceived:
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
		if err := s.appendThreadEvents(ctx, sessionenv.OutboundProduced{RunID: inbound.ID, Message: *outbound.Message}); err != nil {
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
		if err := s.appendThreadEvents(ctx, sessionenv.OutboundProduced{RunID: inbound.ID, Message: *outbound.Message}); err != nil {
			return inputFailed("thread_append_failed", err.Error(), nil)
		}
		return InputResult{Status: InputStatusOK, Agent: agentResult, Effect: lastEffect(effects), Effects: effects, Outbound: &outbound}
	case agent.DecisionNone, agent.DecisionWait:
		return InputResult{Status: InputStatusOK, Agent: agentResult, Effect: lastEffect(effects), Effects: effects}
	default:
		return InputResult{Status: InputStatusUnsupported, Agent: agentResult, Effect: lastEffect(effects), Effects: effects, Error: &CommandError{Code: "unsupported_agent_decision", Message: "agent decision is not supported by session input dispatch yet", Details: map[string]any{"decision": agentResult.Decision.Kind}}}
	}
}

func (s Session) applyAgentOperations(ctx context.Context, agentCtx operation.Context, inbound channel.Inbound, startIndex int, requests []agent.OperationRequest, active *sessionenv.ActiveState, provider coreconversation.ProviderIdentity) ([]environment.EffectResult, []coreconversation.Item, []sessionenv.OperationCompleted, error) {
	effects := make([]environment.EffectResult, 0, len(requests))
	toolResults := make([]coreconversation.Item, 0, len(requests))
	completedEvents := make([]sessionenv.OperationCompleted, 0, len(requests))
	for i, opReq := range requests {
		callID := operationCallID(inbound.ID, startIndex+i+1)
		requested := sessionenv.OperationRequested{
			RunID:     inbound.ID,
			CallID:    callID,
			Operation: opReq.Operation,
			Input:     opReq.Input,
		}
		if err := s.appendThreadEvents(ctx, requested); err != nil {
			return nil, nil, nil, err
		}
		s.emitLive(requested)
		effect := s.applyProjectedOperation(agentCtx, opReq.Operation, opReq.Input, callID)
		if effect.Observation.Metadata == nil {
			effect.Observation.Metadata = map[string]any{}
		}
		effect.Observation.Metadata["operation"] = opReq.Operation.String()
		effect.Observation.Metadata["call_id"] = string(callID)
		if opReq.ProviderCallID != "" {
			effect.Observation.Metadata["provider_call_id"] = opReq.ProviderCallID
		}
		if opReq.Operation.Name == "file_edit" {
			if editedPath, dryRun, ok := fileEditResultPath(effect.Result); ok {
				effect.Observation.Metadata["edited_path"] = editedPath
				effect.Observation.Metadata["edit_dry_run"] = dryRun
			}
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
		if active != nil {
			if preparedActive, ok := s.activeStateFromSurfaceToolResult(effect.Result); ok {
				*active = mergeActiveState(*active, preparedActive)
			}
		}
		completed := sessionenv.OperationCompleted{
			RunID:     inbound.ID,
			CallID:    callID,
			Operation: opReq.Operation,
			Result:    effect.Result,
		}
		completedEvents = append(completedEvents, completed)
		if strings.TrimSpace(opReq.ProviderCallID) == "" {
			continue
		}
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
	}
	return effects, toolResults, completedEvents, nil
}

func (s Session) validateOperationRequests(items []coreconversation.Item, requests []agent.OperationRequest) error {
	open := map[string]coreconversation.ToolCallRef{}
	for _, item := range items {
		switch item.Kind {
		case coreconversation.ItemOutput:
			for _, call := range item.ToolCallRefs() {
				callID := strings.TrimSpace(call.CallID)
				if callID != "" {
					if existing := open[callID]; existing.CallID != "" {
						return fmt.Errorf("assistant tool call %q is duplicated", callID)
					}
					open[callID] = call
				}
			}
		case coreconversation.ItemToolResult:
			delete(open, strings.TrimSpace(item.CallID))
		}
	}
	hasProviderRequest := len(open) > 0
	for _, request := range requests {
		if strings.TrimSpace(request.ProviderCallID) != "" {
			hasProviderRequest = true
			break
		}
	}
	if !hasProviderRequest {
		return nil
	}
	matched := map[string]bool{}
	for i, request := range requests {
		callID := strings.TrimSpace(request.ProviderCallID)
		if callID == "" {
			return fmt.Errorf("operation request %d for %s is missing provider call id", i, request.Operation.String())
		}
		_, ok := open[callID]
		if !ok {
			return fmt.Errorf("operation request %d for %s references provider call id %q without a durable open assistant tool call", i, request.Operation.String(), callID)
		}
		if matched[callID] {
			return fmt.Errorf("operation request %d for %s duplicates provider call id %q", i, request.Operation.String(), callID)
		}
		matched[callID] = true
	}
	for callID, call := range open {
		if !matched[callID] {
			name := strings.TrimSpace(call.Name)
			if name == "" {
				name = callID
			}
			return fmt.Errorf("assistant tool call %q has no matching operation request", name)
		}
	}
	return nil
}

func (s Session) applyPostEditChecks(ctx context.Context, agentCtx operation.Context, inbound channel.Inbound, startIndex int, editEffects []environment.EffectResult) ([]environment.EffectResult, []coreconversation.Item, error) {
	if len(s.PostEditChecks) == 0 || len(editEffects) == 0 {
		return nil, nil, nil
	}
	paths := postEditPaths(editEffects)
	if len(paths) == 0 {
		return nil, nil, nil
	}
	var effects []environment.EffectResult
	for _, editedPath := range paths {
		for _, check := range s.PostEditChecks {
			if !postEditCheckMatches(check, editedPath) {
				continue
			}
			callID := operationCallID(inbound.ID, startIndex+len(effects)+1)
			input := postEditCheckInput(check.Input, editedPath)
			requested := sessionenv.OperationRequested{
				RunID:     inbound.ID,
				CallID:    callID,
				Operation: check.Operation,
				Input:     input,
			}
			if err := s.appendThreadEvents(ctx, requested); err != nil {
				return nil, nil, err
			}
			s.emitLive(requested)
			effect := s.applyOperation(agentCtx, check.Operation, input, callID)
			effect.Result = annotatePostEditCheckResult(check, editedPath, effect.Result)
			effect.Observation.Content = effect.Result
			if effect.Observation.Metadata == nil {
				effect.Observation.Metadata = map[string]any{}
			}
			effect.Observation.Metadata["operation"] = check.Operation.String()
			effect.Observation.Metadata["call_id"] = string(callID)
			effect.Observation.Metadata["post_edit_check"] = check.Name
			effect.Observation.Metadata["edited_path"] = editedPath
			effect, replacementErr := replaceOversizedToolResult(agentCtx, effect, check.Operation, callID)
			if replacementErr != nil {
				effect = operationEffect(operation.Failed("post_edit_check_result_replacement_failed", replacementErr.Error(), map[string]any{
					"operation":       check.Operation.String(),
					"call_id":         string(callID),
					"post_edit_check": check.Name,
					"edited_path":     editedPath,
				}))
				effect.Observation.Metadata = map[string]any{
					"operation":       check.Operation.String(),
					"call_id":         string(callID),
					"post_edit_check": check.Name,
					"edited_path":     editedPath,
				}
			}
			effects = append(effects, effect)
			completed := sessionenv.OperationCompleted{
				RunID:     inbound.ID,
				CallID:    callID,
				Operation: check.Operation,
				Result:    effect.Result,
			}
			if err := s.appendThreadEvents(ctx, completed); err != nil {
				return nil, nil, err
			}
			s.emitLive(completed)
		}
	}
	return effects, nil, nil
}

func postEditPaths(effects []environment.EffectResult) []string {
	seen := map[string]bool{}
	var out []string
	for _, effect := range effects {
		if effect.Result.Status != operation.StatusOK {
			continue
		}
		if name, _ := effect.Observation.Metadata["operation"].(string); name != "file_edit" {
			continue
		}
		editedPath, _ := effect.Observation.Metadata["edited_path"].(string)
		dryRun, _ := effect.Observation.Metadata["edit_dry_run"].(bool)
		if editedPath == "" {
			var ok bool
			editedPath, dryRun, ok = fileEditResultPath(effect.Result)
			if !ok {
				continue
			}
		}
		if dryRun {
			continue
		}
		editedPath = strings.TrimSpace(editedPath)
		if editedPath == "" || seen[editedPath] {
			continue
		}
		seen[editedPath] = true
		out = append(out, editedPath)
	}
	return out
}

func fileEditResultPath(result operation.Result) (string, bool, bool) {
	if result.Status != operation.StatusOK {
		return "", false, false
	}
	rendered, ok := result.Output.(operation.Rendered)
	if !ok {
		return "", false, false
	}
	data, ok := rendered.Data.(map[string]any)
	if !ok {
		return "", false, false
	}
	editedPath, _ := data["path"].(string)
	dryRun, _ := data["dry_run"].(bool)
	editedPath = strings.TrimSpace(editedPath)
	return editedPath, dryRun, editedPath != ""
}

func postEditCheckMatches(check coresession.PostEditCheckSpec, editedPath string) bool {
	if len(check.MatchPaths) == 0 {
		return true
	}
	for _, pattern := range check.MatchPaths {
		pattern = strings.TrimSpace(pattern)
		if pattern == "" {
			continue
		}
		if ok, err := path.Match(pattern, editedPath); err == nil && ok {
			return true
		}
		if ok, err := path.Match(pattern, path.Base(editedPath)); err == nil && ok {
			return true
		}
	}
	return false
}

func postEditCheckInput(input operation.Value, editedPath string) operation.Value {
	if input == nil {
		return map[string]any{"path": editedPath}
	}
	dir := path.Dir(editedPath)
	if dir == "." {
		dir = ""
	}
	values := map[string]string{
		"path": editedPath,
		"dir":  dir,
		"base": path.Base(editedPath),
	}
	return expandPostEditValue(input, values)
}

func expandPostEditValue(value any, values map[string]string) any {
	switch typed := value.(type) {
	case string:
		out := typed
		for key, replacement := range values {
			out = strings.ReplaceAll(out, "${"+key+"}", replacement)
		}
		return out
	case []any:
		out := make([]any, len(typed))
		for i, item := range typed {
			out[i] = expandPostEditValue(item, values)
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(typed))
		for key, item := range typed {
			out[key] = expandPostEditValue(item, values)
		}
		return out
	default:
		return value
	}
}

func annotatePostEditCheckResult(check coresession.PostEditCheckSpec, editedPath string, result operation.Result) operation.Result {
	prefix := fmt.Sprintf("Post-edit check %s ran for %s.", check.Name, editedPath)
	if check.Mode == coresession.PostEditCheckModeFix {
		prefix = fmt.Sprintf("Post-edit check %s may have auto-applied fixes for %s.", check.Name, editedPath)
	}
	if rendered, ok := result.Output.(operation.Rendered); ok {
		rendered.Text = strings.TrimSpace(prefix + "\n\n" + strings.TrimSpace(rendered.Text))
		rendered.Model = strings.TrimSpace(prefix + "\n\n" + strings.TrimSpace(rendered.ModelText()))
		if data, ok := rendered.Data.(map[string]any); ok {
			copied := make(map[string]any, len(data)+2)
			for key, value := range data {
				copied[key] = value
			}
			copied["post_edit_check"] = check.Name
			copied["edited_path"] = editedPath
			rendered.Data = copied
		}
		result.Output = rendered
		return result
	}
	if result.Status == operation.StatusOK {
		result.Output = operation.Rendered{
			Text:  prefix,
			Model: prefix,
			Data: map[string]any{
				"post_edit_check": check.Name,
				"edited_path":     editedPath,
				"output":          result.Output,
			},
		}
	}
	return result
}

func (s Session) applyProjectedOperation(ctx operation.Context, ref operation.Ref, input operation.Value, callID operation.CallID) environment.EffectResult {
	tools := s.TurnTools
	if active, ok := sessionenv.ActiveStateFromContext(ctx); ok {
		tools = s.turnTools(active)
	}
	if tools != nil && !operationProjected(tools, ref) {
		return operationEffect(operation.Failed("operation_not_projected", "operation was not projected for this turn authority", map[string]any{
			"operation": ref.String(),
		}))
	}
	return s.applyOperation(ctx, ref, input, callID)
}

func (s Session) turnTools(active sessionenv.ActiveState) []tool.Spec {
	out := append([]tool.Spec(nil), s.TurnTools...)
	if s.shouldProjectSurfaceTools(active) {
		for _, projected := range s.surfaceToolSpecs() {
			if !toolProjected(out, projected) {
				out = append(out, projected)
			}
		}
	}
	for _, projected := range s.activeOperationTools(active) {
		if !toolProjected(out, projected) {
			out = append(out, projected)
		}
	}
	for _, projected := range s.activeOperationSetTools(active) {
		if !toolProjected(out, projected) {
			out = append(out, projected)
		}
	}
	if out == nil && s.TurnTools != nil {
		return []tool.Spec{}
	}
	return out
}

func (s Session) shouldProjectSurfaceTools(active sessionenv.ActiveState) bool {
	return len(s.ActivationSets) > 0 ||
		len(active.ActivationSets) > 0 ||
		len(active.Operations) > 0 ||
		len(active.OperationSets) > 0 ||
		len(active.ContextProviders) > 0 ||
		len(active.Datasources) > 0 ||
		len(active.InlineContexts) > 0
}

func (s Session) activeOperationTools(active sessionenv.ActiveState) []tool.Spec {
	if len(active.Operations) == 0 {
		return nil
	}
	var refs []operation.Ref
	for ref, enabled := range active.Operations {
		if enabled && strings.TrimSpace(string(ref.Name)) != "" {
			refs = append(refs, ref)
		}
	}
	sort.Slice(refs, func(i, j int) bool { return refs[i].String() < refs[j].String() })
	var out []tool.Spec
	for _, ref := range refs {
		for _, projected := range s.operationTools(ref) {
			if !toolProjected(out, projected) {
				out = append(out, projected)
			}
		}
	}
	return out
}

func (s Session) activeOperationSetTools(active sessionenv.ActiveState) []tool.Spec {
	if len(active.OperationSets) == 0 || len(s.OperationSets) == 0 {
		return nil
	}
	sets := map[string]operation.Set{}
	for _, set := range s.OperationSets {
		if strings.TrimSpace(set.Name) != "" {
			sets[set.Name] = set
		}
	}
	var out []tool.Spec
	for name, enabled := range active.OperationSets {
		if !enabled {
			continue
		}
		set, ok := sets[name]
		if !ok {
			continue
		}
		for _, ref := range set.Operations {
			for _, projected := range s.operationTools(ref) {
				if !toolProjected(out, projected) {
					out = append(out, projected)
				}
			}
		}
	}
	return out
}

func (s Session) operationTools(ref operation.Ref) []tool.Spec {
	if !operation.HasSelectorMeta(ref) {
		projected, ok := s.operationTool(ref)
		if !ok {
			return nil
		}
		return []tool.Spec{projected}
	}
	var out []tool.Spec
	seen := map[operation.Name]bool{}
	for _, op := range s.matchingOperations(ref) {
		if op == nil {
			continue
		}
		spec := op.Spec()
		if seen[spec.Ref.Name] {
			continue
		}
		seen[spec.Ref.Name] = true
		out = append(out, operationToolFromSpec(spec))
	}
	return out
}

func (s Session) matchingOperations(selector operation.Ref) []operation.Operation {
	var out []operation.Operation
	if len(s.OperationCatalog) > 0 {
		keys := make([]string, 0, len(s.OperationCatalog))
		for key := range s.OperationCatalog {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			op := s.OperationCatalog[key].Operation
			if op != nil && selector.Matches(op.Spec().Ref) {
				out = append(out, op)
			}
		}
	}
	if s.Operations != nil {
		ops := s.Operations.All()
		sort.Slice(ops, func(i, j int) bool {
			return ops[i].Spec().Ref.String() < ops[j].Spec().Ref.String()
		})
		for _, op := range ops {
			if op != nil && selector.Matches(op.Spec().Ref) {
				out = append(out, op)
			}
		}
	}
	return out
}

func (s Session) operationTool(ref operation.Ref) (tool.Spec, bool) {
	op, ok := s.resolveOperation(ref)
	if !ok || op == nil {
		return tool.Spec{}, false
	}
	return operationToolFromSpec(op.Spec()), true
}

func operationToolFromSpec(spec operation.Spec) tool.Spec {
	return tool.Spec{
		Name:        tool.Name(spec.Ref.Name),
		Description: spec.Description,
		Target: invocation.Target{
			Kind:      invocation.TargetOperation,
			Operation: spec.Ref,
		},
		Input:     spec.Input,
		Output:    spec.Output,
		Semantics: spec.Semantics,
		Annotations: map[string]string{
			"projection": "reaction_operation_set",
		},
	}
}

func (s Session) resolveOperation(ref operation.Ref) (operation.Operation, bool) {
	if len(s.OperationCatalog) > 0 {
		binding, err := s.OperationCatalog.Resolve(ref.String(), sessioncontrol.ResourceID{})
		if err == nil {
			return binding.Operation, true
		}
	}
	if s.Operations != nil {
		return s.Operations.Resolve(ref)
	}
	return nil, false
}

func toolProjected(tools []tool.Spec, candidate tool.Spec) bool {
	for _, existing := range tools {
		if existing.Name != "" && existing.Name == candidate.Name {
			return true
		}
		if existing.Target.Kind == invocation.TargetOperation && candidate.Target.Kind == invocation.TargetOperation && operationRefEqual(existing.Target.Operation, candidate.Target.Operation) {
			return true
		}
	}
	return false
}

func operationProjected(tools []tool.Spec, ref operation.Ref) bool {
	for _, spec := range tools {
		if spec.Target.Kind == invocation.TargetOperation && operationRefEqual(spec.Target.Operation, ref) {
			return true
		}
		if spec.Dispatch != nil {
			for _, candidate := range spec.Dispatch.Cases {
				if candidate.Target.Kind == invocation.TargetOperation && operationRefEqual(candidate.Target.Operation, ref) {
					return true
				}
			}
		}
	}
	return false
}

func operationRefEqual(a, b operation.Ref) bool {
	return a.Name == b.Name && a.Version == b.Version
}

func replaceOversizedToolResult(ctx operation.Context, effect environment.EffectResult, ref operation.Ref, callID operation.CallID) (environment.EffectResult, error) {
	result, replacement, err := sessionenv.ReplaceLargeResult(ctx, effect.Result, ref, callID)
	if err != nil || replacement == nil {
		return effect, err
	}
	metadata := effect.Observation.Metadata
	effect = operationEffect(result)
	effect.Observation.Metadata = metadata
	return effect, nil
}

func toolResultReplacement(result operation.Result) (sessionenv.ResultReplacement, bool) {
	if rendered, ok := result.Output.(operation.Rendered); ok {
		if replacement, ok := rendered.Data.(sessionenv.ResultReplacement); ok && replacement.Replaced {
			return replacement, true
		}
	}
	if result.Error == nil || result.Error.Details == nil {
		return sessionenv.ResultReplacement{}, false
	}
	replacement, ok := result.Error.Details["replacement"].(sessionenv.ResultReplacement)
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
	if err := s.appendThreadEvents(ctx, sessionenv.OutboundProduced{RunID: inbound.ID, Message: message}); err != nil {
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

func observationsForEffects(effects []environment.EffectResult) []coreevidence.Observation {
	observations := make([]coreevidence.Observation, 0, len(effects))
	for _, effect := range effects {
		obs := effect.Observation
		if obs.ID == "" && obs.Kind == "" {
			obs = coreevidence.Observation{
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
	if inbound.Kind != channel.InboundCommand {
		return commandFailed("invalid_command_inbound", "inbound envelope does not contain a command", nil)
	}
	if inbound.Command == nil {
		parsed, err := s.parseCommandLine(inbound.CommandLine)
		if err != nil {
			return commandFailed("invalid_command_line", err.Error(), map[string]any{"line": inbound.CommandLine})
		}
		inbound.Command = &parsed
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
	if err := s.appendThreadEvents(ctx, sessionenv.CommandReceived{
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
	agentSpec := agent.Spec{}
	if s.Agent != nil {
		agentSpec = s.Agent.Spec()
	}
	ctx = security.ContextForInbound(ctx, s.Security, inbound, agentSpec, s.SecurityTrace)
	evaluation := sessioncontrol.EvaluateInvocation(spec, inbound.Caller, inbound.Trust)
	switch {
	case sessioncontrol.PolicyDenied(evaluation):
		_ = s.appendThreadEvents(ctx, sessionenv.CommandRejected{RunID: inbound.ID, Command: *inbound.Command, Reason: evaluation.Reason})
		return CommandResult{Status: CommandStatusRejected, Spec: spec, Policy: evaluation}
	case sessioncontrol.PolicyApprovalRequired(evaluation):
		_ = s.appendThreadEvents(ctx, sessionenv.CommandRejected{RunID: inbound.ID, Command: *inbound.Command, Reason: evaluation.Reason})
		return CommandResult{Status: CommandStatusApprovalRequired, Spec: spec, Policy: evaluation}
	}

	if sessioncontrol.TargetsSession(spec) {
		if resolved.SessionHandler == nil {
			return s.executeTargetSessionCommand(ctx, inbound, spec, evaluation)
		}
		return resolved.SessionHandler(s, ctx, inbound, spec, evaluation)
	}

	if sessioncontrol.TargetsWorkflow(spec) {
		return s.executeWorkflowCommand(ctx, inbound, resolved.Binding, spec, evaluation)
	}
	if sessioncontrol.TargetsPrompt(spec) {
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
	requested := sessionenv.OperationRequested{
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
	completed := sessionenv.OperationCompleted{
		RunID:     inbound.ID,
		CallID:    callID,
		Operation: spec.Target.Operation,
		Result:    effect.Result,
	}
	if err := s.appendThreadEvents(ctx, completed, sessionenv.OutboundProduced{
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

// ExecuteInboundOperation dispatches a direct channel operation envelope through
// the session operation executor.
func (s Session) ExecuteInboundOperation(ctx context.Context, inbound channel.Inbound) OperationResult {
	if err := inbound.Validate(); err != nil {
		return operationFailed("invalid_operation_inbound", err.Error(), nil)
	}
	if inbound.Kind != channel.InboundOperation || inbound.Operation == nil {
		return operationFailed("invalid_operation_inbound", "inbound envelope does not contain an operation", nil)
	}
	if s.Operations == nil && len(s.OperationCatalog) == 0 {
		return operationFailed("operation_registry_missing", "operation registry is nil", nil)
	}
	opCtx := operation.NewContext(ensureContext(ctx), s.eventSink())
	callID := operationCallID(inbound.ID, 1)
	requested := sessionenv.OperationRequested{
		RunID:     inbound.ID,
		CallID:    callID,
		Operation: inbound.Operation.Operation,
		Input:     inbound.Operation.Input,
	}
	if err := s.appendThreadEvents(ctx, requested); err != nil {
		return operationFailed("thread_append_failed", err.Error(), nil)
	}
	s.emitLive(requested)
	effect := s.applyOperation(opCtx, inbound.Operation.Operation, inbound.Operation.Input, callID)
	completed := sessionenv.OperationCompleted{
		RunID:     inbound.ID,
		CallID:    callID,
		Operation: inbound.Operation.Operation,
		Result:    effect.Result,
	}
	if err := s.appendThreadEvents(ctx, completed, sessionenv.OutboundProduced{
		RunID:   inbound.ID,
		Message: outboundMessageForOperationResult(effect.Result),
	}); err != nil {
		return operationFailed("thread_append_failed", err.Error(), nil)
	}
	s.emitLive(completed)
	status := OperationStatusOK
	if effect.Result.IsError() {
		status = OperationStatusFailed
	}
	return OperationResult{Status: status, Operation: inbound.Operation.Operation, Effect: &effect}
}

func (s Session) executeTargetSessionCommand(ctx context.Context, inbound channel.Inbound, spec command.Spec, evaluation sessioncontrol.PolicyEvaluation) CommandResult {
	if s.SessionAgents == nil {
		return commandFailed("session_target_unavailable", "session-targeted command requires a session-agent runner", map[string]any{
			"path": spec.Path.String(),
		})
	}
	target := strings.TrimSpace(spec.Target.Session)
	if target == "" {
		return commandFailed("session_target_empty", "session-targeted command target is empty", map[string]any{
			"path": spec.Path.String(),
		})
	}
	eventSink := s.eventSink()
	var mu sync.Mutex
	var created []coretask.Created
	recordingEvents := sessionenv.EventSinkFunc(func(payload sessionenv.Event) {
		if eventSink != nil {
			eventSink.Emit(payload)
		}
		if evt, ok := payload.(coretask.Created); ok {
			mu.Lock()
			created = append(created, evt)
			mu.Unlock()
		}
	})
	task := renderSessionTargetInput(spec, inbound.Command)
	result, err := s.SessionAgents.Run(ctx, sessionagent.Request{
		ID:             sessionagent.ID(inbound.ID + ":session:" + target),
		Profile:        coresession.Ref{Name: coresession.Name(target)},
		Task:           task,
		TaskID:         inbound.ID,
		Policy:         s.Delegation,
		ParentThreadID: s.Thread.ID,
		ParentRunID:    inbound.ID,
		ParentCallID:   operation.CallID(inbound.ID + ":command:" + target),
		Events:         recordingEvents,
		Approver:       sessionenv.ApproverFromExecutor(s.OperationExecutor),
	})
	if err != nil {
		return commandFailed("session_target_run_failed", err.Error(), map[string]any{
			"path":    spec.Path.String(),
			"session": target,
		})
	}
	output := strings.TrimSpace(result.Output)
	if output == "" {
		mu.Lock()
		output = sessionTargetCreatedOutput(created)
		mu.Unlock()
	}
	commandResult := CommandResult{
		Status: CommandStatusOK,
		Spec:   spec,
		Policy: evaluation,
		Output: output,
	}
	if err := s.appendThreadEvents(ctx, sessionenv.OutboundProduced{
		RunID:   inbound.ID,
		Message: channel.Message{Content: output},
	}); err != nil {
		return commandFailed("thread_append_failed", err.Error(), nil)
	}
	return commandResult
}

func sessionTargetCreatedOutput(created []coretask.Created) string {
	if len(created) == 0 {
		return ""
	}
	task := created[len(created)-1].Task
	if task.ID == "" {
		task.ID = created[len(created)-1].TaskID
	}
	title := strings.TrimSpace(task.Title)
	if title == "" {
		title = strings.TrimSpace(task.Objective)
	}
	if title == "" {
		title = "task"
	}
	status := task.Status
	if status == "" {
		status = coretask.StatusDraft
	}
	return fmt.Sprintf("Created task %s: %s (status: %s)", task.ID, title, status)
}

func renderSessionTargetInput(spec command.Spec, invocation *command.Invocation) string {
	if invocation == nil {
		return spec.Path.String()
	}
	if text, ok := invocation.Input.(string); ok && strings.TrimSpace(text) != "" {
		return strings.TrimSpace(text)
	}
	if len(invocation.Args) > 0 {
		return strings.TrimSpace(strings.Join(invocation.Args, " "))
	}
	if invocation.Input != nil {
		if data, err := json.Marshal(invocation.Input); err == nil {
			return string(data)
		}
	}
	return spec.Path.String()
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
			"target":  sessioncontrol.TargetPromptKind(),
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
	result := sessionworkflow.Execute(ctx, sessionworkflow.Config{
		WorkflowCatalog:   s.WorkflowCatalog,
		Resolver:          s.Resolver,
		OperationExecutor: s.OperationExecutor,
		SessionAgents:     s.SessionAgents,
		Delegation:        s.Delegation,
		Thread:            s.Thread,
		Events:            s.eventSink(),
		AppendEvents:      s.appendThreadEvents,
		EmitLive:          s.emitLive,
		ResolveOperation: func(ref string, scope sessioncontrol.ResourceID) (operation.Operation, error) {
			binding, err := s.OperationCatalog.Resolve(ref, scope)
			if err != nil {
				return nil, err
			}
			return binding.Operation, nil
		},
	}, inbound.ID, inbound.Command.Input, binding.TargetID, spec)
	commandResult := CommandResult{
		Status: CommandStatus(result.Status),
		Spec:   spec,
		Policy: evaluation,
		Output: result.Output,
	}
	if result.Error != nil {
		commandResult.Error = &CommandError{
			Code:    result.Error.Code,
			Message: result.Error.Message,
			Details: result.Error.Details,
		}
	}
	if err := s.appendThreadEvents(ctx, sessionenv.OutboundProduced{
		RunID:   inbound.ID,
		Message: workflowCommandMessage(commandResult),
	}); err != nil {
		return commandFailed("thread_append_failed", err.Error(), nil)
	}
	return commandResult
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
	preview, err := s.previewContext(ctx, input, inbound)
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
	if err := s.appendThreadEvents(ctx, sessionenv.OutboundProduced{
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

func (s Session) executeWhoamiCommand(ctx context.Context, inbound channel.Inbound, spec command.Spec, evaluation sessioncontrol.PolicyEvaluation) CommandResult {
	text := s.renderWhoami(inbound)
	if err := s.appendThreadEvents(ctx, sessionenv.OutboundProduced{
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

func (s Session) renderWhoami(inbound channel.Inbound) string {
	var agentSpec agent.Spec
	if s.Agent != nil {
		agentSpec = s.Agent.Spec()
	}
	lines := []string{"Current identity:"}
	lines = append(lines, "- caller: "+renderCaller(inbound.Caller))
	if inbound.Actor != nil {
		lines = append(lines, "- resolved: "+resolvedText(inbound.Actor.Resolution))
		if inbound.Actor.User.ID != "" {
			lines = append(lines, "- user: "+string(inbound.Actor.User.ID))
		}
		if inbound.Actor.User.Username != "" && inbound.Actor.User.Username != string(inbound.Actor.User.ID) {
			lines = append(lines, "- username: "+inbound.Actor.User.Username)
		}
		if identityText := renderIdentity(inbound.Actor.Identity); identityText != "" {
			lines = append(lines, "- identity: "+identityText)
		}
		if groups := actorGroupIDs(*inbound.Actor); len(groups) > 0 {
			lines = append(lines, "- groups: "+strings.Join(groups, ", "))
		}
	} else {
		lines = append(lines, "- resolved: false")
	}
	trust := string(inbound.Trust.Level)
	if trust == "" {
		trust = string(policy.TrustUntrusted)
	}
	if inbound.Trust.Downgraded {
		trust += " (downgraded)"
	}
	lines = append(lines, "- trust: "+trust)
	subjects := security.SubjectsForInbound(inbound, agentSpec)
	if len(subjects) > 0 {
		lines = append(lines, "- subjects: "+renderSubjects(subjects))
	}
	return strings.Join(lines, "\n")
}

func renderCaller(caller policy.Caller) string {
	principal := strings.TrimSpace(caller.Principal.ID)
	if caller.Principal.Kind != "" && principal != "" {
		principal = caller.Principal.Kind + ":" + principal
	}
	if principal == "" {
		principal = strings.TrimSpace(caller.Principal.Kind)
	}
	parts := []string{string(caller.Kind)}
	if principal != "" {
		parts = append(parts, principal)
	}
	if caller.Source != "" {
		parts = append(parts, "source="+caller.Source)
	}
	return strings.Join(parts, " ")
}

func renderIdentity(identity user.Identity) string {
	switch {
	case identity.Provider != "" && identity.ProviderID != "":
		return identity.Provider + ":" + identity.ProviderID
	case identity.ProviderID != "":
		return identity.ProviderID
	default:
		return identity.Provider
	}
}

func resolvedText(state user.ResolutionState) string {
	if user.NormalizeResolution(state) == user.ResolutionResolved {
		return "true"
	}
	return "false"
}

func renderSubjects(subjects []policy.SubjectRef) string {
	parts := make([]string, 0, len(subjects))
	for _, subject := range subjects {
		if subject.Kind == "" || subject.ID == "" {
			continue
		}
		parts = append(parts, string(subject.Kind)+":"+subject.ID)
	}
	sort.Strings(parts)
	return strings.Join(parts, ", ")
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
	if err := s.appendThreadEvents(ctx, sessionenv.OutboundProduced{
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
	return sessioncontrol.TargetsSession(spec) || sessioncontrol.TargetsPrompt(spec), nil
}

// ParseCommandLine parses a raw slash command line using session command
// resolution rules.
func ParseCommandLine(line string, registry *command.Registry, catalog CommandCatalog) (command.Invocation, error) {
	invocation, ok, err := command.ParseSlash(line)
	if err != nil {
		return command.Invocation{}, err
	}
	if !ok {
		return command.Invocation{}, fmt.Errorf("command line is not a slash command")
	}
	return preferResolvableCommand(invocation, AvailableCommandSpecs(registry, catalog)), nil
}

func (s Session) parseCommandLine(line string) (command.Invocation, error) {
	return ParseCommandLine(line, s.Commands, s.CommandCatalog)
}

func preferResolvableCommand(invocation command.Invocation, specs []command.Spec) command.Invocation {
	if len(specs) == 0 || len(invocation.Path) <= 1 || knownCommandPath(invocation.Path, specs) {
		return invocation
	}
	for n := len(invocation.Path) - 1; n >= 1; n-- {
		prefix := append(command.Path(nil), invocation.Path[:n]...)
		if !knownCommandPath(prefix, specs) {
			continue
		}
		remainder := append([]string(nil), invocation.Path[n:]...)
		invocation.Path = prefix
		invocation.Args = append(remainder, invocation.Args...)
		return invocation
	}
	return invocation
}

func knownCommandPath(path command.Path, specs []command.Spec) bool {
	key := path.String()
	if key == "" {
		return false
	}
	for _, spec := range specs {
		if spec.Path.String() == key {
			return true
		}
	}
	return false
}

func (s Session) resolveCommand(path command.Path) (resolvedCommand, bool, error) {
	return resolveCommand(path, s.Resolver, s.CommandCatalog, s.Commands)
}

func resolveCommand(path command.Path, resolver *sessioncontrol.Resolver, catalog CommandCatalog, registry *command.Registry) (resolvedCommand, bool, error) {
	if sessionCommand, ok := builtInSessionCommands()[path.String()]; ok {
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
	cfg := s.envConfig(ctx.Events())
	if active, ok := sessionenv.ActiveStateFromContext(ctx); ok {
		cfg.Active = &active
	}
	ctx = sessionenv.OperationContext(ctx, cfg, callID)
	return operationEffect(s.OperationExecutor.Execute(ctx, op, input))
}

func (s Session) withBaseContext(ctx context.Context, callID operation.CallID, events sessionenv.EventSink, active ...sessionenv.ActiveState) context.Context {
	cfg := s.envConfig(events)
	if len(active) > 0 {
		cfg.Active = &active[0]
	}
	return sessionenv.WithBaseContext(ctx, cfg, callID)
}

func (s Session) replaySkillEvents(ctx context.Context) error {
	return sessionenv.ReplaySkillEvents(ctx, s.envConfig(s.eventSink()))
}

func (s Session) replayReactionEvents(ctx context.Context) (reactionState, error) {
	if s.ThreadStore == nil || s.Thread.ID == "" {
		return reactionState{}, nil
	}
	snapshot, err := s.ThreadStore.Read(ensureContext(ctx), corethread.ReadParams{ID: s.Thread.ID})
	if err != nil {
		if errors.Is(err, corethread.ErrNotFound) {
			return reactionState{}, nil
		}
		return reactionState{}, err
	}
	records, err := snapshot.EventsForBranch(s.Thread.BranchID)
	if err != nil {
		return reactionState{}, err
	}
	state := reactionState{AppliedKeys: map[string]bool{}}
	for _, record := range records {
		runtimeEvent, ok := record.Event.Payload.(coresession.RuntimeEmitted)
		if !ok {
			if ptr, ok := record.Event.Payload.(*coresession.RuntimeEmitted); ok && ptr != nil {
				runtimeEvent = *ptr
			} else {
				continue
			}
		}
		if runtimeEvent.Name != corereaction.EventActionApplied {
			continue
		}
		if key := reactionAppliedKey(runtimeEvent.Payload); key != "" {
			state.AppliedKeys[key] = true
		}
		if applied, ok := reactionAppliedPayload(runtimeEvent.Payload); ok {
			state.Applied = append(state.Applied, applied)
		}
		applyReplayedReactionActivation(runtimeEvent.Payload, &state.Active)
	}
	if len(state.AppliedKeys) == 0 {
		state.AppliedKeys = nil
	}
	return state, nil
}

func reactionAppliedPayload(payload any) (corereaction.ActionApplied, bool) {
	switch typed := payload.(type) {
	case corereaction.ActionApplied:
		typed.ObservationIDs = append([]string(nil), typed.ObservationIDs...)
		return typed, true
	case *corereaction.ActionApplied:
		if typed == nil {
			return corereaction.ActionApplied{}, false
		}
		out := *typed
		out.ObservationIDs = append([]string(nil), typed.ObservationIDs...)
		return out, true
	case map[string]any:
		out := corereaction.ActionApplied{}
		out.Rule, _ = typed["rule"].(string)
		action, _ := typed["action"].(string)
		out.Action = corereaction.ActionKind(action)
		out.IdempotencyKey, _ = typed["idempotency_key"].(string)
		out.Target, _ = typed["target"].(string)
		out.Assertion, _ = typed["assertion"].(string)
		out.AssertionTarget, _ = typed["assertion_target"].(string)
		out.AssertionSubjectKind, _ = typed["assertion_subject_kind"].(string)
		out.AssertionSubjectName, _ = typed["assertion_subject_name"].(string)
		out.AssertionSubjectID, _ = typed["assertion_subject_id"].(string)
		out.AssertionScope, _ = typed["assertion_scope"].(string)
		out.AssertionSource, _ = typed["assertion_source"].(string)
		out.ObservationIDs = stringSliceFromAny(typed["observation_ids"])
		return out, true
	default:
		return corereaction.ActionApplied{}, false
	}
}

func reactionAppliedKey(payload any) string {
	switch typed := payload.(type) {
	case corereaction.ActionApplied:
		return strings.TrimSpace(typed.IdempotencyKey)
	case *corereaction.ActionApplied:
		if typed == nil {
			return ""
		}
		return strings.TrimSpace(typed.IdempotencyKey)
	case map[string]any:
		key, _ := typed["idempotency_key"].(string)
		return strings.TrimSpace(key)
	default:
		return ""
	}
}

func stringSliceFromAny(value any) []string {
	switch typed := value.(type) {
	case []string:
		return append([]string(nil), typed...)
	case []any:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			text, ok := item.(string)
			if ok && strings.TrimSpace(text) != "" {
				out = append(out, text)
			}
		}
		return out
	default:
		return nil
	}
}

func applyReplayedReactionActivation(payload any, active *sessionenv.ActiveState) {
	action, target := reactionAppliedActionTarget(payload)
	if active == nil || strings.TrimSpace(target) == "" {
		return
	}
	switch action {
	case corereaction.ActionEnableOperationSet:
		active.EnableOperationSet(target)
	case corereaction.ActionEnableDatasource:
		active.EnableDatasource(coredatasource.Name(target))
	case corereaction.ActionEnableContext:
		active.EnableContextProvider(corecontext.ProviderName(target))
	}
}

func reactionAppliedActionTarget(payload any) (corereaction.ActionKind, string) {
	switch typed := payload.(type) {
	case corereaction.ActionApplied:
		return typed.Action, strings.TrimSpace(typed.Target)
	case *corereaction.ActionApplied:
		if typed == nil {
			return "", ""
		}
		return typed.Action, strings.TrimSpace(typed.Target)
	case map[string]any:
		action, _ := typed["action"].(string)
		target, _ := typed["target"].(string)
		return corereaction.ActionKind(action), strings.TrimSpace(target)
	default:
		return "", ""
	}
}

func (s Session) envConfig(events sessionenv.EventSink) sessionenv.Config {
	return sessionenv.Config{
		Agent:             s.Agent,
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

func reactionOperationCallID(runID, idempotencyKey string, ordinal int) operation.CallID {
	if ordinal < 1 {
		ordinal = 1
	}
	if strings.TrimSpace(idempotencyKey) == "" {
		if runID == "" {
			return operation.CallID(fmt.Sprintf("reaction_operation:%d", ordinal))
		}
		return operation.CallID(fmt.Sprintf("%s:reaction_operation:%d", runID, ordinal))
	}
	sum := sha256.Sum256([]byte(idempotencyKey))
	short := hex.EncodeToString(sum[:8])
	if runID == "" {
		return operation.CallID("reaction_operation:" + short)
	}
	return operation.CallID(runID + ":reaction_operation:" + short)
}

func reactionCommandRunID(runID, idempotencyKey string, ordinal int) string {
	if ordinal < 1 {
		ordinal = 1
	}
	if strings.TrimSpace(idempotencyKey) == "" {
		if runID == "" {
			return fmt.Sprintf("reaction_command:%d", ordinal)
		}
		return fmt.Sprintf("%s:reaction_command:%d", runID, ordinal)
	}
	sum := sha256.Sum256([]byte(idempotencyKey))
	short := hex.EncodeToString(sum[:8])
	if runID == "" {
		return "reaction_command:" + short
	}
	return runID + ":reaction_command:" + short
}

func reactionWorkflowRunID(runID, idempotencyKey string, ordinal int) string {
	if ordinal < 1 {
		ordinal = 1
	}
	if strings.TrimSpace(idempotencyKey) == "" {
		if runID == "" {
			return fmt.Sprintf("reaction_workflow:%d", ordinal)
		}
		return fmt.Sprintf("%s:reaction_workflow:%d", runID, ordinal)
	}
	sum := sha256.Sum256([]byte(idempotencyKey))
	short := hex.EncodeToString(sum[:8])
	if runID == "" {
		return "reaction_workflow:" + short
	}
	return runID + ":reaction_workflow:" + short
}

func cloneCommandInvocation(in command.Invocation) *command.Invocation {
	out := in
	out.Path = append(command.Path(nil), in.Path...)
	out.Args = append([]string(nil), in.Args...)
	return &out
}

func operationEffect(result operation.Result) environment.EffectResult {
	return environment.EffectResult{
		Result: result,
		Observation: coreevidence.Observation{
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

func operationFailed(code, message string, details map[string]any) OperationResult {
	return OperationResult{
		Status: OperationStatusFailed,
		Error:  &CommandError{Code: code, Message: message, Details: details},
	}
}

func (s Session) appendThreadEvents(ctx context.Context, events ...sessionenv.Event) error {
	if s.ThreadStore == nil || s.Thread.ID == "" || len(events) == 0 {
		return nil
	}
	records := sessionenv.ThreadAppendRecords(s.Thread, events...)
	if len(records) == 0 {
		return nil
	}
	return s.appendThreadRecords(ctx, records...)
}

func (s Session) appendConversation(ctx context.Context, turnID string, provider coreconversation.ProviderIdentity, items []coreconversation.Item, handles ...coreconversation.ContinuationHandle) error {
	return s.appendConversationAndEvents(ctx, turnID, provider, items, handles)
}

func (s Session) appendConversationAndEvents(ctx context.Context, turnID string, provider coreconversation.ProviderIdentity, items []coreconversation.Item, handles []coreconversation.ContinuationHandle, payloads ...sessionenv.Event) error {
	conversationRecords, err := conversationruntime.AppendRecords(turnID, provider, items, handles...)
	if err != nil {
		return err
	}
	threadRecords := sessionenv.ThreadAppendRecords(s.Thread, payloads...)
	records := append(conversationRecords, threadRecords...)
	if len(records) == 0 {
		return nil
	}
	return s.appendThreadRecords(ctx, records...)
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
			if !sessionenv.IsAppendConflict(err) {
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
