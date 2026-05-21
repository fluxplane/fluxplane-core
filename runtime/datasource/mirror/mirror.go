package mirror

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	coredatasource "github.com/fluxplane/engine/core/datasource"
)

var (
	ErrNotConfigured = errors.New("datasource field index is not configured")
	ErrNotBuilt      = errors.New("datasource field index is not built")
)

const (
	RunStatusRunning  = "running"
	RunStatusComplete = "complete"
	RunStatusFailed   = "failed"
)

// Store persists structured mirror records and mirror run state.
type Store interface {
	UpsertRecord(context.Context, Record) error
	UpsertRecords(context.Context, ...Record) error
	DeleteRecord(context.Context, coredatasource.RecordRef) error
	Record(context.Context, coredatasource.RecordRef) (Record, bool, error)
	SearchRecords(context.Context, SearchRequest) ([]Hit, error)
	RecordStatus(context.Context, StatusRequest) ([]RecordState, error)
	PutRun(context.Context, RunState) error
	Run(context.Context, RunKey) (RunState, bool, error)
	Runs(context.Context, StatusRequest) ([]RunState, error)
	DeleteRuns(context.Context, StatusRequest) error
}

// Service owns structured datasource mirror operations.
type Service struct {
	store Store
}

// New returns a mirror service over store.
func New(store Store) (*Service, error) {
	if store == nil {
		return nil, fmt.Errorf("datasource mirror: store is nil")
	}
	return &Service{store: store}, nil
}

// UpdateRecord stores structured fields for one corpus document.
func (s *Service) UpdateRecord(ctx context.Context, doc coredatasource.CorpusDocument, entity coredatasource.EntitySpec) (UpdateResult, error) {
	if s == nil {
		return UpdateResult{}, fmt.Errorf("datasource mirror: service is nil")
	}
	key := DocumentKey(doc.Ref)
	if key == "" {
		return UpdateResult{}, fmt.Errorf("datasource mirror: document ref is incomplete")
	}
	if err := s.store.UpsertRecord(ctx, RecordFromDocument(key, doc, entity)); err != nil {
		return UpdateResult{}, err
	}
	return UpdateResult{Key: key, Status: "indexed"}, nil
}

// UpdateRecords stores structured fields for a corpus document batch.
func (s *Service) UpdateRecords(ctx context.Context, docs []coredatasource.CorpusDocument, entity coredatasource.EntitySpec) ([]UpdateResult, error) {
	if s == nil {
		return nil, fmt.Errorf("datasource mirror: service is nil")
	}
	records := make([]Record, 0, len(docs))
	results := make([]UpdateResult, 0, len(docs))
	for _, doc := range docs {
		key := DocumentKey(doc.Ref)
		if key == "" {
			return nil, fmt.Errorf("datasource mirror: document ref is incomplete")
		}
		records = append(records, RecordFromDocument(key, doc, entity))
		results = append(results, UpdateResult{Key: key, Status: "indexed"})
	}
	if len(records) == 0 {
		return nil, nil
	}
	if err := s.store.UpsertRecords(ctx, records...); err != nil {
		return nil, err
	}
	return results, nil
}

// DeleteRecord removes one structured mirror record.
func (s *Service) DeleteRecord(ctx context.Context, ref coredatasource.RecordRef) error {
	if s == nil {
		return fmt.Errorf("datasource mirror: service is nil")
	}
	return s.store.DeleteRecord(ctx, ref)
}

// SearchRecords searches structured mirrored records.
func (s *Service) SearchRecords(ctx context.Context, req SearchRequest) (SearchResult, error) {
	if s == nil {
		return SearchResult{}, fmt.Errorf("datasource mirror: service is nil")
	}
	hits, err := s.store.SearchRecords(ctx, req)
	if err != nil {
		return SearchResult{}, err
	}
	return SearchResult{Hits: hits}, nil
}

// Record returns one exact structured mirror record.
func (s *Service) Record(ctx context.Context, ref coredatasource.RecordRef) (coredatasource.Record, error) {
	if s == nil {
		return coredatasource.Record{}, fmt.Errorf("datasource mirror: service is nil")
	}
	record, ok, err := s.store.Record(ctx, ref)
	if err != nil {
		return coredatasource.Record{}, err
	}
	if !ok {
		return coredatasource.Record{}, coredatasource.ErrNotFound
	}
	return RecordToDatasourceRecord(record), nil
}

// Status returns mirror metadata rows matching a filter.
func (s *Service) Status(ctx context.Context, req StatusRequest) (StatusResult, error) {
	if s == nil {
		return StatusResult{}, fmt.Errorf("datasource mirror: service is nil")
	}
	records, err := s.store.RecordStatus(ctx, req)
	if err != nil {
		return StatusResult{}, err
	}
	runs, err := s.store.Runs(ctx, req)
	if err != nil {
		return StatusResult{}, err
	}
	return StatusResult{Records: records, Runs: runs}, nil
}

// PutRun stores per datasource/entity mirror run metadata.
func (s *Service) PutRun(ctx context.Context, run RunState) error {
	if s == nil {
		return fmt.Errorf("datasource mirror: service is nil")
	}
	return s.store.PutRun(ctx, run)
}

// Run returns the latest stored run state for one datasource/entity phase.
func (s *Service) Run(ctx context.Context, key RunKey) (RunState, bool, error) {
	if s == nil {
		return RunState{}, false, fmt.Errorf("datasource mirror: service is nil")
	}
	return s.store.Run(ctx, key)
}

// DeleteRuns removes mirror run checkpoints matching req.
func (s *Service) DeleteRuns(ctx context.Context, req StatusRequest) error {
	if s == nil {
		return fmt.Errorf("datasource mirror: service is nil")
	}
	return s.store.DeleteRuns(ctx, req)
}

// RequireBuilt reports whether mirror records exist or a mirror/all run
// completed for a datasource entity.
func RequireBuilt(ctx context.Context, service *Service, datasource coredatasource.Name, entity coredatasource.EntityType) error {
	if service == nil {
		return fmt.Errorf("%w for %s/%s", ErrNotConfigured, datasource, entity)
	}
	status, err := service.Status(ctx, StatusRequest{Datasource: datasource, Entity: entity})
	if err != nil {
		return err
	}
	if len(status.Records) > 0 {
		return nil
	}
	for _, run := range status.Runs {
		if run.Status == RunStatusComplete && (run.Phase == "all" || run.Phase == "fields") {
			return nil
		}
	}
	return fmt.Errorf("%w for %s/%s", ErrNotBuilt, datasource, entity)
}

// LookupRequest describes a structured mirror lookup.
type LookupRequest struct {
	Service    *Service
	Datasource coredatasource.Name
	Entity     coredatasource.EntityType
	Query      string
	Filters    map[string]string
	Limit      int
	Cursor     string
}

// LookupResult contains structured mirror records and pagination state.
type LookupResult struct {
	Records    []coredatasource.Record
	NextCursor string
	Complete   bool
}

// Search performs a structured mirror lookup with readiness and cursor handling.
func Search(ctx context.Context, req LookupRequest) (LookupResult, error) {
	if err := RequireBuilt(ctx, req.Service, req.Datasource, req.Entity); err != nil {
		return LookupResult{}, err
	}
	limit := req.Limit
	if limit <= 0 {
		limit = 10
	}
	offset, err := CursorOffset(req.Cursor)
	if err != nil {
		return LookupResult{}, err
	}
	result, err := req.Service.SearchRecords(ctx, SearchRequest{
		Query:       req.Query,
		Datasources: []coredatasource.Name{req.Datasource},
		Entities:    []coredatasource.EntityType{req.Entity},
		Filters:     req.Filters,
		Limit:       limit + 1,
		Offset:      offset,
	})
	if err != nil {
		return LookupResult{}, err
	}
	records := make([]coredatasource.Record, 0, len(result.Hits))
	for _, hit := range result.Hits {
		records = append(records, hit.Record)
	}
	next := ""
	if len(records) > limit {
		records = records[:limit]
		next = strconv.Itoa(offset + limit)
	}
	return LookupResult{Records: records, NextCursor: next, Complete: next == ""}, nil
}

// Get returns one exact structured mirror record after readiness checks.
func Get(ctx context.Context, service *Service, datasource coredatasource.Name, entity coredatasource.EntityType, id string) (coredatasource.Record, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return coredatasource.Record{}, coredatasource.ErrNotFound
	}
	if err := RequireBuilt(ctx, service, datasource, entity); err != nil {
		return coredatasource.Record{}, err
	}
	return service.Record(ctx, coredatasource.RecordRef{Datasource: datasource, Entity: entity, ID: id})
}

// UpdateResult describes one mirror update decision.
type UpdateResult struct {
	Key    string
	Status string
}

// StatusRequest filters mirror status rows.
type StatusRequest struct {
	Datasource coredatasource.Name
	Entity     coredatasource.EntityType
}

// StatusResult describes mirrored records and mirror runs.
type StatusResult struct {
	Records []RecordState
	Runs    []RunState
}

// RunKey identifies one datasource/entity mirror run checkpoint.
type RunKey struct {
	Datasource coredatasource.Name       `json:"datasource"`
	Entity     coredatasource.EntityType `json:"entity"`
	Phase      string                    `json:"phase,omitempty"`
}

// RunState is the persisted status for one datasource/entity mirror run.
type RunState struct {
	Key         string                    `json:"key"`
	Datasource  coredatasource.Name       `json:"datasource"`
	Entity      coredatasource.EntityType `json:"entity"`
	Phase       string                    `json:"phase,omitempty"`
	Status      string                    `json:"status,omitempty"`
	StartedAt   time.Time                 `json:"started_at,omitempty"`
	CompletedAt time.Time                 `json:"completed_at,omitempty"`
	Documents   int                       `json:"documents,omitempty"`
	Indexed     int                       `json:"indexed,omitempty"`
	Queued      int                       `json:"queued,omitempty"`
	Skipped     int                       `json:"skipped,omitempty"`
	Deleted     int                       `json:"deleted,omitempty"`
	Failed      int                       `json:"failed,omitempty"`
	LastError   string                    `json:"last_error,omitempty"`
}

// SearchRequest describes a structured mirror search query.
type SearchRequest struct {
	Query       string
	Datasources []coredatasource.Name
	Entities    []coredatasource.EntityType
	Filters     map[string]string
	Limit       int
	Offset      int
}

// SearchResult contains structured mirror hits.
type SearchResult struct {
	Hits []Hit
}

// Hit is one structured mirror search result.
type Hit struct {
	Record coredatasource.Record
	Score  float64
	Reason string
}

// Record is one mirrored structured datasource record.
type Record struct {
	Key         string                   `json:"key"`
	Ref         coredatasource.RecordRef `json:"ref"`
	Title       string                   `json:"title,omitempty"`
	Content     string                   `json:"content,omitempty"`
	URL         string                   `json:"url,omitempty"`
	Fields      map[string][]string      `json:"fields,omitempty"`
	Search      []string                 `json:"search,omitempty"`
	Identifiers []string                 `json:"identifiers,omitempty"`
	Filters     map[string][]string      `json:"filters,omitempty"`
}

// RecordState is the status row for one mirrored record.
type RecordState struct {
	Key string                   `json:"key"`
	Ref coredatasource.RecordRef `json:"ref"`
}

// DocumentKey returns the stable mirror key for a datasource record.
func DocumentKey(ref coredatasource.RecordRef) string {
	if ref.Datasource == "" || ref.Entity == "" || strings.TrimSpace(ref.ID) == "" {
		return ""
	}
	return string(ref.Datasource) + "\x00" + string(ref.Entity) + "\x00" + strings.TrimSpace(ref.ID)
}

// RunStorageKey returns the stable storage key for a mirror run.
func RunStorageKey(key RunKey) string {
	if key.Datasource == "" || key.Entity == "" {
		return ""
	}
	phase := strings.TrimSpace(key.Phase)
	if phase == "" {
		phase = "all"
	}
	return string(key.Datasource) + "\x00" + string(key.Entity) + "\x00" + phase
}

// RecordFromDocument converts an indexable corpus document into a structured
// mirror record using entity field metadata.
func RecordFromDocument(key string, doc coredatasource.CorpusDocument, entity coredatasource.EntitySpec) Record {
	record := Record{
		Key:     key,
		Ref:     doc.Ref,
		Title:   doc.Title,
		Content: doc.Body,
		URL:     doc.URL,
		Fields:  map[string][]string{},
		Filters: map[string][]string{},
	}
	addFieldValue := func(name string, values ...string) {
		for _, value := range values {
			value = strings.TrimSpace(value)
			if value == "" {
				continue
			}
			record.Fields[name] = appendUnique(record.Fields[name], value)
		}
	}
	addFieldValue("id", doc.Ref.ID)
	addFieldValue("title", doc.Title)
	addFieldValue("body", doc.Body)
	addFieldValue("url", doc.URL)
	for key, value := range doc.Metadata {
		addFieldValue(key, value)
	}
	for _, field := range entity.Fields {
		values := append([]string(nil), record.Fields[field.Name]...)
		if len(values) == 0 {
			continue
		}
		if field.Identifier {
			record.Identifiers = appendUnique(record.Identifiers, values...)
		}
		if field.Searchable || field.Identifier {
			record.Search = appendUnique(record.Search, values...)
		}
		if field.Filterable {
			record.Filters[field.Name] = appendUnique(record.Filters[field.Name], values...)
		}
	}
	record.Identifiers = appendUnique(record.Identifiers, doc.Ref.ID)
	record.Search = appendUnique(record.Search, doc.Ref.ID, doc.Title, doc.Body)
	return record
}

// RecordToDatasourceRecord converts a mirror record into the model-facing
// datasource record shape.
func RecordToDatasourceRecord(record Record) coredatasource.Record {
	metadata := map[string]string{}
	for name, values := range record.Fields {
		if len(values) > 0 {
			metadata[name] = strings.Join(values, ",")
		}
	}
	return coredatasource.Record{
		ID:         record.Ref.ID,
		Datasource: record.Ref.Datasource,
		Entity:     record.Ref.Entity,
		Title:      record.Title,
		Content:    record.Content,
		URL:        record.URL,
		Metadata:   metadata,
	}
}

// SearchRecords scans structured mirror records using the common in-memory
// ranking rules. Stores with native indexes may use the same scoring semantics
// or provide a more efficient implementation.
func SearchRecords(ctx context.Context, records map[string]Record, req SearchRequest) ([]Hit, error) {
	limit := req.Limit
	if limit <= 0 {
		limit = 10
	}
	query := NormalizeText(req.Query)
	queryTokens := Tokenize(req.Query)
	var hits []Hit
	for _, record := range records {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if !ContainsDatasource(req.Datasources, record.Ref.Datasource) || !ContainsEntity(req.Entities, record.Ref.Entity) {
			continue
		}
		if !MatchesFilters(record, req.Filters) {
			continue
		}
		score, reason := Score(record, query, queryTokens)
		if score <= 0 {
			continue
		}
		hits = append(hits, Hit{Record: RecordToDatasourceRecord(record), Score: score, Reason: reason})
	}
	sort.Slice(hits, func(i, j int) bool {
		if hits[i].Score == hits[j].Score {
			return hits[i].Record.ID < hits[j].Record.ID
		}
		return hits[i].Score > hits[j].Score
	})
	offset := req.Offset
	if offset < 0 {
		offset = 0
	}
	if offset >= len(hits) {
		return nil, nil
	}
	hits = hits[offset:]
	if len(hits) > limit {
		hits = hits[:limit]
	}
	return hits, nil
}

func CursorOffset(cursor string) (int, error) {
	cursor = strings.TrimSpace(cursor)
	if cursor == "" {
		return 0, nil
	}
	offset, err := strconv.Atoi(cursor)
	if err != nil || offset < 0 {
		return 0, fmt.Errorf("invalid datasource mirror cursor %q", cursor)
	}
	return offset, nil
}

func MatchesFilters(record Record, filters map[string]string) bool {
	for name, want := range filters {
		want = NormalizeText(want)
		if want == "" {
			continue
		}
		var matched bool
		for _, value := range record.Filters[name] {
			if NormalizeText(value) == want {
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

func Score(record Record, query string, queryTokens []string) (float64, string) {
	if query == "" {
		return 1, "all"
	}
	for _, value := range record.Identifiers {
		norm := NormalizeText(value)
		if norm == query {
			return 100, "identifier"
		}
		if strings.HasPrefix(norm, query) || strings.HasPrefix(query, norm) {
			return 90, "identifier_prefix"
		}
	}
	var best float64
	reason := ""
	for _, value := range record.Search {
		norm := NormalizeText(value)
		switch {
		case norm == query:
			if best < 80 {
				best, reason = 80, "field"
			}
		case strings.HasPrefix(norm, query):
			if best < 65 {
				best, reason = 65, "field_prefix"
			}
		case strings.Contains(norm, query):
			if best < 50 {
				best, reason = 50, "field_contains"
			}
		}
	}
	if len(queryTokens) > 0 {
		haystack := NormalizeText(strings.Join(record.Search, " "))
		var matched int
		for _, token := range queryTokens {
			if strings.Contains(haystack, token) {
				matched++
			}
		}
		if matched > 0 {
			score := float64(20 + matched*10)
			if score > best {
				best, reason = score, "field_tokens"
			}
		}
	}
	return best, reason
}

func ContainsDatasource(values []coredatasource.Name, value coredatasource.Name) bool {
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

func ContainsEntity(values []coredatasource.EntityType, value coredatasource.EntityType) bool {
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

func NormalizeText(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func Tokenize(value string) []string {
	fields := strings.FieldsFunc(NormalizeText(value), func(r rune) bool {
		return (r < 'a' || r > 'z') && (r < '0' || r > '9')
	})
	var out []string
	for _, field := range fields {
		if field != "" {
			out = append(out, field)
		}
	}
	return out
}

func appendUnique(values []string, candidates ...string) []string {
	seen := map[string]bool{}
	for _, value := range values {
		seen[value] = true
	}
	for _, candidate := range candidates {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" || seen[candidate] {
			continue
		}
		values = append(values, candidate)
		seen[candidate] = true
	}
	return values
}
