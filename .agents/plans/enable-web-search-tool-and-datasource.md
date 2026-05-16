# Enable Web Search as Datasource and Native Web Tool

Date: 2026-05-16

## Progress Update - 2026-05-16

Implemented and committed in `6fd5c5a feat: add duckduckgo web search provider`:

- Native `web_search` operation exists in `plugins/webplugin/search.go` with:
  - `query` and `queries` input support.
  - optional `providers` filter.
  - `max` default/clamp behavior.
  - grouped provider/query output and partial provider errors.
- Provider interface and deterministic provider registry are implemented.
- Tavily provider is implemented in `plugins/webplugin/search_tavily.go`:
  - reads `TAVILY_API_KEY` through `system.Environment()`.
  - uses `system.Network().DoHTTP`.
  - sends low-cost `POST https://api.tavily.com/search` requests.
  - maps Tavily results into shared `SearchResult` values.
- Keyless DuckDuckGo provider is implemented in `plugins/webplugin/search_duckduckgo.go`:
  - default provider when network is available.
  - uses `https://html.duckduckgo.com/html/?q={query}`.
  - contains DuckDuckGo-specific URL escaping, HTML parsing, redirect normalization, percent decoding, and HTML cleanup.
- Native provider execution is bounded:
  - fixed worker concurrency: `4` across the whole operation.
  - deterministic output order is preserved by collecting indexed task results.
- `plugins/webplugin/datasource.go` is now a thin datasource adapter:
  - no DuckDuckGo parser/helpers.
  - no `search_url` config.
  - optional provider restriction through `spec.Config["providers"]` only.
  - delegates search execution to the shared provider registry/orchestration.
  - converts shared `SearchResult` values into `coredatasource.Record` values.
- Coder/coding app integration was already in the working tree before this update:
  - `codingplugin` forwards child `DatasourceProviderContributor`s.
  - SDK has `WithDatasource` / `WithDatasources`.
  - coder bundle exposes `web_search`, `datasource_search`, `datasource_get`, and `datasource_batch_get`.
  - coder bundle grants datasource access to `web_search` and defines the default `web_search` datasource spec.
  - coder prompt distinguishes `web_search`, datasource-backed web search, and `web_request`.
- Tests added/updated for:
  - DuckDuckGo native provider fallback without Tavily.
  - explicit `providers=["duckduckgo"]`.
  - bounded provider concurrency and ordering.
  - datasource search through the shared registry.
  - existing Tavily request/response behavior.

Verification run:

```text
go test ./plugins/webplugin ./plugins/codingplugin ./apps/coder ./apps/launch
```

Smoke tests run successfully for native and datasource-backed web search through `go run ./cmd/coder --model=codex --input ...`.

Known remaining work:

- Run the full project gate (`task verify`) before release if practical.
- Add/update `apps/launch` coverage specifically for datasource registry opening `web_search` through `codingplugin` forwarding, if not already covered elsewhere.
- Consider whether to remove old datasource kind aliases `web` and `websearch` in a separate compatibility cleanup. Current code intentionally keeps them accepted.
- Optionally update examples such as `examples/slack-bot` from datasource name/kind `web-search`/`web` to `web_search`/`web_search`. This should be a separate example/docs cleanup.
- Consider provider-level enhancements later:
  - Tavily `include_answer` option.
  - domain include/exclude filters.
  - country/news/topic options.
  - provider-specific rate-limit/error classification.
- Consider adding network usage accounting for provider searches if byte counts are needed in operation events.

Next recommended steps:

1. Audit `apps/launch` tests for explicit `web_search` datasource registry coverage; add a focused test if missing.
2. Run `task verify` or the closest available full verification target.
3. Decide whether to keep datasource kind aliases permanently or schedule a compatibility-breaking cleanup.
4. Update examples/docs to prefer the canonical datasource name/kind `web_search`.


## Goal

Expose web search from `plugins/webplugin` in two ways:

1. Datasource path:
   - generic tool: `datasource_search`
   - default coder datasource name: `web_search`
   - entity: `web.search_result`
   - datasource kind: `web_search` preferred; keep existing accepted aliases `web` and `websearch` unless a separate cleanup removes them.

2. Native operation path:
   - direct tool: `web_search`
   - input supports multiple queries and provider selection, for example:

     ```json
     {
       "queries": ["Query 1", "Query 2"],
       "providers": ["tavily"],
       "max": 10
     }
     ```

   - output returns bounded, provider-labeled search results.

`web_request` remains only for fetching known URLs. It must not be documented or prompted as the search/discovery interface.

## Provider Strategy

### Phase 1: Tavily as first supported native provider

`TAVILY_API_KEY` is available in the intended runtime environment. Tavily is the first supported paid/provider-backed web search implementation for `web_search`.

Phase 1 should make `web_search` work through Tavily, because implementation needs actual web search before researching and adding free providers.

Tavily docs checked:

```text
https://docs.tavily.com/documentation/api-reference/endpoint/search.md
```

Key Tavily API details from docs:

- endpoint: `POST https://api.tavily.com/search`
- auth: Bearer token
  - header: `Authorization: Bearer <TAVILY_API_KEY>`
- request content type: `application/json`
- important request fields:
  - `query` string
  - `search_depth` enum: `advanced`, `basic`, `fast`, `ultra-fast`; default `basic`
  - `max_results` integer, default `5`, maximum `20`
  - `topic` enum: `general`, `news`; default `general`
  - optional: `include_answer`
  - optional: `include_raw_content`
  - optional: `include_images`
  - optional: `include_image_descriptions`
  - optional: `include_domains`
  - optional: `exclude_domains`
  - optional: `country`
- response fields:
  - `query`
  - optional `answer`
  - `results[]`
    - `title`
    - `url`
    - `content`
    - `score`
    - optional `raw_content`
    - optional `favicon`
    - optional `images`
  - `response_time`
- documented errors include:
  - `400` invalid request
  - `401` missing/invalid API key
  - `429` rate limit
  - `432` monthly credit limit reached
  - `433` usage limit reached
  - `500` internal server error

Use low-cost defaults for the initial implementation:

```json
{
  "search_depth": "basic",
  "topic": "general",
  "include_answer": false,
  "include_raw_content": false,
  "include_images": false
}
```

Native `max` maps to Tavily `max_results` and must be clamped to Tavily's max of `20`.

### Phase 2: keyless DuckDuckGo provider

DuckDuckGo HTML search is now implemented as a keyless native provider in `plugins/webplugin/search_duckduckgo.go`.

Important ownership rule:

- DuckDuckGo-specific URL construction, HTML parsing, redirect normalization, percent decoding, and HTML cleanup belong in `search_duckduckgo.go`.
- `datasource.go` must remain provider-agnostic and only adapt shared search results into datasource records.
- Other no-key or free-tier providers can be added later if they fit the project safety and reliability requirements.

### Provider defaults

Current default behavior:

- Native `web_search` registers Tavily first when `TAVILY_API_KEY` is set.
- Native `web_search` also registers keyless DuckDuckGo when `system.Network()` is available.
- If no native provider is available, `web_search` returns a clear provider-unavailable failure.
- Do not silently use `web_request` as a fallback for search.
- Datasource-backed web search delegates to the same provider registry.

## What Already Works Today

### Current web datasource provider

`plugins/webplugin/datasource.go` implements a web search datasource provider:

- entity: `web.search_result`
- struct: `SearchResult`
- provider/accessor:
  - `webSearchProvider`
  - `webSearchAccessor.Search`
- search backend:
  - delegates to the shared native web search provider registry.
  - by default this uses available providers, currently Tavily when configured and DuckDuckGo when network is available.
- datasource config:
  - `providers`: optional comma/whitespace-separated provider restriction, for example `tavily,duckduckgo`.
  - There is intentionally no datasource `search_url` override; provider details belong in provider implementations.

The current datasource provider accepts datasource kinds:

```go
"web", "websearch", "web_search"
```

### Existing example wiring

`examples/slack-bot/agentsdk.app.yaml` already wires web search through datasource search:

```yaml
- name: web-search
  kind: web
  entities:
    - web.search_result
  description: Public web search results.
```

The Slack bot agent grants datasource access to `web-search` and uses `datasource_search` for `web.search_result`.

So the datasource mechanism is not theoretical; it exists and works for at least the Slack bot example.

### Current coder status

The coder app now exposes both native and datasource-backed web search:

- `apps/coder/bundle.go` exposes `web_search`.
- coder bundle defines a default datasource named `web_search`.
- coder agent has datasource ref granting access to `web_search`.
- coder agent exposes `datasource_search`, `datasource_get`, and `datasource_batch_get`.
- `plugins/codingplugin` forwards datasource provider contributions from child plugins.

### Tavily status in repo

Tavily support is implemented in `plugins/webplugin/search_tavily.go` and is available when `TAVILY_API_KEY` is visible through `system.Environment()`.

## Target Architecture in `plugins/webplugin`

Add a provider-based search subsystem inside `webplugin`.

Suggested files:

```text
plugins/webplugin/search.go            # native operation, provider interface, registry/orchestration
plugins/webplugin/search_tavily.go     # Tavily provider, env-gated by TAVILY_API_KEY
plugins/webplugin/search_duckduckgo.go # keyless DuckDuckGo provider and DuckDuckGo-specific parsing helpers
plugins/webplugin/datasource.go        # datasource adapter delegating to the shared provider registry
```

Do not import `os` directly from plugin code. Provider availability must read env through:

```go
p.system.Environment().Getenv("TAVILY_API_KEY")
```

because reusable plugins should use `runtime/system.System` boundaries.

## Search Provider Interface

Define a provider interface in `plugins/webplugin/search.go`.

Shape:

```go
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
```

Add stable provider names:

```go
const (
	SearchProviderTavily     = "tavily"
	SearchProviderDuckDuckGo = "duckduckgo"
)
```

Add provider construction:

```go
func searchProviders(sys system.System) []SearchProvider {
	var providers []SearchProvider
	if tavily := newTavilySearchProvider(sys); tavily.Available(context.Background()) {
		providers = append(providers, tavily)
	}
	if duckduckgo := newDuckDuckGoSearchProvider(sys); duckduckgo.Available(context.Background()) {
		providers = append(providers, duckduckgo)
	}
	return providers
}
```

Provider registration should be deterministic and tested.

## Native `web_search` Operation

### Operation constant

Update `plugins/webplugin/plugin.go`:

```go
const (
	Name         = "web"
	RequestOp    = "web_request"
	SearchOp     = "web_search"
	maxBodyBytes = 5 * 1024 * 1024
)
```

### Input shape

Add in `plugins/webplugin/search.go`:

```go
type searchInput struct {
	Queries   []string `json:"queries,omitempty" jsonschema:"description=Search queries to run. Use one or more concise search queries."`
	Query     string   `json:"query,omitempty" jsonschema:"description=Single search query convenience field."`
	Providers []string `json:"providers,omitempty" jsonschema:"description=Optional provider names such as tavily or duckduckgo. Defaults to available providers."`
	Max       int      `json:"max,omitempty" jsonschema:"description=Maximum results per query/provider. Defaults to 10."`
}
```

Notes:

- Prefer `queries` as the primary field.
- Keep `query` as a convenience field if consistent with existing datasource_search UX.
- Use `max`, not `limit`.
- Do not expose `search_url` on the native public operation. Provider configuration belongs inside providers.

### Output shape

```go
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
```

### Operation spec

```go
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
```

### Operation implementation

`Plugin.search()` should:

1. Normalize `queries` + `query`.
2. Fail if no query remains.
3. Clamp `max`:
   - default: 10
   - maximum: 20.
4. Build available providers from `searchProviders(p.system)`.
5. Filter by `input.Providers` if provided.
6. Run each query/provider pair in parallel with fixed concurrency `4` across the whole operation (not per provider and not per query). Preserve deterministic output ordering by collecting results back in query/provider order after workers complete.
7. Return partial results plus errors if some providers fail.
8. Fail only if all provider/query searches fail or no requested provider is available.
9. Emit network usage events similarly to `web_request` if response byte counts are available from providers.

Concurrency note: provider searches are independent network calls. Use a small bounded worker pool/semaphore with capacity `4`; do not spawn unbounded goroutines when many queries and providers are requested. Tests should prove the limit is honored.

Renderer should group by query and provider.

### Intent

Add `searchIntent` for the native operation.

For Tavily, intent should include:

```text
https://api.tavily.com/search
```

with network fetch behavior.

## Tavily Provider

Add `plugins/webplugin/search_tavily.go`.

Availability:

```go
TAVILY_API_KEY
```

Read via the system boundary:

```go
func env(sys system.System, key string) string {
	if sys == nil || sys.Environment() == nil {
		return ""
	}
	return strings.TrimSpace(sys.Environment().Getenv(key))
}
```

Provider behavior:

- name: `tavily`
- available when `TAVILY_API_KEY` is non-empty.
- use `system.Network().DoHTTP`, not `net/http` directly.
- method: `POST`
- URL: `https://api.tavily.com/search`
- headers:
  - `Authorization: Bearer <TAVILY_API_KEY>`
  - `Content-Type: application/json`
- request body:

  ```json
  {
    "query": "...",
    "search_depth": "basic",
    "topic": "general",
    "max_results": 10,
    "include_answer": false,
    "include_raw_content": false,
    "include_images": false
  }
  ```

- map response into `SearchResult`:
  - `results[].url` -> `SearchResult.URL`
  - `results[].title` -> `SearchResult.Title`
  - `results[].content` -> `SearchResult.Snippet`
  - source/provider -> `SearchResult.Source = "tavily"`
- preserve `answer` in provider result if included later.

Do not hard-fail plugin initialization when `TAVILY_API_KEY` is missing. Absence means the provider is unavailable.

If user explicitly requests `providers=["tavily"]` and `TAVILY_API_KEY` is missing, return a clear error such as:

```text
web search provider "tavily" is not available; TAVILY_API_KEY is not set
```

Handle non-2xx Tavily responses with clear error messages, including `401`, `429`, `432`, and `433` where possible.

### Current DuckDuckGo / Free Provider

DuckDuckGo has been refactored into a native keyless provider:

```text
plugins/webplugin/search_duckduckgo.go
```

DuckDuckGo-specific code lives in that provider file, not in `datasource.go`:

- search URL construction and query escaping.
- DuckDuckGo HTML result parsing.
- DuckDuckGo redirect URL normalization through `uddg`.
- percent decoding.
- HTML cleanup.

Default endpoint:

```text
https://html.duckduckgo.com/html/?q={query}
```

Provider behavior:

- name: `duckduckgo`.
- available if `system.Network()` exists.
- uses GET.
- max bytes: `512 * 1024`.
- timeout: 30 seconds.
- user agent: `agentruntime/0.1`.

## Datasource Integration

### Datasource name

For coder, the datasource must be named:

```text
web_search
```

not `web`.

### Datasource kind

Use:

```text
web_search
```

for new coder configuration.

Keep provider `Open` accepting existing aliases unless intentionally removing compatibility:

```go
if spec.Kind != "web" && spec.Kind != "websearch" && spec.Kind != "web_search" {
	...
}
```

### Datasource provider current behavior

The datasource path delegates to the shared provider registry:

- Native `web_search` and datasource search use the same provider implementations.
- Default datasource behavior uses available providers.
- DuckDuckGo is available by default when network exists.
- Tavily is available when `TAVILY_API_KEY` is set.
- Datasource search has no provider-specific HTML parsing.
- Datasource search has no `search_url` config.

Current datasource behavior:

- Optional `spec.Config["providers"]` restricts providers, for example `tavily,duckduckgo`.
- `req.Limit` maps to provider request `Max`.
- datasource result records are `coredatasource.Record` with:
  - `Datasource`: datasource name.
  - `Entity`: `web.search_result`.
  - `ID`: URL.
  - `URL`: URL.
  - `Title`: title.
  - `Content`: snippet.
  - `Raw`: `SearchResult`.

Provider selection for datasource path:

- If datasource spec config includes provider selection, support it through config, for example:

  ```yaml
  config:
    providers: tavily,duckduckgo
  ```

- If no provider config is present, use the default available provider registry.

### Avoid `datasource_relation`

Do not add `datasource_relation` to coder for web search. Web search results do not need datasource relations.

## Coding Plugin Aggregation

Update:

```text
plugins/codingplugin/plugin.go
```

Add import:

```go
coredatasource "github.com/fluxplane/agentruntime/core/datasource"
```

Add assertion:

```go
var _ pluginhost.DatasourceProviderContributor = Plugin{}
```

Add method to forward datasource providers from child plugins.

## SDK Authoring Helper

Update:

```text
sdk/builder.go
```

Add datasource grant helpers:

```go
func (b *AgentBuilder) WithDatasource(name string) *AgentBuilder
func (b *AgentBuilder) WithDatasources(names ...string) *AgentBuilder
```

These append `coredatasource.Ref{Name: coredatasource.Name(name)}` to `agent.Spec.Datasources`.

## Coder Bundle Wiring

Update:

```text
apps/coder/bundle.go
```

### Imports

Add:

```go
coredatasource "github.com/fluxplane/agentruntime/core/datasource"
"github.com/fluxplane/agentruntime/plugins/webplugin"
```

### Agent operations

Add to `WithOperations(...)`:

```go
"web_search",
"datasource_search",
"datasource_get",
"datasource_batch_get",
```

Do not add `datasource_relation`.

### Agent datasource grant

Add:

```go
.WithDatasource("web_search")
```

### Default datasource spec

After `bundle := ...Build()`, append:

```go
bundle.Datasources = append(bundle.Datasources, coredatasource.Spec{
	Name:        "web_search",
	Description: "Default public web search datasource.",
	Kind:        "web_search",
	Entities:    []coredatasource.EntityType{webplugin.SearchResultEntity},
})
```

### Delegation caps

Add to default delegation operation caps if child agents should search:

```go
{Name: "web_search"},
{Name: "datasource_search"},
{Name: "datasource_get"},
{Name: "datasource_batch_get"},
```

Do not add `datasource_relation`.

### System prompt

Update coder prompt to say:

```text
Use web_search for general web discovery. Use datasource_search with entities=["web.search_result"] for configured web-search datasource queries. Use web_request only for fetching known URLs, not for search.
```

## Launch Wiring

Existing `apps/launch/run.go` should already auto-wire the datasource plugin when any datasource exists:

```go
if opts.Dev || hasAnyDatasource(bundles) {
	registry, err := datasourceRegistry(...)
	plugins = append(plugins, datasourceplugin.NewWithSemantic(registry, index))
	ensurePluginRef(bundles, datasourceplugin.Name)
}
```

Once coder bundle has a `web_search` datasource spec, this should activate.

Also ensure `datasourceRegistry` receives the `webplugin` datasource provider through `codingplugin` provider forwarding.

## Tests

### `plugins/webplugin`

Add/extend tests in:

```text
plugins/webplugin/plugin_test.go
```

Cover:

- `Contributions` includes both `web_request` and `web_search`.
- `Operations` includes both executable operations.
- `web_search` accepts `queries=[...]`.
- `web_search` accepts provider filters.
- `web_search` defaults to Tavily when `TAVILY_API_KEY` is set.
- `web_search` returns grouped results by query/provider.
- empty query list fails.
- `max` default and clamp behavior, especially Tavily maximum `20`.
- Tavily provider is unavailable when `TAVILY_API_KEY` is unset.
- Tavily provider registers when `TAVILY_API_KEY` is set.
- explicit `providers=["tavily"]` with no API key returns a clear unavailable-provider error.
- Tavily request uses:
  - `POST https://api.tavily.com/search`
  - `Authorization: Bearer <key>`
  - JSON body with `query`, `search_depth`, `topic`, `max_results`, and disabled optional expensive fields.
- Tavily response maps `title`, `url`, `content`, `score` into search results.
- datasource search still works.

DuckDuckGo provider tests:

- DuckDuckGo provider parses mocked HTML into `SearchResult` values.
- Native `web_search` falls back to DuckDuckGo when Tavily is unavailable.
- Explicit `providers=["duckduckgo"]` works.
- Provider execution honors fixed concurrency and preserves output order.

### `plugins/codingplugin`

Add tests in:

```text
plugins/codingplugin/plugin_test.go
```

Cover:

- `codingplugin.New(sys).DatasourceProviders(...)` includes a provider exposing `web.search_result`.

### `apps/coder`

Update:

```text
apps/coder/bundle_test.go
```

Cover:

- operation count increases as expected.
- coder agent has `web_search`.
- coder agent has `datasource_search`.
- coder agent has `datasource_get`.
- coder agent has `datasource_batch_get`.
- coder agent does not require `datasource_relation` for this feature.
- coder agent has datasource ref `web_search`.
- bundle has datasource spec:
  - name `web_search`
  - kind `web_search`
  - entity `web.search_result`

### `apps/launch`

Add/update tests to verify:

- coder bundle with `web_search` datasource causes datasource plugin wiring.
- composition includes `datasource_search`.
- composition includes datasource spec `web_search`.
- datasource registry can open `web_search` using provider forwarded through `codingplugin`.

### `examples/slack-bot`

Do not break existing example behavior.

Optionally update later from datasource name `web-search` / kind `web` to `web_search` / kind `web_search`, but that is separate from enabling coder. If changed, update README and tests together.

## Verification

Targeted tests:

```text
go test ./plugins/webplugin ./plugins/codingplugin ./apps/coder ./apps/launch
```

Full gate before commit:

```text
task verify
```

## Checklist

- [x] Add `plugins/webplugin/search.go` with native operation, provider interface, provider registry, and shared orchestration.
- [x] Add Tavily provider gated by `TAVILY_API_KEY` through `system.Environment()`.
- [x] Tavily provider uses `POST https://api.tavily.com/search` with bearer auth and low-cost defaults.
- [x] Add DuckDuckGo/free provider registry implementation.
- [x] Keep existing datasource search working and refactor datasource search to reuse shared search providers.
- [x] Remove datasource `search_url` config; provider details live in provider implementations.
- [x] Use coder datasource name `web_search`, not `web`.
- [x] Do not add `datasource_relation` for coder web search.
- [x] `codingplugin` forwards `DatasourceProviderContributor`.
- [x] SDK gets `WithDatasource` / `WithDatasources`.
- [x] coder bundle defines datasource spec `web_search`.
- [x] coder agent grants datasource access to `web_search`.
- [x] coder agent exposes `web_search`.
- [x] coder agent exposes `datasource_search`, `datasource_get`, `datasource_batch_get`.
- [x] coder prompt says use `web_search` for discovery and `web_request` only for known URLs.
- [x] tests cover provider registration, Tavily env gating, Tavily request/response mapping, operation availability, datasource wiring, and search parsing.
- [x] run targeted tests.
- [ ] run `task verify`.
- [ ] add explicit launch/datasource-registry coverage for opening `web_search` through codingplugin forwarding if missing.
- [ ] update examples/docs to prefer canonical `web_search` datasource name/kind.
