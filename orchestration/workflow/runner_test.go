package workflow

import (
	"context"
	"testing"

	"github.com/fluxplane/engine/core/event"
	"github.com/fluxplane/engine/core/operation"
	coreworkflow "github.com/fluxplane/engine/core/workflow"
)

func TestRunExecutesOperationDAG(t *testing.T) {
	var calls []string
	var events []event.Name
	result := Run(context.Background(), Config{
		Spec: coreworkflow.Spec{
			Name: "feature",
			Steps: []coreworkflow.Step{
				{ID: "one", Operation: operation.Ref{Name: "echo"}},
				{ID: "two", Operation: operation.Ref{Name: "echo"}, DependsOn: []coreworkflow.StepID{"one"}},
			},
		},
		RunID: "run-1",
		Input: "hello",
		Events: event.SinkFunc(func(payload event.Event) {
			events = append(events, payload.EventName())
		}),
		RunOperation: func(_ context.Context, step coreworkflow.Step, input operation.Value, _ operation.CallID) (operation.Result, error) {
			calls = append(calls, string(step.ID))
			return operation.OK(map[string]any{"step": string(step.ID), "input": input}), nil
		},
	})

	if result.Status != coreworkflow.StatusSucceeded {
		t.Fatalf("status = %q, want succeeded: %#v", result.Status, result)
	}
	if len(calls) != 2 || calls[0] != "one" || calls[1] != "two" {
		t.Fatalf("calls = %#v, want one then two", calls)
	}
	wantLast := coreworkflow.EventCompletedName
	if got := events[len(events)-1]; got != wantLast {
		t.Fatalf("last event = %q, want %q", got, wantLast)
	}
}

func TestRunFailsOnStepError(t *testing.T) {
	result := Run(context.Background(), Config{
		Spec: coreworkflow.Spec{
			Name: "feature",
			Steps: []coreworkflow.Step{{
				ID:        "fail",
				Operation: operation.Ref{Name: "echo"},
			}},
		},
		RunID: "run-1",
		RunOperation: func(context.Context, coreworkflow.Step, operation.Value, operation.CallID) (operation.Result, error) {
			return operation.Failed("boom", "failed", nil), nil
		},
	})

	if result.Status != coreworkflow.StatusFailed {
		t.Fatalf("status = %q, want failed", result.Status)
	}
	if result.Error == nil || result.Error.Code != "boom" {
		t.Fatalf("error = %#v, want boom", result.Error)
	}
}

func TestRunContinuesAfterContinuedStepError(t *testing.T) {
	var calls []string
	result := Run(context.Background(), Config{
		Spec: coreworkflow.Spec{
			Name: "feature",
			Steps: []coreworkflow.Step{
				{ID: "soft", Operation: operation.Ref{Name: "echo"}, ErrorPolicy: coreworkflow.StepErrorContinue},
				{ID: "next", Operation: operation.Ref{Name: "echo"}, DependsOn: []coreworkflow.StepID{"soft"}},
			},
		},
		RunID: "run-1",
		RunOperation: func(_ context.Context, step coreworkflow.Step, _ operation.Value, _ operation.CallID) (operation.Result, error) {
			calls = append(calls, string(step.ID))
			if step.ID == "soft" {
				return operation.Failed("soft", "continued", nil), nil
			}
			return operation.OK("done"), nil
		},
	})

	if result.Status != coreworkflow.StatusSucceeded {
		t.Fatalf("status = %q, want succeeded: %#v", result.Status, result)
	}
	if len(calls) != 2 || calls[1] != "next" {
		t.Fatalf("calls = %#v, want continued dependent", calls)
	}
	if result.Steps["soft"].Status != coreworkflow.StatusFailed {
		t.Fatalf("soft step = %#v, want failed step result", result.Steps["soft"])
	}
}
