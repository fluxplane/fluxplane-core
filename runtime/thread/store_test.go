package thread

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	corethread "github.com/fluxplane/fluxplane-core/core/thread"
	"github.com/fluxplane/fluxplane-core/runtime/eventstore"
	runtimeprojection "github.com/fluxplane/fluxplane-core/runtime/projection"
	"github.com/fluxplane/fluxplane-event"
)

type messageAdded struct {
	Text string `json:"text,omitempty"`
}

type conflictStore struct{}

func (conflictStore) Append(context.Context, event.StreamID, event.AppendOptions, ...event.Record) ([]event.StoredRecord, error) {
	return nil, event.AppendConflict{Stream: "thread.thread-diagnostics", Expected: 7, Actual: 8}
}

func (conflictStore) AppendBatch(context.Context, ...event.AppendRequest) ([]event.AppendResult, error) {
	return nil, event.AppendConflict{Stream: "thread.index", Expected: 1, Actual: 2}
}

func (conflictStore) Load(context.Context, event.StreamID, event.LoadOptions) ([]event.StoredRecord, error) {
	return nil, nil
}

func TestStoreReplaysBranchEvents(t *testing.T) {
	ctx := context.Background()
	store, err := NewStore(eventstore.NewMemoryStore())
	if err != nil {
		t.Fatalf("NewStore returned error: %v", err)
	}
	if _, err := store.Create(ctx, corethread.CreateParams{ID: "thread-activation"}); err != nil {
		t.Fatalf("Create returned error: %v", err)
	}
	_, err = store.Append(ctx, corethread.Ref{ID: "thread-activation", BranchID: corethread.MainBranch},
		corethread.AppendRecord{
			NodeID: "signals",
			Event:  event.Record{Payload: messageAdded{Text: "first"}},
		},
		corethread.AppendRecord{
			NodeID:       "status",
			ParentNodeID: "signals",
			Event:        event.Record{Payload: messageAdded{Text: "second"}},
		},
	)
	if err != nil {
		t.Fatalf("Append returned error: %v", err)
	}
	read, err := store.Read(ctx, corethread.ReadParams{ID: "thread-activation"})
	if err != nil {
		t.Fatalf("Read returned error: %v", err)
	}
	visible, err := read.EventsForBranch(corethread.MainBranch)
	if err != nil {
		t.Fatalf("EventsForBranch returned error: %v", err)
	}
	if payload, ok := visible[1].Event.Payload.(messageAdded); !ok || payload.Text != "first" {
		t.Fatalf("event[1] payload = %#v, want first message", visible[1].Event.Payload)
	}
	if payload, ok := visible[2].Event.Payload.(messageAdded); !ok || payload.Text != "second" {
		t.Fatalf("event[2] payload = %#v, want second message", visible[2].Event.Payload)
	}
}

func (messageAdded) EventName() event.Name { return "message.added" }

func TestWriteErrorWrapsAppendConflictDiagnostics(t *testing.T) {
	ctx := context.Background()
	store, err := NewStore(conflictStore{})
	if err != nil {
		t.Fatalf("NewStore returned error: %v", err)
	}

	_, err = store.Create(ctx, corethread.CreateParams{ID: "thread-diagnostics"})
	if err == nil {
		t.Fatal("Create returned nil error, want conflict")
	}
	if !errors.Is(err, event.ErrAppendConflict) {
		t.Fatalf("wrapped error does not match ErrAppendConflict: %v", err)
	}
	var writeErr WriteError
	if !errors.As(err, &writeErr) {
		t.Fatalf("error does not expose WriteError: %v", err)
	}
	if writeErr.ThreadID != "thread-diagnostics" || writeErr.Attempt != threadWriteRetries || writeErr.Attempts != threadWriteRetries {
		t.Fatalf("write error = %+v", writeErr)
	}
	var conflict event.AppendConflict
	if !errors.As(err, &conflict) {
		t.Fatalf("error does not expose AppendConflict: %v", err)
	}
	if conflict.Stream != "thread.index" || conflict.Expected != 1 || conflict.Actual != 2 {
		t.Fatalf("conflict = %+v", conflict)
	}
	text := err.Error()
	for _, want := range []string{"thread_id=\"thread-diagnostics\"", "attempt=16/16", "append conflict", "expected sequence 1, actual 2"} {
		if !strings.Contains(text, want) {
			t.Fatalf("error %q missing %q", text, want)
		}
	}
}

func TestStoreCreateAppendForkRead(t *testing.T) {
	ctx := context.Background()
	store, err := NewStore(eventstore.NewMemoryStore())
	if err != nil {
		t.Fatalf("NewStore returned error: %v", err)
	}

	created, err := store.Create(ctx, corethread.CreateParams{
		ID:       "thread-1",
		Metadata: map[string]string{"title": "Test"},
	})
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}
	if created.BranchID != corethread.MainBranch {
		t.Fatalf("branch = %q, want main", created.BranchID)
	}

	_, err = store.Append(ctx, corethread.Ref{ID: "thread-1", BranchID: corethread.MainBranch},
		corethread.AppendRecord{
			NodeID: "node-1",
			Event:  event.Record{Payload: messageAdded{Text: "one"}},
		},
		corethread.AppendRecord{
			NodeID:       "node-2",
			ParentNodeID: "node-1",
			Event:        event.Record{Payload: messageAdded{Text: "two"}},
		},
	)
	if err != nil {
		t.Fatalf("Append returned error: %v", err)
	}

	forked, err := store.Fork(ctx, corethread.ForkParams{
		ID:           "thread-1",
		FromBranchID: corethread.MainBranch,
		ToBranchID:   "alt",
	})
	if err != nil {
		t.Fatalf("Fork returned error: %v", err)
	}
	if forked.BranchID != corethread.MainBranch {
		t.Fatalf("snapshot branch = %q, want main", forked.BranchID)
	}
	if forked.Branches["alt"].ForkSequence != 3 {
		t.Fatalf("fork sequence = %d, want 3", forked.Branches["alt"].ForkSequence)
	}

	_, err = store.Append(ctx, corethread.Ref{ID: "thread-1", BranchID: "alt"},
		corethread.AppendRecord{
			NodeID:       "node-3",
			ParentNodeID: "node-2",
			Event:        event.Record{Payload: messageAdded{Text: "alt"}},
		},
	)
	if err != nil {
		t.Fatalf("Append alt returned error: %v", err)
	}

	read, err := store.Read(ctx, corethread.ReadParams{ID: "thread-1"})
	if err != nil {
		t.Fatalf("Read returned error: %v", err)
	}
	visible, err := read.EventsForBranch("alt")
	if err != nil {
		t.Fatalf("EventsForBranch returned error: %v", err)
	}
	if len(visible) != 5 {
		t.Fatalf("len(visible) = %d, want 5", len(visible))
	}
	if visible[len(visible)-1].NodeID != "node-3" {
		t.Fatalf("last node = %q, want node-3", visible[len(visible)-1].NodeID)
	}
}

func TestStoreListFallbackReplaysThreads(t *testing.T) {
	ctx := context.Background()
	store, err := NewStore(eventstore.NewMemoryStore())
	if err != nil {
		t.Fatalf("NewStore returned error: %v", err)
	}
	if _, err := store.Create(ctx, corethread.CreateParams{ID: "thread-1"}); err != nil {
		t.Fatalf("Create returned error: %v", err)
	}
	if err := store.Archive(ctx, "thread-1"); err != nil {
		t.Fatalf("Archive returned error: %v", err)
	}

	page, err := store.List(ctx, corethread.ListParams{})
	if err != nil {
		t.Fatalf("List returned error: %v", err)
	}
	if len(page.Threads) != 0 {
		t.Fatalf("len(page.Threads) = %d, want 0", len(page.Threads))
	}

	page, err = store.List(ctx, corethread.ListParams{IncludeArchived: true})
	if err != nil {
		t.Fatalf("List archived returned error: %v", err)
	}
	if len(page.Threads) != 1 {
		t.Fatalf("len(page.Threads) = %d, want 1", len(page.Threads))
	}
}

func TestStoreListUsesProjectedThreadIndex(t *testing.T) {
	ctx := context.Background()
	events := eventstore.NewMemoryStore()
	index := NewThreadIndex()
	store, err := NewStore(events, WithThreadIndex(index))
	if err != nil {
		t.Fatalf("NewStore returned error: %v", err)
	}
	createdAt := time.Date(2026, 5, 12, 11, 0, 0, 0, time.UTC)
	if _, err := store.Create(ctx, corethread.CreateParams{
		ID:       "thread-1",
		Metadata: map[string]string{"title": "Projected"},
		Now:      createdAt,
	}); err != nil {
		t.Fatalf("Create returned error: %v", err)
	}

	page, err := store.List(ctx, corethread.ListParams{IncludeArchived: true})
	if err != nil {
		t.Fatalf("List returned error: %v", err)
	}
	if len(page.Threads) != 0 {
		t.Fatalf("len(page.Threads) before projection = %d, want 0", len(page.Threads))
	}

	runner := runtimeprojection.Runner{
		Events:      events,
		Checkpoints: runtimeprojection.NewMemoryCheckpointStore(),
	}
	if _, err := runner.RunOnce(ctx, "thread-index", defaultIndexStream, index); err != nil {
		t.Fatalf("RunOnce returned error: %v", err)
	}

	page, err = store.List(ctx, corethread.ListParams{IncludeArchived: true})
	if err != nil {
		t.Fatalf("List projected returned error: %v", err)
	}
	if len(page.Threads) != 1 {
		t.Fatalf("len(page.Threads) = %d, want 1", len(page.Threads))
	}
	if page.Threads[0].ID != "thread-1" {
		t.Fatalf("thread id = %q, want thread-1", page.Threads[0].ID)
	}
	if page.Threads[0].Metadata["title"] != "Projected" {
		t.Fatalf("title = %q, want Projected", page.Threads[0].Metadata["title"])
	}
	if len(page.Threads[0].Events) != 0 {
		t.Fatalf("len(events) = %d, want 0 for index-backed list", len(page.Threads[0].Events))
	}
}

func TestStoreIndependentThreadStreamsDoNotConflict(t *testing.T) {
	ctx := context.Background()
	inner := eventstore.NewMemoryStore()
	store, err := NewStore(inner)
	if err != nil {
		t.Fatalf("NewStore returned error: %v", err)
	}

	if _, err := store.Create(ctx, corethread.CreateParams{ID: "thread-1"}); err != nil {
		t.Fatalf("Create thread-1 returned error: %v", err)
	}
	if _, err := store.Create(ctx, corethread.CreateParams{ID: "thread-2"}); err != nil {
		t.Fatalf("Create thread-2 returned error: %v", err)
	}
	if _, err := store.Append(ctx, corethread.Ref{ID: "thread-1"}, corethread.AppendRecord{
		Event: event.Record{Payload: messageAdded{Text: "one"}},
	}); err != nil {
		t.Fatalf("Append thread-1 returned error: %v", err)
	}
	if _, err := store.Append(ctx, corethread.Ref{ID: "thread-2"}, corethread.AppendRecord{
		Event: event.Record{Payload: messageAdded{Text: "two"}},
	}); err != nil {
		t.Fatalf("Append thread-2 returned error: %v", err)
	}
}

func TestStoreAppendRetriesSameThreadConflict(t *testing.T) {
	ctx := context.Background()
	inner := eventstore.NewMemoryStore()
	store, err := NewStore(&sameThreadConflictingEventStore{inner: inner})
	if err != nil {
		t.Fatalf("NewStore returned error: %v", err)
	}
	if _, err := store.Create(ctx, corethread.CreateParams{ID: "thread-1"}); err != nil {
		t.Fatalf("Create returned error: %v", err)
	}

	_, err = store.Append(ctx, corethread.Ref{ID: "thread-1"}, corethread.AppendRecord{
		Event: event.Record{Payload: messageAdded{Text: "one"}},
	})
	if err != nil {
		t.Fatalf("Append returned error after retry: %v", err)
	}
	read, err := store.Read(ctx, corethread.ReadParams{ID: "thread-1"})
	if err != nil {
		t.Fatalf("Read returned error: %v", err)
	}
	if len(read.Events) != 3 {
		t.Fatalf("len(read.Events) = %d, want created plus concurrent plus append", len(read.Events))
	}
}

type sameThreadConflictingEventStore struct {
	inner    event.Store
	injected bool
}

func (s *sameThreadConflictingEventStore) Append(ctx context.Context, stream event.StreamID, opts event.AppendOptions, records ...event.Record) ([]event.StoredRecord, error) {
	if !s.injected && stream == "thread:thread-1" && opts.CheckExpectedSequence && opts.ExpectedSequence > 0 {
		s.injected = true
		if _, err := s.inner.Append(ctx, stream, event.AppendOptions{}, event.Record{
			Payload: messageAdded{Text: "concurrent"},
			Scope:   event.Scope{ThreadID: "thread-1"},
		}); err != nil {
			return nil, err
		}
	}
	return s.inner.Append(ctx, stream, opts, records...)
}

func (s *sameThreadConflictingEventStore) AppendBatch(ctx context.Context, requests ...event.AppendRequest) ([]event.AppendResult, error) {
	return s.inner.AppendBatch(ctx, requests...)
}

func (s *sameThreadConflictingEventStore) Load(ctx context.Context, stream event.StreamID, opts event.LoadOptions) ([]event.StoredRecord, error) {
	return s.inner.Load(ctx, stream, opts)
}
