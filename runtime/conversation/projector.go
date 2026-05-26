package conversation

import (
	"context"
	"fmt"
	"strings"

	coreconversation "github.com/fluxplane/fluxplane-core/core/conversation"
	"github.com/fluxplane/fluxplane-core/core/event"
	corethread "github.com/fluxplane/fluxplane-core/core/thread"
)

// ProjectionInput describes transcript projection over a thread branch.
type ProjectionInput struct {
	Thread                corethread.Snapshot
	BranchID              corethread.BranchID
	Provider              coreconversation.ProviderIdentity
	Pending               []coreconversation.Item
	PendingCommitted      bool
	PendingCommittedCount int
	Mode                  coreconversation.ProjectionMode
	AllowEmpty            bool
}

// ProjectionResult is the provider transcript slice an adapter should send.
type ProjectionResult struct {
	Items        []coreconversation.Item              `json:"items,omitempty"`
	NewItems     []coreconversation.Item              `json:"new_items,omitempty"`
	Continuation *coreconversation.ContinuationHandle `json:"continuation,omitempty"`
	Mode         coreconversation.ProjectionMode      `json:"mode"`
}

// Transcript returns the core transcript shape for adapter consumption.
func (r ProjectionResult) Transcript(provider coreconversation.ProviderIdentity) coreconversation.Transcript {
	return coreconversation.Transcript{
		Provider:     provider,
		Items:        append([]coreconversation.Item(nil), r.Items...),
		NewItems:     append([]coreconversation.Item(nil), r.NewItems...),
		Continuation: r.Continuation,
		Mode:         r.Mode,
	}
}

// Project projects a provider transcript from thread events.
func Project(input ProjectionInput) (ProjectionResult, error) {
	items, head, err := replay(input.Thread, input.BranchID, input.Provider)
	if err != nil {
		return ProjectionResult{}, err
	}
	pending := filterCompatible(input.Pending, input.Provider)
	committedCount := input.PendingCommittedCount
	if input.PendingCommitted && committedCount == 0 {
		committedCount = len(pending)
	}
	if committedCount < 0 {
		committedCount = 0
	}
	if committedCount > len(pending) {
		committedCount = len(pending)
	}
	uncommittedPending := pending[committedCount:]
	canonical := append([]coreconversation.Item(nil), items...)
	canonical = append(canonical, uncommittedPending...)
	if err := ValidateContinuity(canonical, ValidateOptions{Provider: input.Provider}); err != nil {
		return ProjectionResult{}, err
	}
	newItems := uncommittedPending
	if input.Mode == coreconversation.ProjectionNativeContinuation && head != nil && head.SupportsPreviousResponseID() {
		out := append([]coreconversation.Item(nil), pending...)
		if len(out) == 0 && !input.AllowEmpty {
			out = nil
		}
		return ProjectionResult{
			Items:        out,
			NewItems:     append([]coreconversation.Item(nil), newItems...),
			Continuation: head,
			Mode:         coreconversation.ProjectionNativeContinuation,
		}, nil
	}
	return ProjectionResult{
		Items:    canonical,
		NewItems: append([]coreconversation.Item(nil), newItems...),
		Mode:     coreconversation.ProjectionFullReplay,
	}, nil
}

func replay(snapshot corethread.Snapshot, branchID corethread.BranchID, provider coreconversation.ProviderIdentity) ([]coreconversation.Item, *coreconversation.ContinuationHandle, error) {
	records, err := snapshot.EventsForBranch(branchID)
	if err != nil {
		return nil, nil, err
	}
	var (
		items []coreconversation.Item
		head  *coreconversation.ContinuationHandle
	)
	for _, record := range records {
		switch payload := record.Event.Payload.(type) {
		case coreconversation.ItemsAppended:
			if !payload.Provider.Compatible(provider) {
				continue
			}
			items = append(items, filterCompatible(payload.Items, provider)...)
		case *coreconversation.ItemsAppended:
			if payload == nil || !payload.Provider.Compatible(provider) {
				continue
			}
			items = append(items, filterCompatible(payload.Items, provider)...)
		case coreconversation.CompactionStored:
			if !payload.Provider.Compatible(provider) {
				continue
			}
			items = filterCompatible(payload.Items, provider)
			head = nil
		case *coreconversation.CompactionStored:
			if payload == nil || !payload.Provider.Compatible(provider) {
				continue
			}
			items = filterCompatible(payload.Items, provider)
			head = nil
		case coreconversation.ContinuationStored:
			if payload.Handle.Provider.Compatible(provider) {
				copied := payload.Handle
				head = &copied
			}
		case *coreconversation.ContinuationStored:
			if payload != nil && payload.Handle.Provider.Compatible(provider) {
				copied := payload.Handle
				head = &copied
			}
		}
	}
	return items, head, nil
}

func filterCompatible(items []coreconversation.Item, provider coreconversation.ProviderIdentity) []coreconversation.Item {
	out := make([]coreconversation.Item, 0, len(items))
	for _, item := range items {
		if item.Provider.Compatible(provider) {
			out = append(out, item)
		}
	}
	return out
}

// ValidateOptions configures strict provider transcript validation.
type ValidateOptions struct {
	Provider coreconversation.ProviderIdentity
}

// ContinuityError describes a malformed provider-visible transcript. Normal
// projection must return this error instead of synthesizing provider items.
type ContinuityError struct {
	Reason string
	CallID string
	Index  int
}

func (e ContinuityError) Error() string {
	if e.CallID != "" {
		return fmt.Sprintf("conversation continuity: %s call_id=%q item_index=%d", e.Reason, e.CallID, e.Index)
	}
	return fmt.Sprintf("conversation continuity: %s item_index=%d", e.Reason, e.Index)
}

// ValidateContinuity verifies that provider-visible transcript items are
// already structurally valid. It deliberately does not repair malformed
// transcripts; current-version sessions must make invalid states impossible.
func ValidateContinuity(items []coreconversation.Item, opts ValidateOptions) error {
	open := map[string]int{}
	for i, original := range items {
		item := ensureItemProvider(original, opts.Provider)
		if item.Metadata["repair"] != "" {
			return ContinuityError{Reason: "repair artifact is not durable transcript", CallID: item.CallID, Index: i}
		}
		switch item.Kind {
		case coreconversation.ItemOutput:
			calls := item.ToolCallRefs()
			if len(open) > 0 && len(calls) == 0 {
				callID, openedAt := earliestOpen(open)
				return ContinuityError{Reason: fmt.Sprintf("assistant tool call left open before next assistant output opened_at=%d", openedAt), CallID: callID, Index: i}
			}
			for _, call := range calls {
				callID := strings.TrimSpace(call.CallID)
				if callID == "" {
					return ContinuityError{Reason: "assistant tool call missing provider call id", Index: i}
				}
				if prior, ok := open[callID]; ok {
					return ContinuityError{Reason: fmt.Sprintf("duplicate open assistant tool call first_item_index=%d", prior), CallID: callID, Index: i}
				}
				open[callID] = i
			}
		case coreconversation.ItemToolResult:
			callID := strings.TrimSpace(item.CallID)
			if callID == "" {
				return ContinuityError{Reason: "tool result missing provider call id", Index: i}
			}
			if _, ok := open[callID]; !ok {
				return ContinuityError{Reason: "tool result without open assistant tool call", CallID: callID, Index: i}
			}
			delete(open, callID)
		default:
			if len(open) > 0 {
				callID, openedAt := earliestOpen(open)
				return ContinuityError{Reason: fmt.Sprintf("assistant tool call left open before next transcript item opened_at=%d", openedAt), CallID: callID, Index: i}
			}
		}
	}
	if len(open) > 0 {
		callID, openedAt := earliestOpen(open)
		return ContinuityError{Reason: "assistant tool call left open at transcript end", CallID: callID, Index: openedAt}
	}
	return nil
}

// earliestOpen returns the call ID with the smallest opened-at index, with the
// call ID itself as a tie-breaker. It exists so that ValidateContinuity reports
// the same call when multiple tool calls are open simultaneously instead of
// picking one at random via map iteration order.
func earliestOpen(open map[string]int) (string, int) {
	var (
		earliestID    string
		earliestIndex int
		seeded        bool
	)
	for callID, index := range open {
		if !seeded || index < earliestIndex || (index == earliestIndex && callID < earliestID) {
			earliestID = callID
			earliestIndex = index
			seeded = true
		}
	}
	return earliestID, earliestIndex
}

func ensureItemProvider(item coreconversation.Item, provider coreconversation.ProviderIdentity) coreconversation.Item {
	if item.Provider.Provider == "" {
		item.Provider = provider
	}
	return item
}

// AppendRecords builds validated provider transcript append records.
func AppendRecords(turnID string, provider coreconversation.ProviderIdentity, items []coreconversation.Item, handles ...coreconversation.ContinuationHandle) ([]corethread.AppendRecord, error) {
	var records []corethread.AppendRecord
	if len(items) > 0 {
		for i, item := range items {
			if err := item.Validate(); err != nil {
				return nil, fmt.Errorf("conversation: item %d: %w", i, err)
			}
			if item.Metadata["repair"] != "" {
				return nil, fmt.Errorf("conversation: item %d: repair artifact cannot be appended", i)
			}
		}
		payload := coreconversation.ItemsAppended{
			TurnID:   turnID,
			Provider: provider,
			Items:    append([]coreconversation.Item(nil), items...),
		}
		records = append(records, corethread.AppendRecord{Event: event.Record{
			Name:    payload.EventName(),
			Scope:   event.Scope{TurnID: turnID},
			Payload: payload,
		}})
	}
	for _, handle := range handles {
		payload := coreconversation.ContinuationStored{TurnID: turnID, Handle: handle}
		records = append(records, corethread.AppendRecord{Event: event.Record{
			Name:    payload.EventName(),
			Scope:   event.Scope{TurnID: turnID},
			Payload: payload,
		}})
	}
	return records, nil
}

// AppendCompaction records a provider transcript compaction checkpoint.
func AppendCompaction(ctx context.Context, store corethread.Store, ref corethread.Ref, turnID string, provider coreconversation.ProviderIdentity, items []coreconversation.Item, stats coreconversation.CompactionStats) error {
	if store == nil || ref.ID == "" {
		return nil
	}
	for i, item := range items {
		if err := item.Validate(); err != nil {
			return fmt.Errorf("conversation: compaction item %d: %w", i, err)
		}
	}
	if err := ValidateContinuity(items, ValidateOptions{Provider: provider}); err != nil {
		return fmt.Errorf("conversation: compaction continuity: %w", err)
	}
	payload := coreconversation.CompactionStored{
		TurnID:   turnID,
		Provider: provider,
		Items:    append([]coreconversation.Item(nil), items...),
		Stats:    stats,
	}
	_, err := store.Append(ctx, ref, corethread.AppendRecord{Event: event.Record{
		Name:    payload.EventName(),
		Scope:   event.Scope{ThreadID: string(ref.ID), TurnID: turnID},
		Payload: payload,
	}})
	return err
}

// Append records provider transcript events to a thread store.
func Append(ctx context.Context, store corethread.Store, ref corethread.Ref, turnID string, provider coreconversation.ProviderIdentity, items []coreconversation.Item, handles ...coreconversation.ContinuationHandle) error {
	if store == nil || ref.ID == "" {
		return nil
	}
	records, err := AppendRecords(turnID, provider, items, handles...)
	if err != nil {
		return err
	}
	if len(records) == 0 {
		return nil
	}
	_, err = store.Append(ctx, ref, records...)
	return err
}
