package datasourceplugin

import (
	"context"
	"strings"
	"testing"

	corecontext "github.com/fluxplane/agentruntime/core/context"
	coredatasource "github.com/fluxplane/agentruntime/core/datasource"
	"github.com/fluxplane/agentruntime/core/operation"
	"github.com/fluxplane/agentruntime/runtime/datasource/semantic"
)

type memoryAccessor struct {
	spec           coredatasource.Spec
	entity         coredatasource.EntitySpec
	records        []coredatasource.Record
	relationResult coredatasource.RelationResult
}

func (a memoryAccessor) Spec() coredatasource.Spec { return a.spec }
func (a memoryAccessor) Entities() []coredatasource.EntitySpec {
	return []coredatasource.EntitySpec{a.entity}
}

func (a memoryAccessor) Search(_ context.Context, req coredatasource.SearchRequest) (coredatasource.SearchResult, error) {
	var records []coredatasource.Record
	for _, record := range a.records {
		if req.Query == "" || record.Title == req.Query {
			records = append(records, record)
		}
	}
	return coredatasource.SearchResult{Datasource: a.spec.Name, Entity: req.Entity, Records: records, Total: len(records)}, nil
}

func (a memoryAccessor) Get(_ context.Context, req coredatasource.GetRequest) (coredatasource.Record, error) {
	for _, record := range a.records {
		if record.ID == req.ID {
			return record, nil
		}
	}
	return coredatasource.Record{}, coredatasource.ErrNotFound
}

func (a memoryAccessor) BatchGet(_ context.Context, req coredatasource.BatchGetRequest) (coredatasource.BatchGetResult, error) {
	out := coredatasource.BatchGetResult{Datasource: a.spec.Name, Entity: req.Entity}
	for _, id := range req.IDs {
		found := false
		for _, record := range a.records {
			if record.ID == id {
				out.Records = append(out.Records, record)
				found = true
				break
			}
		}
		if !found {
			out.Errors = append(out.Errors, coredatasource.BatchGetError{ID: id, Message: coredatasource.ErrNotFound.Error()})
		}
	}
	return out, nil
}

func (a memoryAccessor) Relation(_ context.Context, req coredatasource.RelationRequest) (coredatasource.RelationResult, error) {
	out := a.relationResult
	out.Datasource = a.spec.Name
	out.Entity = req.Entity
	out.ID = req.ID
	out.Relation = req.Relation
	return out, nil
}

func TestSearchSelectsEntitiesWithoutDatasourceInput(t *testing.T) {
	registry, err := coredatasource.NewRegistry([]coredatasource.Accessor{
		memoryAccessor{
			spec:   coredatasource.Spec{Name: "jira", Entities: []coredatasource.EntityType{"jira.issue"}, Kind: "memory"},
			entity: coredatasource.EntitySpec{Type: "jira.issue"},
			records: []coredatasource.Record{{
				ID:         "DEV-381",
				Datasource: "jira",
				Entity:     "jira.issue",
				Title:      "DEV-381",
			}},
		},
		memoryAccessor{
			spec:   coredatasource.Spec{Name: "gitlab", Entities: []coredatasource.EntityType{"gitlab.project"}, Kind: "memory"},
			entity: coredatasource.EntitySpec{Type: "gitlab.project"},
			records: []coredatasource.Record{{
				ID:         "lyse",
				Datasource: "gitlab",
				Entity:     "gitlab.project",
				Title:      "lyse",
			}},
		},
	}, nil)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	ctx := operation.NewContext(coredatasource.ContextWithAccessPolicy(context.Background(), coredatasource.AccessPolicy{
		Datasources: []coredatasource.Name{"jira", "gitlab"},
	}), nil)
	result := New(registry).search(ctx, searchInput{Query: "DEV-381", Entities: []string{"jira.*"}})
	if result.Status != operation.StatusOK {
		t.Fatalf("result = %#v", result)
	}
	rendered := result.Output.(operation.Rendered)
	out := rendered.Data.(searchOutput)
	if len(out.Results) != 1 || out.Results[0].Datasource != "jira" || out.Results[0].Entity != "jira.issue" {
		t.Fatalf("results = %#v", out.Results)
	}
}

func TestBuildRegistryPassesSemanticIndexToAwareProviders(t *testing.T) {
	index, err := semantic.New(semantic.HashEmbedder{}, semantic.NewJSONStore(""), semantic.Config{})
	if err != nil {
		t.Fatalf("semantic.New: %v", err)
	}
	provider := indexAwareProvider{
		entity: coredatasource.EntitySpec{Type: "file.document"},
	}
	registry, err := BuildRegistryWithOptions(context.Background(), []coredatasource.Spec{{
		Name:     "docs",
		Kind:     "memory",
		Entities: []coredatasource.EntityType{"file.document"},
	}}, []coredatasource.Provider{provider}, RegistryOptions{SemanticIndex: index})
	if err != nil {
		t.Fatalf("BuildRegistryWithOptions: %v", err)
	}
	accessor, ok := registry.Get("docs")
	if !ok {
		t.Fatal("expected docs accessor")
	}
	got := accessor.(indexAwareAccessor)
	if got.index != index {
		t.Fatalf("semantic index was not passed to provider")
	}
}

func TestSearchRendersSlackMessageChannelIdentity(t *testing.T) {
	registry, err := coredatasource.NewRegistry([]coredatasource.Accessor{
		memoryAccessor{
			spec:   coredatasource.Spec{Name: "slack-bot", Entities: []coredatasource.EntityType{"slack.message"}, Kind: "memory"},
			entity: coredatasource.EntitySpec{Type: "slack.message"},
			records: []coredatasource.Record{{
				ID:         "C04LYSEINTERNAL:1710000000.000100",
				Datasource: "slack-bot",
				Entity:     "slack.message",
				Title:      "lyse-internal",
				Content:    "The ticket has a short description first.",
				URL:        "https://example.slack.com/archives/C04LYSEINTERNAL/p1710000000000100",
				Metadata: map[string]string{
					"channel":    "lyse-internal",
					"channel_id": "C04LYSEINTERNAL",
					"permalink":  "https://example.slack.com/archives/C04LYSEINTERNAL/p1710000000000100",
				},
			}},
		},
	}, nil)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	ctx := operation.NewContext(coredatasource.ContextWithAccessPolicy(context.Background(), coredatasource.AccessPolicy{
		Datasources: []coredatasource.Name{"slack-bot"},
	}), nil)

	result := New(registry).search(ctx, searchInput{Query: "lyse-internal", Entities: []string{"slack.message"}})
	if result.Status != operation.StatusOK {
		t.Fatalf("result = %#v", result)
	}
	rendered := result.Output.(operation.Rendered)
	for _, want := range []string{"#lyse-internal", "C04LYSEINTERNAL", "https://example.slack.com/archives/C04LYSEINTERNAL/p1710000000000100", "The ticket has a short description first."} {
		if !strings.Contains(rendered.Text, want) {
			t.Fatalf("rendered text = %q\nmissing %q", rendered.Text, want)
		}
	}
	if strings.Contains(rendered.Text, "#ly-internal") {
		t.Fatalf("rendered text = %q, channel name was shortened", rendered.Text)
	}
}

func TestSearchRejectsAmbiguousBroadSearch(t *testing.T) {
	registry, err := coredatasource.NewRegistry([]coredatasource.Accessor{
		memoryAccessor{
			spec:   coredatasource.Spec{Name: "jira", Entities: []coredatasource.EntityType{"jira.issue"}, Kind: "memory"},
			entity: coredatasource.EntitySpec{Type: "jira.issue"},
		},
		memoryAccessor{
			spec:   coredatasource.Spec{Name: "slack", Entities: []coredatasource.EntityType{"slack.message"}, Kind: "memory"},
			entity: coredatasource.EntitySpec{Type: "slack.message"},
		},
	}, nil)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	ctx := operation.NewContext(coredatasource.ContextWithAccessPolicy(context.Background(), coredatasource.AccessPolicy{
		Datasources: []coredatasource.Name{"jira", "slack"},
	}), nil)

	result := New(registry).search(ctx, searchInput{Query: "DEV-381"})
	if result.Status != operation.StatusFailed || result.Error == nil {
		t.Fatalf("result = %#v, want failure", result)
	}
	for _, want := range []string{"entities filter is required", "jira.issue", "slack.message", "jira.*", "slack.*"} {
		if !strings.Contains(result.Error.Message, want) {
			t.Fatalf("error message = %q, missing %q", result.Error.Message, want)
		}
	}
}

func TestSearchEnforcesAgentDatasourceAccess(t *testing.T) {
	registry, err := coredatasource.NewRegistry([]coredatasource.Accessor{
		memoryAccessor{
			spec:   coredatasource.Spec{Name: "docs", Entities: []coredatasource.EntityType{"file.document"}, Kind: "memory"},
			entity: coredatasource.EntitySpec{Type: "file.document"},
			records: []coredatasource.Record{{
				ID:         "one",
				Datasource: "docs",
				Entity:     "file.document",
				Title:      "hello",
			}},
		},
	}, nil)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	plugin := New(registry)

	denied := plugin.search(operation.NewContext(context.Background(), nil), searchInput{Query: "hello"})
	if denied.Status != operation.StatusFailed || denied.Error == nil || denied.Error.Code != "datasource_search_denied" {
		t.Fatalf("denied result = %#v", denied)
	}

	ctx := operation.NewContext(coredatasource.ContextWithAccessPolicy(context.Background(), coredatasource.AccessPolicy{Datasources: []coredatasource.Name{"docs"}}), nil)
	allowed := plugin.search(ctx, searchInput{Query: "hello"})
	if allowed.Status != operation.StatusOK {
		t.Fatalf("allowed result = %#v", allowed)
	}
	rendered, ok := allowed.Output.(operation.Rendered)
	if !ok {
		t.Fatalf("output = %#v, want Rendered", allowed.Output)
	}
	out, ok := rendered.Data.(searchOutput)
	if !ok || len(out.Results) != 1 || len(out.Results[0].Records) != 1 {
		t.Fatalf("data = %#v", rendered.Data)
	}
}

func TestSearchFiltersForceLexicalMode(t *testing.T) {
	accessor := &filterCaptureAccessor{
		memoryAccessor: memoryAccessor{
			spec: coredatasource.Spec{Name: "history", Entities: []coredatasource.EntityType{"session.operation"}, Kind: "memory"},
			entity: coredatasource.EntitySpec{
				Type: "session.operation",
				Capabilities: []coredatasource.EntityCapability{
					coredatasource.EntityCapabilitySearch,
					coredatasource.EntityCapabilitySemanticSearch,
				},
			},
			records: []coredatasource.Record{{
				ID:         "one",
				Datasource: "history",
				Entity:     "session.operation",
				Title:      "hello",
			}},
		},
	}
	registry, err := coredatasource.NewRegistry([]coredatasource.Accessor{accessor}, nil)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	ctx := operation.NewContext(coredatasource.ContextWithAccessPolicy(context.Background(), coredatasource.AccessPolicy{
		Datasources: []coredatasource.Name{"history"},
	}), nil)

	result := NewWithSemantic(registry, nil).search(ctx, searchInput{
		Query:   "hello",
		Mode:    "semantic",
		Filters: map[string]string{"type": "session.operation"},
	})
	if result.Status != operation.StatusOK {
		t.Fatalf("result = %#v", result)
	}
	rendered := result.Output.(operation.Rendered)
	out := rendered.Data.(searchOutput)
	if len(out.Errors) != 0 {
		t.Fatalf("expected lexical search without semantic errors, got %#v", out.Errors)
	}
	if accessor.searches != 1 {
		t.Fatalf("searches = %d, want 1", accessor.searches)
	}
	if accessor.filters["type"] != "session.operation" {
		t.Fatalf("filters = %#v", accessor.filters)
	}
}

func TestRelationReturnsExactRelatedRecords(t *testing.T) {
	registry, err := coredatasource.NewRegistry([]coredatasource.Accessor{
		memoryAccessor{
			spec: coredatasource.Spec{Name: "slack", Entities: []coredatasource.EntityType{"slack.channel"}, Kind: "memory"},
			entity: coredatasource.EntitySpec{
				Type: "slack.channel",
				Relations: []coredatasource.RelationSpec{{
					Name:         "members",
					TargetEntity: "slack.user",
					Exact:        true,
				}},
			},
			relationResult: coredatasource.RelationResult{
				TargetEntity: "slack.user",
				Records: []coredatasource.Record{{
					ID:         "U1",
					Datasource: "slack",
					Entity:     "slack.user",
					Title:      "Alice",
				}},
				Complete: true,
				Exact:    true,
			},
		},
	}, nil)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	ctx := operation.NewContext(coredatasource.ContextWithAccessPolicy(context.Background(), coredatasource.AccessPolicy{Datasources: []coredatasource.Name{"slack"}}), nil)
	result := New(registry).relation(ctx, relationInput{Datasource: "slack", Entity: "slack.channel", ID: "C1", Relation: "members"})
	if result.Status != operation.StatusOK {
		t.Fatalf("result = %#v", result)
	}
	rendered := result.Output.(operation.Rendered)
	out := rendered.Data.(relationOutput)
	if !out.Result.Exact || !out.Result.Complete || len(out.Result.Records) != 1 {
		t.Fatalf("relation output = %#v, want exact complete record", out.Result)
	}
	if !strings.Contains(rendered.Text, "exact") || !strings.Contains(rendered.Text, "complete") {
		t.Fatalf("text = %q, want exact/complete labels", rendered.Text)
	}
}

func TestRelationEnforcesDatasourceAccess(t *testing.T) {
	registry, err := coredatasource.NewRegistry([]coredatasource.Accessor{
		memoryAccessor{
			spec: coredatasource.Spec{Name: "slack", Entities: []coredatasource.EntityType{"slack.channel"}, Kind: "memory"},
			entity: coredatasource.EntitySpec{Type: "slack.channel", Relations: []coredatasource.RelationSpec{{
				Name:         "members",
				TargetEntity: "slack.user",
			}}},
		},
	}, nil)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	result := New(registry).relation(operation.NewContext(context.Background(), nil), relationInput{Datasource: "slack", Entity: "slack.channel", ID: "C1", Relation: "members"})
	if result.Status != operation.StatusFailed || result.Error == nil || result.Error.Code != "datasource_relation_denied" {
		t.Fatalf("result = %#v, want access denied", result)
	}
}

func TestBatchGetPreservesRequestedIDsAndReportsMisses(t *testing.T) {
	registry, err := coredatasource.NewRegistry([]coredatasource.Accessor{
		memoryAccessor{
			spec:   coredatasource.Spec{Name: "slack", Entities: []coredatasource.EntityType{"slack.user"}, Kind: "memory"},
			entity: coredatasource.EntitySpec{Type: "slack.user"},
			records: []coredatasource.Record{
				{ID: "U2", Datasource: "slack", Entity: "slack.user", Title: "Bob"},
				{ID: "U1", Datasource: "slack", Entity: "slack.user", Title: "Alice"},
			},
		},
	}, nil)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	ctx := operation.NewContext(coredatasource.ContextWithAccessPolicy(context.Background(), coredatasource.AccessPolicy{Datasources: []coredatasource.Name{"slack"}}), nil)
	result := New(registry).batchGet(ctx, batchGetInput{Datasource: "slack", Entity: "slack.user", IDs: []string{"U1", "missing", "U2"}})
	if result.Status != operation.StatusOK {
		t.Fatalf("result = %#v", result)
	}
	out := result.Output.(operation.Rendered).Data.(batchGetOutput)
	if len(out.Result.Records) != 2 || out.Result.Records[0].ID != "U1" || out.Result.Records[1].ID != "U2" || len(out.Result.Errors) != 1 {
		t.Fatalf("batch output = %#v, want ordered hits and one miss", out.Result)
	}
}

func TestCatalogProviderListsOnlyAllowedDatasources(t *testing.T) {
	registry, err := coredatasource.NewRegistry([]coredatasource.Accessor{
		memoryAccessor{
			spec:   coredatasource.Spec{Name: "docs", Entities: []coredatasource.EntityType{"file.document"}, Kind: "memory"},
			entity: coredatasource.EntitySpec{Type: "file.document", Description: "Local document."},
		},
		memoryAccessor{
			spec:   coredatasource.Spec{Name: "jira", Entities: []coredatasource.EntityType{"jira.issue"}, Kind: "memory"},
			entity: coredatasource.EntitySpec{Type: "jira.issue", Description: "Jira issue."},
		},
	}, nil)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	provider := catalogProvider{registry: registry}
	blocks, err := provider.Build(coredatasource.ContextWithAccessPolicy(context.Background(), coredatasource.AccessPolicy{
		Datasources: []coredatasource.Name{"jira"},
	}), corecontext.Request{})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(blocks) != 1 || !strings.Contains(blocks[0].Content, "jira.issue") || strings.Contains(blocks[0].Content, "file.document") {
		t.Fatalf("blocks = %#v", blocks)
	}
}

func TestCatalogProviderListsEntityCapabilities(t *testing.T) {
	registry, err := coredatasource.NewRegistry([]coredatasource.Accessor{
		memoryAccessor{
			spec: coredatasource.Spec{Name: "slack", Entities: []coredatasource.EntityType{"slack.message"}, Kind: "memory"},
			entity: coredatasource.EntitySpec{
				Type:         "slack.message",
				Description:  "Slack message.",
				Capabilities: []coredatasource.EntityCapability{coredatasource.EntityCapabilitySearch},
			},
		},
	}, nil)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	provider := catalogProvider{registry: registry}
	blocks, err := provider.Build(coredatasource.ContextWithAccessPolicy(context.Background(), coredatasource.AccessPolicy{
		Datasources: []coredatasource.Name{"slack"},
	}), corecontext.Request{})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(blocks) != 1 || !strings.Contains(blocks[0].Content, "slack.message [search]") || strings.Contains(blocks[0].Content, "get") {
		t.Fatalf("blocks = %#v", blocks)
	}
	if !strings.Contains(blocks[0].Metadata["datasources"], `"capabilities":["search"]`) {
		t.Fatalf("metadata = %#v, want search capability", blocks[0].Metadata)
	}
}

func TestCatalogProviderListsRelations(t *testing.T) {
	registry, err := coredatasource.NewRegistry([]coredatasource.Accessor{
		memoryAccessor{
			spec: coredatasource.Spec{Name: "slack", Entities: []coredatasource.EntityType{"slack.channel"}, Kind: "memory"},
			entity: coredatasource.EntitySpec{
				Type: "slack.channel",
				Relations: []coredatasource.RelationSpec{{
					Name:         "members",
					TargetEntity: "slack.user",
					Exact:        true,
				}},
			},
		},
	}, nil)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	blocks, err := (catalogProvider{registry: registry}).Build(coredatasource.ContextWithAccessPolicy(context.Background(), coredatasource.AccessPolicy{
		Datasources: []coredatasource.Name{"slack"},
	}), corecontext.Request{})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(blocks) != 1 || !strings.Contains(blocks[0].Content, "relation") || !strings.Contains(blocks[0].Content, "members->slack.user exact") {
		t.Fatalf("blocks = %#v, want relation catalog entry", blocks)
	}
	if !strings.Contains(blocks[0].Metadata["datasources"], `"relations":[{"name":"members","target_entity":"slack.user","exact":true`) {
		t.Fatalf("metadata = %#v, want relation metadata", blocks[0].Metadata)
	}
}

func TestDetectedProviderListsOnlyAllowedLocalReferences(t *testing.T) {
	registry, err := coredatasource.NewRegistry([]coredatasource.Accessor{
		memoryAccessor{
			spec: coredatasource.Spec{Name: "jira", Entities: []coredatasource.EntityType{"jira.issue"}, Kind: "memory"},
			entity: coredatasource.EntitySpec{
				Type:         "jira.issue",
				Capabilities: []coredatasource.EntityCapability{coredatasource.EntityCapabilitySearch, coredatasource.EntityCapabilityGet},
				Detectors: []coredatasource.DetectorSpec{{
					Name:       "issue_key",
					Kind:       coredatasource.DetectorRegex,
					Pattern:    `\b([A-Z]+-\d+)\b`,
					IDTemplate: "$1",
				}},
			},
		},
		memoryAccessor{
			spec: coredatasource.Spec{Name: "docs", Entities: []coredatasource.EntityType{"file.document"}, Kind: "memory"},
			entity: coredatasource.EntitySpec{
				Type: "file.document",
				Detectors: []coredatasource.DetectorSpec{{
					Name:       "path",
					Kind:       coredatasource.DetectorRegex,
					Pattern:    `([a-z]+\.md)`,
					IDTemplate: "$1",
				}},
			},
		},
	}, nil)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	ctx := coredatasource.ContextWithAccessPolicy(context.Background(), coredatasource.AccessPolicy{Datasources: []coredatasource.Name{"jira"}})
	ctx = coredatasource.ContextWithDetectionInput(ctx, coredatasource.DetectionInput{Sources: []coredatasource.DetectionSource{{
		Kind: "channel.message",
		Text: "Please check DEV-381 and README.md",
	}}})
	blocks, err := (detectedProvider{registry: registry}).Build(ctx, corecontext.Request{})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(blocks) != 1 || !strings.Contains(blocks[0].Content, "jira.issue DEV-381") || strings.Contains(blocks[0].Content, "README.md") {
		t.Fatalf("blocks = %#v", blocks)
	}
	if !strings.Contains(blocks[0].Content, "[get,search]") {
		t.Fatalf("content = %q, want capabilities", blocks[0].Content)
	}
}

func TestDetectedProviderDoesNotCallDatasourceIO(t *testing.T) {
	accessor := &countingAccessor{
		memoryAccessor: memoryAccessor{
			spec: coredatasource.Spec{Name: "jira", Entities: []coredatasource.EntityType{"jira.issue"}, Kind: "memory"},
			entity: coredatasource.EntitySpec{
				Type: "jira.issue",
				Detectors: []coredatasource.DetectorSpec{{
					Name:       "issue_key",
					Kind:       coredatasource.DetectorRegex,
					Pattern:    `\b([A-Z]+-\d+)\b`,
					IDTemplate: "$1",
				}},
			},
		},
	}
	registry, err := coredatasource.NewRegistry([]coredatasource.Accessor{accessor}, nil)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	ctx := coredatasource.ContextWithAccessPolicy(context.Background(), coredatasource.AccessPolicy{Datasources: []coredatasource.Name{"jira"}})
	ctx = coredatasource.ContextWithDetectionInput(ctx, coredatasource.DetectionInput{Sources: []coredatasource.DetectionSource{{Text: "DEV-381"}}})
	_, err = (detectedProvider{registry: registry}).Build(ctx, corecontext.Request{})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if accessor.searches != 0 || accessor.gets != 0 {
		t.Fatalf("datasource IO calls = search %d get %d, want zero", accessor.searches, accessor.gets)
	}
}

func TestSearchAddsRecordCrosslinksWithoutAutoFetch(t *testing.T) {
	jira := &countingAccessor{
		memoryAccessor: memoryAccessor{
			spec: coredatasource.Spec{Name: "jira", Entities: []coredatasource.EntityType{"jira.issue"}, Kind: "memory"},
			entity: coredatasource.EntitySpec{
				Type: "jira.issue",
				Detectors: []coredatasource.DetectorSpec{{
					Name:       "issue_key",
					Kind:       coredatasource.DetectorRegex,
					Pattern:    `\b([A-Z]+-\d+)\b`,
					IDTemplate: "$1",
				}},
			},
		},
	}
	docs := &countingAccessor{
		memoryAccessor: memoryAccessor{
			spec:   coredatasource.Spec{Name: "docs", Entities: []coredatasource.EntityType{"file.document"}, Kind: "memory"},
			entity: coredatasource.EntitySpec{Type: "file.document"},
			records: []coredatasource.Record{{
				ID:         "README.md",
				Datasource: "docs",
				Entity:     "file.document",
				Title:      "readme",
				Content:    "See DEV-381 for context.",
			}},
		},
	}
	registry, err := coredatasource.NewRegistry([]coredatasource.Accessor{jira, docs}, nil)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	ctx := operation.NewContext(coredatasource.ContextWithAccessPolicy(context.Background(), coredatasource.AccessPolicy{Datasources: []coredatasource.Name{"jira", "docs"}}), nil)
	result := New(registry).search(ctx, searchInput{Query: "readme", Entities: []string{"file.document"}})
	if result.Status != operation.StatusOK {
		t.Fatalf("result = %#v", result)
	}
	out := result.Output.(operation.Rendered).Data.(searchOutput)
	if len(out.Results) != 1 || len(out.Results[0].Records) != 1 || len(out.Results[0].Records[0].Links) != 1 {
		t.Fatalf("results = %#v, want linked record", out.Results)
	}
	link := out.Results[0].Records[0].Links[0]
	if link.Datasource != "jira" || link.Entity != "jira.issue" || link.ID != "DEV-381" {
		t.Fatalf("link = %#v, want jira issue", link)
	}
	if jira.searches != 0 || jira.gets != 0 {
		t.Fatalf("jira IO calls = search %d get %d, want zero", jira.searches, jira.gets)
	}
}

func TestGetReturnsRecordForAllowedDatasource(t *testing.T) {
	registry, err := coredatasource.NewRegistry([]coredatasource.Accessor{
		memoryAccessor{
			spec:    coredatasource.Spec{Name: "docs", Entities: []coredatasource.EntityType{"file.document"}, Kind: "memory"},
			entity:  coredatasource.EntitySpec{Type: "file.document"},
			records: []coredatasource.Record{{ID: "one", Datasource: "docs", Entity: "file.document", Title: "hello"}},
		},
	}, nil)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	ctx := operation.NewContext(coredatasource.ContextWithAccessPolicy(context.Background(), coredatasource.AccessPolicy{Datasources: []coredatasource.Name{"docs"}}), nil)
	result := New(registry).get(ctx, getInput{Datasource: "docs", Entity: "file.document", ID: "one"})
	if result.Status != operation.StatusOK {
		t.Fatalf("result = %#v", result)
	}
}

type countingAccessor struct {
	memoryAccessor
	searches int
	gets     int
}

func (a *countingAccessor) Search(ctx context.Context, req coredatasource.SearchRequest) (coredatasource.SearchResult, error) {
	a.searches++
	return a.memoryAccessor.Search(ctx, req)
}

func (a *countingAccessor) Get(ctx context.Context, req coredatasource.GetRequest) (coredatasource.Record, error) {
	a.gets++
	return a.memoryAccessor.Get(ctx, req)
}

type filterCaptureAccessor struct {
	memoryAccessor
	searches int
	filters  map[string]string
}

func (a *filterCaptureAccessor) Search(ctx context.Context, req coredatasource.SearchRequest) (coredatasource.SearchResult, error) {
	a.searches++
	a.filters = req.Filters
	return a.memoryAccessor.Search(ctx, req)
}

type indexAwareProvider struct {
	entity coredatasource.EntitySpec
	index  *semantic.Index
}

func (p indexAwareProvider) Entities() []coredatasource.EntitySpec {
	return []coredatasource.EntitySpec{p.entity}
}

func (p indexAwareProvider) WithSemanticIndex(index *semantic.Index) coredatasource.Provider {
	p.index = index
	return p
}

func (p indexAwareProvider) Open(context.Context, coredatasource.Spec) (coredatasource.Accessor, error) {
	return indexAwareAccessor{
		memoryAccessor: memoryAccessor{
			spec:   coredatasource.Spec{Name: "docs", Kind: "memory", Entities: []coredatasource.EntityType{"file.document"}},
			entity: p.entity,
		},
		index: p.index,
	}, nil
}

type indexAwareAccessor struct {
	memoryAccessor
	index *semantic.Index
}
