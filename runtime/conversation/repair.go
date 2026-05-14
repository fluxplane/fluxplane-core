package conversation

import (
	"strings"

	coreconversation "github.com/fluxplane/agentruntime/core/conversation"
)

// ToolContinuityRepairOptions configures canonical transcript continuity
// repair. Providers should render the repaired transcript, not perform this
// repair themselves.
type ToolContinuityRepairOptions struct {
	Provider            coreconversation.ProviderIdentity
	RepairOrphanResults bool
	MissingResultReason string
}

// ToolContinuityRepairKind classifies why the runtime changed or flagged a
// transcript.
type ToolContinuityRepairKind string

const (
	RepairMissingToolResult ToolContinuityRepairKind = "missing_tool_result"
	RepairOrphanToolResult  ToolContinuityRepairKind = "orphan_tool_result"
	RepairInvalidToolCall   ToolContinuityRepairKind = "invalid_tool_call"
)

// ToolContinuityRepairRecord is the inspectable explanation for a repair pass.
type ToolContinuityRepairRecord struct {
	Kind            ToolContinuityRepairKind          `json:"kind"`
	Provider        coreconversation.ProviderIdentity `json:"provider,omitempty"`
	SourceItemIndex int                               `json:"source_item_index,omitempty"`
	CallID          string                            `json:"call_id,omitempty"`
	Name            string                            `json:"name,omitempty"`
	Type            string                            `json:"type,omitempty"`
	ReasonCode      string                            `json:"reason_code,omitempty"`
	Reason          string                            `json:"reason,omitempty"`
}

// ToolContinuityRepairResult contains the repaired transcript and the items
// that were synthesized by the repair pass.
type ToolContinuityRepairResult struct {
	Items       []coreconversation.Item
	Repairs     []coreconversation.Item
	Diagnostics []ToolContinuityRepairRecord
}

// RepairToolContinuity makes assistant tool calls and tool results structurally
// complete in provider-visible transcript order.
func RepairToolContinuity(items []coreconversation.Item, opts ToolContinuityRepairOptions) ToolContinuityRepairResult {
	reason := strings.TrimSpace(opts.MissingResultReason)
	if reason == "" {
		reason = "Tool call did not complete because the previous turn failed before a result could be recorded."
	}
	state := toolContinuityState{
		provider: opts.Provider,
		reason:   reason,
		open:     map[string]coreconversation.ToolCallRef{},
	}
	out := make([]coreconversation.Item, 0, len(items))
	for i, original := range items {
		item := ensureItemProvider(original, opts.Provider)
		switch item.Kind {
		case coreconversation.ItemToolResult:
			callID := strings.TrimSpace(item.CallID)
			if !state.isOpen(callID) {
				out = append(out, state.missingResults()...)
			}
			if callID != "" && !state.isOpen(callID) && opts.RepairOrphanResults {
				synthetic, diagnostic := syntheticToolCallForOrphanResult(opts.Provider, item, i)
				out = append(out, synthetic)
				state.repairs = append(state.repairs, synthetic)
				state.diagnostics = append(state.diagnostics, diagnostic)
				state.openToolCalls(synthetic.ToolCallRefs())
			}
			out = append(out, item)
			state.resolve(callID)
		case coreconversation.ItemOutput:
			calls := item.ToolCallRefs()
			state.flagInvalidToolCalls(i, calls)
			out = append(out, state.missingResults()...)
			if len(calls) == 0 {
				out = append(out, item)
				continue
			}
			out = append(out, item)
			state.openToolCalls(calls)
		case coreconversation.ItemReasoning:
			out = append(out, state.missingResults()...)
			out = append(out, item)
		default:
			out = append(out, state.missingResults()...)
			out = append(out, item)
		}
	}
	out = append(out, state.missingResults()...)
	return ToolContinuityRepairResult{Items: out, Repairs: state.repairs, Diagnostics: state.diagnostics}
}

type toolContinuityState struct {
	provider    coreconversation.ProviderIdentity
	reason      string
	open        map[string]coreconversation.ToolCallRef
	source      map[string]int
	order       []string
	repairs     []coreconversation.Item
	diagnostics []ToolContinuityRepairRecord
}

func (s *toolContinuityState) openToolCalls(calls []coreconversation.ToolCallRef) {
	for _, call := range calls {
		call.CallID = strings.TrimSpace(call.CallID)
		if call.CallID == "" || s.open[call.CallID].CallID != "" {
			continue
		}
		s.open[call.CallID] = call
		s.order = append(s.order, call.CallID)
	}
}

func (s *toolContinuityState) flagInvalidToolCalls(index int, calls []coreconversation.ToolCallRef) {
	for _, call := range calls {
		call.CallID = strings.TrimSpace(call.CallID)
		call.Name = strings.TrimSpace(call.Name)
		call.Type = strings.TrimSpace(call.Type)
		switch {
		case call.CallID == "":
			s.diagnostics = append(s.diagnostics, repairRecord(RepairInvalidToolCall, s.provider, index, call, "empty_call_id", "Tool call did not include a provider call id."))
		case call.Name == "":
			s.diagnostics = append(s.diagnostics, repairRecord(RepairInvalidToolCall, s.provider, index, call, "empty_tool_name", "Tool call did not include a tool name."))
		default:
			if s.source == nil {
				s.source = map[string]int{}
			}
			s.source[call.CallID] = index
		}
	}
}

func (s *toolContinuityState) isOpen(callID string) bool {
	callID = strings.TrimSpace(callID)
	return callID != "" && s.open[callID].CallID != ""
}

func (s *toolContinuityState) resolve(callID string) {
	callID = strings.TrimSpace(callID)
	if callID == "" {
		return
	}
	delete(s.open, callID)
}

func (s *toolContinuityState) missingResults() []coreconversation.Item {
	var out []coreconversation.Item
	for _, callID := range s.order {
		call := s.open[callID]
		if call.CallID == "" {
			continue
		}
		repair := missingToolResult(s.provider, call, s.source[callID], s.reason)
		out = append(out, repair)
		s.repairs = append(s.repairs, repair)
		s.diagnostics = append(s.diagnostics, repairRecord(RepairMissingToolResult, s.provider, s.source[callID], call, "missing_tool_result", s.reason))
		delete(s.open, callID)
	}
	s.order = nil
	return out
}

func missingToolResult(provider coreconversation.ProviderIdentity, call coreconversation.ToolCallRef, sourceIndex int, reason string) coreconversation.Item {
	metadata := map[string]string{
		"is_error":          "true",
		"repair":            string(RepairMissingToolResult),
		"repair_reason":     reason,
		"source_item_index": fmtInt(sourceIndex),
	}
	if strings.TrimSpace(call.Type) != "" {
		metadata["provider_call_type"] = strings.TrimSpace(call.Type)
	}
	return coreconversation.Item{
		Provider: provider,
		Kind:     coreconversation.ItemToolResult,
		CallID:   strings.TrimSpace(call.CallID),
		Name:     call.Name,
		Content: map[string]any{
			"code":    "tool_result_missing",
			"message": reason,
		},
		Metadata: metadata,
	}
}

func syntheticToolCallForOrphanResult(provider coreconversation.ProviderIdentity, result coreconversation.Item, sourceIndex int) (coreconversation.Item, ToolContinuityRepairRecord) {
	callID := strings.TrimSpace(result.CallID)
	name := strings.TrimSpace(result.Name)
	if name == "" {
		name = "tool"
	}
	callType := ""
	if result.Metadata != nil {
		callType = strings.TrimSpace(result.Metadata["provider_call_type"])
	}
	if callType == "" {
		callType = "function_call"
	}
	metadata := map[string]string{
		"provider_call_type": callType,
		"repair":             string(RepairOrphanToolResult),
		"repair_reason":      "matching assistant tool call was missing from replay",
		"source_item_index":  fmtInt(sourceIndex),
	}
	call := coreconversation.ToolCallRef{
		CallID: callID,
		Name:   name,
		Type:   callType,
		Input: map[string]string{
			"repair": "orphan_tool_result",
			"reason": "matching assistant tool call was missing from replay",
		},
	}
	item := coreconversation.Item{
		Provider:  provider,
		Kind:      coreconversation.ItemOutput,
		CallID:    callID,
		Name:      name,
		ToolCalls: []coreconversation.ToolCallRef{call},
		Metadata:  metadata,
	}
	return item, repairRecord(RepairOrphanToolResult, provider, sourceIndex, call, "orphan_tool_result", "matching assistant tool call was missing from replay")
}

func ensureItemProvider(item coreconversation.Item, provider coreconversation.ProviderIdentity) coreconversation.Item {
	if item.Provider.Provider == "" {
		item.Provider = provider
	}
	return item
}

func repairRecord(kind ToolContinuityRepairKind, provider coreconversation.ProviderIdentity, sourceIndex int, call coreconversation.ToolCallRef, code, reason string) ToolContinuityRepairRecord {
	return ToolContinuityRepairRecord{
		Kind:            kind,
		Provider:        provider,
		SourceItemIndex: sourceIndex,
		CallID:          strings.TrimSpace(call.CallID),
		Name:            strings.TrimSpace(call.Name),
		Type:            strings.TrimSpace(call.Type),
		ReasonCode:      code,
		Reason:          reason,
	}
}

func fmtInt(value int) string {
	if value <= 0 {
		return "0"
	}
	const digits = "0123456789"
	var buf [20]byte
	i := len(buf)
	for value > 0 {
		i--
		buf[i] = digits[value%10]
		value /= 10
	}
	return string(buf[i:])
}
