# Tool Ergonomics Review: Reflection Workflow Tool Results

Date: 2026-05-17 14:30
Agent: coder
Topic: writing a self-reflection review for the current reflector session

## Scope

This review covers this short reflector session itself, not a prior coding task. The workflow was limited to inspecting `.agents/reviews`, sampling recent review style, and writing one new markdown review file. The only tools used were `dir_list`, `file_read`, and `file_create`.

## What worked well

`dir_list` was the right first tool. It confirmed that `.agents/reviews` already existed and showed enough existing filenames to infer the local naming style. The directory listing was compact and avoided unnecessary repository exploration.

The review-writing task did not need shell access, git writes, code execution, or broader project inspection. Staying inside the file tools matched the reflector instructions and kept the session low-risk.

`file_create` was straightforward for the final write. The `overwrite: false` flag was useful because the instructions explicitly require creating exactly one new file and not modifying existing reviews. It gave a simple guard against accidentally replacing an existing review.

## What was bad or inefficient

The `file_read` sampling step behaved poorly. I attempted to read recent review files with a small `max_bytes`, but the tool results came back as `tool_result_missing` / orphan replacement artifacts from replay rather than the expected bounded file contents. The later replacement previews did include enough content to understand the repository's review style, but the sequence was confusing and noisier than the task warranted.

The oversized-result replacement was especially odd because I asked for only 5000 bytes. The replacement message said the full JSON result exceeded the provider-facing limit, and the preview included a large embedded review. For a style-sampling task, the useful output should have been a deterministic small slice, not a temp-artifact wrapper with replay repair metadata.

This forced me to spend attention interpreting tool plumbing instead of the review content. In a longer session that kind of result ambiguity could lead to accidental duplicate reads or unnecessary fallback behavior.

## What I would improve

`file_read` should honor `max_bytes` in a way that guarantees the provider-facing result remains below the display threshold, including JSON wrapper overhead. If the requested bounded read can still exceed output limits, the tool should return a smaller inline preview rather than an artifact reference.

Replay repair messages should be separated from normal tool output or summarized tersely. A reflection agent trying to sample file style does not need to see `orphan_tool_result` internals unless there is an actionable failure.

For this specific reflector workflow, a small helper like `review_sample_recent(count, max_bytes_each)` would be faster and less error-prone than combining `dir_list` plus manual `file_read` calls. It could select recent markdown files, return headings and a few section names, and avoid repeatedly pulling whole review bodies.

## Honest self-critique

I should have read only one prior review after the first result became noisy. The instructions ask for a small sample, not exhaustive style matching, and the directory listing plus one preview was already enough to mirror the local style. Continuing to inspect another file added little value.

I also should have used narrower line ranges instead of `max_bytes` alone. A call like lines 1-40 would likely have been more predictable for matching markdown structure and less likely to trip result-size behavior.

## Bottom line

The reflector task was completed safely with exactly one new file and no repository modifications beyond the requested review. The main ergonomic issue was that bounded `file_read` sampling produced confusing replay/artifact output, which made a simple style check feel more complicated than it should have been.