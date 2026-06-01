# Datasource module unification

Date: 2026-06-01

## Objective

Extract the duplicated datasource domain from `fluxplane-core` and `fluxplane-dex` into a new shared Go module:

```text
github.com/fluxplane/fluxplane-datasource
```

This module becomes the single source of truth for datasource runtime contracts, plugin datasource declarations, records, lookup/search/get DTOs, corpus/index metadata, registry behavior, and schema/normalization helpers.

## Non-goals / constraints

- Do **not** preserve backwards compatibility.
- Do **not** add shims.
- Do **not** keep alias packages just to preserve old import paths.
- Do **not** create bridges/adapters whose only purpose is compatibility.
- Update call sites directly to the new module path and new names.
- Remove duplicate datasource definitions from old locations once migrated.
- Keep the shared module independent of both `fluxplane-core` and `fluxplane-dex`.

## Current split

### `fluxplane-core`

Primary datasource domain currently lives in:

```text
fluxplane-core/core/datasource/datasource.go
```

It owns runtime/configured datasource concepts:

- `Name`
- `EntityType`
- `Spec`
- `Ref`
- `IndexSpec`
- `SemanticSpec`
- `EntitySemantic`
- `CorpusSpec`
- `ChunkingSpec`
- `RetrievalSpec`
- `IncrementalSpec`
- `EntitySpec`
- `FieldSpec`
- `RelationSpec`
- `DetectorSpec`
- `EntityCapability`
- `Record`
- `RecordRef`
- `SearchRequest` / `SearchResult`
- `ListRequest` / `ListResult`
- `GetRequest`
- `BatchGetRequest` / `BatchGetResult`
- `RelationRequest` / `RelationResult`
- `CorpusRequest`
- `CorpusPage`
- `CorpusDocument`
- `CorpusChunk`
- provider interfaces:
  - `Accessor`
  - `Searcher`
  - `Lister`
  - `Getter`
  - `BatchGetter`
  - `Relationer`
  - `CorpusProvider`
  - `Provider`
- `Registry`
- access policy helpers.

### `fluxplane-dex`

Datasource concepts are currently split across:

```text
fluxplane-dex/core/types.go
fluxplane-dex/core/pluginbinding/datasource.go
fluxplane-dex/core/pluginbinding/datasource_meta.go
```

They own plugin-facing declaration and binding concepts:

- `DatasourceSpec`
- `DatasourceEntitySchema`
- `DatasourceFieldSpec`
- `DatasourceViewSpec`
- `DatasourceRelationSpec`
- `DatasourceFallback`
- `DatasourceCompletionSpec`
- `DatasourceSource`
- `DatasourceRecord`
- `DatasourceSearchInput`
- `DatasourceLookupInput`
- `DatasourceGetInput`
- `DatasourceSearchResult[T]`
- `DatasourceLookupResult[T]`
- `DatasourceGetResult[T]`
- `LookupSource`
- `LookupMatch[T]`
- `LookupCandidate`
- lookup/scoring helpers
- reflection/schema derivation helpers such as `EntitySchemaFor[T]`.

## Naming decision

There are two similar but different datasource concepts. Keep them distinct:

1. Runtime/configured datasource:
   - name: `Spec`
   - meaning: a configured datasource instance in the app/runtime.
   - examples: `docs`, `gitlab`, `slack`.

2. Plugin datasource declaration:
   - name: `Declaration`
   - meaning: a plugin-exposed datasource operation/entity declaration.
   - examples: `gitlab.projects`, entity `gitlab.project`; `confluence.pages`, entity `confluence.page`.

Do not keep the old dex name `DatasourceSpec` in new code. Rename call sites to `datasource.Declaration`.

## Target module layout

Create:

```text
fluxplane-datasource/
  go.mod
  doc.go
  datasource.go
  declaration.go
  schema.go
  lookup.go
  registry.go
  corpus.go
  normalize.go
  CHANGELOG.md
```

Module path:

```go
module github.com/fluxplane/fluxplane-datasource
```

Dependency rule:

- no dependency on `github.com/fluxplane/fluxplane-core`
- no dependency on `github.com/fluxplane/fluxplane-dex`
- phase 1 should use only the Go standard library unless a dependency is clearly required.

## Shared API shape

### Runtime datasource model

Move the core runtime datasource model into package `datasource`:

```go
type Name string
type EntityType string
type FieldType string
type EntityCapability string
type DetectorKind string

type Ref struct { ... }
type Spec struct { ... }

type IndexSpec struct { ... }
type SemanticSpec struct { ... }
type EntitySemantic struct { ... }
type CorpusSpec struct { ... }
type ChunkingSpec struct { ... }
type RetrievalSpec struct { ... }
type IncrementalSpec struct { ... }

type EntitySpec struct { ... }
type DetectorSpec struct { ... }
type FieldSpec struct { ... }
type RelationSpec struct { ... }

type SearchRequest struct { ... }
type ListRequest struct { ... }
type GetRequest struct { ... }
type BatchGetRequest struct { ... }
type RelationRequest struct { ... }

type SearchResult struct { ... }
type ListResult struct { ... }
type BatchGetResult struct { ... }
type RelationResult struct { ... }

type Record struct { ... }
type RecordRef struct { ... }

type CorpusRequest struct { ... }
type CorpusPage struct { ... }
type CorpusDocument struct { ... }
type CorpusChunk struct { ... }

type Accessor interface { ... }
type Searcher interface { ... }
type Lister interface { ... }
type Getter interface { ... }
type BatchGetter interface { ... }
type Relationer interface { ... }
type CorpusProvider interface { ... }
type Provider interface { ... }

type Registry struct { ... }
```

Also move:

```go
type AccessPolicy struct { ... }
func ContextWithAccessPolicy(...)
func AccessPolicyFromContext(...)
```

### Plugin declaration model

Move dex plugin declaration concepts into the shared module, renamed:

```go
type Declaration struct {
    Name           string                    `json:"name"`
    Entity         string                    `json:"entity"`
    Description    string                    `json:"description,omitempty"`
    Capabilities   []string                  `json:"capabilities,omitempty"`
    Access         []string                  `json:"access,omitempty"`
    SecretPurposes []string                  `json:"secret_purposes,omitempty"`
    Input          json.RawMessage           `json:"input_schema,omitempty"`
    Output         json.RawMessage           `json:"output_schema,omitempty"`
    EntitySchema   *EntitySchema             `json:"entity_schema,omitempty"`
    Views          []ViewSpec                `json:"views,omitempty"`
    Relations      []DeclarationRelationSpec `json:"relations,omitempty"`
    Fallback       Fallback                  `json:"fallback,omitempty"`
    Completion     *CompletionSpec           `json:"completion,omitempty"`
}
```

Rename dex concepts as follows:

| Old dex name | New shared name |
| --- | --- |
| `DatasourceSpec` | `Declaration` |
| `DatasourceEntitySchema` | `EntitySchema` |
| `DatasourceFieldSpec` | `SchemaField` |
| `DatasourceViewSpec` | `ViewSpec` |
| `DatasourceRelationSpec` | `DeclarationRelationSpec` |
| `DatasourceFallback` | `Fallback` |
| `DatasourceCompletionSpec` | `CompletionSpec` |

Keep JSON tags stable.

`Declaration.Access` should be `[]string`, not dex `[]OperationAccess`, so the shared module does not import dex operation concepts.

### Plugin input/result DTOs

Move and rename dex pluginbinding DTOs:

```go
type SearchInput struct {
    Datasource  string `json:"datasource,omitempty" jsonschema:"description=Exact datasource name."`
    Query       string `json:"query,omitempty" jsonschema:"description=Search query."`
    Limit       int    `json:"limit,omitempty" jsonschema:"description=Maximum records to return."`
    Entity      string `json:"entity,omitempty" jsonschema:"description=Entity type filter."`
    EndpointRef string `json:"endpoint_ref,omitempty" jsonschema:"description=Endpoint reference."`
    URL         string `json:"url,omitempty" jsonschema:"description=URL filter or endpoint URL."`
}

type LookupInput struct {
    Datasource  string   `json:"datasource,omitempty" jsonschema:"description=Exact datasource name."`
    Text        string   `json:"text,omitempty" jsonschema:"description=Text to resolve."`
    Terms       []string `json:"terms,omitempty" jsonschema:"description=Lookup terms."`
    Limit       int      `json:"limit,omitempty" jsonschema:"description=Maximum matches to return."`
    Entity      string   `json:"entity,omitempty" jsonschema:"description=Entity type filter."`
    EndpointRef string   `json:"endpoint_ref,omitempty" jsonschema:"description=Endpoint reference."`
}

type GetInput struct {
    Datasource  string `json:"datasource,omitempty" jsonschema:"description=Exact datasource name."`
    ID          string `json:"id,omitempty" jsonschema:"description=Record ID."`
    Entity      string `json:"entity,omitempty" jsonschema:"description=Entity type."`
    EndpointRef string `json:"endpoint_ref,omitempty" jsonschema:"description=Endpoint reference."`
}

type SearchOutput[T any] struct {
    Source  string  `json:"source"`
    Query   string  `json:"query,omitempty"`
    Count   int     `json:"count"`
    Records []T     `json:"records"`
    Errors  []Error `json:"errors,omitempty"`
}

type LookupOutput[T any] struct {
    Source  string   `json:"source"`
    Text    string   `json:"text,omitempty"`
    Terms   []string `json:"terms,omitempty"`
    Count   int      `json:"count"`
    Matches []T      `json:"matches"`
}

type GetOutput[T any] struct {
    Source string `json:"source"`
    Record T      `json:"record"`
}
```

Rename call sites directly:

| Old dex pluginbinding name | New shared name |
| --- | --- |
| `DatasourceSearchInput` | `datasource.SearchInput` |
| `DatasourceLookupInput` | `datasource.LookupInput` |
| `DatasourceGetInput` | `datasource.GetInput` |
| `DatasourceSearchResult[T]` | `datasource.SearchOutput[T]` |
| `DatasourceLookupResult[T]` | `datasource.LookupOutput[T]` |
| `DatasourceGetResult[T]` | `datasource.GetOutput[T]` |

Do not keep old aliases.

### Lookup helpers

Move lookup helpers from dex pluginbinding to the shared module:

```go
type Source struct { ... }
type RecordBase struct { ... }
type LookupSource struct { ... }
type LookupMatch[T any] struct { ... }
type LookupCandidate struct { ... }

func NewRecord(...)
func NewSearchOutput[T any](...)
func NewLookupOutput[T any](...)
func NewGetOutput[T any](...)

func LookupTerms(...)
func LookupTermsFrom(...)
func LookupLimit(...)
func FilterLookupTerms(...)
func LookupMatches(...)
func ScoreLookupValues(...)
func SortLookupMatches(...)
```

Keep only context-bound helpers in dex pluginbinding if they genuinely require plugin context. These must produce shared datasource types, not duplicate structs.

### Reflection/schema helpers

Move pure reflection/schema code from `pluginbinding/datasource_meta.go` to the shared module:

```go
func EntitySchemaFor[T any]() EntitySchema
func NormalizeDeclaration(Declaration) Declaration
func NormalizeDeclarations([]Declaration) []Declaration
func MergeEntitySchema(...)
func ParseDatasourceTag(...)
```

Dex pluginbinding can keep builder-style option functions only if they are part of plugin construction ergonomics, but they must mutate/use `datasource.Declaration` directly and not define duplicate datasource types.

## Migration phases

### Phase 0: audit and lock names

1. Confirm working trees are clean.
2. Inventory all datasource definitions and direct references in:
   - `fluxplane-core/core/datasource`
   - `fluxplane-core/runtime/datasource/...`
   - `fluxplane-core/orchestration/datasourceindex`
   - `fluxplane-core/plugins/native/datasource`
   - `fluxplane-core/plugins/integrations/...`
   - `fluxplane-dex/core/types.go`
   - `fluxplane-dex/core/pluginbinding/datasource*.go`
   - `fluxplane-dex/fluxplaneplugin/datasource.go`
   - all dex plugin manifests/operations.
3. Confirm new names:
   - runtime configured datasource: `datasource.Spec`
   - plugin declaration: `datasource.Declaration`
   - normalized runtime record: `datasource.Record`
   - plugin-side record base: `datasource.RecordBase` or `datasource.PluginRecord`.

### Phase 1: create `fluxplane-datasource`

1. Create the module.
2. Move/copy pure runtime types from `fluxplane-core/core/datasource`.
3. Move/copy pure declaration, lookup, schema, and normalization types from `fluxplane-dex`.
4. Rename types while moving; do not preserve old names.
5. Port relevant tests from:
   - `fluxplane-core/core/datasource`
   - `fluxplane-dex/core/pluginbinding/datasource_test.go`
   - `fluxplane-dex/core/pluginbinding/definition_test.go` datasource/schema sections.
6. Run:

```bash
go test ./...
```

7. Release/tag the module before consumers are updated.

### Phase 2: migrate `fluxplane-core` directly

1. Add dependency on the new module.
2. Replace imports of:

```go
github.com/fluxplane/fluxplane-core/core/datasource
```

with:

```go
github.com/fluxplane/fluxplane-datasource
```

3. Delete or empty the old `fluxplane-core/core/datasource` package. Do not leave aliases/shims.
4. Update all type references and constructor calls directly.
5. Update package docs and imports in affected tests.
6. Run targeted tests:

```bash
go test ./runtime/datasource/...
go test ./adapters/storage/datasourcemirror/...
go test ./orchestration/datasourceindex
go test ./plugins/native/datasource
go test ./plugins/integrations/...
```

7. Run full verification:

```bash
go test ./...
task verify
```

### Phase 3: migrate `fluxplane-dex` core declarations directly

1. Add dependency on the new module.
2. Remove datasource declaration structs from `fluxplane-dex/core/types.go`.
3. Update `PluginManifest.Datasources` to:

```go
Datasources []datasource.Declaration `json:"datasources,omitempty"`
```

4. Update all old `core.DatasourceSpec` references to `datasource.Declaration`.
5. Update old fallback/schema/view/relation names to shared names.
6. Change datasource access metadata to `[]string` and stringify dex operation access constants at the builder boundary.
7. Run:

```bash
go test ./core/...
go test ./...
```

### Phase 4: migrate dex pluginbinding DTOs and helpers directly

1. Remove pluginbinding-owned datasource DTO structs.
2. Update all plugin operations to use shared types:
   - `datasource.SearchInput`
   - `datasource.LookupInput`
   - `datasource.GetInput`
   - `datasource.SearchOutput[T]`
   - `datasource.LookupOutput[T]`
   - `datasource.GetOutput[T]`
3. Remove pluginbinding-owned lookup helper implementations.
4. Update pluginbinding registration helpers to accept shared declaration/input/output types.
5. Keep only plugin-context-specific functions in pluginbinding.
6. Run:

```bash
go test ./core/pluginbinding
go test ./plugins/...
go test ./fluxplaneplugin
```

### Phase 5: update runtime plugin datasource integration without compatibility bridges

`fluxplane-dex/fluxplaneplugin/datasource.go` should convert current plugin declarations into runtime providers because that is real integration logic, not compatibility. It must use shared types on both sides:

- input: `datasource.Declaration`
- output provider contracts: `datasource.Provider`, `datasource.Accessor`, etc.

Mapping should be direct:

- declaration `Name` -> runtime `Spec.Name`
- declaration `Entity` -> runtime `EntitySpec.Type`
- declaration `Capabilities` -> runtime `EntityCapability`
- declaration `EntitySchema.Fields` -> runtime `FieldSpec`
- declaration `Relations` -> runtime `RelationSpec`

Do not add old-name adapters or compatibility wrappers.

Add tests for representative plugin declarations:

- GitLab projects/users/groups/issues/merge requests
- Confluence pages/users
- Jira issues/users/projects if applicable
- Docker containers/images/networks/volumes
- Web search results

### Phase 6: delete duplicates and audit

1. Delete old datasource definitions from:
   - `fluxplane-core/core/datasource`
   - `fluxplane-dex/core/types.go`
   - `fluxplane-dex/core/pluginbinding/datasource.go`
   - `fluxplane-dex/core/pluginbinding/datasource_meta.go`
2. Search for old names:

```bash
rg 'DatasourceSpec|DatasourceEntitySchema|DatasourceSearchInput|DatasourceLookupInput|DatasourceGetInput|DatasourceSearchResult|DatasourceLookupResult|DatasourceGetResult' fluxplane-core fluxplane-dex
```

3. Any remaining match must either be documentation/changelog text or a renamed local concept with a clear reason. There should be no compatibility aliases.
4. Search for old import path:

```bash
rg 'fluxplane-core/core/datasource' fluxplane-core fluxplane-dex
```

5. Result must be empty.

## Test matrix

### `fluxplane-datasource`

```bash
go test ./...
```

Required test coverage:

- `Spec.Validate`
- `EntitySpec.Validate`
- registry duplicate handling
- record/ref JSON stability
- declaration normalization
- schema derivation from struct tags:
  - `datasource:"id"`
  - `datasource:"title"`
  - `datasource:"completion"`
  - `datasource:"view=compact|lookup"`
  - `datasource:"relation=gitlab.project:project"`
- lookup scoring/sorting/deduping.

### `fluxplane-core`

```bash
go test ./runtime/datasource/...
go test ./adapters/storage/datasourcemirror/...
go test ./orchestration/datasourceindex
go test ./plugins/native/datasource
go test ./plugins/integrations/...
go test ./...
task verify
```

### `fluxplane-dex`

For dex root:

```bash
go test ./...
```

For dex multi-module plugins if present:

```bash
for d in $(find . -name go.mod -maxdepth 4 -printf '%h\n' | sort); do
  (cd "$d" && go test ./...)
done
```

Specific packages:

```bash
go test ./core/pluginbinding
go test ./fluxplaneplugin
go test ./plugins/gitlab ./plugins/confluence ./plugins/jira ./plugins/docker ./plugins/slack
```

## Release order

1. Release `fluxplane-datasource`.
2. Update and release `fluxplane-core`.
3. Update and release `fluxplane-dex`.
4. Update/release dex plugin modules if they depend directly on dex or the new datasource module.

Prefer the next synchronized release train version rather than reusing an already-published version.

## Risks

1. Naming confusion between runtime datasource config and plugin datasource declarations.
   - Mitigation: use `Spec` for runtime configured datasource and `Declaration` for plugin manifest datasource.

2. Dex `OperationAccess` dependency leaking into shared module.
   - Mitigation: shared `Declaration.Access` is `[]string`; dex helpers stringify operation access constants.

3. Generic type alias limitations.
   - Mitigation: there should be no aliases; call sites use shared generic types directly.

4. JSON compatibility mistakes.
   - Mitigation: keep JSON field names stable and add fixture tests for representative plugin manifests.

5. Cross-repo release ordering.
   - Mitigation: release shared module first, then update consumers directly.

6. Over-extraction.
   - Mitigation: move provider-neutral datasource vocabulary/DTOs only. Runtime indexing implementation remains in core. Plugin execution remains in dex.

## Acceptance criteria

- `github.com/fluxplane/fluxplane-datasource` exists and owns the shared datasource domain.
- `fluxplane-core` imports `github.com/fluxplane/fluxplane-datasource` directly for datasource types/contracts.
- `fluxplane-dex` imports `github.com/fluxplane/fluxplane-datasource` directly for datasource declarations, DTOs, schema, and lookup helpers.
- No compatibility shims or alias packages remain.
- No duplicate datasource domain structs remain in core or dex.
- Old import path `github.com/fluxplane/fluxplane-core/core/datasource` is gone from active code.
- Old dex type names such as `DatasourceSpec`, `DatasourceSearchInput`, and `DatasourceSearchResult` are gone from active code.
- Tests pass for:
  - `fluxplane-datasource: go test ./...`
  - `fluxplane-core: go test ./...` and `task verify`
  - `fluxplane-dex: go test ./...` across root and plugin modules.
- Changelogs/docs are updated in all touched repositories.
