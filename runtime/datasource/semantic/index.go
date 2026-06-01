// Package semantic provides incremental semantic indexing for datasource corpus.
package semantic

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/fluxplane/fluxplane-core/runtime/datasource/mirror"
	coredatasource "github.com/fluxplane/fluxplane-datasource"
)

const (
	defaultEmbeddingModel = "local/hash-embedding"
	defaultTargetTokens   = 350
	defaultOverlapTokens  = 50
)

// Embedder embeds text into vector space.
type Embedder interface {
	Model() string
	Embed(context.Context, []string) ([][]float32, error)
}

// VectorStore persists embedded chunks and performs vector similarity search.
type VectorStore interface {
	UpsertChunks(context.Context, []EmbeddedChunk) error
	DeleteChunks(context.Context, DeleteRequest) error
	Search(context.Context, VectorSearchRequest) ([]VectorHit, error)
}

// MetadataStore persists per-document incremental indexing state.
type MetadataStore interface {
	Document(context.Context, string) (DocumentState, bool, error)
	PutDocument(context.Context, DocumentState) error
	DeleteDocument(context.Context, string) error
	Documents(context.Context, StatusRequest) ([]DocumentState, error)
}

// QueueStore persists semantic corpus work that should be embedded off-track.
type QueueStore interface {
	PutQueueJob(context.Context, QueueJob) error
	DeleteQueueJob(context.Context, string) error
	QueueJobs(context.Context, QueueRequest) ([]QueueJob, error)
	QueueStatus(context.Context, StatusRequest) ([]QueueState, error)
}

// Store is the complete persistence dependency required by Index.
type Store interface {
	VectorStore
	MetadataStore
	mirror.Store
	QueueStore
}

// Index coordinates chunking, embedding, vector writes, and metadata state.
type Index struct {
	embedder Embedder
	store    Store
	mirror   *mirror.Service
	config   Config
}

// Config configures semantic indexing behavior.
type Config struct {
	Model     string
	Chunking  coredatasource.ChunkingSpec
	Retrieval coredatasource.RetrievalSpec
}

// New returns a semantic index service.
func New(embedder Embedder, store Store, cfg Config) (*Index, error) {
	if embedder == nil {
		embedder = HashEmbedder{ModelName: firstNonEmpty(cfg.Model, defaultEmbeddingModel)}
	}
	if store == nil {
		return nil, fmt.Errorf("semantic: store is nil")
	}
	if strings.TrimSpace(cfg.Model) == "" {
		cfg.Model = embedder.Model()
	}
	mirrorService, err := mirror.New(store)
	if err != nil {
		return nil, err
	}
	return &Index{embedder: embedder, store: store, mirror: mirrorService, config: cfg}, nil
}

// Close releases resources held by the underlying embedder, when supported.
func (i *Index) Close() error {
	if i == nil || i.embedder == nil {
		return nil
	}
	closer, ok := i.embedder.(interface{ Close() error })
	if !ok {
		return nil
	}
	return closer.Close()
}

// Update incrementally indexes one corpus document.
func (i *Index) Update(ctx context.Context, doc coredatasource.CorpusDocument) (UpdateResult, error) {
	if i == nil {
		return UpdateResult{}, fmt.Errorf("semantic: index is nil")
	}
	key := DocumentKey(doc.Ref)
	if key == "" {
		return UpdateResult{}, fmt.Errorf("semantic: document ref is incomplete")
	}
	fingerprint := documentFingerprint(doc)
	policyHash := PolicyHash(effectiveChunking(i.config.Chunking))
	previous, ok, err := i.store.Document(ctx, key)
	if err != nil {
		return UpdateResult{}, err
	}
	if ok && previous.Status == "indexed" && previous.Fingerprint == fingerprint && previous.EmbeddingModel == i.embedder.Model() && previous.ChunkingPolicyHash == policyHash {
		return UpdateResult{Key: key, Status: "skipped", Chunks: previous.ChunkCount}, nil
	}
	chunks := i.planChunks(doc)
	texts := make([]string, 0, len(chunks))
	for _, chunk := range chunks {
		texts = append(texts, chunk.Text)
	}
	vectors, err := i.embedder.Embed(ctx, texts)
	if err != nil {
		state := DocumentState{
			Key:                key,
			Ref:                doc.Ref,
			Fingerprint:        fingerprint,
			UpdatedAt:          doc.UpdatedAt,
			EmbeddingModel:     i.embedder.Model(),
			ChunkingPolicyHash: policyHash,
			Status:             "failed",
			LastError:          err.Error(),
			IndexedAt:          time.Now().UTC(),
		}
		_ = i.store.PutDocument(ctx, state)
		return UpdateResult{}, err
	}
	embedded := make([]EmbeddedChunk, 0, len(chunks))
	for idx, chunk := range chunks {
		embedded = append(embedded, EmbeddedChunk{
			Chunk:  chunk,
			Vector: vectors[idx],
		})
	}
	if err := i.store.DeleteChunks(ctx, DeleteRequest{DocumentKey: key}); err != nil {
		return UpdateResult{}, err
	}
	if err := i.store.UpsertChunks(ctx, embedded); err != nil {
		return UpdateResult{}, err
	}
	state := DocumentState{
		Key:                key,
		Ref:                doc.Ref,
		Fingerprint:        fingerprint,
		UpdatedAt:          doc.UpdatedAt,
		EmbeddingModel:     i.embedder.Model(),
		ChunkingPolicyHash: policyHash,
		IndexedAt:          time.Now().UTC(),
		ChunkCount:         len(embedded),
		Status:             "indexed",
	}
	if err := i.store.PutDocument(ctx, state); err != nil {
		return UpdateResult{}, err
	}
	return UpdateResult{Key: key, Status: "indexed", Chunks: len(embedded)}, nil
}

// UpdateRecord indexes structured fields for one corpus document.
func (i *Index) UpdateRecord(ctx context.Context, doc coredatasource.CorpusDocument, entity coredatasource.EntitySpec) (UpdateResult, error) {
	if i == nil {
		return UpdateResult{}, fmt.Errorf("semantic: index is nil")
	}
	result, err := i.mirror.UpdateRecord(ctx, doc, entity)
	if err != nil {
		return UpdateResult{}, err
	}
	return UpdateResult{Key: result.Key, Status: result.Status}, nil
}

// UpdateRecords indexes structured fields for a corpus document batch.
func (i *Index) UpdateRecords(ctx context.Context, docs []coredatasource.CorpusDocument, entity coredatasource.EntitySpec) ([]UpdateResult, error) {
	if i == nil {
		return nil, fmt.Errorf("semantic: index is nil")
	}
	results, err := i.mirror.UpdateRecords(ctx, docs, entity)
	if err != nil {
		return nil, err
	}
	out := make([]UpdateResult, 0, len(results))
	for _, result := range results {
		out = append(out, UpdateResult{Key: result.Key, Status: result.Status})
	}
	return out, nil
}

// Enqueue stores one corpus document for asynchronous semantic embedding.
func (i *Index) Enqueue(ctx context.Context, doc coredatasource.CorpusDocument) (UpdateResult, error) {
	if i == nil {
		return UpdateResult{}, fmt.Errorf("semantic: index is nil")
	}
	key := DocumentKey(doc.Ref)
	if key == "" {
		return UpdateResult{}, fmt.Errorf("semantic: document ref is incomplete")
	}
	fingerprint := documentFingerprint(doc)
	policyHash := PolicyHash(effectiveChunking(i.config.Chunking))
	previous, ok, err := i.store.Document(ctx, key)
	if err != nil {
		return UpdateResult{}, err
	}
	if ok && previous.Status == QueueStatusIndexed && previous.Fingerprint == fingerprint && previous.EmbeddingModel == i.config.Model && previous.ChunkingPolicyHash == policyHash {
		if err := i.store.DeleteQueueJob(ctx, key); err != nil {
			return UpdateResult{}, err
		}
		return UpdateResult{Key: key, Status: "skipped", Chunks: previous.ChunkCount}, nil
	}
	now := time.Now().UTC()
	if err := i.store.PutQueueJob(ctx, QueueJob{
		Key:                key,
		Ref:                doc.Ref,
		Document:           doc,
		Fingerprint:        fingerprint,
		EmbeddingModel:     i.config.Model,
		ChunkingPolicyHash: policyHash,
		Status:             QueueStatusQueued,
		EnqueuedAt:         now,
		UpdatedAt:          now,
	}); err != nil {
		return UpdateResult{}, err
	}
	return UpdateResult{Key: key, Status: QueueStatusQueued}, nil
}

// ProcessQueue drains queued semantic corpus work and embeds it.
func (i *Index) ProcessQueue(ctx context.Context, req ProcessQueueRequest) (ProcessQueueResult, error) {
	if i == nil {
		return ProcessQueueResult{}, fmt.Errorf("semantic: index is nil")
	}
	jobs, err := i.store.QueueJobs(ctx, QueueRequest{
		Datasource: req.Datasource,
		Entity:     req.Entity,
		Statuses:   []string{QueueStatusQueued, QueueStatusFailed},
		Limit:      req.Limit,
	})
	if err != nil {
		return ProcessQueueResult{}, err
	}
	out := ProcessQueueResult{Queued: len(jobs)}
	reportQueue(req.Progress, QueueProgressEvent{Kind: QueueProgressStart, Queued: out.Queued})
	for _, job := range jobs {
		if err := ctx.Err(); err != nil {
			return out, err
		}
		result, err := i.Update(ctx, job.Document)
		if err != nil {
			out.Failed++
			job.Status = QueueStatusFailed
			job.LastError = err.Error()
			job.Attempts++
			job.UpdatedAt = time.Now().UTC()
			_ = i.store.PutQueueJob(context.WithoutCancel(ctx), job)
			reportQueue(req.Progress, QueueProgressEvent{Kind: QueueProgressFailed, Datasource: job.Ref.Datasource, Entity: job.Ref.Entity, RecordID: job.Ref.ID, Status: job.Status, Message: err.Error(), Embedded: out.Embedded, Skipped: out.Skipped, Failed: out.Failed})
			continue
		}
		if err := i.store.DeleteQueueJob(ctx, job.Key); err != nil {
			return out, err
		}
		switch strings.TrimSpace(result.Status) {
		case QueueStatusIndexed:
			out.Embedded++
			reportQueue(req.Progress, QueueProgressEvent{Kind: QueueProgressEmbedded, Datasource: job.Ref.Datasource, Entity: job.Ref.Entity, RecordID: job.Ref.ID, Status: result.Status, Embedded: out.Embedded, Skipped: out.Skipped, Failed: out.Failed})
		default:
			out.Skipped++
			reportQueue(req.Progress, QueueProgressEvent{Kind: QueueProgressSkipped, Datasource: job.Ref.Datasource, Entity: job.Ref.Entity, RecordID: job.Ref.ID, Status: result.Status, Embedded: out.Embedded, Skipped: out.Skipped, Failed: out.Failed})
		}
	}
	reportQueue(req.Progress, QueueProgressEvent{Kind: QueueProgressComplete, Queued: out.Queued, Embedded: out.Embedded, Skipped: out.Skipped, Failed: out.Failed})
	return out, nil
}

// Delete removes one record from the semantic index.
func (i *Index) Delete(ctx context.Context, ref coredatasource.RecordRef) error {
	key := DocumentKey(ref)
	if key == "" {
		return nil
	}
	if err := i.store.DeleteChunks(ctx, DeleteRequest{DocumentKey: key}); err != nil {
		return err
	}
	if err := i.store.DeleteDocument(ctx, key); err != nil {
		return err
	}
	if err := i.store.DeleteRecord(ctx, ref); err != nil {
		return err
	}
	return i.store.DeleteQueueJob(ctx, key)
}

var (
	ErrFieldIndexNotConfigured = mirror.ErrNotConfigured
	ErrFieldIndexNotBuilt      = mirror.ErrNotBuilt
)

// FieldLookupRequest describes a structured field-index lookup.
type FieldLookupRequest struct {
	Index      *Index
	Datasource coredatasource.Name
	Entity     coredatasource.EntityType
	Query      string
	Filters    map[string]string
	Limit      int
	Cursor     string
}

// FieldLookupResult contains field-index records and pagination state.
type FieldLookupResult struct {
	Records    []coredatasource.Record
	NextCursor string
	Complete   bool
}

// SearchFieldIndex performs a datasource field-index lookup with readiness and
// cursor handling.
func SearchFieldIndex(ctx context.Context, req FieldLookupRequest) (FieldLookupResult, error) {
	if err := RequireFieldIndexBuilt(ctx, req.Index, req.Datasource, req.Entity); err != nil {
		return FieldLookupResult{}, err
	}
	limit := req.Limit
	if limit <= 0 {
		limit = 10
	}
	offset, err := fieldCursorOffset(req.Cursor)
	if err != nil {
		return FieldLookupResult{}, err
	}
	result, err := req.Index.SearchFields(ctx, FieldSearchRequest{
		Query:       req.Query,
		Datasources: []coredatasource.Name{req.Datasource},
		Entities:    []coredatasource.EntityType{req.Entity},
		Filters:     req.Filters,
		Limit:       limit + 1,
		Offset:      offset,
	})
	if err != nil {
		return FieldLookupResult{}, err
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
	return FieldLookupResult{Records: records, NextCursor: next, Complete: next == ""}, nil
}

// GetFieldRecord returns one exact field-index record by datasource/entity/id.
func GetFieldRecord(ctx context.Context, index *Index, datasource coredatasource.Name, entity coredatasource.EntityType, id string) (coredatasource.Record, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return coredatasource.Record{}, coredatasource.ErrNotFound
	}
	if err := RequireFieldIndexBuilt(ctx, index, datasource, entity); err != nil {
		return coredatasource.Record{}, err
	}
	return index.FieldRecord(ctx, coredatasource.RecordRef{Datasource: datasource, Entity: entity, ID: id})
}

// RequireFieldIndexBuilt reports whether field records exist or a field/all
// indexing run completed for a datasource entity.
func RequireFieldIndexBuilt(ctx context.Context, index *Index, datasource coredatasource.Name, entity coredatasource.EntityType) error {
	if index == nil {
		return fmt.Errorf("%w for %s/%s", ErrFieldIndexNotConfigured, datasource, entity)
	}
	return mirror.RequireBuilt(ctx, index.mirror, datasource, entity)
}

func fieldCursorOffset(cursor string) (int, error) {
	cursor = strings.TrimSpace(cursor)
	if cursor == "" {
		return 0, nil
	}
	offset, err := strconv.Atoi(cursor)
	if err != nil || offset < 0 {
		return 0, fmt.Errorf("invalid field index cursor %q", cursor)
	}
	return offset, nil
}

// Search performs semantic vector retrieval.
func (i *Index) Search(ctx context.Context, req SearchRequest) (SearchResult, error) {
	if i == nil {
		return SearchResult{}, fmt.Errorf("semantic: index is nil")
	}
	query := strings.TrimSpace(req.Query)
	if query == "" {
		return SearchResult{}, fmt.Errorf("semantic: query is empty")
	}
	limit := req.Limit
	if limit <= 0 {
		limit = firstPositive(i.config.Retrieval.Limit, 10)
	}
	minScore := req.MinScore
	if minScore == 0 {
		minScore = i.config.Retrieval.MinScore
	}
	vectors, err := i.embedder.Embed(ctx, []string{query})
	if err != nil {
		return SearchResult{}, err
	}
	hits, err := i.store.Search(ctx, VectorSearchRequest{
		Vector:      vectors[0],
		Datasources: req.Datasources,
		Entities:    req.Entities,
		Limit:       limit * 3,
		MinScore:    minScore,
	})
	if err != nil {
		return SearchResult{}, err
	}
	grouped := map[string]Hit{}
	for _, hit := range hits {
		key := hit.Chunk.DocumentKey
		current := grouped[key]
		if current.Ref.Datasource == "" || hit.Score > current.Score {
			grouped[key] = Hit{
				Ref:      hit.Chunk.Ref,
				Title:    hit.Chunk.Title,
				URL:      hit.Chunk.URL,
				Snippet:  hit.Chunk.Text,
				Metadata: cloneStringMap(hit.Chunk.Metadata),
				Score:    hit.Score,
			}
		}
	}
	out := make([]Hit, 0, len(grouped))
	for _, hit := range grouped {
		record, err := i.mirror.Record(ctx, hit.Ref)
		if err == nil {
			hit.Title = firstNonEmpty(record.Title, hit.Title)
			hit.URL = firstNonEmpty(record.URL, hit.URL)
			hit.Metadata = cloneStringMap(record.Metadata)
		} else if err != nil && !errors.Is(err, coredatasource.ErrNotFound) {
			return SearchResult{}, err
		}
		out = append(out, hit)
	}
	sort.Slice(out, func(a, b int) bool {
		if out[a].Score != out[b].Score {
			return out[a].Score > out[b].Score
		}
		if out[a].Ref.ID != out[b].Ref.ID {
			return out[a].Ref.ID < out[b].Ref.ID
		}
		if out[a].Ref.Datasource != out[b].Ref.Datasource {
			return out[a].Ref.Datasource < out[b].Ref.Datasource
		}
		return out[a].Ref.Entity < out[b].Ref.Entity
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return SearchResult{Hits: out}, nil
}

// SearchFields performs structured field search.
func (i *Index) SearchFields(ctx context.Context, req FieldSearchRequest) (FieldSearchResult, error) {
	if i == nil {
		return FieldSearchResult{}, fmt.Errorf("semantic: index is nil")
	}
	result, err := i.mirror.SearchRecords(ctx, req)
	if err != nil {
		return FieldSearchResult{}, err
	}
	return result, nil
}

// FieldRecord returns one exact structured field-index record.
func (i *Index) FieldRecord(ctx context.Context, ref coredatasource.RecordRef) (coredatasource.Record, error) {
	if i == nil {
		return coredatasource.Record{}, fmt.Errorf("semantic: index is nil")
	}
	return i.mirror.Record(ctx, ref)
}

// Status returns index metadata rows matching a filter.
func (i *Index) Status(ctx context.Context, req StatusRequest) (StatusResult, error) {
	docs, err := i.store.Documents(ctx, req)
	if err != nil {
		return StatusResult{}, err
	}
	mirrorStatus, err := i.mirror.Status(ctx, mirror.StatusRequest{Datasource: req.Datasource, Entity: req.Entity})
	if err != nil {
		return StatusResult{}, err
	}
	queue, err := i.store.QueueStatus(ctx, req)
	if err != nil {
		return StatusResult{}, err
	}
	return StatusResult{Documents: docs, Records: mirrorStatus.Records, Queue: queue, Runs: mirrorStatus.Runs}, nil
}

// PutIndexRun stores per datasource/entity indexing metadata.
func (i *Index) PutIndexRun(ctx context.Context, run IndexRunState) error {
	if i == nil {
		return fmt.Errorf("semantic: index is nil")
	}
	return i.mirror.PutRun(ctx, run)
}

// IndexRun returns the latest stored run state for one datasource/entity phase.
func (i *Index) IndexRun(ctx context.Context, key IndexRunKey) (IndexRunState, bool, error) {
	if i == nil {
		return IndexRunState{}, false, fmt.Errorf("semantic: index is nil")
	}
	return i.mirror.Run(ctx, key)
}

// DeleteIndexRuns removes stored run checkpoints matching req.
func (i *Index) DeleteIndexRuns(ctx context.Context, req StatusRequest) error {
	if i == nil {
		return fmt.Errorf("semantic: index is nil")
	}
	return i.mirror.DeleteRuns(ctx, mirror.StatusRequest{Datasource: req.Datasource, Entity: req.Entity})
}

func (i *Index) planChunks(doc coredatasource.CorpusDocument) []Chunk {
	key := DocumentKey(doc.Ref)
	if len(doc.Chunks) > 0 {
		out := make([]Chunk, 0, len(doc.Chunks))
		for n, chunk := range doc.Chunks {
			text := strings.TrimSpace(chunk.Text)
			if text == "" {
				continue
			}
			id := strings.TrimSpace(chunk.ID)
			if id == "" {
				id = chunkID(key, n, text)
			}
			out = append(out, Chunk{
				ID:          id,
				DocumentKey: key,
				Ref:         doc.Ref,
				Title:       firstNonEmpty(chunk.Title, doc.Title),
				Text:        text,
				URL:         doc.URL,
				Ordinal:     n,
				Start:       chunk.Start,
				End:         chunk.End,
				Metadata:    mergeStringMaps(doc.Metadata, chunk.Metadata),
			})
		}
		return out
	}
	text := strings.TrimSpace(strings.Join([]string{doc.Title, doc.Body}, "\n\n"))
	if text == "" {
		return nil
	}
	chunking := effectiveChunking(i.config.Chunking)
	target := chunking.TargetTokens * 4
	overlap := chunking.OverlapTokens * 4
	if overlap >= target {
		overlap = target / 5
	}
	parts := textChunks(text, target, overlap)
	out := make([]Chunk, 0, len(parts))
	for n, part := range parts {
		out = append(out, Chunk{
			ID:          chunkID(key, n, part),
			DocumentKey: key,
			Ref:         doc.Ref,
			Title:       doc.Title,
			Text:        part,
			URL:         doc.URL,
			Ordinal:     n,
			Metadata:    cloneStringMap(doc.Metadata),
		})
	}
	return out
}

// SearchRequest describes a semantic retrieval query.
type SearchRequest struct {
	Query       string
	Datasources []coredatasource.Name
	Entities    []coredatasource.EntityType
	Limit       int
	MinScore    float64
}

// SearchResult contains semantic hits.
type SearchResult struct {
	Hits []Hit
}

// Hit is one grouped semantic retrieval hit.
type Hit struct {
	Ref      coredatasource.RecordRef
	Title    string
	URL      string
	Snippet  string
	Metadata map[string]string
	Score    float64
}

// UpdateResult describes one incremental indexing decision.
type UpdateResult struct {
	Key    string
	Status string
	Chunks int
}

// StatusRequest filters index status rows.
type StatusRequest struct {
	Datasource coredatasource.Name
	Entity     coredatasource.EntityType
}

// StatusResult describes indexed documents.
type StatusResult struct {
	Documents []DocumentState
	Records   []FieldRecordState
	Queue     []QueueState
	Runs      []IndexRunState
}

const (
	IndexRunStatusRunning  = mirror.RunStatusRunning
	IndexRunStatusComplete = mirror.RunStatusComplete
	IndexRunStatusFailed   = mirror.RunStatusFailed
)

// IndexRunKey identifies one datasource/entity index run checkpoint.
type IndexRunKey = mirror.RunKey

// IndexRunState is the persisted status for one datasource/entity indexing run.
type IndexRunState = mirror.RunState

// FieldSearchRequest describes a structured field search query.
type FieldSearchRequest = mirror.SearchRequest

// FieldSearchResult contains structured field hits.
type FieldSearchResult = mirror.SearchResult

// FieldHit is one structured field search result.
type FieldHit = mirror.Hit

// FieldRecord is one indexed structured datasource record.
type FieldRecord = mirror.Record

// FieldRecordState is the status row for one indexed field record.
type FieldRecordState = mirror.RecordState

const (
	QueueStatusQueued  = "queued"
	QueueStatusFailed  = "failed"
	QueueStatusIndexed = "indexed"
)

const (
	QueueProgressStart    = "queue_start"
	QueueProgressEmbedded = "queue_embedded"
	QueueProgressSkipped  = "queue_skipped"
	QueueProgressFailed   = "queue_failed"
	QueueProgressComplete = "queue_complete"
)

// QueueRequest filters queued semantic embedding jobs.
type QueueRequest struct {
	Datasource coredatasource.Name
	Entity     coredatasource.EntityType
	Statuses   []string
	Limit      int
}

// QueueJob is one persisted semantic embedding work item.
type QueueJob struct {
	Key                string                        `json:"key"`
	Ref                coredatasource.RecordRef      `json:"ref"`
	Document           coredatasource.CorpusDocument `json:"document"`
	Fingerprint        string                        `json:"fingerprint,omitempty"`
	EmbeddingModel     string                        `json:"embedding_model,omitempty"`
	ChunkingPolicyHash string                        `json:"chunking_policy_hash,omitempty"`
	Status             string                        `json:"status,omitempty"`
	LastError          string                        `json:"last_error,omitempty"`
	Attempts           int                           `json:"attempts,omitempty"`
	EnqueuedAt         time.Time                     `json:"enqueued_at,omitempty"`
	UpdatedAt          time.Time                     `json:"updated_at,omitempty"`
}

// QueueState is the status row for one queued semantic embedding job.
type QueueState struct {
	Key        string                   `json:"key"`
	Ref        coredatasource.RecordRef `json:"ref"`
	Status     string                   `json:"status,omitempty"`
	LastError  string                   `json:"last_error,omitempty"`
	Attempts   int                      `json:"attempts,omitempty"`
	EnqueuedAt time.Time                `json:"enqueued_at,omitempty"`
	UpdatedAt  time.Time                `json:"updated_at,omitempty"`
}

// ProcessQueueRequest selects queued semantic embedding jobs to process.
type ProcessQueueRequest struct {
	Datasource coredatasource.Name
	Entity     coredatasource.EntityType
	Limit      int
	Progress   QueueProgressReporter
}

// ProcessQueueResult summarizes a semantic embedding queue drain.
type ProcessQueueResult struct {
	Queued   int
	Embedded int
	Skipped  int
	Failed   int
}

// QueueProgressReporter receives semantic embedding queue progress.
type QueueProgressReporter func(QueueProgressEvent)

// QueueProgressEvent describes one semantic embedding queue observation.
type QueueProgressEvent struct {
	Kind       string
	Datasource coredatasource.Name
	Entity     coredatasource.EntityType
	RecordID   string
	Status     string
	Message    string
	Queued     int
	Embedded   int
	Skipped    int
	Failed     int
}

// DocumentState is the persisted incremental state for one record.
type DocumentState struct {
	Key                string                   `json:"key"`
	Ref                coredatasource.RecordRef `json:"ref"`
	Fingerprint        string                   `json:"fingerprint,omitempty"`
	UpdatedAt          string                   `json:"updated_at,omitempty"`
	EmbeddingModel     string                   `json:"embedding_model,omitempty"`
	ChunkingPolicyHash string                   `json:"chunking_policy_hash,omitempty"`
	IndexedAt          time.Time                `json:"indexed_at,omitempty"`
	ChunkCount         int                      `json:"chunk_count,omitempty"`
	Status             string                   `json:"status,omitempty"`
	LastError          string                   `json:"last_error,omitempty"`
}

func reportQueue(reporter QueueProgressReporter, event QueueProgressEvent) {
	if reporter != nil {
		reporter(event)
	}
}

// Chunk is one planned index chunk.
type Chunk struct {
	ID          string                   `json:"id"`
	DocumentKey string                   `json:"document_key"`
	Ref         coredatasource.RecordRef `json:"ref"`
	Title       string                   `json:"title,omitempty"`
	Text        string                   `json:"text,omitempty"`
	URL         string                   `json:"url,omitempty"`
	Ordinal     int                      `json:"ordinal,omitempty"`
	Start       int                      `json:"start,omitempty"`
	End         int                      `json:"end,omitempty"`
	Metadata    map[string]string        `json:"metadata,omitempty"`
}

// EmbeddedChunk is one chunk with its embedding vector.
type EmbeddedChunk struct {
	Chunk  Chunk     `json:"chunk"`
	Vector []float32 `json:"vector"`
}

// DeleteRequest describes chunk deletion.
type DeleteRequest struct {
	DocumentKey string
}

// VectorSearchRequest describes vector-store search filters.
type VectorSearchRequest struct {
	Vector      []float32
	Datasources []coredatasource.Name
	Entities    []coredatasource.EntityType
	Limit       int
	MinScore    float64
}

// VectorHit is one raw vector hit.
type VectorHit struct {
	Chunk Chunk
	Score float64
}

// DocumentKey returns the stable key for a datasource record.
func DocumentKey(ref coredatasource.RecordRef) string {
	if ref.Datasource == "" || ref.Entity == "" || strings.TrimSpace(ref.ID) == "" {
		return ""
	}
	return string(ref.Datasource) + "\x00" + string(ref.Entity) + "\x00" + strings.TrimSpace(ref.ID)
}

// PolicyHash returns a stable hash for chunking policy.
func PolicyHash(policy coredatasource.ChunkingSpec) string {
	data, _ := json.Marshal(policy)
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func effectiveChunking(policy coredatasource.ChunkingSpec) coredatasource.ChunkingSpec {
	if strings.TrimSpace(policy.Strategy) == "" {
		policy.Strategy = "text"
	}
	if policy.TargetTokens <= 0 {
		policy.TargetTokens = defaultTargetTokens
	}
	if policy.OverlapTokens <= 0 {
		policy.OverlapTokens = defaultOverlapTokens
	}
	return policy
}

func documentFingerprint(doc coredatasource.CorpusDocument) string {
	if strings.TrimSpace(doc.Fingerprint) != "" {
		return strings.TrimSpace(doc.Fingerprint)
	}
	data, _ := json.Marshal(struct {
		Title     string
		Body      string
		URL       string
		Metadata  map[string]string
		UpdatedAt string
		Chunks    []coredatasource.CorpusChunk
	}{doc.Title, doc.Body, doc.URL, doc.Metadata, doc.UpdatedAt, doc.Chunks})
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func chunkID(key string, ordinal int, text string) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s\x00%d\x00%s", key, ordinal, text)))
	return hex.EncodeToString(sum[:])
}

func textChunks(text string, target, overlap int) []string {
	text = strings.TrimSpace(text)
	if len(text) <= target {
		return []string{text}
	}
	var out []string
	for start := 0; start < len(text); {
		end := start + target
		if end >= len(text) {
			out = append(out, strings.TrimSpace(text[start:]))
			break
		}
		cut := strings.LastIndex(text[start:end], "\n\n")
		if cut < target/3 {
			cut = strings.LastIndex(text[start:end], "\n")
		}
		if cut < target/3 {
			cut = strings.LastIndex(text[start:end], " ")
		}
		if cut < target/3 {
			cut = target
		}
		end = start + cut
		out = append(out, strings.TrimSpace(text[start:end]))
		next := end - overlap
		if next <= start {
			next = end
		}
		start = next
	}
	return out
}

func cosine(a, b []float32) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	var dot, aa, bb float64
	for idx := 0; idx < n; idx++ {
		av := float64(a[idx])
		bv := float64(b[idx])
		dot += av * bv
		aa += av * av
		bb += bv * bv
	}
	if aa == 0 || bb == 0 {
		return 0
	}
	return dot / (math.Sqrt(aa) * math.Sqrt(bb))
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}

func firstPositive(values ...int) int {
	for _, value := range values {
		if value > 0 {
			return value
		}
	}
	return 0
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

func mergeStringMaps(a, b map[string]string) map[string]string {
	out := cloneStringMap(a)
	if out == nil && len(b) > 0 {
		out = map[string]string{}
	}
	for key, value := range b {
		out[key] = value
	}
	return out
}

func containsDatasource(values []coredatasource.Name, value coredatasource.Name) bool {
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

func containsEntity(values []coredatasource.EntityType, value coredatasource.EntityType) bool {
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
