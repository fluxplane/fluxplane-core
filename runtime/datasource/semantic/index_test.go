package semantic

import (
	"context"
	"testing"
	"time"

	coredatasource "github.com/fluxplane/fluxplane-core/core/datasource"
)

func TestIndexUpdateSkipsUnchangedDocumentAndSearches(t *testing.T) {
	ctx := context.Background()
	store := NewJSONStore("")
	index, err := New(HashEmbedder{ModelName: "test-embedding"}, store, Config{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	doc := coredatasource.CorpusDocument{
		Ref: coredatasource.RecordRef{
			Datasource: "docs",
			Entity:     "file.document",
			ID:         "runbook.md",
		},
		Title: "Login runbook",
		Body:  "Reset the login cache after deploy when auth sessions fail.",
	}
	first, err := index.Update(ctx, doc)
	if err != nil {
		t.Fatalf("Update first: %v", err)
	}
	if first.Status != "indexed" || first.Chunks != 1 {
		t.Fatalf("first update = %#v, want indexed one chunk", first)
	}
	second, err := index.Update(ctx, doc)
	if err != nil {
		t.Fatalf("Update second: %v", err)
	}
	if second.Status != "skipped" {
		t.Fatalf("second update = %#v, want skipped", second)
	}
	results, err := index.Search(ctx, SearchRequest{Query: "login sessions deploy", Datasources: []coredatasource.Name{"docs"}, Limit: 5})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results.Hits) != 1 || results.Hits[0].Ref.ID != "runbook.md" {
		t.Fatalf("hits = %#v, want runbook", results.Hits)
	}
}

func TestIndexSearchHydratesMirrorRecordMetadata(t *testing.T) {
	ctx := context.Background()
	index, err := New(HashEmbedder{ModelName: "test-embedding"}, NewJSONStore(""), Config{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	doc := coredatasource.CorpusDocument{
		Ref:   coredatasource.RecordRef{Datasource: "docs", Entity: "file.document", ID: "runbook.md"},
		Title: "Chunk title",
		Body:  "Reset the login cache after deploy.",
	}
	entity := coredatasource.EntitySpec{
		Type: "file.document",
		Fields: []coredatasource.FieldSpec{
			{Name: "path", Searchable: true, Filterable: true},
		},
	}
	mirrorDoc := doc
	mirrorDoc.Title = "Hydrated runbook"
	mirrorDoc.URL = "https://docs.example/runbook"
	mirrorDoc.Metadata = map[string]string{"path": "runbook.md"}
	if _, err := index.UpdateRecord(ctx, mirrorDoc, entity); err != nil {
		t.Fatalf("UpdateRecord: %v", err)
	}
	if _, err := index.Update(ctx, doc); err != nil {
		t.Fatalf("Update: %v", err)
	}
	results, err := index.Search(ctx, SearchRequest{Query: "login deploy", Datasources: []coredatasource.Name{"docs"}, Limit: 5})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results.Hits) != 1 {
		t.Fatalf("hits = %#v, want one hit", results.Hits)
	}
	hit := results.Hits[0]
	if hit.Title != "Hydrated runbook" || hit.URL != "https://docs.example/runbook" || hit.Metadata["path"] != "runbook.md" {
		t.Fatalf("hit = %#v, want mirror-hydrated title, URL, and metadata", hit)
	}
	if hit.Snippet == "" {
		t.Fatalf("hit = %#v, want semantic chunk snippet preserved", hit)
	}
}

func TestIndexEnqueueDefersEmbeddingUntilQueueProcessing(t *testing.T) {
	ctx := context.Background()
	embedder := &countingEmbedder{model: "test-embedding"}
	index, err := New(embedder, NewJSONStore(""), Config{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	doc := coredatasource.CorpusDocument{
		Ref:   coredatasource.RecordRef{Datasource: "docs", Entity: "file.document", ID: "runbook.md"},
		Title: "Login runbook",
		Body:  "Reset login cache after deploy.",
	}
	enqueued, err := index.Enqueue(ctx, doc)
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	if enqueued.Status != QueueStatusQueued {
		t.Fatalf("enqueue result = %#v, want queued", enqueued)
	}
	if embedder.calls != 0 {
		t.Fatalf("embed calls = %d, want none during enqueue", embedder.calls)
	}
	status, err := index.Status(ctx, StatusRequest{Datasource: "docs", Entity: "file.document"})
	if err != nil {
		t.Fatalf("Status queued: %v", err)
	}
	if len(status.Queue) != 1 || len(status.Documents) != 0 {
		t.Fatalf("status = %#v, want one queued job and no documents", status)
	}
	processed, err := index.ProcessQueue(ctx, ProcessQueueRequest{Datasource: "docs", Entity: "file.document"})
	if err != nil {
		t.Fatalf("ProcessQueue: %v", err)
	}
	if processed.Embedded != 1 || processed.Failed != 0 {
		t.Fatalf("processed = %#v, want one embedded", processed)
	}
	if embedder.calls != 1 {
		t.Fatalf("embed calls = %d, want one after queue processing", embedder.calls)
	}
	status, err = index.Status(ctx, StatusRequest{Datasource: "docs", Entity: "file.document"})
	if err != nil {
		t.Fatalf("Status embedded: %v", err)
	}
	if len(status.Queue) != 0 || len(status.Documents) != 1 {
		t.Fatalf("status = %#v, want one document and no queued jobs", status)
	}
}

func TestIndexDeleteIndexRunsInvalidatesBuildReadiness(t *testing.T) {
	ctx := context.Background()
	index, err := New(HashEmbedder{ModelName: "test-embedding"}, NewJSONStore(""), Config{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	run := IndexRunState{
		Datasource:  "gitlab",
		Entity:      "gitlab.user",
		Phase:       "fields",
		Status:      IndexRunStatusComplete,
		CompletedAt: time.Now().UTC(),
	}
	if err := index.PutIndexRun(ctx, run); err != nil {
		t.Fatalf("PutIndexRun: %v", err)
	}
	if _, ok, err := index.IndexRun(ctx, IndexRunKey{Datasource: "gitlab", Entity: "gitlab.user", Phase: "fields"}); err != nil || !ok {
		t.Fatalf("IndexRun before delete ok=%v err=%v, want present", ok, err)
	}
	if err := index.DeleteIndexRuns(ctx, StatusRequest{Datasource: "gitlab", Entity: "gitlab.user"}); err != nil {
		t.Fatalf("DeleteIndexRuns: %v", err)
	}
	if _, ok, err := index.IndexRun(ctx, IndexRunKey{Datasource: "gitlab", Entity: "gitlab.user", Phase: "fields"}); err != nil || ok {
		t.Fatalf("IndexRun after delete ok=%v err=%v, want missing", ok, err)
	}
}

func TestIndexUpdateRecordSearchesIndexedFields(t *testing.T) {
	ctx := context.Background()
	store := NewJSONStore("")
	index, err := New(HashEmbedder{ModelName: "test-embedding"}, store, Config{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	entity := coredatasource.EntitySpec{
		Type: "gitlab.project",
		Fields: []coredatasource.FieldSpec{
			{Name: "id", Identifier: true, Filterable: true},
			{Name: "name", Searchable: true},
			{Name: "path_with_namespace", Searchable: true, Filterable: true},
		},
	}
	doc := coredatasource.CorpusDocument{
		Ref:   coredatasource.RecordRef{Datasource: "gitlab", Entity: "gitlab.project", ID: "fluxplane/runtime"},
		Title: "fluxplane/runtime",
		Body:  "Runtime repository",
		Metadata: map[string]string{
			"id":                  "12",
			"name":                "runtime",
			"path_with_namespace": "fluxplane/runtime",
		},
	}
	if _, err := index.UpdateRecord(ctx, doc, entity); err != nil {
		t.Fatalf("UpdateRecord: %v", err)
	}
	status, err := index.Status(ctx, StatusRequest{Datasource: "gitlab", Entity: "gitlab.project"})
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if len(status.Records) != 1 || len(status.Documents) != 0 {
		t.Fatalf("status = %#v, want one field record and no semantic documents", status)
	}
	results, err := index.SearchFields(ctx, FieldSearchRequest{
		Query:       "fluxplane/runtime",
		Datasources: []coredatasource.Name{"gitlab"},
		Entities:    []coredatasource.EntityType{"gitlab.project"},
		Filters:     map[string]string{"path_with_namespace": "fluxplane/runtime"},
	})
	if err != nil {
		t.Fatalf("SearchFields: %v", err)
	}
	if len(results.Hits) != 1 || results.Hits[0].Record.ID != "fluxplane/runtime" {
		t.Fatalf("hits = %#v, want runtime project", results.Hits)
	}
}

type countingEmbedder struct {
	model string
	calls int
}

func (e *countingEmbedder) Model() string {
	return e.model
}

func (e *countingEmbedder) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	e.calls++
	return HashEmbedder{ModelName: e.model}.Embed(ctx, texts)
}
