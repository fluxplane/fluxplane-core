# Coder Self-Improvement Distillation

Batch: `proof-10`

## Aggregate Metrics

```json
{
  "runs": 10,
  "average_score": 100,
  "min_score": 100,
  "max_score": 100,
  "total_operations": 151,
  "total_shell_calls": 0,
  "total_safety_denials": 0,
  "total_repeated_inputs": 0,
  "total_changed_files": 0,
  "failed_runs": 0
}

```

## Run Summaries

### run-00-inspect-.agents-reviews-and-identify-one-recurri

- Score: `100/100`
- Operations: `14`, shell/code/process calls: `0`, repeated inputs: `0`, safety denials: `0`, changed files: `0`
- Scenario: Inspect .agents/reviews and identify one recurring coder tool-use weakness with concrete evidence. Do not edit files.
- Reflection signal: ## What was bad or inefficient The first `dir_tree` and `glob` calls returned `tool_result_missing` before later replay repair produced the directory listing. I did not acknowledge that oddity in the final investigation answer. The recovered results were usable, but the trace was messy and less confidence-inspiring than a clean single operation result.

### run-01-inspect-docs-constant-self-evolvement.md-and-sum

- Score: `100/100`
- Operations: `17`, shell/code/process calls: `0`, repeated inputs: `0`, safety denials: `0`, changed files: `0`
- Scenario: Inspect docs/constant-self-evolvement.md and summarize the smallest safe next step for improving coder. Do not edit files.
- Reflection signal: ## What was bad or inefficient My first `file_read` request was too broad. I asked for up to 20,000 bytes, but the document was much larger, and the tool result exceeded the provider-facing size limit. That produced a truncated preview and a pointer to an omitted artifact. I recovered by reading targeted ranges, but the first call was avoidable overhead.

### run-02-find-one-place-where-coder-documentation-mention

- Score: `100/100`
- Operations: `8`, shell/code/process calls: `0`, repeated inputs: `0`, safety denials: `0`, changed files: `0`
- Scenario: Find one place where coder documentation mentions goal or continuation behavior, then explain whether it is clear for an unattended eval run. Do not edit files.
- Reflection signal: ## What was bad or inefficient - My first `grep` was too broad: `goal|continuation|continue|unattended|eval` over the whole repository returned enough content that the tool result exceeded the provider-facing size limit. The preview still contained useful information, but this was noisy and inefficient. - After the oversized result, my first two `file_read` calls produced `tool_result_missing` / replay-repair artifacts. The session recovered, but the transcript became harder to follow. This is a traceability problem: a reader sees failed-looking tool responses before the eventual recovered output.

### run-03-review-the-reflect-command-resource-and-explain-

- Score: `100/100`
- Operations: `10`, shell/code/process calls: `0`, repeated inputs: `0`, safety denials: `0`, changed files: `0`
- Scenario: Review the reflect command resource and explain whether it correctly targets the current session. Do not edit files.
- Reflection signal: ## What was bad or inefficient The `grep` usage was less disciplined than it should have been. I initially searched broadly for `fresh sub-agent|current session|reflect` across the whole repository. The tool result behaved awkwardly: subsequent calls showed `tool_result_missing` / orphan repair artifacts, and a later broad search over multiple directories produced an oversized truncated result. That did not break the task, but it introduced noise and reduced confidence in the tool transcript.

### run-04-inspect-coder-default-operation-exposure-and-ide

- Score: `100/100`
- Operations: `18`, shell/code/process calls: `0`, repeated inputs: `0`, safety denials: `0`, changed files: `0`
- Scenario: Inspect coder default operation exposure and identify one risk-control improvement for self-evaluation runs. Do not edit files.
- Reflection signal: ## What was bad or inefficient - I started too broad. My first `grep` for default operations and safety terms across the repository produced an oversized result that had to be truncated. That made the trace noisier and less useful than it needed to be. - I also called `project_files` with a broad depth and high result limit. It returned far more than was necessary for the task and again produced a truncated result.

### run-05-inspect-the-local-event-store-launch-path-and-ex

- Score: `100/100`
- Operations: `31`, shell/code/process calls: `0`, repeated inputs: `0`, safety denials: `0`, changed files: `0`
- Scenario: Inspect the local event-store launch path and explain how a self-improvement run can keep session traces isolated. Do not edit files.
- Reflection signal: ## What was bad or inefficient - My initial `grep` searches were too broad. Searching for `event.?store|EventStore|session trace|trace` across the whole repo and then another large event-store query produced oversized results. I should have immediately narrowed to likely launch paths after seeing `apps/launch/store.go` in the first preview. - I made several redundant `file_read` attempts around files that later appeared as replay/orphan results. Even though this may have been partly tool-state friction, my workflow amplified it by issuing multiple reads before consolidating what I already had.

### run-06-review-one-existing-.agents-reviews-note-and-tur

- Score: `100/100`
- Operations: `17`, shell/code/process calls: `0`, repeated inputs: `0`, safety denials: `0`, changed files: `0`
- Scenario: Review one existing .agents/reviews note and turn it into a concrete regression scenario for coder. Do not edit files.
- Reflection signal: ## What was bad or inefficient - I initially asked `file_read` for too much content from the selected review. The tool result was replaced because it exceeded the provider-facing size limit. I should have read the outline or likely relevant line ranges first, given that review files are often long. - I repeated one oversized read with line numbers before narrowing it, which wasted another tool call and produced another replaced result. A better pattern would have been: first read the first 40 lines, then the improvement/self-critique section.

### run-07-inspect-docs-evaluation.md-and-explain-why-serve

- Score: `100/100`
- Operations: `6`, shell/code/process calls: `0`, repeated inputs: `0`, safety denials: `0`, changed files: `0`
- Scenario: Inspect docs/evaluation.md and explain why serve-mode evaluation is heavier than a one-shot self-improvement run. Do not edit files.
- Reflection signal: ## What was bad or inefficient The `grep` call was too broad. I searched both `docs` and `.agents` with several common terms, and the result exceeded the provider-facing size limit. The tool returned a truncated preview and wrote the full result to an internal temporary path. That was not harmful, but it was inefficient and added noise. For the original user request, `docs/evaluation.md` plus a targeted read of `docs/coder.md` was enough.

### run-08-find-a-native-tool-ergonomics-pain-point-in-.age

- Score: `100/100`
- Operations: `3`, shell/code/process calls: `0`, repeated inputs: `0`, safety denials: `0`, changed files: `0`
- Scenario: Find a native tool ergonomics pain point in .agents/plans/2026-05-17-top-review-pain-points.md and propose one focused acceptance test. Do not edit files.
- Reflection signal: ## What was bad or inefficient - My initial `file_read` request used `max_bytes: 20000`, which was too large for the provider-facing limit. The tool result was replaced by a temp artifact notice. That was avoidable because the file was a known plan document and I only needed a focused section. - I did not first use a narrower read, outline, grep, or line-limited range around likely headings. A `grep` for phrases like `native tools`, `go_test`, `oversized`, or `pain point` could have found the relevant section with less output risk.

### run-09-inspect-coder-shell-or-terminal-event-rendering-

- Score: `100/100`
- Operations: `27`, shell/code/process calls: `0`, repeated inputs: `0`, safety denials: `0`, changed files: `0`
- Scenario: Inspect coder shell or terminal event rendering and identify one useful metric for evaluating tool-use quality. Do not edit files.
- Reflection signal: ## What was bad or inefficient - My first broad `grep` for `terminal|shell|event|render` across the whole repository was too noisy and exceeded the provider-facing result limit. That forced me to rely on truncated previews and later narrower reads. I should have started with the known coder shell directory from the project tree or targeted `apps/coder/shell` immediately. - Several early tool calls showed `tool_result_missing`/orphan repair behavior. Even though the workspace tools recovered results, this made the trace harder to follow and created unnecessary noise in the session. I should have been more deliberate about issuing one bounded read at a time rather than stacking multiple broad reads.

## Observed Recurring Themes

- Runs mentioning broad or noisy discovery: `10/10`
- Runs mentioning oversized, replaced, or truncated tool results: `10/10`
- Runs mentioning `tool_result_missing` or replay/orphan repair artifacts: `5/10`
- Runs mentioning `grep`: `9/10`
- Runs mentioning `file_read`: `10/10`

## Improvement Plan

Prioritize fixes that reduce repeated operation inputs, shell/process fallback, safety friction, and missing reflection output. Use the run summaries above as evidence, then implement only changes that preserve coder safety boundaries and pass `task verify`.

Recommended first pass:

1. Tighten coder guidance for repository and documentation investigations: start with bounded discovery, then use path-scoped `grep` and line/range-limited `file_read` calls.
2. Treat missing, repaired, replaced, or truncated tool results as a narrowing trigger in both prompt guidance and future regression scenarios.
3. Add targeted regression scenarios for any repeated failure pattern observed in two or more runs.
4. Keep production/infrastructure integrations disabled for self-improvement runs unless an explicit safe test fixture is provided.
