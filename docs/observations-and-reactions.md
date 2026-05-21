# Observations And Reactions

This document defines the observation and reaction model for AgentRuntime. It
generalizes project signals, toolchain availability, skill triggers, and
integration checks such as Kubernetes or AWS availability.

## Summary

The model separates detecting facts from reacting to those facts:

```text
Observer -> Observation -> Signal -> Reaction Rule -> Action
```

- An **observer** is configured code that is allowed to inspect one boundary.
- An **observation** is a non-secret fact the observer found.
- A **signal** is a normalized activation hint derived from observations.
- A **reaction rule** maps a signal to one or more actions.
- An **action** updates session state, context, or invokes configured runtime
  behavior.

Project inventory remains a rich domain result, but it should no longer be the
only activation currency. It becomes one observation source among several.

The design goal is to make ambient facts first-class without making the runtime
magical. Configuration decides what may observe, observers produce facts,
derivers normalize those facts into signals, and reaction rules decide what to
do with signal changes.

## Core Concepts

### Observer

An observer declares what it can inspect. Observers do not exist globally just
because the runtime knows how to probe something. They are available only when
the owning runtime or plugin has been configured.

Examples:

- the baseline runtime observer can report time, locale, current user, and
  workspace summary;
- the project observer can scan workspace roots and produce project inventory;
- the Go observer can probe configured toolchains;
- the Kubernetes plugin can contribute a Kubernetes context observer;
- the AWS plugin can contribute an AWS environment and credentials-presence
  observer.

The important rule is:

```text
configured plugin/resource -> observer exists -> observation may run
```

not:

```text
probe host -> discover plugin should be loaded
```

The app or distribution still selects plugins. Observations and reactions do not
replace plugin selection; they replace the plugin-specific conditional activation
logic that currently sits above selected plugins.

### Observation

An observation is the canonical fact shape. It should be represented with
`core/environment.Observation` and kept non-secret. For sensitive integrations,
the observation may say that credentials are available or verified, but must not
include token, key, cookie, or kubeconfig material.

Examples:

```yaml
kind: system.locale
content:
  timezone: Europe/Berlin
  locale: en_US.UTF-8
```

```yaml
kind: kubernetes.context
source: plugin:kubernetes
content:
  context: k3d-ai
  namespace: ai-bots
```

```yaml
kind: toolchain.status
content:
  id: go
  available: true
  version: go1.24
```

Observations may be dynamic. A Kubernetes context, current AWS profile, or
credential availability can change between turns, so providers that render them
should mark their blocks as dynamic.

An observation should carry enough identity to be useful without exposing secret
material:

- `Kind`: stable fact type such as `system.locale`, `project.inventory`,
  `toolchain.status`, or `kubernetes.context`;
- `Source`: observer identity such as `runtime:baseline`, `plugin:kubernetes`,
  or `operation:project_inventory`;
- `Environment`: the boundary the fact belongs to, such as local session,
  workspace, or external integration;
- `Scope`: the place the fact is valid, such as process, workspace root,
  session, plugin instance, or integration account;
- `Content`: bounded structured data;
- `Metadata`: non-secret identifiers for matching, diagnostics, and display;
- `At`: observation time.

Observers should prefer stable observation IDs when a fact has durable identity,
for example `toolchain:go`, `integration:kubernetes:default`, or
`workspace:primary`. Transient observations can omit IDs.

### Signal

A signal is the compact matching surface for activation and reactions. It is
derived from one or more observations.

Examples:

```yaml
kind: language.detected
target: go
confidence: 1
```

```yaml
kind: toolchain.available
target: go
confidence: 1
```

```yaml
kind: integration.available
target: kubernetes
confidence: 1
```

Signals are intentionally less detailed than observations. Context providers can
use observations; activation and reaction matching should normally use signals.

A signal should have a stable activation key:

```text
kind + target + scope + source
```

The key is used for de-duplication and for deciding when a signal has newly
appeared, changed, or disappeared. Signal metadata can include the originating
observation IDs, but reaction rules should not need to inspect raw observation
content for common cases.

### Scope

Environment facts need scope because the same runtime can talk to multiple
workspaces, clusters, accounts, or channel sessions. Scope is not just metadata;
it is part of matching and de-duplication.

Examples:

```text
workspace:/home/example/projects/ai/agents/slack-bot
session:01J...
integration:kubernetes:context/k3d-ai:namespace/ai-bots
integration:aws:profile=dev:region=us-east-1
```

Reaction rules should default to the current session/workspace scope.
Cross-scope reactions must opt in explicitly so a signal from one workspace does
not enable tools, references, or workflows in another.

### Reaction

A reaction rule is configuration that says what to do when a signal appears.

Low-risk reactions can run automatically:

- activate a skill;
- activate a skill reference;
- enable an operation set or datasource visibility;
- enable a context provider so selected context can render for the session.

Effectful reactions are policy and approval gated:

- run a workflow;
- invoke an operation;
- execute a command;
- start background work.

Normal operation authorization still applies. A reaction never bypasses the
runtime safety envelope.

Reactions should default to edge-triggered semantics:

```text
fire when a matching signal appears or changes, not every time it is still true
```

Rules may later opt into `every_turn`, but the default should be
`on_change`. Each applied reaction should record an idempotency key derived from
the rule name, signal key, signal fingerprint, and action index. This prevents a
long-lived signal such as "Go project detected" from activating the same
reference repeatedly.

Reaction actions should run in declaration order. If one action fails, remaining
actions in that rule should be skipped by default and a diagnostic should be
recorded. A future per-rule `error_policy: continue` can relax that.

## Observation Phases

Not every observer should run at the same time. The runtime should support a
small number of observation phases:

- **startup**: cheap app/runtime facts that rarely change during a process;
- **session_open**: user, workspace, configured plugin, and initial integration
  metadata;
- **turn**: dynamic facts that may change between user turns, such as current
  Kubernetes context;
- **tool_followup**: observations produced by operations or effects in the prior
  turn;
- **lazy**: expensive or remote checks run only when a provider, reaction, or
  operation asks for them.

Baseline runtime observers should stick to startup/session/turn facts that are
cheap and non-secret. Plugin observers choose their phase through their observer
spec. A Kubernetes context observer can be turn-scoped; a cluster inventory
observer should usually be lazy.

## Observation Sets And History

Each turn should build an observation set:

```text
prior stable observations + new phase observations + operation-result observations
```

The active set is the input to signal derivation and context providers. The
runtime should also keep enough previous signal state to compute:

- newly appeared signals;
- changed signals;
- disappeared signals.

Most reaction rules fire on appeared or changed signals. Disappeared signals are
useful for context cleanup and future deactivate-style actions, but should not
run effectful work by default.

Persistence should be bounded. Thread history should record important observation
and reaction events, not every raw probe result forever. A practical split:

- persist signal changes and reaction applications;
- persist observations only when they were rendered, caused a reaction, or came
  from an operation/effect result;
- keep cheap baseline observations in turn-local memory unless a context block
  or reaction needs provenance.

Absence should usually be represented as signal disappearance, not as a
permanent negative signal. For diagnostics, observers may report a bounded
negative observation such as `integration.unavailable` with a reason code, but
reaction rules should avoid treating temporary failures as durable truth unless
the observer declares the result stable.

Observations should also carry freshness. Context providers can decide whether a
stale observation is still useful for display; reaction matching should normally
ignore stale dynamic observations unless the rule explicitly matches staleness.

## Signal Derivation

Signal derivation should be explicit and testable. Observers produce
observations; separate derivation code maps observations to activation signals.

Examples:

```text
project.inventory with Go module facet
  -> language.detected target=go
  -> project.toolchain.hinted target=go

toolchain.status id=go available=true
  -> toolchain.available target=go

kubernetes.context with context set
  -> integration.configured target=kubernetes
  -> integration.available target=kubernetes
```

The same observation may produce multiple signals. Derivers should be registered
by runtime or plugin contribution, mirroring observers. If the Kubernetes plugin
is not enabled, no Kubernetes-specific deriver runs.

Derivers should stay pure: no filesystem, network, process, or connector calls.
They transform an observation set into a signal set. If more information is
needed, that is a lazy observer or operation, not hidden work inside derivation.

## Context Model

Observations should be available to context providers automatically through the
provider request. This is distinct from rendering them automatically.

Rules:

- orchestration passes the active turn observation set to context providers;
- providers decide what to render and placement;
- raw observations are not injected into system context by orchestration;
- providers may render selected observations into user or developer context
  when their own spec and behavior justify it;
- dynamic observation-backed blocks should use `FreshnessDynamic`.

This means a workspace summary provider, identity provider, Kubernetes context
provider, and AWS provider can all use the same observation input shape without
each inventing its own hidden session context.

## Data Flow

The intended flow for a session turn is:

```text
1. Determine configured observers from app/coder config and enabled plugins.
2. Run baseline and configured observers allowed for this phase.
3. Collect environment.Observation values.
4. Derive normalized signals from observations.
5. Evaluate reaction rules against the new signal set.
6. Apply activation/context reactions before context materialization.
7. Dispatch effectful reactions only through policy/approval-gated paths.
8. Pass observations into context providers automatically.
```

Context providers are the rendering boundary. Orchestration should not inject raw
observations into system context directly. Providers decide which observations
matter and whether to render them into user or developer context.

## Plugin Usage Model

Plugins keep the same lifecycle boundary they have today:

```text
app/distribution declares plugin refs
  -> pluginhost resolves selected plugins
  -> selected plugins contribute resources and runtime implementations
  -> observations/reactions decide what is active or visible in a session
```

The new model adds contribution kinds to selected plugins. It does not make
plugins self-loading.

A plugin uses the model in two layers.

At composition time, the plugin contributes inert declarations:

- observer specs, describing what facts it can observe and in which phase;
- signal-deriver specs, describing which observation kinds become which signal
  kinds;
- default reaction rules, such as activating its own context provider,
  operation set, datasource, skill, or reference when a matching signal appears.

At runtime, the same selected plugin may contribute executable implementations:

- observer handles, when observing needs filesystem, process, network, or
  integration access;
- signal deriver handles, mapping that plugin's observations to normalized
  signals;
- context providers that consume the shared observation set.

The plugin authoring contract is deliberately mechanical:

1. Keep `Contributions` as the static declaration surface. Put observer specs,
   signal-deriver specs, reaction rules, operation sets, context provider specs,
   datasource specs, skills, and references in the returned
   `resource.ContributionBundle`.
2. Implement `ObserverContributor` only when the observer needs executable IO.
   The observer must match an observer spec contributed by that selected plugin
   or by the runtime baseline.
3. Implement `SignalDeriverContributor` only when template derivation is not
   expressive enough. Most simple mappings should stay as inert
   `SignalDeriverSpec` templates.
4. Implement `ReactionContributor` only for instance-aware defaults, for
   example a rule that targets `plugin:kubernetes/local` rather than the generic
   Kubernetes plugin type.
5. Do not activate resources directly from plugin code. Contribute resources and
   default rules; orchestration applies the rule result to the session's active
   skill/reference/operation-set/datasource state.

This creates two resource states:

- **selected**: the app/plugin/resource graph contains the capability. Selected
  capabilities can be inspected, validated, and referenced by rules.
- **active**: a session may expose the selected capability to the model or render
  it in context. Active state is produced by explicit defaults or reactions.

Plugins should treat selected state as the installation boundary and active
state as the session boundary. A selected plugin can contribute ten operation
sets, but only the operation sets activated by product defaults or reaction
actions should become model-visible for the current scope.

The exact resolution order should be:

```text
app/distribution/global config selects plugin refs
  -> pluginhost instantiates selected plugin refs with their configured instance
  -> plugin.Contributions returns inert specs into resource.ContributionBundle
  -> optional ObserverContributor returns executable observers
  -> optional SignalDeriverContributor returns executable derivers
  -> optional ReactionContributor returns instance-aware default reaction rules
  -> orchestration merges these with app/workspace/session config
  -> session schedules only the resolved executable observers and derivers
```

This gives plugin authors a concrete rule: a plugin never asks the host to
"observe first so the plugin can decide whether to load." The plugin is selected
first. Observation then decides whether that selected plugin's contributed
capabilities should become active, visible, or rendered in a session.

Plugin dependencies should stay static. If a plugin's default reaction requires
another plugin-provided action or resource, that dependency should be declared in
composition metadata or surfaced as a diagnostic during resolution. It should not
be discovered by running an observer.

For example, a Kubernetes plugin can contribute:

```text
observer: kubernetes.context
  phase: turn
  observes: configured local kube context and namespace

deriver:
  kubernetes.context -> integration.configured target=kubernetes
  kubernetes.context with reachable API -> integration.available target=kubernetes

default reaction:
  when integration.available target=kubernetes
  enable_context_provider: kubernetes.context
  enable_datasource: kubernetes
  activate_reference: kubernetes/references/kubectl.md
```

That whole chain exists only if the Kubernetes plugin was selected by the
distribution, app manifest, or explicit config. If the plugin is not selected,
there is no Kubernetes observer, no Kubernetes deriver, and no Kubernetes default
reaction. The runtime should report an unknown observer/rule diagnostic if user
config names one anyway.

The Kubernetes implementation is the first concrete example of this shape:

```text
plugins:
  - kind: kubernetes
    instance: local
    config:
      namespaces: [ai-bots]

selected kubernetes plugin contributes:
  ObserverSpec: kubernetes.context
  Observer: reads configured kube context/namespace without exposing secrets
  SignalDeriverSpec: kubernetes.signals
  SignalDeriver: kubernetes.context -> integration.configured/available
```

The AWS plugin follows the same pattern for local AWS configuration:

```text
plugins:
  - kind: aws
    instance: dev

selected aws plugin contributes:
  ObserverSpec: aws.environment
  Observer: reads configured profile/region and credential presence without
    exposing access key, secret key, or session token values
  SignalDeriverSpec: aws.signals
  SignalDeriver: aws.environment -> integration.configured/available
```

Future Docker, GitHub, or cloud plugins should use the same structure: selected
plugin first, non-secret observation second, normalized signals third, reaction
rules last.

The pluginhost contract can grow by adding optional contributor interfaces next
to the existing operation, context provider, channel, connector, datasource,
auth, and identity contributors:

```text
ObserverContributor
SignalDeriverContributor
ReactionContributor
```

Inert declarations should live in `core/environment` and `core/reaction`.
Executable observer and deriver implementations should be resolved by
`orchestration/pluginhost` and scheduled by orchestration. A plugin contribution
bundle can carry observer specs and reaction rules for static inspection, while
the pluginhost resolution carries executable observer and deriver handles for a
live session.

The runtime should reject mismatches early. If a plugin contributes an executable
observer without a corresponding spec, or config names an observer whose owning
plugin is not selected, resolution should produce a diagnostic and skip that
observer. If a reaction action references an operation set, datasource, skill, or
reference that was not contributed by the selected app/plugin/resource graph, the
reaction should be skipped with a diagnostic rather than loading another plugin.

Plugin defaults should be ordinary named rules. That keeps overrides simple:

```text
plugin contributes rule "kubernetes.available.default-reference"
  -> distribution may keep it
  -> app config may disable it by name
  -> workspace config may add a narrower rule for a namespace
  -> session state records which rule/action actually applied
```

Rule names should be stable and namespaced by owner, for example
`plugin:kubernetes/default-reference` or
`app:slack-bot/merge-request-reference`. Disable/override semantics
should operate on those names, not on plugin internals.

## Replacing Conditional Loading

Today there are two different ideas that can sound like "conditional loading":

- **plugin selection**: app manifests and distributions decide which plugins are
  available at all;
- **conditional activation**: product code decides which contributed operations,
  operation sets, context, skills, or references become active based on project
  signals, toolchain status, text triggers, or similar facts.

The new model should replace conditional activation, not plugin selection. The
replacement is a session activation layer fed by reactions:

```text
selected resources in app composition
  -> inactive-by-default optional capabilities
  -> observations derive signals
  -> reactions move selected capabilities into active session state
  -> context materialization and operation registry read active session state
```

In other words, plugin loading remains a composition decision. The thing that
becomes conditional is whether already-selected capabilities are visible to the
model or rendered in context for a given scope.

Composition-time dependency inference is also separate from environment
observation. For example, launch currently adds the datasource plugin when a
resource bundle contains datasource specs. That is a static dependency of the
declared app shape, not a probe of the host environment. The refined model should
keep that kind of composition rule explicit, or replace it with plugin
dependency metadata, but it should not turn it into "observe environment, then
load datasource plugin."

Migration examples:

- migrated: `apps/coder.ActivationInput` no longer carries project signals or
  toolchain statuses;
- migrated: `runtime/language.SignalMatcher` no longer activates language
  operation sets from `core/project.Signal`;
- partially migrated: Coder contributes generic reactions for Go parser and
  Markdown operation sets, while broad static product operation selection still
  exists as the selected capability surface;
- migrated: Go toolchain availability has a real observer, deriver, and Coder
  reaction for the `golang.toolchain` operation set;
- migrated: phrase-based skill/reference activation now derives request signals
  from channel-message observations and activates through generated reaction
  rules;
- migrated: datasource detected-context rendering now reads provider request
  observations directly instead of a separate detector context side channel.

The replacement is not one generic "conditional load" switch and not a host
probe that pulls plugins in. It is a set of explicit state transitions:

```text
selected plugin/resource exists
  -> observer may produce observation
  -> deriver may produce normalized signal
  -> matching reaction may activate/enable a resource in the current scope
```

Examples:

```text
project.inventory sees go.mod
  -> language.detected target=go
  -> enable_operation_set: golang.parser

toolchain.status go available=true
  -> toolchain.available target=go
  -> enable_operation_set: golang.toolchain

channel.message mentions "load dex skill"
  -> skill.requested target=dex
  -> activate_skill: dex

kubernetes.context is configured and usable
  -> integration.available target=kubernetes
  -> enable_context_provider: kubernetes.context
  -> enable_datasource: kubernetes
```

This means the Go plugin can still be selected by Coder as a product capability,
but its expensive or narrow Go-specific operation sets do not need to be active
until environment signals justify them. Conversely, not finding a Go project
must not unload the Go plugin; it only means Go-specific activation reactions do
not fire for that scope.

The current "conditionally loaded" behavior should be split as follows:

- if the condition is about the declared shape of the app, keep it in
  composition. Example: a bundle that declares datasources may still require the
  datasource plugin to be selected through explicit dependency metadata.
- if the condition is about the current environment, move it to
  observation/signal/reaction. Example: `go.mod` exists, kube context is
  configured, an AWS profile is available, or a Slack message requested a skill.
- if the condition would require credentials or network calls, the owning plugin
  must already be selected and the observer must report only non-secret status;
  it must not become a plugin discovery mechanism.

The target session runtime should have one active-state object rather than many
feature-specific knobs. Conceptually:

```text
ActiveSessionState:
  skills: set[skill.Ref]
  references: set[skill.Ref + path]
  operation_sets: set[operation.SetName]
  datasources: set[datasource.Ref]
  context_providers: set[context.ProviderRef]
  reaction_applied_keys: set[string]
```

Product defaults can seed this state before reaction planning. Reactions can add
to it. Later context and operation registry construction should read from this
state instead of separately consulting feature-specific side channels.

Migration mapping:

| Existing mechanism | New owner | Replacement |
|---|---|---|
| Coder `ActivationInput.ProjectSignals` | project observer + project signal deriver | `project.inventory` observations derive `language.detected`, `project.task_runner.detected`, and similar signals. |
| Coder `ActivationInput.ToolchainStatuses` | toolchain observers + signal deriver | `toolchain.status` observations derive `toolchain.available`. |
| `runtime/language.SignalMatcher` | `core/reaction.Matcher` over `environment.Signal` | Language plugins contribute reaction rules that enable operation sets from `language.detected` or `toolchain.available`. |
| Skill/reference phrase triggers | channel-message observer + reaction rules | Message observations derive `skill.requested` or `skill.reference.requested`, then activate skill state through normal reactions. |
| Datasource detection from ad hoc context text | context/message observations + datasource reactions | Datasource visibility is enabled by explicit signals, while providers can still inspect observations for rendering. |

The practical target is that Coder's broad static product bundle can remain
selected, but session-visible tools/context become a consequence of reaction
state. That lets us remove bespoke activation inputs without making plugin
loading depend on host probes.

## Coder Configuration

Coder config should gain a top-level `reactions` section. It is deliberately
separate from `workspace` and `imports`.

Configuration should layer in this order:

1. distribution defaults;
2. enabled plugin defaults;
3. global coder config;
4. workspace/app config;
5. session overrides.

Later layers may add rules, disable named rules, or narrow observer phases. They
should not be able to enable an unavailable plugin by naming its observer or
reaction action. The resolved configuration should be explainable as a list of
observer specs, signal derivers, and reaction rules before a session starts.

Example:

```yaml
version: 1

reactions:
  - name: go-project
    when:
      signal: language.detected
      target: go
    actions:
      - activate_skill: go
      - activate_reference:
          skill: go
          path: references/testing.md

  - name: kubernetes-available
    when:
      signal: integration.available
      target: kubernetes
    actions:
      - activate_skill: kubernetes
      - activate_reference:
          skill: kubernetes
          path: references/kubectl.md

  - name: inspect-taskfile
    when:
      signal: project.task_runner.detected
      target: taskfile
    actions:
      - run_workflow:
          name: inspect-project-tasks
          require_approval: true
```

Default behavior:

- `activate_skill`, `activate_reference`, and context-oriented reactions may run
  automatically.
- `run_workflow`, `run_operation`, and command execution require policy approval
  unless an explicit future `auto_run` policy is defined and authorized.
- Unknown skills, references, workflows, or operations are diagnostics, not
  silent no-ops.

Future config should also allow observer selection without implying plugin
loading:

```yaml
version: 1

observations:
  baseline: true
  observers:
    - name: project.inventory
      phase: session_open
    - name: kubernetes.context
      phase: turn

reactions:
  - name: go-project
    mode: on_change
    when:
      signal: language.detected
      target: go
    actions:
      - activate_skill: go
```

If `kubernetes.context` is configured but the Kubernetes plugin is not enabled,
the config should produce a diagnostic. It should not enable the plugin.

Observer configuration is therefore a filter over already-available observers,
not a plugin loader. Valid examples:

```yaml
observations:
  observers:
    - name: runtime.baseline
      phase: turn
    - name: kubernetes.context
      phase: turn
```

Invalid unless `kubernetes` is selected elsewhere:

```yaml
observations:
  observers:
    - name: kubernetes.context
```

The second example should explain: `observer kubernetes.context is unavailable
because plugin kubernetes is not selected`.

## App Manifest Impact

Existing app manifests should not need to change just because observations and
reactions exist.

For example, an existing Slack bot manifest may already declare its plugin
boundary explicitly:

```yaml
plugins:
  - kind: identity
  - kind: slack
    instance: slack-bot
  - kind: gitlab
    instance: main
  - kind: jira
    instance: main
  - kind: confluence
    instance: main
```

Those declarations should continue to mean "these plugin capabilities are part
of the app." The observation/reaction model only changes how selected plugin
capabilities become active inside a session.

For an existing Slack bot, no manifest change is required unless the bot should
gain a new integration boundary or app-specific reaction. If it already declares
Slack, GitLab, Jira, Confluence, identity, and its skills, those plugins
continue to be selected exactly as before. Their future observers and default
reactions come along with those selected plugins.

An app manifest would need changes only when the app owner wants one of these:

- enable a plugin that is not currently part of the app, such as adding
  `kubernetes` or `aws`;
- override a plugin's default observers or phases;
- add app-specific reaction rules, such as activating a code-review skill when a
  GitLab merge request signal appears;
- disable a default plugin reaction that the distribution would otherwise
  provide.

Examples:

```yaml
# No change needed just to use default observations/reactions from selected
# Slack/GitLab/Jira/Confluence plugins.
plugins:
  - kind: slack
    instance: slack-bot
  - kind: gitlab
    instance: main
```

```yaml
# Change needed if the bot should observe Kubernetes, because Kubernetes is a
# new plugin boundary.
plugins:
  - kind: slack
    instance: slack-bot
  - kind: kubernetes
    instance: ai-bots
    config:
      namespaces: [ai-bots]
```

```yaml
# Change needed if the app wants app-specific behavior beyond plugin defaults.
reactions:
  - name: ai-bots-kubernetes
    when:
      signal: integration.available
      target: kubernetes
    actions:
      - kind: activate_reference
        reference:
          skill:
            name: code-review
          path: references/kubernetes-ai-bots.md
```

If app-authored reactions become part of `agentsdk.app.yaml`, they should be
declarative and inert, for example:

```yaml
reactions:
  - name: gitlab-merge-request
    when:
      signal: integration.object.detected
      target: gitlab.merge_request
    actions:
      - activate_skill: merge-request-review
```

But this should be additive. The migration should not require existing apps to
rewrite their `plugins:` section.

The practical migration rule for existing apps is:

```text
same plugin boundary wanted as today -> no manifest change
new integration boundary wanted -> add that plugin ref
app-specific activation behavior wanted -> add reactions
plugin default should not apply -> add an override/disable rule once supported
```

For example, a Slack bot does not need to add Kubernetes merely because the
runtime can observe Kubernetes. It should add the Kubernetes plugin only if the
bot is intended to use Kubernetes capabilities. Once added, the Kubernetes
plugin's observer and default reactions can decide whether those capabilities
are active for a particular workspace, namespace, or session.

## Reaction Actions

Initial action kinds should stay small:

- `activate_skill`;
- `activate_reference`;
- `enable_operation_set`;
- `enable_datasource`;
- `enable_context_provider`;
- `run_workflow`;
- `run_operation`;
- `emit_command` or `run_command`, only if command dispatch semantics are
  already available at that point in the session.

The first implementation should prioritize activation actions. Effectful actions
can be planned and recorded before being dispatched, then sent through the same
approval and authorization path as explicit user/model requests.

Avoid adding a generic shell action. If a reaction needs a process, it should go
through a named operation or workflow so policy and provenance stay meaningful.

## Loops And Cascades

Reactions can produce effects that create new observations and signals. The
runtime needs a simple cascade rule to avoid loops:

- one reaction evaluation pass runs before context materialization;
- effectful reaction results are observed as operation/workflow results and are
  eligible for the next turn or tool-followup pass;
- a reaction rule cannot re-trigger itself within the same pass using the same
  idempotency key;
- future multi-pass cascades must have an explicit depth limit.

This keeps "signal appears -> activate context" immediate, while keeping
"signal appears -> run workflow -> react to workflow result" auditable and
bounded.

## Event Model

The event stream should record enough to explain why the session changed.
Candidate events:

- `environment.observed`: bounded observations collected for a phase;
- `environment.signals_observed`: normalized signal changes;
- `reaction.matched`: rules that matched signal changes;
- `reaction.applied`: actions that changed session state;
- `reaction.rejected`: actions blocked by policy, missing resources, or
  approval requirements.

Existing event names should be replaced during migration. `project.signals_observed`
and `language.toolchain_status_observed` have moved to normalized environment
observations, signals, and reaction events. Since this repository is pre-1.0, do
not add long-lived compatibility shims for old event names.

## Explainability

The model should be inspectable without reading trace logs. A session should be
able to report:

- which observers were configured and which were skipped;
- which observations were collected for the active phase;
- which signals appeared, changed, or disappeared;
- which reaction rules matched;
- which actions were applied, rejected, or waiting for approval.

This can later back a `coder env explain`, debug panel, or structured trace
view. The important implementation constraint is that reaction planning produces
data before it mutates session state.

## Old Implementations To Replace

This model is meant to converge several existing special cases:

- **Project inventory signals**: `project.signals_observed` has been removed
  from the activation path. Project inventory now produces
  `project.inventory` observations plus normalized signals. `core/project.Signal`
  remains as bounded inventory data inside `project.Inventory`, which is still
  returned by `project_inventory`.
- **Toolchain status**: Go toolchain status now has a `toolchain.status`
  observation and `toolchain.available` signal, and the older
  `language.toolchain_status_observed` event has been removed.
- **Coder feature activation**: `apps/coder.ActivationInput` has been removed.
  Coder now uses generic reactions for the first language operation-set
  activations and should continue moving session-visible tools to reaction state.
- **Language support matching**: `runtime/language.SignalMatcher` has been
  removed. Generic `core/reaction.Matcher` over `environment.Signal` is the
  activation matcher.
- **Skill text triggers**: phrase-based skill and reference activation now uses
  generated reaction rules over channel-message observations.
- **Datasource detection input**: detected datasource context now derives local
  detector input directly from provider request observations, not a parallel
  context side channel.
- **Baseline environment context**: workspace summary, current identity, time,
  locale, and similar runtime facts should be baseline observations consumed by
  context providers, instead of separate one-off context-provider inputs.
- **Integration availability**: Kubernetes context detection and AWS credential
  availability are plugin observers that produce observations and signals only
  when their owning plugin/config is enabled. Docker daemon reachability,
  GitHub CLI auth status, and similar integrations should use the same pattern.

## Implementation Progress

This section should be kept current while the design is implemented.

Already present:

- `core/environment` has `Observation`, `EffectRequest`, and `EffectResult`
  shapes.
- `core/environment` has a normalized `Signal` shape with activation keys and
  fingerprints for de-duplication and on-change reaction checks.
- `core/reaction` has inert `Rule`, `Matcher`, and `Action` specs for
  activation and effectful reaction planning.
- `runtime/reaction` has a pure planner that evaluates rules against signals,
  supports `on_change` and `every_turn`, computes current signal fingerprints,
  and suppresses already-applied idempotency keys.
- `core/environment` has inert observer and signal-deriver specs.
- `core/resource.ContributionBundle` can carry observer specs, signal deriver
  specs, and reaction rules for static inspection.
- `runtime/environment` owns executable observer and signal-deriver contracts
  plus pure helpers for phase-filtered observer runs and signal derivation.
- `runtime/environment` includes a baseline turn-phase observer for cheap
  non-secret local runtime facts: current time, locale environment, and current
  username.
- `runtime/environment` can adapt inert `SignalDeriverSpec` templates into
  executable pure signal derivers for app/workspace/user-authored derivation
  rules.
- `orchestration/pluginhost` can resolve selected plugin observer, signal
  deriver, and reaction contributors.
- app composition carries selected plugin observers, signal derivers, and
  reaction rules in the assembled composition so session wiring can consume
  them.
- app composition prepends the baseline runtime observer to composed plugin
  observers so those facts are available as observations without becoming system
  context.
- the in-process facade and harness pass composed observers, signal derivers,
  and reaction rules into `orchestration/session.Session`.
- `orchestration/harness` runs selected `startup` observers once at harness
  construction, derives startup signals once, and passes the precomputed
  startup observations/signals into session turns.
- `orchestration/session` runs selected turn-phase observers before context
  materialization, appends their observations to the active turn observation
  set, runs selected signal derivers over that set, and carries derived signals
  through the inner turn result for reaction planning.
- `orchestration/session` runs selected `session_open` observers before the
  first model turn for a stored thread, derives signals, and applies matching
  reactions before regular turn-phase observation.
- `orchestration/session` also runs selected `tool_followup` observers after
  operation results, re-derives signals over the expanded observation set, and
  applies matching reactions before rendering the follow-up context.
- `orchestration/session` runs selected `lazy` observers once immediately
  before context materialization when context providers are present, re-derives
  signals, and applies matching reactions before the provider build.
- `orchestration/session` routes `run_operation` reaction actions through the
  existing operation registry/catalog resolution, operation executor, safety
  envelope, session operation events, and result observation path before the
  agent step.
- `orchestration/session` routes `run_command` reaction actions through the
  existing command dispatcher, preserving command policy evaluation and
  command-target execution before the agent step.
- `orchestration/session` routes `run_workflow` reaction actions through the
  existing workflow executor and exposes workflow results as observations before
  the agent step.
- `orchestration/session` exposes a built-in `env explain` session command that
  reports configured observers, signal derivers, reaction rule names, replayed
  active reaction state, and applied reaction count.
- `orchestration/session` plans reactions over derived signals before context
  materialization and applies low-risk `activate_skill` and
  `activate_reference` actions through the existing skill activation state and
  replayable skill events.
- `orchestration/sessionenv` applies low-risk `enable_operation_set` and
  `enable_datasource` reactions into replayable active session state, active
  datasources participate in operation and context-provider datasource access
  policy, and `enable_context_provider` reactions activate selected context
  providers.
- `orchestration/session` projects active operation sets into per-turn
  model-visible operation tools and uses that same active projection when
  enforcing projected-operation execution.
- `orchestration/session` includes selected context-provider implementations
  when reaction-produced active state enables them, so context providers can be
  activated in the same turn before context materialization while preserving
  existing agent-configured provider behavior.
- reaction application emits replayable `reaction.action_applied` events and
  reloads persisted action idempotency keys from the thread event stream before
  planning, so already-applied on-change reactions do not repeat across inbound
  turns.
- reaction planning and application also emits replayable planned, skipped, and
  diagnostic events, giving the event stream enough detail to explain why a
  reaction did or did not run.
- the Kubernetes plugin contributes a real turn-phase `kubernetes.context`
  observer and `kubernetes.signals` deriver when the plugin is selected,
  producing non-secret integration configured/available signals from plugin
  configuration and usable client configuration.
- the AWS plugin contributes a real turn-phase `aws.environment` observer and
  `aws.signals` deriver when the plugin is selected, producing non-secret
  integration configured/available signals from profile, region, and credential
  presence without exposing access key, secret key, or session token values.
- the project plugin contributes a real `session_open` `project.inventory`
  observer and `project.signals` deriver when selected, producing
  `language.detected`, `project.toolchain.hinted`, and
  `project.manifest.detected` signals from bounded workspace inventory.
- the Go plugin contributes a real `session_open` `golang.toolchain.status`
  observer and `golang.toolchain.signals` deriver when selected, producing
  `toolchain.available target=go` from non-secret local Go binary status.
- the Coder app contributes initial generic reaction rules that enable selected
  Go parser, Go toolchain, and Markdown operation sets from
  `language.detected` and `toolchain.available` signals, and the old
  `apps/coder.ActivationInput` /
  `runtime/language.SignalMatcher` activation path has been removed from code.
- app composition converts selected skill and reference trigger phrases into a
  `skill.triggers` signal deriver plus generated reaction rules, so text-trigger
  activation flows through normal `skill.requested` and
  `skill.reference.requested` signals instead of a session-local trigger path.
- app manifest decoding supports top-level `observations` and `reactions`
  sections and multi-document `kind: observer`, `kind: signal_deriver`, and
  `kind: reaction` resources, carrying them into contribution bundles without
  enabling plugins by name.
- app composition now includes bundle-authored reaction rules in session
  configuration, so app manifest reactions are functional rather than only
  statically inspectable.
- app composition now turns bundle-authored signal-deriver templates from
  app/user/plugin bundles into executable session derivers when no selected
  plugin provides a custom executable deriver with the same name.
- app composition applies app/user-authored observer specs as overrides for
  already-selected executable observers, allowing config to narrow observer
  phase and observable kinds without enabling unavailable plugins.
- app composition also honors `disabled: true` on app/user-authored observer
  specs, removing already-selected executable observers from the runtime
  schedule without treating absent disabled observers as missing plugins.
- `runtime/environment` enforces observer `observable_kinds` on returned
  observations, so a configured narrowed observer cannot leak unrelated
  observation kinds into derivation or context provider input.
- app composition reports non-fatal diagnostics when configured observer specs
  have no enabled executable observer, or when signal-deriver specs have neither
  templates nor enabled executable derivers.
- app composition reports non-fatal diagnostics when reaction actions reference
  skills, inline skill references, operation sets, datasources, context
  providers, workflows, operations, or commands that are not present in the
  selected resource graph.
- `.coder.yaml` decoding supports top-level `observations.observers`,
  `observations.signal_derivers`, and `reactions`, keeping the file format as a
  boundary DTO before converting into core environment and reaction specs.
- coder local config is converted into a contribution bundle and passed into
  normal coder launches and `fluxplane run`, so parsed `.coder.yaml` reaction
  rules participate in session reaction planning.
- `orchestration/session` already converts inbound channel messages,
  continuations, and operation effects into observations and passes them through
  agent step input.
- context materialization passes the active observations through
  `core/context.Request`, including fingerprinting providers, so context
  providers can consume observations without hidden side channels.
- the datasource detected context provider reads `core/context.Request`
  observations directly for local reference detection, and the older
  `DetectionInput`/detected-ref context side channel has been removed.
- context providers already support dynamic blocks through
  `core/context.FreshnessDynamic`.
- pluginhost already resolves selected plugin refs and collects executable
  operation, context provider, channel, connector, datasource, auth, and
  identity contributions.

Completion criteria for this design:

- selected plugins can contribute observers, derivers, and default reactions;
- configured observers run only when their owning plugin/runtime is enabled;
- observations derive scoped, fresh/stale-aware signals;
- reaction rules can activate skills, references, operation sets, datasources,
  and context providers before context materialization;
- effectful `run_operation`, `run_command`, and `run_workflow` reaction actions
  are routed through existing execution and authorization/approval paths;
- old project/toolchain/skill-trigger conditional activation paths are removed;
- existing app manifests keep their `plugins:` semantics and do not need changes
  unless they opt into new plugins or custom reactions;
- tests cover plugin contribution resolution, signal derivation, reaction
  idempotency, context provider observation access, and migration of Coder
  activation behavior.

## Remaining Steps

No mandatory implementation slices remain for this design. Follow-on extensions
can add more observers without changing the core model:

- Docker daemon reachability can become a Docker plugin observer.
- GitHub CLI auth status can become a GitHub plugin observer.
- Cloud providers beyond AWS can add provider-specific observers and derivers.

Each follow-on slice should leave the tree internally consistent and covered by
tests.

## Placement

Suggested package placement:

```text
core/environment
  Observation, observer/reaction-facing inert signal shapes

core/reaction
  Rule, matcher, action specs

orchestration/pluginhost
  selected plugin observer, deriver, and reaction contributor interfaces

runtime/environment
  observer execution helpers and signal derivation

runtime/reaction
  pure matching and reaction planning

orchestration/sessionenv
  applies context and skill activation reactions during session turns

orchestration/session
  dispatches approved effectful reactions through existing command,
  workflow, and operation paths

apps/coder/app
  parses .coder.yaml reaction rules
```

Plugins may contribute observers and default reaction rules only when the plugin
itself is configured. Apps decide which plugins are enabled; observers must not
implicitly enable plugins.

The dependency direction should remain:

```text
plugins contribute observer specs, derivers, and reaction defaults
apps choose plugins and user config
orchestration schedules observers and applies reactions
runtime executes observers and pure matching helpers
core owns inert observation/signal/reaction shapes
```

## Safety

Observers must be cheap, bounded, and non-secret by default. They should prefer
configuration and metadata checks over network calls. Expensive or remote
verification should be lazy and tied to an enabled plugin or declared resource.

Reactions must be auditable. Any reaction that changes state, calls a networked
service, runs a process, mutates files, or starts workflow execution must use the
same authorization and approval path as an explicit user/model request.
