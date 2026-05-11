package channel

import (
	"testing"

	"github.com/fluxplane/agentruntime/core/command"
	"github.com/fluxplane/agentruntime/core/event"
)

func TestInboundValidateMessage(t *testing.T) {
	inbound := Inbound{Kind: InboundMessage, Message: &Message{Content: "hello"}}
	if err := inbound.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestInboundValidateRejectsUnionExtras(t *testing.T) {
	inbound := Inbound{
		Kind:    InboundMessage,
		Message: &Message{Content: "hello"},
		Command: &command.Invocation{Path: command.Path{"x"}},
	}
	if err := inbound.Validate(); err == nil {
		t.Fatal("Validate succeeded, want error")
	}
}

func TestInboundValidateCommandRequiresValidCommand(t *testing.T) {
	inbound := Inbound{Kind: InboundCommand, Command: &command.Invocation{}}
	if err := inbound.Validate(); err == nil {
		t.Fatal("Validate succeeded, want error")
	}
}

func TestOutboundValidateEvent(t *testing.T) {
	outbound := Outbound{Kind: OutboundEvent, Event: testEvent{}}
	if err := outbound.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestOutboundValidateRejectsUnionExtras(t *testing.T) {
	outbound := Outbound{
		Kind:    OutboundEvent,
		Event:   testEvent{},
		Message: &Message{Content: "hello"},
	}
	if err := outbound.Validate(); err == nil {
		t.Fatal("Validate succeeded, want error")
	}
}

type testEvent struct{}

func (testEvent) EventName() event.Name { return "test.event" }
