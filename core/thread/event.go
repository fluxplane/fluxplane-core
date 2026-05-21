package thread

import (
	"fmt"
	"time"

	"github.com/fluxplane/engine/core/event"
)

const (
	EventThreadCreated    event.Name = "thread.created"
	EventMetadataUpdated  event.Name = "thread.metadata_updated"
	EventThreadArchived   event.Name = "thread.archived"
	EventThreadUnarchived event.Name = "thread.unarchived"

	EventBranchCreated   event.Name = "branch.created"
	EventBranchHeadMoved event.Name = "branch.head_moved"
)

// Source describes the actor or subsystem that changed a thread.
type Source struct {
	Kind      string `json:"kind,omitempty"`
	ID        string `json:"id,omitempty"`
	SessionID string `json:"session_id,omitempty"`
}

// ThreadCreated is emitted when a thread is created.
type ThreadCreated struct {
	ThreadID  ID                `json:"thread_id"`
	BranchID  BranchID          `json:"branch_id,omitempty"`
	Metadata  map[string]string `json:"metadata,omitempty"`
	CreatedAt time.Time         `json:"created_at,omitempty"`
	Source    Source            `json:"source,omitempty"`
}

func (ThreadCreated) EventName() event.Name { return EventThreadCreated }

// MetadataUpdated is emitted when thread metadata changes.
type MetadataUpdated struct {
	ThreadID  ID                `json:"thread_id"`
	Metadata  map[string]string `json:"metadata,omitempty"`
	UpdatedAt time.Time         `json:"updated_at,omitempty"`
	Source    Source            `json:"source,omitempty"`
}

func (MetadataUpdated) EventName() event.Name { return EventMetadataUpdated }

// ThreadArchived is emitted when a thread is archived.
type ThreadArchived struct {
	ThreadID ID        `json:"thread_id"`
	At       time.Time `json:"at,omitempty"`
	Source   Source    `json:"source,omitempty"`
}

func (ThreadArchived) EventName() event.Name { return EventThreadArchived }

// ThreadUnarchived is emitted when a thread is unarchived.
type ThreadUnarchived struct {
	ThreadID ID        `json:"thread_id"`
	At       time.Time `json:"at,omitempty"`
	Source   Source    `json:"source,omitempty"`
}

func (ThreadUnarchived) EventName() event.Name { return EventThreadUnarchived }

// BranchCreated is emitted when a branch is forked from another branch.
type BranchCreated struct {
	ThreadID     ID             `json:"thread_id"`
	FromBranchID BranchID       `json:"from_branch_id,omitempty"`
	ToBranchID   BranchID       `json:"to_branch_id"`
	ForkSequence event.Sequence `json:"fork_sequence,omitempty"`
	CreatedAt    time.Time      `json:"created_at,omitempty"`
	Source       Source         `json:"source,omitempty"`
}

func (BranchCreated) EventName() event.Name { return EventBranchCreated }

// BranchHeadMoved is emitted when a branch head is moved by a runtime.
type BranchHeadMoved struct {
	ThreadID ID        `json:"thread_id"`
	BranchID BranchID  `json:"branch_id"`
	NodeID   NodeID    `json:"node_id,omitempty"`
	At       time.Time `json:"at,omitempty"`
	Source   Source    `json:"source,omitempty"`
}

func (BranchHeadMoved) EventName() event.Name { return EventBranchHeadMoved }

// RegisterEvents registers thread event payloads with registry.
func RegisterEvents(registry *event.Registry) error {
	if registry == nil {
		return fmt.Errorf("thread: event registry is nil")
	}
	for _, sample := range []event.Event{
		ThreadCreated{},
		MetadataUpdated{},
		ThreadArchived{},
		ThreadUnarchived{},
		BranchCreated{},
		BranchHeadMoved{},
	} {
		if err := registry.Register(sample); err != nil {
			return err
		}
	}
	return nil
}
