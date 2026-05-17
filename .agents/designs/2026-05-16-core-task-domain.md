# Core Task Domain

> Historical domain design. Current task-system progress, reliability gaps,
> and next implementation slices are tracked in
> [Task System Reliability Roadmap](../plans/2026-05-17-task-system-roadmap.md).

## Status

Implemented as the task foundation, scheduler slice, and plan execution
replacement. The old plan execution plugin has been removed from runtime
assembly; `/plan` is now a task-planning command and execution progress is
reported through `task.*` events.

- [Task Plugin, Task Commands, and Review Backlog](2026-05-16-task-review-commands.md):
  `taskplugin` ownership of `/task`, `/plan`, task
  creation/modification/read operations, event-sourced task state, scheduler
  execution, and the review follow-up path.
- [Task System Reliability Roadmap](../plans/2026-05-17-task-system-roadmap.md):
  canonical current state, remaining reliability gaps, and next slices.

## Model

`core/task` is the user-facing work item domain:

- `Task`: title, description, objective, acceptance criteria, status, priority,
  assignee, creator, owner, workspace/project refs, task input/output artifact
  declarations, produced artifacts, scope, constraints, labels, metadata,
  optional workflow, and ordered steps.
- `Step`: title, description, objective, acceptance criteria, dependencies,
  input/output artifact declarations, assignee/profile, scope, and metadata.
- `Execution`: one attempt to run a task, with execution status, per-step
  execution state, execution artifacts, optional workflow, output, error, and
  timestamps.
- `ExecutionRequest` / `ExecutionResult`: inert control shapes for task run,
  continue, and future cancel operations.
- `SchedulerStatusRequest`, `SchedulerSetEnabledRequest`, and
  `SchedulerStatusResult`: inert scheduler control-plane shapes for local
  task execution status and automatic scheduling enablement.

The status vocabulary covers the current plan executor:

- task/execution: `draft`, `ready`, `running`, `blocked`, `completed`,
  `failed`, `cancelled`, `interrupted`;
- step execution: `waiting`, `running`, `blocked`, `skipped`, `completed`,
  `failed`, `cancelled`.

## Earlier Plan Executor Mapping

Earlier plan executor types mapped directly:

| Earlier plan executor type | New task model |
|---|---|
| `PlanSpec` | `task.Task` |
| `StepSpec` | `task.Step` |
| `PlanState` | `task.Execution` plus projected `task.Task` |
| `StepExec` | `task.StepExecution` |
| plan phase `drafting` | task status `draft` |
| plan phase `executing` | execution status `running` |
| plan phase `interrupted` | execution status `interrupted` |
| sub-agent worker id | step execution `external_id` |
| sub-agent profile | step `profile` / step execution `profile` |

The old plan execution plugin has been removed from runtime assembly. The table
above is retained only as historical mapping context for the migration.

## Runtime Layer

`runtime/task` is an event projection and coordination layer. It:

- projects `core/task` events into task/execution state;
- projects artifact events into task, execution, or step artifact scopes without
  duplicating step artifacts into execution artifacts;
- computes ready steps from dependencies and step execution status;
- detects terminal step sets against the current declared task steps and treats
  missing execution state as non-terminal;
- reconciles active execution step maps when a task revision adds or removes
  steps;
- marks waiting dependents cancelled after a failed dependency;
- marks running executions interrupted when an external runner disappears.
- materializes the latest `StepProgressed` message on `StepExecution` as
  `last_progress`.
- clears stale terminal metadata when manual step status changes reopen a step
  to `waiting`, `running`, or `blocked`.
- exposes optimistic stream-sequence helpers so orchestration can claim ready
  tasks without adding a separate scheduler store.

It does not spawn agents, run processes, call operations, or own concrete
storage. `runtime/task.Store` is a thin event-store wrapper for task streams and
the derived `task:index` stream; concrete event stores remain in runtime or
adapters.

`orchestration/taskexecutor` owns concrete scheduling. It reacts to indexed
ready task summaries after the task list projection is updated, keeps periodic
index scans as reconciliation for missed or out-of-process events, claims one
execution with an expected task stream sequence, dispatches ready steps through
a worker backend, and records execution/step events back to the task stream.
Worker terminal writes are also expected-sequence appends after re-projecting
current task state, so stale worker completions do not overwrite newer
cancellation, blocking, or manual edits. Declared step outputs are bound to
produced artifacts. When every declared step is terminal but required
task-level outputs are still missing, the scheduler runs a finalization worker
pass that synthesizes aggregate outputs from completed step evidence before
automatic completion validation blocks the task. Interrupted executions can be
resumed when the task becomes `ready` again. Scheduler-side anomalies that affect one task, such as
ignored stale worker output or retry exhaustion, are recorded as durable
`task.scheduler_diagnostic` events and projected onto the task execution or
step. These diagnostics are non-lifecycle events and must not change the
current execution pointer.

Because the scheduler is a background task-stream writer, scheduler/user
concurrency is part of the domain's hardening work. Claims, dispatch, blocking,
dependent cancellation, terminal writes, and task modification retries now use
optimistic stream checks or bounded retry policy. Thread-store create, append,
and fork writes also retry append conflicts so concurrent worker/session
transcript writes do not undermine task execution. Remaining work is focused
load/concurrency coverage and shaping durable/external worker queues behind the
scheduler worker boundary. The first local worker-pool slice is implemented:
roles can define profile lists and per-role capacity, profile selection rotates
within a role pool, fallback profiles are tried in order by the channel worker,
and scheduler status reports running/capacity/profile details by role.

## Relationship To Other Domains

- `core/workflow` remains the executable DAG/action recipe model.
- `core/task` owns user-facing objective, ownership, status, and acceptance
  criteria.
- `/task`, taskplugin ownership, and the review backlog are covered in
  [Task and Review Commands](2026-05-16-task-review-commands.md).
- `core/project.Task` currently means discovered project runner entry from
  Taskfile/Makefile/package scripts. It should later be renamed to avoid
  confusion, but is not changed in this slice.
- `project_task_run` is a concrete operation that can later be referenced from
  task workflow/step execution.

## Current Plugin Slice

`plugins/taskplugin` now provides the first task surface:

- `/task`, a dedicated task creator session, and a narrow task creator agent;
- `/plan`, a dedicated planner session, and a narrow Markdown-instructed
  planner agent that creates draft tasks and marks them ready after approval;
- `task_create`, which appends creation events and returns immediately;
- grouped `task_modify`, with explicit `reopen`, `reopen_step`, artifact
  mutations, status changes, and completion;
- `task_get`, `task_list`, `task_list_artifacts`, `task_get_artifact`, and
  `task_validate`.
- `task_run`, `task_scheduler_status`, and `task_scheduler_set_enabled` for
  explicit execution control and scheduler inspection.

Task lifecycle semantics are intentionally explicit:

- terminal tasks (`completed`, `failed`, `cancelled`) require `reopen` before
  moving back to an active status;
- terminal steps require `reopen_step` before moving back to `waiting`,
  `running`, or `blocked`;
- `complete` validates required outputs and terminal step state unless the
  caller lists explicit `force_overrides` check codes;
- produced artifact IDs are task-wide unique.

## Active Roadmap

Current task-system reliability work is tracked in
[Task System Reliability Roadmap](../plans/2026-05-17-task-system-roadmap.md).
Do not add new progress or next-step lists to this historical domain design.

Keep validating task steps as a DAG. `core/task.Task.Validate`
rejects unknown dependencies, self-dependencies, and dependency cycles so
durable task definitions cannot become permanently unrunnable.
