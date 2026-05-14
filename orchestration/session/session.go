package session

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/fluxplane/agentruntime/core/agent"
	"github.com/fluxplane/agentruntime/core/channel"
	"github.com/fluxplane/agentruntime/core/command"
	corecontext "github.com/fluxplane/agentruntime/core/context"
	coreconversation "github.com/fluxplane/agentruntime/core/conversation"
	coredatasource "github.com/fluxplane/agentruntime/core/datasource"
	"github.com/fluxplane/agentruntime/core/environment"
	"github.com/fluxplane/agentruntime/core/event"
	"github.com/fluxplane/agentruntime/core/invocation"
	"github.com/fluxplane/agentruntime/core/operation"
	"github.com/fluxplane/agentruntime/core/policy"
	"github.com/fluxplane/agentruntime/core/resource"
	coresession "github.com/fluxplane/agentruntime/core/session"
	corethread "github.com/fluxplane/agentruntime/core/thread"
	"github.com/fluxplane/agentruntime/orchestration/subagent"
	llmagent "github.com/fluxplane/agentruntime/runtime/agent/llmagent"
	contextruntime "github.com/fluxplane/agentruntime/runtime/context"
	conversationruntime "github.com/fluxplane/agentruntime/runtime/conversation"
	operationruntime "github.com/fluxplane/agentruntime/runtime/operation"
	runtimeskill "github.com/fluxplane/agentruntime/runtime/skill"
)

// Session is the first orchestration boundary for the observe-decide-apply
// loop. It is intentionally small; lifecycle and persistence will be added only
// after the core loop is stable.
type Session struct {
	Agent             agent.Agent
	Profile           coresession.Spec
	Commands          *command.Registry
	Operations        *operation.Registry
	Resolver          *resource.Resolver
	CommandCatalog    CommandCatalog
	OperationCatalog  OperationCatalog
	OperationExecutor operationruntime.Executor
	Events            event.Sink
	ThreadStore       corethread.Store
	Thread            corethread.Ref
	Subagents         *subagent.Supervisor
	Delegation        coresession.DelegationPolicy
	RunID             string
}

const (
	defaultLLMMaxSteps      = 50
	defaultLLMContinuations = 3
)

var contextCommandSpec = command.Spec{
	Path:        command.Path{"context"},
	Description: "Preview context that would be sent to the LLM.",
	Target:      invocation.Target{Kind: invocation.TargetSession},
	Policy: policy.InvocationPolicy{
		AllowedCallers: []policy.CallerKind{policy.CallerUser, policy.CallerSystem},
		RequiredTrust:  policy.TrustVerified,
	},
}

var errContextProviderNotFound = errors.New("context provider not found")

// IsContextCommandPath reports whether path targets the built-in context
// preview command.
func IsContextCommandPath(path command.Path) bool {
	return len(path) == 1 && strings.TrimSpace(path[0]) == "context"
}

// OperationBinding binds a canonical operation resource to an executable
// implementation.
type OperationBinding struct {
	ID        resource.ResourceID `json:"id"`
	Operation operation.Operation `json:"-"`
}

// OperationCatalog binds canonical operation resource IDs to executable
// implementations.
type OperationCatalog map[string]OperationBinding

// ToolSetBinding binds a projected tool set to its canonical resource identity.
type ToolSetBinding struct {
	ID   resource.ResourceID `json:"id"`
	Spec any                 `json:"spec"`
}

// ToolSetCatalog binds canonical tool set resource IDs to tool set specs.
type ToolSetCatalog map[string]ToolSetBinding

// CommandBinding binds a command contribution to its resolved target.
type CommandBinding struct {
	ID          resource.ResourceID `json:"id"`
	Spec        command.Spec        `json:"spec"`
	TargetID    resource.ResourceID `json:"target_id,omitempty"`
	OperationID resource.ResourceID `json:"operation_id,omitempty"`
}

// CommandCatalog binds canonical command resource IDs to command specs.
type CommandCatalog map[string]CommandBinding

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
		binding, err := s.OperationCatalog.Resolve(ref.String(), resource.ResourceID{})
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
	Status   InputStatus                `json:"status"`
	Agent    agent.StepResult           `json:"agent,omitempty"`
	Effect   *environment.EffectResult  `json:"effect,omitempty"`
	Effects  []environment.EffectResult `json:"effects,omitempty"`
	Outbound *channel.Outbound          `json:"outbound,omitempty"`
	Error    *CommandError              `json:"error,omitempty"`
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
			Observations:        observations,
			State:               state,
			Effects:             effects,
			MaxSteps:            s.maxSteps(),
			FailOnStepLimit:     s.failOnStepLimit(),
			ProviderIdentity:    s.providerIdentity(),
			ConversationTurnID:  inbound.ID,
		})
		if inner.Result.Status != "" {
			return inner.Result
		}
		state = inner.State
		effects = inner.Effects
		if !s.shouldContinueAfterTerminal(continuation, inner.AgentResult) {
			return s.applyTerminalAgentDecision(ctx, inbound, inner.AgentResult, effects)
		}
		pending = []coreconversation.Item{inputTranscriptItem(s.providerIdentity(), "Continue.")}
		observations = []environment.Observation{{
			Source:  "session",
			Kind:    "session.continuation",
			Content: "Continue.",
			Metadata: map[string]any{
				"continuation": continuation + 1,
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
	for step := 0; step < maxSteps; step++ {
		contextResult, projectedPending, err := s.materializeContext(ctx, in, pending, observations)
		if err != nil {
			return innerTurnResult{Result: inputFailed("context_render_failed", err.Error(), nil), State: state, Effects: effects}
		}
		transcript, err := s.transcriptForPending(ctx, projectedPending, derefItems(in.LocalTranscript), derefHandle(in.LocalContinuation))
		if err != nil {
			return innerTurnResult{Result: inputFailed("conversation_projection_failed", err.Error(), nil), State: state, Effects: effects}
		}
		modelCtx := llmagent.ContextWithMaterializedContext(llmagent.ContextWithTranscript(in.BaseContext, &transcript))
		modelCtx = s.withSubagentBaseContext(modelCtx, "", in.Events)
		agentCtx := agentContext{Context: modelCtx, events: in.Events}
		agentResult := s.Agent.Step(agentCtx, agent.StepInput{
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
			if err := s.persistRepairTranscriptItems(ctx, in.ConversationTurnID, in.ProviderIdentity, pending, in.LocalTranscript); err != nil {
				return innerTurnResult{Result: inputFailed("conversation_repair_failed", err.Error(), nil), AgentResult: agentResult, State: state, Effects: effects}
			}
			return innerTurnResult{Result: InputResult{Status: InputStatusFailed, Agent: agentResult, Effect: lastEffect(effects), Effects: effects, Error: agentError(agentResult.Error)}, AgentResult: agentResult, State: state, Effects: effects}
		}
		if err := s.commitContextRender(ctx, contextResult, in.LocalContextRecords); err != nil {
			return innerTurnResult{Result: inputFailed("context_commit_failed", err.Error(), nil), AgentResult: agentResult, State: state, Effects: effects}
		}
		if !stateRefIsZero(agentResult.State.Ref) {
			state = agentResult.State.Ref
		}
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
		if step == maxSteps-1 {
			if err := s.persistRepairTranscriptItems(ctx, in.ConversationTurnID, in.ProviderIdentity, toolResults, in.LocalTranscript); err != nil {
				return innerTurnResult{Result: inputFailed("conversation_repair_failed", err.Error(), nil), AgentResult: agentResult, State: state, Effects: effects}
			}
			if !in.FailOnStepLimit {
				return innerTurnResult{Result: s.operationBoundaryResult(ctx, in.Inbound, agentResult, effects), AgentResult: agentResult, State: state, Effects: effects}
			}
			return innerTurnResult{Result: s.stepLimitResult(ctx, in.Inbound, agentResult, effects), AgentResult: agentResult, State: state, Effects: effects}
		}
		observations = append(observations, observationsForEffects(batch)...)
		pending = toolResults
	}
	return innerTurnResult{Result: InputResult{Status: InputStatusFailed, Effects: effects, Error: &CommandError{Code: "step_limit_exceeded", Message: "agent reached max_steps"}}, State: state, Effects: effects}
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

func (s Session) persistRepairTranscriptItems(ctx context.Context, turnID string, provider coreconversation.ProviderIdentity, items []coreconversation.Item, localItems *[]coreconversation.Item) error {
	if !hasToolResultTranscriptItems(items) {
		return nil
	}
	copied := append([]coreconversation.Item(nil), items...)
	if localItems != nil {
		*localItems = append(*localItems, copied...)
	}
	return s.appendConversation(ctx, turnID, provider, copied)
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

func hasToolResultTranscriptItems(items []coreconversation.Item) bool {
	for _, item := range items {
		if item.Kind == coreconversation.ItemToolResult {
			return true
		}
	}
	return false
}

type contextProviderAgent interface {
	ContextProviders() []corecontext.Provider
}

type contextPreviewInput struct {
	Fresh bool   `json:"fresh,omitempty"`
	Key   string `json:"key,omitempty"`
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
	materializer := contextruntime.NewMaterializer(providers, previous)
	result, err := materializer.Build(s.contextProviderContext(ctx, nil), corecontext.BuildRequest{
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
	if text, ok := contextruntime.RenderDiff(result, corecontext.PlacementSystem); ok {
		data.System = text
	}
	if text, ok := contextruntime.RenderDiff(result, corecontext.PlacementDeveloper); ok {
		data.Developer = text
	}
	if text, ok := contextruntime.RenderDiff(result, corecontext.PlacementUser); ok {
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
	providers := s.contextProviders()
	if len(providers) == 0 {
		return corecontext.BuildResult{}, append([]coreconversation.Item(nil), pending...), nil
	}
	records, err := s.contextRenderRecords(ctx, in.LocalContextRecords)
	if err != nil {
		return corecontext.BuildResult{}, nil, err
	}
	renderCtx := s.contextProviderContext(in.BaseContext, observations)
	materializer := contextruntime.NewMaterializer(providers, records)
	result, err := materializer.Build(renderCtx, corecontext.BuildRequest{
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

func (s Session) contextProviders() []corecontext.Provider {
	carrier, ok := s.Agent.(contextProviderAgent)
	if !ok || carrier == nil {
		return nil
	}
	return carrier.ContextProviders()
}

func (s Session) contextProviderContext(ctx context.Context, observations []environment.Observation) context.Context {
	ctx = ensureContext(ctx)
	ctx = s.withSubagentBaseContext(ctx, "", s.eventSink())
	ctx = coredatasource.ContextWithDetectionInput(ctx, detectionInputFromObservations(observations))
	if s.Agent == nil {
		return ctx
	}
	spec := s.Agent.Spec()
	names := make([]coredatasource.Name, 0, len(spec.Datasources))
	for _, ref := range spec.Datasources {
		if ref.Name != "" {
			names = append(names, ref.Name)
		}
	}
	return coredatasource.ContextWithAccessPolicy(ctx, coredatasource.AccessPolicy{Datasources: names})
}

func detectionInputFromObservations(observations []environment.Observation) coredatasource.DetectionInput {
	var sources []coredatasource.DetectionSource
	for i, observation := range observations {
		text := contextValueText(observation.Content)
		if strings.TrimSpace(text) == "" {
			continue
		}
		sources = append(sources, coredatasource.DetectionSource{
			ID:       fmt.Sprintf("observation-%d", i),
			Kind:     observation.Kind,
			Text:     text,
			Metadata: observationStringMetadata(observation.Metadata),
		})
	}
	return coredatasource.DetectionInput{Sources: sources}
}

func observationStringMetadata(values map[string]any) map[string]string {
	if len(values) == 0 {
		return nil
	}
	out := map[string]string{}
	for key, value := range values {
		text := strings.TrimSpace(fmt.Sprint(value))
		if text != "" && text != "<nil>" {
			out[key] = text
		}
	}
	return out
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
			Fingerprint: contextruntime.BlockFingerprint(block),
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
	if text, ok := contextruntime.RenderDiff(result, corecontext.PlacementSystem); ok {
		prefix = append(prefix, contextTranscriptItem(provider, "system", text))
	}
	if text, ok := contextruntime.RenderDiff(result, corecontext.PlacementDeveloper); ok {
		prefix = append(prefix, contextTranscriptItem(provider, "developer", text))
	}
	if len(prefix) > 0 {
		out = append(prefix, out...)
	}
	if text, ok := contextruntime.RenderDiff(result, corecontext.PlacementUser); ok {
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
			return coreconversation.Transcript{
				Provider:     provider,
				Items:        pending,
				NewItems:     append([]coreconversation.Item(nil), pending...),
				Continuation: &copied,
				Mode:         coreconversation.ProjectionNativeContinuation,
			}, nil
		}
		return coreconversation.Transcript{
			Provider: provider,
			Items:    append(append([]coreconversation.Item(nil), localItems...), pending...),
			NewItems: append([]coreconversation.Item(nil), pending...),
			Mode:     coreconversation.ProjectionFullReplay,
		}, nil
	}
	snapshot, err := s.ThreadStore.Read(ensureContext(ctx), corethread.ReadParams{ID: s.Thread.ID})
	if err != nil {
		if errors.Is(err, corethread.ErrNotFound) {
			return coreconversation.Transcript{
				Provider: provider,
				Items:    pending,
				NewItems: append([]coreconversation.Item(nil), pending...),
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
		spec := s.Agent.Spec()
		identity.Model = spec.Inference.Model
		identity.Provider = firstNonEmptyString(
			spec.Inference.Annotations["provider"],
			spec.Inference.Annotations["llm.provider"],
			spec.Driver.Annotations["provider"],
			stringFromAny(spec.Driver.Config["provider"]),
		)
		identity.API = firstNonEmptyString(
			spec.Inference.Annotations["api"],
			spec.Inference.Annotations["llm.api"],
			spec.Driver.Annotations["api"],
			stringFromAny(spec.Driver.Config["api"]),
		)
		identity.Family = firstNonEmptyString(
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
		effects = append(effects, effect)
		toolResults = append(toolResults, operationResultTranscriptItem(provider, opReq, callID, effect.Result))
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

func (s Session) stepLimitResult(ctx context.Context, inbound channel.Inbound, agentResult agent.StepResult, effects []environment.EffectResult) InputResult {
	return s.operationBoundaryResult(ctx, inbound, agentResult, effects, &CommandError{Code: "step_limit_exceeded", Message: "agent reached max_steps"})
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
	if spec.Policy.MaxSteps > 0 {
		return spec.Policy.MaxSteps
	}
	if isLLMDriverKind(spec.Driver.Kind) {
		return defaultLLMMaxSteps
	}
	return 1
}

func (s Session) failOnStepLimit() bool {
	if s.Agent == nil {
		return true
	}
	spec := s.Agent.Spec()
	return isLLMDriverKind(spec.Driver.Kind) || spec.Policy.MaxSteps > 0
}

func (s Session) maxContinuations() int {
	if s.Agent == nil {
		return 0
	}
	spec := s.Agent.Spec()
	if spec.Policy.MaxContinuations > 0 {
		return spec.Policy.MaxContinuations
	}
	if isLLMDriverKind(spec.Driver.Kind) {
		return defaultLLMContinuations
	}
	return 0
}

func isLLMDriverKind(kind agent.DriverKind) bool {
	return strings.TrimSpace(string(kind)) == string(llmagent.DriverKind)
}

func (s Session) shouldContinueAfterTerminal(completed int, agentResult agent.StepResult) bool {
	if agentResult.Status != agent.StatusOK {
		return false
	}
	spec := s.Agent.Spec()
	if !stopConditionRequestsContinuation(spec.Stop, completed) {
		return false
	}
	return completed < s.maxContinuations()
}

func stopConditionRequestsContinuation(stop agent.StopConditionSpec, completed int) bool {
	switch strings.TrimSpace(stop.Type) {
	case "max-continuations":
		return stop.Max <= 0 || completed < stop.Max
	case "or", "and":
		for _, condition := range stop.Conditions {
			if stopConditionRequestsContinuation(condition, completed) {
				return true
			}
		}
	}
	return false
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
	isContextCommand := IsContextCommandPath(inbound.Command.Path)
	if s.Profile.Commands != nil && !isContextCommand && !commandPathAllowed(s.Profile.Commands, inbound.Command.Path) {
		return commandFailed("command_not_found", "command not found", map[string]any{
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
	if isContextCommand {
		return s.executeContextCommand(ctx, inbound)
	}
	if s.Commands == nil && len(s.CommandCatalog) == 0 {
		return commandFailed("command_registry_missing", "command registry is nil", nil)
	}
	binding, ok, err := s.resolveCommandBinding(inbound.Command.Path)
	if err != nil {
		return commandFailed("command_resolution_failed", err.Error(), map[string]any{
			"path": inbound.Command.Path.String(),
		})
	}
	spec := binding.Spec
	if !ok {
		var found bool
		if s.Commands != nil {
			spec, found = s.Commands.Resolve(inbound.Command.Path)
		}
		if !found {
			return commandFailed("command_not_found", "command not found", map[string]any{
				"path": inbound.Command.Path.String(),
			})
		}
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
		effect := s.applyBoundOperation(opCtx, binding, spec.Target.Operation, inbound.Command.Input, callID)
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

func (s Session) executeContextCommand(ctx context.Context, inbound channel.Inbound) CommandResult {
	spec := contextCommandSpec
	evaluation := policy.EvaluateInvocation(spec.Policy, inbound.Caller, inbound.Trust)
	switch evaluation.Decision {
	case policy.DecisionDeny:
		_ = s.appendThreadEvents(ctx, coresession.CommandRejected{RunID: inbound.ID, Command: *inbound.Command, Reason: evaluation.Reason})
		return CommandResult{Status: CommandStatusRejected, Spec: spec, Policy: evaluation}
	case policy.DecisionApprovalRequired:
		_ = s.appendThreadEvents(ctx, coresession.CommandRejected{RunID: inbound.ID, Command: *inbound.Command, Reason: evaluation.Reason})
		return CommandResult{Status: CommandStatusApprovalRequired, Spec: spec, Policy: evaluation}
	}
	input, err := parseContextPreviewInput(inbound.Command.Input)
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

func parseContextPreviewInput(value any) (contextPreviewInput, error) {
	if value == nil {
		return contextPreviewInput{}, nil
	}
	switch typed := value.(type) {
	case contextPreviewInput:
		return typed, nil
	case *contextPreviewInput:
		if typed == nil {
			return contextPreviewInput{}, nil
		}
		return *typed, nil
	case string:
		return parseContextPreviewArgs(strings.Fields(typed))
	}
	data, err := json.Marshal(value)
	if err != nil {
		return contextPreviewInput{}, err
	}
	var input contextPreviewInput
	if err := json.Unmarshal(data, &input); err != nil {
		return contextPreviewInput{}, err
	}
	input.Key = strings.TrimSpace(input.Key)
	return input, nil
}

func parseContextPreviewArgs(args []string) (contextPreviewInput, error) {
	var input contextPreviewInput
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--fresh":
			input.Fresh = true
		case "--key":
			if i+1 >= len(args) || strings.HasPrefix(args[i+1], "-") {
				return contextPreviewInput{}, fmt.Errorf("--key requires a provider name")
			}
			i++
			input.Key = args[i]
		default:
			return contextPreviewInput{}, fmt.Errorf("unknown /context argument %q", args[i])
		}
	}
	input.Key = strings.TrimSpace(input.Key)
	return input, nil
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

func (s Session) resolveCommandBinding(path command.Path) (CommandBinding, bool, error) {
	if s.Resolver == nil || len(s.CommandCatalog) == 0 {
		return CommandBinding{}, false, nil
	}
	ref := commandPathRef(path)
	id, err := s.Resolver.Resolve("command", ref)
	if err != nil {
		return CommandBinding{}, false, err
	}
	binding, ok := s.CommandCatalog[id.Address()]
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
	ctx = s.withSkillAccess(ctx)
	ctx = s.withDatasourceAccess(ctx)
	ctx = operation.WithCallID(ctx, callID)
	ctx = s.withSubagentScope(ctx, callID)
	return operationEffect(s.OperationExecutor.Execute(ctx, op, input))
}

func (s Session) withSkillAccess(ctx operation.Context) operation.Context {
	if ctx == nil || s.Agent == nil {
		return ctx
	}
	state, ok := runtimeskill.StateFromAgent(s.Agent)
	if !ok {
		return ctx
	}
	base := runtimeskill.ContextWithState(ctx, state)
	return operation.NewContext(base, ctx.Events())
}

func (s Session) withSubagentScope(ctx operation.Context, callID operation.CallID) operation.Context {
	if ctx == nil || s.Subagents == nil {
		return ctx
	}
	base := s.withSubagentBaseContext(ctx, callID, ctx.Events())
	return operation.NewContext(base, ctx.Events())
}

func (s Session) withSubagentBaseContext(ctx context.Context, callID operation.CallID, events event.Sink) context.Context {
	if ctx == nil || s.Subagents == nil {
		return ctx
	}
	if events == nil {
		events = event.Discard()
	}
	return subagent.ContextWithScope(ctx, subagent.Scope{
		Supervisor:     s.Subagents,
		ParentThreadID: s.Thread.ID,
		ParentRunID:    s.RunID,
		ParentCallID:   callID,
		Policy:         s.Delegation,
		Events:         events,
		ThreadStore:    s.ThreadStore,
	})
}

func (s Session) replaySkillEvents(ctx context.Context) error {
	if s.ThreadStore == nil || s.Thread.ID == "" || s.Agent == nil {
		return nil
	}
	state, ok := runtimeskill.StateFromAgent(s.Agent)
	if !ok {
		return nil
	}
	snapshot, err := s.ThreadStore.Read(persistenceContext(ctx), corethread.ReadParams{ID: s.Thread.ID})
	if err != nil {
		if errors.Is(err, corethread.ErrNotFound) {
			return nil
		}
		return err
	}
	records, err := snapshot.EventsForBranch(s.Thread.BranchID)
	if err != nil {
		return err
	}
	for _, record := range records {
		runtimeEvent, ok := record.Event.Payload.(coresession.RuntimeEmitted)
		if !ok {
			if ptr, ok := record.Event.Payload.(*coresession.RuntimeEmitted); ok && ptr != nil {
				runtimeEvent = *ptr
			} else {
				continue
			}
		}
		if err := state.ApplyNamedEvent(runtimeEvent.Name, runtimeEvent.Payload); err != nil {
			return err
		}
	}
	return nil
}

func (s Session) withDatasourceAccess(ctx operation.Context) operation.Context {
	if ctx == nil || s.Agent == nil {
		return ctx
	}
	spec := s.Agent.Spec()
	names := make([]coredatasource.Name, 0, len(spec.Datasources))
	for _, ref := range spec.Datasources {
		if ref.Name != "" {
			names = append(names, ref.Name)
		}
	}
	base := coredatasource.ContextWithAccessPolicy(ctx, coredatasource.AccessPolicy{Datasources: names})
	return operation.NewContext(base, ctx.Events())
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
func (c OperationCatalog) Resolve(ref string, scope resource.ResourceID) (OperationBinding, error) {
	if len(c) == 0 {
		return OperationBinding{}, fmt.Errorf("operation catalog is empty")
	}
	index := resource.NewResourceIndex()
	for _, binding := range c {
		index.Add(binding.ID)
	}
	resolver := resource.NewResolver(resource.ResolverConfig{Index: index})
	var (
		id  resource.ResourceID
		err error
	)
	if scope.IsZero() {
		id, err = resolver.Resolve("operation", ref)
	} else {
		id, err = resolver.ResolveInScope("operation", ref, scope)
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
