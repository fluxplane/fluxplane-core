package client

import (
	"testing"

	"github.com/fluxplane/agentruntime/core/command"
	"github.com/fluxplane/agentruntime/core/event"
)

func TestSubmissionValidateCommand(t *testing.T) {
	submission := Submission{
		Kind:    SubmissionCommand,
		Command: &command.Invocation{Path: command.Path{"echo"}},
	}
	if err := submission.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestSubmissionValidateRejectsUnionExtras(t *testing.T) {
	submission := Submission{
		Kind:    SubmissionCommand,
		Command: &command.Invocation{Path: command.Path{"echo"}},
		Input:   &Input{Text: "hello"},
	}
	if err := submission.Validate(); err == nil {
		t.Fatal("Validate succeeded, want error")
	}
}

func TestSubmissionValidateSignal(t *testing.T) {
	submission := Submission{
		Kind:   SubmissionSignal,
		Signal: &Signal{Name: "timer.tick", Source: "scheduler"},
	}
	if err := submission.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestSubmissionValidateEvent(t *testing.T) {
	submission := Submission{
		Kind:  SubmissionEvent,
		Event: testEvent{},
	}
	if err := submission.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

type testEvent struct{}

func (testEvent) EventName() event.Name { return "test.event" }
