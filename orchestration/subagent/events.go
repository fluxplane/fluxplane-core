package subagent

import (
	"github.com/fluxplane/agentruntime/core/agent"
	"github.com/fluxplane/agentruntime/core/event"
	"github.com/fluxplane/agentruntime/core/operation"
	coresession "github.com/fluxplane/agentruntime/core/session"
	corethread "github.com/fluxplane/agentruntime/core/thread"
)

const (
	EventSpawnRequested event.Name = "subagent.spawn_requested"
	EventStarted        event.Name = "subagent.started"
	EventProgressed     event.Name = "subagent.progressed"
	EventCompleted      event.Name = "subagent.completed"
	EventFailed         event.Name = "subagent.failed"
	EventCancelled      event.Name = "subagent.cancelled"
)

// Causation identifies the parent operation that caused child work.
type Causation struct {
	ParentThreadID corethread.ID     `json:"parent_thread_id,omitempty"`
	ParentRunID    string            `json:"parent_run_id,omitempty"`
	ParentCallID   operation.CallID  `json:"parent_call_id,omitempty"`
	ChildThreadID  corethread.ID     `json:"child_thread_id,omitempty"`
	ChildRunID     string            `json:"child_run_id,omitempty"`
	Profile        coresession.Ref   `json:"profile,omitempty"`
	Agent          agent.Ref         `json:"agent,omitempty"`
	WorkerID       ID                `json:"worker_id,omitempty"`
	TaskID         string            `json:"task_id,omitempty"`
	Metadata       map[string]string `json:"metadata,omitempty"`
}

type SpawnRequested struct {
	Causation
	Task string `json:"task,omitempty"`
}

func (SpawnRequested) EventName() event.Name { return EventSpawnRequested }

type Started struct {
	Causation
	Task string `json:"task,omitempty"`
}

func (Started) EventName() event.Name { return EventStarted }

type Progressed struct {
	Causation
	Message string  `json:"message,omitempty"`
	Percent float64 `json:"percent,omitempty"`
}

func (Progressed) EventName() event.Name { return EventProgressed }

type Completed struct {
	Causation
	Output string `json:"output,omitempty"`
}

func (Completed) EventName() event.Name { return EventCompleted }

type Failed struct {
	Causation
	Error string `json:"error,omitempty"`
}

func (Failed) EventName() event.Name { return EventFailed }

type Cancelled struct {
	Causation
	Reason string `json:"reason,omitempty"`
}

func (Cancelled) EventName() event.Name { return EventCancelled }
