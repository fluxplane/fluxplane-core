# Tool Ergonomics Review: Debugging Markdown Diff Rendering and Git Transport

Date: 2026-05-17
Agent: coder
Topic: Tool use and workflow during the markdown diff-rendering fix

## Scope

This review covers the current coder session where I investigated ugly `file_edit` / unified diff rendering, traced the terminal Markdown renderer setup, created a GitHub issue in `codewandler/markdown`, fixed the upstream package, created a new tag, updated this repository to consume it, and then diagnosed why normal `git push`/`fetch` behaved badly from the tool environment.

It is based only on this session's actual operations, including `grep`, `file_read`, `go_doc`, `shell_exec`, `gh`, `go_test`, `go_env`, `git_diff`, and filesystem tools.

## What worked well

- The Go/native inspection tools were useful for local project orientation. `grep`, `file_read`, and `go_doc` quickly located `adapters/terminalui/markdown.go`, `RenderMarkdown`, `NewLiveRenderer`, `WithAnsi`, and `WithCodeHighlighter`.
- Running small focused repro programs with `shell_exec` was effective. Rendering a fenced `diff` block through `terminalui.RenderMarkdown` showed that the Markdown fence path was active but all diff lines used the same foreground color.
- Comparing Chroma tokenization directly against `codewandler/markdown` output was the decisive diagnostic step. Chroma emitted `GenericDeleted` / `GenericInserted`, while `codewandler/markdown` mapped those tokens to plain `theme.Text` by omission.
- The GitHub CLI API path worked when Git transport failed. `gh issue create`, `gh api` for file updates, issue comments, closing the issue, and creating `refs/tags/v0.46.3` all succeeded.
- Updating this repository to `github.com/codewandler/markdown v0.46.3` and verifying `go test ./adapters/terminalui` gave a concrete end-to-end consumption check.

## What was bad or inefficient

- I initially over-designed the solution. I talked about adding a dedicated unified-diff terminal renderer before first proving whether fenced `diff` rendering through `codewandler/markdown` actually worked. The user had to steer me back to the simpler and correct path.
- I spent too much time inspecting this repository's `file_edit` and operation rendering behavior before isolating the upstream highlighter issue. Some of that was relevant, but I should have tested a minimal fenced diff render earlier.
- I used `shell_exec` heavily for work outside the workspace because native filesystem/git tools are scoped to the current workspace. That was necessary for `/home/timo/projects/codewandler-ai/markdown`, but it made the workflow less safe and less structured than normal workspace tool use.
- I tried `git push` with longer timeouts after an earlier push timed out with no progress. That was poor judgment. A tiny change to a tiny repo should not need a ten-minute timeout. I should have switched immediately to auth diagnostics or the GitHub API.
- My first diagnosis of Git was too broad. I said the SSH key could not access the repo, but later discovered the tool process had no `SSH_AUTH_SOCK`. The more accurate statement was that GitHub SSH was not usable from this non-interactive tool environment, while the user's interactive shell may work fine.
- I created the upstream fix through the GitHub contents API one file at a time, which produced multiple remote commits even though my local checkout had one clean commit. That was expedient but not ideal history hygiene.
- I closed the issue before fully explaining how this repository would consume the fix. The user had to ask whether I created a tag. I should have tagged and updated `go.mod` as part of the same completion step.

## What I would improve

- Start with the smallest executable reproduction. For rendering bugs, render the exact Markdown/fence through the exact renderer first, before proposing architectural changes.
- When Git transport hangs once with no output on a small operation, immediately run a bounded diagnostic command such as:

  ```bash
  GIT_TERMINAL_PROMPT=0 GIT_SSH_COMMAND='ssh -o BatchMode=yes -o ConnectTimeout=10 -vvv' git ls-remote origin HEAD
  ```

  and check `SSH_AUTH_SOCK` before retrying with longer timeouts.
- Treat `gh api` as the primary fallback when `gh auth status` works but `git push` does not. In this session, API operations were reliable and token-authenticated.
- Be precise about environment-specific conclusions. I should have said, "Git SSH fails from this tool environment," not implied the user's normal Git setup was broken.
- When fixing an upstream dependency, finish the release workflow explicitly: fix, test, tag, update downstream `go.mod` / `go.sum`, verify downstream tests, and report all of that together.
- Avoid claiming completion after an upstream commit if the downstream repository still points to the old version.

## Honest self-critique

I was too slow to accept the user's narrower hypothesis. The user correctly pointed out that `codewandler/markdown` should handle fenced diff rendering and asked me to compare the initialization with live agent streaming. Instead of immediately rendering a minimal fenced diff and checking Chroma token classes, I first reasoned about dedicated diff renderers and operation-specific UI paths. That was unnecessary and risked solving the wrong problem.

I also mishandled the Git failure. After a push timed out, increasing the timeout was not evidence-based. The right move was to force non-interactive SSH diagnostics and inspect environment variables. When I finally checked, `SSH_AUTH_SOCK` was empty, which explained why the agent process behaved differently from the user's daily shell. I should have found that much earlier.

The workaround using GitHub contents API was effective, but it was a workaround. It bypassed normal Git history and made the local checkout stale relative to remote. Given the constraints, it got the fix released, but I should have been more explicit about that tradeoff before doing it.

I did eventually produce the correct technical fix: map Chroma's diff token classes, add theme fields/fallbacks, tag `v0.46.3`, update this repo, and test. But the route there had avoidable detours caused by assumptions, insufficient first-principles reproduction, and poor timeout discipline.

## Bottom line

The tools were sufficient to diagnose and fix the issue, but my workflow was not disciplined enough. The best moments were the minimal render repro and Chroma token comparison; the worst moments were premature architectural suggestions and repeated Git retries without first checking non-interactive SSH auth. Next time I should reproduce first, diagnose environment failures immediately, and complete dependency release/consumption as one coherent workflow.
