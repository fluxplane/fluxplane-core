package client

import (
	"reflect"
	"testing"

	"github.com/fluxplane/agentruntime/core/command"
	"github.com/fluxplane/agentruntime/core/event"
	"github.com/fluxplane/agentruntime/core/policy"
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

func TestSubmissionBuilderPayloadSettersClearPreviousPayloads(t *testing.T) {
	submission := NewSubmission().
		WithText("hello").
		WithCommand(command.Invocation{Path: command.Path{"echo"}}).
		WithEvent(testEvent{}).
		WithSignal(Signal{Name: "timer.tick"})
	if submission.Kind != SubmissionSignal {
		t.Fatalf("kind = %q, want signal", submission.Kind)
	}
	if submission.Input != nil || submission.Command != nil || submission.Event != nil {
		t.Fatalf("cleared payloads input=%#v command=%#v event=%#v", submission.Input, submission.Command, submission.Event)
	}
	if submission.Signal == nil || submission.Signal.Name != "timer.tick" {
		t.Fatalf("signal = %#v, want timer.tick", submission.Signal)
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
