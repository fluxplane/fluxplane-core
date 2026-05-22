# Migration From Current `agentsdk`

This document tracks how concepts and packages from the current `agentsdk`
repository should move into `github.com/fluxplane/engine`.

The rewrite is not a compatibility-preserving refactor. Prefer deleting stale
paths and splitting mixed packages over carrying adapters for old package names.
Current product CLI work lands in the nested `github.com/fluxplane/coder`
module under `apps/coder`; the legacy `agentsdk` binary/package has been
removed. Authored app manifests use `fluxplane.yaml`.

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

Codegate makes this dependency direction observable. Use the quality gate for
hard violations and the review report for recurring refactoring review:

```bash
task quality:go
task quality:go:review
```

## Naming

The selected module path is:

```text
github.com/fluxplane/engine
```

Fluxplane is the umbrella brand. Agent Runtime is the first product/repository.

## Decision Log

### Architecture Fitness Function

Codegate and `engine-architecture.rules.json` make package dependency
direction observable. `task quality:go` is the hard check for boundary,
side-effect, and unknown-package violations; `task quality:go:review` is the
recurring review artifact for coupling, fan-in/fan-out, and refactoring
decisions.

The score is intentionally directional, not absolute truth. Boundary
violations carry the largest penalty. Runtime sibling imports and high fan-out
inside `core`, `runtime`, and `orchestration` reduce the score because they
usually indicate concepts that want splitting or a clearer composition point.
High fan-in to core and app/facade fan-out are reported but not treated as
problems by themselves.

Current rewrite-local architecture work keeps the report at 90 or higher with
zero violations. Recent extraction moved event registry assembly, resource
cataloging, executable app resource binding, agent spec filtering, session
environment wiring, and session control-plane helpers into focused
orchestration packages rather than leaving those concerns in the app or
session loops.

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

plugins/task
  first-party contribution that exposes task creation, planning, scheduling,
  and execution capabilities to agents.
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

Session memory has two tracks:

- semantic projections from `session.*` events, used for UI/debug/context
  summaries and generic agent observations;
- provider transcript events in `core/conversation`, used to preserve exactly
  what a model provider saw and returned.

On each conversational input, `orchestration/session` currently reads prior
`session.*` events from `core/thread.Store` and projects a compact
conversation-history context block into the next agent step. The current
inbound message remains an observation, so history does not duplicate the
active turn. This projection is useful but must not become the default
provider-visible LLM history.

Provider-visible history must be reconstructed from `core/conversation`
transcript items and continuation handles. The safe default projection is full
replay of exact provider items. Native continuation, such as OpenAI Responses
`previous_response_id`, is an optimization only when the branch head has a
matching provider/API/model continuation handle and the adapter can send only
the pending delta without changing the provider-visible prefix.

OpenAI-compatible stored-response continuation is automatic best effort, not a
provider-specific CLI flag. The Responses adapter uses max prompt-caching
defaults and records `conversation.continuation.stored` handles when the
provider returns a usable response ID. Subsequent turns can project
`ProjectionNativeContinuation`, send `previous_response_id`, and persist only
new turn items so thread history does not duplicate prior provider payloads.
Hosts can disable provider continuation through generic Responses runtime
config when replay-only behavior is required.

The old `conversation` package should continue migrating into this split:
message/conversation IDs and transcript events in `core/conversation`; exact
replay, native-continuation selection, compaction, summarization, and token
budget handling in runtime/orchestration.

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

adapters/agentdir, adapters/appconfig
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

### SDK Builder Layer

The public builder layer lives in `sdk`. It is an IO-free authoring convenience
over core specs, not a runtime or orchestration shortcut.

`sdk.NewApp`, `sdk.BuildAgent`, `sdk.BuildOperation`, and
`sdk.CommandForOperation` produce normal `core/resource.ContributionBundle`
and core specs. They may reduce boilerplate for embedded apps and library
consumers, but they must not instantiate providers, execute operations, inspect
files, open sessions, or bypass the resource/composition/session path.

This keeps hand-authored specs, manifest-loaded specs, and fluent Go-authored
specs equivalent after normalization. Runtime implementations remain supplied
through composition config, plugins, adapters, or host code.

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
execution operations as plain function calls and retrofit safety later. The
first real operation runtime batch must include the enforcement shape for
sandboxing, ACL/scope checks, command-risk classification
(`codewandler/cmdrisk` or successor), secret handling/redaction, approval
requirements, audit events, and environment boundaries.

Current security model:

- `runtime/operation.SafetyEnvelope` is the mandatory pre-execution boundary
  for side-effecting operations. It composes ACL/scope checks, secret guards,
  command-risk classification, approval policy, and sandbox checks without
  importing concrete safety engines.
- `adapters/cmdrisk` is the first concrete command-risk adapter. It wraps
  `github.com/codewandler/cmdrisk`, evaluates shell commands via command
  parsing, evaluates structured network operations via intent assessment, and
  emits `cmdrisk.assessed` events for debug/audit streams.
- Shell execution in coder runs an executable directly, never
  through a shell interpreter. It rejects shell syntax, rejects a small set of
  explicitly dangerous executables as defense in depth, binds workdir to the
  workspace root, uses a minimal environment, separates stdout/stderr, caps
  output, and reports timeout/truncation explicitly.
- HTTP fetch in coder is GET-only, bounded by timeout and body
  size, blocks loopback/private/link-local/multicast/metadata targets, resolves
  DNS inside a custom dialer to reduce DNS rebinding risk, and revalidates
  redirects.
- This is not a full sandbox yet. The next security phase should add real
  process isolation profiles, filesystem read/write scopes, network egress
  policy, approval UX, secret scanning/redaction, and resource usage accounting
  as reusable runtime/adapters rather than app-local helpers.

### Projections and Tools

`operation` is the executable primitive. A projection describes how a caller or
driver sees an invocation target.

Commands are core because they are channel-facing invocation specs that can be
declared by resources and consumed by terminal, HTTP, Slack, or other channel
adapters.

Model-visible tools are now a driver-independent projection concept:
`core/tool` describes the provider-neutral descriptor shape after orchestration
has applied resource identity, command policy, caller/trust, and operation
semantics. Tools are not executable implementations and do not bypass
command/session policy or operation runtime safety.

Tool projection must be safe by default:

- operation commands must point at an executable operation binding;
- read-only, low-risk operations may be projected;
- side-effecting operations are hidden unless explicitly allowed by the
  projection policy and still remain subject to runtime safety enforcement;
- command `InvocationPolicy` affects whether a tool is visible to an agent;
- approval-required projections are hidden unless projection opts in.

LLM streaming is a runtime concern, not a different agent result shape. The
agent's final result remains a structured decision: message, completion, wait,
or one-or-more operation requests. Streaming content, tool-call deltas, and
thinking deltas are provider-neutral transient events emitted by
`runtime/agent/llmagent`. Raw thinking is opt-in and should not be persisted by
default; provider-hidden reasoning should be summarized or omitted.

Provider adapter helpers live in `adapters/llm`. They translate provider-style
messages, tool descriptors, stream chunks, redaction policy, and streamed tool
calls into the `runtime/agent/llmagent.Model`/`StreamingModel` ports. They do
not own agent loop semantics and do not call live provider APIs by themselves.

`runtime/conversation` owns provider transcript projection. It can return full
replay items or a native continuation handle plus pending delta items. Provider
adapters consume the projection; they should not derive model history from
generic session summaries.

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
| Resources | `core/resource` + `adapters/appconfig`/`adapters/agentdir` + `orchestration/app` | Contribution data is core; loading/parsing is adapter; executable composition is orchestration. |
| Plugins | `core/plugin` or `orchestration/pluginhost` + `plugins/*` | Keep contracts separate from implementations. |
| Datasources | `core/datasource` + `plugins/datasources` | Search contract in core; runtime/provider details outward. |
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
| `action/declarative` | `runtime/operation/declarative` or app/resource config adapters | Split declarative metadata from executable aliases/shell bindings. |
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
| `capabilities/planexec` | `plugins/task` | Replaced by the event-sourced task plugin and task executor. |
| `channel` | `core/channel` + `orchestration/harness` | Core keeps normalized envelopes/specs; harness owns routing/session binding. |
| `channel/httpapi` | `adapters/httpchannel` | Server-side implementation of the direct HTTP/SSE channel. Keep separate from daemon control API. |
| `channels/slackchan` | `adapters/slack` | Protocol adapter. |
| `cmd/agentsdk` | `apps/coder/cmd/coder` + `apps/coder` | Legacy product CLI replaced by coder. |
| `cmd/agentclient` | `apps/agentclient` + `adapters/httpchannel/client` | Product CLI over the direct channel client. It should not call session directly. |
| `cmd/agent-sim` | `apps/agentsim` or test tool | Probably app/tooling. |
| `command` | `core/command` + parser adapter | Descriptors/results/core tree in core; slash parsing may be adapter if terminal-specific. |
| `command/markdown` | `adapters/agentdir` | Markdown command format reader. |
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
| `resource/loader` | `adapters/appconfig`/`adapters/agentdir` | Filesystem appconfig and `.agents` discovery. |
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
2. Should workflow execution grow a durable read model, or are emitted workflow
   and runtime events sufficient until larger workflow state requirements are
   proven?
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
- `orchestration/workflow`: dependency-ordered workflow execution over
  orchestration-provided operation and agent dispatch callbacks.
- `runtime/eventstore`: in-memory append-only event store.
- `adapters/sqleventstore`: SQLite-backed event store adapter.
- `core/thread` + `runtime/thread`: event-backed thread store, branch model,
  thread index, list replay fallback, and optional projected read model.
- `core/projection`, `runtime/projection`, `orchestration/projections`:
  checkpoint/projector contracts, runner, and orchestration freshness manager.
- `core/command`, `core/invocation`, `core/policy`: channel-facing command
  specs, invocation target vocabulary, caller/trust/policy evaluation.
- `core/tool`: provider-neutral model-facing tool projection descriptors.
- `core/channel`: normalized channel specs and inbound/outbound envelopes.
- `orchestration/session`: command and conversational input execution for one
  bound thread, including workflow-target command dispatch.
- `orchestration/sessioncontrol`: session control-plane helpers such as
  continuation stop-condition evaluation, built-in command policy/target
  checks, resource aliases, and LLM-driver control helpers.
- `orchestration/sessionenv`: runtime context assembly for session work,
  including context materialization, skill and datasource access, persisted
  skill replay, and session scope wiring.
- `orchestration/resourcecatalog`: contribution-bundle catalog collection for
  app, agent, skill, context-provider, datasource, LLM, workflow, and operation
  set specs.
- `orchestration/appresources`: executable resource binding for app
  composition, including operation, tool set, command, and session catalogs.
- `orchestration/agentconfig`: agent spec filtering and default-agent
  selection used by app/session composition.
- `orchestration/eventregistry`: event type registry assembly from
  contribution bundles.
- `orchestration/harness`: channel conversation binding, explicit
  session-bound execution, semantic event fanout, and replay from thread store.
- `orchestration/client`: transport-neutral `ChannelClient`, `SessionHandle`,
  `RunHandle`, `Submission`, `Result`, and semantic event contracts.
- `adapters/directchannel`: in-process channel client over harness.
- `adapters/httpssechannel`: remote JSON + SSE channel adapter preserving the
  same client/session/run API.
- `orchestration/app`: resource/app composition over supplied runtime
  implementations, backed by `core/resource` identities, indexes, resolver,
  app/agent/skill/context-provider/workflow catalogs, command catalog,
  operation catalog, and session catalog. Duplicate short names may coexist
  when canonical resource IDs differ.
- `orchestration/pluginhost`: minimal plugin contribution contract and plugin
  ref resolution into resource bundles plus optional executable operation
  implementations. Plugin bundles now receive plugin source refs when the
  implementation does not provide one.
- `plugins/echoplugin`: first migrated low-IO plugin, contributing a pure echo
  operation and command projection.
- `plugins/textplugin`: second migrated low-IO plugin, contributing
  configurable pure text transformation operations and commands.
- `adapters/appconfig`: narrow `fluxplane.yaml` manifest parser for
  app specs, source declarations, discovery policy, model policy, plugin refs,
  and embedded or standalone command, workflow, and operation declarations.
- `adapters/agentdir`: narrow `.agents` parser for the engineer subset:
  agent markdown, prompt command markdown, workflow command YAML, workflow YAML,
  operation and agent workflow steps, and skill metadata.
- `adapters/llm`: provider-neutral adapter toolkit for message parts, tool
  descriptors, stream normalization, redaction, streamed tool-call assembly,
  and scripted fake provider models.
- `adapters/openai`: first live model provider adapter. It uses
  `openai-go/v3` and the Responses API, maps projected runtime tools to
  function tools, converts multiple function calls into batched operation
  requests, and owns shared Responses runtime config for transport,
  continuation, prompt caching, and future Responses-compatible providers.
- `adapters/codex`: Codex Responses provider wrapper using the Codex backend
  URL and local Codex OAuth file (`~/.codex/auth.json` or `CODEX_AUTH_PATH`).
- `adapters/openrouter`: OpenRouter Responses provider wrapper using
  `OPENROUTER_API_KEY`, exact modeldb model IDs, stateless replay
  continuation, and provider-neutral cache request fields.
- `adapters/anthropicmessages`: generic Anthropic-compatible Messages API
  adapter with HTTP/SSE streaming, tool use, replay continuation, usage, and
  modeldb pricing/capability metadata.
- `adapters/anthropic` and `adapters/minimax`: thin provider wrappers for
  Anthropic API-key Messages and MiniMax's Anthropic-compatible Messages
  endpoint.
- `adapters/modelcatalog`: bridge from `github.com/codewandler/modeldb` into
  `core/llm` provider/model/pricing/capability specs.
- `runtime/operation`: pre-execution safety gate/envelope shape for sandbox,
  ACL, command-risk, secrets, and approval enforcement.
- `runtime/agent/llmagent`: first provider-neutral LLM-backed agent runtime
  skeleton with a model port, structured request/response shape, fake/test
  model path, projected tool descriptors, opt-in streaming deltas, redacted
  lifecycle events, and decision mapping.
- `orchestration/toolprojection`: projects command and operation resources into
  model-facing tool descriptors after applying command policy, caller/trust,
  executable binding checks, and safe-by-default operation semantics.
- `orchestration/agentfactory`: resolves composed `core/agent.Spec` entries
  from configured sessions, instantiates provider-neutral LLM agents with an
  injected model implementation, and narrows projected tools by agent-declared
  commands, operations, or tool names.
- `orchestration/daemon` + `adapters/httpcontrol`: separate process/control
  status, configured-session listing, and session-listing surface over an
  existing channel client.
- root `fluxplane.Service`: public in-process facade for library consumers.
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
- Command and workflow helper sessions run through `orchestration/sessionagent`;
  configured sessions carry delegation policy, parent/child causation, and
  child session identity without a separate sub-agent domain.
- Configured session refs are resolved through resource identity and then
  canonicalized to the resolved `ResourceID` address before harness binding.
- Real side-effecting operations must enter through the operation safety
  envelope from their first migration batch.
- Agent operation decisions are batches. An agent may request multiple
  operations from one step; orchestration may execute them sequentially now and
  later schedule them through policy-aware planning or workflow machinery.
- Every concrete operation invocation gets a stable `call_id` assigned by
  session orchestration. The same ID must appear on `operation.requested`,
  runtime `operation.started`/`operation.completed`, and typed
  `operation.completed` client events so repeated calls to the same operation
  can be correlated.
- Model-facing tools are projected views. They do not bypass command/session
  policy or operation runtime safety.
- HTTP/SSE channel transport must preserve the same semantic event contract as
  directchannel. `operation.requested`, typed `operation.completed`,
  `runtime.emitted` usage/model stream events, and `call_id` fields are part of
  that contract.
- LLM streaming emits transient opt-in runtime events. Raw thinking is not
  exposed by default.
- The final normalized LLM response is authoritative. Streaming is UX,
  debugging, and progress; it may help assemble tool calls, but runtime
  decisions consume the final `llmagent.Response`.
- Usage accounting is a cross-cutting runtime concern. `core/usage` owns the
  generic metering event shape for consumed resources: LLM input,
  cached-input, output, and reasoning tokens; network up/down bytes and
  requests; file read/write bytes; process/runtime resources; and other
  operation-specific usage. `runtime/usage` evaluates costs from usage events
  and catalog pricing. Enforcement and budgets remain future runtime policy.
- LLM model/provider catalogs live in `core/llm`, not in a generic
  `core/model` or `core/provider` namespace. Provider specs describe model
  IDs, pricing units, capabilities, and constraints: tool calling, caching,
  thinking/reasoning, streaming, context windows, output limits, modalities,
  safety features, and provider-specific knobs. `github.com/codewandler/modeldb`
  is the source catalog for provider/model lists, supported APIs, parameters,
  capabilities, and pricing. OpenAI, Codex, Anthropic API, Claude Code, and
  selected compatible providers are available as native providers.

Anthropic provider migration notes:

- Anthropic API now uses the generic Messages adapter with API-key auth and
  modeldb metadata for `anthropic-messages` support, pricing, caching,
  thinking, and tool capabilities.
- MiniMax is wired through the same generic Messages adapter against its
  Anthropic-compatible endpoint.
- Claude Code is a separate provider mode using local Claude OAuth
  credentials, Claude Code CLI-compatible headers/preflight behavior, system
  cache-control, and context-management request shape. It must act like Claude
  Code rather than a plain Anthropic API client.
- Prompt caching differs across Anthropic/Claude Code and OpenAI Responses:
  modeldb capabilities should drive whether cache control is top-level,
  per-message/block, implicit, or configurable.
- OpenAI `tool_search` remains a parity target for large tool sets: implement
  it behind modeldb capability/parameter checks and keep normal function-tool
  projection as the fallback.

Still intentionally incomplete:

- `SubmissionEvent` and `SubmissionSignal` are modeled but not executed.
- Only low-IO first-party plugins have been migrated. Shell, browser,
  filesystem, and git plugins are intentionally blocked on concrete safety
  adapters.
- The plugin contract is intentionally minimal and only contributes resource
  bundles plus operation implementations during app composition.
- The daemon/control surface is minimal: status, configured-session listing,
  and live session listing only.
- The engineer app parity plan has Phase 1A pure model support, Phase 1B
  narrow adapters, Phase 1C composition catalogs, and manifest-declared LLM
  agent instantiation through configured sessions. Prompt commands and workflow
  commands now execute through session orchestration. Skill discovery and
  trigger activation now exist for composed skill catalogs, including the
  standalone `coder` app's project and user resource roots.
- The first live provider adapters are OpenAI Responses, Codex Responses,
  OpenRouter Responses, Anthropic Messages, and MiniMax Messages. They are
  still narrow: no complete WebSocket transport implementation in the native
  OpenAI SDK path, retry policy, conversation compaction, durable usage
  aggregation, budget enforcement, or Claude Code provider yet.
- Configured sessions can instantiate manifest-declared LLM agents when the
  host provides `LLMModel`, `LLMModelResolver`, or the devclient `-openai`
  path. Model routing policy beyond that first adapter is still open.
- `adapters/llm` is not a live provider adapter. It is the shared translation
  toolkit live adapters should use.
- There is no terminal/slash parser, Slack adapter, Anthropic/local model
  provider adapter, or approval UX yet.
- The old plan execution plugin has been replaced by `plugins/task`,
  `core/task`, `runtime/task`, and `orchestration/taskexecutor`. `/task`
  creates durable event-sourced tasks, `/plan` plans draft tasks for approval,
  and ready tasks execute through task events and worker profiles.
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
  svc, err := fluxplane.New(...)
  s, err := svc.Open(ctx, fluxplane.OpenRequest{...})
  run, err := s.SendInput(ctx, fluxplane.Input{Text: "hello"})
  result, err := run.Wait(ctx)
  ```

- A resource/app consumer can load an app directory, compose commands,
  operations, agents, context providers, and plugins, then open a session
  through the same `ChannelClient` API.
- Configured sessions can express task worker boundaries and the task executor
  can route work through allowed profiles with concurrency limits and
  parent/task causation metadata.
- A daemon can start configured sessions from app resources, attach event
  triggers, expose a control API for process/admin state, and expose a channel
  API for user/client submissions without mixing those two HTTP surfaces.
- Existing `cmd/agentclient` behavior can be represented as a CLI using a
  channel client; it must not call harness/session internals directly.
- At least one real migrated resource format is supported end to end, not just
  hand-built Go config.
- Real operation implementations are introduced safety-first. Any shell,
  filesystem, network, browser, or code execution work must include sandboxing
  shape, ACL/scope checks, command-risk classification
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
   Extend appconfig and agentdir only where app composition needs it next:
   plugin refs, agent specs, context provider specs, and workflows. Keep
   `.agents`, `agentdir`, and appconfig compatibility as adapter packages, not
   core concepts.

3. **Configured Session Host**
   Initial configured session loading, cataloging, harness defaults,
   daemon listing, and devclient selection exist. Next, decide whether daemon
   startup should eagerly open configured sessions from app resources or only
   open them on trigger/client demand. Keep user/client traffic on
   `ChannelClient` either way.

4. **Sub-Agent Supervisor Hardening**
   The first worker execution slice exists: capacity limits, cancellation,
   progress callbacks/events, prepare-then-start dispatch, parent/child
   causation, durable plan projection, and background plan following in terminal
   and Slack. Next, tighten context and command narrowing for child profiles,
   move worker/explorer profiles into resources, and add richer child-run
   reattachment after process restart.

5. **Operation Safety Runtime**
   Initial `codewandler/cmdrisk` integration, shell hardening, and HTTP target
   checks exist for the coder host. Next, extract reusable sandbox/ACL/secret
   policy from app-local helpers before migrating filesystem/git/browser/web
   operations. Real process isolation, filesystem scopes, network egress
   scopes, approval hooks, and resource usage events remain open.

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
apps/engineer/resources/fluxplane.yaml
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
   default agent. Done: catalogs exist for app/agent/skill/context-provider/
   workflow specs; prompt commands are accepted as prompt targets; workflow,
   agent, and session command targets resolve to resource IDs; operation
   command targets still require executable operation bindings; app default
   agents create a default configured session.

4. **Phase 2: LLM Agent Runtime Skeleton**
   Add `runtime/agent/llmagent` with a fake/test model path before migrating
   real provider transport. Done: the runtime builds structured model
   requests from agent steps, maps model responses to message, completion, or
   one-or-more operation decisions, and emits redacted model lifecycle events.

5. **Phase 3: Tool Projection and Safety**
   Project operations/commands into model tools only behind the operation
   safety envelope. Done: `core/tool` defines provider-neutral descriptors;
   `orchestration/toolprojection` applies command policy, caller/trust,
   executable operation binding checks, and safe-by-default operation
   semantics; `runtime/agent/llmagent` includes projected tools in model
   requests.

6. **Phase 4: Configured Agent Instantiation**
   Turn composed `core/agent.Spec` entries into runnable agents for configured
   sessions. Done: `orchestration/agentfactory` resolves configured session
   agent refs, constructs provider-neutral LLM agents with an injected model or
   model resolver, projects tools from the composition, and lets app/default
   sessions execute through the normal harness/session path.

7. **Phase 5: Provider Adapter Boundary**
   Add the provider-neutral adapter toolkit and first live provider smoke path.
   Done: `adapters/llm` models provider-style messages/tools/stream chunks,
   redacts thinking and sensitive tool args, assembles streamed tool calls into
   operation requests, and includes scripted fake provider models for tests.
   Done: `adapters/openai` calls the OpenAI Responses API through
   `openai-go/v3`, maps projected tools to function tools, streams
   content/thinking/tool-call deltas into runtime events, converts multiple
   function calls into operation requests, applies shared Responses runtime
   config for caching/continuation, and is reachable from
   `apps/devclient -openai`. Done: `adapters/codex` wraps the Codex Responses
   backend using local Codex OAuth credentials, and `adapters/openrouter`
   reuses the shared Responses adapter for OpenRouter models exposed through
   modeldb. The existing devclient also has an app-injected
   `-synthetic-tool` operation for proving OpenAI tool-call continuation
   without adding a separate example app.

8. **Phase 6: Model Turn Continuation**
   Feed operation results back into LLM turns until the agent emits a message,
   completion, wait, or policy stop. Keep the continuation loop in runtime or
   orchestration, not in provider adapters. Done initially for session input:
   LLM agents receive operation results as follow-up observations through an
   inner loop bounded by `turns.max_steps`. A separate
   `turns.continuation.max_continuations` cap applies only to
   stop-condition-driven follow-up turns after a terminal response.

9. **Phase 7: Product CLI and Coder App**
   Use `apps/coder/cmd/coder` as the product CLI over the first-party coder
   app. The nested `apps/coder` module keeps product assembly and app lifecycle
   behavior; reusable runtime operation implementations stay in plugins and
   adapters.

10. **Phase 8: Usage and Model Catalogs**
   Add normalized runtime usage events and provider model catalog specs. Usage
   events should cover LLM tokens, cache hits, reasoning tokens, network
   bandwidth, file bytes, and operation-specific resource consumption. Catalogs
   should describe model capabilities and pricing so cost evaluation is data
   driven. Done initially: `core/usage` defines generic usage records,
   `core/llm` defines LLM provider/model/pricing/capability specs,
   `runtime/usage` evaluates and enriches estimated costs from pricing specs,
   `adapters/modelcatalog` hydrates specs from `github.com/codewandler/modeldb`,
   `core/usage` includes session accumulation, `adapters/openai` emits LLM
   token usage from Responses usage, and `adapters/openrouter` reuses the
   Responses adapter with OpenRouter auth and modeldb validation. Schedule
   Anthropic API and Claude Code in this provider expansion path.

10. **Phase 8: Local CLI Capability Plugin**
   Migrate shell/filesystem/git/search tools through adapters and plugins with
   sandboxing, ACL, command-risk classification, approval, audit, and secret
   handling from the start. Done initially: `runtime/system` defines the
   host-guarded workspace/network/process/browser/clarifier boundary;
   `plugins/codingplugin` aggregates filesystem, web, browser, git, shell,
   background process management, scratch `code_execute`, and `clarify`;
   `runtime/operation.NewTyped` and `NewTypedResult` generate operation
   input/output JSON Schemas from typed Go structs; standard operations emit
   usage events for IO boundaries; HTML responses use the same
   html-to-markdown library as the old implementation. The terminal coder app
   now renders operation begin/end, process output, grouped usage totals, and
   clarify prompts through `adapters/terminalui`.
   Still planned: overlay workspace commit/rollback, stronger approval UX,
   richer web search, durable process ownership, and real sandbox backends such
   as Docker profiles, bubblewrap, or Firecracker.

11. **Phase 9: Commands and Skills Runtime**
   Execute prompt commands, support skill discovery/activation, and preserve
   command visibility policies. Done initially: Markdown prompt commands decode
   to `TargetPrompt`, render invocation args/input, and execute through the
   normal session input path so outbound responses are persisted with the
   thread.

12. **Phase 10: Workflow and Session-Agent Execution**
   Execute the `feature` workflow through helper sessions with capacity,
   progress, parent/child causation, and event linkage. Current state:
   workflow-target commands execute operation steps through the operation
   executor and agent steps through the neutral session-agent runner, with
   workflow runtime events and persisted command output.

13. **Phase 11: Engineer App Assembly**
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
