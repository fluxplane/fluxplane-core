# Migration From Current `agentsdk`

This document tracks how concepts and packages from the current `agentsdk`
repository should move into `github.com/fluxplane/agentruntime`.

The rewrite is not a compatibility-preserving refactor. Prefer deleting stale
paths and splitting mixed packages over carrying adapters for old package names.

## Target Layers

```text
core/
  Pure model, specs, descriptors, event payloads, IDs, validation, small ports.

runtime/
  Concrete execution/storage/projection implementations of core contracts.

orchestration/
  Session/app/workflow/trigger use cases that compose core + runtime.

adapters/
  IO, protocol, filesystem, persistence backend, terminal, HTTP, Slack, shell.

plugins/
  First-party optional contribution bundles.

apps/
  Assembled products and dogfood apps.
```

Dependency direction:

```text
apps -> plugins/adapters -> orchestration -> runtime -> core
```

`plugins` may depend on `core`, `runtime`, `orchestration`, and selected
`adapters` only when the plugin's purpose requires it. Plugin contracts should
not live in plugin implementation packages.

`internal/architecture` and `apps/archreport` make this dependency direction
observable. Use the test for hard violations and the report for recurring
refactoring review:

```bash
go test ./internal/architecture
go run ./apps/archreport
go run ./apps/archreport -format json
go run ./apps/archreport -format dot
```

## Naming

The selected module path is:

```text
github.com/fluxplane/agentruntime
```

Fluxplane is the umbrella brand. Agent Runtime is the first product/repository.

## Decision Log

### Architecture Fitness Function

`internal/architecture` and `apps/archreport` make package dependency direction
observable. The architecture test is the hard check for boundary violations;
the report is a recurring review artifact for coupling, fan-in/fan-out, and
refactoring decisions.

The score is intentionally directional, not absolute truth. Boundary
violations carry the largest penalty. Runtime sibling imports and high fan-out
inside `core`, `runtime`, and `orchestration` reduce the score because they
usually indicate concepts that want splitting or a clearer composition point.
High fan-in to core and app/facade fan-out are reported but not treated as
problems by themselves.

### Channel, Session, and Harness Boundary

The user-facing client contract is handle-oriented:

```text
ChannelClient.Open/Resume/ListSessions
  -> SessionHandle
  -> Submit/SendInput/SendCommand
  -> RunHandle
  -> Events/Done/Wait/Result
```

Once a channel client has opened or resumed a session, execution uses the
resolved session/thread identity instead of re-resolving from channel and
conversation. Channel/conversation metadata may still be supplied by inbound
envelopes for routing and outbound production, but the thread selected by the
session handle is authoritative.

Direct in-process channels and remote HTTP/SSE channels must expose the same
logical handles, normalized caller/trust, semantic events, results, and failure
identity.

`orchestration/harness` is the channel-to-session use-case boundary. Adapters
call harness; they should not reach into sessions directly except in tests for
session behavior itself.

### Sub-Agents, Workers, and Plan Execution

The old implementation has two useful concepts that should remain separate in
the rewrite:

- a generic child-session supervisor for spawning, limiting, tracking,
  cancelling, and reporting progress from sub-agent sessions;
- a plan executor that uses that supervisor to dispatch DAG steps to workers.

Do not bake plan execution into the session manifest or daemon host. The
manifest should instead model reusable session profiles and delegation policy
so plan execution, ad-hoc delegation, and future supervisors can all open child
sessions through the same channel/session/harness path.

Future placement:

```text
core/agent or core/session
  sub-agent role/profile refs, delegation policy specs, worker limits.

orchestration/session or orchestration/worker
  child session supervisor, lifecycle, capacity, cancellation, progress events.

core/workflow or core/plan
  plan/DAG specs and step state events if they become driver-independent.

runtime/workflow or orchestration/workflow
  plan execution state machine and step scheduler.

plugins/planexec
  first-party contribution that exposes plan/delegate capabilities to agents.
```

Current design constraint: a configured session must be able to declare
delegation policy without naming a concrete plan executor. For example, a
future session manifest should be able to say "this parent agent may spawn
workers from these profiles, up to this concurrency, with these tool/context
limits" independently of whether the trigger is `/agent spawn`, an LLM tool
projection, or a plan DAG.

This means `sessions` in manifests are not just chat presets; they are named
entry points that can also be used as parent profiles for supervised child
sessions. Child sessions should have their own durable thread/session identity,
causation link to the parent run/step, policy boundary, context scope, and event
stream linkage.

### Configured Sessions

Configured sessions are named app entry points, not live sessions. They live in
`core/session.Spec`, are contributed through `core/resource.ContributionBundle`,
loaded by resource adapters, indexed by `orchestration/app`, and applied by
`orchestration/harness` when a channel client opens a session by profile ref.

Opening a configured session resolves the profile through the resource resolver
and canonicalizes the returned session ref to the profile's `ResourceID`
address. This prevents aliases such as `coder` and `demo:coder` from creating
separate bindings for the same configured profile.

The current session spec supports default channel/conversation/metadata plus a
reserved `DelegationPolicy`. Delegation is intentionally inert for now: it
declares future child-session limits and narrowing boundaries without creating
workers, plan execution, or implicit agent spawning.

### HTTP Surface Split

There are two HTTP surfaces:

- channel HTTP/SSE exposes the session channel contract remotely. It is a
  transport for `ChannelClient`, `SessionHandle`, `RunHandle`, submissions, and
  semantic event streams.
- daemon/control HTTP manages a running process: status, configured sessions,
  trigger state, logs, health, and administrative lifecycle.

The channel surface must not grow daemon control semantics, and the daemon
control surface must not become a side door into sessions that bypasses channel
trust, submission, and harness rules.

### Resource Boundary and Identity

The old `resource`, `agentdir`, `appconfig`, and resource loader code mixes
three concerns that are split in the rewrite:

```text
core/resource
  ContributionBundle, ResourceID, diagnostics, source refs, resource specs.

adapters/resourcefs, adapters/agentdir, adapters/appconfig
  Filesystem and external-format loading/parsing.

orchestration/app
  Converts loaded resource contributions into an executable app/session
  composition.
```

`core/resource` is pure metadata. It must not read files, inspect directories,
resolve global roots, execute commands, or instantiate plugins.

Composition builds a `core/resource` index and resolver. Local contribution
names are allowed to collide when their canonical `ResourceID`s differ; only
duplicate canonical IDs fail. Command targets are resolved in the source scope
of the command first, then through the app resolver. Ambiguous unqualified refs
are resolver/policy concerns, not flat-registry errors.

Operation specs in resources are declarations. Command execution must resolve
to an executable operation binding in the operation catalog; declaration-only
operation specs are discoverable but do not satisfy executable command targets.

### Plugin Boundary

The plugin model should have one contribution contract instead of many ad hoc
systems:

```go
type Plugin interface {
    Manifest() Manifest
    Contributions(context.Context, PluginContext) (Contribution, error)
}
```

The exact Go API can change, but the architectural rule is stable:

- plugin contracts are inner-layer concepts;
- plugin implementations live under `plugins/`;
- plugin selection and contribution assembly live in orchestration or apps;
- protocol/channel behavior lives in adapters.

Plugin ref `config` is plugin-owned data. Resource adapters may preserve it,
and pluginhost may pass it through, but only the selected plugin implementation
should interpret it. A plugin should reject unsupported or unsafe config early
instead of silently broadening its contribution set.

### Semantics, Policy, and Trust

Keep these concepts separate:

```text
operation.Semantics
  Intrinsic claims about what a capability can do: effects, risk,
  idempotency, determinism.

policy.InvocationPolicy
  Projection exposure rules: allowed caller kinds, required trust,
  required scopes, approval requirement.

policy.Caller / policy.Trust
  Who initiated an invocation and what authority/confidence the channel or
  session assigned to that invocation, source, or target boundary. Trust.Kind
  distinguishes invocation, source, and target trust.

environment.Boundary
  The scoped world in which an effect is meaningful or allowed.
```

Do not put caller/trust/exposure rules on `operation.Spec`. The same operation
may be exposed through different tools, commands, channels, or agent
projections with different policies.

### Operation Safety

When operation implementations move beyond pure/in-memory examples, implement
them safety-first. Do not add shell, filesystem, network, browser, code
execution, or connector operations as plain function calls and retrofit safety
later. The first real operation runtime batch must include the enforcement
shape for sandboxing, ACL/scope checks, command-risk classification
(`codewandler/cmdrisk` or successor), secret handling/redaction, approval
requirements, audit events, and environment boundaries.

### Projections and Tools

`operation` is the executable primitive. A projection describes how a caller or
driver sees an invocation target.

Commands are core because they are channel-facing invocation specs that can be
declared by resources and consumed by terminal, HTTP, Slack, or other channel
adapters.

Do not add a generic `core/tool` package unless "tool" becomes a
driver-independent core concept. In this rewrite, LLM/model tools are expected
to be driver-facing projections of operations and should live near the LLM
agent driver or model adapter layer when that need is concrete.

Shared target vocabulary belongs in `core/invocation`.

### Event State and Thread Store

Durable state should be reconstructable from events plus configuration.

Preferred pattern:

```text
events -> projector -> snapshot/read model
events -> session replay -> runtime state
```

`core/event.Store` is the generic append-only event stream port. It knows about
stream IDs, stream-local sequences, typed event records, schema versions,
causation, correlation, sensitivity, and runtime scope. It must not know about
thread branches, node ancestry, app sessions, SQLite tables, JSONL files, or
subscription servers.

Event append implementations must support optimistic concurrency through
`event.AppendOptions`. When `CheckExpectedSequence` is set, append must only
succeed if the stream currently ends at `ExpectedSequence`; sequence `0` means
the stream must be empty. Failed checks must return an error that matches
`event.ErrAppendConflict`. `event.Store.AppendBatch` must apply all requested
stream appends atomically or none of them.

`core/thread.Store` is the thread-domain port. It knows about thread IDs,
branches, fork points, node IDs, metadata, archive state, and visible branch
history. A thread store implemented by replaying `event.Store` streams belongs
in `runtime/thread`, because it encodes storage/projection semantics even if it
only depends on core interfaces.

The runtime thread store uses one index stream plus per-thread history streams:

```text
thread.index
thread:<thread-id>
```

Memory event stores belong in `runtime/eventstore`, not in `core/event`.
Although they are IO-free, they are mutable runtime implementations: they
assign IDs, timestamps, schema defaults, and stream-local sequence numbers, and
they own concurrency behavior. Filesystem, SQL, and protocol-backed event
stores belong in adapters such as `adapters/sqleventstore` or
`adapters/eventjsonl`.

Shared event record normalization and typed JSON payload encoding belong in
`runtime/eventcodec`. Core owns the event model and registry contract, but
runtime owns defaults and serialization behavior.

Projection freshness policy belongs in orchestration. `orchestration/projections`
coordinates runtime projection runners for use cases, such as ensuring a thread
index has caught up before serving an index-backed list.

Event payloads are not access-controlled by themselves. Event records carry
classification metadata such as `policy.Sensitivity`; missing sensitivity must
be treated as restricted. Event streams, subscriptions, and channel adapters
must enforce observation access using caller trust/scopes, record sensitivity,
and stream-specific policy once those surfaces exist.

## Concept Mapping

This table captures the intended destination for major current concepts before
we audit every package in detail.

| Current concept | New location | Notes |
| --- | --- | --- |
| Agent spec/config | `core/agent` | Pure spec, inference options, model policy, stop config. |
| Running agent instance | `runtime/agent` + `orchestration/session` | Split turn engine/runtime state from session lifecycle. |
| Model/tool turn loop | `runtime/agent` or `runtime/model` | Keep provider transport outside if it needs IO adapters. |
| Actions | `core/operation` + `runtime/operation` | Consider renaming `Action` to `Operation` as the single executable primitive. |
| Tools | later near `core/agent/llmagent` or model adapter layer | Do not recreate old executable tool concept. LLM tools are driver-facing projections of operations. |
| Commands | `core/command` + `orchestration/session` command dispatch | Channel-facing invocation specs. Slash parsing/rendering belongs in adapters. |
| Workflows | `core/workflow` + `orchestration/workflow` or `runtime/workflow` | Specs/events/projectors in core; executor placement depends on whether it composes runtime implementations. |
| Thread/event state | `core/event` + `core/thread` + `runtime/thread` or `adapters/event*` | Generic event stream port in core; thread-domain port in core; event-backed thread projection in runtime. |
| Projections/read models | `core/projection` + `runtime/projection` + runtime read models | Checkpoints/projector contracts in core; runner/checkpoint stores in runtime; concrete read models near their concept. |
| Context providers | `core/context` + `runtime/context` | Structured context blocks in core; provider execution/materialization in runtime. |
| Capabilities | `core/capability` + `runtime/capability` | Capability specs/events in core; state machine implementations in runtime/plugins. |
| Skills | `core/skill` + `plugins/skills` + resource adapters | Metadata/activation in core; discovery/loading in adapters. |
| Resources | `core/resource` + `adapters/resourcefs` + `orchestration/app` | Contribution data is core; loading/parsing is adapter; executable composition is orchestration. |
| Plugins | `core/plugin` or `orchestration/pluginhost` + `plugins/*` | Keep contracts separate from implementations. |
| Datasources | `core/datasource` + `plugins/datasources` + connector adapters | Search contract in core; connector/runtime details outward. |
| Channels | `core/channel` + `orchestration/client` + `adapters/terminal/http/slack` | Core has normalized envelopes/specs; client defines handle API; adapters implement transports. |
| Invocation policy | `core/policy` | Caller, principal, trust kind/level, scopes, and projection invocation policy. |
| Safety | `core/safety` + `runtime/operation` gates + adapter approval UX | Operation semantics remain intrinsic; projection policy and channel trust use `core/policy`; enforcement happens at execution boundary. |
| Usage | `core/usage` + `runtime/usage` + orchestration events | Records/types in core; aggregation/persistence outward. |
| App composition | `orchestration/app` | Current `app.App` should be split from resource loading and plugin implementations. |
| Harness/session/client | `orchestration/client` + `orchestration/harness` + `orchestration/session` | Client defines ChannelClient/SessionHandle/RunHandle and Submission; harness binds channels/conversations; session owns execution for one bound thread. |
| Sub-agents/workers | `core/session`/`core/agent` specs + `orchestration/worker` or `orchestration/session` supervisor | Preserve the old generic worker manager concept: child sessions with capacity, cancellation, progress, and parent causation. Plan execution should consume this rather than own it. |
| Daemon/triggers | `orchestration/trigger` + `adapters/httpapi`/apps | Scheduling use case in orchestration; process hosting outward. |
| Terminal CLI | `adapters/terminal` + `apps/cli` | CLI UX/rendering is adapter/app, not core/runtime. |
| HTTP/SSE API | `adapters/httpapi` | Protocol translation over session APIs. |
| Slack | `adapters/slack` | Protocol translation over session APIs. |
| First-party apps | `apps/*` | Builder, engineer, run app. |

## Initial Package Disposition

This is a first-pass map. Each row still needs a detailed audit before code is
ported.

| Current package/directory | Proposed destination | Audit notes |
| --- | --- | --- |
| `action` | `core/operation` | Pure operation spec/result/type/ref mostly belongs in core. |
| `action/declarative` | `runtime/operation/declarative` or `adapters/resourcefs` | Split declarative metadata from executable aliases/shell bindings. |
| `actionmw` | `runtime/operation/middleware` | Middleware implementation; safety types go to `core/safety`. |
| `agent` | split | Specs to `core/agent`; runtime construction to `runtime/agent`; session lifecycle to `orchestration/session`. |
| `agentconfig` | `core/agent` | Pure config candidate. |
| `agentcontext` | split into `core/context` and `runtime/context` | Descriptors/types core; manager/provider execution runtime. |
| `agentcontext/contextproviders` | `runtime/context/providers` + adapters | Static/time pure-ish; file/cmd/git/environment need adapter/runtime split. |
| `agentdir` | `adapters/agentdir` | Filesystem/external format loader. |
| `agentstate` | `runtime/agent/state` or `core/thread` events | Audit whether this is projected state or event definitions. |
| `agentturn` | `core/turn` + `orchestration/session` | Specs/events core; resolver/session integration outward. |
| `api` | split into `adapters/httpcontrol` and `adapters/httpchannel` clients | Current daemon control API and direct-channel API are separate HTTP surfaces and should stay conceptually separate. |
| `app` | `orchestration/app` + `core/plugin` | Split pure app spec/plugin contract from live composition. |
| `appconfig` | `adapters/appconfig` + `core/resource` | Parsing/loading adapter; schema/spec data maybe core. |
| `apps` | `apps` | Preserve as assembled products, but import new layers. |
| `capability` | `core/capability` + `runtime/capability` | Registry/descriptors/specs likely core; manager/runtime outward. |
| `capabilities/memory` | `plugins/memory` | Capability implementation as plugin. |
| `capabilities/planexec` | `plugins/planexec` | Capability implementation as plugin. |
| `channel` | `core/channel` + `orchestration/harness` | Core keeps normalized envelopes/specs; harness owns routing/session binding. |
| `channel/httpapi` | `adapters/httpchannel` | Server-side implementation of the direct HTTP/SSE channel. Keep separate from daemon control API. |
| `channels/slackchan` | `adapters/slack` | Protocol adapter. |
| `cmd/agentsdk` | `apps/cli` | Final product CLI. |
| `cmd/agentclient` | `apps/agentclient` + `adapters/httpchannel/client` | Product CLI over the direct channel client. It should not call session directly. |
| `cmd/agent-sim` | `apps/agentsim` or test tool | Probably app/tooling. |
| `command` | `core/command` + parser adapter | Descriptors/results/core tree in core; slash parsing may be adapter if terminal-specific. |
| `command/markdown` | `adapters/resourcefs/commands` | Markdown command format reader. |
| `conversation` | `core/conversation` + `runtime/conversation` | IDs/events/projection policy split from runtime history handling. |
| `daemon` | `apps/serve` + `adapters/httpcontrol` + channel adapters | Process host/control plane outward; user messages still enter through channel adapters and harness. |
| `datasource` | `core/datasource` + `runtime/datasource` | Source contract/core types; registry runtime. |
| `datasource/filesource` | `plugins/datasources/filesource` or `adapters/filesource` | File-backed implementation. |
| `eventstore` | `core/event` + `runtime/eventstore` + `adapters/sqleventstore` | `core/event.Store` keeps the append/load stream contract. In-memory store proves runtime; SQLite proves adapter boundary. |
| `expand` | audit | Likely resource/appconfig helper; place by actual IO/purity. |
| `harness` | `orchestration/harness` + `orchestration/session` | Split adapter-facing session binding from per-thread execution. |
| `harness/worker` | `orchestration/worker` or `orchestration/session/worker` | Generic child-session supervisor. Preserve capacity limits, cancellation, progress, parent/child causation, and prepare-then-start semantics so callers can persist dispatch state before work begins. |
| `markdown` | `adapters/markdown` or `core/markup` | Frontmatter parsing likely adapter/helper. |
| `plugins/*` | `plugins/*` | Keep first-party contribution bundles; update contracts. |
| `project` | `runtime/project` + adapters/filesystem | Entity model core-ish; detection scans filesystem. |
| `resource` | split | Contribution structs to `core/resource`; resolver/index pure maybe core; discovery/loading adapters. |
| `resource/loader` | `adapters/resourcefs` | Filesystem/appconfig/embedded discovery. |
| `runner` | `runtime/agent/runner` | Model/tool turn loop. |
| `runnertest` | test support | Place under relevant runtime test package. |
| `runtime` | `runtime/agent` | Current engine/history/toolctx likely runtime agent/conversation. |
| `safety` | `core/safety` + `runtime/operation/safety` | Decision/intents core; gates runtime. |
| `skill` | `core/skill` + `runtime/skill` | Metadata/refs core; repository/search/install may need adapter/runtime split. |
| `stop` | `core/agent/stop` + `runtime/agent/stop` | Config/spec core; runtime evaluation outward. |
| `terminal` | `adapters/terminal` + `apps/cli` | Rendering and CLI host. |
| `thread` | `core/thread` + `runtime/thread` | IDs, thread lifecycle event payloads, branch/node model, snapshot helpers, and `thread.Store` port in core. Live/buffered handles and event-store-backed implementation in runtime. |
| `thread/jsonlstore` | `adapters/eventjsonl` | Concrete persistence adapter. |
| `thread/eventstorebackend` | `runtime/thread` | Implemented as `runtime/thread.Store` by replaying `core/event.Store`; this is projection/storage semantics, not core. |
| `tool` | later LLM-driver projection package, likely near `core/agent/llmagent` | Avoid generic `core/tool` until "tool" is proven driver-independent. Executable behavior belongs to operations. |
| `toolactivation` | `runtime/tool/activation` | Runtime activation manager; activation state data may be core. |
| `toolmw` | `runtime/tool/middleware` | Compatibility bridge; may shrink if operations are primary. |
| `tools/*` | mostly `plugins/*` or `runtime/operation/*` | Shell/browser/filesystem/git/web/vision are concrete implementations/plugins. |
| `trigger` | `core/trigger` + `orchestration/trigger` | Trigger specs core; scheduler/use case orchestration. |
| `usage` | `core/usage` + `runtime/usage` | Records core; tracker/aggregation runtime. |
| `user` | `core/user` + `orchestration/session/user` | Identity structs core; resolver/session state outward. |
| `vcs` | `plugins/vcs` or `plugins/git` | Backend integrations. |
| `websearch` | `core/search` or `plugins/websearch` | Provider contract core if generic; concrete providers plugin/adapter. |
| `workflow` | `core/workflow` + `orchestration/workflow` | Specs/events/projector core; executor may be orchestration if resolving operations. |

## Open Design Questions

1. Should JSONL event persistence be `adapters/eventjsonl` or
   `runtime/eventstore/jsonl`? Current rule: memory stores are runtime;
   filesystem/database formats are adapters unless they are strictly test-only.
2. Is the workflow executor a runtime implementation or an orchestration use
   case? The answer depends on whether it only executes operations or also owns
   session/run lifecycle.
3. Should `event.Store` grow a query/index port, or should non-stream lookup be
   modeled as separate projections/read models?
4. Should plugin contracts live in `core/plugin` or `orchestration/pluginhost`?
5. How much of resource resolution is pure enough for `core/resource` versus
   orchestration/app?
6. Is model provider transport part of `runtime/model` or `adapters/model/*`?
7. Which current channel concepts are pure identity/trust models versus adapter
   behavior?

## Rewrite Backlog

These are known follow-ups from the early core/orchestration design pass. Keep
them visible until they are resolved in code or deliberately rejected.

| Item | Notes |
| --- | --- |
| Event record serialization | Shared helpers now live in `runtime/eventcodec`; future durable/wire adapters should reuse this package. |
| Event store query surface | Current core event store is stream append/load only. Old `eventstore.Query` supported kind/scope/correlation filters; decide whether those are store capabilities or separate read-model indexes. |
| Event store concurrency contract | Initial `event.AppendOptions` and atomic `AppendBatch` APIs exist. `runtime/thread` now scopes write conflicts to per-thread streams while keeping `thread.index` synchronized with batch appends. |
| Runtime thread event-store backend | Initial implementation exists in `runtime/thread` using `thread.index` plus `thread:<id>` streams; continue hardening compaction and large-store read models. |
| Projection runner | Initial `core/projection`, `runtime/projection`, and `orchestration/projections` exist. `runtime/thread.Store` can use `ThreadIndex` for `List` with replay fallback; orchestration can ensure freshness before use. |
| Harness/channel executable path | Initial `orchestration/harness` and `adapters/directchannel` exist. Next HTTP/SSE work should implement the old `cmd/agentclient` shape against harness without bypassing channel semantics. |
| Submission/run handle API | Initial `orchestration/client` exists with `Submission`, `ChannelClient`, `SessionHandle`, and `RunHandle`. `adapters/directchannel` implements command and input submissions; event/signal submissions are modeled but not executed yet. |
| Session event replay | `orchestration/harness.Subscribe` can replay semantic `client.Event` values from the thread store using `client.EventCursor` sequence positions. HTTP/SSE should expose this cursor instead of inventing transport-specific offsets. |
| Configured sessions | Initial `core/session.Spec`, resource manifest loading, app catalog indexing, harness profile application, daemon listing, and devclient `-session` support exist. Delegation policy is declared but not executed. |
| Command path display vs parsing | `command.Path.String()` is display-only. Actual slash parsing belongs in adapters, not `core/command`. |
| Trust level semantics | `policy.TrustSystem` must not become unlimited authority by default; scopes should still matter for system callers. |

## Current Rewrite Status

The current rewrite proves the inner-to-outer path for the first executable
slice:

```text
core operation/command/channel/session/thread/event model
  -> runtime operation/event/thread implementations
  -> orchestration session + harness + client handles
  -> direct channel and HTTP/SSE channel adapters
  -> devclient app
```

Implemented and green:

- `core/operation`: operation specs, semantics, results, events, and registry.
- `core/app`: pure app manifest specs for default refs, sources, discovery
  policy, model policy, and plugin refs.
- `core/agent`: inert agent specs now carry engineer-style system prompts,
  tool refs, command refs, skill refs, inference hints, max-step policy, and
  stop-condition declarations.
- `core/skill`: pure skill metadata specs and source refs.
- `core/event`: event records, stream store port, registry, append conflict and
  batch append contracts.
- `core/session`: configured session specs, session lifecycle event payloads,
  and reserved delegation policy shape for future sub-agents.
- `core/workflow`: operation and agent-dispatched workflow DAG specs with
  dependency validation and raw definition preservation.
- `runtime/eventstore`: in-memory append-only event store.
- `adapters/sqleventstore`: SQLite-backed event store adapter.
- `core/thread` + `runtime/thread`: event-backed thread store, branch model,
  thread index, list replay fallback, and optional projected read model.
- `core/projection`, `runtime/projection`, `orchestration/projections`:
  checkpoint/projector contracts, runner, and orchestration freshness manager.
- `core/command`, `core/invocation`, `core/policy`: channel-facing command
  specs, invocation target vocabulary, caller/trust/policy evaluation.
- `core/channel`: normalized channel specs and inbound/outbound envelopes.
- `orchestration/session`: command and conversational input execution for one
  bound thread.
- `orchestration/harness`: channel conversation binding, explicit
  session-bound execution, semantic event fanout, and replay from thread store.
- `orchestration/client`: transport-neutral `ChannelClient`, `SessionHandle`,
  `RunHandle`, `Submission`, `Result`, and semantic event contracts.
- `adapters/directchannel`: in-process channel client over harness.
- `adapters/httpssechannel`: remote JSON + SSE channel adapter preserving the
  same client/session/run API.
- `orchestration/app`: resource/app composition over supplied runtime
  implementations, backed by `core/resource` identities, indexes, resolver,
  command catalog, operation catalog, and session catalog. Duplicate short
  names may coexist when canonical resource IDs differ.
- `orchestration/pluginhost`: minimal plugin contribution contract and plugin
  ref resolution into resource bundles plus optional executable operation
  implementations. Plugin bundles now receive plugin source refs when the
  implementation does not provide one.
- `plugins/echoplugin`: first migrated low-IO plugin, contributing a pure echo
  operation and command projection.
- `plugins/textplugin`: second migrated low-IO plugin, contributing
  configurable pure text transformation operations and commands.
- `adapters/resourcefs`: first local filesystem manifest loader
  (`agentruntime.json`) into pure resource contribution bundles, including
  configured session profiles.
- `adapters/appconfig`: narrow `agentsdk.app.json`/YAML manifest parser for
  app specs, source declarations, discovery policy, model policy, and plugin
  refs.
- `adapters/agentdir`: narrow `.agents` parser for the engineer subset:
  agent markdown, prompt command markdown, workflow command YAML, workflow YAML,
  and skill metadata.
- `runtime/operation`: pre-execution safety gate/envelope shape for sandbox,
  ACL, command-risk, secrets, and approval enforcement.
- `orchestration/daemon` + `adapters/httpcontrol`: separate process/control
  status, configured-session listing, and session-listing surface over an
  existing channel client.
- root `agentruntime.Service`: public in-process facade for library consumers.
- `apps/devclient`: small dogfood client for local and HTTP/SSE execution,
  including debug event/result output, `-app` resource manifest loading, and
  `-session` configured profile selection.

Important contract decisions from this slice:

- Once opened or resumed, a session handle's thread is authoritative.
  Submissions must execute against that thread instead of re-resolving from
  channel/conversation.
- Direct and remote channel clients must expose the same logical handles,
  events, results, caller/trust normalization, and failure identity.
- `submission.received` events should carry the normalized submission payload,
  caller, and trust context for both live delivery and replay.
- Live event subscriptions are best-effort fanout; durable replay belongs to
  the event/thread stores.
- HTTP/SSE channel transport is not the daemon/control API. It is only a remote
  implementation of the channel client contract.
- Resource operation specs are declarations. Executable operation
  implementations come from runtime/plugin/app code and are matched during app
  composition.
- App composition treats local names as local and `ResourceID`s as global.
  Duplicate short names may coexist across origins/namespaces; duplicate
  canonical IDs fail. Command targets resolve in the command source scope first
  and then through the app resolver.
- Resource operation specs are discoverable declarations. Executable command
  targets must resolve to an operation catalog binding, not merely to a
  declaration-only spec.
- Sub-agent support is intentionally not implemented yet, but configured
  sessions must leave room for delegation policy, parent/child causation, and
  child session identity.
- Configured session refs are resolved through resource identity and then
  canonicalized to the resolved `ResourceID` address before harness binding.
- Real side-effecting operations must enter through the operation safety
  envelope from their first migration batch.

Still intentionally incomplete:

- `SubmissionEvent` and `SubmissionSignal` are modeled but not executed.
- Only low-IO first-party plugins have been migrated. Shell, browser,
  filesystem, git, and connector plugins are intentionally blocked on concrete
  safety adapters.
- The plugin contract is intentionally minimal and only contributes resource
  bundles plus operation implementations during app composition.
- The daemon/control surface is minimal: status, configured-session listing,
  and live session listing only.
- The engineer app parity plan has Phase 1A pure model support and Phase 1B
  narrow adapters, but orchestration/app does not yet index or validate those
  new app, agent, skill, and workflow contribution types.
- There is no LLM-agent runtime yet; `core/agent/llmagent` only contains pure
  spec shape.
- There is no terminal/slash parser, Slack adapter, model provider adapter, or
  approval UX yet.
- There is no child-session supervisor or plan executor yet. The old
  `harness/worker` and `capabilities/planexec` concepts are scheduled; the
  current session profile model only reserves the delegation shape.
- Event subscription authorization is noted but not implemented beyond the
  current policy/trust/sensitivity model foundations.

## Next Migration-Completeness Milestone

The next bigger batch should extend the new resource/app/daemon spine without
pulling old architecture across wholesale. The milestone is:

```text
plugin contributions + signal/trigger execution + context providers
  -> configured sessions
  -> channel clients
  -> executable submissions and event streams
```

Definition of done for that batch:

- A library consumer can still do:

  ```go
  svc, err := agentruntime.New(...)
  s, err := svc.Open(ctx, agentruntime.OpenRequest{...})
  run, err := s.SendInput(ctx, agentruntime.Input{Text: "hello"})
  result, err := run.Wait(ctx)
  ```

- A resource/app consumer can load an app directory, compose commands,
  operations, agents, context providers, and plugins, then open a session
  through the same `ChannelClient` API.
- Configured sessions can express future delegation boundaries without
  enabling them yet: allowed sub-agent profiles, concurrency limit, timeout,
  context/command narrowing, and parent/child causation policy.
- A daemon can start configured sessions from app resources, attach event
  triggers, expose a control API for process/admin state, and expose a channel
  API for user/client submissions without mixing those two HTTP surfaces.
- Existing `cmd/agentclient` behavior can be represented as a CLI using a
  channel client; it must not call harness/session internals directly.
- At least one real migrated resource format is supported end to end, not just
  hand-built Go config.
- Real operation implementations are introduced safety-first. Any shell,
  filesystem, network, browser, code execution, or connector operation work
  must include sandboxing shape, ACL/scope checks, command-risk classification
  (`codewandler/cmdrisk` or successor), secret handling/redaction, approval
  requirements, audit events, and environment-boundary enforcement from the
  start.

Recommended order:

1. **Plugin Contribution Hardening**
   The second low-risk plugin now proves plugin-owned config and multiple
   contribution ordering. Resource identity, indexing, scoped resolution, and
   catalogs now exist for app composition. Next, add richer diagnostic
   aggregation and explicit alias/precedence configuration when a concrete app
   needs them.

2. **Resource Format Expansion**
   Extend `adapters/resourcefs` only where app composition needs it next:
   plugin refs, agent specs, context provider specs, and workflows. Keep
   `.agents`, `agentdir`, and appconfig compatibility as adapter packages, not
   core concepts.

3. **Configured Session Host**
   Initial configured session loading, cataloging, harness defaults,
   daemon listing, and devclient selection exist. Next, decide whether daemon
   startup should eagerly open configured sessions from app resources or only
   open them on trigger/client demand. Keep user/client traffic on
   `ChannelClient` either way.

4. **Sub-Agent Supervisor Shape**
   Add pure delegation policy/spec types only if needed by configured sessions.
   Do not implement worker execution in this batch. The future runtime should
   preserve the old worker manager's useful properties: capacity limits,
   cancellation, progress callbacks/events, prepare-then-start dispatch, and
   parent/child causation.

5. **Operation Safety Runtime**
   Harden the initial safety envelope with concrete adapters before migrating
   shell/filesystem/git/browser/web operations: sandbox execution boundary,
   ACL/scope evaluation, `codewandler/cmdrisk` integration or successor,
   secret handling/redaction, approval hooks, audit events, and environment
   boundary checks.

6. **CLI Client Migration**
   Rebuild the old `cmd/agentclient` shape as an app over channel clients:
   direct for in-process dev mode, HTTP/SSE for daemon mode. Keep `devclient`
   as the small diagnostic client until the real CLI is good enough.

7. **Trigger and Signal Execution**
   Implement `SubmissionSignal` as the entry point for timers, file watchers,
   and daemon-triggered sessions. File watcher/timer IO belongs in adapters or
   apps; signal dispatch semantics belong in orchestration.

8. **Context Provider Runtime**
   Add the first narrow `core/context` provider contracts and
   `runtime/context` materializer, then migrate only providers needed by the
   first app. File/git/shell-backed providers should remain adapter/plugin
   owned.

Do not start with LLM provider integration or Slack. They are important, but
they depend on the app/plugin/channel boundaries being stable enough that they
do not drag old architecture back into the rewrite.

## Engineer App Parity Plan

The old `agentsdk dev` surface is the first meaningful parity target. Its
resource surface is:

```text
apps/engineer/resources/agentsdk.app.json
apps/engineer/resources/.agents/agents/main.md
apps/engineer/resources/.agents/agents/analyst.md
apps/engineer/resources/.agents/agents/implementer.md
apps/engineer/resources/.agents/commands/review.md
apps/engineer/resources/.agents/commands/design.md
apps/engineer/resources/.agents/commands/deploy.md
apps/engineer/resources/.agents/commands/feat.yaml
apps/engineer/resources/.agents/workflows/feature.yaml
apps/engineer/resources/.agents/skills/*/SKILL.md
```

Parity should be reached in small slices:

1. **Phase 1A: Pure Resource Model**
   Add only inert model shape: `core/app`, `core/skill`, stronger
   `core/agent`, stronger `core/workflow`, invocation targets for workflow and
   prompt commands, and matching `core/resource.ContributionBundle` fields. No
   filesystem parsing or execution.

2. **Phase 1B: Engineer Resource Adapters**
   Add narrow `adapters/appconfig` and `adapters/agentdir` support for the
   engineer subset. These adapters parse files into core contribution bundles;
   they do not instantiate plugins or execute workflows.

3. **Phase 1C: Composition Parity**
   Teach `orchestration/app` to index and validate app manifests, agents,
   skills, workflows, prompt commands, workflow commands, sessions, and plugin
   refs. Composition should create the default configured session for the
   default agent.

4. **Phase 2: LLM Agent Runtime Skeleton**
   Add `runtime/agent/llmagent` with a fake/test model path before migrating
   real provider transport.

5. **Phase 3: Tool Projection and Safety**
   Project operations/commands into model tools only behind the operation
   safety envelope.

6. **Phase 4: Local CLI Capability Plugin**
   Migrate shell/filesystem/git/search tools through adapters and plugins with
   sandboxing, ACL, command-risk classification, approval, audit, and secret
   handling from the start.

7. **Phase 5: Commands and Skills Runtime**
   Execute prompt commands, support skill discovery/activation, and preserve
   command visibility policies.

8. **Phase 6: Workflow and Sub-Agent Supervisor**
   Execute the `feature` workflow through child sessions using a generic
   supervisor with capacity, cancellation, progress, parent/child causation,
   and event linkage.

9. **Phase 7: Engineer App Assembly**
   Add the rewrite-native `apps/engineer` and user-facing `agentruntime dev`
   CLI over the same channel client/session/run handles.

Phase 1A definition of done:

- Pure app manifest specs can represent default agent/session, sources,
  discovery policy, model policy, plugin refs, and annotations.
- Agent specs can represent the engineer frontmatter fields without becoming
  executable: system prompt, tool refs, command refs, skill refs, inference
  hints, max steps, and stop condition.
- Skill specs can represent `SKILL.md` metadata and source refs without reading
  files.
- Workflow specs can represent `feature.yaml` as typed DAG steps with agent
  refs, dependency refs, and raw definition metadata.
- Invocation targets can represent operation, workflow, agent/session/message,
  and prompt targets.
- Resource bundles can carry app specs and skill specs.

## Package Audit Template

Use this template as we go through each current package.

```text
Current package:
Current responsibilities:
Pure concepts:
Runtime implementations:
Orchestration/use cases:
Adapters/IO:
Plugin/app ownership:
Target package(s):
Delete/replace:
Open questions:
```
