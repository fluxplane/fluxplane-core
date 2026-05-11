package sqleventstore

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/fluxplane/agentruntime/core/event"
	"github.com/fluxplane/agentruntime/core/policy"
	"github.com/fluxplane/agentruntime/runtime/eventcodec"
	_ "modernc.org/sqlite"
)

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
		db.Close()
		return nil, err
	}
	store.ownedDB = true
	return store, nil
}

// OpenDB wraps an existing database. The caller owns db.
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
	for _, request := range requests {
		if request.Stream == "" {
			return nil, fmt.Errorf("sqleventstore: stream is empty")
		}
		if _, exists := seen[request.Stream]; exists {
			return nil, fmt.Errorf("sqleventstore: duplicate stream %q in append batch", request.Stream)
		}
		seen[request.Stream] = struct{}{}
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("sqleventstore: begin tx: %w", err)
	}
	defer tx.Rollback()

	stmt, err := tx.PrepareContext(ctx, insertSQL)
	if err != nil {
		return nil, fmt.Errorf("sqleventstore: prepare insert: %w", err)
	}
	defer stmt.Close()

	now := time.Now().UTC()
	results := make([]event.AppendResult, 0, len(requests))
	for _, request := range requests {
		result, err := s.appendRequest(ctx, tx, stmt, now, request)
		if err != nil {
			return nil, err
		}
		results = append(results, result)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("sqleventstore: commit: %w", err)
	}
	return results, nil
}

func (s *Store) appendRequest(ctx context.Context, tx *sql.Tx, stmt *sql.Stmt, now time.Time, request event.AppendRequest) (event.AppendResult, error) {
	stream := request.Stream
	if stream == "" {
		return event.AppendResult{}, fmt.Errorf("sqleventstore: stream is empty")
	}
	next, err := nextSequence(ctx, tx, stream)
	if err != nil {
		return event.AppendResult{}, err
	}
	actual := next - 1
	if request.Options.CheckExpectedSequence && request.Options.ExpectedSequence != actual {
		return event.AppendResult{}, event.AppendConflict{
			Stream:   stream,
			Expected: request.Options.ExpectedSequence,
			Actual:   actual,
		}
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
			return event.AppendResult{}, fmt.Errorf("sqleventstore: insert: %w", err)
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
	defer rows.Close()

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

func nextSequence(ctx context.Context, tx *sql.Tx, stream event.StreamID) (event.Sequence, error) {
	var max sql.NullInt64
	if err := tx.QueryRowContext(ctx, `SELECT MAX(stream_seq) FROM events WHERE stream = ?`, string(stream)).Scan(&max); err != nil {
		return 0, fmt.Errorf("sqleventstore: next sequence: %w", err)
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
