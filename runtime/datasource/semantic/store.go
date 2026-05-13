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
	Chunks    []EmbeddedChunk          `json:"chunks,omitempty"`
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
