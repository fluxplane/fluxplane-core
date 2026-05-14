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
	if len(result.NewItems) != 1 {
		t.Fatalf("new items len = %d, want pending only", len(result.NewItems))
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
	if len(result.NewItems) != 1 || string(result.NewItems[0].Native) != string(pending[0].Native) {
		t.Fatalf("new items = %#v, want pending only", result.NewItems)
	}
}

func TestProjectNativeContinuationRepairsMissingToolResult(t *testing.T) {
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
		[]coreconversation.Item{{
			Provider: provider,
			Kind:     coreconversation.ItemOutput,
			CallID:   "call_1",
			Name:     "delegate",
			Native:   raw(`{"type":"function_call","call_id":"call_1","name":"delegate","arguments":"{}"}`),
		}},
		handle,
	); err != nil {
		t.Fatalf("append transcript: %v", err)
	}
	snapshot, err := store.Read(ctx, corethread.ReadParams{ID: ref.ID})
	if err != nil {
		t.Fatalf("read thread: %v", err)
	}
	pending := []coreconversation.Item{{Provider: provider, Kind: coreconversation.ItemInput, Role: "user", Content: "what happened"}}

	result, err := Project(ProjectionInput{
		Thread:   snapshot,
		Provider: provider,
		Pending:  pending,
		Mode:     coreconversation.ProjectionNativeContinuation,
	})
	if err != nil {
		t.Fatalf("Project: %v", err)
	}
	if len(result.Items) != 2 {
		t.Fatalf("items len = %d, want repair + pending", len(result.Items))
	}
	repair := result.Items[0]
	if repair.Kind != coreconversation.ItemToolResult || repair.CallID != "call_1" || repair.Metadata["is_error"] != "true" {
		t.Fatalf("repair item = %#v, want error tool result for call_1", repair)
	}
	if len(result.NewItems) != 2 || result.NewItems[0].CallID != "call_1" {
		t.Fatalf("new items = %#v, want repair recorded before pending", result.NewItems)
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
	if len(result.NewItems) != 1 || string(result.NewItems[0].Native) != string(pending.Native) {
		t.Fatalf("new items = %#v, want pending only", result.NewItems)
	}
}

func TestProjectUsesCompatibleCompactionCheckpoint(t *testing.T) {
	ctx := context.Background()
	store := newThreadStore(t)
	ref := createThread(t, ctx, store)
	provider := coreconversation.ProviderIdentity{Provider: "openai", API: "openai.responses", Model: "gpt-test"}
	old := coreconversation.Item{Provider: provider, Kind: coreconversation.ItemInput, Role: "user", Content: "old"}
	checkpoint := []coreconversation.Item{{Provider: provider, Kind: coreconversation.ItemInput, Role: "developer", Content: "compacted"}}
	later := coreconversation.Item{Provider: provider, Kind: coreconversation.ItemOutput, Role: "assistant", Content: "later"}
	if err := Append(ctx, store, ref, "turn-1", provider, []coreconversation.Item{old}); err != nil {
		t.Fatalf("append old transcript: %v", err)
	}
	if err := AppendCompaction(ctx, store, ref, "compact-1", provider, checkpoint, coreconversation.CompactionStats{Compacted: true}); err != nil {
		t.Fatalf("append compaction: %v", err)
	}
	if err := Append(ctx, store, ref, "turn-2", provider, []coreconversation.Item{later}); err != nil {
		t.Fatalf("append later transcript: %v", err)
	}
	snapshot, err := store.Read(ctx, corethread.ReadParams{ID: ref.ID})
	if err != nil {
		t.Fatalf("read thread: %v", err)
	}

	result, err := Project(ProjectionInput{Thread: snapshot, Provider: provider, Mode: coreconversation.ProjectionFullReplay})
	if err != nil {
		t.Fatalf("Project: %v", err)
	}
	if len(result.Items) != 2 {
		t.Fatalf("items = %#v, want checkpoint + later", result.Items)
	}
	if result.Items[0].Content != "compacted" || result.Items[1].Content != "later" {
		t.Fatalf("items = %#v, want old replay replaced by checkpoint", result.Items)
	}
}

func TestProjectCompactionCheckpointClearsPriorContinuation(t *testing.T) {
	ctx := context.Background()
	store := newThreadStore(t)
	ref := createThread(t, ctx, store)
	provider := coreconversation.ProviderIdentity{Provider: "openai", API: "openai.responses", Model: "gpt-test"}
	handle := coreconversation.ContinuationHandle{
		Provider:   provider,
		Mode:       coreconversation.ContinuationPreviousResponseID,
		Transport:  coreconversation.TransportHTTPSSE,
		ResponseID: "resp_old",
	}
	if err := Append(ctx, store, ref, "turn-1", provider,
		[]coreconversation.Item{{Provider: provider, Kind: coreconversation.ItemOutput, Role: "assistant", Content: "old"}},
		handle,
	); err != nil {
		t.Fatalf("append transcript: %v", err)
	}
	if err := AppendCompaction(ctx, store, ref, "compact-1", provider,
		[]coreconversation.Item{{Provider: provider, Kind: coreconversation.ItemInput, Role: "developer", Content: "compacted"}},
		coreconversation.CompactionStats{Compacted: true},
	); err != nil {
		t.Fatalf("append compaction: %v", err)
	}
	snapshot, err := store.Read(ctx, corethread.ReadParams{ID: ref.ID})
	if err != nil {
		t.Fatalf("read thread: %v", err)
	}

	result, err := Project(ProjectionInput{
		Thread:   snapshot,
		Provider: provider,
		Pending:  []coreconversation.Item{{Provider: provider, Kind: coreconversation.ItemInput, Role: "user", Content: "next"}},
		Mode:     coreconversation.ProjectionNativeContinuation,
	})
	if err != nil {
		t.Fatalf("Project: %v", err)
	}
	if result.Mode != coreconversation.ProjectionFullReplay {
		t.Fatalf("mode = %q, want full replay after checkpoint clears continuation", result.Mode)
	}
	if result.Continuation != nil {
		t.Fatalf("continuation = %#v, want nil", result.Continuation)
	}
	if len(result.Items) != 2 || result.Items[0].Content != "compacted" || result.Items[1].Content != "next" {
		t.Fatalf("items = %#v, want checkpoint + pending", result.Items)
	}
}

func TestProjectIgnoresIncompatibleCompactionCheckpoint(t *testing.T) {
	ctx := context.Background()
	store := newThreadStore(t)
	ref := createThread(t, ctx, store)
	provider := coreconversation.ProviderIdentity{Provider: "openai", API: "openai.responses", Model: "gpt-test"}
	otherProvider := coreconversation.ProviderIdentity{Provider: "anthropic", API: "anthropic.messages", Model: "claude-test"}
	old := coreconversation.Item{Provider: provider, Kind: coreconversation.ItemInput, Role: "user", Content: "old"}
	if err := Append(ctx, store, ref, "turn-1", provider, []coreconversation.Item{old}); err != nil {
		t.Fatalf("append transcript: %v", err)
	}
	if err := AppendCompaction(ctx, store, ref, "compact-1", otherProvider,
		[]coreconversation.Item{{Provider: otherProvider, Kind: coreconversation.ItemInput, Role: "developer", Content: "other compacted"}},
		coreconversation.CompactionStats{Compacted: true},
	); err != nil {
		t.Fatalf("append compaction: %v", err)
	}
	snapshot, err := store.Read(ctx, corethread.ReadParams{ID: ref.ID})
	if err != nil {
		t.Fatalf("read thread: %v", err)
	}

	result, err := Project(ProjectionInput{Thread: snapshot, Provider: provider, Mode: coreconversation.ProjectionFullReplay})
	if err != nil {
		t.Fatalf("Project: %v", err)
	}
	if len(result.Items) != 1 || result.Items[0].Content != "old" {
		t.Fatalf("items = %#v, want original provider replay", result.Items)
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
