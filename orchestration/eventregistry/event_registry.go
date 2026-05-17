// Package eventregistry assembles event decoder registries for composed apps.
package eventregistry

import (
	"fmt"

	corecontext "github.com/fluxplane/agentruntime/core/context"
	coreconversation "github.com/fluxplane/agentruntime/core/conversation"
	"github.com/fluxplane/agentruntime/core/event"
	"github.com/fluxplane/agentruntime/core/operation"
	coresession "github.com/fluxplane/agentruntime/core/session"
	"github.com/fluxplane/agentruntime/core/skill"
	coretask "github.com/fluxplane/agentruntime/core/task"
	corethread "github.com/fluxplane/agentruntime/core/thread"
	"github.com/fluxplane/agentruntime/core/usage"
	coreworkflow "github.com/fluxplane/agentruntime/core/workflow"
	"github.com/fluxplane/agentruntime/orchestration/sessionagent"
	llmagent "github.com/fluxplane/agentruntime/runtime/agent/llmagent"
	operationruntime "github.com/fluxplane/agentruntime/runtime/operation"
	"github.com/fluxplane/agentruntime/runtime/system"
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
