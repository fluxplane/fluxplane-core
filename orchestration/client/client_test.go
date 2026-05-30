package client

import (
	"reflect"
	"testing"

	"github.com/fluxplane/fluxplane-core/core/command"
	"github.com/fluxplane/fluxplane-core/core/operation"
	"github.com/fluxplane/fluxplane-event"
	"github.com/fluxplane/fluxplane-policy"
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

func TestSubmissionValidateAssertion(t *testing.T) {
	submission := Submission{
		Kind:    SubmissionTrigger,
		Trigger: &Trigger{Name: "timer.tick", Source: "scheduler"},
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

func TestSubmissionBuilderWithText(t *testing.T) {
	submission := NewSubmission().WithText("hello")
	if submission.Kind != SubmissionInput {
		t.Fatalf("kind = %q, want input", submission.Kind)
	}
	if submission.Input == nil || submission.Input.Text != "hello" {
		t.Fatalf("input = %#v, want text hello", submission.Input)
	}
	if err := submission.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestSubmissionBuilderWithCommand(t *testing.T) {
	submission := NewSubmission().WithCommand(command.Invocation{Path: command.Path{"echo"}})
	if submission.Kind != SubmissionCommand {
		t.Fatalf("kind = %q, want command", submission.Kind)
	}
	if submission.Command == nil || submission.Command.Path.String() != "/echo" {
		t.Fatalf("command = %#v, want /echo", submission.Command)
	}
	if err := submission.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestSubmissionBuilderWithCommandLine(t *testing.T) {
	submission := NewSubmission().WithCommandLine(`/task "verify"`)
	if submission.Kind != SubmissionCommand {
		t.Fatalf("kind = %q, want command", submission.Kind)
	}
	if submission.Command != nil || submission.CommandLine != `/task "verify"` {
		t.Fatalf("submission = %#v, want raw command line", submission)
	}
	if err := submission.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestSubmissionValidateRejectsCommandAndCommandLine(t *testing.T) {
	submission := NewSubmission().WithCommand(command.Invocation{Path: command.Path{"echo"}})
	submission.CommandLine = "/echo"
	if err := submission.Validate(); err == nil {
		t.Fatal("Validate succeeded, want error")
	}
}

func TestSubmissionBuilderWithOperation(t *testing.T) {
	submission := NewSubmission().WithOperation(operation.Ref{Name: "echo"}, "hello")
	if submission.Kind != SubmissionOperation {
		t.Fatalf("kind = %q, want operation", submission.Kind)
	}
	if submission.Operation == nil || submission.Operation.Operation.Name != "echo" || submission.Operation.Input != "hello" {
		t.Fatalf("operation = %#v, want echo hello", submission.Operation)
	}
	if err := submission.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestSubmissionBuilderPayloadSettersClearPreviousPayloads(t *testing.T) {
	submission := NewSubmission().
		WithText("hello").
		WithCommand(command.Invocation{Path: command.Path{"echo"}}).
		WithOperation(operation.Ref{Name: "echo"}, "hello").
		WithEvent(testEvent{}).
		WithTrigger(Trigger{Name: "timer.tick"})
	if submission.Kind != SubmissionTrigger {
		t.Fatalf("kind = %q, want trigger", submission.Kind)
	}
	if submission.Input != nil || submission.Command != nil || submission.Operation != nil || submission.Event != nil {
		t.Fatalf("cleared payloads input=%#v command=%#v operation=%#v event=%#v", submission.Input, submission.Command, submission.Operation, submission.Event)
	}
	if submission.Trigger == nil || submission.Trigger.Name != "timer.tick" {
		t.Fatalf("trigger = %#v, want timer.tick", submission.Trigger)
	}
	if err := submission.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestSubmissionBuilderPreservesEnvelopeFields(t *testing.T) {
	caller := policy.Caller{Kind: policy.CallerUser}
	trust := policy.Trust{Kind: policy.TrustInvocation, Level: policy.TrustVerified}
	metadata := map[string]any{"source": "test"}
	submission := NewSubmission().
		WithID("run_custom").
		WithCaller(caller).
		WithTrust(trust).
		WithMetadata(metadata).
		WithText("hello")
	if submission.ID != "run_custom" {
		t.Fatalf("id = %q, want run_custom", submission.ID)
	}
	if !reflect.DeepEqual(submission.Caller, caller) {
		t.Fatalf("caller = %#v, want %#v", submission.Caller, caller)
	}
	if !reflect.DeepEqual(submission.Trust, trust) {
		t.Fatalf("trust = %#v, want %#v", submission.Trust, trust)
	}
	if submission.Metadata["source"] != "test" {
		t.Fatalf("metadata = %#v, want source test", submission.Metadata)
	}
	if err := submission.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}
