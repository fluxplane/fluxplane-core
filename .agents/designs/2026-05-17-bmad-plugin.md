# DESIGN: BMad Plugin

## Summary

Add a first-party `plugins/bmadplugin` that brings BMad-style agentic agile
workflows into AgentRuntime without copying BMad as a separate runtime. The
plugin should provide a BMAD-compatible project/module layout, import or generate
BMad-like agents/workflows/tasks/templates/checklists, and map the method onto
AgentRuntime primitives: agents, skills, workflows, tasks, operations, resources,
and durable file artifacts.

The default BMad root should be `.agents/bmad`, keeping generated assets inside
AgentRuntime's `.agents` resource tree while allowing compatibility with the
common BMad-style layout when a project opts into another root.

## Research notes

BMad Method, commonly expanded as Build More Architect Dreams or Breakthrough
Method for Agile AI-Driven Development, is an AI-driven agile development
framework. Its recurring ideas are:

- specialized AI personas such as analyst, product manager, architect, UX,
  developer, QA/test architect, product owner, scrum master, and technical
  writer;
- guided workflows that progressively build context from ideation and planning
  to implementation;
- persistent project documents that agents read and hand off between phases;
- module-style installation with agents, workflows, tasks, templates,
  checklists, and data/knowledge files;
- scale-adaptive paths, where small fixes can use a quick flow and larger
  efforts can use full discovery, PRD, architecture, epic/story, and
  implementation phases;
- menu/trigger style commands that invoke an agent or workflow and then prompt
  for missing input;
- implementation driven by prepared stories rather than only ad hoc chat.

BMad's workflow map is commonly described as four broad phases:

1. discovery/ideation and product concept shaping;
2. requirements and planning, including PRD-like outputs;
3. architecture and work breakdown into epics/stories;
4. implementation, verification, and review story by story.

The important AgentRuntime takeaway is that BMad is not just a spec file format.
It is a role-and-workflow bundle that creates durable planning context before
coding and then feeds that context into implementation agents.

## Problem

AgentRuntime already has agents, workflows, task execution, commands, skills,
and project files, but it does not provide a packaged agile agent team with
opinionated planning-to-implementation workflows. Users can ask coder to plan and
implement, but there is no reusable, inspectable BMad-style project layer that
keeps personas, templates, checklists, stories, architecture, and handoff
artifacts organized.

We need a plugin that imports the useful BMad pattern while preserving
AgentRuntime's architecture boundaries and avoiding a second agent runtime,
installer, scheduler, or task system.

## Goals

- Provide BMad-style agile agent-team workflows as an optional first-party
  capability bundle.
- Keep generated and authored files under `.agents/bmad` by default.
- Support compatibility with BMad-like module layout: agents, workflows, tasks,
  templates, checklists, data, and output documents.
- Map BMad personas to `core/agent.Spec` and/or `core/skill` resources.
- Map BMad workflows to `core/workflow.Spec` resources.
- Map BMad tasks/checklists/templates to resources and task/workflow artifacts,
  not executable code paths.
- Use AgentRuntime `core/task` for durable work lifecycle and story execution.
- Use typed operations for init, import, validate, list, show, generate context,
  create story, and run/check workflow preparation.
- Use `runtime/system.System` and the operation safety envelope for all file and
  process boundaries.
- Allow scale-adaptive flows: quick fix, story implementation, and full product
  planning.

## Non-goals

- No embedded Node/npm BMad installer in v1.
- No direct execution of external BMad scripts as the plugin's primary model.
- No separate BMad scheduler, memory store, task state, or event store.
- No exact reproduction of every upstream BMad command, prompt, or template in
  the first version.
- No hidden autonomous multi-agent execution without explicit workflow/task
  dispatch.
- No bypass of AgentRuntime command, operation, workflow, task, or safety
  boundaries.
- No new core domain package until stable inert contracts are proven.

## Default layout

The default root is:

```text
.agents/bmad/
```

Within that root, use a BMad-compatible module-style layout:

```text
.agents/bmad/
├── project-context.md
├── config.yaml
├── agents/
│   ├── analyst.md
│   ├── pm.md
│   ├── architect.md
│   ├── dev.md
│   ├── qa.md
│   ├── po.md
│   ├── sm.md
│   └── tech-writer.md
├── workflows/
│   ├── quick-dev.yaml
│   ├── discovery.yaml
│   ├── prd.yaml
│   ├── architecture.yaml
│   ├── create-epics-and-stories.yaml
│   ├── create-story.yaml
│   └── dev-story.yaml
├── tasks/
│   ├── brainstorm.md
│   ├── create-prd.md
│   ├── create-architecture.md
│   ├── create-story.md
│   ├── implement-story.md
│   └── review-story.md
├── templates/
│   ├── prd.md
│   ├── architecture.md
│   ├── epic.md
│   ├── story.md
│   ├── test-strategy.md
│   └── code-review.md
├── checklists/
│   ├── prd.md
│   ├── architecture.md
│   ├── story-ready.md
│   ├── code-quality.md
│   └── release.md
├── data/
│   ├── coding-standards.md
│   ├── tech-stack.md
│   └── domain-glossary.md
└── output/
    ├── briefs/
    ├── prds/
    ├── architecture/
    ├── epics/
    ├── stories/
    ├── reviews/
    └── reports/
```

Compatibility means the selected root contains the expected BMad categories and
files can be imported from or exported to similar upstream module structures. The
plugin should not assume root-level `.bmad-core` or any upstream exact directory
name unless explicitly configured.

## Package placement

Initial implementation should live in:

```text
plugins/bmadplugin/
```

The plugin may contribute operations, resources, commands, context providers,
and workflows through existing plugin host contracts. It should not introduce a
new layer. It may depend on `core`, `sdk`, `runtime`, `orchestration`, and
allowed plugin dependencies according to the architecture matrix.

Do not add `core/bmad` in v1. BMad-specific personas, templates, and workflow
metadata are product resources. If later several packages need inert module
contracts, extract minimal IO-free types to a neutral package such as
`core/module` or `core/playbook`, not a broad framework-specific core domain.

## Concept mapping

| BMad concept | AgentRuntime concept |
|---|---|
| BMad module | plugin-contributed resource bundle |
| Agent persona | `core/agent.Spec`, optionally surfaced as `core/skill` |
| Trigger/menu command | `core/command.Spec` resource |
| Workflow | `core/workflow.Spec` |
| Task prompt | resource/template plus task step guidance |
| Checklist | artifact, validation input, or workflow gate |
| Template | resource used by generation operations |
| PRD/architecture/epic/story | file artifact and/or task artifact |
| Story implementation | `core/task.Task` with steps and outputs |
| QA/test architect review | task review step or reviewer-assigned task |
| Party mode / multi-persona discussion | future orchestrated multi-agent workflow, not v1 magic |

## Internal model

Keep BMad module parsing local to the plugin:

```go
type Root string

type Module struct {
    Root       string
    Agents     []AgentFile
    Workflows  []WorkflowFile
    Tasks      []TaskFile
    Templates  []TemplateFile
    Checklists []ChecklistFile
    Data       []DataFile
}

type AgentFile struct {
    ID          string
    Name        string
    Role        string
    Description string
    Path        string
    Triggers    []string
    Workflows   []string
}

type WorkflowFile struct {
    ID          string
    Name        string
    Description string
    Path        string
    Phases      []string
}

type DocumentRef struct {
    Kind string
    Path string
}
```

These structs are implementation details. Exposed operation outputs should use
small JSON-friendly records with bounded fields and file refs.

## Operations

All operations should use typed input/output structs with `json` and
`jsonschema` tags and should be implemented through `runtime/operation.NewTyped`
or `NewTypedResult`.

### `bmad_init`

Create the default layout and optional starter resources.

Input:

```go
type InitInput struct {
    Root      string `json:"root,omitempty" jsonschema:"description=BMad root directory; defaults to .agents/bmad."`
    Profile   string `json:"profile,omitempty" jsonschema:"description=Starter profile: minimal, agile, full."`
    Overwrite bool   `json:"overwrite,omitempty"`
}
```

Output includes root, created files, skipped files, and selected profile.

### `bmad_validate`

Validate the module structure and basic resource references.

Input:

```go
type ValidateInput struct {
    Root   string `json:"root,omitempty"`
    Strict bool   `json:"strict,omitempty"`
}
```

Checks:

- known directories exist;
- agent files have IDs/names/roles;
- workflow files have IDs/names/steps or phase declarations;
- triggers do not collide unless aliases are explicit;
- workflow references to agents/tasks/templates/checklists resolve;
- output document paths remain under the root or configured project output
  paths;
- generated AgentRuntime command/workflow resource names are unique.

### `bmad_list`

List agents, workflows, tasks, templates, checklists, data files, and output
artifacts.

### `bmad_show`

Render one agent, workflow, task, template, checklist, or document for model/UI
context.

### `bmad_generate_project_context`

Generate or refresh `project-context.md` from current project inventory,
detected language/toolchain signals, existing architecture docs, and optional
user notes.

This operation should not run arbitrary project commands. It should use existing
read operations and project inventory/toolchain status where available.

### `bmad_create_brief`

Create a product/project brief from a user idea using a template. The actual
content generation may initially be prompt/workflow driven by an agent step; the
operation should scaffold the file and metadata.

### `bmad_create_prd`

Create or update a PRD document under `output/prds/` from a brief, idea, or
existing notes.

### `bmad_create_architecture`

Create or update architecture documentation under `output/architecture/` from a
PRD and project context.

### `bmad_create_epics_and_stories`

Break a PRD and architecture document into epic/story files under
`output/epics/` and `output/stories/`.

### `bmad_create_story`

Prepare the next implementation-ready story. This should produce a file artifact
and can create a draft or ready AgentRuntime task through workflow composition.
Prefer composition with `task_create` over hidden task mutation inside the
operation.

### `bmad_story_status`

Report whether a story is ready for implementation by checking required sections,
links to context documents, acceptance criteria, and checklist state.

## Workflow resources

The plugin should provide canonical workflows as resources. The first three are
most important.

### Quick dev flow

For small, well-understood changes:

```yaml
name: bmad-quick-dev
description: Clarify intent, create a compact spec/story, implement, verify, and present.
steps:
  - id: clarify
    agent: analyst
  - id: plan
    agent: pm
    depends-on: [clarify]
  - id: implement
    agent: dev
    depends-on: [plan]
  - id: verify
    agent: qa
    depends-on: [implement]
  - id: present
    agent: tech-writer
    depends-on: [verify]
```

### Full planning flow

For larger work:

```yaml
name: bmad-full-planning
description: Build product context from idea through PRD, architecture, epics, and stories.
steps:
  - id: discovery
    agent: analyst
  - id: prd
    agent: pm
    depends-on: [discovery]
  - id: architecture
    agent: architect
    depends-on: [prd]
  - id: epics-and-stories
    agent: po
    depends-on: [architecture]
  - id: story-readiness
    agent: sm
    depends-on: [epics-and-stories]
```

### Story implementation flow

For implementation of one prepared story:

```yaml
name: bmad-dev-story
description: Implement one prepared story with verification and review.
steps:
  - id: load-story
    operation: bmad_show
  - id: implement
    agent: dev
    depends-on: [load-story]
  - id: test
    operation: project_task_run
    input:
      name: verify
    depends-on: [implement]
  - id: qa-review
    agent: qa
    depends-on: [test]
  - id: document
    agent: tech-writer
    depends-on: [qa-review]
```

Current workflow input mapping may require these to start as command prompts or
task DAGs rather than fully automatic workflow resources.

## Agents and skills

Starter agents should be contributed as resources or generated into
`.agents/bmad/agents`. Suggested v1 personas:

- `bmad-analyst`: discovery, brainstorming, research synthesis;
- `bmad-pm`: PRD and requirement shaping;
- `bmad-architect`: architecture, technical constraints, integration design;
- `bmad-dev`: implementation against a prepared story;
- `bmad-qa`: test strategy, acceptance review, risk analysis;
- `bmad-po`: backlog, epic/story organization, acceptance clarity;
- `bmad-sm`: story readiness, sequencing, process facilitation;
- `bmad-tech-writer`: docs, release notes, explanatory material.

Expose them as skills only if that matches existing skill/resource conventions.
AgentRuntime should avoid BMad-specific prompt dispatch code; command and skill
activation should use existing resource loading and session-agent/task execution.

## Command resources

Suggested commands under app resources once operations are stable:

- `/bmad init`
- `/bmad help`
- `/bmad context`
- `/bmad quick-dev <request>`
- `/bmad plan <idea>`
- `/bmad story create <epic-or-request>`
- `/bmad story dev <story-path>`
- `/bmad validate`
- `/bmad show <resource>`

Command behavior should target workflows, prompts, or operations. CLI flags may
select the submission shape but should not own semantic validation.

## Task integration

BMad implementation work should become AgentRuntime tasks.

A story task should include:

- title from story file;
- objective from story goal;
- inputs for PRD, architecture, project context, story, and relevant checklists;
- acceptance criteria copied from the story;
- outputs for diff, tests, review, and docs;
- steps for load context, implement, verify, QA review, and document.

This keeps BMad's story-driven implementation inspectable through existing
`task_create`, `task_run`, `task_modify`, review, and scheduler primitives.

## Configuration

V1 operations accept `root` and default to `.agents/bmad`.

Future config can live in `.agents/bmad/config.yaml` or an app/workspace config:

```yaml
bmad:
  root: .agents/bmad
  output: .agents/bmad/output
  profile: agile
  commands:
    prefix: bmad
  verification:
    project_task: verify
```

Root and output paths must be workspace-relative and must not escape the
workspace.

## Compatibility and import/export

The plugin should support a compatibility layer, not a hard dependency on the
upstream installer:

- import a directory containing BMad-like agents/workflows/tasks/templates into
  `.agents/bmad`;
- list unsupported files as warnings rather than failing by default;
- preserve source file content where possible;
- optionally generate AgentRuntime command/workflow resources from imported
  BMad metadata;
- export a simple module bundle later if users need sharing.

Because upstream BMad changes quickly, compatibility should be structural and
best-effort: categories, IDs, triggers, dependencies, templates, and outputs are
more important than exact upstream generated filenames.

## Safety and policy

- Init, import, context generation, and document scaffolding mutate files and
  must run through the operation safety envelope.
- Read operations such as list/show/validate are non-mutating.
- Any project verification should use existing managed process or project task
  operations, not direct `os/exec`.
- Generated prompts must not smuggle commands that bypass the runtime tool
  policy.
- Multi-agent workflows must remain explicit and auditable through task/workflow
  events.

## Implementation plan

1. Add `plugins/bmadplugin` with default root constants and typed operation
   structs.
2. Implement root resolution and workspace-safe path validation.
3. Implement module scanning for agents, workflows, tasks, templates,
   checklists, data, and output documents.
4. Implement `bmad_validate`, `bmad_list`, and `bmad_show` first.
5. Implement `bmad_init` with minimal/agile/full starter profiles.
6. Implement project-context scaffolding using existing project inventory and
   language/toolchain read operations.
7. Add story parsing/readiness checks and story task composition guidance.
8. Add starter workflow and command resources for coder.
9. Add import compatibility for BMad-like module directories.
10. Add docs and run `task verify` before commit.

## Test strategy

Unit tests should cover:

- default root `.agents/bmad`;
- configured compatibility roots;
- invalid path rejection;
- module scanning with missing optional directories;
- trigger collision diagnostics;
- workflow dependency resolution;
- template/checklist/data reference resolution;
- init profile output;
- story readiness diagnostics;
- bounded show/list output.

Integration tests should use `runtime/systemtest` for filesystem behavior and
avoid direct host filesystem/process access.

## Open questions

- Should BMad starter agents be generated into `.agents/bmad/agents` or bundled
  as immutable app resources and copied only on `bmad_init`?
- Should BMad story files directly create tasks, or should command/workflow
  composition call `task_create` explicitly? Prefer composition in v1.
- How much upstream BMad metadata should be parsed versus preserved as opaque
  markdown/YAML?
- Should `/bmad quick-dev` emit an OpenSpec-compatible spec file when
  `openspecplugin` is also enabled?
- Should BMad and OpenSpec share a future neutral `spec/story/document` helper,
  or remain separate plugins with optional composition?
