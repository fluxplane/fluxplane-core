# DESIGN: Multi-Root Workspace Startup

## Status

Draft.

## Summary

Allow local apps to start with additional workspace roots, for example:

```bash
coder --workspace-root ../api --workspace-root web=../web
coder serve --workspace-root api=../api
```

The current working directory remains the primary root. Extra roots are added to
the runtime filesystem boundary and should also become visible to project
inventory/discovery so project detectors run across the complete selected working
set. `apps/coder` should reuse this lower-level launch/runtime functionality
rather than owning it as a coder-only concern.

This is a startup/runtime selection feature, not a change that merges the
concepts of workspace, filesystem root, and project.

## Goals

- Add generic local launch support for repeated `--workspace-root` flags.
- Reuse that support from coder and other local app surfaces.
- Support `PATH` and `NAME=PATH` forms.
- Expose roots through the existing runtime workspace system as `@name/...`.
- Make project inventory scan primary plus additional roots.
- Preserve root identity in inventory output and tool paths.
- Keep memory design aligned with durable workspace identity rules.

## Non-goals

- No global workspace registry in this slice.
- No automatic durable memory identity for ad hoc CLI roots.
- No VCS probing or remote canonicalization in the first implementation.
- No broad access to arbitrary host paths without explicit user flags.

## Current State

The runtime already supports additional filesystem roots:

```go
system.WorkspaceConfig{
    Roots: []system.WorkspaceRootConfig{
        {Name: "api", Path: "../api", Access: system.WorkspaceAccessReadWrite},
    },
}
```

Launch config already has a matching neutral shape:

```go
distribution.WorkspaceConfig{
    Roots: []distribution.WorkspaceRoot{{Name: "api", Path: "../api"}},
}
```

The missing pieces are:

1. local app CLI flags that populate this launch workspace config;
2. operational project inventory scanning all selected roots, not only the
   primary workspace root;
3. concept docs that clearly distinguish `Workspace` from `Project`.

## CLI Design

Support repeated flags on generic local run surfaces and on `coder serve`:

```bash
coder --workspace-root ../api
coder --workspace-root api=../api --workspace-root web=../web
coder serve --workspace-root api=../api
```

Parsing rules:

- `NAME=PATH` uses explicit root name.
- `PATH` derives `NAME` from the path basename.
- names must be valid runtime workspace root names;
- paths must be non-empty;
- duplicate names are rejected;
- default access is read-write;
- future syntax may add read-only roots, for example `docs=../docs:ro`.

User-visible addressing:

```text
@api/go.mod
@web/package.json
```

## Runtime Wiring

Implementation should thread parsed roots into the existing launch config:

```go
Launch.Workspace.Roots = append(Launch.Workspace.Roots, distribution.WorkspaceRoot{
    Name: rootName,
    Path: rootPath,
    Access: "read_write",
})
```

Then existing `apps/launch.systemWorkspaceConfig` maps the launch config into
`runtime/system.WorkspaceConfig`.

The primary root remains `startup.Root` / current working directory. Extra roots
are named roots, not replacements for the primary root.

## Project Inventory Behavior

Project inventory should scan:

1. primary root;
2. every configured extra root.

Inventory output should retain root-relative identity so results remain usable by
filesystem operations:

```json
{
  "workspace_id": "...",
  "projects": [
    { "id": "root", "path": "go.mod" },
    { "id": "api", "path": "@api/go.mod" },
    { "id": "web", "path": "@web/package.json" }
  ]
}
```

The exact `Project.ID` format can remain implementation-defined, but paths should
be safe workspace-relative paths, using named-root prefixes for extra roots.

## Workspace and Memory Semantics

Ad hoc CLI roots should create an operational multi-root working set for the
current process. They should not automatically create a durable workspace memory
identity.

Memory rules:

- reads may use the selected operational roots as context signals;
- workspace-scope writes should prefer a declared durable workspace ID;
- if only CLI roots exist, workspace memory writes should either ask for a stable
  identity or degrade to session-scoped memory;
- `.agents/workspaces.json` remains the better durable declaration mechanism.

## Architecture Placement

- `apps/launch` / distribution CLI adapters: generic flag parsing and neutral
  runtime wiring from launch config to `runtime/system`.
- `apps/coder`: reuse the generic flags and add serve-specific forwarding.
- `plugins/workspaceplugin`: auto-context provider that renders basic workspace
  root information for coder, `agentsdk run`, daemon apps, and examples.
- `runtime/system`: existing named root enforcement and path resolution plus root
  metadata introspection.
- `runtime/project`: scan all selected runtime roots and preserve root prefixes.
- `plugins/projectplugin`: expose multi-root inventory through existing project
  operations/context providers.
- `core/workspace`: no IO; only inert model changes if later needed.
- `core/project`: project records remain detected inventory units inside
  workspace roots.

## Task List

1. Add a reusable parser for `--workspace-root` values.
2. Add `--workspace-root` to generic local run surfaces, including coder
   one-shot/REPL.
3. Add `--workspace-root` to `coder serve`.
4. Wire parsed roots into `distribution.LaunchConfig.Workspace.Roots`.
5. Add CLI/unit tests for parsing, help text, duplicate names, and serve wiring.
6. Update project inventory to scan primary and extra runtime roots.
7. Add tests showing project detectors find manifests under `@name/...` roots.
8. Update `docs/concepts.md` with `Workspace` and `Project`, especially the
   summary table. (Done.)
9. Add a generic `workspace.summary` context provider rendered for all local app
   launches, not only coder. (Done.)
10. Update memory implementation/design notes as needed so ad hoc CLI roots do not
   become durable workspace memory IDs by accident.

## Open Questions

- Should unnamed roots derive names from basename only, or include a collision
  suffix automatically?
- Should CLI roots default to read-write or read-only?
- Should `--workspace-root` start on all local distribution CLI surfaces or only
  the app surfaces that can launch local filesystem-backed runtimes?
- Should root metadata include a user-visible title/description?
