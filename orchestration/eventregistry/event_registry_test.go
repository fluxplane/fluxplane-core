package eventregistry

import (
	"encoding/json"
	"testing"

	operationruntime "github.com/fluxplane/fluxplane-core/runtime/operation"
	"github.com/fluxplane/fluxplane-policy"
)

func TestRegistryIncludesApprovalEvents(t *testing.T) {
	registry, err := New(Config{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	raw, err := json.Marshal(operationruntime.ApprovalDenied{
		Resource: policy.ResourceRef{Kind: policy.ResourceOperation, Name: "shell_exec"},
		Action:   policy.ActionApprovalGrant,
		Error:    "approval_denied",
	})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	decoded, err := registry.Decode(operationruntime.EventApprovalDeniedName, raw)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	approval, ok := decoded.(operationruntime.ApprovalDenied)
	if !ok {
		t.Fatalf("decoded = %T, want ApprovalDenied", decoded)
	}
	if approval.Resource.Name != "shell_exec" || approval.Error != "approval_denied" {
		t.Fatalf("decoded = %#v", approval)
	}
}
