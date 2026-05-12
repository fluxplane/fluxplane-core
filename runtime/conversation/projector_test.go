package conversation

import (
	"context"
	"encoding/json"
	"testing"

	coreconversation "github.com/fluxplane/agentruntime/core/conversation"
	corethread "github.com/fluxplane/agentruntime/core/thread"
	"github.com/fluxplane/agentruntime/runtime/eventstore"
	runtimethread "github.com/fluxplane/agentruntime/runtime/thread"
)

func TestProjectFullReplayReturnsExactProviderItems(t *testing.T) {
	ctx := context.Background()
	store := newThreadStore(t)
	ref := createThread(t, ctx, store)
	provider := coreconversation.ProviderIdentity{Provider: "openai", API: "openai.responses", Model: "gpt-test"}
	items := []coreconversation.Item{
		{Provider: provider, Kind: coreconversation.ItemInput, Role: "user", Native: raw(`{"type":"message","role":"user","content":"hello"}`)},
		{Provider: provider, Kind: coreconversation.ItemOutput, Role: "assistant", ID: "msg_1", Native: raw(`{"type":"message","id":"msg_1","role":"assistant","content":"hi"}`)},
	}
	if err := Append(ctx, store, ref, "turn-1", provider, items); err != nil {
		t.Fatalf("append transcript: %v", err)
	}
	snapshot, err := store.Read(ctx, corethread.ReadParams{ID: ref.ID})
	if err != nil {
		t.Fatalf("read thread: %v", err)
	}

	result, err := Project(ProjectionInput{
		Thread:   snapshot,
		Provider: provider,
		Pending: []coreconversation.Item{
			{Provider: provider, Kind: coreconversation.ItemInput, Role: "user", Native: raw(`{"type":"message","role":"user","content":"again"}`)},
		},
		Mode: coreconversation.ProjectionFullReplay,
	})
	if err != nil {
		t.Fatalf("Project: %v", err)
	}
	if result.Mode != coreconversation.ProjectionFullReplay {
		t.Fatalf("mode = %q, want full replay", result.Mode)
	}
	if len(result.Items) != 3 {
		t.Fatalf("items len = %d, want 3", len(result.Items))
	}
	if string(result.Items[0].Native) != string(items[0].Native) {
		t.Fatalf("first native = %s, want %s", result.Items[0].Native, items[0].Native)
	}
}

func TestProjectNativeContinuationReturnsOnlyPendingItems(t *testing.T) {
	ctx := context.Background()
	store := newThreadStore(t)
	ref := createThread(t, ctx, store)
	provider := coreconversation.ProviderIdentity{Provider: "openai", API: "openai.responses", Model: "gpt-test"}
	handle := coreconversation.ContinuationHandle{
		Provider:   provider,
		Mode:       coreconversation.ContinuationPreviousResponseID,
		Transport:  coreconversation.TransportHTTPSSE,
		ResponseID: "resp_1",
	}
	if err := Append(ctx, store, ref, "turn-1", provider,
		[]coreconversation.Item{{Provider: provider, Kind: coreconversation.ItemOutput, Role: "assistant", ID: "msg_1", Native: raw(`{"id":"msg_1"}`)}},
		handle,
	); err != nil {
		t.Fatalf("append transcript: %v", err)
	}
	snapshot, err := store.Read(ctx, corethread.ReadParams{ID: ref.ID})
	if err != nil {
		t.Fatalf("read thread: %v", err)
	}
	pending := []coreconversation.Item{{Provider: provider, Kind: coreconversation.ItemInput, Role: "user", Native: raw(`{"content":"next"}`)}}

	result, err := Project(ProjectionInput{
		Thread:   snapshot,
		Provider: provider,
		Pending:  pending,
		Mode:     coreconversation.ProjectionNativeContinuation,
	})
	if err != nil {
		t.Fatalf("Project: %v", err)
	}
	if result.Mode != coreconversation.ProjectionNativeContinuation {
		t.Fatalf("mode = %q, want native continuation", result.Mode)
	}
	if result.Continuation == nil || result.Continuation.ResponseID != "resp_1" {
		t.Fatalf("continuation = %#v, want resp_1", result.Continuation)
	}
	if len(result.Items) != 1 || string(result.Items[0].Native) != string(pending[0].Native) {
		t.Fatalf("items = %#v, want pending only", result.Items)
	}
}

func TestProjectNativeContinuationFallsBackToFullReplay(t *testing.T) {
	ctx := context.Background()
	store := newThreadStore(t)
	ref := createThread(t, ctx, store)
	provider := coreconversation.ProviderIdentity{Provider: "anthropic", API: "anthropic.messages", Model: "claude-test"}
	stored := coreconversation.Item{Provider: provider, Kind: coreconversation.ItemInput, Role: "user", Native: raw(`{"content":"hello"}`)}
	pending := coreconversation.Item{Provider: provider, Kind: coreconversation.ItemInput, Role: "user", Native: raw(`{"content":"again"}`)}
	if err := Append(ctx, store, ref, "turn-1", provider, []coreconversation.Item{stored}); err != nil {
		t.Fatalf("append transcript: %v", err)
	}
	snapshot, err := store.Read(ctx, corethread.ReadParams{ID: ref.ID})
	if err != nil {
		t.Fatalf("read thread: %v", err)
	}

	result, err := Project(ProjectionInput{
		Thread:   snapshot,
		Provider: provider,
		Pending:  []coreconversation.Item{pending},
		Mode:     coreconversation.ProjectionNativeContinuation,
	})
	if err != nil {
		t.Fatalf("Project: %v", err)
	}
	if result.Mode != coreconversation.ProjectionFullReplay {
		t.Fatalf("mode = %q, want full replay fallback", result.Mode)
	}
	if result.Continuation != nil {
		t.Fatalf("continuation = %#v, want nil", result.Continuation)
	}
	if len(result.Items) != 2 {
		t.Fatalf("items len = %d, want 2", len(result.Items))
	}
}

func newThreadStore(t *testing.T) corethread.Store {
	t.Helper()
	store, err := runtimethread.NewStore(eventstore.NewMemoryStore())
	if err != nil {
		t.Fatalf("new thread store: %v", err)
	}
	return store
}

func createThread(t *testing.T, ctx context.Context, store corethread.Store) corethread.Ref {
	t.Helper()
	snapshot, err := store.Create(ctx, corethread.CreateParams{ID: "thread-1"})
	if err != nil {
		t.Fatalf("create thread: %v", err)
	}
	return corethread.Ref{ID: snapshot.ID, BranchID: snapshot.BranchID}
}

func raw(s string) json.RawMessage {
	return json.RawMessage(s)
}
