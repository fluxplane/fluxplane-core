# Task Management Tool-Use Review: Glob Pattern Session

- Review timestamp: `20260517-115537` UTC
- Related commits: `a7af343 feat: centralize path pattern glob matching`
- Related tasks:
  - `task_4c0a6431e1253254` — cancelled
  - `task_1a2a7f2bf88ce0a1` — cancelled

## What I did

I first discussed the design for supporting `.agents/{designs,plans,reviews}/**/*` in the filesystem glob operation and agreed that the reusable piece should be pure path-pattern matching in `core`, with filesystem enumeration remaining in `runtime/system` and adapters.

At the user's request, I created an asynchronous developer task in `ready` state. It started running automatically, but stalled after producing artifacts for the first one or two steps. I then cancelled and restarted it once, observed the restart also sitting in its initial step, cancelled all tasks at the user's request, and completed the implementation directly in the current session.

The direct implementation reused/finished the partial worker changes, added a `core/pathpattern` package, wired it into runtime and test workspace glob implementations, migrated distribution asset matching, added tests, updated the changelog, ran targeted Go tests, and committed the glob-specific files.

## Tools used

- `task_create` to create the original and restarted async implementation tasks.
- `task_get`, `task_list`, `task_list_artifacts`, and `task_get_artifact` to inspect task progress and outputs.
- `task_scheduler_status` to confirm that the scheduler still considered the stalled task running.
- `task_validate` to see why the task could not complete.
- `task_modify` to cancel stalled tasks.
- `file_read`, `grep`, `dir_tree`, and `git_diff` to inspect existing and partial implementation state.
- `go_fmt` and `go_test` for targeted verification.
- `git_add`, `git_status`, and `git_commit` to commit only the glob-related changes.
- `code_execute` to generate the review timestamp without using shell.
- `file_create` to write this review.

## What went well

- The initial task definition captured the desired design and acceptance criteria clearly enough for a worker to make real progress.
- Task artifacts were useful after the stall: `core_pathpattern_patch` and `runtime_glob_patch` indicated what had already been implemented.
- `task_list_artifacts` and `task_get_artifact` helped recover partial progress instead of starting over blindly.
- Cancelling the tasks with `task_modify` worked, and `task_list` confirmed both were cancelled.
- Once I stopped relying on the stalled async path, the direct coding loop was straightforward.
- Explicit path staging avoided committing unrelated worktree changes.

## What went wrong

### The task workflow stalled but still looked active

The first task remained `running` while no downstream artifacts appeared for a long period. The scheduler reported it as active, but task progress was effectively stuck. The restart repeated a similar pattern: the task became `running`, but remained in its first inspection step.

From the agent/user perspective, this creates an awkward state: the task is neither failed nor progressing, and there is no clear diagnosis.

### Step status visibility was too coarse

`task_get` showed step states, but not enough execution detail to answer:

- Is the worker currently doing work?
- Is it blocked on a tool call?
- Did it crash without marking the step failed?
- Is it waiting for scheduler capacity?
- Has it produced logs that explain why it stopped?

The artifacts revealed completed work only after the fact. They did not explain why later steps did not start or finish.

### Polling was noisy and inefficient

I manually polled with `task_get`, `task_list`, `task_list_artifacts`, and even attempted a bad `browser_wait` call before falling back to `sleep`. That was clumsy. A task system should offer a bounded wait/watch primitive instead of forcing manual polling loops.

### Large task payloads exceeded result limits

Several `task_get` calls exceeded provider-facing output limits once the task accumulated descriptions, steps, and artifacts. This made the most obvious inspection command less useful exactly when the task became complex.

The compact `task_list` status remained useful, but it could not show step-level progress.

### Restarting duplicated orchestration complexity

The restarted task needed explicit instructions to inspect previous artifacts and avoid duplicate work. That is reasonable, but manual. A first-class restart/resume operation could have carried forward completed step artifacts and reset only incomplete steps.

## What I should have done differently

- I should have used the async task only if I intended to let it finish independently, not as the primary implementation path for a small bounded code change.
- After the first long stall, I should have switched to direct implementation sooner instead of creating a restarted task with nearly the same execution risk.
- I should have checked `task_list_artifacts` earlier, because it exposed useful partial implementation state even while the task remained `running`.
- I should not have attempted `browser_wait` as a generic sleep mechanism; using a bounded process sleep was clearer, though still not ideal compared with a native `task_wait`.
- I should have summarized to the user earlier that the scheduler thought the task was running but lacked evidence of live progress.

## Suggested task-tool improvements

### 1. Add `task_wait`

A blocking wait operation would simplify this workflow:

```text
task_wait(id, until=status_changed|step_completed|terminal, timeout=60s)
```

It should return compact deltas: task status, changed steps, new artifacts, and whether the wait timed out.

### 2. Add execution heartbeat / last activity

`task_get` or `task_scheduler_status` should expose:

- current execution id,
- current step id,
- last state transition time,
- last tool call time,
- last artifact time,
- worker heartbeat age.

Then a stalled task can be distinguished from a long-running task.

### 3. Add compact task views

The existing `summary` view still overflowed once artifacts were attached. Useful projections would be:

- `view=steps`
- `view=artifacts`
- `view=diagnostics`
- `view=timeline`

Each should be aggressively bounded and omit the full original task description unless requested.

### 4. Add restart/resume semantics

Instead of manually cancelling and creating a new task, support:

```text
task_restart(id, from_failed_or_running_step=true)
task_resume(id, reset_steps=[...])
```

The operation should preserve completed step artifacts, clear stale running states, and continue from the first incomplete dependency-ready step.

### 5. Surface worker logs separately

A compact `task_execution_logs` or `task_get_execution` operation would help diagnose stalls without overloading `task_get`. It should show recent worker events, tool calls, errors, and scheduler decisions.

### 6. Make already-running behavior idempotent

When scheduling/running a task that is already running, return the active execution and current step as success-like information, not merely a status conflict. That would make orchestration loops easier.

## Bottom line

The task system was useful for capturing intent and partial progress, but it was not reliable enough as the execution path for this small implementation. The main gap was not task creation; it was task lifecycle observability. Once a task was `running` but not visibly progressing, I had no clean way to wait, diagnose, resume, or restart it. For now, I should use async tasks for delegation only when the user explicitly wants that mode, and switch to direct implementation quickly when progress becomes ambiguous.
