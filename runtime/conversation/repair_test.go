package conversation

import (
	"testing"

	coreconversation "github.com/fluxplane/agentruntime/core/conversation"
)

func TestRepairToolContinuityClosesToolCallsBeforeNextAssistantOutput(t *testing.T) {
	provider := coreconversation.ProviderIdentity{Provider: "claudecode", API: "claudecode.messages", Model: "claude-test"}
	first := coreconversation.Item{
		Provider: provider,
		Kind:     coreconversation.ItemOutput,
		Role:     "assistant",
		ToolCalls: []coreconversation.ToolCallRef{{
			CallID: "toolu_1",
			Name:   "file_read",
			Type:   "tool_use",
			Input:  map[string]string{"path": "one.md"},
		}},
	}
	second := coreconversation.Item{
		Provider: provider,
		Kind:     coreconversation.ItemOutput,
		Role:     "assistant",
		ToolCalls: []coreconversation.ToolCallRef{{
			CallID: "toolu_2",
			Name:   "file_read",
			Type:   "tool_use",
			Input:  map[string]string{"path": "two.md"},
		}},
	}

	repaired := RepairToolContinuity([]coreconversation.Item{first, second}, ToolContinuityRepairOptions{Provider: provider})

	if len(repaired.Items) != 4 {
		t.Fatalf("items = %#v, want first output, first repair, second output, second repair", repaired.Items)
	}
	if repaired.Items[0].Kind != coreconversation.ItemOutput || repaired.Items[0].ToolCalls[0].CallID != "toolu_1" {
		t.Fatalf("first item = %#v, want first output", repaired.Items[0])
	}
	if repaired.Items[1].Kind != coreconversation.ItemToolResult || repaired.Items[1].CallID != "toolu_1" {
		t.Fatalf("second item = %#v, want missing result repair for first output before next assistant output", repaired.Items[1])
	}
	if repaired.Items[2].Kind != coreconversation.ItemOutput || repaired.Items[2].ToolCalls[0].CallID != "toolu_2" {
		t.Fatalf("third item = %#v, want second output", repaired.Items[2])
	}
	if repaired.Items[3].Kind != coreconversation.ItemToolResult || repaired.Items[3].CallID != "toolu_2" {
		t.Fatalf("fourth item = %#v, want missing result repair for second output", repaired.Items[3])
	}
}

func TestRepairToolContinuityClosesOpenCallsBeforeOrphanResultRepair(t *testing.T) {
	provider := coreconversation.ProviderIdentity{Provider: "claudecode", API: "claudecode.messages", Model: "claude-test"}
	call := coreconversation.Item{
		Provider: provider,
		Kind:     coreconversation.ItemOutput,
		Role:     "assistant",
		ToolCalls: []coreconversation.ToolCallRef{{
			CallID: "toolu_1",
			Name:   "file_read",
			Type:   "tool_use",
		}},
	}
	orphan := coreconversation.Item{
		Provider: provider,
		Kind:     coreconversation.ItemToolResult,
		CallID:   "toolu_orphan",
		Name:     "grep",
		Content:  "orphan",
	}

	repaired := RepairToolContinuity([]coreconversation.Item{call, orphan}, ToolContinuityRepairOptions{
		Provider:            provider,
		RepairOrphanResults: true,
	})

	if len(repaired.Items) != 4 {
		t.Fatalf("items = %#v, want output, missing repair, synthetic orphan call, orphan result", repaired.Items)
	}
	if repaired.Items[1].Kind != coreconversation.ItemToolResult || repaired.Items[1].CallID != "toolu_1" {
		t.Fatalf("second item = %#v, want missing result repair before orphan repair", repaired.Items[1])
	}
	if repaired.Items[2].Kind != coreconversation.ItemOutput || repaired.Items[2].CallID != "toolu_orphan" {
		t.Fatalf("third item = %#v, want synthetic orphan tool call", repaired.Items[2])
	}
	if repaired.Items[3].Kind != coreconversation.ItemToolResult || repaired.Items[3].CallID != "toolu_orphan" {
		t.Fatalf("fourth item = %#v, want orphan result", repaired.Items[3])
	}
}
