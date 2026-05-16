# Core Task Domain

## Status

Implemented as a foundation slice. `planexecplugin` is intentionally not
migrated yet, but the new model is shaped so the current plan execution model
can move onto it without losing semantics.

## Model

`core/task` is the user-facing work item domain:

- `Task`: title, description, objective, acceptance criteria, status, priority,
  assignee, creator, owner, workspace/project refs, labels, metadata, optional
  workflow, and ordered steps.
- `Step`: title, description, objective, acceptance criteria, dependencies,
  assignee/profile, scope, and metadata.
- `Execution`: one attempt to run a task, with execution status, per-step
  execution state, optional workflow, output, error, and timestamps.
- `ExecutionRequest` / `ExecutionResult`: inert control shapes for future
  run/continue/cancel operations.

The status vocabulary covers the current plan executor:

- task/execution: `draft`, `ready`, `running`, `blocked`, `completed`,
  `failed`, `cancelled`, `interrupted`;
- step execution: `waiting`, `running`, `completed`, `failed`, `cancelled`.

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
- computes ready steps from dependencies and step execution status;
- detects terminal step sets against the current declared task steps and treats
  missing execution state as non-terminal;
- reconciles active execution step maps when a task revision adds or removes
  steps;
- marks waiting dependents cancelled after a failed dependency;
- marks running executions interrupted when an external runner disappears.
- materializes the latest `StepProgressed` message on `StepExecution` as
  `last_progress`.

It does not spawn agents, run processes, call operations, or own storage.
Concrete dispatch remains injected by orchestration or plugins.

## Relationship To Other Domains

- `core/workflow` remains the executable DAG/action recipe model.
- `core/task` owns user-facing objective, ownership, status, and acceptance
  criteria.
- `core/project.Task` currently means discovered project runner entry from
  Taskfile/Makefile/package scripts. It should later be renamed to avoid
  confusion, but is not changed in this slice.
- `project_task_run` is a concrete operation that can later be referenced from
  task workflow/step execution.

## Next Migration Slice

Refactor `planexecplugin` internals to use `core/task` and `runtime/task`:

1. Convert incoming `plan` create/revise payloads into `task.Task`.
2. Emit core task events instead of plugin-local plan events.
3. Replace the plugin-local projector with `runtime/task.Project`.
4. Keep public `plan` operation output shape stable until callers can move to
   dedicated task operations.

Before migration, keep validating task steps as a DAG. `core/task.Task.Validate`
rejects unknown dependencies, self-dependencies, and dependency cycles so
durable task definitions cannot become permanently unrunnable.
