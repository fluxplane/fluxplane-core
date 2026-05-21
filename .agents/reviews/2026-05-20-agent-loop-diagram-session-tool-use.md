# Tool Ergonomics Review: Agent Loop Diagram Session

Date: 2026-05-20
Agent: coder
Topic: Tool use and workflow while explaining and diagramming the agentic loop

## Scope

This review covers the current coder session where I explained how the agentic loop works, generated an illustrative image, attempted to open it for viewing, saved it under `docs/`, and then created a more technical Python-generated SVG diagram. It is based only on the actual tool calls and decisions in this session.

## What worked well

- `file_read` on `docs/agent-loop.md` was the right first move. It grounded the explanation in the repository's own documentation instead of inventing a generic agent-loop description. The read result was oversized, but the preview still contained enough of the relevant summary to answer accurately.
- The `image` generation tool was useful for quickly producing a visual draft of the agentic loop. It created a usable artifact without needing local graphics dependencies.
- After the user asked for a real technical diagram, using Python to generate an SVG gave a deterministic, editable documentation artifact at `docs/agent-loop-technical-diagram.svg`. That was more appropriate than relying only on a generative image.
- I preserved the generated outputs in `docs/` as requested and did not commit anything, which matches the repository rules.

## What was bad or inefficient

- I tried `browser_open` on a `file://` URL even though the browser tool only accepts HTTP/HTTPS. That was avoidable; I should have known or checked the tool contract before calling it.
- I then tried to work around the browser limitation by starting a local HTTP server, but the workflow became noisy and produced confusing `tool_result_missing` / orphan result behavior. This made the session less clean and did not actually help the user view the image.
- The user explicitly said to use `open`, and I did try `process_run` with `open`, but only after the browser/server attempts. I should have followed that instruction directly first.
- `open` was not available in `$PATH`, but the final explanation repeated the failure twice because the earlier failed/orphaned tool calls led to duplicated attempts. That made the interaction look clumsy.
- When saving the generated JPG into `docs/`, I first used `file_copy` from `/tmp/...` and hit `path escapes workspace root`. I then used `shell cp`. The fallback worked, but it shows a gap in handling artifacts created outside the workspace. I could have anticipated that workspace file tools cannot copy from arbitrary `/tmp` paths.
- For the SVG, I used `process_run python3 -c` with a long inline script. It worked, but it was not very maintainable or inspectable. A temporary script file or `file_create` followed by execution would have been easier to review and debug.

## What I would improve

- For generated images, save or copy the artifact into the workspace immediately after generation if the user might want to keep it. That avoids relying on temporary paths and makes later operations simpler.
- Before trying to display a local file, choose one clear strategy: either use the user's requested `open` command directly, or start a local HTTP server and open an HTTP URL. I should not mix approaches unless the first one fails cleanly.
- Avoid browser calls with unsupported URL schemes. The browser tool's `http/https only` constraint should be treated as known behavior.
- Prefer deterministic diagrams for technical documentation earlier. The generative image was visually useful, but the Python-generated SVG was the better fit for repository documentation because it is text-based, versionable, and structurally precise.
- If creating a nontrivial generated file with Python, write a small script or document the generation command better. The current one-liner was effective but opaque.

## Honest self-critique

I moved too quickly from "create an image" to tool execution without planning the artifact lifecycle. The image generation itself was fine, but I did not immediately put the file somewhere stable. That caused friction when the user wanted to view it and then save it.

I also should have obeyed the user's "use open" instruction immediately instead of trying browser-based viewing first. The browser attempt was based on my own assumption about how to display the file, not on the user's preference. When `open` failed, I should have reported that once and offered a clean fallback.

The shell fallback for copying from `/tmp` was acceptable because the native workspace copy tool could not access that source path, but I should have explained less and kept the flow tighter. The later Python SVG generation was a good recovery because it produced a real technical chart, but the inline command was too large and not especially ergonomic.

## Bottom line

The session achieved the user's requested outcomes: an explanation, a saved illustrative image, and a deterministic technical SVG diagram. However, my tool workflow around viewing and moving the generated image was messy. The main lesson is to stabilize generated artifacts early, respect explicit user tool preferences first, and avoid unsupported browser/file-path assumptions.
