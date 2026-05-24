package session

import (
	"testing"

	"github.com/fluxplane/fluxplane-core/core/agent"
	"github.com/fluxplane/fluxplane-core/core/command"
	"github.com/fluxplane/fluxplane-core/core/event"
	"github.com/fluxplane/fluxplane-core/core/operation"
)

func TestSessionEventNames(t *testing.T) {
	checks := []struct {
		name string
		got  event.Name
		want event.Name
	}{
		{"InputReceived", InputReceived{}.EventName(), EventInputReceived},
		{"CommandReceived", CommandReceived{}.EventName(), EventCommandReceived},
		{"CommandRejected", CommandRejected{}.EventName(), EventCommandRejected},
		{"AgentStepCompleted", AgentStepCompleted{}.EventName(), EventAgentStepCompleted},
		{"OperationRequested", OperationRequested{}.EventName(), EventOperationRequested},
		{"OperationCompleted", OperationCompleted{}.EventName(), EventOperationCompleted},
		{"OutboundProduced", OutboundProduced{}.EventName(), EventOutboundProduced},
		{"RuntimeEmitted", RuntimeEmitted{}.EventName(), EventRuntimeEmitted},
	}
	for _, tc := range checks {
		if tc.got != tc.want {
			t.Errorf("%s EventName = %q, want %q", tc.name, tc.got, tc.want)
		}
	}
}

func TestRegisterEventsNilRegistry(t *testing.T) {
	if err := RegisterEvents(nil); err == nil {
		t.Fatal("RegisterEvents(nil) should return error")
	}
}

func TestRegisterEventsSucceeds(t *testing.T) {
	r := event.NewRegistry()
	if err := RegisterEvents(r); err != nil {
		t.Fatalf("RegisterEvents: %v", err)
	}
}

func TestSpecValidateEmptyCommandPath(t *testing.T) {
	s := Spec{Name: "s", Commands: []command.Path{{}}}
	if err := s.Validate(); err == nil {
		t.Fatal("Validate with empty command path should fail")
	}
}

func TestSpecValidateEmptyOperationRef(t *testing.T) {
	s := Spec{Name: "s", Operations: []operation.Ref{{}}}
	if err := s.Validate(); err == nil {
		t.Fatal("Validate with empty operation ref should fail")
	}
}

func TestSpecValidateDelegationEmptyAgent(t *testing.T) {
	s := Spec{Name: "s", Delegation: DelegationPolicy{
		AllowedAgents: []agent.Ref{{Name: ""}},
	}}
	if err := s.Validate(); err == nil {
		t.Fatal("Validate with empty delegation agent ref should fail")
	}
}
