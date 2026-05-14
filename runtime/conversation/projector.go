package conversation

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

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
		repair := missingToolResults(items, pending, input.Provider)
		out := append([]coreconversation.Item(nil), repair...)
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
	out := append(items, pending...)
	return ProjectionResult{
		Items:    out,
		NewItems: append([]coreconversation.Item(nil), pending...),
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

func missingToolResults(items, pending []coreconversation.Item, provider coreconversation.ProviderIdentity) []coreconversation.Item {
	resolved := map[string]bool{}
	for _, item := range append(append([]coreconversation.Item(nil), items...), pending...) {
		if item.Kind == coreconversation.ItemToolResult && strings.TrimSpace(item.CallID) != "" {
			resolved[item.CallID] = true
		}
	}
	var out []coreconversation.Item
	for _, item := range items {
		if item.Kind != coreconversation.ItemOutput || strings.TrimSpace(item.CallID) == "" || resolved[item.CallID] {
			continue
		}
		resolved[item.CallID] = true
		repair := coreconversation.Item{
			Provider: provider,
			Kind:     coreconversation.ItemToolResult,
			CallID:   item.CallID,
			Name:     item.Name,
			Content: map[string]any{
				"code":    "tool_result_missing",
				"message": "Tool call did not complete because the previous turn failed before a result could be recorded.",
			},
			Metadata: map[string]string{"is_error": "true"},
		}
		if callType := providerCallType(item); callType != "" {
			repair.Metadata["provider_call_type"] = callType
		}
		out = append(out, repair)
	}
	return out
}

func providerCallType(item coreconversation.Item) string {
	if item.Metadata != nil && strings.TrimSpace(item.Metadata["provider_call_type"]) != "" {
		return strings.TrimSpace(item.Metadata["provider_call_type"])
	}
	if len(item.Native) == 0 {
		return ""
	}
	var raw struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(item.Native, &raw); err != nil {
		return ""
	}
	switch raw.Type {
	case "function_call", "custom_tool_call":
		return raw.Type
	default:
		return ""
	}
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
