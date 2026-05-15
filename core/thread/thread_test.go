package thread

import (
	"testing"

	"github.com/fluxplane/agentruntime/core/event"
)

func TestThreadEventNames(t *testing.T) {
	checks := []struct {
		name string
		got  event.Name
		want event.Name
	}{
		{"ThreadCreated", ThreadCreated{}.EventName(), EventThreadCreated},
		{"MetadataUpdated", MetadataUpdated{}.EventName(), EventMetadataUpdated},
		{"ThreadArchived", ThreadArchived{}.EventName(), EventThreadArchived},
		{"ThreadUnarchived", ThreadUnarchived{}.EventName(), EventThreadUnarchived},
		{"BranchCreated", BranchCreated{}.EventName(), EventBranchCreated},
		{"BranchHeadMoved", BranchHeadMoved{}.EventName(), EventBranchHeadMoved},
	}
	for _, tc := range checks {
		if tc.got != tc.want {
			t.Errorf("%s EventName = %q, want %q", tc.name, tc.got, tc.want)
		}
	}
}

func TestRegisterEventsNilRegistry(t *testing.T) {
	if err := RegisterEvents(nil); err == nil {
		t.Fatal("RegisterEvents(nil) should return error")
	}
}

func TestRegisterEventsSucceeds(t *testing.T) {
	r := event.NewRegistry()
	if err := RegisterEvents(r); err != nil {
		t.Fatalf("RegisterEvents: %v", err)
	}
}
