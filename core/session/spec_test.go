package session

import (
	"testing"

	"github.com/fluxplane/agentruntime/core/agent"
	"github.com/fluxplane/agentruntime/core/operation"
)

func TestSpecValidateAllowsDelegationPolicy(t *testing.T) {
	spec := Spec{
		Name: "coder",
		Agent: agent.Ref{
			Name: "dev-agent",
		},
		Delegation: DelegationPolicy{
			AllowedProfiles: []Ref{{Name: "worker"}},
			MaxParallel:     2,
			Operations:      []operation.Ref{{Name: "file_read"}},
		},
	}

	if err := spec.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestSpecValidateRejectsEmptyDelegationOperation(t *testing.T) {
	err := Spec{
		Name: "coder",
		Delegation: DelegationPolicy{
			Operations: []operation.Ref{{}},
		},
	}.Validate()
	if err == nil {
		t.Fatal("Validate error is nil, want empty delegation operation error")
	}
}

func TestSpecValidateRejectsEmptyName(t *testing.T) {
	err := Spec{}.Validate()
	if err == nil {
		t.Fatal("Validate error is nil, want empty name error")
	}
}

func TestSpecValidateRejectsEmptyDelegationProfile(t *testing.T) {
	err := Spec{
		Name: "coder",
		Delegation: DelegationPolicy{
			AllowedProfiles: []Ref{{}},
		},
	}.Validate()
	if err == nil {
		t.Fatal("Validate error is nil, want empty delegation profile error")
	}
}
