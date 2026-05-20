package memoryplugin

import (
	"context"
	"fmt"
	"strings"

	coredata "github.com/fluxplane/agentruntime/core/data"
	corememory "github.com/fluxplane/agentruntime/core/memory"
	"github.com/fluxplane/agentruntime/core/operation"
	"github.com/fluxplane/agentruntime/core/policy"
	"github.com/fluxplane/agentruntime/core/resource"
	corethread "github.com/fluxplane/agentruntime/core/thread"
	"github.com/fluxplane/agentruntime/core/user"
	coreworkspace "github.com/fluxplane/agentruntime/core/workspace"
	"github.com/fluxplane/agentruntime/orchestration/pluginhost"
	"github.com/fluxplane/agentruntime/orchestration/sessionenv"
	runtimememory "github.com/fluxplane/agentruntime/runtime/memory"
	operationruntime "github.com/fluxplane/agentruntime/runtime/operation"
)

const (
	Name             = "memory"
	MemorizeOp       = "memory_memorize"
	RetrieveOp       = "memory_retrieve"
	ForgetOp         = "memory_forget"
	OrganizeOp       = "memory_organize"
	MemoryDataSource = coredata.SourceName("memory")
)

type Plugin struct{}

var _ pluginhost.Plugin = Plugin{}
var _ pluginhost.OperationContributor = Plugin{}

func New() Plugin { return Plugin{} }

func (Plugin) Manifest() pluginhost.Manifest {
	return pluginhost.Manifest{Name: Name, Description: "Scoped structured memory operations."}
}

func (Plugin) Contributions(context.Context, pluginhost.Context) (resource.ContributionBundle, error) {
	specs := []operation.Spec{memorizeSpec(), retrieveSpec(), forgetSpec(), organizeSpec()}
	return resource.ContributionBundle{
		OperationSets: []operation.Set{{
			Name:        Name,
			Description: "Scoped structured memory tools.",
			Operations:  operationRefs(specs),
		}},
		Operations:  specs,
		DataSources: []coredata.SourceSpec{DataSourceSpec()},
	}, nil
}

func (Plugin) Operations(_ context.Context, ctx pluginhost.Context) ([]operation.Operation, error) {
	store, err := runtimememory.NewStore(ctx.EventStore, ctx.DataStore)
	if err != nil {
		return nil, err
	}
	return []operation.Operation{
		operationruntime.NewTyped[corememory.MemorizeRequest, corememory.MemorizeResult](memorizeSpec(), func(ctx operation.Context, req corememory.MemorizeRequest) (corememory.MemorizeResult, error) {
			scope, err := authorizedAccessScope(ctx, req.AccessScope)
			if err != nil {
				return corememory.MemorizeResult{}, err
			}
			req.AccessScope = scope
			return store.Memorize(ctx, req)
		}),
		operationruntime.NewTyped[corememory.RetrieveRequest, corememory.RetrieveResult](retrieveSpec(), func(ctx operation.Context, req corememory.RetrieveRequest) (corememory.RetrieveResult, error) {
			scope, err := authorizedAccessScope(ctx, req.AccessScope)
			if err != nil {
				return corememory.RetrieveResult{}, err
			}
			req.AccessScope = scope
			return store.Retrieve(ctx, req)
		}),
		operationruntime.NewTyped[corememory.ForgetRequest, corememory.ForgetResult](forgetSpec(), func(ctx operation.Context, req corememory.ForgetRequest) (corememory.ForgetResult, error) {
			scope, err := authorizedAccessScope(ctx, req.AccessScope)
			if err != nil {
				return corememory.ForgetResult{}, err
			}
			req.AccessScope = scope
			return store.Forget(ctx, req)
		}),
		operationruntime.NewTyped[corememory.OrganizeRequest, corememory.OrganizeResult](organizeSpec(), func(ctx operation.Context, req corememory.OrganizeRequest) (corememory.OrganizeResult, error) {
			scope, err := authorizedAccessScope(ctx, req.AccessScope)
			if err != nil {
				return corememory.OrganizeResult{}, err
			}
			req.AccessScope = scope
			return store.Organize(ctx, req)
		}),
	}, nil
}

func DataSourceSpec() coredata.SourceSpec {
	return coredata.SourceSpec{
		Name:        MemoryDataSource,
		Kind:        Name,
		Description: "Scoped structured memories with separate subject and access dimensions.",
		Entities: []coredata.EntitySpec{{
			Type:        corememory.ItemEntity,
			Description: "One structured memory item.",
			Capabilities: []coredata.EntityCapability{
				coredata.CapabilityGet,
				coredata.CapabilityList,
				coredata.CapabilitySearch,
				coredata.CapabilityMaterialize,
			},
			Fields: []coredata.FieldSpec{
				{Name: "kind", Type: coredata.FieldString, Filterable: true, Searchable: true},
				{Name: "status", Type: coredata.FieldString, Filterable: true},
				{Name: "visibility", Type: coredata.FieldString, Filterable: true},
				{Name: "tag", Type: coredata.FieldString, Filterable: true, Searchable: true},
				{Name: "subject.kind", Type: coredata.FieldString, Filterable: true},
				{Name: "subject.id", Type: coredata.FieldString, Filterable: true, Searchable: true},
				{Name: "subject.name", Type: coredata.FieldString, Searchable: true},
			},
		}},
		Views: []coredata.ViewSpec{{
			Name:        coredata.ViewName(corememory.ItemEntity),
			Entity:      corememory.ItemEntity,
			Source:      corememory.ItemEntity,
			Description: "Queryable current memory snapshots.",
			QueryHints:  []coredata.QueryHint{coredata.QueryGet, coredata.QueryList, coredata.QuerySearch},
		}},
	}
}

func operationRefs(specs []operation.Spec) []operation.Ref {
	out := make([]operation.Ref, 0, len(specs))
	for _, spec := range specs {
		out = append(out, spec.Ref)
	}
	return out
}

func authorizedAccessScope(ctx context.Context, requested coredata.Scope) (coredata.Scope, error) {
	authorized := authorizedScopes(ctx)
	if accessScopeEmpty(requested) {
		if len(authorized) == 0 {
			return coredata.Scope{}, nil
		}
		return authorized[0], nil
	}
	if scopeAuthorized(requested, authorized) {
		return requested, nil
	}
	return coredata.Scope{}, fmt.Errorf("memory: access_scope is outside caller scope")
}

func authorizedScopes(ctx context.Context) []coredata.Scope {
	var scopes []coredata.Scope
	if auth, ok := policy.AuthorizationFromContext(ctx); ok {
		for _, subject := range auth.Subjects {
			if subject.Kind == policy.SubjectUser {
				if id := strings.TrimSpace(subject.ID); id != "" {
					scopes = append(scopes, coredata.Scope{UserID: user.ID(id)})
				}
			}
		}
		for _, subject := range auth.Subjects {
			if subject.Kind == policy.SubjectAgent {
				if id := strings.TrimSpace(subject.ID); id != "" {
					scopes = append(scopes, coredata.Scope{AgentID: id})
				}
			}
		}
	}
	if sessionScope, ok := sessionenv.ScopeFromContext(ctx); ok {
		scopes = append(scopes, coredata.Scope{ThreadID: sessionScope.Thread.ID})
	}
	return scopes
}

func scopeAuthorized(requested coredata.Scope, authorized []coredata.Scope) bool {
	if len(authorized) == 0 {
		return false
	}
	if requested.TenantID != "" && !tenantAuthorized(requested.TenantID, authorized) {
		return false
	}
	if requested.AppID != "" && !appAuthorized(requested.AppID, authorized) {
		return false
	}
	if requested.WorkspaceID != "" && !workspaceAuthorized(requested.WorkspaceID, authorized) {
		return false
	}
	if requested.UserID != "" && !userAuthorized(requested.UserID, authorized) {
		return false
	}
	if requested.AgentID != "" && !agentAuthorized(requested.AgentID, authorized) {
		return false
	}
	if requested.SessionID != "" && !sessionAuthorized(requested.SessionID, authorized) {
		return false
	}
	if requested.ThreadID != "" && !threadAuthorized(requested.ThreadID, authorized) {
		return false
	}
	if requested.ChannelID != "" && !channelAuthorized(requested.ChannelID, authorized) {
		return false
	}
	for key, value := range requested.Annotations {
		if strings.TrimSpace(key) != "" && strings.TrimSpace(value) != "" && !annotationAuthorized(key, value, authorized) {
			return false
		}
	}
	return true
}

func tenantAuthorized(value string, authorized []coredata.Scope) bool {
	for _, scope := range authorized {
		if scope.TenantID == value {
			return true
		}
	}
	return false
}

func appAuthorized(value string, authorized []coredata.Scope) bool {
	for _, scope := range authorized {
		if scope.AppID == value {
			return true
		}
	}
	return false
}

func workspaceAuthorized(value coreworkspace.ID, authorized []coredata.Scope) bool {
	for _, scope := range authorized {
		if scope.WorkspaceID == value {
			return true
		}
	}
	return false
}

func userAuthorized(value user.ID, authorized []coredata.Scope) bool {
	for _, scope := range authorized {
		if scope.UserID == value {
			return true
		}
	}
	return false
}

func agentAuthorized(value string, authorized []coredata.Scope) bool {
	for _, scope := range authorized {
		if scope.AgentID == value {
			return true
		}
	}
	return false
}

func sessionAuthorized(value string, authorized []coredata.Scope) bool {
	for _, scope := range authorized {
		if scope.SessionID == value {
			return true
		}
	}
	return false
}

func threadAuthorized(value corethread.ID, authorized []coredata.Scope) bool {
	for _, scope := range authorized {
		if scope.ThreadID == value {
			return true
		}
	}
	return false
}

func channelAuthorized(value string, authorized []coredata.Scope) bool {
	for _, scope := range authorized {
		if scope.ChannelID == value {
			return true
		}
	}
	return false
}

func annotationAuthorized(key, value string, authorized []coredata.Scope) bool {
	for _, scope := range authorized {
		if scope.Annotations[key] == value {
			return true
		}
	}
	return false
}

func accessScopeEmpty(scope coredata.Scope) bool {
	if scope.TenantID != "" ||
		scope.AppID != "" ||
		scope.WorkspaceID != "" ||
		scope.UserID != "" ||
		scope.AgentID != "" ||
		scope.SessionID != "" ||
		scope.ThreadID != "" ||
		scope.ChannelID != "" {
		return false
	}
	for key, value := range scope.Annotations {
		if strings.TrimSpace(key) != "" && strings.TrimSpace(value) != "" {
			return false
		}
	}
	return true
}

func memorizeSpec() operation.Spec {
	return operationruntime.WithTypedContract[corememory.MemorizeRequest, corememory.MemorizeResult](operation.Spec{
		Ref:         operation.Ref{Name: MemorizeOp},
		Description: "Store a scoped structured memory. Subject says what the memory is about; access_scope says who or what may retrieve it.",
		Semantics: operation.Semantics{
			Determinism: operation.DeterminismNonDeterministic,
			Effects:     operation.EffectSet{operation.EffectCreate},
			Idempotency: operation.IdempotencyNonIdempotent,
			Risk:        operation.RiskLow,
		},
	})
}

func retrieveSpec() operation.Spec {
	return operationruntime.WithTypedContract[corememory.RetrieveRequest, corememory.RetrieveResult](operation.Spec{
		Ref:         operation.Ref{Name: RetrieveOp},
		Description: "Retrieve memories by access scope first, then by subject, kind, tag, id, or text.",
		Semantics: operation.Semantics{
			Determinism: operation.DeterminismDeterministic,
			Effects:     operation.EffectSet{},
			Idempotency: operation.IdempotencyIdempotent,
			Risk:        operation.RiskLow,
		},
	})
}

func forgetSpec() operation.Spec {
	return operationruntime.WithTypedContract[corememory.ForgetRequest, corememory.ForgetResult](operation.Spec{
		Ref:         operation.Ref{Name: ForgetOp},
		Description: "Forget, archive, or expire memories within an access scope.",
		Semantics: operation.Semantics{
			Determinism: operation.DeterminismNonDeterministic,
			Effects:     operation.EffectSet{operation.EffectUpdate},
			Idempotency: operation.IdempotencyNonIdempotent,
			Risk:        operation.RiskLow,
		},
	})
}

func organizeSpec() operation.Spec {
	return operationruntime.WithTypedContract[corememory.OrganizeRequest, corememory.OrganizeResult](operation.Spec{
		Ref:         operation.Ref{Name: OrganizeOp},
		Description: "Retag, merge, supersede, summarize, or archive memories while preserving access/provenance unless explicitly changed.",
		Semantics: operation.Semantics{
			Determinism: operation.DeterminismNonDeterministic,
			Effects:     operation.EffectSet{operation.EffectUpdate},
			Idempotency: operation.IdempotencyNonIdempotent,
			Risk:        operation.RiskLow,
		},
	})
}
