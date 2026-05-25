package evaluator

import (
	"testing"

	"github.com/fluxplane/fluxplane-core/core/channel"
	clientapi "github.com/fluxplane/fluxplane-core/orchestration/client"
)

// TestSummarizeEventNilContentDoesNotLeakNilString regresses an
// fmt.Sprint(nil) foot-gun: when an Outbound carries a Message with a nil
// Content (a valid "no content" state), the old code wrote "<nil>" into
// the Outbound field of the event summary. The expected behavior is to
// leave the Outbound text empty - the JSON output otherwise carries a
// literal "<nil>" string that downstream evaluators / dashboards must
// special-case.
func TestSummarizeEventNilContentDoesNotLeakNilString(t *testing.T) {
	ev := clientapi.Event{
		Kind:     clientapi.EventOutboundProduced,
		Outbound: &channel.Outbound{Message: &channel.Message{Content: nil}},
	}
	out := summarizeEvent(ev)
	if out.Outbound != "" {
		t.Fatalf("Outbound = %q, want empty (nil content must not produce \"<nil>\")", out.Outbound)
	}
}

func TestSummarizeEventPreservesStringContent(t *testing.T) {
	ev := clientapi.Event{
		Kind:     clientapi.EventOutboundProduced,
		Outbound: &channel.Outbound{Message: &channel.Message{Content: "hello"}},
	}
	out := summarizeEvent(ev)
	if out.Outbound != "hello" {
		t.Fatalf("Outbound = %q, want hello", out.Outbound)
	}
}
