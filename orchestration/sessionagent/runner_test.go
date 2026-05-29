package sessionagent

import (
	"context"
	"testing"

	coresession "github.com/fluxplane/fluxplane-core/core/session"
	corethread "github.com/fluxplane/fluxplane-core/core/thread"
	"github.com/fluxplane/fluxplane-event"
)

func TestRunnerRunsSessionAndEmitsLifecycle(t *testing.T) {
	client := &fakeClient{output: "done"}
	runner := New(Config{Client: client, MaxParallel: 1})
	var emitted []event.Event
	result, err := runner.Run(context.Background(), Request{
		ID:      "helper-1",
		Profile: coresession.Ref{Name: "task"},
		Task:    "create task",
		Policy: coresession.DelegationPolicy{
			AllowedProfiles: []coresession.Ref{{Name: "task"}},
		},
		ParentThreadID: "thread-1",
		ParentRunID:    "run-1",
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
	if client.input != "create task" {
		t.Fatalf("input = %q, want task text", client.input)
	}
	if !hasEvent(emitted, EventRequested) || !hasEvent(emitted, EventStarted) || !hasEvent(emitted, EventCompleted) {
		t.Fatalf("events = %#v, want requested/started/completed", eventNames(emitted))
	}
}

func TestRunnerRejectsDisallowedProfile(t *testing.T) {
	runner := New(Config{Client: &fakeClient{}})
	_, err := runner.Run(context.Background(), Request{
		ID:      "helper-1",
		Profile: coresession.Ref{Name: "task-planner"},
		Task:    "create plan",
		Policy: coresession.DelegationPolicy{
			AllowedProfiles: []coresession.Ref{{Name: "task"}},
		},
	})
	if err == nil {
		t.Fatal("run succeeded, want profile rejection")
	}
}

type fakeClient struct {
	input  string
	output string
}

func (c *fakeClient) Open(context.Context, OpenRequest) (Session, error) {
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

func (r fakeRun) ID() string {
	return "child-run"
}

func (r fakeRun) Events() <-chan RunEvent {
	return r.events
}

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
