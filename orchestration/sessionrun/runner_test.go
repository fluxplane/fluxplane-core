package sessionrun

import (
	"context"
	"testing"

	coresession "github.com/fluxplane/fluxplane-core/core/session"
	corethread "github.com/fluxplane/fluxplane-core/core/thread"
	"github.com/fluxplane/fluxplane-event"
)

func TestRunnerRunsSessionWithFreshConversationAndLifecycle(t *testing.T) {
	client := &fakeClient{output: "done"}
	runner := New(Config{Client: client, MaxParallel: 1})
	var emitted []event.Event
	result, err := runner.Run(context.Background(), Request{
		ID:      "loop-1",
		Session: coresession.Ref{Name: "assistant"},
		Input:   "repeat this",
		Events: event.SinkFunc(func(payload event.Event) {
			emitted = append(emitted, payload)
		}),
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if result.Output != "done" {
		t.Fatalf("output = %q, want done", result.Output)
	}
	if result.ChildThreadID != "child-thread" || result.ChildRunID != "child-run" {
		t.Fatalf("child ids = %q/%q, want child-thread/child-run", result.ChildThreadID, result.ChildRunID)
	}
	if client.open.Conversation.ID != "loop-1" {
		t.Fatalf("conversation = %q, want loop-1", client.open.Conversation.ID)
	}
	if client.input != "repeat this" {
		t.Fatalf("input = %q, want prompt", client.input)
	}
	if !hasEvent(emitted, EventRequested) || !hasEvent(emitted, EventStarted) || !hasEvent(emitted, EventCompleted) {
		t.Fatalf("events = %#v, want requested/started/completed", eventNames(emitted))
	}
}

func TestRunnerRejectsDisallowedProfileWhenPolicyEnforced(t *testing.T) {
	runner := New(Config{Client: &fakeClient{}})
	_, err := runner.Run(context.Background(), Request{
		ID:            "loop-1",
		Session:       coresession.Ref{Name: "explorer"},
		Input:         "repeat this",
		EnforcePolicy: true,
		Policy: coresession.DelegationPolicy{
			AllowedProfiles: []coresession.Ref{{Name: "worker"}},
		},
	})
	if err == nil {
		t.Fatal("run succeeded, want profile rejection")
	}
}

type fakeClient struct {
	open   OpenRequest
	input  string
	output string
}

func (c *fakeClient) Open(_ context.Context, req OpenRequest) (Session, error) {
	c.open = req
	return fakeSession{client: c}, nil
}

type fakeSession struct {
	client *fakeClient
}

func (s fakeSession) Info() SessionInfo {
	return SessionInfo{Thread: corethread.Ref{ID: "child-thread"}}
}

func (s fakeSession) SendInput(_ context.Context, input Input) (Run, error) {
	s.client.input = input.Text
	events := make(chan RunEvent)
	close(events)
	return fakeRun{output: s.client.output, events: events}, nil
}

type fakeRun struct {
	output string
	events <-chan RunEvent
}

func (r fakeRun) ID() string { return "child-run" }

func (r fakeRun) Events() <-chan RunEvent { return r.events }

func (r fakeRun) Wait(context.Context) (RunResult, error) {
	return RunResult{Text: r.output}, nil
}

func hasEvent(events []event.Event, name event.Name) bool {
	for _, evt := range events {
		if evt.EventName() == name {
			return true
		}
	}
	return false
}

func eventNames(events []event.Event) []event.Name {
	names := make([]event.Name, 0, len(events))
	for _, evt := range events {
		names = append(names, evt.EventName())
	}
	return names
}
