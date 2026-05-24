package sqlstore

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	coredata "github.com/fluxplane/fluxplane-core/core/data"
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

var _ coredata.Store = (*Store)(nil)

func OpenDB(ctx context.Context, db *sql.DB, dialect Dialect) (*Store, error) {
	if db == nil {
		return nil, fmt.Errorf("data sqlstore: db is nil")
	}
	if dialect != DialectSQLite && dialect != DialectMySQL {
		return nil, fmt.Errorf("data sqlstore: unsupported dialect %q", dialect)
	}
	store := &Store{db: db, dialect: dialect}
	for _, stmt := range schemaStatements(dialect) {
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			if dialect == DialectMySQL && isDuplicateIndexError(err) {
				continue
			}
			return nil, fmt.Errorf("data sqlstore: schema: %w", err)
		}
	}
	return store, nil
}

func (s *Store) UpsertRecords(ctx context.Context, records ...coredata.Record) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer rollback(tx)
	for _, record := range records {
		if recordHash(record.Scope, record.Ref) == "" {
			return fmt.Errorf("data sqlstore: record ref is incomplete")
		}
		if _, err := tx.ExecContext(ctx, s.upsertRecordSQL(),
			recordHash(record.Scope, record.Ref),
			scopeHash(record.Scope),
			string(record.Ref.Source),
			string(record.Ref.Entity),
			string(record.Ref.View),
			string(record.Ref.ID),
			mustJSON(record.Scope),
			record.Title,
			record.Content,
			record.URL,
			mustJSON(record.Fields),
			mustJSON(record.Relations),
			mustJSON(record.BlobRefs),
			mustJSON(record.Raw),
			mustJSON(record.Metadata),
			record.Fingerprint,
			record.UpdatedAt,
		); err != nil {
			return fmt.Errorf("data sqlstore: upsert record: %w", err)
		}
		hash := recordHash(record.Scope, record.Ref)
		if _, err := tx.ExecContext(ctx, `DELETE FROM data_store_record_field WHERE record_hash = ?`, hash); err != nil {
			return fmt.Errorf("data sqlstore: delete record fields: %w", err)
		}
		for field, values := range record.Fields {
			for _, value := range values {
				value = strings.TrimSpace(value)
				if strings.TrimSpace(field) == "" || value == "" {
					continue
				}
				if _, err := tx.ExecContext(ctx, `INSERT INTO data_store_record_field (record_hash, field_name, value_norm, value_text) VALUES (?, ?, ?, ?)`, hash, strings.TrimSpace(field), normalizedIndexValue(value), value); err != nil {
					return fmt.Errorf("data sqlstore: insert record field: %w", err)
				}
			}
		}
	}
	return tx.Commit()
}

func (s *Store) DeleteRecords(ctx context.Context, scope coredata.Scope, refs ...coredata.Ref) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer rollback(tx)
	for _, ref := range refs {
		rows, err := tx.QueryContext(ctx, selectRecordSQL+refWhere(ref), refWhereArgs(ref)...)
		if err != nil {
			return err
		}
		var hashes []string
		for rows.Next() {
			record, err := scanRecord(rows)
			if err != nil {
				_ = rows.Close()
				return err
			}
			if record.Scope.Matches(scope) {
				hashes = append(hashes, recordHash(record.Scope, record.Ref))
			}
		}
		if err := rows.Close(); err != nil {
			return err
		}
		for _, hash := range hashes {
			if _, err := tx.ExecContext(ctx, `DELETE FROM data_store_record_field WHERE record_hash = ?`, hash); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `DELETE FROM data_store_record WHERE record_hash = ?`, hash); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}

func (s *Store) GetRecord(ctx context.Context, scope coredata.Scope, ref coredata.Ref) (coredata.Record, bool, error) {
	records, err := s.BatchGetRecords(ctx, scope, ref)
	if err != nil {
		return coredata.Record{}, false, err
	}
	if len(records) == 0 {
		return coredata.Record{}, false, nil
	}
	return records[0], true, nil
}

func (s *Store) BatchGetRecords(ctx context.Context, scope coredata.Scope, refs ...coredata.Ref) ([]coredata.Record, error) {
	var out []coredata.Record
	for _, ref := range refs {
		rows, err := s.db.QueryContext(ctx, selectRecordSQL+refWhere(ref), refWhereArgs(ref)...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			record, err := scanRecord(rows)
			if err != nil {
				_ = rows.Close()
				return nil, err
			}
			if record.Scope.Matches(scope) {
				out = append(out, record)
				break
			}
		}
		if err := rows.Close(); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func (s *Store) QueryRecords(ctx context.Context, req coredata.Query) (coredata.QueryResult, error) {
	rows, err := s.db.QueryContext(ctx, selectRecordSQL+queryWhere(req), queryWhereArgs(req)...)
	if err != nil {
		return coredata.QueryResult{}, err
	}
	defer func() { _ = rows.Close() }()
	var records []coredata.Record
	for rows.Next() {
		record, err := scanRecord(rows)
		if err != nil {
			return coredata.QueryResult{}, err
		}
		if matchesRecord(record, req) {
			records = append(records, record)
		}
	}
	if err := rows.Err(); err != nil {
		return coredata.QueryResult{}, err
	}
	sort.Slice(records, func(i, j int) bool {
		return recordHash(records[i].Scope, records[i].Ref) < recordHash(records[j].Scope, records[j].Ref)
	})
	return paginateRecords(records, req.Limit, req.Cursor)
}

func (s *Store) UpsertRelations(ctx context.Context, relations ...coredata.Relation) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer rollback(tx)
	for _, relation := range relations {
		hash := relationHash(relation)
		if hash == "" {
			return fmt.Errorf("data sqlstore: relation ref is incomplete")
		}
		if _, err := tx.ExecContext(ctx, s.upsertRelationSQL(),
			hash,
			scopeHash(relation.Scope),
			string(relation.Source.Source),
			string(relation.Source.Entity),
			string(relation.Source.View),
			string(relation.Source.ID),
			string(relation.Name),
			string(relation.Target.Source),
			string(relation.Target.Entity),
			string(relation.Target.View),
			string(relation.Target.ID),
			mustJSON(relation.Scope),
			mustJSON(relation.Summary),
			mustJSON(relation.Metadata),
		); err != nil {
			return fmt.Errorf("data sqlstore: upsert relation: %w", err)
		}
	}
	return tx.Commit()
}

func (s *Store) QueryRelations(ctx context.Context, req coredata.RelationQuery) (coredata.RelationResult, error) {
	rows, err := s.db.QueryContext(ctx, selectRelationSQL+relationWhere(req), relationWhereArgs(req)...)
	if err != nil {
		return coredata.RelationResult{}, err
	}
	defer func() { _ = rows.Close() }()
	var relations []coredata.Relation
	for rows.Next() {
		relation, err := scanRelation(rows)
		if err != nil {
			return coredata.RelationResult{}, err
		}
		if matchesRelation(relation, req) {
			relations = append(relations, relation)
		}
	}
	if err := rows.Err(); err != nil {
		return coredata.RelationResult{}, err
	}
	sort.Slice(relations, func(i, j int) bool { return relationHash(relations[i]) < relationHash(relations[j]) })
	return paginateRelations(relations, req.Limit, req.Cursor)
}

func (s *Store) PutBlob(ctx context.Context, blob coredata.Blob) (coredata.BlobRef, error) {
	ref := blob.Ref
	if ref.ID == "" {
		ref.ID = coredata.BlobID(contentHash(blob.Content))
	}
	ref.Size = int64(len(blob.Content))
	ref.Digest = "sha256:" + contentHash(blob.Content)
	_, err := s.db.ExecContext(ctx, s.upsertBlobSQL(),
		blobHash(ref.Scope, ref.ID),
		string(ref.ID),
		scopeHash(ref.Scope),
		mustJSON(ref.Scope),
		ref.MediaType,
		ref.Size,
		ref.Digest,
		mustJSON(ref.Metadata),
		blob.Content,
	)
	return ref, err
}

func (s *Store) GetBlob(ctx context.Context, ref coredata.BlobRef) (coredata.Blob, bool, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT scope_json, media_type, size_bytes, digest, metadata_json, content FROM data_store_blob WHERE blob_id = ?`, string(ref.ID))
	if err != nil {
		return coredata.Blob{}, false, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		blob := coredata.Blob{Ref: coredata.BlobRef{ID: ref.ID}}
		var scopeJSON, metadataJSON string
		if err := rows.Scan(&scopeJSON, &blob.Ref.MediaType, &blob.Ref.Size, &blob.Ref.Digest, &metadataJSON, &blob.Content); err != nil {
			return coredata.Blob{}, false, err
		}
		if err := json.Unmarshal([]byte(scopeJSON), &blob.Ref.Scope); err != nil {
			return coredata.Blob{}, false, err
		}
		if err := json.Unmarshal([]byte(metadataJSON), &blob.Ref.Metadata); err != nil {
			return coredata.Blob{}, false, err
		}
		if blob.Ref.Scope.Matches(ref.Scope) {
			return blob, true, nil
		}
	}
	if err := rows.Err(); err != nil {
		return coredata.Blob{}, false, err
	}
	return coredata.Blob{}, false, nil
}

func (s *Store) upsertRecordSQL() string {
	base := `INSERT INTO data_store_record (record_hash, scope_hash, source, entity, view_name, record_id, scope_json, title, content, url, fields_json, relations_json, blob_refs_json, raw_json, metadata_json, fingerprint, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	if s.dialect == DialectMySQL {
		return base + ` ON DUPLICATE KEY UPDATE scope_hash=VALUES(scope_hash), source=VALUES(source), entity=VALUES(entity), view_name=VALUES(view_name), record_id=VALUES(record_id), scope_json=VALUES(scope_json), title=VALUES(title), content=VALUES(content), url=VALUES(url), fields_json=VALUES(fields_json), relations_json=VALUES(relations_json), blob_refs_json=VALUES(blob_refs_json), raw_json=VALUES(raw_json), metadata_json=VALUES(metadata_json), fingerprint=VALUES(fingerprint), updated_at=VALUES(updated_at)`
	}
	return base + ` ON CONFLICT(record_hash) DO UPDATE SET scope_hash=excluded.scope_hash, source=excluded.source, entity=excluded.entity, view_name=excluded.view_name, record_id=excluded.record_id, scope_json=excluded.scope_json, title=excluded.title, content=excluded.content, url=excluded.url, fields_json=excluded.fields_json, relations_json=excluded.relations_json, blob_refs_json=excluded.blob_refs_json, raw_json=excluded.raw_json, metadata_json=excluded.metadata_json, fingerprint=excluded.fingerprint, updated_at=excluded.updated_at`
}

func (s *Store) upsertRelationSQL() string {
	base := `INSERT INTO data_store_relation (relation_hash, scope_hash, source, source_entity, source_view, source_id, relation_name, target_source, target_entity, target_view, target_id, scope_json, summary_json, metadata_json) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	if s.dialect == DialectMySQL {
		return base + ` ON DUPLICATE KEY UPDATE scope_hash=VALUES(scope_hash), source=VALUES(source), source_entity=VALUES(source_entity), source_view=VALUES(source_view), source_id=VALUES(source_id), relation_name=VALUES(relation_name), target_source=VALUES(target_source), target_entity=VALUES(target_entity), target_view=VALUES(target_view), target_id=VALUES(target_id), scope_json=VALUES(scope_json), summary_json=VALUES(summary_json), metadata_json=VALUES(metadata_json)`
	}
	return base + ` ON CONFLICT(relation_hash) DO UPDATE SET scope_hash=excluded.scope_hash, source=excluded.source, source_entity=excluded.source_entity, source_view=excluded.source_view, source_id=excluded.source_id, relation_name=excluded.relation_name, target_source=excluded.target_source, target_entity=excluded.target_entity, target_view=excluded.target_view, target_id=excluded.target_id, scope_json=excluded.scope_json, summary_json=excluded.summary_json, metadata_json=excluded.metadata_json`
}

func (s *Store) upsertBlobSQL() string {
	base := `INSERT INTO data_store_blob (blob_hash, blob_id, scope_hash, scope_json, media_type, size_bytes, digest, metadata_json, content) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`
	if s.dialect == DialectMySQL {
		return base + ` ON DUPLICATE KEY UPDATE blob_id=VALUES(blob_id), scope_hash=VALUES(scope_hash), scope_json=VALUES(scope_json), media_type=VALUES(media_type), size_bytes=VALUES(size_bytes), digest=VALUES(digest), metadata_json=VALUES(metadata_json), content=VALUES(content)`
	}
	return base + ` ON CONFLICT(blob_hash) DO UPDATE SET blob_id=excluded.blob_id, scope_hash=excluded.scope_hash, scope_json=excluded.scope_json, media_type=excluded.media_type, size_bytes=excluded.size_bytes, digest=excluded.digest, metadata_json=excluded.metadata_json, content=excluded.content`
}

const selectRecordSQL = `SELECT scope_json, source, entity, view_name, record_id, title, content, url, fields_json, relations_json, blob_refs_json, raw_json, metadata_json, fingerprint, updated_at FROM data_store_record`

const selectRelationSQL = `SELECT scope_json, source, source_entity, source_view, source_id, relation_name, target_source, target_entity, target_view, target_id, summary_json, metadata_json FROM data_store_relation`

func scanRecord(rows interface{ Scan(...any) error }) (coredata.Record, error) {
	var record coredata.Record
	var scopeJSON, fieldsJSON, relationsJSON, blobsJSON, rawJSON, metadataJSON string
	var source, entity, view string
	if err := rows.Scan(&scopeJSON, &source, &entity, &view, &record.Ref.ID, &record.Title, &record.Content, &record.URL, &fieldsJSON, &relationsJSON, &blobsJSON, &rawJSON, &metadataJSON, &record.Fingerprint, &record.UpdatedAt); err != nil {
		return coredata.Record{}, err
	}
	record.Ref.Source = coredata.SourceName(source)
	record.Ref.Entity = coredata.EntityType(entity)
	record.Ref.View = coredata.ViewName(view)
	if err := json.Unmarshal([]byte(scopeJSON), &record.Scope); err != nil {
		return coredata.Record{}, err
	}
	_ = json.Unmarshal([]byte(fieldsJSON), &record.Fields)
	_ = json.Unmarshal([]byte(relationsJSON), &record.Relations)
	_ = json.Unmarshal([]byte(blobsJSON), &record.BlobRefs)
	_ = json.Unmarshal([]byte(rawJSON), &record.Raw)
	_ = json.Unmarshal([]byte(metadataJSON), &record.Metadata)
	return record, nil
}

func scanRelation(rows interface{ Scan(...any) error }) (coredata.Relation, error) {
	var relation coredata.Relation
	var scopeJSON, summaryJSON, metadataJSON string
	var source, sourceEntity, sourceView, relationName, targetSource, targetEntity, targetView string
	if err := rows.Scan(&scopeJSON, &source, &sourceEntity, &sourceView, &relation.Source.ID, &relationName, &targetSource, &targetEntity, &targetView, &relation.Target.ID, &summaryJSON, &metadataJSON); err != nil {
		return coredata.Relation{}, err
	}
	relation.Source.Source = coredata.SourceName(source)
	relation.Source.Entity = coredata.EntityType(sourceEntity)
	relation.Source.View = coredata.ViewName(sourceView)
	relation.Name = coredata.RelationName(relationName)
	relation.Target.Source = coredata.SourceName(targetSource)
	relation.Target.Entity = coredata.EntityType(targetEntity)
	relation.Target.View = coredata.ViewName(targetView)
	_ = json.Unmarshal([]byte(scopeJSON), &relation.Scope)
	_ = json.Unmarshal([]byte(summaryJSON), &relation.Summary)
	_ = json.Unmarshal([]byte(metadataJSON), &relation.Metadata)
	return relation, nil
}

func queryWhere(req coredata.Query) string {
	var where []string
	if len(req.Sources) > 0 {
		where = append(where, " source IN ("+placeholders(len(req.Sources))+")")
	}
	if len(req.Entities) > 0 {
		where = append(where, " entity IN ("+placeholders(len(req.Entities))+")")
	}
	if len(req.Views) > 0 {
		where = append(where, " view_name IN ("+placeholders(len(req.Views))+")")
	}
	if len(req.IDs) > 0 {
		where = append(where, " record_id IN ("+placeholders(len(req.IDs))+")")
	}
	for _, name := range sortedFilterNames(req.Filters) {
		value := strings.TrimSpace(req.Filters[name])
		if value == "" {
			continue
		}
		where = append(where, " record_hash IN (SELECT record_hash FROM data_store_record_field WHERE field_name = ? AND value_norm = ?)")
	}
	if len(where) == 0 {
		return ""
	}
	return " WHERE" + strings.Join(where, " AND")
}

func queryWhereArgs(req coredata.Query) []any {
	var args []any
	for _, value := range req.Sources {
		args = append(args, string(value))
	}
	for _, value := range req.Entities {
		args = append(args, string(value))
	}
	for _, value := range req.Views {
		args = append(args, string(value))
	}
	for _, value := range req.IDs {
		args = append(args, string(value))
	}
	for _, name := range sortedFilterNames(req.Filters) {
		value := strings.TrimSpace(req.Filters[name])
		if value == "" {
			continue
		}
		args = append(args, name, normalizedIndexValue(value))
	}
	return args
}

func refWhere(ref coredata.Ref) string {
	var where []string
	if ref.Source != "" {
		where = append(where, " source = ?")
	}
	if ref.Entity != "" {
		where = append(where, " entity = ?")
	}
	if ref.View != "" {
		where = append(where, " view_name = ?")
	}
	if ref.ID != "" {
		where = append(where, " record_id = ?")
	}
	if len(where) == 0 {
		return ""
	}
	return " WHERE" + strings.Join(where, " AND")
}

func refWhereArgs(ref coredata.Ref) []any {
	var args []any
	if ref.Source != "" {
		args = append(args, string(ref.Source))
	}
	if ref.Entity != "" {
		args = append(args, string(ref.Entity))
	}
	if ref.View != "" {
		args = append(args, string(ref.View))
	}
	if ref.ID != "" {
		args = append(args, string(ref.ID))
	}
	return args
}

func relationWhere(req coredata.RelationQuery) string {
	var where []string
	if len(req.Sources) > 0 {
		where = append(where, " source IN ("+placeholders(len(req.Sources))+")")
	}
	if len(req.Views) > 0 {
		where = append(where, " source_view IN ("+placeholders(len(req.Views))+")")
	}
	if req.Relation != "" {
		where = append(where, " relation_name = ?")
	}
	if req.Source.Source != "" {
		where = append(where, " source = ?")
	}
	if req.Source.Entity != "" {
		where = append(where, " source_entity = ?")
	}
	if req.Source.View != "" {
		where = append(where, " source_view = ?")
	}
	if req.Source.ID != "" {
		where = append(where, " source_id = ?")
	}
	if req.Target.Source != "" {
		where = append(where, " target_source = ?")
	}
	if req.Target.Entity != "" {
		where = append(where, " target_entity = ?")
	}
	if req.Target.View != "" {
		where = append(where, " target_view = ?")
	}
	if req.Target.ID != "" {
		where = append(where, " target_id = ?")
	}
	if len(where) == 0 {
		return ""
	}
	return " WHERE" + strings.Join(where, " AND")
}

func relationWhereArgs(req coredata.RelationQuery) []any {
	var args []any
	for _, value := range req.Sources {
		args = append(args, string(value))
	}
	for _, value := range req.Views {
		args = append(args, string(value))
	}
	if req.Relation != "" {
		args = append(args, string(req.Relation))
	}
	for _, value := range []string{string(req.Source.Source), string(req.Source.Entity), string(req.Source.View), string(req.Source.ID), string(req.Target.Source), string(req.Target.Entity), string(req.Target.View), string(req.Target.ID)} {
		if value != "" {
			args = append(args, value)
		}
	}
	return args
}

func matchesRecord(record coredata.Record, req coredata.Query) bool {
	if !record.Scope.Matches(req.Scope) || !matchesFilters(record.Fields, req.Filters) || !matchesRelationFilters(record, req.RelationFilters) {
		return false
	}
	if strings.TrimSpace(req.Text) != "" && !recordMatchesText(record, req.Text) {
		return false
	}
	return true
}

func matchesRelation(relation coredata.Relation, req coredata.RelationQuery) bool {
	return relation.Scope.Matches(req.Scope)
}

func matchesFilters(fields map[string][]string, filters map[string]string) bool {
	for name, want := range filters {
		want = normalize(want)
		if want == "" {
			continue
		}
		var matched bool
		for _, value := range fields[name] {
			if normalize(value) == want {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}
	return true
}

func matchesRelationFilters(record coredata.Record, filters []coredata.RelationFilter) bool {
	for _, filter := range filters {
		var matched bool
		for _, summary := range record.Relations[string(filter.Relation)] {
			if filter.Target != "" && summary.Ref.Entity != filter.Target {
				continue
			}
			if matchesSummaryFields(summary, filter.Filters) {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}
	return true
}

func matchesSummaryFields(summary coredata.Summary, filters map[string]string) bool {
	for name, want := range filters {
		if normalize(summary.Fields[name]) != normalize(want) {
			return false
		}
	}
	return true
}

func recordMatchesText(record coredata.Record, query string) bool {
	query = normalize(query)
	values := []string{record.Title, record.Content, record.URL, string(record.Ref.ID), string(record.Ref.Entity), string(record.Ref.View)}
	for _, fieldValues := range record.Fields {
		values = append(values, fieldValues...)
	}
	for _, summaries := range record.Relations {
		for _, summary := range summaries {
			values = append(values, summary.Title)
			for _, value := range summary.Fields {
				values = append(values, value)
			}
		}
	}
	for _, value := range values {
		if strings.Contains(normalize(value), query) {
			return true
		}
	}
	return false
}

func paginateRecords(records []coredata.Record, limit int, cursor string) (coredata.QueryResult, error) {
	if limit <= 0 {
		limit = 20
	}
	offset, err := parseCursor(cursor)
	if err != nil {
		return coredata.QueryResult{}, err
	}
	if offset >= len(records) {
		return coredata.QueryResult{Complete: true}, nil
	}
	records = records[offset:]
	next := ""
	if len(records) > limit {
		records = records[:limit]
		next = strconv.Itoa(offset + limit)
	}
	return coredata.QueryResult{Records: records, NextCursor: next, Complete: next == ""}, nil
}

func paginateRelations(relations []coredata.Relation, limit int, cursor string) (coredata.RelationResult, error) {
	if limit <= 0 {
		limit = 20
	}
	offset, err := parseCursor(cursor)
	if err != nil {
		return coredata.RelationResult{}, err
	}
	if offset >= len(relations) {
		return coredata.RelationResult{Complete: true}, nil
	}
	relations = relations[offset:]
	next := ""
	if len(relations) > limit {
		relations = relations[:limit]
		next = strconv.Itoa(offset + limit)
	}
	return coredata.RelationResult{Relations: relations, NextCursor: next, Complete: next == ""}, nil
}

func schemaStatements(dialect Dialect) []string {
	textType := "TEXT"
	blobType := "BLOB"
	if dialect == DialectMySQL {
		textType = "LONGTEXT"
		blobType = "LONGBLOB"
	}
	record := `CREATE TABLE IF NOT EXISTS data_store_record (
record_hash CHAR(64) PRIMARY KEY,
scope_hash CHAR(64) NOT NULL,
source VARCHAR(191) NOT NULL,
entity VARCHAR(191) NOT NULL,
view_name VARCHAR(191) NOT NULL,
record_id VARCHAR(191) NOT NULL,
scope_json ` + textType + ` NOT NULL,
title ` + textType + `,
content ` + textType + `,
url ` + textType + `,
fields_json ` + textType + ` NOT NULL,
relations_json ` + textType + ` NOT NULL,
blob_refs_json ` + textType + ` NOT NULL,
raw_json ` + textType + ` NOT NULL,
metadata_json ` + textType + ` NOT NULL,
fingerprint VARCHAR(191),
updated_at VARCHAR(64)
)`
	recordField := `CREATE TABLE IF NOT EXISTS data_store_record_field (
record_hash CHAR(64) NOT NULL,
field_name VARCHAR(191) NOT NULL,
value_norm VARCHAR(191) NOT NULL,
value_text ` + textType + ` NOT NULL
)`
	relation := `CREATE TABLE IF NOT EXISTS data_store_relation (
relation_hash CHAR(64) PRIMARY KEY,
scope_hash CHAR(64) NOT NULL,
source VARCHAR(191) NOT NULL,
source_entity VARCHAR(191) NOT NULL,
source_view VARCHAR(191) NOT NULL,
source_id VARCHAR(191) NOT NULL,
relation_name VARCHAR(191) NOT NULL,
target_source VARCHAR(191) NOT NULL,
target_entity VARCHAR(191) NOT NULL,
target_view VARCHAR(191) NOT NULL,
target_id VARCHAR(191) NOT NULL,
scope_json ` + textType + ` NOT NULL,
summary_json ` + textType + ` NOT NULL,
metadata_json ` + textType + ` NOT NULL
)`
	blob := `CREATE TABLE IF NOT EXISTS data_store_blob (
blob_hash CHAR(64) PRIMARY KEY,
blob_id VARCHAR(191) NOT NULL,
scope_hash CHAR(64) NOT NULL,
scope_json ` + textType + ` NOT NULL,
media_type VARCHAR(191),
size_bytes INTEGER NOT NULL,
digest VARCHAR(191) NOT NULL,
metadata_json ` + textType + ` NOT NULL,
content ` + blobType + ` NOT NULL
)`
	if dialect == DialectMySQL {
		return []string{
			record,
			`CREATE INDEX data_store_record_lookup ON data_store_record (source(128), entity(128), view_name(128), record_id(128))`,
			recordField,
			`CREATE INDEX data_store_record_field_lookup ON data_store_record_field (field_name(128), value_norm(128), record_hash)`,
			`CREATE INDEX data_store_record_field_record ON data_store_record_field (record_hash)`,
			relation,
			`CREATE INDEX data_store_relation_source ON data_store_relation (source(96), source_entity(96), source_view(96), source_id(96), relation_name(96))`,
			blob,
			`CREATE INDEX data_store_blob_id ON data_store_blob (blob_id(128))`,
			`CREATE INDEX data_store_blob_scope ON data_store_blob (scope_hash)`,
		}
	}
	return []string{
		record,
		`CREATE INDEX IF NOT EXISTS data_store_record_lookup ON data_store_record (source, entity, view_name, record_id)`,
		recordField,
		`CREATE INDEX IF NOT EXISTS data_store_record_field_lookup ON data_store_record_field (field_name, value_norm, record_hash)`,
		`CREATE INDEX IF NOT EXISTS data_store_record_field_record ON data_store_record_field (record_hash)`,
		relation,
		`CREATE INDEX IF NOT EXISTS data_store_relation_source ON data_store_relation (source, source_entity, source_view, source_id, relation_name)`,
		blob,
		`CREATE INDEX IF NOT EXISTS data_store_blob_id ON data_store_blob (blob_id)`,
		`CREATE INDEX IF NOT EXISTS data_store_blob_scope ON data_store_blob (scope_hash)`,
	}
}

func recordHash(scope coredata.Scope, ref coredata.Ref) string {
	if ref.Source == "" || ref.ID == "" {
		return ""
	}
	return contentHash([]byte(scopeKey(scope) + "\x00" + refKey(ref)))
}

func relationHash(relation coredata.Relation) string {
	if relation.Source.Source == "" || relation.Source.ID == "" || relation.Target.Source == "" || relation.Target.ID == "" || relation.Name == "" {
		return ""
	}
	return contentHash([]byte(scopeKey(relation.Scope) + "\x00" + refKey(relation.Source) + "\x00" + string(relation.Name) + "\x00" + refKey(relation.Target)))
}

func blobHash(scope coredata.Scope, id coredata.BlobID) string {
	if id == "" {
		return ""
	}
	return contentHash([]byte(scopeKey(scope) + "\x00" + string(id)))
}

func refKey(ref coredata.Ref) string {
	return strings.Join([]string{string(ref.Source), string(ref.Entity), string(ref.View), string(ref.ID)}, "\x00")
}

func scopeKey(scope coredata.Scope) string {
	data, _ := json.Marshal(scope)
	return string(data)
}

func scopeHash(scope coredata.Scope) string {
	return contentHash([]byte(scopeKey(scope)))
}

func contentHash(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func mustJSON(value any) string {
	data, err := json.Marshal(value)
	if err != nil {
		return "null"
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

func parseCursor(cursor string) (int, error) {
	cursor = strings.TrimSpace(cursor)
	if cursor == "" {
		return 0, nil
	}
	offset, err := strconv.Atoi(cursor)
	if err != nil || offset < 0 {
		return 0, fmt.Errorf("data sqlstore: invalid cursor %q", cursor)
	}
	return offset, nil
}

func normalize(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func normalizedIndexValue(value string) string {
	value = normalize(value)
	if len(value) <= 191 {
		return value
	}
	return value[:191]
}

func sortedFilterNames(filters map[string]string) []string {
	names := make([]string, 0, len(filters))
	for name := range filters {
		if strings.TrimSpace(name) != "" {
			names = append(names, strings.TrimSpace(name))
		}
	}
	sort.Strings(names)
	return names
}

func isDuplicateIndexError(err error) bool {
	message := err.Error()
	return strings.Contains(message, "Error 1061") || strings.Contains(message, "Duplicate key name")
}

func rollback(tx *sql.Tx) {
	_ = tx.Rollback()
}
