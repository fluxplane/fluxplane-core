package datasourceindex

import (
	"context"
	"testing"

	coredatasource "github.com/fluxplane/agentruntime/core/datasource"
	"github.com/fluxplane/agentruntime/runtime/datasource/semantic"
)

func TestBuildIndexesCorpusProviderIncrementally(t *testing.T) {
	ctx := context.Background()
	accessor := fakeCorpusAccessor{
		spec: coredatasource.Spec{
			Name:     "docs",
			Kind:     "fake",
			Entities: []coredatasource.EntityType{"file.document"},
		},
		entity: coredatasource.EntitySpec{
			Type:         "file.document",
			Capabilities: []coredatasource.EntityCapability{coredatasource.EntityCapabilitySemanticSearch},
		},
		docs: []coredatasource.CorpusDocument{{
			Ref:   coredatasource.RecordRef{Datasource: "docs", Entity: "file.document", ID: "a.md"},
			Title: "Alpha",
			Body:  "semantic indexing alpha document",
		}},
	}
	registry, err := coredatasource.NewRegistry([]coredatasource.Accessor{accessor}, nil)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	index, err := semantic.New(semantic.HashEmbedder{}, semantic.NewJSONStore(""), semantic.Config{})
	if err != nil {
		t.Fatalf("semantic.New: %v", err)
	}
	first, err := Build(ctx, Request{Registry: registry, Index: index, Datasource: "docs", Entity: "file.document"})
	if err != nil {
		t.Fatalf("Build first: %v", err)
	}
	if first.Indexed != 1 || first.Skipped != 0 {
		t.Fatalf("first result = %#v, want one indexed", first)
	}
	second, err := Build(ctx, Request{Registry: registry, Index: index, Datasource: "docs", Entity: "file.document"})
	if err != nil {
		t.Fatalf("Build second: %v", err)
	}
	if second.Indexed != 0 || second.Skipped != 1 {
		t.Fatalf("second result = %#v, want one skipped", second)
	}
}

type fakeCorpusAccessor struct {
	spec   coredatasource.Spec
	entity coredatasource.EntitySpec
	docs   []coredatasource.CorpusDocument
}

func (a fakeCorpusAccessor) Spec() coredatasource.Spec { return a.spec }
func (a fakeCorpusAccessor) Entities() []coredatasource.EntitySpec {
	return []coredatasource.EntitySpec{a.entity}
}
func (a fakeCorpusAccessor) Corpus(context.Context, coredatasource.CorpusRequest) (coredatasource.CorpusPage, error) {
	return coredatasource.CorpusPage{Documents: a.docs, Complete: true}, nil
}
