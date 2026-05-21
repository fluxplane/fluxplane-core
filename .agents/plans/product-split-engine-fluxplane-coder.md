# Product Split: Engine, Fluxplane CLI, and Coder Product

Last reviewed: 2026-05-22.

## Summary

We are separating three concerns that currently overlap in one repository:

- **Engine**: the reusable Go module and runtime architecture.
- **Fluxplane CLI**: the generic app-manifest product for running authored app
  bundles.
- **Coder product**: a concrete app built on the engine with its own default
  bundle, product config, and simplified user experience.

The split starts in this repository. We first make the module identity explicit
as `github.com/fluxplane/engine`, then extract the generic app CLI out of
`coder app ...`, then isolate `coder` as a product module that imports the
engine.

## Why This Exists

The current `coder` binary carries two different product ideas:

- `coder` itself is a product: a bundled coding agent with product defaults,
  product auth, product datasource exposure, and a simpler end-user config.
- `coder app ...` is not really part of the coder product. It is the generic
  app-manifest workflow for running and deploying authored Fluxplane apps.

That overlap caused scope bugs:

- Product commands and generic app commands shared implementation paths without
  always passing an explicit product/app scope.
- Auth-backed integration plugins could appear in one surface and disappear in
  another because management commands, datasource indexing, and runtime
  projection did not always use the same scoped plugin factory inputs.
- Generic app docs and coder product docs used the same command namespace,
  making it unclear which bundle/config/auth store a command was managing.

The desired result is not just a rename. The desired result is that each
runtime surface has an explicit owner and an explicit scope.

## Current State

This plan file is part of the repository state. A reader should not need this
chat session to understand what has already happened.

- Baseline cleanup commit:
  `2c966c2 chore: capture cli cleanup baseline`.
- Engine module rename commit:
  `861c833 refactor: rename root module to fluxplane engine`.
- Current root module:
  `github.com/fluxplane/engine`.
- Current root facade package:
  `fluxplane`.
- Current physical repository/worktree path:
  `/home/timo/projects/fluxplane/agentruntime`.
- Current product CLI entrypoint:
  `cmd/coder`.
- Current generic app command surface:
  `coder app ...`, implemented under `apps/coder/app_command.go` and backed by
  reusable code in `apps/launch`.
- Current app manifest filename:
  `agentsdk.app.yaml`.
- Target app manifest filename:
  `fluxplane.yaml`.
- Known external acceptance fixture:
  `<local-slack-bot-app>`, the user's local Slack bot app outside this repo.
- Known unrelated local file that should not be swept into commits unless a
  future task explicitly says so:
  `.agents/plans/gitlab-dex-parity.md`.

## Glossary

- **Engine**: the reusable Go module and runtime architecture published as
  `github.com/fluxplane/engine`.
- **Fluxplane CLI**: the generic product CLI for app manifests. It operates on
  `fluxplane.yaml` bundles and should eventually own the command surface that
  is currently exposed as `coder app ...`.
- **Coder product**: a concrete product built on the engine. It owns the
  `coder` binary behavior, coder defaults, coder bundle, coder config, coder
  auth scope, and coder datasource scope.
- **Bundle scope**: the concrete set of resources, plugins, auth targets,
  datasources, operations, reactions, and defaults selected for a command.
- **Manifest scope**: bundle scope loaded from an authored app manifest.
- **Product scope**: bundle scope assembled by a product such as coder.
- **Scoped plugin factory**: a plugin factory built from explicit inputs such
  as auth store, auth resolver, System, plugin refs, and environment policy.
  Management commands, datasource indexing, and runtime execution should use
  equivalent scoped factories for the same bundle.

## Target Product Model

### Engine

- Module path: `github.com/fluxplane/engine`.
- Root package name: `fluxplane`.
- Owns reusable concepts and implementations:
  - `core`
  - `sdk`
  - `runtime`
  - `orchestration`
  - `adapters`
  - `plugins`
  - reusable app/distribution launch support
- Does not own a concrete product's user configuration policy.
- Does not import `apps/coder` or any future product module.

### Fluxplane CLI

- Binary name: `fluxplane`.
- Command package target: `cmd/fluxplane`.
- App assembly package target: `apps/fluxplane`, if a distinct assembly layer
  is needed beyond the existing reusable `apps/launch` package.
- Owns the generic app-manifest workflows currently exposed as
  `coder app ...`.
- Uses `fluxplane.yaml` as the only app manifest filename.
- Does not discover `agentsdk.app.yaml` once the manifest rename slice lands.
- Commands operate on the selected app manifest bundle, not on the coder
  product bundle.

Expected command families:

- `fluxplane init`
- `fluxplane run`
- `fluxplane serve`
- `fluxplane build`
- `fluxplane deploy`
- `fluxplane auth`
- `fluxplane datasource`
- `fluxplane resource` where still needed

Current repo fact: `apps/launch` already contains most generic app lifecycle
command implementation. The intended extraction should first make that package
cleanly reusable and scoped, then add `apps/fluxplane` only for product
assembly/defaults that do not belong in `apps/launch`.

### Coder Product

- Future module path: `github.com/fluxplane/coder`.
- Initial location: nested module under `apps/coder`.
- Imports the engine through:

  ```go
  replace github.com/fluxplane/engine => ../..
  ```

- Owns:
  - the coder product bundle
  - coder-specific defaults
  - coder-specific config file
  - coder auth scope
  - coder datasource scope
  - the `coder` binary user experience
- Does not expose `coder app`.
- Reuses engine command builders where possible by passing coder-specific
  scope inputs.

## CLI Scope Rules

- `coder auth` manages auth for the coder product bundle plus coder config
  overlays.
- `coder datasource` manages datasources exposed by the coder product bundle.
- `coder run` or plain `coder --input ...` runs the coder product bundle.
- `fluxplane auth` manages auth declared by the selected `fluxplane.yaml`
  bundle.
- `fluxplane datasource` manages datasources declared by the selected
  `fluxplane.yaml` bundle.
- `fluxplane run` runs the selected `fluxplane.yaml` bundle.
- `fluxplane build`, `fluxplane deploy`, and `fluxplane serve` operate on the
  selected `fluxplane.yaml` bundle and generated runtime/deployment artifacts.
- Reusable command implementations must accept their resource scope through
  loader/target-registry/plugin-factory inputs. They must not hard-code coder
  scope or current-directory app scope.
- `coder app` is removed rather than kept as an alias or compatibility shim.

Expected command ownership after the split:

| Command | Owner | Scope |
|---|---|---|
| `coder` | coder product | coder product bundle |
| `coder auth` | coder product using reusable auth command builder | coder product bundle |
| `coder datasource` | coder product using reusable datasource command builder | coder product bundle |
| `fluxplane run` | Fluxplane CLI using reusable app runner | selected `fluxplane.yaml` |
| `fluxplane serve` | Fluxplane CLI using reusable app server | selected `fluxplane.yaml` |
| `fluxplane build` | Fluxplane CLI using reusable build/deploy adapters | selected `fluxplane.yaml` |
| `fluxplane deploy` | Fluxplane CLI using reusable build/deploy adapters | selected `fluxplane.yaml` |
| `fluxplane auth` | Fluxplane CLI using reusable auth command builder | selected `fluxplane.yaml` |
| `fluxplane datasource` | Fluxplane CLI using reusable datasource command builder | selected `fluxplane.yaml` |

## Architectural Rules

- Generic app command logic belongs in engine-owned reusable packages or in
  `apps/fluxplane`, depending on whether it is product-neutral or Fluxplane CLI
  assembly.
- `apps/launch` is the current candidate for product-neutral generic app
  command implementation. Do not copy that logic into `apps/fluxplane`; move
  or rename only when the package name itself becomes misleading.
- Coder command logic belongs in `apps/coder` until the nested module split,
  then remains in the `github.com/fluxplane/coder` module.
- Command builders must be parameterized by scope:
  - distribution or bundle loader
  - config source
  - auth store source
  - plugin factory set
  - resource target registry
  - environment/auth resolution policy
- Auth environment access stays explicit. Product CLIs decide whether to pass
  environment resolution into runtime/plugin auth. Reusable modules must not
  read process environment directly unless they are the System adapter boundary.
- Datasource indexing and detection must use the same scoped plugin factories
  as runtime execution so "available in CLI management" and "available to the
  model/runtime" do not drift.
- Generated deployments must preserve the same explicit auth-env policy. A CLI
  flag may choose to pass selected environment auth into a local run or
  generated deployment, but runtime/plugin code must still receive that as an
  explicit scoped input.
- No compatibility aliases for pre-1.0 names unless a later plan explicitly
  reverses that decision.

## Auth Activation Invariants

Auth-backed integrations were one of the reasons this split became necessary.
These invariants apply to all first-party integration plugins, especially
plugins under `plugins/integrations/*` that declare auth requirements.

- Auth-required operations, datasources, observers, evidence producers,
  reactions, and projected model tools must be inactive until the selected
  bundle scope has usable auth for that plugin instance.
- The first activation gate is auth availability in the selected scope, not the
  presence of a plugin package in the binary.
- Stored auth, connector auth, and process-env-backed auth must all flow through
  explicit scoped resolvers.
- Process-env-backed auth is available only when the product/CLI boundary opts
  in, for example through `--allow-plugin-auth-env`.
- `coder auth status`, `coder datasource index build`, and coder runtime tool
  projection must agree about which coder-scoped integration plugins are active.
- `fluxplane auth status`, `fluxplane datasource index build`, and Fluxplane
  runtime tool projection must agree about which manifest-scoped integration
  plugins are active.
- If auth is missing, management commands may list the declared target and
  explain what is missing, but runtime projection must not expose the callable
  operation/datasource as active.
- If auth becomes available, datasource indexing and runtime projection should
  activate from the same scoped plugin factory path. Do not add one-off
  projection-only or datasource-only auth checks.
- Tests should cover both negative and positive cases:
  - auth missing: integration operations/datasources/tools are not active
  - stored auth available: they are active
  - process env auth present but not allowed: they are not active
  - process env auth present and explicitly allowed: they are active

## Current Repo Observations

- `cmd/coder` is the only product CLI entrypoint today.
- `apps/coder/app_command.go` owns the current `coder app ...` surface.
- `apps/launch` already owns reusable app lifecycle operations such as run,
  serve, init, datasource indexing, deploy/build support, and auth-env
  plumbing.
- `apps/coder/app.go` already wires coder-scoped auth and datasource commands
  through product plugin factories.
- `adapters/resources/appconfig` still discovers `agentsdk.app.yaml`.
- Public docs still contain many `coder app ...` and `agentsdk.app.yaml`
  references. These should move in the same slices as the command and manifest
  behavior, not as an isolated wording-only cleanup.
- The local slack-bot app at `<local-slack-bot-app>` is an
  external validation fixture for the generic app workflow. It should be
  migrated in the manifest rename slice and used as a smoke test before the
  plan is considered complete.

## Non-Goals

- Do not add `agentsdk` binary compatibility.
- Do not keep `coder app` as a compatibility alias.
- Do not make `agentsdk.app.yaml` an active discovery alias after the manifest
  rename.
- Do not physically extract repositories until the nested-module staging work
  is clean and explicitly approved.
- Do not broaden runtime/plugin access to process environment by default.
- Do not duplicate generic app command implementation between coder and
  fluxplane.

## Implementation Slices

### 1. Clean baseline

Status: complete.

Commit: `2c966c2 chore: capture cli cleanup baseline`.

Deliverables:

   - Persist this plan under `.agents/plans/`.
   - Commit the current repo state before rename work starts.

Assertions:

- The plan exists at
  `.agents/plans/product-split-engine-fluxplane-coder.md`.
- The baseline commit contains all intended pre-rename cleanup.
- Unrelated local files remain uncommitted.

### 2. Engine module rename

Status: complete.

Commit: `861c833 refactor: rename root module to fluxplane engine`.

Deliverables:

   - Change the root module path to `github.com/fluxplane/engine`.
   - Rewrite imports from `github.com/fluxplane/agentruntime/...` to
     `github.com/fluxplane/engine/...`.
   - Rename the root facade package from `agentruntime` to `fluxplane`.
   - Update public docs and comments that describe the module/repo identity to
     say Fluxplane Engine.
   - Update `CHANGELOG.md`.
   - Validate and ask for approval before committing the rename.

Assertions:

- `go.mod` declares `module github.com/fluxplane/engine`.
- No Go import path references `github.com/fluxplane/agentruntime`.
- The root package is `package fluxplane`.
- Public docs describe the reusable module as Fluxplane Engine.
- Runtime data names that are intentionally not product identity, such as
  legacy env vars, local store paths, subjects, labels, and scratch prefixes,
  are not renamed in this slice.
- `go test ./...` passes.
- `go run ./apps/archreport` reports zero architecture violations.

### 3. Fluxplane CLI extraction

Status: next.

Purpose: create the generic app CLI without changing the manifest filename yet.
The command split should happen while `agentsdk.app.yaml` still works, so
command-scope mistakes are easier to isolate from manifest-rename mistakes.

Deliverables:

   - Add `apps/fluxplane` and `cmd/fluxplane`.
   - Treat `apps/fluxplane` as a thin assembly package unless implementation
     proves a stronger package boundary is needed.
   - Move generic app command assembly out of coder-specific packages.
   - Preserve reusable app command internals where they already exist in
     `apps/launch`.
   - Introduce explicit scope inputs for reusable auth, datasource, resource,
     build, run, serve, deploy, and init command builders.
   - Route `fluxplane auth` and `fluxplane datasource` through selected app
     manifest scope.
   - Route `coder auth` and `coder datasource` through coder product scope.
   - Remove the `coder app` command surface.
   - Move public docs for generic app workflows away from `docs/coder.md`.
   - Add or update `CHANGELOG.md` for the command-surface removal/addition.

Assertions:

- `go run ./cmd/fluxplane --help` shows generic app commands.
- `go run ./cmd/coder --help` does not show an `app` command.
- `go run ./cmd/coder auth status` uses the coder product scope.
- `go run ./cmd/coder datasource index build` indexes coder product
  datasources, including integration plugins that require auth when auth is
  available and allowed by policy.
- `go run ./cmd/fluxplane auth status` requires or selects a manifest scope
  and does not silently fall back to coder scope.
- `go run ./cmd/fluxplane datasource index build` indexes the selected
  manifest bundle only.
- Existing authored apps still using `agentsdk.app.yaml` can run through
  `fluxplane run` in this slice, because the manifest rename has not happened
  yet.
- There is no duplicated command implementation between coder and fluxplane;
  product differences are represented as passed-in scope/config/factory inputs.
- `apps/fluxplane`, if present, mostly wires options and defaults; generic
  command behavior remains in `apps/launch` or another product-neutral engine
  package.
- `go test ./apps/coder ./apps/launch ./adapters/distribution/...` passes.
- `go test ./...` passes after the slice is complete.
- `go run ./apps/archreport` reports zero architecture violations.

### 4. Manifest rename

Status: pending.

Purpose: rename the generic app manifest file after the Fluxplane CLI exists,
then migrate known authored apps and docs onto the new filename.

Deliverables:

   - Change discovery/init/deploy/docs/tests from `agentsdk.app.yaml` to
     `fluxplane.yaml`.
   - Make `fluxplane init` write `fluxplane.yaml`.
   - Make generic app discovery read `fluxplane.yaml`.
   - Make deploy/build/run/serve docs and tests reference `fluxplane.yaml`.
   - Do not retain old manifest discovery aliases.
   - Refactor `<local-slack-bot-app>` from
     `agentsdk.app.yaml` to `fluxplane.yaml`.
   - Update any local slack-bot scripts/docs that invoke `coder app ...` so
     they invoke `fluxplane ...`.
   - Add or update `CHANGELOG.md`.

Assertions:

- `rg "agentsdk\\.app\\.yaml"` returns only historical migration notes, if any
  are intentionally kept.
- `fluxplane init` creates `fluxplane.yaml`.
- `fluxplane run` and related commands fail clearly when only
  `agentsdk.app.yaml` exists.
- The local slack-bot app at `<local-slack-bot-app>` has
  its manifest refactored to `fluxplane.yaml` and still runs through the new
  `fluxplane` CLI.
- No coder product command depends on an app manifest filename unless it is
  intentionally running an authored app through engine APIs.
- `go test ./adapters/resources/... ./apps/launch/... ./apps/fluxplane/...`
  passes.

### 5. Coder module split

Status: pending.

Purpose: turn coder into a product module that imports the engine, while still
staging the work inside this physical repository.

Deliverables:

   - Add `apps/coder/go.mod` with module path `github.com/fluxplane/coder`.
   - Use `replace github.com/fluxplane/engine => ../..`.
   - Move the coder entrypoint under the nested module, most likely to
     `apps/coder/cmd/coder`.
   - Ensure the engine module no longer imports coder packages.
   - Move coder-only code that must belong to the product into the nested
     module.
   - Keep product-neutral code in the engine.
   - Update build/test tasks for both modules.
   - Update install/release documentation for the staged nested-module layout.
   - Add or update `CHANGELOG.md`.
   - Keep this as an in-repo nested module before any physical repository
     extraction.

Assertions:

- `apps/coder/go.mod` declares `module github.com/fluxplane/coder`.
- The engine root module does not import `github.com/fluxplane/coder`.
- The coder module imports `github.com/fluxplane/engine`.
- `go test ./...` passes in the engine root.
- `go test ./...` passes in `apps/coder`.
- The coder module entrypoint works from the expected build task.
- The root engine module no longer contains `cmd/coder`, unless it is a
  deliberately temporary development shim that imports the nested module
  through an explicit root-module dependency.
- Architecture checks still report zero violations for the engine module.

### 6. Repository extraction readiness

Status: pending, not implemented until explicitly requested.

Purpose: prepare for physical repository alignment only after the in-repo module
boundaries are already clean.

Deliverables:

- Decide whether this repository remains `github.com/fluxplane/engine` or is
  physically moved.
- Decide whether `apps/coder` is extracted to `github.com/fluxplane/coder`.
- Keep local replace directives only while the nested-module staging setup is
  required.

Assertions:

- Release/build docs describe where each product is published.
- Cross-module imports use public engine APIs rather than internal package
  reach-through.
- The coder product can build from a clean checkout with documented replace or
  module workspace setup.

## Test Plan

- For broad engine slices, run `go test ./...` from the engine root.
- For architecture-sensitive slices, run `go run ./apps/archreport`.
- For command-surface changes, run focused CLI smoke tests with `go run` before
  committing.
- For installed-binary behavior, use the repository task that already wraps the
  intended coder live-test provider where appropriate:

  ```bash
  task coder:live-test -- "prompt describing the scenario"
  ```

- For auth and datasource scope changes, validate both sides:
  - coder product scope through `coder auth` and `coder datasource`
  - manifest scope through `fluxplane auth` and `fluxplane datasource`
- After the coder nested module exists, run tests in both modules.
- Before committing a broad slice, ensure `CHANGELOG.md` reflects user-visible
  changes.

Concrete smoke commands to keep current as the implementation evolves:

```bash
go run ./cmd/coder --help
go run ./cmd/coder auth status
go run ./cmd/coder datasource index build
go run ./cmd/fluxplane --help
go run ./cmd/fluxplane auth status
go run ./cmd/fluxplane datasource index build
go run ./cmd/fluxplane run <local-slack-bot-app> --input "health check"
go test ./...
go run ./apps/archreport
```

The exact `fluxplane run` prompt may be adjusted to whatever the slack-bot app
supports after migration. The important assertion is that the external app is
loaded through `fluxplane.yaml` and reaches the same runtime behavior it had
before the split.

## Acceptance Criteria

The split is complete when all of the following are true:

- The reusable engine module is `github.com/fluxplane/engine`.
- The root facade package is `fluxplane`.
- The generic app CLI is `fluxplane`, not `coder app`.
- `fluxplane` commands operate on `fluxplane.yaml` manifest scope.
- `coder` commands operate on the coder product bundle/config scope.
- Auth resolution is explicit at CLI/product boundaries and does not widen
  runtime access to process environment by default.
- Datasource management and runtime tool exposure use the same scoped plugin
  factory inputs.
- Integration plugins that require auth are inactive until scoped auth is
  available, then become available consistently to management commands,
  datasource indexing, and runtime projection.
- `coder app` is absent.
- `agentsdk.app.yaml` is not discovered as an active manifest name.
- The local app at `<local-slack-bot-app>` continues
  working after its manifest is refactored to `fluxplane.yaml` and its commands
  use `fluxplane` instead of `coder app`.
- The coder product can be built and tested independently from the engine
  module in the nested-module staging layout.
- The engine module does not import the coder product.
- Full tests and architecture checks pass.

## Decisions

These are settled for this plan unless a later plan explicitly changes them.

1. `fluxplane` commands are top-level.

   Use `fluxplane run`, `fluxplane auth`, `fluxplane datasource`, and related
   top-level commands rather than `fluxplane app ...`. This is the cleanest
   expression of Fluxplane as the generic app-manifest product, and it avoids
   recreating the same ambiguity currently caused by `coder app ...`.

2. `coder app` is removed.

   Do not keep an alias or compatibility shim. This is a pre-1.0 rewrite and
   the product boundary should be visible in the command surface.

3. `apps/launch` remains the first reuse point for generic app command logic.

   Do not duplicate its behavior into `apps/fluxplane`. Add `apps/fluxplane`
   only as a thin product assembly/defaults package if the new `cmd/fluxplane`
   needs a product-owned package.

4. The coder binary moves into the coder module.

   After `apps/coder` becomes module `github.com/fluxplane/coder`, the cleaner
   target is `apps/coder/cmd/coder`. The root engine module should not retain a
   normal `cmd/coder`, because that creates the wrong ownership direction.

5. Manifest discovery is strict.

   `fluxplane.yaml` is the active manifest filename. `agentsdk.app.yaml` is not
   discovered as a compatibility alias.

6. Docs split by product.

   Generic app workflow docs should move to Fluxplane-oriented docs, likely
   `docs/fluxplane.md`. `docs/coder.md` should describe the coder product only.
   Engine docs should describe reusable architecture and APIs, not product CLI
   workflows.

7. Auth environment access remains explicit.

   Product CLIs may opt into process-env-backed plugin auth through explicit
   flags and scoped inputs. Runtime/plugin code must not directly read process
   environment unless it is at the approved System adapter boundary.

8. The engine repository will be physically renamed.

   The final repository path should align with the module path
   `github.com/fluxplane/engine`. The in-repo staging work can continue in the
   current worktree path until the module and product boundaries are clean, but
   before release automation depends on the new module path the physical
   repository should be renamed to match it.

9. Inspection commands remain available and configurable.

   Keep resource/discover/describe-style command families where they provide
   useful inspection or diagnostics. Implement them through reusable command
   builders with explicit scope inputs, so Fluxplane can expose manifest-scoped
   inspection and coder can expose the appropriate coder-scoped subset without
   duplicating command logic.

10. Keep the `apps/launch` name through Fluxplane CLI extraction.

   The Fluxplane CLI extraction slice should change command ownership and scope
   wiring, not package names at the same time. Reassess the package name only
   after `cmd/fluxplane` exists and the remaining ambiguity, if any, is visible.

11. Auth store defaults are product-owned and configurable.

   Keep coder's current auth store default for the coder product during this
   split. Introduce a Fluxplane-owned default for manifest apps, for example
   `~/.fluxplane/auth`, and keep auth paths configurable everywhere. This avoids
   breaking existing coder auth while preventing the generic app CLI from
   inheriting coder product identity.

12. Deployed apps require explicit env contracts.

   Treat `--allow-plugin-auth-env` as local-runtime convenience by default. For
   build/deploy flows, require explicit env declarations or emit a visible,
   reviewable env contract. Generated deployments must not silently inherit the
   developer shell's broad credential surface.

13. Manifest rename diagnostics are strict but actionable.

   When `agentsdk.app.yaml` is present without `fluxplane.yaml`, fail with a
   structured diagnostic that says the old filename is no longer supported and
   points to `fluxplane.yaml`. Do not load the old file as a compatibility
   alias.

14. Add a coder-module architecture check after the nested module split.

   Start with a small coder-specific check that asserts coder imports public
   engine APIs and does not reach into engine internals beyond approved
   packages. Generalize to a multi-module architecture checker only after
   another product module needs the same enforcement.

## Open Questions

No product questions are currently open. If implementation reveals a new
choice, add it here with options and a recommendation before making the choice
implicitly in code.

## Assumptions

- The first implementation keeps a single physical repository.
- The canonical generic app CLI is `fluxplane`.
- The engine module is `github.com/fluxplane/engine`.
- The root facade package is `fluxplane`.
- Old `coder app` and `agentsdk.app.yaml` compatibility is intentionally not
  preserved.
- The repository is pre-1.0, so clean replacement is preferred over
  compatibility shims.
- External app migration under `<local-slack-bot-app>`
  is allowed during the manifest rename slice and should be reviewed as part
  of that slice.
