package projections

import (
	"context"
	"testing"

	corethread "github.com/fluxplane/fluxplane-core/core/thread"
	"github.com/fluxplane/fluxplane-core/runtime/eventstore"
	runtimeprojection "github.com/fluxplane/fluxplane-core/runtime/projection"
	runtimethread "github.com/fluxplane/fluxplane-core/runtime/thread"
	"github.com/fluxplane/fluxplane-event"
)

func TestManagerEnsureFreshUpdatesThreadIndex(t *testing.T) {
	ctx := context.Background()
	events := eventstore.NewMemoryStore()
	index := runtimethread.NewThreadIndex()
	store, err := runtimethread.NewStore(events, runtimethread.WithThreadIndex(index))
	if err != nil {
		t.Fatalf("NewStore returned error: %v", err)
	}
	if _, err := store.Create(ctx, corethread.CreateParams{ID: "thread-1"}); err != nil {
		t.Fatalf("Create thread-1 returned error: %v", err)
	}
	if _, err := store.Create(ctx, corethread.CreateParams{ID: "thread-2"}); err != nil {
		t.Fatalf("Create thread-2 returned error: %v", err)
	}

	page, err := store.List(ctx, corethread.ListParams{})
	if err != nil {
		t.Fatalf("List before freshness returned error: %v", err)
	}
	if len(page.Threads) != 0 {
		t.Fatalf("len(page.Threads) before freshness = %d, want 0", len(page.Threads))
	}

	manager := Manager{
		Runner: runtimeprojection.Runner{
			Events:      events,
			Checkpoints: runtimeprojection.NewMemoryCheckpointStore(),
			BatchSize:   1,
		},
	}
	checkpoint, err := manager.EnsureFresh(ctx, "thread-index", "thread.index", index)
	if err != nil {
		t.Fatalf("EnsureFresh returned error: %v", err)
	}
	if checkpoint.Sequence != 2 {
		t.Fatalf("checkpoint sequence = %d, want 2", checkpoint.Sequence)
	}

	page, err = store.List(ctx, corethread.ListParams{})
	if err != nil {
		t.Fatalf("List after freshness returned error: %v", err)
	}
	if len(page.Threads) != 2 {
		t.Fatalf("len(page.Threads) after freshness = %d, want 2", len(page.Threads))
	}
}

func TestManagerEnsureFreshMaxBatches(t *testing.T) {
	ctx := context.Background()
	events := eventstore.NewMemoryStore()
	if _, err := events.Append(ctx, "stream", event.ExpectSequence(0),
		event.Record{Payload: testEvent{}},
		event.Record{Payload: testEvent{}},
	); err != nil {
		t.Fatalf("Append returned error: %v", err)
	}
	manager := Manager{
		Runner: runtimeprojection.Runner{
			Events:      events,
			Checkpoints: runtimeprojection.NewMemoryCheckpointStore(),
			BatchSize:   1,
		},
		MaxBatches: 1,
	}
	_, err := manager.EnsureFresh(ctx, "test", "stream", coreProjectionFunc(func(context.Context, []event.StoredRecord) error {
		return nil
	}))
	if err == nil {
		t.Fatal("EnsureFresh returned nil error, want max batch error")
	}
}

type testEvent struct{}

func (testEvent) EventName() event.Name { return "test.event" }

type coreProjectionFunc func(context.Context, []event.StoredRecord) error

func (f coreProjectionFunc) Project(ctx context.Context, records []event.StoredRecord) error {
	return f(ctx, records)
}
