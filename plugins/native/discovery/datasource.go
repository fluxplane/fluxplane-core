package discovery

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"

	coredata "github.com/fluxplane/fluxplane-core/core/data"
	coredatasource "github.com/fluxplane/fluxplane-core/core/datasource"
	corediscovery "github.com/fluxplane/fluxplane-core/core/discovery"
	coreendpoint "github.com/fluxplane/fluxplane-core/core/endpoint"
	"github.com/fluxplane/fluxplane-core/orchestration/pluginhost"
	runtimedata "github.com/fluxplane/fluxplane-core/runtime/data"
	runtimedatasource "github.com/fluxplane/fluxplane-core/runtime/datasource"
	runtimediscovery "github.com/fluxplane/fluxplane-core/runtime/discovery"
	runtimeendpoint "github.com/fluxplane/fluxplane-core/runtime/endpoint"
)

const (
	EndpointDatasourceName  coredatasource.Name       = "endpoint"
	EndpointRecordEntity    coredatasource.EntityType = "endpoint.record"
	EndpointCandidateEntity coredatasource.EntityType = "endpoint.candidate"
)

type EndpointRecord struct {
	ID        string                 `json:"id" datasource:"id" jsonschema:"description=Endpoint ref."`
	URL       string                 `json:"url,omitempty" datasource:"url,searchable" jsonschema:"description=Resolved endpoint URL."`
	Product   string                 `json:"product,omitempty" datasource:"filterable,searchable" jsonschema:"description=Endpoint product such as mysql or loki."`
	Protocol  string                 `json:"protocol,omitempty" datasource:"filterable,searchable" jsonschema:"description=Endpoint protocol."`
	Provider  string                 `json:"provider,omitempty" datasource:"filterable,searchable" jsonschema:"description=Discovery provider that owns the endpoint."`
	Namespace string                 `json:"namespace,omitempty" datasource:"filterable,searchable" jsonschema:"description=Source namespace when available."`
	Source    coreendpoint.SourceRef `json:"source,omitempty" datasource:"object" jsonschema:"description=Non-secret endpoint source."`
	Metadata  map[string]string      `json:"metadata,omitempty" datasource:"object" jsonschema:"description=Non-secret endpoint metadata."`
	ExpiresAt string                 `json:"expires_at,omitempty" datasource:"filterable" jsonschema:"description=Expiration timestamp for discovered endpoint records."`
}

type EndpointCandidate struct {
	ID            string                 `json:"id" datasource:"id" jsonschema:"description=Stable endpoint candidate id."`
	URL           string                 `json:"url,omitempty" datasource:"url,searchable" jsonschema:"description=Candidate endpoint URL."`
	Product       string                 `json:"product,omitempty" datasource:"filterable,searchable" jsonschema:"description=Candidate product hint."`
	Protocol      string                 `json:"protocol,omitempty" datasource:"filterable,searchable" jsonschema:"description=Candidate protocol."`
	Provider      string                 `json:"provider,omitempty" datasource:"filterable,searchable" jsonschema:"description=Discovery provider name."`
	Namespace     string                 `json:"namespace,omitempty" datasource:"filterable,searchable" jsonschema:"description=Source namespace when available."`
	Source        coreendpoint.SourceRef `json:"source,omitempty" datasource:"object" jsonschema:"description=Non-secret candidate source."`
	Score         float64                `json:"score,omitempty" datasource:"filterable" jsonschema:"description=Candidate confidence score."`
	CredentialRef string                 `json:"credential_ref,omitempty" jsonschema:"description=Credential reference associated with the endpoint candidate."`
	Labels        map[string]string      `json:"labels,omitempty" datasource:"object" jsonschema:"description=Candidate labels."`
	Annotations   map[string]string      `json:"annotations,omitempty" datasource:"object" jsonschema:"description=Candidate annotations."`
	Reasons       []string               `json:"reasons,omitempty" datasource:"array" jsonschema:"description=Candidate match reasons."`
}

var _ pluginhost.DatasourceProviderContributor = Plugin{}

type endpointDatasourceProvider struct {
	discovery *runtimediscovery.Registry
	endpoints *runtimeendpoint.Registry
}

type endpointAccessor struct {
	spec      coredatasource.Spec
	entities  []coredatasource.EntitySpec
	discovery *runtimediscovery.Registry
	endpoints *runtimeendpoint.Registry
}

func EndpointDatasourceSpec() coredatasource.Spec {
	return coredatasource.Spec{
		Name:        EndpointDatasourceName,
		Description: "Runtime endpoint registry and live endpoint discovery candidates.",
		Kind:        string(EndpointDatasourceName),
		Entities:    []coredatasource.EntityType{EndpointRecordEntity, EndpointCandidateEntity},
	}
}

func EndpointDataSourceSpec() coredata.SourceSpec {
	return runtimedata.SourceFromDatasource(coredata.SourceName(EndpointDatasourceName), string(EndpointDatasourceName), endpointEntitySpecs())
}

func (p Plugin) DatasourceProviders(_ context.Context, ctx pluginhost.Context) ([]coredatasource.Provider, error) {
	discovery := p.discovery
	if discovery == nil {
		discovery = ctx.Discovery
	}
	endpoints := p.endpoints
	if endpoints == nil {
		endpoints = ctx.Endpoints
	}
	return []coredatasource.Provider{endpointDatasourceProvider{discovery: discovery, endpoints: endpoints}}, nil
}

func (p endpointDatasourceProvider) Entities() []coredatasource.EntitySpec {
	return endpointEntitySpecs()
}

func (p endpointDatasourceProvider) Open(_ context.Context, spec coredatasource.Spec) (coredatasource.Accessor, error) {
	if spec.Kind != string(EndpointDatasourceName) && spec.Name != EndpointDatasourceName {
		return nil, fmt.Errorf("unsupported datasource kind %q", spec.Kind)
	}
	selected := endpointEntitySpecs()
	if len(spec.Entities) > 0 {
		var err error
		selected, err = runtimedatasource.SelectEntities(string(EndpointDatasourceName), endpointEntitySpecs(), spec.Entities)
		if err != nil {
			return nil, err
		}
	}
	return &endpointAccessor{spec: spec, entities: selected, discovery: p.discovery, endpoints: p.endpoints}, nil
}

func (a *endpointAccessor) Spec() coredatasource.Spec { return a.spec }
func (a *endpointAccessor) Entities() []coredatasource.EntitySpec {
	return append([]coredatasource.EntitySpec(nil), a.entities...)
}

func (a *endpointAccessor) Search(ctx context.Context, req coredatasource.SearchRequest) (coredatasource.SearchResult, error) {
	entity := req.Entity
	if entity == "" {
		entity = EndpointRecordEntity
	}
	if !runtimedatasource.HasEntity(a.entities, entity) {
		return coredatasource.SearchResult{}, fmt.Errorf("datasource %q does not expose entity %q", a.spec.Name, entity)
	}
	switch entity {
	case EndpointRecordEntity:
		records := a.endpointRecords(req.Filters)
		records = filterEndpointRecords(records, req.Query, req.Filters)
		total := len(records)
		records = limitEndpointRecords(records, req.Limit)
		return coredatasource.SearchResult{Datasource: a.spec.Name, Entity: entity, Records: records, Total: total}, nil
	case EndpointCandidateEntity:
		return a.searchCandidates(ctx, req)
	default:
		return coredatasource.SearchResult{}, fmt.Errorf("entity %q does not support search", entity)
	}
}

func (a *endpointAccessor) List(_ context.Context, req coredatasource.ListRequest) (coredatasource.ListResult, error) {
	entity := req.Entity
	if entity == "" {
		entity = EndpointRecordEntity
	}
	if entity != EndpointRecordEntity {
		return coredatasource.ListResult{}, fmt.Errorf("entity %q does not support list", entity)
	}
	records := filterEndpointRecords(a.endpointRecords(req.Filters), "", req.Filters)
	total := len(records)
	records = limitEndpointRecords(records, req.Limit)
	return coredatasource.ListResult{Datasource: a.spec.Name, Entity: entity, Records: records, Total: total, Complete: true}, nil
}

func (a *endpointAccessor) Get(_ context.Context, req coredatasource.GetRequest) (coredatasource.Record, error) {
	if req.Entity != EndpointRecordEntity {
		return coredatasource.Record{}, fmt.Errorf("entity %q does not support get", req.Entity)
	}
	if a.endpoints == nil {
		return coredatasource.Record{}, coredatasource.ErrNotFound
	}
	resolved, ok := a.endpoints.Resolve(coreendpoint.NewRef(req.ID))
	if !ok {
		return coredatasource.Record{}, coredatasource.ErrNotFound
	}
	return a.recordFromResolved(resolved), nil
}

func (a *endpointAccessor) searchCandidates(ctx context.Context, req coredatasource.SearchRequest) (coredatasource.SearchResult, error) {
	if a.discovery == nil {
		return coredatasource.SearchResult{}, fmt.Errorf("discovery registry is nil")
	}
	filters := cloneMap(req.Filters)
	query := cloneMap(filters)
	if strings.TrimSpace(req.Query) != "" {
		query["query"] = strings.TrimSpace(req.Query)
	}
	product := firstNonEmpty(filters["product"], filters["products"])
	result, err := a.discovery.Discover(ctx, corediscovery.Request{
		Product:   product,
		Providers: splitCSV(filters["provider"]),
		Query:     query,
		Limit:     req.Limit,
	})
	if err != nil {
		return coredatasource.SearchResult{}, err
	}
	records := make([]coredatasource.Record, 0, len(result.Candidates))
	for _, candidate := range result.Candidates {
		record := candidateRecord(a.spec.Name, candidate, filters["provider"])
		if !endpointRecordMatchesFilters(record, filters) || !endpointRecordMatches(record, req.Query) {
			continue
		}
		records = append(records, record)
	}
	return coredatasource.SearchResult{Datasource: a.spec.Name, Entity: EndpointCandidateEntity, Records: records, Total: len(records)}, nil
}

func (a *endpointAccessor) endpointRecords(filters map[string]string) []coredatasource.Record {
	if a.endpoints == nil {
		return nil
	}
	records := a.endpoints.List(filters["product"])
	out := make([]coredatasource.Record, 0, len(records))
	for _, record := range records {
		out = append(out, registryRecord(a.spec.Name, record))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func (a *endpointAccessor) recordFromResolved(resolved coreendpoint.Resolved) coredatasource.Record {
	record := runtimeendpoint.Record{Resolved: resolved, Metadata: cloneMap(resolved.Metadata), Source: resolved.Source}
	return registryRecord(a.spec.Name, record)
}

func registryRecord(datasource coredatasource.Name, record runtimeendpoint.Record) coredatasource.Record {
	resolved := record.Resolved
	if resolved.URL == "" {
		resolved.URL = record.Spec.URL
	}
	metadata := cloneMap(record.Metadata)
	if len(metadata) == 0 {
		metadata = cloneMap(resolved.Metadata)
	}
	product := firstNonEmpty(record.Spec.Product, metadata["product"])
	protocol := firstNonEmpty(record.Spec.Protocol, metadata["protocol"])
	provider := metadata["provider"]
	raw := EndpointRecord{ID: string(resolved.Ref), URL: resolved.URL, Product: product, Protocol: protocol, Provider: provider, Namespace: resolved.Source.Namespace, Source: resolved.Source, Metadata: metadata, ExpiresAt: resolved.ExpiresAt}
	meta := cloneMap(metadata)
	if meta == nil {
		meta = map[string]string{}
	}
	meta["product"] = product
	meta["protocol"] = protocol
	meta["provider"] = provider
	meta["namespace"] = raw.Namespace
	meta["source_kind"] = resolved.Source.Kind
	meta["source_name"] = resolved.Source.Name
	return coredatasource.Record{ID: raw.ID, Datasource: datasource, Entity: EndpointRecordEntity, Title: firstNonEmpty(product, resolved.URL, raw.ID), Content: strings.Join(nonEmpty(raw.URL, raw.Product, raw.Protocol, raw.Provider, raw.Namespace, resolved.Source.Kind, resolved.Source.Name), " "), URL: raw.URL, Metadata: cleanMap(meta), Raw: raw}
}

func candidateRecord(datasource coredatasource.Name, candidate corediscovery.Candidate, provider string) coredatasource.Record {
	product := candidate.ProductHint
	protocol := firstNonEmpty(candidate.Protocol, candidate.Scheme)
	raw := EndpointCandidate{ID: candidate.ID, URL: candidate.URL, Product: product, Protocol: protocol, Provider: provider, Namespace: candidate.Source.Namespace, Source: candidate.Source, Score: candidate.Score, CredentialRef: candidate.AuthRef, Labels: candidate.Labels, Annotations: candidate.Annotations, Reasons: candidate.Reasons}
	meta := map[string]string{"product": product, "protocol": protocol, "provider": provider, "namespace": raw.Namespace, "source_kind": candidate.Source.Kind, "source_name": candidate.Source.Name}
	if candidate.Score != 0 {
		meta["score"] = strconv.FormatFloat(candidate.Score, 'f', 2, 64)
	}
	return coredatasource.Record{ID: raw.ID, Datasource: datasource, Entity: EndpointCandidateEntity, Title: firstNonEmpty(product, candidate.URL, candidate.ID), Content: strings.Join(append(nonEmpty(candidate.URL, product, protocol, provider, raw.Namespace, candidate.Source.Kind, candidate.Source.Name), candidate.Reasons...), " "), URL: candidate.URL, Score: candidate.Score, Metadata: cleanMap(meta), Raw: raw}
}

func endpointEntitySpecs() []coredatasource.EntitySpec {
	record := runtimedatasource.EntityOf[EndpointRecord](EndpointRecordEntity, "Registered runtime endpoint record.")
	record.Capabilities = []coredatasource.EntityCapability{coredatasource.EntityCapabilitySearch, coredatasource.EntityCapabilityList, coredatasource.EntityCapabilityGet}
	candidate := runtimedatasource.EntityOf[EndpointCandidate](EndpointCandidateEntity, "Live endpoint discovery candidate.")
	candidate.Capabilities = []coredatasource.EntityCapability{coredatasource.EntityCapabilitySearch}
	return []coredatasource.EntitySpec{record, candidate}
}

func filterEndpointRecords(records []coredatasource.Record, query string, filters map[string]string) []coredatasource.Record {
	out := records[:0]
	for _, record := range records {
		if endpointRecordMatches(record, query) && endpointRecordMatchesFilters(record, filters) {
			out = append(out, record)
		}
	}
	return out
}

func endpointRecordMatches(record coredatasource.Record, query string) bool {
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		return true
	}
	values := []string{record.ID, record.Title, record.Content, record.URL}
	for k, v := range record.Metadata {
		values = append(values, k, v)
	}
	for _, value := range values {
		if strings.Contains(strings.ToLower(value), query) {
			return true
		}
	}
	return false
}

func endpointRecordMatchesFilters(record coredatasource.Record, filters map[string]string) bool {
	for key, want := range filters {
		want = strings.ToLower(strings.TrimSpace(want))
		if want == "" || key == "query" || key == "products" {
			continue
		}
		if key == "provider" && strings.Contains(want, ",") {
			allowed := map[string]bool{}
			for _, item := range splitCSV(want) {
				allowed[strings.ToLower(item)] = true
			}
			if !allowed[strings.ToLower(record.Metadata[key])] {
				return false
			}
			continue
		}
		if strings.ToLower(strings.TrimSpace(record.Metadata[key])) != want {
			return false
		}
	}
	return true
}

func limitEndpointRecords(records []coredatasource.Record, limit int) []coredatasource.Record {
	if limit <= 0 || len(records) <= limit {
		return records
	}
	return records[:limit]
}

func splitCSV(value string) []string {
	var out []string
	for _, part := range strings.Split(value, ",") {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func nonEmpty(values ...string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			out = append(out, strings.TrimSpace(value))
		}
	}
	return out
}

func cleanMap(in map[string]string) map[string]string {
	out := map[string]string{}
	for key, value := range in {
		if strings.TrimSpace(value) != "" {
			out[key] = value
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
