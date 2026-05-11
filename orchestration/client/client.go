package client

import (
	"context"
	"fmt"

	"github.com/fluxplane/agentruntime/core/channel"
	"github.com/fluxplane/agentruntime/core/command"
	"github.com/fluxplane/agentruntime/core/event"
	"github.com/fluxplane/agentruntime/core/policy"
	corethread "github.com/fluxplane/agentruntime/core/thread"
	"github.com/fluxplane/agentruntime/orchestration/session"
)

// ChannelClient opens and discovers sessions through one channel transport.
type ChannelClient interface {
	Open(context.Context, OpenRequest) (SessionHandle, error)
	Resume(context.Context, ResumeRequest) (SessionHandle, error)
	ListSessions(context.Context, ListSessionsRequest) ([]SessionSummary, error)
}

// SessionHandle is the user-facing handle for one opened/resumed session.
type SessionHandle interface {
	Info() SessionInfo
	Submit(context.Context, Submission) (RunHandle, error)
	SendCommand(context.Context, command.Invocation) (RunHandle, error)
	SendInput(context.Context, Input) (RunHandle, error)
	Events(context.Context, EventOptions) (<-chan Event, func(), error)
	OnEvent(context.Context, func(Event)) (func(), error)
	Close(context.Context) error
}

// RunHandle tracks one submitted interaction.
type RunHandle interface {
	ID() RunID
	Session() SessionInfo
	Submission() Submission
	Events() <-chan Event
	Done() <-chan struct{}
	Err() error
	Wait(context.Context) (Result, error)
}

// OpenRequest opens or creates a session for a channel conversation.
type OpenRequest struct {
	Conversation channel.ConversationRef `json:"conversation,omitempty"`
	ThreadID     corethread.ID           `json:"thread_id,omitempty"`
	Metadata     map[string]string       `json:"metadata,omitempty"`
}

// ResumeRequest resumes a known session/thread.
type ResumeRequest struct {
	ThreadID corethread.ID `json:"thread_id"`
}

// ListSessionsRequest filters session discovery.
type ListSessionsRequest struct {
	IncludeArchived bool `json:"include_archived,omitempty"`
	Limit           int  `json:"limit,omitempty"`
}

// SessionInfo is stable session identity exposed to clients.
type SessionInfo struct {
	Thread       corethread.Ref          `json:"thread"`
	Channel      channel.Ref             `json:"channel,omitempty"`
	Conversation channel.ConversationRef `json:"conversation,omitempty"`
	Metadata     map[string]string       `json:"metadata,omitempty"`
	Resumed      bool                    `json:"resumed,omitempty"`
}

// SessionSummary is a list view of a session.
type SessionSummary struct {
	Info     SessionInfo `json:"info"`
	Archived bool        `json:"archived,omitempty"`
}

// RunID identifies one submitted interaction.
type RunID string

// SubmissionKind classifies what is being sent to a session.
type SubmissionKind string

const (
	SubmissionInput   SubmissionKind = "input"
	SubmissionCommand SubmissionKind = "command"
	SubmissionEvent   SubmissionKind = "event"
	SubmissionSignal  SubmissionKind = "signal"
)

// Submission is the neutral shape for anything sent to a session.
type Submission struct {
	ID       RunID          `json:"id,omitempty"`
	Kind     SubmissionKind `json:"kind"`
	Input    *Input         `json:"input,omitempty"`
	Command  *command.Invocation
	Event    event.Event    `json:"event,omitempty"`
	Signal   *Signal        `json:"signal,omitempty"`
	Caller   policy.Caller  `json:"caller,omitempty"`
	Trust    policy.Trust   `json:"trust,omitempty"`
	Metadata map[string]any `json:"metadata,omitempty"`
}

// Input is a conversational/user input payload.
type Input struct {
	Text     string         `json:"text,omitempty"`
	Content  any            `json:"content,omitempty"`
	Metadata map[string]any `json:"metadata,omitempty"`
}

// Signal is a structured non-message trigger, such as a scheduler or file
// watcher notification. Concrete timer/fs implementations belong outside this
// package.
type Signal struct {
	Name     string         `json:"name"`
	Source   string         `json:"source,omitempty"`
	Payload  any            `json:"payload,omitempty"`
	Metadata map[string]any `json:"metadata,omitempty"`
}

// Validate checks that the submission carries exactly the payload required by
// Kind.
func (s Submission) Validate() error {
	switch s.Kind {
	case SubmissionInput:
		if s.Input == nil {
			return fmt.Errorf("client: input submission payload is nil")
		}
		return rejectSubmissionExtras(s, "input")
	case SubmissionCommand:
		if s.Command == nil {
			return fmt.Errorf("client: command submission payload is nil")
		}
		if err := s.Command.Validate(); err != nil {
			return err
		}
		return rejectSubmissionExtras(s, "command")
	case SubmissionEvent:
		if s.Event == nil {
			return fmt.Errorf("client: event submission payload is nil")
		}
		return rejectSubmissionExtras(s, "event")
	case SubmissionSignal:
		if s.Signal == nil {
			return fmt.Errorf("client: signal submission payload is nil")
		}
		if s.Signal.Name == "" {
			return fmt.Errorf("client: signal name is empty")
		}
		return rejectSubmissionExtras(s, "signal")
	default:
		return fmt.Errorf("client: submission kind %q is invalid", s.Kind)
	}
}

func rejectSubmissionExtras(submission Submission, expected string) error {
	if expected != "input" && submission.Input != nil {
		return fmt.Errorf("client: %s submission cannot also carry input", expected)
	}
	if expected != "command" && submission.Command != nil {
		return fmt.Errorf("client: %s submission cannot also carry command", expected)
	}
	if expected != "event" && submission.Event != nil {
		return fmt.Errorf("client: %s submission cannot also carry event", expected)
	}
	if expected != "signal" && submission.Signal != nil {
		return fmt.Errorf("client: %s submission cannot also carry signal", expected)
	}
	return nil
}

// EventKind classifies client-facing run/session events.
type EventKind string

const (
	EventSubmissionReceived EventKind = "submission.received"
	EventCommandCompleted   EventKind = "command.completed"
	EventOutboundProduced   EventKind = "outbound.produced"
	EventRunCompleted       EventKind = "run.completed"
	EventRunFailed          EventKind = "run.failed"
)

// Event is a semantic event delivered to channel clients and run handles.
type Event struct {
	Kind       EventKind   `json:"kind"`
	RunID      RunID       `json:"run_id,omitempty"`
	Session    SessionInfo `json:"session,omitempty"`
	Submission *Submission `json:"submission,omitempty"`
	Command    *session.CommandResult
	Outbound   *channel.Outbound
	Error      error `json:"-"`
}

// EventOptions configures session event subscriptions.
type EventOptions struct {
	Buffer int `json:"buffer,omitempty"`
}

// Result is the terminal result of one run.
type Result struct {
	RunID      RunID       `json:"run_id,omitempty"`
	Session    SessionInfo `json:"session"`
	Submission Submission  `json:"submission"`
	Command    *session.CommandResult
	Outbound   *channel.Outbound
}
