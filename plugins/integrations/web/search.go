package web

import (
	"context"
	"fmt"
	fpsystem "github.com/fluxplane/fluxplane-system"
	"strings"
	"sync"

	"github.com/fluxplane/fluxplane-core/core/operation"
	operationruntime "github.com/fluxplane/fluxplane-core/runtime/operation"
	"github.com/fluxplane/fluxplane-policy"
)

const (
	SearchProviderTavily     = "tavily"
	SearchProviderDuckDuckGo = "duckduckgo"
	defaultSearchMax         = 10
	maxSearchMax             = 20
	searchConcurrency        = 4
)

type SearchProvider interface {
	Name() string
	Available(context.Context) bool
	Search(context.Context, SearchProviderRequest) (SearchProviderResult, error)
}

type SearchProviderRequest struct {
	Query string
	Max   int
}

type SearchProviderResult struct {
	Provider string
	Query    string
	Results  []SearchResult
	Answer   string
}

type searchInput struct {
	Queries   []string `json:"queries,omitempty" jsonschema:"description=Search queries to run. Use one or more concise search queries."`
	Query     string   `json:"query,omitempty" jsonschema:"description=Single search query convenience field."`
	Providers []string `json:"providers,omitempty" jsonschema:"description=Optional provider names such as tavily or duckduckgo. Defaults to available providers."`
	Max       int      `json:"max,omitempty" jsonschema:"description=Maximum results per query/provider. Defaults to 10."`
}

type searchOutput struct {
	Results []searchResultSet `json:"results,omitempty"`
	Errors  []searchError     `json:"errors,omitempty"`
}

type searchResultSet struct {
	Provider string         `json:"provider"`
	Query    string         `json:"query"`
	Answer   string         `json:"answer,omitempty"`
	Results  []SearchResult `json:"results,omitempty"`
}

type searchError struct {
	Provider string `json:"provider,omitempty"`
	Query    string `json:"query,omitempty"`
	Message  string `json:"message"`
}

func searchSpec() operation.Spec {
	return operationruntime.WithTypedContract[searchInput, searchOutput](operation.Spec{
		Ref:         operation.Ref{Name: SearchOp},
		Description: "Search the web using available search providers and return bounded results.",
		Semantics: operation.Semantics{
			Determinism: operation.DeterminismNonDeterministic,
			Effects:     operation.EffectSet{operation.EffectNetwork, operation.EffectReadExternal},
			Idempotency: operation.IdempotencyIdempotent,
			Risk:        operation.RiskLow,
		},
	})
}

func (p Plugin) search() operationruntime.TypedResultHandler[searchInput, searchOutput] {
	return func(ctx operation.Context, req searchInput) operation.Result {
		queries := normalizeQueries(req)
		if len(queries) == 0 {
			return operation.Failed("invalid_web_search_input", "at least one query is required", nil)
		}
		max := normalizeSearchMax(req.Max)
		providers, errors := selectSearchProviders(context.Context(ctx), p.system, req.Providers)
		if len(providers) == 0 {
			if len(errors) > 0 {
				return operation.Failed("web_search_provider_unavailable", errors[0].Message, nil)
			}
			return operation.Failed("web_search_provider_unavailable", "no web search provider is available", nil)
		}

		out := runProviderSearches(context.Context(ctx), queries, providers, max, errors)
		if len(out.Results) == 0 {
			message := "web search returned no results"
			if len(out.Errors) > 0 {
				message = out.Errors[0].Message
			}
			return operation.Failed("web_search_failed", message, map[string]any{"errors": out.Errors})
		}
		return operation.OK(operation.Rendered{Text: renderSearchResults(out), Data: out})
	}
}

type searchTask struct {
	index    int
	query    string
	provider SearchProvider
}

type searchTaskResult struct {
	set searchResultSet
	err searchError
}

func runProviderSearches(ctx context.Context, queries []string, providers []SearchProvider, max int, initialErrors []searchError) searchOutput {
	var tasks []searchTask
	for _, query := range queries {
		for _, provider := range providers {
			tasks = append(tasks, searchTask{index: len(tasks), query: query, provider: provider})
		}
	}

	results := make([]searchTaskResult, len(tasks))
	workerCount := searchConcurrency
	if len(tasks) < workerCount {
		workerCount = len(tasks)
	}
	jobs := make(chan searchTask)
	var wg sync.WaitGroup
	for range workerCount {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for task := range jobs {
				result, err := task.provider.Search(ctx, SearchProviderRequest{Query: task.query, Max: max})
				if err != nil {
					results[task.index].err = searchError{Provider: task.provider.Name(), Query: task.query, Message: err.Error()}
					continue
				}
				results[task.index].set = searchResultSet{
					Provider: firstNonEmpty(result.Provider, task.provider.Name()),
					Query:    firstNonEmpty(result.Query, task.query),
					Answer:   result.Answer,
					Results:  result.Results,
				}
			}
		}()
	}
	for _, task := range tasks {
		jobs <- task
	}
	close(jobs)
	wg.Wait()

	out := searchOutput{Errors: initialErrors}
	for _, result := range results {
		if result.err.Message != "" {
			out.Errors = append(out.Errors, result.err)
			continue
		}
		out.Results = append(out.Results, result.set)
	}
	return out
}

func searchIntent(_ operation.Context, req searchInput) (operation.IntentSet, error) {
	if len(normalizeQueries(req)) == 0 {
		return operation.IntentSet{}, fmt.Errorf("at least one query is required")
	}
	providers := normalizeProviderNames(req.Providers)
	if len(providers) == 0 || providers[SearchProviderTavily] {
		return operation.IntentSet{Operations: []operation.IntentOperation{{
			Behavior:  operation.IntentNetworkFetch,
			Target:    operation.URLTarget{URL: operation.URL("https://api.tavily.com/search")},
			Role:      operation.IntentRoleNetworkTarget,
			Certainty: operation.IntentCertain,
		}}}, nil
	}
	return operation.IntentSet{Operations: []operation.IntentOperation{{
		Behavior:  operation.IntentNetworkFetch,
		Target:    operation.URLTarget{URL: operation.URL("https://api.tavily.com/search")},
		Role:      operation.IntentRoleNetworkTarget,
		Certainty: operation.IntentPotential,
	}}}, nil
}

func searchAccess(_ operation.Context, req searchInput) ([]operationruntime.AccessDescriptor, error) {
	if len(normalizeQueries(req)) == 0 {
		return nil, fmt.Errorf("at least one query is required")
	}
	providers := normalizeProviderNames(req.Providers)
	if len(providers) == 0 {
		return []operationruntime.AccessDescriptor{networkAccess(tavilySearchURL), networkAccess(duckDuckGoSearchURLTemplate)}, nil
	}
	access := make([]operationruntime.AccessDescriptor, 0, len(providers))
	if providers[SearchProviderTavily] {
		access = append(access, networkAccess(tavilySearchURL))
	}
	if providers[SearchProviderDuckDuckGo] {
		access = append(access, networkAccess(duckDuckGoSearchURLTemplate))
	}
	if len(access) == 0 {
		return []operationruntime.AccessDescriptor{networkAccess("*")}, nil
	}
	return access, nil
}

func networkAccess(target string) operationruntime.AccessDescriptor {
	return operationruntime.NetworkDescriptor(target, policy.ActionNetworkFetch)
}

func searchProviders(ctx context.Context, sys fpsystem.System) []SearchProvider {
	var providers []SearchProvider
	if tavily := newTavilySearchProvider(ctx, sys); tavily.Available(ctx) {
		providers = append(providers, tavily)
	}
	if duckduckgo := newDuckDuckGoSearchProvider(sys); duckduckgo.Available(ctx) {
		providers = append(providers, duckduckgo)
	}
	return providers
}

func selectSearchProviders(ctx context.Context, sys fpsystem.System, requested []string) ([]SearchProvider, []searchError) {
	available := searchProviders(ctx, sys)
	if len(requested) == 0 {
		return available, nil
	}
	availableByName := map[string]SearchProvider{}
	for _, provider := range available {
		availableByName[provider.Name()] = provider
	}
	var selected []SearchProvider
	var errors []searchError
	seen := map[string]bool{}
	for _, raw := range requested {
		name := normalizeProviderName(raw)
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		provider, ok := availableByName[name]
		if ok && provider.Available(ctx) {
			selected = append(selected, provider)
			continue
		}
		switch name {
		case SearchProviderTavily:
			errors = append(errors, searchError{Provider: name, Message: "web search provider \"tavily\" is not available; TAVILY_API_KEY is not set"})
		case SearchProviderDuckDuckGo:
			errors = append(errors, searchError{Provider: name, Message: "web search provider \"duckduckgo\" is not available; network is not configured"})
		default:
			errors = append(errors, searchError{Provider: name, Message: fmt.Sprintf("unknown web search provider %q", name)})
		}
	}
	return selected, errors
}

func normalizeQueries(req searchInput) []string {
	seen := map[string]bool{}
	var out []string
	appendQuery := func(query string) {
		query = strings.TrimSpace(query)
		if query == "" || seen[query] {
			return
		}
		seen[query] = true
		out = append(out, query)
	}
	appendQuery(req.Query)
	for _, query := range req.Queries {
		appendQuery(query)
	}
	return out
}

func normalizeSearchMax(max int) int {
	if max <= 0 {
		return defaultSearchMax
	}
	if max > maxSearchMax {
		return maxSearchMax
	}
	return max
}

func normalizeProviderNames(providers []string) map[string]bool {
	out := map[string]bool{}
	for _, provider := range providers {
		if name := normalizeProviderName(provider); name != "" {
			out[name] = true
		}
	}
	return out
}

func normalizeProviderName(provider string) string {
	return strings.ToLower(strings.TrimSpace(provider))
}

func renderSearchResults(out searchOutput) string {
	var b strings.Builder
	b.WriteString("Web search results")
	for _, set := range out.Results {
		fmt.Fprintf(&b, "\n\nQuery: %s\nProvider: %s", set.Query, set.Provider)
		if strings.TrimSpace(set.Answer) != "" {
			fmt.Fprintf(&b, "\nAnswer: %s", strings.TrimSpace(set.Answer))
		}
		for i, result := range set.Results {
			fmt.Fprintf(&b, "\n%d. %s\n   %s", i+1, firstNonEmpty(result.Title, result.URL), result.URL)
			if strings.TrimSpace(result.Snippet) != "" {
				fmt.Fprintf(&b, "\n   %s", strings.TrimSpace(result.Snippet))
			}
		}
	}
	if len(out.Errors) > 0 {
		b.WriteString("\n\nErrors")
		for _, err := range out.Errors {
			label := err.Provider
			if err.Query != "" {
				label = strings.TrimSpace(label + " " + err.Query)
			}
			if label == "" {
				label = "search"
			}
			fmt.Fprintf(&b, "\n- %s: %s", label, err.Message)
		}
	}
	return strings.TrimSpace(b.String())
}

func env(ctx context.Context, sys fpsystem.System, key string) string {
	if sys == nil || sys.Environment() == nil {
		return ""
	}
	value, ok, err := sys.Environment().Lookup(ctx, key)
	if err != nil || !ok {
		return ""
	}
	return strings.TrimSpace(value)
}
