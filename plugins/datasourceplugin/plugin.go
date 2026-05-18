package datasourceplugin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"

	corecontext "github.com/fluxplane/agentruntime/core/context"
	coredatasource "github.com/fluxplane/agentruntime/core/datasource"
	"github.com/fluxplane/agentruntime/core/operation"
	"github.com/fluxplane/agentruntime/core/policy"
	"github.com/fluxplane/agentruntime/core/resource"
	"github.com/fluxplane/agentruntime/orchestration/pluginhost"
	runtimedatasource "github.com/fluxplane/agentruntime/runtime/datasource"
	"github.com/fluxplane/agentruntime/runtime/datasource/semantic"
	operationruntime "github.com/fluxplane/agentruntime/runtime/operation"
)

const (
	Name                = "datasource"
	SearchOperation     = "datasource_search"
	GetOperation        = "datasource_get"
	RelationOperation   = "datasource_relation"
	BatchGetOperation   = "datasource_batch_get"
	ContextProvider     = "datasource.catalog"
	DetectedProvider    = "datasource.detected"
	defaultSearchLimit  = 10
	maxParallelSearches = 4
	maxDetectedRefs     = 20
)

type Plugin struct {
	registry *coredatasource.Registry
}

var _ pluginhost.Plugin = Plugin{}
var _ pluginhost.OperationContributor = Plugin{}
var _ pluginhost.ContextProviderContributor = Plugin{}

func New(registry *coredatasource.Registry) Plugin {
	return Plugin{registry: registry}
}

func NewWithSemantic(registry *coredatasource.Registry, _ *semantic.Index) Plugin {
	return Plugin{registry: registry}
}

func (Plugin) Manifest() pluginhost.Manifest {
	return pluginhost.Manifest{Name: Name, Description: "Generic datasource search and retrieval tools."}
}

func (p Plugin) Contributions(context.Context, pluginhost.Context) (resource.ContributionBundle, error) {
	return resource.ContributionBundle{
		ContextProviders: []corecontext.ProviderSpec{contextSpec(), detectedContextSpec()},
		Operations:       []operation.Spec{searchSpec(), getSpec(), relationSpec(), batchGetSpec()},
	}, nil
}

func (p Plugin) Operations(context.Context, pluginhost.Context) ([]operation.Operation, error) {
	return []operation.Operation{
		operationruntime.NewTypedResult[searchInput, operation.Rendered](searchSpec(), p.search, operationruntime.WithAccessFields[searchInput](
			operationruntime.StaticAccess[searchInput](policy.ResourceRef{Kind: policy.ResourceDatasource, Name: "*"}, policy.ActionDatasourceSearch),
		)),
		operationruntime.NewTypedResult[getInput, operation.Rendered](getSpec(), p.get, operationruntime.WithAccessFields[getInput](
			operationruntime.DatasourceAccess(func(input getInput) string { return input.Datasource }, policy.ActionDatasourceRead),
		)),
		operationruntime.NewTypedResult[relationInput, operation.Rendered](relationSpec(), p.relation, operationruntime.WithAccessFields[relationInput](
			operationruntime.DatasourceAccess(func(input relationInput) string { return input.Datasource }, policy.ActionDatasourceRead),
		)),
		operationruntime.NewTypedResult[batchGetInput, operation.Rendered](batchGetSpec(), p.batchGet, operationruntime.WithAccessFields[batchGetInput](
			operationruntime.DatasourceAccess(func(input batchGetInput) string { return input.Datasource }, policy.ActionDatasourceRead),
		)),
	}, nil
}

func (p Plugin) ContextProviders(context.Context, pluginhost.Context) ([]corecontext.Provider, error) {
	return []corecontext.Provider{catalogProvider(p), detectedProvider(p)}, nil
}

func contextSpec() corecontext.ProviderSpec {
	return corecontext.ProviderSpec{
		Name:        ContextProvider,
		Description: "Lists datasources and entities available to the current agent.",
		Kinds:       []corecontext.BlockKind{corecontext.BlockText, corecontext.BlockData},
	}
}

func detectedContextSpec() corecontext.ProviderSpec {
	return corecontext.ProviderSpec{
		Name:        DetectedProvider,
		Description: "Lists local datasource references detected in the current turn.",
		Kinds:       []corecontext.BlockKind{corecontext.BlockText, corecontext.BlockData},
	}
}

func searchSpec() operation.Spec {
	return operationruntime.WithTypedContract[searchInput, operation.Rendered](operation.Spec{
		Ref:         operation.Ref{Name: SearchOperation},
		Description: "Search configured datasources the current agent is allowed to access.",
		Semantics: operation.Semantics{
			Determinism: operation.DeterminismNonDeterministic,
			Effects:     operation.EffectSet{operation.EffectNetwork, operation.EffectFilesystem, operation.EffectReadExternal},
			Idempotency: operation.IdempotencyIdempotent,
			Risk:        operation.RiskLow,
		},
	})
}

func getSpec() operation.Spec {
	return operationruntime.WithTypedContract[getInput, operation.Rendered](operation.Spec{
		Ref:         operation.Ref{Name: GetOperation},
		Description: "Retrieve one record from a configured datasource the current agent is allowed to access.",
		Semantics: operation.Semantics{
			Determinism: operation.DeterminismNonDeterministic,
			Effects:     operation.EffectSet{operation.EffectNetwork, operation.EffectFilesystem, operation.EffectReadExternal},
			Idempotency: operation.IdempotencyIdempotent,
			Risk:        operation.RiskLow,
		},
	})
}

func relationSpec() operation.Spec {
	return operationruntime.WithTypedContract[relationInput, operation.Rendered](operation.Spec{
		Ref:         operation.Ref{Name: RelationOperation},
		Description: "Retrieve exact related datasource records, such as Slack channel members.",
		Semantics: operation.Semantics{
			Determinism: operation.DeterminismNonDeterministic,
			Effects:     operation.EffectSet{operation.EffectNetwork, operation.EffectFilesystem, operation.EffectReadExternal},
			Idempotency: operation.IdempotencyIdempotent,
			Risk:        operation.RiskLow,
		},
	})
}

func batchGetSpec() operation.Spec {
	return operationruntime.WithTypedContract[batchGetInput, operation.Rendered](operation.Spec{
		Ref:         operation.Ref{Name: BatchGetOperation},
		Description: "Retrieve multiple records from one configured datasource entity by id.",
		Semantics: operation.Semantics{
			Determinism: operation.DeterminismNonDeterministic,
			Effects:     operation.EffectSet{operation.EffectNetwork, operation.EffectFilesystem, operation.EffectReadExternal},
			Idempotency: operation.IdempotencyIdempotent,
			Risk:        operation.RiskLow,
		},
	})
}

type searchInput struct {
	Queries  []string          `json:"queries,omitempty" jsonschema:"description=Search queries to run. Use one or more short text queries."`
	Query    string            `json:"query,omitempty" jsonschema:"description=Single search query convenience field."`
	Entities []string          `json:"entities,omitempty" jsonschema:"description=Optional entity type filters such as gitlab.project, jira.issue, or jira.*."`
	Filters  map[string]string `json:"filters,omitempty" jsonschema:"description=Provider-specific structured filters for lexical datasource search."`
	Limit    int               `json:"limit,omitempty" jsonschema:"description=Maximum records per datasource per query. Defaults to 10."`
	Mode     string            `json:"mode,omitempty" jsonschema:"description=Provider-specific search mode: auto, semantic, hybrid, lexical, provider, or live. Defaults to auto."`
	MinScore float64           `json:"min_score,omitempty" jsonschema:"description=Minimum semantic score when semantic search is used."`
}

type getInput struct {
	Datasource string `json:"datasource" jsonschema:"description=Configured datasource name.,required"`
	Entity     string `json:"entity" jsonschema:"description=Entity type to retrieve from, such as jira.issue.,required"`
	ID         string `json:"id" jsonschema:"description=Record id to retrieve.,required"`
}

type relationInput struct {
	Datasource string `json:"datasource" jsonschema:"description=Configured datasource name.,required"`
	Entity     string `json:"entity" jsonschema:"description=Source entity type, such as slack.channel.,required"`
	ID         string `json:"id" jsonschema:"description=Source record id.,required"`
	Relation   string `json:"relation" jsonschema:"description=Relation name, such as members.,required"`
	Limit      int    `json:"limit,omitempty" jsonschema:"description=Maximum related records to return for one page."`
	Cursor     string `json:"cursor,omitempty" jsonschema:"description=Pagination cursor from a previous relation call."`
}

type batchGetInput struct {
	Datasource string   `json:"datasource" jsonschema:"description=Configured datasource name.,required"`
	Entity     string   `json:"entity" jsonschema:"description=Entity type to retrieve from, such as slack.user.,required"`
	IDs        []string `json:"ids,omitempty" jsonschema:"description=Record ids to retrieve."`
}

type searchOutput struct {
	Results []coredatasource.SearchResult `json:"results,omitempty"`
	Errors  []sourceError                 `json:"errors,omitempty"`
}

type getOutput struct {
	Record coredatasource.Record `json:"record,omitempty"`
}

type relationOutput struct {
	Result coredatasource.RelationResult `json:"result,omitempty"`
}

type batchGetOutput struct {
	Result coredatasource.BatchGetResult `json:"result,omitempty"`
}

type sourceError struct {
	Datasource string `json:"datasource,omitempty"`
	Entity     string `json:"entity,omitempty"`
	Message    string `json:"message"`
}

func (p Plugin) search(ctx operation.Context, input searchInput) operation.Result {
	if p.registry == nil {
		return operation.Failed("datasource_registry_missing", "datasource registry is nil", nil)
	}
	queries := cleaned(input.Queries)
	if strings.TrimSpace(input.Query) != "" {
		queries = append(queries, strings.TrimSpace(input.Query))
	}
	if len(queries) == 0 {
		return operation.Failed("invalid_datasource_search_input", "at least one query is required", nil)
	}
	limit := input.Limit
	if limit <= 0 {
		limit = defaultSearchLimit
	}
	targets, err := p.selectedSearchTargets(ctx, input.Entities)
	if err != nil {
		return operation.Failed("datasource_search_denied", err.Error(), nil)
	}
	out := p.withRecordLinks(ctx, p.runSearches(ctx, targets, queries, limit, input.Filters, searchMode(input.Mode), input.MinScore))
	return operation.OK(operation.Rendered{Text: renderSearch(out), Data: out})
}

func (p Plugin) runSearches(ctx operation.Context, targets []searchTarget, queries []string, limit int, filters map[string]string, mode string, minScore float64) searchOutput {
	type searchJob struct {
		index  int
		target searchTarget
		query  string
	}
	type searchJobResult struct {
		index  int
		result coredatasource.SearchResult
		err    sourceError
	}
	var jobs []searchJob
	for _, target := range targets {
		for _, query := range queries {
			jobs = append(jobs, searchJob{index: len(jobs), target: target, query: query})
		}
	}
	results := make([]searchJobResult, len(jobs))
	sem := make(chan struct{}, maxParallelSearches)
	var wg sync.WaitGroup
	for _, job := range jobs {
		job := job
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			spec := job.target.Accessor.Spec()
			searcher, ok := job.target.Accessor.(coredatasource.Searcher)
			if !ok {
				results[job.index] = searchJobResult{index: job.index, err: sourceError{Datasource: string(spec.Name), Entity: string(job.target.Entity.Type), Message: "search is not supported"}}
				return
			}
			result, err := searcher.Search(ctx, coredatasource.SearchRequest{Entity: job.target.Entity.Type, Query: job.query, Limit: limit, Filters: filters, Mode: mode, MinScore: minScore})
			if err != nil {
				results[job.index] = searchJobResult{index: job.index, err: sourceError{Datasource: string(spec.Name), Entity: string(job.target.Entity.Type), Message: err.Error()}}
				return
			}
			results[job.index] = searchJobResult{index: job.index, result: result}
		}()
	}
	wg.Wait()
	out := searchOutput{}
	for _, result := range results {
		if result.err.Message != "" {
			out.Errors = append(out.Errors, result.err)
			continue
		}
		out.Results = append(out.Results, result.result)
	}
	return out
}

func (p Plugin) get(ctx operation.Context, input getInput) operation.Result {
	if p.registry == nil {
		return operation.Failed("datasource_registry_missing", "datasource registry is nil", nil)
	}
	name := coredatasource.Name(strings.TrimSpace(input.Datasource))
	entity := coredatasource.EntityType(strings.TrimSpace(input.Entity))
	if name == "" || entity == "" || strings.TrimSpace(input.ID) == "" {
		return operation.Failed("invalid_datasource_get_input", "datasource, entity, and id are required", nil)
	}
	if err := allowed(ctx, name); err != nil {
		return operation.Failed("datasource_get_denied", err.Error(), nil)
	}
	accessor, ok := p.registry.Get(name)
	if !ok {
		return operation.Failed("datasource_not_found", fmt.Sprintf("datasource %q not found", name), nil)
	}
	if !accessorHasEntity(accessor, entity) {
		return operation.Failed("datasource_entity_mismatch", "datasource entity does not match requested entity", map[string]any{
			"datasource": name,
			"entity":     entity,
		})
	}
	if entitySpec, ok := accessorEntity(accessor, entity); ok && !entitySupports(accessor, entitySpec, coredatasource.EntityCapabilityGet) {
		return operation.Failed("datasource_get_unsupported", fmt.Sprintf("datasource %q entity %q does not support get", name, entity), nil)
	}
	getter, ok := accessor.(coredatasource.Getter)
	if !ok {
		return operation.Failed("datasource_get_unsupported", fmt.Sprintf("datasource %q does not support get", name), nil)
	}
	record, err := getter.Get(ctx, coredatasource.GetRequest{Entity: entity, ID: strings.TrimSpace(input.ID)})
	if errors.Is(err, coredatasource.ErrNotFound) {
		return operation.Failed("datasource_record_not_found", err.Error(), nil)
	}
	if err != nil {
		return operation.Failed("datasource_get_failed", err.Error(), nil)
	}
	out := getOutput{Record: p.withRecordLinksForRecord(ctx, record)}
	return operation.OK(operation.Rendered{Text: renderRecord(out.Record), Data: out})
}

func (p Plugin) relation(ctx operation.Context, input relationInput) operation.Result {
	if p.registry == nil {
		return operation.Failed("datasource_registry_missing", "datasource registry is nil", nil)
	}
	name := coredatasource.Name(strings.TrimSpace(input.Datasource))
	entity := coredatasource.EntityType(strings.TrimSpace(input.Entity))
	relation := strings.TrimSpace(input.Relation)
	id := strings.TrimSpace(input.ID)
	if name == "" || entity == "" || id == "" || relation == "" {
		return operation.Failed("invalid_datasource_relation_input", "datasource, entity, id, and relation are required", nil)
	}
	if err := allowed(ctx, name); err != nil {
		return operation.Failed("datasource_relation_denied", err.Error(), nil)
	}
	accessor, ok := p.registry.Get(name)
	if !ok {
		return operation.Failed("datasource_not_found", fmt.Sprintf("datasource %q not found", name), nil)
	}
	if !accessorHasEntity(accessor, entity) {
		return operation.Failed("datasource_entity_mismatch", "datasource entity does not match requested entity", map[string]any{
			"datasource": name,
			"entity":     entity,
		})
	}
	entitySpec, ok := accessorEntity(accessor, entity)
	if ok && !entityHasRelation(entitySpec, relation) {
		return operation.Failed("datasource_relation_unsupported", fmt.Sprintf("datasource %q entity %q does not expose relation %q", name, entity, relation), nil)
	}
	relationer, ok := accessor.(coredatasource.Relationer)
	if !ok {
		return operation.Failed("datasource_relation_unsupported", fmt.Sprintf("datasource %q does not support relations", name), nil)
	}
	result, err := relationer.Relation(ctx, coredatasource.RelationRequest{
		Entity:   entity,
		ID:       id,
		Relation: relation,
		Limit:    input.Limit,
		Cursor:   strings.TrimSpace(input.Cursor),
	})
	if err != nil {
		return operation.Failed("datasource_relation_failed", err.Error(), nil)
	}
	out := relationOutput{Result: result}
	return operation.OK(operation.Rendered{Text: renderRelation(result), Data: out})
}

func (p Plugin) batchGet(ctx operation.Context, input batchGetInput) operation.Result {
	if p.registry == nil {
		return operation.Failed("datasource_registry_missing", "datasource registry is nil", nil)
	}
	name := coredatasource.Name(strings.TrimSpace(input.Datasource))
	entity := coredatasource.EntityType(strings.TrimSpace(input.Entity))
	ids := cleaned(input.IDs)
	if name == "" || entity == "" || len(ids) == 0 {
		return operation.Failed("invalid_datasource_batch_get_input", "datasource, entity, and ids are required", nil)
	}
	if err := allowed(ctx, name); err != nil {
		return operation.Failed("datasource_batch_get_denied", err.Error(), nil)
	}
	accessor, ok := p.registry.Get(name)
	if !ok {
		return operation.Failed("datasource_not_found", fmt.Sprintf("datasource %q not found", name), nil)
	}
	if !accessorHasEntity(accessor, entity) {
		return operation.Failed("datasource_entity_mismatch", "datasource entity does not match requested entity", map[string]any{
			"datasource": name,
			"entity":     entity,
		})
	}
	var result coredatasource.BatchGetResult
	if batchGetter, ok := accessor.(coredatasource.BatchGetter); ok {
		var err error
		result, err = batchGetter.BatchGet(ctx, coredatasource.BatchGetRequest{Entity: entity, IDs: ids})
		if err != nil {
			return operation.Failed("datasource_batch_get_failed", err.Error(), nil)
		}
	} else {
		getter, ok := accessor.(coredatasource.Getter)
		if !ok {
			return operation.Failed("datasource_batch_get_unsupported", fmt.Sprintf("datasource %q does not support get", name), nil)
		}
		result = coredatasource.BatchGetResult{Datasource: name, Entity: entity}
		for _, id := range ids {
			record, err := getter.Get(ctx, coredatasource.GetRequest{Entity: entity, ID: id})
			if err != nil {
				result.Errors = append(result.Errors, coredatasource.BatchGetError{ID: id, Message: err.Error()})
				continue
			}
			result.Records = append(result.Records, record)
		}
	}
	out := batchGetOutput{Result: result}
	return operation.OK(operation.Rendered{Text: renderBatchGet(result), Data: out})
}

func (p Plugin) withRecordLinks(ctx context.Context, out searchOutput) searchOutput {
	for i := range out.Results {
		for j := range out.Results[i].Records {
			out.Results[i].Records[j] = p.withRecordLinksForRecord(ctx, out.Results[i].Records[j])
		}
	}
	return out
}

func (p Plugin) withRecordLinksForRecord(ctx context.Context, record coredatasource.Record) coredatasource.Record {
	if p.registry == nil {
		return record
	}
	accessors := allowedAccessors(ctx, p.registry)
	if len(accessors) == 0 {
		return record
	}
	input := coredatasource.DetectionInput{Sources: []coredatasource.DetectionSource{recordDetectionSource(record)}, MaxRefs: maxDetectedRefs}
	links := runtimedatasource.Detect(ctx, input, accessors, runtimedatasource.DetectOptions{MaxRefs: maxDetectedRefs})
	links = removeSelfLinks(record, links)
	if len(links) == 0 {
		return record
	}
	record.Links = links
	return record
}

func recordDetectionSource(record coredatasource.Record) coredatasource.DetectionSource {
	var parts []string
	for _, value := range []string{record.ID, record.Title, record.Content, record.URL} {
		if strings.TrimSpace(value) != "" {
			parts = append(parts, value)
		}
	}
	for key, value := range record.Metadata {
		if strings.TrimSpace(value) != "" {
			parts = append(parts, key+": "+value)
		}
	}
	return coredatasource.DetectionSource{
		ID:   string(record.Datasource) + ":" + string(record.Entity) + ":" + record.ID,
		Kind: "datasource.record",
		Text: strings.Join(parts, "\n"),
	}
}

func removeSelfLinks(record coredatasource.Record, links []coredatasource.RecordRef) []coredatasource.RecordRef {
	var out []coredatasource.RecordRef
	for _, link := range links {
		if link.Datasource == record.Datasource && link.Entity == record.Entity && link.ID != "" && link.ID == record.ID {
			continue
		}
		out = append(out, link)
	}
	return out
}

type searchTarget struct {
	Accessor coredatasource.Accessor
	Entity   coredatasource.EntitySpec
}

func (p Plugin) selectedSearchTargets(ctx context.Context, entityNames []string) ([]searchTarget, error) {
	allowedNames, err := allowedSet(ctx)
	if err != nil {
		return nil, err
	}
	matchers := entityMatchers(entityNames)
	var candidates []searchTarget
	for _, accessor := range p.registry.All() {
		spec := accessor.Spec()
		if !allowedNames[spec.Name] {
			continue
		}
		for _, entity := range accessor.Entities() {
			if entity.Type == "" {
				continue
			}
			if entitySupports(accessor, entity, coredatasource.EntityCapabilitySearch) {
				candidates = append(candidates, searchTarget{Accessor: accessor, Entity: entity})
			}
		}
	}
	if len(matchers) == 0 && countEntityTypes(candidates) > 1 {
		return nil, fmt.Errorf("entities filter is required because more than one searchable entity is available; valid filters: %s", strings.Join(validEntityFilters(candidates), ", "))
	}
	var out []searchTarget
	for _, candidate := range candidates {
		if len(matchers) > 0 && !matchesEntity(matchers, candidate.Entity.Type) {
			continue
		}
		out = append(out, candidate)
	}
	if len(out) == 0 {
		if len(candidates) > 0 {
			return nil, fmt.Errorf("no allowed searchable datasource entities match the request; valid filters: %s", strings.Join(validEntityFilters(candidates), ", "))
		}
		return nil, fmt.Errorf("no allowed searchable datasource entities match the request")
	}
	return out, nil
}

func accessorHasEntity(accessor coredatasource.Accessor, typ coredatasource.EntityType) bool {
	_, ok := accessorEntity(accessor, typ)
	return ok
}

func accessorEntity(accessor coredatasource.Accessor, typ coredatasource.EntityType) (coredatasource.EntitySpec, bool) {
	for _, entity := range accessor.Entities() {
		if entity.Type == typ {
			return entity, true
		}
	}
	return coredatasource.EntitySpec{}, false
}

func entitySupports(accessor coredatasource.Accessor, entity coredatasource.EntitySpec, capability coredatasource.EntityCapability) bool {
	if len(entity.Capabilities) > 0 {
		return entity.Supports(capability)
	}
	switch capability {
	case coredatasource.EntityCapabilitySearch:
		_, ok := accessor.(coredatasource.Searcher)
		return ok
	case coredatasource.EntityCapabilityGet:
		_, ok := accessor.(coredatasource.Getter)
		return ok
	case coredatasource.EntityCapabilityRelation:
		_, ok := accessor.(coredatasource.Relationer)
		return ok && len(entity.Relations) > 0
	default:
		return false
	}
}

func entityCapabilities(accessor coredatasource.Accessor, entity coredatasource.EntitySpec) []coredatasource.EntityCapability {
	var out []coredatasource.EntityCapability
	for _, capability := range []coredatasource.EntityCapability{coredatasource.EntityCapabilitySearch, coredatasource.EntityCapabilityGet, coredatasource.EntityCapabilityRelation} {
		if entitySupports(accessor, entity, capability) {
			out = append(out, capability)
		}
	}
	return out
}

func entityHasRelation(entity coredatasource.EntitySpec, name string) bool {
	name = strings.TrimSpace(name)
	for _, relation := range entity.Relations {
		if relation.Name == name {
			return true
		}
	}
	return false
}

func countEntityTypes(targets []searchTarget) int {
	seen := map[coredatasource.EntityType]bool{}
	for _, target := range targets {
		seen[target.Entity.Type] = true
	}
	return len(seen)
}

func validEntityFilters(targets []searchTarget) []string {
	exact := map[string]bool{}
	wildcards := map[string]bool{}
	for _, target := range targets {
		entity := string(target.Entity.Type)
		exact[entity] = true
		if prefix, _, ok := strings.Cut(entity, "."); ok && prefix != "" {
			wildcards[prefix+".*"] = true
		}
	}
	out := make([]string, 0, len(exact)+len(wildcards))
	for entity := range exact {
		out = append(out, entity)
	}
	for wildcard := range wildcards {
		out = append(out, wildcard)
	}
	sort.Strings(out)
	return out
}

func searchMode(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "auto":
		return "auto"
	case "lexical", "keyword":
		return "lexical"
	case "provider", "live":
		return "provider"
	case "semantic", "vector":
		return "semantic"
	case "hybrid":
		return "hybrid"
	default:
		return "auto"
	}
}

func allowed(ctx context.Context, name coredatasource.Name) error {
	set, err := allowedSet(ctx)
	if err != nil {
		return err
	}
	if !set[name] {
		return fmt.Errorf("datasource %q is not allowed for this agent", name)
	}
	return nil
}

func allowedAccessors(ctx context.Context, registry *coredatasource.Registry) []coredatasource.Accessor {
	if registry == nil {
		return nil
	}
	allowedNames, err := allowedSet(ctx)
	if err != nil {
		return nil
	}
	var out []coredatasource.Accessor
	for _, accessor := range registry.All() {
		spec := accessor.Spec()
		if allowedNames[spec.Name] {
			out = append(out, accessor)
		}
	}
	return out
}

func allowedSet(ctx context.Context) (map[coredatasource.Name]bool, error) {
	policy, ok := coredatasource.AccessPolicyFromContext(ctx)
	if !ok || len(policy.Datasources) == 0 {
		return nil, fmt.Errorf("no datasources are allowed for this agent")
	}
	out := map[coredatasource.Name]bool{}
	for _, name := range policy.Datasources {
		if name != "" {
			out[name] = true
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no datasources are allowed for this agent")
	}
	return out, nil
}

type entityMatcher struct {
	exact  coredatasource.EntityType
	prefix string
	all    bool
}

func entityMatchers(values []string) []entityMatcher {
	var out []entityMatcher
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			switch {
			case value == "*":
				out = append(out, entityMatcher{all: true})
			case strings.HasSuffix(value, ".*"):
				out = append(out, entityMatcher{prefix: strings.TrimSuffix(value, "*")})
			default:
				out = append(out, entityMatcher{exact: coredatasource.EntityType(value)})
			}
		}
	}
	return out
}

func matchesEntity(matchers []entityMatcher, typ coredatasource.EntityType) bool {
	for _, matcher := range matchers {
		switch {
		case matcher.all:
			return true
		case matcher.exact != "" && matcher.exact == typ:
			return true
		case matcher.prefix != "" && strings.HasPrefix(string(typ), matcher.prefix):
			return true
		}
	}
	return false
}

func cleaned(values []string) []string {
	var out []string
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			out = append(out, value)
		}
	}
	return out
}

func renderSearch(out searchOutput) string {
	var lines []string
	total := 0
	for _, result := range out.Results {
		count := len(result.Records)
		if count == 0 {
			continue
		}
		lines = append(lines, fmt.Sprintf("%s from %s: %s", result.Entity, result.Datasource, plural(count, "result")))
		for _, record := range result.Records {
			total++
			line := renderSearchRecord(result, record)
			if record.URL != "" {
				line += " (" + record.URL + ")"
			}
			if record.Content != "" {
				line += ": " + record.Content
			}
			lines = append(lines, line)
			if len(record.Links) > 0 {
				lines = append(lines, "  related: "+renderRefsInline(record.Links))
			}
		}
	}
	if total == 0 {
		lines = append(lines, "No datasource records found.")
	}
	if len(out.Errors) > 0 {
		lines = append(lines, "Partial errors:")
		for _, err := range out.Errors {
			label := err.Datasource
			if err.Entity != "" {
				label += "/" + err.Entity
			}
			lines = append(lines, "- "+label+": "+err.Message)
		}
	}
	return strings.Join(lines, "\n")
}

func renderSearchRecord(result coredatasource.SearchResult, record coredatasource.Record) string {
	entity := record.Entity
	if entity == "" {
		entity = result.Entity
	}
	label := string(entity)
	if record.ID != "" {
		label += " " + record.ID
	}
	line := "- " + strings.TrimSpace(label)
	if record.Title != "" && record.Title != record.ID {
		line += " - " + record.Title
	}
	if metadata := renderSlackMessageMetadata(record); metadata != "" {
		line += " [" + metadata + "]"
	}
	if strings.TrimSpace(line) == "-" {
		line = "- " + record.Title
	}
	if strings.TrimSpace(line) == "-" {
		line = "- " + record.ID
	}
	return line
}

func renderSlackMessageMetadata(record coredatasource.Record) string {
	if record.Entity != "slack.message" {
		return ""
	}
	channel := strings.TrimSpace(record.Metadata["channel"])
	channelID := strings.TrimSpace(record.Metadata["channel_id"])
	permalink := strings.TrimSpace(firstMetadataNonEmpty(record.Metadata["permalink"], record.URL))
	var parts []string
	if channel != "" || channelID != "" {
		label := ""
		if channel != "" {
			label = slackChannelLabel(channel)
		}
		if channelID != "" {
			if label != "" {
				label += " (" + channelID + ")"
			} else {
				label = channelID
			}
		}
		parts = append(parts, "channel="+label)
	}
	if permalink != "" {
		parts = append(parts, "message="+permalink)
	}
	return strings.Join(parts, "; ")
}

func slackChannelLabel(channel string) string {
	channel = strings.TrimSpace(channel)
	if channel == "" || strings.HasPrefix(channel, "#") {
		return channel
	}
	return "#" + channel
}

func firstMetadataNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func plural(count int, singular string) string {
	if count == 1 {
		return "1 " + singular
	}
	return fmt.Sprintf("%d %ss", count, singular)
}

func renderRecord(record coredatasource.Record) string {
	var parts []string
	if record.Title != "" {
		parts = append(parts, record.Title)
	}
	if record.URL != "" {
		parts = append(parts, record.URL)
	}
	if record.Content != "" {
		parts = append(parts, record.Content)
	}
	if len(record.Links) > 0 {
		parts = append(parts, "Related refs: "+renderRefsInline(record.Links))
	}
	if len(parts) == 0 {
		return record.ID
	}
	return strings.Join(parts, "\n")
}

func renderRelation(result coredatasource.RelationResult) string {
	var parts []string
	mode := "inferred"
	if result.Exact {
		mode = "exact"
	}
	status := "partial"
	if result.Complete {
		status = "complete"
	}
	header := fmt.Sprintf("%s %s %s for %s %s from %s: %s, %s", result.Entity, result.ID, result.Relation, result.TargetEntity, plural(len(result.Records), "record"), result.Datasource, mode, status)
	parts = append(parts, header)
	for _, record := range result.Records {
		line := "- " + record.ID
		if record.Title != "" && record.Title != record.ID {
			line += " - " + record.Title
		}
		if record.URL != "" {
			line += " (" + record.URL + ")"
		}
		parts = append(parts, line)
	}
	if result.NextCursor != "" {
		parts = append(parts, "next_cursor: "+result.NextCursor)
	}
	return strings.Join(parts, "\n")
}

func renderBatchGet(result coredatasource.BatchGetResult) string {
	var lines []string
	lines = append(lines, fmt.Sprintf("%s from %s: %s", result.Entity, result.Datasource, plural(len(result.Records), "record")))
	for _, record := range result.Records {
		line := "- " + record.ID
		if record.Title != "" && record.Title != record.ID {
			line += " - " + record.Title
		}
		lines = append(lines, line)
	}
	if len(result.Errors) > 0 {
		lines = append(lines, "Errors:")
		for _, err := range result.Errors {
			lines = append(lines, "- "+err.ID+": "+err.Message)
		}
	}
	return strings.Join(lines, "\n")
}

func renderRefsInline(refs []coredatasource.RecordRef) string {
	values := make([]string, 0, len(refs))
	for _, ref := range refs {
		values = append(values, renderRefLabel(ref))
	}
	sort.Strings(values)
	return strings.Join(values, "; ")
}

func renderRefLabel(ref coredatasource.RecordRef) string {
	label := string(ref.Entity)
	switch {
	case ref.ID != "":
		label += " " + ref.ID
	case ref.Query != "":
		label += " query " + ref.Query
	case ref.URL != "":
		label += " " + ref.URL
	}
	if ref.Datasource != "" {
		label += " from " + string(ref.Datasource)
	}
	return strings.TrimSpace(label)
}

type catalogProvider struct {
	registry *coredatasource.Registry
}

func (p catalogProvider) Spec() corecontext.ProviderSpec {
	return contextSpec()
}

func (p catalogProvider) Build(ctx context.Context, _ corecontext.Request) ([]corecontext.Block, error) {
	if p.registry == nil {
		return nil, nil
	}
	allowedNames, err := allowedSet(ctx)
	if err != nil {
		return nil, nil
	}
	var datasources []catalogDatasource
	var lines []string
	for _, accessor := range p.registry.All() {
		spec := accessor.Spec()
		if !allowedNames[spec.Name] {
			continue
		}
		entry := catalogDatasource{
			Name:        string(spec.Name),
			Description: spec.Description,
			Kind:        spec.Kind,
			Connector:   spec.Connector,
		}
		for _, entity := range accessor.Entities() {
			var capabilities []string
			for _, capability := range entityCapabilities(accessor, entity) {
				capabilities = append(capabilities, string(capability))
			}
			entry.Entities = append(entry.Entities, catalogEntity{Type: string(entity.Type), Description: entity.Description, Capabilities: capabilities, Relations: catalogRelations(entity.Relations)})
		}
		if len(entry.Entities) == 0 {
			continue
		}
		sort.Slice(entry.Entities, func(i, j int) bool { return entry.Entities[i].Type < entry.Entities[j].Type })
		datasources = append(datasources, entry)
		lines = append(lines, renderCatalogLine(entry))
	}
	if len(datasources) == 0 {
		return nil, nil
	}
	sort.Strings(lines)
	sort.Slice(datasources, func(i, j int) bool { return datasources[i].Name < datasources[j].Name })
	data, _ := json.Marshal(datasources)
	content := "Available datasources for this agent:\n" + strings.Join(lines, "\n") + "\nUse datasource_search with entity filters such as entities=[\"jira.issue\"] or entities=[\"jira.*\"]."
	return []corecontext.Block{{
		ID:        ContextProvider,
		Provider:  ContextProvider,
		Kind:      corecontext.BlockText,
		Title:     "Available Datasources",
		Content:   content,
		MediaType: "text/plain",
		Freshness: corecontext.FreshnessDynamic,
		Metadata: map[string]string{
			"datasources": string(data),
		},
	}}, nil
}

type catalogDatasource struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Kind        string          `json:"kind,omitempty"`
	Connector   string          `json:"connector,omitempty"`
	Entities    []catalogEntity `json:"entities,omitempty"`
}

type catalogEntity struct {
	Type         string            `json:"type"`
	Description  string            `json:"description,omitempty"`
	Capabilities []string          `json:"capabilities,omitempty"`
	Relations    []catalogRelation `json:"relations,omitempty"`
}

type catalogRelation struct {
	Name         string `json:"name"`
	TargetEntity string `json:"target_entity"`
	Exact        bool   `json:"exact,omitempty"`
	Description  string `json:"description,omitempty"`
}

func catalogRelations(relations []coredatasource.RelationSpec) []catalogRelation {
	out := make([]catalogRelation, 0, len(relations))
	for _, relation := range relations {
		out = append(out, catalogRelation{
			Name:         relation.Name,
			TargetEntity: string(relation.TargetEntity),
			Exact:        relation.Exact,
			Description:  relation.Description,
		})
	}
	return out
}

func renderCatalogLine(entry catalogDatasource) string {
	var entities []string
	for _, entity := range entry.Entities {
		label := entity.Type
		if len(entity.Capabilities) > 0 {
			label += " [" + strings.Join(entity.Capabilities, ",") + "]"
		}
		if len(entity.Relations) > 0 {
			var relations []string
			for _, relation := range entity.Relations {
				relationLabel := relation.Name + "->" + relation.TargetEntity
				if relation.Exact {
					relationLabel += " exact"
				}
				relations = append(relations, relationLabel)
			}
			label += " relations[" + strings.Join(relations, ",") + "]"
		}
		if entity.Description != "" {
			entities = append(entities, label+" - "+entity.Description)
			continue
		}
		entities = append(entities, label)
	}
	prefix := "- " + entry.Name
	var details []string
	if entry.Kind != "" {
		details = append(details, "kind "+entry.Kind)
	}
	if entry.Connector != "" {
		details = append(details, "connector "+entry.Connector)
	}
	if len(details) > 0 {
		prefix += " (" + strings.Join(details, ", ") + ")"
	}
	return prefix + ": " + strings.Join(entities, "; ")
}

type detectedProvider struct {
	registry *coredatasource.Registry
}

func (p detectedProvider) Spec() corecontext.ProviderSpec {
	return detectedContextSpec()
}

func (p detectedProvider) Build(ctx context.Context, _ corecontext.Request) ([]corecontext.Block, error) {
	if p.registry == nil {
		return nil, nil
	}
	input, ok := coredatasource.DetectionInputFromContext(ctx)
	if !ok || len(input.Sources) == 0 {
		return nil, nil
	}
	accessors := allowedAccessors(ctx, p.registry)
	if len(accessors) == 0 {
		return nil, nil
	}
	refs := runtimedatasource.Detect(ctx, input, accessors, runtimedatasource.DetectOptions{MaxRefs: maxDetectedRefs})
	if len(refs) == 0 {
		return nil, nil
	}
	content := renderDetectedRefs(ctx, p.registry, refs)
	data, _ := json.Marshal(refs)
	return []corecontext.Block{{
		ID:        DetectedProvider,
		Provider:  DetectedProvider,
		Kind:      corecontext.BlockText,
		Title:     "Detected Datasource References",
		Content:   content,
		MediaType: "text/plain",
		Freshness: corecontext.FreshnessDynamic,
		Metadata: map[string]string{
			"references": string(data),
		},
	}}, nil
}

func renderDetectedRefs(ctx context.Context, registry *coredatasource.Registry, refs []coredatasource.RecordRef) string {
	lines := []string{"Detected datasource references from the current message:"}
	for _, ref := range refs {
		label := "- " + renderRefLabel(ref)
		if capabilityText := refCapabilityText(ctx, registry, ref); capabilityText != "" {
			label += " [" + capabilityText + "]"
		}
		if ref.SourceText != "" {
			label += ` from "` + compactInline(ref.SourceText, 120) + `"`
		}
		lines = append(lines, label)
	}
	return strings.Join(lines, "\n")
}

func refCapabilityText(ctx context.Context, registry *coredatasource.Registry, ref coredatasource.RecordRef) string {
	if registry == nil || ref.Datasource == "" || ref.Entity == "" {
		return ""
	}
	accessor, ok := registry.Get(ref.Datasource)
	if !ok {
		return ""
	}
	if err := allowed(ctx, ref.Datasource); err != nil {
		return ""
	}
	entity, ok := accessorEntity(accessor, ref.Entity)
	if !ok {
		return ""
	}
	capabilities := entityCapabilities(accessor, entity)
	labels := make([]string, 0, len(capabilities)+1)
	for _, capability := range capabilities {
		labels = append(labels, string(capability))
	}
	if ref.ID == "" {
		labels = append(labels, "candidate")
	}
	sort.Strings(labels)
	return strings.Join(labels, ",")
}

func compactInline(text string, max int) string {
	text = strings.Join(strings.Fields(text), " ")
	if max <= 0 || len(text) <= max {
		return text
	}
	return text[:max]
}
