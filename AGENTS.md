# Fluxplane Agent Runtime Agent Notes

This file is for AI agents and developers working in
`github.com/fluxplane/agentruntime`.

For migration rationale, package disposition, historical decisions, and current
rewrite milestones, use [docs/migration-from-agent-sdk.md](docs/migration-from-agent-sdk.md).
For the current security model and roadmap, use [docs/security.md](docs/security.md).
Do not put migration decision logs in this file.

## Worktree Rules

- Never commit unless explicitly asked.
- Never run destructive git commands such as `git reset`, `git clean`,
  `git checkout --`, or force pushes.
- When committing, use semantic/conventional commit messages with a body:
  `feat: short summary`, blank line, then a short body explaining what changed
  and why. Use the appropriate type (`feat`, `fix`, `docs`, `test`, `chore`,
  `refactor`, and so on).
- Before committing user-visible changes, documentation changes, removals, or
  release-affecting work, update `CHANGELOG.md` in the same commit unless the
  user explicitly says to skip it.
- The generated architecture render directory `.agents/architecture/` is
  ignored. Regenerate it when needed.
- This is a pre-1.0 rewrite. Do not add backward-compatibility shims,
  deprecated wrappers, or compatibility paths unless explicitly requested.
- We do not care about backward compatibility. Prefer the clean current design
  and delete or replace stale shapes instead of preserving old behavior by
  default.

## Verification

Use the rewrite-local quality gate:

```bash
task verify
```

`task verify` runs formatting checks, module consistency checks, whitespace
checks, `go vet`, `golangci-lint`, tests, and the architecture import check.

New clones should enable the tracked Git hooks:

```bash
task hooks:install
```

The tracked pre-commit hook runs the staged security scan and staged whitespace
check. The tracked pre-push hook runs the full security scan and `task verify`.

Do not run the old repository root CI for rewrite work unless explicitly
asked.

Architecture reports:

```bash
go run ./apps/archreport
go run ./apps/archreport -format json
go run ./apps/archreport -format dot
go run ./apps/archreport -format mermaid
task arch:render
```

The architecture test is the hard boundary check. The report is a review tool
for coupling, fan-in/fan-out, and refactoring decisions.

## Dependency Direction

The top-level layers are:

```text
core -> runtime -> orchestration -> adapters/plugins -> apps
core -> sdk -> apps/user code
```

Dependencies must point inward. Outer layers may depend on inner layers; inner
layers must not depend on outer layers.

`apps` and the root facade may assemble outward-facing products. Inner packages
must not import apps.

`sdk` is an IO-free authoring convenience layer. It may depend on `core` only
and emits specs/contribution bundles. It must not execute operations, open
sessions, instantiate model providers, inspect files, or import runtime,
orchestration, adapters, plugins, or apps.

LLM provider/model availability, pricing, APIs, and capability metadata should
come from `github.com/codewandler/modeldb` through an adapter bridge. Do not
hand-maintain broad model lists in core or app packages.

## `core/`

`core` is the inner domain/kernel layer.

Allowed:

- value objects, IDs, refs, names, descriptors, policies, events, and result
  types;
- inert `Spec` types authored by users, resources, or apps;
- pure builders, validation, and normalization;
- small interfaces only when the inner model genuinely needs a port;
- pure registries over specs or core port interfaces.

Forbidden:

- filesystem, environment, process, terminal, network, HTTP, Slack, browser,
  database, JSONL, SQLite, model-provider clients, goroutine hosts, or daemon
  lifecycle;
- concrete plugin discovery;
- rendering;
- persistence implementation details;
- imports from `runtime`, `orchestration`, `plugins`, `adapters`, or `apps`.

Core should answer: "What is the stable shape of this agent system?"

## `runtime/`

`runtime` contains concrete implementations of core contracts and execution
mechanics that are still surface-neutral.

Allowed:

- agent turn engine implementations;
- operation executors and middleware;
- context materialization engines;
- event projection runners and stores;
- surface-neutral persistence implementations.

Forbidden:

- CLI/terminal presentation;
- HTTP/SSE or Slack protocol handling;
- app/product assembly;
- plugin selection policy;
- filesystem resource discovery policy.

Runtime packages should depend on `core`. Avoid sibling runtime imports unless
there is a clear local reason; larger composition usually belongs in
`orchestration`.

Runtime should answer: "How is this core concept executed or stored?"

## `orchestration/`

`orchestration` is the application/use-case layer. It composes core definitions
and runtime implementations into higher-level flows.

Allowed:

- session lifecycle;
- command dispatch against a live session;
- workflow or trigger use cases;
- resource-to-app composition after adapters have loaded resources;
- event fanout and read-model coordination;
- plugin contribution resolution at the abstract plugin contract level.

Forbidden:

- terminal rendering;
- protocol wire formats;
- provider-specific transport code;
- concrete first-party plugin internals;
- filesystem crawling details.

Orchestration should answer: "Which runtime pieces are combined to fulfill a
user/application use case?"

## `adapters/`

`adapters` contains IO and protocol boundaries. Adapters translate the outside
world into core/orchestration requests and translate events/results back out.

Allowed:

- filesystem resource loading and discovery;
- compatibility format readers;
- terminal, HTTP/SSE, Slack, browser, shell, and process boundaries;
- concrete filesystem, SQL, JSONL, or protocol-backed stores.

Forbidden:

- new domain concepts;
- session semantics that bypass orchestration;
- plugin contribution types that should be inner contracts.

Adapters should answer: "How does an outside system talk to the runtime?"

Keep channel HTTP/SSE and daemon/control HTTP conceptually separate.

## `plugins/`

`plugins` contains first-party plugin implementations. A plugin contributes
optional capability bundles through core/orchestration contracts.

Allowed:

- concrete plugin packages such as git, code, browser, skills, memory, plan
  executor, datasources, and local CLI support;
- plugin-specific adapters when tightly scoped to that plugin;
- plugin tests and fixtures.

Forbidden:

- global app assembly;
- hidden default bundles;
- core abstractions used by unrelated packages.

Plugins should answer: "Which optional capability bundle is being contributed?"

## `apps/`

`apps` contains assembled products and dogfood apps.

Allowed:

- first-party apps;
- product-specific defaults;
- app-specific embedded resources;
- app-specific composition tests.

Forbidden:

- reusable domain model;
- reusable runtime semantics that should live in inner layers.

Apps should answer: "What product did we assemble from the runtime and SDK?"

## Safety Rule

When operation implementations move beyond pure/in-memory examples, implement
them safety-first. Do not add shell, filesystem, network, browser, code
execution, or connector operations as plain function calls and retrofit safety
later.

The first real operation runtime batch must include the enforcement shape for
sandboxing, ACL/scope checks, command-risk classification
(`codewandler/cmdrisk` or successor), secret handling/redaction, approval
requirements, audit events, and environment boundaries.

Current concrete side-effecting operations must enter through
`runtime/operation.SafetyEnvelope`. The first-party coder host wires
`adapters/cmdrisk` for shell and structured network intent assessment and keeps
operation-local checks as defense in depth. Do not add a new shell, filesystem,
network, browser, code execution, or connector path that bypasses the safety
envelope.

Standard operation implementations must also use `runtime/system.System` for
filesystem, network, process, browser, and human-clarification access. Do not
import or call `os`, `os/exec`, `syscall`, `net`, `net/http`, or `net/url`
directly from a reusable standard plugin unless the package is itself
implementing a `System` adapter. Process operations must preserve the managed
process boundary so stdout/stderr streaming, background process handles, and
per-session cleanup can be enforced centrally.

Prefer `runtime/operation.NewTyped` or `NewTypedResult` for new operation
implementations. Define input/output structs with `json` and `jsonschema` tags
and let the typed helper generate `operation.Type` JSON Schemas. Avoid
hand-written schema strings unless a type needs a custom union or discriminator
shape that reflection cannot express cleanly.

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

Use `Spec` for inert, user-authored, or resource-authored configuration shapes:

```text
agent.Spec
operation.Spec
environment.Spec
workflow.Spec
context.ProviderSpec
```

Reserve `Definition` for a distinct normalized, validated, executable internal
form if the codebase later needs to distinguish authored config from prepared
runtime form. Do not use `Definition` as a synonym for `Spec`.

## Migration Rule

Do not copy packages wholesale just because names match. For every old package,
identify the concepts inside it and split by responsibility:

- pure model/spec/event data -> `core`;
- execution implementation -> `runtime`;
- use-case composition -> `orchestration`;
- IO/protocol/loading/rendering -> `adapters`;
- optional first-party contribution bundle -> `plugins`;
- assembled product -> `apps`.
