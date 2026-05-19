# Design: data source and mirror architecture

## Status

Partially implemented. The current slice wires datasource mirror records into
`core/data.Store`, keeps semantic indexing as a sidecar, moves durable store
configuration under `runtime.data.store`, and exposes the datasource catalog as
a synthetic datasource. The larger rename from "index" to "mirror" is still
pending.

## Summary

The datasource concept should return to being the stable boundary for external
or local data access: where data comes from, which source schemas exist, and
which relationships exist in that source. The thing currently called the
datasource "index" should become a local data mirror: a datastore for configured
queryable views or materializations of datasource entities, plus secondary
indexes for exact lookup, filters, text search, relations, and optional semantic
vectors.

The mirror should not be limited to third-party APIs. The same record, relation,
blob, scope, and query contracts should also support local memory later:
per-user, per-agent, per-session, per-workspace, per-channel, and tenant/app
scoped data. Datasource mirroring is one producer of this local data store, not
the whole abstraction.

The target architecture is:

- `core/data` owns provider-neutral source, view, scope, record, relation, blob,
  query, and store contracts.
- `core/datasource` keeps the current live datasource specs, entity specs,
  relationship schema, record shapes, and accessor contracts while the
  datasource package evolves toward `core/data`.
- `runtime/data` owns local store implementations and adapters between current
  datasource schemas and the new data source/view model.
- `runtime/datasource/mirror` can later own the mirror service, query planner,
  freshness state, queueing, and durable mirror store implementations.
- `runtime/datasource/search` or `runtime/datasource/semantic` owns optional
  retrieval engines such as embedding and vector chunk search.
- storage backends implement mirror primitives using their native indexes. The
  file backend can use in-memory maps and persisted snapshots; MySQL can use
  primary keys, field tables, FULLTEXT, and batch hydration.
- `orchestration/datasourcemirror` replaces `orchestration/datasourceindex` as
  the build/warmup flow.
- provider plugins declare default views/materializations next to their source
  schemas; the GitLab and Slack packages are the first proving ground.
- `plugins/datasourceplugin` continues to expose the same model-facing tools.
  Most provider plugins should not need to know which mirror backend is in use.

This keeps plugin churn low while fixing the conceptual issue: a datasource is
not a materialized view, an index is not a datastore, and semantic vectors are
not the same thing as a local copy of entity records.

## Current Findings

`core/datasource` currently mixes the right stable ideas with some overloaded
naming:

- `Spec` describes a configured datasource instance and includes `IndexSpec` and
  `SemanticSpec`.
- `EntitySpec` declares fields, detectors, relations, and capabilities such as
  `search`, `list`, `get`, `relation`, `index`, and `semantic_search`.
- `Accessor` plus optional interfaces (`Searcher`, `Lister`, `Getter`,
  `BatchGetter`, `Relationer`, `CorpusProvider`) form the provider-facing access
  contract.
- `Record` is the normalized entity instance returned by tools.
- `CorpusDocument` is the current feed shape for indexable records, but it is
  biased toward semantic text and loses richer entity information such as typed
  values, raw provider payloads, and relation/link state.

`runtime/datasource/semantic` currently does more than semantic retrieval:

- `Index` coordinates field record writes, semantic embedding queueing, vector
  writes, document state, index run state, and status reporting.
- `JSONStore` stores all documents, field records, queue jobs, run state, and
  chunks in one JSON file.
- `FieldStore` can search structured records, but the JSON implementation scans
  all field records and scores in memory.
- `VectorStore` is correctly separated as a secondary retrieval mechanism, but
  it is owned by the same `Index` type as field-record mirroring.

`orchestration/datasourceindex` is really a mirror builder plus semantic queue
builder:

- it scans each datasource/entity through `CorpusProvider`;
- writes field records when the entity has `EntityCapabilityIndex`;
- queues semantic embedding work when the entity has
  `EntityCapabilitySemanticSearch`;
- tracks freshness and run state.

Plugin usage is uneven:

- `plugins/datasourceplugin` exposes generic `datasource_search`,
  `datasource_list`, `datasource_get`, `datasource_relation`, and
  `datasource_batch_get`. It does not need to know storage details.
- `plugins/gitlabplugin` imports `runtime/datasource/semantic` directly and uses
  `SearchFieldIndex` and `GetFieldRecord` when `spec.Index.Enabled` is true.
  This is the highest-churn integration point.
- Confluence, connector, session history, filesystem, and GitLab provide
  corpus enumeration for indexing.
- Slack and web search are mostly live provider access today.

The main issue is not package placement. The issue is vocabulary and ownership:
the current "index" is trying to be a field cache, semantic embedding queue,
vector index, run checkpoint store, and partial read model. It should become a
mirror datastore with indexes attached to it.

## Goals

- Make "datasource" mean live or configured data boundary.
- Make "data store" mean scoped local records, relations, and blobs.
- Make "mirror" mean local materialized datastore for datasource entities.
- Make "index" mean a secondary access path over mirrored records.
- Let the mirror satisfy datasource `Getter`, `BatchGetter`, `Lister`,
  `Searcher`, and selected relation reads when it has sufficient data.
- Avoid storage-specific lookup loops in plugins.
- Keep generic datasource operations stable.
- Keep provider plugins focused on declaring entities and producing records.
- Support efficient search with the fewest backend round trips.
- Support a MySQL backend without changing plugin APIs.
- Preserve semantic search as an optional retrieval index over mirrored records.
- Leave room for scoped memory without inventing a second datastore concept.

## Non-goals

- Do not make all datasources mirrored by default. Some sources are live-only,
  high-volume, sensitive, or not enumerable.
- Do not require every plugin to implement a new storage backend.
- Do not make `core/datasource` perform IO or indexing.
- Do not collapse provider-backed live search and local mirror search into one
  hidden behavior. Query mode and freshness policy must stay explicit.
- Do not assume MySQL is the only durable backend. It should be one mirror store
  implementation.

## Vocabulary

### Datasource

A configured boundary for reading entities from an external system, local files,
session history, or another provider.

In code, this remains `core/datasource.Spec`, `Accessor`, and provider
interfaces. The datasource may be live-only, mirrored, or hybrid.

The datasource answers:

- where data comes from;
- which source entity schemas exist in that system;
- which fields identify, filter, sort, search, or describe those entities;
- which source relationships exist between entities and how they are resolved.

It should not decide which query-optimized shapes are stored locally. That is a
view/materialization concern.

### Entity

A logical record type inside a datasource, such as `gitlab.project`,
`slack.user`, or `file.document`.

`EntitySpec` should remain the schema and capability declaration. Fields marked
`identifier`, `searchable`, `filterable`, `sortable`, `url`, or `corpus` are the
portable hints the mirror query planner can use.

Entity relationships belong with source schema metadata. A relation declaration
describes the meaning and target shape, such as `gitlab.user -> groups` or
`gitlab.group -> projects`. It does not by itself decide whether relation data
is joined at query time, embedded into a materialized record, or stored as a
separate edge table.

### Mirror

A local store for configured materializations of datasource records. The mirror
stores view records, tombstones, freshness state, and source checkpoint state.

The mirror is a datastore. It must be able to answer:

- exact get by datasource/entity/id;
- batch get by ids;
- list by datasource/entity with filters and cursor;
- search by datasource/entity, text query, filters, limit, and cursor;
- status/freshness;
- optionally relation reads when relation edges or relation fields are mirrored.

The mirror should be built from explicit materialization specs, not assumed to
mirror every source entity exactly as-is.

### View or Materialization

A materialization is the queryable shape derived from one or more datasource
entities. It is the answer to:

> Which local object shape should exist so the expected queries are cheap and
> useful?

A materialization may be one-to-one with a source entity:

```text
gitlab.project source entity -> gitlab.project mirror view
```

It may also denormalize selected related data:

```text
gitlab.user source entity
  + membership relation
  + minimal group summaries
  -> gitlab.user_with_groups view
```

For the query "find all GitLab users being part of group ABC", the mirror should
not have to repeatedly call the provider or hydrate every group membership one
record at a time. The materialized query shape can expose records like:

```json
{
  "kind": "user",
  "id": "42",
  "name": "Ada",
  "username": "ada",
  "groups": [
    {"id": "10", "path": "abc", "name": "ABC"}
  ]
}
```

The source datasource still owns the schemas for `gitlab.user`,
`gitlab.group`, and `gitlab.membership`. The mirror owns the
`gitlab.user_with_groups` read shape and its indexes.

### Index

A secondary access path used by the mirror store:

- primary key index for exact get;
- field indexes for filters and sorting;
- text index for lexical search;
- relation edge indexes;
- vector index for semantic chunk retrieval.

Index data is derived from mirror records. It is not the source of truth.

### Corpus

Text used for semantic retrieval. Corpus is a projection of mirrored records,
not the mirror itself.

`CorpusDocument` can remain useful internally for chunk planning, but it should
not be the only feed shape for local datastore state.

### Synthetic Datasource Catalog

There should also be a synthetic datasource named `datasource` that exposes the
current registry as datasource records. It is not an external provider and does
not have its own durable mirror. It is a runtime view over the registry,
filtered by the current agent's datasource access policy.

Suggested entities:

- `datasource.source`: one configured datasource instance, including name, kind,
  connector, description, mirror mode, semantic mode, and entity count.
- `datasource.entity`: one entity exposed by one datasource, including entity
  type, description, capabilities, field names, detector names, and relation
  names.
- `datasource.view`: one declared materialized view, including view name,
  source entity, model-facing entity, relation includes, fields, and query
  hints.
- `datasource.relation`: optional detailed relation records when relation
  discovery needs to be searchable independently.

This synthetic datasource should support `list`, `get`, and `search`. It should
answer questions such as "what datasources are available?", "which entities can
search Jira issues?", or "what relations exist for gitlab.user?" without a
separate context-only code path.

### Scoped Memory

Scoped memory should use the same `core/data` store model instead of becoming a
parallel persistence abstraction. Memory records can be a synthetic source such
as `memory` with views like:

- `memory.user_note`: user-scoped durable facts and preferences.
- `memory.agent_note`: agent-scoped operating notes.
- `memory.session_note`: temporary session state.
- `memory.workspace_note`: workspace/project facts.
- `memory.channel_note`: channel-specific shared context.

The scope dimensions are part of the stored record keyspace and query selector:
tenant, app, workspace, user, agent, session, and channel. A query with
`Scope{UserID: "..."}` can retrieve user-visible memory without scanning or
post-filtering unrelated session or workspace data. MySQL should include these
dimensions in secondary indexes; the file and memory stores can keep scope-aware
maps.

## Proposed Package Model

```text
core/data
  SourceSpec, EntitySpec, ViewSpec, Scope, Ref, Record, Summary, Relation,
  BlobRef, Blob, Query, RelationQuery, Store.

core/datasource
  Current live datasource Spec, IndexSpec, SemanticSpec, EntitySpec, FieldSpec,
  RelationSpec, Record, RecordRef, Accessor, Searcher, Lister, Getter,
  BatchGetter, Relationer, Provider, Registry.

runtime/datasource
  EntityOf, conversion helpers, provider-neutral result helpers, detection.

runtime/data
  MemoryStore, datasource schema conversion helpers, initial query primitives.

runtime/datasource/mirror
  Mirror service, Store interfaces, RecordSnapshot, Query, status/run state,
  query planner, live fallback policy helpers, file store.

runtime/datasource/semantic
  Embedder, chunk planner, vector store interface, vector search over mirror
  records. It no longer owns field records or mirror run state.

orchestration/datasourcemirror
  Build/warmup flow that enumerates provider records and writes mirror state.

adapters/mysqlmirror or adapters/datasourcemirror/mysql
  MySQL-backed mirror store.

plugins/datasourceplugin
  Generic datasource operations and context providers. It receives a registry
  whose accessors may be live, mirrored, hybrid, or the synthetic datasource
  catalog.
```

The layer rule stays intact. Core declares inert contracts. Runtime owns mirror
storage behavior. Orchestration owns build flows. Adapters own concrete database
IO. Plugins contribute optional providers and tools.

The current implementation slice adds `core/data` and `runtime/data` first,
without forcing an immediate rename of `core/datasource`. This gives us a stable
target contract for views, scopes, relations, blobs, and store queries while the
existing datasource tools and providers keep working.

## Current Implementation Slice

The first implementation step is intentionally small and low-churn:

- `core/data` defines `SourceSpec`, `ViewSpec`, scoped `Record`, normalized
  `Relation`, blob storage, query shapes, and the `Store` port.
- `runtime/data` provides an in-memory `Store` and conversion helpers from the
  existing `core/datasource.EntitySpec` schema into `core/data.EntitySpec`.
- `runtime/data` owns the shared struct-tag metadata parser for source entities
  and materialized views. It reads `json`, `datasource`, `jsonschema`, and
  `corpus` tags so plugins do not have to duplicate field metadata by hand.
- The initial store API includes scoped get, batch get, query, relations, and
  blob operations so lexical/vector candidates can be hydrated in one store call
  without losing user/session/workspace isolation.
- `runtime/datasource/mirror` owns structured datasource mirror records,
  readiness checks, field search, exact record hydration, and mirror run state.
  `runtime/datasource/semantic` now delegates those responsibilities and keeps
  embedding, vector chunks, queue jobs, and semantic document state.
- `adapters/datasourcemirror/sqlstore` persists the structured legacy
  datasource mirror boundary on `database/sql`, with SQLite coverage and a
  gated MySQL testcontainers contract test.
- `adapters/datastore/sqlstore` persists the provider-neutral `core/data.Store`
  on `database/sql`, including scoped records, denormalized relation summaries,
  normalized relation edges, blobs, SQLite coverage, and a gated MySQL
  testcontainers contract test. It stores JSON payloads as the canonical record
  body and maintains a normalized `data_store_record_field` table so exact field
  filters use backend indexes before the store rechecks full JSON values.
- `plugins/datasourceplugin` exposes the synthetic `datasource` datasource
  through the generic datasource tools. It lists and searches
  `datasource.source`, `datasource.entity`, `datasource.view`, and
  `datasource.relation` records derived from the current registry and filtered
  by the current access policy.
- Plugin-contributed `core/data.SourceSpec` declarations now flow through
  resource contributions. The datasource plugin receives those declarations and
  exposes declared materialized views through the synthetic catalog datasource.
- Runtime-owned durable store selection is configured under manifest
  `runtime.data.store`, not top-level app config. This keeps `core/app.Spec`
  inert and keeps storage selection in launch/runtime metadata.
- `apps/launch` opens a `core/data.Store` from runtime launch config and passes
  it to the datasource plugin and datasource index warmup. Supported kinds are
  in-memory and MySQL via `adapters/datastore/sqlstore`.
- `orchestration/datasourceindex` now performs a data-store materialization pass
  after source records and relation edges are written. Declared views are stored
  as `core/data.Record` rows with `Ref.View` set to the view name. Relation
  includes are embedded as compact summaries and indexed into fields such as
  `groups.full_path` or `members.email`.
- The materialization pass first uses stored relation edges. If a declared view
  includes a relation that has not been mirrored as edges and the accessor
  implements `Relationer`, it performs a bounded live relation lookup during the
  build and stores both the relation edge and the materialized view summary. This
  supports Slack `channel_with_members` while GitLab membership views use stored
  membership edges.
- `plugins/gitlabplugin` declares default data views, including
  `gitlab.user_with_groups` for queries like "find all GitLab users in group
  ABC".
- `plugins/slackplugin` declares default data views, including
  `slack.channel_with_members` for questions about channel membership.
- `core/datasource` remains the live accessor contract until the mirror query
  planner and hybrid accessor can consume `core/data.Store` directly.

This keeps upstream plugin changes declarative: a plugin owns source schema and
default materialization declarations, while runtime chooses whether those views
are served from memory, file, MySQL, or live fallback.

View declarations should prefer typed structs plus `runtime/data.ViewOf` for
normal and denormalized fields. Manual `FieldSpec` lists remain an escape hatch
for projections that cannot be represented cleanly as Go structs.

## Plugin View Ownership

Plugin packages should declare default views because they know which source
relations are meaningful and what minimal related fields are useful to agents.
Product/app configuration can still override or disable views for storage,
privacy, or freshness reasons.

In the current implementation slice, GitLab and Slack contribute their default
`core/data.SourceSpec` declarations from the plugin contribution bundle:

- GitLab declares one-to-one project/user/group/membership views plus
  `gitlab.user_with_groups`.
- Slack declares one-to-one user/channel/message views plus
  `slack.channel_with_members`.

The declarations use typed structs and `runtime/data.ViewOf` so JSON,
`datasource`, `jsonschema`, and `corpus` tags remain the single source for field
metadata. The runtime can search those declarations through
`datasource_list(datasource="datasource", entity="datasource.view")` or
`datasource_search(..., entities=["datasource.view"])`.

Options:

| Option | Upside | Downside |
|---|---|---|
| Plugin-declared defaults | Low app churn; source schema and recommended read models stay together. | Plugins must avoid over-materializing sensitive/high-volume relations. |
| App-only views | Products control storage and privacy exactly. | Every app repeats GitLab/Slack knowledge and agents get uneven behavior. |
| Runtime-generated one-to-one views only | Lowest initial plugin work. | Relation-heavy questions still need live fanout or N lookups. |

Recommended policy: plugins declare conservative default views, apps can opt out
or add richer materializations, and runtime can synthesize one-to-one views for
mirror-capable entities when no explicit view exists.

## Core Shape

Replace `Spec.Index` with a mirror-oriented configuration in the core model:

```go
type Spec struct {
    Name        Name
    Description string
    Entities    []EntityType
    Connector   string
    Kind        string
    Config      map[string]string
    Mirror      MirrorSpec
    Semantic    SemanticSpec
    Annotations map[string]string
}

type MirrorSpec struct {
    Enabled          bool
    Freshness        string
    Mode             string // auto, mirror, live, hybrid
    Materializations []MaterializationSpec
}

type MaterializationSpec struct {
    Name        string
    Entity      EntityType
    Source      EntityType
    Includes    []RelationIncludeSpec
    Fields      []FieldProjectionSpec
    QueryHints  []string // get, list, search, relation, aggregate
    Freshness   string
}
```

Rename `EntityCapabilityIndex` to `EntityCapabilityMirror` in the concept model.
Entities that support local materialization declare `mirror`. Entities that
support embedding-based retrieval still declare `semantic_search`.

The implementation can keep the initial migration mechanical:

- `Spec.Index.Enabled` call sites become `Spec.Mirror.Enabled`.
- `EntityCapabilityIndex` call sites become `EntityCapabilityMirror`.
- CLI strings and config docs change from "index" to "mirror" where they refer
  to local datastore state.
- The embedding/vector path can still use "semantic index" when it specifically
  means vector search.

Materialization specs should be optional. If none are configured, the runtime can
create a default one-to-one materialization for each mirror-capable source
entity. Query-heavy products can add explicit views:

```yaml
datasources:
  - name: gitlab
    kind: gitlab
    entities:
      - gitlab.user
      - gitlab.group
      - gitlab.membership
    mirror:
      enabled: true
      materializations:
        - name: gitlab.user
          source: gitlab.user
          query_hints: [get, list, search]
        - name: gitlab.user_with_groups
          entity: gitlab.user
          source: gitlab.user
          includes:
            - relation: groups
              target: gitlab.group
              fields: [id, path, name]
          fields:
            - name: groups.path
              filterable: true
            - name: groups.id
              filterable: true
          query_hints: [list, search, relation]
```

This keeps source schema in the datasource while making query-optimized local
read shapes explicit.

## Mirror Record Model

The mirror should store view records, not only corpus text. A view record may be
an exact source record snapshot or a denormalized materialized object:

```go
type RecordSnapshot struct {
    Key         string
    Ref         coredatasource.RecordRef
    View        string
    Record      coredatasource.Record
    Fields      map[string][]FieldValue
    Relations   map[string][]RecordSummary
    Corpus      coredatasource.CorpusDocument
    Fingerprint string
    UpdatedAt   string
    ObservedAt  time.Time
    DeletedAt   time.Time
    Source      SourceState
}

type FieldValue struct {
    Name   string
    Value  string
    Number *float64
    Bool   *bool
}

type RecordSummary struct {
    Ref    coredatasource.RecordRef
    Title  string
    Fields map[string]string
}
```

`Record` is the canonical value returned to agents for the view. `Fields` is
the normalized query projection derived from `EntitySpec.Fields`,
`MaterializationSpec.Fields`, `Record.Metadata`, and later typed record
adapters. `Relations` stores small related summaries only when the
materialization chooses to embed them. `Corpus` is optional text/chunk material
used by semantic retrieval. `Fingerprint` drives idempotent writes.

The current `CorpusProvider` feed can be adapted into `RecordSnapshot` for the
first implementation, but the preferred provider-side contract should become a
mirror feed:

```go
type MirrorProvider interface {
    Mirror(context.Context, MirrorRequest) (MirrorPage, error)
}

type MirrorRequest struct {
    Entity       EntityType
    Cursor       string
    Limit        int
    ChangedSince string
    Full         bool
}

type MirrorPage struct {
    Records    []RecordSnapshot
    Tombstones []RecordRef
    NextCursor string
    Complete   bool
}
```

To reduce plugin churn, add helper adapters:

- `RecordsToSnapshots(records []Record, entity EntitySpec)`;
- `CorpusDocumentsToSnapshots(docs []CorpusDocument)`;
- `MirrorPageFromList(result ListResult, entity EntitySpec)`.

Most existing plugins can continue producing records with their existing
conversion functions. GitLab membership and Confluence pages are good examples:
their current record shapes already contain stable IDs, titles, content, URLs,
and filterable metadata.

Materializations should be configured by query need. For GitLab, a minimal first
set could be:

- `gitlab.project`: exact source-entity view for project get/list/search.
- `gitlab.user`: exact source-entity view for user get/list/search.
- `gitlab.group`: exact source-entity view for group get/list/search.
- `gitlab.user_with_groups`: denormalized user view with minimal group
  summaries for membership queries.
- `gitlab.membership`: edge/source view when precise membership provenance or
  relation traversal is needed.

## Mirror Store Interface

The store interface should express operations the query planner needs, not the
current JSON file layout:

```go
type Store interface {
    UpsertRecords(context.Context, []RecordSnapshot) error
    DeleteRecords(context.Context, []RecordRef) error

    GetRecord(context.Context, RecordRef) (RecordSnapshot, bool, error)
    BatchGetRecords(context.Context, []RecordRef) ([]RecordSnapshot, error)
    QueryRecords(context.Context, Query) (QueryResult, error)

    PutRun(context.Context, RunState) error
    Run(context.Context, RunKey) (RunState, bool, error)
    Status(context.Context, StatusRequest) (StatusResult, error)
}

type Query struct {
    Datasources     []Name
    Entities        []EntityType
    View            string
    IDs             []string
    Query           string
    Filters         map[string]string
    RelationFilters map[string]map[string]string
    Sort            []SortField
    Limit           int
    Cursor          string
    Mode            QueryMode // exact, list, lexical, semantic, hybrid
}
```

`QueryRecords` returns records, scores, reasons, and an opaque cursor. The
backend may return fully hydrated records when that is cheaper, or return keys
and perform one batch hydration inside the store. Callers should not have to run
N provider lookups after a mirror search.

## Query Planning

The mirror service should choose the cheapest access path in this order:

1. Exact IDs: use primary-key lookup or batch primary-key lookup.
2. Filter-only list: use field indexes and stable cursor ordering.
3. Relation-filtered list: use a relation edge index or a denormalized relation
   field index, depending on the materialization.
4. Text plus filters: use backend full-text or inverted text index, constrained
   by datasource/entity/filter predicates.
5. Semantic: use vector top-K to get candidate record keys, then batch hydrate
   mirror records.
6. Hybrid: get lexical candidates and vector candidates, merge scores by record
   key, then batch hydrate once.
7. Live fallback: only when policy allows it and mirror freshness/readiness is
   insufficient.

The planner must keep result hydration close to the store. A search operation
should not do:

```text
search keys -> plugin Get(key1) -> plugin Get(key2) -> ...
```

It should do:

```text
query backend index -> batch hydrate local records -> return records
```

For MySQL this means one query can often return full rows directly. For vector
search backed by a separate vector store, the mirror service should make one
`BatchGetRecords` call for the winning keys.

The planner should support two physical strategies for related data:

- **Join materialization**: store normalized records and relation edges, then
  answer relation-filtered queries with joins or edge-index lookups.
- **Embedded materialization**: store selected related summaries inside the view
  record bytes and index the embedded relation fields needed by queries.

The choice is per materialization, not global.

## File Store Backend

The current `JSONStore` reads, scans, and rewrites one JSON document. That is
acceptable for tests and tiny local mirrors, but it is not a good conceptual
store.

The next file backend should be `runtime/datasource/mirror.FileStore`:

- load a persisted snapshot on open;
- keep in-memory maps for primary key, filters, sortable fields, and token
  search;
- update those maps on each upsert/delete;
- persist atomically to disk;
- optionally append a write-ahead JSONL log and compact to a snapshot for larger
  local mirrors.

Minimum in-memory indexes:

- `byKey[key]RecordSnapshot`;
- `byEntity[datasource][entity][]key`;
- `byView[datasource][view][]key`;
- `byFilter[datasource][entity][field][normalizedValue]set(key)`;
- `byViewFilter[datasource][view][field][normalizedValue]set(key)`;
- `byRelation[datasource][view][relation][targetEntity][targetID]set(key)`;
- `byToken[datasource][entity][token]set(key)`;
- `runState[runKey]RunState`.

This gives file-backed search O(candidate set) instead of O(all records), while
still using a simple file format suitable for local development.

The file format should be named as a mirror store, for example:

```text
.agents/mirror/datasources.json
```

or, for the log plus snapshot form:

```text
.agents/mirror/datasources.snapshot.json
.agents/mirror/datasources.log.jsonl
```

## MySQL Backend

A MySQL implementation should store records and secondary projections in
separate tables so native indexes can do the work.

The MySQL backend should support both normalized and embedded materialization
styles. Normalized relation queries use edge tables and joins. Embedded
materializations store compact related summaries in `record_json`, plus selected
relation fields in indexed projection tables so queries do not have to scan JSON
bytes.

Recommended tables:

```sql
CREATE TABLE datasource_mirror_record (
  datasource VARCHAR(128) NOT NULL,
  view_name VARCHAR(128) NOT NULL,
  entity VARCHAR(128) NOT NULL,
  id VARCHAR(512) NOT NULL,
  title TEXT,
  content MEDIUMTEXT,
  url TEXT,
  record_json JSON NOT NULL,
  metadata_json JSON,
  fingerprint VARCHAR(128),
  updated_at VARCHAR(64),
  observed_at TIMESTAMP(6) NOT NULL,
  deleted_at TIMESTAMP(6) NULL,
  PRIMARY KEY (datasource, view_name, id),
  KEY mirror_entity_observed (datasource, entity, observed_at),
  KEY mirror_view_observed (datasource, view_name, observed_at),
  KEY mirror_entity_updated (datasource, entity, updated_at),
  FULLTEXT KEY mirror_text (title, content)
);

CREATE TABLE datasource_mirror_field (
  datasource VARCHAR(128) NOT NULL,
  view_name VARCHAR(128) NOT NULL,
  entity VARCHAR(128) NOT NULL,
  id VARCHAR(512) NOT NULL,
  field VARCHAR(128) NOT NULL,
  value_text VARCHAR(1024) NOT NULL,
  value_norm VARCHAR(1024) NOT NULL,
  is_identifier BOOLEAN NOT NULL DEFAULT FALSE,
  is_filterable BOOLEAN NOT NULL DEFAULT FALSE,
  is_sortable BOOLEAN NOT NULL DEFAULT FALSE,
  PRIMARY KEY (datasource, view_name, id, field, value_norm),
  KEY mirror_field_filter (datasource, view_name, field, value_norm),
  KEY mirror_entity_field_filter (datasource, entity, field, value_norm),
  KEY mirror_field_identifier (datasource, view_name, field, is_identifier, value_norm)
);

CREATE TABLE datasource_mirror_relation (
  datasource VARCHAR(128) NOT NULL,
  view_name VARCHAR(128) NOT NULL,
  entity VARCHAR(128) NOT NULL,
  id VARCHAR(512) NOT NULL,
  relation VARCHAR(128) NOT NULL,
  target_datasource VARCHAR(128) NOT NULL,
  target_entity VARCHAR(128) NOT NULL,
  target_id VARCHAR(512) NOT NULL,
  target_title TEXT,
  target_json JSON,
  PRIMARY KEY (datasource, view_name, id, relation, target_datasource, target_entity, target_id),
  KEY mirror_relation_source (datasource, view_name, relation, entity, id),
  KEY mirror_relation_target (target_datasource, target_entity, target_id)
);

CREATE TABLE datasource_mirror_run (
  datasource VARCHAR(128) NOT NULL,
  entity VARCHAR(128) NOT NULL,
  phase VARCHAR(64) NOT NULL,
  status VARCHAR(32) NOT NULL,
  cursor_text TEXT,
  started_at TIMESTAMP(6) NULL,
  completed_at TIMESTAMP(6) NULL,
  counts_json JSON,
  last_error TEXT,
  PRIMARY KEY (datasource, entity, phase)
);

CREATE TABLE datasource_mirror_semantic_job (
  datasource VARCHAR(128) NOT NULL,
  entity VARCHAR(128) NOT NULL,
  id VARCHAR(512) NOT NULL,
  model VARCHAR(256) NOT NULL,
  chunking_hash VARCHAR(128) NOT NULL,
  status VARCHAR(32) NOT NULL,
  attempts INT NOT NULL DEFAULT 0,
  job_json JSON NOT NULL,
  enqueued_at TIMESTAMP(6) NOT NULL,
  updated_at TIMESTAMP(6) NOT NULL,
  PRIMARY KEY (datasource, entity, id, model, chunking_hash),
  KEY mirror_semantic_queue (status, updated_at)
);
```

Semantic chunks can be stored in MySQL if the deployment has a usable vector
feature, but the store contract should not require that. A sidecar vector store
can store chunks keyed by `(datasource, entity, id, chunk_id)` and the mirror can
hydrate records from MySQL after vector retrieval.

Efficient MySQL query patterns:

- exact get: primary key on `datasource_mirror_record`;
- batch get: tuple `IN` over primary keys;
- filter list: join or semijoin `datasource_mirror_field` rows by indexed
  `field,value_norm`, then join record rows once;
- relation-filtered list: use `datasource_mirror_relation` when relations are
  normalized, or use indexed `datasource_mirror_field` projections when the
  materialized view embeds related summaries;
- text search: `MATCH(title, content) AGAINST (...)` plus datasource/entity and
  optional filter semijoins;
- hybrid search: full-text top N plus vector top N, merge in Go, batch get
  records by primary key.

The MySQL store should expose opaque cursor tokens rather than leaking offsets.
For stable list/search pagination, encode the last sort key and primary key in
the cursor.

## Registry and Accessor Wrapping

Datasource registry construction should wrap accessors when mirror services are
available:

```text
provider.Open(spec) -> live accessor
if spec.Mirror.Enabled:
  accessor = mirror.NewHybridAccessor(live accessor, mirror service, spec)
registry.Add(accessor)
```

The hybrid accessor can implement:

- `Get` and `BatchGet` from mirror when records exist and freshness policy
  allows it;
- `List` from mirror for filterable mirrored entities;
- `Search` from mirror for lexical, semantic, or hybrid mode;
- `Relation` from mirror only when relation edges are materialized, otherwise
  delegate live.

This removes the need for GitLab-specific calls to `semantic.SearchFieldIndex`
for generic mirrored entity reads. GitLab may still keep custom live relation
logic for entities that are not mirrored.

Mode behavior:

| Mode | Behavior |
|---|---|
| `mirror` | Use mirror only. Fail with mirror-not-built or stale errors when needed. |
| `live` or `provider` | Bypass mirror and call provider. |
| `auto` | Use mirror when enabled, fresh, and queryable; otherwise live fallback. |
| `hybrid` | Use local mirror candidates plus optional live provider search where implemented. |
| `semantic` | Use semantic vector index over mirrored records. |

The generic datasource tools can keep their current inputs. The `mode` field
already accepts `auto`, `semantic`, `hybrid`, `lexical`, `provider`, and `live`.

## Synthetic Datasource Accessor

The synthetic `datasource` accessor should be built after live/hybrid accessors
are assembled:

```text
registry = BuildRegistry(live and hybrid accessors)
catalog = datasourcecatalog.NewAccessor(registry)
registry = registry.With(catalog)
```

It should not cause infinite recursion. The catalog accessor may include a
record for itself, but it must not need itself to build its records.

Record shape:

```text
datasource.source ID = <datasource name>
datasource.entity ID = <datasource name>/<entity type>
datasource.relation ID = <datasource name>/<entity type>/<relation name>
```

The accessor should derive records from the same policy-filtered view currently
used by `datasource.catalog`:

- `List(datasource.source)` returns allowed datasource instances.
- `List(datasource.entity)` returns allowed datasource/entity pairs.
- `Get(datasource.source, name)` returns one allowed datasource summary.
- `Get(datasource.entity, name/type)` returns one allowed entity summary.
- `Search` matches datasource names, kinds, descriptions, entity types,
  capabilities, fields, detectors, and relations.

The `datasource.catalog` context provider should then render from this synthetic
datasource instead of duplicating registry traversal logic. That keeps the model
visible through both context and tools:

```text
datasource.catalog context
  -> datasource.List(datasource.source)
  -> datasource.List(datasource.entity)
  -> render compact text and structured metadata
```

Authorization should remain conservative. The synthetic datasource must never
show a datasource or entity that the current agent could not otherwise access.
The implementation can either make `datasource` an always-present internal
read-only datasource whose contents are policy-filtered, or explicitly inject
`datasource` into each agent's datasource refs when the datasource plugin is
enabled. The first option is less config churn; the second is more explicit.

## Semantic Retrieval

Semantic search should depend on the mirror, not own it.

Recommended split:

- `mirror.Build` writes `RecordSnapshot` rows and queues semantic work for rows
  whose entity/spec requests semantic search.
- `semantic.Service` reads mirror snapshots, plans chunks, embeds text, writes
  vector chunks, and stores per-record semantic state.
- `mirror.Search(mode=semantic)` calls the semantic service for candidate refs,
  then hydrates records from the mirror store.

`CorpusDocument` becomes a projection:

```text
RecordSnapshot -> CorpusDocument -> Chunks -> EmbeddedChunks
```

Provider plugins may still provide custom chunks for better semantic quality,
but the mirror stores the canonical record snapshot first.

## Configuration and CLI Names

Rename app config and CLI surfaces from index to mirror where they refer to
field records and local datastore state:

```yaml
datasource:
  mirror:
    concurrency: 4
    freshness: 10m
  datasources:
    - name: gitlab
      kind: gitlab
      entities: [gitlab.project, gitlab.user, gitlab.group]
      mirror:
        enabled: true
        freshness: 30m

semantic_search:
  enabled: true
  embeddings:
    provider: axon
  store:
    kind: file
    path: .agents/mirror/datasources.json
```

CLI:

```bash
coder datasource mirror build .
coder datasource mirror embed .
coder datasource mirror status .
coder datasource mirror clear .
```

If the unified coder surface lands first, the same commands should move to:

```bash
coder datasource mirror build .
```

The word "index" should remain only for implementation-specific secondary
structures or semantic/vector index documentation.

## Migration Plan

### Slice 1: introduce neutral data substrate

- Add `core/data` as the future-facing model for sources, views, scopes,
  records, relations, blobs, queries, and store ports.
- Add `runtime/data` with a memory store and datasource schema conversion
  helpers.
- Let GitLab and Slack declare default materialized views in their plugin
  packages.
- Keep existing `core/datasource` accessors unchanged during this slice.

### Slice 2: rename and isolate datasource mirror concepts

- Add the design vocabulary to docs.
- Rename `orchestration/datasourceindex` to `orchestration/datasourcemirror`.
- Rename public CLI strings from datasource index to datasource mirror.
- Rename `Spec.Index` to `Spec.Mirror` and `EntityCapabilityIndex` to
  `EntityCapabilityMirror`.
- Keep the existing JSON-backed implementation operational while it is moved
  behind mirror-oriented names.

### Slice 3: create mirror runtime package

- Move field record storage, run state, status, and readiness checks from
  `runtime/datasource/semantic` into `runtime/datasource/mirror`.
- Add `RecordSnapshot`, `Store`, `Query`, `QueryResult`, and a file-backed
  store.
- Keep semantic embedding interfaces in `runtime/datasource/semantic`.
- Make semantic retrieval hydrate records through the mirror store.

### Slice 4: wrap accessors generically

- Add `mirror.HybridAccessor`.
- Inject the mirror service through `datasourceplugin.RegistryOptions` or a
  renamed assembly option.
- Remove GitLab's direct dependency on `runtime/datasource/semantic` for field
  indexed membership/search reads.
- Let GitLab delegate mirrored `Get`, `List`, `Search`, and relation reads to
  the generic wrapper when possible.

### Slice 5: improve provider feeds

- Add `MirrorProvider` or evolve `CorpusProvider` into a mirror feed.
- Convert current corpus pages into record snapshots using helpers.
- Update Confluence, GitLab, connector, filesystem, skill, and session history
  providers incrementally.
- Keep live-only Slack/web behavior unchanged unless a datasource is configured
  as mirrorable.

### Slice 6: MySQL backend

- Add a MySQL mirror store adapter. The first version now exists for both the
  structured datasource mirror boundary and the provider-neutral `core/data`
  store.
- Implement primary-key, batch, field-filter, and status queries. Full-text is
  still a backend optimization step; the current contract uses indexed
  datasource/entity/view/id prefilters plus in-process matching for portable
  SQLite/MySQL behavior.
- Add migration/schema setup through the adapter package.
- Keep vector storage separate unless the selected MySQL deployment supports a
  suitable vector index.

## Datasource Package Evolution

`core/datasource` should evolve from "source plus index plus semantic plus
materialized cache" into the live-source contract only. It should keep source
schema, detectors, relationships, live accessor interfaces, and provider
registry contracts until callers move to `core/data` for local storage and
materialized views.

Removal policy in this pre-1.0 rewrite:

- Do not add long-lived backwards compatibility adapters.
- Temporary conversion helpers may live in `runtime/data` while
  `core/datasource` providers still declare entities.
- Remove `Spec.Index`, index-oriented capability names, and direct field-index
  access from plugins once the hybrid accessor can serve mirrored get/list/search
  generically.
- Remove conversion helpers when plugins declare `core/data.SourceSpec` and
  `core/data.ViewSpec` directly or when `core/datasource` has been narrowed to
  live access only.

## Churn Management

The highest-value low-churn path is to keep provider access contracts stable
while moving storage behavior out of plugins.

Expected plugin changes:

- mechanical rename from index to mirror capability/config;
- optional feed signature update if `MirrorProvider` is introduced;
- removal of GitLab-specific field-index helper calls after hybrid accessor
  wrapping exists.

Expected unchanged surfaces:

- `datasource_search`, `datasource_list`, `datasource_get`,
  `datasource_relation`, `datasource_batch_get`;
- `Record`, `RecordRef`, `EntitySpec`, and field tags;
- detector behavior;
- live provider implementations for non-mirrored entities.

Expected new surface:

- a synthetic `datasource` datasource that backs catalog discovery through the
  same `datasource_*` operations and the `datasource.catalog` context provider.

## Open Questions

- Should mirror state store raw provider payloads in `Record.Raw`, or should raw
  payload persistence be opt-in per datasource due to size and sensitivity?
- Should relation edges be first-class in v1, or derived from mirrored fields
  only until a plugin needs generic mirrored relation traversal?
- For each relation-heavy materialization, should related objects be joined from
  normalized relation tables or embedded into the stored view bytes with
  selected relation fields indexed? Joins reduce storage and update fanout;
  embedded summaries reduce query-time lookups and make model-facing records
  immediately useful.
- Should semantic queue jobs live in the mirror store or in a separate workflow
  queue abstraction once broader job queues exist?
- Should mirror freshness be enforced at query time, build time, or both?
- Should MySQL table keys include app, tenant, or workspace scope now, or wait
  until multi-tenant runtime state is first-class?
- Should the synthetic `datasource` catalog be always readable when the
  datasource plugin is enabled, or should agents explicitly include
  `datasource` in their datasource refs?

## Recommendation

Do the rename and package split before adding another backend. A MySQL
implementation will be much cleaner if it targets a `mirror.Store` interface
whose contract is "store and query local entity records" instead of the current
`semantic.Store` interface whose contract is a bundle of vectors, field records,
queue jobs, and run state.

The first usable end state should be:

- a file-backed mirror that replaces the current JSON index store;
- generic mirrored get/list/search through a hybrid accessor;
- semantic vectors as optional secondary indexes over mirror records;
- GitLab membership lookups using the generic mirror instead of plugin-local
  field-index helpers.
