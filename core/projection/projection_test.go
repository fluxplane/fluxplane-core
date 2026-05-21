package projection

import (
	"context"
	"errors"
	"testing"

	"github.com/fluxplane/engine/core/event"
)

func TestProjectorFuncNil(t *testing.T) {
	var f ProjectorFunc
	if err := f.Project(context.Background(), nil); err != nil {
		t.Fatalf("nil ProjectorFunc.Project returned error: %v", err)
	}
}

func TestProjectorFuncCallsFunction(t *testing.T) {
	called := false
	f := ProjectorFunc(func(_ context.Context, records []event.StoredRecord) error {
		called = true
		return nil
	})
	if err := f.Project(context.Background(), nil); err != nil {
		t.Fatalf("Project returned error: %v", err)
	}
	if !called {
		t.Fatal("ProjectorFunc was not called")
	}
}

func TestProjectorFuncPropagatesError(t *testing.T) {
	sentinel := errors.New("projection error")
	f := ProjectorFunc(func(_ context.Context, _ []event.StoredRecord) error {
		return sentinel
	})
	if err := f.Project(context.Background(), nil); !errors.Is(err, sentinel) {
		t.Fatalf("Project error = %v, want sentinel", err)
	}
}

func TestCheckpointFields(t *testing.T) {
	cp := Checkpoint{Stream: "stream-1", Sequence: 42}
	if cp.Stream != "stream-1" {
		t.Fatalf("Stream = %q, want stream-1", cp.Stream)
	}
	if cp.Sequence != 42 {
		t.Fatalf("Sequence = %d, want 42", cp.Sequence)
	}
}
