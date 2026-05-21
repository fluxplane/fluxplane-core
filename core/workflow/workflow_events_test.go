package workflow

import (
	"testing"

	"github.com/fluxplane/engine/core/agent"
	"github.com/fluxplane/engine/core/event"
	"github.com/fluxplane/engine/core/operation"
)

func TestWorkflowEventNames(t *testing.T) {
	checks := []struct {
		name string
		got  event.Name
		want event.Name
	}{
		{"Queued", Queued{}.EventName(), EventQueuedName},
		{"Started", Started{}.EventName(), EventStartedName},
		{"StepStarted", StepStarted{}.EventName(), EventStepStartedName},
		{"StepCompleted", StepCompleted{}.EventName(), EventStepCompletedName},
		{"StepFailed", StepFailed{}.EventName(), EventStepFailedName},
		{"Completed", Completed{}.EventName(), EventCompletedName},
		{"Failed", Failed{}.EventName(), EventFailedName},
		{"Canceled", Canceled{}.EventName(), EventCanceledName},
	}
	for _, tc := range checks {
		if tc.got != tc.want {
			t.Errorf("%s EventName = %q, want %q", tc.name, tc.got, tc.want)
		}
	}
}

func TestRegisterEvents(t *testing.T) {
	registry := event.NewRegistry()
	if err := RegisterEvents(registry); err != nil {
		t.Fatalf("RegisterEvents: %v", err)
	}
	if _, ok, err := registry.TryDecode(EventCompletedName, []byte(`{"run_id":"run","workflow":"wf"}`)); err != nil || !ok {
		t.Fatalf("completed event not registered")
	}
}

func TestSpecValidateAgentStep(t *testing.T) {
	s := Spec{
		Name: "wf",
		Steps: []Step{
			{ID: "s1", Kind: StepAgent, Agent: agent.Ref{Name: "my-agent"}},
		},
	}
	if err := s.Validate(); err != nil {
		t.Fatalf("Validate agent step: %v", err)
	}
}

func TestSpecValidateAgentStepMissingAgent(t *testing.T) {
	s := Spec{
		Name: "wf",
		Steps: []Step{
			{ID: "s1", Kind: StepAgent},
		},
	}
	if err := s.Validate(); err == nil {
		t.Fatal("Validate agent step with empty agent should fail")
	}
}

func TestSpecValidateInvalidKind(t *testing.T) {
	s := Spec{
		Name: "wf",
		Steps: []Step{
			{ID: "s1", Kind: "bogus", Operation: operation.Ref{Name: "my-op"}},
		},
	}
	if err := s.Validate(); err == nil {
		t.Fatal("Validate with invalid kind should fail")
	}
}

func TestSpecValidateInferredAgentKind(t *testing.T) {
	// Kind is inferred as StepAgent when Agent.Name is set and Kind is empty.
	s := Spec{
		Name: "wf",
		Steps: []Step{
			{ID: "s1", Agent: agent.Ref{Name: "my-agent"}},
		},
	}
	if err := s.Validate(); err != nil {
		t.Fatalf("Validate inferred agent step: %v", err)
	}
}

func TestSpecValidateOperationStepMissingOp(t *testing.T) {
	s := Spec{
		Name: "wf",
		Steps: []Step{
			{ID: "s1", Kind: StepOperation},
		},
	}
	if err := s.Validate(); err == nil {
		t.Fatal("Validate operation step with empty op should fail")
	}
}
