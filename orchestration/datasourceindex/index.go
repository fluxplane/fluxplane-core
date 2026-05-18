// Package datasourceindex orchestrates semantic indexing for datasource corpus.
package datasourceindex

import (
	"context"
	"fmt"
	"strings"

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
	var out Result
	seen := map[string]bool{}
	for _, accessor := range req.Registry.All() {
		spec := accessor.Spec()
		if req.Datasource != "" && spec.Name != req.Datasource {
			continue
		}
		if req.IndexedOnly && !spec.Index {
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
			entityResult := Result{}
			report(req.Progress, ProgressEvent{Kind: ProgressEntityStart, Datasource: spec.Name, Entity: entity.Type, Phase: phase})
			cursor := ""
			for {
				if err := ctx.Err(); err != nil {
					return out, err
				}
				page, err := corpus.Corpus(ctx, coredatasource.CorpusRequest{Entity: entity.Type, Cursor: cursor, Limit: req.Limit})
				if err != nil {
					out.Failed++
					entityResult.Failed++
					report(req.Progress, ProgressEvent{Kind: ProgressEntityComplete, Datasource: spec.Name, Entity: entity.Type, Failed: entityResult.Failed, Message: err.Error(), Phase: phase})
					return out, fmt.Errorf("datasource index: %s/%s corpus: %w", spec.Name, entity.Type, err)
				}
				report(req.Progress, ProgressEvent{
					Kind:       ProgressPageFetched,
					Datasource: spec.Name,
					Entity:     entity.Type,
					Phase:      phase,
					Cursor:     cursor,
					NextCursor: page.NextCursor,
					Documents:  len(page.Documents),
					Tombstones: len(page.Tombstones),
				})
				for _, doc := range page.Documents {
					key := semantic.DocumentKey(doc.Ref)
					if key == "" {
						out.Failed++
						entityResult.Failed++
						report(req.Progress, ProgressEvent{Kind: ProgressDocumentFailed, Datasource: spec.Name, Entity: entity.Type, RecordID: doc.Ref.ID, Failed: entityResult.Failed, Message: "document ref is incomplete", Phase: phase})
						continue
					}
					seen[key] = true
					out.Documents++
					entityResult.Documents++
					if req.DryRun {
						out.Skipped++
						entityResult.Skipped++
						report(req.Progress, ProgressEvent{Kind: ProgressDocumentSkipped, Datasource: spec.Name, Entity: entity.Type, RecordID: doc.Ref.ID, Documents: entityResult.Documents, Skipped: entityResult.Skipped, Phase: phase})
						continue
					}
					if buildFields {
						result, err := req.Index.UpdateRecord(ctx, doc, entity)
						if err != nil {
							out.Failed++
							entityResult.Failed++
							report(req.Progress, ProgressEvent{Kind: ProgressDocumentFailed, Datasource: spec.Name, Entity: entity.Type, RecordID: doc.Ref.ID, Documents: entityResult.Documents, Failed: entityResult.Failed, Message: err.Error(), Phase: PhaseFields})
							continue
						}
						if strings.TrimSpace(result.Status) == "indexed" {
							out.Indexed++
							entityResult.Indexed++
							report(req.Progress, ProgressEvent{Kind: ProgressDocumentIndexed, Datasource: spec.Name, Entity: entity.Type, RecordID: doc.Ref.ID, Documents: entityResult.Documents, Indexed: entityResult.Indexed, Phase: PhaseFields})
						}
					}
					if enqueueSemantic {
						result, err := req.Index.Enqueue(ctx, doc)
						if err != nil {
							out.Failed++
							entityResult.Failed++
							report(req.Progress, ProgressEvent{Kind: ProgressDocumentFailed, Datasource: spec.Name, Entity: entity.Type, RecordID: doc.Ref.ID, Documents: entityResult.Documents, Failed: entityResult.Failed, Message: err.Error(), Phase: PhaseEnqueue})
							continue
						}
						switch strings.TrimSpace(result.Status) {
						case semantic.QueueStatusQueued:
							out.Queued++
							entityResult.Queued++
							report(req.Progress, ProgressEvent{Kind: ProgressDocumentQueued, Datasource: spec.Name, Entity: entity.Type, RecordID: doc.Ref.ID, Documents: entityResult.Documents, Queued: entityResult.Queued, Phase: PhaseEnqueue})
						default:
							out.Skipped++
							entityResult.Skipped++
							report(req.Progress, ProgressEvent{Kind: ProgressDocumentSkipped, Datasource: spec.Name, Entity: entity.Type, RecordID: doc.Ref.ID, Documents: entityResult.Documents, Skipped: entityResult.Skipped, Phase: PhaseEnqueue})
						}
					}
				}
				for _, ref := range page.Tombstones {
					if req.DryRun {
						continue
					}
					if err := req.Index.Delete(ctx, ref); err != nil {
						out.Failed++
						entityResult.Failed++
						report(req.Progress, ProgressEvent{Kind: ProgressTombstoneFailed, Datasource: spec.Name, Entity: entity.Type, RecordID: ref.ID, Failed: entityResult.Failed, Message: err.Error(), Phase: phase})
						continue
					}
					out.Deleted++
					entityResult.Deleted++
					report(req.Progress, ProgressEvent{Kind: ProgressTombstoneDeleted, Datasource: spec.Name, Entity: entity.Type, RecordID: ref.ID, Deleted: entityResult.Deleted, Phase: phase})
				}
				if page.NextCursor == "" {
					break
				}
				cursor = page.NextCursor
			}
			report(req.Progress, ProgressEvent{Kind: ProgressEntityComplete, Datasource: spec.Name, Entity: entity.Type, Documents: entityResult.Documents, Indexed: entityResult.Indexed, Queued: entityResult.Queued, Skipped: entityResult.Skipped, Deleted: entityResult.Deleted, Failed: entityResult.Failed, Phase: phase})
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
