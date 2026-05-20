package web

import (
	"context"
	"fmt"
	"strings"

	coredatasource "github.com/fluxplane/agentruntime/core/datasource"
	"github.com/fluxplane/agentruntime/orchestration/pluginhost"
	runtimedatasource "github.com/fluxplane/agentruntime/runtime/datasource"
	"github.com/fluxplane/agentruntime/runtime/system"
)

const SearchResultEntity coredatasource.EntityType = "web.search_result"

var _ pluginhost.DatasourceProviderContributor = Plugin{}

type SearchResult struct {
	URL     string `json:"url" datasource:"id,url,searchable" jsonschema:"description=Search result URL.,required"`
	Title   string `json:"title,omitempty" datasource:"searchable" jsonschema:"description=Search result title."`
	Snippet string `json:"snippet,omitempty" datasource:"searchable" jsonschema:"description=Search result snippet."`
	Source  string `json:"source,omitempty" datasource:"filterable" jsonschema:"description=Search provider source."`
}

// DatasourceProviders returns web-backed datasource providers.
func (p Plugin) DatasourceProviders(context.Context, pluginhost.Context) ([]coredatasource.Provider, error) {
	return []coredatasource.Provider{webSearchProvider(p)}, nil
}

type webSearchProvider struct {
	system system.System
}

func (p webSearchProvider) Entities() []coredatasource.EntitySpec {
	entity := runtimedatasource.EntityOf[SearchResult](SearchResultEntity, "Web search result.")
	entity.Capabilities = []coredatasource.EntityCapability{coredatasource.EntityCapabilitySearch}
	return []coredatasource.EntitySpec{entity}
}

func (p webSearchProvider) Open(_ context.Context, spec coredatasource.Spec) (coredatasource.Accessor, error) {
	if !specHasEntity(spec, SearchResultEntity) {
		return nil, fmt.Errorf("unsupported entities %q", spec.Entities)
	}
	if spec.Kind != "web" && spec.Kind != "websearch" && spec.Kind != "web_search" {
		return nil, fmt.Errorf("unsupported datasource kind %q", spec.Kind)
	}
	if p.system == nil {
		return nil, fmt.Errorf("web datasource system is nil")
	}
	return &webSearchAccessor{system: p.system, spec: spec, entity: p.Entities()[0]}, nil
}

type webSearchAccessor struct {
	system system.System
	spec   coredatasource.Spec
	entity coredatasource.EntitySpec
}

func (a *webSearchAccessor) Spec() coredatasource.Spec { return a.spec }

func (a *webSearchAccessor) Entities() []coredatasource.EntitySpec {
	return []coredatasource.EntitySpec{a.entity}
}

func (a *webSearchAccessor) Search(ctx context.Context, req coredatasource.SearchRequest) (coredatasource.SearchResult, error) {
	if req.Entity != SearchResultEntity {
		return coredatasource.SearchResult{}, fmt.Errorf("datasource %q does not expose entity %q", a.spec.Name, req.Entity)
	}
	query := strings.TrimSpace(req.Query)
	if query == "" {
		return coredatasource.SearchResult{}, fmt.Errorf("web search query is empty")
	}
	limit := req.Limit
	if limit <= 0 {
		limit = 10
	}
	providers, errors := selectSearchProviders(ctx, a.system, datasourceSearchProviders(a.spec.Config["providers"]))
	out := runProviderSearches(ctx, []string{query}, providers, limit, errors)
	if len(out.Results) == 0 {
		message := "web search returned no results"
		if len(out.Errors) > 0 {
			message = out.Errors[0].Message
		}
		return coredatasource.SearchResult{}, fmt.Errorf("%s", message)
	}
	records := searchResultSetsToRecords(a.spec.Name, req.Entity, out.Results)
	return coredatasource.SearchResult{Datasource: a.spec.Name, Entity: req.Entity, Records: records, Total: len(records)}, nil
}

func datasourceSearchProviders(config string) []string {
	fields := strings.FieldsFunc(config, func(r rune) bool {
		return r == ',' || r == ' ' || r == '\n' || r == '\t'
	})
	providers := make([]string, 0, len(fields))
	for _, field := range fields {
		if provider := strings.TrimSpace(field); provider != "" {
			providers = append(providers, provider)
		}
	}
	return providers
}

func searchResultSetsToRecords(datasource coredatasource.Name, entity coredatasource.EntityType, sets []searchResultSet) []coredatasource.Record {
	var records []coredatasource.Record
	for _, set := range sets {
		for _, result := range set.Results {
			if strings.TrimSpace(result.URL) == "" {
				continue
			}
			raw := result
			if strings.TrimSpace(raw.Source) == "" {
				raw.Source = set.Provider
			}
			records = append(records, coredatasource.Record{
				Datasource: datasource,
				Entity:     entity,
				ID:         result.URL,
				URL:        result.URL,
				Title:      result.Title,
				Content:    result.Snippet,
				Metadata:   map[string]string{"source": raw.Source},
				Raw:        raw,
			})
		}
	}
	return records
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func specHasEntity(spec coredatasource.Spec, entity coredatasource.EntityType) bool {
	for _, candidate := range spec.Entities {
		if candidate == entity {
			return true
		}
	}
	return false
}
