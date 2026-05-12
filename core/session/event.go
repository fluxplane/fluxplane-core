package session

import (
	"github.com/fluxplane/agentruntime/core/agent"
	"github.com/fluxplane/agentruntime/core/channel"
	"github.com/fluxplane/agentruntime/core/command"
	"github.com/fluxplane/agentruntime/core/event"
	"github.com/fluxplane/agentruntime/core/operation"
	"github.com/fluxplane/agentruntime/core/policy"
)

const (
	EventInputReceived      event.Name = "session.input.received"
	EventCommandReceived    event.Name = "session.command.received"
	EventCommandRejected    event.Name = "session.command.rejected"
	EventAgentStepCompleted event.Name = "session.agent_step.completed"
	EventOperationRequested event.Name = "session.operation.requested"
	EventOperationCompleted event.Name = "session.operation.completed"
	EventOutboundProduced   event.Name = "session.outbound.produced"
)

// InputReceived records inbound conversational input accepted by the session
// boundary.
type InputReceived struct {
	RunID        string                  `json:"run_id,omitempty"`
	Message      channel.Message         `json:"message"`
	Channel      channel.Ref             `json:"channel,omitempty"`
	Conversation channel.ConversationRef `json:"conversation,omitempty"`
	Caller       policy.Caller           `json:"caller,omitempty"`
	Trust        policy.Trust            `json:"trust,omitempty"`
}

func (InputReceived) EventName() event.Name { return EventInputReceived }

// CommandReceived records an inbound command accepted by the session boundary.
type CommandReceived struct {
	RunID        string                  `json:"run_id,omitempty"`
	Command      command.Invocation      `json:"command"`
	Channel      channel.Ref             `json:"channel,omitempty"`
	Conversation channel.ConversationRef `json:"conversation,omitempty"`
	Caller       policy.Caller           `json:"caller,omitempty"`
	Trust        policy.Trust            `json:"trust,omitempty"`
}

func (CommandReceived) EventName() event.Name { return EventCommandReceived }

// CommandRejected records a command rejected before target execution.
type CommandRejected struct {
	RunID   string             `json:"run_id,omitempty"`
	Command command.Invocation `json:"command"`
	Reason  string             `json:"reason,omitempty"`
}

func (CommandRejected) EventName() event.Name { return EventCommandRejected }

// AgentStepCompleted records the result of one agent decision step.
type AgentStepCompleted struct {
	RunID  string           `json:"run_id,omitempty"`
	Result agent.StepResult `json:"result"`
}

func (AgentStepCompleted) EventName() event.Name { return EventAgentStepCompleted }

// OperationRequested records a session request to execute an operation.
type OperationRequested struct {
	RunID     string           `json:"run_id,omitempty"`
	CallID    operation.CallID `json:"call_id,omitempty"`
	Operation operation.Ref    `json:"operation"`
	Input     operation.Value  `json:"input,omitempty"`
}

func (OperationRequested) EventName() event.Name { return EventOperationRequested }

// OperationCompleted records an operation result observed by a session.
type OperationCompleted struct {
	RunID     string           `json:"run_id,omitempty"`
	CallID    operation.CallID `json:"call_id,omitempty"`
	Operation operation.Ref    `json:"operation"`
	Result    operation.Result `json:"result"`
}

func (OperationCompleted) EventName() event.Name { return EventOperationCompleted }

// OutboundProduced records output produced by a session.
type OutboundProduced struct {
	RunID   string          `json:"run_id,omitempty"`
	Message channel.Message `json:"message"`
}

func (OutboundProduced) EventName() event.Name { return EventOutboundProduced }
