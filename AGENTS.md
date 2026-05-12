# Fluxplane Agent Runtime Architecture Notes

This repository is `github.com/fluxplane/agentruntime`, the rewrite target for
the current `agentsdk` codebase. It is an agent runtime plus SDK: a pure inner
model, concrete runtime implementations, orchestration/use-case services,
adapters, first-party plugins, and assembled apps.

The main architectural goal is cognitive and mechanical separation:

```text
core -> runtime -> orchestration -> plugins/adapters -> apps
```

Dependencies must point inward. Outer layers may depend on inner layers; inner
layers must not depend on outer layers.

Architecture import rules are executable. `internal/architecture` builds the
package import graph from `go list`, validates layer direction, computes a
coupling score, and renders reports. The fail-fast test remains the authority
for violations; the report is the recurring review artifact for refactoring
decisions.

```bash
go test ./internal/architecture
go run ./apps/archreport
go run ./apps/archreport -format json
go run ./apps/archreport -format dot
go run ./apps/archreport -format mermaid
```

The score is intentionally directional, not absolute truth. Boundary
violations carry the largest penalty. Runtime sibling imports and high fan-out
inside `core`, `runtime`, and `orchestration` reduce the score because they
usually indicate concepts that want splitting or a clearer composition point.
High fan-in to core and app/facade fan-out are reported but not treated as
problems by themselves.

## Layer Layout

### `core/`

`core` is the inner domain/kernel layer.

It contains pure data types, specs, small builders, stable identifiers,
schemas, events, result types, policies, and minimal contracts that are part of
the domain model.

Allowed in `core`:

- value objects such as IDs, refs, names, policies, descriptors, event payloads;
- inert specs such as agent specs, operation specs, workflow specs, command
  descriptors, resource contributions;
- builders that only assemble data;
- pure validation and normalization;
- small interfaces only when the inner model genuinely needs a port.
- pure registries over specs or core port interfaces. Registries may store and
  return values, but must not execute, instantiate, wrap, validate, observe, or
  own lifecycle.

Forbidden in `core`:

- filesystem, environment, process, terminal, network, HTTP, Slack, browser,
  database, JSONL, SQLite, model-provider clients, goroutine hosts, or daemon
  lifecycle;
- concrete plugin discovery;
- rendering;
- persistence implementation details;
- package imports from `runtime`, `orchestration`, `plugins`, `adapters`, or
  `apps`.

Core should answer: "What is the stable shape of this agent system?"

Candidate subpackages:

```text
core/agent
core/operation
core/command
core/invocation
core/workflow
core/thread
core/context
core/environment
core/capability
core/skill
core/policy
core/resource
core/channel
core/safety
core/usage
```

### `runtime/`

`runtime` contains concrete implementations of core contracts and execution
mechanics that are still surface-neutral.

Runtime packages depend on `core` and should not depend on sibling runtime
packages unless an explicit local design note says why. If one runtime
implementation needs to compose several other implementations, that composition
usually belongs in `orchestration`.

Allowed in `runtime`:

- agent turn engine implementations;
- operation executors and middleware;
- model request/response shaping behind core model-client ports;
- context materialization engines;
- capability runtime implementations;
- event projection implementations;
- persistence implementations when they are surface-neutral, such as eventstore
  backends, JSONL, or SQLite stores.

Forbidden in `runtime`:

- CLI/terminal presentation;
- HTTP/SSE or Slack protocol handling;
- app/product assembly;
- plugin selection policy;
- filesystem resource discovery policy unless it is a narrow adapter
  implementation behind a core/resource port.

Runtime should answer: "How is this core concept executed or stored?"

Candidate subpackages:

```text
runtime/agent
runtime/model
runtime/operation
runtime/context
runtime/thread
runtime/capability
runtime/usage
runtime/compaction
```

### `orchestration/`

`orchestration` is the application/use-case layer. It composes core definitions
and runtime implementations into higher-level flows.

This layer may depend on `core` and `runtime`. It should not depend on concrete
terminal, HTTP, Slack, browser, or app packages.

Allowed in `orchestration`:

- session service and session lifecycle;
- agent turn use cases;
- command dispatch against a live session;
- workflow execution as a session use case;
- trigger scheduling as a session/workflow use case;
- resource-to-app composition after adapters have loaded resources;
- event fanout and read-model coordination;
- plugin contribution resolution at the abstract plugin contract level.

Forbidden in `orchestration`:

- terminal rendering;
- protocol wire formats;
- provider-specific transport code;
- concrete first-party plugin internals;
- filesystem crawling details.

Orchestration should answer: "Which runtime pieces are combined to fulfill a
user/application use case?"

Candidate subpackages:

```text
orchestration/app
orchestration/session
orchestration/workflow
orchestration/trigger
orchestration/client
orchestration/harness
orchestration/pluginhost
```

`orchestration/client` defines the user-facing channel client, session handle,
run handle, `Submission`, and semantic event contracts. A direct in-process
channel and a remote HTTP/SSE channel should implement the same interfaces.
Client events are the shared live/replay shape; replay cursors are thread event
sequence positions, not transport-specific offsets.

The client contract is handle-oriented:

```text
ChannelClient.Open/Resume/ListSessions
  -> SessionHandle
  -> Submit/SendInput/SendCommand
  -> RunHandle
  -> Events/Done/Wait/Result
```

Transport differences must not leak through that contract. An in-process
direct channel and a remote HTTP/SSE channel should return the same logical
session and run handles, normalize caller/trust the same way, expose the same
semantic events, and preserve run/session/submission identity on failures.
`Submission` is the neutral ingress shape for user input, commands, future
events, and future signals/triggers.

`orchestration/harness` is the channel-to-session use-case boundary. It should
receive normalized `core/channel.Inbound`, bind channel conversations to
session/thread refs, delegate execution to `orchestration/session`, and publish
normalized `core/channel.Outbound`. Adapters and apps should call harness, not
reach into sessions directly, unless they are testing session behavior itself.

Once a channel client has opened or resumed a session, execution should use the
resolved session/thread identity instead of re-resolving from channel and
conversation. Channel/conversation metadata may still be supplied by the
inbound envelope for routing and outbound production, but the thread selected by
the session handle is authoritative.

`orchestration/app` composes pure resource contribution bundles with supplied
runtime implementations. Resource operation specs are declarations; executable
operation implementations still come from runtime/plugin/app code. Composition
may validate that resource commands point at available operation
implementations, but it must not read files or choose protocol adapters.
Composition builds a `core/resource` index and resolver. Local contribution
names are allowed to collide when their canonical `ResourceID`s differ; only
duplicate canonical IDs fail. Command targets are resolved in the source scope
of the command first, then through the app resolver. Ambiguous unqualified refs
are resolver/policy concerns, not flat-registry errors.
Operation specs in resources are declarations. Command execution must resolve
to an executable operation binding in the operation catalog; declaration-only
operation specs are discoverable but do not satisfy executable command targets.

`orchestration/daemon` models process/control-plane state over an already
assembled channel client. It can list sessions and report host status, but it
must not become a second session execution path.

`orchestration/pluginhost` resolves plugin refs into resource contribution
bundles and optional executable operation implementations during app
composition. It owns plugin selection at the abstract contract level only;
concrete first-party implementations still belong in `plugins/`.
When a plugin returns a bundle without a source, pluginhost stamps it with a
plugin source ref so later composition diagnostics can point at the contribution
that caused the failure.

Plugin ref `config` is plugin-owned data. Resource adapters may preserve it,
and pluginhost may pass it through, but only the selected plugin implementation
should interpret it. A plugin should reject unsupported or unsafe config early
instead of silently broadening its contribution set.

### `adapters/`

`adapters` contains IO and protocol boundaries. Adapters translate the outside
world into core/orchestration requests and translate events/results back out.

Allowed in `adapters`:

- filesystem resource loading and discovery;
- `.agents`, appconfig, agentdir, and compatibility format readers;
- terminal CLI/REPL/TUI;
- HTTP/SSE;
- Slack;
- model provider transports if they are not kept in `runtime/model`;
- browser automation transport;
- shell/process execution;
- JSONL/SQLite/eventstore backends if the team chooses to treat persistence as
  infrastructure instead of surface-neutral runtime.

Forbidden in `adapters`:

- new domain concepts;
- session semantics that bypass orchestration;
- plugin contribution types that should be core contracts.

Adapters should answer: "How does an outside system talk to the runtime?"

HTTP surfaces must stay conceptually separate:

- **channel HTTP/SSE** exposes the session channel contract remotely. It is a
  transport for `ChannelClient`, `SessionHandle`, `RunHandle`, submissions, and
  semantic event streams.
- **daemon/control HTTP** will manage a running process: status, configured
  sessions, trigger state, logs, health, and administrative lifecycle.

The channel surface must not grow daemon control semantics, and the daemon
control surface must not become a side door into sessions that bypasses channel
trust, submission, and harness rules.

`adapters/resourcefs` is the local filesystem resource boundary. It may parse
files such as `agentruntime.json` into `core/resource.ContributionBundle`, but
all execution, plugin selection, and session behavior must stay in
orchestration/runtime/apps.

The seed `agentruntime.json` manifest may declare operation specs, command
projections, and plugin refs with plugin-owned config. This is a rewrite-local
seed format used to prove composition; compatibility with external formats must
be implemented as additional adapters rather than folded into core.

Candidate subpackages:

```text
adapters/resourcefs
adapters/agentdir
adapters/appconfig
adapters/terminal
adapters/httpapi
adapters/directchannel
adapters/slack
adapters/shell
adapters/browser
adapters/eventjsonl
adapters/eventsqlite
```

### `plugins/`

`plugins` contains first-party plugin implementations. A plugin contributes
operations, tools, context providers, capabilities, workflows, commands,
datasources, or resource sources through core/orchestration plugin contracts.

Plugin contracts belong in `core` or `orchestration`, not in each plugin
implementation package.

Allowed in `plugins`:

- concrete plugin packages such as git, code, browser, skills, memory, plan
  executor, datasources, and local CLI support;
- plugin-specific adapters when they are tightly scoped to one plugin;
- plugin tests and fixtures.

Forbidden in `plugins`:

- global app assembly;
- hidden default bundles;
- core abstractions used by unrelated packages.

Plugins should answer: "Which optional capability bundle is being contributed?"

### `apps/`

`apps` contains assembled products and dogfood apps.

Apps may depend on all inner layers and selected adapters/plugins. Apps own
product policy, default plugin sets, command-line choices, branding, and
deployment-specific configuration.

Allowed in `apps`:

- `run`, `engineer`, `builder`, and other first-party apps;
- product-specific defaults;
- app-specific embedded resources;
- app-specific composition tests.

Forbidden in `apps`:

- reusable domain model;
- reusable runtime semantics that should live in inner layers.

Apps should answer: "What product did we assemble from the runtime and SDK?"

## Resource Boundary

The current `agentsdk/resource`, `agentdir`, `appconfig`, and resource loader
code mixes three concerns that should be split:

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

## Plugin Boundary

The plugin model should have one contribution contract instead of many ad hoc
systems.

Preferred shape:

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

## Semantics, Policy, and Trust

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
may be exposed through different tools, commands, channels, or agent projections
with different policies. Runtime and orchestration combine operation semantics,
projection policy, caller trust, and environment boundary to make enforcement
decisions.

When operation implementations move beyond pure/in-memory examples, implement
them safety-first. Do not add shell, filesystem, network, browser, code
execution, or connector operations as plain function calls and retrofit safety
later. The first real operation runtime batch must include the enforcement
shape for sandboxing, ACL/scope checks, command-risk classification
(`codewandler/cmdrisk` or successor), secret handling/redaction, approval
requirements, audit events, and environment boundaries.

## Projections and Tools

`operation` is the executable primitive. A projection describes how a caller or
driver sees an invocation target.

Commands are core because they are channel-facing invocation specs that can be
declared by resources and consumed by terminal, HTTP, Slack, or other channel
adapters.

Do not add a generic `core/tool` package unless "tool" becomes a
driver-independent core concept. In this rewrite, LLM/model tools are expected
to be driver-facing projections of operations and should live near the LLM agent
driver or model adapter layer when that need is concrete.

Shared target vocabulary belongs in `core/invocation`.

## Event State Rule

Durable state should be reconstructable from events plus configuration.

Live runtime structs may cache projected state, but they should not be the
authoritative source of long-term state.

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
stream appends atomically or none of them. Use batch appends for cross-stream
invariants, such as keeping a thread history stream and its index stream in
sync.

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

The index stream supports listing. Per-thread streams keep optimistic
concurrency scoped to one thread instead of serializing unrelated conversations.
Concrete adapters still store these logical streams however they want; for
SQLite they are rows in one database table, not separate databases.

Memory event stores belong in `runtime/eventstore`, not in `core/event`.
Although they are IO-free, they are mutable runtime implementations: they assign
IDs, timestamps, schema defaults, and stream-local sequence numbers, and they
own concurrency behavior. Filesystem, SQL, and protocol-backed event stores
belong in adapters such as `adapters/sqleventstore` or
`adapters/eventjsonl`.

Shared event record normalization and typed JSON payload encoding belong in
`runtime/eventcodec`. Core owns the event model and registry contract, but
runtime owns defaults and serialization behavior. Adapters should reuse the
runtime codec instead of duplicating payload, attribute, sensitivity, timestamp,
ID, and schema-version rules.

`core/projection` defines pure event projection contracts such as checkpoints
and projectors. Runtime projection runners and checkpoint stores live in
`runtime/projection`. Concrete read models belong near the runtime concept they
serve; for example, the thread index read model lives in `runtime/thread`.
Runtime services may accept projected read models as optional accelerators, but
must keep a replay fallback unless orchestration guarantees the projection is
available and current.

Projection freshness policy belongs in orchestration. `orchestration/projections`
coordinates runtime projection runners for use cases, such as ensuring a thread
index has caught up before serving an index-backed list. It should not own
storage implementations, daemon scheduling, adapter protocols, or app-specific
lifecycle.

Do not put live lifecycle in core thread contracts. Flush, shutdown, buffering,
discarding uncommitted local state, and process-owned live handles are runtime
or orchestration concerns.

Event payloads are not access-controlled by themselves. Event records carry
classification metadata such as `policy.Sensitivity`; missing sensitivity must
be treated as restricted. Event streams, subscriptions, and channel adapters
must enforce observation access using caller trust/scopes, record sensitivity,
and stream-specific policy once those surfaces exist.

## Naming Rule

Use layer names for top-level directories and concept names below them.

Good:

```text
core/workflow
runtime/agent
orchestration/session
adapters/terminal
plugins/git
apps/builder
```

Avoid:

```text
core/misc
runtime/common
orchestration/utils
plugins/standard
```

Use `Spec` for inert, user-authored, or resource-authored configuration shapes:

```text
agent.Spec
operation.Spec
environment.Spec
workflow.Spec
context.ProviderSpec
```

Reserve `Definition` for a distinct normalized, validated, executable internal
form if the codebase later needs to distinguish authored config from prepared
runtime form. Do not use `Definition` as a synonym for `Spec`.

## Migration Rule

Do not copy packages wholesale just because names match. For every old package,
identify the concepts inside it and split by responsibility:

- pure model/spec/event data -> `core`;
- execution implementation -> `runtime`;
- use-case composition -> `orchestration`;
- IO/protocol/loading/rendering -> `adapters`;
- optional first-party contribution bundle -> `plugins`;
- assembled product -> `apps`.

If one old package maps to multiple new packages, split it. This rewrite is not
required to preserve source compatibility.
