package thread

import (
	"context"
	"testing"
	"time"

	"github.com/fluxplane/engine/core/event"
	corethread "github.com/fluxplane/engine/core/thread"
	"github.com/fluxplane/engine/runtime/eventstore"
	runtimeprojection "github.com/fluxplane/engine/runtime/projection"
)

func TestThreadIndexProjectsIndexStream(t *testing.T) {
	ctx := context.Background()
	events := eventstore.NewMemoryStore()
	store, err := NewStore(events)
	if err != nil {
		t.Fatalf("NewStore returned error: %v", err)
	}
	createdAt := time.Date(2026, 5, 12, 10, 0, 0, 0, time.UTC)
	if _, err := store.Create(ctx, corethread.CreateParams{
		ID:       "thread-1",
		Metadata: map[string]string{"title": "Test"},
		Now:      createdAt,
	}); err != nil {
		t.Fatalf("Create returned error: %v", err)
	}

	index := NewThreadIndex()
	runner := runtimeprojection.Runner{
		Events:      events,
		Checkpoints: runtimeprojection.NewMemoryCheckpointStore(),
	}
	if _, err := runner.RunOnce(ctx, "thread-index", defaultIndexStream, index); err != nil {
		t.Fatalf("RunOnce returned error: %v", err)
	}

	entries := index.List(corethread.ListParams{})
	if len(entries) != 1 {
		t.Fatalf("len(entries) = %d, want 1", len(entries))
	}
	if entries[0].ID != "thread-1" {
		t.Fatalf("entry id = %q, want thread-1", entries[0].ID)
	}
	if entries[0].Metadata["title"] != "Test" {
		t.Fatalf("title = %q, want Test", entries[0].Metadata["title"])
	}
	if !entries[0].CreatedAt.Equal(createdAt) {
		t.Fatalf("created_at = %v, want %v", entries[0].CreatedAt, createdAt)
	}

	if err := store.Archive(ctx, "thread-1"); err != nil {
		t.Fatalf("Archive returned error: %v", err)
	}
	if _, err := runner.RunOnce(ctx, "thread-index", defaultIndexStream, index); err != nil {
		t.Fatalf("RunOnce archive returned error: %v", err)
	}
	if got := len(index.List(corethread.ListParams{})); got != 0 {
		t.Fatalf("len(active entries) = %d, want 0", got)
	}
	if got := len(index.List(corethread.ListParams{IncludeArchived: true})); got != 1 {
		t.Fatalf("len(all entries) = %d, want 1", got)
	}
}

func TestThreadIndexSkipsArchived(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 5, 12, 10, 0, 0, 0, time.UTC)
	index := NewThreadIndex()
	records := []event.StoredRecord{
		{
			Stream:   defaultIndexStream,
			Sequence: 1,
			Record: event.Record{
				Time:  now,
				Scope: event.Scope{ThreadID: "thread-1"},
				Payload: corethread.ThreadCreated{
					ThreadID:  "thread-1",
					BranchID:  corethread.MainBranch,
					CreatedAt: now,
				},
			},
		},
		{
			Stream:   defaultIndexStream,
			Sequence: 2,
			Record: event.Record{
				Time:    now.Add(time.Second),
				Scope:   event.Scope{ThreadID: "thread-1"},
				Payload: corethread.ThreadArchived{ThreadID: "thread-1", At: now.Add(time.Second)},
			},
		},
	}
	if err := index.Project(ctx, records); err != nil {
		t.Fatalf("Project returned error: %v", err)
	}
	if got := len(index.List(corethread.ListParams{})); got != 0 {
		t.Fatalf("len(active entries) = %d, want 0", got)
	}
	if got := len(index.List(corethread.ListParams{IncludeArchived: true})); got != 1 {
		t.Fatalf("len(all entries) = %d, want 1", got)
	}
}
