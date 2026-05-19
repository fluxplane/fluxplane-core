package environment

import (
	"context"
	"testing"

	coreenvironment "github.com/fluxplane/agentruntime/core/environment"
)

func TestBaselineObserverReportsNonSecretLocalFacts(t *testing.T) {
	observer := BaselineObserver()
	spec := observer.Spec()
	if spec.Name != BaselineObserverName || spec.Phase != coreenvironment.PhaseTurn || !spec.Dynamic {
		t.Fatalf("spec = %#v, want turn-phase dynamic baseline observer", spec)
	}
	observations, err := observer.Observe(context.Background(), ObservationRequest{Phase: coreenvironment.PhaseTurn})
	if err != nil {
		t.Fatalf("Observe: %v", err)
	}
	byKind := map[string]coreenvironment.Observation{}
	for _, observation := range observations {
		byKind[observation.Kind] = observation
	}
	if byKind[ObservationSystemTime].ID != "system:time" {
		t.Fatalf("observations = %#v, missing system time", observations)
	}
	content, ok := byKind[ObservationSystemTime].Content.(map[string]any)
	if !ok || content["utc_time"] == "" || content["timezone"] == "" {
		t.Fatalf("time content = %#v, want utc_time and timezone", byKind[ObservationSystemTime].Content)
	}
	if byKind[ObservationSystemUser].Kind != "" {
		userContent, ok := byKind[ObservationSystemUser].Content.(map[string]any)
		if !ok || userContent["username"] == "" {
			t.Fatalf("user content = %#v, want username when user observation exists", byKind[ObservationSystemUser].Content)
		}
	}
}
