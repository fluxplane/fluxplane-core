# Task System Reliability Roadmap

## Status

This is the canonical progress file for the task system. Historical design
rationale remains in:

- [Core Task Domain](../designs/2026-05-16-core-task-domain.md)
- [Task Plugin, Task Commands, and Review Backlog](../designs/2026-05-16-task-review-commands.md)
- [Workspace-Scoped Project Task Execution](../designs/2026-05-16-project-task-execution.md)

Do not add new task-system progress sections to those historical designs. Move
completed or newly discovered task work through this roadmap instead.

## Current State

Implemented:

- `core/task` owns task, step, execution, artifact, scheduler-control, and
  review-request contracts.
- `runtime/task` projects task event streams, computes ready DAG steps,
  validates completion readiness, and wraps `event.Store` as the task store.
- `plugins/taskplugin` contributes `/task`, `/plan`, task creator/planner
  sessions, typed task operations, scheduler controls, `review_request`, and
  artifact readers.
- `orchestration/taskexecutor` claims ready tasks, runs whole-task or step-DAG
  executions, handles human blockers, resumes interrupted executions, finalizes
  missing task-level outputs, and records scheduler diagnostics.
- Scheduler execution claims record worker id, lease id, and lease expiry on
  `core/task.Execution`. Long-running worker calls renew the same lease through
  `task.execution_lease_renewed` events. The local reconciliation loop
  interrupts or requeues expired running executions from workers that are no
  longer locally active and records a durable scheduler diagnostic.
- Local launch starts the scheduler when `taskplugin` is selected and uses
  indexed ready-task events as the primary reactive trigger. Periodic index
  scans remain only as reconciliation.
- Worker dispatch uses a `WorkerClient` boundary. The current concrete worker
  is `ChannelWorker`, which opens profiled sessions through the channel client.
- Scheduler worker pools support role-specific profile lists, profile
  rotation, fallback attempts, and per-role capacity reporting.
- `task_scheduler_status` includes durable queued ready tasks and durable
  running execution leases projected from the task store in addition to local
  in-memory running tasks.
- Scheduler workers register durable capacity snapshots in the task event
  store. `task_scheduler_status` merges those registrations with the local
  scheduler snapshot and marks expired worker registrations inactive. Internal
  task index and worker-registration streams use names that cannot collide with
  user task stream IDs.
- Reconciliation can recover a running execution before its long execution
  lease expires when the assigned worker's durable registration has expired.
- Terminal UI renders task creation, revision, status, execution, step,
  artifact, completion, failure, cancellation, and scheduler diagnostic events.
  Task snapshots now carry task-level phase/detail so finalization, deferred
  scheduling, disabled scheduling, and blockers are visible in the task card
  rather than only as separate log lines.
- Session-origin task metadata lets background scheduler events mirror back to
  the originating session thread for live feedback and replay.
- `task_run` records the calling session as the latest execution watcher, and
  scheduler runtime events are mirrored to both the task origin and that
  watcher. This lets `/plan` approval turns receive completion feedback even
  when the draft was created by an earlier command/session. Watcher metadata is
  recorded only after the task stream is known to exist, and publish failures
  for one destination do not suppress delivery to remaining destinations.
- `task_run` returns explicit started/running/waiting/background fields so
  planner and task agents do not have to infer scheduler acceptance from prose.
- One-shot terminal runs wait for scheduler-run tasks from the submitted turn
  before closing the local runtime. The wait is bounded and reports any
  still-running task IDs after the deadline instead of leaving the terminal
  without feedback indefinitely. REPL turns watch briefly, then return the
  prompt while background tasks continue.
- One-shot terminal turns render the agent's final response before following
  scheduler task events. This preserves chronological feedback when the model
  reports "running" and the scheduler completes the task moments later.
- Live one-shot smoke verification succeeded with
  `task coder:live-test -- "Create a ready task titled 'scheduler smoke live test' ..."`:
  the main coder agent created `task_b67d880b5d87e114`, `task_run` observed it
  running in the background, the scheduler produced required `smoke-report`,
  and the one-shot process exited after `task.execution_completed`.
- Live `/task` command smoke verification succeeded with
  `go run ./cmd/coder --provider codex --model gpt-5.5 --yolo --input "/task \"create a draft task titled slash task smoke, no execution\""`:
  the command delegated to the task session and returned
  `task_145bcb3c39e916bc` as a draft with expected output.
- Live `/plan` draft command smoke verification succeeded with
  `go run ./cmd/coder --provider codex --model gpt-5.5 --yolo --input "/plan \"create a tiny demo task for planner smoke, do not approve yet\""`:
  the command delegated to `task-planner`, created draft
  `task_1ae45a5493022179`, explicitly said it was not scheduled yet, presented
  steps/outputs/assumptions, and asked for approval or refinement.
- Live approval follow-up against `task_1ae45a5493022179` verified the
  structured `task_run` flags (`started=false`, `running=true`,
  `waiting_for_capacity=false`, `background=true`) and printed the one-shot
  wait message. The worker completion did not finish in a reasonable interval,
  so the process was stopped. This finding is superseded by the bounded wait
  and watcher verification below.
- Live approval follow-up against `task_171b2452f1f4f6e0` showed the task
  completed durably about 12 seconds after execution started, but the approving
  terminal session timed out because scheduler feedback was still published
  only to the task's original `/plan` thread. The watcher metadata path above
  fixes this; the fresh watcher verification below confirms the fix.
- Fresh live approval follow-up against `task_762f39032391dffe` verified the
  watcher path: the task was drafted by `/plan`, approved in a later one-shot
  turn, `task_run` reported `running=true`/`background=true`, and the approving
  terminal received worker artifacts plus `task completed` without timing out.
  The run also exposed stale model text ordering, which is now addressed by
  rendering the final response before follow-up scheduler events.
- Fresh live approval follow-up against `task_1db2b350a20e2379` verified the
  ordered feedback path after the rendering change: `/plan` created a draft,
  the approval turn set it ready, `task_run` reported it already running in the
  background, worker artifacts and `task completed` rendered in the approving
  session, and the agent's observed final status was `completed`.
- Live aggregate-output finalizer smoke verification succeeded with
  `task coder:live-test -- "Create a ready task titled scheduler aggregate finalizer smoke ..."`:
  the main coder agent created `task_67ab07f20d3c47cf`, ran three worker
  steps, the scheduler emitted `task_finalizing_outputs`, produced required
  task-level `aggregate-summary`, and emitted `task.execution_completed`
  without manual repair.
- The old plan execution plugin has been removed from runtime assembly; `/plan`
  now creates draft tasks and marks them ready after approval.
- The legacy `orchestration/subagent` supervisor has been removed. Command
  helper sessions and workflow agent steps now run through
  `orchestration/sessionagent`, while scheduled task execution remains on
  `orchestration/taskexecutor`.

## Deferred Follow-ups

1. Durable worker queue and leases
   - Current scheduling records worker id, lease id, and lease expiry on task
     executions, renews active leases while worker calls are running, and local
     reconciliation interrupts or requeues expired leases according to a
     bounded max-attempt policy. A restarted local scheduler can recover an
     expired running lease or an execution assigned to an expired worker
     registration from the task stream, requeue it, and complete the retry.
   - Deferred: stronger cross-process claim ownership for a future external
     worker protocol where workers renew registrations independently of local
     scheduler processes.

2. Distributed worker capacity
   - Current worker pools describe capacity inside one scheduler process.
   - Scheduler status reports durable queued ready tasks, durable running
     execution leases, and durable worker registrations.
   - Deferred: cross-process capacity accounting and external queue support for
     daemon or remote workers. Worker registrations are observable state, not a
     distributed external-worker allocator.

3. Task execution UX
   - Current terminal output renders task events and now carries explicit
     task-level phase/detail for finalizing, queued, waiting, and blocked
     states.
   - Main-agent immediate task creation plus explicit `task_run` has live
     one-shot smoke coverage.
   - `/task` draft creation has live command smoke coverage.
   - `/plan` draft creation and presentation has live command smoke coverage.
   - `/plan` approval follow-up has live one-shot smoke coverage: ready
     transition, scheduler acceptance, worker execution, artifact feedback, and
     completion were visible in the approving terminal session.

4. `/plan` approval clarity
   - Current planner behavior is prompt-driven: draft, present, refine, mark
     ready after approval, then call `task_run`.
   - `task_run` now returns structured scheduler outcome flags.
   - Live follow-up verified ready transition and scheduler outcome flags.
   - The approval turn now records itself as a watcher so scheduler events are
     mirrored to the approving session, not only the original drafting session.
   - Fresh live verification confirmed the approval turn receives completion
     feedback through the watcher path.

5. Session-agent migration
   - Task execution uses `ChannelWorker` through the scheduler `WorkerClient`
     boundary.
   - Target-session command dispatch for `/task` and `/plan` now uses
     `orchestration/sessionagent` and emits `session_agent.*` events.
   - Workflow agent steps now use `orchestration/sessionagent` and emit
     `session_agent.*` events.
   - Terminal, Slack, event-registry, runtime persistence, and session-history
     classification use `session_agent.*`; legacy `subagent.*` registration
     and rendering have been removed.
   - `orchestration/subagent` has been deleted. There is no remaining
     sub-agent runtime domain for new work.

6. Long-running reliability tests
   - Current tests cover targeted contention, stale worker results, capacity
     deferral, event-triggered bursts, restart recovery for expired leases and
     expired worker registrations, long-running worker cancellation, lease
     expiry/requeue behavior, finalizer output binding, and concurrent
     scheduler instances contending for the same ready task stream.
   - Durable-store coverage now includes two scheduler instances using separate
     SQLite event-store connections against the same database file and
     verifying that only one execution claim wins.
   - Deferred: longer soak coverage with external worker processes. That
     belongs with the external/distributed worker allocator follow-up, because
     there is no external worker protocol yet.

## Next Slices

### Slice 1: Task UX Clarity

Goal: make local task execution feedback unambiguous before changing deeper
scheduler mechanics.

- Render a stable task execution summary for active tasks in terminal output.
  Done for terminal task snapshots, which include phase/detail and step state.
- Make finalizer state visible as "finalizing outputs" rather than a generic
  scheduler diagnostic. Done for terminal rendering.
- Make blocked state include the concrete missing outputs or human blockers.
  Done for scheduler completion-block reasons and terminal blocked snapshots.
- Make `/plan` approval-to-ready flow print task ID, ready status, scheduler
  acceptance, and background-follow mode. Done for planner instructions,
  `task_run` structured scheduler outcome flags, bounded one-shot follow
  feedback, and live approval-to-completion smoke coverage.
- Live-test `/task`, `/plan`, explicit `task_run`, and a multi-step task with a
  required aggregate output. Done for main-agent immediate task creation plus
  explicit `task_run`, `/task` draft creation, `/plan` draft/presentation,
  `/plan` approval/completion, and multi-step aggregate-output finalizer smoke
  coverage.

Done when:

- A user can tell whether a task is draft, queued, running, finalizing,
  completed, failed, cancelled, or blocked from the terminal output alone.
- The three-step backend demo completes through scheduler finalization without
  a manual repair step and without ambiguous "is it done?" output. Done for
  the targeted aggregate-output finalizer smoke; keep the original code
  backend demo as a release-candidate regression scenario.

### Slice 2: Durable Scheduler Queue

Goal: make task execution survive scheduler restarts and support more than one
worker process later.

- Add durable scheduler claim/lease events or records while keeping task state
  event-sourced. Done for execution claim lease fields.
- Record worker identity and lease deadlines. Done for local scheduler claims.
- Reconcile expired leases into retry, interrupted, or failed execution state.
  Done for local max-attempt retry/requeue or interruption.
- Add heartbeat/lease renewal for long-running workers. Done for the local
  scheduler worker-call boundary.
- Make `task_scheduler_status` report durable queued/running/leased work, not
  only local in-memory running tasks. Done for queued ready tasks and running
  execution leases projected from the task store.
- Add durable worker registration snapshots so scheduler status can show
  active and expired workers across processes. Done for event-sourced worker
  registration and local status merge.
- Use expired worker registrations to recover in-flight executions before the
  longer execution lease expires. Done for bounded retry/requeue and
  active-worker preservation.
- Keep event-triggered scheduling as the fast path and index scans as
  reconciliation.

Done when:

- Killing and restarting the local runtime does not leave ready or running
  tasks permanently invisible. Done for expired running leases with remaining
  retry attempts.
- A stale worker result from an expired lease cannot complete a superseded
  execution.
- Two scheduler instances using separate SQLite event-store connections cannot
  both claim and execute the same ready task.

### Slice 3: Worker Backend Consolidation

Goal: make helper-agent execution explicit session-agent plumbing and keep
scheduled task execution on scheduler workers.

- Audit direct `orchestration/subagent` usage in user-facing flows. Done:
  package deleted; command and workflow helper sessions use
  `orchestration/sessionagent`.
- Route new multi-worker execution through task steps and `WorkerClient`. Done
  for task scheduler/executor.
- Delete the old supervisor after product flows stop using it. Done:
  `orchestration/subagent` removed after command target sessions and workflow
  agent steps moved to `orchestration/sessionagent`.
- Keep role/profile routing in scheduler config, not in a separate helper-agent
  domain. Done for task worker pools and scheduler status.

Done when:

- New user-visible planning/execution paths create or run tasks. Done for
  `/plan` draft/approval and task scheduler execution.
- There are no remaining direct sub-agent APIs. Done: helper sessions use
  `sessionagent`, and scheduled work uses task scheduler workers.

### Slice 4: Review Semantics

Goal: move review from "task with review artifacts" toward first-class review
state only if needed.

- Keep `review_request` producing reviewer-assigned tasks for now. Done:
  `review_request` creates reviewer-assigned tasks linked through
  `review_subject_task_id`, with required `review-report` artifacts.
- Add richer review projection only after real review workflows need structured
  findings, verdicts, or approval/blocking semantics. Decision: postpone
  `core/review`; review tasks plus required review artifacts are enough for the
  next task-system slice.
- Avoid adding `core/review` until artifact metadata is insufficient. Done for
  this roadmap; revisit only after a real review workflow needs verdict state
  that cannot be represented as task artifacts and status.

Done when:

- Review tasks can block or approve follow-up execution without ambiguous
  artifact-only conventions, or this roadmap explicitly decides artifact-based
  review is enough for the next release. Done: artifact-based review is enough
  for the next release.

### Slice 5: Full Task-Based Agent Integration

Goal: finish the migration from user-facing sub-agent concepts to task,
execution, worker, and command session-agent concepts.

- Add a neutral command session-agent execution boundary for `/task`, `/plan`,
  and future command agents. It should open configured sessions through the
  channel client, preserve parent thread/run/call metadata, and emit neutral
  session-agent runtime events. Done for the synchronous command helper path.
- Replace `Session.executeTargetSessionCommand` usage of
  `orchestration/subagent` with that command session-agent boundary. Done for
  TargetSession commands.
- Keep scheduler worker execution on `taskexecutor.WorkerClient` and
  `ChannelWorker`; do not make `orchestration/subagent` a worker backend. Done.
- Update terminal, Slack, event registry, and session-history classification
  for neutral command session-agent events. Done for `session_agent.*` event
  registration, terminal rendering, Slack observation, runtime persistence, and
  session-history classification.
- Migrate session-workflow agent steps to task/workflow execution plumbing or
  isolate them behind a dated deletion note if that migration is too large for
  the same slice. Done: workflow agent steps use `sessionagent`.
- Update docs so "sub-agent" is no longer the recommended domain concept for
  new planning, delegation, review, or scheduled execution work. Done.

Done when:

- `/task` and `/plan` work without `orchestration/subagent.Supervisor`. Done
  at the session command-dispatch layer.
- Main-agent immediate task creation, `/plan` approval, and scheduled task
  worker execution all use task/session-agent events and preserve live watcher
  feedback.
- No new user-visible command flow emits `subagent.*` events. Done.
- The remaining `orchestration/subagent` package is either deleted or has one
  documented owner and deletion condition. Done: package deleted.

## Verification Matrix

- `go test ./core/task ./runtime/task ./orchestration/taskexecutor ./plugins/taskplugin ./adapters/terminalui`
- `task verify` before commits that touch task execution, scheduler, terminal,
  or taskplugin behavior.
- Live one-shot scenario:
  `task coder:live-test -- "Create a ready task titled 'scheduler smoke live test' ..."`
  verified main-agent task creation, explicit `task_run`, required output
  production, and one-shot completion.
- Live `/task` scenario:
  `go run ./cmd/coder --provider codex --model gpt-5.5 --yolo --input "/task \"create a draft task titled slash task smoke, no execution\""`
  verified task command delegation and draft creation.
- Live `/plan` scenarios:
  `go run ./cmd/coder --provider codex --model gpt-5.5 --yolo --input "/plan ... do not approve yet"`
  plus later approval turns verified draft presentation, ready transition,
  `task_run`, watcher feedback, worker artifacts, and completion in the
  approving terminal.
- Live aggregate-output scenario:
  `task coder:live-test -- "Create a ready task titled scheduler aggregate finalizer smoke ..."`
  verified three worker steps, finalizer diagnostics, required task-level
  output synthesis, and completion without manual repair.
- Restart and lease scenarios are covered by
  `orchestration/taskexecutor` tests for expired leases, expired worker
  registrations, active worker preservation, stale worker results, and
  requeue/exhaustion behavior.
- Saturation and durable contention scenarios are covered by
  `orchestration/taskexecutor` burst tests and the SQLite-backed
  `apps/launch` multi-scheduler claim test.

## Progress Rules

- Update this file in the same commit as task-system behavior changes.
- Move completed work into `Current State`.
- Keep active work in `Next Slices` with a clear "Done when" condition.
- Add live-test or reviewer findings under `Deferred Follow-ups` before fixing
  them, unless the finding is fixed in the same commit.
- Do not use the historical design files as active progress trackers.
