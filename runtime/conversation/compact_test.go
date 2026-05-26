package conversation

import (
	"strings"
	"testing"

	coreconversation "github.com/fluxplane/fluxplane-core/core/conversation"
)

func TestCompactTranscriptCompactsLargeToolResultsAndClearsNative(t *testing.T) {
	large := strings.Repeat("diff line\n", 1000)
	transcript := coreconversation.Transcript{
		Provider: coreconversation.ProviderIdentity{Provider: "codex", API: "codex.responses"},
		Items: []coreconversation.Item{
			{Kind: coreconversation.ItemOutput, CallID: "call_1", Name: "file_edit"},
			{
				Kind:    coreconversation.ItemToolResult,
				CallID:  "call_1",
				Name:    "file_edit",
				Content: large,
				Native:  []byte(`{"type":"function_call_output","call_id":"call_1","output":"` + large + `"}`),
			},
		},
		NewItems: []coreconversation.Item{
			{Kind: coreconversation.ItemOutput, CallID: "call_1", Name: "file_edit"},
			{
				Kind:    coreconversation.ItemToolResult,
				CallID:  "call_1",
				Name:    "file_edit",
				Content: large,
				Native:  []byte(`{"type":"function_call_output","call_id":"call_1","output":"` + large + `"}`),
			},
		},
	}

	result := CompactTranscript(transcript, CompactOptions{MaxInputTokens: 2048, SafetyMarginTokens: 1, LargeItemTokens: 128})
	if !result.Compacted {
		t.Fatal("result.Compacted = false, want true")
	}
	item := result.Transcript.Items[1]
	if item.Native != nil {
		t.Fatalf("native = %s, want cleared", item.Native)
	}
	if !strings.Contains(item.Content.(string), "file_edit result omitted") {
		t.Fatalf("content = %#v, want compact tool result", item.Content)
	}
	if item.Metadata["compaction"] != "tool_result_summary" {
		t.Fatalf("metadata = %#v, want tool_result_summary", item.Metadata)
	}
	if result.Transcript.NewItems[1].Native != nil {
		t.Fatal("new item native was not cleared")
	}
}

// TestCompactTranscriptSummarizedItemsCoversBothItemsAndNewItems regresses a
// counting bug: compactLargeItems was called on both out.Items and
// out.NewItems, but only the first call's return was assigned to
// SummarizedItems - the second call's count was discarded so the reported
// total under-counted when both lists carried large items.
func TestCompactTranscriptSummarizedItemsCoversBothItemsAndNewItems(t *testing.T) {
	large := strings.Repeat("diff line\n", 1000)
	mkPair := func() []coreconversation.Item {
		return []coreconversation.Item{
			{Kind: coreconversation.ItemOutput, CallID: "call_x", Name: "file_edit"},
			{
				Kind:    coreconversation.ItemToolResult,
				CallID:  "call_x",
				Name:    "file_edit",
				Content: large,
			},
		}
	}
	transcript := coreconversation.Transcript{
		Provider: coreconversation.ProviderIdentity{Provider: "codex", API: "codex.responses"},
		Items:    mkPair(),
		NewItems: mkPair(),
	}
	result := CompactTranscript(transcript, CompactOptions{MaxInputTokens: 2048, SafetyMarginTokens: 1, LargeItemTokens: 128})
	if !result.Compacted {
		t.Fatal("result.Compacted = false, want true")
	}
	// One large tool result in Items + one in NewItems = 2 summarized items.
	// The old code reported 1 because it ignored the NewItems call's return.
	if result.SummarizedItems != 2 {
		t.Fatalf("SummarizedItems = %d, want 2 (covers both Items and NewItems)", result.SummarizedItems)
	}
}

func TestCompactTranscriptLeavesLargeInBudgetToolResultUnchanged(t *testing.T) {
	large := strings.Repeat("file content\n", 1000)
	native := []byte(`{"type":"function_call_output","call_id":"call_1","output":"` + large + `"}`)
	transcript := coreconversation.Transcript{
		Provider: coreconversation.ProviderIdentity{Provider: "openai", API: "openai.responses"},
		Items: []coreconversation.Item{
			{Kind: coreconversation.ItemOutput, CallID: "call_1", Name: "file_read"},
			{
				Kind:    coreconversation.ItemToolResult,
				CallID:  "call_1",
				Name:    "file_read",
				Content: large,
				Native:  native,
			},
		},
		NewItems: []coreconversation.Item{
			{Kind: coreconversation.ItemOutput, CallID: "call_1", Name: "file_read"},
			{
				Kind:    coreconversation.ItemToolResult,
				CallID:  "call_1",
				Name:    "file_read",
				Content: large,
				Native:  native,
			},
		},
	}

	result := CompactTranscript(transcript, CompactOptions{MaxInputTokens: 128000, SafetyMarginTokens: 1, LargeItemTokens: 128})
	if result.Compacted {
		t.Fatal("result.Compacted = true, want false")
	}
	item := result.Transcript.Items[1]
	if item.Content != large {
		t.Fatalf("content changed, got %#v", item.Content)
	}
	if string(item.Native) != string(native) {
		t.Fatalf("native changed, got %s", item.Native)
	}
	if result.Transcript.NewItems[1].Content != large {
		t.Fatalf("new item content changed, got %#v", result.Transcript.NewItems[1].Content)
	}
}

func TestCompactTranscriptDropsOldCompletedToolPairs(t *testing.T) {
	transcript := coreconversation.Transcript{
		Provider: coreconversation.ProviderIdentity{Provider: "codex", API: "codex.responses"},
		Items: []coreconversation.Item{
			{Kind: coreconversation.ItemInput, Role: "user", Content: strings.Repeat("old user ", 200)},
			{Kind: coreconversation.ItemOutput, Role: "assistant", CallID: "call_1", Name: "file_edit", Native: []byte(strings.Repeat("tool call ", 400))},
			{Kind: coreconversation.ItemToolResult, CallID: "call_1", Name: "file_edit", Content: strings.Repeat("tool result ", 400)},
			{Kind: coreconversation.ItemOutput, Role: "assistant", Content: strings.Repeat("old answer ", 200)},
			{Kind: coreconversation.ItemInput, Role: "user", Content: "current"},
		},
		NewItems: []coreconversation.Item{{Kind: coreconversation.ItemInput, Role: "user", Content: "current"}},
	}

	result := CompactTranscript(transcript, CompactOptions{
		MaxInputTokens:      128,
		SafetyMarginTokens:  1,
		LargeItemTokens:     64,
		PreserveRecentItems: 1,
	})
	if result.OmittedItems == 0 {
		t.Fatalf("omitted = %d, want old items omitted", result.OmittedItems)
	}
	if !hasCompactionNotice(result.Transcript.Items) {
		t.Fatalf("items = %#v, want compaction notice", result.Transcript.Items)
	}
	if hasCallID(result.Transcript.Items, "call_1") {
		t.Fatalf("items = %#v, want completed old tool pair removed together", result.Transcript.Items)
	}
	if got := result.Transcript.Items[len(result.Transcript.Items)-1].Content; got != "current" {
		t.Fatalf("last content = %#v, want current", got)
	}
}

func TestCompactTranscriptDropsOldMultiToolCallGroupsTogether(t *testing.T) {
	transcript := coreconversation.Transcript{
		Provider: coreconversation.ProviderIdentity{Provider: "codex", API: "codex.responses"},
		Items: []coreconversation.Item{
			{Kind: coreconversation.ItemInput, Role: "user", Content: strings.Repeat("old user ", 200)},
			{Kind: coreconversation.ItemOutput, Role: "assistant", CallID: "call_1", Name: "read", Native: []byte(strings.Repeat("tool call 1 ", 250))},
			{Kind: coreconversation.ItemOutput, Role: "assistant", CallID: "call_2", Name: "diff", Native: []byte(strings.Repeat("tool call 2 ", 250))},
			{Kind: coreconversation.ItemToolResult, CallID: "call_1", Name: "read", Content: strings.Repeat("tool result 1 ", 250)},
			{Kind: coreconversation.ItemToolResult, CallID: "call_2", Name: "diff", Content: strings.Repeat("tool result 2 ", 250)},
			{Kind: coreconversation.ItemInput, Role: "user", Content: "current"},
		},
		NewItems: []coreconversation.Item{{Kind: coreconversation.ItemInput, Role: "user", Content: "current"}},
	}

	result := CompactTranscript(transcript, CompactOptions{
		MaxInputTokens:      128,
		SafetyMarginTokens:  1,
		LargeItemTokens:     64,
		PreserveRecentItems: 1,
	})
	if result.OmittedItems == 0 {
		t.Fatalf("omitted = %d, want old items omitted", result.OmittedItems)
	}
	if hasCallID(result.Transcript.Items, "call_1") || hasCallID(result.Transcript.Items, "call_2") {
		t.Fatalf("items = %#v, want multi-tool group removed together", result.Transcript.Items)
	}
	if got := result.Transcript.Items[len(result.Transcript.Items)-1].Content; got != "current" {
		t.Fatalf("last content = %#v, want current", got)
	}
}

func TestCompactTranscriptGroupsToolCallsWithoutTopLevelCallID(t *testing.T) {
	provider := coreconversation.ProviderIdentity{Provider: "codex", API: "codex.responses"}
	transcript := coreconversation.Transcript{
		Provider: provider,
		Items: []coreconversation.Item{
			{Kind: coreconversation.ItemInput, Role: "user", Content: strings.Repeat("old user ", 200)},
			{
				Kind: coreconversation.ItemOutput,
				Name: "file_edit",
				ToolCalls: []coreconversation.ToolCallRef{{
					CallID: "call_1",
					Name:   "file_edit",
					Type:   "function_call",
					Input:  `{"path":"AGENTS.md"}`,
				}},
				Native: []byte(strings.Repeat("tool call ", 400)),
			},
			{Kind: coreconversation.ItemToolResult, CallID: "call_1", Name: "file_edit", Content: strings.Repeat("tool result ", 400)},
			{Kind: coreconversation.ItemInput, Role: "user", Content: "current"},
		},
		NewItems: []coreconversation.Item{{Kind: coreconversation.ItemInput, Role: "user", Content: "current"}},
	}

	result := CompactTranscript(transcript, CompactOptions{
		MaxInputTokens:      128,
		SafetyMarginTokens:  1,
		LargeItemTokens:     64,
		PreserveRecentItems: 1,
	})
	if result.OmittedItems == 0 {
		t.Fatalf("omitted = %d, want old tool call/result group omitted", result.OmittedItems)
	}
	if hasCallID(result.Transcript.Items, "call_1") {
		t.Fatalf("items = %#v, want tool call and result removed together", result.Transcript.Items)
	}
	if err := ValidateContinuity(result.Transcript.Items, ValidateOptions{Provider: provider}); err != nil {
		t.Fatalf("compacted transcript continuity: %v", err)
	}
}

func hasCompactionNotice(items []coreconversation.Item) bool {
	for _, item := range items {
		if item.Metadata["compaction"] == "transcript_omission" {
			return true
		}
	}
	return false
}

func hasCallID(items []coreconversation.Item, callID string) bool {
	for _, item := range items {
		if item.CallID == callID {
			return true
		}
		for _, ref := range item.ToolCallRefs() {
			if ref.CallID == callID {
				return true
			}
		}
	}
	return false
}
