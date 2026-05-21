package thread

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/fluxplane/engine/core/event"
)

var (
	// ErrNotFound is returned when a requested thread or branch does not exist.
	ErrNotFound = errors.New("thread: not found")

	// ErrAlreadyExists is returned when creating a duplicate thread or branch.
	ErrAlreadyExists = errors.New("thread: already exists")
)

// CreateParams describes a thread creation request.
type CreateParams struct {
	ID       ID                `json:"id,omitempty"`
	BranchID BranchID          `json:"branch_id,omitempty"`
	Metadata map[string]string `json:"metadata,omitempty"`
	Source   Source            `json:"source,omitempty"`
	Now      time.Time         `json:"now,omitempty"`
}

// ForkParams describes a branch creation request.
type ForkParams struct {
	ID           ID        `json:"id"`
	FromBranchID BranchID  `json:"from_branch_id,omitempty"`
	ToBranchID   BranchID  `json:"to_branch_id,omitempty"`
	Source       Source    `json:"source,omitempty"`
	Now          time.Time `json:"now,omitempty"`
}

// ReadParams describes a thread read request.
type ReadParams struct {
	ID ID `json:"id"`
}

// ListParams describes a thread list request.
type ListParams struct {
	IncludeArchived bool `json:"include_archived,omitempty"`
	Limit           int  `json:"limit,omitempty"`
}

// Page is a page of stored thread snapshots.
type Page struct {
	Threads []Snapshot `json:"threads,omitempty"`
}

// Branch describes branch ancestry and the fork point in thread sequence
// coordinates.
type Branch struct {
	ID           BranchID       `json:"id"`
	Parent       BranchID       `json:"parent,omitempty"`
	ForkSequence event.Sequence `json:"fork_sequence,omitempty"`
	CreatedAt    time.Time      `json:"created_at,omitempty"`
}

// AppendRecord is one event to append with thread node coordinates.
type AppendRecord struct {
	NodeID       NodeID       `json:"node_id,omitempty"`
	ParentNodeID NodeID       `json:"parent_node_id,omitempty"`
	Event        event.Record `json:"event"`
}

// Record is a thread-positioned event record.
type Record struct {
	ThreadID     ID             `json:"thread_id"`
	BranchID     BranchID       `json:"branch_id"`
	NodeID       NodeID         `json:"node_id,omitempty"`
	ParentNodeID NodeID         `json:"parent_node_id,omitempty"`
	Sequence     event.Sequence `json:"sequence"`
	Event        event.Record   `json:"event"`
}

// Snapshot is a durable view of one thread.
type Snapshot struct {
	ID        ID                  `json:"id"`
	BranchID  BranchID            `json:"branch_id,omitempty"`
	Branches  map[BranchID]Branch `json:"branches,omitempty"`
	Metadata  map[string]string   `json:"metadata,omitempty"`
	Archived  bool                `json:"archived,omitempty"`
	CreatedAt time.Time           `json:"created_at,omitempty"`
	UpdatedAt time.Time           `json:"updated_at,omitempty"`
	Events    []Record            `json:"events,omitempty"`
}

// Store is the core thread persistence port.
//
// Unlike the old live thread API, this interface does not own flush, shutdown,
// or buffering lifecycle. Runtime may build live sessions on top of it.
type Store interface {
	Create(context.Context, CreateParams) (Snapshot, error)
	Append(context.Context, Ref, ...AppendRecord) ([]Record, error)
	Fork(context.Context, ForkParams) (Snapshot, error)
	Read(context.Context, ReadParams) (Snapshot, error)
	List(context.Context, ListParams) (Page, error)
	Archive(context.Context, ID) error
	Unarchive(context.Context, ID) error
}

// EventsForBranch returns the visible event history for branchID, including
// inherited parent branch windows.
func (s Snapshot) EventsForBranch(branchID BranchID) ([]Record, error) {
	if branchID == "" {
		branchID = s.BranchID
	}
	if branchID == "" {
		branchID = MainBranch
	}
	windows, err := s.branchWindows(branchID)
	if err != nil {
		return nil, err
	}
	var out []Record
	for _, record := range s.Events {
		window, ok := windows[record.BranchID]
		if !ok {
			continue
		}
		if record.Sequence <= window.after {
			continue
		}
		if window.until > 0 && record.Sequence > window.until {
			continue
		}
		out = append(out, record)
	}
	return out, nil
}

type branchWindow struct {
	after event.Sequence
	until event.Sequence
}

func (s Snapshot) branchWindows(branchID BranchID) (map[BranchID]branchWindow, error) {
	if len(s.Branches) == 0 {
		if branchID == MainBranch || branchID == "" {
			return map[BranchID]branchWindow{MainBranch: {}}, nil
		}
		return nil, fmt.Errorf("%w: branch %q", ErrNotFound, branchID)
	}
	var reversed []Branch
	for current := branchID; current != ""; {
		branch, ok := s.Branches[current]
		if !ok {
			return nil, fmt.Errorf("%w: branch %q", ErrNotFound, current)
		}
		reversed = append(reversed, branch)
		current = branch.Parent
	}
	path := make([]Branch, len(reversed))
	for i := range reversed {
		path[len(reversed)-1-i] = reversed[i]
	}
	windows := make(map[BranchID]branchWindow, len(path))
	for i, branch := range path {
		window := branchWindow{after: branch.ForkSequence}
		if i+1 < len(path) {
			window.until = path[i+1].ForkSequence
		}
		windows[branch.ID] = window
	}
	return windows, nil
}
