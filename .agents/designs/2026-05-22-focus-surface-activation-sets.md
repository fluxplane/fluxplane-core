# Design: Focus, Surfaces, and Activation Sets

## Status

Brainstorm/design proposal for reducing large model-facing tool surfaces while
keeping broad app integrations usable in Coder and other Fluxplane apps.

This file is intended to be self-contained for implementation handoff. If a
session is lost, start with the "Implementation Slices" section near the end.
The recommended first slice is intentionally inert: add `core/activation` specs,
wire them into resource contributions/catalogs, and test composition without
changing runtime tool projection.

Current relevant code anchors:

- `core/operation.Set`: plugin/domain operation grouping.
- `core/tool.Set`: model-facing action-dispatch tool grouping.
- `core/reaction.Rule` and `core/reaction.Action`: assertion-to-action rules.
- `core/evidence.Observation` and `core/evidence.Assertion`: evidence and
  normalized matching claims.
- `core/resource.ContributionBundle`: normalized resource contribution shape.
- `orchestration/resourcecatalog`: broad resource catalog collection.
- `orchestration/appresources`: executable app resource binding.
- `orchestration/sessionenv.ActiveState`: session-local active resources.
- `orchestration/toolprojection`: operation/command/tool-set projection to
  `core/tool.Spec`.
- `runtime/context.Materializer`: context diff and stable block machinery.
- `adapters/ui/terminal.Renderer.renderRuntime`: terminal rendering of runtime
  events.

This design refines the earlier "generic tool projection" idea. The important
change is vocabulary: the model should not primarily be asked to "activate
tools". The model should declare or refine the current work focus. The engine
then deterministically prepares the relevant surface through assertions,
reactions, activation sets, policy, and projection.

The core invariant is:

```text
selected resources != prepared surface != projected model tools != execution
```

Selected resources are the static app/plugin/project graph. A prepared surface is
the currently active subset of that graph plus temporary context. Projected tools
are only one possible model-facing view of that surface. Execution remains a
separate runtime attempt behind policy and safety.

## Problem

Large apps such as Coder compose many plugins:

- local filesystem, shell, process, git, task, skills, datasource, memory;
- language and toolchain plugins;
- GitLab, Slack, Jira, Confluence, Kubernetes, Loki, MySQL, Docker, image,
  browser, web, and future Prometheus/Grafana integrations.

If every operation from every selected plugin is projected as an LLM tool, the
model-facing tool list becomes expensive and noisy:

- tool selection quality drops;
- prompt/token cost rises;
- provider-side schema handling becomes heavier;
- users lose confidence because irrelevant tools are always present;
- broad integrations make the model reach for the wrong action more often.

The engine already moved toward evidence-driven activation:

```text
observations -> assertions -> reactions -> active resources -> projected tools
```

That works for deterministic environment facts such as "Go project detected" or
"Loki endpoint available". It does not yet cover an important interactive case:
the user or model knows the current work requires a temporary group of systems
before the engine has an authored rule for that exact pattern.

Example:

```text
User:
hey, please check this slack thread: <URL>. troubleshoot the issue Sergey from
SRE posted. high load on backend or so.
```

The model should be able to say, in a structured way:

```json
{
  "objective": "Troubleshoot the Slack thread about backend high load.",
  "intents": ["troubleshoot", "incident_investigation"],
  "subjects": ["slack", "sre", "sergey", "backend", "prometheus", "grafana"]
}
```

That declaration is not a direct tool activation command. It is work focus
evidence. The engine can derive assertions from it, run reaction rules, prepare
Slack/observability/troubleshooting resources, and project a smaller
deterministic surface.

## Existing Vocabulary Review

This repo already uses the word `capability` heavily:

- `core/llm.Capability` describes model/provider features such as tool calling,
  reasoning, vision, prompt caching, and web search.
- `core/datasource.EntityCapability` describes datasource entity actions such as
  search, list, get, relation, index, and semantic search.
- `core/language.Capability` and `ToolchainCapability` describe language and
  toolchain support.
- `core/evidence.SubjectCapability` classifies assertion subjects.
- `docs/concepts.md` describes an operation as a callable capability.

Because of that, this design should not introduce a broad `CapabilitySet`
concept for temporary tool/context preparation. It would collide with existing
domain meanings and blur operation semantics.

Relevant existing terms:

- **Request**: a boundary ask from a user, channel, model, or API.
- **Task**: a work objective with lifecycle.
- **Command**: known imperative control instruction such as `/compact`.
- **Workflow**: structured process shape.
- **Operation**: callable contract with typed input/output.
- **Execution**: one runtime attempt.
- **Evidence Observation**: rich runtime knowledge.
- **Evidence Assertion**: normalized claim used for matching and activation.
- **Reaction**: deterministic mapping from assertions to inert actions.
- **Operation Set**: related atomic operations, usually plugin-local.
- **Tool Set**: model-facing projection that can collapse several targets into
  one dispatching tool.
- **Distribution Surface**: launch/deploy surfaces such as CLI, REPL, one-shot,
  and serve. This term already exists in `core/distribution.Surfaces`.

The missing terms are:

- what the model declares about the current work;
- what the engine prepares for that work;
- how plugins/apps/users name reusable bundles of things to prepare;
- how reflection proposes better future bundles and rules.

## Proposed Vocabulary

### Focus

Focus is a structured declaration of what the current work appears to be about.

It says:

> This is the task intent, topic, system, source, or investigation area I am
> currently focusing on.

Focus can come from:

- user natural language;
- an explicit command such as `/focus incident backend high-load`;
- an LLM tool call such as `session_focus`;
- a URL/parser/observer that recognizes a Slack thread, Jira issue, GitLab MR,
  Loki query, or similar source;
- reflection-derived future rules.

Focus is not an execution request. It is evidence. Runtime should record it as
an observation and derive assertions from it.

Focus is also not hidden model reasoning. It should be a short, auditable
working summary that the user could reasonably inspect. Do not ask providers to
emit chain-of-thought fields such as `thought`; use task-oriented fields such as
`objective`, `intents`, `subjects`, `sources`, and short `rationale` when
needed.

Example focus declaration:

```json
{
  "objective": "Troubleshoot backend high load from a Slack thread.",
  "intents": ["troubleshoot", "incident_investigation"],
  "subjects": [
    {"kind": "integration", "name": "slack"},
    {"kind": "team", "name": "sre"},
    {"kind": "person", "name": "sergey"},
    {"kind": "service", "name": "backend"},
    {"kind": "observability", "name": "prometheus"},
    {"kind": "observability", "name": "grafana"}
  ],
  "sources": [
    {"kind": "url", "value": "https://..."}
  ]
}
```

Derived assertions might include:

- `intent.requested` with subject `intent/troubleshoot`;
- `integration.requested` with subject `integration/slack`;
- `team.requested` with subject `team/sre`;
- `person.mentioned` with subject `person/sergey`;
- `service.requested` with subject `service/backend`;
- `observability.requested` with subject `observability/prometheus`;
- `url.detected` with subject `integration/slack` and metadata identifying a
  Slack thread URL.

Use "assertion" in code and docs, not "signal", because `core/evidence`
already owns the normalized matching currency. "Signal" can remain an informal
product word, but it should not become a new parallel core type.

### Surface

Surface is the prepared working interface for the current session/run/turn.

It says:

> Given current focus, user directives, rules, policy, auth, and environment,
> this is what the agent can see or call now.

A surface can include:

- projected tools;
- active operation refs or operation sets;
- commands;
- workflows;
- skills and skill references;
- context providers;
- datasource access;
- file/reference snippets;
- generated temporary context blocks;
- provider-visible schema details for generic calls.

Surface is not a static resource declaration. It is a runtime projection/read
model over selected resources plus active state and policy. Some parts are
model-visible, some are only enforcement state.

The term is deliberately narrower than `core/distribution.Surfaces`.
Distribution surfaces answer "how can this app be launched or reached?"
Prepared surfaces answer "what is available for this unit of work?" When writing
code, prefer qualified names such as `activation surface`, `prepared surface`,
or `session surface` where ambiguity is likely.

### Prepare

Prepare is a request to make a surface available.

It says:

> Please prepare the resources matching these terms, focus, or activation sets.

Prepare may be triggered by:

- a user command: `/activate slack prometheus jira`;
- imperative user speech: "load Slack, Prometheus, and Jira, then continue";
- model-declared focus when no direct rule exists;
- reaction actions from derived assertions;
- a workflow step that needs a bounded tool/context surface.

Prepare is not the same as execution. It changes active session state and
provider-visible context/tool projection. It still remains policy-gated.

Suggested LLM-facing tool name:

```text
surface_prepare
```

Suggested user/control command:

```text
/activate
```

The command can use expert language. The model-facing tool should use the
surface language so it does not leak engine internals as much.

### Activation Set

Activation set is an authored bundle of things that can be prepared together.

It says:

> When this named work surface is needed, activate or expose these resources
> together.

Activation sets can be contributed by:

- plugins, within their own scope;
- apps, across plugins;
- project/user resources under `.agents`;
- reflection-generated candidates accepted by the user.

Examples:

- `gitlab.mr_review`
- `slack.thread_investigation`
- `observability.prometheus_read`
- `incident.slack_prom_jira`
- `release.gitlab_jira_slack`
- `coder.local_editing`

Activation sets should be inert specs. They should not execute effectful work by
themselves.

### Reaction

Reaction remains the deterministic bridge from assertions to actions.

This design should reuse existing `core/reaction.Rule` semantics rather than
inventing a parallel rule engine.

New or extended actions can reference activation sets:

```yaml
reactions:
  - name: incident-slack-thread
    when:
      assertion: intent.requested
      subject:
        kind: intent
        name: troubleshoot
    actions:
      - kind: enable_activation_set
        activation_set: incident.slack_thread_troubleshoot
```

The rule says "this focus implies this set". The set says "these resources are
prepared together".

Implementation note: `core/reaction.Action` can carry an `ActivationSet string`
field initially. It does not need to import `core/activation` unless a typed ref
proves useful. Keep `core/activation` independent of `core/reaction`; activation
sets describe reusable bundles, while reactions describe one way to enable them.

### Reflection

Reflection is the review pass that proposes better future focus handling.

It says:

> Based on this session, what surfaces were useful, noisy, missing, or worth
> turning into future activation sets and reactions?

Reflection should produce candidates, not silently mutate behavior.

Candidates can include:

- new activation sets;
- changes to existing activation sets;
- new reaction rules;
- direct surface defaults for a session profile;
- operations or context providers to suppress because they were noisy.

This keeps adaptation explicit and reviewable.

## LLM-Facing Tool Names

Avoid `activation_info` as a model-facing name. It is engine-centric and
ambiguous. A model doing incident work should not need to reason about
"activation" as the primary action.

Recommended generic tool names:

```text
session_focus
surface_info
surface_prepare
surface_call
```

### `session_focus`

Declares or refines current work focus.

Input shape should avoid hidden reasoning fields. Do not use `thought`.

Use:

- `objective`;
- `intents`;
- `subjects`;
- `sources`;
- `rationale` or `summary`, if needed and short;
- `confidence`;
- `requested_surface`, when the user explicitly names systems to prepare.

Example:

```json
{
  "objective": "Troubleshoot backend high load from the provided Slack thread.",
  "intents": ["troubleshoot", "incident_investigation"],
  "subjects": [
    {"kind": "integration", "name": "slack"},
    {"kind": "service", "name": "backend"},
    {"kind": "observability", "name": "prometheus"}
  ],
  "requested_surface": ["slack", "prometheus", "jira"],
  "sources": [
    {"kind": "url", "value": "https://..."}
  ]
}
```

Runtime behavior:

- records focus as evidence;
- derives assertions;
- runs reactions;
- may prepare matching activation sets;
- returns a summary of changed surface state.

### `surface_info`

Inspects prepared and matching surfaces.

It answers:

- what is currently prepared;
- which activation sets match a query or focus;
- which systems are configured/authenticated;
- which operations are active/callable through `surface_call`;
- why a candidate is unavailable.

Example:

```json
{
  "query": "slack incident prometheus jira",
  "include_inactive": true
}
```

Runtime should return compact summaries, not full schemas by default.
For callable targets, return stable aliases and native refs that can be passed
to `surface_call`. Do not return provider-generated tool names as identifiers.

### `surface_prepare`

Requests preparation of named surfaces, systems, activation sets, or patterns.

Example:

```json
{
  "terms": ["slack", "prometheus", "jira"],
  "objective": "Investigate backend high load",
  "duration": "run"
}
```

This is useful when:

- the user explicitly asks to load systems;
- the model needs a surface but no authored reaction rule exists;
- the model wants to narrow broad focus into a concrete prepared surface.

`surface_prepare` should be policy-gated and explain partial preparation.

### `surface_call`

Calls an active operation-like target through the prepared surface.

Example:

```json
{
  "surface": "incident.slack_prom_jira",
  "operation": "loki_query",
  "data": {
    "query": "{app=\"backend\"} |= \"error\"",
    "since": "30m"
  }
}
```

`surface_call` should not execute arbitrary resources. In v1 it should resolve
only to operations. Later it can dispatch to commands or workflows if the engine
has a safe, structured invocation path for those target kinds.

Generated provider tool names remain projection output only. Activation sets and
surface calls should reference stable resource IDs or native refs, not generated
tool names.

The `operation` field may be either:

- a native operation ref such as `loki_query`;
- a canonical operation resource address when the catalog exposes one;
- a stable active-surface alias returned by `surface_info`.

If an alias is used, the response should echo the resolved operation ref so
auditing and reflection are based on real operations rather than aliases.

## User-Facing Commands

Add or refine command vocabulary separately from LLM tools:

```text
/focus incident backend high-load
/activate slack prometheus jira
/surface
/reflect
```

`/activate` is intentionally acceptable for expert users because it is a control
command. It should call the same internal preparation path as `surface_prepare`,
but with a stronger source:

```text
source = user_directive
```

Imperative natural language should be treated similarly:

```text
Please load Slack, Prometheus, and Jira, then continue.
```

The model can translate this into `session_focus` or `surface_prepare`, and the
engine marks the resulting request as user-directed when provenance shows the
terms came from user text.

## Concept Alignment

### Request vs Focus

A user message is a request. Focus is an interpreted structure about what the
request is about.

```text
request: "check this Slack thread and troubleshoot backend load"
focus: intents=[troubleshoot], subjects=[slack, backend, sre]
```

Focus should not replace requests.

### Task vs Focus

A task is durable work with objective/lifecycle. Focus is lightweight,
session-local evidence that helps prepare the right surface.

A request may create both:

- a task: "investigate backend high load";
- a focus declaration: "incident troubleshooting, Slack, Prometheus, backend".

### Command vs Prepare

`/activate` is a command. It invokes a handler that produces a prepare request
and applies active state changes.

The semantic validation and policy checks belong in session/orchestration, not
in terminal parsing.

### Workflow vs Activation Set

A workflow coordinates steps. An activation set prepares the resources that a
workflow, agent, or command may need.

Do not model incident troubleshooting as an activation set if it has step order,
branching, approvals, retries, or durable lifecycle. That is a workflow. The
activation set is the prepared surface that workflow uses.

### Operation Set vs Activation Set

`operation.Set` groups related operations, usually within a plugin or domain.

`activation.Set` groups resources to prepare for a work focus. It may include
operation sets, individual operation refs, skills, references, context
providers, datasources, and temporary context.

Example:

```text
operation.Set: slack.message_read
operation.Set: jira.issue_write
activation.Set: incident.slack_jira_triage
  -> enables slack.message_read + jira.issue_write + incident-response skill
```

### Tool Set vs Activation Set

`tool.Set` is about model-facing projection shape. It can collapse multiple
targets into one provider tool with an action discriminator.

`activation.Set` is about active working surface. It decides what should be
prepared, not exactly how the provider sees it.

Projection may later decide that active targets are:

- direct tools;
- one `surface_call` generic tool plus rendered schema context;
- one `tool.Set` dispatching tool;
- hidden from the model but available to a workflow.

### Reaction vs Activation Set

Reaction is a rule:

```text
if assertion matches -> apply actions
```

Activation set is a named bundle:

```text
incident.slack_prom_jira -> actions/resources to prepare
```

Reactions may enable activation sets. User commands and model prepare requests
may also enable activation sets. Rules are useful, but not required for manual
or model-requested preparation.

### Skill vs Activation Set

Skills already have activation semantics for guidance and references.

Activation sets can activate skills and skill references as part of a broader
surface. This is similar to "generic skills" in agent systems, but more
deterministic because the activation can also make exact operations and schemas
callable.

### Capability

Avoid using `capability` as the official new concept. Keep it only where it
already means model capability, datasource entity capability, language feature,
or the informal nature of an operation.

## Package Placement

Recommended v1 placement:

```text
core/activation
```

Why:

- The target may be an operation, command, workflow, skill, file/reference,
  context provider, datasource, or inline context.
- It is broader than `core/tool`, `core/operation`, and `core/reaction`.
- The spec is inert and can live in core without IO or execution.
- Runtime/orchestration can own active state and application.
- It avoids making reaction rules the only way to prepare a surface. User
  commands, model prepare requests, workflows, and future reflection candidates
  can all use the same inert set definition.

`core/activation` should contain:

- `Set`;
- `Target`;
- `TargetKind`;
- `Ref` or `SetRef`;
- validation helpers;
- possibly inert event payloads later if needed.

Do not put runtime activation state in core. State belongs in
`orchestration/sessionenv` or a runtime package if it becomes reusable outside
session orchestration.

Do not make plugins import app or session packages. Plugins contribute inert
activation sets through `resource.ContributionBundle`.

Avoid import cycles: `core/resource` will import `core/activation` once
contribution bundles carry activation sets, so `core/activation` must not import
`core/resource`. If a generic resource address is needed, use
`core/resourceaddr.Address` or native refs rather than `resource.ResourceID`.

Layer rules:

- `core/activation` may import other core concept packages only.
- `core/resource` may import `core/activation` for contribution bundles.
- `orchestration/resourcecatalog` and `orchestration/appresources` may collect
  and resolve activation sets.
- `orchestration/session` and `orchestration/sessionenv` may own active state,
  preparation application, and replay/explain behavior.
- `orchestration/toolprojection` may use active surface state to decide what is
  projected.
- `adapters/ui/terminal` may render client/runtime events, but must not own
  activation semantics.
- Plugins may contribute activation sets, but should not apply them directly.

## Resource Contribution Shape

Extend `core/resource.ContributionBundle`:

```go
ActivationSets []activation.Set `json:"activation_sets,omitempty"`
```

Initial target shape:

```go
type Set struct {
    Name        string
    Description string
    Aliases     []string
    Targets     []Target
    Annotations map[string]string
}

type TargetKind string

const (
    TargetOperation       TargetKind = "operation"
    TargetOperationSet    TargetKind = "operation_set"
    TargetCommand         TargetKind = "command"
    TargetWorkflow        TargetKind = "workflow"
    TargetSkill           TargetKind = "skill"
    TargetReference       TargetKind = "reference"
    TargetContextProvider TargetKind = "context_provider"
    TargetDatasource      TargetKind = "datasource"
    TargetResource        TargetKind = "resource"
    TargetInlineContext   TargetKind = "inline_context"
)

type Target struct {
    Kind            TargetKind
    Operation       operation.Ref
    OperationSet    string
    Command         command.Path
    Workflow        workflow.Name
    Skill           skill.Ref
    Reference       ReferenceTarget
    ContextProvider corecontext.ProviderRef
    Datasource      datasource.Ref
    ResourceAddr    resourceaddr.Address
    InlineContext   *ContextTarget
    Annotations     map[string]string
}
```

Validation rules for the inert spec:

- `Set.Name` must be non-empty after trimming.
- `Aliases` must be non-empty strings when present and must not duplicate the
  set name or each other.
- `Targets` must contain at least one target.
- each `Target` must have exactly one populated ref matching `Kind`;
- target refs are structurally validated only, not resolved in core;
- unsupported target kinds should fail validation in core once the enum is
  fixed, but orchestration may still diagnose unsupported-but-valid kinds when
  a future kind is declared before execution support exists.

Target refs should be authored with native identities where possible. Canonical
resource IDs are resolved later by orchestration/resource catalogs after bundles
are composed. Do not reference generated provider tool names in activation sets.
Inline context targets should contain static text or inert templates over known
session fields; dynamic IO belongs in context providers, observers, operations,
or datasources.

For v1 implementation, support only non-effectful preparation targets and
operation-like call targets:

- operation refs;
- operation sets;
- skills;
- skill references;
- context providers;
- datasources;
- inline/static context blocks.

Commands and workflows can be declared later once the invocation path is clear.
If included in v1 specs, they should be ignored with diagnostics unless
explicitly supported.

## Resolution Rules

Surface preparation resolves human/model terms into selected resources. This is
not stringly typed execution; resolution should be deterministic and explainable.

Resolution inputs:

- exact activation set names;
- activation set descriptions and annotations;
- plugin names and instances;
- operation set names;
- datasource names and entity types;
- context provider names;
- skill names and reference paths;
- observed focus assertions.

Resolution order should prefer stable exact matches before fuzzy matches:

1. exact activation set name;
2. exact resource name within an allowed target kind;
3. exact plugin or named instance;
4. explicit aliases declared on activation sets/resources;
5. ranked fuzzy/text match, returned as candidates when ambiguous.

Ambiguity should not silently activate a broad surface. If a term such as
`prom` matches Prometheus, Promtail, and a project named `prom`, return
candidates through `surface_info` or a clarification result. Exact user commands
may allow multiple explicit terms, but each term should still resolve
predictably.

All resolution happens against selected resources. `surface_prepare` cannot load
a plugin or operation that the app/resource graph did not select.

## Activation Sources and Authority

Track the source of preparation requests because authority differs.

Sources:

- `reaction`: deterministic rule from assertions;
- `user_command`: `/activate`;
- `user_directive`: imperative natural language from the user;
- `model_focus`: model declared focus;
- `model_prepare`: model requested surface preparation;
- `workflow`: workflow step requested preparation;
- `reflection_candidate`: not active until accepted.

Policy should be able to distinguish these.

Suggested ordering from strongest to weakest:

1. user command;
2. user directive;
3. workflow owned by an authorized session;
4. reaction from trusted assertions;
5. model prepare request;
6. model focus declaration.

Even strong sources do not bypass security. They only improve confidence that
preparation was requested intentionally.

Default policy should be conservative:

- `model_focus` may prepare read-only or context-only targets by default;
- `model_prepare` may request broader targets but should receive diagnostics or
  approval requirements for write/destructive operations;
- `user_command` and `user_directive` may prepare write-capable surfaces, but
  individual calls still require operation authorization and safety approval;
- reaction rules can opt into write-capable activation sets only when the rule
  is authored by a trusted resource source.

## Active State Model

The active state should distinguish why something is active from what became
visible.

Suggested runtime records:

- active activation sets with source, lifetime, and resolved target IDs;
- active operation refs and operation-set names;
- active context providers and datasources;
- active skills/references;
- inline context blocks with stable IDs;
- diagnostics for requested targets that were unavailable or denied.

Do not store only a flat list of tools. Tools are projection output and may
change by provider, policy, grouping, and collision handling. Store native refs
and resolved resource addresses where available, then project tools/context from
that state.

Run-scoped state can live in session orchestration memory during the inbound
loop. Persisted events should capture enough provenance for `/env/explain`,
conversation replay, and `/reflect`, but should not make every short-lived
activation permanently active on replay.

## UI And Frontend Trace

Focus detection and surface preparation must be visible to users. Otherwise the
agent will appear to "magically" gain or lose tools, and users will not know
whether the right systems were prepared.

The UI path should be structured and surface-neutral:

```text
session/orchestration
  -> runtime/domain events
  -> session.runtime.emitted
  -> orchestration/client.EventRuntimeEmitted
  -> terminal renderer / future app frontend
```

Do not create a terminal-only side channel. Terminal rendering should be one
consumer of the same events that a web app, Slack adapter, debug panel, or
reflection pass can consume.

### Trace Events

Add activation/focus events as core event payloads, likely in
`core/activation`, or in a small adjacent package if the final placement changes.

Suggested events:

- `focus.detected`
  - emitted when user input, model `session_focus`, URL detection, or a command
    produces focus evidence;
  - includes objective, intents, subjects, sources, source authority, and
    confidence;
  - links to produced observation/assertion IDs where available.
- `surface.prepare_requested`
  - emitted for `/activate`, imperative user directives, `surface_prepare`,
    reactions, or workflows;
  - includes terms, requested activation sets, requested lifetime, source, and
    provenance.
- `surface.resolved`
  - emitted after terms are resolved to activation sets/resources;
  - includes matched sets, matched direct resources, ambiguous candidates, and
    unmatched terms.
- `surface.prepared`
  - emitted after policy and active-state application;
  - includes activated sets, active operations/operation sets, context providers,
    datasources, skills/references, inline context blocks, lifetime, and
    diagnostics.
- `surface.prepare_skipped`
  - emitted when a candidate is not activated;
  - includes reason such as ambiguous, not selected, unauthorized, risk too high,
    approval required, unavailable instance, or unsupported target kind.
- `surface.expired`
  - emitted when run/turn-scoped activation is removed;
  - includes removed sets/resources and whether context removal diffs were
    rendered.

These events should use stable names and simple JSON fields. They should not
embed provider-specific tool schemas. Large schema details belong in context
blocks and resource catalogs; trace events should carry compact references and
summaries.

### Terminal Rendering

The terminal renderer should display a concise trace on stderr, similar to how
operation starts/completions and authorization/approval events are rendered
today.

Example:

```text
focus: troubleshoot incident [slack, backend, prometheus]
  from user text, confidence=0.86
surface requested: slack prometheus jira duration=run source=user_directive
surface prepared: incident.slack_prom_jira
  operations: slack_thread_read, loki_query, jira_issue_create
  context: incident-response, sre-runbooks
  skipped: prometheus.query unavailable (plugin not selected)
```

Rendering rules:

- Keep the default output compact; one to three lines per preparation is enough.
- Render focus before preparation when both happen in the same run.
- Group by run ID so follow-up tool loops do not interleave confusing traces.
- Show why: source, matched assertion/rule, command, or model request.
- Show what changed: activated sets/resources and notable skipped candidates.
- Hide full schemas, secrets, auth headers, and large resource lists.
- In debug mode, render full event JSON through existing debug rendering.

Terminal implementation should extend `adapters/ui/terminal.Renderer.renderRuntime`
with type-specific cases for the new focus/surface events. Unknown events should
still remain visible through debug mode and should not break rendering.

### Frontend Read Model

Future app frontends should not have to reconstruct activation state from raw
terminal strings. Provide a small surface trace/read model that can be built
from the same events:

- current focus summary;
- active activation sets and lifetimes;
- active callable operation aliases/refs;
- active context providers, datasources, skills/references;
- preparation provenance: user command, model focus, reaction rule, workflow;
- diagnostics and skipped candidates;
- recent activation timeline for the current run/session.

This read model can back:

- a terminal `/surface` command;
- a web sidebar showing "prepared for this run";
- a debug inspector similar to `/env/explain`;
- reflection prompts that ask which prepared resources were useful or noisy.

### Explainability Commands

`/env/explain` already reports evidence, assertions, reaction matches, active
operation sets, datasources, and context providers. Surface work should extend
that explainability rather than creating a separate opaque debug path.

Add:

- `/surface`: concise current prepared surface and recent preparation trace;
- `/surface --json`: structured current surface read model;
- `/env/explain`: include focus assertions, activation-set matches, surface
  prepare requests, skipped candidates, and active surface state;
- `/reflect`: consume the same trace to propose future activation sets and
  reaction rules.

### User Experience Invariants

The user should be able to answer:

- What intent did the system detect?
- Which activation sets or resources matched that intent?
- What was actually prepared?
- What was skipped, and why?
- Which prepared operations are callable through `surface_call`?
- Which context blocks were rendered because of this preparation?
- When will this activation expire?

If the terminal cannot answer those questions compactly, the design will be hard
to trust even if the projection mechanics are correct.

## Activation Lifetime

Default lifetime should be short:

```text
duration = run
```

Meaning:

- active for the current inbound run and its model/tool follow-up loop;
- expires before the next user input unless explicitly configured otherwise;
- rendered context uses stable block IDs and normal context diff records;
- previous transcript content is not rewritten.

Supported lifetime values can start small:

- `turn`: current model step only;
- `run`: current inbound run, recommended default;
- `session`: persists until deactivated or session reset, opt-in only.

Avoid conversation-persistent activation as the default. It solves repeated
activation at the cost of stale authority and growing surface size.

## Surface Projection Flow

Recommended flow:

```text
user request
  -> model/user focus declaration
  -> evidence observation
  -> derived assertions
  -> reactions
  -> activation sets
  -> active surface state
  -> tool projection + context materialization
  -> model step
```

Manual flow:

```text
/activate slack prometheus jira
  -> prepare request source=user_command
  -> resolve terms to activation sets/resources
  -> policy filter
  -> active surface state
  -> context/tool diff
  -> ask model to continue
```

No-rules flow:

```text
surface_prepare terms=["slack", "prometheus", "jira"]
  -> resolver finds matching plugins, datasources, operation sets, activation sets
  -> active surface state
```

Rules improve precision, but the system remains useful without them.

## Generic Surface Projection

Keep direct tools for high-frequency local work. Use generic surface tools for
large or situational integrations.

Direct baseline examples:

- project inventory/files/tasks/docs;
- file read/edit/stat/create;
- grep/glob/dir tree;
- git status/diff/add/commit where configured;
- shell/process/code execution where configured;
- task operations;
- datasource search/list/get;
- memory retrieval.

Generic tools:

- `session_focus`;
- `surface_info`;
- `surface_prepare`;
- `surface_call`.

When a surface is active, detailed operation schemas should be rendered by a
context provider rather than projected as dozens of provider tool definitions.
Use stable block IDs such as:

```text
surface/schema/incident.slack_prom_jira/loki_query
surface/schema/gitlab.mr_review/gitlab_mr_get
```

This uses the existing context materializer and diff machinery:

- unchanged schema blocks fingerprint-skip;
- changed schemas render as updates;
- expired activation renders removals;
- prior transcript content remains immutable.

Schema rendering should be bounded:

- sort active targets deterministically;
- render only active targets, not all selected resources;
- cap the number of schemas and total estimated tokens;
- include input schema, short description, risk/effect summary, and call alias;
- omit bulky output schemas unless they materially help the model;
- redact secrets and auth details;
- place schema blocks in developer context by default so they are distinct from
  user content.

The surface schema provider should remain mounted whenever generic surface tools
are available. That lets it emit removal diffs when run-scoped activations
expire instead of leaving stale schema blocks without an explicit removal.

## `surface_call` Enforcement

`surface_call` must resolve to a real operation binding and then use normal
operation execution.

Checks:

- target exists in selected resources;
- target is active in current surface;
- caller/trust is allowed;
- session projection policy allows it;
- authorization policy allows it;
- operation risk and side effects are allowed;
- approval requirement is satisfied or surfaced;
- named plugin instance is available;
- execution still enters `runtime/operation.SafetyEnvelope`.

Failure identity should be explicit:

- `surface_not_active`;
- `surface_target_not_found`;
- `surface_target_not_callable`;
- `operation_not_authorized`;
- `operation_not_projected`;
- `operation_risk_too_high`;
- `approval_required`;
- `named_plugin_instance_unavailable`.

## Reflection Loop

Reflection can turn usage patterns into candidate improvements.

Inputs:

- focus declarations;
- prepare requests;
- active surfaces;
- operations actually called;
- operations repeatedly searched for but missing;
- tool errors and rejections;
- oversized or noisy results;
- user corrections;
- successful call chains;
- context blocks that were used before useful calls;
- context blocks that churned or seemed irrelevant.

Outputs should be candidates:

```yaml
candidate_activation_sets:
  - name: incident.slack_prom_jira
    reason: Slack, Prometheus, Loki, and Jira were prepared together in several incident sessions.
    targets:
      - operation_set: slack.message_read
      - operation_set: prometheus.query
      - operation_set: loki
      - operation_set: jira.issue_write
      - skill: incident-response

candidate_reactions:
  - name: focus-incident-slack-observability
    reason: This combination matched useful incident sessions.
    when:
      assertions:
        - intent.requested: troubleshoot
        - integration.requested: slack
        - observability.requested: prometheus
    activate:
      - activation_set: incident.slack_prom_jira
```

Candidates must be accepted or edited by the user before they become resources.
This keeps self-improvement auditable.

## Example: Slack Incident Troubleshooting

User:

```text
hey, please check this slack thread: <URL> - troubleshoot the issue Sergey from
SRE posted. high load on backend or so.
```

Model:

```json
session_focus({
  "objective": "Troubleshoot backend high load reported in the Slack thread.",
  "intents": ["troubleshoot", "incident_investigation"],
  "subjects": [
    {"kind": "integration", "name": "slack"},
    {"kind": "team", "name": "sre"},
    {"kind": "person", "name": "sergey"},
    {"kind": "service", "name": "backend"},
    {"kind": "observability", "name": "prometheus"}
  ],
  "sources": [{"kind": "url", "value": "<URL>"}]
})
```

Engine:

- records focus observation;
- derives assertions;
- matches `intent.troubleshoot + integration.slack`;
- enables `incident.slack_thread_troubleshoot`;
- detects/prompts for Prometheus/Grafana/Loki surface if configured;
- renders schema/context diffs;
- returns active surface summary.

Model:

```json
surface_call({
  "surface": "incident.slack_thread_troubleshoot",
  "operation": "slack_thread_read",
  "data": {"url": "<URL>"}
})
```

Then:

- read thread;
- identify service, timeframe, symptoms;
- query logs/metrics using active observability surface;
- create or update Jira if needed;
- summarize findings in Slack if authorized.

## Example: Manual Activation

User:

```text
/activate slack prometheus jira
```

Engine:

- resolves `slack`, `prometheus`, and `jira` against activation sets, plugins,
  datasources, operation sets, and context providers;
- applies policy;
- activates matching resources for current run or session command follow-up;
- reports what changed.

Then the user can continue:

```text
now investigate the backend load issue from this thread
```

The model starts with a prepared surface instead of needing to infer everything
from scratch.

## Implementation Slices

Implement this in slices. Do not start by changing Coder's default tool surface;
that is too broad and makes regressions hard to isolate.

### Slice 1: Inert Activation Sets

Goal:

Add the reusable resource vocabulary without changing runtime behavior.

Scope:

- add `core/activation` with `Set`, `Target`, `TargetKind`, optional `Ref`, and
  validation;
- add `ActivationSets []activation.Set` to `core/resource.ContributionBundle`;
- clone/append activation sets in contribution bundle merge helpers;
- collect activation sets in the resource catalog and app resources path;
- expose enough catalog data for tests and future `/surface` work;
- add a few example activation sets in tests only.

Do not implement yet:

- `/activate`;
- `session_focus`;
- `surface_*` model tools;
- terminal rendering;
- Coder default projection changes;
- active state changes;
- context schema rendering.

Acceptance:

- existing tests keep passing;
- zero runtime behavior changes;
- plugin/app bundles can contribute activation sets;
- duplicate/invalid activation set specs produce deterministic diagnostics or
  validation errors in the same style as operation/tool set collection;
- architecture test has zero violations.

Suggested focused checks:

```bash
go test ./core/activation ./core/resource ./orchestration/resourcecatalog ./orchestration/appresources
go test ./internal/architecture
```

### Slice 2: Trace Events And Read Model

Goal:

Make focus/surface activity observable before implementing broad preparation.

Scope:

- add focus/surface event payloads;
- add a small surface trace/read model over runtime events;
- render focus/surface runtime events in terminal;
- add `/surface` read-only inspection over current/replayed trace when enough
  state exists.

Do not implement model-facing `surface_prepare` or Coder projection changes yet.

### Slice 3: User-Directed Preparation

Goal:

Support explicit user preparation without model inference.

Scope:

- implement `/activate`;
- resolve terms to activation sets/resources;
- apply run-scoped active surface state;
- emit trace events and diagnostics;
- extend `/env/explain`.

### Slice 4: Model-Facing Surface Tools

Goal:

Allow the model to declare focus and call active operation targets.

Scope:

- implement `session_focus`;
- implement `surface_info`;
- implement `surface_prepare`;
- implement `surface_call` for operation targets only;
- add active-surface schema context provider.

### Slice 5: Coder Migration And Reflection

Goal:

Use surfaces to reduce Coder's large integration tool footprint.

Scope:

- keep high-frequency local coding tools direct;
- move bulky integrations behind activation sets and generic surface tools;
- add curated activation sets for incident troubleshooting, MR review, release
  support, and remote project investigation;
- extend `/reflect` to propose candidate activation sets and reaction rules.

## Handoff Recommendation

Start with Slice 1 only. It is the minimum safe foundation and should not alter
runtime behavior, model-visible tools, terminal output, or Coder defaults. After
Slice 1 lands, re-run a short design review against the actual code before
starting event/read-model work in Slice 2.

## Tests and Acceptance

Core tests:

- validate activation set names and targets;
- reject unsupported empty targets;
- serialize and collect activation set contributions;
- avoid `capability` terminology in public new types.

Reaction/session tests:

- focus assertions can enable activation sets through reactions;
- `/activate` prepares surfaces without authored rules;
- run-scoped activation expires before next user input;
- active context provider renders stable diff blocks;
- deactivation/expiry emits removals rather than rewriting history;
- focus and surface events include enough provenance to explain why preparation
  happened.

Projection tests:

- baseline tool projection remains unchanged without config;
- hybrid mode projects direct local tools plus generic surface tools;
- inactive surface operations are not callable;
- active surface operations are callable through `surface_call`;
- authorization/risk/approval checks still apply.

Coder tests:

- default projected tool count drops materially;
- local coding tools remain directly available;
- integration surfaces are discoverable through `surface_info`;
- incident/troubleshooting surfaces can activate Slack, observability, and Jira
  together.

Reflection tests:

- reflection produces candidate activation sets from observed usage;
- candidates are not auto-applied;
- accepted candidates can be loaded as project/user resources.

Terminal/frontend tests:

- terminal renders compact focus and surface preparation lines from runtime
  events;
- skipped candidates render with reason but without full schemas or secrets;
- `/surface --json` returns the same active surface read model used by frontend
  consumers;
- debug rendering still exposes full event JSON for unknown or new event types.

## Open Questions

- Should `surface_call` accept only operation refs in v1, or also command
  invocations once command policy is fully projected?
- Should session focus be persisted as a dedicated event type or represented as
  a normal evidence observation first?
- Should `/activate` default to `run` lifetime or support `session` with an
  explicit flag such as `/activate --session slack`?
- Should activation set resolution prefer exact names over plugin names when a
  term matches both?
- Should reflection write candidate resources under `.agents/reviews`,
  `.agents/plans`, or a new `.agents/candidates` path?
- Should focus/surface events live directly in `core/activation`, or should
  focus get a tiny `core/focus` package if it grows beyond activation?
- Should terminal focus/surface rendering be always-on, or controlled by a
  verbosity flag once users have stable `/surface` inspection?

## Recommendation

Adopt the following terms:

- **Focus** for model/user-declared work intent.
- **Surface** for prepared visible/callable context and tools.
- **Prepare** for requests to make a surface available.
- **Activation Set** for authored reusable bundles of resources to prepare.
- **Reaction** for deterministic assertion-to-activation mapping.
- **Reflection** for review-derived candidate sets and rules.

Use these LLM-facing tools:

- `session_focus`
- `surface_info`
- `surface_prepare`
- `surface_call`

Use this user command:

- `/activate`

Avoid introducing `CapabilitySet` or `capability_call` as official names for
this feature. The underlying implementation may still prepare callable
operations, but the concept is broader than operations and clearer as surface
preparation.
