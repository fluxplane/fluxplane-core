package terminal

import (
	"bytes"
	"strings"
	"testing"
)

func TestHandleUIReasoningCommandShowsAndSetsMode(t *testing.T) {
	var out bytes.Buffer
	state := UIState{}

	handled, err := HandleUICommand("/ui:reasoning", &state, &out)
	if err != nil {
		t.Fatalf("HandleUICommand: %v", err)
	}
	if !handled {
		t.Fatal("HandleUICommand handled = false, want true")
	}
	if !strings.Contains(out.String(), "ui: reasoning off") {
		t.Fatalf("out = %q, want off status", out.String())
	}

	out.Reset()
	handled, err = HandleUICommand("/ui:reasoning raw", &state, &out)
	if err != nil {
		t.Fatalf("HandleUICommand raw: %v", err)
	}
	if !handled || state.Reasoning != ReasoningDisplayRaw {
		t.Fatalf("handled = %v state = %#v, want raw", handled, state)
	}
	if !strings.Contains(out.String(), "ui: reasoning raw") {
		t.Fatalf("out = %q, want raw status", out.String())
	}
}

func TestHandleUIReasoningCommandRejectsInvalidMode(t *testing.T) {
	handled, err := HandleUICommand("/ui:reasoning loud", &UIState{}, &bytes.Buffer{})
	if !handled {
		t.Fatal("HandleUICommand handled = false, want true")
	}
	if err == nil || !strings.Contains(err.Error(), "want off|on|raw") {
		t.Fatalf("HandleUICommand error = %v, want invalid mode", err)
	}
}

func TestHandleUICommandIgnoresOtherInput(t *testing.T) {
	handled, err := HandleUICommand("/context", &UIState{}, &bytes.Buffer{})
	if err != nil {
		t.Fatalf("HandleUICommand: %v", err)
	}
	if handled {
		t.Fatal("HandleUICommand handled = true, want false")
	}
}
