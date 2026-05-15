package conversation

import (
	"testing"

	"github.com/fluxplane/agentruntime/core/event"
)

func TestCompactionStoredEventName(t *testing.T) {
	ev := CompactionStored{}
	if got := ev.EventName(); got != EventCompactionStored {
		t.Fatalf("EventName = %q, want %q", got, EventCompactionStored)
	}
}

func TestRegisterEventsNilRegistry(t *testing.T) {
	if err := RegisterEvents(nil); err == nil {
		t.Fatal("RegisterEvents(nil) should return error")
	}
}

func TestRegisterEventsSucceeds(t *testing.T) {
	r := event.NewRegistry()
	if err := RegisterEvents(r); err != nil {
		t.Fatalf("RegisterEvents: %v", err)
	}
}

func TestToolCallRefsFromToolCalls(t *testing.T) {
	item := Item{
		Kind: ItemOutput,
		ToolCalls: []ToolCallRef{
			{CallID: "  c1  ", Name: ""},
			{CallID: "c2", Name: "fn"},
		},
		Name:     "default-fn",
		Metadata: map[string]string{"provider_call_type": " function "},
	}
	refs := item.ToolCallRefs()
	if len(refs) != 2 {
		t.Fatalf("ToolCallRefs len = %d, want 2", len(refs))
	}
	if refs[0].CallID != "c1" {
		t.Fatalf("refs[0].CallID = %q, want c1", refs[0].CallID)
	}
	if refs[0].Name != "default-fn" {
		t.Fatalf("refs[0].Name = %q, want default-fn", refs[0].Name)
	}
	if refs[0].Type != "function" {
		t.Fatalf("refs[0].Type = %q, want function", refs[0].Type)
	}
	if refs[1].Name != "fn" {
		t.Fatalf("refs[1].Name = %q, want fn", refs[1].Name)
	}
}

func TestToolCallRefsFromCallID(t *testing.T) {
	item := Item{
		Kind:     ItemOutput,
		CallID:   " call-abc ",
		Name:     "fn",
		Metadata: map[string]string{"provider_call_type": "function_call"},
	}
	refs := item.ToolCallRefs()
	if len(refs) != 1 {
		t.Fatalf("ToolCallRefs len = %d, want 1", len(refs))
	}
	if refs[0].CallID != "call-abc" {
		t.Fatalf("CallID = %q, want call-abc", refs[0].CallID)
	}
	if refs[0].Type != "function_call" {
		t.Fatalf("Type = %q, want function_call", refs[0].Type)
	}
}

func TestToolCallRefsNonOutput(t *testing.T) {
	item := Item{Kind: ItemInput, CallID: "c1"}
	if refs := item.ToolCallRefs(); refs != nil {
		t.Fatalf("ToolCallRefs for non-output = %v, want nil", refs)
	}
}

func TestToolCallRefsEmptyCallID(t *testing.T) {
	item := Item{Kind: ItemOutput}
	if refs := item.ToolCallRefs(); refs != nil {
		t.Fatalf("ToolCallRefs for empty callID = %v, want nil", refs)
	}
}

func TestToolCallRefsNoMetadata(t *testing.T) {
	item := Item{Kind: ItemOutput, CallID: "c1", Name: "fn"}
	refs := item.ToolCallRefs()
	if len(refs) != 1 || refs[0].Type != "" {
		t.Fatalf("ToolCallRefs without metadata: %v", refs)
	}
}

func TestCompatibleProviderFamily(t *testing.T) {
	// Family field is checked the same way as API.
	recorded := ProviderIdentity{Family: "gpt"}
	requested := ProviderIdentity{Family: "gpt"}
	if !recorded.Compatible(requested) {
		t.Fatal("Compatible = false for matching family")
	}
	recorded2 := ProviderIdentity{Family: "gpt"}
	requested2 := ProviderIdentity{Family: "claude"}
	if recorded2.Compatible(requested2) {
		t.Fatal("Compatible = true for mismatched family")
	}
}
