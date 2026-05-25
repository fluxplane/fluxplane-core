package conversation

import "testing"

func TestProviderIdentityCompatibleResponsesFamily(t *testing.T) {
	recorded := ProviderIdentity{Provider: "openai", API: "openai.responses", Model: "gpt-test"}
	requested := ProviderIdentity{Provider: "openai", API: "responses", Model: "gpt-test"}
	if !recorded.Compatible(requested) {
		t.Fatalf("Compatible = false, want true")
	}
}

func TestProviderIdentityRejectsModelMismatch(t *testing.T) {
	recorded := ProviderIdentity{Provider: "openai", API: "openai.responses", Model: "gpt-a"}
	requested := ProviderIdentity{Provider: "openai", API: "openai.responses", Model: "gpt-b"}
	if recorded.Compatible(requested) {
		t.Fatalf("Compatible = true, want false")
	}
}

func TestProviderIdentityCompatibleProviderPrefixedModel(t *testing.T) {
	recorded := ProviderIdentity{Provider: "openrouter", API: "openrouter.responses", Model: "openrouter/moonshotai/kimi-k2"}
	requested := ProviderIdentity{Provider: "openrouter", API: "openrouter.responses", Model: "moonshotai/kimi-k2"}
	if !recorded.Compatible(requested) {
		t.Fatalf("Compatible = false, want true")
	}
}

func TestContinuationHandleSupportsPreviousResponseID(t *testing.T) {
	handle := ContinuationHandle{
		Mode:       ContinuationPreviousResponseID,
		ResponseID: "resp_123",
	}
	if !handle.SupportsPreviousResponseID() {
		t.Fatalf("SupportsPreviousResponseID = false, want true")
	}
}

func TestContinuationHandleRejectsMissingResponseID(t *testing.T) {
	handle := ContinuationHandle{
		Mode:       ContinuationPreviousResponseID,
		ResponseID: "",
	}
	if handle.SupportsPreviousResponseID() {
		t.Fatalf("SupportsPreviousResponseID = true, want false with empty response ID")
	}
}

func TestContinuationHandleRejectsWrongMode(t *testing.T) {
	handle := ContinuationHandle{
		Mode:       ContinuationFullReplay,
		ResponseID: "resp_123",
	}
	if handle.SupportsPreviousResponseID() {
		t.Fatalf("SupportsPreviousResponseID = true, want false with wrong mode")
	}
}

func TestContinuationHandleRejectsWebSocketTransport(t *testing.T) {
	handle := ContinuationHandle{
		Mode:       ContinuationPreviousResponseID,
		Transport:  TransportWebSocket,
		ResponseID: "resp_123",
	}
	if handle.SupportsPreviousResponseID() {
		t.Fatalf("SupportsPreviousResponseID = true, want false with websocket transport")
	}
}

func TestTranscriptEmpty(t *testing.T) {
	tests := []struct {
		name       string
		transcript Transcript
		expected   bool
	}{
		{
			name:       "empty transcript",
			transcript: Transcript{},
			expected:   true,
		},
		{
			name: "with items",
			transcript: Transcript{
				Items: []Item{{Kind: ItemInput}},
			},
			expected: false,
		},
		{
			name: "with continuation",
			transcript: Transcript{
				Continuation: &ContinuationHandle{Mode: ContinuationFullReplay},
			},
			expected: false,
		},
	}
	for _, tt := range tests {
		got := tt.transcript.Empty()
		if got != tt.expected {
			t.Errorf("%s: Empty = %v, want %v", tt.name, got, tt.expected)
		}
	}
}

func TestItemValidateWithContent(t *testing.T) {
	item := Item{
		Kind:    ItemInput,
		Content: "test content",
	}
	if err := item.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestItemValidateWithNative(t *testing.T) {
	item := Item{
		Kind:   ItemOutput,
		Native: []byte(`{"key":"value"}`),
	}
	if err := item.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestItemValidateWithID(t *testing.T) {
	item := Item{
		Kind: ItemToolResult,
		ID:   "item-123",
	}
	if err := item.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestItemValidateWithCallID(t *testing.T) {
	item := Item{
		Kind:   ItemReasoning,
		CallID: "call-456",
	}
	if err := item.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestItemValidateRejectsEmptyKind(t *testing.T) {
	item := Item{
		Kind:    "",
		Content: "test",
	}
	if err := item.Validate(); err == nil {
		t.Fatal("Validate succeeded, want error for empty kind")
	}
}

func TestItemValidateRejectsEmptyPayload(t *testing.T) {
	item := Item{
		Kind: ItemInput,
	}
	if err := item.Validate(); err == nil {
		t.Fatal("Validate succeeded, want error for empty payload")
	}
}

func TestProviderIdentityWildcardProvider(t *testing.T) {
	recorded := ProviderIdentity{Provider: "openai"}
	requested := ProviderIdentity{Provider: ""}
	if !recorded.Compatible(requested) {
		t.Fatalf("Compatible = false, want true (wildcard provider)")
	}
}

func TestProviderIdentityWildcardAPI(t *testing.T) {
	recorded := ProviderIdentity{API: "openai.responses"}
	requested := ProviderIdentity{API: ""}
	if !recorded.Compatible(requested) {
		t.Fatalf("Compatible = false, want true (wildcard api)")
	}
}

func TestProviderIdentityAPIMismatch(t *testing.T) {
	recorded := ProviderIdentity{API: "openai.responses"}
	requested := ProviderIdentity{API: "anthropic.messages"}
	if recorded.Compatible(requested) {
		t.Fatalf("Compatible = true, want false (different api families)")
	}
}

func TestItemsAppendedEventName(t *testing.T) {
	event := ItemsAppended{
		Items: []Item{{Kind: ItemInput}},
	}
	if got := event.EventName(); got != EventItemsAppended {
		t.Errorf("EventName = %q, want %q", got, EventItemsAppended)
	}
}

func TestContinuationStoredEventName(t *testing.T) {
	event := ContinuationStored{
		Handle: ContinuationHandle{Mode: ContinuationFullReplay},
	}
	if got := event.EventName(); got != EventContinuationStored {
		t.Errorf("EventName = %q, want %q", got, EventContinuationStored)
	}
}
