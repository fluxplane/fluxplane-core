// Package sessionenv assembles context state for session execution.
package sessionenv

import (
	"context"
	"errors"
	"github.com/fluxplane/fluxplane-policy/policyauth"

	"github.com/fluxplane/fluxplane-core/core/agent"
	corecontext "github.com/fluxplane/fluxplane-core/core/context"
	coreevidence "github.com/fluxplane/fluxplane-core/core/evidence"
	"github.com/fluxplane/fluxplane-core/core/operation"
	coresession "github.com/fluxplane/fluxplane-core/core/session"
	corethread "github.com/fluxplane/fluxplane-core/core/thread"
	contextruntime "github.com/fluxplane/fluxplane-core/runtime/context"
	operationruntime "github.com/fluxplane/fluxplane-core/runtime/operation"
	runtimeskill "github.com/fluxplane/fluxplane-core/runtime/skill"
	coredatasource "github.com/fluxplane/fluxplane-datasource"
	"github.com/fluxplane/fluxplane-event"
	"github.com/fluxplane/fluxplane-policy"
)

// SessionSpec aliases the core session spec used by session orchestration.
type SessionSpec = coresession.Spec

// DelegationPolicy aliases the core session delegation policy.
type DelegationPolicy = coresession.DelegationPolicy

type (
	// InputReceived aliases the persisted session input event.
	InputReceived = coresession.InputReceived
	// AgentStepCompleted aliases the persisted agent-step event.
	AgentStepCompleted = coresession.AgentStepCompleted
	// OutboundProduced aliases the persisted outbound event.
	OutboundProduced = coresession.OutboundProduced
	// CommandReceived aliases the persisted command input event.
	CommandReceived = coresession.CommandReceived
	// TriggerReceived aliases the persisted trigger input event.
	TriggerReceived = coresession.TriggerReceived
	// CommandRejected aliases the persisted command rejection event.
	CommandRejected = coresession.CommandRejected
	// OperationRequested aliases the persisted operation request event.
	OperationRequested = coresession.OperationRequested
	// OperationCompleted aliases the persisted operation completion event.
	OperationCompleted = coresession.OperationCompleted
)

type (
	// Event aliases a runtime event payload.
	Event = event.Event
	// EventSink aliases an event sink.
	EventSink = event.Sink
	// EventSinkFunc adapts a function into an event sink.
	EventSinkFunc = event.SinkFunc
)

// OperationExecutor aliases the runtime operation executor used by sessions.
type OperationExecutor = operationruntime.Executor

// ResultReplacement aliases large-result replacement metadata.
type ResultReplacement = operationruntime.ResultReplacement

// Scope records the session coordinates that produced an execution context.
type Scope struct {
	Thread      corethread.Ref
	ThreadStore corethread.Store
	RunID       string
}

type scopeContextKey struct{}

// Config carries session state needed to materialize execution contexts.
type Config struct {
	Agent             agent.Agent
	Thread            corethread.Ref
	ThreadStore       corethread.Store
	Delegation        coresession.DelegationPolicy
	RunID             string
	OperationExecutor OperationExecutor
	Events            event.Sink
	Active            *ActiveState
}

type activeStateContextKey struct{}

// ContextWithActiveState attaches a snapshot of reaction-activated session
// capabilities to ctx.
func ContextWithActiveState(ctx context.Context, active ActiveState) context.Context {
	ctx = ensureContext(ctx)
	return context.WithValue(ctx, activeStateContextKey{}, active.Clone())
}

// ActiveStateFromContext returns reaction-activated session capabilities from
// ctx.
func ActiveStateFromContext(ctx context.Context) (ActiveState, bool) {
	if ctx == nil {
		return ActiveState{}, false
	}
	active, ok := ctx.Value(activeStateContextKey{}).(ActiveState)
	if !ok {
		return ActiveState{}, false
	}
	return active.Clone(), true
}

// BuildContext materializes provider context blocks.
func BuildContext(providers []corecontext.Provider, previous map[corecontext.ProviderName]corecontext.ProviderRenderRecord, ctx context.Context, req corecontext.BuildRequest) (corecontext.BuildResult, error) {
	return contextruntime.NewMaterializer(providers, previous).Build(ctx, req)
}

// RenderDiff renders a context diff for one placement.
func RenderDiff(result corecontext.BuildResult, placement corecontext.Placement) (string, bool) {
	return contextruntime.RenderDiff(result, placement)
}

// BlockFingerprint returns the stable fingerprint for a rendered context block.
func BlockFingerprint(block corecontext.Block) string {
	return contextruntime.BlockFingerprint(block)
}

// DiscardEvents returns an event sink that drops payloads.
func DiscardEvents() EventSink {
	return event.Discard()
}

// ThreadAppendRecords converts event payloads to append records scoped to a
// thread.
func ThreadAppendRecords(thread corethread.Ref, payloads ...Event) []corethread.AppendRecord {
	records := make([]corethread.AppendRecord, 0, len(payloads))
	for _, payload := range payloads {
		if payload == nil {
			continue
		}
		records = append(records, corethread.AppendRecord{
			Event: event.Record{
				Name:    payload.EventName(),
				Payload: payload,
				Scope: event.Scope{
					ThreadID: string(thread.ID),
				},
			},
		})
	}
	return records
}

// IsAppendConflict reports whether err is an event append conflict.
func IsAppendConflict(err error) bool {
	return errors.Is(err, event.ErrAppendConflict)
}

// OperationContext adds session-scoped skill, datasource, and call state to an
// operation context.
func OperationContext(ctx operation.Context, cfg Config, callID operation.CallID) operation.Context {
	ctx = withSkillAccess(ctx, cfg.Agent)
	active := cfg.Active
	if active == nil {
		if fromContext, ok := ActiveStateFromContext(ctx); ok {
			active = &fromContext
		}
	}
	ctx = withDatasourceAccess(ctx, cfg.Agent, active)
	ctx = operation.WithCallID(ctx, callID)
	return operation.NewContext(context.WithValue(ctx, scopeContextKey{}, Scope{Thread: cfg.Thread, ThreadStore: cfg.ThreadStore, RunID: cfg.RunID}), ctx.Events())
}

// ScopeFromContext returns the session coordinates attached to a session
// operation context.
func ScopeFromContext(ctx context.Context) (Scope, bool) {
	if ctx == nil {
		return Scope{}, false
	}
	scope, ok := ctx.Value(scopeContextKey{}).(Scope)
	if !ok || scope.Thread.ID == "" {
		return Scope{}, false
	}
	return scope, true
}

// ContextProviderContext adds session-scoped datasource state to a context
// provider render context.
func ContextProviderContext(ctx context.Context, cfg Config, observations []coreevidence.Observation) context.Context {
	ctx = ensureContext(ctx)
	ctx = WithBaseContext(ctx, cfg, "")
	if cfg.Agent == nil && cfg.Active == nil {
		return ctx
	}
	return datasourceAccessContext(ctx, cfg.Agent, cfg.Active)
}

// WithBaseContext returns the base session context for non-operation callers.
func WithBaseContext(ctx context.Context, cfg Config, callID operation.CallID) context.Context {
	if cfg.Active != nil {
		ctx = ContextWithActiveState(ctx, cfg.Active.Clone())
	}
	return ctx
}

// ReplaySkillEvents rehydrates skill activation state from persisted runtime
// events.
func ReplaySkillEvents(ctx context.Context, cfg Config) error {
	if cfg.ThreadStore == nil || cfg.Thread.ID == "" || cfg.Agent == nil {
		return nil
	}
	state, ok := runtimeskill.StateFromAgent(cfg.Agent)
	if !ok {
		return nil
	}
	snapshot, err := cfg.ThreadStore.Read(persistenceContext(ctx), corethread.ReadParams{ID: cfg.Thread.ID})
	if err != nil {
		if errors.Is(err, corethread.ErrNotFound) {
			return nil
		}
		return err
	}
	records, err := snapshot.EventsForBranch(cfg.Thread.BranchID)
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

func withSkillAccess(ctx operation.Context, agent agent.Agent) operation.Context {
	if ctx == nil || agent == nil {
		return ctx
	}
	state, ok := runtimeskill.StateFromAgent(agent)
	if !ok {
		return ctx
	}
	base := runtimeskill.ContextWithState(ctx, state)
	return operation.NewContext(base, ctx.Events())
}

func withDatasourceAccess(ctx operation.Context, agent agent.Agent, active *ActiveState) operation.Context {
	if ctx == nil || (agent == nil && active == nil) {
		return ctx
	}
	base := datasourceAccessContext(ctx, agent, active)
	return operation.NewContext(base, ctx.Events())
}

func datasourceAccessContext(ctx context.Context, agent agent.Agent, active *ActiveState) context.Context {
	var refs []coredatasource.Ref
	if agent != nil {
		refs = append(refs, agent.Spec().Datasources...)
	}
	if active != nil {
		for name, enabled := range active.Datasources {
			if enabled {
				refs = append(refs, coredatasource.Ref{Name: name})
			}
		}
	}
	seen := map[coredatasource.Name]bool{}
	names := make([]coredatasource.Name, 0, len(refs))
	for _, ref := range refs {
		if ref.Name != "" && !seen[ref.Name] && datasourceAuthorized(ctx, ref.Name) {
			seen[ref.Name] = true
			names = append(names, ref.Name)
		}
	}
	return coredatasource.ContextWithAccessPolicy(ctx, coredatasource.AccessPolicy{Datasources: names})
}

func datasourceAuthorized(ctx context.Context, name coredatasource.Name) bool {
	auth, ok := policyauth.AuthorizationFromContext(ctx)
	if !ok || auth.Policy.IsZero() {
		return true
	}
	for _, action := range []policy.Action{policy.ActionDatasourceSearch, policy.ActionDatasourceRead} {
		evaluation := policy.EvaluateAuthorization(auth.Policy, policy.AuthorizationRequest{
			Subjects: auth.Subjects,
			Trust:    auth.Trust,
			Resource: policy.ResourceRef{Kind: policy.ResourceDatasource, Name: string(name)},
			Action:   action,
		})
		if evaluation.Decision == policy.DecisionAllow || evaluation.Decision == policy.DecisionApprovalRequired {
			return true
		}
	}
	return false
}

// ApproverFromExecutor returns the approval gate carried by an operation
// executor safety envelope, if one is configured.
func ApproverFromExecutor(exec operationruntime.Executor) operationruntime.ApprovalGate {
	if env, ok := exec.Safety.(operationruntime.SafetyEnvelope); ok {
		return env.Approval
	}
	return nil
}

// ReplaceLargeResult applies the runtime large-result replacement policy.
func ReplaceLargeResult(ctx operation.Context, result operation.Result, ref operation.Ref, callID operation.CallID) (operation.Result, *ResultReplacement, error) {
	return operationruntime.ReplaceLargeResult(ctx, result, operationruntime.ReplacementOptions{
		Operation: ref,
		CallID:    callID,
	})
}

func ensureContext(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	return ctx
}

func persistenceContext(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	return context.WithoutCancel(ctx)
}
