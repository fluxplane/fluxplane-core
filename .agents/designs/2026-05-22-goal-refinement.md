# Goal Refinement, Goal Review, and Long-Lived Continuation Control

## Status

Proposed design.

This design replaces the current one-shot `/goal` command semantics with a
thread-scoped goal lifecycle and reviewer-driven continuation control.

The current implementation treats `/goal <text> --max <n>` as a temporary
wrapper around one inbound message. It installs a prompt stop condition for that
run only, and the stop evaluator returns either `stop` or `continue` with a next
instruction. That is useful as a loop primitive, but it is not a durable goal
model. It does not give the user a long-lived goal, does not preserve acceptance
criteria, and makes the stop evaluator both reviewer and continuation
controller.

The new direction splits those responsibilities:

- `/goal` owns a durable thread goal.
- A goal reviewer verifies progress against explicit acceptance criteria.
- Reviewer tool calls update goal state.
- The continuation loop reads goal state and decides whether to stop or continue.

Related context:

- [Agent Loop](../../docs/agent-loop.md)
- [Configuration](../../docs/configuration.md)
- [Concepts](../../docs/concepts.md)
- [Architecture](../../docs/architecture.md)
- [Task Plugin, Task Commands, and Review Backlog](2026-05-16-task-review-commands.md)

## Problem

The existing `/goal` command overloads a user-facing product concept with an
implementation detail of the agent loop.

Today, `/goal` means "run this message with a temporary prompt stop condition."
That makes the goal disappear after the command returns. It also makes the stop
evaluator decide directly whether the parent agent should continue. The
evaluator sees some recent context and can return a continuation instruction, but
there is no durable, inspectable object representing:

- the goal text;
- acceptance criteria;
- whether the goal is active, paused, reached, rejected, or cleared;
- the latest review;
- evidence for completion;
- rejection reasons and suggested next actions;
- which thread owns the goal.

The result is a continuation mechanism without an explicit control-plane state
machine. Users cannot pause or resume the goal. A later turn cannot inspect the
current goal. Another component cannot reliably tell whether continuation should
run because the goal is active or because a one-shot command is still unwinding.

The stop evaluator also has too much authority. It evaluates progress and
controls loop continuation in one response. The new model should make review a
separate action: a goal-reviewer agent examines evidence, calls a constrained
goal tool, and writes the result into goal state. Only after that does the
session continuation controller read state and decide the next loop action.

## Goals

- Make a goal a durable thread-scoped entity that can live across turns.
- Replace `/goal --max ...` with a simple command surface:
  - `/goal <goal>` sets or replaces the current thread goal.
  - `/goal` shows current goal status.
  - `/goal pause` pauses automatic goal continuation.
  - `/goal resume` resumes automatic goal continuation.
  - `/goal clear` clears the current goal.
- Generate initial acceptance criteria when a goal is set.
- Run a goal-reviewer in a fresh child session or equivalent empty-context review
  call after each inner loop when an active goal needs verification.
- Require the reviewer to call a bound goal decision tool.
- Persist reviewer decisions as goal state changes.
- Continue the parent loop only from rejected review results, using reviewer
  reasons and suggestions as the continuation prompt.
- Stop the parent loop when the goal is reached, paused, cleared, absent, or
  failed in a way that requires user intervention.
- Render current goal state into the parent agent context automatically through a
  goal context provider whenever a goal exists and has not been cleared.
- Move goal behavior into a first-party goal plugin rather than keeping `/goal`
  as a hard-coded session command.
- Define a base native `/review` plugin and shared `core/review`
  request/result contracts that goal review can reuse without plugin-to-plugin
  coupling.
- Define smoke tests that prove the lifecycle and continuation behavior work.

## Non-goals

- Do not preserve `/goal --max`, `--max-continuations`, or `--max <n>` as public
  command syntax. This rewrite is pre-1.0 and should remove stale shapes.
- Do not make provider transcript continuation part of goal state. Provider
  continuation handles remain conversation projection concerns.
- Do not make the goal reviewer a general unrestricted worker. It is a verifier
  with bounded read-only capabilities and a narrow goal decision tool.
- Do not make goals global across users or sessions. The first implementation is
  one active goal per thread.
- Do not require persisted generic review reports before implementing goals.
  The generic `/review` plugin can return structured command results first.

## Concepts

### Goal

A goal is a durable objective attached to a thread. It is not a single request
and not a task execution record.

The goal represents "what this conversation is trying to drive toward until the
user changes it." It can influence multiple user turns and multiple continuation
loops in the same thread.

The goal belongs conceptually between command control and task lifecycle:

- A `Request` asks to set, pause, resume, clear, or inspect the goal.
- A `Goal` is an objective state for the thread.
- A `Task` remains a durable work item with lifecycle, assignment, steps, and
  artifacts.
- A `Review` is a verifier's assessment of evidence against criteria.
- A continuation is a runtime attempt to make progress toward the active goal.

The initial implementation should model goal as a new concept only as far as it
is needed for stable cross-package state. Core owns inert contracts and event
shapes; runtime owns projection/storage; orchestration owns session-loop use;
plugins own commands, agents, and operations.

### Goal Reviewer

The goal reviewer is a verifier agent, not the parent implementation agent. It
gets a fresh review context and must call a bounded decision tool. Its job is to
answer:

- Is the current goal reached according to acceptance criteria?
- If reached, what evidence supports that?
- If not reached, why not?
- What concrete next suggestion should be sent back to the parent agent?

The reviewer must not directly continue the parent loop. It updates goal state.
The continuation controller reads that state.

### Goal Continuation Controller

The continuation controller is session orchestration logic. It runs after the
inner agent/tool loop has reached a terminal response or a clean step-limit
boundary where goal continuation is allowed.

It does this sequence:

1. Read the thread's active goal state.
2. If no active/resumed goal exists, stop normally.
3. Run goal review.
4. Read the updated goal state.
5. If reached, stop successfully.
6. If rejected, continue with the reviewer's suggestion.
7. If paused or cleared, stop.
8. If review fails structurally, return a session error.

The controller should not infer completion from natural-language reviewer text.
Only the bound goal decision tool updates state.

### Base Review Contract

There should be a small reusable review contract, but the reusable structs
should not live inside `plugins/native/review` if the goal plugin needs them.
Putting the structs in the review plugin would force the goal plugin to import a
sibling optional plugin or require the review plugin to be selected whenever
goals are selected. That is unnecessary coupling.

Use this split instead:

- `core/review`: inert review request/result contracts.
- `plugins/native/review`: generic `/review` command, review agent/session, and
  operations using `core/review`.
- `plugins/native/goal`: goal command, goal-reviewer agent/session, goal state
  operations, and goal-specific review decisions that embed or reference
  `core/review.Result`.

The base review contract is intentionally generic:

```go
package review

type ID string
type RequestID string
type CriterionID string

type SubjectKind string

const (
	SubjectText       SubjectKind = "text"
	SubjectThread     SubjectKind = "thread"
	SubjectDiff       SubjectKind = "diff"
	SubjectGoal       SubjectKind = "goal"
	SubjectTask       SubjectKind = "task"
	SubjectResource   SubjectKind = "resource"
)

type Request struct {
	ID           RequestID        `json:"id,omitempty"`
	Subject      Subject          `json:"subject"`
	Criteria     []Criterion      `json:"criteria,omitempty"`
	Instructions string           `json:"instructions,omitempty"`
	Evidence     []EvidenceRef    `json:"evidence,omitempty"`
	Metadata     map[string]string `json:"metadata,omitempty"`
}

type Subject struct {
	Kind     SubjectKind       `json:"kind"`
	Text     string            `json:"text,omitempty"`
	Refs     []Ref             `json:"refs,omitempty"`
	Metadata map[string]string `json:"metadata,omitempty"`
}

type Ref struct {
	Kind string `json:"kind,omitempty"`
	URI  string `json:"uri,omitempty"`
	Name string `json:"name,omitempty"`
}

type Criterion struct {
	ID          CriterionID       `json:"id,omitempty"`
	Description string            `json:"description"`
	Required    bool              `json:"required,omitempty"`
	Metadata    map[string]string `json:"metadata,omitempty"`
}

type EvidenceRef struct {
	Kind    string            `json:"kind,omitempty"`
	URI     string            `json:"uri,omitempty"`
	Summary string            `json:"summary,omitempty"`
	Metadata map[string]string `json:"metadata,omitempty"`
}

type Decision string

const (
	DecisionAccepted     Decision = "accepted"
	DecisionRejected     Decision = "rejected"
	DecisionInconclusive Decision = "inconclusive"
)

type Result struct {
	ID              ID                `json:"id,omitempty"`
	RequestID       RequestID         `json:"request_id,omitempty"`
	Decision        Decision          `json:"decision"`
	Summary         string            `json:"summary,omitempty"`
	CriteriaResults []CriterionResult `json:"criteria_results,omitempty"`
	Findings        []Finding         `json:"findings,omitempty"`
	Evidence        []Evidence        `json:"evidence,omitempty"`
	Suggestions     []Suggestion      `json:"suggestions,omitempty"`
	Risk            string            `json:"risk,omitempty"`
	Metadata        map[string]string `json:"metadata,omitempty"`
}

type CriterionResult struct {
	CriterionID CriterionID `json:"criterion_id,omitempty"`
	Description string      `json:"description,omitempty"`
	Status      string      `json:"status"` // met, unmet, unknown
	Notes       string      `json:"notes,omitempty"`
}

type Finding struct {
	Severity string `json:"severity,omitempty"` // info, warning, error
	Message  string `json:"message"`
	Ref      Ref    `json:"ref,omitempty"`
}

type Evidence struct {
	Kind    string `json:"kind,omitempty"`
	Summary string `json:"summary"`
	Ref     Ref    `json:"ref,omitempty"`
}

type Suggestion struct {
	Text     string `json:"text"`
	Priority string `json:"priority,omitempty"`
}
```

The goal plugin maps this generic result into goal state:

- `review.DecisionAccepted` becomes `GoalReached` when required criteria are met
  and evidence is present.
- `review.DecisionRejected` becomes `GoalRejected` with reasons and suggestions.
- `review.DecisionInconclusive` also becomes `GoalRejected`, but with a reason
  that the reviewer could not establish completion.

This gives `/review` and `/goal` shared vocabulary without making goal state
depend on the review plugin implementation.

## Command Design

### Public Command Surface

The public command shape is intentionally small:

```text
/goal
/goal <goal text>
/goal pause
/goal resume
/goal clear
```

There are no flags and no structured user parameters for max continuations in
the command.

### `/goal <goal>`

Sets the current thread goal.

If the thread already has an active or paused goal, the current goal is archived
and replaced. Replacement is explicit in state, not a destructive overwrite:

- previous goal receives an archived/replaced terminal state;
- new goal receives a fresh ID;
- new acceptance criteria are generated;
- new goal becomes active.

The command returns a status summary containing:

- goal text;
- active status;
- generated acceptance criteria;
- any warnings from criteria generation.

### `/goal`

Shows current thread goal status. It must not run the agent or reviewer.

Output includes:

- status: active, paused, reached, cleared, rejected, or absent;
- goal text;
- acceptance criteria;
- latest reviewer result, if any;
- latest evidence, if reached;
- latest rejection reasons and suggestions, if rejected;
- whether automatic continuation is currently enabled.

If no goal exists, it reports that no goal is set and shows the supported
commands.

### `/goal pause`

Marks the current active or rejected goal as paused. A paused goal remains
inspectable but does not trigger post-turn review or continuation.

If no active goal exists, return a command error with code
`goal_not_active`.

### `/goal resume`

Marks a paused or rejected goal as active again. The next normal user turn, or a
goal-triggered continuation if one is already in progress, can review it.

If the goal was reached, cleared, or absent, return a command error. Reached and
cleared goals are terminal and should not be resumed.

### `/goal clear`

Clears the current active, paused, or rejected goal. Clearing archives the goal
for history and disables automatic continuation. It does not delete prior goal
events.

If no goal exists, the command can return `ok` with an "already clear" status so
scripts and UIs can safely call it.

### Command Ownership

The existing built-in `/goal` command should be removed from the hard-coded
session command list once the goal plugin can contribute current-session command
handlers.

This requires one small orchestration extension: plugin resolution must be able
to contribute session command handlers, not only inert command specs. The current
`TargetSession` command target means "run a delegated child session"; it is not
the right shape for `/goal`, because `/goal` must inspect and mutate current
thread goal state. The goal command should therefore resolve to a
current-session handler supplied by the goal plugin.

The goal plugin contributes:

- path: `/goal`;
- target: current-session handler, not delegated child session;
- policy: verified user/system callers;
- input: no flags; command arguments are interpreted as either subcommand or goal
  text.

The parser rule is:

- no args means status;
- one arg exactly `pause`, `resume`, or `clear` means lifecycle command;
- any other args are joined as goal text.

This keeps `/goal resume` unambiguous and makes `/goal "resume the migration"`
possible through quoting only if command parsing preserves it as a goal argument.
If slash parsing cannot distinguish quoted command words today, the command
handler should treat multiple args or any non-exact single lifecycle token as
goal text.

### Command Specs

These commands should be contributed by plugins in Go, not authored as current
agentdir YAML command files. The current YAML command loader supports prompt and
workflow targets. `/goal` also needs a current-session handler, which YAML
cannot express today.

The goal plugin contribution should be equivalent to a command spec plus a
session handler binding:

```go
type SessionCommandContribution struct {
	Spec    command.Spec
	Handler session.CommandHandler
}

SessionCommandContribution{
	Spec: command.Spec{
		Path:        command.Path{"goal"},
		Description: "Set, inspect, pause, resume, or clear the current thread goal.",
		Target: invocation.Target{
			Kind: invocation.TargetSession,
		},
		Input: runtimeoperation.TypeOf[goal.CommandInput]("goal_command_input"),
		Policy: policy.InvocationPolicy{
			AllowedCallers: []policy.CallerKind{
				policy.CallerUser,
				policy.CallerSystem,
			},
			RequiredTrust: policy.TrustVerified,
		},
	},
	Handler: goal.ExecuteCommand,
}
```

The command input type is parser-facing and deliberately minimal:

```go
type CommandInput struct {
	Args []string `json:"args,omitempty" command:"arg"`
}
```

The handler resolves the mode from `Args`:

- `[]`: status;
- `["pause"]`: pause;
- `["resume"]`: resume;
- `["clear"]`: clear;
- anything else: set or replace goal with `strings.Join(args, " ")`.

The review plugin contribution should be equivalent to:

```go
command.Spec{
	Path:        command.Path{"review"},
	Description: "Run a fresh read-only review of a subject.",
	Target: invocation.Target{
		Kind:    invocation.TargetSession,
		Session: "review",
	},
	Input: runtimeoperation.TypeOf[review.CommandInput]("review_command_input"),
	Policy: policy.InvocationPolicy{
		AllowedCallers: []policy.CallerKind{policy.CallerUser},
		RequiredTrust:  policy.TrustVerified,
	},
}
```

`/review` can use delegated `TargetSession` because generic review intentionally
runs as a fresh child session and does not mutate current-session control state.

`/review` accepts a free-form subject:

```go
type CommandInput struct {
	Subject []string `json:"subject,omitempty" command:"arg"`
}
```

The YAML-like resource shape below is illustrative only. It describes the target
resource shape the plugins contribute; it is not meant to be loaded by the
current agentdir command YAML decoder until that decoder supports session
targets.

```yaml
commands:
  - path: /goal
    description: Set, inspect, pause, resume, or clear the current thread goal.
    target:
      current_session_handler: goal.ExecuteCommand
    input:
      type: goal_command_input
    policy:
      callers: [user, system]
      trust: verified

  - path: /review
    description: Run a fresh read-only review of a subject.
    target:
      session: review
    input:
      type: review_command_input
    policy:
      callers: [user]
      trust: verified
```

## Goal State Model

### Placement

Use the layer rules:

- `core/goal`: inert contracts, events, statuses, IDs, review records.
- `runtime/goal`: event-sourced projection/store over `event.Store`.
- `orchestration/session`: reads projected goal state and triggers review after
  inner turns.
- `plugins/native/goal`: command contribution, context provider, operations,
  goal-reviewer agent and session.

If implementation shows that `core/goal` is premature, keep the first model in
`plugins/native/goal` plus runtime projection local to the plugin. However, the
preferred design is `core/goal` because session orchestration needs to read goal
state without importing plugin internals.

### Entity Fields

`goal.State` should include:

- `ID`: stable goal ID.
- `ThreadID`: owning thread.
- `Status`: active, paused, reached, rejected, cleared, archived.
- `Text`: original user goal.
- `AcceptanceCriteria`: generated criteria.
- `CreatedAt`, `UpdatedAt`: event-derived timestamps if available from records;
  otherwise optional.
- `Revision`: monotonically increasing projection revision.
- `LatestReview`: latest goal review result.
- `ArchivedReason`: replacement, clear, or superseded.
- `SupersededBy`: replacement goal ID when applicable.

`goal.Review` should include:

- `ReviewID`: stable review attempt ID.
- `GoalID`: reviewed goal ID.
- `RunID`: parent run being reviewed.
- `ReviewerThreadID`: child review thread, when available.
- `ReviewerRunID`: child review run, when available.
- `Decision`: reached or rejected.
- `Evidence`: completion evidence for reached decisions.
- `Reasons`: rejection reasons for rejected decisions.
- `Suggestions`: concrete next actions for rejected decisions.
- `CriteriaResults`: optional per-criterion pass/fail notes.

### Status Semantics

Use the following statuses:

- `active`: goal participates in post-turn review and continuation.
- `paused`: goal is retained but ignored by automatic continuation.
- `rejected`: latest review says the goal is not reached; continuation may run.
- `reached`: latest review says the goal is complete; terminal.
- `cleared`: user cleared the goal; terminal.
- `archived`: goal was replaced by a newer goal; terminal.

`rejected` is intentionally not terminal. It captures reviewer state and still
allows continuation. The controller may treat `active` with no latest review and
`rejected` with latest review similarly: both are resumable.

A reached goal remains visible as the current goal until the user runs
`/goal clear` or replaces it with `/goal <new goal>`. Reached stops automatic
continuation, but it is not hidden from `/goal` status or the goal context
provider. This makes the final objective and evidence available to later turns
without requiring a read tool.

`cleared` is the only user-facing state that removes the goal from ambient
context. The event history remains append-only, but context rendering treats the
thread as having no current goal after clear.

### Events

Goal events should be append-only:

- `GoalSet`: new goal created for a thread.
- `GoalArchived`: old goal replaced or superseded.
- `GoalAcceptanceCriteriaGenerated`: criteria generated by reviewer.
- `GoalPaused`: user paused.
- `GoalResumed`: user resumed.
- `GoalCleared`: user cleared.
- `GoalReviewRequested`: session requested verification after an inner turn.
- `GoalReached`: reviewer marked reached with evidence.
- `GoalRejected`: reviewer marked rejected with reasons and suggestions.
- `GoalReviewFailed`: optional diagnostic event for reviewer execution failure.

State projection should be deterministic from events. Command handlers and
review tools append events; they do not mutate a database row directly.

### Storage

The first implementation should use the existing event store path, mirroring the
task store approach:

- append goal events to the thread's event stream;
- project current goal state from thread events;
- optionally index current state later if list/search is needed.

There is no need for a separate SQL table for the first goal slice.

### Goal Context Provider

The goal plugin should contribute a context provider, for example
`session_goal`, that renders the current thread goal into the parent agent's
normal context whenever a non-cleared goal exists.

This replaces the need for a model-facing "read goal status" tool in the first
implementation. The parent agent should not need to ask for goal state; it
should see the current objective, criteria, and latest reviewer feedback as part
of the session context.

Render policy:

- no goal or cleared goal: render nothing;
- active goal: render goal text and acceptance criteria;
- paused goal: render goal text, acceptance criteria, and that continuation is
  paused;
- rejected goal: render goal text, acceptance criteria, latest rejection reasons,
  and latest suggestions;
- reached goal: render goal text, acceptance criteria, latest evidence, and that
  the goal has been reached.

The context provider should render a compact stable text block:

```text
Current goal
Status: rejected
Goal: Improve parser error coverage.

Acceptance criteria:
- Parser error cases are covered by focused tests.
- Relevant package tests pass.

Latest review:
Rejected: malformed quoted input is still untested.
Suggested next action: add a test for unterminated quoted input.
```

The provider should also support change-aware rendering. When the projected goal
state changes between turns, the provider can render a diff-style summary of the
change in addition to the current state:

```text
Goal update
- Status: active
+ Status: rejected
+ Latest reviewer reason: malformed quoted input is still untested.
+ Suggested next action: add a test for unterminated quoted input.
```

The diff does not need to be a formal patch format. It is a compact context
rendering that helps the parent agent notice goal changes without tool calls.
The durable source of truth remains the projected goal state.

## Acceptance Criteria Generation

When `/goal <goal>` sets a new goal, the system should generate initial
acceptance criteria.

Generation uses the goal-reviewer profile in "criteria generation" mode:

- fresh child session or direct model call with empty conversation;
- input contains goal text and any available thread metadata;
- reviewer is instructed to produce concise acceptance criteria;
- reviewer must call a criteria tool, not answer in free text.

The criteria tool writes `GoalAcceptanceCriteriaGenerated`.

If criteria generation fails:

- the goal can still be created with a single fallback criterion: "The stated
  goal is complete.";
- the command result reports the generation failure as a warning;
- continuation review can still run.

This avoids making `/goal <goal>` unusable when the reviewer model is
temporarily unavailable, while preserving a visible diagnostic.

## Reviewer Design

### Fresh Context

The reviewer must not inherit the parent conversation as a normal transcript.
It receives a fresh review prompt assembled from structured inputs:

- goal text;
- acceptance criteria;
- latest parent agent response;
- operation effect summaries from the latest turn;
- bounded evidence references;
- current goal status;
- parent run ID and thread ID.

The reviewer may also use read-only tools:

- project inventory/status operations;
- file read/search operations;
- git status/diff operations;
- task/status read operations if selected.

It must not receive write tools. It must not run shell or browser tools unless a
future explicit review policy allows them.

### Reviewer Tool

The reviewer receives exactly one goal decision tool family:

- `goal_mark_reached`
- `goal_mark_rejected`

or a single typed tool:

- `goal_review_decision` with action `reached` or `rejected`.

Prefer a single typed tool for schema simplicity and to mirror the existing
`continuation_decision` pattern, but make it a real goal operation that appends
goal events rather than a synthetic in-memory decision.

Input shape:

```json
{
  "goal_id": "goal_...",
  "action": "reached | rejected",
  "evidence": ["..."],
  "reasons": ["..."],
  "suggestions": ["..."],
  "criteria_results": [
    {
      "criterion": "...",
      "status": "met | unmet | unknown",
      "notes": "..."
    }
  ]
}
```

Validation:

- reached requires non-empty evidence;
- rejected requires at least one reason and one suggestion;
- reviewer can only update the goal ID it was bound to;
- reviewer decisions are rejected if the goal is no longer active/rejected at
  decision time.

### Reviewer Result Contract

The reviewer result consumed by the continuation controller is not text output.
It is the projected goal state after the decision event.

The child agent may produce a textual summary for logs, but session control must
ignore it for state decisions.

### Reviewer Agents And Sessions

The base review plugin contributes a generic reviewer agent and session. The
goal plugin contributes a stricter goal-reviewer agent and session.

The generic review agent is for user-requested `/review` work. It can produce a
structured `core/review.Result`, but it does not mutate goal state and does not
drive continuation.

YAML-like shape:

```yaml
agents:
  - name: reviewer
    description: Read-only reviewer for user-requested review subjects.
    driver:
      kind: llmagent
    system: |
      You are a read-only reviewer. Inspect the requested subject against the
      supplied criteria. Report findings first, then evidence, suggestions, and
      residual risk. Do not modify files or external systems.
    turns:
      max_steps: 20
    operations:
      - review_submit_result
      - project_inventory
      - project_files
      - project_docs
      - dir_list
      - dir_tree
      - file_read
      - grep
      - glob
      - git_status
      - git_diff

sessions:
  - name: review
    description: Fresh read-only review session.
    agent: reviewer
    operations:
      - review_submit_result
      - project_inventory
      - project_files
      - project_docs
      - dir_list
      - dir_tree
      - file_read
      - grep
      - glob
      - git_status
      - git_diff
    metadata:
      role: reviewer
      context_mode: fresh
```

The goal reviewer is narrower. It exists only to verify a specific bound goal.
It should receive the goal ID, goal text, criteria, latest-turn summary, and
bounded evidence refs. It must call the goal decision operation exactly once.

YAML-like shape:

```yaml
agents:
  - name: goal-reviewer
    description: Verifies whether the current thread goal has been reached.
    driver:
      kind: llmagent
    system: |
      You are a goal verifier. Decide whether the bound goal is reached using
      only the supplied goal, acceptance criteria, evidence, and read-only
      inspection tools. You must call goal_review_decision exactly once. Do not
      answer in free text. Do not modify files or external systems.
    turns:
      max_steps: 12
    operations:
      - goal_review_decision
      - project_inventory
      - project_files
      - project_docs
      - file_read
      - grep
      - glob
      - git_status
      - git_diff
      - task_get
      - task_list_artifacts
      - task_read_artifact

sessions:
  - name: goal-reviewer
    description: Fresh goal verification session.
    agent: goal-reviewer
    operations:
      - goal_review_decision
      - project_inventory
      - project_files
      - project_docs
      - file_read
      - grep
      - glob
      - git_status
      - git_diff
      - task_get
      - task_list_artifacts
      - task_read_artifact
    metadata:
      role: goal_reviewer
      context_mode: fresh
```

The actual operation list should be filtered by selected plugins and available
catalog entries. Missing optional read operations should not prevent the goal
plugin from loading; they should simply be omitted from the reviewer session.

## Continuation Loop Integration

### Current Loop

Today, after `runInnerTurn`, session calls `evaluateContinuation`. The existing
path evaluates a stop condition and gets `continue` or `stop`.

### New Loop

For goal-aware sessions, the post-inner-loop step becomes:

1. Project current thread goal.
2. If no active or rejected goal exists, use normal terminal behavior.
3. If goal is paused, cleared, reached, or archived, stop normally.
4. Build a review request from the latest inner result and accumulated effects.
5. Run goal reviewer.
6. Re-project goal state.
7. If state is reached, apply terminal agent decision and stop.
8. If state is rejected, continue with the latest review suggestion.
9. If review failed, return `goal_review_failed`.

The continuation prompt sent back to the parent agent should be concise and
explicit:

```text
The goal reviewer rejected completion.

Reasons:
- ...

Suggested next action:
- ...
```

This prompt becomes both:

- the pending transcript input for the next model call;
- a `session.continuation` observation with metadata linking the goal ID and
  review ID.

### Caps And Safety

Public `/goal` no longer accepts a cap. The loop still needs an internal safety
limit to avoid runaway continuations.

Use an internal default such as the current LLM continuation default. It should
be configured by session/agent policy, not by `/goal` flags.

When the internal cap is reached:

- mark the goal as rejected or paused with reason `continuation_limit_reached`;
- return a clear session error or terminal message;
- keep `/goal` status inspectable so the user can resume after adjusting the
  goal or providing more input.

The exact cap configuration can remain `turns.continuation.max_continuations`
for now, but it should not be exposed through `/goal`.

### Failure Modes

Reviewer unavailable:

- do not silently continue;
- emit `GoalReviewFailed`;
- return `goal_review_failed` with a clear error;
- leave the goal active so `/goal` status and future resume are possible.

Reviewer returns text without tool call:

- treat as structural failure;
- do not infer a decision.

Goal changed during review:

- decision tool rejects stale goal ID or revision;
- session re-reads state;
- if a newer goal exists, stop current continuation and let the next user turn
  proceed under the new state.

User pauses during a long review:

- decision tool may reject if status changed;
- continuation controller re-reads state and stops.

## Plugin Split

### Goal Plugin

Add `plugins/native/goal`.

Responsibilities:

- contribute `/goal` through a current-session command handler contribution;
- contribute goal operations and operation specs;
- contribute a `session_goal` context provider that renders current goal state
  automatically;
- contribute goal-reviewer agent and session;
- bind reviewer decision tools;
- expose read-only reviewer operation set;
- convert `core/review.Result`-shaped reviewer output into goal events.

The plugin should be selected explicitly by the app/distribution. The generic
Fluxplane app may include it by default if `/goal` is expected to be available
in local sessions.

### Review Plugin

Add `plugins/native/review` as a base review capability. It is a sibling
capability to the goal plugin, not a runtime prerequisite for the first goal
implementation.

The review plugin owns generic `/review` behavior:

- `/review <subject>` starts a fresh reviewer session;
- reviewer receives a subject, optional criteria, and read-only tools;
- reviewer calls `review_submit_result` and returns a structured
  `core/review.Result`;
- no parent continuation occurs automatically.

The review plugin may provide shared helper functions for prompt rendering or
read-only operation selection, but the goal plugin must not import the review
plugin package for core types. Shared types live in `core/review`; shared runtime
helpers, if needed, should live in `runtime/review` or `orchestration/review`
only when more than one caller needs them.

The relationship is:

- `review` plugin: generic human-requested review.
- `goal` plugin: goal-specific review that updates goal state and drives
  continuation.

This avoids making the generic review command block the goal work while still
giving both plugins a common request/result vocabulary.

### Coupling Decision

Use `core/review` now if both `/review` and goal review are implemented in the
same slice. If goal review is implemented first and `/review` is deferred, do
not invent a broad review runtime. Add only the minimal `core/review` structs
used by goal review and leave `plugins/native/review` as a follow-up.

This keeps the direction clear:

- reusable review data contracts are acceptable in core;
- reusable review execution is not added until a second execution path actually
  exists;
- plugins remain optional and do not import one another just to share structs.

## Security And Tool Policy

Goal review tools are control-plane tools. They should not be exposed to the
parent implementation agent as general capabilities.

Rules:

- parent agent receives current goal status through the `session_goal` context
  provider; no goal read tool is needed in the first implementation;
- reviewer agent can call the goal decision tool;
- decision tool validates the bound goal ID and expected reviewer context;
- reviewer session gets read-only tools by default;
- all read operations still pass through existing operation safety envelopes;
- no shell/filesystem/network side-effecting operation is granted to the
  reviewer by default.

The command handlers remain verified-user/system commands.

## User Experience

Example flow:

```text
user: /goal Improve parser error coverage.
system: Goal set.
        Acceptance criteria:
        - Parser error cases are covered by focused tests.
        - Tests can be run with the relevant package command.
        - No unrelated behavior is changed.

user: Add the tests.
agent: ...
reviewer: rejects completion, suggests adding missing malformed input case.
agent continuation input: The goal reviewer rejected completion...
agent: ...
reviewer: marks reached with evidence.
system: final agent response is returned; goal status is reached.
```

Pause:

```text
user: /goal pause
system: Goal paused.
```

Status:

```text
user: /goal
system: Goal: Improve parser error coverage.
        Status: rejected
        Latest reviewer reason: malformed input case is missing.
        Suggested next action: add a test for unterminated quoted input.
```

Replace:

```text
user: /goal Update the README examples.
system: Previous goal archived. New goal set.
```

## Implementation Plan

### Slice 1: Design And Contracts

- Add this design document.
- Add `core/review` with minimal inert review request/result structs shared by
  generic review and goal review.
- Add `core/goal` with inert IDs, statuses, review structs, command result
  structs, and events. Goal review records may embed or reference
  `core/review.Result`.
- Register goal event types through the existing event registry path. Review
  result structs do not need events until the generic review plugin persists
  review reports.
- Add validation tests for review and goal structs plus goal event codec round
  trips.

### Slice 2: Runtime Projection

- Add `runtime/goal` projection over thread events.
- Implement current-goal projection:
  - no goal;
  - active;
  - paused;
  - rejected with latest review;
  - reached;
  - cleared;
  - archived/replaced.
- Add tests for replacement, stale decisions, pause/resume/clear, and reached
  terminal state.

### Slice 3: Goal Plugin

- Add a pluginhost/session contribution path for current-session command
  handlers, so `/goal` can be plugin-owned without delegating to a child
  session.
- Add `plugins/native/goal`.
- Contribute `/goal`, goal-reviewer agent/session, operation specs, and
  executable operations.
- Contribute `session_goal` context provider and include it in the default goal
  plugin bundle so active/rejected/reached goal state is ambient in the parent
  agent context until cleared.
- Implement command parsing with no flags.
- Implement criteria generation with fallback criterion on reviewer failure.
- Bind `goal_review_decision` so reviewer calls append `GoalReached` or
  `GoalRejected`.
- Add plugin contribution tests.

### Slice 4: Session Integration

- Replace hard-coded built-in `/goal` with plugin-contributed `/goal`.
- Add goal-aware continuation control after the inner loop.
- Run goal reviewer through the existing session-agent runner or an equivalent
  fresh child session path.
- Ensure continuation prompt uses latest rejected review reasons and suggestions.
- Preserve existing non-goal continuation behavior for configured
  `turns.continuation` until it is intentionally removed or migrated.

### Slice 5: Review Plugin

- Add `plugins/native/review` with generic `/review`.
- Add `review_submit_result` operation returning `core/review.Result`.
- Use fresh reviewer context and read-only tools.
- Return a structured review report from state/tool output rather than free-text
  reviewer prose.
- Do not persist generic review reports in the first slice; return them as
  command results.
- Do not couple `/review` to goal state or continuation.

## Smoke Testing

### Unit Tests

- Goal projection:
  - set active goal;
  - replace archives previous goal;
  - pause/resume transitions;
  - clear disables continuation and removes ambient goal context;
  - reached is terminal for continuation but remains visible until cleared;
  - rejected stores reasons and suggestions.
- Goal context provider:
  - renders nothing when no goal exists or the current goal is cleared;
  - renders active goal text and criteria;
  - renders rejected goal feedback and suggestions;
  - renders reached evidence until `/goal clear`;
  - renders a compact update/diff block after goal state changes.
- Goal command parsing:
  - `/goal` returns status;
  - `/goal pause`, `/goal resume`, `/goal clear` route lifecycle commands;
  - `/goal fix tests` sets goal text;
  - `/goal --max 2 fix tests` is invalid or treated as literal goal text only
    if slash parser cannot reject flags cleanly.
- Reviewer decision operation:
  - reached requires evidence;
  - rejected requires reason and suggestion;
  - stale goal ID or revision is rejected;
  - reviewer cannot update a cleared/reached goal.
- Review contract:
  - `core/review.Request` validates subject text or refs;
  - `core/review.Result` validates accepted/rejected/inconclusive decisions;
  - review result criteria statuses accept only met, unmet, or unknown.
- Review plugin:
  - contributes `/review`, reviewer agent/session, and `review_submit_result`;
  - `/review <subject>` rejects an empty subject;
  - text-only reviewer output without `review_submit_result` is a structural
    failure.

### Session Tests

- Active goal plus reviewer reached:
  - parent inner turn runs once;
  - reviewer marks reached;
  - session stops;
  - `/goal` reports reached with evidence.
- Active goal plus reviewer rejected:
  - parent inner turn runs;
  - reviewer rejects with suggestion;
  - next continuation turn receives suggestion;
  - status stores rejection.
- Paused goal:
  - parent turn does not trigger reviewer;
  - no continuation occurs.
- Reviewer structural failure:
  - text-only reviewer response fails;
  - no continuation occurs;
  - goal remains active with review failure diagnostic.
- Thread isolation:
  - two threads can have separate active goals;
  - replacing a goal in one thread does not affect the other.
- Generic review:
  - `/review "inspect current diff"` starts the review session;
  - reviewer receives fresh context and read-only tools;
  - `review_submit_result` returns a structured report;
  - no goal state changes and no continuation prompt are produced.

### CLI/Smoke Scenarios

Use a fake or deterministic model in tests where possible.

Manual smoke test shape:

```text
fluxplane run . --input '/goal Improve parser error coverage'
fluxplane run . --input 'Add tests for parser errors'
fluxplane run . --input '/goal'
fluxplane run . --input '/goal pause'
fluxplane run . --input '/goal resume'
fluxplane run . --input '/goal clear'
```

Expected behavior:

- first command sets goal and shows generated criteria;
- work input triggers reviewer after agent response;
- status and context show latest review;
- pause disables review/continuation;
- resume re-enables it;
- clear removes active goal control and ambient goal context.

## Migration Notes

- Remove public max arguments from `/goal`.
- Update docs that currently describe `/goal --max` and
  `--max-continuations`.
- Keep internal continuation caps under agent/session configuration.
- Apps that want `/goal` must select the goal plugin.
- Existing tests for one-shot `/goal` prompt stop conditions should be rewritten
  around durable goal state and reviewer decisions.

## Open Follow-ups

- Decide whether a user should be able to edit acceptance criteria directly in a
  later command, such as `/goal criteria ...`.
- Decide later whether generic `/review` reports should be persisted as events.
  The first review-plugin slice returns structured command results only.
