package datasourceindex

import (
	"context"
	"testing"
	"time"

	coredatasource "github.com/fluxplane/agentruntime/core/datasource"
	"github.com/fluxplane/agentruntime/runtime/datasource/semantic"
)

func TestBuildQueuesSemanticCorpusWithoutEmbedding(t *testing.T) {
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
	if first.Queued != 1 || first.Indexed != 0 || first.Skipped != 0 {
		t.Fatalf("first result = %#v, want one queued", first)
	}
	status, err := index.Status(ctx, semantic.StatusRequest{Datasource: "docs", Entity: "file.document"})
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if len(status.Queue) != 1 || len(status.Documents) != 0 {
		t.Fatalf("status = %#v, want queued semantic job and no embedded document", status)
	}
}

func TestBuildIndexedOnlySkipsNonIndexedDatasources(t *testing.T) {
	ctx := context.Background()
	indexed := fakeCorpusAccessor{
		spec: coredatasource.Spec{
			Name:     "indexed",
			Kind:     "fake",
			Entities: []coredatasource.EntityType{"file.document"},
			Index:    coredatasource.IndexSpec{Enabled: true},
		},
		entity: coredatasource.EntitySpec{
			Type:         "file.document",
			Capabilities: []coredatasource.EntityCapability{coredatasource.EntityCapabilitySemanticSearch},
		},
		docs: []coredatasource.CorpusDocument{{
			Ref:   coredatasource.RecordRef{Datasource: "indexed", Entity: "file.document", ID: "indexed.md"},
			Title: "Indexed",
			Body:  "indexed document",
		}},
	}
	live := fakeCorpusAccessor{
		spec: coredatasource.Spec{
			Name:     "live",
			Kind:     "fake",
			Entities: []coredatasource.EntityType{"file.document"},
		},
		entity: coredatasource.EntitySpec{
			Type:         "file.document",
			Capabilities: []coredatasource.EntityCapability{coredatasource.EntityCapabilitySemanticSearch},
		},
		docs: []coredatasource.CorpusDocument{{
			Ref:   coredatasource.RecordRef{Datasource: "live", Entity: "file.document", ID: "live.md"},
			Title: "Live",
			Body:  "live document",
		}},
	}
	registry, err := coredatasource.NewRegistry([]coredatasource.Accessor{indexed, live}, nil)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	index, err := semantic.New(semantic.HashEmbedder{}, semantic.NewJSONStore(""), semantic.Config{})
	if err != nil {
		t.Fatalf("semantic.New: %v", err)
	}
	result, err := Build(ctx, Request{Registry: registry, Index: index, IndexedOnly: true})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if result.Queued != 1 || result.Documents != 1 {
		t.Fatalf("result = %#v, want one queued document", result)
	}
	status, err := index.Status(ctx, semantic.StatusRequest{})
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if len(status.Queue) != 1 || status.Queue[0].Ref.Datasource != "indexed" {
		t.Fatalf("queue = %#v, want only indexed datasource", status.Queue)
	}
}

func TestBuildReportsProgress(t *testing.T) {
	ctx := context.Background()
	accessor := fakeCorpusAccessor{
		spec: coredatasource.Spec{
			Name:     "docs",
			Kind:     "fake",
			Entities: []coredatasource.EntityType{"file.document"},
			Index:    coredatasource.IndexSpec{Enabled: true},
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
	var events []ProgressEvent
	_, err = Build(ctx, Request{
		Registry: registry,
		Index:    index,
		Progress: func(event ProgressEvent) {
			events = append(events, event)
		},
	})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	kinds := map[string]bool{}
	for _, event := range events {
		kinds[event.Kind] = true
	}
	for _, want := range []string{ProgressEntityStart, ProgressPageFetched, ProgressDocumentQueued, ProgressEntityComplete, ProgressComplete} {
		if !kinds[want] {
			t.Fatalf("progress kinds = %#v, missing %s", kinds, want)
		}
	}
}

func TestBuildFieldsPhaseIndexesRecordsWithoutSemanticDocuments(t *testing.T) {
	ctx := context.Background()
	accessor := fakeCorpusAccessor{
		spec: coredatasource.Spec{
			Name:     "gitlab",
			Kind:     "fake",
			Entities: []coredatasource.EntityType{"gitlab.project"},
			Index:    coredatasource.IndexSpec{Enabled: true},
		},
		entity: coredatasource.EntitySpec{
			Type:         "gitlab.project",
			Capabilities: []coredatasource.EntityCapability{coredatasource.EntityCapabilityIndex},
			Fields: []coredatasource.FieldSpec{
				{Name: "id", Identifier: true, Filterable: true},
				{Name: "name", Searchable: true},
				{Name: "path_with_namespace", Searchable: true, Filterable: true},
			},
		},
		docs: []coredatasource.CorpusDocument{{
			Ref:   coredatasource.RecordRef{Datasource: "gitlab", Entity: "gitlab.project", ID: "fluxplane/runtime"},
			Title: "fluxplane/runtime",
			Body:  "Runtime repository",
			Metadata: map[string]string{
				"id":                  "12",
				"name":                "runtime",
				"path_with_namespace": "fluxplane/runtime",
			},
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
	result, err := Build(ctx, Request{Registry: registry, Index: index, Phase: PhaseFields})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if result.Indexed != 1 || result.Documents != 1 {
		t.Fatalf("result = %#v, want one indexed field record", result)
	}
	status, err := index.Status(ctx, semantic.StatusRequest{Datasource: "gitlab", Entity: "gitlab.project"})
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if len(status.Records) != 1 || len(status.Documents) != 0 {
		t.Fatalf("status = %#v, want one field record and no semantic documents", status)
	}
	search, err := index.SearchFields(ctx, semantic.FieldSearchRequest{
		Query:       "fluxplane/runtime",
		Datasources: []coredatasource.Name{"gitlab"},
		Entities:    []coredatasource.EntityType{"gitlab.project"},
	})
	if err != nil {
		t.Fatalf("SearchFields: %v", err)
	}
	if len(search.Hits) != 1 || search.Hits[0].Record.ID != "fluxplane/runtime" {
		t.Fatalf("hits = %#v, want runtime project", search.Hits)
	}
}

func TestBuildSkipsFreshDatasourceEntity(t *testing.T) {
	ctx := context.Background()
	accessor := &countingCorpusAccessor{
		spec: coredatasource.Spec{
			Name:     "gitlab",
			Kind:     "fake",
			Entities: []coredatasource.EntityType{"gitlab.project"},
			Index:    coredatasource.IndexSpec{Enabled: true},
		},
		entity: coredatasource.EntitySpec{
			Type:         "gitlab.project",
			Capabilities: []coredatasource.EntityCapability{coredatasource.EntityCapabilityIndex},
		},
		docs: []coredatasource.CorpusDocument{{
			Ref:   coredatasource.RecordRef{Datasource: "gitlab", Entity: "gitlab.project", ID: "fluxplane/runtime"},
			Title: "fluxplane/runtime",
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
	now := time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC)
	_, err = Build(ctx, Request{Registry: registry, Index: index, Phase: PhaseFields, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatalf("Build first: %v", err)
	}
	result, err := Build(ctx, Request{
		Registry:  registry,
		Index:     index,
		Phase:     PhaseFields,
		Freshness: time.Hour,
		Now:       func() time.Time { return now.Add(5 * time.Minute) },
	})
	if err != nil {
		t.Fatalf("Build second: %v", err)
	}
	if result.Skipped != 1 || result.Documents != 0 {
		t.Fatalf("result = %#v, want fresh entity skip", result)
	}
	if accessor.calls != 1 {
		t.Fatalf("corpus calls = %d, want first run only", accessor.calls)
	}
}

func TestBuildRunsDatasourceEntitiesConcurrently(t *testing.T) {
	ctx := context.Background()
	started := make(chan coredatasource.Name, 2)
	release := make(chan struct{})
	accessors := []coredatasource.Accessor{
		blockingCorpusAccessor{name: "one", started: started, release: release},
		blockingCorpusAccessor{name: "two", started: started, release: release},
	}
	registry, err := coredatasource.NewRegistry(accessors, nil)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	index, err := semantic.New(semantic.HashEmbedder{}, semantic.NewJSONStore(""), semantic.Config{})
	if err != nil {
		t.Fatalf("semantic.New: %v", err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := Build(ctx, Request{Registry: registry, Index: index, Phase: PhaseFields, Concurrency: 2})
		done <- err
	}()
	for i := 0; i < 2; i++ {
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatal("timed out waiting for concurrent corpus starts")
		}
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatalf("Build: %v", err)
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

type countingCorpusAccessor struct {
	spec   coredatasource.Spec
	entity coredatasource.EntitySpec
	docs   []coredatasource.CorpusDocument
	calls  int
}

func (a *countingCorpusAccessor) Spec() coredatasource.Spec { return a.spec }
func (a *countingCorpusAccessor) Entities() []coredatasource.EntitySpec {
	return []coredatasource.EntitySpec{a.entity}
}
func (a *countingCorpusAccessor) Corpus(context.Context, coredatasource.CorpusRequest) (coredatasource.CorpusPage, error) {
	a.calls++
	return coredatasource.CorpusPage{Documents: a.docs, Complete: true}, nil
}

type blockingCorpusAccessor struct {
	name    coredatasource.Name
	started chan<- coredatasource.Name
	release <-chan struct{}
}

func (a blockingCorpusAccessor) Spec() coredatasource.Spec {
	return coredatasource.Spec{
		Name:     a.name,
		Kind:     "fake",
		Entities: []coredatasource.EntityType{"test.entity"},
		Index:    coredatasource.IndexSpec{Enabled: true},
	}
}
func (a blockingCorpusAccessor) Entities() []coredatasource.EntitySpec {
	return []coredatasource.EntitySpec{{
		Type:         "test.entity",
		Capabilities: []coredatasource.EntityCapability{coredatasource.EntityCapabilityIndex},
	}}
}
func (a blockingCorpusAccessor) Corpus(ctx context.Context, _ coredatasource.CorpusRequest) (coredatasource.CorpusPage, error) {
	a.started <- a.name
	select {
	case <-a.release:
	case <-ctx.Done():
		return coredatasource.CorpusPage{}, ctx.Err()
	}
	return coredatasource.CorpusPage{Documents: []coredatasource.CorpusDocument{{
		Ref:   coredatasource.RecordRef{Datasource: a.name, Entity: "test.entity", ID: "1"},
		Title: string(a.name),
	}}}, nil
}
