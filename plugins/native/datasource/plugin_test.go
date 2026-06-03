package datasource

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"github.com/fluxplane/fluxplane-core/core/activation"
	coredata "github.com/fluxplane/fluxplane-core/core/data"
	coreevidence "github.com/fluxplane/fluxplane-core/core/evidence"
	"github.com/fluxplane/fluxplane-core/orchestration/pluginhost"
	corecontext "github.com/fluxplane/fluxplane-core/runtime/context"
	runtimedata "github.com/fluxplane/fluxplane-core/runtime/data"
	"github.com/fluxplane/fluxplane-core/runtime/datasource/semantic"
	coredatasource "github.com/fluxplane/fluxplane-datasource"
	"github.com/fluxplane/fluxplane-operation"
)

func TestStringFilterMapAcceptsScalarJSONValues(t *testing.T) {
	var filters stringFilterMap
	if err := json.Unmarshal([]byte(`{"merge_request_iid":2553,"archived":false,"project_id":"sbf/services","empty":null}`), &filters); err != nil {
		t.Fatalf("UnmarshalJSON: %v", err)
	}
	if filters["merge_request_iid"] != "2553" || filters["archived"] != "false" || filters["project_id"] != "sbf/services" {
		t.Fatalf("filters = %#v, want scalar values normalized to strings", filters)
	}
	if _, ok := filters["empty"]; ok {
		t.Fatalf("filters = %#v, want null filter omitted", filters)
	}
}

func TestSearchAllowsFilterOnlyRequestWithExplicitEntities(t *testing.T) {
	registry, err := coredatasource.NewRegistry([]coredatasource.Accessor{memoryAccessor{
		spec:   coredatasource.Spec{Name: "gitlab", Kind: "gitlab", Entities: []coredatasource.EntityType{"gitlab.discussion"}},
		entity: coredatasource.EntitySpec{Type: "gitlab.discussion"},
		records: []coredatasource.Record{{
			ID:         "sbf/services!2553!discussion-1",
			Datasource: "gitlab",
			Entity:     "gitlab.discussion",
			Title:      "discussion-1",
		}},
	}}, nil)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	ctx := operation.NewContext(coredatasource.ContextWithAccessPolicy(context.Background(), coredatasource.AccessPolicy{
		Datasources: []coredatasource.Name{"gitlab"},
	}), nil)
	result := New(registry).search(ctx, searchInput{
		Entities: []string{"gitlab.discussion"},
		Filters:  stringFilterMap{"project_id": "sbf/services", "merge_request_iid": "2553"},
	})
	if result.Status != operation.StatusOK {
		t.Fatalf("search status = %s error=%#v, want ok", result.Status, result.Error)
	}
}

func TestConfigSchemaContributionsExposeCatalogDatasource(t *testing.T) {
	bundle, err := New(nil).ConfigSchemaContributions(context.Background(), pluginhost.Context{})
	if err != nil {
		t.Fatalf("ConfigSchemaContributions: %v", err)
	}
	if len(bundle.Datasources) != 1 {
		t.Fatalf("datasources = %#v, want catalog datasource", bundle.Datasources)
	}
	spec := bundle.Datasources[0]
	if spec.Name != coredatasource.Name(Name) || spec.Kind != "synthetic" {
		t.Fatalf("datasource spec = %#v, want synthetic datasource catalog", spec)
	}
}

func TestContributionsExposeDefaultActivationSet(t *testing.T) {
	bundle, err := New(nil).Contributions(context.Background(), pluginhost.Context{})
	if err != nil {
		t.Fatalf("Contributions: %v", err)
	}
	if len(bundle.ActivationSets) != 1 || bundle.ActivationSets[0].Name != Name {
		t.Fatalf("activation sets = %#v, want datasource set", bundle.ActivationSets)
	}
	if bundle.ActivationSets[0].Annotations[activation.AnnotationIncludeConfiguredDatasources] != "true" {
		t.Fatalf("activation set annotations = %#v, want configured datasource annotation", bundle.ActivationSets[0].Annotations)
	}
	if len(bundle.OperationSets) != 1 || bundle.OperationSets[0].Name != Name {
		t.Fatalf("operation sets = %#v, want datasource operation set", bundle.OperationSets)
	}
}

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

func (a memoryAccessor) List(_ context.Context, req coredatasource.ListRequest) (coredatasource.ListResult, error) {
	limit := req.Limit
	if limit <= 0 {
		limit = defaultSearchLimit
	}
	offset := 0
	if req.Cursor != "" {
		parsed, err := strconv.Atoi(req.Cursor)
		if err != nil {
			return coredatasource.ListResult{}, err
		}
		offset = parsed
	}
	if offset > len(a.records) {
		offset = len(a.records)
	}
	end := offset + limit
	if end > len(a.records) {
		end = len(a.records)
	}
	next := ""
	if end < len(a.records) {
		next = strconv.Itoa(end)
	}
	return coredatasource.ListResult{
		Datasource: a.spec.Name,
		Entity:     req.Entity,
		Records:    append([]coredatasource.Record(nil), a.records[offset:end]...),
		Total:      len(a.records),
		NextCursor: next,
		Complete:   next == "",
	}, nil
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

func TestSearchRendersKeyRecordMetadata(t *testing.T) {
	registry, err := coredatasource.NewRegistry([]coredatasource.Accessor{
		memoryAccessor{
			spec:   coredatasource.Spec{Name: "gitlab", Entities: []coredatasource.EntityType{"gitlab.project"}, Kind: "memory"},
			entity: coredatasource.EntitySpec{Type: "gitlab.project"},
			records: []coredatasource.Record{{
				ID:         "sbf/services",
				Datasource: "gitlab",
				Entity:     "gitlab.project",
				Title:      "sbf/services",
				Metadata: map[string]string{
					"id":                  "12",
					"path_with_namespace": "sbf/services",
				},
			}},
		},
	}, nil)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	ctx := operation.NewContext(coredatasource.ContextWithAccessPolicy(context.Background(), coredatasource.AccessPolicy{
		Datasources: []coredatasource.Name{"gitlab"},
	}), nil)

	result := New(registry).search(ctx, searchInput{Query: "sbf/services", Entities: []string{"gitlab.project"}})
	if result.Status != operation.StatusOK {
		t.Fatalf("result = %#v", result)
	}
	rendered := result.Output.(operation.Rendered)
	if !strings.Contains(rendered.Text, "project_id=12") {
		t.Fatalf("rendered text = %q, want project id metadata", rendered.Text)
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

func TestListReturnsAllowedDatasourceRecords(t *testing.T) {
	registry, err := coredatasource.NewRegistry([]coredatasource.Accessor{
		memoryAccessor{
			spec: coredatasource.Spec{Name: "gitlab", Entities: []coredatasource.EntityType{"gitlab.project"}, Kind: "memory"},
			entity: coredatasource.EntitySpec{
				Type:         "gitlab.project",
				Capabilities: []coredatasource.EntityCapability{coredatasource.EntityCapabilityList},
			},
			records: []coredatasource.Record{
				{ID: "runtime", Datasource: "gitlab", Entity: "gitlab.project", Title: "Runtime"},
				{ID: "agents", Datasource: "gitlab", Entity: "gitlab.project", Title: "Agents"},
			},
		},
	}, nil)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	ctx := operation.NewContext(coredatasource.ContextWithAccessPolicy(context.Background(), coredatasource.AccessPolicy{Datasources: []coredatasource.Name{"gitlab"}}), nil)
	result := New(registry).list(ctx, listInput{Datasource: "gitlab", Entity: "gitlab.project", Limit: 1})
	if result.Status != operation.StatusOK {
		t.Fatalf("result = %#v", result)
	}
	rendered := result.Output.(operation.Rendered)
	out := rendered.Data.(listOutput)
	if len(out.Result.Records) != 1 || out.Result.Records[0].ID != "runtime" || out.Result.NextCursor != "1" || out.Result.Complete {
		t.Fatalf("list output = %#v, want first page with next cursor", out.Result)
	}
	if !strings.Contains(rendered.Text, "next_cursor: 1") {
		t.Fatalf("rendered text = %q, want next cursor", rendered.Text)
	}
}

func TestSearchDataStoreHidesArchivedGitLabProjectsByDefault(t *testing.T) {
	registry, err := coredatasource.NewRegistry([]coredatasource.Accessor{
		memoryAccessor{
			spec: coredatasource.Spec{Name: "gitlab", Entities: []coredatasource.EntityType{"gitlab.project"}, Kind: "memory"},
			entity: coredatasource.EntitySpec{
				Type:         "gitlab.project",
				Capabilities: []coredatasource.EntityCapability{coredatasource.EntityCapabilitySearch},
			},
		},
	}, nil)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	store := runtimedata.NewMemoryStore()
	if err := store.UpsertRecords(context.Background(),
		gitlabDataRecord("gitlab.project", "12", "Runtime", map[string][]string{"archived": {"false"}}),
		gitlabDataRecord("gitlab.project", "13", "Old Runtime", map[string][]string{"archived": {"true"}}),
	); err != nil {
		t.Fatalf("UpsertRecords: %v", err)
	}
	ctx := operation.NewContext(coredatasource.ContextWithAccessPolicy(context.Background(), coredatasource.AccessPolicy{Datasources: []coredatasource.Name{"gitlab"}}), nil)
	plugin := NewWithDataStore(registry, store)

	defaults := plugin.search(ctx, searchInput{Query: "Runtime", Entities: []string{"gitlab.project"}})
	if defaults.Status != operation.StatusOK {
		t.Fatalf("default search = %#v", defaults)
	}
	defaultOut := defaults.Output.(operation.Rendered).Data.(searchOutput)
	if len(defaultOut.Results) != 1 || len(defaultOut.Results[0].Records) != 1 || defaultOut.Results[0].Records[0].ID != "12" {
		t.Fatalf("default records = %#v, want active project only", defaultOut.Results)
	}

	archived := plugin.search(ctx, searchInput{Query: "Runtime", Entities: []string{"gitlab.project"}, Filters: map[string]string{"archived": "true"}})
	if archived.Status != operation.StatusOK {
		t.Fatalf("archived search = %#v", archived)
	}
	archivedOut := archived.Output.(operation.Rendered).Data.(searchOutput)
	if len(archivedOut.Results) != 1 || len(archivedOut.Results[0].Records) != 1 || archivedOut.Results[0].Records[0].ID != "13" {
		t.Fatalf("archived records = %#v, want archived project", archivedOut.Results)
	}
}

func TestListDataStoreHidesArchivedGitLabMembershipsByDefault(t *testing.T) {
	registry, err := coredatasource.NewRegistry([]coredatasource.Accessor{
		memoryAccessor{
			spec: coredatasource.Spec{Name: "gitlab", Entities: []coredatasource.EntityType{"gitlab.user_membership"}, Kind: "memory"},
			entity: coredatasource.EntitySpec{
				Type:         "gitlab.user_membership",
				Capabilities: []coredatasource.EntityCapability{coredatasource.EntityCapabilityList},
			},
		},
	}, nil)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	store := runtimedata.NewMemoryStore()
	if err := store.UpsertRecords(context.Background(),
		gitlabDataRecord("gitlab.user_membership", "42:project:12", "Runtime membership", map[string][]string{"user_id": {"42"}, "source_archived": {"false"}}),
		gitlabDataRecord("gitlab.user_membership", "42:project:13", "Old Runtime membership", map[string][]string{"user_id": {"42"}, "source_archived": {"true"}}),
	); err != nil {
		t.Fatalf("UpsertRecords: %v", err)
	}
	ctx := operation.NewContext(coredatasource.ContextWithAccessPolicy(context.Background(), coredatasource.AccessPolicy{Datasources: []coredatasource.Name{"gitlab"}}), nil)
	plugin := NewWithDataStore(registry, store)

	defaults := plugin.list(ctx, listInput{Datasource: "gitlab", Entity: "gitlab.user_membership", Filters: map[string]string{"user_id": "42"}})
	if defaults.Status != operation.StatusOK {
		t.Fatalf("default list = %#v", defaults)
	}
	defaultOut := defaults.Output.(operation.Rendered).Data.(listOutput)
	if len(defaultOut.Result.Records) != 1 || defaultOut.Result.Records[0].ID != "42:project:12" {
		t.Fatalf("default records = %#v, want active membership only", defaultOut.Result.Records)
	}

	archived := plugin.list(ctx, listInput{Datasource: "gitlab", Entity: "gitlab.user_membership", Filters: map[string]string{"user_id": "42", "source_archived": "true"}})
	if archived.Status != operation.StatusOK {
		t.Fatalf("archived list = %#v", archived)
	}
	archivedOut := archived.Output.(operation.Rendered).Data.(listOutput)
	if len(archivedOut.Result.Records) != 1 || archivedOut.Result.Records[0].ID != "42:project:13" {
		t.Fatalf("archived records = %#v, want archived membership", archivedOut.Result.Records)
	}
}

func TestSearchDataStoreHidesArchivedGitLabMembershipsByDefault(t *testing.T) {
	registry, err := coredatasource.NewRegistry([]coredatasource.Accessor{
		memoryAccessor{
			spec: coredatasource.Spec{Name: "gitlab", Entities: []coredatasource.EntityType{"gitlab.user_membership"}, Kind: "memory"},
			entity: coredatasource.EntitySpec{
				Type:         "gitlab.user_membership",
				Capabilities: []coredatasource.EntityCapability{coredatasource.EntityCapabilitySearch},
			},
		},
	}, nil)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	store := runtimedata.NewMemoryStore()
	if err := store.UpsertRecords(context.Background(),
		gitlabDataRecord("gitlab.user_membership", "42:project:12", "Runtime membership", map[string][]string{"source_archived": {"false"}}),
		gitlabDataRecord("gitlab.user_membership", "42:project:13", "Old Runtime membership", map[string][]string{"source_archived": {"true"}}),
	); err != nil {
		t.Fatalf("UpsertRecords: %v", err)
	}
	ctx := operation.NewContext(coredatasource.ContextWithAccessPolicy(context.Background(), coredatasource.AccessPolicy{Datasources: []coredatasource.Name{"gitlab"}}), nil)
	plugin := NewWithDataStore(registry, store)

	defaults := plugin.search(ctx, searchInput{Query: "Runtime", Entities: []string{"gitlab.user_membership"}})
	if defaults.Status != operation.StatusOK {
		t.Fatalf("default search = %#v", defaults)
	}
	defaultOut := defaults.Output.(operation.Rendered).Data.(searchOutput)
	if len(defaultOut.Results) != 1 || len(defaultOut.Results[0].Records) != 1 || defaultOut.Results[0].Records[0].ID != "42:project:12" {
		t.Fatalf("default records = %#v, want active membership only", defaultOut.Results)
	}

	archived := plugin.search(ctx, searchInput{Query: "Runtime", Entities: []string{"gitlab.user_membership"}, Filters: map[string]string{"source_archived": "true"}})
	if archived.Status != operation.StatusOK {
		t.Fatalf("archived search = %#v", archived)
	}
	archivedOut := archived.Output.(operation.Rendered).Data.(searchOutput)
	if len(archivedOut.Results) != 1 || len(archivedOut.Results[0].Records) != 1 || archivedOut.Results[0].Records[0].ID != "42:project:13" {
		t.Fatalf("archived records = %#v, want archived membership", archivedOut.Results)
	}
}

func TestListEnforcesAgentDatasourceAccess(t *testing.T) {
	registry, err := coredatasource.NewRegistry([]coredatasource.Accessor{
		memoryAccessor{
			spec: coredatasource.Spec{Name: "gitlab", Entities: []coredatasource.EntityType{"gitlab.project"}, Kind: "memory"},
			entity: coredatasource.EntitySpec{
				Type:         "gitlab.project",
				Capabilities: []coredatasource.EntityCapability{coredatasource.EntityCapabilityList},
			},
		},
	}, nil)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	result := New(registry).list(operation.NewContext(context.Background(), nil), listInput{Datasource: "gitlab", Entity: "gitlab.project"})
	if result.Status != operation.StatusFailed || result.Error == nil || result.Error.Code != "datasource_list_denied" {
		t.Fatalf("result = %#v, want access denied", result)
	}
}

func TestListRejectsUnsupportedEntity(t *testing.T) {
	registry, err := coredatasource.NewRegistry([]coredatasource.Accessor{
		memoryAccessor{
			spec: coredatasource.Spec{Name: "gitlab", Entities: []coredatasource.EntityType{"gitlab.merge_request"}, Kind: "memory"},
			entity: coredatasource.EntitySpec{
				Type:         "gitlab.merge_request",
				Capabilities: []coredatasource.EntityCapability{coredatasource.EntityCapabilitySearch},
			},
		},
	}, nil)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	ctx := operation.NewContext(coredatasource.ContextWithAccessPolicy(context.Background(), coredatasource.AccessPolicy{Datasources: []coredatasource.Name{"gitlab"}}), nil)
	result := New(registry).list(ctx, listInput{Datasource: "gitlab", Entity: "gitlab.merge_request"})
	if result.Status != operation.StatusFailed || result.Error == nil || result.Error.Code != "datasource_list_unsupported" {
		t.Fatalf("result = %#v, want unsupported list", result)
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

func TestSyntheticDatasourceListsVisibleSources(t *testing.T) {
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
	ctx := operation.NewContext(coredatasource.ContextWithAccessPolicy(context.Background(), coredatasource.AccessPolicy{
		Datasources: []coredatasource.Name{"datasource", "jira"},
	}), nil)
	result := New(registry).list(ctx, listInput{Datasource: "datasource", Entity: string(CatalogSourceEntity)})
	if result.Status != operation.StatusOK {
		t.Fatalf("result = %#v", result)
	}
	out := result.Output.(operation.Rendered).Data.(listOutput)
	if len(out.Result.Records) != 1 || out.Result.Records[0].ID != "jira" {
		t.Fatalf("records = %#v, want only jira catalog source", out.Result.Records)
	}
}

func TestSyntheticDatasourceSearchesEntitySchemas(t *testing.T) {
	registry, err := coredatasource.NewRegistry([]coredatasource.Accessor{
		memoryAccessor{
			spec: coredatasource.Spec{Name: "slack", Entities: []coredatasource.EntityType{"slack.channel"}, Kind: "memory"},
			entity: coredatasource.EntitySpec{
				Type:        "slack.channel",
				Description: "Slack channel.",
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
	ctx := operation.NewContext(coredatasource.ContextWithAccessPolicy(context.Background(), coredatasource.AccessPolicy{
		Datasources: []coredatasource.Name{"datasource", "slack"},
	}), nil)
	result := New(registry).search(ctx, searchInput{Query: "members", Entities: []string{string(CatalogEntityEntity)}})
	if result.Status != operation.StatusOK {
		t.Fatalf("result = %#v", result)
	}
	out := result.Output.(operation.Rendered).Data.(searchOutput)
	if len(out.Results) != 1 || len(out.Results[0].Records) != 1 || out.Results[0].Records[0].ID != "slack/slack.channel" {
		t.Fatalf("results = %#v, want slack.channel catalog entity", out.Results)
	}
}

func TestSyntheticDatasourceListsMaterializedViews(t *testing.T) {
	registry, err := coredatasource.NewRegistry([]coredatasource.Accessor{
		memoryAccessor{
			spec:   coredatasource.Spec{Name: "gitlab-main", Entities: []coredatasource.EntityType{"gitlab.user"}, Kind: "gitlab"},
			entity: coredatasource.EntitySpec{Type: "gitlab.user", Description: "GitLab user."},
		},
	}, nil)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	source := coredata.SourceSpec{
		Name: "gitlab",
		Kind: "gitlab",
		Views: []coredata.ViewSpec{{
			Name:        "gitlab.user_with_groups",
			Entity:      "gitlab.user",
			Source:      "gitlab.user",
			Description: "Users with group summaries.",
			Includes:    []coredata.RelationIncludeSpec{{Relation: "groups", Target: "gitlab.group", Fields: []string{"id", "full_path"}}},
			QueryHints:  []coredata.QueryHint{coredata.QuerySearch, coredata.QueryRelation},
		}},
	}
	ctx := operation.NewContext(coredatasource.ContextWithAccessPolicy(context.Background(), coredatasource.AccessPolicy{
		Datasources: []coredatasource.Name{"datasource", "gitlab-main"},
	}), nil)
	result := NewWithDataStore(registry, nil, source).list(ctx, listInput{Datasource: "datasource", Entity: string(CatalogViewEntity)})
	if result.Status != operation.StatusOK {
		t.Fatalf("result = %#v", result)
	}
	out := result.Output.(operation.Rendered).Data.(listOutput)
	if len(out.Result.Records) != 1 || out.Result.Records[0].ID != "gitlab-main/gitlab.user_with_groups" {
		t.Fatalf("records = %#v, want gitlab user_with_groups view", out.Result.Records)
	}
	if out.Result.Records[0].Metadata["includes"] != "groups->gitlab.group" || out.Result.Records[0].Metadata["query_hints"] != "relation,search" {
		t.Fatalf("metadata = %#v, want view includes/query hints", out.Result.Records[0].Metadata)
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

func TestSemanticContextProviderReturnsNoBlockWithoutBuiltIndex(t *testing.T) {
	index, err := semantic.New(semantic.HashEmbedder{}, semantic.NewJSONStore(""), semantic.Config{})
	if err != nil {
		t.Fatalf("semantic.New: %v", err)
	}
	registry := semanticContextRegistry(t)
	ctx := coredatasource.ContextWithAccessPolicy(context.Background(), coredatasource.AccessPolicy{Datasources: []coredatasource.Name{"docs"}})

	blocks, err := (semanticContextProvider{registry: registry, index: index}).Build(ctx, corecontext.Request{InputText: "deploy"})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(blocks) != 0 {
		t.Fatalf("blocks = %#v, want none for empty semantic index", blocks)
	}
}

func TestSemanticContextProviderSearchesAllowedSemanticEntities(t *testing.T) {
	index, err := semantic.New(semantic.HashEmbedder{}, semantic.NewJSONStore(""), semantic.Config{})
	if err != nil {
		t.Fatalf("semantic.New: %v", err)
	}
	doc := coredatasource.CorpusDocument{
		Ref:   coredatasource.RecordRef{Datasource: "docs", Entity: "file.document", ID: "runbook.md"},
		Title: "Deploy Runbook",
		Body:  "Restart workers after deploying the queue consumer.",
		URL:   "file://runbook.md",
	}
	if _, err := index.Update(context.Background(), doc); err != nil {
		t.Fatalf("Update: %v", err)
	}
	registry := semanticContextRegistry(t)
	ctx := coredatasource.ContextWithAccessPolicy(context.Background(), coredatasource.AccessPolicy{Datasources: []coredatasource.Name{"docs"}})

	blocks, err := (semanticContextProvider{registry: registry, index: index}).Build(ctx, corecontext.Request{InputText: "queue deploy", RecentContext: "workers are stuck"})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(blocks) != 1 {
		t.Fatalf("blocks = %#v, want one semantic context block", blocks)
	}
	content := blocks[0].Content
	for _, want := range []string{"docs/file.document runbook.md", "Deploy Runbook"} {
		if !strings.Contains(content, want) {
			t.Fatalf("content = %q, missing %q", content, want)
		}
	}
	if !strings.Contains(blocks[0].Metadata["hits"], `"datasource":"docs"`) {
		t.Fatalf("metadata = %#v, want stable hit data", blocks[0].Metadata)
	}
}

func TestSemanticContextProviderPreservesDatasourceEntityPairs(t *testing.T) {
	index, err := semantic.New(semantic.HashEmbedder{}, semantic.NewJSONStore(""), semantic.Config{})
	if err != nil {
		t.Fatalf("semantic.New: %v", err)
	}
	docs := []coredatasource.CorpusDocument{
		{Ref: coredatasource.RecordRef{Datasource: "docs", Entity: "file.document", ID: "runbook.md"}, Title: "Runbook", Body: "ordinary file"},
		{Ref: coredatasource.RecordRef{Datasource: "issues", Entity: "issue.ticket", ID: "DEV-1"}, Title: "Ticket", Body: "ordinary ticket"},
		{Ref: coredatasource.RecordRef{Datasource: "docs", Entity: "issue.ticket", ID: "rogue"}, Title: "Rogue Cross Pair", Body: "needle needle needle"},
	}
	for _, doc := range docs {
		if _, err := index.Update(context.Background(), doc); err != nil {
			t.Fatalf("Update %s/%s/%s: %v", doc.Ref.Datasource, doc.Ref.Entity, doc.Ref.ID, err)
		}
	}
	registry, err := coredatasource.NewRegistry([]coredatasource.Accessor{
		memoryAccessor{
			spec: coredatasource.Spec{Name: "docs", Kind: "memory", Entities: []coredatasource.EntityType{"file.document"}},
			entity: coredatasource.EntitySpec{
				Type:         "file.document",
				Capabilities: []coredatasource.EntityCapability{coredatasource.EntityCapabilitySemanticSearch},
			},
		},
		memoryAccessor{
			spec: coredatasource.Spec{Name: "issues", Kind: "memory", Entities: []coredatasource.EntityType{"issue.ticket"}},
			entity: coredatasource.EntitySpec{
				Type:         "issue.ticket",
				Capabilities: []coredatasource.EntityCapability{coredatasource.EntityCapabilitySemanticSearch},
			},
		},
	}, nil)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	ctx := coredatasource.ContextWithAccessPolicy(context.Background(), coredatasource.AccessPolicy{Datasources: []coredatasource.Name{"docs", "issues"}})

	blocks, err := (semanticContextProvider{registry: registry, index: index}).Build(ctx, corecontext.Request{InputText: "needle"})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(blocks) == 0 {
		return
	}
	if strings.Contains(blocks[0].Metadata["hits"], `"datasource":"docs","entity":"issue.ticket"`) {
		t.Fatalf("metadata = %#v, contains unselected datasource/entity cross-pair", blocks[0].Metadata)
	}
}

func semanticContextRegistry(t *testing.T) *coredatasource.Registry {
	t.Helper()
	registry, err := coredatasource.NewRegistry([]coredatasource.Accessor{
		memoryAccessor{
			spec: coredatasource.Spec{Name: "docs", Kind: "memory", Entities: []coredatasource.EntityType{"file.document"}},
			entity: coredatasource.EntitySpec{
				Type: "file.document",
				Capabilities: []coredatasource.EntityCapability{
					coredatasource.EntityCapabilitySearch,
					coredatasource.EntityCapabilitySemanticSearch,
				},
			},
		},
		memoryAccessor{
			spec: coredatasource.Spec{Name: "private", Kind: "memory", Entities: []coredatasource.EntityType{"file.document"}},
			entity: coredatasource.EntitySpec{
				Type:         "file.document",
				Capabilities: []coredatasource.EntityCapability{coredatasource.EntityCapabilitySemanticSearch},
			},
		},
	}, nil)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	return registry
}

func gitlabDataRecord(entity, id, title string, fields map[string][]string) coredata.Record {
	metadata := map[string]string{}
	for key, values := range fields {
		if len(values) > 0 {
			metadata[key] = values[0]
		}
	}
	return coredata.Record{
		Ref: coredata.Ref{
			Source: "gitlab",
			Entity: coredata.EntityType(entity),
			View:   coredata.ViewName(entity),
			ID:     coredata.RecordID(id),
		},
		Title:    title,
		Fields:   fields,
		Metadata: metadata,
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
	blocks, err := (detectedProvider{registry: registry}).Build(ctx, corecontext.Request{Observations: []coreevidence.Observation{{
		Kind:    "channel.message",
		Content: "Please check DEV-381 and README.md",
	}}})
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
	_, err = (detectedProvider{registry: registry}).Build(ctx, corecontext.Request{Observations: []coreevidence.Observation{{
		Kind:    "channel.message",
		Content: "DEV-381",
	}}})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if accessor.searches != 0 || accessor.gets != 0 {
		t.Fatalf("datasource IO calls = search %d get %d, want zero", accessor.searches, accessor.gets)
	}
}

func TestPrewarmProviderFetchesDetectedRecordsRelationsAndNestedRefs(t *testing.T) {
	slack := memoryAccessor{
		spec: coredatasource.Spec{Name: "slack", Entities: []coredatasource.EntityType{"slack.message"}, Kind: "memory"},
		entity: coredatasource.EntitySpec{
			Type:         "slack.message",
			Capabilities: []coredatasource.EntityCapability{coredatasource.EntityCapabilityGet, coredatasource.EntityCapabilityRelation},
			Detectors: []coredatasource.DetectorSpec{{
				Name:       "slack_message",
				Kind:       coredatasource.DetectorRegex,
				Pattern:    `SLACK-(\d+)`,
				IDTemplate: "msg-$1",
				Annotations: map[string]string{
					"prewarm.get":       "true",
					"prewarm.relations": "thread_messages",
				},
			}},
			Relations: []coredatasource.RelationSpec{{Name: "thread_messages", TargetEntity: "slack.thread_message", Exact: true}},
		},
		records: []coredatasource.Record{{
			ID:         "msg-42",
			Datasource: "slack",
			Entity:     "slack.message",
			Title:      "review request",
			Content:    "Please review https://gitlab.example.com/fluxplane/runtime/-/merge_requests/7",
		}},
		relationResult: coredatasource.RelationResult{
			TargetEntity: "slack.thread_message",
			Records: []coredatasource.Record{{
				ID:         "reply-1",
				Datasource: "slack",
				Entity:     "slack.thread_message",
				Content:    "related thread reply",
			}},
			Complete: true,
			Exact:    true,
		},
	}
	gitlab := memoryAccessor{
		spec: coredatasource.Spec{Name: "gitlab", Entities: []coredatasource.EntityType{"gitlab.merge_request"}, Kind: "memory"},
		entity: coredatasource.EntitySpec{
			Type:         "gitlab.merge_request",
			Capabilities: []coredatasource.EntityCapability{coredatasource.EntityCapabilitySearch},
			Detectors: []coredatasource.DetectorSpec{{
				Name:          "gitlab_mr",
				Kind:          coredatasource.DetectorURL,
				Pattern:       `https?://[^/\s<>"']+/([^\s<>"']+)/-/merge_requests/([0-9]+)`,
				QueryTemplate: "$1!$2",
				URLTemplate:   "$0",
				Annotations:   map[string]string{"prewarm.search": "true"},
			}},
		},
		records: []coredatasource.Record{{
			ID:         "39!7",
			Datasource: "gitlab",
			Entity:     "gitlab.merge_request",
			Title:      "fluxplane/runtime!7",
			Content:    "Fix context prewarm",
		}},
	}
	registry, err := coredatasource.NewRegistry([]coredatasource.Accessor{slack, gitlab}, nil)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	ctx := coredatasource.ContextWithAccessPolicy(context.Background(), coredatasource.AccessPolicy{Datasources: []coredatasource.Name{"slack", "gitlab"}})
	blocks, err := (prewarmProvider{registry: registry}).Build(ctx, corecontext.Request{Observations: []coreevidence.Observation{{
		Kind:    "channel.message",
		Content: "Review SLACK-42",
	}}})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(blocks) != 1 {
		t.Fatalf("blocks = %#v, want one prewarm block", blocks)
	}
	for _, want := range []string{"review request", "related thread reply", "Fix context prewarm"} {
		if !strings.Contains(blocks[0].Content, want) {
			t.Fatalf("content = %q, want %q", blocks[0].Content, want)
		}
	}
}

func TestRenderRecordUsesCanonicalURLNotAPIURLMetadata(t *testing.T) {
	text := renderRecord(coredatasource.Record{
		ID:      "DEV-380",
		Title:   "Jira issue DEV-380",
		URL:     "https://company.atlassian.net/browse/DEV-380",
		Content: "Fix datasource links",
		Metadata: map[string]string{
			"api_url": "https://api.atlassian.com/ex/jira/cloud-1/rest/api/3/issue/48997",
		},
	})
	if !strings.Contains(text, "https://company.atlassian.net/browse/DEV-380") {
		t.Fatalf("rendered text = %q, want canonical URL", text)
	}
	if strings.Contains(text, "rest/api/3") {
		t.Fatalf("rendered text = %q, must not include API URL metadata", text)
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

func TestSearchUsesDataStoreBeforeAccessor(t *testing.T) {
	accessor := &countingAccessor{
		memoryAccessor: memoryAccessor{
			spec:   coredatasource.Spec{Name: "docs", Entities: []coredatasource.EntityType{"file.document"}, Kind: "memory"},
			entity: coredatasource.EntitySpec{Type: "file.document", Capabilities: []coredatasource.EntityCapability{coredatasource.EntityCapabilitySearch}},
			records: []coredatasource.Record{{
				ID:         "live",
				Datasource: "docs",
				Entity:     "file.document",
				Title:      "live",
			}},
		},
	}
	registry, err := coredatasource.NewRegistry([]coredatasource.Accessor{accessor}, nil)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	store := runtimedata.NewMemoryStore()
	if err := store.UpsertRecords(context.Background(), coredata.Record{
		Ref:     coredata.Ref{Source: "docs", Entity: "file.document", View: "file.document", ID: "stored"},
		Title:   "stored",
		Content: "mysql mirrored document",
		Fields:  map[string][]string{"id": {"stored"}},
	}); err != nil {
		t.Fatalf("UpsertRecords: %v", err)
	}
	ctx := operation.NewContext(coredatasource.ContextWithAccessPolicy(context.Background(), coredatasource.AccessPolicy{Datasources: []coredatasource.Name{"docs"}}), nil)
	result := NewWithDataStore(registry, store).search(ctx, searchInput{Query: "mysql", Entities: []string{"file.document"}})
	if result.Status != operation.StatusOK {
		t.Fatalf("result = %#v", result)
	}
	out := result.Output.(operation.Rendered).Data.(searchOutput)
	if len(out.Results) != 1 || len(out.Results[0].Records) != 1 || out.Results[0].Records[0].ID != "stored" {
		t.Fatalf("results = %#v, want stored record", out.Results)
	}
	if accessor.searches != 0 {
		t.Fatalf("accessor searches = %d, want data-store fast path", accessor.searches)
	}
}

func TestSearchUsesMaterializedViewWithoutDuplicateBaseRecord(t *testing.T) {
	accessor := memoryAccessor{
		spec:   coredatasource.Spec{Name: "gitlab", Entities: []coredatasource.EntityType{"gitlab.user"}, Kind: "memory"},
		entity: coredatasource.EntitySpec{Type: "gitlab.user", Capabilities: []coredatasource.EntityCapability{coredatasource.EntityCapabilitySearch}},
	}
	registry, err := coredatasource.NewRegistry([]coredatasource.Accessor{accessor}, nil)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	store := runtimedata.NewMemoryStore()
	if err := store.UpsertRecords(context.Background(),
		coredata.Record{
			Ref:    coredata.Ref{Source: "gitlab", Entity: "gitlab.user", View: "gitlab.user", ID: "42"},
			Title:  "Ada",
			Fields: map[string][]string{"username": {"ada"}},
		},
		coredata.Record{
			Ref:    coredata.Ref{Source: "gitlab", Entity: "gitlab.user", View: "gitlab.user_with_groups", ID: "42"},
			Title:  "Ada",
			Fields: map[string][]string{"username": {"ada"}, "groups.full_path": {"engineering/platform"}},
			Relations: map[string][]coredata.Summary{
				"groups": {{Ref: coredata.Ref{Source: "gitlab", Entity: "gitlab.group", View: "gitlab.group", ID: "engineering/platform"}, Title: "Platform", Fields: map[string]string{"full_path": "engineering/platform"}}},
			},
		},
	); err != nil {
		t.Fatalf("UpsertRecords: %v", err)
	}
	ctx := operation.NewContext(coredatasource.ContextWithAccessPolicy(context.Background(), coredatasource.AccessPolicy{Datasources: []coredatasource.Name{"gitlab"}}), nil)
	result := NewWithDataStore(registry, store).search(ctx, searchInput{Query: "ada", Entities: []string{"gitlab.user"}})
	if result.Status != operation.StatusOK {
		t.Fatalf("result = %#v", result)
	}
	out := result.Output.(operation.Rendered).Data.(searchOutput)
	if len(out.Results) != 1 || len(out.Results[0].Records) != 1 || out.Results[0].Records[0].ID != "42" {
		t.Fatalf("results = %#v, want one deduped user", out.Results)
	}
	if out.Results[0].Records[0].Metadata["groups.full_path"] != "engineering/platform" {
		t.Fatalf("record metadata = %#v, want materialized group field", out.Results[0].Records[0].Metadata)
	}
}

func TestRelationUsesDataStoreBeforeAccessor(t *testing.T) {
	accessor := memoryAccessor{
		spec: coredatasource.Spec{Name: "gitlab", Entities: []coredatasource.EntityType{"gitlab.user"}, Kind: "memory"},
		entity: coredatasource.EntitySpec{
			Type:         "gitlab.user",
			Capabilities: []coredatasource.EntityCapability{coredatasource.EntityCapabilityRelation},
			Relations:    []coredatasource.RelationSpec{{Name: "groups", TargetEntity: "gitlab.group", Exact: true}},
		},
	}
	registry, err := coredatasource.NewRegistry([]coredatasource.Accessor{accessor}, nil)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	store := runtimedata.NewMemoryStore()
	if err := store.UpsertRelations(context.Background(), coredata.Relation{
		Source: coredata.Ref{Source: "gitlab", Entity: "gitlab.user", View: "gitlab.user", ID: "42"},
		Name:   "groups",
		Target: coredata.Ref{Source: "gitlab", Entity: "gitlab.group", View: "gitlab.group", ID: "engineering/platform"},
		Summary: coredata.Summary{
			Ref:    coredata.Ref{Source: "gitlab", Entity: "gitlab.group", View: "gitlab.group", ID: "engineering/platform"},
			Title:  "Platform",
			Fields: map[string]string{"path": "engineering/platform"},
		},
	}); err != nil {
		t.Fatalf("UpsertRelations: %v", err)
	}
	ctx := operation.NewContext(coredatasource.ContextWithAccessPolicy(context.Background(), coredatasource.AccessPolicy{Datasources: []coredatasource.Name{"gitlab"}}), nil)
	result := NewWithDataStore(registry, store).relation(ctx, relationInput{Datasource: "gitlab", Entity: "gitlab.user", ID: "42", Relation: "groups"})
	if result.Status != operation.StatusOK {
		t.Fatalf("result = %#v", result)
	}
	out := result.Output.(operation.Rendered).Data.(relationOutput)
	if len(out.Result.Records) != 1 || out.Result.Records[0].ID != "engineering/platform" {
		t.Fatalf("relation result = %#v, want data-store group", out.Result)
	}
}

func TestSearchAndGetBypassDataStoreForScopedMemoryDatasource(t *testing.T) {
	store := runtimedata.NewMemoryStore()
	if err := store.UpsertRecords(context.Background(), coredata.Record{
		Ref:     coredata.Ref{Source: "memory", Entity: "memory.item", View: "memory.item", ID: "mem-private"},
		Title:   "private memory",
		Content: "must not leak through unscoped datasource cache",
	}); err != nil {
		t.Fatalf("UpsertRecords: %v", err)
	}
	accessor := &countingAccessor{memoryAccessor: memoryAccessor{
		spec: coredatasource.Spec{Name: "memory", Entities: []coredatasource.EntityType{"memory.item"}, Kind: "memory"},
		entity: coredatasource.EntitySpec{
			Type:         "memory.item",
			Capabilities: []coredatasource.EntityCapability{coredatasource.EntityCapabilitySearch, coredatasource.EntityCapabilityGet},
		},
	}}
	registry, err := coredatasource.NewRegistry([]coredatasource.Accessor{accessor}, nil)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	ctx := operation.NewContext(coredatasource.ContextWithAccessPolicy(context.Background(), coredatasource.AccessPolicy{Datasources: []coredatasource.Name{"memory"}}), nil)

	search := NewWithDataStore(registry, store).search(ctx, searchInput{Query: "private memory", Entities: []string{"memory.item"}})
	if search.Status != operation.StatusOK {
		t.Fatalf("search = %#v, want ok from accessor", search)
	}
	if accessor.searches != 1 {
		t.Fatalf("searches = %d, want accessor search", accessor.searches)
	}
	out := search.Output.(operation.Rendered).Data.(searchOutput)
	if len(out.Results) != 1 || len(out.Results[0].Records) != 0 {
		t.Fatalf("search results = %#v, want no unscoped memory cache records", out.Results)
	}

	get := NewWithDataStore(registry, store).get(ctx, getInput{Datasource: "memory", Entity: "memory.item", ID: "mem-private"})
	if get.Status != operation.StatusFailed || accessor.gets != 1 {
		t.Fatalf("get = %#v gets=%d, want accessor miss without datastore shortcut", get, accessor.gets)
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
