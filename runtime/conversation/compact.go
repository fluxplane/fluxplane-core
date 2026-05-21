package conversation

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	coreconversation "github.com/fluxplane/engine/core/conversation"
)

const (
	defaultLargeItemTokens     = 4096
	defaultPreserveRecentItems = 12
	defaultSafetyMarginTokens  = 4096
)

// CompactOptions configures deterministic provider-transcript compaction.
type CompactOptions struct {
	MaxInputTokens      int
	SafetyMarginTokens  int
	LargeItemTokens     int
	PreserveRecentItems int
}

// CompactResult describes a transcript compaction pass.
type CompactResult struct {
	Transcript      coreconversation.Transcript
	OriginalTokens  int
	CompactedTokens int
	Compacted       bool
	OmittedItems    int
	SummarizedItems int
}

// CompactTranscript bounds the provider-visible transcript without changing
// durable semantic history. It preserves recent items and keeps tool result
// items structurally valid when their content is shortened.
func CompactTranscript(transcript coreconversation.Transcript, opts CompactOptions) CompactResult {
	opts = compactOptionsWithDefaults(opts)
	out := cloneTranscript(transcript)
	original := estimateItemsTokens(out.Items)
	if opts.MaxInputTokens <= 0 {
		return CompactResult{Transcript: out, OriginalTokens: original, CompactedTokens: original}
	}
	limit := opts.MaxInputTokens - opts.SafetyMarginTokens
	if limit <= 0 {
		limit = opts.MaxInputTokens
	}
	if original <= limit {
		return CompactResult{Transcript: out, OriginalTokens: original, CompactedTokens: original}
	}

	compacted := false
	omitted := 0
	if estimateItemsTokens(out.Items) > limit {
		var changed bool
		out.Items, omitted, changed = omitOldItems(out.Items, limit, opts.PreserveRecentItems, len(out.NewItems), out.Provider)
		if changed {
			compacted = true
		}
	}
	if estimateItemsTokens(out.Items) > limit {
		summarized := compactLargeItems(out.Items, opts.LargeItemTokens)
		if summarized > 0 {
			compacted = true
		}
		if compactLargeItems(out.NewItems, opts.LargeItemTokens) > 0 {
			compacted = true
		}
		return CompactResult{
			Transcript:      out,
			OriginalTokens:  original,
			CompactedTokens: estimateItemsTokens(out.Items),
			Compacted:       compacted,
			OmittedItems:    omitted,
			SummarizedItems: summarized,
		}
	}

	return CompactResult{
		Transcript:      out,
		OriginalTokens:  original,
		CompactedTokens: estimateItemsTokens(out.Items),
		Compacted:       compacted,
		OmittedItems:    omitted,
	}
}

func compactOptionsWithDefaults(opts CompactOptions) CompactOptions {
	if opts.LargeItemTokens <= 0 {
		opts.LargeItemTokens = defaultLargeItemTokens
	}
	if opts.PreserveRecentItems <= 0 {
		opts.PreserveRecentItems = defaultPreserveRecentItems
	}
	if opts.SafetyMarginTokens < 0 {
		opts.SafetyMarginTokens = 0
	}
	if opts.SafetyMarginTokens == 0 {
		opts.SafetyMarginTokens = defaultSafetyMarginTokens
	}
	return opts
}

func compactLargeItems(items []coreconversation.Item, maxTokens int) int {
	changed := 0
	for i := range items {
		tokens := estimateItemTokens(items[i])
		if tokens <= maxTokens {
			continue
		}
		switch {
		case items[i].Kind == coreconversation.ItemToolResult:
			items[i] = compactToolResult(items[i], tokens)
			changed++
		case items[i].Kind == coreconversation.ItemOutput && strings.TrimSpace(items[i].CallID) == "":
			items[i] = compactAssistantOutput(items[i], tokens)
			changed++
		case items[i].Kind == coreconversation.ItemInput && items[i].Metadata["context"] == "diff" && strings.TrimSpace(items[i].Role) != "user":
			items[i] = compactContextInput(items[i], tokens)
			changed++
		}
	}
	return changed
}

func compactToolResult(item coreconversation.Item, tokens int) coreconversation.Item {
	name := strings.TrimSpace(item.Name)
	if name == "" {
		name = "tool"
	}
	item.Content = fmt.Sprintf("%s result omitted from model transcript because it exceeded the transcript budget. Original estimate: %d tokens. Use a follow-up read/diff operation if exact content is needed.", name, tokens)
	item.Native = nil
	item.Metadata = compactMetadata(item.Metadata, tokens, "tool_result_summary")
	return item
}

func compactAssistantOutput(item coreconversation.Item, tokens int) coreconversation.Item {
	item.Content = fmt.Sprintf("Assistant output omitted from model transcript because it exceeded the transcript budget. Original estimate: %d tokens.", tokens)
	item.Native = nil
	item.Metadata = compactMetadata(item.Metadata, tokens, "assistant_output_summary")
	return item
}

func compactContextInput(item coreconversation.Item, tokens int) coreconversation.Item {
	item.Content = fmt.Sprintf("Context diff omitted from model transcript because it exceeded the transcript budget. Original estimate: %d tokens.", tokens)
	item.Native = nil
	item.Metadata = compactMetadata(item.Metadata, tokens, "context_diff_summary")
	return item
}

func compactMetadata(metadata map[string]string, tokens int, mode string) map[string]string {
	out := cloneStringMap(metadata)
	out["compacted"] = "true"
	out["compaction"] = mode
	out["original_estimated_tokens"] = strconv.Itoa(tokens)
	return out
}

type itemGroup struct {
	start     int
	end       int
	tokens    int
	droppable bool
}

func omitOldItems(items []coreconversation.Item, limit, preserveRecent, newItemCount int, provider coreconversation.ProviderIdentity) ([]coreconversation.Item, int, bool) {
	if len(items) == 0 || estimateItemsTokens(items) <= limit {
		return items, 0, false
	}
	preserveFrom := len(items) - maxInt(preserveRecent, newItemCount)
	if preserveFrom < 0 {
		preserveFrom = 0
	}
	groups := transcriptGroups(items, preserveFrom)
	remove := make([]bool, len(items))
	total := estimateItemsTokens(items)
	omitted := 0
	for _, group := range groups {
		if total <= limit {
			break
		}
		if !group.droppable {
			continue
		}
		for i := group.start; i < group.end; i++ {
			remove[i] = true
		}
		total -= group.tokens
		omitted += group.end - group.start
	}
	if omitted == 0 {
		return items, 0, false
	}

	out := make([]coreconversation.Item, 0, len(items)-omitted+1)
	insertedNotice := false
	for i, item := range items {
		if remove[i] {
			if !insertedNotice {
				out = append(out, compactionNotice(provider, omitted))
				insertedNotice = true
			}
			continue
		}
		out = append(out, item)
	}
	return out, omitted, true
}

func transcriptGroups(items []coreconversation.Item, preserveFrom int) []itemGroup {
	groups := make([]itemGroup, 0, len(items))
	for i := 0; i < len(items); i++ {
		end := toolCallResultGroupEnd(items, i)
		droppable := end <= preserveFrom && groupDroppable(items[i:end])
		groups = append(groups, itemGroup{start: i, end: end, tokens: estimateItemsTokens(items[i:end]), droppable: droppable})
		i = end - 1
	}
	return groups
}

func toolCallResultGroupEnd(items []coreconversation.Item, start int) int {
	if start >= len(items) || items[start].Kind != coreconversation.ItemOutput || strings.TrimSpace(items[start].CallID) == "" {
		return start + 1
	}
	callIDs := map[string]bool{}
	endCalls := start
	for endCalls < len(items) && items[endCalls].Kind == coreconversation.ItemOutput {
		callID := strings.TrimSpace(items[endCalls].CallID)
		if callID == "" {
			break
		}
		callIDs[callID] = true
		endCalls++
	}
	if len(callIDs) == 0 || endCalls >= len(items) {
		return start + 1
	}
	seenResults := map[string]bool{}
	end := endCalls
	for end < len(items) && items[end].Kind == coreconversation.ItemToolResult {
		callID := strings.TrimSpace(items[end].CallID)
		if !callIDs[callID] {
			break
		}
		seenResults[callID] = true
		end++
		if len(seenResults) == len(callIDs) {
			return end
		}
	}
	if matchingToolResult(items[start], items[start+1]) {
		return start + 2
	}
	return start + 1
}

func groupDroppable(items []coreconversation.Item) bool {
	for _, item := range items {
		if item.Kind == coreconversation.ItemInput {
			role := strings.TrimSpace(item.Role)
			if role == "system" || role == "developer" {
				return false
			}
		}
		if item.Kind == coreconversation.ItemToolResult {
			return len(items) > 1
		}
	}
	return true
}

func matchingToolResult(call, result coreconversation.Item) bool {
	return result.Kind == coreconversation.ItemToolResult && strings.TrimSpace(call.CallID) != "" && call.CallID == result.CallID
}

func compactionNotice(provider coreconversation.ProviderIdentity, omitted int) coreconversation.Item {
	return coreconversation.Item{
		Provider: provider,
		Kind:     coreconversation.ItemInput,
		Role:     "developer",
		Content:  fmt.Sprintf("Earlier transcript items omitted to fit the model context window. Omitted items: %d. Durable session history remains available through tools and stored events.", omitted),
		Metadata: map[string]string{"compacted": "true", "compaction": "transcript_omission"},
	}
}

// EstimateItemsTokens returns the deterministic transcript token estimate used
// by compaction decisions.
func EstimateItemsTokens(items []coreconversation.Item) int {
	return estimateItemsTokens(items)
}

func estimateItemsTokens(items []coreconversation.Item) int {
	total := 0
	for _, item := range items {
		total += estimateItemTokens(item)
	}
	return total
}

func estimateItemTokens(item coreconversation.Item) int {
	size := len(item.Native)
	if size == 0 {
		size = len(contentText(item.Content))
	}
	if size == 0 {
		size = len(item.Kind) + len(item.Role) + len(item.Name) + len(item.CallID)
	}
	return (size + 3) / 4
}

func contentText(value any) string {
	switch typed := value.(type) {
	case nil:
		return ""
	case string:
		return typed
	case []byte:
		return string(typed)
	default:
		data, err := json.Marshal(typed)
		if err != nil {
			return fmt.Sprint(typed)
		}
		return string(data)
	}
}

func cloneTranscript(in coreconversation.Transcript) coreconversation.Transcript {
	out := in
	out.Items = cloneItems(in.Items)
	out.NewItems = cloneItems(in.NewItems)
	if in.Continuation != nil {
		copied := *in.Continuation
		out.Continuation = &copied
	}
	return out
}

func cloneItems(items []coreconversation.Item) []coreconversation.Item {
	out := make([]coreconversation.Item, len(items))
	for i, item := range items {
		out[i] = item
		if item.Native != nil {
			out[i].Native = append([]byte(nil), item.Native...)
		}
		out[i].Metadata = cloneStringMap(item.Metadata)
	}
	return out
}

func cloneStringMap(values map[string]string) map[string]string {
	if values == nil {
		return map[string]string{}
	}
	out := make(map[string]string, len(values))
	for key, value := range values {
		out[key] = value
	}
	return out
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
