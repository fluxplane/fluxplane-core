# Core Task Domain

## Status

Implemented as a foundation slice. `planexecplugin` is intentionally not
migrated yet, but the new model is shaped so the current plan execution model
can move onto it without losing semantics.

- [Task Plugin, Task Commands, and Review Backlog](2026-05-16-task-review-commands.md):
  `taskplugin` ownership of `/task`, task creation/modification/read
  operations, event-sourced task state, and the review follow-up path.

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
- `ExecutionRequest` / `ExecutionResult`: inert control shapes for future
  run/continue/cancel operations.

The status vocabulary covers the current plan executor:

- task/execution: `draft`, `ready`, `running`, `blocked`, `completed`,
  `failed`, `cancelled`, `interrupted`;
- step execution: `waiting`, `running`, `blocked`, `skipped`, `completed`,
  `failed`, `cancelled`.

## Planexec Mapping

Current plan executor types can map directly:

| Current planexec type | New task model |
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

The existing `plan` operation can keep its public actions during migration:
`create`, `revise`, `execute`, `wait`, `status`, `step_output`, and `cancel`.

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

It does not spawn agents, run processes, call operations, or own concrete
storage. `runtime/task.Store` is a thin event-store wrapper for task streams and
the derived `task:index` stream; concrete event stores remain in runtime or
adapters.

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
- `task_create`, which appends creation events and returns immediately;
- grouped `task_modify`, with explicit `reopen`, `reopen_step`, artifact
  mutations, status changes, and completion;
- `task_get`, `task_list`, `task_list_artifacts`, `task_get_artifact`, and
  `task_validate`.

Task lifecycle semantics are intentionally explicit:

- terminal tasks (`completed`, `failed`, `cancelled`) require `reopen` before
  moving back to an active status;
- terminal steps require `reopen_step` before moving back to `waiting`,
  `running`, or `blocked`;
- `complete` validates required outputs and terminal step state unless the
  caller lists explicit `force_overrides` check codes;
- produced artifact IDs are task-wide unique.

## Next Migration Slice

Refactor or replace `planexecplugin` once the task scheduler/executor covers the
same behavior:

1. Add task scheduler/executor behavior using task events and runtime readiness
   helpers.
2. Match remaining planexec behavior using task execution events and workers.
3. Replace app references from `planexec` to `task`.
4. Delete `planexecplugin` and its plugin-local plan events once replacement is
   complete.

Before migration, keep validating task steps as a DAG. `core/task.Task.Validate`
rejects unknown dependencies, self-dependencies, and dependency cycles so
durable task definitions cannot become permanently unrunnable.
