package thread

import (
	"testing"

	"github.com/fluxplane/fluxplane-event"
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

func TestSnapshotEventsForMainBranch(t *testing.T) {
	snapshot := Snapshot{
		ID:       "thread-1",
		BranchID: MainBranch,
		Branches: map[BranchID]Branch{
			MainBranch: {ID: MainBranch},
		},
		Events: []Record{
			{ThreadID: "thread-1", BranchID: MainBranch, Sequence: 1},
			{ThreadID: "thread-1", BranchID: MainBranch, Sequence: 2},
			{ThreadID: "thread-1", BranchID: MainBranch, Sequence: 3},
		},
	}

	events, err := snapshot.EventsForBranch(MainBranch)
	if err != nil {
		t.Fatalf("EventsForBranch returned error: %v", err)
	}
	if len(events) != 3 {
		t.Fatalf("len(events) = %d, want 3", len(events))
	}
}

func TestSnapshotEventsForInvalidBranch(t *testing.T) {
	snapshot := Snapshot{
		ID:       "thread-1",
		BranchID: MainBranch,
		Branches: map[BranchID]Branch{
			MainBranch: {ID: MainBranch},
		},
	}

	_, err := snapshot.EventsForBranch("nonexistent")
	if err == nil {
		t.Fatal("EventsForBranch succeeded for nonexistent branch, want error")
	}
}

func TestSnapshotEventsWithNestedBranches(t *testing.T) {
	snapshot := Snapshot{
		ID:       "thread-1",
		BranchID: "level2",
		Branches: map[BranchID]Branch{
			MainBranch: {ID: MainBranch},
			"level1":   {ID: "level1", Parent: MainBranch, ForkSequence: 2},
			"level2":   {ID: "level2", Parent: "level1", ForkSequence: 4},
		},
		Events: []Record{
			{ThreadID: "thread-1", BranchID: MainBranch, Sequence: 1},
			{ThreadID: "thread-1", BranchID: MainBranch, Sequence: 2},
			{ThreadID: "thread-1", BranchID: "level1", Sequence: 3},
			{ThreadID: "thread-1", BranchID: "level1", Sequence: 4},
			{ThreadID: "thread-1", BranchID: "level2", Sequence: 5},
		},
	}

	events, err := snapshot.EventsForBranch("level2")
	if err != nil {
		t.Fatalf("EventsForBranch returned error: %v", err)
	}
	if len(events) != 5 {
		t.Fatalf("len(events) = %d, want 5", len(events))
	}
	want := []event.Sequence{1, 2, 3, 4, 5}
	for i, record := range events {
		if record.Sequence != want[i] {
			t.Fatalf("events[%d].Sequence = %d, want %d", i, record.Sequence, want[i])
		}
	}
}
