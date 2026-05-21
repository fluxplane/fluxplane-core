package channel

import (
	"testing"

	"github.com/fluxplane/engine/core/command"
	"github.com/fluxplane/engine/core/event"
	"github.com/fluxplane/engine/core/operation"
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

func TestInboundValidateOperation(t *testing.T) {
	inbound := Inbound{
		Kind: InboundOperation,
		Operation: &OperationInvocation{
			Operation: operation.Ref{Name: "echo"},
			Input:     "hello",
		},
	}
	if err := inbound.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestInboundValidateOperationRequiresName(t *testing.T) {
	inbound := Inbound{
		Kind:      InboundOperation,
		Operation: &OperationInvocation{},
	}
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

func TestInboundValidateEvent(t *testing.T) {
	inbound := Inbound{Kind: InboundEvent, Event: testEvent{}}
	if err := inbound.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestInboundValidateRejectsNilMessage(t *testing.T) {
	inbound := Inbound{Kind: InboundMessage}
	if err := inbound.Validate(); err == nil {
		t.Fatal("Validate succeeded, want error for nil message")
	}
}

func TestInboundValidateRejectsNilCommand(t *testing.T) {
	inbound := Inbound{Kind: InboundCommand}
	if err := inbound.Validate(); err == nil {
		t.Fatal("Validate succeeded, want error for nil command")
	}
}

func TestInboundValidateRejectsNilEvent(t *testing.T) {
	inbound := Inbound{Kind: InboundEvent}
	if err := inbound.Validate(); err == nil {
		t.Fatal("Validate succeeded, want error for nil event")
	}
}

func TestInboundValidateRejectsInvalidKind(t *testing.T) {
	inbound := Inbound{Kind: InboundKind("invalid")}
	if err := inbound.Validate(); err == nil {
		t.Fatal("Validate succeeded, want error for invalid kind")
	}
}

func TestInboundValidateRejectsEventAndMessage(t *testing.T) {
	inbound := Inbound{
		Kind:    InboundEvent,
		Event:   testEvent{},
		Message: &Message{Content: "hello"},
	}
	if err := inbound.Validate(); err == nil {
		t.Fatal("Validate succeeded, want error")
	}
}

func TestInboundValidateRejectsEventAndCommand(t *testing.T) {
	inbound := Inbound{
		Kind:    InboundEvent,
		Event:   testEvent{},
		Command: &command.Invocation{Path: command.Path{"x"}},
	}
	if err := inbound.Validate(); err == nil {
		t.Fatal("Validate succeeded, want error")
	}
}

func TestOutboundValidateMessage(t *testing.T) {
	outbound := Outbound{Kind: OutboundMessage, Message: &Message{Content: "hello"}}
	if err := outbound.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestOutboundValidateRejectsNilMessage(t *testing.T) {
	outbound := Outbound{Kind: OutboundMessage}
	if err := outbound.Validate(); err == nil {
		t.Fatal("Validate succeeded, want error for nil message")
	}
}

func TestOutboundValidateRejectsNilEvent(t *testing.T) {
	outbound := Outbound{Kind: OutboundEvent}
	if err := outbound.Validate(); err == nil {
		t.Fatal("Validate succeeded, want error for nil event")
	}
}

func TestOutboundValidateRejectsInvalidKind(t *testing.T) {
	outbound := Outbound{Kind: OutboundKind("invalid")}
	if err := outbound.Validate(); err == nil {
		t.Fatal("Validate succeeded, want error for invalid kind")
	}
}
