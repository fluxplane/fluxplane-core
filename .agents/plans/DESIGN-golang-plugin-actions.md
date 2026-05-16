# DESIGN: Go Plugin Actions

## Status

Implemented first read-only slice. Updated direction: no Axon dependency for
project or Go language support.

## Summary

Add generic project inventory and language-support models, then add
`projectplugin` and `golangplugin` as the first read-only implementations.
`projectplugin` owns workspace facts such as manifests, Makefiles, Taskfiles,
and markdown document outlines. `golangplugin` owns Go-specific source
structure such as modules, packages, outlines, and declaration symbols.
`codingplugin` also contributes compact automatic context providers so new
turns receive project and Go orientation before the model asks for a tool.
Markdown gets its own language-support plugin for accurate document outlines,
link listing, and local-only diagnostics.

The first version is Workspace-native, memory-only, and does not use Axon.
Axon remains useful prior art for vocabulary and future feature shape, but it
must not be imported by `plugins/golangplugin` or the project/language core
models.

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
uses `go/parser`, `go/ast`, `go/token`, and `golang.org/x/mod/modfile`. It does
not use Axon, `go/packages`, `os`, or direct host filesystem walking.

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
- `go.summary` context provider with compact module/package/command
  orientation.

Later operations:

- `go_imports`: direct import views and reverse importers from parsed files.
- `go_implementations`: interface/type implementation lookup.
- `go_callers` and `go_callees`: bounded static call hierarchy with explicit
  limitations.
- `go_doc`: package or symbol documentation, possibly with `system.Process`
  fallback.

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


Command-like helpers can be added once the read path exists:

- `go_test`: structured wrapper around `go test` with package list, timeout,
  env passthrough policy, and summarized failures.
- `go_list`: structured wrapper around `go list` for package metadata.
- `go_fmt`: optional later operation; formatting mutates files and should be
  treated as an explicit side-effecting operation, not hidden inside reads.

## Operation Contracts

Keep model-facing inputs small and explicit:

- all path inputs are workspace-relative;
- no operation accepts shell fragments;
- every result includes stable IDs or URIs where available, plus human-readable
  text;
- large results are bounded by `max_results`, `max_bytes`, or `max_depth`;
- refresh means rebuild the current in-memory view for the plugin instance;
- read operations are low risk and filesystem-read only;
- command wrappers declare process intent and use `system.Process`.

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

## Editing and Refactoring Direction

Do not put AST editing into `file_edit`. `file_edit` remains file-oriented and
operates on existing files only.

Future Go-aware mutation should be exposed as separate operations, for example:

- `go_rename_symbol`
- `go_add_function`
- `go_replace_function_body`
- `go_add_method`
- `go_update_imports`

These should be designed as Go operations with AST/package semantics, not line
number semantics. They must resolve against the current indexed source, produce
a dry-run diff by default, and route final writes through `system.Workspace`.

For the first plugin version, keep these as non-goals. Build reliable read and
navigation operations first.

## Tests

Use table-driven tests with temporary Go modules:

- project detection from root and nested `go.mod`;
- mixed manifest roots such as `go.mod` plus `package.json`;
- Makefile, Taskfile, package scripts, and markdown heading outlines;
- package listing with normal and `_test` packages;
- file outline for funcs, methods, structs, interfaces, consts, and vars;
- symbol lookup across packages;
- references and call graph for a small multi-file module;
- markdown outline availability if the design later adds generic project
  outline operations through language plugins;
- automatic context provider aggregation through `codingplugin`;
- markdown outline parsing through goldmark, including Setext headings, inline
  markup, fenced-code exclusion, local links, missing files, missing anchors,
  and unchecked external links;
- stale/refresh behavior;
- operation schema documentation for path bounds, result bounds, and refresh.

For command wrappers:

- `go_test` success and failure summaries;
- timeout behavior;
- process intent declaration;
- no direct `os/exec` use in the plugin.

Run at minimum:

```bash
go test ./plugins/golangplugin ./plugins/codingplugin
go test ./plugins/markdownplugin
task verify
```

## Rollout

First implementation slice:

1. Add `plugins/golangplugin` with read-only operations:
  `project_inventory`, `project_files`, `project_tasks`, `project_docs`,
  `go_project`, `go_packages`, `go_outline`, and `go_symbol`.
2. Use Workspace-native parsers and shared core DTOs.
3. Add the plugins to `codingplugin.New` after the core read operations pass.
4. Add `go_references`, then `go_callers`, `go_callees`, and `go_imports` once
   the navigation path is proven.
5. Consider `go_test` and `go_list` wrappers after read/navigation tools are
   stable.

Navigation implementation slice:

1. Add `go_definition` and `go_symbol_info`.
2. Keep resolution AST/package-level only with `resolution_mode: ast`,
   `complete: false`, and explicit limitation warnings.
3. Support package declarations, imports, top-level declarations, local vars,
   parameters, receivers, range vars, obvious local receiver selectors, and
   struct/interface fields.
4. Add `go_references` on the same parser-only foundation with package/file
   scope, declaration inclusion, and test-file filtering.
5. Defer import graph views, call hierarchy, implementation lookup, type
   checking, and process wrappers.

Current context/markdown implementation slice:

1. Add `project.summary` and `go.summary` as auto context providers.
2. Aggregate child context providers from `codingplugin`.
3. Add `plugins/markdownplugin` with `markdown_outline`, `markdown_links`, and
   `markdown_diagnostics`.
4. Expose markdown operations through the coder app and delegation operation
   allowlist.

## Open Questions

- Should `coder` warm project inventory during startup, or keep it strictly
  operation-triggered?
- Should future operations become language-agnostic wrappers over provider
  implementations, or should language-specific tool names remain model-facing?
- When references/callers/callees arrive, how much type resolution is worth
  doing without `go/packages`?

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
