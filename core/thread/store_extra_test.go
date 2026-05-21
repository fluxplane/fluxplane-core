package thread

import (
	"testing"

	"github.com/fluxplane/engine/core/event"
)

// makeRecord builds a minimal thread Record for testing EventsForBranch.
func makeRecord(branch BranchID, seq event.Sequence) Record {
	return Record{
		BranchID: branch,
		Sequence: seq,
		Event:    event.Record{Name: "test.event"},
	}
}

func TestEventsForBranchDefaultsToMain(t *testing.T) {
	// No branches map → treated as main branch.
	snap := Snapshot{
		BranchID: MainBranch,
		Events: []Record{
			makeRecord(MainBranch, 1),
			makeRecord(MainBranch, 2),
		},
	}
	records, err := snap.EventsForBranch("")
	if err != nil {
		t.Fatalf("EventsForBranch: %v", err)
	}
	if len(records) != 2 {
		t.Fatalf("EventsForBranch: got %d records, want 2", len(records))
	}
}

func TestEventsForBranchMissingBranch(t *testing.T) {
	// No branches map but a non-main branch ID requested → ErrNotFound.
	snap := Snapshot{}
	_, err := snap.EventsForBranch("nonexistent")
	if err == nil {
		t.Fatal("EventsForBranch(nonexistent): want error")
	}
}

func TestEventsForBranchSingleBranch(t *testing.T) {
	snap := Snapshot{
		BranchID: MainBranch,
		Branches: map[BranchID]Branch{
			MainBranch: {ID: MainBranch},
		},
		Events: []Record{
			makeRecord(MainBranch, 1),
			makeRecord(MainBranch, 2),
			makeRecord(MainBranch, 3),
		},
	}
	records, err := snap.EventsForBranch(MainBranch)
	if err != nil {
		t.Fatalf("EventsForBranch: %v", err)
	}
	if len(records) != 3 {
		t.Fatalf("EventsForBranch: got %d, want 3", len(records))
	}
}

func TestEventsForBranchWithFork(t *testing.T) {
	// main:  seq 1,2,3 (before fork at 2)
	// fork:  seq 3,4 (after fork at 2)
	const forkBranch BranchID = "fork"
	snap := Snapshot{
		BranchID: MainBranch,
		Branches: map[BranchID]Branch{
			MainBranch: {ID: MainBranch},
			forkBranch: {ID: forkBranch, Parent: MainBranch, ForkSequence: 2},
		},
		Events: []Record{
			makeRecord(MainBranch, 1),
			makeRecord(MainBranch, 2),
			makeRecord(MainBranch, 3), // beyond fork on main — excluded from fork view
			makeRecord(forkBranch, 3),
			makeRecord(forkBranch, 4),
		},
	}

	// Fork branch should see main[1,2] + fork[3,4].
	records, err := snap.EventsForBranch(forkBranch)
	if err != nil {
		t.Fatalf("EventsForBranch(fork): %v", err)
	}
	if len(records) != 4 {
		t.Fatalf("EventsForBranch(fork): got %d records, want 4", len(records))
	}
}

func TestEventsForBranchUnknownBranch(t *testing.T) {
	snap := Snapshot{
		Branches: map[BranchID]Branch{
			MainBranch: {ID: MainBranch},
		},
	}
	_, err := snap.EventsForBranch("ghost")
	if err == nil {
		t.Fatal("EventsForBranch(ghost): want ErrNotFound")
	}
}

func TestBranchWindowsNoBranchesMainOK(t *testing.T) {
	snap := Snapshot{}
	windows, err := snap.branchWindows(MainBranch)
	if err != nil {
		t.Fatalf("branchWindows(main): %v", err)
	}
	if _, ok := windows[MainBranch]; !ok {
		t.Fatal("branchWindows: want MainBranch window present")
	}
}

func TestBranchWindowsNoBranchesNonMainFails(t *testing.T) {
	snap := Snapshot{}
	_, err := snap.branchWindows("other")
	if err == nil {
		t.Fatal("branchWindows(other) with no branches: want error")
	}
}

func TestEventsForBranchUsesSnapshotBranchID(t *testing.T) {
	// When called with "", it falls back to s.BranchID, then MainBranch.
	snap := Snapshot{
		BranchID: MainBranch,
		Branches: map[BranchID]Branch{
			MainBranch: {ID: MainBranch},
		},
		Events: []Record{makeRecord(MainBranch, 1)},
	}
	records, err := snap.EventsForBranch("")
	if err != nil {
		t.Fatalf("EventsForBranch(''): %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("EventsForBranch(''): got %d, want 1", len(records))
	}
}
