// Package eventregistry assembles event decoder registries for composed apps.
package eventregistry

import (
	"fmt"

	corecontext "github.com/fluxplane/engine/core/context"
	coreconversation "github.com/fluxplane/engine/core/conversation"
	"github.com/fluxplane/engine/core/event"
	coregoal "github.com/fluxplane/engine/core/goal"
	corememory "github.com/fluxplane/engine/core/memory"
	"github.com/fluxplane/engine/core/operation"
	corereaction "github.com/fluxplane/engine/core/reaction"
	coresession "github.com/fluxplane/engine/core/session"
	"github.com/fluxplane/engine/core/skill"
	coretask "github.com/fluxplane/engine/core/task"
	corethread "github.com/fluxplane/engine/core/thread"
	"github.com/fluxplane/engine/core/usage"
	coreworkflow "github.com/fluxplane/engine/core/workflow"
	"github.com/fluxplane/engine/orchestration/sessionagent"
	llmagent "github.com/fluxplane/engine/runtime/agent/llmagent"
	operationruntime "github.com/fluxplane/engine/runtime/operation"
	"github.com/fluxplane/engine/runtime/system"
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
		event.AuthorizationDecision{},
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
		system.ProcessEvent{Kind: "started"},
		system.ProcessEvent{Kind: "output"},
		system.ProcessEvent{Kind: "exited"},
		sessionagent.Requested{},
		sessionagent.Started{},
		sessionagent.Progressed{},
		sessionagent.Completed{},
		sessionagent.Failed{},
		sessionagent.Cancelled{},
	}
}
