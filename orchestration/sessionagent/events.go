// Package sessionagent runs short-lived command helper sessions.
package sessionagent

import (
	"github.com/fluxplane/agentruntime/core/agent"
	"github.com/fluxplane/agentruntime/core/event"
	"github.com/fluxplane/agentruntime/core/operation"
	coresession "github.com/fluxplane/agentruntime/core/session"
	corethread "github.com/fluxplane/agentruntime/core/thread"
)

const (
	EventRequested  event.Name = "session_agent.requested"
	EventStarted    event.Name = "session_agent.started"
	EventProgressed event.Name = "session_agent.progressed"
	EventCompleted  event.Name = "session_agent.completed"
	EventFailed     event.Name = "session_agent.failed"
	EventCancelled  event.Name = "session_agent.cancelled"
)

// ID identifies one command helper session run.
type ID string

// Causation identifies the parent command that started a helper session.
type Causation struct {
	ID             ID                `json:"id,omitempty"`
	ParentThreadID corethread.ID     `json:"parent_thread_id,omitempty"`
	ParentRunID    string            `json:"parent_run_id,omitempty"`
	ParentCallID   operation.CallID  `json:"parent_call_id,omitempty"`
	ChildThreadID  corethread.ID     `json:"child_thread_id,omitempty"`
	ChildRunID     string            `json:"child_run_id,omitempty"`
	Profile        coresession.Ref   `json:"profile,omitempty"`
	Agent          agent.Ref         `json:"agent,omitempty"`
	TaskID         string            `json:"task_id,omitempty"`
	Metadata       map[string]string `json:"metadata,omitempty"`
}

type Requested struct {
	Causation
	Task string `json:"task,omitempty"`
}

func (Requested) EventName() event.Name { return EventRequested }

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
