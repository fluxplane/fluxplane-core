package memory

import (
	"context"
	"fmt"
	"github.com/fluxplane/fluxplane-policy/policyauth"
	"strings"
	"time"

	"github.com/fluxplane/fluxplane-core/core/activation"
	coredata "github.com/fluxplane/fluxplane-core/core/data"
	coredatasource "github.com/fluxplane/fluxplane-core/core/datasource"
	coreevidence "github.com/fluxplane/fluxplane-core/core/evidence"
	corememory "github.com/fluxplane/fluxplane-core/core/memory"
	"github.com/fluxplane/fluxplane-core/core/operation"
	"github.com/fluxplane/fluxplane-core/core/resource"
	corethread "github.com/fluxplane/fluxplane-core/core/thread"
	"github.com/fluxplane/fluxplane-core/core/user"
	coreworkspace "github.com/fluxplane/fluxplane-core/core/workspace"
	"github.com/fluxplane/fluxplane-core/orchestration/pluginhost"
	"github.com/fluxplane/fluxplane-core/orchestration/sessionenv"
	runtimeevidence "github.com/fluxplane/fluxplane-core/runtime/evidence"
	runtimememory "github.com/fluxplane/fluxplane-core/runtime/memory"
	operationruntime "github.com/fluxplane/fluxplane-core/runtime/operation"
	"github.com/fluxplane/fluxplane-policy"
)

const (
	Name             = "memory"
	MemorizeOp       = "memory_memorize"
	RetrieveOp       = "memory_retrieve"
	ForgetOp         = "memory_forget"
	OrganizeOp       = "memory_organize"
	MemoryDataSource = coredata.SourceName("memory")
	MutationSet      = "memory.mutation"

	ObservationMemoryStore        = "memory.store"
	AssertionMemoryMutationReady  = "capability.available"
	memoryStoreObserverName       = "memory.store"
	memoryAvailabilityDeriverName = "memory.availability"
)

type Plugin struct{}

var _ pluginhost.Plugin = Plugin{}
var _ pluginhost.OperationContributor = Plugin{}
var _ pluginhost.DatasourceProviderContributor = Plugin{}
var _ pluginhost.ObserverContributor = Plugin{}
var _ pluginhost.AssertionDeriverContributor = Plugin{}

func New() Plugin { return Plugin{} }

func (Plugin) Manifest() pluginhost.Manifest {
	return pluginhost.Manifest{Name: Name, Description: "Scoped structured memory operations."}
}

func (Plugin) Contributions(_ context.Context, ctx pluginhost.Context) (resource.ContributionBundle, error) {
	specs := []operation.Spec{memorizeSpec(), retrieveSpec(), forgetSpec(), organizeSpec()}
	name := ctx.Ref.InstanceName()
	if name == "" {
		name = Name
	}
	return resource.ContributionBundle{
		ActivationSets: []activation.Set{{
			Name:        name,
			Aliases:     []string{name + ".default"},
			Description: "Scoped structured memory tools and datasource.",
			Targets: []activation.Target{{
				Kind:         activation.TargetOperationSet,
				OperationSet: Name,
			}, {
				Kind:       activation.TargetDatasource,
				Datasource: coredatasource.Ref{Name: coredatasource.Name(Name)},
			}},
		}},
		OperationSets: []operation.Set{{
			Name:        Name,
			Description: "Scoped structured memory tools.",
			Operations:  operationRefs(specs),
		}, {
			Name:        MutationSet,
			Description: "Scoped structured memory mutation tools.",
			Operations:  []operation.Ref{{Name: MemorizeOp}, {Name: ForgetOp}, {Name: OrganizeOp}},
		}},
		Operations:  specs,
		DataSources: []coredata.SourceSpec{DataSourceSpec()},
		Datasources: []coredatasource.Spec{DatasourceSpec()},
		Observers: []coreevidence.ObserverSpec{{
			Name:            memoryStoreObserverName,
			Description:     "Observes whether scoped memory storage is configured.",
			Environment:     coreevidence.Ref{Name: Name},
			Phase:           coreevidence.PhaseTurn,
			ObservableKinds: []string{ObservationMemoryStore},
			Dynamic:         true,
		}},
		AssertionDerivers: []coreevidence.AssertionDeriverSpec{{
			Name:             memoryAvailabilityDeriverName,
			Description:      "Derives memory mutation activation from stable storage availability.",
			ObservationKinds: []string{ObservationMemoryStore},
			Assertions: []coreevidence.AssertionTemplate{
				{Kind: AssertionMemoryMutationReady, Target: MutationSet, Subject: coreevidence.Subject{Kind: coreevidence.SubjectCapability, Name: MutationSet}},
			},
		}},
	}, nil
}

func (Plugin) EnvironmentObservers(_ context.Context, ctx pluginhost.Context) ([]runtimeevidence.Observer, error) {
	return []runtimeevidence.Observer{memoryStoreObserver{configured: ctx.EventStore != nil}}, nil
}

func (Plugin) AssertionDerivers(context.Context, pluginhost.Context) ([]runtimeevidence.AssertionDeriver, error) {
	return []runtimeevidence.AssertionDeriver{memoryAvailabilityDeriver{}}, nil
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

func (Plugin) DatasourceProviders(_ context.Context, ctx pluginhost.Context) ([]coredatasource.Provider, error) {
	store, err := runtimememory.NewStore(ctx.EventStore, ctx.DataStore)
	if err != nil {
		return nil, err
	}
	return []coredatasource.Provider{memoryDatasourceProvider{store: store}}, nil
}

type MemoryStoreEvidence struct {
	Configured bool `json:"configured"`
}

type memoryStoreObserver struct {
	configured bool
}

func (o memoryStoreObserver) Spec() coreevidence.ObserverSpec {
	return coreevidence.ObserverSpec{
		Name:            memoryStoreObserverName,
		Description:     "Observes whether scoped memory storage is configured.",
		Environment:     coreevidence.Ref{Name: Name},
		Phase:           coreevidence.PhaseTurn,
		ObservableKinds: []string{ObservationMemoryStore},
		Dynamic:         true,
	}
}

func (o memoryStoreObserver) Observe(_ context.Context, _ runtimeevidence.ObservationRequest) ([]coreevidence.Observation, error) {
	return []coreevidence.Observation{{
		ID:          "memory:store",
		Environment: coreevidence.Ref{Name: Name},
		Kind:        ObservationMemoryStore,
		Scope:       "runtime",
		Content:     MemoryStoreEvidence{Configured: o.configured},
		At:          time.Now().UTC(),
	}}, nil
}

type memoryAvailabilityDeriver struct{}

func (memoryAvailabilityDeriver) Spec() coreevidence.AssertionDeriverSpec {
	return coreevidence.AssertionDeriverSpec{
		Name:             memoryAvailabilityDeriverName,
		Description:      "Derives memory mutation activation from stable storage availability.",
		ObservationKinds: []string{ObservationMemoryStore},
	}
}

func (memoryAvailabilityDeriver) Derive(_ context.Context, req runtimeevidence.AssertionDeriveRequest) ([]coreevidence.Assertion, error) {
	var configured bool
	var ids []string
	var scope string
	for _, observation := range req.Observations {
		if observation.Kind != ObservationMemoryStore {
			continue
		}
		if memoryStoreConfigured(observation.Content) {
			configured = true
			ids = appendMemoryObservationID(ids, observation.ID)
			if scope == "" {
				scope = observation.Scope
			}
		}
	}
	if !configured {
		return nil, nil
	}
	return []coreevidence.Assertion{{
		Kind:           AssertionMemoryMutationReady,
		Target:         MutationSet,
		Subject:        coreevidence.Subject{Kind: coreevidence.SubjectCapability, Name: MutationSet},
		Scope:          scope,
		Environment:    coreevidence.Ref{Name: Name},
		Confidence:     1,
		ObservationIDs: ids,
		Metadata:       map[string]string{"capability": MutationSet},
	}}, nil
}

func memoryStoreConfigured(content any) bool {
	switch typed := content.(type) {
	case MemoryStoreEvidence:
		return typed.Configured
	case *MemoryStoreEvidence:
		return typed != nil && typed.Configured
	case map[string]any:
		configured, _ := typed["configured"].(bool)
		return configured
	default:
		return false
	}
}

func appendMemoryObservationID(ids []string, id string) []string {
	if strings.TrimSpace(id) == "" {
		return ids
	}
	return append(ids, id)
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

func DatasourceSpec() coredatasource.Spec {
	return coredatasource.Spec{
		Name:        coredatasource.Name(Name),
		Kind:        Name,
		Description: "Scoped structured memories available through datasource search and retrieval.",
		Entities:    []coredatasource.EntityType{memoryItemDatasourceEntity},
		Index:       coredatasource.IndexSpec{Enabled: true},
		Semantic: coredatasource.SemanticSpec{
			Enabled: true,
			Entities: map[coredatasource.EntityType]coredatasource.EntitySemantic{
				memoryItemDatasourceEntity: {
					Corpus: coredatasource.CorpusSpec{
						TitleFields:    []string{"title"},
						BodyFields:     []string{"content"},
						MetadataFields: []string{"kind", "tag", "subject.kind", "subject.id", "subject.name"},
					},
				},
			},
		},
	}
}

const memoryItemDatasourceEntity coredatasource.EntityType = coredatasource.EntityType(corememory.ItemEntity)

type memoryDatasourceProvider struct {
	store *runtimememory.Store
}

func (p memoryDatasourceProvider) Entities() []coredatasource.EntitySpec {
	return []coredatasource.EntitySpec{memoryDatasourceEntitySpec()}
}

func (p memoryDatasourceProvider) Open(_ context.Context, spec coredatasource.Spec) (coredatasource.Accessor, error) {
	if p.store == nil {
		return nil, fmt.Errorf("memory: store is nil")
	}
	if spec.Kind != "" && spec.Kind != Name {
		return nil, fmt.Errorf("memory: unsupported datasource kind %q", spec.Kind)
	}
	return memoryDatasourceAccessor{spec: spec, store: p.store}, nil
}

type memoryDatasourceAccessor struct {
	spec  coredatasource.Spec
	store *runtimememory.Store
}

func (a memoryDatasourceAccessor) Spec() coredatasource.Spec {
	if a.spec.Name == "" {
		return DatasourceSpec()
	}
	return a.spec
}

func (a memoryDatasourceAccessor) Entities() []coredatasource.EntitySpec {
	return []coredatasource.EntitySpec{memoryDatasourceEntitySpec()}
}

func (a memoryDatasourceAccessor) Search(ctx context.Context, req coredatasource.SearchRequest) (coredatasource.SearchResult, error) {
	scope, err := authorizedAccessScope(ctx, coredata.Scope{})
	if err != nil {
		return coredatasource.SearchResult{}, err
	}
	result, err := a.store.Retrieve(ctx, corememory.RetrieveRequest{
		AccessScope: scope,
		Text:        strings.TrimSpace(req.Query),
		Limit:       req.Limit,
	})
	if err != nil {
		return coredatasource.SearchResult{}, err
	}
	return coredatasource.SearchResult{Datasource: a.Spec().Name, Entity: memoryItemDatasourceEntity, Records: datasourceRecordsFromMemories(a.Spec().Name, result.Memories), Total: len(result.Memories)}, nil
}

func (a memoryDatasourceAccessor) Get(ctx context.Context, req coredatasource.GetRequest) (coredatasource.Record, error) {
	scope, err := authorizedAccessScope(ctx, coredata.Scope{})
	if err != nil {
		return coredatasource.Record{}, err
	}
	result, err := a.store.Retrieve(ctx, corememory.RetrieveRequest{
		AccessScope: scope,
		IDs:         []corememory.ID{corememory.ID(strings.TrimSpace(req.ID))},
		Limit:       1,
	})
	if err != nil {
		return coredatasource.Record{}, err
	}
	if len(result.Memories) == 0 {
		return coredatasource.Record{}, coredatasource.ErrNotFound
	}
	return datasourceRecordFromMemory(a.Spec().Name, result.Memories[0]), nil
}

func (a memoryDatasourceAccessor) Corpus(ctx context.Context, req coredatasource.CorpusRequest) (coredatasource.CorpusPage, error) {
	scope, err := authorizedAccessScope(ctx, coredata.Scope{})
	if err != nil || accessScopeEmpty(scope) {
		return coredatasource.CorpusPage{Complete: true}, nil
	}
	limit := req.Limit
	if limit <= 0 {
		limit = 1000
	}
	result, err := a.store.Retrieve(ctx, corememory.RetrieveRequest{
		AccessScope: scope,
		Limit:       limit,
		Cursor:      strings.TrimSpace(req.Cursor),
	})
	if err != nil {
		return coredatasource.CorpusPage{}, err
	}
	docs := make([]coredatasource.CorpusDocument, 0, len(result.Memories))
	for _, memory := range result.Memories {
		docs = append(docs, corpusDocumentFromMemory(a.Spec().Name, memory))
	}
	return coredatasource.CorpusPage{Documents: docs, NextCursor: result.NextCursor, Complete: result.Complete}, nil
}

func memoryDatasourceEntitySpec() coredatasource.EntitySpec {
	return coredatasource.EntitySpec{
		Type:        memoryItemDatasourceEntity,
		Description: "One structured memory item.",
		Capabilities: []coredatasource.EntityCapability{
			coredatasource.EntityCapabilitySearch,
			coredatasource.EntityCapabilityGet,
			coredatasource.EntityCapabilityIndex,
			coredatasource.EntityCapabilitySemanticSearch,
		},
		Fields: []coredatasource.FieldSpec{
			{Name: "kind", Type: coredatasource.FieldString, Filterable: true, Searchable: true, Corpus: true},
			{Name: "status", Type: coredatasource.FieldString, Filterable: true},
			{Name: "visibility", Type: coredatasource.FieldString, Filterable: true},
			{Name: "tag", Type: coredatasource.FieldString, Filterable: true, Searchable: true, Corpus: true},
			{Name: "subject.kind", Type: coredatasource.FieldString, Filterable: true, Corpus: true},
			{Name: "subject.id", Type: coredatasource.FieldString, Filterable: true, Searchable: true, Corpus: true},
			{Name: "subject.name", Type: coredatasource.FieldString, Searchable: true, Corpus: true},
		},
	}
}

func datasourceRecordsFromMemories(datasource coredatasource.Name, memories []corememory.Memory) []coredatasource.Record {
	out := make([]coredatasource.Record, 0, len(memories))
	for _, memory := range memories {
		out = append(out, datasourceRecordFromMemory(datasource, memory))
	}
	return out
}

func datasourceRecordFromMemory(datasource coredatasource.Name, memory corememory.Memory) coredatasource.Record {
	return coredatasource.Record{
		ID:         string(memory.ID),
		Datasource: datasource,
		Entity:     memoryItemDatasourceEntity,
		Title:      memory.Title,
		Content:    memory.Content,
		Metadata:   memoryDatasourceMetadata(memory),
		Raw:        memory,
	}
}

func corpusDocumentFromMemory(datasource coredatasource.Name, memory corememory.Memory) coredatasource.CorpusDocument {
	return coredatasource.CorpusDocument{
		Ref: coredatasource.RecordRef{
			Datasource: datasource,
			Entity:     memoryItemDatasourceEntity,
			ID:         string(memory.ID),
		},
		Title:     memory.Title,
		Body:      memory.Content,
		Metadata:  memoryDatasourceMetadata(memory),
		UpdatedAt: memory.Provenance.UpdatedAt.Format("2006-01-02T15:04:05.999999999Z07:00"),
	}
}

func memoryDatasourceMetadata(memory corememory.Memory) map[string]string {
	metadata := map[string]string{
		"kind":       string(memory.Kind),
		"status":     string(memory.Status),
		"visibility": string(memory.Visibility),
		"tags":       strings.Join(memory.Tags, ","),
	}
	var subjectKinds, subjectIDs, subjectNames []string
	for _, subject := range memory.Subjects {
		if subject.Kind != "" {
			subjectKinds = append(subjectKinds, string(subject.Kind))
		}
		if subject.ID != "" {
			subjectIDs = append(subjectIDs, subject.ID)
		}
		if subject.Name != "" {
			subjectNames = append(subjectNames, subject.Name)
		}
	}
	metadata["subject.kind"] = strings.Join(subjectKinds, ",")
	metadata["subject.id"] = strings.Join(subjectIDs, ",")
	metadata["subject.name"] = strings.Join(subjectNames, ",")
	return metadata
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
	if auth, ok := policyauth.AuthorizationFromContext(ctx); ok {
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
