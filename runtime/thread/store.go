package thread

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/fluxplane/engine/core/event"
	corethread "github.com/fluxplane/engine/core/thread"
)

const (
	defaultIndexStream   event.StreamID = "thread.index"
	threadWriteRetries                  = 16
	threadWriteRetryBase                = time.Millisecond

	attrBranchID     = "thread.branch_id"
	attrNodeID       = "thread.node_id"
	attrParentNodeID = "thread.parent_node_id"

	// attrIdempotencyKey documents the runtime/thread-owned logical write key.
	// Thread mutators assign record IDs before event.Store calls so automatic
	// retries reuse the same IDs. Stores reject duplicate record IDs atomically;
	// runtime/thread recovers ambiguous committed results by loading the thread.
	attrIdempotencyKey = "thread.idempotency_key"
)

// WriteError adds payload-free thread identifiers and retry metadata to
// storage failures returned while mutating a thread.
type WriteError struct {
	Op       string
	ThreadID corethread.ID
	Attempt  int
	Attempts int
	Err      error
}

func (e WriteError) Error() string {
	msg := fmt.Sprintf("thread: %s failed", e.Op)
	if e.ThreadID != "" {
		msg += fmt.Sprintf(" thread_id=%q", e.ThreadID)
	}
	if e.Attempts > 0 {
		msg += fmt.Sprintf(" attempt=%d/%d", e.Attempt, e.Attempts)
	}
	if e.Err != nil {
		msg += ": " + e.Err.Error()
	}
	return msg
}

func (e WriteError) Unwrap() error { return e.Err }

func wrapWriteError(op string, id corethread.ID, attempt int, err error) error {
	if err == nil {
		return nil
	}
	var existing WriteError
	if errors.As(err, &existing) {
		if op != "" {
			existing.Op = op
		}
		if id != "" {
			existing.ThreadID = id
		}
		if attempt > 0 {
			existing.Attempt = attempt
			existing.Attempts = threadWriteRetries
		}
		return existing
	}
	return WriteError{Op: op, ThreadID: id, Attempt: attempt, Attempts: threadWriteRetries, Err: err}
}

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
	params.ID = id

	operationID := newID("thread_create_")
	var lastConflict error
	for attempt := 0; attempt < threadWriteRetries; attempt++ {
		snapshot, err := s.createOnce(ctx, params, operationID)
		if err == nil {
			return snapshot, nil
		}
		if !errors.Is(err, event.ErrAppendConflict) {
			return corethread.Snapshot{}, wrapWriteError("create", id, attempt+1, err)
		}
		lastConflict = wrapWriteError("create", id, attempt+1, err)
		if !isAppendConflictOn(err, s.index) {
			observed, loadErr := s.loadThread(ctx, id)
			if loadErr != nil {
				return corethread.Snapshot{}, wrapWriteError("create load after conflict", id, attempt+1, loadErr)
			}
			if len(observed.records) > 0 {
				return corethread.Snapshot{}, corethread.ErrAlreadyExists
			}
			return corethread.Snapshot{}, err
		}
		if err := sleepThreadWriteRetry(ctx, attempt); err != nil {
			return corethread.Snapshot{}, wrapWriteError("create retry sleep", id, attempt+1, err)
		}
	}
	return corethread.Snapshot{}, lastConflict
}

func (s *Store) createOnce(ctx context.Context, params corethread.CreateParams, operationID string) (corethread.Snapshot, error) {
	id := params.ID
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
	threadKey := threadRecordID(id, operationID, "thread", payload.EventName(), "0")
	indexKey := threadRecordID(id, operationID, "index", payload.EventName(), "0")
	if _, err := s.events.AppendBatch(ctx,
		event.AppendRequest{
			Stream:  s.threadStream(id),
			Options: event.ExpectSequence(0),
			Records: []event.Record{{
				ID:      threadKey,
				Name:    payload.EventName(),
				Time:    now,
				Scope:   event.Scope{ThreadID: string(id)},
				Payload: payload,
				Attributes: map[string]string{
					attrBranchID:       string(branchID),
					attrIdempotencyKey: threadKey,
				},
			}},
		},
		event.AppendRequest{
			Stream:  s.index,
			Options: event.ExpectSequence(index.lastSequence),
			Records: []event.Record{{
				ID:      indexKey,
				Name:    payload.EventName(),
				Time:    now,
				Scope:   event.Scope{ThreadID: string(id)},
				Payload: payload,
				Attributes: map[string]string{
					attrIdempotencyKey: indexKey,
				},
			}},
		},
	); err != nil {
		if errors.Is(err, event.ErrDuplicateRecord) {
			return s.Read(ctx, corethread.ReadParams{ID: id})
		}
		return corethread.Snapshot{}, wrapWriteError("create", id, 1, err)
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
	operationID := newID("thread_append_")
	appendNow := time.Now().UTC()
	stableRecords := make([]corethread.AppendRecord, len(records))
	for i, record := range records {
		stableRecords[i] = record
		if stableRecords[i].Event.ID == "" {
			stableRecords[i].Event.ID = threadRecordID(ref.ID, operationID, "thread", stableRecords[i].Event.Name, fmt.Sprintf("%d", i))
		}
		if stableRecords[i].Event.Time.IsZero() {
			stableRecords[i].Event.Time = appendNow
		}
	}
	var lastConflict error
	for attempt := 0; attempt < threadWriteRetries; attempt++ {
		observed, err := s.loadThread(ctx, ref.ID)
		if err != nil {
			return nil, wrapWriteError("append", ref.ID, attempt+1, err)
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
		for i, record := range stableRecords {
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
			if errors.Is(err, event.ErrDuplicateRecord) {
				return s.recoverDuplicateAppend(ctx, ref.ID, eventRecords)
			}
			if errors.Is(err, event.ErrAppendConflict) {
				lastConflict = wrapWriteError("append", ref.ID, attempt+1, err)
				if err := sleepThreadWriteRetry(ctx, attempt); err != nil {
					return nil, wrapWriteError("append retry sleep", ref.ID, attempt+1, err)
				}
				continue
			}
			return nil, wrapWriteError("append", ref.ID, attempt+1, err)
		}

		out := make([]corethread.Record, len(stored))
		nextSequence := event.Sequence(len(snapshot.Events) + 1)
		for i, storedRecord := range stored {
			out[i] = toThreadRecord(ref.ID, nextSequence+event.Sequence(i), storedRecord.Record)
		}
		return out, nil
	}
	return nil, lastConflict
}

// Fork creates a branch from another branch.
func (s *Store) Fork(ctx context.Context, params corethread.ForkParams) (corethread.Snapshot, error) {
	if params.ID == "" {
		return corethread.Snapshot{}, fmt.Errorf("thread: id is empty")
	}
	to := params.ToBranchID
	if to == "" {
		to = corethread.BranchID(newID("branch_"))
	}
	var lastConflict error
	for attempt := 0; attempt < threadWriteRetries; attempt++ {
		observed, err := s.loadThread(ctx, params.ID)
		if err != nil {
			return corethread.Snapshot{}, wrapWriteError("fork", params.ID, attempt+1, err)
		}
		if len(observed.records) == 0 {
			return corethread.Snapshot{}, corethread.ErrNotFound
		}
		snapshot := projectSnapshot(params.ID, observed.records)
		from := normalizeBranch(params.FromBranchID)
		if _, ok := snapshot.Branches[from]; !ok {
			return corethread.Snapshot{}, fmt.Errorf("%w: branch %q", corethread.ErrNotFound, from)
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
			if errors.Is(err, event.ErrAppendConflict) {
				lastConflict = wrapWriteError("fork", params.ID, attempt+1, err)
				if err := sleepThreadWriteRetry(ctx, attempt); err != nil {
					return corethread.Snapshot{}, wrapWriteError("fork retry sleep", params.ID, attempt+1, err)
				}
				continue
			}
			return corethread.Snapshot{}, wrapWriteError("fork", params.ID, attempt+1, err)
		}
		return s.Read(ctx, corethread.ReadParams{ID: params.ID})
	}
	return corethread.Snapshot{}, lastConflict
}

func (s *Store) recoverDuplicateAppend(ctx context.Context, id corethread.ID, records []event.Record) ([]corethread.Record, error) {
	observed, err := s.loadThread(ctx, id)
	if err != nil {
		return nil, err
	}
	byID := map[string]corethread.Record{}
	for i, record := range observed.records {
		if record.ID != "" {
			byID[record.ID] = toThreadRecord(id, event.Sequence(i+1), record)
		}
	}
	out := make([]corethread.Record, len(records))
	for i, record := range records {
		existing, ok := byID[record.ID]
		if !ok || !sameLogicalRecord(existing.Event, record) {
			return nil, event.DuplicateRecord{Stream: s.threadStream(id), ID: record.ID}
		}
		out[i] = existing
	}
	return out, nil
}

func sameLogicalRecord(existing, retry event.Record) bool {
	if existing.ID != retry.ID || existing.Name != retry.Name || !existing.Time.Equal(retry.Time) {
		return false
	}
	if existing.Scope.ThreadID != retry.Scope.ThreadID || existing.Scope.TurnID != retry.Scope.TurnID || existing.Scope.OperationID != retry.Scope.OperationID {
		return false
	}
	if !sameStringMap(existing.Attributes, retry.Attributes) {
		return false
	}
	return fmt.Sprintf("%#v", existing.Payload) == fmt.Sprintf("%#v", retry.Payload)
}

func sameStringMap(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for key, av := range a {
		if b[key] != av {
			return false
		}
	}
	return true
}

func isAppendConflictOn(err error, stream event.StreamID) bool {
	var conflict event.AppendConflict
	if errors.As(err, &conflict) {
		return conflict.Stream == stream
	}
	return false
}

func threadRecordID(id corethread.ID, operationID, streamRole string, eventName event.Name, ordinal string) string {
	return strings.Join([]string{string(id), operationID, streamRole, string(eventName), ordinal}, ":")
}

func sleepThreadWriteRetry(ctx context.Context, attempt int) error {
	delay := threadWriteRetryBase << attempt
	if delay > 50*time.Millisecond {
		delay = 50 * time.Millisecond
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
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

// Archive archives a thread. Repeated archives are idempotent; conflicts on the
// shared thread index are retried after reloading state. Same-thread append
// conflicts are retried only after re-evaluating the archive state.
func (s *Store) Archive(ctx context.Context, id corethread.ID) error {
	var lastConflict error
	for attempt := 0; attempt < threadWriteRetries; attempt++ {
		err := s.setArchivedOnce(ctx, id, true)
		if err == nil {
			return nil
		}
		if !errors.Is(err, event.ErrAppendConflict) {
			return err
		}
		lastConflict = err
		if err := sleepThreadWriteRetry(ctx, attempt); err != nil {
			return err
		}
	}
	return lastConflict
}

// Unarchive unarchives a thread. Repeated unarchives are idempotent; conflicts
// on the shared thread index are retried after reloading state. Same-thread
// append conflicts are retried only after re-evaluating the archive state.
func (s *Store) Unarchive(ctx context.Context, id corethread.ID) error {
	var lastConflict error
	for attempt := 0; attempt < threadWriteRetries; attempt++ {
		err := s.setArchivedOnce(ctx, id, false)
		if err == nil {
			return nil
		}
		if !errors.Is(err, event.ErrAppendConflict) {
			return err
		}
		lastConflict = err
		if err := sleepThreadWriteRetry(ctx, attempt); err != nil {
			return err
		}
	}
	return lastConflict
}

func (s *Store) setArchivedOnce(ctx context.Context, id corethread.ID, archived bool) error {
	observed, err := s.loadThread(ctx, id)
	if err != nil {
		return err
	}
	if len(observed.records) == 0 {
		return corethread.ErrNotFound
	}
	snapshot := projectSnapshot(id, observed.records)
	if snapshot.Archived == archived {
		return nil
	}
	index, err := s.loadIndex(ctx)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	var payload event.Event
	var name event.Name
	if archived {
		archivePayload := corethread.ThreadArchived{ThreadID: id, At: now}
		payload = archivePayload
		name = archivePayload.EventName()
	} else {
		unarchivePayload := corethread.ThreadUnarchived{ThreadID: id, At: now}
		payload = unarchivePayload
		name = unarchivePayload.EventName()
	}
	_, err = s.events.AppendBatch(ctx,
		event.AppendRequest{
			Stream:  s.threadStream(id),
			Options: event.ExpectSequence(observed.lastSequence),
			Records: []event.Record{{
				Name:    name,
				Time:    now,
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
				Name:    name,
				Time:    now,
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
