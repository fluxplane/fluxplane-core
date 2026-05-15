// Package sessionenv assembles context state for session execution.
package sessionenv

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/fluxplane/agentruntime/core/agent"
	corecontext "github.com/fluxplane/agentruntime/core/context"
	coredatasource "github.com/fluxplane/agentruntime/core/datasource"
	"github.com/fluxplane/agentruntime/core/environment"
	"github.com/fluxplane/agentruntime/core/event"
	"github.com/fluxplane/agentruntime/core/operation"
	coresession "github.com/fluxplane/agentruntime/core/session"
	corethread "github.com/fluxplane/agentruntime/core/thread"
	"github.com/fluxplane/agentruntime/orchestration/subagent"
	contextruntime "github.com/fluxplane/agentruntime/runtime/context"
	operationruntime "github.com/fluxplane/agentruntime/runtime/operation"
	runtimeskill "github.com/fluxplane/agentruntime/runtime/skill"
)

// SubagentSupervisor aliases the supervisor type used by session environment
// wiring.
type SubagentSupervisor = subagent.Supervisor

// Config carries session state needed to materialize execution contexts.
type Config struct {
	Agent             agent.Agent
	Subagents         *subagent.Supervisor
	Thread            corethread.Ref
	ThreadStore       corethread.Store
	Delegation        coresession.DelegationPolicy
	RunID             string
	OperationExecutor operationruntime.Executor
	Events            event.Sink
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

// OperationContext adds session-scoped skill, datasource, call, and sub-agent
// state to an operation context.
func OperationContext(ctx operation.Context, cfg Config, callID operation.CallID) operation.Context {
	ctx = withSkillAccess(ctx, cfg.Agent)
	ctx = withDatasourceAccess(ctx, cfg.Agent)
	ctx = operation.WithCallID(ctx, callID)
	return withSubagentScope(ctx, cfg, callID)
}

// ContextProviderContext adds session-scoped datasource and sub-agent state to
// a context provider render context.
func ContextProviderContext(ctx context.Context, cfg Config, observations []environment.Observation) context.Context {
	ctx = ensureContext(ctx)
	ctx = WithBaseContext(ctx, cfg, "")
	ctx = coredatasource.ContextWithDetectionInput(ctx, detectionInputFromObservations(observations))
	if cfg.Agent == nil {
		return ctx
	}
	return datasourceAccessContext(ctx, cfg.Agent)
}

// WithBaseContext adds sub-agent scope to a non-operation context.
func WithBaseContext(ctx context.Context, cfg Config, callID operation.CallID) context.Context {
	if ctx == nil || cfg.Subagents == nil {
		return ctx
	}
	events := cfg.Events
	if events == nil {
		events = event.Discard()
	}
	return subagent.ContextWithScope(ctx, subagent.Scope{
		Supervisor:     cfg.Subagents,
		ParentThreadID: cfg.Thread.ID,
		ParentRunID:    cfg.RunID,
		ParentCallID:   callID,
		Policy:         cfg.Delegation,
		Events:         events,
		ThreadStore:    cfg.ThreadStore,
		Approver:       approverFromExecutor(cfg.OperationExecutor),
	})
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

// ActivateSkillTriggers activates skill and reference triggers for the current
// session agent before context rendering.
func ActivateSkillTriggers(text string, cfg Config) error {
	if cfg.Agent == nil {
		return nil
	}
	state, ok := runtimeskill.StateFromAgent(cfg.Agent)
	if !ok {
		return nil
	}
	events := cfg.Events
	if events == nil {
		events = event.Discard()
	}
	_, err := state.ActivateTriggers(text, events)
	return err
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

func withDatasourceAccess(ctx operation.Context, agent agent.Agent) operation.Context {
	if ctx == nil || agent == nil {
		return ctx
	}
	base := datasourceAccessContext(ctx, agent)
	return operation.NewContext(base, ctx.Events())
}

func withSubagentScope(ctx operation.Context, cfg Config, callID operation.CallID) operation.Context {
	if ctx == nil || cfg.Subagents == nil {
		return ctx
	}
	base := WithBaseContext(ctx, cfg, callID)
	return operation.NewContext(base, ctx.Events())
}

func datasourceAccessContext(ctx context.Context, agent agent.Agent) context.Context {
	spec := agent.Spec()
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

func approverFromExecutor(exec operationruntime.Executor) operationruntime.ApprovalGate {
	if env, ok := exec.Safety.(operationruntime.SafetyEnvelope); ok {
		return env.Approval
	}
	return nil
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
