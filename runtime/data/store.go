package data

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"

	coredata "github.com/fluxplane/agentruntime/core/data"
)

// MemoryStore is an in-memory data store useful for local runtime composition
// and tests.
type MemoryStore struct {
	mu        sync.RWMutex
	records   map[string]coredata.Record
	relations []coredata.Relation
	blobs     map[coredata.BlobID]coredata.Blob
	nextBlob  int
}

// NewMemoryStore returns an empty in-memory data store.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		records: map[string]coredata.Record{},
		blobs:   map[coredata.BlobID]coredata.Blob{},
	}
}

var _ coredata.Store = (*MemoryStore)(nil)

// UpsertRecords inserts or replaces records by scope and ref.
func (s *MemoryStore) UpsertRecords(ctx context.Context, records ...coredata.Record) error {
	if s == nil {
		return fmt.Errorf("data: store is nil")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, record := range records {
		if err := ctx.Err(); err != nil {
			return err
		}
		key := recordKey(record.Ref)
		if key == "" {
			return fmt.Errorf("data: record ref is incomplete")
		}
		s.records[scopedRecordKey(record.Scope, record.Ref)] = cloneRecord(record)
	}
	return nil
}

// DeleteRecords removes records by scope selector and ref.
func (s *MemoryStore) DeleteRecords(ctx context.Context, scope coredata.Scope, refs ...coredata.Ref) error {
	if s == nil {
		return fmt.Errorf("data: store is nil")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, ref := range refs {
		if err := ctx.Err(); err != nil {
			return err
		}
		for key, record := range s.records {
			if refSelectorMatches(record.Ref, ref) && record.Scope.Matches(scope) {
				delete(s.records, key)
			}
		}
	}
	return nil
}

// GetRecord returns one record by scope selector and exact ref.
func (s *MemoryStore) GetRecord(ctx context.Context, scope coredata.Scope, ref coredata.Ref) (coredata.Record, bool, error) {
	if s == nil {
		return coredata.Record{}, false, fmt.Errorf("data: store is nil")
	}
	if err := ctx.Err(); err != nil {
		return coredata.Record{}, false, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, record := range s.records {
		if refSelectorMatches(record.Ref, ref) && record.Scope.Matches(scope) {
			return cloneRecord(record), true, nil
		}
	}
	return coredata.Record{}, false, nil
}

// BatchGetRecords returns records by exact refs in request order, skipping refs
// that are not present.
func (s *MemoryStore) BatchGetRecords(ctx context.Context, scope coredata.Scope, refs ...coredata.Ref) ([]coredata.Record, error) {
	if s == nil {
		return nil, fmt.Errorf("data: store is nil")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	records := make([]coredata.Record, 0, len(refs))
	for _, ref := range refs {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		for _, record := range s.records {
			if refSelectorMatches(record.Ref, ref) && record.Scope.Matches(scope) {
				records = append(records, cloneRecord(record))
				break
			}
		}
	}
	return records, nil
}

// QueryRecords searches records using simple in-memory indexes.
func (s *MemoryStore) QueryRecords(ctx context.Context, req coredata.Query) (coredata.QueryResult, error) {
	if s == nil {
		return coredata.QueryResult{}, fmt.Errorf("data: store is nil")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	limit := req.Limit
	if limit <= 0 {
		limit = 20
	}
	offset, err := parseCursor(req.Cursor)
	if err != nil {
		return coredata.QueryResult{}, err
	}
	var records []coredata.Record
	for _, record := range s.records {
		if err := ctx.Err(); err != nil {
			return coredata.QueryResult{}, err
		}
		if !matchesRecord(record, req) {
			continue
		}
		records = append(records, cloneRecord(record))
	}
	sort.Slice(records, func(i, j int) bool {
		return scopedRecordKey(records[i].Scope, records[i].Ref) < scopedRecordKey(records[j].Scope, records[j].Ref)
	})
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

// UpsertRelations inserts or replaces relation edges by source/name/target.
func (s *MemoryStore) UpsertRelations(ctx context.Context, relations ...coredata.Relation) error {
	if s == nil {
		return fmt.Errorf("data: store is nil")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	byKey := map[string]coredata.Relation{}
	for _, relation := range s.relations {
		byKey[scopedRelationKey(relation)] = relation
	}
	for _, relation := range relations {
		if err := ctx.Err(); err != nil {
			return err
		}
		if relationKey(relation) == "" {
			return fmt.Errorf("data: relation ref is incomplete")
		}
		byKey[scopedRelationKey(relation)] = cloneRelation(relation)
	}
	s.relations = s.relations[:0]
	for _, relation := range byKey {
		s.relations = append(s.relations, relation)
	}
	return nil
}

// QueryRelations searches relation edges.
func (s *MemoryStore) QueryRelations(ctx context.Context, req coredata.RelationQuery) (coredata.RelationResult, error) {
	if s == nil {
		return coredata.RelationResult{}, fmt.Errorf("data: store is nil")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	limit := req.Limit
	if limit <= 0 {
		limit = 20
	}
	offset, err := parseCursor(req.Cursor)
	if err != nil {
		return coredata.RelationResult{}, err
	}
	var relations []coredata.Relation
	for _, relation := range s.relations {
		if err := ctx.Err(); err != nil {
			return coredata.RelationResult{}, err
		}
		if !matchesRelation(relation, req) {
			continue
		}
		relations = append(relations, cloneRelation(relation))
	}
	sort.Slice(relations, func(i, j int) bool {
		return scopedRelationKey(relations[i]) < scopedRelationKey(relations[j])
	})
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

// PutBlob stores one payload.
func (s *MemoryStore) PutBlob(ctx context.Context, blob coredata.Blob) (coredata.BlobRef, error) {
	if s == nil {
		return coredata.BlobRef{}, fmt.Errorf("data: store is nil")
	}
	if err := ctx.Err(); err != nil {
		return coredata.BlobRef{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	ref := blob.Ref
	if ref.ID == "" {
		s.nextBlob++
		ref.ID = coredata.BlobID(fmt.Sprintf("blob_%d", s.nextBlob))
	}
	ref.Size = int64(len(blob.Content))
	sum := sha256.Sum256(blob.Content)
	ref.Digest = "sha256:" + hex.EncodeToString(sum[:])
	blob.Ref = ref
	s.blobs[ref.ID] = coredata.Blob{Ref: cloneBlobRef(ref), Content: append([]byte(nil), blob.Content...)}
	return cloneBlobRef(ref), nil
}

// GetBlob returns one payload by ref.
func (s *MemoryStore) GetBlob(ctx context.Context, ref coredata.BlobRef) (coredata.Blob, bool, error) {
	if s == nil {
		return coredata.Blob{}, false, fmt.Errorf("data: store is nil")
	}
	if err := ctx.Err(); err != nil {
		return coredata.Blob{}, false, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	blob, ok := s.blobs[ref.ID]
	if !ok || !blob.Ref.Scope.Matches(ref.Scope) {
		return coredata.Blob{}, false, nil
	}
	return coredata.Blob{Ref: cloneBlobRef(blob.Ref), Content: append([]byte(nil), blob.Content...)}, true, nil
}

func matchesRecord(record coredata.Record, req coredata.Query) bool {
	if !record.Scope.Matches(req.Scope) {
		return false
	}
	if !containsSource(req.Sources, record.Ref.Source) ||
		!containsEntity(req.Entities, record.Ref.Entity) ||
		!containsView(req.Views, record.Ref.View) ||
		!containsID(req.IDs, record.Ref.ID) {
		return false
	}
	if !matchesFilters(record.Fields, req.Filters) {
		return false
	}
	if !matchesRelationFilters(record, req.RelationFilters) {
		return false
	}
	if strings.TrimSpace(req.Text) != "" && !recordMatchesText(record, req.Text) {
		return false
	}
	return true
}

func matchesRelation(relation coredata.Relation, req coredata.RelationQuery) bool {
	if !relation.Scope.Matches(req.Scope) {
		return false
	}
	if !containsSource(req.Sources, relation.Source.Source) ||
		!containsView(req.Views, relation.Source.View) {
		return false
	}
	if req.Relation != "" && relation.Name != req.Relation {
		return false
	}
	if !refSelectorMatches(relation.Source, req.Source) || !refSelectorMatches(relation.Target, req.Target) {
		return false
	}
	return true
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
		summaries := record.Relations[string(filter.Relation)]
		if len(summaries) == 0 {
			return false
		}
		var matched bool
		for _, summary := range summaries {
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
		want = normalize(want)
		if want == "" {
			continue
		}
		if normalize(summary.Fields[name]) != want {
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

func refSelectorMatches(ref, selector coredata.Ref) bool {
	if selector.Source != "" && ref.Source != selector.Source {
		return false
	}
	if selector.Entity != "" && ref.Entity != selector.Entity {
		return false
	}
	if selector.View != "" && ref.View != selector.View {
		return false
	}
	if selector.ID != "" && ref.ID != selector.ID {
		return false
	}
	return true
}

func recordKey(ref coredata.Ref) string {
	if ref.Source == "" || strings.TrimSpace(string(ref.ID)) == "" {
		return ""
	}
	return strings.Join([]string{string(ref.Source), string(ref.Entity), string(ref.View), strings.TrimSpace(string(ref.ID))}, "\x00")
}

func scopedRecordKey(scope coredata.Scope, ref coredata.Ref) string {
	return scopeKey(scope) + "\x00" + recordKey(ref)
}

func scopeKey(scope coredata.Scope) string {
	annotations := make([]string, 0, len(scope.Annotations))
	for key, value := range scope.Annotations {
		annotations = append(annotations, key+"="+value)
	}
	sort.Strings(annotations)
	return strings.Join([]string{
		scope.TenantID,
		scope.AppID,
		string(scope.WorkspaceID),
		string(scope.UserID),
		scope.AgentID,
		scope.SessionID,
		scope.ChannelID,
		strings.Join(annotations, "\x1f"),
	}, "\x1e")
}

func relationKey(relation coredata.Relation) string {
	source := recordKey(relation.Source)
	target := recordKey(relation.Target)
	if source == "" || target == "" || relation.Name == "" {
		return ""
	}
	return source + "\x00" + string(relation.Name) + "\x00" + target
}

func scopedRelationKey(relation coredata.Relation) string {
	return scopeKey(relation.Scope) + "\x00" + relationKey(relation)
}

func parseCursor(cursor string) (int, error) {
	cursor = strings.TrimSpace(cursor)
	if cursor == "" {
		return 0, nil
	}
	offset, err := strconv.Atoi(cursor)
	if err != nil || offset < 0 {
		return 0, fmt.Errorf("data: invalid cursor %q", cursor)
	}
	return offset, nil
}

func containsSource(values []coredata.SourceName, value coredata.SourceName) bool {
	if len(values) == 0 {
		return true
	}
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}

func containsEntity(values []coredata.EntityType, value coredata.EntityType) bool {
	if len(values) == 0 {
		return true
	}
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}

func containsView(values []coredata.ViewName, value coredata.ViewName) bool {
	if len(values) == 0 {
		return true
	}
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}

func containsID(values []coredata.RecordID, value coredata.RecordID) bool {
	if len(values) == 0 {
		return true
	}
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}

func normalize(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func cloneRecord(record coredata.Record) coredata.Record {
	record.Fields = cloneStringSliceMap(record.Fields)
	record.Relations = cloneSummaryMap(record.Relations)
	record.BlobRefs = append([]coredata.BlobRef(nil), record.BlobRefs...)
	record.Metadata = cloneStringMap(record.Metadata)
	return record
}

func cloneRelation(relation coredata.Relation) coredata.Relation {
	relation.Summary.Fields = cloneStringMap(relation.Summary.Fields)
	relation.Metadata = cloneStringMap(relation.Metadata)
	return relation
}

func cloneBlobRef(ref coredata.BlobRef) coredata.BlobRef {
	ref.Metadata = cloneStringMap(ref.Metadata)
	return ref
}

func cloneStringSliceMap(in map[string][]string) map[string][]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string][]string, len(in))
	for key, values := range in {
		out[key] = append([]string(nil), values...)
	}
	return out
}

func cloneSummaryMap(in map[string][]coredata.Summary) map[string][]coredata.Summary {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string][]coredata.Summary, len(in))
	for key, values := range in {
		copied := make([]coredata.Summary, 0, len(values))
		for _, value := range values {
			value.Fields = cloneStringMap(value.Fields)
			copied = append(copied, value)
		}
		out[key] = copied
	}
	return out
}

func cloneStringMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}
