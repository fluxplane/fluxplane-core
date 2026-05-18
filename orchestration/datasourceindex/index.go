// Package datasourceindex orchestrates semantic indexing for datasource corpus.
package datasourceindex

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	coredatasource "github.com/fluxplane/agentruntime/core/datasource"
	"github.com/fluxplane/agentruntime/runtime/datasource/semantic"
)

// Request selects datasource corpus to index.
type Request struct {
	Registry    *coredatasource.Registry
	Index       *semantic.Index
	Datasource  coredatasource.Name
	Entity      coredatasource.EntityType
	Full        bool
	DryRun      bool
	Limit       int
	Concurrency int
	Freshness   time.Duration
	Force       bool
	Now         func() time.Time
	IndexedOnly bool
	Phase       string
	Progress    ProgressReporter
}

// Result summarizes an indexing run.
type Result struct {
	Indexed   int
	Queued    int
	Skipped   int
	Deleted   int
	Failed    int
	Documents int
}

// ProgressReporter receives indexing progress events.
type ProgressReporter func(ProgressEvent)

// ProgressEvent describes one indexing progress observation.
type ProgressEvent struct {
	Kind       string
	Datasource coredatasource.Name
	Entity     coredatasource.EntityType
	RecordID   string
	Cursor     string
	NextCursor string
	Documents  int
	Indexed    int
	Queued     int
	Skipped    int
	Deleted    int
	Failed     int
	Tombstones int
	FreshUntil time.Time
	Message    string
	Phase      string
}

const (
	PhaseAll      = "all"
	PhaseFields   = "fields"
	PhaseEnqueue  = "enqueue"
	PhaseSemantic = "semantic"
	PhaseEmbed    = "embed"

	ProgressEntityStart      = "entity_start"
	ProgressPageFetched      = "page_fetched"
	ProgressDocumentIndexed  = "document_indexed"
	ProgressDocumentQueued   = "document_queued"
	ProgressDocumentSkipped  = "document_skipped"
	ProgressDocumentFailed   = "document_failed"
	ProgressEntityFresh      = "entity_fresh"
	ProgressTombstoneDeleted = "tombstone_deleted"
	ProgressTombstoneFailed  = "tombstone_failed"
	ProgressEntityComplete   = "entity_complete"
	ProgressComplete         = "complete"
)

// Build incrementally indexes corpus from selected datasources/entities.
func Build(ctx context.Context, req Request) (Result, error) {
	if req.Registry == nil {
		return Result{}, fmt.Errorf("datasource index: registry is nil")
	}
	phase := normalizedPhase(req.Phase)
	if req.Index == nil && !req.DryRun {
		return Result{}, fmt.Errorf("datasource index: semantic index is nil")
	}
	jobs := collectJobs(req, phase)
	concurrency := req.Concurrency
	if concurrency <= 0 {
		concurrency = 1
	}
	if concurrency > len(jobs) && len(jobs) > 0 {
		concurrency = len(jobs)
	}
	var out Result
	var outMu sync.Mutex
	seen := map[string]bool{}
	var seenMu sync.Mutex
	if concurrency > 0 {
		ctx, cancel := context.WithCancel(ctx)
		defer cancel()
		jobCh := make(chan indexJob)
		var wg sync.WaitGroup
		var firstErr error
		var errMu sync.Mutex
		setErr := func(err error) {
			if err == nil {
				return
			}
			errMu.Lock()
			if firstErr == nil {
				firstErr = err
				cancel()
			}
			errMu.Unlock()
		}
		for range concurrency {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for job := range jobCh {
					result, jobSeen, err := buildJob(ctx, req, job, phase)
					outMu.Lock()
					addResult(&out, result)
					outMu.Unlock()
					if len(jobSeen) > 0 {
						seenMu.Lock()
						for key := range jobSeen {
							seen[key] = true
						}
						seenMu.Unlock()
					}
					setErr(err)
				}
			}()
		}
		for _, job := range jobs {
			if ctx.Err() != nil {
				break
			}
			jobCh <- job
		}
		close(jobCh)
		wg.Wait()
		if firstErr != nil {
			return out, firstErr
		}
		if err := ctx.Err(); err != nil {
			return out, err
		}
	}
	if req.Full && !req.DryRun && req.Index != nil {
		status, err := req.Index.Status(ctx, semantic.StatusRequest{Datasource: req.Datasource, Entity: req.Entity})
		if err != nil {
			return out, err
		}
		deleted := map[string]bool{}
		for _, doc := range status.Documents {
			if seen[doc.Key] || deleted[doc.Key] {
				continue
			}
			if err := req.Index.Delete(ctx, doc.Ref); err != nil {
				out.Failed++
				report(req.Progress, ProgressEvent{Kind: ProgressTombstoneFailed, Datasource: doc.Ref.Datasource, Entity: doc.Ref.Entity, RecordID: doc.Ref.ID, Failed: out.Failed, Message: err.Error(), Phase: phase})
				continue
			}
			deleted[doc.Key] = true
			out.Deleted++
			report(req.Progress, ProgressEvent{Kind: ProgressTombstoneDeleted, Datasource: doc.Ref.Datasource, Entity: doc.Ref.Entity, RecordID: doc.Ref.ID, Deleted: out.Deleted, Phase: phase})
		}
		for _, record := range status.Records {
			if seen[record.Key] || deleted[record.Key] {
				continue
			}
			if err := req.Index.Delete(ctx, record.Ref); err != nil {
				out.Failed++
				report(req.Progress, ProgressEvent{Kind: ProgressTombstoneFailed, Datasource: record.Ref.Datasource, Entity: record.Ref.Entity, RecordID: record.Ref.ID, Failed: out.Failed, Message: err.Error(), Phase: phase})
				continue
			}
			deleted[record.Key] = true
			out.Deleted++
			report(req.Progress, ProgressEvent{Kind: ProgressTombstoneDeleted, Datasource: record.Ref.Datasource, Entity: record.Ref.Entity, RecordID: record.Ref.ID, Deleted: out.Deleted, Phase: phase})
		}
		for _, job := range status.Queue {
			if seen[job.Key] || deleted[job.Key] {
				continue
			}
			if err := req.Index.Delete(ctx, job.Ref); err != nil {
				out.Failed++
				report(req.Progress, ProgressEvent{Kind: ProgressTombstoneFailed, Datasource: job.Ref.Datasource, Entity: job.Ref.Entity, RecordID: job.Ref.ID, Failed: out.Failed, Message: err.Error(), Phase: phase})
				continue
			}
			deleted[job.Key] = true
			out.Deleted++
			report(req.Progress, ProgressEvent{Kind: ProgressTombstoneDeleted, Datasource: job.Ref.Datasource, Entity: job.Ref.Entity, RecordID: job.Ref.ID, Deleted: out.Deleted, Phase: phase})
		}
	}
	report(req.Progress, ProgressEvent{Kind: ProgressComplete, Documents: out.Documents, Indexed: out.Indexed, Queued: out.Queued, Skipped: out.Skipped, Deleted: out.Deleted, Failed: out.Failed, Phase: phase})
	return out, nil
}

type indexJob struct {
	spec            coredatasource.Spec
	entity          coredatasource.EntitySpec
	corpus          coredatasource.CorpusProvider
	buildFields     bool
	enqueueSemantic bool
}

func collectJobs(req Request, phase string) []indexJob {
	var jobs []indexJob
	for _, accessor := range req.Registry.All() {
		spec := accessor.Spec()
		if req.Datasource != "" && spec.Name != req.Datasource {
			continue
		}
		if req.IndexedOnly && !spec.Index.Enabled {
			continue
		}
		corpus, ok := accessor.(coredatasource.CorpusProvider)
		if !ok {
			continue
		}
		for _, entity := range accessor.Entities() {
			if req.Entity != "" && entity.Type != req.Entity {
				continue
			}
			buildFields := (phase == PhaseAll || phase == PhaseFields) && supportsIndex(entity)
			enqueueSemantic := (phase == PhaseAll || phase == PhaseEnqueue || phase == PhaseSemantic) && supportsSemantic(entity)
			if !buildFields && !enqueueSemantic {
				continue
			}
			jobs = append(jobs, indexJob{
				spec:            spec,
				entity:          entity,
				corpus:          corpus,
				buildFields:     buildFields,
				enqueueSemantic: enqueueSemantic,
			})
		}
	}
	return jobs
}

func buildJob(ctx context.Context, req Request, job indexJob, phase string) (Result, map[string]bool, error) {
	now := time.Now
	if req.Now != nil {
		now = req.Now
	}
	freshness, err := jobFreshness(req, job.spec)
	if err != nil {
		return Result{}, nil, err
	}
	if !req.DryRun && !req.Full && !req.Force && freshness > 0 && req.Index != nil {
		if run, ok, err := req.Index.IndexRun(ctx, semantic.IndexRunKey{Datasource: job.spec.Name, Entity: job.entity.Type, Phase: phase}); err != nil {
			return Result{}, nil, err
		} else if ok && run.Status == semantic.IndexRunStatusComplete && !run.CompletedAt.IsZero() {
			freshUntil := run.CompletedAt.Add(freshness)
			if now().Before(freshUntil) {
				report(req.Progress, ProgressEvent{Kind: ProgressEntityFresh, Datasource: job.spec.Name, Entity: job.entity.Type, FreshUntil: freshUntil, Phase: phase})
				return Result{Skipped: 1}, nil, nil
			}
		}
	}
	startedAt := now().UTC()
	if !req.DryRun && req.Index != nil {
		_ = req.Index.PutIndexRun(ctx, semantic.IndexRunState{
			Datasource: job.spec.Name,
			Entity:     job.entity.Type,
			Phase:      phase,
			Status:     semantic.IndexRunStatusRunning,
			StartedAt:  startedAt,
		})
	}
	entityResult := Result{}
	seen := map[string]bool{}
	report(req.Progress, ProgressEvent{Kind: ProgressEntityStart, Datasource: job.spec.Name, Entity: job.entity.Type, Phase: phase})
	cursor := ""
	for {
		if err := ctx.Err(); err != nil {
			finishRun(ctx, req, job, phase, startedAt, entityResult, err)
			return entityResult, seen, err
		}
		page, err := job.corpus.Corpus(ctx, coredatasource.CorpusRequest{Entity: job.entity.Type, Cursor: cursor, Limit: req.Limit})
		if err != nil {
			entityResult.Failed++
			report(req.Progress, ProgressEvent{Kind: ProgressEntityComplete, Datasource: job.spec.Name, Entity: job.entity.Type, Failed: entityResult.Failed, Message: err.Error(), Phase: phase})
			wrapped := fmt.Errorf("datasource index: %s/%s corpus: %w", job.spec.Name, job.entity.Type, err)
			finishRun(ctx, req, job, phase, startedAt, entityResult, wrapped)
			return entityResult, seen, wrapped
		}
		report(req.Progress, ProgressEvent{
			Kind:       ProgressPageFetched,
			Datasource: job.spec.Name,
			Entity:     job.entity.Type,
			Phase:      phase,
			Cursor:     cursor,
			NextCursor: page.NextCursor,
			Documents:  len(page.Documents),
			Tombstones: len(page.Tombstones),
		})
		for _, doc := range page.Documents {
			key := semantic.DocumentKey(doc.Ref)
			if key == "" {
				entityResult.Failed++
				report(req.Progress, ProgressEvent{Kind: ProgressDocumentFailed, Datasource: job.spec.Name, Entity: job.entity.Type, RecordID: doc.Ref.ID, Failed: entityResult.Failed, Message: "document ref is incomplete", Phase: phase})
				continue
			}
			seen[key] = true
			entityResult.Documents++
			if req.DryRun {
				entityResult.Skipped++
				report(req.Progress, ProgressEvent{Kind: ProgressDocumentSkipped, Datasource: job.spec.Name, Entity: job.entity.Type, RecordID: doc.Ref.ID, Documents: entityResult.Documents, Skipped: entityResult.Skipped, Phase: phase})
				continue
			}
			if job.buildFields {
				result, err := req.Index.UpdateRecord(ctx, doc, job.entity)
				if err != nil {
					entityResult.Failed++
					report(req.Progress, ProgressEvent{Kind: ProgressDocumentFailed, Datasource: job.spec.Name, Entity: job.entity.Type, RecordID: doc.Ref.ID, Documents: entityResult.Documents, Failed: entityResult.Failed, Message: err.Error(), Phase: PhaseFields})
					continue
				}
				if strings.TrimSpace(result.Status) == "indexed" {
					entityResult.Indexed++
					report(req.Progress, ProgressEvent{Kind: ProgressDocumentIndexed, Datasource: job.spec.Name, Entity: job.entity.Type, RecordID: doc.Ref.ID, Documents: entityResult.Documents, Indexed: entityResult.Indexed, Phase: PhaseFields})
				}
			}
			if job.enqueueSemantic {
				result, err := req.Index.Enqueue(ctx, doc)
				if err != nil {
					entityResult.Failed++
					report(req.Progress, ProgressEvent{Kind: ProgressDocumentFailed, Datasource: job.spec.Name, Entity: job.entity.Type, RecordID: doc.Ref.ID, Documents: entityResult.Documents, Failed: entityResult.Failed, Message: err.Error(), Phase: PhaseEnqueue})
					continue
				}
				switch strings.TrimSpace(result.Status) {
				case semantic.QueueStatusQueued:
					entityResult.Queued++
					report(req.Progress, ProgressEvent{Kind: ProgressDocumentQueued, Datasource: job.spec.Name, Entity: job.entity.Type, RecordID: doc.Ref.ID, Documents: entityResult.Documents, Queued: entityResult.Queued, Phase: PhaseEnqueue})
				default:
					entityResult.Skipped++
					report(req.Progress, ProgressEvent{Kind: ProgressDocumentSkipped, Datasource: job.spec.Name, Entity: job.entity.Type, RecordID: doc.Ref.ID, Documents: entityResult.Documents, Skipped: entityResult.Skipped, Phase: PhaseEnqueue})
				}
			}
		}
		for _, ref := range page.Tombstones {
			if req.DryRun {
				continue
			}
			if err := req.Index.Delete(ctx, ref); err != nil {
				entityResult.Failed++
				report(req.Progress, ProgressEvent{Kind: ProgressTombstoneFailed, Datasource: job.spec.Name, Entity: job.entity.Type, RecordID: ref.ID, Failed: entityResult.Failed, Message: err.Error(), Phase: phase})
				continue
			}
			entityResult.Deleted++
			report(req.Progress, ProgressEvent{Kind: ProgressTombstoneDeleted, Datasource: job.spec.Name, Entity: job.entity.Type, RecordID: ref.ID, Deleted: entityResult.Deleted, Phase: phase})
		}
		if page.NextCursor == "" {
			break
		}
		cursor = page.NextCursor
	}
	report(req.Progress, ProgressEvent{Kind: ProgressEntityComplete, Datasource: job.spec.Name, Entity: job.entity.Type, Documents: entityResult.Documents, Indexed: entityResult.Indexed, Queued: entityResult.Queued, Skipped: entityResult.Skipped, Deleted: entityResult.Deleted, Failed: entityResult.Failed, Phase: phase})
	finishRun(ctx, req, job, phase, startedAt, entityResult, nil)
	return entityResult, seen, nil
}

func finishRun(ctx context.Context, req Request, job indexJob, phase string, startedAt time.Time, result Result, err error) {
	if req.DryRun || req.Index == nil {
		return
	}
	state := semantic.IndexRunState{
		Datasource:  job.spec.Name,
		Entity:      job.entity.Type,
		Phase:       phase,
		Status:      semantic.IndexRunStatusComplete,
		StartedAt:   startedAt,
		CompletedAt: time.Now().UTC(),
		Documents:   result.Documents,
		Indexed:     result.Indexed,
		Queued:      result.Queued,
		Skipped:     result.Skipped,
		Deleted:     result.Deleted,
		Failed:      result.Failed,
	}
	if req.Now != nil {
		state.CompletedAt = req.Now().UTC()
	}
	if err != nil || result.Failed > 0 {
		state.Status = semantic.IndexRunStatusFailed
		if err != nil {
			state.LastError = err.Error()
		}
	}
	_ = req.Index.PutIndexRun(context.WithoutCancel(ctx), state)
}

func addResult(out *Result, result Result) {
	out.Indexed += result.Indexed
	out.Queued += result.Queued
	out.Skipped += result.Skipped
	out.Deleted += result.Deleted
	out.Failed += result.Failed
	out.Documents += result.Documents
}

func jobFreshness(req Request, spec coredatasource.Spec) (time.Duration, error) {
	value := strings.TrimSpace(spec.Index.Freshness)
	if value == "" {
		return req.Freshness, nil
	}
	freshness, err := time.ParseDuration(value)
	if err != nil {
		return 0, fmt.Errorf("datasource index: %s index freshness: %w", spec.Name, err)
	}
	return freshness, nil
}

func report(reporter ProgressReporter, event ProgressEvent) {
	if reporter != nil {
		reporter(event)
	}
}

func supportsSemantic(entity coredatasource.EntitySpec) bool {
	for _, capability := range entity.Capabilities {
		if capability == coredatasource.EntityCapabilitySemanticSearch {
			return true
		}
	}
	return false
}

func supportsIndex(entity coredatasource.EntitySpec) bool {
	for _, capability := range entity.Capabilities {
		if capability == coredatasource.EntityCapabilityIndex {
			return true
		}
	}
	return false
}

func normalizedPhase(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", PhaseAll:
		return PhaseAll
	case PhaseFields:
		return PhaseFields
	case PhaseEnqueue, PhaseSemantic:
		return PhaseEnqueue
	default:
		return PhaseAll
	}
}
