# Core Concepts: Request, Task, Command, Workflow, Operation, and Execution

This document defines the vocabulary AgentRuntime uses for common work and
execution concepts. The terms are intentionally separated because each answers a
different semantic question. Keeping the boundaries crisp prevents transport
inputs, user-facing commands, model-facing tools, process definitions, and runtime
attempts from collapsing into one vague "thing to do".

## Summary

| Concept | Main question | Typical shape | Runtime state? | Notes |
|---|---|---|---|---|
| Request | What is being asked? | Boundary input or ask | Not necessarily | Communication intent from one actor/system to another. |
| Task | What work needs doing? | Objective with lifecycle | Sometimes | A unit of work, often assignable and decomposable. |
| Command | What known imperative was invoked? | Verb plus structured arguments | Dispatch has runtime state | Human/control-plane instruction routed to a handler. |
| Workflow | What process coordinates the work? | Steps, dependencies, gates | Spec or run | Reusable or ad hoc process structure. |
| Operation | What callable capability is exposed? | Tool/action spec plus typed input/output | Invocation has runtime state | Model/tool-facing capability contributed by runtime or plugins. |
| Execution | What actually ran? | Runtime attempt/result | Yes | Concrete run of a command, operation, workflow, process, or model call. |

A common path is:

```text
Request -> Task -> Workflow -> Command/Operation -> Execution
```

That path is illustrative, not mandatory. A simple request may produce a direct
answer with no explicit task or workflow. A command may directly trigger one
operation. A workflow may contain many tasks and operations. A task may be
completed through many executions.

## Request

A request is an expression of intent from one actor or system to another.

It says:

> Please do something, answer something, change something, or start something.

Requests are communication-level concepts. They are often created at boundaries:
terminal input, HTTP requests, channel messages, model tool-call requests, API
calls, or provider calls. A request may be informal natural language or a fully
structured payload.

Semantic properties:

- has a requester or source;
- has a target receiver, system, or surface;
- may be accepted, rejected, transformed, or ignored;
- may or may not create work;
- may be synchronous or asynchronous;
- often requires interpretation, validation, authorization, or routing before it
  becomes executable.

Examples:

- "Can you summarize this file?"
- `POST /runs`
- "Please deploy staging."
- A model tool-call request for `project_inventory`.
- A Slack message asking the agent to investigate an incident.

A request is not necessarily executable by itself. It is the ask, not the full
execution semantics.

## Task

A task is a unit of work to be completed.

It says:

> This is a piece of work with an objective.

A task is more work-shaped than a request. A request can create a task, but a
task may outlive the original request and may require several commands,
operations, workflow steps, or executions.

Semantic properties:

- has an objective or desired outcome;
- may have acceptance criteria;
- may declare required inputs and expected output artifacts;
- may be assigned to an actor, agent, or worker;
- may have lifecycle state such as draft, ready, running, blocked, completed,
  failed, cancelled, or interrupted;
- may be decomposed into DAG-shaped steps;
- may be part of a workflow or plan;
- may have produced artifacts and provenance back to a request, session, issue,
  ticket, or user.

Examples:

- "Fix the failing tests."
- "Implement workspace-scoped memory."
- "Review this pull request."
- "Migrate package X to the new API."

A task is not a tool call. It is the work objective. It may require many tool
calls and many executions.

In AgentRuntime, durable task state lives in `core/task` and is projected by
`runtime/task` from task event streams. The first bundled surface is
`plugins/taskplugin`, which contributes `/task`, `/plan`, narrow
task-creator/planner agents, and typed operations such as `task_create`,
grouped `task_modify`, `task_get`, `task_list`, artifact readers, and
`task_validate`. It also contributes scheduler controls such as `task_run`,
`task_scheduler_status`, and `task_scheduler_set_enabled`. Task creation
returns after recording the task; ready tasks can then be claimed and executed
by the orchestration task executor.

## Command

A command is an imperative instruction to perform a known action.

It says:

> Do this specific thing.

Commands are usually part of a control plane or user-facing syntax. They have a
known verb and structured arguments. A command should be parseable, routable,
validated, authorized, and dispatched to a handler.

Semantic properties:

- imperative;
- has a known command name or verb;
- usually has arguments, flags, or typed input;
- expects a known handler or dispatch target;
- can be rejected if unknown, malformed, unauthorized, or invalid;
- may create tasks, invoke operations, start workflow runs, or mutate session
  state;
- often originates from a request but is more concrete than the request.

Examples:

- `/help`
- `/compact`
- `/commit --message "..."`
- `git status`
- `kubectl rollout restart deployment/api`

A command is not the same thing as an operation. A command is usually
human/control-plane syntax and dispatch. An operation is a callable capability,
often model/tool-facing. A command handler may invoke one or more operations, but
commands should not be reduced to "tools with slashes" unless their semantics
really match.

## Workflow

A workflow is a structured process for coordinating work.

It says:

> Here is the process by which work should happen.

A workflow can be a reusable specification or a one-off process shape. It may
coordinate tasks, commands, operations, human approvals, retries, branches, and
other workflow steps.

Semantic properties:

- composed of steps;
- may include dependencies, conditions, loops, retries, fan-out/fan-in, or
  approval gates;
- may coordinate multiple tasks, commands, and operations;
- may have inputs and outputs;
- may have durable state across time;
- can be declarative as a spec or concrete as a run/execution.

Examples:

- CI pipeline: checkout -> install -> lint -> test -> build -> publish.
- Release workflow: update changelog -> tag -> publish -> announce.
- Agent coding workflow: inspect -> plan -> edit -> test -> summarize.
- Support workflow: classify ticket -> search docs -> draft answer -> request
  approval.

A workflow is not just a task. The task is the work objective; the workflow is
the process structure for completing work. One task can use many workflows over
time, and one workflow template can be used for many tasks.

A plan is not currently a separate core domain. In the task system, an approved
plan becomes the committed executable task step DAG (`task.Task.Steps`). Draft
or alternative plans should be represented as task drafts, artifacts, or future
workflow/plan-specific contracts if the product needs versioned alternatives.

## Operation

An operation is a callable capability exposed by the runtime, an adapter, or a
plugin.

It says:

> This named capability can be invoked with this input shape and returns this
> output shape.

Operations are usually model-facing or tool-facing. They are more atomic than a
workflow and more capability-oriented than a command. An operation spec describes
what can be called; an operation execution is one concrete invocation.

Semantic properties:

- has a stable name or reference;
- has an input schema or typed input contract;
- has an output/result contract;
- may include rendering metadata for model or UI consumption;
- may be contributed by plugins;
- may require policy and safety evaluation;
- may be pure or side-effecting;
- side-effecting operations must execute through the runtime safety and system
  boundaries.

Examples:

- `project_inventory`
- `project_files`
- `go_outline`
- `web_search`
- `file_edit`
- `memory_store`

An operation is not the same thing as its execution. The operation spec is the
capability contract. The execution is the runtime attempt to invoke that
capability with a particular input.

## Execution

An execution is an actual runtime attempt to carry something out.

It says:

> This thing is running, ran, failed, completed, timed out, or was cancelled.

Execution is runtime state. It can apply to operations, commands, workflows,
processes, model requests, tool calls, or session loops.

Semantic properties:

- has a runtime identity or correlation handle;
- has start and end time;
- has status such as running, succeeded, failed, cancelled, or timed out;
- has input and result/error metadata;
- may have logs, events, traces, artifacts, or diagnostics;
- may consume resources;
- may produce side effects;
- may be retried, resumed, or superseded depending on the runtime.

Examples:

- one invocation of `project_inventory`;
- one run of a workflow;
- one dispatch of `/commit`;
- one managed process run;
- one model completion request;
- one CI job attempt.

Execution should not be used as a synonym for a spec, task, workflow, command,
or operation. It is the runtime fact that something was attempted.

## Relationship examples

### Simple question

```text
Request:  "What is 2 + 2?"
Task:     Answer the question, often implicit.
Workflow: None or implicit.
Command:  None.
Operation: Maybe none.
Execution: Model response generation.
```

### Slash command

```text
Request:   User sends "/commit" in a terminal or channel.
Command:   Parsed command invocation for commit.
Task:      Create a commit for the current work.
Workflow:  Validate -> stage/select files -> create commit -> report result.
Operation: File/git/process operations, depending on implementation.
Execution: Concrete command dispatch and operation/process runs.
```

### Agent coding assignment

```text
Request:   "Fix memory scoping."
Task:      Implement the fix.
Workflow:  Inspect -> design -> edit -> test -> summarize.
Command:   Optional control commands such as /plan or /commit.
Operation: file_read, grep, file_edit, go test, project_inventory, etc.
Execution: Every concrete tool call, process run, and model turn.
```

### Tool call from a model

```text
Request:   Model asks to call `project_inventory`.
Task:      Understand project structure for the current work.
Workflow:  May be part of a larger agent workflow.
Command:   None.
Operation: `project_inventory`.
Execution: Runtime invocation of `project_inventory` with specific input.
```

## Mapping to AgentRuntime packages

AgentRuntime uses these concepts across layers. The package names and ownership
rules should reflect the semantic boundaries.

### Requests in AgentRuntime

There is intentionally no universal `core/request` package. Requests appear at
many boundaries:

- terminal or channel user input;
- HTTP/control API inputs;
- operation invocation inputs;
- context provider requests;
- datasource queries;
- model/provider requests.

Adapters and orchestration code should translate boundary requests into canonical
internal concepts such as command invocations, messages, operation inputs, or
workflow/session submissions. Adapters should avoid owning domain semantics that
belong to core, runtime, or orchestration layers.

### Tasks in AgentRuntime

`core/task` is the durable work-objective domain for first-class, trackable work
with lifecycle, assignment, acceptance criteria, artifact contracts, and
relationships to sessions, workspaces, projects, or workflows.

Task-like concepts may still be implicit in:

- user prompts;
- session goals;
- delegated worker assignments;
- plan steps;
- workflow steps.

When a task is represented explicitly, it means the work objective, not a tool
call, command, or operation execution. Runtime task state is inferred from task
events in the event store; there is no separate task database.

Automatic task execution is orchestration-owned. `runtime/task` projects state
and computes ready steps; `orchestration/taskexecutor` claims ready tasks and
dispatches workers through an execution backend while recording all progress as
task events. The scheduler can run automatically over ready tasks or be
controlled explicitly with task plugin operations. Because the scheduler writes
task streams concurrently with user/model task operations, scheduler transitions
must use event-stream conflict handling and expose retryable conflicts or
diagnostics rather than hiding background failures.

### Commands in AgentRuntime

`core/command` owns command syntax and invocation contracts. Commands are parsed
once at the command boundary and transported as structured invocations.

Commands should be:

- declared through specs/registries;
- dispatched by target kind and command metadata;
- validated by the owning backend/session dispatch layer;
- free from command-specific parsers in terminal UI code;
- implemented with typed inputs and binders rather than ad hoc maps spread across
  layers.

A command handler may invoke operations or start workflows, but the command
itself represents the parsed imperative control instruction.

### Workflows in AgentRuntime

`core/workflow` should contain inert workflow specifications and stable workflow
contracts. Runtime or orchestration packages should own workflow runs,
coordination, state transitions, retries, and execution.

A workflow spec may reference operations, commands, agents, approval gates, or
other workflow steps. The spec remains distinct from the run.

```text
workflow.Spec != workflow run/execution
```

### Operations in AgentRuntime

`core/operation` owns operation specs and model-facing operation contracts.
Operation implementations live in runtime, adapters, or plugins depending on the
responsibility and side-effect boundary.

Important distinctions:

```text
operation.Spec       = what capability exists
operation input      = request to invoke that capability
operation execution  = one concrete runtime attempt
operation result     = output/error/rendering from that attempt
```

Side-effecting operations must enter through `runtime/operation.SafetyEnvelope`
and use `runtime/system.System` for filesystem, network, process, browser, and
human-clarification access. Reusable plugins should not bypass those boundaries.

### Executions in AgentRuntime

Execution is primarily a runtime/orchestration concern. Different subsystems may
track different execution kinds:

- operation invocation/execution;
- workflow run;
- command dispatch;
- session/agent loop iteration;
- model provider request;
- managed process execution;
- background task/process handle.

A stable core execution contract should be added only when multiple runtime
surfaces need to exchange or persist the same execution record. Until then,
execution state should stay close to the runtime component that owns the actual
attempt.

## Vocabulary guidance

Prefer these names consistently:

- `Request` for boundary/API/provider inputs.
- `Invocation` for parsed calls to known command or operation surfaces.
- `Spec` for inert declarations.
- `Run` or `Execution` for runtime attempts.
- `Task` for durable work objectives with lifecycle.
- `Workflow` for structured multi-step process definitions and runs.
- `Operation` for plugin/model-facing callable capabilities.

Avoid using the terms interchangeably. In particular:

- do not call a task a tool call;
- do not put execution state into specs;
- do not treat every request as a command;
- do not treat commands and operations as the same concept;
- do not use workflow when a single operation or command is meant;
- do not introduce broad packages such as `core/execution`; new durable domains
  need crisp ownership, lifecycle, and cross-package value.

## Quick distinction checklist

When naming or placing a new concept, ask:

1. Is this an ask crossing a boundary? Use request language.
2. Is this the objective of work to be completed? Use task language.
3. Is this a parsed imperative with a known handler? Use command language.
4. Is this a multi-step process or template? Use workflow language.
5. Is this a callable capability/tool contract? Use operation language.
6. Is this one actual runtime attempt? Use run or execution language.

If more than one answer seems true, split the concept. For example, a user
request can produce a command invocation, which starts a workflow run, which
invokes operations, each of which has executions and results.
