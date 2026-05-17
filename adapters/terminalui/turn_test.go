package terminalui

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/fluxplane/agentruntime/core/channel"
	"github.com/fluxplane/agentruntime/core/event"
	coretask "github.com/fluxplane/agentruntime/core/task"
	"github.com/fluxplane/agentruntime/core/usage"
	clientapi "github.com/fluxplane/agentruntime/orchestration/client"
	"github.com/fluxplane/agentruntime/orchestration/session"
)

func TestUsageFromEventParsesTypedPayload(t *testing.T) {
	typed := usage.Recorded{
		Subject: usage.Subject{Kind: usage.SubjectLLM, Provider: "openai", Name: "gpt-test"},
		Measurements: []usage.Measurement{{
			Metric:   usage.MetricLLMInputTokens,
			Quantity: 12,
			Unit:     usage.UnitToken,
		}},
	}
	got, ok := usageFromEvent(clientapi.Event{Runtime: &clientapi.RuntimeEvent{Name: usage.EventRecordedName, Payload: typed}})
	if !ok || got.Subject.Provider != "openai" || len(got.Measurements) != 1 {
		t.Fatalf("usageFromEvent = %#v, %v", got, ok)
	}
	if _, ok := usageFromEvent(clientapi.Event{Runtime: &clientapi.RuntimeEvent{Name: event.Name("other")}}); ok {
		t.Fatalf("usageFromEvent accepted non-usage event")
	}
	if _, ok := usageFromEvent(clientapi.Event{Runtime: &clientapi.RuntimeEvent{Name: usage.EventRecordedName, Payload: map[string]any{}}}); ok {
		t.Fatalf("usageFromEvent accepted untyped usage payload")
	}
}

func TestTrackTaskRuntimeEventTracksActiveTasksAndSeenKeys(t *testing.T) {
	active := map[string]bool{}
	seen := map[string]bool{}
	created := clientapi.Event{
		Kind: clientapi.EventRuntimeEmitted,
		Runtime: &clientapi.RuntimeEvent{
			Name: coretask.EventCreatedName,
			Payload: coretask.Created{
				TaskID: "task_1",
				Task:   coretask.Task{ID: "task_1", Title: "Review", Status: coretask.StatusReady},
			},
		},
	}
	trackTaskRuntimeEvent(created, active, seen)
	if !active["task_1"] {
		t.Fatalf("active = %#v, want task_1 active", active)
	}
	if key := runtimeEventKey(created); key == "" || !seen[key] {
		t.Fatalf("seen missing runtime key %q: %#v", key, seen)
	}
	trackTaskRuntimeEvent(clientapi.Event{
		Kind: clientapi.EventRuntimeEmitted,
		Runtime: &clientapi.RuntimeEvent{
			Name:    coretask.EventExecutionCompletedName,
			Payload: coretask.ExecutionCompleted{TaskID: "task_1", ExecutionID: "exec_1"},
		},
	}, active, seen)
	if active["task_1"] {
		t.Fatalf("active = %#v, want task_1 removed", active)
	}
}

func TestResultErrorReportsFailedInput(t *testing.T) {
	err := ResultError(clientapi.Result{
		Input: &session.InputResult{
			Status: session.InputStatusFailed,
			Error:  &session.CommandError{Code: "model_failed", Message: "boom"},
		},
	})
	if err == nil || !strings.Contains(err.Error(), "model_failed: boom") {
		t.Fatalf("err = %v, want model_failed", err)
	}
}

func TestRenderOutboundRendersMarkdown(t *testing.T) {
	var out bytes.Buffer
	renderOutbound(&out, clientapi.Result{
		Outbound: &channel.Outbound{
			Message: &channel.Message{Content: "**Hi** `there`"},
		},
	})

	got := out.String()
	if !strings.Contains(got, "Hi") || !strings.Contains(got, "there") {
		t.Fatalf("out = %q, want rendered final outbound", got)
	}
	if strings.Contains(got, "**Hi**") || strings.Contains(got, "`there`") {
		t.Fatalf("out = %q, want markdown rendered without source markers", got)
	}
}

func TestFollowBackgroundTasksReturnsAfterIdleWindow(t *testing.T) {
	previous := backgroundTaskFollowIdle
	backgroundTaskFollowIdle = 10 * time.Millisecond
	defer func() { backgroundTaskFollowIdle = previous }()

	events := make(chan clientapi.Event)
	defer close(events)
	var out, err bytes.Buffer
	result := followBackgroundTasks(context.Background(), staticEventSession{events: events}, turnRenderResult{
		ActiveTasks: map[string]bool{"task_1": true},
		SeenRuntime: map[string]bool{},
	}, nil, TurnOptions{Out: &out, Err: &err})

	if !result.ActiveTasks["task_1"] {
		t.Fatalf("active tasks = %#v, want task_1 still active", result.ActiveTasks)
	}
	got := err.String()
	for _, want := range []string{"task scheduled: task_1 running in background", "task still running in background: task_1"} {
		if !strings.Contains(got, want) {
			t.Fatalf("background output = %q, missing %q", got, want)
		}
	}
}

func TestFollowBackgroundTasksCanWaitForCompletion(t *testing.T) {
	previous := backgroundTaskFollowIdle
	backgroundTaskFollowIdle = time.Millisecond
	defer func() { backgroundTaskFollowIdle = previous }()

	events := make(chan clientapi.Event)
	defer close(events)
	go func() {
		time.Sleep(10 * time.Millisecond)
		events <- clientapi.Event{
			Kind: clientapi.EventRuntimeEmitted,
			Runtime: &clientapi.RuntimeEvent{
				Name:    coretask.EventExecutionCompletedName,
				Payload: coretask.ExecutionCompleted{TaskID: "task_1", ExecutionID: "exec_1"},
			},
		}
	}()

	var out, err bytes.Buffer
	result := followBackgroundTasks(context.Background(), staticEventSession{events: events}, turnRenderResult{
		ActiveTasks: map[string]bool{"task_1": true},
		SeenRuntime: map[string]bool{},
	}, nil, TurnOptions{Out: &out, Err: &err, WaitForBackgroundTasks: true})

	if result.ActiveTasks["task_1"] {
		t.Fatalf("active tasks = %#v, want task_1 completed", result.ActiveTasks)
	}
	got := err.String()
	if !strings.Contains(got, "waiting for completion") {
		t.Fatalf("background output = %q, want wait message", got)
	}
	if strings.Contains(got, "still running in background") {
		t.Fatalf("background output = %q, want no idle timeout", got)
	}
}

type staticEventSession struct {
	events <-chan clientapi.Event
}

func (s staticEventSession) Info() clientapi.SessionInfo { return clientapi.SessionInfo{} }

func (s staticEventSession) Submit(context.Context, clientapi.Submission) (clientapi.RunHandle, error) {
	return nil, nil
}

func (s staticEventSession) Events(context.Context, clientapi.EventOptions) (<-chan clientapi.Event, func(), error) {
	return s.events, func() {}, nil
}

func (s staticEventSession) OnEvent(context.Context, func(clientapi.Event)) (func(), error) {
	return func() {}, nil
}

func (s staticEventSession) Close(context.Context) error { return nil }
