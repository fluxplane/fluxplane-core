# Fluxplane Engine Agent Notes

Operative rules for AI agents and developers working in
`github.com/fluxplane/fluxplane-core`. Keep background, migration decisions,
and explanatory material in [docs/](docs/README.md), not here.

## Worktree Rules

- Never commit unless explicitly asked.
- Never run destructive git commands (`git reset`, `git clean`,
  `git checkout --`, force pushes).
- Use conventional commit messages with a body: `feat: short summary`, blank
  line, then why/what changed. Pick the right type (`feat`, `fix`, `docs`,
  `test`, `chore`, `refactor`, ...).
- Update `CHANGELOG.md` in the same commit for user-visible changes,
  documentation changes, removals, or release-affecting work unless explicitly
  told to skip it.
- This is a pre-1.0 rewrite: no backward compatibility, compat shims,
  deprecated wrappers, or stale shapes.
- `fluxplane` is the generic app CLI here. The `coder` product lives in
  `github.com/fluxplane/coder`. Do not add/preserve `agentsdk` binary
  compatibility. The app manifest filename is `fluxplane.yaml`.

## Verification

- Run focused checks while iterating, such as
  `go test ./runtime/conversation ./orchestration/session` or the touched
  package set.
- Do not run `task verify` routinely; it is expensive and also runs from the
  Git hook path when committing. Use it only when explicitly requested, when
  preparing a commit, or when the change is broad enough for the full local
  gate.
- `task verify` is the rewrite-local full gate: format, modules, whitespace,
  vet, lint, tests, and the codegate Go quality gate.
- `engine-architecture.rules.json` is the hard Go policy for architecture
  boundary, side-effect, and unknown-package violations. Zero hard violations
  are required.
- See [docs/verification.md](docs/verification.md) for hooks, security scan,
  and codegate review commands.

## Live Testing

When asked to live-test `coder`, use `github.com/fluxplane/coder`; this engine
repository no longer contains the coder product module.

## Layer Rules

Dependency direction is fixed and enforced by `engine-architecture.rules.json`:

```text
cmd -> apps -> {plugins, adapters} -> orchestration -> runtime -> core
sdk -> core
facade (root module) -> {core, sdk, runtime, orchestration, adapters}
```

Outer layers may depend inward; inner layers must not depend outward. The exact
allowed-import matrix lives in `engine-architecture.rules.json`.

| Layer | Purpose | May import | Key constraints |
|---|---|---|---|
| `core` | Stable system shape | `core` | Inert specs/events/refs/descriptors/policies/registries only; no IO, execution, or rendering. |
| `sdk` | IO-free authoring sugar | `core` | Imported only by `plugins`, `apps`, and root facade. |
| `runtime` | Execute/store core contracts | `core`, `runtime` | Surface-neutral; no CLI/HTTP/Slack presentation; avoid sibling runtime imports without clear reason. |
| `orchestration` | Compose runtime use cases | `core`, `runtime`, `orchestration` | Session, command dispatch, plugin contribution resolution, daemon lifecycle; no protocol wire formats or terminal rendering. |
| `adapters` | External IO/protocol boundary | `core`, `runtime`, `orchestration`, `adapters` | Translate external systems in and events/results out; no new domain concepts. |
| `plugins` | Optional first-party bundles | `core`, `sdk`, `runtime`, `orchestration`, `adapters`, `plugins` | Contribute through core/orchestration contracts; no global app assembly. |
| `apps` | Assembled products/default sets | `core`, `sdk`, `runtime`, `orchestration`, `adapters`, `plugins`, `apps`, root facade | No reusable domain/runtime concepts. |
| `cmd` | Executable entrypoints | `apps`, `adapters`, `cmd` (and stdlib) | Glue only; adapter import allowed for distribution CLIs; no feature logic or plugin assembly. |
| `facade` | Root `fluxplane` embedding API | `core`, `sdk`, `runtime`, `orchestration`, `adapters` | Assembles outward-facing in-process products; inner packages must not import it. |

See [docs/architecture.md](docs/architecture.md) for per-layer concepts,
common flows, and report posture.

### Architecture References

Keep this file operative. Put explanations in:

- [docs/architecture.md](docs/architecture.md): layers, responsibilities,
  common flows, architecture reports.
- [docs/agent-loop.md](docs/agent-loop.md): session loop, continuation,
  compaction, transcripts.
- [docs/security.md](docs/security.md): side effects, operation safety, system
  boundaries.
- [docs/verification.md](docs/verification.md): `task verify`, hooks,
  codegate review commands, quality gates.
- [docs/migration-from-agent-sdk.md](docs/migration-from-agent-sdk.md):
  migration rationale, fitness-function notes, package status.
- [docs/concepts.md](docs/concepts.md): vocabulary for requests, tasks,
  commands, operations, workflows, executions/runs.

## Placement Checklist

When placing new code, walk top-down and stop at the first match. When
extracting from the old SDK, split by responsibility; do not copy packages
wholesale just because names match.

```text
Authoring sugar for inert specs, no IO/execution?              -> sdk
Pure spec, event, ID, ref, descriptor, policy, or registry?    -> core
Concrete execution/storage implementation of a core contract?  -> runtime
Use-case flow composing runtime pieces?                        -> orchestration
IO/protocol boundary: fs, terminal, HTTP, Slack, provider,
  SQL, browser, shell, CLI?                                    -> adapters
Core-bundled contribution provider?                            -> contrib
Assembled product, distribution, or default set?                -> apps
Executable main package only?                                  -> cmd
```

## Concept Rules

Use [docs/concepts.md](docs/concepts.md). If a change blurs concepts, split the
shapes instead of using vague packages.

When extracting reusable concepts into sibling modules, avoid broad packages named
`common`, `coretypes`, or `contracts`. Each extracted module must answer one
clear question (for example: event = what is an event, system = host capability
boundary, user = who is the actor, policy = may this subject do this action on
this resource, operation = what is callable, datasource = what data is
searchable/gettable, app = what is an authored Fluxplane app). If the module
name cannot be explained that way, it probably should not exist yet.

- `Request`: boundary ask from user/model/protocol/provider. Keep DTOs near
  adapters/orchestration unless they are stable model-facing contracts; map
  them to canonical messages, `command.Invocation`s, operation inputs, or
  workflow/session submissions.
- `Task`: work objective with lifecycle, assignment, or acceptance criteria.
  Use `core/task` only for durable task lifecycle/work-objective contracts; do
  not use `task` for tool calls, commands, or process runs. Runtime task
  projection/execution state belongs under `runtime/task` and
  `orchestration/taskexecutor`, not `core/task`.
- `Command`: parsed imperative control instruction with a known handler.
  `core/command` owns syntax/invocation contracts; UI/adapters do not own
  command semantics.
- `Operation`: callable capability/tool contract. `fluxplane-operation` owns
  specs and schemas; runtime/plugins/adapters implement execution behind
  safety.
- `Workflow`: multi-step process shape. `core/workflow` stays inert;
  runtime/orchestration owns runs and state transitions.
- `Execution`/`Run`: one runtime attempt. Do not put execution state in `Spec`
  types or create broad `core/execution` abstractions unless a stable
  cross-runtime record is required.

Quick gate: is the thing a boundary ask, work objective, imperative control,
callable capability, process spec, or runtime attempt? If several apply, model
separate parts.

## Operations and Safety

- All side-effecting operations enter through
  `runtime/operation.SafetyEnvelope`; no shell, filesystem, network, browser,
  or code execution path may bypass it.
- Reusable plugins use explicit capabilities such as
  `github.com/fluxplane/fluxplane-system.System`, `runtime/workspace.Workspace`,
  process, browser, and human clarification boundaries. Do not import/call `os`,
  `os/exec`, `syscall`, `net`, `net/http`, or `net/url` directly unless the
  package implements an approved system adapter.
- Preserve the managed process boundary for stdout/stderr streaming,
  background handles, and per-session cleanup.
- Prefer `runtime/operation.NewTyped` or `NewTypedResult`. Use input/output
  structs with `json` and `jsonschema` tags so typed helpers generate
  `operation.Type` JSON Schema.
- Do not hand-craft JSON Schema raw strings. For shapes reflection cannot
  express, use structured schema helpers or typed `JSONSchema()` methods near
  the Go input type. Keep runtime validation aligned with advertised schemas;
  core schemas are model-facing contracts, not enforcement.

See [docs/security.md](docs/security.md) for the full safety model.

## Commands

- UI/adapters translate input into canonical submissions; they do not validate
  domain semantics.
- Parse command syntax once at the command boundary and carry a structured
  `command.Invocation`.
- Dispatch through specs, registries, and `Target.Kind`, not hard-coded path
  checks.
- Built-ins use the same registry/handler model as contributed commands.
- Backend/session dispatch owns validation, policy evaluation, and execution
  semantics.
- Typed command input uses structs and binders, not scattered
  `map[string]any` parsing.
- CLI flags may choose submission shape, but not semantic validity.
- Harness code may inspect command metadata for routing, but must not turn
  pre-routing lookup failures into execution failures.
- Terminal UI must not implement command-specific parsers while `core/command`
  owns parsing.
- Apply defaults at the owning semantic layer, not hidden in transport or
  presentation adapters.

## Plugin Contributions

- Plugins contribute optional bundles (specs, operations, context providers,
  channels, datasource providers, auth methods) through `core` and
  `orchestration` contracts.
- Plugin contracts belong in `core` or `orchestration`, never in concrete
  plugin implementation packages.
- Plugins may depend on adapters only when the adapter is part of that plugin's
  concrete implementation boundary.
- Apps select plugins explicitly. There are no hidden default bundles.

## Distributions vs Apps

- A distribution is a runnable package of defaults, metadata, bundled
  resources, and supported surfaces; describe it in `core/distribution`.
- An `app.Spec` is resource-authored configuration inside a bundle, not a
  distribution.
- Distribution loading/runtime handles live in `orchestration/distribution`.
  CLI/local/remote/describe helpers live under `adapters/distribution/*`.
  Concrete generic CLI assembly lives in `apps/fluxplane` and `apps/launch`;
  specialized app assemblies such as `apps/evaluator` stay under `apps`, and
  product repositories such as `github.com/fluxplane/coder` assemble their own
  distributions.

## Channel HTTP/SSE vs Daemon Control HTTP

- Keep channel HTTP/SSE (user/agent message transport) separate from
  daemon/control HTTP (daemon lifecycle, health, admin).
- Use separate adapter packages; do not collapse them, share routers, or let
  either reach into session internals. Both enter through
  `orchestration/harness`.

## Naming Rule

- Use layer names for top-level directories and concept names below them.
  Good: `core/workflow`, `runtime/agent`, `orchestration/session`,
  `adapters/ui/terminal`, `plugins/git`, `apps/builder`.
  Avoid: `core/misc`, `runtime/common`, `orchestration/utils`,
  `plugins/standard`.
- Use `Spec` for inert user-authored/resource-authored configuration shapes:
  `agent.Spec`, `operation.Spec`, `environment.Spec`, `workflow.Spec`,
  `context.ProviderSpec`.
- Reserve `Definition` for a distinct normalized, validated, executable
  internal form. Do not use it as a synonym for `Spec`.
