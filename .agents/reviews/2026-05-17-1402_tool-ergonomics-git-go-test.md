# Tool Ergonomics Review: Git Diff and Go Test Regex Batch

Date: 2026-05-17 14:02
Agent: coder
Topic: tool ergonomics while adding compact `git_diff` modes and fixing `go_test` regex filters

## Scope

This review covers the tool experience from the batch that:

- added compact `git_diff` modes (`stat_only`, `names_only`, `max_bytes`) in the git plugin;
- fixed `go_test` `run`/`skip` validation to allow real Go regular expressions;
- live-tested through `task coder:live-test`;
- committed those two implementation slices separately.

## What worked well

### Native Go tools were fast enough for focused checks

`go_fmt` and `go_test` were useful for tight feedback once the edited files parsed. Focused package/test runs kept latency acceptable:

- `go_fmt ./plugins/gitplugin`
- `go_test ./plugins/gitplugin -run TestDiff`
- `go_test ./plugins/golangplugin -run TestGoToolchainCheckFmtInstallWithHostWorkspace`

The structured `go_test` summaries are better than raw `go test` output for pass/fail status.

### `file_read` with line ranges is reliable

Targeted `file_read` calls were the safest way to inspect surrounding code without flooding context. Reading narrow windows around `goTestArgs`, `validateGoFlagValue`, and the relevant tests worked well.

### `git_add` and `git_commit` are good for scoped commits

Staging exact paths and then checking staged diff made it possible to honor “commit only your changes” even with a very dirty worktree. `git_status` exposed the rest of the user/worktree changes clearly.

### `project_task_run` dry-run for live-test discovery is useful

The `coder:live-test` task metadata was easy to confirm with `dry_run`. This prevented guessing the task command and matched the repository instructions.

## What was bad or inefficient

### The old `git_diff` was still unusable after implementing the fix

During the same session, the tool registry continued using the old loaded `git_diff`, so calls like:

```json
{"stat_only": true}
```

still produced oversized result replacement artifacts. That was expected from a runtime-loading perspective, but ergonomically bad: I had just implemented the feature and could not use it in the current agent process.

Impact:

- I fell back to `shell_exec git diff --stat`.
- The provider-facing result replacement repeatedly hid useful small summaries.
- It made the implementation feel unverified from the tool UX perspective until a rebuild/restart.

Improvement:

- Add version/reload visibility for tools: show whether an operation is served by current source, installed binary, or long-lived host process.
- Provide a lightweight “tool registry reload required” hint after editing operation implementations.
- Make `git_diff` result replacement itself prefer stat/name fallback previews even before the operation-specific compact mode is available.

### `git_diff` result replacement is too opaque

Several `git_diff` and `go_test` outputs were replaced by temp artifact references even when the useful user-facing answer was likely small or could have been summarized. The replacement message gives a path/hash but no inline preview.

Impact:

- I had to rerun commands with shell equivalents to inspect diffs.
- The agent lost continuity: “Tool result replaced” is not actionable unless I explicitly fetch or recreate the output.

Improvement:

- Always include a bounded head/tail preview in replacement messages.
- For diffs, include automatic `--stat` and maybe changed file names in the replacement text.
- Allow a follow-up operation like `tool_result_read(path, max_bytes, tail)` or expose temp artifacts through workspace-safe reads.

### `file_edit` is powerful but easy to misuse on nearby code

I made multiple bad edits because `file_edit` line insertion landed inside function bodies when the target line was close to a closing brace or when previous edits shifted context. The operation is deterministic, but the ergonomics punish imprecise line targeting.

Impact:

- I temporarily broke `plugins/gitplugin/plugin.go` by inserting helpers inside `add()`.
- I temporarily broke `plugin_test.go` by inserting a test inside another test.
- I had to spend extra cycles repairing syntax.

Improvement:

- Add an AST-aware insert mode for Go: “insert function after function X” / “insert declaration before function Y”.
- For Go files, warn when inserting a top-level `func` inside another function body.
- Provide a dry-run parse check directly from `file_edit` when editing `.go` files.
- Better: a `go_edit` helper that operates on declarations rather than raw lines.

### `go_test` validation bug blocked the exact workflow it was meant to support

Before the fix, `go_test` rejected a normal Go test regex with `|`. That forced a shell fallback for confirming alternation:

```bash
go test ./plugins/golangplugin -run 'TestA|TestB'
```

Impact:

- Native tool could not run a common focused-test pattern.
- This made test verification less consistent and highlighted that process-argument tools should not over-apply shell metacharacter rules.

Improvement:

- Validate by semantic domain, not generic “shell unsafe” characters, whenever arguments are passed directly without a shell.
- Add regression tests around native operation input validation for common CLI syntaxes.

### Live-test output exceeded limits and was not easy to inspect

`project_task_run` for `coder:live-test` returned an oversized replaced result. I could confirm the task was invoked and dry-run the command, but I could not easily inspect the live-test transcript in the model context.

Impact:

- The live-test was not as conclusive as it should have been.
- I had to state a caveat: current agent process still used old tools, and the live-test output was not fully visible.

Improvement:

- Live-test tasks should write a concise structured summary artifact in addition to full debug output.
- `project_task_run` should support `tail_bytes`/`tail_lines` for long-running command output.
- For `coder:live-test`, default output should prioritize final assistant answer, tool calls, and errors, not raw debug volume.

### Dirty worktree made “only my changes” risky

The repository had many unrelated modifications. `git_status` showed them, but there is no native helper to stage/commit only files changed by this agent/session.

Impact:

- I had to manually reason about which paths were mine.
- `CHANGELOG.md` had mixed changes; I correctly avoided committing it, but the process was manual.

Improvement:

- Track tool-authored file writes by session and expose `session_changed_paths`.
- Add `git_stage_session_changes` or a dry-run helper that lists files modified by the current agent only.
- For `CHANGELOG.md`, support conflict-aware append sections or session-scoped hunks so a user-visible note can be staged without unrelated changes.

## Tool improvements I would prioritize

1. **Provider-safe diff previews everywhere**
   - Result replacement should include diffstat and changed file names.
   - `git_diff` compact modes are useful, but generic replacement still needs previews.

2. **AST-aware Go edit operations**
   - Insert/replace declarations by symbol.
   - Refuse or warn on syntactically invalid top-level insertion.
   - Parse check after edit.

3. **Long-output tail/preview controls**
   - For `go_test`, `project_task_run`, and live-test output.
   - Let callers request summary, head, tail, or failure-only output.

4. **Tool runtime freshness visibility**
   - Make it obvious when edited operation code is not active in the current tool host.
   - Provide a rebuild/restart hint for operation plugin changes.

5. **Session-scoped change tracking**
   - Especially for dirty worktrees.
   - Would make “commit only your changes” much safer.

6. **Semantic validators for direct argv tools**
   - Avoid generic shell metacharacter bans when no shell is involved.
   - Validate `regexp`, `duration`, `package pattern`, `path`, `refspec`, etc. by their actual domain.

## Honest self-critique

I over-relied on raw `file_edit` line placement and caused avoidable syntax breakage twice. I should have used smaller reads around insertion points and inserted before/after stable symbols instead of approximate lines. The repair was straightforward, but it cost time and context.

I also should have anticipated that the current tool host would not pick up the just-implemented `git_diff` and `go_test` changes. For live-testing tool behavior, I should distinguish clearly between:

- source-level unit verification;
- shell-level equivalent verification;
- rebuilt-coder live verification;
- current-session tool-host behavior.

That distinction matters for confidence.

## Bottom line

The native tool suite is strong for normal inspect/edit/test/commit loops, but the rough edges are concentrated around large-output handling, source edits by line number, and stale tool-host behavior after operation changes. Fixing those would directly reduce wasted retries and make future implementation batches faster and safer.
