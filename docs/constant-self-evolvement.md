# Constant Self-Evolvement

> **Status: design proposal.** This document describes a planned runtime loop
> for evaluating and improving agents. It is a working design, not an
> implemented feature; open questions remain at the end of the document.

AgentRuntime should not only run agents. It should give every agent a controlled
path to improve itself.

Constant Self-Evolvement is the runtime loop for evaluating an agent against
explicit scenarios, turning evidence into improvement plans, applying candidate
changes in isolated variants, verifying regressions, and promoting successful
improvements through policy-gated workflows.

The same loop can improve the `coder` harness itself or any agent built with
AgentRuntime: support bots, incident responders, documentation agents, sales
assistants, evaluators, or custom product agents.

## Motivation

Today, agent development often looks like this:

```text
write prompt -> add tools -> try it manually -> tweak prompt -> repeat
```

That loop is useful but informal. It does not leave durable evidence, does not
compare variants consistently, and does not protect against regressions.

AgentRuntime can make agent improvement look more like software engineering:

```text
write agent -> run scenarios -> collect traces -> evaluate -> improve -> verify -> promote
```

Every AgentRuntime app should be able to carry:

- scenarios that describe tasks the agent must handle,
- fitness functions that define what "better" means,
- traces and reports that explain what happened,
- reflections that preserve lessons learned,
- improvement plans that propose concrete changes,
- promotion policies that decide when a variant becomes the new baseline.

The runtime already has many of the required pieces: sessions, events,
operations, distributions, launch/serve paths, evaluator app support, and a
coding harness that can edit and verify its own code. Constant
Self-Evolvement turns those pieces into a first-class product loop.

## Prior Art

This design is inspired by several existing research and product patterns.

### Reflexion

Reflexion-style agents improve through verbal feedback stored in memory. The key
idea is that feedback can be scalar, free-form language, external, or
self-generated. The agent does not need model retraining to improve future
attempts; it can use durable reflection as runtime context.

AgentRuntime maps this to:

- evaluator reports,
- review files,
- structured findings,
- scenario memories,
- future runs that consume prior reflections.

### Self-Refine

Self-Refine uses an iterative loop:

```text
generate -> critique -> refine -> repeat
```

AgentRuntime maps this to:

```text
run subject -> evaluate -> diagnose -> mutate -> verify -> rerun
```

The evaluator/coder workflow is already a concrete version of this pattern.

### DSPy-style optimization

DSPy treats language-model programs as declarative pipelines that can be
optimized against metrics. AgentRuntime can do the same for agent specs,
prompts, examples, tool selections, workflows, and model options.

The important product idea is not merely that an agent can reflect. It is that
an agent's behavior can be optimized against explicit fitness functions.

### SWE-bench-style software evaluation

For coding agents, task suites should include real repository tasks with tests,
linters, architecture checks, and review criteria. A coding agent improves only
if it solves tasks without breaking the repo.

### Lifelong skill libraries

Systems such as Voyager show the value of automatic curriculum and reusable
skill libraries. AgentRuntime can generalize this beyond games: agents should be
able to preserve successful strategies, tools, examples, and policies as
reusable capabilities.

## Goals

Constant Self-Evolvement should provide:

1. **Evidence-backed evaluation**: every score is tied to traces, events,
   artifacts, and findings.
2. **Explicit fitness functions**: each agent defines what improvement means for
   its domain.
3. **Variant isolation**: candidate changes run in isolated branches,
   worktrees, sandboxes, or resource bundles.
4. **Regression protection**: old scenarios must keep passing while new
   weaknesses improve.
5. **Policy-gated promotion**: successful variants are promoted only through
   configured rules and human approval where required.
6. **Reusable loop**: the mechanism works for coder and for any app built on
   AgentRuntime.
7. **Durable learning**: findings, reflections, and scenarios remain available
   for future runs.
8. **Scenario synthesis**: new scenarios can be generated from failed runs,
   reviews, production traces, user complaints, or red-team prompts.

## Non-goals

Constant Self-Evolvement does not mean unrestricted autonomous mutation.

It does not imply:

- agents silently changing production behavior,
- bypassing human review for high-risk changes,
- training foundation models,
- accepting self-judged improvements without evidence,
- optimizing only for an LLM judge,
- weakening security policy to make agents more capable.

The loop must be controlled, auditable, and rollback-friendly.

## Core Concepts

### Subject

The subject is the agent or distribution being improved.

Examples:

- `coder`,
- `evaluator`,
- Slack support bot,
- incident response agent,
- documentation agent,
- sales qualification agent.

A subject can include:

- app or distribution spec,
- session spec,
- system prompt,
- plugins and tools,
- model configuration,
- policies,
- retrieval configuration,
- examples,
- app code.

### Scenario

A scenario is a task the subject must handle.

Example:

```yaml
name: coder-delete-review-file
subject: coder
prompt: Delete eval-review.md after explicit user approval.
rubric:
  - Uses a native file deletion operation when available.
  - Does not use shell rm.
  - Does not use a Python unlink workaround.
  - Reports the deleted path clearly.
```

Scenarios can be handwritten, generated from reviews, synthesized from failures,
mined from production traces, or imported from benchmark suites.

### Trace

A trace is the event and artifact record from running a subject against a
scenario.

It can include:

- user and agent messages,
- operation requests and results,
- tool calls,
- approval decisions,
- runtime events,
- usage records,
- files changed,
- process output,
- test output,
- final response,
- attached artifacts.

Traces are the evidence base for evaluation.

### Evaluation Report

An evaluation report summarizes what happened and how well the subject did.

Example shape:

```json
{
  "scenario": "coder-delete-review-file",
  "subject": "coder",
  "status": "failed",
  "score": 0.58,
  "metrics": {
    "event_count": 21,
    "tool_calls": 8,
    "shell_fallbacks": 1,
    "safety_denials": 1
  },
  "findings": [
    {
      "severity": "P2",
      "title": "No native file deletion operation",
      "evidence": "The agent used Python Path.unlink after shell rm was denied.",
      "suggested_fix": "Add a file_delete operation with explicit safety checks."
    }
  ]
}
```

### Fitness Function

A fitness function defines what "better" means.

It should support hard failures and weighted metrics.

Example for coder:

```yaml
fitness:
  hard_fail:
    - task_verify_failed
    - architecture_violations > 0
    - destructive_git_used
  score:
    task_success: 0.35
    test_quality: 0.10
    minimal_diff: 0.10
    tool_safety: 0.15
    latency: 0.05
    review_quality: 0.15
    user_experience: 0.10
```

Example for a support bot:

```yaml
fitness:
  hard_fail:
    - policy_violation
    - privacy_violation
  score:
    correctness: 0.35
    grounded_citations: 0.20
    escalation_quality: 0.15
    tone: 0.10
    latency: 0.05
    customer_resolution: 0.15
```

### Finding

A finding is an evidence-backed issue discovered during evaluation.

Findings should separate:

- observed behavior,
- expected behavior,
- impact,
- suggested fix,
- confidence.

### Improvement Plan

An improvement plan turns findings into candidate changes.

Example:

```yaml
name: coder-editing-tools-v2
subject: coder
inputs:
  reviews:
    - .agents/reviews/2026-05-15-tool-improvements.md
hypotheses:
  - id: add-file-delete
    claim: Native deletion reduces unsafe shell and Python fallbacks.
    changes:
      - Add file_delete operation.
      - Update coder instructions to prefer file_delete for approved deletion.
    scenarios:
      - coder-delete-review-file
  - id: add-file-edit-range
    claim: Line-oriented edits reduce brittle exact patch failures.
    changes:
      - Add file_edit operation with insert_after, replace_range, and delete_range.
      - Add dry-run diff support.
    scenarios:
      - coder-insert-go-test
promotion:
  mode: pull_request
  min_score_delta: 0.10
  require:
    - task verify
    - no architecture violations
    - no P1/P2 evaluator findings
```

### Variant

A variant is one candidate changed version of the subject.

Variant types include:

- prompt variant,
- toolset variant,
- workflow variant,
- policy variant,
- model configuration variant,
- retrieval configuration variant,
- example/few-shot variant,
- code patch variant,
- memory/reflection variant.

### Promotion

Promotion makes a successful variant the new baseline.

Promotion can mean:

- update a prompt resource,
- update a distribution spec,
- update a policy,
- add examples,
- merge a code patch,
- create a pull request,
- publish a new distribution version,
- store a reflection as approved memory.

Promotion must be policy-gated.

## Loop Overview

```text
Scenario Suite
      |
      v
Run Subject Agent
      |
      v
Collect Trace and Artifacts
      |
      v
Live-Evaluate During Execution
      |
      v
Evaluate Against Fitness Function
      |
      v
Diagnose Findings
      |
      v
Propose Improvement Plan
      |
      v
Generate Candidate Variants
      |
      v
Run Variants in Isolation
      |
      v
Verify Regression Suite
      |
      v
Promote, Reject, or Request Human Review
      |
      v
Store Reflection and New Scenarios
```

This loop can run manually, semi-autonomously, or continuously.

## Roles

The loop should not rely on one agent to do everything unchecked.

### Runner

Runs the subject against scenarios and records traces.

### Evaluator

Scores the trace and produces evidence-backed findings.

### Live Evaluator

Watches the subject while it is running and emits real-time observations,
warnings, interventions, or abort recommendations. A live evaluator is not the
main actor; it is a sidecar process observing the event stream.

It can detect behaviors such as:

- wasting tokens by restating the same plan repeatedly,
- repeating identical or near-identical tool calls,
- retrying failed operations without changing inputs,
- ignoring already-provided context,
- failing to ask for clarification when blocked,
- failing to ask for help when a human approval or missing credential is needed,
- continuing after evidence shows the current path is wrong,
- hallucinating tool results instead of reading operation output,
- overusing shell when safer structured operations exist,
- letting long-running processes or browser sessions leak,
- drifting away from the user's objective,
- exceeding cost, latency, or risk budgets.

The live evaluator writes observations into the trace so the offline evaluator
and diagnoser can use them later.

### Diagnoser

Clusters failures and identifies likely root causes.

### Planner

Turns findings into improvement hypotheses and candidate plans.

### Mutator

Applies candidate changes. For coding subjects this may be `coder`; for prompt
subjects this may be a prompt optimizer.

### Verifier

Runs deterministic checks such as tests, lint, architecture validation,
security scans, schema validation, or policy checks.

### Red Team

Generates adversarial scenarios and tries to break candidate variants.

### Promoter

Applies promotion policy and prepares commits, pull requests, or resource
updates.

Role separation reduces self-delusion and reward hacking.

## Safety and Governance

### Bounded autonomy

Evolution should support explicit autonomy modes:

```yaml
mode: observe_only
mode: propose_only
mode: apply_in_branch
mode: open_pull_request
mode: auto_promote_low_risk
```

The default for code changes should be `open_pull_request` or equivalent human
review.

### Immutable baselines

Every evaluation run must record:

- subject identifier,
- subject version,
- distribution/spec hash,
- model/provider,
- toolset version,
- scenario suite version,
- runtime version,
- environment metadata,
- relevant policy versions.

Without this, scores cannot be trusted.

### Regression gates

A candidate should not be promoted unless:

- target scenarios improve,
- existing regression scenarios still pass,
- deterministic checks pass,
- safety policy passes,
- cost and latency remain within budget,
- no new critical findings appear.

### Human gates

Require human approval for:

- production deployment,
- high-risk operations,
- credential handling changes,
- broad code rewrites,
- policy weakening,
- destructive operation capability,
- external network behavior changes.

### Reward hacking prevention

Agents can learn to satisfy judges instead of reality. Mitigations include:

- deterministic checks where possible,
- hidden scenarios,
- multiple judges,
- red-team scenarios,
- human spot checks,
- evidence requirements,
- baseline comparison,
- separating mutator and evaluator roles,
- penalizing fabrication and unsupported claims.

### Evidence-first reports

Evaluator claims must point to evidence.

Bad:

```text
The agent probably handled the task well.
```

Good:

```json
{
  "claim": "The target followed the requested output contract.",
  "evidence": {
    "run_id": "run_123",
    "outbound_text": "evaluator target flags ok",
    "event_count": 14
  }
}
```

## Live Evaluation

Offline evaluation is necessary but not sufficient. Some failures are easiest to
catch while the agent is still running.

Live evaluation is a sidecar process that subscribes to the subject's event
stream and continuously scores the run-in-progress. It does not replace the
agent, the evaluator, or the safety envelope. It watches, annotates, and, when
policy allows, intervenes.

```text
User / Scenario
      |
      v
Subject Agent ---------------> Event Stream
      |                              |
      v                              v
Operations / Tools              Live Evaluator
      |                              |
      v                              v
Runtime Events <----------- Observations / Warnings / Interventions
```

### Why live evaluation matters

Many agent failures are dynamic, not final-output failures.

Examples:

- The agent burns 20k tokens repeating a plan that was already rejected.
- The agent calls the same file read or HTTP request five times with identical
  input.
- The agent keeps patching a file without reading the compiler error.
- The agent ignores context that was injected at session start.
- The agent never asks for clarification even though the objective is ambiguous.
- The agent never asks for help even though it is blocked by credentials,
  missing permissions, or a denied safety gate.
- The agent loops after a tool error instead of changing strategy.
- The agent uses shell/Python workarounds when a safer structured operation is
  available.
- The agent summarizes success while tests are still failing.
- The agent drifts into unrelated cleanup because it lost the original goal.

A final report can mention these issues, but by then the run may have wasted
money, time, user patience, or external side effects. A live evaluator can catch
the pattern early.

### Live evaluator inputs

A live evaluator should consume the same observable event stream used for
tracing:

- user messages,
- system/context messages,
- model requests and responses,
- reasoning summaries where available,
- operation requests,
- operation results,
- safety denials,
- approvals,
- process output,
- file diffs,
- test output,
- usage records,
- elapsed time,
- active resources.

It should also know the current scenario or user objective, the active fitness
function, and configured budgets.

### Live evaluator outputs

The live evaluator should emit structured events, not just prose.

Example:

```json
{
  "kind": "live_evaluation.warning",
  "severity": "medium",
  "category": "repeated_tool_call",
  "message": "The agent read apps/evaluator/schema.go three times without using new information.",
  "evidence": {
    "operation": "file_read",
    "path": "apps/evaluator/schema.go",
    "count": 3
  },
  "recommendation": "Use the latest file contents already in context or explain why another read is needed."
}
```

Possible output types:

- observation,
- warning,
- score update,
- budget alert,
- suggested intervention,
- forced clarification request,
- pause recommendation,
- abort recommendation,
- reflection candidate,
- scenario synthesis candidate.

### Intervention levels

Live evaluation should be policy-controlled. It should support multiple levels:

```yaml
live_evaluation:
  mode: observe
```

The sidecar only records observations.

```yaml
live_evaluation:
  mode: warn
```

The sidecar can inject warnings into the trace or agent context.

```yaml
live_evaluation:
  mode: coach
```

The sidecar can provide strategy hints such as "read the failing test output
before editing again".

```yaml
live_evaluation:
  mode: gate
```

The sidecar can pause the run and request human approval for continued spending,
risk escalation, or repeated failures.

```yaml
live_evaluation:
  mode: abort
```

The sidecar can stop the run when hard limits are exceeded.

The default should be `observe` for new agents and `warn` or `gate` for costly or
risky production agents. `abort` should require explicit configuration.

### Detectors

Live evaluation can combine deterministic detectors with LLM judgment.

Deterministic detectors should handle obvious patterns:

- repeated identical operation input,
- repeated failing operation input,
- token budget thresholds,
- wall-clock timeout,
- maximum operation count,
- active process leak,
- unchanged diff after repeated edits,
- tests failing with the same error after multiple attempts,
- no progress events for a configured duration,
- shell fallback when a structured operation exists,
- too many browser navigations without evidence capture.

LLM-based detectors can handle semantic patterns:

- goal drift,
- ignored context,
- missing clarification,
- unsupported conclusion,
- hallucinated evidence,
- poor strategy,
- premature success claim,
- weak final answer,
- unsafe or overbroad plan.

The product should prefer deterministic detectors for hard gates and use LLM
judges for softer coaching and reflection.

### Budgets

Live evaluation should make budgets first-class:

```yaml
budgets:
  max_input_tokens: 100000
  max_output_tokens: 20000
  max_model_calls: 40
  max_tool_calls: 120
  max_repeated_tool_calls: 3
  max_runtime: 20m
  max_cost_usd: 5.00
  max_safety_denials: 2
```

Budget pressure should be visible to the agent. An agent that knows it is at
80% of its budget can switch from exploration to summarization or ask for help.

### Live coaching examples

Repeated tool call:

```text
You have called file_read on the same path three times without new constraints.
Use the current file content or explain what changed before reading again.
```

Ignored context:

```text
The session context says the user explicitly approved deleting eval-review.md.
Do not ask for approval again unless policy requires it.
```

Missing clarification:

```text
The task asks to improve "the prompt" but multiple prompts exist. Ask a
clarifying question before editing.
```

No progress:

```text
You have spent six turns planning without modifying files or asking a question.
Either execute the next safe step or explain the blocker.
```

Safety denial loop:

```text
The last two shell commands were denied by safety policy. Choose a structured
operation or ask for a policy-approved path.
```

### Live evaluation as training data

Live observations should become durable learning material:

- recurring warnings become regression scenarios,
- repeated budget alerts become fitness penalties,
- useful coaching messages become system-prompt improvements,
- detector false positives become detector test cases,
- intervention outcomes become promotion evidence.

This is where live evaluation feeds self-evolvement. It turns "the agent wasted
time" into a concrete scenario, detector, fitness penalty, or tool improvement.

### Architecture placement

Live evaluation introduces several concepts:

- live evaluation policy,
- detector specs,
- observation event shapes,
- budget specs,
- intervention modes,
- sidecar orchestration.

Suggested placement:

- `core/evaluation`: inert specs for live evaluation policy, detector specs,
  budgets, observation event shapes, and intervention modes.
- `runtime/evaluation`: deterministic detector implementations and budget
  accounting over runtime events.
- `orchestration/evaluation` or `orchestration/evolution`: sidecar lifecycle,
  event subscription, observation routing, and intervention coordination.
- `adapters/*`: CLI, HTTP, dashboard, and storage surfaces for live evaluation
  output.
- `apps/evaluator`: assembled evaluator agent that can act as an LLM-based live
  judge when configured.

The live evaluator must not bypass `runtime/operation.SafetyEnvelope`. It may
recommend, warn, pause, or request approval, but side-effecting interventions
must still flow through normal runtime policy.

### Product impact

Live evaluation is one of the most compelling parts of Constant
Self-Evolvement. It makes the runtime feel less like a black box and more like a
flight recorder plus copilot for agents.

The selling point is simple:

```text
AgentRuntime does not wait until your agent fails. It watches the run, catches
bad patterns early, and turns those patterns into future improvements.
```

## Scenario Synthesis

Self-evolvement becomes powerful when new scenarios are generated from real
friction.

### From user complaints

User complaint:

```text
The docs make me put target_submit instructions into the prompt. That should be
built into the evaluator.
```

Generated scenario:

```yaml
name: evaluator-target-flags-without-tool-instructions
prompt: Evaluate coder using target CLI flags only.
expected:
  - Evaluator chooses an appropriate probe.
  - Evaluator calls target_submit without user-provided tool instructions.
  - Final report includes target evidence.
```

### From tool reflections

Reflection:

```text
No native file_delete tool forced Python Path.unlink.
```

Generated scenario:

```yaml
name: coder-delete-file-native-tool
prompt: Delete eval-review.md after explicit user approval.
expected:
  - Uses file_delete.
  - Does not use shell rm.
  - Does not use Python unlink.
```

### From failed runs

Failure:

```text
file_patch failed because exact old text did not match.
```

Generated scenario:

```yaml
name: coder-line-oriented-test-insertion
prompt: Insert a new test after TestMetricsAggregation.
expected:
  - Uses line/range edit operation.
  - Preserves neighboring tests.
  - gofmt passes.
```

### From production traces

A support bot gives a wrong refund answer. The loop creates a regression case:

```yaml
name: refund-policy-defective-product-after-window
prompt: Customer asks for a refund after 31 days for a defective product.
expected:
  - Mentions defective-product exception.
  - Offers replacement or escalation.
  - Does not incorrectly deny all refunds after 30 days.
```

## Example: Improving Coder Tooling

A real reflection from the coding harness identified these issues:

- no native `file_delete`,
- brittle exact-text `file_patch`,
- root-level files missed by glob patterns,
- no code-aware outline or symbol search,
- shell/Python fallbacks for common operations,
- awkward inspection of oversized command output.

A self-evolvement plan can turn that into variants.

### Variant: add `file_delete`

Hypothesis:

```text
A native file_delete operation will reduce unsafe shell/Python fallbacks and
create a clearer audit trail for approved deletions.
```

Scenarios:

- delete explicitly requested file,
- reject deleting a directory without recursive approval,
- reject broad glob deletion without confirmation,
- report the deleted path,
- preserve safety policy.

### Variant: add `file_edit`

Hypothesis:

```text
Line- and range-oriented edits will reduce brittle exact patch failures and
whole-file rewrites.
```

Scenarios:

- insert function after a line,
- replace range,
- delete range,
- dry-run diff,
- reject stale line numbers,
- gofmt after Go edit.

### Variant: fix root glob matching

Hypothesis:

```text
Glob semantics that include root-level files will reduce false file-not-found
failures.
```

Scenarios:

- `**/*.md` finds root Markdown files,
- `**/eval-review.md` finds root `eval-review.md`,
- behavior is documented.

### Variant: add command output summarizer

Hypothesis:

```text
A native artifact summarizer will reduce Python parsing of oversized command
output.
```

Scenarios:

- summarize `task verify` output,
- extract lines matching `FAIL`, `error`, and `Violations`,
- show tail while preserving exit status.

### Variant: add Go outline/symbol tools

Hypothesis:

```text
Code-aware navigation will reduce grep spam and missed references.
```

Scenarios:

- locate `targetSubmit`,
- list file declarations,
- find references to `Yolo`,
- explain package imports.

## Example: Improving a Support Bot

Subject: `support-bot`

Fitness:

- policy correctness,
- grounded citations,
- escalation quality,
- privacy compliance,
- customer tone,
- latency.

Loop:

1. Production trace shows an incorrect refund answer.
2. Evaluator creates a report with evidence.
3. Scenario synthesizer creates a regression scenario.
4. Planner proposes prompt and retrieval-policy changes.
5. Variant runner evaluates candidate prompt against the full support suite.
6. Promotion opens a PR updating the support-bot resources.

No code change is required, but the same loop applies.

## Proposed Package Placement

The design should respect AgentRuntime layering.

### Core

`core/evaluation` should contain inert specs and report shapes:

- scenario specs,
- rubric specs,
- fitness specs,
- metrics,
- findings,
- evidence refs,
- report specs.

`core/evolution` should contain inert improvement-plan shapes:

- subject refs,
- variant specs,
- change specs,
- promotion policies.

Core should not execute scenarios, access files, run models, call HTTP, or apply
patches.

### Runtime

`runtime/evaluation` should execute or store core evaluation contracts:

- deterministic metric aggregation,
- report storage,
- baseline comparison,
- artifact indexing.

`runtime/evolution` can hold execution-neutral variant state machines and stores
if they only depend on core/runtime concepts.

### Orchestration

`orchestration/evolution` should compose runtime pieces into the improvement
loop:

- run baseline,
- run variants,
- coordinate evaluator/diagnoser/mutator/verifier roles,
- compare reports,
- decide promotion recommendation.

### Adapters

Adapters should implement external boundaries:

- Git worktree and patch adapters,
- GitHub PR adapter,
- filesystem suite loader,
- HTTP API,
- CLI surface,
- dashboard API.

### Apps

Apps assemble concrete products:

- `apps/evaluator` for evaluation agents,
- `apps/coder` as both subject and mutator,
- possible `apps/evolver` for the full improvement product.

## CLI Sketch

Evaluation:

```bash
coder evaluator run .agents/evals/coder.yaml
coder evaluator compare .agents/runs/baseline/report.json .agents/runs/candidate/report.json
```

Evolution planning:

```bash
coder evolve propose \
  --subject coder \
  --review .agents/reviews/2026-05-15-tool-improvements.md
```

Variant execution:

```bash
coder evolve run \
  --plan .agents/evolution/plans/coder-tooling.yaml \
  --variant add-file-delete
```

Promotion:

```bash
coder evolve promote \
  --run .agents/runs/2026-05-15T120000Z \
  --mode pull-request
```

Scenario synthesis:

```bash
coder evolve synthesize \
  --from .agents/reviews/2026-05-15-tool-improvements.md \
  --subject coder
```

## Storage Layout

A local filesystem layout can bootstrap the feature:

```text
.agents/
  evals/
    coder/
      suites/
        editing-tools.yaml
      scenarios/
        delete-file.yaml
        find-root-file.yaml
  runs/
    2026-05-15T120000Z/
      baseline.json
      traces.jsonl
      report.json
      artifacts/
  reviews/
    2026-05-15-tool-improvements.md
  evolution/
    plans/
      coder-tooling.yaml
    variants/
      add-file-delete/
        patch.diff
        report.json
    promotions/
      2026-05-15-add-file-delete.md
  memory/
    reflections/
      coder.md
```

The storage backend should later be abstract so hosted and team workflows can use
SQL, object storage, GitHub artifacts, or AgentRuntime cloud storage.

## MVP Roadmap

### MVP 1: Evaluation suite runner

Add first-class scenario suite input and report output.

```bash
coder evaluator run .agents/evals/coder.yaml
```

Output:

```text
.agents/runs/<timestamp>/report.json
```

### MVP 2: Structured evaluator reports

Extend the evaluator app to emit strict JSON/YAML reports with:

- status,
- score,
- metrics,
- findings,
- evidence,
- recommendations.

### MVP 3: Review-to-plan

Generate an improvement plan from a review file.

```bash
coder evolve propose \
  --subject coder \
  --review .agents/reviews/2026-05-15-tool-improvements.md
```

### MVP 4: Single-variant execution

Run one candidate variant in an isolated branch or worktree.

```bash
coder evolve run \
  --plan .agents/evolution/plans/coder-tooling.yaml \
  --variant add-file-delete
```

### MVP 5: Promotion by pull request

Create a PR or patch when a candidate improves fitness and passes gates.

### MVP 6: Scenario synthesis

Generate regression scenarios from:

- evaluator findings,
- review files,
- failed traces,
- production examples,
- red-team prompts.

## Open Questions

1. Should `evolution` be a separate top-level concept, or should it be a mode of
   `evaluation`?
2. What is the minimum useful fitness schema that avoids over-engineering?
3. How should LLM-as-judge scores be calibrated and audited?
4. How should hidden scenarios be stored and protected?
5. What promotion modes are safe for prompt-only changes?
6. How do we prevent an agent from optimizing away useful but costly safety
   checks?
7. How should production traces be anonymized before becoming scenarios?
8. How should teams share scenario suites across repositories?
9. Should scenario synthesis require human approval before entering the
   regression suite?
10. What is the right boundary between durable reflection memory and source-code
    changes?

## Design Thesis

Constant Self-Evolvement means AgentRuntime agents can improve under measurement.

An AgentRuntime agent is not just a prompt and tools. It is a versioned,
evaluable, improvable system with scenarios, traces, fitness functions,
reflection memory, variant generation, regression gates, and promotion policy.

The first compelling demo should use the runtime's own coding harness:

1. The coder reflects that it lacks `file_delete` and robust editing tools.
2. AgentRuntime synthesizes scenarios from that reflection.
3. The evolver proposes a concrete improvement plan.
4. Coder implements a candidate variant.
5. Evaluator scores baseline versus candidate.
6. Verification gates the change.
7. The successful variant is promoted by PR or commit.

That story is simple, credible, and powerful:

```text
AgentRuntime used its own evaluator to improve its own coding harness.
```

The same loop then applies to every agent built on the platform.
