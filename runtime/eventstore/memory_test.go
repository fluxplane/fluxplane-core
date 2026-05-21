package eventstore

import (
	"context"
	"errors"
	"testing"

	"github.com/fluxplane/engine/core/event"
	"github.com/fluxplane/engine/core/policy"
)

type testEvent struct{}

func (testEvent) EventName() event.Name { return "test.event" }

func TestMemoryStoreAppendLoad(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	stored, err := store.Append(ctx, "test", event.ExpectSequence(0), event.Record{Payload: testEvent{}})
	if err != nil {
		t.Fatalf("Append returned error: %v", err)
	}
	if stored[0].Sequence != 1 {
		t.Fatalf("sequence = %d, want 1", stored[0].Sequence)
	}
	if stored[0].Record.Name != "test.event" {
		t.Fatalf("name = %q, want test.event", stored[0].Record.Name)
	}
	if stored[0].Record.Sensitivity != policy.SensitivityRestricted {
		t.Fatalf("sensitivity = %q, want restricted", stored[0].Record.Sensitivity)
	}

	loaded, err := store.Load(ctx, "test", event.LoadOptions{})
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if len(loaded) != 1 {
		t.Fatalf("len(loaded) = %d, want 1", len(loaded))
	}
}

func TestMemoryStoreAppendConflict(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	if _, err := store.Append(ctx, "test", event.ExpectSequence(0), event.Record{Payload: testEvent{}}); err != nil {
		t.Fatalf("Append returned error: %v", err)
	}
	_, err := store.Append(ctx, "test", event.ExpectSequence(0), event.Record{Payload: testEvent{}})
	if err == nil {
		t.Fatal("Append returned nil error, want conflict")
	}
	if !errors.Is(err, event.ErrAppendConflict) {
		t.Fatalf("error = %v, want append conflict", err)
	}
}

func TestMemoryStoreAppendBatchIsAtomic(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	_, err := store.AppendBatch(ctx,
		event.AppendRequest{
			Stream:  "a",
			Options: event.ExpectSequence(0),
			Records: []event.Record{{Payload: testEvent{}}},
		},
		event.AppendRequest{
			Stream:  "b",
			Options: event.ExpectSequence(1),
			Records: []event.Record{{Payload: testEvent{}}},
		},
	)
	if err == nil {
		t.Fatal("AppendBatch returned nil error, want conflict")
	}
	if !errors.Is(err, event.ErrAppendConflict) {
		t.Fatalf("error = %v, want append conflict", err)
	}
	loaded, err := store.Load(ctx, "a", event.LoadOptions{})
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if len(loaded) != 0 {
		t.Fatalf("len(loaded) = %d, want 0", len(loaded))
	}
}
