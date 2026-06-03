package trigger

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	coretrigger "github.com/fluxplane/fluxplane-core/core/trigger"
	clientapi "github.com/fluxplane/fluxplane-core/orchestration/client"
	sessionruntime "github.com/fluxplane/fluxplane-core/orchestration/session"
	"github.com/fluxplane/fluxplane-operation"
	corereaction "github.com/fluxplane/fluxplane-reaction"
)

func TestHostFireSubmitsConfiguredTrigger(t *testing.T) {
	client := &fakeClient{}
	host, err := New(Config{
		Client: client,
		NewRunID: func(prefix string) string {
			return prefix + "test"
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	spec := coretrigger.Spec{
		Name:     "nightly",
		Kind:     coretrigger.KindSchedule,
		Schedule: coretrigger.Schedule{Every: "1h"},
		Session:  "main",
		Actions: []corereaction.Action{{
			Kind: corereaction.ActionRunOperation,
			Operation: corereaction.OperationAction{
				Operation: operation.Ref{Name: "echo"},
				Input:     "hello",
			},
		}},
	}
	if err := host.Fire(context.Background(), spec, time.Date(2026, 5, 23, 10, 0, 0, 0, time.UTC)); err != nil {
		t.Fatalf("Fire: %v", err)
	}
	if client.open.Session.Name != "main" {
		t.Fatalf("session = %q, want main", client.open.Session.Name)
	}
	submission := client.session.submission
	if submission.ID != "run_trigger_test" {
		t.Fatalf("run id = %q, want run_trigger_test", submission.ID)
	}
	if submission.Kind != clientapi.SubmissionTrigger || submission.Trigger == nil {
		t.Fatalf("submission = %#v, want trigger", submission)
	}
	if submission.Trigger.Name != "nightly" || submission.Trigger.Source != "schedule" {
		t.Fatalf("trigger = %#v, want nightly schedule", submission.Trigger)
	}
	if len(submission.Trigger.Actions) != 1 || submission.Trigger.Actions[0].Operation.Operation.Name != "echo" {
		t.Fatalf("actions = %#v, want echo operation", submission.Trigger.Actions)
	}
}

func TestHostFireSubmitsStartupTrigger(t *testing.T) {
	client := &fakeClient{}
	host, err := New(Config{
		Client: client,
		NewRunID: func(prefix string) string {
			return prefix + "startup"
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	spec := coretrigger.Spec{
		Name:    "monitoring-started",
		Kind:    coretrigger.KindStartup,
		Session: "main",
		Actions: []corereaction.Action{{
			Kind: corereaction.ActionRunOperation,
			Operation: corereaction.OperationAction{
				Operation: operation.Ref{Name: "notify_send"},
				Input:     map[string]any{"speak": "System monitoring active."},
			},
		}},
	}
	if err := host.Fire(context.Background(), spec, time.Date(2026, 5, 23, 10, 0, 0, 0, time.UTC)); err != nil {
		t.Fatalf("Fire: %v", err)
	}
	submission := client.session.submission
	if submission.Trigger == nil || submission.Trigger.Source != "startup" {
		t.Fatalf("trigger = %#v, want startup trigger", submission.Trigger)
	}
	if len(submission.Trigger.Actions) != 1 || submission.Trigger.Actions[0].Operation.Operation.Name != "notify_send" {
		t.Fatalf("actions = %#v, want notify_send operation", submission.Trigger.Actions)
	}
}

func TestHostRunRejectsBadScheduleBeforeSpawningGoroutines(t *testing.T) {
	client := &countingClient{}
	host, err := New(Config{
		Client: client,
		NewRunID: func(prefix string) string {
			return prefix + "test"
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	host.specs = []coretrigger.Spec{
		{
			Name:    "startup-trigger",
			Kind:    coretrigger.KindStartup,
			Session: "main",
			Actions: []corereaction.Action{{
				Kind: corereaction.ActionRunOperation,
				Operation: corereaction.OperationAction{
					Operation: operation.Ref{Name: "noop"},
				},
			}},
		},
		{
			Name:     "bad-schedule",
			Kind:     coretrigger.KindSchedule,
			Schedule: coretrigger.Schedule{Every: "not-a-duration"},
			Session:  "main",
			Actions: []corereaction.Action{{
				Kind: corereaction.ActionRunOperation,
				Operation: corereaction.OperationAction{
					Operation: operation.Ref{Name: "noop"},
				},
			}},
		},
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	err = host.Run(ctx)
	if err == nil || !strings.Contains(err.Error(), "schedule.every") {
		t.Fatalf("Run error = %v, want schedule.every parse error", err)
	}
	time.Sleep(50 * time.Millisecond)
	if got := client.opens.Load(); got != 0 {
		t.Fatalf("client.opens = %d after rejected Run, want 0 (the startup trigger goroutine should not have spawned)", got)
	}
}

type countingClient struct {
	opens atomic.Int32
}

func (c *countingClient) Open(_ context.Context, req clientapi.OpenRequest) (clientapi.SessionHandle, error) {
	c.opens.Add(1)
	return &fakeSession{info: clientapi.SessionInfo{Session: req.Session}}, nil
}

func (*countingClient) Resume(context.Context, clientapi.ResumeRequest) (clientapi.SessionHandle, error) {
	return nil, nil
}

func (*countingClient) ListSessions(context.Context, clientapi.ListSessionsRequest) ([]clientapi.SessionSummary, error) {
	return nil, nil
}

type fakeClient struct {
	open    clientapi.OpenRequest
	session fakeSession
}

func (c *fakeClient) Open(_ context.Context, req clientapi.OpenRequest) (clientapi.SessionHandle, error) {
	c.open = req
	c.session.info = clientapi.SessionInfo{Session: req.Session}
	return &c.session, nil
}

func (*fakeClient) Resume(context.Context, clientapi.ResumeRequest) (clientapi.SessionHandle, error) {
	return nil, nil
}

func (*fakeClient) ListSessions(context.Context, clientapi.ListSessionsRequest) ([]clientapi.SessionSummary, error) {
	return nil, nil
}

type fakeSession struct {
	info       clientapi.SessionInfo
	submission clientapi.Submission
}

func (s *fakeSession) Info() clientapi.SessionInfo { return s.info }

func (s *fakeSession) Submit(_ context.Context, submission clientapi.Submission) (clientapi.RunHandle, error) {
	s.submission = submission
	return fakeRun{session: s.info, submission: submission}, nil
}

func (*fakeSession) Events(context.Context, clientapi.EventOptions) (<-chan clientapi.Event, func(), error) {
	ch := make(chan clientapi.Event)
	close(ch)
	return ch, func() {}, nil
}

func (*fakeSession) OnEvent(context.Context, func(clientapi.Event)) (func(), error) {
	return func() {}, nil
}

func (*fakeSession) Close(context.Context) error { return nil }

type fakeRun struct {
	session    clientapi.SessionInfo
	submission clientapi.Submission
}

func (r fakeRun) ID() clientapi.RunID              { return r.submission.ID }
func (r fakeRun) Session() clientapi.SessionInfo   { return r.session }
func (r fakeRun) Submission() clientapi.Submission { return r.submission }
func (fakeRun) Events() <-chan clientapi.Event {
	ch := make(chan clientapi.Event)
	close(ch)
	return ch
}
func (fakeRun) Done() <-chan struct{} { ch := make(chan struct{}); close(ch); return ch }
func (fakeRun) Err() error            { return nil }
func (r fakeRun) Wait(context.Context) (clientapi.Result, error) {
	return clientapi.Result{
		RunID:      r.submission.ID,
		Session:    r.session,
		Submission: r.submission,
		Trigger: &sessionruntime.TriggerResult{
			Status: sessionruntime.TriggerStatusOK,
		},
	}, nil
}
