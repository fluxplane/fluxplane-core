package conversation

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/fluxplane/fluxplane-core/core/event"
	"github.com/fluxplane/fluxplane-core/core/thread"
)

// ProviderIdentity identifies a provider transcript shape.
type ProviderIdentity struct {
	Provider string `json:"provider,omitempty"`
	API      string `json:"api,omitempty"`
	Family   string `json:"family,omitempty"`
	Model    string `json:"model,omitempty"`
}

// Compatible reports whether continuation recorded under c can be used for
// requested. Empty fields act as wildcards.
func (c ProviderIdentity) Compatible(requested ProviderIdentity) bool {
	recordedProvider, recordedModel := NormalizeProviderModel(c.Provider, c.Model)
	requestedProvider, requestedModel := NormalizeProviderModel(requested.Provider, requested.Model)
	if recordedProvider == "" && requestedProvider != "" {
		_, recordedModel = NormalizeProviderModel(requestedProvider, recordedModel)
	}
	if requestedProvider == "" && recordedProvider != "" {
		_, requestedModel = NormalizeProviderModel(recordedProvider, requestedModel)
	}
	if !compatibleField(recordedProvider, requestedProvider) {
		return false
	}
	if !compatibleAPI(c.API, requested.API) {
		return false
	}
	if !compatibleAPI(c.Family, requested.Family) {
		return false
	}
	return compatibleField(recordedModel, requestedModel)
}

// NormalizeProviderModel strips a leading provider prefix from a model name
// when the provider is already represented separately.
func NormalizeProviderModel(provider, model string) (string, string) {
	provider = strings.TrimSpace(provider)
	model = strings.TrimSpace(model)
	before, after, ok := strings.Cut(model, "/")
	if ok && before == provider && after != "" {
		return provider, after
	}
	return provider, model
}

func compatibleField(a, b string) bool {
	a = strings.TrimSpace(a)
	b = strings.TrimSpace(b)
	return a == "" || b == "" || a == b
}

func compatibleAPI(a, b string) bool {
	a = strings.ToLower(strings.TrimSpace(a))
	b = strings.ToLower(strings.TrimSpace(b))
	if a == "" || b == "" || a == b {
		return true
	}
	return isResponsesAPI(a) && isResponsesAPI(b)
}

func isResponsesAPI(api string) bool {
	return api == "responses" || strings.HasSuffix(api, ".responses") || strings.Contains(api, "responses")
}

// Transport identifies a provider transport.
type Transport string

const (
	TransportHTTPSSE   Transport = "http_sse"
	TransportWebSocket Transport = "websocket"
)

// ContinuationMode describes how a provider can continue from a prior response.
type ContinuationMode string

const (
	ContinuationFullReplay         ContinuationMode = "full_replay"
	ContinuationPreviousResponseID ContinuationMode = "previous_response_id"
	ContinuationConversationID     ContinuationMode = "conversation_id"
)

// ContinuationHandle is the durable provider-native head pointer for a branch.
type ContinuationHandle struct {
	Provider    ProviderIdentity           `json:"provider,omitempty"`
	Mode        ContinuationMode           `json:"mode,omitempty"`
	Transport   Transport                  `json:"transport,omitempty"`
	ResponseID  string                     `json:"response_id,omitempty"`
	SessionID   string                     `json:"session_id,omitempty"`
	BranchID    thread.BranchID            `json:"branch_id,omitempty"`
	RequestHash string                     `json:"request_hash,omitempty"`
	PrefixHash  string                     `json:"prefix_hash,omitempty"`
	Native      map[string]json.RawMessage `json:"native,omitempty"`
}

// ProjectionMode records the projection strategy used for provider-visible
// transcript input.
type ProjectionMode string

const (
	ProjectionFullReplay         ProjectionMode = "full_replay"
	ProjectionNativeContinuation ProjectionMode = "native_continuation"
)

// Transcript is a provider-visible request slice plus optional continuation
// handle. It is already projected; adapters should encode it rather than
// deriving model history from semantic session summaries.
type Transcript struct {
	Provider     ProviderIdentity    `json:"provider,omitempty"`
	Items        []Item              `json:"items,omitempty"`
	NewItems     []Item              `json:"new_items,omitempty"`
	Continuation *ContinuationHandle `json:"continuation,omitempty"`
	Mode         ProjectionMode      `json:"mode,omitempty"`
}

// Empty reports whether the transcript has no provider-visible input or
// continuation.
func (t Transcript) Empty() bool {
	return len(t.Items) == 0 && t.Continuation == nil
}

// SupportsPreviousResponseID reports whether the handle can be used as an
// OpenAI Responses-style previous_response_id continuation.
func (h ContinuationHandle) SupportsPreviousResponseID() bool {
	return h.Mode == ContinuationPreviousResponseID && h.Transport != TransportWebSocket && strings.TrimSpace(h.ResponseID) != ""
}

// ItemKind classifies provider transcript items.
type ItemKind string

const (
	ItemInput      ItemKind = "input"
	ItemOutput     ItemKind = "output"
	ItemToolResult ItemKind = "tool_result"
	ItemReasoning  ItemKind = "reasoning"
	ItemNative     ItemKind = "native"
)

// ToolCallRef is the provider-normalized identity of a tool call emitted by an
// assistant transcript item.
type ToolCallRef struct {
	CallID string `json:"call_id"`
	Name   string `json:"name,omitempty"`
	Type   string `json:"type,omitempty"`
	Input  any    `json:"input,omitempty"`
}

// Item is one exact provider-visible transcript item.
type Item struct {
	Provider  ProviderIdentity  `json:"provider,omitempty"`
	Kind      ItemKind          `json:"kind"`
	Role      string            `json:"role,omitempty"`
	Phase     string            `json:"phase,omitempty"`
	ID        string            `json:"id,omitempty"`
	CallID    string            `json:"call_id,omitempty"`
	Name      string            `json:"name,omitempty"`
	ToolCalls []ToolCallRef     `json:"tool_calls,omitempty"`
	Content   any               `json:"content,omitempty"`
	Native    json.RawMessage   `json:"native,omitempty"`
	Metadata  map[string]string `json:"metadata,omitempty"`
}

// ToolCallRefs returns normalized assistant tool calls carried by the item.
func (i Item) ToolCallRefs() []ToolCallRef {
	if i.Kind != ItemOutput {
		return nil
	}
	if len(i.ToolCalls) > 0 {
		out := make([]ToolCallRef, 0, len(i.ToolCalls))
		for _, call := range i.ToolCalls {
			call.CallID = strings.TrimSpace(call.CallID)
			if call.Name == "" {
				call.Name = i.Name
			}
			if call.Type == "" && i.Metadata != nil {
				call.Type = strings.TrimSpace(i.Metadata["provider_call_type"])
			}
			out = append(out, call)
		}
		return out
	}
	callID := strings.TrimSpace(i.CallID)
	if callID == "" {
		return nil
	}
	ref := ToolCallRef{CallID: callID, Name: i.Name}
	if i.Metadata != nil {
		ref.Type = strings.TrimSpace(i.Metadata["provider_call_type"])
	}
	return []ToolCallRef{ref}
}

// Validate checks that an item has enough information to preserve a transcript
// position. It intentionally does not validate provider-native payload schemas.
func (i Item) Validate() error {
	if i.Kind == "" {
		return fmt.Errorf("conversation: item kind is empty")
	}
	if len(i.Native) == 0 && i.Content == nil && i.ID == "" && i.CallID == "" && len(i.ToolCalls) == 0 {
		return fmt.Errorf("conversation: item has no content or native payload")
	}
	return nil
}

const (
	EventItemsAppended      event.Name = "conversation.items.appended"
	EventContinuationStored event.Name = "conversation.continuation.stored"
	EventCompactionStored   event.Name = "conversation.compaction.stored"
)

// ItemsAppended records provider-visible transcript items in the exact order
// they were sent or received.
type ItemsAppended struct {
	TurnID   string           `json:"turn_id,omitempty"`
	Provider ProviderIdentity `json:"provider,omitempty"`
	Items    []Item           `json:"items,omitempty"`
}

func (ItemsAppended) EventName() event.Name { return EventItemsAppended }

// ContinuationStored records a provider continuation handle available at the
// current branch head.
type ContinuationStored struct {
	TurnID string             `json:"turn_id,omitempty"`
	Handle ContinuationHandle `json:"handle"`
}

func (ContinuationStored) EventName() event.Name { return EventContinuationStored }

// CompactionStats describes a deterministic transcript compaction pass.
type CompactionStats struct {
	OriginalItems     int  `json:"original_items,omitempty"`
	CompactedItems    int  `json:"compacted_items,omitempty"`
	OriginalTokens    int  `json:"original_tokens,omitempty"`
	CompactedTokens   int  `json:"compacted_tokens,omitempty"`
	OmittedItems      int  `json:"omitted_items,omitempty"`
	SummarizedItems   int  `json:"summarized_items,omitempty"`
	Compacted         bool `json:"compacted,omitempty"`
	CheckpointPersist bool `json:"checkpoint_persist,omitempty"`
}

// CompactionStored records a provider-visible transcript checkpoint. Projection
// replays compatible later items from this compacted prefix.
type CompactionStored struct {
	TurnID   string           `json:"turn_id,omitempty"`
	Provider ProviderIdentity `json:"provider,omitempty"`
	Items    []Item           `json:"items,omitempty"`
	Stats    CompactionStats  `json:"stats,omitempty"`
}

func (CompactionStored) EventName() event.Name { return EventCompactionStored }

// RegisterEvents registers conversation transcript events.
func RegisterEvents(registry *event.Registry) error {
	if registry == nil {
		return fmt.Errorf("conversation: event registry is nil")
	}
	for _, sample := range []event.Event{
		ItemsAppended{},
		ContinuationStored{},
		CompactionStored{},
	} {
		if err := registry.Register(sample); err != nil {
			return err
		}
	}
	return nil
}
