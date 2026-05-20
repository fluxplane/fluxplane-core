package channel

import (
	"fmt"
	"strings"

	"github.com/fluxplane/agentruntime/core/command"
	"github.com/fluxplane/agentruntime/core/event"
	"github.com/fluxplane/agentruntime/core/operation"
	"github.com/fluxplane/agentruntime/core/policy"
	"github.com/fluxplane/agentruntime/core/user"
)

// Name identifies a channel.
type Name string

// Ref identifies a channel by name.
type Ref struct {
	Name Name `json:"name"`
}

// Kind describes a channel family without importing protocol adapters.
type Kind string

const (
	KindLocal    Kind = "local"
	KindService  Kind = "service"
	KindExternal Kind = "external"
	KindEmbedded Kind = "embedded"
)

// SharingMode describes how external conversations map to runtime sessions.
type SharingMode string

const (
	SharingPrivate      SharingMode = "private"
	SharingConversation SharingMode = "conversation"
	SharingChannel      SharingMode = "channel"
	SharingGlobal       SharingMode = "global"
)

// Spec is an inert channel definition.
type Spec struct {
	Name        Name              `json:"name"`
	Description string            `json:"description,omitempty"`
	Kind        Kind              `json:"kind,omitempty"`
	Provider    Provider          `json:"provider,omitempty"`
	Sharing     SharingMode       `json:"sharing,omitempty"`
	Annotations map[string]string `json:"annotations,omitempty"`
}

// Provider describes the concrete adapter/provider behind a generic channel
// kind without making the provider a core concept.
type Provider struct {
	Name          string `json:"name,omitempty"`
	DisplayName   string `json:"display_name,omitempty"`
	Documentation string `json:"documentation,omitempty"`
}

// ConversationRef identifies an external conversation/thread boundary.
type ConversationRef struct {
	ID string `json:"id,omitempty"`
}

// Message is a payload. Routing, channel, caller, trust, and conversation
// metadata belong to inbound/outbound envelopes.
type Message struct {
	Content  any            `json:"content,omitempty"`
	Metadata map[string]any `json:"metadata,omitempty"`
}

// InboundKind classifies inbound payload shape.
type InboundKind string

const (
	InboundMessage   InboundKind = "message"
	InboundCommand   InboundKind = "command"
	InboundOperation InboundKind = "operation"
	InboundEvent     InboundKind = "event"
)

// OperationInvocation is a direct operation submission entering a session
// through a channel.
type OperationInvocation struct {
	Operation operation.Ref   `json:"operation"`
	Input     operation.Value `json:"input,omitempty"`
}

// Validate checks that the invocation names an operation.
func (i OperationInvocation) Validate() error {
	if i.Operation.Name == "" {
		return fmt.Errorf("channel: operation invocation name is empty")
	}
	return nil
}

// Inbound is the normalized envelope for external input entering the runtime.
type Inbound struct {
	ID            string               `json:"id,omitempty"`
	Channel       Ref                  `json:"channel,omitempty"`
	Conversation  ConversationRef      `json:"conversation,omitempty"`
	Caller        policy.Caller        `json:"caller,omitempty"`
	Trust         policy.Trust         `json:"trust,omitempty"`
	Actor         *user.Actor          `json:"actor,omitempty"`
	Kind          InboundKind          `json:"kind"`
	Message       *Message             `json:"message,omitempty"`
	Command       *command.Invocation  `json:"command,omitempty"`
	CommandLine   string               `json:"command_line,omitempty"`
	Operation     *OperationInvocation `json:"operation,omitempty"`
	Event         event.Event          `json:"event,omitempty"`
	CorrelationID string               `json:"correlation_id,omitempty"`
}

// Validate checks that the inbound envelope has exactly the payload required by
// Kind.
func (i Inbound) Validate() error {
	switch i.Kind {
	case InboundMessage:
		if i.Message == nil {
			return fmt.Errorf("channel: inbound message payload is nil")
		}
		return rejectInboundExtras(i, "message")
	case InboundCommand:
		if i.Command == nil && strings.TrimSpace(i.CommandLine) == "" {
			return fmt.Errorf("channel: inbound command payload is nil")
		}
		if i.Command != nil && strings.TrimSpace(i.CommandLine) != "" {
			return fmt.Errorf("channel: inbound command cannot carry both invocation and command line")
		}
		if i.Command != nil {
			if err := i.Command.Validate(); err != nil {
				return err
			}
		}
		return rejectInboundExtras(i, "command")
	case InboundOperation:
		if i.Operation == nil {
			return fmt.Errorf("channel: inbound operation payload is nil")
		}
		if err := i.Operation.Validate(); err != nil {
			return err
		}
		return rejectInboundExtras(i, "operation")
	case InboundEvent:
		if i.Event == nil {
			return fmt.Errorf("channel: inbound event payload is nil")
		}
		return rejectInboundExtras(i, "event")
	default:
		return fmt.Errorf("channel: inbound kind %q is invalid", i.Kind)
	}
}

// OutboundKind classifies outbound payload shape.
type OutboundKind string

const (
	OutboundMessage OutboundKind = "message"
	OutboundEvent   OutboundKind = "event"
)

// Outbound is the normalized envelope for output leaving the runtime through a
// channel adapter.
type Outbound struct {
	ID            string           `json:"id,omitempty"`
	Channel       Ref              `json:"channel,omitempty"`
	Conversation  ConversationRef  `json:"conversation,omitempty"`
	Recipient     policy.Principal `json:"recipient,omitempty"`
	Kind          OutboundKind     `json:"kind"`
	Message       *Message         `json:"message,omitempty"`
	Event         event.Event      `json:"event,omitempty"`
	CorrelationID string           `json:"correlation_id,omitempty"`
	CausationID   string           `json:"causation_id,omitempty"`
}

// Validate checks that the outbound envelope has exactly the payload required
// by Kind.
func (o Outbound) Validate() error {
	switch o.Kind {
	case OutboundMessage:
		if o.Message == nil {
			return fmt.Errorf("channel: outbound message payload is nil")
		}
		if o.Event != nil {
			return fmt.Errorf("channel: outbound message cannot also carry event")
		}
		return nil
	case OutboundEvent:
		if o.Event == nil {
			return fmt.Errorf("channel: outbound event payload is nil")
		}
		if o.Message != nil {
			return fmt.Errorf("channel: outbound event cannot also carry message")
		}
		return nil
	default:
		return fmt.Errorf("channel: outbound kind %q is invalid", o.Kind)
	}
}

func rejectInboundExtras(inbound Inbound, expected string) error {
	if expected != "message" && inbound.Message != nil {
		return fmt.Errorf("channel: inbound %s cannot also carry message", expected)
	}
	if expected != "command" && inbound.Command != nil {
		return fmt.Errorf("channel: inbound %s cannot also carry command", expected)
	}
	if expected != "command" && strings.TrimSpace(inbound.CommandLine) != "" {
		return fmt.Errorf("channel: inbound %s cannot also carry command line", expected)
	}
	if expected != "operation" && inbound.Operation != nil {
		return fmt.Errorf("channel: inbound %s cannot also carry operation", expected)
	}
	if expected != "event" && inbound.Event != nil {
		return fmt.Errorf("channel: inbound %s cannot also carry event", expected)
	}
	return nil
}
