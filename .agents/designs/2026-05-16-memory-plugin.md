# DESIGN: Memory Plugin

Implemented workspace model dependency. Workspace-scoped memory should use
`core/workspace.ID` rather than a memory-local workspace identity, and should
respect `core/workspace.Durability`: durable configured/provider-backed
workspaces are suitable for cross-session memory keys, while ephemeral local
fallback workspaces are not durable by default.

## Summary

Add a first-party `plugins/memoryplugin` that lets agents store, retrieve,
forget, and organize structured memories. Memories are durable agent-assistive
state, not hidden prompts: every mutation is represented as domain events in the
append-only event store and materialized by replaying the relevant scope's event
streams.

The plugin should contribute:

- model-facing memory operations for explicit agent actions;
- one or more smart context providers that surface relevant memories for the
  current turn;
- optional integration with repository/user memory files such as `MEMORY.md`,
  without making files the primary source of truth.

## Research notes

A brief scan of common agent memory patterns suggests these recurring concepts:

- local/project memory files such as `CLAUDE.md`, `AGENTS.md`, or `MEMORY.md`
  for durable instructions and preferences near a workspace;
- separate user-level memory for preferences, stable identity facts, and working
  style across sessions;
- conversation/session memory for short-term facts and decisions from the active
  thread;
- retrieval-augmented memory, where a context provider injects only memories
  relevant to the current task instead of dumping the whole store;
- explicit delete/forget controls, because learned memories can become stale,
  wrong, sensitive, or user-rejected.

The repository already has `AGENTS.md` and `.agents` resources for operative
agent notes. Memory should complement those resources, not replace them.

## Problem

Agents currently have durable session history, project files, and static agent
notes, but they lack a first-class way to intentionally remember structured facts
or decisions for future work. If memory is stored only in free-form files, the
runtime cannot reliably scope, retrieve, expire, audit, or forget it. If memory
is stored only in model context, it is lost after compaction, continuation, or a
new session.

We need a runtime-native memory capability with explicit scopes, structured
records, and event-sourced persistence.

## Goals

- Provide explicit operations: store, retrieve, forget, and organize memories.
- Support scoped memory for the current session, workspace, user, and channel-like
  surfaces.
- Reconstruct memory by replaying event records from `event.Store` streams.
- Preserve auditability: who/what wrote a memory, when, from which session/turn,
  and with what sensitivity.
- Support structured memory values, not only markdown snippets.
- Allow smart context providers to inject relevant memories under a token budget.
- Integrate with `MEMORY.md`-style files as import/export/context surfaces while
  keeping event-store memory authoritative.
- Keep the plugin optional and contributed through existing plugin contracts.
- Keep privacy and forget semantics explicit.

## Non-goals

- No global hidden memory automatically available to all agents.
- No direct filesystem, network, or model calls outside existing runtime/system
  and operation safety boundaries.
- No new persistence backend separate from `event.Store`.
- No cross-user memory sharing by default.
- No guarantee that append-only event storage can physically erase historical
  sensitive data in the first version. Physical purge is a separate storage and
  compliance capability.
- No autonomous background memory mining in v1. Memories are written by explicit
  tool calls or explicit import/organize operations.

Memory scope should be explicit on every operation and reflected in event record
scope metadata. Recommended v1 scopes:

| Scope | Meaning | Typical use |
|---|---|---|
| `session` | Current thread/session only. | Temporary facts, unresolved TODOs, decisions local to this conversation. |
| `workspace` | Current workspace root or repository/worktree boundary, independent from the narrower `core/project.Project` inventory concept. | Repo conventions, architecture decisions, recurring commands, workspace-specific preferences. |
| `user` | Current canonical `core/user.User` across sessions/workspaces where the user is known. | Communication preferences, stable personal preferences, recurring constraints. |
| `channel` | Current external channel or conversation surface. | Slack channel norms, shared channel context, standing team instructions. |

`user` scope should operate on canonical users, not provider identities. Provider
identities from `core/user.Identity` are resolver inputs and provenance, while
memory ownership should resolve to `core/user.ID` when possible.

`workspace` is intentionally not named `project` for v1. `core/project.Project`
represents detected units inside a workspace inventory, such as Go modules,
package manifests, or docs. Memory scope usually needs the broader working root
or multi-root working set where the agent is operating. The workspace concept is
defined separately in [2026-05-16-workspace-model.md](2026-05-16-workspace-model.md)
and should live in `core/workspace`. A workspace memory may cite
`core/project.ID` values in provenance or structured refs, but its scope identity
is `core/workspace.ID`.
Workspace scope must be backed by a `core/workspace.Selection`, not by a raw
filesystem path. The selected workspace supplies the active `workspace.ID`,
ancestor IDs for read expansion, and durability metadata for write policy.


Candidate future scopes:

- `app`: distribution/app-level memory shared by agents in one assembled app.
- `tenant` or `organization`: team-wide memory, only when tenancy and access
  policy are mature.
- `agent_profile`: memory bound to a configured agent persona. This should be
  used cautiously because the user asked for memory about the user/project, not
  private self-modification by the agent.
- `task` or `workflow`: memory bound to a long-running plan/workflow. This may
  later be distinct from session memory when workflow identity is first-class.

Scopes should be composable internally. For example, workspace memory should
include a `core/workspace.ID` and may include root/origin provenance, optional
related `core/project.ID` values, and optional `TenantID`; user memory should
include a canonical `core/user.ID` plus provider identity provenance when useful.
Workspace write policy should account for durability:

- durable workspace IDs (`workspace:configured:*`, provider-backed canonical
  origins, or declarations marked durable) may be used for cross-session
  workspace memory;
- ephemeral workspace IDs (`workspace:local:*` fallback) should not receive
  durable workspace memories by default;
- if only an ephemeral workspace is available, the memory operation should either
  store in session scope, ask for/require a durable workspace declaration, or
  mark the record as explicitly local/ephemeral depending on product policy.


## Memory record shape

A materialized memory should have a stable ID and structured metadata:

- `id`: stable memory identifier, generated on first store.
- `scope`: normalized scope selector.
- `kind`: broad memory kind, for filtering and context formatting.
- `title`: short human-readable label.
- `content`: concise natural-language summary.
- `data`: optional JSON object for structured facts.
- `tags`: small normalized labels.
- `source`: session/thread/turn/operation provenance.
- `confidence`: optional model-assessed confidence or user-confirmed marker.
- `sensitivity`: core policy sensitivity.
- `created_at`, `updated_at`.
- `expires_at`: optional expiry for volatile memories.
- `status`: active, archived, forgotten, superseded.
- `supersedes`: optional previous memory IDs.

Suggested `kind` values:

- `fact`: stable fact about a project, user, channel, or domain.
- `preference`: user or team preference.
- `instruction`: standing instruction that may affect behavior.
- `decision`: decision and rationale.
- `procedure`: reusable workflow or command sequence.
- `entity`: structured contact, system, service, ticket, or account reference.
- `summary`: compact summary of a session or topic.
- `artifact_ref`: pointer to a file path, URL, datasource entity, or event.

## Event model

`core/memory` should define memory domain events as durable contracts. The
first-party event catalog should register them when memory support is linked.

Minimum events:

- `memory.stored`: creates or updates a memory.
- `memory.forgotten`: marks one or more memories as forgotten/tombstoned.
- `memory.organized`: records merges, retags, archive/supersede actions, or
  generated summaries.

Additional events:

- `memory.context_selected`: compact audit event recording which memory IDs a
  context provider injected, with provider name, render reason, scope, ranking
  metadata, and budget information. It should not duplicate full memory content.
- `memory.imported`: records import from `MEMORY.md` or another source.
- `memory.exported`: records file export/checkpoint.

`runtime/memory` owns event stream strategy and should optimize scoped replay:

- session memories: `memory/session/{session_id}` or thread-equivalent stream;
- workspace memories: `memory/workspace/{workspace_id_or_fingerprint}`;
- user memories: `memory/user/{user_id}`;
- channel memories: `memory/channel/{channel_id}`.

The event record's `Scope` should also carry available `SessionID`, `UserID`,
`ChannelID`, `AgentID`, `ThreadID`, and `OperationID` for audit and
correlation. User memory payloads should reference canonical `core/user.ID` and
may include `core/user.Identity` values as provenance/resolver evidence.
Workspace memory payloads should reference `core/workspace.ID`; workspace roots,
origins, aliases, and related `core/project.ID` values can be attached as
structured refs/provenance. The stream key is for efficient replay; the record
scope is for querying, filtering, and provenance.

Because the store is append-only, `forget` is normally a tombstone in the
materialized view. Physical erasure or crypto-shredding should be handled by a
separate storage-policy capability if required.
## Operations

### `memory_store`

Stores a new memory or updates/supersedes an existing one.

Inputs:

- `scope`: explicit scope selector, defaulting only when the host can unambiguously
  identify the current session/project/user/channel.
- `kind`, `title`, `content`, `data`, `tags`.
- optional `memory_id` for update.
- optional `supersedes` IDs.
- optional `expires_at`, `sensitivity`, and `confidence`.
- optional `source_refs` to files, datasource records, messages, or operations.

Output:

- stored memory ID;
- normalized scope;
- rendered summary;
- warnings for missing identity, broad scope, sensitivity, or likely duplicates.

### `memory_retrieve`

Retrieves active memories by ID, filters, text query, semantic query, or current
work context.

Inputs:

- `scope` or list of scopes;
- optional `ids`, `kinds`, `tags`, `query`, `since`, `limit`;
- `include_archived` and `include_forgotten` default false;
- output mode: concise text, structured records, or both.

Output:

- ranked memories with IDs, scope, kind, title, content, tags, and provenance;
- explanation of scope coverage and any skipped scopes.

### `memory_forget`

Forgets, archives, or expires memories.

Inputs:

- `scope`;
- `ids` or constrained query;
- `mode`: `forget`, `archive`, `expire`, or future `purge_request`;
- optional `reason`.

Output:

- affected memory IDs;
- tombstone/archive event summary;
- warning that append-only event stores retain historical events unless a
  storage-level purge capability is available.

The operation should require precise selection for user/project-wide forgetting.
Broad query-based forgetting should be treated as higher-risk by the safety
policy.

### `memory_organize`

Curates existing memories without necessarily adding new facts.

Inputs:

- `scope`;
- optional filters/query;
- actions such as `deduplicate`, `merge`, `retag`, `summarize`, `archive_stale`,
  `promote_scope`, or `demote_scope`;
- optional target memory IDs and replacement content.

Output:

- proposed or applied organization actions;
- memories created, superseded, archived, or retagged;
- warnings when promoting narrower-scope memory into broader scope.

For v1, `memory_organize` may be conservative and require explicit action
inputs rather than fully autonomous curation.

## Context providers

The plugin should contribute smart context providers in addition to operations.

### `memory.relevant`

Dynamic provider that selects a small set of relevant active memories for the
current turn. It should consider:

- current render reason and token budget;
- request scope and available session/workspace/user/channel IDs;
- current thread messages or task summary if available in the request;
- recency, kind, sensitivity, and explicit tags;
- retrieval score and diversity across scopes.

Default placement should be `user_context` or `system_context` depending on how
context policy evolves. The block must clearly identify its source as memory and
include memory IDs so the agent can update or forget stale entries.

### `memory.catalog`

Lightweight dynamic provider listing enabled memory scopes and operation hints.
This is useful when no memories match but the agent should know that memory tools
exist.

### `memory.project_file`

Optional provider that reads `MEMORY.md` or configured memory-file paths through
the workspace system adapter. This provider should present file memory as a
separate source, or as imported memory with provenance, to avoid confusing file
contents with event-sourced records.

## `MEMORY.md` integration

`MEMORY.md` is useful because it is visible to humans, portable with a repo, and
works outside this runtime. It is also lossy compared with structured event
memory. Treat it as an integration surface, not the primary database.

Recommended behavior:

- discover `MEMORY.md` in workspace roots and possibly `.agents/MEMORY.md`;
- parse simple markdown sections into candidate memories with provenance;
- allow explicit import through `memory_store` or `memory_organize` rather than
  silently importing everything;
- optionally export/checkpoint selected project memories to `MEMORY.md` through a
  future explicit operation that uses filesystem safety envelopes;
- avoid writing to memory files from context providers.

Open question: this repository already uses `AGENTS.md` for operative rules. The
memory plugin should not automatically merge `AGENTS.md` and `MEMORY.md`; the
coding plugin can continue to expose `AGENTS.md`, while memory plugin handles
remembered facts/preferences.

## Safety, privacy, and policy

Memory is sensitive by default because it can encode personal facts, credentials,
private project details, and model mistakes. Requirements:

- operation specs must advertise sensitivity and side effects accurately;
- store operations should reject or warn on obvious secrets unless the host
  policy explicitly permits storing sensitive memory;
- user-scope writes should be more constrained than session-scope writes;
- user-confirmed memories should be distinguishable from model-inferred memories;
- context providers must respect sensitivity and scope access;
- forgetting must be easy and auditable;
- cross-user, cross-channel, or cross-workspace retrieval must be opt-in and policy
  gated.

## Architecture placement

### `core/memory`

`core/memory` owns stable, inert memory contracts shared across implementations:

- scope selectors and scope kinds;
- typed refs for workspace and channel scope, plus references to canonical
  `core/user.ID` for user scope;
- memory IDs, kind/status constants, and the memory record/view shape;
- consent and provenance structs;
- filter/query/result structs;
- memory event payload types implementing `event.Event`.

These concepts are not plugin-private because operations, context providers,
apps, tests, future datasources, and runtime projectors need to agree on the same
durable event and materialized shapes. Core must not contain event-store replay,
filesystem access, ranking implementations, operation handlers, or context block
rendering.

### `runtime/memory`

`runtime/memory` owns the concrete event-sourced memory implementation over
`event.Store`:

- scoped stream naming and replay strategy;
- append helpers for store/forget/organize/context-selected events;
- materializer/projector logic that reconstructs the active memory view;
- tombstone, archive, supersession, and expiry semantics;
- a runtime memory service used by plugins and apps;
- memory-specific retrieval/index ports and the initial lexical/tag index;
- later semantic index integration, while keeping event replay authoritative.

The materializer belongs here because it is runtime state reconstruction from
core events, not optional plugin behavior.

### `plugins/memoryplugin`

`plugins/memoryplugin` is the optional first-party agent capability bundle:

- plugin manifest and resource contributions;
- operation specs and model-facing input/output structs;
- operation handlers for `memory_store`, `memory_retrieve`, `memory_forget`, and
  `memory_organize` that delegate durable behavior to `runtime/memory`;
- context provider specs and implementations such as `memory.catalog` and
  `memory.relevant`;
- optional `MEMORY.md` import/export wrappers using `runtime/system` workspace
  access.

The plugin must not own core event payloads, stream naming, materialization,
low-level scoped replay, or generic indexing implementation.

### Plugin-local `MEMORY.md` helpers

`MEMORY.md` integration is an IO boundary. The first implementation should keep
file import/export helpers inside `plugins/memoryplugin` and route all workspace
access through `runtime/system.Workspace`. A dedicated `adapters/memoryfile`
package remains backlog work if memory-file behavior becomes shared by apps,
CLIs, or other plugins.

### `orchestration` and `apps/*`
`orchestration` may need a typed plugin environment or service registry so
plugins can receive runtime dependencies such as `runtime/system.System`,
`event.Store`, identity resolvers, or memory services without every app inventing
ad hoc constructor patterns. This should be bounded and typed rather than a bag
of arbitrary values.

Apps and distributions explicitly assemble memory support: instantiate
`runtime/memory` with the chosen `event.Store`, configure enabled scopes and
consent policy, construct `plugins/memoryplugin`, and add memory operations and
context providers to agent allowlists.

The split follows the repo's layer rule: pure specs, events, refs, descriptors,
policies, and registries belong in `core`; concrete execution and storage live
in `runtime`; optional capability bundles and model-facing UX live in `plugins`;
filesystem/protocol IO belongs behind system/adapters; product defaults live in
`apps`.

## Decided design choices

1. Memory gets a `core/memory` package for durable domain contracts and event
   payloads.
2. The event-sourced service, scoped replay, materializer/projector, and initial
   lexical/tag retrieval index live in `runtime/memory`.
3. `plugins/memoryplugin` is thin: operations and context providers call
   `runtime/memory` instead of replaying events directly.
4. Workspace/user/channel identity is represented explicitly in the memory
   payload: user scope uses canonical `core/user.ID`, provider identities remain
   provenance/resolver inputs, and workspace scope represents the current root or
   worktree boundary rather than `core/project.Project`.
5. Context providers need bounded relevance input from `core/context.Request`:
   plain text query plus structured signals such as file paths, resource refs,
   topics, and intent.
6. Plugin runtime dependencies may need a typed plugin environment/service
   registry, similar in spirit to `runtime/system.System`, so plugins can receive
   context and services without ad hoc wiring. It must remain typed and bounded.
7. User-scope inferred memories require explicit consent by default, and consent
   state belongs in the memory schema.
8. `memory.context_selected` is a compact audit event from the start. It records
   selected memory IDs and ranking metadata, not full duplicated memory content.
9. `MEMORY.md` is an integration surface, not the authoritative store.
10. `adapters/memoryfile` is backlog work; v1 `MEMORY.md` support stays
   plugin-local behind `runtime/system.Workspace`.

## Remaining open design questions

1. What exact workspace ref fields from `core/workspace` should memory copy into
   events as provenance, beyond storing the canonical `workspace.ID` in scope?
2. What is the exact bounded relevance shape added to `core/context.Request`?
   It should support both plain text query and structured signals such as file
   paths, resource refs, topics, and intent.
3. Should `runtime/memory` expose only a concrete service, or should
   `core/memory` define a small service port for tools/context providers that
   need to depend on memory behavior without depending on the runtime package?
4. Should plugin runtime dependencies be supplied through explicit constructors,
   a typed plugin environment/service registry, or both? If a service registry is
   added, what are its typing and lifecycle rules so it does not become an
   unbounded service locator?
5. What is the default consent policy for workspace-scope inferred memories,
   especially instructions and preferences that may affect future agent behavior?

## Suggested rollout

1. Implement the workspace model from
   [2026-05-16-workspace-model.md](2026-05-16-workspace-model.md).
2. Define `core/memory` contracts, workspace-scope references to
   `core/workspace.ID`, user-scope references to `core/user.ID`,
   consent/provenance structs, query shapes, and event payloads.
3. Add `runtime/memory` event-store-backed service, scoped replay strategy,
   materializer/projector, and lexical/tag retrieval tests.
4. Extend `core/context.Request` with bounded relevance input for smart context
   providers.
5. Add `plugins/memoryplugin` operation specs/handlers backed by
   `runtime/memory`.
6. Add `memory.catalog` and `memory.relevant` providers with scoped lexical/tag
   retrieval and compact `memory.context_selected` audit events.
7. Wire the plugin into one app distribution behind explicit operation and
   context-provider allowlists.
8. Add `MEMORY.md` read/import support for workspace memory.
9. Add semantic ranking and richer organization helpers after the event model and
   runtime service are stable.
5. Add `memory.catalog` and `memory.relevant` providers with scoped lexical/tag
   retrieval and compact `memory.context_selected` audit events.
6. Wire the plugin into one app distribution behind explicit operation and
   context-provider allowlists.
7. Add `MEMORY.md` read/import support for workspace memory.
8. Add semantic ranking and richer organization helpers after the event model and
   runtime service are stable.

## Acceptance criteria for first implementation

- Memories can be stored, retrieved, forgotten, and organized through operations.
- Replaying memory events for a scope reconstructs the same active memory view.
- Session and workspace scopes work without user identity; user and channel scopes
  fail gracefully when identity is unavailable.
- Context provider output is bounded, cites memory IDs, and respects scopes.
- `forget` tombstones memories in the materialized view.
- Tests cover event replay, scope isolation, operation validation, and context
  provider ranking/budget behavior.
