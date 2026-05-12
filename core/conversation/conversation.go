package conversation

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/fluxplane/agentruntime/core/event"
	"github.com/fluxplane/agentruntime/core/thread"
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
	if !compatibleField(c.Provider, requested.Provider) {
		return false
	}
	if !compatibleAPI(c.API, requested.API) {
		return false
	}
	if !compatibleAPI(c.Family, requested.Family) {
		return false
	}
	return compatibleField(c.Model, requested.Model)
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
	return h.Mode == ContinuationPreviousResponseID && strings.TrimSpace(h.ResponseID) != ""
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

// Item is one exact provider-visible transcript item.
type Item struct {
	Provider ProviderIdentity  `json:"provider,omitempty"`
	Kind     ItemKind          `json:"kind"`
	Role     string            `json:"role,omitempty"`
	ID       string            `json:"id,omitempty"`
	CallID   string            `json:"call_id,omitempty"`
	Name     string            `json:"name,omitempty"`
	Content  any               `json:"content,omitempty"`
	Native   json.RawMessage   `json:"native,omitempty"`
	Metadata map[string]string `json:"metadata,omitempty"`
}

// Validate checks that an item has enough information to preserve a transcript
// position. It intentionally does not validate provider-native payload schemas.
func (i Item) Validate() error {
	if i.Kind == "" {
		return fmt.Errorf("conversation: item kind is empty")
	}
	if len(i.Native) == 0 && i.Content == nil && i.ID == "" && i.CallID == "" {
		return fmt.Errorf("conversation: item has no content or native payload")
	}
	return nil
}

const (
	EventItemsAppended      event.Name = "conversation.items.appended"
	EventContinuationStored event.Name = "conversation.continuation.stored"
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

// RegisterEvents registers conversation transcript events.
func RegisterEvents(registry *event.Registry) error {
	if registry == nil {
		return fmt.Errorf("conversation: event registry is nil")
	}
	for _, sample := range []event.Event{
		ItemsAppended{},
		ContinuationStored{},
	} {
		if err := registry.Register(sample); err != nil {
			return err
		}
	}
	return nil
}
