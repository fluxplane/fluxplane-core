package conversation

import (
	"context"
	"fmt"

	coreconversation "github.com/fluxplane/agentruntime/core/conversation"
	"github.com/fluxplane/agentruntime/core/event"
	corethread "github.com/fluxplane/agentruntime/core/thread"
)

// ProjectionInput describes transcript projection over a thread branch.
type ProjectionInput struct {
	Thread     corethread.Snapshot
	BranchID   corethread.BranchID
	Provider   coreconversation.ProviderIdentity
	Pending    []coreconversation.Item
	Mode       coreconversation.ProjectionMode
	AllowEmpty bool
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
	if input.Mode == coreconversation.ProjectionNativeContinuation && head != nil && head.SupportsPreviousResponseID() {
		repaired := RepairToolContinuity(append(append([]coreconversation.Item(nil), items...), pending...), ToolContinuityRepairOptions{
			Provider: input.Provider,
		})
		out := append([]coreconversation.Item(nil), repaired.Repairs...)
		out = append(out, pending...)
		if len(out) == 0 && !input.AllowEmpty {
			out = nil
		}
		return ProjectionResult{
			Items:        out,
			NewItems:     append([]coreconversation.Item(nil), out...),
			Continuation: head,
			Mode:         coreconversation.ProjectionNativeContinuation,
		}, nil
	}
	repaired := RepairToolContinuity(append(append([]coreconversation.Item(nil), items...), pending...), ToolContinuityRepairOptions{
		Provider:            input.Provider,
		RepairOrphanResults: true,
	})
	newItems := append([]coreconversation.Item(nil), repaired.Repairs...)
	newItems = append(newItems, pending...)
	return ProjectionResult{
		Items:    repaired.Items,
		NewItems: newItems,
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
	var records []corethread.AppendRecord
	if len(items) > 0 {
		for i, item := range items {
			if err := item.Validate(); err != nil {
				return fmt.Errorf("conversation: item %d: %w", i, err)
			}
		}
		payload := coreconversation.ItemsAppended{
			TurnID:   turnID,
			Provider: provider,
			Items:    append([]coreconversation.Item(nil), items...),
		}
		records = append(records, corethread.AppendRecord{Event: event.Record{
			Name:    payload.EventName(),
			Scope:   event.Scope{ThreadID: string(ref.ID), TurnID: turnID},
			Payload: payload,
		}})
	}
	for _, handle := range handles {
		payload := coreconversation.ContinuationStored{TurnID: turnID, Handle: handle}
		records = append(records, corethread.AppendRecord{Event: event.Record{
			Name:    payload.EventName(),
			Scope:   event.Scope{ThreadID: string(ref.ID), TurnID: turnID},
			Payload: payload,
		}})
	}
	if len(records) == 0 {
		return nil
	}
	_, err := store.Append(ctx, ref, records...)
	return err
}
