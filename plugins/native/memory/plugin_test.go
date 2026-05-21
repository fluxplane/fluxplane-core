package memory

import (
	"context"
	"strings"
	"testing"

	coredata "github.com/fluxplane/engine/core/data"
	coredatasource "github.com/fluxplane/engine/core/datasource"
	corememory "github.com/fluxplane/engine/core/memory"
	"github.com/fluxplane/engine/core/operation"
	"github.com/fluxplane/engine/core/policy"
	corethread "github.com/fluxplane/engine/core/thread"
	"github.com/fluxplane/engine/core/user"
	"github.com/fluxplane/engine/orchestration/pluginhost"
	"github.com/fluxplane/engine/orchestration/sessionenv"
	runtimedata "github.com/fluxplane/engine/runtime/data"
	"github.com/fluxplane/engine/runtime/eventstore"
)

func TestPluginOperationsUseHybridMemoryStore(t *testing.T) {
	ctx := policy.ContextWithAuthorization(context.Background(), policy.AuthorizationContext{
		Subjects: []policy.SubjectRef{{Kind: policy.SubjectUser, ID: "user-a"}},
	})
	ops, err := New().Operations(ctx, pluginhost.Context{
		EventStore: eventstore.NewMemoryStore(),
		DataStore:  runtimedata.NewMemoryStore(),
	})
	if err != nil {
		t.Fatalf("Operations: %v", err)
	}
	byName := map[operation.Name]operation.Operation{}
	for _, op := range ops {
		byName[op.Spec().Ref.Name] = op
	}
	opCtx := operation.NewContext(ctx, nil)
	memorize := byName[MemorizeOp].Run(opCtx, corememory.MemorizeRequest{
		Kind:        corememory.KindPreference,
		Visibility:  corememory.VisibilityPrivateUser,
		Subjects:    []corememory.Subject{{Kind: corememory.SubjectUser, ID: "user-a"}},
		AccessScope: coredata.Scope{UserID: user.ID("user-a")},
		Content:     "Use direct answers for short questions.",
	})
	if memorize.Status != operation.StatusOK {
		t.Fatalf("memorize = %#v, want ok", memorize)
	}
	retrieve := byName[RetrieveOp].Run(opCtx, corememory.RetrieveRequest{
		AccessScope: coredata.Scope{UserID: user.ID("user-a")},
		Subjects:    []corememory.Subject{{Kind: corememory.SubjectUser, ID: "user-a"}},
	})
	if retrieve.Status != operation.StatusOK {
		t.Fatalf("retrieve = %#v, want ok", retrieve)
	}
	result, ok := retrieve.Output.(corememory.RetrieveResult)
	if !ok {
		t.Fatalf("retrieve output = %T, want RetrieveResult", retrieve.Output)
	}
	if len(result.Memories) != 1 || result.Memories[0].Content != "Use direct answers for short questions." {
		t.Fatalf("memories = %#v, want stored preference", result.Memories)
	}
}

func TestPluginOperationsRejectAccessScopeOutsideCaller(t *testing.T) {
	ctx := policy.ContextWithAuthorization(context.Background(), policy.AuthorizationContext{
		Subjects: []policy.SubjectRef{{Kind: policy.SubjectUser, ID: "user-a"}},
	})
	ops, err := New().Operations(ctx, pluginhost.Context{
		EventStore: eventstore.NewMemoryStore(),
		DataStore:  runtimedata.NewMemoryStore(),
	})
	if err != nil {
		t.Fatalf("Operations: %v", err)
	}
	byName := operationsByName(ops)
	opCtx := operation.NewContext(ctx, nil)
	result := byName[RetrieveOp].Run(opCtx, corememory.RetrieveRequest{
		AccessScope: coredata.Scope{UserID: user.ID("user-b")},
	})
	if result.Status != operation.StatusFailed {
		t.Fatalf("retrieve = %#v, want failed", result)
	}
	if result.Error == nil || result.Error.Message != "memory: access_scope is outside caller scope" {
		t.Fatalf("error = %#v, want outside caller scope", result.Error)
	}
}

func TestPluginOperationsAllowAccessScopeCoveredByCallerAndSession(t *testing.T) {
	ctx := policy.ContextWithAuthorization(context.Background(), policy.AuthorizationContext{
		Subjects: []policy.SubjectRef{{Kind: policy.SubjectUser, ID: "user-a"}},
	})
	ops, err := New().Operations(ctx, pluginhost.Context{
		EventStore: eventstore.NewMemoryStore(),
		DataStore:  runtimedata.NewMemoryStore(),
	})
	if err != nil {
		t.Fatalf("Operations: %v", err)
	}
	byName := operationsByName(ops)
	opCtx := sessionenv.OperationContext(operation.NewContext(ctx, nil), sessionenv.Config{
		Thread: corethread.Ref{ID: "thread-a"},
	}, "")
	result := byName[RetrieveOp].Run(opCtx, corememory.RetrieveRequest{
		AccessScope: coredata.Scope{UserID: user.ID("user-a"), ThreadID: "thread-a"},
	})
	if result.Status != operation.StatusOK {
		t.Fatalf("retrieve = %#v, want ok", result)
	}
}

func TestPluginOperationsDefaultAccessScopeToUserSubject(t *testing.T) {
	ctx := policy.ContextWithAuthorization(context.Background(), policy.AuthorizationContext{
		Subjects: []policy.SubjectRef{{Kind: policy.SubjectUser, ID: "user-a"}},
	})
	ops, err := New().Operations(ctx, pluginhost.Context{
		EventStore: eventstore.NewMemoryStore(),
		DataStore:  runtimedata.NewMemoryStore(),
	})
	if err != nil {
		t.Fatalf("Operations: %v", err)
	}
	byName := operationsByName(ops)
	opCtx := operation.NewContext(ctx, nil)
	memorize := byName[MemorizeOp].Run(opCtx, corememory.MemorizeRequest{
		Kind:       corememory.KindFact,
		Visibility: corememory.VisibilityPrivateUser,
		Content:    "pin is 123",
	})
	if memorize.Status != operation.StatusOK {
		t.Fatalf("memorize = %#v, want ok", memorize)
	}
	retrieve := byName[RetrieveOp].Run(opCtx, corememory.RetrieveRequest{Text: "pin"})
	if retrieve.Status != operation.StatusOK {
		t.Fatalf("retrieve = %#v, want ok", retrieve)
	}
	result, ok := retrieve.Output.(corememory.RetrieveResult)
	if !ok {
		t.Fatalf("retrieve output = %T, want RetrieveResult", retrieve.Output)
	}
	if len(result.Memories) != 1 || result.Memories[0].AccessScope.UserID != "user-a" {
		t.Fatalf("memories = %#v, want user-scoped memory", result.Memories)
	}
}

func TestPluginOperationsDefaultAccessScopeToThread(t *testing.T) {
	ops, err := New().Operations(context.Background(), pluginhost.Context{
		EventStore: eventstore.NewMemoryStore(),
		DataStore:  runtimedata.NewMemoryStore(),
	})
	if err != nil {
		t.Fatalf("Operations: %v", err)
	}
	byName := operationsByName(ops)
	opCtx := sessionenv.OperationContext(operation.NewContext(context.Background(), nil), sessionenv.Config{
		Thread: corethread.Ref{ID: "thread-a"},
	}, "")
	memorize := byName[MemorizeOp].Run(opCtx, corememory.MemorizeRequest{
		Kind:       corememory.KindFact,
		Visibility: corememory.VisibilityPrivateAgent,
		Content:    "thread-local note",
	})
	if memorize.Status != operation.StatusOK {
		t.Fatalf("memorize = %#v, want ok", memorize)
	}
	retrieve := byName[RetrieveOp].Run(opCtx, corememory.RetrieveRequest{Text: "thread-local"})
	if retrieve.Status != operation.StatusOK {
		t.Fatalf("retrieve = %#v, want ok", retrieve)
	}
	result, ok := retrieve.Output.(corememory.RetrieveResult)
	if !ok {
		t.Fatalf("retrieve output = %T, want RetrieveResult", retrieve.Output)
	}
	if len(result.Memories) != 1 || result.Memories[0].AccessScope.ThreadID != "thread-a" {
		t.Fatalf("memories = %#v, want thread-scoped memory", result.Memories)
	}
}

func TestPluginContributesDataSourceAndEventTypes(t *testing.T) {
	bundle, err := New().Contributions(context.Background(), pluginhost.Context{})
	if err != nil {
		t.Fatalf("Contributions: %v", err)
	}
	if len(bundle.DataSources) != 1 || bundle.DataSources[0].Name != MemoryDataSource {
		t.Fatalf("data sources = %#v, want memory source", bundle.DataSources)
	}
	if len(bundle.Datasources) != 1 || bundle.Datasources[0].Name != coredatasource.Name(Name) {
		t.Fatalf("datasources = %#v, want memory datasource", bundle.Datasources)
	}
	if !bundle.Datasources[0].Semantic.Enabled || !bundle.Datasources[0].Index.Enabled {
		t.Fatalf("datasource = %#v, want semantic index enabled", bundle.Datasources[0])
	}
}

func TestMemoryDatasourceSearchGetAndCorpusAreScoped(t *testing.T) {
	ctxA := policy.ContextWithAuthorization(context.Background(), policy.AuthorizationContext{
		Subjects: []policy.SubjectRef{{Kind: policy.SubjectUser, ID: "user-a"}},
	})
	ctxB := policy.ContextWithAuthorization(context.Background(), policy.AuthorizationContext{
		Subjects: []policy.SubjectRef{{Kind: policy.SubjectUser, ID: "user-b"}},
	})
	events := eventstore.NewMemoryStore()
	data := runtimedata.NewMemoryStore()
	ops, err := New().Operations(ctxA, pluginhost.Context{EventStore: events, DataStore: data})
	if err != nil {
		t.Fatalf("Operations: %v", err)
	}
	memorize := operationsByName(ops)[MemorizeOp].Run(operation.NewContext(ctxA, nil), corememory.MemorizeRequest{
		ID:          "mem-a",
		Kind:        corememory.KindFact,
		Visibility:  corememory.VisibilityPrivateUser,
		Subjects:    []corememory.Subject{{Kind: corememory.SubjectUser, ID: "user-a", Name: "Ada"}},
		AccessScope: coredata.Scope{UserID: user.ID("user-a")},
		Title:       "Deploy preference",
		Content:     "Use staged deploys on Fridays.",
		Tags:        []string{"deploy", "friday"},
	})
	if memorize.Status != operation.StatusOK {
		t.Fatalf("memorize = %#v, want ok", memorize)
	}
	providers, err := New().DatasourceProviders(ctxA, pluginhost.Context{EventStore: events, DataStore: data})
	if err != nil {
		t.Fatalf("DatasourceProviders: %v", err)
	}
	accessor, err := providers[0].Open(ctxA, DatasourceSpec())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	search, err := accessor.(coredatasource.Searcher).Search(ctxA, coredatasource.SearchRequest{Entity: memoryItemDatasourceEntity, Query: "Fridays", Limit: 10})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(search.Records) != 1 || search.Records[0].ID != "mem-a" {
		t.Fatalf("search records = %#v, want scoped memory", search.Records)
	}
	if _, err := accessor.(coredatasource.Getter).Get(ctxB, coredatasource.GetRequest{Entity: memoryItemDatasourceEntity, ID: "mem-a"}); err == nil {
		t.Fatal("Get from another user succeeded, want scoped miss")
	}
	corpus, err := accessor.(coredatasource.CorpusProvider).Corpus(ctxA, coredatasource.CorpusRequest{Entity: memoryItemDatasourceEntity})
	if err != nil {
		t.Fatalf("Corpus: %v", err)
	}
	if len(corpus.Documents) != 1 {
		t.Fatalf("corpus = %#v, want one document", corpus)
	}
	doc := corpus.Documents[0]
	if doc.Title != "Deploy preference" || !strings.Contains(doc.Body, "staged deploys") {
		t.Fatalf("doc = %#v, want memory title/content", doc)
	}
	if doc.Metadata["tags"] != "deploy,friday" || doc.Metadata["subject.name"] != "Ada" {
		t.Fatalf("metadata = %#v, want tags and subject metadata", doc.Metadata)
	}
}

func operationsByName(ops []operation.Operation) map[operation.Name]operation.Operation {
	byName := map[operation.Name]operation.Operation{}
	for _, op := range ops {
		byName[op.Spec().Ref.Name] = op
	}
	return byName
}
