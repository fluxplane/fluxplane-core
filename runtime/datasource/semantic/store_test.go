package semantic

import (
	"context"
	"testing"

	coredatasource "github.com/fluxplane/fluxplane-core/core/datasource"
)

// TestIndexSearchTieBreaksDeterministically regresses the non-determinism at
// the Index.Search layer. Even after JSONStore.Search was made deterministic,
// Index.Search rebuilt the result slice from a map[string]Hit grouped by
// document key and then sorted by Score with sort.Slice (unstable) without a
// tie-breaker, so documents with identical scores ended up in random order.
func TestIndexSearchTieBreaksDeterministically(t *testing.T) {
	ctx := context.Background()
	body := "reset the login cache after deploy when auth sessions fail"
	// Identical title+body across documents -> identical chunk text ->
	// identical embedding -> identical cosine score for the same query.
	// Only the Ref.ID differs, so deterministic tie-breaking must order them
	// by ID even though the upstream map iteration is randomised.
	docs := []coredatasource.CorpusDocument{
		{Ref: coredatasource.RecordRef{Datasource: "docs", Entity: "file.document", ID: "c.md"}, Title: "same", Body: body},
		{Ref: coredatasource.RecordRef{Datasource: "docs", Entity: "file.document", ID: "a.md"}, Title: "same", Body: body},
		{Ref: coredatasource.RecordRef{Datasource: "docs", Entity: "file.document", ID: "b.md"}, Title: "same", Body: body},
	}
	for i := 0; i < 5; i++ {
		index, err := New(HashEmbedder{ModelName: "test-embedding"}, NewJSONStore(""), Config{})
		if err != nil {
			t.Fatalf("iter %d New: %v", i, err)
		}
		for _, d := range docs {
			if _, err := index.Update(ctx, d); err != nil {
				t.Fatalf("iter %d Update %s: %v", i, d.Ref.ID, err)
			}
		}
		res, err := index.Search(ctx, SearchRequest{Query: body, Datasources: []coredatasource.Name{"docs"}, Limit: 10})
		if err != nil {
			t.Fatalf("iter %d Search: %v", i, err)
		}
		if len(res.Hits) != 3 {
			t.Fatalf("iter %d len(hits) = %d, want 3", i, len(res.Hits))
		}
		got := []string{res.Hits[0].Ref.ID, res.Hits[1].Ref.ID, res.Hits[2].Ref.ID}
		want := []string{"a.md", "b.md", "c.md"}
		if got[0] != want[0] || got[1] != want[1] || got[2] != want[2] {
			t.Fatalf("iter %d hit order = %v, want %v (deterministic tie-break by Ref.ID)", i, got, want)
		}
	}
}

// TestSearchTieBreaksDeterministically regresses a determinism bug: when two
// chunks have the exact same score, the result order depended on map iteration
// order from UpsertChunks (which rebuilt state.Chunks from a map) plus the
// non-stable sort.Slice ordering of equal elements. Same inputs produced
// different orderings across runs.
func TestSearchTieBreaksDeterministically(t *testing.T) {
	// Use identical vectors so all hits get the same cosine score.
	v := []float32{1, 0, 0}
	chunks := []EmbeddedChunk{
		{Chunk: Chunk{ID: "c"}, Vector: v},
		{Chunk: Chunk{ID: "a"}, Vector: v},
		{Chunk: Chunk{ID: "b"}, Vector: v},
	}
	ctx := context.Background()
	for i := 0; i < 10; i++ {
		store := NewJSONStore("")
		if err := store.UpsertChunks(ctx, chunks); err != nil {
			t.Fatalf("UpsertChunks iter %d: %v", i, err)
		}
		hits, err := store.Search(ctx, VectorSearchRequest{Vector: v})
		if err != nil {
			t.Fatalf("Search iter %d: %v", i, err)
		}
		if len(hits) != 3 {
			t.Fatalf("len(hits) = %d, want 3", len(hits))
		}
		if hits[0].Chunk.ID != "a" || hits[1].Chunk.ID != "b" || hits[2].Chunk.ID != "c" {
			t.Fatalf("iter %d: hit order = %q,%q,%q, want a,b,c (deterministic tie-break by ID)", i, hits[0].Chunk.ID, hits[1].Chunk.ID, hits[2].Chunk.ID)
		}
	}
}
