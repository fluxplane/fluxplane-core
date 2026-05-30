# Runtime System Retirement Plan

Last reviewed: 2026-05-30.

Status: implemented in this migration; retained as design/audit notes.

## Goal

Delete `github.com/fluxplane/fluxplane-core/runtime/system` as an engine-local
package. Keep primitive host authority in `github.com/fluxplane/fluxplane-system`,
keep workspace-specific behavior in `runtime/workspace`, and move policy wrappers
and plugin-specific helpers to more precise owners.

## Position

Yes: the remaining `runtime/system` package is now mostly a staging area, not a
clean domain. In particular, the current env and process implementations are not
generic host primitives anymore; they are workspace-aware adapters:

- env loading reads workspace `.env` files and root-specific env file sets;
- process execution validates workspace workdirs and merges workspace-scoped env;
- `HostWorkspace` owns root confinement, named roots, scratch dirs, and filesystem
  views;
- memory fixtures implement the workspace contract more than a broad runtime
  system domain.

So the final shape should not be `runtime/system` with fewer files. It should be
no `runtime/system` package at all.

Important nuance: move only workspace-scoped env/process behavior into
`runtime/workspace`. Primitive host process/env/network/filesystem contracts and
implementations should stay in `fluxplane-system` / `hostsystem`, not migrate
back into engine runtime packages.

## Current Contents and Target Owners

| Current file | Current role | Target owner |
| --- | --- | --- |
| `runtime/system/workspace.go` | host-backed confined workspace, named roots, scratch | `runtime/workspace/host.go` |
| `runtime/system/env.go` | workspace `.env` loading and executable lookup | `runtime/workspace/env.go` |
| `runtime/system/process.go` | workspace-aware process manager over `hostsystem` | `runtime/workspace/process.go` |
| `runtime/system/memory.go` | in-memory workspace/system fixture | `runtime/workspace/memory.go` plus tiny test helper if still needed |
| `runtime/system/authorization.go` | policy wrappers for system/workspace/network/process/env | `orchestration/security` or new `runtime/policysystem` package |
| `runtime/system/network.go` | public/private host network implementation | preferably `fluxplane-system/hostsystem`; temporary `runtime/httptransport` if engine-only |
| `runtime/system/system.go` | aggregate assembly (`NewHost`, config) | app/bootstrap-owned assembly, likely `apps/launch` or `orchestration/sessionenv` |
| `runtime/system/piper_tts*.go` | human/TTS helper | `plugins/native/human` or a dedicated human adapter |
| `runtime/system/*_test.go` | tests | move with implementation |

## Desired End State

```text
runtime/workspace
  Workspace interface and resolver/manager declarations
  HostWorkspace implementation
  WorkspaceEnvironment and env file parsing
  WorkspaceProcessManager wrapper
  MemoryWorkspace fixture

fluxplane-system / hostsystem
  Primitive System contracts
  Host filesystem/network/process/environment/clock implementations
  Reusable memory/test unsupported helpers where applicable

orchestration/security, or runtime/policysystem
  Authorization wrappers that decorate fluxplane-system boundaries and
  runtime/workspace.Workspace with policy enforcement

apps/launch / orchestration/sessionenv
  Host runtime assembly: workspace + system boundaries + policy wrappers + plugins

plugins/native/human
  Piper TTS helper, if still needed
```

After migration, `runtime/system` should not exist and should not appear in
architecture exceptions.

## Migration Strategy

### Phase 0: Freeze imports

Add/adjust an architecture rule or CI grep so no new non-test imports of
`runtime/system` are introduced during the migration.

Acceptance:

- `grep -R "github.com/fluxplane/fluxplane-core/runtime/system" --include='*.go'`
  has only the files being actively migrated.

### Phase 1: Move workspace implementation first

Move `HostWorkspace`, workspace config types, root access types, scratch dirs,
and related tests from `runtime/system` to `runtime/workspace`.

Suggested API:

```go
workspace.NewHost(workspace.HostConfig{...}) (*workspace.HostWorkspace, error)
workspace.HostConfig
workspace.RootConfig
workspace.AccessReadOnly / workspace.AccessReadWrite
```

Keep compatibility aliases only if needed for one short commit; remove them before
claiming completion.

Acceptance:

- `runtime/workspace` owns all host workspace code and tests.
- No package imports `runtime/system` only for workspace config or workspace
  construction.

### Phase 2: Move workspace environment

Move `.env` loading, `WorkspaceEnvironment`, and executable resolution into
`runtime/workspace`.

Suggested API:

```go
workspace.NewEnvironment(ws *workspace.HostWorkspace) (*workspace.Environment, error)
workspace.LoadEnvFiles(...)
```

Acceptance:

- Env file behavior tests live under `runtime/workspace`.
- Process code depends on `runtime/workspace.Environment`, not on
  `runtime/system`.

### Phase 3: Move workspace process wrapper

Move only the workspace-aware process wrapper into `runtime/workspace`. It should
wrap `hostsystem.NewProcessManager` from `fluxplane-system`, validate workdirs
through `Workspace.ResolveCreate/ResolveExisting`, and merge workspace env.

Suggested API:

```go
workspace.NewProcessManager(ws *workspace.HostWorkspace, env *workspace.Environment) fpsystem.ProcessManager
```

Acceptance:

- `plugins/native/shell` and Go/Markdown language plugins can use process
  boundaries without importing `runtime/system`.
- Process tests move to `runtime/workspace`.

### Phase 4: Move memory fixture

Move `MemoryWorkspace` into `runtime/workspace`. Prefer tests to depend on the
narrowest thing they need:

- workspace tests use `workspace.NewMemory()` or `workspace.NewMemoryWorkspace()`;
- plugin tests that need a full `fpsystem.System` use a tiny fixture constructor
  near plugin test support or from `fluxplane-system/systemtest` if it grows the
  needed behavior.

Acceptance:

- No `runtime/system.NewMemory()` callers remain.
- No `runtime/system` import remains for test fixtures.

### Phase 5: Retire engine-local network implementation

Move `HostNetwork` to `fluxplane-system/hostsystem` if it is generally reusable.
If the public/private URL semantics are engine policy rather than primitive host
networking, split them:

- primitive dialer/resolver in `hostsystem`;
- URL/public transport policy in `runtime/httptransport` or security layer.

Acceptance:

- Launch assembly obtains network from `hostsystem` or a precise transport
  package.
- `runtime/system/network.go` is gone.

### Phase 6: Move authorization wrappers

Move `AuthConfig`, `WithAuthorization`, `WorkspaceWithAuthorization`,
`NetworkURLAuthorizer`, and `Authorize` out of `runtime/system`.

Preferred owner: `orchestration/security`, because these wrappers enforce engine
policy around runtime/session execution rather than implement host IO.

Alternative owner: `runtime/policysystem`, if we want a reusable runtime-level
package with no orchestration dependency.

Acceptance:

- Launch/session assembly wraps boundaries with the new package.
- Authorization tests move with the wrappers.
- No policy concepts remain in workspace or hostsystem packages.

### Phase 7: Move Piper TTS

Move `SpeakPiperBackground` into `plugins/native/human` or a dedicated human
adapter package. It is not a system boundary.

Acceptance:

- Only human plugin/adapters know about Piper.
- `runtime/system/piper_tts*.go` is gone.

### Phase 8: Delete aggregate `NewHost`

Replace `runtime/system.NewHost` with explicit assembly in the bootstrap layer:

```go
ws := workspace.NewHost(...)
env := workspace.NewEnvironment(ws)
proc := workspace.NewProcessManager(ws, env)
net := hostsystem.NewNetwork(...)
base := systemkit.NewSystem(...)
secured := security.WithAuthorization(base, ...)
```

The exact builder can live in `apps/launch` or `orchestration/sessionenv`, but it
should be named as assembly, not as a reusable runtime domain.

Acceptance:

- `runtime/system/system.go` is gone.
- `runtime/system` directory is deleted.
- `go list ./runtime/...` contains no `runtime/system` package.
- Architecture exceptions no longer mention `runtime/system`.

## Validation Checklist

Run after each phase:

```sh
go test ./runtime/workspace ./plugins/native/shell ./plugins/native/workspace ./plugins/languages/golang ./plugins/languages/markdown
```

Run at completion:

```sh
go test ./...
grep -R "github.com/fluxplane/fluxplane-core/runtime/system" --include='*.go' .
grep -R "package system" runtime --include='*.go'
go list ./runtime/...
```

Expected completion evidence:

- full test suite passes;
- no imports of `runtime/system`;
- no `runtime/system` directory;
- no architecture exception for `runtime/system`;
- workspace, security, hostsystem, and plugin/human ownership boundaries are
  explicit in file/package names.

## Risks and Mitigations

- **Circular imports:** `runtime/workspace` must not import orchestration or plugin
  packages. Keep authorization wrappers outside workspace.
- **Overloading workspace:** only workspace-scoped env/process wrappers belong
  there. Primitive host implementations belong in `fluxplane-system`.
- **Large rename blast radius:** migrate one owner at a time and keep each commit
  testable.
- **Compatibility aliases lingering:** allow them only inside an intermediate
  commit; final acceptance requires no `runtime/system` imports.
