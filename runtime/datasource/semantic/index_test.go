package semantic

import (
	"context"
	"testing"

	coredatasource "github.com/fluxplane/agentruntime/core/datasource"
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
