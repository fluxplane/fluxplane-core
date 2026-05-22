package goal

import (
	"strings"
	"testing"

	"github.com/fluxplane/engine/core/event"
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
