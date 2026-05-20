package memoryplugin

import (
	"context"
	"testing"

	coredata "github.com/fluxplane/agentruntime/core/data"
	corememory "github.com/fluxplane/agentruntime/core/memory"
	"github.com/fluxplane/agentruntime/core/operation"
	"github.com/fluxplane/agentruntime/core/policy"
	corethread "github.com/fluxplane/agentruntime/core/thread"
	"github.com/fluxplane/agentruntime/core/user"
	"github.com/fluxplane/agentruntime/orchestration/pluginhost"
	"github.com/fluxplane/agentruntime/orchestration/sessionenv"
	runtimedata "github.com/fluxplane/agentruntime/runtime/data"
	"github.com/fluxplane/agentruntime/runtime/eventstore"
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
}

func operationsByName(ops []operation.Operation) map[operation.Name]operation.Operation {
	byName := map[operation.Name]operation.Operation{}
	for _, op := range ops {
		byName[op.Spec().Ref.Name] = op
	}
	return byName
}
