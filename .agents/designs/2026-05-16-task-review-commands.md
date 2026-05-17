# Task Plugin, Task Commands, and Review Backlog

> Historical implementation design. Current task-system progress, reliability
> gaps, and next implementation slices are tracked in
> [Task System Reliability Roadmap](../plans/2026-05-17-task-system-roadmap.md).

## Status

Initial creation slice implemented. This replaces the earlier generic
task/review command shape with a plugin-owned design: `plugins/taskplugin`
owns `/task`, the task creator agent/session, task operations, event-sourced
task state, and scheduler/executor control.

This design assumes the foundation in
[Core Task Domain](2026-05-16-core-task-domain.md), the vocabulary rules in
[Core Concepts](../../docs/concepts.md), and the architecture constraints in
[Architecture](../../docs/architecture.md) and [AGENTS.md](../../AGENTS.md).

The short-term goal has been implemented: `/task <input>` creates durable
`core/task.Task` values through a narrow task-creator agent, task operations can
inspect, modify, validate, and complete those tasks, and
`orchestration/taskexecutor` can claim ready tasks and dispatch their steps to
worker sessions. `taskplugin` now also owns `/plan`; the old plan execution
plugin has been removed from runtime assembly.

Related resources:

- [Core Task Domain](2026-05-16-core-task-domain.md)
- [Task System Reliability Roadmap](../plans/2026-05-17-task-system-roadmap.md)
- [Workspace-Scoped Project Task Execution](2026-05-16-project-task-execution.md)
- [Core Concepts](../../docs/concepts.md)
- [Architecture](../../docs/architecture.md)
- [Security](../../docs/security.md)
- [Verification](../../docs/verification.md)
- [AGENTS.md](../../AGENTS.md)

## Problem

Before this slice, user prompts could describe real work but had no canonical
event-sourced task creation path. That creation/read/modify path now exists in
`taskplugin`, and execution is represented through `core/task`,
`runtime/task`, and `orchestration/taskexecutor`.

The replacement needs to separate the durable work item from execution:

- a request asks for work;
- `/task` routes the request to a task creator;
- `task_create` creates a durable task and returns immediately;
- task state is inferred from events;
- a scheduler/executor can run ready tasks without blocking the creator;
- workers and sub-agents become an execution mechanism, not the task domain.

## Goals

- Add `plugins/taskplugin` as the optional first-party task capability bundle.
- Make `taskplugin` contribute `/task`, `/plan`, task creator and planner
  agents/sessions,
  `task_create`, grouped `task_modify`, task read/list operations, artifact
  read operations, and `task_validate`.
- Keep core task event types globally registered through
  `orchestration/eventregistry`; plugin bundles do not re-register them.
- Keep `TaskCreateRequest`, task creation results, task IO declarations, and
  task/artifact events in `core/task` as inert contracts.
- Add `runtime/task.Store` over `event.Store`; do not add a separate task
  database.
- Default tasks created through `task_create` to `ready` unless the request
  explicitly asks for `draft`.
- Ensure `task_create` returns after task creation/enqueue; it must not block
  while the task executes.
- Model task inputs and expected outputs so future schedulers can reason about
  readiness and completion.
- Keep review as a backlog follow-up while ensuring task IO/artifacts can
  represent review subjects, findings, and reports later.
- Preserve layer rules: core is inert, runtime projects state and readiness,
  orchestration/plugins perform dispatch, and adapters only transport input.

## Non-goals

- Do not add `core/plan`; committed executable decomposition is represented by
  `task.Task.Steps`.
- Do not add `core/review` or a full review workflow in this slice.
- Do not add a special `.agents/commands/*.md` task front matter schema.
- Do not refactor `orchestration/subagent` in this slice.
- Do not add model calls, IO, storage implementation, or process execution to
  `core/task`.
- Do not rename `core/project.Task` in this slice, though it remains a known
  source of naming confusion.

## Conceptual Model

Use the vocabulary from [Core Concepts](../../docs/concepts.md):

- the user message is a request;
- `/task` is a command;
- `taskplugin` routes `/task` to a dedicated task session;
- the task session runs a narrow task-creator agent;
- the task-creator agent clarifies if needed, then returns structured task data
  or calls `task_create`;
- `task_create` appends task events and returns a `TaskCreateResult`;
- `core/task.Task` is the durable work objective;
- task execution is a runtime attempt against a task;
- future scheduling chooses ready work and dispatches workers.

```text
user request text
  -> command.Invocation{/task}
  -> task session
  -> task creator agent
  -> task_create(TaskCreateRequest)
  -> task events in event.Store
  -> runtime/task.Store projection
  -> ready task for scheduler/executor
```

`task_create` is not a "run this task to completion" call. It creates or
records the task and returns. Progress, artifacts, completion, and failure are
later events.

## Task Plugin Contributions

`plugins/taskplugin` contributes:

- operation `task_create`;
- operation `task_modify`;
- operation `task_get`;
- operation `task_list`;
- operation `task_list_artifacts`;
- operation `task_get_artifact`;
- operation `task_read_artifact`;
- operation `task_validate`;
- operation `review_request`;
- command `/task`;
- command `/plan`;
- built-in agent `task` or `task-creator`;
- built-in agent `task-planner`;
- dedicated session `task`;
- dedicated session `task-planner`;
- automatic scheduler/executor wiring in local launch when `taskplugin` is
  selected.

Scheduler-visible anomalies that affect a task are task history, not only local
process state: ignored stale worker results and retry exhaustion are emitted as
`task.scheduler_diagnostic` events so `task_get` and session replay can explain
what happened. Diagnostic events are non-lifecycle records and must not move
the projected current execution.

Task event types remain owned by `core/task` and are registered by
`orchestration/eventregistry` for all runtimes.

The task creator agent is intentionally narrow. It is not a general-purpose
coding agent. Its job is to turn the input plus available context into a
structured task and then stop.

Initial task creator guidance:

```text
You create structured tasks only.
Given the user's request and context, produce a complete task.
Prefer creating a task with explicit inputs, outputs, assumptions, and
acceptance criteria over asking clarification.
Ask clarification only when the request cannot be represented as a useful draft
or ready task.
When enough information exists, call task_create.
If the user asks for immediate execution, create the task with status=ready,
call task_run, and report whether it started, is already running, is not ready,
or is waiting for capacity.
Use task_modify for follow-up changes to an existing task.
After task_create succeeds, send a concise final message with the task id,
title, status, and expected outputs.
```

Initial tool set for the task creator:

- `task_create`;
- `task_modify` for grouped follow-up management;
- `task_get`, `task_list`, `task_list_artifacts`, `task_get_artifact`, and
  `task_validate` for inspection and validation;
- `task_run` and `task_scheduler_status` for immediate-execution feedback;
- `review_request`;
- `clarify` when the host provides it;

The planner agent is also intentionally narrow. It is driven by Markdown
instructions and must only clarify, create/update a `draft` task, present it to
the user, loop on refinement, and mark the task `ready` after human approval.
It must not execute the planned work itself. Planner-created drafts should stay
visible in the current session's default `task_list` by attributing delegated
draft creation to the parent user thread, and approval/refinement must update
the same draft task instead of creating a second task.

`apps/coder` should select `taskplugin`. It should not own task command
semantics directly.

## Command Routing

`/task <input>` targets the task session contributed by `taskplugin`.
`/plan <input>` targets the task planner session contributed by `taskplugin`.

The dedicated session is preferred over a direct command-to-agent execution path
because a session already owns the loop behavior needed here:

- limited tools and context;
- instruction-following until completion;
- clarification turns;
- operation results;
- event/thread correlation;
- stop behavior after `task_create`.

The task session should run in an isolated child or branch thread tied to the
parent run/thread for traceability. The exact thread mechanics belong to
orchestration/session plumbing, not `core/task`.

Same-name commands and resources use existing resource identity and resolution:
`resource.ResourceID`, origin/namespace, and resolver precedence. There is no
task-specific override system.

## Core Task Create Shape

Add the inert task create request shape to `core/task`.

```go
package task

type TaskCreateKind string

const (
    TaskCreateKindGeneric TaskCreateKind = "generic"
)

type TaskCreateRequest struct {
    ID          ID             `json:"id,omitempty"`
    Kind        TaskCreateKind `json:"kind,omitempty"`
    Instruction string         `json:"instruction,omitempty"`
    Intent      string         `json:"intent,omitempty"`
    Objective   string         `json:"objective,omitempty"`
    Title       string         `json:"title,omitempty"`
    Description string         `json:"description,omitempty"`

    AcceptanceCriteria []string          `json:"acceptance_criteria,omitempty"`
    Inputs             []ArtifactSpec    `json:"inputs,omitempty"`
    Outputs            []ArtifactSpec    `json:"outputs,omitempty"`
    Scope              []string          `json:"scope,omitempty"`
    Constraints        []string          `json:"constraints,omitempty"`
    Labels             []string          `json:"labels,omitempty"`
    Priority           Priority          `json:"priority,omitempty"`
    Assignee           Role              `json:"assignee,omitempty"`
    Owner              Role              `json:"owner,omitempty"`
    WorkspaceID        workspace.ID      `json:"workspace_id,omitempty"`
    ProjectID          project.ID        `json:"project_id,omitempty"`
    WorkflowRef        workflow.Name     `json:"workflow_ref,omitempty"`
    SuggestedSteps     []Step            `json:"suggested_steps,omitempty"`
    Status             Status            `json:"status,omitempty"`
    Metadata           map[string]string `json:"metadata,omitempty"`
}

type TaskCreateResult struct {
    Task        Task         `json:"task"`
    Diagnostics []Diagnostic `json:"diagnostics,omitempty"`
}
```

`task_create` assigns a task ID when absent. Caller-supplied IDs must be
unique; creation appends with an empty-stream expectation and rejects duplicate
task IDs. The default status is `ready`. Explicit `draft` is respected.

Task operation input structs must use typed operation contracts and
model-facing JSON Schema tags for enum-like fields such as task status,
priority, artifact kind, and creation kind.

Post-create writes are grouped under one typed operation:

```go
type TaskModifyRequest struct {
    ID            ID                 `json:"id"`
    Modifications []TaskModification `json:"modifications"`
    Reason        string             `json:"reason,omitempty"`
}
```

The reflected outer schema is narrowed with a `oneOf` item schema for each
modification discriminator. Initial modification operations:

- `update_metadata`
- `add_acceptance_criterion`
- `add_output`
- `add_step`
- `update_step`
- `remove_step`
- `set_step_status`
- `add_artifact`
- `update_artifact`
- `remove_artifact`
- `set_status`
- `reopen`
- `reopen_step`
- `complete`

`complete` validates required outputs and step terminal state. Forced
completion must list explicit validation check codes in `force_overrides`,
such as `required_output` or `steps_terminal`; there is no blanket force
bypass. Step execution state supports `waiting`, `running`, `blocked`,
`skipped`, `completed`, `failed`, and `cancelled`.

Terminal state changes are explicit. `set_status` cannot silently move a
terminal task (`completed`, `cancelled`, `failed`) back to an active status;
use `reopen`. `set_step_status` cannot silently move a terminal step back to
`waiting`, `running`, or `blocked`; use `reopen_step`. `remove_step` rejects
steps with projected execution state so task revisions do not drop execution
history.

Artifact modifications use task-wide artifact IDs. `add_artifact` rejects an
explicit ID that already exists in any task, execution, or step scope, and
generated IDs are checked against the same scoped projection. Step artifacts do
not require callers to know an execution ID: when `step_id` is set and
`execution_id` is omitted, the operation uses the current execution or the
stable manual execution ID `manual`.

## Task IO and Artifacts

Tasks and steps need to describe what inputs they require and what outputs are
expected. This supports future scheduling, dependency resolution, and completion
checks without forcing every deliverable into a rigid schema.

Add flexible artifact declarations to `Task`, `Step`, and
`TaskCreateRequest`:

```go
type ArtifactKind string

const (
    ArtifactText       ArtifactKind = "text"
    ArtifactFile       ArtifactKind = "file"
    ArtifactPatch      ArtifactKind = "patch"
    ArtifactDiff       ArtifactKind = "diff"
    ArtifactReport     ArtifactKind = "report"
    ArtifactReview     ArtifactKind = "review"
    ArtifactTestResult ArtifactKind = "test_result"
    ArtifactBuild      ArtifactKind = "build"
    ArtifactReference  ArtifactKind = "reference"
    ArtifactJSON       ArtifactKind = "json"
)

type ArtifactSpec struct {
    ID          string            `json:"id,omitempty"`
    Name        string            `json:"name,omitempty"`
    Kind        ArtifactKind      `json:"kind,omitempty"`
    Description string            `json:"description,omitempty"`
    Required    bool              `json:"required,omitempty"`
    Schema      operation.Type    `json:"schema,omitempty"`
    Ref         string            `json:"ref,omitempty"`
    Value       operation.Value   `json:"value,omitempty"`
    Metadata    map[string]string `json:"metadata,omitempty"`
}
```

Human-readable deliverable expectations still belong in acceptance criteria.
For example:

```text
kind=deliverable
description=List of reviewed core packages must be provided as flat JSON in the
format {"packages":[{"path":"core/task","findings":[]}]}
```

Artifact specs provide a structured frame. Acceptance criteria provide the
natural-language contract a reviewer or agent can judge.

Completion gates are intentionally split: required output artifacts and
terminal step state are hard validation checks; acceptance criteria are
reported as manual/advisory checks for a caller or reviewer to judge.
`task_get_artifact` must render artifact values, refs, descriptions, and
metadata in its model-facing text so inline values are readable without relying
on raw JSON.

Scheduling interpretation:

```text
Task ready if:
  status == ready
  required task inputs are available
  no blocking task dependencies remain

Step ready if:
  depends_on steps completed
  required step inputs are available
  worker capacity/profile is available
```

For the first implementation, structural declaration is enough. Deeper
availability checks can come later through artifact refs/providers.

## Event-Sourced State

Task state is inferred from events. There is no separate task database.

Add `runtime/task.Store` over `event.Store`:

```go
type Store interface {
    Create(ctx context.Context, taskID coretask.ID, events ...event.Event) error
    Append(ctx context.Context, taskID coretask.ID, events ...event.Event) error
    Index(ctx context.Context, summary coretask.TaskSummary) error
    List(ctx context.Context) ([]coretask.TaskSummary, error)
    Load(ctx context.Context, taskID coretask.ID) ([]event.Record, error)
    Project(ctx context.Context, taskID coretask.ID) (State, error)
}
```

Task stream identity:

```text
task:{task_id}
task:index
```

The store is a convenience over event storage and projection. It must not own
business logic that belongs in `core/task` validation or `runtime/task`
readiness helpers.

Task creation and artifact events:

```text
task.create_requested
task.created
task.revised
task.status_changed
task.artifact_added
task.artifact_updated
task.artifact_removed
task.step_status_changed
task.indexed
task.execution_started
task.execution_interrupted
task.step_dispatched
task.step_progressed
task.step_completed
task.step_failed
task.step_cancelled
task.execution_completed
task.execution_failed
task.execution_cancelled
```

`task.artifact_added` should carry task, execution, and optional step identity:

```go
type ArtifactAdded struct {
    TaskID      ID          `json:"task_id"`
    ExecutionID ExecutionID `json:"execution_id,omitempty"`
    StepID      StepID      `json:"step_id,omitempty"`
    Artifact    ArtifactSpec `json:"artifact"`
}
```

Actual produced artifacts are recorded by events and projected into state.
`Execution.Output` and `StepExecution.Output` remain the actual output values
for one execution attempt.

Projection keeps artifact scopes distinct. Task artifacts project to
`Task.Artifacts`, execution artifacts project to `Execution.Artifacts`, and
step artifacts project only to `StepExecution.Artifacts`; list/get renderers
are responsible for presenting all scopes together with clear scope labels.

## Scheduler and Execution Direction

The first scheduler slice is implemented in `orchestration/taskexecutor`.
`runtime/task` remains pure projection/readiness logic, while
`orchestration/taskexecutor` owns claiming, worker dispatch, cancellation, and
execution event writes.

Long-term execution model:

```text
ready task
  -> scheduler starts task execution
  -> runtime/task.ReadySteps
  -> resolve step assignee/profile
  -> dispatch worker
  -> emit task step events
  -> collect artifacts/output
  -> complete/fail/cancel execution
```

Role-to-profile defaults:

| Assignee | Default profile |
|---|---|
| `developer` | `worker` |
| `reviewer` | `reviewer`, fallback `worker` |
| `tester` | `worker` |
| `explorer` | `explorer` |
| `human` | blocked, clarification, or manual handoff |

`runtime/task` owns pure scheduling and readiness helpers. Concrete execution
and worker dispatch belong in orchestration. The first worker backend is
`ChannelWorker`, which wraps `orchestration/client.ChannelClient` behind a
small `WorkerClient` interface. This keeps scheduler logic independent from
session internals and from the current `orchestration/subagent` package while
still reusing profiled sessions, tool projection, safety approval, transcript
storage, cancellation, and run waiting.

Current scheduler behavior:

- local launch starts the scheduler when `taskplugin` is selected;
- local launch wraps the event store with a task-ready notifier so appended
  `task.indexed` records with `ready` summaries trigger scheduler claim
  attempts after the task list projection has been updated;
- periodic scans of the task index remain as reconciliation for missed,
  out-of-process, or externally appended events, not as the primary trigger;
- `task_run` schedules one ready task asynchronously without blocking the
  caller until worker completion;
- coder and task-creator instructions call `task_run` after creating or moving
  a task to `ready` when the user asks for immediate execution, then report
  started, already-running, not-ready, or capacity-waiting state;
- `task_scheduler_status` reports enablement, active state, capacity, and
  running task IDs;
- `task_scheduler_set_enabled` pauses or resumes automatic ready-task
  scheduling; manual `task_run` remains available while automatic scheduling is
  disabled;
- ready tasks are claimed with `task.execution_started` using the task stream
  sequence as an optimistic precondition;
- declared task steps run as a dependency DAG through `runtime/task.ReadySteps`;
- tasks without declared steps run as one whole-task worker prompt;
- human-assigned work is blocked instead of dispatched;
- role-to-profile routing can be configured on the scheduler, with default
  mappings for developer/tester, reviewer, and explorer roles;
- blocked interrupted executions can resume when the task is marked `ready`
  after the blocking human/manual step is cleared;
- worker output is recorded as step or execution output plus an artifact only
  after re-projecting the task stream and confirming the same execution/step is
  still running;
- missing declared step outputs are bound to produced artifacts with the
  declared output IDs/names, and automatic execution completion validates
  required task outputs before writing `task.execution_completed`;
- when all declared steps are terminal but required task-level outputs are
  missing, the scheduler runs a finalization worker pass to synthesize those
  aggregate outputs from completed step evidence before it blocks completion;
- failed completion validation after finalization blocks the execution with a
  visible reason so a caller can add missing artifacts or revise the task and
  mark it ready again;
- terminal one-shot and goal turns wait for scheduler-run tasks from the
  submitted turn to finish before closing the local runtime; REPL turns watch
  briefly and then return the prompt while background tasks continue;
- scheduler shutdown cancellation is propagated into in-flight worker runs.

The current control operations are intentionally local-runtime controls. They
do not introduce a second task store; task/execution state still comes from the
event store projection.

### Scheduler Concurrency Hardening

The scheduler adds a normal background writer to task streams. That is the
right execution shape, but it makes task-stream contention a first-class
correctness concern because user/model `task_modify` calls, scheduler claims,
step dispatch, terminal step writes, task blocking, and task indexing can now
overlap.

Current concurrency posture:

- task claims use `ProjectWithSequence` plus `AppendExpected`, so concurrent
  claims have one winner and conflict losers skip cleanly;
- ready task notification is event-triggered from indexed ready summaries
  through a local event-store wrapper, with periodic index scans retained as
  fallback reconciliation;
- terminal step/execution writes re-project state and retry conflicts, so stale
  worker completion does not overwrite newer cancellation or blocking state;
- scheduler dispatch, block, and dependent-cancellation transitions use
  current-state checks with expected-sequence appends;
- terminal retry loops are bounded, context-aware, and lightly backed off;
- task index writes are blind appends to `task:index`, avoiding stale global
  expected-sequence conflicts;
- `task_modify` reloads and reapplies modifications on append conflicts and
  returns `task_conflict_retry` if contention persists;
- thread-store create/append/fork writes use bounded conflict retries, so
  concurrent worker/session transcript writes do not fail on one stale stream
  sequence read;
- scheduler goroutine errors are observable through scheduler status
  diagnostics and an optional error hook;
- task-affecting scheduler anomalies are durable
  `task.scheduler_diagnostic` events that do not change task lifecycle or the
  current execution pointer.
- automatic ready-task notifications append task diagnostics when scheduling is
  disabled or local capacity is saturated, so the task history explains why a
  ready task did not immediately start.

Current scheduler/task-operation hardening coverage includes:

1. focused reproduction tests for concurrent scheduler claims, stale worker
   terminal writes, human-blocker resume, worker shutdown cancellation, and
   concurrent task modification conflicts;
2. event-triggered ready-task burst coverage where notifications saturate local
   capacity and index-scan reconciliation must complete the remaining ready
   tasks;
3. SQLite/thread-store concurrency stress coverage for shared event streams and
   thread index contention.

Future hardening should add longer soak/multi-process scheduler coverage when
durable queues or external worker pools are introduced.

## Sub-Agents and Workers

Sub-agents are an execution mechanism, not a separate task domain.

The task executor owns the task execution loop around `core/task` events:

```text
task.Step
  -> worker routing by assignee/profile
  -> child session / worker execution
  -> task.step_progressed
  -> task.step_completed or task.step_failed
```

Do not refactor `orchestration/subagent` in this slice. The remaining sub-agent
abstraction can be reconsidered once the task executor has richer worker pools;
it may become an internal worker backend or disappear into the task executor.

## Plan Relationship

Do not introduce `core/plan` now.

`task.Task.Steps` is the committed executable decomposition of a task. The DAG
is represented by `Step.DependsOn`.

Use this rule:

```text
If it is the committed executable decomposition, store it as task.Steps.
If it is a proposal, rationale, or competing alternative before commitment,
that is a future plan artifact.
```

Add `core/plan` only if the product needs versioned competing plans, plan
approval, plan diffs, or plan rationale separate from task revision history.

## Review Backlog

Review must not be blocked by this design, but it is not implemented in the
first taskplugin slice.

Backlog:

- add `core/review` for review subject, scope, findings, reports, verdicts, and
  review events;
- add `review_request` operation to create/link a review task and review spec;
- add review artifact kinds and report projection;
- add `/review` as either a taskplugin command or a reviewplugin command using
  the same task creation path.

The task IO/artifact model must support review later:

```text
review task input:
  current diff, PR, file set, package list, or document

review task output:
  review report, findings, recommendations, and verdict
```

Until `core/review` exists, review-specific semantics should remain in artifact
descriptions, labels, and metadata rather than as task-only fields.

## Migration From Earlier Plan Execution

Completed replacement:

1. Built `taskplugin` creation path and validated `/task` UI behavior.
2. Added scheduler/executor behavior using `core/task` events,
   `runtime/task` readiness helpers, and `orchestration/taskexecutor`.
3. Added `/plan` for draft task planning and approval before ready-state
   scheduling.
4. Matched execution progress with task events and worker profiles.
5. Replaced coder/local launch references with `task`.
6. Removed the old plan execution plugin and its event catalog references.

This is a pre-1.0 rewrite. No compatibility shim remains.

## Implementation Sequence

Completed in this slice:

1. Updated this design and linked it from the core task domain design.
2. Extended `core/task` with creation, modification, artifact, validation, and
   event contracts.
3. Extended `runtime/task` projection for artifacts, step progress, manual step
   status changes, execution-step reconciliation, and terminal metadata cleanup.
4. Added `runtime/task.Store` over `event.Store` using `task:{task_id}` streams
   plus the derived append-only `task:index` stream.
5. Added `plugins/taskplugin` with `task_create`, grouped `task_modify`, task
   read/list/artifact/validation operations, task agent, task session, and
   `/task`.
6. Wired `apps/coder` to select taskplugin and expose task operations to
   delegated child agents.
7. Added command/session and operation tests for `/task`, duplicate task IDs,
   task modification, artifact scopes, validation, lifecycle reopening, and
   forced completion overrides.
8. Added `orchestration/taskexecutor` with optimistic task claiming, DAG
   execution, human blocking, whole-task fallback execution, and a
   `ChannelClient`-backed worker backend.
9. Added `/plan`, the built-in task planner agent/session, and local launch
   scheduler startup when `taskplugin` is selected.
10. Added explicit scheduler controls through `task_run`,
    `task_scheduler_status`, and `task_scheduler_set_enabled`, plus
    configurable scheduler role-to-profile routing.
11. Added local event-triggered ready-task notification, bounded/context-aware
    scheduler retries, expected-sequence scheduler transitions, task modification
    conflict retry UX, and scheduler status diagnostics.
12. Added bounded thread-store conflict retries for concurrent worker/session
    transcript writes.
13. Added terminal rendering for typed `task.*` runtime events and persisted
    task runtime events in session threads for replay.
14. Fixed direct-channel run-event shutdown ordering so live run forwarding
    cannot send on a closed run event channel.
15. Added task origin thread/run metadata and scheduler-to-session runtime event
    mirroring for background task progress.
16. Added oversized artifact/result ergonomics: tool-result replacements carry
    previews/tails, task artifact reads default to bounded previews, and
    scheduler worker outputs are normalized into referenced artifacts when they
    exceed provider-facing result limits.
17. Added the first worker-pool slice: scheduler config can define role-specific
    profile lists and per-role capacity, profile selection rotates within a
    role pool, channel workers try fallback profiles in order, and scheduler
    status exposes running/capacity/profile details by role.
18. Added `review_request` to create reviewer-assigned review tasks linked to
    an existing task, and `task_read_artifact` for bounded inline or safe
    workspace-ref artifact content reads.
19. Added immediate-execution guidance for coder and task-creator agents,
    exposed scheduler controls in the task session, and recorded durable task
    diagnostics when automatic ready-task scheduling is disabled or deferred by
    local capacity.
20. Made `task coder:live-test` use a repository-local writable state directory
    and wrapped local event-store open errors with the database path.
21. Made terminal one-shot and goal turns wait for scheduler-run tasks from the
    submitted turn to finish before closing the local runtime.
22. Added scheduler finalization for multi-step tasks: after all declared steps
    are terminal, missing required task-level outputs are synthesized from
    completed step evidence before completion validation blocks the task.

Follow-up slices now live in
[Task System Reliability Roadmap](../plans/2026-05-17-task-system-roadmap.md).
Do not add new progress or next-step lists to this historical implementation
design.

## Testing

- `TaskCreateRequest` converts into a valid task.
- `task_create` assigns an ID when absent.
- `task_create` defaults status to `ready`.
- Explicit `draft` remains draft.
- `task_create` emits task events and returns immediately.
- `runtime/task.Store` loads and projects `task:{task_id}` events.
- `runtime/task.Store` indexes summaries through `task:index` without stale
  shared stream preconditions.
- `task.artifact_added`, `task.artifact_updated`, and `task.artifact_removed`
  project into the correct task, execution, or step artifact scope.
- Step-scoped artifacts are not duplicated into execution artifacts.
- `task_get_artifact` model text includes refs, descriptions, metadata, and
  bounded value previews by default; full inline values require
  `include_value=true`.
- `task_validate` reports required output and terminal step checks.
- `complete` requires all non-manual checks or explicit `force_overrides`.
- Terminal tasks require `reopen`; terminal steps require `reopen_step`.
- `remove_step` rejects steps with projected execution state.
- `taskplugin` contributes `task_create`, grouped `task_modify`, task
  read/list/artifact/validation operations, scheduler controls, `/task`,
  `/plan`, and the task and planner agent/sessions.
- `/task` targets the dedicated task session.
- `/plan` targets the dedicated planner session and creates draft tasks until
  approval.
- `orchestration/taskexecutor` claims only one ready execution per task stream.
- `orchestration/taskexecutor` reacts to indexed ready summaries and keeps
  periodic index scans as reconciliation.
- `orchestration/taskexecutor` runs ready DAG steps in dependency order.
- `orchestration/taskexecutor` binds declared step outputs to produced
  artifacts, runs a finalization pass for missing required task-level outputs
  after all steps are terminal, and only blocks automatic completion when
  required outputs remain missing after that pass.
- Large scheduler worker outputs are represented as replacement references with
  preview metadata instead of embedding provider-sized payloads in task events.
- `task_run` schedules ready tasks asynchronously.
- Immediate-execution task requests call `task_run` after the task is ready and
  return explicit scheduler-state feedback.
- `task_scheduler_status` and `task_scheduler_set_enabled` expose local
  scheduler control state.
- Automatic ready-task notifications record task diagnostics when scheduling is
  disabled or deferred by capacity.
- Terminal UI renders typed task lifecycle/progress/artifact events and filters
  noisy model-stream progress.
- Terminal one-shot and goal turns wait for scheduler-run tasks from the
  submitted turn to reach terminal state before closing the local runtime.
- Harness persists `task.*` runtime events so task feedback can be replayed
  from the session thread.
- Scheduler-produced task execution events are mirrored into the originating
  session thread when the task carries origin metadata.
- Human-assigned steps block the task instead of dispatching a worker.
- Concurrent scheduler claims create one execution, and concurrent
  `task_modify` calls with independent artifacts retry stream conflicts.
- Event-triggered ready-task burst scheduling completes saturated ready tasks
  through index-scan reconciliation while respecting scheduler parallelism.
- Coder/local launch, terminal feedback, Slack feedback, and event catalog
  references use task events and task worker profiles.
- Longer soak/multi-process scheduler tests are tracked in
  [Task System Reliability Roadmap](../plans/2026-05-17-task-system-roadmap.md).
