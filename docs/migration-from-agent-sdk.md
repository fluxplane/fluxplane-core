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
| `harness/worker` | `orchestration/session/worker` or `plugins/planexec` | Depends whether generic delegation or planexec-specific. |
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
- `core/event`: event records, stream store port, registry, append conflict and
  batch append contracts.
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
  command catalog, and operation catalog. Duplicate short names may coexist
  when canonical resource IDs differ.
- `orchestration/pluginhost`: minimal plugin contribution contract and plugin
  ref resolution into resource bundles plus optional executable operation
  implementations. Plugin bundles now receive plugin source refs when the
  implementation does not provide one.
- `plugins/echoplugin`: first migrated low-IO plugin, contributing a pure echo
  operation and command projection.
- `plugins/textplugin`: second migrated low-IO plugin, contributing
  configurable pure text transformation operations and commands.
- `adapters/resourcefs`: first local filesystem manifest loader
  (`agentruntime.json`) into pure resource contribution bundles.
- `runtime/operation`: pre-execution safety gate/envelope shape for sandbox,
  ACL, command-risk, secrets, and approval enforcement.
- `orchestration/daemon` + `adapters/httpcontrol`: separate process/control
  status and session-listing surface over an existing channel client.
- root `agentruntime.Service`: public in-process facade for library consumers.
- `apps/devclient`: small dogfood client for local and HTTP/SSE execution,
  including debug event/result output and `-app` resource manifest loading.

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
- Real side-effecting operations must enter through the operation safety
  envelope from their first migration batch.

Still intentionally incomplete:

- `SubmissionEvent` and `SubmissionSignal` are modeled but not executed.
- Only low-IO first-party plugins have been migrated. Shell, browser,
  filesystem, git, and connector plugins are intentionally blocked on concrete
  safety adapters.
- The plugin contract is intentionally minimal and only contributes resource
  bundles plus operation implementations during app composition.
- The daemon/control surface is minimal: status and session listing only.
- The resource loader supports only the first JSON manifest shape; `.agents`,
  `agentdir`, and appconfig compatibility are not migrated yet.
- There is no LLM-agent runtime yet; `core/agent/llmagent` only contains pure
  spec shape.
- There is no terminal/slash parser, Slack adapter, model provider adapter, or
  approval UX yet.
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
   Extend `orchestration/daemon` from status/listing to configured session
   startup from composed app resources. Keep user/client traffic on
   `ChannelClient`.

4. **Operation Safety Runtime**
   Harden the initial safety envelope with concrete adapters before migrating
   shell/filesystem/git/browser/web operations: sandbox execution boundary,
   ACL/scope evaluation, `codewandler/cmdrisk` integration or successor,
   secret handling/redaction, approval hooks, audit events, and environment
   boundary checks.

5. **CLI Client Migration**
   Rebuild the old `cmd/agentclient` shape as an app over channel clients:
   direct for in-process dev mode, HTTP/SSE for daemon mode. Keep `devclient`
   as the small diagnostic client until the real CLI is good enough.

6. **Trigger and Signal Execution**
   Implement `SubmissionSignal` as the entry point for timers, file watchers,
   and daemon-triggered sessions. File watcher/timer IO belongs in adapters or
   apps; signal dispatch semantics belong in orchestration.

7. **Context Provider Runtime**
   Add the first narrow `core/context` provider contracts and
   `runtime/context` materializer, then migrate only providers needed by the
   first app. File/git/shell-backed providers should remain adapter/plugin
   owned.

Do not start with LLM provider integration or Slack. They are important, but
they depend on the app/plugin/channel boundaries being stable enough that they
do not drag old architecture back into the rewrite.

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
