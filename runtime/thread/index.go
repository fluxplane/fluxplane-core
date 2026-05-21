package thread

import (
	"context"
	"sync"
	"time"

	"github.com/fluxplane/engine/core/event"
	corethread "github.com/fluxplane/engine/core/thread"
)

// IndexEntry is the projected listing state for one thread.
type IndexEntry struct {
	ID        corethread.ID       `json:"id"`
	BranchID  corethread.BranchID `json:"branch_id,omitempty"`
	Metadata  map[string]string   `json:"metadata,omitempty"`
	Archived  bool                `json:"archived,omitempty"`
	CreatedAt time.Time           `json:"created_at,omitempty"`
	UpdatedAt time.Time           `json:"updated_at,omitempty"`
}

// ThreadIndex is an in-memory read model for the thread index stream.
type ThreadIndex struct {
	mu      sync.RWMutex
	entries map[corethread.ID]IndexEntry
	order   []corethread.ID
}

// NewThreadIndex returns an empty thread index read model.
func NewThreadIndex() *ThreadIndex {
	return &ThreadIndex{entries: map[corethread.ID]IndexEntry{}}
}

// Project applies stored thread-index records to the read model.
func (i *ThreadIndex) Project(ctx context.Context, records []event.StoredRecord) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.entries == nil {
		i.entries = map[corethread.ID]IndexEntry{}
	}
	for _, stored := range records {
		id := corethread.ID(stored.Record.Scope.ThreadID)
		if id == "" {
			continue
		}
		entry, exists := i.entries[id]
		if !exists {
			entry = IndexEntry{ID: id}
			i.order = append(i.order, id)
		}
		switch payload := stored.Record.Payload.(type) {
		case corethread.ThreadCreated:
			applyIndexThreadCreated(&entry, payload)
		case *corethread.ThreadCreated:
			applyIndexThreadCreated(&entry, *payload)
		case corethread.MetadataUpdated:
			mergeIndexMetadata(&entry, payload.Metadata)
			if !payload.UpdatedAt.IsZero() {
				entry.UpdatedAt = payload.UpdatedAt
			}
		case *corethread.MetadataUpdated:
			mergeIndexMetadata(&entry, payload.Metadata)
			if !payload.UpdatedAt.IsZero() {
				entry.UpdatedAt = payload.UpdatedAt
			}
		case corethread.ThreadArchived:
			entry.Archived = true
			if !payload.At.IsZero() {
				entry.UpdatedAt = payload.At
			}
		case *corethread.ThreadArchived:
			entry.Archived = true
			if !payload.At.IsZero() {
				entry.UpdatedAt = payload.At
			}
		case corethread.ThreadUnarchived:
			entry.Archived = false
			if !payload.At.IsZero() {
				entry.UpdatedAt = payload.At
			}
		case *corethread.ThreadUnarchived:
			entry.Archived = false
			if !payload.At.IsZero() {
				entry.UpdatedAt = payload.At
			}
		}
		if entry.UpdatedAt.IsZero() || stored.Record.Time.After(entry.UpdatedAt) {
			entry.UpdatedAt = stored.Record.Time
		}
		i.entries[id] = entry
	}
	return nil
}

// List returns index entries in creation order.
func (i *ThreadIndex) List(params corethread.ListParams) []IndexEntry {
	i.mu.RLock()
	defer i.mu.RUnlock()
	var out []IndexEntry
	for _, id := range i.order {
		entry := i.entries[id]
		if entry.Archived && !params.IncludeArchived {
			continue
		}
		entry.Metadata = cloneStringMap(entry.Metadata)
		out = append(out, entry)
		if params.Limit > 0 && len(out) >= params.Limit {
			break
		}
	}
	return out
}

func applyIndexThreadCreated(entry *IndexEntry, payload corethread.ThreadCreated) {
	entry.ID = payload.ThreadID
	entry.BranchID = normalizeBranch(payload.BranchID)
	entry.Metadata = cloneStringMap(payload.Metadata)
	entry.CreatedAt = payload.CreatedAt
	entry.UpdatedAt = payload.CreatedAt
}

func mergeIndexMetadata(entry *IndexEntry, metadata map[string]string) {
	if entry.Metadata == nil {
		entry.Metadata = map[string]string{}
	}
	for key, value := range metadata {
		entry.Metadata[key] = value
	}
}
