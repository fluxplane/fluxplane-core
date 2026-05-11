package sqleventstore

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/fluxplane/agentruntime/core/event"
	corethread "github.com/fluxplane/agentruntime/core/thread"
	runtimethread "github.com/fluxplane/agentruntime/runtime/thread"
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
	defer store.Close()

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
	defer store.Close()

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
	defer store.Close()

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
	defer sqlStore.Close()
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
