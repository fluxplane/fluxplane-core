package workflow

import (
	"context"
	"testing"

	"github.com/fluxplane/fluxplane-core/core/event"
	"github.com/fluxplane/fluxplane-core/core/operation"
	coreworkflow "github.com/fluxplane/fluxplane-core/core/workflow"
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

func TestRunMapsDependencyOutputIntoStepInput(t *testing.T) {
	var nextInput operation.Value
	result := Run(context.Background(), Config{
		Spec: coreworkflow.Spec{
			Name: "feature",
			Steps: []coreworkflow.Step{
				{ID: "collect", Operation: operation.Ref{Name: "echo"}},
				{
					ID:        "summarize",
					Operation: operation.Ref{Name: "echo"},
					DependsOn: []coreworkflow.StepID{"collect"},
					InputMap: map[string]string{
						"metrics": "collect",
						"request": "$input",
					},
				},
			},
		},
		RunID: "run-1",
		Input: map[string]any{"trigger": "scheduled"},
		RunOperation: func(_ context.Context, step coreworkflow.Step, input operation.Value, _ operation.CallID) (operation.Result, error) {
			if step.ID == "summarize" {
				nextInput = input
			}
			return operation.OK(map[string]any{"step": string(step.ID), "input": input}), nil
		},
	})

	if result.Status != coreworkflow.StatusSucceeded {
		t.Fatalf("status = %q, want succeeded: %#v", result.Status, result)
	}
	mapped, ok := nextInput.(map[string]operation.Value)
	if !ok {
		t.Fatalf("next input = %#v, want mapped input", nextInput)
	}
	if mapped["metrics"] == nil || mapped["request"] == nil {
		t.Fatalf("mapped input = %#v, want metrics and request", mapped)
	}
}

func TestRunSkipsStepWhenConditionDoesNotMatch(t *testing.T) {
	var calls []string
	result := Run(context.Background(), Config{
		Spec: coreworkflow.Spec{
			Name: "feature",
			Steps: []coreworkflow.Step{
				{ID: "classify", Operation: operation.Ref{Name: "echo"}},
				{
					ID:        "notify",
					Operation: operation.Ref{Name: "echo"},
					DependsOn: []coreworkflow.StepID{"classify"},
					When: coreworkflow.Condition{
						StepID: "classify",
						Equals: "NO_ACTION",
						Not:    true,
					},
				},
			},
		},
		RunID: "run-1",
		RunOperation: func(_ context.Context, step coreworkflow.Step, _ operation.Value, _ operation.CallID) (operation.Result, error) {
			calls = append(calls, string(step.ID))
			return operation.OK("NO_ACTION\n"), nil
		},
	})

	if result.Status != coreworkflow.StatusSucceeded {
		t.Fatalf("status = %q, want succeeded: %#v", result.Status, result)
	}
	if len(calls) != 1 || calls[0] != "classify" {
		t.Fatalf("calls = %#v, want only classify", calls)
	}
	if result.Steps["notify"].Output != nil {
		t.Fatalf("notify output = %#v, want nil skipped output", result.Steps["notify"].Output)
	}
}

func TestRunExecutesStepWhenNotEqualsConditionMatches(t *testing.T) {
	var calls []string
	result := Run(context.Background(), Config{
		Spec: coreworkflow.Spec{
			Name: "feature",
			Steps: []coreworkflow.Step{
				{ID: "classify", Operation: operation.Ref{Name: "echo"}},
				{
					ID:        "notify",
					Operation: operation.Ref{Name: "echo"},
					DependsOn: []coreworkflow.StepID{"classify"},
					When: coreworkflow.Condition{
						StepID: "classify",
						Equals: "NO_ACTION",
						Not:    true,
					},
				},
			},
		},
		RunID: "run-1",
		RunOperation: func(_ context.Context, step coreworkflow.Step, _ operation.Value, _ operation.CallID) (operation.Result, error) {
			calls = append(calls, string(step.ID))
			if step.ID == "classify" {
				return operation.OK("Disk usage is at 91 percent. Free space or expand the volume."), nil
			}
			return operation.OK("notified"), nil
		},
	})

	if result.Status != coreworkflow.StatusSucceeded {
		t.Fatalf("status = %q, want succeeded: %#v", result.Status, result)
	}
	if len(calls) != 2 || calls[1] != "notify" {
		t.Fatalf("calls = %#v, want notify to run", calls)
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
