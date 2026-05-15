// Package eventregistry assembles event decoder registries for composed apps.
package eventregistry

import (
	"fmt"

	corecontext "github.com/fluxplane/agentruntime/core/context"
	coreconversation "github.com/fluxplane/agentruntime/core/conversation"
	"github.com/fluxplane/agentruntime/core/event"
	"github.com/fluxplane/agentruntime/core/operation"
	"github.com/fluxplane/agentruntime/core/resource"
	coresession "github.com/fluxplane/agentruntime/core/session"
	"github.com/fluxplane/agentruntime/core/skill"
	corethread "github.com/fluxplane/agentruntime/core/thread"
	"github.com/fluxplane/agentruntime/core/usage"
	"github.com/fluxplane/agentruntime/orchestration/subagent"
	llmagent "github.com/fluxplane/agentruntime/runtime/agent/llmagent"
	"github.com/fluxplane/agentruntime/runtime/system"
)

// Config describes event payload types visible to an app.
type Config struct {
	Bundles    []resource.ContributionBundle
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
	for _, bundle := range cfg.Bundles {
		for _, sample := range bundle.EventTypes {
			if err := registerEventType(registry, sample); err != nil {
				return nil, fmt.Errorf("app: event type from %s: %w", sourceLabel(bundle.Source), err)
			}
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
		subagent.SpawnRequested{},
		subagent.Started{},
		subagent.Progressed{},
		subagent.Completed{},
		subagent.Failed{},
		subagent.Cancelled{},
	}
}

func sourceLabel(source resource.SourceRef) string {
	if source.ID != "" {
		return source.ID
	}
	if source.Location != "" {
		return source.Location
	}
	if source.Ref != "" {
		return source.Ref
	}
	return "unknown source"
}
