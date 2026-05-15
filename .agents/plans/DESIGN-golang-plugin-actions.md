# DESIGN: Go Plugin Actions

## Status

Draft design.

## Summary

Add a first-party `golangplugin` that contributes Go-aware read and analysis
operations for coding agents. The first version should focus on project/module
discovery, package and file outlines, symbol lookup, references, call graph
navigation, and lightweight Go command wrappers. AST-backed editing and
workspace-wide refactoring should be designed but not implemented in the first
cut.

The initial implementation should prefer Axon's existing Go indexer and graph
model when it can be embedded cleanly. If Axon integration is too heavy for a
small first slice, build thin Go standard-library readers for outlines first and
keep the operation shapes compatible with later Axon-backed answers.

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

## Existing Axon Fit

`github.com/codewandler/axon` is already in the module graph and currently used
only through `adapters/embed/axon` for embeddings. The local replace points to
`/home/timo/projects/codewandler-ai/axon`.

Axon already provides:

- filesystem tree indexing;
- project detection around resources such as `go.mod`;
- markdown document/section indexing;
- Go module/package/symbol indexing triggered from `go.mod`;
- Go node types for modules, packages, structs, interfaces, funcs, methods,
  fields, consts, vars, and references;
- reference and call edges;
- in-memory SQLite storage by default.

The main integration question is not capability, but ownership and lifecycle:
the runtime needs a session/workspace-scoped index that can be built on demand,
invalidated after file writes, and queried without leaking Axon concepts into
model-facing operation schemas.

## First Plugin Shape

Create `plugins/golangplugin` with `Name = "golang"` and an operation set named
`golang`. The plugin depends on `runtime/system.System` for workspace access and
may depend on Axon directly because plugins may import optional implementation
libraries. It must still route process execution through `runtime/system`
instead of `os/exec`.

Initial operations:

- `go_project`: detect Go modules under the workspace, returning module path,
  root, go version, package count when indexed, and whether the index is fresh.
- `go_packages`: list packages for a module or path, including package name,
  import path, directory, file count, direct imports, and test package metadata.
- `go_outline`: return a bounded outline for a Go file or package: types,
  funcs, methods, consts, vars, line positions, signatures, docs when small.
- `go_symbol`: find symbol definitions by name, package, kind, or file path.
- `go_references`: find usage sites for a symbol, returning caller, file, line,
  reference kind, and a short source preview.
- `go_callers` and `go_callees`: traverse call edges for functions/methods.
- `go_imports`: show direct imports for a file/package and reverse importers
  inside the workspace when indexed.
- `go_doc`: return package or symbol documentation from indexed docs or `go doc`
  through `system.Process` as a fallback.

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
- stale-index results must say they are stale and expose an `indexed_at` or
  generation field;
- read operations are low risk and filesystem-read only;
- command wrappers declare process intent and use `system.Process`.

The first version should not expose raw Axon graph nodes as the public schema.
Convert Axon data into stable Go-specific DTOs so the plugin can change the
backend later without changing operation contracts.

## Index Lifecycle

Start with on-demand indexing:

1. A Go read operation asks for an index.
2. The plugin resolves the workspace root through `system.Workspace`.
3. The index manager builds or reuses an Axon instance for that root.
4. If no index exists, run an initial index with bounded progress reporting.
5. Results include whether indexing was performed, reused, or skipped.

Invalidation can be coarse in the first cut:

- rebuild when the caller asks `refresh: true`;
- rebuild when no index exists;
- later, mark stale after filesystem write operations or use a workspace event
  hook if one exists.

Keep the index manager inside the plugin initially. If other plugins need the
same project graph, promote it later into `orchestration` or `runtime` behind a
neutral interface.

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

- module detection from root and nested `go.mod`;
- package listing with normal and `_test` packages;
- file outline for funcs, methods, structs, interfaces, consts, and vars;
- symbol lookup across packages;
- references and call graph for a small multi-file module;
- markdown outline availability if the design later adds generic project
  outline operations through Axon;
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
task verify
```

## Rollout

First implementation slice:

1. Add `plugins/golangplugin` with read-only operations:
   `go_project`, `go_packages`, `go_outline`, and `go_symbol`.
2. Use Axon if the integration stays small; otherwise implement `go_outline`
   with `go/parser` and `go/types` and keep DTOs backend-neutral.
3. Add the plugin to `codingplugin.New` after the core read operations pass.
4. Add `go_references`, `go_callers`, `go_callees`, and `go_imports` once the
   index path is proven.
5. Consider `go_test` and `go_list` wrappers after read/navigation tools are
   stable.

## Open Questions

- Should Axon indexing live only in `golangplugin`, or should a project graph
  service be promoted for markdown/project/code plugins too?
- Should indexing be purely on demand, or should `coder` warm it during startup?
- Should `go_outline` accept packages as first-class targets immediately, or
  start with files only?
- Do Go read operations belong in a language-specific plugin only, or should
  there eventually be generic `project_outline` / `symbol_search` operations
  backed by Axon across languages and markdown?
