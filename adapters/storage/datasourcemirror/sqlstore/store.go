package sqlstore

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	coredatasource "github.com/fluxplane/fluxplane-core/core/datasource"
	"github.com/fluxplane/fluxplane-core/runtime/datasource/mirror"
)

type Dialect string

const (
	DialectSQLite Dialect = "sqlite"
	DialectMySQL  Dialect = "mysql"
)

type Store struct {
	db      *sql.DB
	dialect Dialect
}

var _ mirror.Store = (*Store)(nil)

func OpenDB(ctx context.Context, db *sql.DB, dialect Dialect) (*Store, error) {
	if db == nil {
		return nil, fmt.Errorf("datasource mirror sqlstore: db is nil")
	}
	switch dialect {
	case DialectSQLite, DialectMySQL:
	default:
		return nil, fmt.Errorf("datasource mirror sqlstore: unsupported dialect %q", dialect)
	}
	store := &Store{db: db, dialect: dialect}
	if err := store.init(ctx); err != nil {
		return nil, err
	}
	return store, nil
}

func (s *Store) init(ctx context.Context) error {
	for _, stmt := range schemaStatements(s.dialect) {
		if _, err := s.db.ExecContext(ctx, stmt); err != nil {
			if s.dialect == DialectMySQL && isDuplicateIndexError(err) {
				continue
			}
			return fmt.Errorf("datasource mirror sqlstore: schema: %w", err)
		}
	}
	return nil
}

func (s *Store) UpsertRecord(ctx context.Context, record mirror.Record) error {
	return s.UpsertRecords(ctx, record)
}

func (s *Store) UpsertRecords(ctx context.Context, records ...mirror.Record) error {
	if s == nil {
		return fmt.Errorf("datasource mirror sqlstore: store is nil")
	}
	if len(records) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer rollback(tx)
	for _, record := range records {
		if err := s.upsertRecordTx(ctx, tx, record); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) upsertRecordTx(ctx context.Context, tx *sql.Tx, record mirror.Record) error {
	if strings.TrimSpace(record.Key) == "" {
		record.Key = mirror.DocumentKey(record.Ref)
	}
	if record.Key == "" {
		return fmt.Errorf("datasource mirror sqlstore: record key is empty")
	}
	hash := keyHash(record.Key)
	if _, err := tx.ExecContext(ctx, s.upsertRecordSQL(),
		hash,
		record.Key,
		string(record.Ref.Datasource),
		string(record.Ref.Entity),
		record.Ref.ID,
		record.Title,
		record.Content,
		record.URL,
		mustJSON(record.Fields),
		mustJSON(record.Search),
		mustJSON(record.Identifiers),
		mustJSON(record.Filters),
	); err != nil {
		return fmt.Errorf("datasource mirror sqlstore: upsert record: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM datasource_mirror_filter WHERE record_hash = ?`, hash); err != nil {
		return fmt.Errorf("datasource mirror sqlstore: clear filters: %w", err)
	}
	for name, values := range record.Filters {
		for _, value := range values {
			value = strings.TrimSpace(value)
			if value == "" {
				continue
			}
			if _, err := tx.ExecContext(ctx, s.insertFilterSQL(),
				hash,
				string(record.Ref.Datasource),
				string(record.Ref.Entity),
				name,
				truncate(mirror.NormalizeText(value), 191),
				value,
			); err != nil {
				return fmt.Errorf("datasource mirror sqlstore: insert filter: %w", err)
			}
		}
	}
	return nil
}

func (s *Store) DeleteRecord(ctx context.Context, ref coredatasource.RecordRef) error {
	key := mirror.DocumentKey(ref)
	if key == "" {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer rollback(tx)
	hash := keyHash(key)
	if _, err := tx.ExecContext(ctx, `DELETE FROM datasource_mirror_filter WHERE record_hash = ?`, hash); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM datasource_mirror_record WHERE record_hash = ?`, hash); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) Record(ctx context.Context, ref coredatasource.RecordRef) (mirror.Record, bool, error) {
	key := mirror.DocumentKey(ref)
	if key == "" {
		return mirror.Record{}, false, nil
	}
	rows, err := s.db.QueryContext(ctx, selectRecordSQL+` WHERE record_hash = ?`, keyHash(key))
	if err != nil {
		return mirror.Record{}, false, err
	}
	defer func() { _ = rows.Close() }()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return mirror.Record{}, false, err
		}
		return mirror.Record{}, false, nil
	}
	record, err := scanRecord(rows)
	if err != nil {
		return mirror.Record{}, false, err
	}
	return record, true, rows.Err()
}

func (s *Store) SearchRecords(ctx context.Context, req mirror.SearchRequest) ([]mirror.Hit, error) {
	if strings.TrimSpace(req.Query) == "" && len(req.Filters) == 0 {
		return s.searchRecordsPaged(ctx, req)
	}
	query, args := s.searchSQL(req, false)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	records := map[string]mirror.Record{}
	for rows.Next() {
		record, err := scanRecord(rows)
		if err != nil {
			return nil, err
		}
		records[record.Key] = record
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return mirror.SearchRecords(ctx, records, req)
}

func (s *Store) searchRecordsPaged(ctx context.Context, req mirror.SearchRequest) ([]mirror.Hit, error) {
	query, args := s.searchSQL(req, true)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var hits []mirror.Hit
	for rows.Next() {
		record, err := scanRecord(rows)
		if err != nil {
			return nil, err
		}
		hits = append(hits, mirror.Hit{Record: mirror.RecordToDatasourceRecord(record), Score: 1, Reason: "all"})
	}
	return hits, rows.Err()
}

func (s *Store) RecordStatus(ctx context.Context, req mirror.StatusRequest) ([]mirror.RecordState, error) {
	where, args := statusWhere(req, "r")
	rows, err := s.db.QueryContext(ctx, `SELECT r.record_key, r.datasource, r.entity, r.record_id FROM datasource_mirror_record r`+where+` ORDER BY r.record_key`, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []mirror.RecordState
	for rows.Next() {
		var state mirror.RecordState
		var datasource, entity string
		if err := rows.Scan(&state.Key, &datasource, &entity, &state.Ref.ID); err != nil {
			return nil, err
		}
		state.Ref.Datasource = coredatasource.Name(datasource)
		state.Ref.Entity = coredatasource.EntityType(entity)
		out = append(out, state)
	}
	return out, rows.Err()
}

func (s *Store) PutRun(ctx context.Context, run mirror.RunState) error {
	run.Key = mirror.RunStorageKey(mirror.RunKey{Datasource: run.Datasource, Entity: run.Entity, Phase: run.Phase})
	if run.Key == "" {
		return nil
	}
	_, err := s.db.ExecContext(ctx, s.upsertRunSQL(),
		keyHash(run.Key),
		run.Key,
		string(run.Datasource),
		string(run.Entity),
		phase(run.Phase),
		run.Status,
		formatTime(run.StartedAt),
		formatTime(run.CompletedAt),
		run.Documents,
		run.Indexed,
		run.Queued,
		run.Skipped,
		run.Deleted,
		run.Failed,
		run.LastError,
	)
	if err != nil {
		return fmt.Errorf("datasource mirror sqlstore: upsert run: %w", err)
	}
	return nil
}

func (s *Store) Run(ctx context.Context, key mirror.RunKey) (mirror.RunState, bool, error) {
	runKey := mirror.RunStorageKey(key)
	if runKey == "" {
		return mirror.RunState{}, false, nil
	}
	rows, err := s.db.QueryContext(ctx, selectRunSQL+` WHERE run_hash = ?`, keyHash(runKey))
	if err != nil {
		return mirror.RunState{}, false, err
	}
	defer func() { _ = rows.Close() }()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return mirror.RunState{}, false, err
		}
		return mirror.RunState{}, false, nil
	}
	run, err := scanRun(rows)
	if err != nil {
		return mirror.RunState{}, false, err
	}
	return run, true, rows.Err()
}

func (s *Store) Runs(ctx context.Context, req mirror.StatusRequest) ([]mirror.RunState, error) {
	where, args := statusWhere(req, "r")
	rows, err := s.db.QueryContext(ctx, selectRunSQL+where+` ORDER BY r.run_key`, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []mirror.RunState
	for rows.Next() {
		run, err := scanRun(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, run)
	}
	return out, rows.Err()
}

func (s *Store) DeleteRuns(ctx context.Context, req mirror.StatusRequest) error {
	where, args := statusWhere(req, "")
	_, err := s.db.ExecContext(ctx, `DELETE FROM datasource_mirror_run`+where, args...)
	return err
}

func (s *Store) searchSQL(req mirror.SearchRequest, paginate bool) (string, []any) {
	var joins []string
	var joinArgs []any
	var where []string
	var whereArgs []any
	for i, datasource := range req.Datasources {
		if i == 0 {
			where = append(where, "r.datasource IN ("+placeholders(len(req.Datasources))+")")
		}
		whereArgs = append(whereArgs, string(datasource))
	}
	for i, entity := range req.Entities {
		if i == 0 {
			where = append(where, "r.entity IN ("+placeholders(len(req.Entities))+")")
		}
		whereArgs = append(whereArgs, string(entity))
	}
	filterIndex := 0
	for name, want := range req.Filters {
		want = mirror.NormalizeText(want)
		if want == "" {
			continue
		}
		alias := fmt.Sprintf("f%d", filterIndex)
		filterIndex++
		joins = append(joins, fmt.Sprintf(` JOIN datasource_mirror_filter %s ON %s.record_hash = r.record_hash AND %s.datasource = r.datasource AND %s.entity = r.entity AND %s.field_name = ? AND %s.value_norm = ?`, alias, alias, alias, alias, alias, alias))
		joinArgs = append(joinArgs, name, truncate(want, 191))
	}
	query := selectRecordSQL + strings.Join(joins, "")
	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}
	query += " ORDER BY r.record_key"
	if paginate {
		limit := req.Limit
		if limit <= 0 {
			limit = 10
		}
		offset := req.Offset
		if offset < 0 {
			offset = 0
		}
		query += " LIMIT ? OFFSET ?"
		whereArgs = append(whereArgs, limit, offset)
	}
	return query, append(joinArgs, whereArgs...)
}

func (s *Store) upsertRecordSQL() string {
	base := `INSERT INTO datasource_mirror_record
(record_hash, record_key, datasource, entity, record_id, title, content, url, fields_json, search_json, identifiers_json, filters_json)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	switch s.dialect {
	case DialectMySQL:
		return base + ` ON DUPLICATE KEY UPDATE record_key=VALUES(record_key), datasource=VALUES(datasource), entity=VALUES(entity), record_id=VALUES(record_id), title=VALUES(title), content=VALUES(content), url=VALUES(url), fields_json=VALUES(fields_json), search_json=VALUES(search_json), identifiers_json=VALUES(identifiers_json), filters_json=VALUES(filters_json)`
	default:
		return base + ` ON CONFLICT(record_hash) DO UPDATE SET record_key=excluded.record_key, datasource=excluded.datasource, entity=excluded.entity, record_id=excluded.record_id, title=excluded.title, content=excluded.content, url=excluded.url, fields_json=excluded.fields_json, search_json=excluded.search_json, identifiers_json=excluded.identifiers_json, filters_json=excluded.filters_json`
	}
}

func (s *Store) insertFilterSQL() string {
	base := `INSERT INTO datasource_mirror_filter (record_hash, datasource, entity, field_name, value_norm, value_text) VALUES (?, ?, ?, ?, ?, ?)`
	if s.dialect == DialectMySQL {
		return base + ` ON DUPLICATE KEY UPDATE value_text=VALUES(value_text)`
	}
	return base + ` ON CONFLICT(record_hash, field_name, value_norm) DO UPDATE SET value_text=excluded.value_text`
}

func (s *Store) upsertRunSQL() string {
	base := `INSERT INTO datasource_mirror_run
(run_hash, run_key, datasource, entity, phase, status, started_at, completed_at, documents, indexed, queued, skipped, deleted, failed, last_error)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	if s.dialect == DialectMySQL {
		return base + ` ON DUPLICATE KEY UPDATE run_key=VALUES(run_key), datasource=VALUES(datasource), entity=VALUES(entity), phase=VALUES(phase), status=VALUES(status), started_at=VALUES(started_at), completed_at=VALUES(completed_at), documents=VALUES(documents), indexed=VALUES(indexed), queued=VALUES(queued), skipped=VALUES(skipped), deleted=VALUES(deleted), failed=VALUES(failed), last_error=VALUES(last_error)`
	}
	return base + ` ON CONFLICT(run_hash) DO UPDATE SET run_key=excluded.run_key, datasource=excluded.datasource, entity=excluded.entity, phase=excluded.phase, status=excluded.status, started_at=excluded.started_at, completed_at=excluded.completed_at, documents=excluded.documents, indexed=excluded.indexed, queued=excluded.queued, skipped=excluded.skipped, deleted=excluded.deleted, failed=excluded.failed, last_error=excluded.last_error`
}

const selectRecordSQL = `SELECT r.record_key, r.datasource, r.entity, r.record_id, r.title, r.content, r.url, r.fields_json, r.search_json, r.identifiers_json, r.filters_json FROM datasource_mirror_record r`

const selectRunSQL = `SELECT r.run_key, r.datasource, r.entity, r.phase, r.status, r.started_at, r.completed_at, r.documents, r.indexed, r.queued, r.skipped, r.deleted, r.failed, r.last_error FROM datasource_mirror_run r`

func scanRecord(rows interface{ Scan(...any) error }) (mirror.Record, error) {
	var record mirror.Record
	var datasource, entity string
	var fieldsJSON, searchJSON, identifiersJSON, filtersJSON string
	if err := rows.Scan(&record.Key, &datasource, &entity, &record.Ref.ID, &record.Title, &record.Content, &record.URL, &fieldsJSON, &searchJSON, &identifiersJSON, &filtersJSON); err != nil {
		return mirror.Record{}, err
	}
	record.Ref.Datasource = coredatasource.Name(datasource)
	record.Ref.Entity = coredatasource.EntityType(entity)
	if err := json.Unmarshal([]byte(fieldsJSON), &record.Fields); err != nil {
		return mirror.Record{}, err
	}
	if err := json.Unmarshal([]byte(searchJSON), &record.Search); err != nil {
		return mirror.Record{}, err
	}
	if err := json.Unmarshal([]byte(identifiersJSON), &record.Identifiers); err != nil {
		return mirror.Record{}, err
	}
	if err := json.Unmarshal([]byte(filtersJSON), &record.Filters); err != nil {
		return mirror.Record{}, err
	}
	return record, nil
}

func scanRun(rows interface{ Scan(...any) error }) (mirror.RunState, error) {
	var run mirror.RunState
	var datasource, entity, started, completed string
	if err := rows.Scan(&run.Key, &datasource, &entity, &run.Phase, &run.Status, &started, &completed, &run.Documents, &run.Indexed, &run.Queued, &run.Skipped, &run.Deleted, &run.Failed, &run.LastError); err != nil {
		return mirror.RunState{}, err
	}
	run.Datasource = coredatasource.Name(datasource)
	run.Entity = coredatasource.EntityType(entity)
	run.StartedAt = parseTime(started)
	run.CompletedAt = parseTime(completed)
	return run, nil
}

func statusWhere(req mirror.StatusRequest, alias string) (string, []any) {
	var where []string
	var args []any
	prefix := ""
	if alias != "" {
		prefix = alias + "."
	}
	if req.Datasource != "" {
		where = append(where, prefix+"datasource = ?")
		args = append(args, string(req.Datasource))
	}
	if req.Entity != "" {
		where = append(where, prefix+"entity = ?")
		args = append(args, string(req.Entity))
	}
	if len(where) == 0 {
		return "", nil
	}
	return " WHERE " + strings.Join(where, " AND "), args
}

func schemaStatements(dialect Dialect) []string {
	textType := "TEXT"
	if dialect == DialectMySQL {
		textType = "LONGTEXT"
	}
	recordTable := `CREATE TABLE IF NOT EXISTS datasource_mirror_record (
record_hash CHAR(64) PRIMARY KEY,
record_key ` + textType + ` NOT NULL,
datasource VARCHAR(191) NOT NULL,
entity VARCHAR(191) NOT NULL,
record_id VARCHAR(191) NOT NULL,
title ` + textType + `,
content ` + textType + `,
url ` + textType + `,
fields_json ` + textType + ` NOT NULL,
search_json ` + textType + ` NOT NULL,
identifiers_json ` + textType + ` NOT NULL,
filters_json ` + textType + ` NOT NULL
)`
	filterTable := `CREATE TABLE IF NOT EXISTS datasource_mirror_filter (
record_hash CHAR(64) NOT NULL,
datasource VARCHAR(191) NOT NULL,
entity VARCHAR(191) NOT NULL,
field_name VARCHAR(191) NOT NULL,
value_norm VARCHAR(191) NOT NULL,
value_text ` + textType + `,
PRIMARY KEY (record_hash, field_name, value_norm)
)`
	runTable := `CREATE TABLE IF NOT EXISTS datasource_mirror_run (
run_hash CHAR(64) PRIMARY KEY,
run_key ` + textType + ` NOT NULL,
datasource VARCHAR(191) NOT NULL,
entity VARCHAR(191) NOT NULL,
phase VARCHAR(64) NOT NULL,
status VARCHAR(64) NOT NULL,
started_at VARCHAR(64),
completed_at VARCHAR(64),
documents INTEGER NOT NULL DEFAULT 0,
indexed INTEGER NOT NULL DEFAULT 0,
queued INTEGER NOT NULL DEFAULT 0,
skipped INTEGER NOT NULL DEFAULT 0,
deleted INTEGER NOT NULL DEFAULT 0,
failed INTEGER NOT NULL DEFAULT 0,
last_error ` + textType + `
)`
	if dialect == DialectMySQL {
		return []string{
			recordTable,
			`CREATE INDEX datasource_mirror_record_entity ON datasource_mirror_record (datasource, entity, record_id)`,
			`CREATE INDEX datasource_mirror_record_scan ON datasource_mirror_record (datasource, entity, record_key(191))`,
			filterTable,
			`CREATE INDEX datasource_mirror_filter_lookup ON datasource_mirror_filter (datasource, entity, field_name, value_norm)`,
			runTable,
			`CREATE INDEX datasource_mirror_run_entity ON datasource_mirror_run (datasource, entity, phase)`,
		}
	}
	return []string{
		recordTable,
		`CREATE INDEX IF NOT EXISTS datasource_mirror_record_entity ON datasource_mirror_record (datasource, entity, record_id)`,
		`CREATE INDEX IF NOT EXISTS datasource_mirror_record_scan ON datasource_mirror_record (datasource, entity, record_key)`,
		filterTable,
		`CREATE INDEX IF NOT EXISTS datasource_mirror_filter_lookup ON datasource_mirror_filter (datasource, entity, field_name, value_norm)`,
		runTable,
		`CREATE INDEX IF NOT EXISTS datasource_mirror_run_entity ON datasource_mirror_run (datasource, entity, phase)`,
	}
}

func keyHash(key string) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:])
}

func mustJSON(value any) string {
	data, err := json.Marshal(value)
	if err != nil {
		return "{}"
	}
	return string(data)
}

func placeholders(n int) string {
	values := make([]string, n)
	for i := range values {
		values[i] = "?"
	}
	return strings.Join(values, ", ")
}

func formatTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339Nano)
}

func parseTime(value string) time.Time {
	if strings.TrimSpace(value) == "" {
		return time.Time{}
	}
	t, _ := time.Parse(time.RFC3339Nano, value)
	return t
}

func phase(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "all"
	}
	return value
}

func truncate(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	return value[:limit]
}

func isDuplicateIndexError(err error) bool {
	message := err.Error()
	return strings.Contains(message, "Error 1061") || strings.Contains(message, "Duplicate key name")
}

func rollback(tx *sql.Tx) {
	_ = tx.Rollback()
}
