# Product Split: Engine, Fluxplane CLI, Coder Product

## Summary

- Rename the reusable module from `github.com/fluxplane/agentruntime` to
  `github.com/fluxplane/engine`.
- Keep everything in this repository for the first implementation, then split
  `apps/coder` into its own nested Go module with a local replace back to the
  engine.
- Introduce `cmd/fluxplane` as the generic app-manifest CLI. It owns the
  current intent behind `coder app ...`.
- Keep `coder` as a standalone product CLI with its own bundle/config, reusing
  engine APIs and reusable command builders in-process.
- Rename app manifests from `agentsdk.app.yaml` to `fluxplane.yaml`; old
  manifest names are not discovered.

## Target Product Model

- `github.com/fluxplane/engine` is the reusable agent application engine:
  `core`, `runtime`, `orchestration`, `adapters`, `plugins`, `sdk`, and the
  root `fluxplane` facade.
- `fluxplane` is the engine-owned generic app CLI for manifest-scoped app
  workflows: init, run, serve, build, deploy, auth, datasource, and related
  app-resource commands.
- `coder` is a separate product built on the engine. It owns its bundle,
  product defaults, auth/datasource scope, and simplified config file.
- The current repository stays the staging area for this split. `apps/coder`
  becomes a nested module before any physical repository extraction.

## CLI Scope Rules

- `coder auth` manages auth for the coder product bundle plus coder config
  overlays.
- `coder datasource` manages datasources exposed by the coder product bundle.
- `fluxplane auth` manages auth declared by the selected `fluxplane.yaml`.
- `fluxplane datasource` manages datasources declared by the selected
  `fluxplane.yaml`.
- Reusable command implementations must accept their resource scope through
  loader/target-registry/plugin-factory inputs. They must not hard-code coder
  scope or current-directory app scope.
- `coder app` is removed rather than kept as an alias.

## Implementation Slices

1. Clean baseline:
   - Persist this plan under `.agents/plans/`.
   - Commit the current repo state before rename work starts.

2. Engine module rename:
   - Change the root module path to `github.com/fluxplane/engine`.
   - Rewrite imports from `github.com/fluxplane/agentruntime/...` to
     `github.com/fluxplane/engine/...`.
   - Rename the root facade package from `agentruntime` to `fluxplane`.
   - Update public docs and comments that describe the module/repo identity to
     say Fluxplane Engine.
   - Validate and ask for approval before committing the rename.

3. Fluxplane CLI extraction:
   - Add `apps/fluxplane` and `cmd/fluxplane`.
   - Move generic app commands out of coder-specific assembly.
   - Wire auth and datasource command scopes through reusable engine command
     builders.

4. Manifest rename:
   - Change discovery/init/deploy/docs/tests from `agentsdk.app.yaml` to
     `fluxplane.yaml`.
   - Do not retain old manifest discovery aliases.

5. Coder module split:
   - Add `apps/coder/go.mod` with module path `github.com/fluxplane/coder`.
   - Use `replace github.com/fluxplane/engine => ../..`.
   - Move coder entrypoint under the nested module.
   - Ensure the engine module no longer imports coder packages.

## Test Plan

- Run `go test ./...` in the engine root after each broad slice.
- Run coder tests from the nested module after the coder split.
- Keep `internal/architecture` at zero violations.
- Verify `fluxplane init/run/auth/datasource` use manifest scope.
- Verify `coder auth/datasource` use coder product scope.
- Verify `coder app` is not present.

## Assumptions

- The first implementation keeps a single physical repository.
- The canonical generic app CLI is `fluxplane`.
- The engine module is `github.com/fluxplane/engine`.
- The root facade package is `fluxplane`.
- Old `coder app` and `agentsdk.app.yaml` compatibility is intentionally not
  preserved.
