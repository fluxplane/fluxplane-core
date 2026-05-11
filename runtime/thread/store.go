package thread

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/fluxplane/agentruntime/core/event"
	corethread "github.com/fluxplane/agentruntime/core/thread"
)

const (
	defaultIndexStream event.StreamID = "thread.index"

	attrBranchID     = "thread.branch_id"
	attrNodeID       = "thread.node_id"
	attrParentNodeID = "thread.parent_node_id"
)

// Store implements core/thread.Store by replaying a core/event.Store stream.
type Store struct {
	events    event.Store
	index     event.StreamID
	readIndex *ThreadIndex
}

var _ corethread.Store = (*Store)(nil)

// Option configures Store.
type Option func(*Store)

// WithStream overrides the index event stream used to list threads.
//
// Thread history records are stored in per-thread streams derived from thread
// IDs. The configured stream is only the thread index stream.
func WithStream(stream event.StreamID) Option {
	return func(s *Store) {
		if stream != "" {
			s.index = stream
		}
	}
}

// WithThreadIndex configures an optional projected read model for List.
func WithThreadIndex(index *ThreadIndex) Option {
	return func(s *Store) {
		s.readIndex = index
	}
}

// NewStore returns a thread store backed by events.
func NewStore(events event.Store, opts ...Option) (*Store, error) {
	if events == nil {
		return nil, fmt.Errorf("thread: event store is nil")
	}
	store := &Store{events: events, index: defaultIndexStream}
	for _, opt := range opts {
		opt(store)
	}
	return store, nil
}

// Create creates a new thread.
func (s *Store) Create(ctx context.Context, params corethread.CreateParams) (corethread.Snapshot, error) {
	id := params.ID
	if id == "" {
		id = corethread.ID(newID("thread_"))
	}
	index, err := s.loadIndex(ctx)
	if err != nil {
		return corethread.Snapshot{}, err
	}
	if index.exists(id) {
		return corethread.Snapshot{}, corethread.ErrAlreadyExists
	}

	branchID := normalizeBranch(params.BranchID)
	now := params.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}
	payload := corethread.ThreadCreated{
		ThreadID:  id,
		BranchID:  branchID,
		Metadata:  cloneStringMap(params.Metadata),
		CreatedAt: now,
		Source:    params.Source,
	}
	if _, err := s.events.AppendBatch(ctx,
		event.AppendRequest{
			Stream:  s.threadStream(id),
			Options: event.ExpectSequence(0),
			Records: []event.Record{{
				Name:    payload.EventName(),
				Time:    now,
				Scope:   event.Scope{ThreadID: string(id)},
				Payload: payload,
				Attributes: map[string]string{
					attrBranchID: string(branchID),
				},
			}},
		},
		event.AppendRequest{
			Stream:  s.index,
			Options: event.ExpectSequence(index.lastSequence),
			Records: []event.Record{{
				Name:    payload.EventName(),
				Time:    now,
				Scope:   event.Scope{ThreadID: string(id)},
				Payload: payload,
			}},
		},
	); err != nil {
		return corethread.Snapshot{}, err
	}
	return s.Read(ctx, corethread.ReadParams{ID: id})
}

// Append appends thread-positioned event records.
func (s *Store) Append(ctx context.Context, ref corethread.Ref, records ...corethread.AppendRecord) ([]corethread.Record, error) {
	if ref.ID == "" {
		return nil, fmt.Errorf("thread: id is empty")
	}
	if len(records) == 0 {
		return nil, nil
	}
	observed, err := s.loadThread(ctx, ref.ID)
	if err != nil {
		return nil, err
	}
	if len(observed.records) == 0 {
		return nil, corethread.ErrNotFound
	}
	snapshot := projectSnapshot(ref.ID, observed.records)
	branchID := normalizeBranch(ref.BranchID)
	if _, ok := snapshot.Branches[branchID]; !ok {
		return nil, fmt.Errorf("%w: branch %q", corethread.ErrNotFound, branchID)
	}

	eventRecords := make([]event.Record, len(records))
	for i, record := range records {
		eventRecord := record.Event
		eventRecord.Scope.ThreadID = string(ref.ID)
		eventRecord.Attributes = cloneStringMap(eventRecord.Attributes)
		if eventRecord.Attributes == nil {
			eventRecord.Attributes = map[string]string{}
		}
		eventRecord.Attributes[attrBranchID] = string(branchID)
		if record.NodeID != "" {
			eventRecord.Attributes[attrNodeID] = string(record.NodeID)
		}
		if record.ParentNodeID != "" {
			eventRecord.Attributes[attrParentNodeID] = string(record.ParentNodeID)
		}
		eventRecords[i] = eventRecord
	}
	stored, err := s.events.Append(ctx, s.threadStream(ref.ID), event.ExpectSequence(observed.lastSequence), eventRecords...)
	if err != nil {
		return nil, err
	}

	out := make([]corethread.Record, len(stored))
	nextSequence := event.Sequence(len(snapshot.Events) + 1)
	for i, storedRecord := range stored {
		out[i] = toThreadRecord(ref.ID, nextSequence+event.Sequence(i), storedRecord.Record)
	}
	return out, nil
}

// Fork creates a branch from another branch.
func (s *Store) Fork(ctx context.Context, params corethread.ForkParams) (corethread.Snapshot, error) {
	if params.ID == "" {
		return corethread.Snapshot{}, fmt.Errorf("thread: id is empty")
	}
	observed, err := s.loadThread(ctx, params.ID)
	if err != nil {
		return corethread.Snapshot{}, err
	}
	if len(observed.records) == 0 {
		return corethread.Snapshot{}, corethread.ErrNotFound
	}
	snapshot := projectSnapshot(params.ID, observed.records)
	from := normalizeBranch(params.FromBranchID)
	if _, ok := snapshot.Branches[from]; !ok {
		return corethread.Snapshot{}, fmt.Errorf("%w: branch %q", corethread.ErrNotFound, from)
	}
	to := params.ToBranchID
	if to == "" {
		to = corethread.BranchID(newID("branch_"))
	}
	if _, ok := snapshot.Branches[to]; ok {
		return corethread.Snapshot{}, fmt.Errorf("%w: branch %q", corethread.ErrAlreadyExists, to)
	}
	now := params.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}
	payload := corethread.BranchCreated{
		ThreadID:     params.ID,
		FromBranchID: from,
		ToBranchID:   to,
		ForkSequence: event.Sequence(len(snapshot.Events)),
		CreatedAt:    now,
		Source:       params.Source,
	}
	if _, err := s.events.Append(ctx, s.threadStream(params.ID), event.ExpectSequence(observed.lastSequence), event.Record{
		Name:    payload.EventName(),
		Time:    now,
		Scope:   event.Scope{ThreadID: string(params.ID)},
		Payload: payload,
		Attributes: map[string]string{
			attrBranchID: string(to),
		},
	}); err != nil {
		return corethread.Snapshot{}, err
	}
	return s.Read(ctx, corethread.ReadParams{ID: params.ID})
}

// Read returns a projected thread snapshot.
func (s *Store) Read(ctx context.Context, params corethread.ReadParams) (corethread.Snapshot, error) {
	if params.ID == "" {
		return corethread.Snapshot{}, fmt.Errorf("thread: id is empty")
	}
	observed, err := s.loadThread(ctx, params.ID)
	if err != nil {
		return corethread.Snapshot{}, err
	}
	if len(observed.records) == 0 {
		return corethread.Snapshot{}, corethread.ErrNotFound
	}
	return projectSnapshot(params.ID, observed.records), nil
}

// List returns projected thread snapshots.
func (s *Store) List(ctx context.Context, params corethread.ListParams) (corethread.Page, error) {
	if s.readIndex != nil {
		return listFromReadIndex(s.readIndex, params), nil
	}
	return s.listByReplay(ctx, params)
}

func (s *Store) listByReplay(ctx context.Context, params corethread.ListParams) (corethread.Page, error) {
	index, err := s.loadIndex(ctx)
	if err != nil {
		return corethread.Page{}, err
	}
	page := corethread.Page{}
	for _, id := range index.order {
		observed, err := s.loadThread(ctx, id)
		if err != nil {
			return corethread.Page{}, err
		}
		if len(observed.records) == 0 {
			continue
		}
		snapshot := projectSnapshot(id, observed.records)
		if snapshot.Archived && !params.IncludeArchived {
			continue
		}
		page.Threads = append(page.Threads, snapshot)
		if params.Limit > 0 && len(page.Threads) >= params.Limit {
			break
		}
	}
	return page, nil
}

// Archive archives a thread.
func (s *Store) Archive(ctx context.Context, id corethread.ID) error {
	observed, err := s.loadThread(ctx, id)
	if err != nil {
		return err
	}
	if len(observed.records) == 0 {
		return corethread.ErrNotFound
	}
	index, err := s.loadIndex(ctx)
	if err != nil {
		return err
	}
	payload := corethread.ThreadArchived{ThreadID: id, At: time.Now().UTC()}
	_, err = s.events.AppendBatch(ctx,
		event.AppendRequest{
			Stream:  s.threadStream(id),
			Options: event.ExpectSequence(observed.lastSequence),
			Records: []event.Record{{
				Name:    payload.EventName(),
				Time:    payload.At,
				Scope:   event.Scope{ThreadID: string(id)},
				Payload: payload,
				Attributes: map[string]string{
					attrBranchID: string(corethread.MainBranch),
				},
			}},
		},
		event.AppendRequest{
			Stream:  s.index,
			Options: event.ExpectSequence(index.lastSequence),
			Records: []event.Record{{
				Name:    payload.EventName(),
				Time:    payload.At,
				Scope:   event.Scope{ThreadID: string(id)},
				Payload: payload,
			}},
		},
	)
	return err
}

// Unarchive unarchives a thread.
func (s *Store) Unarchive(ctx context.Context, id corethread.ID) error {
	observed, err := s.loadThread(ctx, id)
	if err != nil {
		return err
	}
	if len(observed.records) == 0 {
		return corethread.ErrNotFound
	}
	index, err := s.loadIndex(ctx)
	if err != nil {
		return err
	}
	payload := corethread.ThreadUnarchived{ThreadID: id, At: time.Now().UTC()}
	_, err = s.events.AppendBatch(ctx,
		event.AppendRequest{
			Stream:  s.threadStream(id),
			Options: event.ExpectSequence(observed.lastSequence),
			Records: []event.Record{{
				Name:    payload.EventName(),
				Time:    payload.At,
				Scope:   event.Scope{ThreadID: string(id)},
				Payload: payload,
				Attributes: map[string]string{
					attrBranchID: string(corethread.MainBranch),
				},
			}},
		},
		event.AppendRequest{
			Stream:  s.index,
			Options: event.ExpectSequence(index.lastSequence),
			Records: []event.Record{{
				Name:    payload.EventName(),
				Time:    payload.At,
				Scope:   event.Scope{ThreadID: string(id)},
				Payload: payload,
			}},
		},
	)
	return err
}

type observedThread struct {
	lastSequence event.Sequence
	records      []event.Record
}

func (s *Store) loadThread(ctx context.Context, id corethread.ID) (observedThread, error) {
	stored, err := s.events.Load(ctx, s.threadStream(id), event.LoadOptions{})
	if err != nil {
		return observedThread{}, err
	}
	observed := observedThread{}
	for _, storedRecord := range stored {
		if storedRecord.Sequence > observed.lastSequence {
			observed.lastSequence = storedRecord.Sequence
		}
		observed.records = append(observed.records, storedRecord.Record)
	}
	return observed, nil
}

type observedIndex struct {
	lastSequence event.Sequence
	seen         map[corethread.ID]struct{}
	order        []corethread.ID
}

func (i observedIndex) exists(id corethread.ID) bool {
	_, exists := i.seen[id]
	return exists
}

func (s *Store) loadIndex(ctx context.Context) (observedIndex, error) {
	stored, err := s.events.Load(ctx, s.index, event.LoadOptions{})
	if err != nil {
		return observedIndex{}, err
	}
	observed := observedIndex{seen: map[corethread.ID]struct{}{}}
	for _, storedRecord := range stored {
		if storedRecord.Sequence > observed.lastSequence {
			observed.lastSequence = storedRecord.Sequence
		}
		id := corethread.ID(storedRecord.Record.Scope.ThreadID)
		if id == "" {
			continue
		}
		if _, exists := observed.seen[id]; exists {
			continue
		}
		observed.seen[id] = struct{}{}
		observed.order = append(observed.order, id)
	}
	return observed, nil
}

func (s *Store) threadStream(id corethread.ID) event.StreamID {
	return event.StreamID("thread:" + string(id))
}

func projectSnapshot(id corethread.ID, records []event.Record) corethread.Snapshot {
	snapshot := corethread.Snapshot{
		ID:       id,
		BranchID: corethread.MainBranch,
		Branches: map[corethread.BranchID]corethread.Branch{
			corethread.MainBranch: {ID: corethread.MainBranch},
		},
	}
	for i, record := range records {
		sequence := event.Sequence(i + 1)
		threadRecord := toThreadRecord(id, sequence, record)
		snapshot.Events = append(snapshot.Events, threadRecord)

		switch payload := record.Payload.(type) {
		case corethread.ThreadCreated:
			applyThreadCreated(&snapshot, payload, threadRecord.BranchID)
		case *corethread.ThreadCreated:
			applyThreadCreated(&snapshot, *payload, threadRecord.BranchID)
		case corethread.MetadataUpdated:
			mergeMetadata(&snapshot, payload.Metadata)
			if !payload.UpdatedAt.IsZero() {
				snapshot.UpdatedAt = payload.UpdatedAt
			}
		case *corethread.MetadataUpdated:
			mergeMetadata(&snapshot, payload.Metadata)
			if !payload.UpdatedAt.IsZero() {
				snapshot.UpdatedAt = payload.UpdatedAt
			}
		case corethread.ThreadArchived:
			snapshot.Archived = true
			snapshot.UpdatedAt = payload.At
		case *corethread.ThreadArchived:
			snapshot.Archived = true
			snapshot.UpdatedAt = payload.At
		case corethread.ThreadUnarchived:
			snapshot.Archived = false
			snapshot.UpdatedAt = payload.At
		case *corethread.ThreadUnarchived:
			snapshot.Archived = false
			snapshot.UpdatedAt = payload.At
		case corethread.BranchCreated:
			applyBranchCreated(&snapshot, payload)
		case *corethread.BranchCreated:
			applyBranchCreated(&snapshot, *payload)
		}
		if snapshot.UpdatedAt.IsZero() || record.Time.After(snapshot.UpdatedAt) {
			snapshot.UpdatedAt = record.Time
		}
	}
	return snapshot
}

func toThreadRecord(id corethread.ID, sequence event.Sequence, record event.Record) corethread.Record {
	return corethread.Record{
		ThreadID:     id,
		BranchID:     normalizeBranch(corethread.BranchID(record.Attributes[attrBranchID])),
		NodeID:       corethread.NodeID(record.Attributes[attrNodeID]),
		ParentNodeID: corethread.NodeID(record.Attributes[attrParentNodeID]),
		Sequence:     sequence,
		Event:        record,
	}
}

func applyThreadCreated(snapshot *corethread.Snapshot, payload corethread.ThreadCreated, branchID corethread.BranchID) {
	if payload.BranchID != "" {
		branchID = payload.BranchID
	}
	branchID = normalizeBranch(branchID)
	snapshot.BranchID = branchID
	snapshot.Metadata = cloneStringMap(payload.Metadata)
	snapshot.CreatedAt = payload.CreatedAt
	snapshot.UpdatedAt = payload.CreatedAt
	snapshot.Branches[branchID] = corethread.Branch{ID: branchID, CreatedAt: payload.CreatedAt}
}

func applyBranchCreated(snapshot *corethread.Snapshot, payload corethread.BranchCreated) {
	to := normalizeBranch(payload.ToBranchID)
	snapshot.Branches[to] = corethread.Branch{
		ID:           to,
		Parent:       normalizeBranch(payload.FromBranchID),
		ForkSequence: payload.ForkSequence,
		CreatedAt:    payload.CreatedAt,
	}
}

func mergeMetadata(snapshot *corethread.Snapshot, metadata map[string]string) {
	if snapshot.Metadata == nil {
		snapshot.Metadata = map[string]string{}
	}
	for key, value := range metadata {
		snapshot.Metadata[key] = value
	}
}

func normalizeBranch(branchID corethread.BranchID) corethread.BranchID {
	if branchID == "" {
		return corethread.MainBranch
	}
	return branchID
}

func cloneStringMap(in map[string]string) map[string]string {
	if in == nil {
		return nil
	}
	out := make(map[string]string, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func newID(prefix string) string {
	var bytes [16]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return fmt.Sprintf("%s%d", prefix, time.Now().UnixNano())
	}
	return prefix + hex.EncodeToString(bytes[:])
}
