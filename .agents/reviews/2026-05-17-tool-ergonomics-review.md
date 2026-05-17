# Tool Ergonomics Review: Go Test Diagnostics Session

Date: 2026-05-17

## Summary

This session exposed a sharp split: the tool surface is powerful enough to do
real implementation, verification, live product testing, and commits without
leaving the agent boundary, but the ergonomics still create avoidable drag. The
biggest sources of friction were output-size blowups, inconsistent task discovery
state, weak live-test affordances before we added one, and native tool safety
validation that blocks otherwise normal regex/test workflows.

The good news: once the workflow was encoded as `task coder:live-test`, the loop
became much faster. The bad news: it took too many exploratory steps and too much
manual output filtering to get there.

## What worked well

### Native Go tools are excellent for focused checks

`go_fmt`, `go_test`, and `go_build` were the right tools for most verification:

- `go_fmt` gave package-scoped formatting without shell ceremony.
- `go_test` gave compact package summaries for targeted tests.
- `go_build -trimpath ./...` gave a clean repository-wide compile check.

For coding work, this is much better than reflexively running opaque shell
commands. The model can keep the verification intent explicit and bounded.

### File edit operations are good when patches are small

`file_edit` with exact-text patches and range replacements was productive for
surgical edits. It made it easy to add the terminal renderer hook, tests, and
design status without hand-applying diffs.

The atomic original-file resolution is useful because it prevents accidental
sequential-drift edits from silently applying to the wrong text.

### Live testing became good after adding a project task

Before `coder:live-test`, live testing was ambiguous and slow. After adding:

```bash
task coder:live-test -- "prompt"
```

it became a crisp product-level smoke test. It encoded the known-good provider,
model, yolo/debug flags, and prompt input path. That is exactly the kind of
project-specific affordance agents need.

### Debug output was valuable for product/UI bugs

The debug stream made it possible to distinguish:

- what the model saw,
- what the operation returned,
- what terminal UI rendered,
- what final assistant text summarized.

That was essential for finding the mismatch between raw `test_run_event` data and
human-facing terminal output.

### Git staging by path worked well

Because the worktree had unrelated modifications, explicit path staging was
critical. `git_add` with a curated file list and `git_commit` avoided pulling in
pre-existing task-domain changes.

## What was bad / frustrating

### Output-size blowups were constant friction

Many reads/diffs/greps returned:

```text
Tool result replaced because it exceeded the provider-facing size limit.
```

This happened on:

- `file_read` for medium-sized files,
- `grep` results,
- `git_diff`,
- `go_test` output,
- live-test captures.

The fallback path became manual shell filtering into `/tmp`, `grep`, `tail`, and
Python scripts. That is not ideal: the agent loses structured data and spends
extra turns reconstructing the useful slice.

Roast: the tools are like a microscope that frequently says “object too large,
please squint through a keyhole instead.”

### `grep` is too eager to explode

The native `grep` tool often exceeded output limits even with modest match caps
and context. In practice, shell `grep -nE ... | head` was more predictable.

That is backwards. Native grep should be the safer, more ergonomic choice.

Needed: a truly compact grep mode that returns only file:line snippets, no huge
JSON envelope, no oversized context, and with hard byte budgeting per match.

### `go_test` run regex validation is too restrictive

The native `go_test` rejected normal Go test regexes containing `|`:

```text
run "TestA|TestB" contains unsupported character '|'
```

That forced less precise patterns like `Test.*TestRunEvent` or shell fallback.
For Go, alternation is standard and useful. The safety check is overzealous.

Roast: a Go test tool that distrusts Go test regex syntax is wearing a bike
helmet inside a parked car.

### Project task discovery had stale/odd behavior

After adding `coder:live-test`, `project_task_run` initially said the task was
not found. Then `project_tasks(refresh=true)` found it, and then the run worked.

That means task discovery caching is visible to the user/agent in a bad way.
Agents should not need to know when task inventory has stale state.

Needed: `project_task_run` should either refresh automatically on not-found, or
return a hint like “task not found in cached inventory; retry with refresh or run
project_tasks(refresh=true).”

### Shell was still too necessary for bounded inspection

Despite guidance to prefer native operations, shell remained the practical tool
for:

- checking exact live-test UI lines,
- stripping ANSI,
- grepping huge debug logs,
- getting compact git/test output after native result truncation.

This is not because shell is semantically better; it is because shell gives
composable output shaping. The native tools need more first-class filtering and
projection options.

### Debug live-test output is too noisy for UI verification

`--debug` is useful, but it floods the same raw `go_test: PASS` text in many JSON
locations. To verify UI, I had to filter around the actual terminal completion
line.

Needed: a mode or flag that separates:

- human terminal rendering,
- structured debug payloads,
- final model output.

For example:

```bash
task coder:live-test --ui-only -- "prompt"
task coder:live-test --debug-json -- "prompt"
```

or coder flags that write debug JSON to a separate file while leaving stdout/stderr
human-readable.

### File edit failure/replay weirdness was confusing

One `file_edit` returned a missing result / replay repair situation. The durable
context recovered it, but from the agent perspective it looked like a ghost edit.

Roast: nothing says confidence like “your edit both failed and is now in the
system context.”

The repair mechanism is good, but the user-facing/tool-facing story should be
clearer: either the edit happened or it did not; if replay repaired it, say so
plainly and include final file status.

### The model can misread structured data even when tools return it

The first live test had `test_run_event` in operation data, but the model under
test summarized that it was not present. This is not a tool bug exactly, but it
highlights why UI rendering should not rely on the final model summary.

Good product lesson: terminal UI should render important operation facts from
structured data directly, not hope the model notices them.

## What I would improve

### 1. Add compact projections to native tools

For every high-output tool, add projection knobs:

- `grep`: `format=lines`, `max_bytes_per_match`, `no_context`, `head_per_file`.
- `git_diff`: `stat_only`, `names_only`, `hunks_matching`, `max_bytes_per_file`.
- `file_read`: `grep_lines` or better bounded section extraction by regex.
- `go_test`: `summary_only`, `failures_only`, `raw_artifact_ref`.

The current all-or-truncated behavior wastes turns.

### 2. Make live-test a first-class project concept

The new `coder:live-test` task helped immediately. I would go further:

- standardize `task coder:live-test -- "prompt"`,
- write debug logs to `.cache/live-test/<timestamp>.jsonl`,
- print a compact UI transcript to terminal,
- optionally expose `task coder:live-test:ui` for rendering-only checks.

This would prevent future agents from rediscovering provider/model/auth details.

### 3. Let `project_task_run` refresh on miss

If a task is not found:

1. refresh task inventory once,
2. retry lookup,
3. only then fail.

This avoids a whole class of stale-cache confusion.

### 4. Make Go regex validation match Go reality

Allow safe Go test regex syntax, especially `|`, grouping, anchors, dots, and
slashes. If safety requires restrictions, document them and expose an escape hatch
for trusted repository-local tests.

### 5. Preserve structured artifacts separately from model-facing output

The session repeatedly needed both compact text and structured data. The operation
result pattern is good, but the terminal/debug tooling should make it easier to
inspect one without drowning in the other.

A nice shape:

- terminal line: concise human UX,
- model text: compact summary,
- artifact ref: full JSON / logs,
- debug: separate file or channel.

### 6. Add a native ANSI-stripping / transcript slicing helper

UI work often needs “show me the human-visible terminal lines matching X, with
ANSI stripped.” Right now that required shell + Python. A native helper could
consume a live-test artifact or process output and return exact rendered lines.

### 7. Better staged-change review summaries

`git_diff` over realistic changes exceeded output limits. A native staged diff
summary that says:

- files changed,
- additions/deletions,
- function names touched,
- tests added,
- suspicious large hunks,

would make pre-commit review much faster.

## Session-specific lessons

### Encode known workflows early

The biggest efficiency win was turning “live-test coder” from tribal knowledge
into `AGENTS.md` + `Taskfile.yaml`. Agents are much faster when project-specific
recipes are executable, not implicit.

### UI verification must inspect the UI, not the model answer

The model under test can summarize incorrectly. For UI/UX bugs, inspect the
actual terminal rendering line. In this session, that caught both:

- the absence of emoji rendering before wiring,
- the confusing `❌ tests failed` on a passing truncated run.

### Treat parse warnings differently from test failures

The final bug was a good example of diagnostic severity leaking into UX status.
A successful test run with a parse-tail warning from truncated JSON should not be
rendered as failed. Tool output status, test-run status, and parser health are
related but distinct concepts.

## Bottom line

The tool ecosystem is powerful and close to excellent for agentic coding. The
main weakness is not capability; it is friction around bounded inspection and
workflow discovery. Give agents compact projections, better task refresh behavior,
real Go regex support, and first-class live-test transcripts, and this kind of
session would be noticeably faster and less noisy.
