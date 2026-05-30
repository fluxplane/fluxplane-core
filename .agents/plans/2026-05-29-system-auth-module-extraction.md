# System Module Extraction Plan

Last reviewed: 2026-05-30.

## Goal

Start the Fluxplane module split with `fluxplane-system`, the reusable host/IO
authority currently embedded in engine `runtime/system`.

The extraction should let the engine and dex share host authority contracts and
local host implementations without making dex depend on the engine. Dex must
remain useful as an integration gateway and standalone CLI for Fluxplane,
Codex, Claude, and direct shell use.

Auth is deliberately out of scope for this plan. It may become a later module,
but the current work is system only.

## Current Problem

`fluxplane-core` is named like a minimal kernel, but in practice it is the
engine and harness: sessions, orchestration, runtime execution, adapters,
plugins, apps, and CLI entrypoints.

`fluxplane-dex` has become the integration gateway. It owns marketplace
plugins, standalone CLI behavior, plugin protocol, host capability access, and
provider-specific implementations.

The awkward boundary today:

- Dex has its own host capability access for plugin subprocesses and standalone
  CLI behavior.
- Engine has overlapping host capability access under `runtime/system`.
- The `fluxplane-dex/fluxplaneplugin` bridge imports both dex and engine and
  maps concepts between them.
- Engine `runtime/system` is the most reusable IO boundary, but it lives inside
  the engine, so dex either duplicates local host access or requires a bridge.

The desired outcome for this plan is one canonical vocabulary for host
authority while preserving separate runtime ownership.

## Target Module Graph

Initial target:

```text
fluxplane-system
  no dependency on engine or dex

fluxplane-browser, later
  depends on fluxplane-system
  owns browser automation contracts and implementations

fluxplane-dex
  depends on fluxplane-system
  remains standalone CLI and integration gateway

fluxplane-core
  remains the engine for now
  depends on fluxplane-system

fluxplane-engine-dex bridge, future name TBD
  depends on engine + dex + shared system module
  owns mapping from dex marketplace/plugins into engine resource/pluginhost shapes
```

Hard dependency rule:

```text
system <- {dex, engine}
system <- browser
bridge -> {dex, engine}
```

Dex must not import engine. Engine should not import dex except through a
dedicated bridge package/module.

## Proposed Repositories and Module Names

Start with separate repositories/modules:

```text
github.com/fluxplane/fluxplane-system
```

Keep the current engine repository/module name unchanged during the first
extraction unless a separate rename is explicitly planned. The rename from
`fluxplane-core` to `fluxplane-engine` is desirable, but it is a separate
blast radius and should not be mixed into the system split.

Avoid names like `fluxplane-contracts` or `fluxplane-common`. They are too
abstract and invite dumping unrelated shapes into one package.

## `fluxplane-system` Scope

`fluxplane-system` owns primitive host authority and side-effect boundaries.

Initial packages:

```text
system       root package: primitive contracts and generic fs helpers
hostsystem   local host implementation: filesystem, network, process, env
memsystem    reusable deterministic in-memory system implementation
mountfs      wrapper layer with a virtual root and named mounted roots
systemkit    ergonomic facade, HTTP helpers, and assembly builder
systemtest   test fakes/helpers only
hostinfo     optional derived host metadata helper, not part of System
```

Actual first-slice shape:

- `System` aggregate interface.
- `FileSystem`, based on `io/fs` read interfaces plus explicit write, mkdir,
  remove, and rename interfaces. It is not a workspace sandbox.
- Generic filesystem helper functions for bounded reads, line reads, walks,
  and globs.
- `mountfs` wrapper package for applying a workspace-like mount table around
  any `system.FileSystem`. It exposes named roots with `@name/path` syntax and
  enforces coarse read-only/read-write access without adding cwd, sessions,
  operation approval, overlays, or engine workspace policy.
- `mountfs` has a virtual root at `.`. Listing `.` shows visible entries from
  the primary root, when configured, plus synthetic named-root mount points like
  `@docs`. It never lists the wrapped filesystem's raw parent tree, and root
  paths such as `../folder-b` or `../../..` are rejected instead of escaping the
  wrapped base filesystem.
- `Network`, limited to primitive dialing and DNS resolution:
  `DialContext` plus `Resolver`.
- HTTP request/response helpers live in `systemkit`, not on `Network`.
- `ProcessManager`, process start/stream/cleanup types, managed background
  process handles.
- `Environment`, executable resolver, and `Clock`.
- Host-backed local implementation in `hostsystem`.
- `hostsystem.FileSystem` treats its root as a filesystem confinement boundary
  for file operations. It resolves symlinks on reads and on create parents so
  symlinked paths cannot escape the configured root. Process execution remains
  normal host execution and is not a sandbox.

Keep root package `system` flat for primitive contracts and generic
capability helpers. Do not put host implementations, HTTP conveniences, policy,
workspace sandboxing, browser automation, or human clarification in root.

Filesystem mounting/scope is a wrapper, not a top-level `System` concept.
Compose it where a product or session chooses its authority:

```go
base := hostsystem.NewFileSystem(root)
mounted, err := mountfs.New(base, mountfs.Spec{
  Roots: []mountfs.Root{
    {Name: "", Path: ".", Access: mountfs.ReadWrite},
    {Name: "docs", Path: "docs", Access: mountfs.ReadOnly},
    {Name: "tmp", Path: ".tmp", Access: mountfs.ReadWrite, Scratch: true},
  },
})
```

```go
import "github.com/fluxplane/fluxplane-system"
```

Keep implementations out of the root package:

```go
import "github.com/fluxplane/fluxplane-system/hostsystem"
```

Expected local host usage:

```go
sys, err := hostsystem.New(hostsystem.Config{Root: root})
var _ system.System = sys
```

Expected ergonomic usage:

```go
sys, err := systemkit.NewSystem().
  WithHostFileSystem(root).
  WithHostNetwork().
  WithHostEnvironment().
  WithRealClock().
  WithHostProcess(root).
  Build()
resp, err := sys.DoHTTP(ctx, systemkit.HTTPRequest{URL: "https://example.com"})
```

Expected mounted filesystem usage:

```go
sys, err := systemkit.NewSystem().
  WithHostFileSystem(root).
  WithMountedFileSystem(mountfs.Spec{
    Roots: []mountfs.Root{
      {Name: "workspace", Path: ".", Access: mountfs.ReadWrite},
      {Name: "docs", Path: "docs", Access: mountfs.ReadOnly},
    },
  }).
  WithoutNetwork().
  Build()
```

Design constraints:

- Prefer small capability interfaces over requiring the full aggregate
  `System` everywhere.
- Root package `system` must stay IO-free.
- Package `hostsystem` may perform host IO because that is its purpose.
- Policy hooks must be explicit; callers should be able to wrap or deny
  filesystem, network, process, and env access.
- Keep the managed process boundary intact: stdout/stderr streaming,
  background handles, and per-session cleanup must survive the extraction.
- Keep engine operation safety envelopes outside this module unless a minimal
  reusable approval/policy contract is proven necessary.
- Do not move Piper/TTS or desktop notification helpers into
  `fluxplane-system`.
- Do not add raw syscall APIs. Syscalls remain implementation details behind
  typed capabilities.
- Do not add signal APIs yet. Sending signals belongs on a future
  `ProcessManager.Signal`; receiving host signals should be a separate
  `SignalSource` only when engine or dex needs it.

## Browser Decision

Browser automation should not be part of `fluxplane-system`.

Reasoning:

- Browser automation is side-effecting host IO, but it is not a primitive host
  capability like workspace, network, process, environment, or clarification.
- Browser automation has its own lifecycle, dependency weight, security
  posture, and adapter choices.
- The base system module should remain small enough for dex and other tools to
  import without pulling browser concepts or browser dependencies.

Future target:

```text
github.com/fluxplane/fluxplane-browser
  browser       interfaces and inert request/result structs
  cdpbrowser    chromedp implementation
  browsertest   fake browser manager
```

Dependency direction:

```text
fluxplane-system <- fluxplane-browser
```

The engine should compose browser separately from system instead of requiring
browser on the `system.System` aggregate.

## Phase 1: Inventory and Type Map

Create a type map before moving code.

Engine sources to inspect:

- `runtime/system`
- `adapters/system`
- packages that pass `runtime/system.System`

Dex sources to inspect:

- `runtime/capabilities.go`
- `fluxplaneplugin/system_host.go`

Output:

- A table of duplicate or near-duplicate types.
- A decision for each type: move, rename, adapt, leave engine-owned, or leave
  dex-owned.
- A list of import cycles that must not be introduced.

### Engine Inventory Result

Reviewed on 2026-05-30:

- `runtime/system` is not only primitive system access. It currently combines
  engine workspace semantics, host implementations, HTTP convenience, browser
  contracts, human clarification, Piper/TTS helpers, process management,
  environment handling, private-network HTTP guards, and engine policy
  authorization wrappers.
- `adapters/system/browsercdp` implements `runtime/system.BrowserManager` and
  writes browser artifacts through `runtime/system.Workspace`.
- `runtime/systemtest` contains an in-memory `System` and `Workspace` used by
  workspace-heavy tests. This should be replaced or reduced only after an
  engine workspace compatibility wrapper exists over `fluxplane-system`.
- Many plugin packages import `runtime/system` directly. The highest-use
  surfaces are `Workspace()`, `Network().DoHTTP`, `Process()`,
  `Environment()`, `Browser()`, and `Clarifier()`.

Type ownership map:

| Current engine type | Target owner | Decision |
|---|---|---|
| `System` aggregate | engine compatibility layer first | Keep `runtime/system.System` temporarily because plugins expect `Workspace`, `Browser`, and `Clarifier`; later split to smaller dependencies. |
| `Workspace`, `ResolvedPath`, `ScratchDir` | engine | Keep engine-owned. It carries engine semantics: absolute path evidence, copy/move helpers, scratch dirs, env-file roots, and policy resource paths. It can be backed by `fluxplane-system.FileSystem`/`mountfs`, but it is not the same abstraction. |
| `WorkspaceConfig`, `WorkspaceRootConfig`, `WorkspaceAccess` | engine launch/runtime | Keep engine-owned while app manifest and distribution config still speak workspace roots. Add mapping to `mountfs.Spec` when the host workspace is refactored. |
| `HostWorkspace` | engine for now | Refactor later to wrap `hostsystem.FileSystem` plus `mountfs`; do not move to `fluxplane-system` because it exposes absolute paths and engine helpers. |
| `WalkOptions`, `WalkEntry`, `GlobOptions` | split | Primitive versions already exist in `fluxplane-system`; engine `Workspace` keeps resolved-path wrappers until plugin APIs migrate. |
| `Network`, `HTTPRequest`, `HTTPResponse`, `HostNetwork` | split | Primitive dial/DNS and HTTP convenience live in `fluxplane-system`/`systemkit`. Engine keeps `Network().DoHTTP` compatibility initially, implemented by adapting to `systemkit` plus engine authorization/private-target policy. |
| `PublicNetworkTransport`, `ValidatePublicURL` | engine policy/adapters for now | Do not move as-is. The private-network guard is product policy, not primitive network. Later expose as an engine network wrapper over `fluxplane-system.Network`. |
| `ProcessManager`, process request/result/event types | `fluxplane-system` | Already represented in `fluxplane-system`. Engine should alias or adapt these types and map event names in engine code where needed. |
| `HostProcess` | `fluxplane-system/hostsystem` plus engine wrapper | The primitive managed process implementation has moved. Engine still needs workspace/env-file workdir behavior, so create a wrapper before deleting engine `HostProcess`. |
| `Environment`, `ExecutableResolver` | `fluxplane-system` | Already represented. Engine-specific env-file loading and process env construction remain engine-owned. |
| `WorkspaceEnvironment`, env-file parsing | engine | Keep engine-owned because it depends on workspace roots/env file semantics. It may later be a wrapper around `system.Environment`. |
| `BrowserManager` and browser request/result structs | future `fluxplane-browser` or engine until split | Do not add to `fluxplane-system`. Keep in engine during system migration; move with `adapters/system/browsercdp` later. |
| `Clarifier` | engine/plugin human boundary | Do not add to `fluxplane-system`; keep engine-owned until a separate human/interaction boundary is designed. |
| `WithAuthorization` wrappers | engine policy runtime | Keep engine-owned. They import `core/policy` and emit engine events, so they must not move to `fluxplane-system`. |
| `SpeakPiperBackground` | separate audio/TTS adapter or removal | Do not move to `fluxplane-system`. |

Core integration strategy:

1. Add `fluxplane-system` as a module dependency in `fluxplane-core`.
2. Keep `runtime/system` as a compatibility package while the import surface is
   large. Do not create stale compatibility for external users; this is an
   internal migration step inside the rewrite.
3. Replace duplicate primitive type definitions in `runtime/system` with type
   aliases only where safe:
   `ProcessRequest`, `ProcessResult`, `ProcessInfo`, `ProcessOutput`,
   `ProcessEvent`, `Environment`, and `ExecutableResolver`.
   Process events now implement event naming in `fluxplane-system` itself via
   `fluxplane-event`, so the engine event registry and terminal renderer can
   register/render `fluxplane-system.ProcessEvent` directly.
4. Replace engine host primitive implementations with wrappers around
   `hostsystem` incrementally:
   environment first, then network, then process.
5. Leave `Workspace` in engine, but introduce an internal implementation path
   based on `hostsystem.FileSystem` plus `mountfs` so the same mounted root
   semantics are used by engine and dex.
6. After plugin code no longer needs engine `runtime/system.System`, migrate
   plugins to accept smaller capabilities: workspace filesystem, network HTTP,
   process manager, environment, browser, or clarifier as needed.

First core implementation slice:

- Add `github.com/fluxplane/fluxplane-system` to `fluxplane-core/go.mod`.
- Add `github.com/fluxplane/fluxplane-event` as the reusable event package so
  shared process events can be emitted without depending on the engine.
- Alias process and environment primitives in engine `runtime/system` to
  `fluxplane-system` while workspace, browser, clarification, and engine
  authorization wrappers remain engine-owned.
- Add a guarded HTTP/network adapter from engine `Network().DoHTTP` to
  `fluxplane-system/systemkit` only after private-network policy tests are
  mapped.
- Run focused tests for `runtime/system`, `plugins/native/shell`, and
  `plugins/integrations/web` before broadening.

Status on 2026-05-30:

- Created `github.com/fluxplane/fluxplane-event` as a sibling module and moved
  generic event contracts/codecs plus sensitivity labels out of engine `core`.
- Engine imports now use `fluxplane-event` instead of
  `github.com/fluxplane/fluxplane-core/core/event`.
- Engine authorization decision events moved to `core/policy` because they are
  policy-domain events, not generic event module concepts.
- `fluxplane-system.ProcessEvent` now implements `fluxplane-event.Event`.
- Engine `runtime/system` process and environment primitive names are aliases
  to `fluxplane-system` types. This avoids duplicate concept definitions while
  leaving engine workspace/browser/clarifier surfaces in place.
- `orchestration/eventregistry`, terminal rendering, and shell event tests now
  use `fluxplane-system.ProcessEvent` directly.
- Engine `runtime/system.Network` is now an alias of
  `fluxplane-system.Network`. HTTP request helpers moved out of the engine
  network interface and into `fluxplane-system/systemkit`.
- Engine `HostNetwork` is now a guarded primitive dialer/resolver. Private
  target blocking remains engine-owned for now through `ValidatePublicURL`,
  `PublicNetworkTransport`, and guarded `DialContext`.
- Network-heavy plugins and integrations now call `systemkit.DoHTTP`,
  `systemkit.NewHTTPClient`, or `systemkit.NewRoundTripper` instead of
  `Network().DoHTTP` or engine HTTP-client helpers.
- Engine `runtime/system.HostProcess` no longer owns managed process
  execution. It is now a thin workspace/env wrapper over
  `fluxplane-system/hostsystem.Process`.
- `hostsystem.Process` accepts an optional `hostsystem.ProcessEnvironment`
  provider so engine workspace env files can supply a complete process
  environment without reintroducing process execution code in the engine.
- Engine `runtime/system.HostWorkspace` now builds on
  `fluxplane-system/hostsystem.FileSystem` plus `mountfs` for named-root
  resolution, read/write access enforcement, and generic bounded read, line
  read, walk, and glob helpers. Engine workspace remains engine-owned because
  it exposes absolute path evidence, scratch dirs, env-file roots, and policy
  resource paths.
- Engine workspace keeps the old semantic meaning of `.` as the primary
  workspace root. The broader `mountfs` virtual-root behavior remains available
  in `fluxplane-system`, but is not surfaced through engine workspace `ReadDir`,
  `Walk`, or project discovery because that would change existing root
  discovery semantics.
- Local `go.work` currently contains version-specific replaces for unpublished
  `v0.0.0` sibling modules. These must not become committed module-level
  replaces; once `fluxplane-event` and `fluxplane-system` are tagged, dependent
  `go.mod` files should use real versions and the workspace replaces can be
  removed.

## Phase 2: Extract `fluxplane-system`

Work:

- Create the `github.com/fluxplane/fluxplane-system` module.
- Move capability interfaces first, with minimal implementation in the root
  package.
- Move local host implementations into package `hostsystem`, not root
  `system`.
- Preserve the engine runtime behavior by adapting engine imports to the new
  module.
- Add test fakes for each major capability.
- Port host-backed implementation from engine `runtime/system` only after the
  interface split is stable.
- Update dex to accept a `fluxplane-system` host boundary where it currently
  accepts or implements local capability access.

Acceptance checks:

- Engine focused tests for packages that use `runtime/system` pass.
- Dex focused tests for local capability host and plugin runtime pass.
- No dex import of engine.
- No committed `replace` directives.

## Phase 3: Engine Compatibility Layer

Work:

- Decide whether engine `runtime/system` becomes a short-lived alias package or
  whether imports are rewritten directly.
- If using aliases, keep them temporary and avoid adding new behavior there.
- Move engine authorization wrappers to an engine-owned package that wraps
  `system.System` using engine policy concepts.
- Map plain system process events into engine event names inside engine
  event-registry code, not inside `fluxplane-system`.

Acceptance checks:

- Existing engine behavior remains intact.
- Engine policy and authorization still gate side effects.
- `fluxplane-system` has no engine imports.
- No committed `replace` directives.

## Phase 4: Bridge Cleanup

After system is shared, simplify the dex-engine bridge.

Work:

- Reduce `fluxplane-dex/fluxplaneplugin/system_host.go` mapping code.
- Decide whether the bridge should stay under dex as `fluxplaneplugin` or move
  to a clearer module such as `github.com/fluxplane/fluxplane-engine-dex`.
- Keep the bridge as the only package that imports both dex and engine.
- Do not let bridge convenience APIs leak back into the shared system module.

Acceptance checks:

- Dex remains usable without engine.
- Engine can assemble dex marketplace plugins through the bridge.
- Bridge code contains mappings for engine resource/pluginhost shapes, not
  generic system concepts.

## Multi-Module Development Workflow

Use Go workspaces for local development. Do not use committed `replace`
directives as the normal workflow.

Recommended local checkout:

```text
/home/timo/projects/fluxplane/
  fluxplane-core/
  fluxplane-dex/
  fluxplane-system/
```

Create a parent workspace locally:

```sh
cd /home/timo/projects/fluxplane
go work init ./fluxplane-core ./fluxplane-dex ./fluxplane-system
```

When adding another local module:

```sh
go work use ./new-module
```

Keep `go.work` out of individual module repositories. If a checked-in
workspace is wanted, put it in a dedicated meta checkout repository, not inside
one product/module repo. That avoids making one repo's tests depend on sibling
paths that do not exist for ordinary users or CI.

Use two verification modes:

```sh
# Local integration mode: uses sibling checkouts from go.work.
go test ./...

# Release/dependency mode: ignores go.work and proves go.mod versions are real.
GOWORK=off go test ./...
```

Before tagging or preparing a release, run release/dependency mode for each
module that will be published.

## Versioning and Release Management

Rules:

- Every module imports released versions of the other modules in committed
  `go.mod` files.
- Local unpublished changes are tested through `go.work`, not `replace`.
- CI for each module should run with `GOWORK=off`.
- A separate integration CI job may use a workspace checkout of all Fluxplane
  modules, but that is an additional signal, not a substitute for module CI.
- Avoid cycles at the module level. Cycles are more painful than package-level
  cycles because they block releases.

Suggested release order for this split:

```text
fluxplane-system
fluxplane-dex
fluxplane-core
bridge module, if separated
```

When a change spans modules:

1. Land and tag the lower-level module first.
2. Update dependent modules with `go get github.com/fluxplane/<module>@vX.Y.Z`.
3. Verify with `GOWORK=off`.
4. Tag the dependent module.

For rapid pre-1.0 development, use small tags often. It is better to have many
boring `v0.x.y` tags than a web of local `replace` directives that nobody can
reproduce.

## What Not To Do Yet

- Do not split datasource, endpoint, and operation into separate modules in the
  first step.
- Do not split auth in this plan.
- Do not rename `fluxplane-core` to `fluxplane-engine` in the same change.
- Do not make `fluxplane-system` depend on auth, dex, or engine.
- Do not put browser automation, Piper/TTS, or desktop notification helpers in
  `fluxplane-system`.
- Do not make root package `system` contain local host implementations.

## Open Questions

- Should the bridge become `github.com/fluxplane/fluxplane-engine-dex`, or
  should it remain under dex with a clearer package name?
- Should `fluxplane-system` include a reusable safety approval interface, or
  should operation safety remain engine-owned for now?
- Should browser automation become `github.com/fluxplane/fluxplane-browser`
  immediately after system, or wait until the engine import rewrite is stable?
