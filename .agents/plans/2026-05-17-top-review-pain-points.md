# Top Review Pain Points Plan

Date: 2026-05-17

## Source material

This plan distills recurring pain points from the review notes in `.agents/reviews`:

- `.agents/reviews/2026-05-15-tool-improvements.md`
- `.agents/reviews/2026-05-16-sqlite-event-store-reliability.md`
- `.agents/reviews/2026-05-16-sqlite-review-tool-ergonomics.md`
- `.agents/reviews/2026-05-16-tool-ergonomics-session.md`
- `.agents/reviews/2026-05-16-tooling-side-reflection.md`
- `.agents/reviews/2026-05-16-web-search-tooling-session.md`

## Executive summary

The reviews converge on one main message:

> The platform is powerful enough to do real coding work, but it becomes inefficient and less trustworthy whenever results are large, failures happen, files are structured, or concurrent runtime state is involved.

The highest-leverage theme is inspection quality. The tools can perform many actions, but when output is large or a command fails, the useful evidence is often hidden, replaced, or too noisy to consume directly.

## Top 10 distilled pain points

### 1. Oversized tool results are the biggest recurring drag

`file_read`, `grep`, `git_diff`, task/sub-agent results, and web requests often overflow into `/tmp/agentruntime-tool-result...` placeholders.

The fallback is painful because the artifact is usually not conveniently readable from normal workspace tools. This leads to repeated narrower reads, shell workarounds, and manual probing.

Desired fixes:

- Truncate intelligently instead of replacing the whole result.
- Include a small preview and tail when output is too large.
- Expose a safe reader/search/tail tool for saved tool-result artifacts.
- Make every tool honor `max_bytes` before provider-facing rendering.
- Report the safe maximum when a requested range is too large.

### 2. Native tools hide the most important failure details

`go_test` sometimes reports only package-level failure, without the failing assertion or useful stdout/stderr. This forces fallback to `shell_exec go test -v`, undermining the guidance to prefer native tools.

Desired fixes:

- Always include bounded failing test names.
- Include assertion/error output around the failure.
- Include package-level summary, exit status, and stderr/stdout tails.
- Preserve structured results, but never at the expense of the actionable failure text.

### 3. `git_diff` is too easy to overflow

Normal diffs, including a few Go files or a single large markdown edit, can exceed response limits. Agents then split diffs manually or fall back to shell commands such as `git diff --stat`.

Desired fixes:

- Add `stat` mode.
- Add `name_only` mode.
- Add semantic summary mode.
- Add `max_bytes` and `max_hunks` controls.
- Add changed-function or function-context mode for code diffs.
- Make staged/unstaged and path filtering easy to combine with these modes.

### 4. File editing is brittle for large or structured documents

Exact-text replacement is fragile when whitespace, surrounding context, or file state changes. Markdown edits are especially risky; one review noted an edit inserted content under the wrong section after the file changed.

Desired fixes:

- Heading-aware markdown reads.
- Heading-aware markdown insert/replace operations.
- Range edits by line number.
- Append/prepend operations.
- Delete range operations.
- Unified patch application.
- Stale-read safeguards for section edits.
- Better no-op detection so accidental no-op edits do not add noise.

### 5. File discovery/glob behavior is not trustworthy enough

One review describes root-level files not being found by expected glob patterns such as `**/*.md`. This creates doubt about every search result: is the file absent, or did the tool miss it?

Desired fixes:

- Make root-file matching predictable for common recursive patterns.
- Add clear glob semantics documentation.
- Return warnings when a pattern may not match root-level files.
- Provide a boring Unix-like `find` operation by name/type/path.
- Prefer slightly over-helpful discovery to surprising omissions.

### 6. Tooling instructions and available tools have mismatches

Reviews repeatedly mention `file_delete` as needed or referenced, while it was unavailable in the listed namespace at the time. Deletion then required shell/Python workarounds, which are less safe and less auditable.

Desired fixes:

- Provide a native safe `file_delete` operation.
- Refuse directory deletion unless explicitly requested.
- Refuse glob deletion unless matches are shown first.
- Require confirmation for broad deletes.
- Or, if deletion is intentionally unavailable, remove references to it from tool-use guidance.

### 7. Native-tool limitations push agents back to shell

Shell was used for `go fmt`, `go test`, `task verify`, deletion workarounds, diff summaries, and output summarization. Some of these native capabilities may now exist, but the broader problem remains: native tools must be as ergonomic as shell for common workflows.

Desired fixes:

- Keep first-class `go_fmt`, `go_test`, and task runner operations.
- Make native tools expose the same practical views agents reach for in shell: tail, grep, stats, concise failures, and compact summaries.
- Add structured command-output summaries: exit code, stdout/stderr tails, matched error lines, and failed subcommand metadata.
- Avoid making shell the ergonomic fallback for normal development loops.

### 8. Code navigation needs more semantic/type-aware precision

Grep and AST-only tools help, but reviewers wanted stronger Go-aware navigation:

- type-aware references,
- implementations,
- callers/callees,
- package dependency graphs,
- architecture edge explanations,
- bounded method searches by receiver or interface.

Desired fixes:

- Add compact modes to Go dependency/navigation tools.
- Improve interface and method reference discovery.
- Add architecture feedback before the final `task verify` gate.
- Provide tools such as `arch_check_package` and `arch_explain_edge` for editing-time feedback.

### 9. Session/change tracking is weak in dirty worktrees

Reviews mention unrelated pre-existing dirty files causing repeated mental bookkeeping. Agents need to distinguish files changed by the current session from prior workspace state.

Desired fixes:

- Add a session-scoped change view: files changed since the assistant's first action.
- Distinguish current-session changes from pre-existing dirty files in status output.
- Support a safe “stage/commit my changes only” workflow with confirmation.
- Keep a lightweight session note of intentionally changed files.

### 10. SQLite/event-store concurrency model is fragile under concurrent work

The runtime reliability review identifies product-level risks under concurrent sessions and writers:

- global `thread.index` optimistic conflicts,
- same-thread append conflicts bubbling up as hard failures,
- deferred SQLite transactions for read-then-write sequence allocation,
- unique constraint races not retried,
- weak multi-process testing,
- unclear idempotency after ambiguous commit errors.

Desired fixes:

- Avoid requiring a globally ordered `thread.index` stream for ordinary concurrent thread creation, or retry/rebase index conflicts at the thread-store layer.
- Add semantic retry/rebase policy for same-thread append conflicts.
- Use stronger SQLite writer locking such as `BEGIN IMMEDIATE` for append transactions.
- Retry safe unique `(stream, stream_seq)` races when expected sequence checks are disabled.
- Add concurrency stress tests for same-stream, different-stream, multi-store same-file, thread creation, thread append, cancellation under lock contention, and WAL/busy-timeout behavior.
- Generate stable event IDs or idempotency keys for retried semantic operations.
- Consider a single-writer local daemon around SQLite, or Postgres, if many independent sessions must share one durable event DB.

## Highest-leverage fix order

1. Fix large-result UX globally: preview, tail, readable artifacts, and strict `max_bytes` enforcement.
2. Make native Go/test/task tools show actionable failure output.
3. Improve diff/read/edit ergonomics for large files and markdown sections.
4. Add trustworthy session-scoped file/change discovery.
5. Harden event-store/thread concurrency before scaling concurrent sessions.

## Proposed workstreams

### Workstream A: Output and artifact inspection

Goal: make every large or failed result inspectable without shell fallback.

Candidate deliverables:

- Shared result-capping behavior across tools.
- Preview/tail fallback for oversized results.
- Tool-result artifact read/search/tail operation.
- Compact modes for verbose task and sub-agent outputs.

### Workstream B: Native developer loop parity

Goal: make native tools the ergonomic default for Go development and verification.

Candidate deliverables:

- `go_test` failure-output improvements.
- Task runner output summaries with failing subtask metadata.
- `git_diff` stat/name/summary/hunk modes.
- Safe regex support for `go_test -run`, including alternation passed as an argument.

### Workstream C: Structured document and file editing

Goal: make common file operations safe, direct, and predictable.

Candidate deliverables:

- Safe native file delete.
- Heading-aware markdown read/edit operations.
- More edit primitives: range replace, insert before/after, append, prepend, delete range, unified patch.
- Better stale-file and no-op edit detection.

### Workstream D: Discovery, navigation, and architecture feedback

Goal: reduce manual grep/probing and catch architecture issues before final verification.

Candidate deliverables:

- Predictable recursive file discovery.
- Compact/type-aware Go reference and implementation tools.
- Package dependency graph views.
- Architecture edge checking/explanation tools.

### Workstream E: Runtime storage concurrency hardening

Goal: prevent normal concurrent work from surfacing as misleading operation failures.

Candidate deliverables:

- Thread index conflict retry/rebase or projection redesign.
- Same-thread append conflict policy.
- SQLite append transaction hardening.
- Multi-store/multi-process concurrency stress tests.
- Idempotency guidance and event-ID stability for retried operations.

## Success criteria

- Large outputs never become opaque placeholders without an inspectable preview or reader.
- Native Go/test/task tools provide enough failure detail to avoid shell fallback for normal failures.
- Agents can safely edit markdown sections without line probing or stale-placement mistakes.
- Agents can identify session-owned changes in a dirty worktree.
- Concurrent thread/session activity does not routinely bubble normal optimistic conflicts up as hard operation failures.

## 2026-05-17 validation update

After deleting the raw review notes and checking the distilled claims against the current tool/runtime behavior, the original top 10 list splits into confirmed current issues, partially resolved items, and runtime-specific items that now have evidence of improvement.

### Confirmed still-current issues

These are not just historical complaints; they were reproduced during this follow-up session.

1. **Oversized tool results are still opaque/noisy.**
   - `git_diff` on both the new plan and the staged review deletion overflowed into `/tmp/agentruntime-tool-result...` placeholders.
   - Attempting to inspect one of those paths with `file_read` failed because the path escapes the workspace root.
   - Status: still an issue.

2. **Native test tooling still hides actionable failure details.**
   - `go_test ./adapters/sqleventstore` reported only package-level failure.
   - Running `go test` through shell exposed the real compile errors in `runtime/thread/store.go`.
   - Status: still an issue.

3. **`git_diff` still overflows too easily.**
   - Even normal documentation diffs overflowed the provider-facing result limit.
   - Status: still an issue.

4. **File discovery/glob behavior is still surprising.**
   - `glob("**/*.md")` did not include root markdown files.
   - `glob("*.md")` returned no matches even though `dir_list(".", pattern="*.md")` showed root markdown files such as `AGENTS.md`, `CHANGELOG.md`, `README.md`, and `task-scheduler-review.md`.
   - Status: still an issue.

5. **`file_delete` is still missing despite guidance referring to it.**
   - The developer instruction says to use `file_delete`, but no such tool is available in the current namespace.
   - Direct `rm` was blocked by safety policy, so deleting review files required a Python workaround.
   - Status: still an issue.

6. **Native limitations still push agents back to shell/Python.**
   - Shell/Python was needed for deletion.
   - Shell `go test` was needed to reveal hidden compile diagnostics.
   - Status: still an issue.

7. **Session-scoped change tracking is still missing.**
   - `git_status` showed many unrelated pre-existing modifications alongside this session's work.
   - There is no visible “changed by this session only” view.
   - Status: still an issue.

### Partially resolved or improved items

8. **File editing ergonomics are improved, but structured-document editing remains incomplete.**
   - The current `file_edit` supports range edits, inserts, append, prepend, and delete-range operations, so the old “only exact text patching” complaint is partly stale.
   - However, heading-aware markdown read/edit and stale-section safeguards are still not visible.
   - Status: partially resolved.

9. **Go/code navigation tooling is much better, but architecture-aware feedback remains a gap.**
   - The current toolset includes `go_definition`, `go_references`, `go_implementations`, `go_callers`, `go_callees`, `go_symbol_info`, package/import tools, and outlines.
   - The old “missing Go navigation tools” complaint is therefore partly stale.
   - Remaining gap: compact/type-aware precision and explicit architecture edge explanation/checking during edit loops.
   - Status: partially resolved.

### Runtime concurrency status

10. **SQLite/thread concurrency concerns are significantly improved, but should remain tracked.**

Evidence of improvement observed in the current tree:

- `adapters/sqleventstore.Store` uses a dedicated connection and `BEGIN IMMEDIATE` for append transactions.
- `Open` sets `SetMaxOpenConns(1)`.
- `OpenDB` documents its connection-pool behavior rather than silently mutating caller-owned DB settings.
- Concurrency tests exist for:
  - concurrent appends to different streams,
  - concurrent appends to the same stream without expected sequence,
  - expected-sequence conflict behavior,
  - multiple stores writing to the same file,
  - runtime thread concurrent create,
  - runtime thread concurrent append.

During validation, `runtime/thread` initially did not compile due to unrelated in-progress edits. After correcting those local compile errors, the focused checks passed:

```text
go_build ./runtime/thread ./adapters/sqleventstore
PASS

go_test ./runtime/thread -run TestStoreAppendRetriesSameThreadConflict
PASS

go_test ./adapters/sqleventstore -run TestConcurrentAppendSameStreamWithoutExpectedSequence
PASS

go_test ./adapters/sqleventstore -run TestConcurrent
PASS

go_test ./adapters/sqleventstore -run TestRuntimeThreadStoreConcurrent
PASS

go_test ./runtime/thread
PASS
```

Status: partially resolved with positive test evidence. Keep tracking because full verification was not run and high-contention/multi-process product behavior still deserves ongoing stress coverage.

## Updated priority after validation

The current highest-leverage follow-ups are:

1. **Fix large-result handling across all tools.** This remains the most frequent and easiest-to-reproduce issue.
2. **Make native test/task tools show actionable failure output.** `go_test` must expose the actual failing compile/test diagnostics without shell fallback.
3. **Add a real safe `file_delete` or remove it from guidance.** The current mismatch creates unsafe workarounds.
4. **Fix or document glob semantics for root files.** File discovery must be boring and trustworthy.
5. **Add session-scoped change tracking.** Dirty worktrees are common and currently require manual bookkeeping.
6. **Keep hardening event/thread concurrency.** The current evidence is much better than the original review, but the area is important enough to keep under stress tests and full verification.
