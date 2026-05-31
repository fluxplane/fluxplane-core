package authstatus

import (
	"context"
	"testing"

	coreevidence "github.com/fluxplane/fluxplane-core/core/evidence"
	runtimeevidence "github.com/fluxplane/fluxplane-core/runtime/evidence"
)

func TestAssertionDeriverEmitsAuthenticatedOnlyWhenConnected(t *testing.T) {
	deriver := NewAssertionDeriver()
	assertions, err := deriver.Derive(context.Background(), runtimeevidence.AssertionDeriveRequest{
		Observations: []coreevidence.Observation{{
			ID:      "auth:gitlab:work",
			Kind:    ObservationKind,
			Content: Status{Plugin: "gitlab", Instance: "work", Status: StatusConnected, Connected: true, Method: "token"},
		}, {
			ID:      "auth:slack:slack",
			Kind:    ObservationKind,
			Content: Status{Plugin: "slack", Instance: "slack", Status: StatusNotConnected},
		}},
	})
	if err != nil {
		t.Fatalf("Derive: %v", err)
	}
	if len(assertions) != 1 {
		t.Fatalf("assertions = %#v", assertions)
	}
	assertion := assertions[0]
	if assertion.Kind != AssertionAuthenticated || assertion.Target != "gitlab" || assertion.Subject.ID != "gitlab/work" || assertion.Metadata["method"] != "token" {
		t.Fatalf("assertion = %#v", assertion)
	}
}
