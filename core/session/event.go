package session

import (
	"github.com/fluxplane/agentruntime/core/channel"
	"github.com/fluxplane/agentruntime/core/command"
	"github.com/fluxplane/agentruntime/core/event"
	"github.com/fluxplane/agentruntime/core/operation"
	"github.com/fluxplane/agentruntime/core/policy"
)

const (
	EventCommandReceived    event.Name = "session.command.received"
	EventCommandRejected    event.Name = "session.command.rejected"
	EventOperationRequested event.Name = "session.operation.requested"
	EventOperationCompleted event.Name = "session.operation.completed"
	EventOutboundProduced   event.Name = "session.outbound.produced"
)

// CommandReceived records an inbound command accepted by the session boundary.
type CommandReceived struct {
	Command      command.Invocation      `json:"command"`
	Channel      channel.Ref             `json:"channel,omitempty"`
	Conversation channel.ConversationRef `json:"conversation,omitempty"`
	Caller       policy.Caller           `json:"caller,omitempty"`
}

func (CommandReceived) EventName() event.Name { return EventCommandReceived }

// CommandRejected records a command rejected before target execution.
type CommandRejected struct {
	Command command.Invocation `json:"command"`
	Reason  string             `json:"reason,omitempty"`
}

func (CommandRejected) EventName() event.Name { return EventCommandRejected }

// OperationRequested records a session request to execute an operation.
type OperationRequested struct {
	Operation operation.Ref   `json:"operation"`
	Input     operation.Value `json:"input,omitempty"`
}

func (OperationRequested) EventName() event.Name { return EventOperationRequested }

// OperationCompleted records an operation result observed by a session.
type OperationCompleted struct {
	Operation operation.Ref    `json:"operation"`
	Result    operation.Result `json:"result"`
}

func (OperationCompleted) EventName() event.Name { return EventOperationCompleted }

// OutboundProduced records output produced by a session.
type OutboundProduced struct {
	Message channel.Message `json:"message"`
}

func (OutboundProduced) EventName() event.Name { return EventOutboundProduced }
