# Fluxplane Engine Agent Notes

This file is for AI agents and developers working in
`github.com/fluxplane/engine`. It carries the operative rules only.
Background lives in:

- [docs/architecture.md](docs/architecture.md): full layer model, package
  responsibilities, and common flows.
- [docs/security.md](docs/security.md): full safety model and roadmap.
- [docs/verification.md](docs/verification.md): quality gate, Git hooks, and
  architecture report commands.
- [docs/migration-from-agent-sdk.md](docs/migration-from-agent-sdk.md):
  migration rationale and historical decisions.

Do not put migration decision logs in this file.

## Worktree Rules

- Never commit unless explicitly asked.
- Never run destructive git commands (`git reset`, `git clean`,
  `git checkout --`, force pushes).
- Commit messages are conventional with a body: `feat: short summary`, blank
  line, then a short body explaining what changed and why. Use the
  appropriate type (`feat`, `fix`, `docs`, `test`, `chore`, `refactor`, ...).
- Update `CHANGELOG.md` in the same commit for user-visible changes,
  documentation changes, removals, or release-affecting work, unless the user
  explicitly says to skip it.
- This is a pre-1.0 rewrite. No backward compatibility, no compat shims, no
  deprecated wrappers. Replace stale shapes; do not preserve them.
- `coder` is the product CLI and main entrypoint. Do not add or preserve
  `agentsdk` binary compatibility. `fluxplane.yaml` is the app manifest
  filename.

## Verification

Run focused checks while iterating. Do not run `task verify` after every small
change or as a routine approval gate; it is expensive and also runs from the
Git hook path when committing. Run `task verify` only when explicitly requested,
when preparing a commit, or when the change is broad enough that the full local
quality gate is justified.

When a narrower command covers the edited area, prefer that instead, for
example `go test ./runtime/conversation ./orchestration/session` or the
specific package set touched by the change. `task verify` remains the
rewrite-local full quality gate (format, modules, whitespace, vet, lint, tests,
architecture check). See [docs/verification.md](docs/verification.md) for
hooks, the security scan, and architecture report invocations.

The architecture test in `internal/architecture` is the hard boundary check.
Zero violations is required.


## Live Testing

When asked to "live-test" coder behavior, run the actual coder binary path without
rediscovering CLI defaults:

```bash
task coder:live-test -- "prompt describing the scenario"
```

This task runs the nested coder module with `CODER_ROOT` pointed at the engine
checkout, equivalent to `cd apps/coder && CODER_ROOT=<repo> go run ./cmd/coder
--provider codex --model gpt-5.5 --yolo --debug --input ...`. The default
OpenAI provider usually lacks credentials in agent workspaces, so do not use
the default provider for live-test requests unless explicitly asked. Inspect
the terminal output as well as debug JSON when checking
rendering behavior; model summaries may omit structured operation data.
## Layer Rules

The dependency direction is fixed and enforced by `apps/archreport` and
`internal/architecture`:

```text
cmd -> apps -> {plugins, adapters} -> orchestration -> runtime -> core
sdk -> core
facade (root module) -> {core, sdk, runtime, orchestration, adapters}
```

Outer layers may depend on inner layers; inner layers must not depend on
outer layers. The exact allowed-import matrix is defined in
`internal/architecture/model.go` (`allowedImport`).

| Layer | Answers | May import | Notes |
|---|---|---|---|
| `core` | What is the stable shape of the agent system? | `core` | Inert specs, events, refs, descriptors, policies, registries. No IO, no execution, no rendering. |
| `sdk` | How do users author inert specs conveniently? | `core` | IO-free authoring sugar. Imported by `plugins`, `apps`, and the root facade only. |
| `runtime` | How are core contracts executed or stored? | `core`, `runtime` | Surface-neutral execution and storage. No CLI/HTTP/Slack presentation. Avoid sibling runtime imports without a clear reason. |
| `orchestration` | Which runtime pieces are combined for a use case? | `core`, `runtime`, `orchestration` | Session lifecycle, command dispatch, plugin contribution resolution, daemon lifecycle. No protocol wire formats, no terminal rendering. |
| `adapters` | How does the outside world talk to the runtime? | `core`, `runtime`, `orchestration`, `adapters` | IO and protocol boundaries. Translate external systems in and events/results back out. No new domain concepts. |
| `plugins` | Which optional first-party capability bundle is contributed? | `core`, `sdk`, `runtime`, `orchestration`, `adapters`, `plugins` | Optional capability bundles via core/orchestration contracts. No global app assembly. |
| `apps` | What product was assembled? | `core`, `sdk`, `runtime`, `orchestration`, `adapters`, `plugins`, `apps`, root facade | Assembled products and dogfood apps. No reusable domain or runtime concepts. |
| `cmd` | How is an assembled product launched as a process? | `apps`, `adapters`, `cmd` (and stdlib) | Executable entrypoint glue only. Adapter import is allowed for distribution-CLI launchers; no feature logic, no plugin assembly. |
| `facade` (root module) | In-process embedding entrypoint. | `core`, `sdk`, `runtime`, `orchestration`, `adapters` | The root `agentruntime` package. Assembles outward-facing in-process products. Inner packages must not import it. |

See [docs/architecture.md](docs/architecture.md) for per-layer concept lists
and common flows.

### Architecture References

Keep this file to the operative rules. Put explanatory architecture updates in
the docs:

- [docs/architecture.md](docs/architecture.md): layer model, package
  responsibilities, common flows, and expected architecture report posture.
- [docs/agent-loop.md](docs/agent-loop.md): session execution loop,
  continuation loop, compaction, and transcript flow.
- [docs/security.md](docs/security.md): side-effect, operation safety, and
  system boundary model.
- [docs/verification.md](docs/verification.md): `task verify`,
  architecture report, hooks, and quality gates.
- [docs/migration-from-agent-sdk.md](docs/migration-from-agent-sdk.md):
  migration rationale, architecture fitness-function notes, and package status.

## Placement Checklist

When placing new code, walk this list top-down and stop at the first match.
This subsumes the migration rule: when extracting from the old SDK, identify
each concept and place it independently using this list.

```text
Authoring sugar for inert specs, no IO, no execution?
  -> sdk

Pure spec, event, ID, ref, descriptor, policy, or registry over them?
  -> core

Concrete execution or storage implementation of a core contract?
  -> runtime

Use-case flow that composes runtime pieces (session, dispatch, lifecycle)?
  -> orchestration

IO or protocol boundary (filesystem, terminal, HTTP, Slack, provider, SQL,
browser, shell, CLI)?
  -> adapters

Optional first-party capability bundle contributed through core/orchestration
contracts?
  -> plugins

Assembled product, distribution, or default set?
  -> apps

Executable main package only?
  -> cmd
```

Do not copy old packages wholesale just because names match. Split by
responsibility using this list.

## Concept Rules

Concept rules cut across layers. They constrain how specific concepts are
shaped and where their logic lives, regardless of which layer the code is
in.

### Concept Vocabulary

Use the vocabulary in [docs/concepts.md](docs/concepts.md) when naming or
placing new code. If a change blurs two concepts, split the shapes instead of
choosing a vague generic package.

- A `Request` is a boundary ask from a user, model, protocol, or provider.
  Keep request DTOs near adapters/orchestration unless they are stable
  model-facing contracts. Translate requests into canonical messages,
  `command.Invocation`s, operation inputs, or workflow/session submissions.
- A `Task` is a work objective with lifecycle, assignment, or acceptance
  criteria. Do not use `task` to mean a tool call, command, or process run.
  Add a first-class `core/task` only if durable task lifecycle is actually
  needed across packages.
- A `Command` is a parsed imperative control instruction with a known handler.
  `core/command` owns command syntax/invocation contracts; UI/adapters do not
  own command semantics.
- An `Operation` is a callable capability/tool contract. `core/operation`
  owns specs and schemas; runtime/plugins/adapters implement execution behind
  the safety envelope.
- A `Workflow` is a multi-step process shape. `core/workflow` stays inert;
  runtime/orchestration owns workflow runs and state transitions.
- An `Execution` or `Run` is one runtime attempt. Do not put execution state in
  `Spec` types. Do not create broad `core/execution` abstractions unless a
  stable cross-runtime record is required.

Quick gate before adding a concept: ask whether it is a boundary ask, work
objective, imperative control instruction, callable capability, process spec,
or runtime attempt. If several answers apply, model those parts separately.

### Operations and Safety

- All side-effecting operations enter through
  `runtime/operation.SafetyEnvelope`. No shell, filesystem, network,
  browser, code execution, or connector path may bypass it.
- Reusable plugins use `runtime/system.System` for filesystem, network,
  process, browser, and human-clarification access. Do not import or call
  `os`, `os/exec`, `syscall`, `net`, `net/http`, or `net/url` directly
  unless the package itself implements a `System` adapter.
- Process operations preserve the managed process boundary so stdout/stderr
  streaming, background process handles, and per-session cleanup stay
  centralized.
- Prefer `runtime/operation.NewTyped` or `NewTypedResult`. Define
  input/output structs with `json` and `jsonschema` tags and let the typed
  helper generate the `operation.Type` JSON Schema. Hand-written schema
  strings are only for shapes reflection cannot express cleanly.
- Do not hand-craft JSON Schema as raw strings. For shapes reflection cannot
  express, use structured schema helpers or typed `JSONSchema()` methods near
  the Go input type, and keep runtime validation aligned with the advertised
  schema because core schemas are model-facing contracts, not enforcement.

See [docs/security.md](docs/security.md) for the full safety model.

### Commands

- UI/adapters translate user input into canonical submissions and do not
  validate domain semantics.
- Command syntax is parsed once at the command boundary and transported as a
  structured `command.Invocation`.
- Command behavior is dispatched through specs, registries, and
  `Target.Kind`, not hard-coded path checks.
- Built-in commands use the same registry and handler model as contributed
  commands.
- Backend/session dispatch owns command validation, policy evaluation, and
  execution semantics.
- Typed command input uses structs and binders, not ad hoc `map[string]any`
  parsing spread across layers.
- CLI flags may choose the submission shape, but must not decide whether
  that submission is semantically valid.
- Harness code may inspect command metadata for routing, but must not turn
  pre-routing lookup failures into execution failures.
- Terminal UI must not implement command-specific parsers when
  `core/command` owns parsing.
- Defaults are applied at the owning semantic layer, not hidden in transport
  or presentation adapters.

### Plugin Contributions

- A plugin contributes optional capability bundles (specs, operations,
  context providers, channels, datasource providers, connector providers)
  through `core` and `orchestration` contracts.
- Plugin contracts belong in `core` or `orchestration`, never in a concrete
  plugin implementation package.
- Plugins may depend on adapters only when the adapter is part of that
  plugin's concrete implementation boundary.
- Apps select plugins explicitly. There are no hidden default bundles.

### Distributions vs Apps

- A distribution is a runnable package of defaults, metadata, bundled
  resources, and supported surfaces. It is described in
  `core/distribution`.
- An `app.Spec` is resource-authored configuration inside a bundle. It is
  not a distribution.
- Distribution loading and runtime handles live in
  `orchestration/distribution`. CLI/local/remote/describe helpers are
  adapters under `adapters/distribution/*`. Concrete assembly lives in
  `apps/launch` (and products like the nested `apps/coder` module).

### Channel HTTP/SSE vs Daemon Control HTTP

- The channel HTTP/SSE surface (user/agent message transport) and the
  daemon/control HTTP surface (daemon lifecycle, health, admin) are
  conceptually separate and live in separate adapter packages.
- Do not collapse them, share routers across them, or let either reach into
  session internals; both enter through `orchestration/harness`.

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

Use `Spec` for inert, user-authored, or resource-authored configuration
shapes:

```text
agent.Spec
operation.Spec
environment.Spec
workflow.Spec
context.ProviderSpec
```

Reserve `Definition` for a distinct normalized, validated, executable
internal form if the codebase later needs to distinguish authored config
from prepared runtime form. Do not use `Definition` as a synonym for `Spec`.
