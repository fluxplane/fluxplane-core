package datasourceindex

import (
	"context"
	"strings"
	"testing"
	"time"

	coredata "github.com/fluxplane/engine/core/data"
	coredatasource "github.com/fluxplane/engine/core/datasource"
	runtimedata "github.com/fluxplane/engine/runtime/data"
	"github.com/fluxplane/engine/runtime/datasource/semantic"
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
	var pageEvent ProgressEvent
	for _, event := range events {
		if event.Kind == ProgressPageFetched {
			pageEvent = event
			break
		}
	}
	if pageEvent.Page != 1 || pageEvent.PageDocuments != 1 || pageEvent.Documents != 1 || pageEvent.Queued != 1 || !pageEvent.Complete || pageEvent.FirstID != "a.md" || pageEvent.LastID != "a.md" {
		t.Fatalf("page event = %#v, want cumulative progress details", pageEvent)
	}
}

func TestBuildReportsCumulativePageProgress(t *testing.T) {
	ctx := context.Background()
	accessor := fakePagingCorpusAccessor{
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
		pages: []coredatasource.CorpusPage{
			{
				Documents: []coredatasource.CorpusDocument{{
					Ref:   coredatasource.RecordRef{Datasource: "docs", Entity: "file.document", ID: "a.md"},
					Title: "A",
				}},
				NextCursor: "next",
			},
			{
				Documents: []coredatasource.CorpusDocument{{
					Ref:   coredatasource.RecordRef{Datasource: "docs", Entity: "file.document", ID: "b.md"},
					Title: "B",
				}},
				Complete: true,
			},
		},
	}
	registry, err := coredatasource.NewRegistry([]coredatasource.Accessor{accessor}, nil)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	index, err := semantic.New(semantic.HashEmbedder{}, semantic.NewJSONStore(""), semantic.Config{})
	if err != nil {
		t.Fatalf("semantic.New: %v", err)
	}
	var pages []ProgressEvent
	_, err = Build(ctx, Request{
		Registry: registry,
		Index:    index,
		Now:      incrementingNow(time.Date(2026, 5, 19, 12, 0, 0, 0, time.UTC), time.Second),
		Progress: func(event ProgressEvent) {
			if event.Kind == ProgressPageFetched {
				pages = append(pages, event)
			}
		},
	})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(pages) != 2 {
		t.Fatalf("pages = %#v, want two page events", pages)
	}
	if pages[0].Page != 1 || pages[0].Documents != 1 || pages[0].Queued != 1 || pages[0].Complete {
		t.Fatalf("first page = %#v, want incomplete cumulative first page", pages[0])
	}
	if pages[1].Page != 2 || pages[1].Documents != 2 || pages[1].Queued != 2 || !pages[1].Complete || pages[1].Rate <= 0 {
		t.Fatalf("second page = %#v, want complete cumulative second page with rate", pages[1])
	}
}

func TestBuildReportsStaleRunningCheckpoint(t *testing.T) {
	ctx := context.Background()
	accessor := fakeCorpusAccessor{
		spec: coredatasource.Spec{
			Name:     "gitlab",
			Kind:     "fake",
			Entities: []coredatasource.EntityType{"gitlab.user_membership"},
			Index:    coredatasource.IndexSpec{Enabled: true},
		},
		entity: coredatasource.EntitySpec{
			Type:         "gitlab.user_membership",
			Capabilities: []coredatasource.EntityCapability{coredatasource.EntityCapabilityIndex},
		},
		docs: []coredatasource.CorpusDocument{{
			Ref:   coredatasource.RecordRef{Datasource: "gitlab", Entity: "gitlab.user_membership", ID: "42:project:12"},
			Title: "Ada in runtime",
		}},
	}
	registry, err := coredatasource.NewRegistry([]coredatasource.Accessor{accessor}, nil)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	store := runtimedata.NewMemoryStore()
	startedAt := time.Date(2026, 5, 19, 11, 0, 0, 0, time.UTC)
	if err := putDataIndexRun(ctx, store, semantic.IndexRunState{
		Datasource: "gitlab",
		Entity:     "gitlab.user_membership",
		Phase:      PhaseFields,
		Status:     semantic.IndexRunStatusRunning,
		StartedAt:  startedAt,
	}); err != nil {
		t.Fatalf("putDataIndexRun: %v", err)
	}
	var sawStale bool
	_, err = Build(ctx, Request{
		Registry:  registry,
		DataStore: store,
		Phase:     PhaseFields,
		Freshness: time.Hour,
		Progress: func(event ProgressEvent) {
			if event.Kind == ProgressEntityRunningStale {
				sawStale = strings.Contains(event.Message, startedAt.Format(time.RFC3339Nano))
			}
		},
	})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if !sawStale {
		t.Fatal("missing stale running checkpoint progress event")
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

func TestBuildFieldsPhaseWritesDataStore(t *testing.T) {
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
				{Name: "name", Searchable: true, Filterable: true},
			},
		},
		docs: []coredatasource.CorpusDocument{{
			Ref:      coredatasource.RecordRef{Datasource: "gitlab", Entity: "gitlab.project", ID: "12"},
			Title:    "runtime",
			Body:     "Runtime repository",
			Metadata: map[string]string{"name": "runtime"},
		}},
	}
	registry, err := coredatasource.NewRegistry([]coredatasource.Accessor{accessor}, nil)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	store := runtimedata.NewMemoryStore()
	result, err := Build(ctx, Request{Registry: registry, DataStore: store, Phase: PhaseFields})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if result.Indexed != 1 || result.Documents != 1 {
		t.Fatalf("result = %#v, want one data record", result)
	}
	record, ok, err := store.GetRecord(ctx, coredata.Scope{}, coredata.Ref{Source: "gitlab", Entity: "gitlab.project", View: "gitlab.project", ID: "12"})
	if err != nil {
		t.Fatalf("GetRecord: %v", err)
	}
	if !ok || record.Title != "runtime" || record.Fields["name"][0] != "runtime" {
		t.Fatalf("record = %#v ok=%v, want materialized project", record, ok)
	}
}

func TestBuildFieldsPhaseBatchesDataStoreWritesByCorpusPage(t *testing.T) {
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
			Fields:       []coredatasource.FieldSpec{{Name: "name", Searchable: true, Filterable: true}},
		},
		docs: []coredatasource.CorpusDocument{
			{
				Ref:      coredatasource.RecordRef{Datasource: "gitlab", Entity: "gitlab.project", ID: "12"},
				Title:    "runtime",
				Metadata: map[string]string{"name": "runtime"},
			},
			{
				Ref:      coredatasource.RecordRef{Datasource: "gitlab", Entity: "gitlab.project", ID: "13"},
				Title:    "coder",
				Metadata: map[string]string{"name": "coder"},
			},
		},
	}
	registry, err := coredatasource.NewRegistry([]coredatasource.Accessor{accessor}, nil)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	store := &countingDataStore{Store: runtimedata.NewMemoryStore()}
	result, err := Build(ctx, Request{Registry: registry, DataStore: store, Phase: PhaseFields})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if result.Indexed != 2 || result.Documents != 2 {
		t.Fatalf("result = %#v, want two indexed records", result)
	}
	if store.maxRecordBatch != 2 {
		t.Fatalf("record calls=%d max batch=%d, want page-sized corpus batch", store.recordCalls, store.maxRecordBatch)
	}
}

func TestBuildFieldsPhaseWritesDataStoreRelations(t *testing.T) {
	ctx := context.Background()
	accessor := fakeCorpusAccessor{
		spec: coredatasource.Spec{
			Name:     "gitlab",
			Kind:     "fake",
			Entities: []coredatasource.EntityType{"gitlab.user_membership"},
			Index:    coredatasource.IndexSpec{Enabled: true},
		},
		entity: coredatasource.EntitySpec{
			Type:         "gitlab.user_membership",
			Capabilities: []coredatasource.EntityCapability{coredatasource.EntityCapabilityIndex},
		},
		docs: []coredatasource.CorpusDocument{{
			Ref:   coredatasource.RecordRef{Datasource: "gitlab", Entity: "gitlab.user_membership", ID: "42:namespace:7"},
			Title: "Ada in Platform",
			Metadata: map[string]string{
				"relation.user_group.source_entity":     "gitlab.user",
				"relation.user_group.source_id":         "42",
				"relation.user_group.name":              "groups",
				"relation.user_group.target_entity":     "gitlab.group",
				"relation.user_group.target_id":         "engineering/platform",
				"relation.user_group.target_title":      "Platform",
				"relation.user_group.target_field.path": "engineering/platform",
			},
		}},
	}
	registry, err := coredatasource.NewRegistry([]coredatasource.Accessor{accessor}, nil)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	store := runtimedata.NewMemoryStore()
	if _, err := Build(ctx, Request{Registry: registry, DataStore: store, Phase: PhaseFields}); err != nil {
		t.Fatalf("Build: %v", err)
	}
	result, err := store.QueryRelations(ctx, coredata.RelationQuery{
		Sources:  []coredata.SourceName{"gitlab"},
		Relation: "groups",
		Source:   coredata.Ref{Source: "gitlab", Entity: "gitlab.user", View: "gitlab.user", ID: "42"},
	})
	if err != nil {
		t.Fatalf("QueryRelations: %v", err)
	}
	if len(result.Relations) != 1 || result.Relations[0].Target.ID != "engineering/platform" || result.Relations[0].Summary.Fields["path"] != "engineering/platform" {
		t.Fatalf("relations = %#v, want user group edge", result.Relations)
	}
}

func TestBuildMaterializesDeclaredViewWithRelationSummaries(t *testing.T) {
	ctx := context.Background()
	accessor := fakeMultiCorpusAccessor{
		spec: coredatasource.Spec{
			Name:     "gitlab",
			Kind:     "fake",
			Entities: []coredatasource.EntityType{"gitlab.user", "gitlab.user_membership"},
			Index:    coredatasource.IndexSpec{Enabled: true},
		},
		entities: []coredatasource.EntitySpec{{
			Type:         "gitlab.user",
			Capabilities: []coredatasource.EntityCapability{coredatasource.EntityCapabilityIndex},
		}, {
			Type:         "gitlab.user_membership",
			Capabilities: []coredatasource.EntityCapability{coredatasource.EntityCapabilityIndex},
		}},
		docs: map[coredatasource.EntityType][]coredatasource.CorpusDocument{
			"gitlab.user": {{
				Ref:      coredatasource.RecordRef{Datasource: "gitlab", Entity: "gitlab.user", ID: "42"},
				Title:    "Ada",
				Body:     "GitLab user Ada",
				Metadata: map[string]string{"username": "ada"},
			}},
			"gitlab.user_membership": {{
				Ref:   coredatasource.RecordRef{Datasource: "gitlab", Entity: "gitlab.user_membership", ID: "42:namespace:7"},
				Title: "Ada in Platform",
				Metadata: map[string]string{
					"relation.user_group.source_entity":          "gitlab.user",
					"relation.user_group.source_id":              "42",
					"relation.user_group.name":                   "groups",
					"relation.user_group.target_entity":          "gitlab.group",
					"relation.user_group.target_id":              "engineering/platform",
					"relation.user_group.target_title":           "Platform",
					"relation.user_group.target_field.id":        "7",
					"relation.user_group.target_field.full_path": "engineering/platform",
					"relation.user_group.target_field.name":      "Platform",
				},
			}},
		},
	}
	registry, err := coredatasource.NewRegistry([]coredatasource.Accessor{accessor}, nil)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	store := runtimedata.NewMemoryStore()
	_, err = Build(ctx, Request{
		Registry:  registry,
		DataStore: store,
		Phase:     PhaseFields,
		DataSources: []coredata.SourceSpec{{
			Name: "gitlab",
			Kind: "fake",
			Views: []coredata.ViewSpec{{
				Name:   "gitlab.user_with_groups",
				Entity: "gitlab.user",
				Source: "gitlab.user",
				Includes: []coredata.RelationIncludeSpec{{
					Relation: "groups",
					Target:   "gitlab.group",
					Fields:   []string{"id", "full_path", "name"},
				}},
			}},
		}},
	})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	record, ok, err := store.GetRecord(ctx, coredata.Scope{}, coredata.Ref{Source: "gitlab", Entity: "gitlab.user", View: "gitlab.user_with_groups", ID: "42"})
	if err != nil {
		t.Fatalf("GetRecord: %v", err)
	}
	if !ok {
		t.Fatal("materialized view record missing")
	}
	if len(record.Relations["groups"]) != 1 || record.Relations["groups"][0].Fields["full_path"] != "engineering/platform" {
		t.Fatalf("relations = %#v, want embedded group summary", record.Relations)
	}
	if got := record.Fields["groups.full_path"]; len(got) != 1 || got[0] != "engineering/platform" {
		t.Fatalf("groups.full_path = %#v, want engineering/platform", got)
	}
}

func TestBuildMaterializesDeclaredViewWithLiveRelationFallback(t *testing.T) {
	ctx := context.Background()
	accessor := fakeRelationCorpusAccessor{
		fakeCorpusAccessor: fakeCorpusAccessor{
			spec: coredatasource.Spec{
				Name:     "slack",
				Kind:     "fake",
				Entities: []coredatasource.EntityType{"slack.channel"},
				Index:    coredatasource.IndexSpec{Enabled: true},
			},
			entity: coredatasource.EntitySpec{
				Type:         "slack.channel",
				Capabilities: []coredatasource.EntityCapability{coredatasource.EntityCapabilityIndex},
			},
			docs: []coredatasource.CorpusDocument{{
				Ref:      coredatasource.RecordRef{Datasource: "slack", Entity: "slack.channel", ID: "C1"},
				Title:    "engineering",
				Metadata: map[string]string{"name": "engineering"},
			}},
		},
		relation: coredatasource.RelationResult{
			Datasource:   "slack",
			Entity:       "slack.channel",
			ID:           "C1",
			Relation:     "members",
			TargetEntity: "slack.user",
			Records: []coredatasource.Record{{
				ID:         "U1",
				Datasource: "slack",
				Entity:     "slack.user",
				Title:      "Ada",
				Metadata:   map[string]string{"id": "U1", "name": "ada", "email": "ada@example.test"},
			}},
			Complete: true,
			Exact:    true,
		},
	}
	registry, err := coredatasource.NewRegistry([]coredatasource.Accessor{accessor}, nil)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	store := runtimedata.NewMemoryStore()
	_, err = Build(ctx, Request{
		Registry:  registry,
		DataStore: store,
		Phase:     PhaseFields,
		DataSources: []coredata.SourceSpec{{
			Name: "slack",
			Kind: "fake",
			Views: []coredata.ViewSpec{{
				Name:   "slack.channel_with_members",
				Entity: "slack.channel",
				Source: "slack.channel",
				Includes: []coredata.RelationIncludeSpec{{
					Relation: "members",
					Target:   "slack.user",
					Fields:   []string{"id", "name", "email"},
				}},
			}},
		}},
	})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	record, ok, err := store.GetRecord(ctx, coredata.Scope{}, coredata.Ref{Source: "slack", Entity: "slack.channel", View: "slack.channel_with_members", ID: "C1"})
	if err != nil {
		t.Fatalf("GetRecord: %v", err)
	}
	if !ok || len(record.Relations["members"]) != 1 || record.Fields["members.email"][0] != "ada@example.test" {
		t.Fatalf("record = %#v ok=%v, want materialized member summary", record, ok)
	}
	edges, err := store.QueryRelations(ctx, coredata.RelationQuery{Sources: []coredata.SourceName{"slack"}, Relation: "members"})
	if err != nil {
		t.Fatalf("QueryRelations: %v", err)
	}
	if len(edges.Relations) != 1 || edges.Relations[0].Target.ID != "U1" {
		t.Fatalf("relations = %#v, want stored live relation edge", edges.Relations)
	}
}

func TestFullBuildDeletesRemovedMaterializedViewRows(t *testing.T) {
	ctx := context.Background()
	accessor := fakeCorpusAccessor{
		spec: coredatasource.Spec{
			Name:     "gitlab",
			Kind:     "fake",
			Entities: []coredatasource.EntityType{"gitlab.user"},
			Index:    coredatasource.IndexSpec{Enabled: true},
		},
		entity: coredatasource.EntitySpec{
			Type:         "gitlab.user",
			Capabilities: []coredatasource.EntityCapability{coredatasource.EntityCapabilityIndex},
		},
		docs: []coredatasource.CorpusDocument{{
			Ref:      coredatasource.RecordRef{Datasource: "gitlab", Entity: "gitlab.user", ID: "42"},
			Title:    "Ada",
			Metadata: map[string]string{"username": "ada"},
		}},
	}
	registry, err := coredatasource.NewRegistry([]coredatasource.Accessor{accessor}, nil)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	store := runtimedata.NewMemoryStore()
	if err := store.UpsertRecords(ctx,
		coredata.Record{
			Ref:    coredata.Ref{Source: "gitlab", Entity: "gitlab.user", View: "gitlab.user", ID: "42"},
			Title:  "Ada",
			Fields: map[string][]string{"username": {"ada"}},
		},
		coredata.Record{
			Ref:    coredata.Ref{Source: "gitlab", Entity: "gitlab.user", View: "gitlab.old_user_with_groups", ID: "42"},
			Title:  "Ada stale",
			Fields: map[string][]string{"username": {"ada"}},
		},
	); err != nil {
		t.Fatalf("UpsertRecords: %v", err)
	}
	if _, err := Build(ctx, Request{Registry: registry, DataStore: store, Phase: PhaseFields, Full: true}); err != nil {
		t.Fatalf("Build: %v", err)
	}
	if _, ok, err := store.GetRecord(ctx, coredata.Scope{}, coredata.Ref{Source: "gitlab", Entity: "gitlab.user", View: "gitlab.user", ID: "42"}); err != nil || !ok {
		t.Fatalf("base record ok=%v err=%v, want present", ok, err)
	}
	if record, ok, err := store.GetRecord(ctx, coredata.Scope{}, coredata.Ref{Source: "gitlab", Entity: "gitlab.user", View: "gitlab.old_user_with_groups", ID: "42"}); err != nil || ok {
		t.Fatalf("stale view record = %#v ok=%v err=%v, want deleted", record, ok, err)
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

type fakeMultiCorpusAccessor struct {
	spec     coredatasource.Spec
	entities []coredatasource.EntitySpec
	docs     map[coredatasource.EntityType][]coredatasource.CorpusDocument
}

func (a fakeMultiCorpusAccessor) Spec() coredatasource.Spec { return a.spec }
func (a fakeMultiCorpusAccessor) Entities() []coredatasource.EntitySpec {
	return append([]coredatasource.EntitySpec(nil), a.entities...)
}
func (a fakeMultiCorpusAccessor) Corpus(_ context.Context, req coredatasource.CorpusRequest) (coredatasource.CorpusPage, error) {
	return coredatasource.CorpusPage{Documents: a.docs[req.Entity], Complete: true}, nil
}

type fakeRelationCorpusAccessor struct {
	fakeCorpusAccessor
	relation coredatasource.RelationResult
}

func (a fakeRelationCorpusAccessor) Relation(_ context.Context, req coredatasource.RelationRequest) (coredatasource.RelationResult, error) {
	if req.Entity != a.relation.Entity || req.ID != a.relation.ID || req.Relation != a.relation.Relation {
		return coredatasource.RelationResult{}, coredatasource.ErrNotFound
	}
	return a.relation, nil
}

type fakePagingCorpusAccessor struct {
	spec   coredatasource.Spec
	entity coredatasource.EntitySpec
	pages  []coredatasource.CorpusPage
}

func (a fakePagingCorpusAccessor) Spec() coredatasource.Spec { return a.spec }
func (a fakePagingCorpusAccessor) Entities() []coredatasource.EntitySpec {
	return []coredatasource.EntitySpec{a.entity}
}
func (a fakePagingCorpusAccessor) Corpus(_ context.Context, req coredatasource.CorpusRequest) (coredatasource.CorpusPage, error) {
	if req.Cursor == "" {
		return a.pages[0], nil
	}
	return a.pages[1], nil
}

func incrementingNow(start time.Time, step time.Duration) func() time.Time {
	current := start.Add(-step)
	return func() time.Time {
		current = current.Add(step)
		return current
	}
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

type countingDataStore struct {
	coredata.Store
	recordCalls    int
	maxRecordBatch int
}

func (s *countingDataStore) UpsertRecords(ctx context.Context, records ...coredata.Record) error {
	s.recordCalls++
	if len(records) > s.maxRecordBatch {
		s.maxRecordBatch = len(records)
	}
	return s.Store.UpsertRecords(ctx, records...)
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
