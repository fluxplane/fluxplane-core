// Package eventregistry assembles event decoder registries for composed apps.
package eventregistry

import (
	"fmt"
	corepolicy "github.com/fluxplane/fluxplane-core/core/policy"

	corecontext "github.com/fluxplane/fluxplane-core/core/context"
	coreconversation "github.com/fluxplane/fluxplane-core/core/conversation"
	coregoal "github.com/fluxplane/fluxplane-core/core/goal"
	corememory "github.com/fluxplane/fluxplane-core/core/memory"
	"github.com/fluxplane/fluxplane-core/core/operation"
	corereaction "github.com/fluxplane/fluxplane-core/core/reaction"
	coresession "github.com/fluxplane/fluxplane-core/core/session"
	"github.com/fluxplane/fluxplane-core/core/skill"
	coretask "github.com/fluxplane/fluxplane-core/core/task"
	corethread "github.com/fluxplane/fluxplane-core/core/thread"
	"github.com/fluxplane/fluxplane-core/core/usage"
	coreworkflow "github.com/fluxplane/fluxplane-core/core/workflow"
	"github.com/fluxplane/fluxplane-core/orchestration/sessionagent"
	"github.com/fluxplane/fluxplane-core/orchestration/sessionrun"
	"github.com/fluxplane/fluxplane-core/runtime/agent/llmagent"
	operationruntime "github.com/fluxplane/fluxplane-core/runtime/operation"
	"github.com/fluxplane/fluxplane-event"
	fpsystem "github.com/fluxplane/fluxplane-system"
)

// Config describes event payload types visible to an app.
type Config struct {
	EventTypes []event.Event
}

// New builds a decoder registry for runtime event payloads.
func New(cfg Config) (*event.Registry, error) {
	registry := event.NewRegistry()
	if err := corethread.RegisterEvents(registry); err != nil {
		return nil, fmt.Errorf("app: register thread events: %w", err)
	}
	if err := coresession.RegisterEvents(registry); err != nil {
		return nil, fmt.Errorf("app: register session events: %w", err)
	}
	if err := coreworkflow.RegisterEvents(registry); err != nil {
		return nil, fmt.Errorf("app: register workflow events: %w", err)
	}
	if err := coretask.RegisterEvents(registry); err != nil {
		return nil, fmt.Errorf("app: register task events: %w", err)
	}
	if err := corememory.RegisterEvents(registry); err != nil {
		return nil, fmt.Errorf("app: register memory events: %w", err)
	}
	if err := coregoal.RegisterEvents(registry); err != nil {
		return nil, fmt.Errorf("app: register goal events: %w", err)
	}
	for _, sample := range defaultEventTypes() {
		if err := registerEventType(registry, sample); err != nil {
			return nil, err
		}
	}
	for _, sample := range cfg.EventTypes {
		if err := registerEventType(registry, sample); err != nil {
			return nil, err
		}
	}
	return registry, nil
}

func registerEventType(registry *event.Registry, sample event.Event) error {
	if sample == nil {
		return fmt.Errorf("app: event type sample is nil")
	}
	if err := registry.Register(sample); err != nil {
		return fmt.Errorf("app: register event type %q: %w", sample.EventName(), err)
	}
	return nil
}

func defaultEventTypes() []event.Event {
	return []event.Event{
		operation.OperationStarted{},
		operation.OperationCompleted{},
		operation.OperationFailed{},
		operation.OperationRejected{},
		operation.OperationCanceled{},
		corepolicy.AuthorizationDecision{},
		operationruntime.ApprovalRequested{},
		operationruntime.ApprovalGranted{},
		operationruntime.ApprovalDenied{},
		corereaction.ActionPlanned{},
		corereaction.ActionApplied{},
		corereaction.ActionSkipped{},
		corereaction.Diagnostic{},
		coreconversation.ItemsAppended{},
		coreconversation.ContinuationStored{},
		coreconversation.CompactionStored{},
		corecontext.BlockRecorded{},
		corecontext.BlockRemovedRecorded{},
		corecontext.RenderCommitted{},
		usage.Recorded{},
		skill.SkillActivated{},
		skill.SkillReferenceActivated{},
		llmagent.ModelRequested{},
		llmagent.ModelStreamed{},
		llmagent.ModelCompleted{},
		llmagent.ModelFailed{},
		fpsystem.ProcessEvent{Kind: fpsystem.ProcessEventStarted},
		fpsystem.ProcessEvent{Kind: fpsystem.ProcessEventOutput},
		fpsystem.ProcessEvent{Kind: fpsystem.ProcessEventExited},
		sessionagent.Requested{},
		sessionagent.Started{},
		sessionagent.Progressed{},
		sessionagent.Completed{},
		sessionagent.Failed{},
		sessionagent.Cancelled{},
		sessionrun.Requested{},
		sessionrun.Started{},
		sessionrun.Progressed{},
		sessionrun.Completed{},
		sessionrun.Failed{},
		sessionrun.Cancelled{},
	}
}
