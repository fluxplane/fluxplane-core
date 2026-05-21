# DESIGN: Go Plugin Actions

## Status

Implemented first read-only slice plus Go navigation, references, import views,
implementation lookup, and direct call hierarchy. Updated direction: no Axon
dependency for project or Go language support. Follow-up scalability work for
language-specific contracts, toolchain activation, events, and coder capability
projection is tracked in
[`2026-05-16-language-toolchain-activation.md`](../designs/2026-05-16-language-toolchain-activation.md).
Generic code-editing and refactoring direction has moved to
[`2026-05-16-1425-code-edit.md`](../designs/2026-05-16-1425-code-edit.md).
Generic project task execution is tracked separately from the Go plugin in
[`2026-05-16-project-task-execution.md`](../designs/2026-05-16-project-task-execution.md).
The broader user-facing task/execution domain for future plan execution lives
in [`2026-05-16-core-task-domain.md`](../designs/2026-05-16-core-task-domain.md).

Current implementation status:

| Area | Status |
|---|---|
| Project inventory/read ops | Implemented |
| `project.summary`, `go.summary` | Implemented |
| Markdown outline/link/diagnostics plugin | Implemented |
| Go project/package/outline/symbol ops | Implemented |
| `go_definition`, `go_symbol_info` | Implemented |
| `go_references` | Implemented; package scope is constrained to exact package directory/package ID |
| `go_imports` | Implemented; parser-only direct and reverse import edges |
| `go_implementations` | Implemented; host workspaces prefer `go/packages` + `go/types` type-checked matching with AST fallback |
| `go_callers` / `go_callees` | Implemented and exposed to coder delegation; AST-only direct calls with package/module scope and explicit limitations |
| `go_info`, `go_env`, `go_version` | Implemented; read-only process-backed Go toolchain orientation |
| `go_doc`, `go_list` | Implemented; process-backed docs plus structured `go list -json` package/module metadata |
| `go_test`, `go_vet`, `go_build` | Implemented; process-backed checks return structured pass/fail summaries and diagnostics |
| `go_fmt` | Implemented; defaults to dry-run and supports explicit formatting writes |
| `go_install` | Implemented; defaults to dry-run with restricted environment overrides |
| Follow-up | Language/toolchain activation design in `2026-05-16-language-toolchain-activation.md` implemented |
| Project task execution | Implemented in `projectplugin` as `project_task_run`; intentionally outside the Go plugin |
| Core task domain | Implemented as a foundation for future `planexecplugin` migration |
| Deferred | Abstract code-editing/refactor operations tracked separately in `2026-05-16-1425-code-edit.md` |

## Current Progress

Completed:

- Go project/package/outline/symbol discovery.
- Position-based definition, symbol info, references, imports,
  implementations, callers, and callees.
- Type-aware `go_implementations` for host workspaces, with clean AST fallback
  for virtual workspaces and method-correspondence lookups.
- Type-checked zero-match implementation results are authoritative and do not
  fall back to AST name matching.
- Read-only process-backed `go_info`, `go_env`, and `go_version` operations.
- Process-backed `go_doc` and `go_list` operations, including position-derived
  documentation lookup and structured `go list -json` parsing.
- Process-backed `go_test`, `go_vet`, and `go_build` checks with bounded output
  and structured summaries/diagnostics.
- Explicit `go_fmt` formatting with dry-run default and real formatting when
  requested.
- Explicit `go_install` with dry-run default and restricted environment
  overrides.
- Coder delegation exposes `go_callers` and `go_callees`.
- Language/toolchain activation follow-up is implemented, including separated
  Go/Markdown contracts, project signals, toolchain specs/statuses, activation
  events, and coder feature expansion. Review fixes ensure bounded inventories
  do not leak signals for omitted projects and binary-free toolchain specs do
  not require process support.
- Workspace-scoped project task execution is implemented in `projectplugin`
  with stable task IDs, dry-run command resolution, managed process execution,
  and coder exposure.
- The neutral `core/task` and `runtime/task` foundation is implemented for
  future plan execution migration.
- Generic code editing/refactoring scope is split into
  `2026-05-16-1425-code-edit.md`.

Remaining:

- No implementation tasks remain for the current Go-only roadmap slice.
- Type-aware `go_callers` / `go_callees` is not required for this roadmap slice;
  current operations intentionally remain AST-only and report limitations.
- Scaling beyond Go/Markdown should reuse the implemented
  `2026-05-16-language-toolchain-activation.md` pattern.

Next steps:

1. Add the next language/toolchain plugin using the implemented activation
   pattern, continue with the separate abstract code-editing design, or expand
   generic project task runner metadata outside the Go plugin.

## Summary

Add generic project inventory and language-support models, then add
`projectplugin` and `golangplugin` as the first read-only implementations.
`projectplugin` owns workspace facts such as manifests, Makefiles, Taskfiles,
project task execution, and markdown document outlines. `golangplugin` owns Go-specific source
structure such as modules, packages, outlines, and declaration symbols.
`codingplugin` also contributes compact automatic context providers so new
turns receive project and Go orientation before the model asks for a tool.
Markdown gets its own language-support plugin for accurate document outlines,
link listing, and local-only diagnostics.

The first version is Workspace-native and does not use Axon. Project inventory
remains process-local memory only. Go operations should read through
`runtime/system.Workspace` unless a specific semantic backend requires host Go
tooling; today that exception is `go_implementations`, which can use
`go/packages` / `go/types` for host workspaces and falls back to parser-only
matching when semantic loading is unavailable.

## Motivation

The coding toolset has good raw filesystem and grep primitives, but Go work
still requires too much manual line-range probing and text search. Common agent
questions are structural:

- Which Go modules and packages are in this workspace?
- What declarations are in this file?
- Where is this symbol defined?
- Who references or calls this function?
- Which package imports another package?
- What should I read before editing this code?

`codeplugin` currently runs scratch code in containers. `codingplugin`
aggregates filesystem, shell, git, browser, web, scratch code, and human tools.
Neither owns language-specific project understanding. A dedicated
`golangplugin` keeps Go semantics separate from generic file editing and can be
added to `codingplugin` once the API is stable.

## Axon Inspiration Only

`github.com/codewandler/axon` is already in the module graph for embeddings,
but its current project/Go indexers are host-filesystem oriented. The runtime
direction here is different: all plugin IO must go through
`runtime/system.Workspace`, and the project inventory is process-local memory
only.

Concepts worth borrowing from Axon:

- filesystem tree indexing;
- project detection around resources such as `go.mod`;
- markdown document/section indexing;
- Go module/package/symbol indexing triggered from `go.mod`;
- Go node types for modules, packages, structs, interfaces, funcs, methods,
  fields, consts, vars, and references;
- reference and call edges;
- bounded graph-like relationships between projects, packages, symbols, docs,
  references, and calls.

The implementation does not expose raw Axon graph nodes and does not depend on
Axon for indexing.

## Project And Language Split

`core/project` defines inert workspace inventory DTOs: projects, facets,
manifests, tasks, document outlines, warnings, and query shapes.

`runtime/project.Manager` scans a `system.Workspace` into a memory-only
inventory. It discovers multiple project roots. A directory with multiple
manifests, such as `go.mod` plus `package.json`, is one project with multiple
facets. Nested manifests become child projects. `go.work` is a Go workspace
facet and links related module directories when cheaply parseable. Agent
resource directories such as `.agents` and `.claude` are also project facets on
their owning project, not separate project roots. Markdown document outlines are
parsed with goldmark and stored as nested heading trees so fenced code,
Setext headings, and inline markup are handled by a real Markdown parser.

`plugins/projectplugin` exposes project inventory operations:

- `project_inventory`
- `project_files`
- `project_tasks`
- `project_docs`
- `project.summary` context provider with a compact project/facet/docs/tasks
  orientation block

`core/language` defines inert language DTOs: provider metadata, positions,
ranges, packages, documents, outlines, symbols, imports, references, and call
edges.

## First Plugin Shape

Create `plugins/golangplugin` with `Name = "golang"` and an operation set named
`golang`. The plugin depends on `runtime/system.System` for workspace access and
uses `go/parser`, `go/ast`, `go/token`, `golang.org/x/mod/modfile`, and targeted
`go/packages` / `go/types` loading where semantic Go answers require type
identity. It does not use Axon, `os`, or direct host filesystem walking for the
Workspace-native parser paths.

Initial operations:

- `go_project`: summarize Go modules and `go.work` workspaces from project
  inventory.
- `go_packages`: list packages for a module or path, including package name,
  import path, directory, file count, direct imports, and test package metadata.
- `go_outline`: return a bounded outline for a Go file or package: types,
  funcs, methods, consts, vars, line positions, signatures, docs when small.
- `go_symbol`: find declaration symbols by name, package, kind, or file path.
- `go_definition`: AST/package-level declaration lookup from a source position.
- `go_symbol_info`: compact AST/package-level symbol detail from a source
  position, with enclosing declaration fallback.
- `go_references`: bounded AST/package-level reference lookup from a source
  position, with package/file scope, declaration inclusion, and test-file
  filtering.
- `go_imports`: parser-only direct and reverse import views with test-file
  filtering and best-effort stdlib/module-local/external classification.
- `go_implementations`: interface/concrete type implementation lookup from a
  selected source position; host workspaces prefer type-checked matching and
  memory workspaces fall back to AST/package-qualified method-name matching.
- `go_callers` and `go_callees`: bounded AST-only direct call hierarchy lookup
  for selected functions and methods.
- `go.summary` context provider with compact module/package/command
  orientation.

Implemented operation set:

- `go_info`: compact Go toolchain orientation for agents, aggregating version,
  curated environment, module/workspace paths, proxy/private settings, cache
  dirs, and diagnostics.
- `go_env`: structured read-only `go env -json` wrapper for selected variables
  or the full Go environment; mutating `go env -w` and `go env -u` are out of
  scope.
- `go_version`: explicit `go version` wrapper, including optional
  workspace-relative binary build-info inspection.
- `go_doc`: package or symbol documentation, possibly with `system.Process`
  fallback.
- `go_list`: structured `go list -json` package/module metadata wrapper.
- `go_test`: structured `go test -json` execution summaries.
- `go_fmt`: explicit `go fmt` formatting operation with dry-run support.
- `go_vet`: structured `go vet` diagnostics; `-fix` is out of scope.
- `go_build`: compile-check wrapper; output artifact placement is out of
  scope for the first slice.
- `go_install`: follow-up explicit installer operation; default to dry-run
  first because it can write outside the workspace and download modules.

### Navigation Requirements

Go navigation operations should prefer position-based selectors over name-based
selectors. Name/package/kind filters are useful for discovery, but reference,
definition, implementation, caller, and callee queries must accept a
workspace-relative `path` plus `line`/`column` or byte offset so overloaded
names, methods, fields, locals, imports, and shadowed identifiers can be
resolved precisely.

Add read-only navigation operations in this order:

- `go_definition`: resolve the AST/package-level declaration for the
  identifier, selector, import path, or package declaration at a source
  position.
- `go_symbol_info`: return compact type, signature, constant value,
  documentation, and method/field summary for the symbol at a position.
- `go_references`: return bounded references to the selected symbol, with
  `include_declaration`, `scope`, `include_tests`, and result limits.
- `go_imports`: return direct imports and reverse importers, separating
  production/test imports and standard-library/module-local/external imports.
- `go_implementations`: find workspace types implementing an interface,
  interfaces satisfied by a type, and matching interface/concrete methods.
- `go_callers` and `go_callees`: return static call hierarchy edges for the
  selected function or method.

Every navigation result must include source ranges, line previews, package
identity, test/generated-file flags, and stable best-effort symbol IDs where
available. Results must also report resolution mode and completeness, for
example `lexical`, `ast`, `package`, or `type_checked`, plus warnings for known
limitations.

The first implementation may be AST/package based and incomplete. It must not
pretend to be fully semantic. Interface dispatch, function values, reflection,
build tags, generated code, cgo, and dependencies outside the workspace should
be surfaced as limitations unless the backend later adds type-aware support.

The first `go_definition`/`go_symbol_info` slice supports `file` and
same-directory `package` scopes only. Later reference/call operations can add
`module` or `workspace` scopes when their broader scan behavior is explicit.
Navigation queries must be bounded by `max_results` and `max_bytes`. Defaults
should favor useful local context over whole-workspace scans.

The first `go_references` slice is also AST/package-level. It resolves the
selected symbol with same-directory package context, then applies the requested
reference scan scope. It supports package declarations, imports, top-level
declarations, locals, parameters, receivers, range vars, obvious local receiver
selectors, struct/interface fields, and composite literal field keys. It does
not resolve external package selectors, interface dispatch, function values, or
workspace-wide references.

The first `go_imports` slice is parser-only. Direct mode returns import edges
from a selected file or exact package directory/package ID. Reverse mode scans a
bounded requested path scope for importers of an explicit `import_path`, or a
module-derived selected package import path when one is available. It does not
run `go list`, resolve build tags, or consult the module graph.

The current `go_implementations` slice resolves a selected interface, concrete
type, or method. For host workspaces and type selections, it first loads package
or module scope with `go/packages` and checks interface satisfaction with
`go/types`, including pointer/value method sets, aliases, type identity, and
embedded/promoted methods. If type loading is unavailable or the selected
symbol is a method, it falls back to parser-only package-qualified method-name
matching. Results must report `resolution_mode` and diagnostics so callers can
distinguish type-checked answers from best-effort AST answers.

The first `go_callers` / `go_callees` slice is parser-only direct-call
hierarchy. It resolves selected functions and methods, supports file/package
scope plus best-effort module scope for module-local package function
selectors, and reports AST-only limitations for interface dispatch, function
values, reflection, external packages, build tags, cgo, and full type-checked
resolution.

## Automatic Context Providers

`project.summary` and `go.summary` are system-placement text context providers
annotated with `agentruntime.auto_context=true`. This lets agent configuration
include the summaries even when the visible agent spec has a tight context
provider allowlist.

The summaries are intentionally small hints, not indexes:

- project summary: first few detected projects, facets, docs, and task sources;
- Go summary: first few Go modules/workspaces, discovered package groups, and
  command package directories;
- both point the model at the richer operations to request details.

`codingplugin` aggregates executable context providers from child plugins in
addition to `agents.md`, so apps can opt into `WithPlugin("coding")` and receive
the same project/go orientation implementations as the standalone plugins.

## Markdown Language Support

Markdown support is a separate language-support plugin, not a project-specific
special case. It uses goldmark for parsing so headings are accurate for ATX,
Setext, inline markup, and fenced-code edge cases.

Initial operations:

- `markdown_outline`: parse one markdown file or a markdown tree and return
  nested heading outlines.
- `markdown_links`: list links and images with source path, line, enclosing
  heading, target kind, normalized target path, and fragment.
- `markdown_diagnostics`: run local-only diagnostics for workspace-relative
  file links and markdown heading anchors.

Diagnostics do not perform network checks. External URLs and other non-file
targets are reported as unchecked informational diagnostics. Local missing
files and missing markdown anchors are reported as errors, and anchors on
non-markdown files are warnings.


## Go Environment And Toolchain Introspection

`go_info` is the default quick-orientation operation for Go agents. It should
run `go version` and `go env -json`, then return a curated object rather than
a raw environment dump:

- `version`: `GOVERSION` plus the `go version` text.
- `target`: `GOOS`, `GOARCH`, `GOHOSTOS`, `GOHOSTARCH`, `CGO_ENABLED`.
- `workspace`: selected working directory, `GOMOD`, and `GOWORK`.
- `paths`: `GOROOT`, `GOPATH`, `GOBIN`, `GOMODCACHE`, `GOCACHE`, `GOTOOLDIR`.
- `modules`: `GO111MODULE`, `GOTOOLCHAIN`, `GOFLAGS`.
- `network`: raw and parsed `GOPROXY`, `GOSUMDB`, `GOINSECURE`.
- `private`: parsed `GOPRIVATE`, `GONOPROXY`, `GONOSUMDB`.
- `diagnostics`: process or parsing warnings.

Parse comma-separated private settings into lists. For `GOPROXY`, preserve the
raw value and expose a parsed chain that distinguishes comma fallbacks from pipe
fallback groups.

`go_env` is the lower-level read-only environment wrapper. It supports
`go env -json`, explicit variable selection, and `go env -changed -json`, but
must not expose `go env -w` or `go env -u`.

`go_version` is the explicit version wrapper. With no files, it reports the
toolchain version. With workspace-relative files, it may run `go version -m`
and parse binary build metadata. It must not recursively scan large directories
by default.

## Go Toolchain Operation Scope

The Go plugin may expose selected `go <command>` operations as structured
operations. The immediate toolchain surface is:

- `go_doc`: documentation lookup for a package or symbol.
- `go_list`: package/module metadata.
- `go_test`: structured test execution.
- `go_fmt`: explicit formatting mutation with dry-run support.
- `go_vet`: diagnostics, with `-fix` excluded from this slice.
- `go_build`: compile checks, with output artifact placement excluded from v1.

`go_install` is a follow-up operation. It belongs in Go-plugin scope, but it is
higher risk because it writes binaries outside the workspace by default and may
download modules.

Deferred commands:

- `go_generate`: can execute arbitrary generators and mutate source.
- `go_fix`: mutates source.
- `go_get`, `go_mod_*`, `go_work_*`: mutate module/workspace dependency state.
- `go_run`: executes project code.
- `go_clean`: deletes build artifacts and caches.
- `go_telemetry`, `go_bug`: process/global environment surfaces, not coding
  context primitives.
- arbitrary `go_tool`: too broad for the first structured operation set.

## Toolchain Operation Parameters

`go_info` input:

- `path`: workspace-relative working directory.
- `include_private`: default true; includes parsed private/proxy/sumdb config
  but no secrets.
- `include_paths`: default true; includes GOROOT/GOPATH/GOMOD/GOWORK/cache/tool
  dirs.
- `include_raw_env`: default false; when true embeds selected raw `go env`
  values.
- `max_bytes`: bound raw output capture.

`go_env` input:

- `path`: workspace-relative working directory.
- `vars`: optional env var names; default is the curated `go_info` set.
- `all`: when true, return all `go env -json`.
- `changed`: maps to `go env -changed -json`.
- `redact`: default true; redact sensitive-looking values if new env keys are
  added later.
- Explicitly unsupported: write, unset, and any `GOENV` mutation.

`go_version` input:

- `path`: workspace-relative working directory.
- `files`: optional workspace-relative binary files.
- `module_info`: maps to `go version -m`.
- `json`: enabled internally when `module_info` is true.
- `verbose`: maps to `-v`, only for explicit file or directory inspection.
- `max_results`, `max_bytes`.

`go_doc` input:

- `path`: workspace-relative file or directory.
- `line`, `column`, `offset`: optional source-position selector.
- `package`: optional import path or package suffix.
- `symbol`: optional symbol, method, or field selector.
- `all`, `short`, `source`, `include_unexported`, `include_cmd`.
- `max_bytes`.

`go_list` input:

- `path`: workspace-relative working directory.
- `patterns`: package/module patterns; default `["."]`.
- `modules`, `deps`, `test`, `compiled`, `find`, `include_errors`.
- `max_results`, `max_bytes`.
- Always invoke with `-json` and `-buildvcs=false` internally and parse
  structured output.

`go_test` input:

- `path`: workspace-relative working directory.
- `patterns`: package patterns; default `["."]`.
- `run`, `skip`, `short`, `failfast`, `count`, `timeout`.
- `vet`: `default`, `off`, or `all`.
- `race`, `cover`.
- `max_output_bytes`.
- Prefer `-json` internally and summarize package/test events.

`go_fmt` input:

- `path`: workspace-relative working directory.
- `patterns`: package patterns; default `["."]`.
- `dry_run`: when true, run `go fmt -n`; when false, run `go fmt`.
- `trace`: maps to `-x`.
- `mod`: optional `readonly`, `vendor`, or `mod`.
- Output changed or would-change file paths.

`go_vet` input:

- `path`: workspace-relative working directory.
- `patterns`: package patterns; default `["."]`.
- `tags`: optional build tags.
- `json`: enabled internally where supported.
- `diff`: optional dry-run patch output.
- `fix`: unsupported in this milestone.
- `vettool`: unsupported in this milestone.
- `max_output_bytes`.

`go_build` input:

- `path`: workspace-relative working directory.
- `patterns`: package patterns; default `["."]`.
- `tags`: optional build tags.
- `race`, `cover`, `trimpath`.
- `mod`: optional `readonly`, `vendor`, or `mod`.
- `output`: unsupported in v1.
- `max_output_bytes`.
- Always invoke with `-buildvcs=false` internally so compile checks are not
  blocked by unavailable or copied VCS metadata.

`go_install` input:

- `path`: workspace-relative working directory.
- `packages`: package paths or patterns; required.
- `version`: optional shared suffix such as `latest` or `v1.2.3`; when set,
  render every package as `pkg@version`.
- `dry_run`: default true in the first implementation; maps to `go install -n`.
- `trace`, `tags`, `race`, `trimpath`.
- `mod`: allowed only when `version` is empty.
- restricted env allowlist: `GOBIN`, `GOPATH`, `GOOS`, `GOARCH`,
  `CGO_ENABLED`.
- `max_output_bytes`.
- Always invoke with `-buildvcs=false` internally so dry-run and real installs
  are not blocked by unavailable or copied VCS metadata.

## Operation Contracts

Keep model-facing inputs small and explicit:

- all path inputs are workspace-relative;
- no operation accepts shell fragments;
- every result includes stable IDs or URIs where available, plus human-readable
  text;
- large results are bounded by `max_results`, `max_bytes`, or `max_depth`;
- refresh means rebuild the current in-memory view for the plugin instance;
- parser read operations are low risk and filesystem-read only;
- toolchain wrappers declare process intent and use `system.Process`;
- no toolchain operation accepts shell fragments;
- toolchain operations build direct `go` argv arrays only;
- process output is bounded through `MaxStdout`, `MaxStderr`, and timeout;
- `go_doc`, `go_env`, `go_info`, `go_version`, and `go_list` are low-risk
  process reads;
- `go_test`, `go_vet`, and `go_build` are medium-risk process checks;
- `go_fmt` is medium-risk and declares filesystem update effects when not
  dry-run;
- `go_install` is high-risk unless constrained to dry-run or an isolated temp
  `GOBIN`.

The first version should not expose raw Axon graph nodes as the public schema.
Convert Axon data into stable Go-specific DTOs so the plugin can change the
backend later without changing operation contracts.

## Memory Lifecycle

Start with on-demand memory scans:

1. A project or Go read operation asks for inventory or source structure.
2. The plugin reads through `system.Workspace`.
3. `runtime/project.Manager` may reuse its process-local inventory unless
   `refresh: true` is set.
4. Go source operations parse files on demand and return bounded DTOs.

Invalidation is intentionally coarse:

- rebuild when the caller asks `refresh: true`;
- rebuild when no in-memory inventory exists;
- no disk cache, watcher, SQLite store, or cross-session index.

If other plugins need richer shared project state later, expand
`runtime/project` behind neutral core DTOs rather than introducing a
language-specific manager first.

## Code Editing Scope

Generic code editing and language-aware refactoring are tracked separately in
[`2026-05-16-1425-code-edit.md`](../designs/2026-05-16-1425-code-edit.md).
This plan stays focused on project inventory, Go structural reads, navigation,
documentation, and explicit process-backed Go toolchain commands. `go_fmt` is
in scope here because it is an explicit formatting command, not a hidden edit
or refactor primitive. Future Go-aware edit operations can consume the Go
read/navigation capabilities from this plan, but they should not expand the
scope of the current project+Go roadmap.

## Tests

Use table-driven tests with temporary Go modules:

- project detection from root and nested `go.mod`;
- mixed manifest roots such as `go.mod` plus `package.json`;
- Makefile, Taskfile, package scripts, and markdown heading outlines;
- package listing with normal and `_test` packages;
- file outline for funcs, methods, structs, interfaces, consts, and vars;
- symbol lookup across packages;
- references, imports, and implementation lookup for a small multi-file module;
- type-checked implementation lookup for host workspaces, including aliases,
  pointer/value method sets, and embedded/promoted methods, with AST fallback
  preserved for memory workspaces and method selections;
- call graph for a small multi-file module, including same-package calls,
  module-local import selectors, test filtering, and nested package exclusion;
- markdown outline availability if the design later adds generic project
  outline operations through language plugins;
- automatic context provider aggregation through `codingplugin`;
- markdown outline parsing through goldmark, including Setext headings, inline
  markup, fenced-code exclusion, local links, missing files, missing anchors,
  and unchecked external links;
- stale/refresh behavior;
- operation schema documentation for path bounds, result bounds, and refresh.

For Go toolchain wrappers:

- `go_info`: curated fields present, parsed proxy/private lists, and no full
  raw environment dump by default.
- `go_env`: curated vars, explicit vars, all vars, changed mode, and rejection
  of write/unset inputs.
- `go_version`: toolchain version, binary module-info path, and max output
  bounds.
- `go_doc`: package docs, symbol docs, unexported docs, and no-doc diagnostics.
- `go_list`: package metadata, module metadata, test packages, and broken
  package diagnostics.
- `go_test`: pass, fail, compile error, `-run`, timeout, and JSON event
  parsing.
- `go_fmt`: dry-run command reporting and real formatting mutation in a temp
  workspace.
- `go_vet`: JSON diagnostics, `-diff`, and rejection of `fix`.
- `go_build`: successful compile check and compile failure diagnostics.
- `go_install`: dry-run local main package, `pkg@version` rendering, rejected
  mixed/empty package input, restricted env allowlist, and real install only
  into an isolated temp `GOBIN`.
- timeout behavior, process intent declaration, output truncation, and no
  direct `os/exec` use in the plugin.

Run at minimum:

```bash
go test ./plugins/golangplugin ./plugins/codingplugin ./apps/coder
go test ./plugins/markdownplugin
env -u TAVILY_API_KEY task verify
```

## Rollout

First implementation slice:

1. Add `plugins/golangplugin` with read-only operations:
  `project_inventory`, `project_files`, `project_tasks`, `project_docs`,
  `go_project`, `go_packages`, `go_outline`, and `go_symbol`.
2. Use Workspace-native parsers and shared core DTOs.
3. Add the plugins to `codingplugin.New` after the core read operations pass.
4. Add `go_references` and `go_imports` once the navigation path is proven.
5. Add `go_implementations` once import/reference navigation is stable.
6. Add `go_callers` / `go_callees` once implementation lookup is stable.
7. Upgrade `go_implementations` with a type-aware backend for host workspaces.
8. Add read-only Go toolchain orientation: `go_info`, `go_env`,
   `go_version`, `go_doc`, and `go_list`. Status: implemented.
9. Add process-backed checks and explicit formatting: `go_test`, `go_fmt`,
   `go_vet`, and `go_build`. Status: implemented.
10. Add `go_install` after the safer command wrappers, defaulting to dry-run
   and supporting isolated temp `GOBIN` tests before normal install writes.
   Status: implemented.

Navigation implementation slice:

1. Add `go_definition` and `go_symbol_info`.
2. Keep resolution AST/package-level only with `resolution_mode: ast`,
   `complete: false`, and explicit limitation warnings.
3. Support package declarations, imports, top-level declarations, local vars,
   parameters, receivers, range vars, obvious local receiver selectors, and
   struct/interface fields.
4. Add `go_references` on the same parser-only foundation with package/file
   scope, declaration inclusion, and test-file filtering.
5. Add parser-only `go_imports` with direct and reverse import edges.
6. Add parser-only `go_implementations` with package/module method-name
   matching.
7. Upgrade type selections in `go_implementations` to prefer
   `go/packages` / `go/types` when host package loading is available.
8. Add parser-only `go_callers` / `go_callees` with direct call edges.
9. Defer further type checking and process wrappers.

Current context/markdown implementation slice:

1. Add `project.summary` and `go.summary` as auto context providers.
2. Aggregate child context providers from `codingplugin`.
3. Add `plugins/markdownplugin` with `markdown_outline`, `markdown_links`, and
   `markdown_diagnostics`.
4. Expose markdown operations through the coder app and delegation operation
   allowlist.

Go toolchain implementation slice:

1. Add shared direct-argv helpers for bounded `go` process execution through
   `runtime/system.Process`. Status: implemented for the initial read-only
   toolchain operations.
2. Add `go_info`, `go_env`, and `go_version` with read-only semantics. Status:
   implemented.
3. Add `go_doc` and `go_list`, parsing JSON where the Go command supports it.
   Status: implemented.
4. Add `go_test`, `go_vet`, and `go_build` using bounded output and structured
   summaries. Status: implemented.
5. Add `go_fmt` as an explicit mutating operation with dry-run support.
   Status: implemented.
6. Add `go_install` as a follow-up high-risk operation with dry-run-first
   behavior and restricted environment overrides. Status: implemented.

## Open Questions

- Should `coder` warm project inventory during startup, or keep it strictly
  operation-triggered?
- Should future operations become language-agnostic wrappers over provider
  implementations, or should language-specific tool names remain model-facing?
- Future precision upgrade: decide how much type resolution `go_callers` /
  `go_callees` should gain after the AST-only direct-call implementation.
- `go_install` remains dry-run by default; real execution requires explicit
  `dry_run=false` and should use an isolated `GOBIN` when possible.

## REVIEW #1

Reviewer findings recorded before fixes:

- P2: `project_inventory` used caller `max_results` as the filesystem traversal
  cap. Fix: always scan with an internal traversal cap, then apply
  `max_results` only to returned project records.
- P2: nested markdown docs such as `docs/guide.md` created a separate docs-only
  project instead of attaching to the nearest owning project. Fix: collect
  markdown facets during scan and attach them to the nearest manifest-backed
  project, creating docs-only projects only when no owner exists.
- P2: `go_project` ignored `path` and returned all Go projects. Fix: path scopes
  to the nearest relevant project.
- P2: `go_packages` ignored `project_id`. Fix: use `project_id` to resolve the
  project root when no explicit `path` is supplied.
- P2: root-level `vendor/...` Go files were not excluded. Fix: reject any path
  containing a `vendor` segment.
- P3: `.git` directory facets were unreachable because directory entries were
  skipped first. Fix: detect `.git` directories before skipping other dirs.

Status: resolved with regression tests in `runtime/project` and
`plugins/golangplugin`.

## REVIEW #2

Reviewer findings recorded before fixes:

- P2: `go_references` package scope could include nested directories with the
  same Go package name. Fix: constrain reference scan files to the selected
  package directory and package ID before matching references.

Status: resolved with regression coverage in `plugins/golangplugin`.

## REVIEW #4

Reviewer findings recorded before fixes:

- P2: `go_implementations` keyed interfaces, concrete types, and method sets by
  bare type name, so module-scope lookups could merge same-name types from
  different packages. Fix: key implementation indexes by package ID plus type
  name.

Status: resolved with regression coverage in `plugins/golangplugin`.

## REVIEW #3

Reviewer findings recorded before fixes:

- P2: `go_imports` did not widen package-directory/package-id derived reverse
  lookups to the enclosing module. Fix: when reverse target is derived, scan
  the nearest module root for importers.
- P2: dotless module paths such as `module app` were misclassified as stdlib.
  Fix: classify known module-path prefixes before applying the stdlib
  heuristic.

Status: resolved with regression coverage in `plugins/golangplugin`.

## REVIEW #5

Reviewer findings recorded before fixes:

- P2: `go_callers` / `go_callees` module-scope selector resolution could map a
  module-local import path to an external `_test` package ID when
  `include_tests` defaulted to true. Fix: preserve production package IDs for
  import selector lookup and never let `package foo_test` overwrite the
  production `package foo` mapping.

Status: resolved with regression coverage in `plugins/golangplugin`.

## REVIEW #6

Review input: `go-plugin-review.md`.

Reviewer findings recorded before fixes:

- `go_implementations` could not reliably answer "find implementations of
  interface X" for cross-package interfaces because its AST-only method-name
  matcher missed type identity, imported aliases, embedded/promoted methods,
  generic method sets, build constraints, and precise pointer/value receiver
  semantics. The concrete motivating case was `core/datasource.Provider`,
  where likely providers existed but module-scope lookup did not find them
  authoritatively.

Fix:

- Add a type-aware `go_implementations` backend for host workspaces using
  `go/packages` and `go/types`.
- Keep the existing AST matcher as fallback for memory workspaces, package load
  failures, and method-correspondence lookups.
- Report `resolution_mode`, warnings, and package-load diagnostics so callers
  can tell semantic answers from best-effort parser answers.

Status: implemented with regression coverage for aliases, promoted methods,
and pointer/value method-set matching in `plugins/golangplugin`.

## REVIEW #7

Reviewer findings recorded before fixes:

- P2: a successful type-checked lookup with zero matches returned `ok=false`,
  causing `go_implementations` to fall back to AST method-name matching and
  report false positives that `go/types` had already rejected.
- P2: virtual workspaces such as `systemtest.MemoryWorkspace` still attempted
  `go/packages` loading against fake host paths like `/memory-workspace`,
  producing bogus package-load diagnostics before AST fallback.

Fix:

- Treat host type-checked zero-match results as authoritative typed results.
- Skip `go/packages` unless the workspace is backed by `runtime/system.HostWorkspace`.
- Render type-checked no-match results as type-checked no matches instead of
  AST-level no matches.

Status: resolved with regression coverage for incompatible method signatures
and clean virtual-workspace AST fallback in `plugins/golangplugin`.
