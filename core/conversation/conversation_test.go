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

func TestContinuationHandleSupportsPreviousResponseID(t *testing.T) {
	handle := ContinuationHandle{
		Mode:       ContinuationPreviousResponseID,
		ResponseID: "resp_123",
	}
	if !handle.SupportsPreviousResponseID() {
		t.Fatalf("SupportsPreviousResponseID = false, want true")
	}
}
