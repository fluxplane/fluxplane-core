package client

import (
	"context"
	"fmt"
	"strings"

	"github.com/fluxplane/engine/core/agent"
	"github.com/fluxplane/engine/core/channel"
	"github.com/fluxplane/engine/core/command"
	"github.com/fluxplane/engine/core/event"
	"github.com/fluxplane/engine/core/operation"
	"github.com/fluxplane/engine/core/policy"
	coresession "github.com/fluxplane/engine/core/session"
	corethread "github.com/fluxplane/engine/core/thread"
	"github.com/fluxplane/engine/orchestration/session"
	operationruntime "github.com/fluxplane/engine/runtime/operation"
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
	Session      coresession.Ref         `json:"session,omitempty"`
	Profile      coresession.Spec        `json:"profile,omitempty"`
	Conversation channel.ConversationRef `json:"conversation,omitempty"`
	ThreadID     corethread.ID           `json:"thread_id,omitempty"`
	Metadata     map[string]string       `json:"metadata,omitempty"`
	// Approver is an optional in-process approval gate override. It is not
	// serialised and only applies to direct (non-remote) channel clients. Sub-
	// agent supervisors use it to propagate the parent session's approval policy
	// (e.g. AutoApprover for --yolo) into child sessions.
	Approver operationruntime.ApprovalGate `json:"-"`
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
	Session      coresession.Ref         `json:"session,omitempty"`
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

// DefaultRunEventBuffer is the standard per-run live event buffer used by
// channel transports. It is sized for model streaming bursts without making
// subscriber queues unbounded.
const DefaultRunEventBuffer = 1024

// SubmissionKind classifies what is being sent to a session.
type SubmissionKind string

const (
	SubmissionInput     SubmissionKind = "input"
	SubmissionCommand   SubmissionKind = "command"
	SubmissionOperation SubmissionKind = "operation"
	SubmissionEvent     SubmissionKind = "event"
	SubmissionTrigger   SubmissionKind = "trigger"
)

// OperationInvocation is a direct operation call submitted through a channel
// session.
type OperationInvocation struct {
	Operation operation.Ref   `json:"operation"`
	Input     operation.Value `json:"input,omitempty"`
}

// Validate checks that the invocation names an operation.
func (i OperationInvocation) Validate() error {
	if i.Operation.Name == "" {
		return fmt.Errorf("client: operation invocation name is empty")
	}
	return nil
}

// Submission is the neutral shape for anything sent to a session.
type Submission struct {
	ID          RunID                `json:"id,omitempty"`
	Kind        SubmissionKind       `json:"kind"`
	Input       *Input               `json:"input,omitempty"`
	Command     *command.Invocation  `json:"command,omitempty"`
	CommandLine string               `json:"command_line,omitempty"`
	Operation   *OperationInvocation `json:"operation,omitempty"`
	Event       event.Event          `json:"event,omitempty"`
	Trigger     *Trigger             `json:"trigger,omitempty"`
	Caller      policy.Caller        `json:"caller,omitempty"`
	Trust       policy.Trust         `json:"trust,omitempty"`
	// TrustDowngrade requests a lower trust level on remote transports that
	// explicitly allow trust simulation. It must never raise authority.
	TrustDowngrade *TrustDowngrade `json:"trust_downgrade,omitempty"`
	Metadata       map[string]any  `json:"metadata,omitempty"`
}

// TrustDowngrade is an explicit request to run below transport authority.
type TrustDowngrade struct {
	Level  policy.TrustLevel `json:"level"`
	Reason string            `json:"reason,omitempty"`
	Scopes []policy.Scope    `json:"scopes,omitempty"`
}

// NewSubmission returns an empty fluent submission value.
func NewSubmission() Submission {
	return Submission{}
}

// WithText configures the submission as conversational text input.
func (s Submission) WithText(text string) Submission {
	return s.WithInput(Input{Text: text})
}

// WithInput configures the submission as conversational input.
func (s Submission) WithInput(input Input) Submission {
	s.clearPayload()
	s.Kind = SubmissionInput
	s.Input = &input
	return s
}

// WithCommand configures the submission as a command invocation.
func (s Submission) WithCommand(invocation command.Invocation) Submission {
	s.clearPayload()
	s.Kind = SubmissionCommand
	s.Command = &invocation
	return s
}

// WithCommandLine configures the submission as a raw slash command line.
// The session command dispatcher owns parsing and catalog-based resolution.
func (s Submission) WithCommandLine(line string) Submission {
	s.clearPayload()
	s.Kind = SubmissionCommand
	s.CommandLine = line
	return s
}

// WithOperation configures the submission as a direct operation invocation.
func (s Submission) WithOperation(ref operation.Ref, input operation.Value) Submission {
	s.clearPayload()
	s.Kind = SubmissionOperation
	s.Operation = &OperationInvocation{Operation: ref, Input: input}
	return s
}

// WithEvent configures the submission as a domain event.
func (s Submission) WithEvent(ev event.Event) Submission {
	s.clearPayload()
	s.Kind = SubmissionEvent
	s.Event = ev
	return s
}

// WithTrigger configures the submission as a structured trigger.
func (s Submission) WithTrigger(trigger Trigger) Submission {
	s.clearPayload()
	s.Kind = SubmissionTrigger
	s.Trigger = &trigger
	return s
}

// WithID sets the client-visible run ID for the submission.
func (s Submission) WithID(id RunID) Submission {
	s.ID = id
	return s
}

// WithCaller sets caller identity metadata for the submission.
func (s Submission) WithCaller(caller policy.Caller) Submission {
	s.Caller = caller
	return s
}

// WithTrust sets caller trust metadata for the submission.
func (s Submission) WithTrust(trust policy.Trust) Submission {
	s.Trust = trust
	return s
}

// WithTrustDowngrade requests lower trust on transports that allow simulation.
func (s Submission) WithTrustDowngrade(downgrade TrustDowngrade) Submission {
	s.TrustDowngrade = &downgrade
	return s
}

// WithMetadata sets submission metadata.
func (s Submission) WithMetadata(metadata map[string]any) Submission {
	s.Metadata = metadata
	return s
}

func (s *Submission) clearPayload() {
	s.Input = nil
	s.Command = nil
	s.Operation = nil
	s.Event = nil
	s.Trigger = nil
}

// Input is a conversational/user input payload.
type Input struct {
	Text     string         `json:"text,omitempty"`
	Content  any            `json:"content,omitempty"`
	Metadata map[string]any `json:"metadata,omitempty"`
}

// ContentOrText returns structured content when set, otherwise the text field.
func (i Input) ContentOrText() any {
	if i.Content != nil {
		return i.Content
	}
	return i.Text
}

// Trigger is a structured non-message trigger, such as a scheduler or file
// watcher notification. Concrete timer/fs implementations belong outside this
// package.
type Trigger struct {
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
		if s.Command == nil && strings.TrimSpace(s.CommandLine) == "" {
			return fmt.Errorf("client: command submission payload is nil")
		}
		if s.Command != nil && strings.TrimSpace(s.CommandLine) != "" {
			return fmt.Errorf("client: command submission cannot carry both invocation and command line")
		}
		if s.Command != nil {
			if err := s.Command.Validate(); err != nil {
				return err
			}
		}
		return rejectSubmissionExtras(s, "command")
	case SubmissionOperation:
		if s.Operation == nil {
			return fmt.Errorf("client: operation submission payload is nil")
		}
		if err := s.Operation.Validate(); err != nil {
			return err
		}
		return rejectSubmissionExtras(s, "operation")
	case SubmissionEvent:
		if s.Event == nil {
			return fmt.Errorf("client: event submission payload is nil")
		}
		return rejectSubmissionExtras(s, "event")
	case SubmissionTrigger:
		if s.Trigger == nil {
			return fmt.Errorf("client: trigger submission payload is nil")
		}
		if s.Trigger.Name == "" {
			return fmt.Errorf("client: trigger name is empty")
		}
		return rejectSubmissionExtras(s, "trigger")
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
	if expected != "command" && strings.TrimSpace(submission.CommandLine) != "" {
		return fmt.Errorf("client: %s submission cannot also carry command line", expected)
	}
	if expected != "operation" && submission.Operation != nil {
		return fmt.Errorf("client: %s submission cannot also carry operation", expected)
	}
	if expected != "event" && submission.Event != nil {
		return fmt.Errorf("client: %s submission cannot also carry event", expected)
	}
	if expected != "trigger" && submission.Trigger != nil {
		return fmt.Errorf("client: %s submission cannot also carry trigger", expected)
	}
	return nil
}

// EventKind classifies client-facing run/session events.
type EventKind string

const (
	EventSubmissionReceived EventKind = "submission.received"
	EventInputCompleted     EventKind = "input.completed"
	EventCommandCompleted   EventKind = "command.completed"
	EventAgentStepCompleted EventKind = "agent_step.completed"
	EventOperationRequested EventKind = "operation.requested"
	EventOperationCompleted EventKind = "operation.completed"
	EventOutboundProduced   EventKind = "outbound.produced"
	EventRuntimeEmitted     EventKind = "runtime.emitted"
	EventRunCompleted       EventKind = "run.completed"
	EventRunFailed          EventKind = "run.failed"
)

// EventCursor identifies a durable position in a session event stream.
type EventCursor struct {
	Sequence event.Sequence `json:"sequence,omitempty"`
}

// OperationEvent describes one operation lifecycle event visible to clients.
type OperationEvent struct {
	CallID    operation.CallID  `json:"call_id,omitempty"`
	Operation operation.Ref     `json:"operation"`
	Input     operation.Value   `json:"input,omitempty"`
	Result    *operation.Result `json:"result,omitempty"`
}

// RuntimeEvent exposes a typed runtime/domain event to channel clients.
// Transports may decode Payload as a generic JSON object.
type RuntimeEvent struct {
	Name    event.Name `json:"name"`
	Payload any        `json:"payload,omitempty"`
}

// Event is a semantic event delivered to channel clients and run handles.
type Event struct {
	Kind       EventKind              `json:"kind"`
	Cursor     EventCursor            `json:"cursor,omitempty"`
	Replayed   bool                   `json:"replayed,omitempty"`
	RunID      RunID                  `json:"run_id,omitempty"`
	Session    SessionInfo            `json:"session,omitempty"`
	Submission *Submission            `json:"submission,omitempty"`
	Input      *session.InputResult   `json:"input,omitempty"`
	Command    *session.CommandResult `json:"command,omitempty"`
	Agent      *agent.StepResult      `json:"agent,omitempty"`
	Operation  *OperationEvent        `json:"operation,omitempty"`
	Outbound   *channel.Outbound      `json:"outbound,omitempty"`
	Runtime    *RuntimeEvent          `json:"runtime,omitempty"`
	Error      error                  `json:"-"`
}

// EventOptions configures session event subscriptions.
type EventOptions struct {
	Buffer int         `json:"buffer,omitempty"`
	Replay bool        `json:"replay,omitempty"`
	After  EventCursor `json:"after,omitempty"`
}

// Result is the terminal result of one run.
type Result struct {
	RunID      RunID                    `json:"run_id,omitempty"`
	Session    SessionInfo              `json:"session"`
	Submission Submission               `json:"submission"`
	Input      *session.InputResult     `json:"input,omitempty"`
	Command    *session.CommandResult   `json:"command,omitempty"`
	Operation  *session.OperationResult `json:"operation,omitempty"`
	Outbound   *channel.Outbound        `json:"outbound,omitempty"`
}
