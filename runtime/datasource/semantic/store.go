package semantic

import (
	"context"
	"encoding/json"
	"errors"
	"hash/fnv"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	coredatasource "github.com/fluxplane/agentruntime/core/datasource"
)

const hashEmbeddingDimensions = 128

// HashEmbedder is a deterministic local embedder useful for tests and local indexing.
type HashEmbedder struct {
	ModelName string
}

func (e HashEmbedder) Model() string {
	if strings.TrimSpace(e.ModelName) != "" {
		return strings.TrimSpace(e.ModelName)
	}
	return defaultEmbeddingModel
}

func (e HashEmbedder) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	out := make([][]float32, 0, len(texts))
	for _, text := range texts {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		vector := make([]float32, hashEmbeddingDimensions)
		for _, token := range strings.Fields(strings.ToLower(text)) {
			token = strings.Trim(token, ".,:;!?()[]{}<>\"'`")
			if token == "" {
				continue
			}
			h := fnv.New32a()
			_, _ = h.Write([]byte(token))
			idx := int(h.Sum32() % hashEmbeddingDimensions)
			vector[idx]++
		}
		normalize(vector)
		out = append(out, vector)
	}
	return out, nil
}

// JSONStore stores index metadata and vectors in one JSON file.
type JSONStore struct {
	path   string
	mu     sync.Mutex
	memory jsonState
}

// NewJSONStore returns a JSON-backed semantic index store.
func NewJSONStore(path string) *JSONStore {
	return &JSONStore{path: path}
}

func (s *JSONStore) UpsertChunks(ctx context.Context, chunks []EmbeddedChunk) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	state, err := s.loadLocked(ctx)
	if err != nil {
		return err
	}
	byID := map[string]EmbeddedChunk{}
	for _, chunk := range state.Chunks {
		byID[chunk.Chunk.ID] = chunk
	}
	for _, chunk := range chunks {
		byID[chunk.Chunk.ID] = chunk
	}
	state.Chunks = make([]EmbeddedChunk, 0, len(byID))
	for _, chunk := range byID {
		state.Chunks = append(state.Chunks, chunk)
	}
	return s.saveLocked(ctx, state)
}

func (s *JSONStore) DeleteChunks(ctx context.Context, req DeleteRequest) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	state, err := s.loadLocked(ctx)
	if err != nil {
		return err
	}
	var kept []EmbeddedChunk
	for _, chunk := range state.Chunks {
		if req.DocumentKey != "" && chunk.Chunk.DocumentKey == req.DocumentKey {
			continue
		}
		kept = append(kept, chunk)
	}
	state.Chunks = kept
	return s.saveLocked(ctx, state)
}

func (s *JSONStore) UpsertRecord(ctx context.Context, record FieldRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	state, err := s.loadLocked(ctx)
	if err != nil {
		return err
	}
	if state.Records == nil {
		state.Records = map[string]FieldRecord{}
	}
	state.Records[record.Key] = record
	return s.saveLocked(ctx, state)
}

func (s *JSONStore) DeleteRecord(ctx context.Context, ref coredatasource.RecordRef) error {
	key := DocumentKey(ref)
	if key == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	state, err := s.loadLocked(ctx)
	if err != nil {
		return err
	}
	delete(state.Records, key)
	return s.saveLocked(ctx, state)
}

func (s *JSONStore) PutQueueJob(ctx context.Context, job QueueJob) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	state, err := s.loadLocked(ctx)
	if err != nil {
		return err
	}
	if state.Queue == nil {
		state.Queue = map[string]QueueJob{}
	}
	if strings.TrimSpace(job.Status) == "" {
		job.Status = QueueStatusQueued
	}
	state.Queue[job.Key] = job
	return s.saveLocked(ctx, state)
}

func (s *JSONStore) DeleteQueueJob(ctx context.Context, key string) error {
	key = strings.TrimSpace(key)
	if key == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	state, err := s.loadLocked(ctx)
	if err != nil {
		return err
	}
	delete(state.Queue, key)
	return s.saveLocked(ctx, state)
}

func (s *JSONStore) QueueJobs(ctx context.Context, req QueueRequest) ([]QueueJob, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	state, err := s.loadLocked(ctx)
	if err != nil {
		return nil, err
	}
	statuses := map[string]bool{}
	for _, status := range req.Statuses {
		status = strings.TrimSpace(status)
		if status != "" {
			statuses[status] = true
		}
	}
	var jobs []QueueJob
	for _, job := range state.Queue {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if req.Datasource != "" && job.Ref.Datasource != req.Datasource {
			continue
		}
		if req.Entity != "" && job.Ref.Entity != req.Entity {
			continue
		}
		if len(statuses) > 0 && !statuses[job.Status] {
			continue
		}
		jobs = append(jobs, job)
	}
	sort.Slice(jobs, func(i, j int) bool {
		if jobs[i].EnqueuedAt.Equal(jobs[j].EnqueuedAt) {
			return jobs[i].Key < jobs[j].Key
		}
		return jobs[i].EnqueuedAt.Before(jobs[j].EnqueuedAt)
	})
	if req.Limit > 0 && len(jobs) > req.Limit {
		jobs = jobs[:req.Limit]
	}
	return jobs, nil
}

func (s *JSONStore) QueueStatus(ctx context.Context, req StatusRequest) ([]QueueState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	state, err := s.loadLocked(ctx)
	if err != nil {
		return nil, err
	}
	var queue []QueueState
	for _, job := range state.Queue {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if req.Datasource != "" && job.Ref.Datasource != req.Datasource {
			continue
		}
		if req.Entity != "" && job.Ref.Entity != req.Entity {
			continue
		}
		queue = append(queue, QueueState{
			Key:        job.Key,
			Ref:        job.Ref,
			Status:     job.Status,
			LastError:  job.LastError,
			Attempts:   job.Attempts,
			EnqueuedAt: job.EnqueuedAt,
			UpdatedAt:  job.UpdatedAt,
		})
	}
	sort.Slice(queue, func(i, j int) bool { return queue[i].Key < queue[j].Key })
	return queue, nil
}

func (s *JSONStore) SearchRecords(ctx context.Context, req FieldSearchRequest) ([]FieldHit, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	state, err := s.loadLocked(ctx)
	if err != nil {
		return nil, err
	}
	limit := req.Limit
	if limit <= 0 {
		limit = 10
	}
	query := normalizeText(req.Query)
	queryTokens := tokenize(req.Query)
	var hits []FieldHit
	for _, record := range state.Records {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if !containsDatasource(req.Datasources, record.Ref.Datasource) || !containsEntity(req.Entities, record.Ref.Entity) {
			continue
		}
		if !matchesFilters(record, req.Filters) {
			continue
		}
		score, reason := fieldScore(record, query, queryTokens)
		if score <= 0 {
			continue
		}
		hits = append(hits, FieldHit{Record: fieldRecordToRecord(record), Score: score, Reason: reason})
	}
	sort.Slice(hits, func(i, j int) bool {
		if hits[i].Score == hits[j].Score {
			return hits[i].Record.ID < hits[j].Record.ID
		}
		return hits[i].Score > hits[j].Score
	})
	if len(hits) > limit {
		hits = hits[:limit]
	}
	return hits, nil
}

func (s *JSONStore) RecordStatus(ctx context.Context, req StatusRequest) ([]FieldRecordState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	state, err := s.loadLocked(ctx)
	if err != nil {
		return nil, err
	}
	var records []FieldRecordState
	for _, record := range state.Records {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if req.Datasource != "" && record.Ref.Datasource != req.Datasource {
			continue
		}
		if req.Entity != "" && record.Ref.Entity != req.Entity {
			continue
		}
		records = append(records, FieldRecordState{Key: record.Key, Ref: record.Ref})
	}
	sort.Slice(records, func(i, j int) bool { return records[i].Key < records[j].Key })
	return records, nil
}

func (s *JSONStore) Search(ctx context.Context, req VectorSearchRequest) ([]VectorHit, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	state, err := s.loadLocked(ctx)
	if err != nil {
		return nil, err
	}
	var hits []VectorHit
	for _, chunk := range state.Chunks {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if !containsDatasource(req.Datasources, chunk.Chunk.Ref.Datasource) || !containsEntity(req.Entities, chunk.Chunk.Ref.Entity) {
			continue
		}
		score := cosine(req.Vector, chunk.Vector)
		if req.MinScore > 0 && score < req.MinScore {
			continue
		}
		hits = append(hits, VectorHit{Chunk: chunk.Chunk, Score: score})
	}
	sort.Slice(hits, func(i, j int) bool { return hits[i].Score > hits[j].Score })
	if req.Limit > 0 && len(hits) > req.Limit {
		hits = hits[:req.Limit]
	}
	return hits, nil
}

func (s *JSONStore) Document(ctx context.Context, key string) (DocumentState, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	state, err := s.loadLocked(ctx)
	if err != nil {
		return DocumentState{}, false, err
	}
	doc, ok := state.Documents[key]
	return doc, ok, nil
}

func (s *JSONStore) PutDocument(ctx context.Context, doc DocumentState) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	state, err := s.loadLocked(ctx)
	if err != nil {
		return err
	}
	if state.Documents == nil {
		state.Documents = map[string]DocumentState{}
	}
	state.Documents[doc.Key] = doc
	return s.saveLocked(ctx, state)
}

func (s *JSONStore) DeleteDocument(ctx context.Context, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	state, err := s.loadLocked(ctx)
	if err != nil {
		return err
	}
	delete(state.Documents, key)
	return s.saveLocked(ctx, state)
}

func (s *JSONStore) Documents(ctx context.Context, req StatusRequest) ([]DocumentState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	state, err := s.loadLocked(ctx)
	if err != nil {
		return nil, err
	}
	var docs []DocumentState
	for _, doc := range state.Documents {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if req.Datasource != "" && doc.Ref.Datasource != req.Datasource {
			continue
		}
		if req.Entity != "" && doc.Ref.Entity != req.Entity {
			continue
		}
		docs = append(docs, doc)
	}
	sort.Slice(docs, func(i, j int) bool { return docs[i].Key < docs[j].Key })
	return docs, nil
}

func (s *JSONStore) loadLocked(ctx context.Context) (jsonState, error) {
	if err := ctx.Err(); err != nil {
		return jsonState{}, err
	}
	path := strings.TrimSpace(s.path)
	if path == "" {
		if s.memory.Documents == nil {
			s.memory.Documents = map[string]DocumentState{}
		}
		if s.memory.Records == nil {
			s.memory.Records = map[string]FieldRecord{}
		}
		if s.memory.Queue == nil {
			s.memory.Queue = map[string]QueueJob{}
		}
		return s.memory, nil
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return jsonState{Documents: map[string]DocumentState{}}, nil
	}
	if err != nil {
		return jsonState{}, err
	}
	var state jsonState
	if len(data) > 0 {
		if err := json.Unmarshal(data, &state); err != nil {
			return jsonState{}, err
		}
	}
	if state.Documents == nil {
		state.Documents = map[string]DocumentState{}
	}
	if state.Records == nil {
		state.Records = map[string]FieldRecord{}
	}
	if state.Queue == nil {
		state.Queue = map[string]QueueJob{}
	}
	return state, nil
}

func (s *JSONStore) saveLocked(ctx context.Context, state jsonState) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	path := strings.TrimSpace(s.path)
	if path == "" {
		if state.Documents == nil {
			state.Documents = map[string]DocumentState{}
		}
		if state.Records == nil {
			state.Records = map[string]FieldRecord{}
		}
		if state.Queue == nil {
			state.Queue = map[string]QueueJob{}
		}
		s.memory = state
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

type jsonState struct {
	Documents map[string]DocumentState `json:"documents,omitempty"`
	Records   map[string]FieldRecord   `json:"records,omitempty"`
	Queue     map[string]QueueJob      `json:"semantic_queue,omitempty"`
	Chunks    []EmbeddedChunk          `json:"chunks,omitempty"`
}

func matchesFilters(record FieldRecord, filters map[string]string) bool {
	for name, want := range filters {
		want = normalizeText(want)
		if want == "" {
			continue
		}
		var matched bool
		for _, value := range record.Filters[name] {
			if normalizeText(value) == want {
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

func fieldScore(record FieldRecord, query string, queryTokens []string) (float64, string) {
	if query == "" {
		return 1, "all"
	}
	for _, value := range record.Identifiers {
		norm := normalizeText(value)
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
		norm := normalizeText(value)
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
		haystack := normalizeText(strings.Join(record.Search, " "))
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

func fieldRecordToRecord(record FieldRecord) coredatasource.Record {
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

func normalizeText(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func tokenize(value string) []string {
	fields := strings.FieldsFunc(normalizeText(value), func(r rune) bool {
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

func normalize(vector []float32) {
	var sum float64
	for _, value := range vector {
		sum += float64(value * value)
	}
	if sum == 0 {
		return
	}
	scale := float32(1 / math.Sqrt(sum))
	for i := range vector {
		vector[i] *= scale
	}
}

// RecordFromHit converts a semantic hit into a datasource record result.
func RecordFromHit(hit Hit) coredatasource.Record {
	return coredatasource.Record{
		ID:         hit.Ref.ID,
		Datasource: hit.Ref.Datasource,
		Entity:     hit.Ref.Entity,
		Title:      hit.Title,
		Content:    hit.Snippet,
		URL:        hit.URL,
		Score:      hit.Score,
		Metadata:   cloneStringMap(hit.Metadata),
	}
}
