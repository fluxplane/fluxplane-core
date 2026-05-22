# Codegate Go Quality Gate And Plugin Tooling Plan

## Integration Status

Implemented in engine:

- `engine-architecture.rules.json` at the repository root.
- `task quality:go`, `task quality:go:review`, and `task quality:go:full`.
- `task verify` now runs the codegate Go quality gate.
- `go_assess` and `go_review` are exposed by the Go language plugin through
  released `github.com/fluxplane/codegate v1.1.0`.
- The legacy `internal/architecture` checker and `apps/archreport` command
  are removed; codegate is the single Go architecture and review gate, with no
  `arch:*` compatibility Taskfile targets.

## Context

Engine used to own a project-specific architecture mechanism in
`internal/architecture` and exposed it through `apps/archreport` plus Taskfile
targets:

- `task arch:check` fails on production boundary violations through
  `go run ./apps/archreport -fail-on boundary`.
- `task arch:report` and `task arch:render` produce review artifacts.
- The report still carries an architecture score, component scores, coupling
  penalties, host-IO diagnostics, unknown-package diagnostics, and graph output.

The neighboring codegate repository is at `/home/timo/projects/editor` in this
workspace. It provides the reusable assessment surface that should replace the
score/report logic here:

- Go API: `github.com/fluxplane/codegate` with `language/golang`.
- CLI: `codegate --root . --language go assess ...`.
- Architecture rules are consumer-owned JSON, not hard-coded in codegate.
- `assess --fail-on boundary,effects,unknown` prints normal JSON and exits
  non-zero for selected hard categories.
- Reports include `summary`, `scores`, validation, findings, violations,
  diagnostics, top units, and suggestions.
- Go assessment covers architecture, maintainability, safety, coverage, and
  code-review evidence such as complexity, doc coverage, weak names, ignored
  errors, unchecked assertions, dynamic exec, SQL/path risks, reflection,
  slice capacity opportunities, and large range copies.
- Codegate also ships agent/plugin assets: an `assess` command and a reviewer
  agent that treat scores as secondary to concrete findings.

Use the released codegate module for engine integration. The local checkout is
reference material only; do not add a local `replace` to `/home/timo/projects/editor`.
The latest release visible in the local checkout during this planning pass is
`v1.1.0`.

## Goals

1. Replace the engine-owned architecture score/report path with a codegate
   backed quality gate in `Taskfile.yaml`.
2. Preserve hard architecture enforcement for engine's layer model, host-effect
   rules, unknown-package coverage, and reviewed fan-out exceptions.
3. Expose codegate assessment as Go language plugin tooling so agents can run
   focused Go review and architecture checks through engine operations, not only
   through shell commands.
4. Make score output a review signal, not an engine-specific architecture
   contract.

## Non-Goals

- Do not copy codegate's internals into this repository.
- Do not add global app defaults or hidden plugin bundles.
- Do not preserve `apps/archreport` score semantics as a compatibility layer.
  This rewrite is pre-1.0, so stale shapes can be replaced.
- Do not run codegate outside the runtime safety envelope from plugin
  operations.

## Target Shape

### Architecture Rules

Add a repo-owned codegate rules file, for example:

```text
engine-architecture.rules.json
```

Seed it from `/home/timo/projects/editor/examples/agentruntime-architecture.rules.json`,
then update it to match this module exactly. That example was created before
this repository's module was renamed from agentruntime to engine, so the old
module name is not a compatibility target:

- `module_path`: `github.com/fluxplane/engine`
- layers: facade, core, sdk, runtime, orchestration, adapters, plugins, apps,
  cmd
- allowed dependency direction from the engine layer model
- inner-layer host IO effects from the engine safety model
- runtime host IO effects plus all current allowlist exceptions
- plugin host-effect call rules
- coupling fan-out threshold and all current reviewed fan-out reasons
- unknown-package exceptions only when there is a reviewed reason in this
  rules file

The rules file becomes the source of truth for policy data. Any remaining Go
code should read this policy rather than duplicate it.

### Dependency Policy

Pin codegate to a released version in engine:

- initial target: `github.com/fluxplane/codegate v1.1.0`
- no local `replace` for normal development or CI
- no dependency on unreleased local checkout state
- update intentionally through a normal dependency bump with changelog entry
  when codegate releases a needed fix or detector

For Taskfile commands, prefer a repo-pinned invocation over an arbitrary
`codegate` binary on `PATH`. Acceptable implementation options are:

- add codegate as a Go tool dependency and invoke the pinned tool, if the
  current Go toolchain setup supports that cleanly
- otherwise invoke `go run github.com/fluxplane/codegate/cmd/codegate@v1.1.0`
  in the Taskfile until a pinned tool wrapper exists

The Go plugin integration should import the released library directly from
`github.com/fluxplane/codegate`.

### Taskfile Quality Gate

Update `Taskfile.yaml` so `task verify` still runs narrow deterministic checks,
but architecture scoring moves to codegate:

```yaml
quality:go:
  desc: Run codegate Go quality assessment.
  cmds:
    - go run github.com/fluxplane/codegate/cmd/codegate@v1.1.0 --root . --language go --format json assess --gate all --rules engine-architecture.rules.json --fail-on boundary,effects,unknown --view summary

quality:go:review:
  desc: Print compact codegate Go review report.
  cmds:
    - go run github.com/fluxplane/codegate/cmd/codegate@v1.1.0 --root . --language go --format json assess --gate all --rules engine-architecture.rules.json --view compact --suggestions 10

quality:go:full:
  desc: Print full codegate Go review evidence.
  cmds:
    - go run github.com/fluxplane/codegate/cmd/codegate@v1.1.0 --root . --language go --format json assess --gate all --rules engine-architecture.rules.json --view full --suggestions 20
```

Then replace `task arch:check` inside `task verify` with `task quality:go`.

### Go Plugin Tooling

Add codegate-backed operations to `plugins/languages/golang` because this is
optional first-party Go capability tooling:

- `go_assess`: run a codegate assessment for a workspace path.
- `go_review`: return compact review-oriented findings for agent use.
- Optional later operations:
  - `go_assessment_capabilities`
  - `go_suggest`
  - `go_validate`

Contracts belong in `core/language/golang` beside existing Go operation DTOs.
Execution belongs in `plugins/languages/golang` and must use
`runtime/system.System` boundaries:

- filesystem reads through `system.Workspace()`
- any CLI fallback through `system.Process()` and the safety envelope
- no direct `os`, `os/exec`, `net/http`, or host-path access in the plugin

Preferred implementation path:

1. Add released `github.com/fluxplane/codegate v1.1.0` as a module dependency.
2. Build codegate in-process with a small source adapter backed by
   `system.Workspace()`, so normal assessment does not shell out.
3. Register `language/golang.New(golang.Config{})` from codegate.
4. Map engine operation inputs into `codegate.AssessmentOptions`.
5. Return `operation.Rendered` with:
   - concise text summary
   - raw structured report in `Data["assessment"]`
   - normalized `summary`, `scores`, `finding_counts`, `violation_counts`,
     `top_findings`, `top_violations`, and `top_units`
6. Support an explicit rules path first, defaulting to
   `engine-architecture.rules.json` when present.
7. Keep failure semantics explicit: `go_assess` returns an operation failure
   only for execution/validation errors or selected `fail_on` categories.

Do not make the plugin depend on `apps` or `cmd`. This stays within the layer
rule: `plugins -> core, runtime, orchestration, adapters, plugins`.

## Migration Steps

1. Add `engine-architecture.rules.json` and verify it
   reproduces the current hard categories:
   - boundary
   - side effects/effects
   - unknown packages
   - reviewed fan-out notes
2. Add Taskfile targets:
   - `quality:go`
   - `quality:go:review`
   - `quality:go:full`
   - optional `quality:go:html`
3. Change `task verify` to call `quality:go` instead of `arch:check`.
4. Update docs:
   - `docs/verification.md`
   - `docs/architecture.md`
   - `AGENTS.md` verification notes if command names change
5. Introduce core Go assessment DTOs in `core/language/golang`.
6. Add released codegate dependency and codegate source adapter plus assessment
   execution in
   `plugins/languages/golang`.
7. Add plugin operation specs, operation set membership, and tests.
8. Run focused tests:
   - `go test ./plugins/languages/golang ./core/language/golang`
   - `task quality:go`
9. Remove or demote legacy scoring:
   - remove `internal/architecture`
   - retire `apps/archreport`
   - remove stale docs that describe expected score thresholds such as
     `>= 98`
10. Run `task verify` once the migration is broad enough to justify the full
    gate.

## Acceptance Criteria

- `task verify` fails on codegate-selected hard gates:
  `boundary,effects,unknown`.
- `task quality:go:review` produces compact JSON suitable for agent review.
- Engine architecture policy lives in one rules file, not duplicated across
  score code and docs.
- Existing hard layer violations remain covered.
- Agents can call Go assessment through plugin operations without invoking a
  shell directly.
- The Go plugin returns structured findings and violations with file/line
  locations when codegate supplies them.
- Documentation no longer treats the old architecture score as a release gate
  or expected numeric threshold.

## Risks And Open Questions

- Codegate's example rules use `github.com/fluxplane/agentruntime` because they
  predate the rename. Engine rules must use `github.com/fluxplane/engine` and
  should never preserve the old name.
- `go run github.com/fluxplane/codegate/cmd/codegate@v1.1.0` may be too slow
  for repeated local `task verify` runs. If so, add a pinned Go tool wrapper,
  but keep the version controlled by this repository.
- Codegate graph rendering is not equivalent to the removed archreport graph
  formats; keep graph rendering only if there is still a concrete consumer.
- In-process codegate assessment needs a `system.Workspace()` backed `Source`.
  If codegate requires APIs not currently exposed by `Workspace`, add a narrow
  adapter rather than broadening plugin host IO access.
- Keep the root rules file as the only engine architecture policy source.

## First Implementation Slice

The smallest useful slice is:

1. Add `engine-architecture.rules.json`.
2. Add `quality:go` and `quality:go:review` Taskfile targets.
3. Change `verify` to run `quality:go`.
4. Add docs that point reviewers to `task quality:go:review`.
5. Remove `apps/archreport` and `internal/architecture` once codegate is wired
   into `task verify`.

The second slice should add `go_assess` to the Go plugin and prove the same
rules can be run through the runtime operation surface.
