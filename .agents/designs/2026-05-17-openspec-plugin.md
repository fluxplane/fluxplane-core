# DESIGN: OpenSpec Plugin

## Summary

Add a first-party `plugins/openspecplugin` that provides OpenSpec-compatible
spec-driven development primitives for AgentRuntime. The plugin should preserve
OpenSpec's public project layout and markdown delta format while defaulting the
spec root to `.agents/openspec` so it fits AgentRuntime's existing project
resource conventions.

The plugin should contribute typed operations for initializing, proposing,
validating, showing, listing, and archiving spec changes. Coder-facing commands
and workflows can then compose those operations with `core/task` so a proposed
spec change becomes a durable task with explicit artifacts, steps, validation,
implementation, review, and archive state.

## Research notes

OpenSpec's useful semantics are small and concrete:

- a spec root containing project context, current specs, active changes, and
  archived changes;
- current truth under `specs/<capability>/spec.md`;
- proposed work under `changes/<change-id>/`;
- change proposals split into `proposal.md`, `tasks.md`, optional `design.md`,
  and delta specs under `changes/<change-id>/specs/<capability>/spec.md`;
- markdown deltas with `ADDED`, `MODIFIED`, `REMOVED`, and `RENAMED`
  requirement sections;
- a workflow of proposal, validation, implementation, and archive;
- archive merges accepted deltas into current specs and moves the change into
  archive history.

The key value for agentic development is alignment before coding: requirements,
scenarios, tasks, and reviewable deltas are durable files instead of transient
chat context.

## Problem

Coder can already inspect files, create tasks, run verification, and produce
patches, but it lacks a lightweight spec-driven loop that forces requirements to
be stated and validated before implementation. Without a durable proposal layer,
agent sessions can drift from user intent, bury decisions in conversation
history, or update documentation only after code is already written.

We need a plugin that gives agents a predictable project-local source of truth
for capabilities and a safe way to propose and apply spec deltas without adding a
parallel task system.

## Goals

- Be layout-compatible with OpenSpec so projects can interoperate with existing
  conventions and documentation.
- Default the spec root to `.agents/openspec` rather than repository-root
  `openspec`.
- Keep the root configurable per operation and, later, through app/project
  configuration.
- Represent spec-driven work as AgentRuntime tasks, workflows, operations, and
  artifacts instead of a new lifecycle domain.
- Provide typed operations with generated JSON Schema using
  `runtime/operation.NewTyped` or `NewTypedResult`.
- Use `runtime/system.System` for filesystem access and preserve operation
  safety boundaries.
- Validate markdown structure and delta consistency before implementation and
  before archive.
- Merge deltas into current specs deterministically during archive.
- Keep the plugin optional and contributed through existing plugin host
  contracts.

## Non-goals

- No wholesale OpenSpec CLI embedding in v1.
- No new core task lifecycle or scheduler concept.
- No direct filesystem access from the plugin outside `runtime/system.System`.
- No hidden automatic archive after implementation; archive is an explicit
  operation/workflow step.
- No full natural-language semantic proof that implementation satisfies every
  requirement. The plugin validates structure and references; verification and
  review remain task/workflow concerns.
- No backwards compatibility shims for stale internal AgentRuntime shapes.

## Default layout

The plugin's default root is:

```text
.agents/openspec/
```

Within that root, the layout is OpenSpec-compatible:

```text
.agents/openspec/
├── AGENTS.md
├── project.md
├── specs/
│   └── <capability>/
│       ├── spec.md
│       └── design.md        # optional
├── changes/
│   ├── <change-id>/
│   │   ├── proposal.md
│   │   ├── tasks.md
│   │   ├── design.md        # optional
│   │   └── specs/
│   │       └── <capability>/
│   │           └── spec.md
│   └── archive/
│       └── <timestamp>-<change-id>/
```

Compatibility means the relative structure below the selected root matches
OpenSpec. A project can opt into root-level OpenSpec by passing `root:
"openspec"` or configuring that root later.

## Package placement

Initial implementation should live in:

```text
plugins/openspecplugin/
```

This package may depend on `core`, `sdk` if resource authoring sugar is needed,
`runtime`, `orchestration`, `adapters` only where allowed for concrete plugin
implementation, and sibling plugin packages only with a clear optional reason.

Do not add `core/openspec` in the first pass. The markdown/delta shapes should
stabilize inside the plugin first. If multiple packages later need inert spec
contracts, extract only the stable IO-free types to `core/openspec` or
`core/spec`.

## Core model inside the plugin

Use small internal structs for parsed files:

```go
type Root string

type CapabilityID string

type ChangeID string

type Requirement struct {
    Name      string
    Body      string
    Scenarios []Scenario
}

type Scenario struct {
    Name string
    Body string
}

type DeltaKind string

const (
    DeltaAdded    DeltaKind = "added"
    DeltaModified DeltaKind = "modified"
    DeltaRemoved  DeltaKind = "removed"
    DeltaRenamed  DeltaKind = "renamed"
)

type DeltaSpec struct {
    Capability CapabilityID
    Added      []Requirement
    Modified   []Requirement
    Removed    []string
    Renamed    []Rename
}

type Rename struct {
    From string
    To   string
}
```

These are parser/merge implementation details in v1, not exported core domain
concepts.

## Markdown format

The plugin should accept the OpenSpec-style format:

```markdown
### Requirement: User Authentication
The system SHALL provide secure user authentication.

#### Scenario: Successful login
- **GIVEN** a user with valid credentials
- **WHEN** they submit the login form
- **THEN** they are authenticated and redirected
```

Delta files support:

```markdown
## ADDED Requirements
## MODIFIED Requirements
## REMOVED Requirements
## RENAMED Requirements
```

Rules:

- requirement headings must be `### Requirement: <name>`;
- scenario headings must be `#### Scenario: <name>`;
- modified and removed requirements must exist in the current spec in strict
  mode;
- added requirements must not already exist in strict mode;
- renamed `FROM` requirements must exist and `TO` requirements must not already
  exist in strict mode;
- duplicate requirement names within a capability are validation errors;
- unknown top-level delta sections are warnings by default and errors in strict
  mode.

## Operations

All operations should use typed input/output structs with `json` and
`jsonschema` tags. They should be registered through the plugin host like other
first-party operations.

### `openspec_init`

Create the root structure if missing.

Input:

```go
type InitInput struct {
    Root      string `json:"root,omitempty" jsonschema:"description=Spec root directory; defaults to .agents/openspec."`
    Overwrite bool   `json:"overwrite,omitempty" jsonschema:"description=Replace generated boilerplate files when they already exist."`
}
```

Output:

```go
type InitOutput struct {
    Root  string   `json:"root"`
    Files []string `json:"files"`
}
```

### `openspec_propose`

Scaffold a change proposal.

Input:

```go
type ProposeInput struct {
    Root         string   `json:"root,omitempty"`
    ChangeID     string   `json:"change_id,omitempty" jsonschema:"description=Kebab-case ID; generated from title when omitted."`
    Title        string   `json:"title"`
    Objective    string   `json:"objective,omitempty"`
    Capabilities []string `json:"capabilities,omitempty"`
    Tasks        []string `json:"tasks,omitempty"`
}
```

Output:

```go
type ProposeOutput struct {
    Root     string   `json:"root"`
    ChangeID string   `json:"change_id"`
    Files    []string `json:"files"`
}
```

### `openspec_validate`

Validate a root or one change.

Input:

```go
type ValidateInput struct {
    Root     string `json:"root,omitempty"`
    ChangeID string `json:"change_id,omitempty"`
    Strict   bool   `json:"strict,omitempty"`
}
```

Output:

```go
type ValidateOutput struct {
    Root        string       `json:"root"`
    ChangeID    string       `json:"change_id,omitempty"`
    Valid       bool         `json:"valid"`
    Diagnostics []Diagnostic `json:"diagnostics,omitempty"`
}
```

Diagnostics should include path, line when known, severity, code, and message.

### `openspec_list`

List capabilities and active changes.

Input:

```go
type ListInput struct {
    Root string `json:"root,omitempty"`
}
```

Output includes root, capabilities, active changes, and archived changes with
bounded counts.

### `openspec_show`

Render one capability or change into concise markdown for model context or UI.

Input:

```go
type ShowInput struct {
    Root       string `json:"root,omitempty"`
    Capability string `json:"capability,omitempty"`
    ChangeID   string `json:"change_id,omitempty"`
}
```

Exactly one of `Capability` or `ChangeID` should be supplied.

### `openspec_archive`

Apply a validated change's deltas to current specs and move the change to
archive.

Input:

```go
type ArchiveInput struct {
    Root     string `json:"root,omitempty"`
    ChangeID string `json:"change_id"`
    Yes      bool   `json:"yes,omitempty" jsonschema:"description=Confirm archive mutation."`
    Strict   bool   `json:"strict,omitempty"`
}
```

Output:

```go
type ArchiveOutput struct {
    Root        string   `json:"root"`
    ChangeID    string   `json:"change_id"`
    ArchivePath string   `json:"archive_path"`
    Updated     []string `json:"updated"`
    Moved       []string `json:"moved"`
}
```

The operation should reject execution unless `Yes` is true or the safety envelope
has obtained explicit approval, depending on existing operation safety policy.

## Archive merge semantics

Archive should be deterministic and conservative:

1. Run strict validation.
2. Parse current `specs/<capability>/spec.md` files.
3. Parse change delta specs.
4. Apply renames first, then removals, then modifications, then additions.
5. Preserve existing non-requirement preamble where practical.
6. Render requirements in a stable order:
   - existing order for unchanged/modified requirements;
   - renamed requirements remain at the original position;
   - added requirements append in file order.
7. Write updated current specs.
8. Move the change directory to `changes/archive/<timestamp>-<change-id>/`.

If a target archive directory already exists, append a short deterministic suffix
or return a conflict diagnostic; prefer returning an error over silently
clobbering history.

## Task and workflow integration

OpenSpec changes should become tasks, not a separate lifecycle system.

A `/openspec propose` command or workflow can create a `core/task.Task` with:

- `objective`: the requested change;
- `acceptance_criteria`: proposal validates, implementation passes project
  verification, and archive succeeds;
- `outputs`: proposal file, delta specs, implementation diff, verification
  report, archive result;
- `steps`: propose, validate, implement, verify, review, archive.

The plugin operations produce and mutate files. The task system tracks the work,
artifacts, assignment, execution, and completion.

Suggested workflow resource:

```yaml
name: openspec-change
description: Propose, implement, verify, and archive an OpenSpec-compatible change.
steps:
  - id: propose
    operation: openspec_propose
  - id: validate
    operation: openspec_validate
    depends-on: [propose]
  - id: implement
    agent: coder
    depends-on: [validate]
  - id: verify
    operation: project_task_run
    input:
      name: verify
    depends-on: [implement]
  - id: archive
    operation: openspec_archive
    depends-on: [verify]
```

The exact workflow should account for current workflow input mapping limits and
may initially be implemented as command prompt guidance plus operations.

## Command resources

Coder can expose ergonomic command resources under
`apps/coder/resources/.agents/commands/` once the plugin exists:

- `openspec-init.yaml`
- `openspec-propose.yaml`
- `openspec-validate.yaml`
- `openspec-show.yaml`
- `openspec-archive.yaml`

Command aliases can be decided later. Avoid overloading `/task` semantics; spec
commands should create or modify tasks through the task operations when they need
lifecycle tracking.

## Configuration

V1 operations accept `root` directly and default to `.agents/openspec`.

Future app/project configuration can add:

```yaml
openspec:
  root: .agents/openspec
  strict: true
  archive:
    timestamp_format: "20060102-150405"
```

Root handling must stay workspace-relative. Absolute paths and parent-directory
escapes should be rejected by the workspace filesystem boundary.

## Safety and policy

- `openspec_init` and `openspec_propose` create files and directories; they are
  filesystem mutations and must run through the safety envelope.
- `openspec_archive` edits current specs and moves change directories; it should
  require explicit confirmation or policy approval.
- Read-only operations such as list/show/validate should not mutate files.
- All filesystem IO should go through `runtime/system.System`.
- Operation output must be bounded and should return file refs rather than full
  file contents for large specs.

## Implementation plan

1. Add `plugins/openspecplugin` with constants, typed input/output structs, and
   plugin registration.
2. Implement root resolution with default `.agents/openspec` and workspace-safe
   path validation.
3. Implement markdown scanning for requirements, scenarios, and delta sections.
4. Implement `openspec_validate` first, with table-driven tests.
5. Implement `openspec_init` and `openspec_propose` scaffolding.
6. Implement `openspec_list` and `openspec_show`.
7. Implement archive merge and move semantics with tests for added, modified,
   removed, and renamed requirements.
8. Add coder resources for commands/workflow after operation behavior is stable.
9. Add documentation and examples.
10. Run `task verify` before committing.

## Test strategy

Unit tests should cover:

- default root resolution;
- compatibility with `root: openspec`;
- init scaffolding;
- change ID generation and validation;
- parsing requirements and scenarios;
- parsing each delta section;
- validation diagnostics with path and line numbers;
- strict vs non-strict validation;
- archive merge ordering;
- archive conflict behavior;
- safety around invalid paths such as `../openspec` and absolute paths.

Integration-style tests can use `runtime/systemtest` to exercise filesystem
operations through the system boundary.

## Open questions

- Should the root default be configurable from `agentsdk.app.yaml`, `.agents`, or
  a future workspace declaration?
- Should archive require project verification artifacts before running, or should
  that remain a workflow/task policy?
- Should `AGENTS.md` generated under the spec root be OpenSpec-compatible text,
  AgentRuntime-specific guidance, or both?
- Should command names use `/openspec ...`, `/spec ...`, or both?
- Should task creation be built into `openspec_propose`, or should a separate
  workflow/command compose `openspec_propose` with `task_create`? The cleaner v1
  answer is composition, not hidden task creation.
