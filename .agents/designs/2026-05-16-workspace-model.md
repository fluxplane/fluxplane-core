# DESIGN: Workspace Model

## Status

Implemented in the current workspace batch. The model exists in
`core/workspace`; runtime resolution, declaration loading, and project inventory
scoping exist in `runtime/workspace`, `runtime/project`, and
`plugins/projectplugin`. This design remains the concept reference for follow-up
work such as VCS evidence gathering, operational multi-root scanning, and memory
integration.

## Summary

Add a first-class workspace model that represents the working boundary for an
agent task. A workspace is not the same thing as a Git repository, a remote, a
local directory, or a detected `core/project.Project`. It is the user-declared or
runtime-detected working set in which tools, project inventory, context, and
memory operate.

A workspace may be:

- a single local root;
- a Git worktree or repository checkout;
- a multi-root working set, such as three sibling repositories used together;
- a declared parent workspace containing child/member workspaces;
- an ad hoc workspace detected from the current working directory.

The core concept should live in `core/workspace`. Detection and resolution live
in runtime/adapters.

## Problem

Several runtime concepts need a stable answer to “where am I working?” Today that
can be confused with:

- current process directory;
- workspace root;
- Git repository root;
- Git remote/origin;
- detected project inventory unit from `core/project`;
- app/distribution configuration;
- agent session scope.

These are related but not equivalent. The memory plugin especially needs a scope
that can cover both single-repository work and multi-repository work without
storing memory under brittle local paths or ambiguous VCS remotes.

Example:

```text
/home/timo/my_org/proj_a
/home/timo/my_org/proj_b
/home/timo/my_org/proj_c
```

The user may want to declare these three repositories as one workspace
`my_org`, while still allowing repo-specific memories and context for `proj_a`,
`proj_b`, or `proj_c`.

## Goals

- Provide a stable, inert workspace model in `core/workspace`.
- Support single-root and multi-root workspaces.
- Support parent/member relationships so a child repo can inherit parent
  workspace context and memory.
- Distinguish workspace roots from detected `core/project.Project` inventory
  units.
- Represent origins/remotes as evidence and aliases, not as the workspace concept
  itself.
- Allow runtime resolution from current directory, declared config, project
  inventory, and VCS evidence.
- Enable memory scope to reference `workspace.ID`.
- Avoid tree-shaped physical memory storage; use workspace relationships at query
  time.

## Non-goals

- No filesystem walking or Git probing in `core/workspace`.
- No replacement for `core/project`; project inventory remains a separate concept.
- No mandatory global workspace registry in v1.
- No attempt to perfectly identify every fork/mirror relationship from remotes.
- No automatic merging of workspace memories across unrelated remotes or local
  roots without explicit declaration or resolver evidence.

## Core concepts

### Workspace

A workspace is the selected or resolved working boundary.

Suggested shape:

```go
package workspace

type ID string

type Durability string

const (
    DurabilityEphemeral Durability = "ephemeral"
    DurabilityDurable   Durability = "durable"
)

type Workspace struct {
    ID          ID                `json:"id"`
    Name        string            `json:"name,omitempty"`
    Durability  Durability        `json:"durability,omitempty"`
    Roots       []Root            `json:"roots,omitempty"`
    ParentID    ID                `json:"parent_id,omitempty"`
    Members     []ID              `json:"members,omitempty"`
    Origins     []Origin          `json:"origins,omitempty"`
    Aliases     []Alias           `json:"aliases,omitempty"`
    Annotations map[string]string `json:"annotations,omitempty"`
}
```

`ParentID` and `Members` are identity relationships between workspaces. They are
not storage paths. They let a resolver say that `proj_b` is active while `my_org`
is an ancestor workspace.
`Durability` communicates whether a resolved identity is suitable for durable
cross-session state. Declared/configured and canonical non-local origin
workspaces are durable. Local fallback workspaces are ephemeral and should not be
used as long-lived memory keys unless a product policy explicitly allows that.


### Root

A root is one local or virtual root included in the workspace.

```go
type Root struct {
    Path       string   `json:"path,omitempty"`
    Kind       RootKind `json:"kind,omitempty"`
    ProjectIDs []string `json:"project_ids,omitempty"`
    Origins    []Origin `json:"origins,omitempty"`
}

type RootKind string

const (
    RootLocal       RootKind = "local"
    RootGitWorktree RootKind = "git_worktree"
    RootVirtual     RootKind = "virtual"
    RootRemote      RootKind = "remote"
)
```

`ProjectIDs` can reference detected `core/project.Project` records under that
root, but the root is not itself a project. The field intentionally uses strings
instead of `core/project.ID` so `core/workspace` stays independent from
`core/project` and avoids a core-level import cycle.

### Origin

An origin describes where a root or workspace is known from. It may be a local
path, configured ID, or VCS provider remote.

```go
type Origin struct {
    Kind    OriginKind `json:"kind"`
    Locator string     `json:"locator"`
    Subpath string     `json:"subpath,omitempty"`
}

type OriginKind string

const (
    OriginConfigured OriginKind = "configured"
    OriginLocal      OriginKind = "local"
    OriginGitHub     OriginKind = "github"
    OriginGitLab     OriginKind = "gitlab"
    OriginGit        OriginKind = "git"
)
```

Examples:

```text
configured:my_org
github:codewandler/foo-project
git:https://example.com/team/repo.git
local:/home/timo/my_org/proj_a
```

Origins are evidence and lookup handles. They should not by themselves force two
workspaces to merge. A repository with multiple remotes may have multiple
origins.

### Alias

An alias is another known locator for the same workspace.

```go
type Alias struct {
    Kind    OriginKind `json:"kind"`
    Locator string     `json:"locator"`
    Subpath string     `json:"subpath,omitempty"`
}
```

Aliases are useful when a workspace was first known as a local root and later a
better configured or VCS-backed identity is discovered. New events can use the
canonical workspace ID while runtime queries can still include alias streams or
legacy records.

### Selection

A selection describes the active workspace for a session/tool invocation.

```go
type Selection struct {
    Active    ID   `json:"active"`
    Ancestors []ID `json:"ancestors,omitempty"`
    Members   []ID `json:"members,omitempty"`
}
```

The active workspace is where writes usually go. Ancestors can be included for
context/memory reads.

## Relationship to `core/project`

`core/project` describes detected inventory units inside a workspace: Go modules,
package manifests, docs, taskfiles, agents dirs, Git repositories, and similar
facets. A workspace is the broader operating boundary.

Examples:

- A single workspace root may contain multiple `core/project.Project` records.
- A multi-root workspace may contain one or more projects per root.
- A `core/project.Project` can be cited by memory provenance or context signals,
  but memory scope should reference `workspace.ID`, not `project.ID`.

`core/project`, `runtime/project`, and `plugins/projectplugin` now carry
`workspace.ID` through project inventory:

- `core/project.Inventory`, `Project`, `Signal`, and `SignalsObserved` include
  optional `WorkspaceID` fields.
- project inventory/files/tasks/docs query structs accept optional
  `workspace_id`.
- `runtime/project.NewManagerForWorkspace` scopes inventory to a resolved
  workspace ID.
- scoped project managers reject mismatched workspace requests.
- unscoped project managers reject explicitly workspace-scoped requests rather
  than pretending the request was enforced.
- `plugins/projectplugin` resolves workspace selection from `system.Workspace`,
  creates a scoped project manager, includes `workspace_id` in rendered data, and
  emits signal events with workspace identity.

This makes project inventory workspace-aware without making projects and
workspaces the same concept.

## Workspace resolution

Resolution is runtime behavior and should not live in `core/workspace`.

A resolver takes available evidence:

- explicit app/session workspace selection;
- configured workspace declarations;
- current working directory;
- project inventory;
- Git repository root and remotes;
- known aliases or prior workspace records.

It returns:

- active workspace;
- ancestor workspaces;
- member/child workspaces when relevant;
- evidence and warnings.

Suggested package placement:

```text
core/workspace       inert model
runtime/workspace    resolver, selection logic, registry/projection over config/events
adapters/*           filesystem/Git/provider-specific evidence gathering if needed
apps/*               product defaults and configured workspace declarations
```

Implementation status:

- `runtime/workspace.Resolver` is pure and selects from explicit IDs,
  declarations, path evidence, and supplied origins.
- `runtime/workspace.Manager` bridges `runtime/system.Workspace` to the resolver.
- `runtime/workspace.DeclarationLoader` reads `.agents/workspaces.json` and
  `.agents/workspace.json` through `system.Workspace`.
- Declaration loading validates declarations, warns on invalid/duplicate data,
  ignores only missing files, and does not silently swallow other read failures.
- `plugins/projectplugin` resolves lazily, so declarations added after plugin
  construction are honored when operations/context providers are requested.

Declaration examples:

```json
{
  "workspaces": [
    {
      "id": "workspace:configured:my_org",
      "name": "my_org",
      "roots": [
        { "path": "/home/timo/my_org/proj_a" },
        { "path": "/home/timo/my_org/proj_b" },
        { "path": "/home/timo/my_org/proj_c" }
      ],
      "members": [
        "workspace:configured:proj_a",
        "workspace:configured:proj_b",
        "workspace:configured:proj_c"
      ]
    },
    {
      "id": "workspace:configured:proj_b",
      "parent_id": "workspace:configured:my_org",
      "roots": [{ "path": "/home/timo/my_org/proj_b" }]
    }
  ]
}
```

## Resolution behavior

### Single local directory with no VCS

```text
pwd = /home/timo/scratch/foo
```

Resolver returns a local workspace:

```text
active = workspace:local:/home/timo/scratch/foo
durability = ephemeral
ancestors = []
```

A privacy-preserving implementation may hash the local path for durable IDs while
keeping the clear path as local provenance visible only where policy allows.

### Single Git repository with one recognized remote

```text
pwd = /home/timo/my_org/proj_a
remote = git@github.com:my_org/proj_a.git
```

Resolver may return:

```text
active = workspace:github:my_org/proj_a
durability = durable
origin = github:my_org/proj_a
alias = local:/home/timo/my_org/proj_a
```

This improves portability across clones. The local path remains provenance or an
alias, not the primary identity.

### Repository with multiple remotes

If several remotes exist, the resolver should not blindly choose one canonical
remote unless app config or provider policy says which remote is authoritative.

Safer behavior:

```text
active = configured ID if present, otherwise workspace:local:<path>
durability = durable for configured/canonical origins, ephemeral for local fallback
origins = [github:org/repo, gitlab:mirror/repo, ...]
warnings = [multiple_remotes_no_canonical_origin]
```

The user or app can later declare the canonical workspace ID.

The implemented resolver currently supports supplied origin evidence, but does
not yet gather Git/VCS evidence itself. Git probing belongs in an adapter or
runtime-system-backed evidence gatherer, not in `core/workspace`.

### Declared multi-root workspace

Configured declaration:

```text
workspace my_org:
  /home/timo/my_org/proj_a
  /home/timo/my_org/proj_b
  /home/timo/my_org/proj_c
```

Resolver can return, when explicitly selected:

```text
active = my_org
members = [proj_a, proj_b, proj_c]
```

### Jumping directly into a member root

If `proj_b` is known to be a member of `my_org` and the current directory is
inside `proj_b`, resolver returns:

```text
active = proj_b
ancestors = [my_org]
```

This gives the user access to repo-specific context plus the broader workspace
context.

## Memory behavior

The memory plugin design depends on this model.

Memory scopes remain:

```text
session
workspace
user
channel
```

Workspace memory references `workspace.ID`.
Local fallback workspace IDs are intentionally marked ephemeral. Memory must not
use ephemeral local IDs as durable cross-session keys by default. A memory write
for workspace scope should prefer a durable declared/configured/provider-backed
workspace ID, ask for configuration, or treat the memory as session-local until a
durable workspace identity exists.


Default memory retrieval for context providers should include:

```text
session + active workspace + workspace ancestors + user + channel
```

Workspace memory writes should default to the narrowest active workspace. If the
user asks for broader memory, writes can target an ancestor workspace explicitly.

Examples:

```text
"Remember for this repo: use pnpm."
→ workspace:proj_b

"Remember for all my_org repos: use the shared staging account."
→ workspace:my_org
```

Do not store memory in a physical tree. Store memory by scope stream:

```text
memory/workspace/my_org
memory/workspace/proj_b
```

Use the workspace relationship graph at query time to include exact, ancestor,
descendant, or related scopes.

Suggested query expansion:

```go
type Expansion string

const (
    ExpansionExact       Expansion = "exact"
    ExpansionAncestors   Expansion = "ancestors"
    ExpansionDescendants Expansion = "descendants"
    ExpansionRelated     Expansion = "related"
)
```

Default for smart context providers: exact + ancestors. Descendants should be
explicit because they can be noisy and may cross sensitivity boundaries.

## Architecture placement

### `core/workspace`

Belongs here:

- `ID`;
- `Workspace`;
- `Root`;
- `RootKind`;
- `Origin`;
- `OriginKind`;
- `Alias`;
- `Selection`;
- validation and normalization helpers that are pure and IO-free.

Does not belong here:

- filesystem discovery;
- Git commands;
- provider API calls;
- reading config files;
- session mutation;
- memory retrieval.

### `runtime/workspace`

Belongs here:

- workspace resolver over supplied evidence;
- registry/projection of declared workspaces if needed;
- current-directory-to-workspace selection logic;
- parent/member relationship resolution;
- canonicalization policy over configured IDs, VCS evidence, and local roots.

### Adapters

Filesystem, Git, and provider-specific evidence gathering belongs behind runtime
system/adapters. Reusable plugins should not call `os`, `os/exec`, `net`, or Git
directly.

### Apps

Apps/distributions choose defaults:

- whether multi-root workspaces are enabled;
- where declarations come from;
- canonicalization policy for multiple remotes;
- whether local paths are stored clear or hashed;
- default workspace selection for sessions.

## Open questions

1. Where should workspace declarations live: app config, `.agents/workspaces.*`,
   a resource bundle, or all of these?
2. Should local workspace IDs store clear absolute paths, hashed paths, or both
   with policy-controlled visibility?
3. What is the canonical ID format for configured, provider, generic Git, and
   local workspaces?
4. Should workspace parent/member relationships be stored as config, event-sourced
   records, or both?
5. How should the resolver choose a canonical origin when a repository has
   multiple remotes?
6. Should a selected parent workspace include child memories by default, or only
   its own memory unless descendants are requested?

## Suggested rollout

1. Add `core/workspace` inert types and validation tests. (Done.)
2. Add `runtime/workspace` resolver interfaces and pure resolution tests using
   supplied evidence. (Done.)
3. Add declaration loading from `.agents/workspaces.json` and
   `.agents/workspace.json`, including validation, warning codes, duplicate
   detection, and local fallback semantics. (Done.)
4. Add workspace selection to running plugin wiring where current working root is
   known. (`projectplugin` now resolves lazily through `runtime/system.Workspace`.)
5. Update `core/project` and runtime project inventory to reference
   `workspace.ID` without merging the concepts. (Done.)
6. Update the memory design to use `workspace.ID` and workspace durability for
   workspace-scope memory. (Done in design; implementation not started.)
7. Future: add VCS evidence gathering through runtime-system-backed adapters.
8. Future: make operational project inventory scan multiple runtime roots when a
   selected workspace declares multiple roots.
