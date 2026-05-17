package sqleventstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/fluxplane/agentruntime/core/event"
	"github.com/fluxplane/agentruntime/core/eventcodec"
	"github.com/fluxplane/agentruntime/core/policy"
	_ "modernc.org/sqlite"
)

const (
	sqliteBusyCode       = 5
	sqliteLockedCode     = 6
	sqliteConstraintCode = 19
	sqliteWriteAttempts  = 8
	sqliteWriteRetryBase = 25 * time.Millisecond
	sqliteWriteRetryMax  = 500 * time.Millisecond
)

// StorageErrorClass identifies the storage failure category for retry,
// logging, and tests without parsing error strings.
type StorageErrorClass string

const (
	StorageAppendConflict StorageErrorClass = "append_conflict"
	StorageBusyLocked     StorageErrorClass = "sqlite_busy_locked"
	StorageConstraint     StorageErrorClass = "sqlite_constraint"
	StorageContext        StorageErrorClass = "context"
	StorageUnknown        StorageErrorClass = "unknown_storage"
)

// StorageError adds structured, payload-free observability to SQLite event-store
// failures. It intentionally carries stream and sequence metadata, but not event
// payloads or attributes.
type StorageError struct {
	Class           StorageErrorClass
	Op              string
	Stream          event.StreamID
	RequestIndex    int
	HasRequestIndex bool
	Expected        event.Sequence
	Actual          event.Sequence
	HasSequence     bool
	Attempt         int
	Attempts        int
	SQLiteCode      int
	Err             error
}

func (e StorageError) Error() string {
	msg := fmt.Sprintf("sqleventstore: %s failed class=%s", e.Op, e.Class)
	if e.Stream != "" {
		msg += fmt.Sprintf(" stream=%q", e.Stream)
	}
	if e.HasRequestIndex {
		msg += fmt.Sprintf(" request_index=%d", e.RequestIndex)
	}
	if e.HasSequence {
		msg += fmt.Sprintf(" expected_sequence=%d actual_sequence=%d", e.Expected, e.Actual)
	}
	if e.Attempts > 0 {
		msg += fmt.Sprintf(" attempt=%d/%d", e.Attempt, e.Attempts)
	}
	if e.SQLiteCode != 0 {
		msg += fmt.Sprintf(" sqlite_code=%d", e.SQLiteCode)
	}
	if e.Err != nil {
		msg += ": " + e.Err.Error()
	}
	return msg
}

func (e StorageError) Unwrap() error { return e.Err }

func storageErrorClass(err error) StorageErrorClass {
	if err == nil {
		return StorageUnknown
	}
	if errors.Is(err, event.ErrAppendConflict) {
		return StorageAppendConflict
	}
	if errors.Is(err, event.ErrDuplicateRecord) {
		return StorageConstraint
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return StorageContext
	}
	code, ok := sqliteErrorCode(err)
	if !ok {
		return StorageUnknown
	}
	switch code & 0xff {
	case sqliteBusyCode, sqliteLockedCode:
		return StorageBusyLocked
	case sqliteConstraintCode:
		return StorageConstraint
	default:
		return StorageUnknown
	}
}

func wrapStorageError(op string, stream event.StreamID, err error) error {
	return wrapStorageErrorForRequest(op, stream, -1, err)
}

func wrapStorageErrorForRequest(op string, stream event.StreamID, requestIndex int, err error) error {
	if err == nil {
		return nil
	}
	var existing StorageError
	if errors.As(err, &existing) {
		if requestIndex >= 0 && !existing.HasRequestIndex {
			existing.RequestIndex = requestIndex
			existing.HasRequestIndex = true
			return existing
		}
		return err
	}
	wrapped := StorageError{Class: storageErrorClass(err), Op: op, Stream: stream, Err: err}
	if requestIndex >= 0 {
		wrapped.RequestIndex = requestIndex
		wrapped.HasRequestIndex = true
	}
	if conflict, ok := appendConflict(err); ok {
		wrapped.Stream = conflict.Stream
		wrapped.Expected = conflict.Expected
		wrapped.Actual = conflict.Actual
		wrapped.HasSequence = true
	}
	if code, ok := sqliteErrorCode(err); ok {
		wrapped.SQLiteCode = code
	}
	if errors.Is(err, event.ErrDuplicateRecord) {
		wrapped.Class = StorageConstraint
	}
	return wrapped
}

func withAttempt(err error, attempt, attempts int) error {
	if err == nil {
		return nil
	}
	var storage StorageError
	if errors.As(err, &storage) {
		storage.Attempt = attempt
		storage.Attempts = attempts
		return storage
	}
	storage = StorageError{Class: storageErrorClass(err), Op: "append batch", Attempt: attempt, Attempts: attempts, Err: err}
	if code, ok := sqliteErrorCode(err); ok {
		storage.SQLiteCode = code
	}
	if errors.Is(err, event.ErrDuplicateRecord) {
		storage.Class = StorageConstraint
	}
	return storage
}

func appendConflict(err error) (event.AppendConflict, bool) {
	var conflict event.AppendConflict
	if errors.As(err, &conflict) {
		return conflict, true
	}
	return event.AppendConflict{}, false
}

// Store is a SQLite-backed event store.
type Store struct {
	db       *sql.DB
	registry *event.Registry
	ownedDB  bool
}

var _ event.Store = (*Store)(nil)

// Open opens or creates a SQLite event store at path.
func Open(path string, registry *event.Registry) (*Store, error) {
	if path != ":memory:" {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return nil, fmt.Errorf("sqleventstore: create dir: %w", err)
		}
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("sqleventstore: open: %w", err)
	}
	db.SetMaxOpenConns(1)
	store, err := OpenDB(db, registry)
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	store.ownedDB = true
	return store, nil
}

// OpenDB wraps an existing database. The caller owns db.
//
// Append operations use a dedicated connection and BEGIN IMMEDIATE so SQLite
// acquires writer intent before reading stream sequence state. OpenDB does not
// change the caller's connection-pool settings; callers that share one *sql.DB
// with other work should size that pool deliberately. Open uses a single pooled
// connection because SQLite permits only one writer at a time.
func OpenDB(db *sql.DB, registry *event.Registry) (*Store, error) {
	if db == nil {
		return nil, fmt.Errorf("sqleventstore: db is nil")
	}
	if _, err := db.Exec(schemaSQL); err != nil {
		return nil, fmt.Errorf("sqleventstore: schema: %w", err)
	}
	return &Store{db: db, registry: registry}, nil
}

// Close closes the database when this store opened it.
func (s *Store) Close() error {
	if s == nil || s.db == nil || !s.ownedDB {
		return nil
	}
	return s.db.Close()
}

// Append writes records atomically to stream.
func (s *Store) Append(ctx context.Context, stream event.StreamID, opts event.AppendOptions, records ...event.Record) ([]event.StoredRecord, error) {
	results, err := s.AppendBatch(ctx, event.AppendRequest{
		Stream:  stream,
		Options: opts,
		Records: records,
	})
	if err != nil {
		return nil, err
	}
	if len(results) == 0 {
		return nil, nil
	}
	return results[0].Records, nil
}

// AppendBatch writes requests atomically in one transaction.
func (s *Store) AppendBatch(ctx context.Context, requests ...event.AppendRequest) ([]event.AppendResult, error) {
	if len(requests) == 0 {
		return nil, nil
	}
	seen := map[event.StreamID]struct{}{}
	for i, request := range requests {
		if request.Stream == "" {
			return nil, wrapStorageErrorForRequest("append batch validate", request.Stream, i, fmt.Errorf("sqleventstore: stream is empty"))
		}
		if _, exists := seen[request.Stream]; exists {
			return nil, wrapStorageErrorForRequest("append batch validate", request.Stream, i, fmt.Errorf("sqleventstore: duplicate stream %q in append batch", request.Stream))
		}
		seen[request.Stream] = struct{}{}
	}

	var last error
	for attempt := 0; attempt < sqliteWriteAttempts; attempt++ {
		results, err := s.appendBatchOnce(ctx, requests...)
		if err == nil {
			return results, nil
		}
		err = withAttempt(err, attempt+1, sqliteWriteAttempts)
		if !isSQLiteBusy(err) {
			return nil, err
		}
		last = err
		if err := sleepSQLiteWriteRetry(ctx, attempt); err != nil {
			return nil, wrapStorageError("append retry sleep", "", err)
		}
	}
	return nil, last
}

func (s *Store) appendBatchOnce(ctx context.Context, requests ...event.AppendRequest) ([]event.AppendResult, error) {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return nil, wrapStorageError("conn", "", err)
	}
	defer func() { _ = conn.Close() }()

	if _, err := conn.ExecContext(ctx, `BEGIN IMMEDIATE`); err != nil {
		return nil, wrapStorageError("begin immediate", "", err)
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = conn.ExecContext(context.WithoutCancel(ctx), `ROLLBACK`)
		}
	}()

	stmt, err := conn.PrepareContext(ctx, insertSQL)
	if err != nil {
		return nil, wrapStorageError("prepare insert", "", err)
	}
	defer func() { _ = stmt.Close() }()

	now := time.Now().UTC()
	results := make([]event.AppendResult, 0, len(requests))
	for i, request := range requests {
		result, err := s.appendRequest(ctx, conn, stmt, now, request)
		if err != nil {
			return nil, wrapStorageErrorForRequest("append batch request", request.Stream, i, err)
		}
		results = append(results, result)
	}
	if _, err := conn.ExecContext(ctx, `COMMIT`); err != nil {
		return nil, wrapStorageError("commit", "", err)
	}
	committed = true
	return results, nil
}

func sleepSQLiteWriteRetry(ctx context.Context, attempt int) error {
	delay := sqliteWriteRetryBase << attempt
	if delay > sqliteWriteRetryMax {
		delay = sqliteWriteRetryMax
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

type sqliteErrorCoder interface {
	Code() int
}

func sqliteErrorCode(err error) (int, bool) {
	var coded sqliteErrorCoder
	if !errors.As(err, &coded) {
		return 0, false
	}
	return coded.Code(), true
}

func isSQLiteBusy(err error) bool {
	return storageErrorClass(err) == StorageBusyLocked
}

type sequenceQuerier interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func duplicateRecordError(ctx context.Context, q sequenceQuerier, id string, err error) (event.DuplicateRecord, bool) {
	code, ok := sqliteErrorCode(err)
	if !ok || code&0xff != sqliteConstraintCode || id == "" {
		return event.DuplicateRecord{}, false
	}
	var stream string
	if scanErr := q.QueryRowContext(ctx, `SELECT stream FROM events WHERE id = ?`, id).Scan(&stream); scanErr != nil {
		return event.DuplicateRecord{}, false
	}
	return event.DuplicateRecord{Stream: event.StreamID(stream), ID: id}, true
}

func (s *Store) appendRequest(ctx context.Context, q sequenceQuerier, stmt *sql.Stmt, now time.Time, request event.AppendRequest) (event.AppendResult, error) {
	stream := request.Stream
	if stream == "" {
		return event.AppendResult{}, fmt.Errorf("sqleventstore: stream is empty")
	}
	next, err := nextSequence(ctx, q, stream)
	if err != nil {
		return event.AppendResult{}, err
	}
	actual := next - 1
	if request.Options.CheckExpectedSequence && request.Options.ExpectedSequence != actual {
		return event.AppendResult{}, wrapStorageError("append", stream, event.AppendConflict{
			Stream:   stream,
			Expected: request.Options.ExpectedSequence,
			Actual:   actual,
		})
	}
	result := event.AppendResult{Stream: stream}
	for _, record := range request.Records {
		normalized, err := eventcodec.NormalizeRecord(record, now)
		if err != nil {
			return event.AppendResult{}, err
		}
		payload, err := eventcodec.EncodePayload(normalized.Payload)
		if err != nil {
			return event.AppendResult{}, err
		}
		attributes, err := eventcodec.EncodeAttributes(normalized.Attributes)
		if err != nil {
			return event.AppendResult{}, err
		}
		sequence := next
		next++

		_, err = stmt.ExecContext(ctx,
			string(stream),
			int64(sequence),
			normalized.ID,
			normalized.Name,
			normalized.SchemaVersion,
			normalized.Time.Format(time.RFC3339Nano),
			nullBytes(payload),
			nullBytes(attributes),
			nullString(string(normalized.Sensitivity)),
			nullString(normalized.Source.Component),
			nullString(normalized.Source.Instance),
			nullString(normalized.Scope.TenantID),
			nullString(normalized.Scope.AppID),
			nullString(normalized.Scope.SessionID),
			nullString(normalized.Scope.UserID),
			nullString(normalized.Scope.ChannelID),
			nullString(normalized.Scope.AgentID),
			nullString(normalized.Scope.AgentInstanceID),
			nullString(normalized.Scope.ThreadID),
			nullString(normalized.Scope.TurnID),
			nullString(normalized.Scope.WorkflowID),
			nullString(normalized.Scope.RunID),
			nullString(normalized.Scope.StepID),
			nullString(normalized.Scope.OperationID),
			nullString(normalized.CorrelationID),
			nullString(normalized.CausationID),
		)
		if err != nil {
			if duplicate, ok := duplicateRecordError(ctx, q, normalized.ID, err); ok {
				return event.AppendResult{}, fmt.Errorf("%w: %w", duplicate, err)
			}
			return event.AppendResult{}, wrapStorageError("insert", stream, err)
		}
		result.Records = append(result.Records, event.StoredRecord{
			Stream:   stream,
			Sequence: sequence,
			Record:   normalized,
		})
	}
	return result, nil
}

// Load returns records from stream.
func (s *Store) Load(ctx context.Context, stream event.StreamID, opts event.LoadOptions) ([]event.StoredRecord, error) {
	if stream == "" {
		return nil, fmt.Errorf("sqleventstore: stream is empty")
	}
	query := selectSQL
	args := []any{string(stream)}
	if opts.After > 0 {
		query += " AND stream_seq > ?"
		args = append(args, int64(opts.After))
	}
	if opts.Before > 0 {
		query += " AND stream_seq < ?"
		args = append(args, int64(opts.Before))
	}
	if opts.Direction == event.DirectionBackward {
		query += " ORDER BY stream_seq DESC"
	} else {
		query += " ORDER BY stream_seq ASC"
	}
	if opts.Limit > 0 {
		query += " LIMIT ?"
		args = append(args, opts.Limit)
	}

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("sqleventstore: load: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []event.StoredRecord
	for rows.Next() {
		stored, err := s.scanStoredRecord(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, stored)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func nextSequence(ctx context.Context, q sequenceQuerier, stream event.StreamID) (event.Sequence, error) {
	var max sql.NullInt64
	if err := q.QueryRowContext(ctx, `SELECT MAX(stream_seq) FROM events WHERE stream = ?`, string(stream)).Scan(&max); err != nil {
		return 0, wrapStorageError("next sequence", stream, err)
	}
	if !max.Valid {
		return 1, nil
	}
	return event.Sequence(max.Int64 + 1), nil
}

func (s *Store) scanStoredRecord(rows *sql.Rows) (event.StoredRecord, error) {
	var (
		stored      event.StoredRecord
		id          string
		name        string
		version     int
		ts          string
		payload     sql.NullString
		attributes  sql.NullString
		sensitivity sql.NullString
		sourceComp  sql.NullString
		sourceInst  sql.NullString
		tenantID    sql.NullString
		appID       sql.NullString
		sessionID   sql.NullString
		userID      sql.NullString
		channelID   sql.NullString
		agentID     sql.NullString
		agentInst   sql.NullString
		threadID    sql.NullString
		turnID      sql.NullString
		workflowID  sql.NullString
		runID       sql.NullString
		stepID      sql.NullString
		operationID sql.NullString
		correlation sql.NullString
		causation   sql.NullString
	)
	if err := rows.Scan(
		&stored.Stream,
		&stored.Sequence,
		&id,
		&name,
		&version,
		&ts,
		&payload,
		&attributes,
		&sensitivity,
		&sourceComp,
		&sourceInst,
		&tenantID,
		&appID,
		&sessionID,
		&userID,
		&channelID,
		&agentID,
		&agentInst,
		&threadID,
		&turnID,
		&workflowID,
		&runID,
		&stepID,
		&operationID,
		&correlation,
		&causation,
	); err != nil {
		return event.StoredRecord{}, err
	}
	record := event.Record{
		ID:            id,
		Name:          event.Name(name),
		SchemaVersion: version,
		Source: event.Source{
			Component: sourceComp.String,
			Instance:  sourceInst.String,
		},
		Scope: event.Scope{
			TenantID:        tenantID.String,
			AppID:           appID.String,
			SessionID:       sessionID.String,
			UserID:          userID.String,
			ChannelID:       channelID.String,
			AgentID:         agentID.String,
			AgentInstanceID: agentInst.String,
			ThreadID:        threadID.String,
			TurnID:          turnID.String,
			WorkflowID:      workflowID.String,
			RunID:           runID.String,
			StepID:          stepID.String,
			OperationID:     operationID.String,
		},
		Sensitivity:   policy.NormalizeSensitivity(policy.Sensitivity(sensitivity.String)),
		CorrelationID: correlation.String,
		CausationID:   causation.String,
	}
	if parsed, err := time.Parse(time.RFC3339Nano, ts); err == nil {
		record.Time = parsed
	}
	if attributes.Valid {
		decoded, err := eventcodec.DecodeAttributes([]byte(attributes.String))
		if err != nil {
			return event.StoredRecord{}, err
		}
		record.Attributes = decoded
	}
	if payload.Valid {
		decoded, err := eventcodec.DecodePayload(s.registry, record.Name, []byte(payload.String))
		if err != nil {
			return event.StoredRecord{}, err
		}
		record.Payload = decoded
	}
	stored.Record = record
	return stored, nil
}

func nullString(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func nullBytes(value []byte) any {
	if len(value) == 0 {
		return nil
	}
	return string(value)
}

const schemaSQL = `
PRAGMA journal_mode = WAL;
PRAGMA busy_timeout = 5000;

CREATE TABLE IF NOT EXISTS events (
	global_seq        INTEGER PRIMARY KEY AUTOINCREMENT,
	stream            TEXT    NOT NULL,
	stream_seq        INTEGER NOT NULL,
	id                TEXT    NOT NULL UNIQUE,
	name              TEXT    NOT NULL,
	schema_version    INTEGER NOT NULL DEFAULT 1,
	ts                TEXT    NOT NULL,
	payload           TEXT,
	attributes        TEXT,
	sensitivity       TEXT,
	source_component  TEXT,
	source_instance   TEXT,
	tenant_id         TEXT,
	app_id            TEXT,
	session_id        TEXT,
	user_id           TEXT,
	channel_id        TEXT,
	agent_id          TEXT,
	agent_instance_id TEXT,
	thread_id         TEXT,
	turn_id           TEXT,
	workflow_id       TEXT,
	run_id            TEXT,
	step_id           TEXT,
	operation_id      TEXT,
	correlation_id    TEXT,
	causation_id      TEXT,
	UNIQUE(stream, stream_seq)
);

CREATE INDEX IF NOT EXISTS idx_events_stream ON events(stream, stream_seq);
CREATE INDEX IF NOT EXISTS idx_events_name ON events(name);
CREATE INDEX IF NOT EXISTS idx_events_thread ON events(thread_id, stream_seq)
	WHERE thread_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_events_correlation ON events(correlation_id)
	WHERE correlation_id IS NOT NULL;
`

const insertSQL = `
INSERT INTO events (
	stream, stream_seq, id, name, schema_version, ts, payload, attributes,
	sensitivity, source_component, source_instance,
	tenant_id, app_id, session_id, user_id, channel_id,
	agent_id, agent_instance_id, thread_id, turn_id,
	workflow_id, run_id, step_id, operation_id,
	correlation_id, causation_id
) VALUES (
	?, ?, ?, ?, ?, ?, ?, ?,
	?, ?, ?,
	?, ?, ?, ?, ?,
	?, ?, ?, ?,
	?, ?, ?, ?,
	?, ?
)`

const selectSQL = `
SELECT
	stream, stream_seq, id, name, schema_version, ts, payload, attributes,
	sensitivity, source_component, source_instance,
	tenant_id, app_id, session_id, user_id, channel_id,
	agent_id, agent_instance_id, thread_id, turn_id,
	workflow_id, run_id, step_id, operation_id,
	correlation_id, causation_id
FROM events
WHERE stream = ?`
