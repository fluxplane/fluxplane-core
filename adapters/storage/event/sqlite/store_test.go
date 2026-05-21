package sqlite

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/fluxplane/engine/core/event"
	corethread "github.com/fluxplane/engine/core/thread"
	runtimethread "github.com/fluxplane/engine/runtime/thread"
)

type messageAdded struct {
	Text string `json:"text,omitempty"`
}

func (messageAdded) EventName() event.Name { return "message.added" }

func TestStoreAppendLoad(t *testing.T) {
	registry := event.NewRegistry()
	if err := registry.Register(messageAdded{}); err != nil {
		t.Fatalf("register message event: %v", err)
	}
	store, err := Open(filepath.Join(t.TempDir(), "events.db"), registry)
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	ctx := context.Background()
	if _, err := store.Append(ctx, "test", event.ExpectSequence(0), event.Record{
		Payload:    messageAdded{Text: "hello"},
		Attributes: map[string]string{"k": "v"},
	}); err != nil {
		t.Fatalf("Append returned error: %v", err)
	}

	loaded, err := store.Load(ctx, "test", event.LoadOptions{})
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if len(loaded) != 1 {
		t.Fatalf("len(loaded) = %d, want 1", len(loaded))
	}
	payload, ok := loaded[0].Record.Payload.(messageAdded)
	if !ok {
		t.Fatalf("payload type = %T, want messageAdded", loaded[0].Record.Payload)
	}
	if payload.Text != "hello" {
		t.Fatalf("payload text = %q, want hello", payload.Text)
	}
	if loaded[0].Record.Attributes["k"] != "v" {
		t.Fatalf("attribute k = %q, want v", loaded[0].Record.Attributes["k"])
	}
}

func TestStoreAppendConflict(t *testing.T) {
	registry := event.NewRegistry()
	if err := registry.Register(messageAdded{}); err != nil {
		t.Fatalf("register message event: %v", err)
	}
	store, err := Open(filepath.Join(t.TempDir(), "events.db"), registry)
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	ctx := context.Background()
	if _, err := store.Append(ctx, "test", event.ExpectSequence(0), event.Record{Payload: messageAdded{Text: "one"}}); err != nil {
		t.Fatalf("Append returned error: %v", err)
	}
	_, err = store.Append(ctx, "test", event.ExpectSequence(0), event.Record{Payload: messageAdded{Text: "two"}})
	if err == nil {
		t.Fatal("Append returned nil error, want conflict")
	}
	if !errors.Is(err, event.ErrAppendConflict) {
		t.Fatalf("error = %v, want append conflict", err)
	}
}

func TestStoreAppendBatchIsAtomic(t *testing.T) {
	registry := event.NewRegistry()
	if err := registry.Register(messageAdded{}); err != nil {
		t.Fatalf("register message event: %v", err)
	}
	store, err := Open(filepath.Join(t.TempDir(), "events.db"), registry)
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	ctx := context.Background()
	_, err = store.AppendBatch(ctx,
		event.AppendRequest{
			Stream:  "a",
			Options: event.ExpectSequence(0),
			Records: []event.Record{{Payload: messageAdded{Text: "a"}}},
		},
		event.AppendRequest{
			Stream:  "b",
			Options: event.ExpectSequence(1),
			Records: []event.Record{{Payload: messageAdded{Text: "b"}}},
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

func TestIsSQLiteBusyRecognizesBusyAndLockedErrors(t *testing.T) {
	for _, code := range []int{sqliteBusyCode, sqliteBusyCode | 2<<8, sqliteLockedCode, sqliteLockedCode | 1<<8} {
		if !isSQLiteBusy(fmt.Errorf("wrapped: %w", fakeSQLiteError{code: code})) {
			t.Fatalf("code %d not recognized as busy/locked", code)
		}
	}
	if isSQLiteBusy(fakeSQLiteError{code: 19}) {
		t.Fatal("constraint error recognized as busy/locked")
	}
	if isSQLiteBusy(errors.New("database is locked")) {
		t.Fatal("plain string error recognized as busy/locked")
	}
}

func TestStorageErrorClassification(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want StorageErrorClass
	}{
		{name: "append conflict", err: event.AppendConflict{Stream: "s", Expected: 1, Actual: 2}, want: StorageAppendConflict},
		{name: "busy", err: fmt.Errorf("wrapped: %w", fakeSQLiteError{code: sqliteBusyCode | 2<<8}), want: StorageBusyLocked},
		{name: "locked", err: fakeSQLiteError{code: sqliteLockedCode | 1<<8}, want: StorageBusyLocked},
		{name: "constraint", err: fakeSQLiteError{code: sqliteConstraintCode}, want: StorageConstraint},
		{name: "canceled", err: context.Canceled, want: StorageContext},
		{name: "deadline", err: context.DeadlineExceeded, want: StorageContext},
		{name: "unknown", err: errors.New("boom"), want: StorageUnknown},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := storageErrorClass(tt.err); got != tt.want {
				t.Fatalf("storageErrorClass() = %s, want %s", got, tt.want)
			}
		})
	}
}

func TestStorageErrorWrapsAppendConflictWithContext(t *testing.T) {
	err := withAttempt(wrapStorageError("append", "s", event.AppendConflict{Stream: "s", Expected: 4, Actual: 5}), 3, sqliteWriteAttempts)
	if !errors.Is(err, event.ErrAppendConflict) {
		t.Fatalf("wrapped error does not match ErrAppendConflict: %v", err)
	}
	var storage StorageError
	if !errors.As(err, &storage) {
		t.Fatalf("error does not expose StorageError: %v", err)
	}
	if storage.Class != StorageAppendConflict || storage.Stream != "s" || storage.Expected != 4 || storage.Actual != 5 || storage.Attempt != 3 || storage.Attempts != sqliteWriteAttempts {
		t.Fatalf("storage error = %+v", storage)
	}
	text := err.Error()
	for _, want := range []string{"class=append_conflict", "stream=\"s\"", "expected_sequence=4", "actual_sequence=5", "attempt=3/8"} {
		if !strings.Contains(text, want) {
			t.Fatalf("error %q missing %q", text, want)
		}
	}
}

func TestAppendBatchErrorIdentifiesRequest(t *testing.T) {
	err := wrapStorageErrorForRequest("append batch request", "b", 1, event.AppendConflict{Stream: "b", Expected: 0, Actual: 1})
	if !errors.Is(err, event.ErrAppendConflict) {
		t.Fatalf("wrapped error does not match ErrAppendConflict: %v", err)
	}
	var storage StorageError
	if !errors.As(err, &storage) {
		t.Fatalf("error does not expose StorageError: %v", err)
	}
	if !storage.HasRequestIndex || storage.RequestIndex != 1 || storage.Stream != "b" || storage.Expected != 0 || storage.Actual != 1 {
		t.Fatalf("storage error = %+v", storage)
	}
	text := err.Error()
	for _, want := range []string{"request_index=1", "stream=\"b\"", "expected_sequence=0", "actual_sequence=1"} {
		if !strings.Contains(text, want) {
			t.Fatalf("error %q missing %q", text, want)
		}
	}
}

func TestStoreAppendDuplicateEventIDIsNotBusy(t *testing.T) {
	registry := event.NewRegistry()
	if err := registry.Register(messageAdded{}); err != nil {
		t.Fatalf("register message event: %v", err)
	}
	store, err := Open(filepath.Join(t.TempDir(), "events.db"), registry)
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	ctx := context.Background()
	record := event.Record{ID: "duplicate-id", Payload: messageAdded{Text: "one"}}
	if _, err := store.Append(ctx, "a", event.AppendOptions{}, record); err != nil {
		t.Fatalf("Append returned error: %v", err)
	}
	_, err = store.Append(ctx, "b", event.AppendOptions{}, event.Record{ID: "duplicate-id", Payload: messageAdded{Text: "two"}})
	if err == nil {
		t.Fatal("Append returned nil error, want duplicate ID constraint error")
	}
	if isSQLiteBusy(err) {
		t.Fatalf("duplicate ID error classified as busy/locked: %v", err)
	}
	var storage StorageError
	if !errors.As(err, &storage) {
		t.Fatalf("duplicate ID error does not expose StorageError: %v", err)
	}
	if storage.Class != StorageConstraint {
		t.Fatalf("duplicate ID class = %s, want %s", storage.Class, StorageConstraint)
	}
	if storage.SQLiteCode&0xff != sqliteConstraintCode {
		t.Fatalf("duplicate ID sqlite code = %d, want base %d", storage.SQLiteCode, sqliteConstraintCode)
	}
}

func TestRuntimeThreadStoreOnSQLStore(t *testing.T) {
	registry := event.NewRegistry()
	if err := corethread.RegisterEvents(registry); err != nil {
		t.Fatalf("register thread events: %v", err)
	}
	if err := registry.Register(messageAdded{}); err != nil {
		t.Fatalf("register message event: %v", err)
	}
	sqlStore, err := Open(filepath.Join(t.TempDir(), "threads.db"), registry)
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	t.Cleanup(func() { _ = sqlStore.Close() })
	threadStore, err := runtimethread.NewStore(sqlStore)
	if err != nil {
		t.Fatalf("NewStore returned error: %v", err)
	}

	ctx := context.Background()
	if _, err := threadStore.Create(ctx, corethread.CreateParams{ID: "thread-1"}); err != nil {
		t.Fatalf("Create returned error: %v", err)
	}
	if _, err := threadStore.Append(ctx, corethread.Ref{ID: "thread-1"}, corethread.AppendRecord{
		NodeID: "node-1",
		Event:  event.Record{Payload: messageAdded{Text: "from sqlite"}},
	}); err != nil {
		t.Fatalf("Append returned error: %v", err)
	}
	read, err := threadStore.Read(ctx, corethread.ReadParams{ID: "thread-1"})
	if err != nil {
		t.Fatalf("Read returned error: %v", err)
	}
	if len(read.Events) != 2 {
		t.Fatalf("len(read.Events) = %d, want 2", len(read.Events))
	}
	if read.Events[1].NodeID != "node-1" {
		t.Fatalf("node id = %q, want node-1", read.Events[1].NodeID)
	}
}

type fakeSQLiteError struct {
	code int
}

func (e fakeSQLiteError) Error() string { return fmt.Sprintf("sqlite code %d", e.code) }

func (e fakeSQLiteError) Code() int { return e.code }

func TestRuntimeThreadStorePersistsAcrossReopen(t *testing.T) {
	registry := event.NewRegistry()
	if err := corethread.RegisterEvents(registry); err != nil {
		t.Fatalf("register thread events: %v", err)
	}
	if err := registry.Register(messageAdded{}); err != nil {
		t.Fatalf("register message event: %v", err)
	}
	path := filepath.Join(t.TempDir(), "threads.db")
	ctx := context.Background()

	sqlStore, err := Open(path, registry)
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	threadStore, err := runtimethread.NewStore(sqlStore)
	if err != nil {
		t.Fatalf("NewStore returned error: %v", err)
	}
	if _, err := threadStore.Create(ctx, corethread.CreateParams{ID: "thread-1"}); err != nil {
		t.Fatalf("Create returned error: %v", err)
	}
	if _, err := threadStore.Append(ctx, corethread.Ref{ID: "thread-1"}, corethread.AppendRecord{
		Event: event.Record{Payload: messageAdded{Text: "persisted"}},
	}); err != nil {
		t.Fatalf("Append returned error: %v", err)
	}
	if err := sqlStore.Close(); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}

	reopened, err := Open(path, registry)
	if err != nil {
		t.Fatalf("reopen returned error: %v", err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	reopenedThreads, err := runtimethread.NewStore(reopened)
	if err != nil {
		t.Fatalf("NewStore reopened returned error: %v", err)
	}
	read, err := reopenedThreads.Read(ctx, corethread.ReadParams{ID: "thread-1"})
	if err != nil {
		t.Fatalf("Read returned error: %v", err)
	}
	if len(read.Events) != 2 {
		t.Fatalf("len(read.Events) = %d, want 2", len(read.Events))
	}
}

func TestConcurrentAppendDifferentStreams(t *testing.T) {
	registry := event.NewRegistry()
	if err := registry.Register(messageAdded{}); err != nil {
		t.Fatalf("register message event: %v", err)
	}
	store, err := Open(filepath.Join(t.TempDir(), "events.db"), registry)
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	ctx := context.Background()
	const goroutines = 16
	const perStream = 20
	errs := runConcurrent(goroutines, func(worker int) error {
		stream := event.StreamID(fmt.Sprintf("stream-%02d", worker))
		for i := 0; i < perStream; i++ {
			if _, err := store.Append(ctx, stream, event.AppendOptions{}, event.Record{Payload: messageAdded{Text: fmt.Sprintf("%d/%d", worker, i)}}); err != nil {
				return fmt.Errorf("append %s/%d: %w", stream, i, err)
			}
		}
		return nil
	})
	if len(errs) != 0 {
		t.Fatalf("concurrent append returned %d errors, first: %v", len(errs), errs[0])
	}
	for worker := 0; worker < goroutines; worker++ {
		stream := event.StreamID(fmt.Sprintf("stream-%02d", worker))
		loaded, err := store.Load(ctx, stream, event.LoadOptions{})
		if err != nil {
			t.Fatalf("Load(%s) returned error: %v", stream, err)
		}
		assertContiguousSequences(t, stream, loaded, perStream)
	}
}

func TestConcurrentAppendSameStreamWithoutExpectedSequence(t *testing.T) {
	registry := event.NewRegistry()
	if err := registry.Register(messageAdded{}); err != nil {
		t.Fatalf("register message event: %v", err)
	}
	store, err := Open(filepath.Join(t.TempDir(), "events.db"), registry)
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	ctx := context.Background()
	const goroutines = 16
	const perWorker = 20
	errs := runConcurrent(goroutines, func(worker int) error {
		for i := 0; i < perWorker; i++ {
			if _, err := store.Append(ctx, "shared", event.AppendOptions{}, event.Record{Payload: messageAdded{Text: fmt.Sprintf("%d/%d", worker, i)}}); err != nil {
				return fmt.Errorf("append worker %d record %d: %w", worker, i, err)
			}
		}
		return nil
	})
	if len(errs) != 0 {
		t.Fatalf("concurrent append returned %d errors, first: %v", len(errs), errs[0])
	}
	loaded, err := store.Load(ctx, "shared", event.LoadOptions{})
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	assertContiguousSequences(t, "shared", loaded, goroutines*perWorker)
}

func TestConcurrentAppendSameStreamExpectedSequenceConflictsCleanly(t *testing.T) {
	registry := event.NewRegistry()
	if err := registry.Register(messageAdded{}); err != nil {
		t.Fatalf("register message event: %v", err)
	}
	store, err := Open(filepath.Join(t.TempDir(), "events.db"), registry)
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	ctx := context.Background()
	if _, err := store.Append(ctx, "shared", event.ExpectSequence(0), event.Record{Payload: messageAdded{Text: "seed"}}); err != nil {
		t.Fatalf("seed Append returned error: %v", err)
	}

	const goroutines = 16
	errs := runConcurrent(goroutines, func(worker int) error {
		_, err := store.Append(ctx, "shared", event.ExpectSequence(1), event.Record{Payload: messageAdded{Text: fmt.Sprintf("worker-%d", worker)}})
		return err
	})

	conflicts := 0
	for _, err := range errs {
		if !errors.Is(err, event.ErrAppendConflict) {
			t.Fatalf("unexpected non-conflict error: %v", err)
		}
		conflicts++
	}
	if conflicts != goroutines-1 {
		t.Fatalf("conflicts = %d, want %d", conflicts, goroutines-1)
	}
	loaded, err := store.Load(ctx, "shared", event.LoadOptions{})
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	assertContiguousSequences(t, "shared", loaded, 2)
}

func TestConcurrentStoresSameFileDifferentStreams(t *testing.T) {
	registry := event.NewRegistry()
	if err := registry.Register(messageAdded{}); err != nil {
		t.Fatalf("register message event: %v", err)
	}
	path := filepath.Join(t.TempDir(), "events.db")
	storeA, err := Open(path, registry)
	if err != nil {
		t.Fatalf("Open A returned error: %v", err)
	}
	t.Cleanup(func() { _ = storeA.Close() })
	storeB, err := Open(path, registry)
	if err != nil {
		t.Fatalf("Open B returned error: %v", err)
	}
	t.Cleanup(func() { _ = storeB.Close() })

	ctx := context.Background()
	stores := []*Store{storeA, storeB}
	const goroutines = 16
	const perStream = 10
	errs := runConcurrent(goroutines, func(worker int) error {
		store := stores[worker%len(stores)]
		stream := event.StreamID(fmt.Sprintf("store-stream-%02d", worker))
		for i := 0; i < perStream; i++ {
			if _, err := store.Append(ctx, stream, event.AppendOptions{}, event.Record{Payload: messageAdded{Text: fmt.Sprintf("%d/%d", worker, i)}}); err != nil {
				return fmt.Errorf("append %s/%d: %w", stream, i, err)
			}
		}
		return nil
	})
	if len(errs) != 0 {
		t.Fatalf("concurrent append returned %d errors, first: %v", len(errs), errs[0])
	}
	for worker := 0; worker < goroutines; worker++ {
		stream := event.StreamID(fmt.Sprintf("store-stream-%02d", worker))
		loaded, err := storeA.Load(ctx, stream, event.LoadOptions{})
		if err != nil {
			t.Fatalf("Load(%s) returned error: %v", stream, err)
		}
		assertContiguousSequences(t, stream, loaded, perStream)
	}
}

func TestConcurrentStoresSameFileSameStreamWithoutExpectedSequence(t *testing.T) {
	registry := event.NewRegistry()
	if err := registry.Register(messageAdded{}); err != nil {
		t.Fatalf("register message event: %v", err)
	}
	path := filepath.Join(t.TempDir(), "events.db")
	storeA, err := Open(path, registry)
	if err != nil {
		t.Fatalf("Open A returned error: %v", err)
	}
	t.Cleanup(func() { _ = storeA.Close() })
	storeB, err := Open(path, registry)
	if err != nil {
		t.Fatalf("Open B returned error: %v", err)
	}
	t.Cleanup(func() { _ = storeB.Close() })

	ctx := context.Background()
	stores := []*Store{storeA, storeB}
	const goroutines = 16
	const perWorker = 10
	errs := runConcurrent(goroutines, func(worker int) error {
		store := stores[worker%len(stores)]
		for i := 0; i < perWorker; i++ {
			if _, err := store.Append(ctx, "shared", event.AppendOptions{}, event.Record{Payload: messageAdded{Text: fmt.Sprintf("%d/%d", worker, i)}}); err != nil {
				return fmt.Errorf("append worker %d record %d: %w", worker, i, err)
			}
		}
		return nil
	})
	if len(errs) != 0 {
		t.Fatalf("concurrent append returned %d errors, first: %v", len(errs), errs[0])
	}
	loaded, err := storeA.Load(ctx, "shared", event.LoadOptions{})
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	assertContiguousSequences(t, "shared", loaded, goroutines*perWorker)
}

func TestConcurrentStoresSameFileSameStreamWithoutExpectedSequenceStress(t *testing.T) {
	if testing.Short() {
		t.Skip("stress test skipped in short mode")
	}
	for iteration := 0; iteration < 10; iteration++ {
		iteration := iteration
		t.Run(fmt.Sprintf("iteration-%02d", iteration), func(t *testing.T) {
			registry := event.NewRegistry()
			if err := registry.Register(messageAdded{}); err != nil {
				t.Fatalf("register message event: %v", err)
			}
			path := filepath.Join(t.TempDir(), "events.db")
			storeA, err := Open(path, registry)
			if err != nil {
				t.Fatalf("Open A returned error: %v", err)
			}
			t.Cleanup(func() { _ = storeA.Close() })
			storeB, err := Open(path, registry)
			if err != nil {
				t.Fatalf("Open B returned error: %v", err)
			}
			t.Cleanup(func() { _ = storeB.Close() })

			ctx := context.Background()
			stores := []*Store{storeA, storeB}
			const goroutines = 32
			const perWorker = 20
			errs := runConcurrent(goroutines, func(worker int) error {
				store := stores[worker%len(stores)]
				for i := 0; i < perWorker; i++ {
					if _, err := store.Append(ctx, "shared", event.AppendOptions{}, event.Record{Payload: messageAdded{Text: fmt.Sprintf("%d/%d", worker, i)}}); err != nil {
						return fmt.Errorf("append worker %d record %d: %w", worker, i, err)
					}
				}
				return nil
			})
			if len(errs) != 0 {
				t.Fatalf("concurrent append returned %d errors, first: %v", len(errs), errs[0])
			}
			loaded, err := storeA.Load(ctx, "shared", event.LoadOptions{})
			if err != nil {
				t.Fatalf("Load returned error: %v", err)
			}
			assertContiguousSequences(t, "shared", loaded, goroutines*perWorker)
		})
	}
}

func TestRuntimeThreadStoreConcurrentCreateDifferentThreads(t *testing.T) {
	registry := event.NewRegistry()
	if err := corethread.RegisterEvents(registry); err != nil {
		t.Fatalf("register thread events: %v", err)
	}
	sqlStore, err := Open(filepath.Join(t.TempDir(), "threads.db"), registry)
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	t.Cleanup(func() { _ = sqlStore.Close() })
	threadStore, err := runtimethread.NewStore(sqlStore)
	if err != nil {
		t.Fatalf("NewStore returned error: %v", err)
	}

	ctx := context.Background()
	const goroutines = 16
	errs := runConcurrent(goroutines, func(worker int) error {
		_, err := threadStore.Create(ctx, corethread.CreateParams{ID: corethread.ID(fmt.Sprintf("thread-%02d", worker))})
		return err
	})
	if len(errs) != 0 {
		conflicts := 0
		for _, err := range errs {
			if errors.Is(err, event.ErrAppendConflict) {
				conflicts++
				continue
			}
			t.Fatalf("unexpected non-conflict create error: %v", err)
		}
		t.Fatalf("concurrent Create returned %d append conflicts; this reproduces thread.index contention", conflicts)
	}
	page, err := threadStore.List(ctx, corethread.ListParams{})
	if err != nil {
		t.Fatalf("List returned error: %v", err)
	}
	if len(page.Threads) != goroutines {
		t.Fatalf("len(page.Threads) = %d, want %d", len(page.Threads), goroutines)
	}
}

func TestRuntimeThreadStoreConcurrentAppendSameThread(t *testing.T) {
	registry := event.NewRegistry()
	if err := corethread.RegisterEvents(registry); err != nil {
		t.Fatalf("register thread events: %v", err)
	}
	if err := registry.Register(messageAdded{}); err != nil {
		t.Fatalf("register message event: %v", err)
	}
	sqlStore, err := Open(filepath.Join(t.TempDir(), "threads.db"), registry)
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	t.Cleanup(func() { _ = sqlStore.Close() })
	threadStore, err := runtimethread.NewStore(sqlStore)
	if err != nil {
		t.Fatalf("NewStore returned error: %v", err)
	}

	ctx := context.Background()
	if _, err := threadStore.Create(ctx, corethread.CreateParams{ID: "thread-1"}); err != nil {
		t.Fatalf("Create returned error: %v", err)
	}

	const goroutines = 16
	errs := runConcurrent(goroutines, func(worker int) error {
		_, err := threadStore.Append(ctx, corethread.Ref{ID: "thread-1"}, corethread.AppendRecord{
			Event: event.Record{Payload: messageAdded{Text: fmt.Sprintf("worker-%d", worker)}},
		})
		return err
	})
	if len(errs) != 0 {
		conflicts := 0
		for _, err := range errs {
			if errors.Is(err, event.ErrAppendConflict) {
				conflicts++
				continue
			}
			t.Fatalf("unexpected non-conflict append error: %v", err)
		}
		t.Fatalf("concurrent Append returned %d append conflicts; this reproduces same-thread optimistic contention", conflicts)
	}
	read, err := threadStore.Read(ctx, corethread.ReadParams{ID: "thread-1"})
	if err != nil {
		t.Fatalf("Read returned error: %v", err)
	}
	if len(read.Events) != goroutines+1 {
		t.Fatalf("len(read.Events) = %d, want %d", len(read.Events), goroutines+1)
	}
}

func runConcurrent(workers int, fn func(int) error) []error {
	var wg sync.WaitGroup
	start := make(chan struct{})
	errs := make(chan error, workers)
	wg.Add(workers)
	for worker := 0; worker < workers; worker++ {
		worker := worker
		go func() {
			defer wg.Done()
			<-start
			if err := fn(worker); err != nil {
				errs <- err
			}
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	out := make([]error, 0, len(errs))
	for err := range errs {
		out = append(out, err)
	}
	return out
}

func assertContiguousSequences(t *testing.T, stream event.StreamID, records []event.StoredRecord, want int) {
	t.Helper()
	if len(records) != want {
		t.Fatalf("len(%s records) = %d, want %d", stream, len(records), want)
	}
	for i, record := range records {
		wantSequence := event.Sequence(i + 1)
		if record.Sequence != wantSequence {
			t.Fatalf("%s record %d sequence = %d, want %d", stream, i, record.Sequence, wantSequence)
		}
		if record.Stream != stream {
			t.Fatalf("%s record %d stream = %q, want %q", stream, i, record.Stream, stream)
		}
	}
}
