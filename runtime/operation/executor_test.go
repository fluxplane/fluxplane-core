package operationruntime

import (
	"context"
	"testing"

	"github.com/fluxplane/fluxplane-core/core/operation"
	"github.com/fluxplane/fluxplane-event"
)

func TestExecutorEmitsOperationCallID(t *testing.T) {
	var events []event.Event
	ctx := operation.WithCallID(operation.NewContext(context.Background(), event.SinkFunc(func(event event.Event) {
		events = append(events, event)
	})), "call-1")
	op := operation.New(operation.Spec{Ref: operation.Ref{Name: "lookup"}}, func(operation.Context, operation.Value) operation.Result {
		return operation.OK("ok")
	})

	result := NewExecutor().Execute(ctx, op, nil)
	if result.Status != operation.StatusOK {
		t.Fatalf("result = %#v, want ok", result)
	}
	if len(events) != 2 {
		t.Fatalf("events len = %d, want started and completed", len(events))
	}
	started, ok := events[0].(operation.OperationStarted)
	if !ok || started.CallID != "call-1" {
		t.Fatalf("started = %#v, want call id", events[0])
	}
	completed, ok := events[1].(operation.OperationCompleted)
	if !ok || completed.CallID != "call-1" {
		t.Fatalf("completed = %#v, want call id", events[1])
	}
}
