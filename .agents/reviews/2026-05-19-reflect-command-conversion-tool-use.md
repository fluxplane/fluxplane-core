# Tool Ergonomics Review: Reflect Command Conversion

Date: 2026-05-19
Agent: coder
Topic: Converting `reflect.yaml` to a markdown-based command

## Scope

This review covers the current coder session where I found `apps/coder/resources/.agents/commands/reflect.yaml`, converted it into a markdown command at `apps/coder/resources/.agents/commands/reflect.md`, and then responded to the generated reflect prompt by writing this review. The session involved filesystem discovery, file reads and writes, Go package testing, and git inspection.

## What worked well

- `glob` was the right first tool for the user's short request to find `reflect.yaml`. It found the exact file quickly without scanning irrelevant output.
- `file_read` gave a line-numbered view of the YAML command, which made the conversion straightforward: frontmatter description plus prompt body.
- `file_move` and `file_create` were effective for changing the resource from `.yaml` to `.md` while preserving the intended command name from the filename.
- Running `go_test` on `./adapters/agentdir` was a good targeted validation step. It checked the loader path relevant to markdown command resources without running the whole repository.
- `git_status` was useful because it exposed pre-existing unrelated workspace changes. That helped avoid claiming a clean worktree or implying all changes were mine.

## What was bad or inefficient

- I used broad `grep` searches to understand markdown command conventions, and one result exceeded the provider-facing size limit. That was unnecessary noise for this small conversion. A narrower read of `adapters/agentdir/loader.go` and existing tests was enough.
- There was a confusing tool-result replay issue after `dir_list` and `glob`, reported as `tool_result_missing` with later orphaned repaired results. I continued correctly, but the interaction made the transcript harder to follow.
- I converted the file with `file_move` and then immediately overwrote its contents with `file_create`. This worked, but it was not the cleanest sequence. A single move followed by `file_edit` or direct `file_create` at the new path plus deletion would have made intent clearer.
- `git_diff` for only the command directory showed the deleted tracked YAML file but did not show the new untracked markdown file. I should have anticipated that and used `git_status` first or included an explicit read/stat of the new file in the final summary.
- I did not update `CHANGELOG.md`, even though the repository guidance says documentation or user-visible command changes should update it unless told to skip. The user's request was small and direct, but the operative rule still applies. I should have either updated it or asked if they wanted to skip changelog work.

## What I would improve

- For future format conversions, first inspect the exact loader contract, then perform one minimal edit path, then validate with the smallest relevant test package.
- Avoid broad repository greps unless the target convention is unknown. In this case, `adapters/agentdir/loader.go` and `loader_test.go` were already enough.
- Use `git_status` before `git_diff` when new files are expected, because untracked files will not appear in a normal diff.
- Be more careful with template variables. I preserved `{{ .Argument }}` from YAML, but the tests I saw used `{{.Query}}` for markdown commands. I should have confirmed the runtime command argument template data instead of only preserving the existing YAML content.
- Apply the repository's changelog rule consistently for resource-command changes, or explicitly mention when I am not doing it because the user requested a narrow conversion.

## Honest self-critique

I got the practical change done quickly, but I was a bit too mechanical. I preserved the prompt content and validated the markdown decoder, yet I did not fully verify whether the markdown command invocation context supports `.Argument` the same way the YAML command did. The grep results suggested `.Query` appears in markdown command tests, while `.Argument` had no clear local usage. That should have made me pause and inspect the command rendering path more deeply.

I also tolerated noisy tool behavior instead of tightening my approach. The oversized grep and orphaned tool-result repair did not break the task, but they indicate I was using exploration tools more broadly than necessary. For a small file conversion, the workflow should have been calmer and more deterministic.

## Bottom line

The tool set was sufficient and mostly ergonomic for this change: `glob`, `file_read`, file operations, `go_test`, and `git_status` covered the essential loop. The weak points were my own validation depth around the template variable and a couple of inefficient discovery steps. The next improvement is to treat command resource conversions as loader-contract work, not just text-format reshaping.
