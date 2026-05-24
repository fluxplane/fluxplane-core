// Package datasourceindex orchestrates semantic indexing for datasource corpus.
package datasourceindex

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	coredata "github.com/fluxplane/fluxplane-core/core/data"
	coredatasource "github.com/fluxplane/fluxplane-core/core/datasource"
	runtimedata "github.com/fluxplane/fluxplane-core/runtime/data"
	"github.com/fluxplane/fluxplane-core/runtime/datasource/semantic"
)

// Request selects datasource corpus to index.
type Request struct {
	Registry    *coredatasource.Registry
	Index       *semantic.Index
	DataStore   coredata.Store
	DataSources []coredata.SourceSpec
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
	Kind          string
	Datasource    coredatasource.Name
	Entity        coredatasource.EntityType
	RecordID      string
	Cursor        string
	NextCursor    string
	Page          int
	PageDocuments int
	Documents     int
	Indexed       int
	Queued        int
	Skipped       int
	Deleted       int
	Failed        int
	Tombstones    int
	FirstID       string
	LastID        string
	Complete      bool
	Elapsed       time.Duration
	Rate          float64
	FreshUntil    time.Time
	Message       string
	Phase         string
}

const (
	PhaseAll      = "all"
	PhaseFields   = "fields"
	PhaseEnqueue  = "enqueue"
	PhaseSemantic = "semantic"
	PhaseEmbed    = "embed"

	ProgressEntityStart        = "entity_start"
	ProgressPageFetched        = "page_fetched"
	ProgressDocumentIndexed    = "document_indexed"
	ProgressDocumentQueued     = "document_queued"
	ProgressDocumentSkipped    = "document_skipped"
	ProgressDocumentFailed     = "document_failed"
	ProgressEntityFresh        = "entity_fresh"
	ProgressEntityRunningStale = "entity_running_stale"
	ProgressTombstoneDeleted   = "tombstone_deleted"
	ProgressTombstoneFailed    = "tombstone_failed"
	ProgressEntityComplete     = "entity_complete"
	ProgressComplete           = "complete"
)

// Build incrementally indexes corpus from selected datasources/entities.
func Build(ctx context.Context, req Request) (Result, error) {
	if req.Registry == nil {
		return Result{}, fmt.Errorf("datasource index: registry is nil")
	}
	phase := normalizedPhase(req.Phase)
	if req.Index == nil && req.DataStore == nil && !req.DryRun {
		return Result{}, fmt.Errorf("datasource index: store is nil")
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
	if req.Full && !req.DryRun && req.DataStore != nil {
		result, materializedSeen, err := materializeDataViews(ctx, req)
		addResult(&out, result)
		if err != nil {
			return out, err
		}
		for key := range materializedSeen {
			seen[key] = true
		}
		result, err = deleteMissingDataRecords(ctx, req, seen)
		addResult(&out, result)
		if err != nil {
			return out, err
		}
	} else if !req.DryRun && req.DataStore != nil {
		result, _, err := materializeDataViews(ctx, req)
		addResult(&out, result)
		if err != nil {
			return out, err
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
	startedAt := now().UTC()
	if !req.DryRun && !req.Full && !req.Force && freshness > 0 {
		if run, ok, err := dataIndexRun(ctx, req.DataStore, job.spec.Name, job.entity.Type, phase); err != nil {
			return Result{}, nil, err
		} else if ok && run.Status == semantic.IndexRunStatusComplete && !run.CompletedAt.IsZero() {
			freshUntil := run.CompletedAt.Add(freshness)
			if now().Before(freshUntil) {
				report(req.Progress, ProgressEvent{Kind: ProgressEntityFresh, Datasource: job.spec.Name, Entity: job.entity.Type, FreshUntil: freshUntil, Phase: phase})
				return Result{Skipped: 1}, nil, nil
			}
		} else if ok && run.Status == semantic.IndexRunStatusRunning {
			report(req.Progress, ProgressEvent{Kind: ProgressEntityRunningStale, Datasource: job.spec.Name, Entity: job.entity.Type, Phase: phase, Message: staleRunMessage(run)})
		} else if req.Index != nil {
			run, ok, err := req.Index.IndexRun(ctx, semantic.IndexRunKey{Datasource: job.spec.Name, Entity: job.entity.Type, Phase: phase})
			if err != nil {
				return Result{}, nil, err
			}
			if ok && run.Status == semantic.IndexRunStatusComplete && !run.CompletedAt.IsZero() {
				freshUntil := run.CompletedAt.Add(freshness)
				if now().Before(freshUntil) {
					report(req.Progress, ProgressEvent{Kind: ProgressEntityFresh, Datasource: job.spec.Name, Entity: job.entity.Type, FreshUntil: freshUntil, Phase: phase})
					return Result{Skipped: 1}, nil, nil
				}
			} else if ok && run.Status == semantic.IndexRunStatusRunning {
				report(req.Progress, ProgressEvent{Kind: ProgressEntityRunningStale, Datasource: job.spec.Name, Entity: job.entity.Type, Phase: phase, Message: staleRunMessage(run)})
			}
		}
	}
	if !req.DryRun && req.DataStore != nil {
		_ = putDataIndexRun(ctx, req.DataStore, semantic.IndexRunState{
			Datasource: job.spec.Name,
			Entity:     job.entity.Type,
			Phase:      phase,
			Status:     semantic.IndexRunStatusRunning,
			StartedAt:  startedAt,
		})
	}
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
	pageNumber := 0
	for {
		if err := ctx.Err(); err != nil {
			finishRun(ctx, req, job, phase, startedAt, entityResult, err)
			return entityResult, seen, err
		}
		pageNumber++
		page, err := job.corpus.Corpus(ctx, coredatasource.CorpusRequest{Entity: job.entity.Type, Cursor: cursor, Limit: req.Limit})
		if err != nil {
			entityResult.Failed++
			report(req.Progress, ProgressEvent{Kind: ProgressEntityComplete, Datasource: job.spec.Name, Entity: job.entity.Type, Failed: entityResult.Failed, Message: err.Error(), Phase: phase})
			wrapped := fmt.Errorf("datasource index: %s/%s corpus: %w", job.spec.Name, job.entity.Type, err)
			finishRun(ctx, req, job, phase, startedAt, entityResult, wrapped)
			return entityResult, seen, wrapped
		}
		pageDocs := make([]coredatasource.CorpusDocument, 0, len(page.Documents))
		for _, doc := range page.Documents {
			key := semantic.DocumentKey(doc.Ref)
			if key == "" {
				entityResult.Failed++
				report(req.Progress, ProgressEvent{Kind: ProgressDocumentFailed, Datasource: job.spec.Name, Entity: job.entity.Type, RecordID: doc.Ref.ID, Failed: entityResult.Failed, Message: "document ref is incomplete", Phase: phase})
				continue
			}
			seen[key] = true
			seen[dataRecordKey(runtimedata.RefFromDatasourceRef(doc.Ref))] = true
			entityResult.Documents++
			if req.DryRun {
				entityResult.Skipped++
				report(req.Progress, ProgressEvent{Kind: ProgressDocumentSkipped, Datasource: job.spec.Name, Entity: job.entity.Type, RecordID: doc.Ref.ID, Documents: entityResult.Documents, Skipped: entityResult.Skipped, Phase: phase})
				continue
			}
			pageDocs = append(pageDocs, doc)
		}
		fieldFailed := map[string]bool{}
		if job.buildFields && len(pageDocs) > 0 {
			indexed, failed := buildPageFields(ctx, req, job, pageDocs, &entityResult)
			for key := range failed {
				fieldFailed[key] = true
			}
			for _, doc := range pageDocs {
				key := semantic.DocumentKey(doc.Ref)
				if fieldFailed[key] || !indexed[key] {
					continue
				}
				entityResult.Indexed++
				report(req.Progress, ProgressEvent{Kind: ProgressDocumentIndexed, Datasource: job.spec.Name, Entity: job.entity.Type, RecordID: doc.Ref.ID, Documents: entityResult.Documents, Indexed: entityResult.Indexed, Phase: PhaseFields})
			}
		}
		for _, doc := range pageDocs {
			if fieldFailed[semantic.DocumentKey(doc.Ref)] {
				continue
			}
			if job.enqueueSemantic && req.Index != nil {
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
			if req.DataStore != nil {
				if err := req.DataStore.DeleteRecords(ctx, coredata.Scope{}, runtimedata.RefFromDatasourceRef(ref)); err != nil {
					entityResult.Failed++
					report(req.Progress, ProgressEvent{Kind: ProgressTombstoneFailed, Datasource: job.spec.Name, Entity: job.entity.Type, RecordID: ref.ID, Failed: entityResult.Failed, Message: err.Error(), Phase: phase})
					continue
				}
			}
			if req.Index != nil {
				if err := req.Index.Delete(ctx, ref); err != nil {
					entityResult.Failed++
					report(req.Progress, ProgressEvent{Kind: ProgressTombstoneFailed, Datasource: job.spec.Name, Entity: job.entity.Type, RecordID: ref.ID, Failed: entityResult.Failed, Message: err.Error(), Phase: phase})
					continue
				}
			}
			entityResult.Deleted++
			report(req.Progress, ProgressEvent{Kind: ProgressTombstoneDeleted, Datasource: job.spec.Name, Entity: job.entity.Type, RecordID: ref.ID, Deleted: entityResult.Deleted, Phase: phase})
		}
		firstID, lastID := pageBoundaryIDs(page)
		elapsed := now().UTC().Sub(startedAt)
		report(req.Progress, ProgressEvent{
			Kind:          ProgressPageFetched,
			Datasource:    job.spec.Name,
			Entity:        job.entity.Type,
			Phase:         phase,
			Cursor:        cursor,
			NextCursor:    page.NextCursor,
			Page:          pageNumber,
			PageDocuments: len(page.Documents),
			Documents:     entityResult.Documents,
			Indexed:       entityResult.Indexed,
			Queued:        entityResult.Queued,
			Skipped:       entityResult.Skipped,
			Deleted:       entityResult.Deleted,
			Failed:        entityResult.Failed,
			Tombstones:    len(page.Tombstones),
			FirstID:       firstID,
			LastID:        lastID,
			Complete:      page.NextCursor == "" || page.Complete,
			Elapsed:       elapsed,
			Rate:          documentsPerSecond(entityResult.Documents, elapsed),
		})
		if page.NextCursor == "" {
			break
		}
		cursor = page.NextCursor
	}
	report(req.Progress, ProgressEvent{Kind: ProgressEntityComplete, Datasource: job.spec.Name, Entity: job.entity.Type, Documents: entityResult.Documents, Indexed: entityResult.Indexed, Queued: entityResult.Queued, Skipped: entityResult.Skipped, Deleted: entityResult.Deleted, Failed: entityResult.Failed, Phase: phase})
	finishRun(ctx, req, job, phase, startedAt, entityResult, nil)
	return entityResult, seen, nil
}

func pageBoundaryIDs(page coredatasource.CorpusPage) (string, string) {
	if len(page.Documents) > 0 {
		return page.Documents[0].Ref.ID, page.Documents[len(page.Documents)-1].Ref.ID
	}
	if len(page.Tombstones) > 0 {
		return page.Tombstones[0].ID, page.Tombstones[len(page.Tombstones)-1].ID
	}
	return "", ""
}

func documentsPerSecond(documents int, elapsed time.Duration) float64 {
	if documents <= 0 || elapsed <= 0 {
		return 0
	}
	return float64(documents) / elapsed.Seconds()
}

func staleRunMessage(run semantic.IndexRunState) string {
	if run.StartedAt.IsZero() {
		return "previous running checkpoint has no completion timestamp"
	}
	return "previous running checkpoint started at " + run.StartedAt.Format(time.RFC3339Nano) + " and has no completion timestamp"
}

func buildPageFields(ctx context.Context, req Request, job indexJob, docs []coredatasource.CorpusDocument, result *Result) (map[string]bool, map[string]bool) {
	indexed := map[string]bool{}
	failed := map[string]bool{}
	if req.DataStore != nil {
		records := make([]coredata.Record, 0, len(docs))
		relationDocs := map[string]bool{}
		var relations []coredata.Relation
		for _, doc := range docs {
			records = append(records, runtimedata.RecordFromCorpusDocument(doc, job.entity))
			docRelations := runtimedata.RelationsFromCorpusDocument(doc)
			if len(docRelations) > 0 {
				relationDocs[semantic.DocumentKey(doc.Ref)] = true
				relations = append(relations, docRelations...)
			}
		}
		if err := req.DataStore.UpsertRecords(ctx, records...); err != nil {
			reportPageFieldFailures(req, job, docs, result, failed, err)
			return indexed, failed
		}
		for _, doc := range docs {
			indexed[semantic.DocumentKey(doc.Ref)] = true
		}
		if len(relations) > 0 {
			if err := req.DataStore.UpsertRelations(ctx, relations...); err != nil {
				for _, doc := range docs {
					key := semantic.DocumentKey(doc.Ref)
					if !relationDocs[key] {
						continue
					}
					markPageFieldFailure(req, job, doc, result, failed, err)
					delete(indexed, key)
				}
			}
		}
	}
	if req.Index != nil {
		mirrorDocs := make([]coredatasource.CorpusDocument, 0, len(docs))
		for _, doc := range docs {
			if !failed[semantic.DocumentKey(doc.Ref)] {
				mirrorDocs = append(mirrorDocs, doc)
			}
		}
		fieldResults, err := req.Index.UpdateRecords(ctx, mirrorDocs, job.entity)
		if err != nil {
			reportPageFieldFailures(req, job, mirrorDocs, result, failed, err)
			return indexed, failed
		}
		for _, fieldResult := range fieldResults {
			if strings.TrimSpace(fieldResult.Status) == "indexed" {
				indexed[fieldResult.Key] = true
			}
		}
	}
	return indexed, failed
}

func reportPageFieldFailures(req Request, job indexJob, docs []coredatasource.CorpusDocument, result *Result, failed map[string]bool, err error) {
	for _, doc := range docs {
		markPageFieldFailure(req, job, doc, result, failed, err)
	}
}

func markPageFieldFailure(req Request, job indexJob, doc coredatasource.CorpusDocument, result *Result, failed map[string]bool, err error) {
	key := semantic.DocumentKey(doc.Ref)
	if failed[key] {
		return
	}
	failed[key] = true
	result.Failed++
	report(req.Progress, ProgressEvent{Kind: ProgressDocumentFailed, Datasource: job.spec.Name, Entity: job.entity.Type, RecordID: doc.Ref.ID, Documents: result.Documents, Failed: result.Failed, Message: err.Error(), Phase: PhaseFields})
}

func finishRun(ctx context.Context, req Request, job indexJob, phase string, startedAt time.Time, result Result, err error) {
	if req.DryRun || (req.Index == nil && req.DataStore == nil) {
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
	_ = putDataIndexRun(context.WithoutCancel(ctx), req.DataStore, state)
	if req.Index != nil {
		_ = req.Index.PutIndexRun(context.WithoutCancel(ctx), state)
	}
}

func addResult(out *Result, result Result) {
	out.Indexed += result.Indexed
	out.Queued += result.Queued
	out.Skipped += result.Skipped
	out.Deleted += result.Deleted
	out.Failed += result.Failed
	out.Documents += result.Documents
}

func materializeDataViews(ctx context.Context, req Request) (Result, map[string]bool, error) {
	if req.DataStore == nil || len(req.DataSources) == 0 {
		return Result{}, nil, nil
	}
	phase := normalizedPhase(req.Phase)
	if phase != PhaseAll && phase != PhaseFields {
		return Result{}, nil, nil
	}
	var out Result
	seen := map[string]bool{}
	for _, accessor := range req.Registry.All() {
		spec := accessor.Spec()
		if req.Datasource != "" && spec.Name != req.Datasource {
			continue
		}
		for _, source := range matchingDataSources(req.DataSources, spec) {
			for _, view := range source.Views {
				if view.Source == "" {
					continue
				}
				count, viewSeen, err := materializeDataView(ctx, req.DataStore, accessor, spec, view)
				out.Indexed += count
				if err != nil {
					out.Failed++
					return out, seen, err
				}
				for key := range viewSeen {
					seen[key] = true
				}
			}
		}
	}
	return out, seen, nil
}

func matchingDataSources(sources []coredata.SourceSpec, spec coredatasource.Spec) []coredata.SourceSpec {
	var out []coredata.SourceSpec
	for _, source := range sources {
		if source.Name == coredata.SourceName(spec.Name) || (source.Kind != "" && source.Kind == spec.Kind) {
			out = append(out, source)
		}
	}
	return out
}

func materializeDataView(ctx context.Context, store coredata.Store, accessor coredatasource.Accessor, spec coredatasource.Spec, view coredata.ViewSpec) (int, map[string]bool, error) {
	cursor := ""
	var indexed int
	seen := map[string]bool{}
	for {
		result, err := store.QueryRecords(ctx, coredata.Query{
			Sources:  []coredata.SourceName{coredata.SourceName(spec.Name)},
			Entities: []coredata.EntityType{view.Source},
			Views:    []coredata.ViewName{coredata.ViewName(view.Source)},
			Limit:    1000,
			Cursor:   cursor,
		})
		if err != nil {
			return indexed, seen, err
		}
		materializedRecords := make([]coredata.Record, 0, len(result.Records))
		for _, record := range result.Records {
			materialized, err := materializeRecord(ctx, store, accessor, record, view)
			if err != nil {
				return indexed, seen, err
			}
			materializedRecords = append(materializedRecords, materialized)
			seen[dataRecordKey(materialized.Ref)] = true
			indexed++
		}
		if len(materializedRecords) > 0 {
			if err := store.UpsertRecords(ctx, materializedRecords...); err != nil {
				return indexed, seen, err
			}
		}
		if result.NextCursor == "" {
			break
		}
		cursor = result.NextCursor
	}
	return indexed, seen, nil
}

func materializeRecord(ctx context.Context, store coredata.Store, accessor coredatasource.Accessor, source coredata.Record, view coredata.ViewSpec) (coredata.Record, error) {
	entity := view.Entity
	if entity == "" {
		entity = source.Ref.Entity
	}
	record := cloneDataRecord(source)
	record.Ref = coredata.Ref{
		Source: source.Ref.Source,
		Entity: entity,
		View:   view.Name,
		ID:     source.Ref.ID,
	}
	if record.Fields == nil {
		record.Fields = map[string][]string{}
	}
	if record.Relations == nil {
		record.Relations = map[string][]coredata.Summary{}
	}
	record.Metadata = cloneStringMap(record.Metadata)
	if record.Metadata == nil {
		record.Metadata = map[string]string{}
	}
	record.Metadata["view"] = string(view.Name)
	record.Metadata["source_entity"] = string(view.Source)
	for _, include := range view.Includes {
		summaries, err := relationSummariesForView(ctx, store, accessor, source, include)
		if err != nil {
			return coredata.Record{}, err
		}
		if len(summaries) == 0 {
			continue
		}
		relationName := string(include.Relation)
		record.Relations[relationName] = mergeSummaries(record.Relations[relationName], summaries)
		for _, summary := range summaries {
			addViewSummaryFields(record.Fields, relationName, summary)
		}
	}
	return record, nil
}

func relationSummariesForView(ctx context.Context, store coredata.Store, accessor coredatasource.Accessor, source coredata.Record, include coredata.RelationIncludeSpec) ([]coredata.Summary, error) {
	var summaries []coredata.Summary
	cursor := ""
	for {
		result, err := store.QueryRelations(ctx, coredata.RelationQuery{
			Sources:  []coredata.SourceName{source.Ref.Source},
			Relation: include.Relation,
			Source:   source.Ref,
			Limit:    1000,
			Cursor:   cursor,
		})
		if err != nil {
			return nil, err
		}
		for _, relation := range result.Relations {
			summary := relation.Summary
			if summary.Ref.Source == "" {
				summary.Ref = relation.Target
			}
			summary.Fields = selectedSummaryFields(summary, include.Fields)
			summaries = append(summaries, summary)
		}
		if result.NextCursor == "" {
			break
		}
		cursor = result.NextCursor
	}
	if len(summaries) > 0 {
		return summaries, nil
	}
	relationer, ok := accessor.(coredatasource.Relationer)
	if !ok {
		return nil, nil
	}
	cursor = ""
	for {
		page, err := relationer.Relation(ctx, coredatasource.RelationRequest{
			Entity:   coredatasource.EntityType(source.Ref.Entity),
			ID:       string(source.Ref.ID),
			Relation: string(include.Relation),
			Limit:    1000,
			Cursor:   cursor,
		})
		if err != nil {
			return nil, err
		}
		var relations []coredata.Relation
		for _, record := range page.Records {
			summary := summaryFromDatasourceRecord(source.Ref.Source, coredatasource.EntityType(include.Target), record, include.Fields)
			summaries = append(summaries, summary)
			relations = append(relations, coredata.Relation{
				Source:  source.Ref,
				Name:    include.Relation,
				Target:  summary.Ref,
				Summary: summary,
			})
		}
		if len(relations) > 0 {
			if err := store.UpsertRelations(ctx, relations...); err != nil {
				return nil, err
			}
		}
		if page.NextCursor == "" || page.Complete {
			break
		}
		cursor = page.NextCursor
	}
	return summaries, nil
}

func summaryFromDatasourceRecord(source coredata.SourceName, target coredatasource.EntityType, record coredatasource.Record, fields []string) coredata.Summary {
	ref := coredata.Ref{
		Source: coredata.SourceName(record.Datasource),
		Entity: coredata.EntityType(record.Entity),
		View:   coredata.ViewName(record.Entity),
		ID:     coredata.RecordID(record.ID),
	}
	if ref.Source == "" {
		ref.Source = source
	}
	if ref.Entity == "" {
		ref.Entity = coredata.EntityType(target)
		ref.View = coredata.ViewName(target)
	}
	values := map[string]string{
		"id":      record.ID,
		"title":   record.Title,
		"url":     record.URL,
		"content": record.Content,
	}
	for key, value := range record.Metadata {
		values[key] = value
	}
	out := coredata.Summary{Ref: ref, Title: record.Title, Fields: map[string]string{}}
	if len(fields) == 0 {
		out.Fields = values
		return out
	}
	for _, field := range fields {
		if value := strings.TrimSpace(values[field]); value != "" {
			out.Fields[field] = value
		}
	}
	return out
}

func selectedSummaryFields(summary coredata.Summary, fields []string) map[string]string {
	if len(fields) == 0 {
		return cloneStringMap(summary.Fields)
	}
	out := map[string]string{}
	for _, field := range fields {
		if value := strings.TrimSpace(summary.Fields[field]); value != "" {
			out[field] = value
		}
	}
	return out
}

func addViewSummaryFields(fields map[string][]string, relation string, summary coredata.Summary) {
	for key, value := range summary.Fields {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		fields[relation+"."+key] = appendUniqueString(fields[relation+"."+key], value)
	}
	if summary.Title != "" {
		fields[relation+".title"] = appendUniqueString(fields[relation+".title"], summary.Title)
	}
	if summary.Ref.ID != "" {
		fields[relation+".id"] = appendUniqueString(fields[relation+".id"], string(summary.Ref.ID))
	}
}

func mergeSummaries(existing []coredata.Summary, candidates []coredata.Summary) []coredata.Summary {
	seen := map[string]bool{}
	for _, summary := range existing {
		seen[summaryKey(summary)] = true
	}
	for _, summary := range candidates {
		key := summaryKey(summary)
		if key == "" || seen[key] {
			continue
		}
		existing = append(existing, summary)
		seen[key] = true
	}
	return existing
}

func summaryKey(summary coredata.Summary) string {
	return strings.Join([]string{string(summary.Ref.Source), string(summary.Ref.Entity), string(summary.Ref.View), string(summary.Ref.ID)}, "\x00")
}

func cloneDataRecord(record coredata.Record) coredata.Record {
	record.Fields = cloneStringSliceMap(record.Fields)
	record.Relations = cloneSummaryMap(record.Relations)
	record.BlobRefs = append([]coredata.BlobRef(nil), record.BlobRefs...)
	record.Metadata = cloneStringMap(record.Metadata)
	return record
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

func appendUniqueString(values []string, candidate string) []string {
	for _, value := range values {
		if value == candidate {
			return values
		}
	}
	return append(values, candidate)
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

const dataIndexRunSource coredata.SourceName = "_runtime"
const dataIndexRunEntity coredata.EntityType = "datasource.index_run"
const dataIndexRunView coredata.ViewName = "datasource.index_run"

func dataIndexRun(ctx context.Context, store coredata.Store, datasource coredatasource.Name, entity coredatasource.EntityType, phase string) (semantic.IndexRunState, bool, error) {
	if store == nil {
		return semantic.IndexRunState{}, false, nil
	}
	record, ok, err := store.GetRecord(ctx, coredata.Scope{}, dataIndexRunRef(datasource, entity, phase))
	if err != nil || !ok {
		return semantic.IndexRunState{}, ok, err
	}
	run := semantic.IndexRunState{
		Key:        string(record.Ref.ID),
		Datasource: coredatasource.Name(record.Metadata["datasource"]),
		Entity:     coredatasource.EntityType(record.Metadata["entity"]),
		Phase:      record.Metadata["phase"],
		Status:     record.Metadata["status"],
		LastError:  record.Metadata["last_error"],
	}
	run.StartedAt = parseRunTime(record.Metadata["started_at"])
	run.CompletedAt = parseRunTime(record.Metadata["completed_at"])
	run.Documents = parseRunInt(record.Metadata["documents"])
	run.Indexed = parseRunInt(record.Metadata["indexed"])
	run.Queued = parseRunInt(record.Metadata["queued"])
	run.Skipped = parseRunInt(record.Metadata["skipped"])
	run.Deleted = parseRunInt(record.Metadata["deleted"])
	run.Failed = parseRunInt(record.Metadata["failed"])
	return run, true, nil
}

func putDataIndexRun(ctx context.Context, store coredata.Store, run semantic.IndexRunState) error {
	if store == nil {
		return nil
	}
	ref := dataIndexRunRef(run.Datasource, run.Entity, run.Phase)
	metadata := map[string]string{
		"datasource": string(run.Datasource),
		"entity":     string(run.Entity),
		"phase":      normalizedPhase(run.Phase),
		"status":     run.Status,
		"started_at": run.StartedAt.Format(time.RFC3339Nano),
		"documents":  fmt.Sprint(run.Documents),
		"indexed":    fmt.Sprint(run.Indexed),
		"queued":     fmt.Sprint(run.Queued),
		"skipped":    fmt.Sprint(run.Skipped),
		"deleted":    fmt.Sprint(run.Deleted),
		"failed":     fmt.Sprint(run.Failed),
		"last_error": run.LastError,
	}
	if !run.CompletedAt.IsZero() {
		metadata["completed_at"] = run.CompletedAt.Format(time.RFC3339Nano)
	}
	return store.UpsertRecords(ctx, coredata.Record{
		Ref:      ref,
		Title:    string(run.Datasource) + "/" + string(run.Entity) + " " + normalizedPhase(run.Phase),
		Metadata: metadata,
		Fields: map[string][]string{
			"datasource": {string(run.Datasource)},
			"entity":     {string(run.Entity)},
			"phase":      {normalizedPhase(run.Phase)},
			"status":     {run.Status},
		},
		UpdatedAt: metadata["completed_at"],
	})
}

// DeleteDataIndexRuns removes datastore-backed freshness checkpoints.
func DeleteDataIndexRuns(ctx context.Context, store coredata.Store, datasource coredatasource.Name, entity coredatasource.EntityType) error {
	if store == nil {
		return nil
	}
	result, err := store.QueryRecords(ctx, coredata.Query{
		Sources:  []coredata.SourceName{dataIndexRunSource},
		Entities: []coredata.EntityType{dataIndexRunEntity},
		Filters: map[string]string{
			"datasource": string(datasource),
			"entity":     string(entity),
		},
		Limit: 1000,
	})
	if err != nil {
		return err
	}
	for _, record := range result.Records {
		if err := store.DeleteRecords(ctx, coredata.Scope{}, record.Ref); err != nil {
			return err
		}
	}
	return nil
}

func deleteMissingDataRecords(ctx context.Context, req Request, seen map[string]bool) (Result, error) {
	result, err := req.DataStore.QueryRecords(ctx, coredata.Query{
		Sources:  dataSources(req.Datasource),
		Entities: dataEntities(req.Entity),
		Limit:    100000,
	})
	if err != nil {
		return Result{}, err
	}
	var out Result
	for _, record := range result.Records {
		if record.Ref.Source == dataIndexRunSource {
			continue
		}
		key := dataRecordKey(record.Ref)
		if seen[key] {
			continue
		}
		if err := req.DataStore.DeleteRecords(ctx, coredata.Scope{}, record.Ref); err != nil {
			out.Failed++
			report(req.Progress, ProgressEvent{Kind: ProgressTombstoneFailed, Datasource: coredatasource.Name(record.Ref.Source), Entity: coredatasource.EntityType(record.Ref.Entity), RecordID: string(record.Ref.ID), Failed: out.Failed, Message: err.Error(), Phase: normalizedPhase(req.Phase)})
			continue
		}
		out.Deleted++
		report(req.Progress, ProgressEvent{Kind: ProgressTombstoneDeleted, Datasource: coredatasource.Name(record.Ref.Source), Entity: coredatasource.EntityType(record.Ref.Entity), RecordID: string(record.Ref.ID), Deleted: out.Deleted, Phase: normalizedPhase(req.Phase)})
	}
	return out, nil
}

func dataRecordKey(ref coredata.Ref) string {
	return strings.Join([]string{string(ref.Source), string(ref.Entity), string(ref.View), string(ref.ID)}, "\x00")
}

func dataIndexRunRef(datasource coredatasource.Name, entity coredatasource.EntityType, phase string) coredata.Ref {
	return coredata.Ref{
		Source: dataIndexRunSource,
		Entity: dataIndexRunEntity,
		View:   dataIndexRunView,
		ID:     coredata.RecordID(string(datasource) + "|" + string(entity) + "|" + normalizedPhase(phase)),
	}
}

func dataSources(value coredatasource.Name) []coredata.SourceName {
	if value == "" {
		return nil
	}
	return []coredata.SourceName{coredata.SourceName(value)}
}

func dataEntities(value coredatasource.EntityType) []coredata.EntityType {
	if value == "" {
		return nil
	}
	return []coredata.EntityType{coredata.EntityType(value)}
}

func parseRunTime(value string) time.Time {
	parsed, _ := time.Parse(time.RFC3339Nano, value)
	return parsed
}

func parseRunInt(value string) int {
	var out int
	_, _ = fmt.Sscan(value, &out)
	return out
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
