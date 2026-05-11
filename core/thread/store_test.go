package thread

import (
	"testing"

	"github.com/fluxplane/agentruntime/core/event"
)

func TestSnapshotEventsForBranch(t *testing.T) {
	snapshot := Snapshot{
		ID:       "thread-1",
		BranchID: "alt",
		Branches: map[BranchID]Branch{
			MainBranch: {ID: MainBranch},
			"alt":      {ID: "alt", Parent: MainBranch, ForkSequence: 2},
		},
		Events: []Record{
			{ThreadID: "thread-1", BranchID: MainBranch, Sequence: 1},
			{ThreadID: "thread-1", BranchID: MainBranch, Sequence: 2},
			{ThreadID: "thread-1", BranchID: MainBranch, Sequence: 3},
			{ThreadID: "thread-1", BranchID: "alt", Sequence: 4},
		},
	}

	events, err := snapshot.EventsForBranch("alt")
	if err != nil {
		t.Fatalf("EventsForBranch returned error: %v", err)
	}
	if len(events) != 3 {
		t.Fatalf("len(events) = %d, want 3", len(events))
	}
	want := []event.Sequence{1, 2, 4}
	for i, record := range events {
		if record.Sequence != want[i] {
			t.Fatalf("events[%d].Sequence = %d, want %d", i, record.Sequence, want[i])
		}
	}
}
