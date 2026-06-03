package sessionrun

import (
	"github.com/fluxplane/fluxplane-core/core/agent"
	coresession "github.com/fluxplane/fluxplane-core/core/session"
	corethread "github.com/fluxplane/fluxplane-core/core/thread"
	"github.com/fluxplane/fluxplane-event"
	"github.com/fluxplane/fluxplane-operation"
)

const (
	EventRequested  event.Name = "session_run.requested"
	EventStarted    event.Name = "session_run.started"
	EventProgressed event.Name = "session_run.progressed"
	EventCompleted  event.Name = "session_run.completed"
	EventFailed     event.Name = "session_run.failed"
	EventCancelled  event.Name = "session_run.cancelled"
)

// ID identifies one session run request.
type ID string

// Causation identifies the parent execution that requested a session run.
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
	Input string `json:"input,omitempty"`
}

func (Requested) EventName() event.Name { return EventRequested }

type Started struct {
	Causation
	Input string `json:"input,omitempty"`
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
