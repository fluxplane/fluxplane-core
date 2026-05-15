# Architecture

Fluxplane Agent Runtime is organized as a layered Go module. The layers keep
stable domain concepts separate from execution, use-case composition, IO
adapters, optional plugins, and assembled products.

This document describes the current architecture. Migration history and older
package disposition notes live in
[migration-from-agent-sdk.md](migration-from-agent-sdk.md). Operation safety
details live in [security.md](security.md).

## Layer Model

The shortest version:

```text
cmd
  -> apps
     -> plugins
     -> adapters
        -> orchestration
           -> runtime
              -> core

sdk
  -> core
```

The rule is dependency direction: outer layers may depend on inner layers, but
inner layers must not import outward. `core` is the center. `cmd` is only
process entrypoints.

More detail:

```text
                 +-------------------------+
                 | cmd                     |
                 | executable glue         |
                 +------------+------------+
                              |
                              v
                 +-------------------------+
                 | apps                    |
                 | assembled products      |
                 +------+----------+-------+
                        |          |
                        v          v
        +-------------------+   +-------------------+
        | plugins           |   | adapters          |
        | optional bundles  |   | IO/protocol edges |
        +---------+---------+   +---------+---------+
                  |                       |
                  +----------+------------+
                             v
                 +-------------------------+
                 | orchestration           |
                 | app/session use cases   |
                 +------------+------------+
                              |
                              v
                 +-------------------------+
                 | runtime                 |
                 | execution/storage impls |
                 +------------+------------+
                              |
                              v
                 +-------------------------+
                 | core                    |
                 | specs/events/contracts  |
                 +-------------------------+

                 +-------------------------+
                 | sdk                     |
                 | authoring helpers only  |
                 +------------+------------+
                              |
                              v
                            core
```

The same model as package responsibilities:

```text
core/
  What is the stable shape of an agent system?

runtime/
  How are core contracts executed or stored?

orchestration/
  Which runtime pieces are combined for a use case?

adapters/
  How does the outside world talk to the runtime?

plugins/
  Which optional first-party capabilities are contributed?

apps/
  What product or runnable distribution was assembled?

cmd/
  How is an assembled product launched as a process?

sdk/
  How do users author inert specs conveniently?
```

## Layers

### `core`

`core` is the domain kernel. It contains value objects, specs, descriptors,
events, refs, names, policies, registries, and small contracts that need to be
visible across the system.

Core concepts include:

```text
agent specs
app specs
channel refs and messages
commands and invocations
context provider specs
conversation transcript events
datasource specs
distribution specs
environment specs
events
LLM request/response shapes
operation specs and semantics
policy descriptors
resources and contribution bundles
session specs
skills
threads
tools
usage records
workflow specs
```

`core` must stay inert. It should not perform IO, execute operations, inspect
the filesystem, create goroutines for hosts, call providers, render terminal
output, or import outer layers.

### `runtime`

`runtime` contains concrete implementations of core contracts that are still
surface-neutral. It knows how to execute or store concepts, but not how a CLI,
HTTP server, Slack channel, or product chooses to expose them.

Runtime concepts include:

```text
LLM agent turn engine
context materialization
conversation projection and replay helpers
datasource registry/runtime access
event codecs and event stores
operation executor and safety envelope
projection runners
skill runtime loading
system boundary interfaces and host implementations
thread stores
usage tracking
```

Runtime may depend on `core`. Larger composition normally belongs in
`orchestration` or `apps`, not in runtime sibling packages.

### `orchestration`

`orchestration` is the use-case layer. It composes core definitions and runtime
implementations into flows such as opening a session, dispatching a command,
resolving plugin contributions, or running a daemon lifecycle.

Orchestration concepts include:

```text
agent factory composition
agent spec filtering for app/session selection
app composition
app resource binding and catalog collection
channel runtime contracts
client/session handles
daemon lifecycle
distribution loading result contracts
event registry assembly
harness channel-to-session boundary
plugin host and contribution resolution
projection coordination
resource cataloging over contribution bundles
session control-plane helpers
session environment/context wiring
session lifecycle and Submit handling
sub-agent supervision
tool projection use cases
workflow execution
```

Adapters should enter orchestration instead of reaching directly into lower
session internals. The important example is the channel path:

```text
channel adapter
  -> orchestration/harness
     -> orchestration/session
        -> orchestration/sessioncontrol + orchestration/sessionenv
        -> runtime agent/context/operation pieces
```

The session package owns one bound thread's execution loop. Supporting
orchestration packages keep high-coupling concerns narrow: `sessioncontrol`
contains stop-condition evaluation, built-in command policy/target helpers,
resource aliases, and LLM-driver control helpers; `sessionenv` assembles the
runtime context, skill, datasource, and sub-agent scope used by session work.

### `adapters`

`adapters` are IO and protocol boundaries. They translate external systems
into core/orchestration requests and translate results/events back out.

Adapter concepts include:

```text
filesystem resource discovery
app config loading
terminal rendering
HTTP/SSE channel transport
HTTP control server
direct in-process channel client
connector auth CLI
distribution CLI/describe/local/remote/run/serve helpers
provider clients and provider wire formats
model catalog bridges
SQL/event persistence backends
browser, command-risk, and HTML conversion adapters
```

Adapters may import `core`, `runtime`, and `orchestration` as needed. They must
not introduce reusable domain concepts that belong in inner layers, and they
must not import `apps`.

### `plugins`

`plugins` are optional first-party capability bundles. They contribute specs,
operations, context providers, channels, datasource providers, or connector
providers through core/orchestration contracts.

Plugin concepts include:

```text
browser operations
coding operation bundle
connector-backed operations
datasource provider bundle
filesystem operations
git and GitLab capabilities
human clarification operation
Jira capabilities
OpenAI connector provider
plan execution operations
shell operations
skills
Slack channel/search/provider capabilities
text operations
web operations
```

Plugin contracts belong in `core` or `orchestration`, not in a concrete plugin
implementation package. Plugins can depend on adapters only when the adapter is
part of that plugin's concrete implementation boundary.

### `apps`

`apps` are assembled products and dogfood applications. They choose defaults,
select plugins, wire concrete runtime pieces, and expose product-level command
trees or distribution bundles.

Current app concepts include:

```text
apps/agentsdk
  product assembly for the agentsdk CLI

apps/coder
  coder distribution bundle and command surface

apps/launch
  local run/serve assembly for distributions

apps/devclient
  development client

apps/archreport
  architecture reporting tool
```

`apps` may import plugins, adapters, orchestration, runtime, and core because
they are assembly points. Reusable domain or runtime concepts should move
inward once their shape is stable.

### `cmd`

`cmd` contains executable entrypoints only. A `cmd/*/main.go` should parse
process-level exit behavior and call an assembled app command.

Example:

```text
cmd/agentsdk
  -> apps/agentsdk.NewCommand()

cmd/coder
  -> apps/coder distribution command
```

`cmd` should not contain feature logic, command implementations, provider
selection, resource loading, or plugin assembly.

### `sdk`

`sdk` is the user-authoring convenience layer. It helps users produce inert
specs and contribution bundles.

`sdk` may depend on `core` only. It must not execute operations, instantiate
providers, inspect the filesystem, open sessions, import runtime/adapters/apps,
or hide side effects behind authoring helpers.

## Common Flows

### Local CLI Run

```text
cmd/agentsdk
  -> apps/agentsdk
     -> apps/launch.NewRunCommand
        -> apps/launch.RunPath
           -> adapters/distribution/local.Load
           -> apps/launch.AttachLocalRuntime
           -> adapters/distribution/cli.Run
              -> orchestration/distribution.Runtime.OpenSession
                 -> orchestration/session
                    -> orchestration/sessioncontrol + orchestration/sessionenv
                    -> runtime/agent + runtime/operation + runtime/context
```

The local run path treats a filesystem path as an ephemeral distribution, gives
it a concrete local runtime, then runs either one-shot input or a REPL through
the generic distribution CLI adapter.

### Remote CLI Run

```text
cmd/agentsdk
  -> apps/agentsdk
     -> adapters/distribution/remote.NewCommand
        -> adapters/distribution/remote.Run
           -> adapters/distribution/remote.ResolveTarget
           -> adapters/httpssechannel.Client
              -> remote daemon channel HTTP/SSE
                 -> orchestration/harness
                    -> orchestration/session
```

The remote path is an adapter concern: it resolves a URL, Unix socket, local
default socket, or app manifest listener into a channel client and then uses
the same logical session handle contract as local clients.

### Daemon Serve

```text
cmd/agentsdk
  -> apps/agentsdk
     -> apps/launch.NewServeCommand
        -> apps/launch.Serve
           -> adapters/appconfig
           -> plugins selected by the app
           -> orchestration/app.Compose
           -> orchestration/daemon
           -> adapters/httpcontrol
           -> adapters/httpssechannel.Server
```

Daemon serve is app assembly plus protocol hosting. The channel HTTP/SSE
surface is kept separate from daemon/control HTTP.

### Connector Auth

```text
cmd/agentsdk
  -> apps/agentsdk
     -> adapters/connectors/cli.NewCommand
        -> plugin registry supplied by apps/agentsdk
        -> codewandler/connectors runtime
        -> credential stores
```

Connector auth is an adapter-level CLI. It does not know which first-party
plugins a product wants; `apps/agentsdk` supplies that registry.

### Plugin Contribution Resolution

```text
resource bundles
  -> orchestration/pluginhost
     -> selected plugins
        -> specs, operations, channels, datasource providers, connector providers
  -> orchestration/app composition
     -> runtime execution pieces
```

The plugin host is the abstract contribution resolver. Concrete plugins remain
optional capability packages.

## Distribution Concepts

The distribution split is:

```text
core/distribution
  inert distribution spec and surface metadata

orchestration/distribution
  loaded distribution and runtime handle contracts

adapters/distribution/*
  CLI, local loading, describe, remote, run/serve helper adapters

apps/launch
  concrete local runtime assembly for run/serve

apps/coder
  a ready-made coder distribution bundle
```

A distribution is a runnable package of defaults, metadata, bundled resources,
and supported surfaces. It is not the same thing as `core/app.Spec`: app specs
are resource-authored configuration inside bundles; distributions are product
or packaging-level descriptions of how those resources can be run, served,
described, or eventually deployed.

## Boundary Rules

Use this checklist when placing new code:

```text
Is it a pure spec, event, ID, ref, descriptor, or policy?
  -> core

Is it a concrete implementation of a core execution/storage contract?
  -> runtime

Is it a use-case flow combining runtime pieces?
  -> orchestration

Is it filesystem, terminal, HTTP, Slack, provider, SQL, browser, shell, or CLI IO?
  -> adapters

Is it an optional first-party capability bundle?
  -> plugins

Is it a product, distribution, default set, or concrete assembly?
  -> apps

Is it only an executable main package?
  -> cmd

Is it authoring sugar for inert specs only?
  -> sdk
```

Avoid these placements:

```text
core importing runtime/adapters/plugins/apps
runtime knowing about CLI, HTTP, Slack, or products
orchestration rendering terminal output
adapters defining reusable domain contracts
plugins acting as global app assembly
apps exporting concepts needed by inner layers
cmd containing feature logic
sdk performing IO or execution
```

## Architecture Checks

The architecture boundary is enforced by tests and reviewed with reports:

```bash
go test ./internal/architecture
go run ./apps/archreport
go run ./apps/archreport -format json
go run ./apps/archreport -format dot
go run ./apps/archreport -format mermaid
task arch:render
```

The hard requirement is zero architecture violations. The numeric score is a
review signal, not a release gate by itself. Fan-out in app assembly packages
is expected; fan-out in inner layers usually deserves review.

As of the current architecture split, the report is expected to remain at or
above 90 with zero violations. Remaining score penalties are intentionally
visible in the report so future work can decide whether a runtime sibling edge
or inner-layer fan-out still warrants extraction.
