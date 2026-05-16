# Workspace-Scoped Project Task Execution

## Status

Implemented in this slice.

This design adds first-class project task execution without making task runners
part of the Go plugin. Task discovery remains a `core/project` fact. Running a
task is a side-effecting `projectplugin` operation that enters through
`runtime/system.Process`.

## Model

`core/project.Task` now carries:

- a stable `id` in the form `{kind}:{manifest_path}:{name}`;
- the discovered `name`, `kind`, `path`, and display `command`;
- structured runner fields: `executable`, `args`, and `workdir`;
- optional description and metadata.

`TaskRunRequest` selects a task by:

1. `task_id`, preferred and unambiguous;
2. `kind` + `name`;
3. `name` only when it has exactly one match in the selected project.

`TaskRunResult` records the selected project/task, resolved process command,
stdout/stderr, exit code, timeout/truncation flags, duration, dry-run state,
and diagnostics.

## Runtime Resolution

`runtime/project.Manager.ResolveTaskRun` uses the existing workspace-scoped
project inventory path. Scoped managers reject mismatched `workspace_id`; task
selection never bypasses project inventory.

Runner mappings:

- Taskfile: `task --taskfile <basename> <task> -- <extra args...>`, with
  workdir set to the Taskfile directory.
- Makefile: `make -f <basename> <target> <extra args...>`, with workdir set to
  the Makefile directory.
- `package.json` scripts: `<package-manager> run <script> -- <extra args...>`,
  selecting `pnpm`, `yarn`, or `npm` from nearest ancestor lockfile evidence.
  This covers monorepos where nested packages share a workspace root lockfile,
  while still honoring a nearer `package-lock.json` as npm evidence before
  walking to an ancestor pnpm/yarn lockfile.

Taskfile parsing uses `gopkg.in/yaml.v3` to read task names and lightweight
`desc` / `summary` metadata. It does not emulate Taskfile evaluation.

## Operation

`projectplugin` contributes `project_task_run`.

The operation:

- exposes typed `TaskRunRequest` and `TaskRunResult` contracts;
- supports `dry_run` to resolve the command without starting a process;
- executes through `System.Process().Start` and `Wait`, never through a shell;
- forwards managed process events to the operation event sink;
- emits process usage measurements;
- declares process, filesystem, and read-external effects with unknown
  idempotency and medium risk.

The typed safety intent resolves the selected task and advertises the concrete
process target. Dry runs still advertise that resolved process target because
the operation spec declares process effects and the safety envelope requires a
command intent before dispatch, even though the handler does not start a
process.

## Coder Surface

The coder bundle includes `project_task_run` in project feature expansion and
delegation. The coder prompt now prefers project inventory/task operations for
discovered tasks before falling back to shell execution.

## Follow-Ups

- Add richer task metadata per runner, such as Taskfile aliases and Make target
  comments, when needed.
- Add task runner availability status if activation later needs to hide task
  execution when binaries are unavailable.
- Keep task execution in `projectplugin`; language plugins can consume project
  tasks but should not own generic task runners.
