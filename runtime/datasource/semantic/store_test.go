package semantic

import (
	"context"
	"testing"
)

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
