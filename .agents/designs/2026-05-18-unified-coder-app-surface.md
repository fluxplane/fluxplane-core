# Design: unified coder app surface

## Status

Implemented for the agentsdk-parity, explicit resource-run, and REPL `/run`
milestones. Remaining backlog is tracked below for explicit `.coder.yaml`
resource imports and app deploy/install/test actions.

## Summary

`coder` becomes the primary Fluxplane Agent Runtime interface for both local
coding work and building/running agentruntime apps. The current split between
`agentsdk` and `coder` has duplicated app entry behavior such as describe,
discover, serve, run, build, and model selection. The refactor should make
`coder` the native product surface while keeping reusable distribution,
appconfig, launch, and adapter packages available for Go embedding and custom
apps.

The target model is:

- `coder` opens the interactive coder REPL.
- Coder commands follow an area/action grammar:
  `coder <area> <action>` or `coder <area> <sub-area> <action>`.
- `coder app run [path]` runs the agentruntime app facet in a workspace.
- `coder workflow run <id>`, `coder agent run <name>`, and
  `coder op run <name>` run explicit resources.
- `coder app build`, `coder app deploy`, `coder app install`, and
  `coder app test` are coder-native app actions backed by shared
  runtime/distribution code.
- `coder config show|edit` manages coder configuration, while
  `coder app config show|edit` manages the current app manifest/config.
- `coder shell` starts a separate AI-native shell/TUI surface; it has its own
  design file and is not part of the first agentsdk-parity milestone.
- `.coder.yaml` configures the local coder app.
- `agentsdk.app.yaml` remains the app manifest/package file for agentruntime
  apps built with coder.

The `agentsdk` binary has been removed. Product behavior should land in coder,
with shared launch/build/runtime helpers kept outside CLI-only packages.

## Goals

- Make coder the single main CLI and programmatic interface for coding,
  app-building, app-running, serving, packaging, installation, and deployment.
- Remove product-level duplication by making `apps/coder` the product assembly.
- Keep reusable libraries reusable: app loading, serving, distribution build,
  model resolution, auth, datasource indexing, and local runtime assembly remain
  outside CLI-only packages.
- Treat repository resources as inventory facets by default, not as automatic
  extensions of the coder session.
- Preserve closed-by-default coder behavior: project app tools, agents,
  workflows, and skills are visible and runnable by explicit target, but are not
  automatically imported into coder.
- Provide `coderapp.New(coderapp.Config)` as the stable Go interface for native
  coder behavior.

## Non-goals

- Do not rename `agentsdk.app.yaml` in this refactor. The manifest remains the
  app/package file, even when authored and run through coder.
- Do not auto-load an app facet's resources into the coder REPL just because the
  workspace contains `agentsdk.app.yaml`.
- Do not collapse app manifests and coder configuration into one file.
- Do not move low-level distribution or local runtime assembly into `cmd/coder`.
- Do not preserve old `agentsdk` command shapes as compatibility wrappers. This
  is a pre-1.0 rewrite; remove stale surfaces when coder owns the capability.

## Current State

`cmd/coder` is already thin and calls `apps/coder.NewCommand()`. That command is
built from a distribution command and adds coder-specific `discover` and
`serve`.

The old agentsdk entrypoint is gone. Any remaining duplicate product package
code is stale and should be deleted rather than kept as a wrapper.

Shared pieces already exist:

- `adapters/appconfig` loads `agentsdk.app.yaml` resources and launch metadata.
- `adapters/distribution/cli` adapts a distribution into a REPL/one-shot CLI.
- `adapters/distribution/describe`, `local`, `remote`, `run`, `serve`, and
  `deploy` provide reusable distribution behaviors.
- `apps/launch` composes local runtimes, run paths, serve paths, plugin
  registries, connector auth, datasource indexing, and model resolution.
- `runtime/project` already emits project facets for common manifests and
  resource directories, but does not yet model agentruntime app manifests,
  `.coder.yaml`, or AI configuration surfaces such as `AGENTS.md`, `CLAUDE.md`,
  `MEMORY.md`, and `.claude/agents/*.md` as first-class facets.

The previous configurable coder bundle design remains useful for app resource
construction, but this design moves the larger product API above it.

## Product Model

### Coder App

The coder app is the local coding/editor product. It has built-in tools, skills,
commands, workflows, prompts, model defaults, session policies, and workspace
behavior. It can inspect and interact with any software project, including
projects that contain agentruntime app manifests.

Coder is a closed system by default:

- Built-in coder resources are loaded into the coder session.
- Workspace resources are discovered as inventory/facet signals.
- Project app resources are not loaded into the coder session unless
  `.coder.yaml` opts in.
- Running a project app is explicit through `coder app run` or `/run`.

### App Facet

An app facet is a detected agentruntime app manifest in the current workspace,
usually `agentsdk.app.yaml`. It describes the app being built. Coder can run,
serve, test, build, install, deploy, describe, or inspect that app, but the app
resources remain scoped to the app target unless explicitly imported into coder.

### Coder Configuration

`.coder.yaml` configures the coder product in the current workspace. It is not a
distribution manifest. It may declare:

- additional workspace roots;
- env files for coder's local runtime;
- resource imports into the coder session;
- preferred target app facet;
- model/provider defaults for coder;
- command aliases or run target defaults;
- opt-in loading of app facet agents, skills, workflows, or operations into
  coder.

The first implementation should keep the schema small and additive. The minimum
useful shape is:

```yaml
version: 1
workspace:
  roots:
    - name: runtime
      path: ../agentruntime
      access: read_write
  env_files:
    - .env.local
app:
  default: .
coder:
  model: codex/gpt-5.5
  imports:
    agents: []
    skills: []
    workflows: []
    operations: []
```

Empty import lists mean no project app resources are imported into the coder
session. Later schema versions can add presets such as `imports: {app: true}`,
but the default remains no import.

### App Manifest

`agentsdk.app.yaml` remains the build/package/manifest file for an
agentruntime app. It continues to be decoded by `adapters/appconfig` and
materialized through distribution/local runtime code. It should not contain
coder-product preferences.

## CLI Design

### Default Invocation

```bash
coder
```

Opens the coder REPL, as today.

```bash
coder --input "Summarize this repo"
coder --goal "Finish the focused refactor"
```

Continue to run a prompt or goal in the coder app itself.

### Command Grammar

Coder commands should read as area/action, not as one global verb with many
target types. The grammar is:

```text
coder <area> <action>
coder <area> <sub-area> <action>
```

Examples:

```bash
coder serve
coder app run .
coder app build .
coder app deploy .
coder app config show
coder app config edit
coder workflow run release
coder agent run planner
coder op run echo --arg text=hello
coder config show
coder config edit
coder shell
```

First-milestone commands are the ones needed to cover current `agentsdk`
behavior through coder. The broader area/action list is backlog and naming
guidance, not a requirement to implement every command in the first slice.

### App Actions

Top-level `coder serve` is intentionally not an app action. It serves the coder
app itself. `coder app serve [path]` serves the selected agentruntime app facet.

`coder app` owns agentruntime app lifecycle actions:

```bash
coder app run [path]
coder app serve [path]
coder app build [path]
coder app deploy [path]
coder app install [path]
coder app test [path]
coder app config show [path]
coder app config edit [path]
```

First-milestone app actions:

- `coder app run [path]` is the local app run path.
- `coder app serve [path]` is the app daemon path.
- `coder app build [path]` is the app build path.
- `coder app config show [path]` should expose the loaded app manifest and
  distribution/runtime summary.

Backlog app actions:

- `coder app deploy [path]`.
- `coder app install [path]`.
- `coder app test [path]`.
- `coder app config edit [path]`.

Target behavior:

- `run` loads the selected app facet and opens its default app session.
- `serve` serves the selected app facet through the daemon/channel stack.
- `build`, `deploy`, `install`, and `test` use distribution/app adapters rather
  than shell snippets.
- `config` reads or edits the app manifest/config, not `.coder.yaml`.

Scope selection:

- If `--app` is set, target that app manifest/path.
- If no `--app` is set and the command target needs app scope, use the nearest
  app facet in the current workspace.
- If no app facet is available for an app-scoped target, fail with a message
  pointing to app initialization.
- If the target is explicitly a coder-scoped resource, run against the coder app.

### Resource Actions

Resource actions use the same area/action grammar:

```bash
coder workflow run <id> [--app <path>] [--input "..."]
coder agent run <name> [--app <path>] [--input "..."]
coder op run <name> [--app <path>] [--arg key=value] [--json input.json]
```

Target behavior:

- `workflow run` executes the named workflow in the selected app/coder scope.
- `agent run` opens the selected app/coder scope and runs the named agent.
- `op run` invokes the named operation through the same runtime safety envelope
  used by normal tool execution.

These are backlog after agentsdk parity unless one is needed to implement
`/run` or app smoke testing.

Argument handling for `coder op run`:

- `--arg key=value` may repeat and is decoded into typed operation input fields.
- `--json path` reads a JSON object input.
- Positional values after `--` are passed as target-specific argv only for
  operations that declare argv-style input.
- Invalid or unknown typed fields fail before operation execution.

### REPL `/run`

Inside the coder REPL:

```text
coder> /run
coder> /run app
coder> /run agent planner
coder> /run workflow release
coder> /run op echo --arg text=hello
```

`/run` without arguments means `app run` for the current workspace app facet.
This keeps app execution close to the coding workflow without importing the
app's resources into the coder session.

Slash-command aliases may accept the old target-first shorthand because they are
REPL ergonomics, but CLI commands should use the area/action form.

### Coder Config

```bash
coder config show
coder config edit
```

`coder config` manages `.coder.yaml` and resolved coder defaults. It is distinct
from `coder app config`, which manages an app manifest.

First milestone only needs `show` if it is helpful for debugging config
resolution. `edit` is backlog.

`cmd/build/main.go` should be added as a standalone build entrypoint for repo
and release automation. Taskfile build commands should eventually call:

```bash
go run ./cmd/build/main.go apps/coder
```

instead of direct ad hoc `go build` invocations.

## Programmatic API

Introduce a package dedicated to product-level coder behavior:

```text
apps/coderapp
```

`apps/coder` can continue to hold the inert coder resource bundle during the
transition. `apps/coderapp` owns loading `.coder.yaml`, resolving facets,
building commands, and invoking run/build/serve/install/deploy/test behaviors.

Suggested API:

```go
type Config struct {
    Root string
    CoderConfigPath string
    Model string
    Provider string
    Workspace distribution.WorkspaceConfig
    Imports ImportConfig
    AuthPath string
    Debug bool
    Dev bool
    Yolo bool
}

type App struct {
    // unexported resolved config, facet inventory, and distribution handles
}

func New(ctx context.Context, cfg Config) (*App, error)

func (a *App) Command() *cobra.Command
func (a *App) Run(ctx context.Context, target RunTarget, opts RunOptions) error
func (a *App) Serve(ctx context.Context, opts ServeOptions) error
func (a *App) Build(ctx context.Context, opts BuildOptions) error
func (a *App) Install(ctx context.Context, opts InstallOptions) error
func (a *App) Deploy(ctx context.Context, opts DeployOptions) error
func (a *App) Test(ctx context.Context, opts TestOptions) error
```

`cmd/coder` should only construct `coderapp.Config`, call `coderapp.New`, and
execute the returned command.

Layer placement:

- `apps/coderapp`: product assembly and CLI/native command coordination.
- `apps/coder`: coder resource bundle, features, session defaults.
- `apps/launch`: local runtime, serving, datasource indexing, model resolver,
  auth plugin registry, and distribution composition.
- `adapters/distribution/*`: reusable CLI-independent distribution adapters.
- `runtime/project` and `core/project`: inert facet inventory and scanning.
- `cmd/*`: process entrypoint glue only.

## Inventory And Facets

Extend project inventory so repo-local app/editor surfaces are explicit facets.

New facet kinds:

- `agentruntime_app_manifest` for `agentsdk.app.json`, `agentsdk.app.yaml`, and
  `agentsdk.app.yml`.
- `coder_config` for `.coder.yaml` and `.coder.yml`.
- `ai_config` for AI-facing instructions, memory/context files, agent files,
  and vendor config bundles.

`ai_config` is the generalized facet for files and directories that configure
an AI assistant or provide assistant-facing context. It should carry cheap
metadata instead of requiring one facet kind per vendor or filename.

Suggested metadata:

- `vendor`: `generic`, `claude`, `codex`, or another vendor/tool owner inferred
  from path conventions.
- `kind`: `instruction`, `context:memory`, `agent`, `skill`, `command`,
  `workflow`, `bundle`, or another narrow config role.
- `path`: workspace-relative file or directory path.
- `format`: `markdown`, `yaml`, `json`, or directory/bundle format when known.
- `parent`: optional parent bundle/config path when the vendor/kind is inherited
  from a directory.

Examples:

- `AGENTS.md` -> `ai_config(vendor=generic, kind=instruction)`.
- `CLAUDE.md` -> `ai_config(vendor=claude, kind=instruction)`.
- `MEMORY.md` -> `ai_config(vendor=generic, kind=context:memory)`.
- `.claude/` -> `ai_config(vendor=claude, kind=bundle)`.
- `.claude/agents/writer.md` ->
  `ai_config(vendor=claude, kind=agent, parent=.claude/)`.
- `.agents/` can remain `agents_dir` during transition, but should also be
  representable as `ai_config(vendor=generic, kind=bundle)` once inventory
  consumers can handle the generalized facet.

Inventory output should make clear that facets are detection signals, not
activation.

Facet rules:

- Facet detection may parse cheap metadata and report parse diagnostics.
- Facet detection must not load operations, skills, agents, commands, or
  workflows into the running coder session.
- Run/build/serve targets may load the app facet in an isolated app scope.
- `.coder.yaml` may explicitly import selected facet resources into coder.

## Configuration Resolution

On startup, coder resolves inputs in this order:

1. CLI flags.
2. Explicit coder config path, when provided by CLI/API.
3. Nearest `.coder.yaml` or `.coder.yml`, found by walking upward from the
   selected working directory to the workspace root.
4. Built-in coder defaults.

An explicit config path is authoritative and should not be merged with a
discovered parent config. Upward discovery should stop at the active workspace
root so coder does not accidentally inherit unrelated home-directory or parent
monorepo settings.

For app targets, app manifest values are loaded only after target selection.
App manifest runtime workspace roots and env files apply to the app run/serve
scope, not to the already-running coder session, unless `.coder.yaml` explicitly
mirrors or imports them.

Workspace roots:

- `.coder.yaml` roots extend the coder app workspace.
- app manifest `runtime.workspace.roots` extend that app's runtime scope.
- CLI `--workspace-root` values override or append per command, following the
  existing distribution workspace parser rules.

Model/provider:

- Coder model defaults configure coder sessions.
- App manifest model defaults configure app sessions.
- CLI model/provider flags override the selected execution scope only.

Resource imports:

- `.coder.yaml` imports should start with explicit names/refs for agents,
  skills, workflows, operations, and app facets.
- Glob/pattern imports are allowed only if the implementation can reuse existing
  workspace-safe helpers such as `runtime/system.Workspace` path resolution and
  `core/pathpattern`; do not add ad hoc glob parsing for this feature.
- If pattern support is included in v1, patterns must be resolved inside the
  selected workspace roots and reported as concrete imports in `coder config
  show`.
- If no reusable helper path is clean in the first implementation, defer pattern
  imports and keep v1 explicit-only.

## Migration Plan

### Phase 1: Design and Naming

- Add this design.
- Use it as the source of truth for subsequent implementation slices.

### Phase 2: Agentsdk Parity Through Coder

- Add `apps/coderapp` with `Config`, `New`, and command construction that wraps
  current `apps/coder` behavior.
- Add `.coder.yaml` loading with versioned schema, explicit-path precedence,
  nearest-upward discovery, workspace roots, env files, and empty import lists.
- Keep existing `coder` command behavior unchanged by default.
- Add area/action commands that cover current `agentsdk` behavior:
  `coder app run`, `coder app serve`, `coder app build`, `coder models`,
  `coder connect`, `coder datasource`, `coder discover`, and app/config
  inspection as needed.
- Implement these by delegating to existing shared `apps/launch` and
  `adapters/distribution/*` code first, then remove duplication.

### Phase 3: Inventory Facets

- [x] Add app manifest, coder config, and generalized `ai_config` facet metadata in
  `core/project`.
- [x] Update `runtime/project` scanning and tests.
- [x] Ensure app facets do not activate app resources in coder.

### Phase 4: Resource Actions And `/run`

- [x] Add `coder workflow run`, `coder agent run`, and `coder op run`.
- [x] Delegate current local app run behavior through the new `coderapp.Run`.
- [x] Add `/run` as a REPL command targeting the nearest app facet by default.
- [x] Remove temporary `agentsdk` binary compatibility; `coder app run` is the
  app run path.

### Phase 5: Distribution Backlog

- [ ] Add `coder app install`, `coder app deploy`, and `coder app test`.
- [x] Add `coder config edit` and `coder app config edit`.
- [x] Add `cmd/build/main.go` and switch repo build automation to it.
- [x] Keep `coder shell` in its own design and implementation track.

### Phase 6: Remove Agentsdk

- [x] Remove the `agentsdk` binary from normal build outputs.
- [x] Update docs from `agentsdk ...` workflows to `coder ...` workflows.
- [x] Remove remaining duplicated command implementations from the old product package.

## Testing

Unit tests:

- `.coder.yaml` decode/default/override behavior, including explicit config
  path precedence and nearest-upward discovery boundaries.
- `coderapp.Config` resolution from CLI-like inputs and config file values.
- resource import resolution with explicit refs, plus pattern imports only when
  backed by existing workspace/pathpattern helpers.
- project inventory facet detection for app manifests, `.coder.yaml`, and
  generalized `ai_config` examples including `AGENTS.md`, `CLAUDE.md`,
  `MEMORY.md`, `.claude/`, and `.claude/agents/writer.md`.
- no automatic app resource import when an app facet exists.
- area/action parsing for `app run`, `workflow run`, `agent run`, and `op run`.
- operation argument binding for `--arg`, `--json`, and `--`.

Integration tests:

- `coder` still opens the coder REPL by default.
- `coder --input` still runs against coder, not the app facet.
- `coder serve` serves the coder app.
- `coder app run . --input "hello"` opens the app manifest's default session.
- `/run` in a coder session targets the current app facet.
- `coder app serve .` serves the app facet through the existing daemon/channel
  stack.
- `coder app build . --dry-run` resolves the selected app and distribution build
  inputs.

Architecture tests:

- `cmd/coder` imports only app/cmd-level packages.
- `apps/coderapp` may depend on adapters, plugins, orchestration, runtime, sdk,
  and core through allowed app-layer directions.
- `runtime/project` and `core/project` stay inert and do not import appconfig,
  coder, or launch packages.

## Decisions

- `coder serve` serves the coder app itself.
- `coder app serve [path]` serves the selected agentruntime app facet.
- Explicit coder config path wins over discovery; otherwise coder uses the
  nearest `.coder.yaml`/`.coder.yml` found by walking upward to the workspace
  root.
- `.coder.yaml` imports are explicit by default. Pattern imports may ship in v1
  only if they reuse existing workspace-safe `System`/`pathpattern` helpers.
