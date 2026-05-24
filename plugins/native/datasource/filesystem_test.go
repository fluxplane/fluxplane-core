package datasource

import (
	"context"
	"io/fs"
	"testing"
	"testing/fstest"

	coredatasource "github.com/fluxplane/fluxplane-core/core/datasource"
)

func TestFilesystemCorpusSkipsRuntimeAndDependencyDirs(t *testing.T) {
	root := fstest.MapFS{
		"README.md":               &fstest.MapFile{Data: []byte("# Readme")},
		"docs/security.md":        &fstest.MapFile{Data: []byte("# Security")},
		".git/ignored.md":         &fstest.MapFile{Data: []byte("# Git")},
		".agents/ignored.md":      &fstest.MapFile{Data: []byte("# Agents")},
		".codex/ignored.md":       &fstest.MapFile{Data: []byte("# Codex")},
		"node_modules/ignored.md": &fstest.MapFile{Data: []byte("# Node")},
		"vendor/ignored.md":       &fstest.MapFile{Data: []byte("# Vendor")},
	}
	provider := NewFilesystemProvider(root)
	accessor, err := provider.Open(context.Background(), coredatasource.Spec{
		Name:     "local-docs",
		Kind:     "filesystem",
		Entities: []coredatasource.EntityType{FileDocumentEntity},
		Config:   map[string]string{"path": ".", "include": "*.md"},
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	corpusProvider, ok := accessor.(coredatasource.CorpusProvider)
	if !ok {
		t.Fatal("filesystem accessor does not implement CorpusProvider")
	}
	page, err := corpusProvider.Corpus(context.Background(), coredatasource.CorpusRequest{Entity: FileDocumentEntity})
	if err != nil {
		t.Fatalf("Corpus: %v", err)
	}
	got := map[string]bool{}
	for _, doc := range page.Documents {
		got[doc.Ref.ID] = true
	}
	for _, id := range []string{"README.md", "docs/security.md"} {
		if !got[id] {
			t.Fatalf("missing indexed document %q in %#v", id, got)
		}
	}
	for _, id := range []string{".git/ignored.md", ".agents/ignored.md", ".codex/ignored.md", "node_modules/ignored.md", "vendor/ignored.md"} {
		if got[id] {
			t.Fatalf("unexpected indexed document %q in %#v", id, got)
		}
	}
	if len(page.Documents) != 2 {
		t.Fatalf("documents = %d, want 2", len(page.Documents))
	}
}

var _ fs.FS = fstest.MapFS{}
