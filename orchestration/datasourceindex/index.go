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
	Registry   *coredatasource.Registry
	Index      *semantic.Index
	Datasource coredatasource.Name
	Entity     coredatasource.EntityType
	Full       bool
	DryRun     bool
	Limit      int
}

// Result summarizes an indexing run.
type Result struct {
	Indexed   int
	Skipped   int
	Deleted   int
	Failed    int
	Documents int
}

// Build incrementally indexes corpus from selected datasources/entities.
func Build(ctx context.Context, req Request) (Result, error) {
	if req.Registry == nil {
		return Result{}, fmt.Errorf("datasource index: registry is nil")
	}
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
		corpus, ok := accessor.(coredatasource.CorpusProvider)
		if !ok {
			continue
		}
		for _, entity := range accessor.Entities() {
			if req.Entity != "" && entity.Type != req.Entity {
				continue
			}
			if !supportsSemantic(entity) {
				continue
			}
			cursor := ""
			for {
				if err := ctx.Err(); err != nil {
					return out, err
				}
				page, err := corpus.Corpus(ctx, coredatasource.CorpusRequest{Entity: entity.Type, Cursor: cursor, Limit: req.Limit})
				if err != nil {
					out.Failed++
					return out, fmt.Errorf("datasource index: %s/%s corpus: %w", spec.Name, entity.Type, err)
				}
				for _, doc := range page.Documents {
					key := semantic.DocumentKey(doc.Ref)
					if key == "" {
						out.Failed++
						continue
					}
					seen[key] = true
					out.Documents++
					if req.DryRun {
						out.Skipped++
						continue
					}
					result, err := req.Index.Update(ctx, doc)
					if err != nil {
						out.Failed++
						continue
					}
					switch strings.TrimSpace(result.Status) {
					case "indexed":
						out.Indexed++
					default:
						out.Skipped++
					}
				}
				for _, ref := range page.Tombstones {
					if req.DryRun {
						continue
					}
					if err := req.Index.Delete(ctx, ref); err != nil {
						out.Failed++
						continue
					}
					out.Deleted++
				}
				if page.NextCursor == "" {
					break
				}
				cursor = page.NextCursor
			}
		}
	}
	if req.Full && !req.DryRun && req.Index != nil {
		status, err := req.Index.Status(ctx, semantic.StatusRequest{Datasource: req.Datasource, Entity: req.Entity})
		if err != nil {
			return out, err
		}
		for _, doc := range status.Documents {
			if seen[doc.Key] {
				continue
			}
			if err := req.Index.Delete(ctx, doc.Ref); err != nil {
				out.Failed++
				continue
			}
			out.Deleted++
		}
	}
	return out, nil
}

func supportsSemantic(entity coredatasource.EntitySpec) bool {
	for _, capability := range entity.Capabilities {
		if capability == coredatasource.EntityCapabilitySemanticSearch {
			return true
		}
	}
	return false
}
