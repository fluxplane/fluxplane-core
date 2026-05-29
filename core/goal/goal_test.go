package goal

import (
	"strings"
	"testing"

	"github.com/fluxplane/fluxplane-event"
)

func TestRegisterEvents(t *testing.T) {
	registry := event.NewRegistry()
	if err := RegisterEvents(registry); err != nil {
		t.Fatalf("RegisterEvents: %v", err)
	}
	for _, name := range []event.Name{
		EventSetName,
		EventAcceptanceCriteriaGeneratedName,
		EventPausedName,
		EventResumedName,
		EventClearedName,
		EventReviewRequestedName,
		EventReachedName,
		EventRejectedName,
	} {
		if _, ok, err := registry.TryDecode(name, []byte(`{}`)); err != nil || !ok {
			t.Fatalf("registered events missing %q", name)
		}
	}
}

func TestValidateTextRequiresText(t *testing.T) {
	err := ValidateText("  ")
	if err == nil || !strings.Contains(err.Error(), "required") {
		t.Fatalf("ValidateText error = %v, want required error", err)
	}
}

func TestStateContinuationAndVisibility(t *testing.T) {
	cases := []struct {
		name        string
		state       State
		continuable bool
		visible     bool
	}{
		{"empty", State{}, false, false},
		{"active", State{ID: "goal_1", Status: StatusActive}, true, true},
		{"rejected", State{ID: "goal_1", Status: StatusRejected}, true, true},
		{"paused", State{ID: "goal_1", Status: StatusPaused}, false, true},
		{"reached", State{ID: "goal_1", Status: StatusReached}, false, true},
		{"cleared", State{ID: "goal_1", Status: StatusCleared}, false, false},
		{"archived", State{ID: "goal_1", Status: StatusArchived}, false, false},
	}
	for _, tc := range cases {
		if got := tc.state.ActiveForContinuation(); got != tc.continuable {
			t.Fatalf("%s ActiveForContinuation = %v, want %v", tc.name, got, tc.continuable)
		}
		if got := tc.state.Visible(); got != tc.visible {
			t.Fatalf("%s Visible = %v, want %v", tc.name, got, tc.visible)
		}
	}
}

func TestNormalizeStatus(t *testing.T) {
	for _, status := range []Status{StatusActive, StatusPaused, StatusRejected, StatusReached, StatusCleared, StatusArchived} {
		if got := NormalizeStatus(status); got != status {
			t.Fatalf("NormalizeStatus(%q) = %q, want original", status, got)
		}
	}
	if got := NormalizeStatus("unknown"); got != "" {
		t.Fatalf("NormalizeStatus unknown = %q, want empty", got)
	}
}

func TestValidateTextAcceptsNonBlank(t *testing.T) {
	if err := ValidateText(" ship it "); err != nil {
		t.Fatalf("ValidateText nonblank: %v", err)
	}
}
