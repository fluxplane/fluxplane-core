# Evidence, Assertions, and Coder Tool-Surface Reduction

Date: 2026-05-20

## Goal

Reduce the default model-facing tool surface in `apps/coder` by replacing
always-on capability exposure with an evidence-driven activation path.

The target flow is:

```text
rich evidence -> typed assertions -> session-local activation -> projected tools
```

This plan intentionally evolves the existing `core/environment` design instead
of introducing an unrelated fourth concept. The current observation/signal path
is directionally correct, but its ergonomics are too stringly and its evidence
payloads are not treated as first-class enough.

The first milestone is deliberately practical: prove that the existing reaction
machinery can reduce projected tools before doing the package rename. Plugin
contributions should remain composed; only the default agent operation allow-list
should shrink.

Refined direction after review:

- Treat this as an evolutionary refactor, not a new parallel core concept.
- Keep `core/environment` working while the tool-surface slices prove the value.
- Rename/migrate only the observation/signal half after behavior is stable.
- Prefer rich evidence payloads and typed assertion subjects over adding more
  string constants.
- Keep activation session-local by default. Discovery and evidence can describe
  global/runtime state, but a model-facing tool should appear only when the
  current session has enough evidence to justify it.

## Target Vocabulary

### Evidence

Evidence is rich, inspectable runtime knowledge. It answers: **what do we know?**

Examples:

- workspace project inventory was detected;
- Go module exists at a workspace path;
- Docker CLI and daemon are available;
- Kubernetes context/namespace is configured and reachable;
- Loki endpoint candidate was discovered and probed ready;
- image provider credentials are available;
- current user turn asks for browser or image work.

Current code maps roughly to:

- `core/environment.Observation`
- `core/environment.ObserverSpec`
- `runtime/environment.Observer`

Target naming:

- `core/evidence.Observation`
- `core/evidence.ObserverSpec`
- `runtime/evidence.Observer`

Implementation note: do not blindly rename all of `core/environment`.
`environment.Spec`, `Boundary`, `Effect`, `EffectRequest`, and `EffectResult`
may stay under `core/environment` or move separately after the evidence path is
stable. The first migration should focus on the observation/derivation/reaction
surface.

### Assertion

An assertion is a small normalized claim derived from evidence. It answers:
**what does this evidence mean for matching and activation?**

Examples:

- `language.detected` for subject `language/go`;
- `toolchain.available` for subject `toolchain/go`;
- `integration.available` for subject `integration/kubernetes`;
- `endpoint.available` for subject `endpoint/loki`;
- `capability.requested` for subject `capability/browser`.

Current code maps roughly to:

- `core/environment.Signal`
- `core/environment.SignalDeriverSpec`
- `runtime/environment.SignalDeriver`

Target naming:

- `core/evidence.Assertion`
- `core/evidence.AssertionDeriverSpec`
- `runtime/evidence.AssertionDeriver`

Assertions should keep a stable matcher identity without relying on arbitrary
`Kind + Target + Scope` string conventions. Add a typed subject shape before
large migrations, for example:

```go
type Subject struct {
    Kind string `json:"kind,omitempty"`
    Name string `json:"name,omitempty"`
    ID   string `json:"id,omitempty"`
}
```

`Assertion.Kind` can remain a string initially, but common assertion kinds and
subject kinds should be declared centrally and reused by plugins.

Near-term subject taxonomy:

| Subject kind | Example subject | Purpose |
| --- | --- | --- |
| `language` | `language/go` | Enables language parsers and navigation tools. |
| `toolchain` | `toolchain/go` | Enables build/test/fmt/doc tools that require binaries. |
| `integration` | `integration/docker` | Enables integration-backed operations when runtime dependencies exist. |
| `endpoint` | `endpoint/loki` | Enables product tools backed by a configured or probed endpoint. |
| `capability` | `capability/browser` | Enables broad specialty surfaces when requested by the turn. |
| `provider` | `provider/image/openai` | Explains configured provider availability without exposing tools by itself. |

Do not rely on free-form `Target` values forever. The migration should introduce
a typed `Subject` while preserving the existing `Kind + Target` matcher only
long enough to move call sites.

### Deriver

A deriver converts evidence into assertions. It should usually be pure:

```text
project inventory evidence -> language.detected(go)
kubernetes context evidence -> integration.available(kubernetes)
endpoint probe evidence -> endpoint.available(loki)
channel message evidence -> capability.requested(browser)
```

Plugins own domain-specific interpretation. Core owns only the neutral
evidence/assertion contracts.

### Reaction and Activation

Reactions remain the bridge from assertions to active session resources.

Current `orchestration/sessionenv.ActiveState` remains useful:

- operation sets;
- datasources;
- context providers.

The first tool-surface work should use the existing reaction machinery. Do not
block on a perfect evidence package rename before reducing default tools.

Important implementation rule:

- keep plugin contributions, operation specs, executable operation
  implementations, and operation sets registered in composition;
- remove gated operations from `coderAgentSpec(...).Operations`, not from the
  product/plugins;
- rely on `Session.activeOperationSetTools` to project tools from
  reaction-enabled operation sets after assertions are present.

### Activation Shape

Activation has three distinct layers:

1. **Composition:** app/plugin bundles register operation specs, tool sets,
   operation sets, datasources, observers, derivers, and reactions.
2. **Default allow-list:** the default agent spec names the small always-on
   operation set. This is what directly shrinks the model-facing surface.
3. **Session active state:** evidence-derived assertions fire reactions that
   enable extra operation sets/datasources/context providers for that session.

The important invariant is:

```text
registered != projected
```

An operation can be registered, executable, and available for activation without
being projected to the model on an ordinary turn.

Current reaction matchers match a single signal/assertion. For gates that need
multiple facts, use one of these approaches:

- **Phase 1:** derive a composite assertion, for example
  `capability.ready_and_requested` for `capability/browser`, after seeing both
  availability evidence and turn intent evidence.
- **Phase 2:** add conjunctive matchers to `core/reaction` once enough use cases
  prove the shape. Do not add this before the simpler composite assertion path
  becomes insufficient.

## Current Coder Surface Problem

`apps/coder/features.go` has historically expanded the main local-coding feature
into a very broad default operation list:

- project, Go, and markdown tools;
- filesystem/search tools;
- web search/request tools;
- discovery and endpoint introspection tools;
- Loki, MySQL, and datasource tools;
- browser tools;
- git tools;
- shell/process/code execution tools;
- task, memory, image, skill, and clarification tools.

Some activation paths already exist, but they cannot reduce surface because the
same operations are still directly listed in the default operation set. For
example:

- Docker evidence already enables the `code` operation set, but `code_execute`
  is also listed directly.
- Project/language evidence already enables Go and markdown operation sets, but
  those operation sets are expanded into defaults.

The feature should be named for the baseline it actually exposes, for example
`BaseLocalCodingFeature`. Avoid names that imply every local capability should
be default-expanded.

## Baseline Always-On Surface

The goal is a smaller useful default, not an inert coder. These tool groups
should stay available without evidence-gated activation unless a later plan
chooses to gate them explicitly:

- core workspace and filesystem read/edit/search operations;
- project inventory, docs, files, tasks, and dry-run task resolution;
- basic git status/diff/add/commit operations used in normal coding workflows;
- task creation/planning/execution/review operations;
- clarification and essential skill/reference operations;
- web search and direct web request, unless network capability gating becomes a
  separate policy decision.

Specialty or environment-dependent groups should be gated:

- Go parser/toolchain and markdown language operations;
- Docker-backed `code_execute`;
- Loki, MySQL, product-specific datasources, and endpoint discovery tools;
- browser automation;
- image generation/understanding;
- memory mutation tools.

Baseline should remain biased toward tools that are cheap, local, and broadly
useful in almost every coding task. Gated groups are large, external,
credential-dependent, high-latency, high-risk, or only relevant to a minority of
turns.

## Evidence Classes

Use evidence in two different ways:

- **Availability evidence** enables stable capability groups for the session or
  workspace, such as Go tools in a Go repository, Docker-backed code execution,
  or Loki tools when a configured/probed Loki endpoint exists.
- **Turn intent evidence** enables large specialty surfaces only when the
  current user turn calls for them, such as browser or image work.

Do not expose browser/image tools just because the runtime can launch a browser
or has image provider credentials. Availability alone is too broad for those
clusters.

Activation policy matrix:

| Capability group | Required evidence/assertion | Default projection |
| --- | --- | --- |
| Project inventory/docs/tasks | Workspace/session exists | Always on |
| Filesystem read/edit/search | Workspace/session exists | Always on |
| Basic git | Git plugin composed | Always on |
| Go parser | Project inventory detects Go | Gated |
| Go toolchain | Go binary/toolchain evidence | Gated |
| Markdown | Project inventory detects markdown | Gated |
| Docker code execution | Docker daemon/CLI available | Gated |
| Loki | Explicit/probed Loki endpoint | Gated |
| MySQL | Explicit/probed MySQL endpoint | Gated |
| Discovery introspection | Discovery provider/manual discovery intent | Gated or command-only |
| Browser | Browser available plus browser turn intent | Gated |
| Image | Provider available plus image turn intent | Gated |
| Memory retrieval | Memory store configured | Optional gated |
| Memory mutation | Memory store configured plus explicit memory intent/policy | Gated |

## Implementation Slices

### Slice 1: Stop Bypassing Existing Reactions

Goal: immediate reduction with minimal new concept work.

Changes:

- Rename the old exhaustive local-coding feature to `BaseLocalCodingFeature`, or
  equivalent, and make the new name mean "always-on baseline", not "everything
  local".
- Remove `code_execute` from the base feature operations.
- Keep the existing Docker observer/deriver and
  `coder.integration.docker.available` reaction as the way to enable the
  `code` operation set.
- Stop expanding `golang.parser`, `golang.toolchain`, and `markdown` operation
  sets into the default coder operations.
- Keep project inventory operations available by default, because they are the
  evidence source for language/project assertions.
- Keep existing project and Go toolchain reactions responsible for enabling Go
  and markdown tools.
- Add a before/after tool-count measurement for the default coder session and
  keep it visible in tests or test output.

Implementation notes:

- Delegated sub-sessions may keep a broader allowed operation list if that is
  needed for worker agents, but the main coder agent's model-facing surface must
  be reduced.
- Tests should distinguish three inventories:
  - operations in the composed catalog;
  - operations in operation sets;
  - operations projected to the model for the active session.

Expected effect:

- Non-Docker sessions do not expose `code_execute`.
- Non-Go workspaces do not expose Go parser/toolchain tools.
- Non-markdown-oriented workspaces do not expose markdown diagnostics/outline
  tools unless project evidence activates them.

Tests:

- Update `apps/coder` bundle/feature tests to assert these operations are not
  default-expanded.
- Add or update session tests proving reaction-enabled operation sets still
  project tools when their assertions are present.
- Verify the removed operations still exist in the composed operation and
  operation-set catalogs so reactions can activate them later.

### Slice 2: Endpoint Availability Assertions

Goal: gate Loki/MySQL/discovery-related tools behind actual endpoint evidence.

Changes:

- Add an endpoint-discovery evidence observation for configured endpoints,
  completed discovery runs, or ready endpoint records.
- Derive assertions such as:
  - `endpoint.available` subject `endpoint/loki`;
  - `endpoint.available` subject `endpoint/mysql`;
  - optionally `integration.available` subject `integration/loki` when a usable
    Loki endpoint or explicit Loki URL is present.
- Add coder reactions:
  - Loki endpoint/config available -> enable `loki` operation set and datasource.
  - MySQL endpoint/config available -> enable `mysql` operation set.
  - Discovery provider available -> enable discovery introspection tools, or
    keep only a small manual discovery command visible if needed.
- Remove `discovery_*`, `endpoint_*`, `loki_*`, and `mysql_query` from the
  unconditional default operation list once reactions cover them.

Endpoint evidence shape should include at least:

```go
type EndpointEvidence struct {
    Ref       endpoint.Ref
    Product   string
    URL       string // sanitized if needed
    Source    endpoint.SourceRef
    Readiness string // configured, probed, unprobed, failed
    Metadata  map[string]string
}
```

The assertion deriver should emit `endpoint.available` only for:

- explicit configured endpoints or URLs;
- endpoint refs that were validated by a product operation;
- discovery candidates with successful readiness/product probes.

It should not emit availability assertions for raw provider candidates merely
because the product name looks like `loki` or `mysql`.

Activation rules:

- explicit configured URL or endpoint ref counts as availability;
- discovered endpoint counts only after a successful readiness/product probe;
- unprobed candidates are evidence for context/introspection, but must not
  enable product operation sets by default.

Expected effect:

- Most local coding sessions do not expose observability/database tools.
- A Kubernetes-backed session with discovered Loki can still auto-enable Loki
  tools and use the resolved endpoint.

Tests:

- Kubernetes discovery provider produces endpoint records for Loki/MySQL
  candidates as today.
- Endpoint evidence derives availability assertions.
- Reactions enable Loki/MySQL operation sets only when assertions are present.
- Unprobed Loki/MySQL-looking candidates do not activate product tools.
- Loki operations still resolve configured URLs or endpoint refs at call time.

Implementation shortcut:

- Initial endpoint availability may use metadata such as
  `readiness=configured|probed` or `probe_status=ready`.
- Provider-owned discovery candidates without readiness metadata remain rich
  evidence for context/explainability but do not activate product tools.

### Slice 3: Browser Intent and Availability

Goal: avoid exposing thirteen browser tools unless the browser is usable and
relevant to the turn.

Changes:

- Add browser availability evidence from the browser adapter/plugin when a
  browser can be launched or connected.
- Add a lightweight turn-intent deriver from `channel.message` evidence for
  browser-related requests.
- Derive:
  - `integration.available` subject `integration/browser`;
  - `capability.requested` subject `capability/browser`.
- Enable browser operation set only when both availability and request intent
  are present, or when a session/profile explicitly opts into always-on browser
  tools.

The initial intent deriver may be simple keyword/phrase matching. It should be
isolated behind a deriver contract so it can later be replaced by a classifier
without changing reaction semantics.

Because the current reaction matcher is single-assertion, the first browser
implementation should derive a composite assertion only when both facts are true:

```text
browser runtime evidence + browser turn intent -> capability.ready_and_requested(browser)
```

The reaction then matches that composite assertion and enables the `browser`
operation set. This avoids adding conjunction semantics to reactions too early.

Intent matching should be conservative. Strong examples:

- "open this page in a browser";
- "click the login button";
- "take a screenshot";
- "inspect the rendered UI";
- "use the browser to check ...".

Weak examples such as "browse the repo", "navigate files", or "open the file"
should not activate browser tools.

Expected effect:

- Normal coding turns do not expose browser navigation/click/screenshot tools.
- Browser-heavy tasks still get the full browser surface when the user asks for
  it.

Tests:

- Browser tools are absent from ordinary coding turns.
- Browser tools appear when a browser request is present and availability
  evidence exists.
- Explicit session opt-in can keep browser tools always available.

### Slice 4: Image and Memory Gating

Goal: remove specialty tools from the default surface unless configured or
requested.

Changes:

- Image tools:
  - derive provider availability from image provider configuration;
  - derive image request intent from user turn evidence;
  - enable image operation set only when provider availability and intent match.
- Memory tools:
  - derive `integration.available` subject `integration/memory` when a memory
    store is configured;
  - keep memory mutation tools gated by policy and/or explicit memory intent;
  - consider leaving retrieval available only if it is cheap and clearly useful.

Image should follow the same composite assertion rule as browser:

```text
image provider evidence + image turn intent -> capability.ready_and_requested(image)
```

Provider evidence alone should be visible to `/env explain` but should not
project the image action tool. The model should not see image generation simply
because an API key is present.

Memory should be split more carefully than image/browser:

- retrieval may be a low-noise datasource/context feature if memory is configured;
- mutation operations (`memory_memorize`, `memory_forget`, `memory_organize`)
  should require explicit user intent or policy-enabled agent behavior;
- retrieval and mutation should not be forced into one all-or-nothing operation
  set if that causes unnecessary projection.

As with browser intent, the initial image/memory intent detection may be simple
and conservative. False negatives are acceptable if the user can still request
or enable the capability explicitly; false positives should be avoided because
the goal is tool-surface reduction.

Expected effect:

- `image_generate`, `image_understand`, and `image_providers` are not present in
  normal coding turns.
- Memory mutation tools do not appear unless memory is configured and relevant.

## Evidence Package Migration

After the first tool-surface slices prove the behavior, migrate naming in small
steps:

1. Add `core/evidence` as a new package that aliases or wraps the observation,
   assertion, observer-spec, and deriver-spec shapes.
2. Add `runtime/evidence` aliases/wrappers for observer and assertion-deriver
   execution contracts.
3. Add typed assertion subjects and constants for common subject kinds.
4. Move reaction matching to assertion vocabulary while preserving old field
   compatibility during the migration.
5. Update project, Docker, Kubernetes, AWS, endpoint discovery, browser, image,
   memory, and skill trigger code to import `core/evidence` /
   `runtime/evidence`.
6. Remove or shrink the observation/signal half of `core/environment` once all
   imports are migrated.

Aliases/wrappers are only temporary migration aids inside this chapter. Avoid
compatibility shims that preserve stale concepts after the migration is
complete. This repository is pre-1.0; prefer replacing stale shapes cleanly.

## Open Design Decisions

- Whether `Signal` should be renamed directly to `Assertion` in one migration
  or introduced as an alias first.
- Whether assertion `Subject` should be a new struct immediately or phased in
  alongside current `Target`.
- Whether discovery introspection tools should be always hidden, enabled by
  provider availability, or exposed as user-only commands rather than model
  tools.
- Whether browser/image request intent should remain simple keyword derivation,
  become a model/pluggable classifier, or be driven by explicit command/session
  metadata.
- Whether reaction conjunctions are worth adding after composite assertions have
  covered the first browser/image cases.
- Whether memory retrieval belongs in the default datasource/context path while
  mutation remains operation-gated.

## Implementation Order

1. Shrink the default coder operation list and verify Go/Markdown/Docker
   activation still works.
2. Add endpoint evidence/assertions and gate Loki/MySQL/discovery tools.
3. Add composite capability assertions for browser availability plus intent.
4. Add composite capability assertions for image provider availability plus
   intent.
5. Split memory retrieval/mutation activation policy.
6. Add `/env explain` or equivalent diagnostics for why a capability was or was
   not activated.
7. Start the `core/environment` -> `core/evidence` naming migration after the
   behavior is exercised by tests.

## Acceptance Criteria

- Default coder sessions expose materially fewer tools before any user turn, with
  a measured before/after count captured in tests or diagnostics.
- Existing evidence-backed activation still makes Go, markdown, Docker/code,
  Loki, and MySQL tools available when their assertions are present.
- Removed default operations still remain available through composed operation
  catalogs and reaction-enabled operation sets.
- Configured or probed-ready Loki restores Loki tools; unprobed endpoint
  candidates do not.
- Browser and image tools are absent on ordinary turns and present when
  availability plus user intent exists.
- `/env explain` or its successor can explain which evidence/assertions caused
  tools, datasources, and context providers to activate.
- Context providers can still access rich evidence, not only assertions.
- Endpoint discovery remains a concrete resolver for runtime targets, not the
  generic activation framework.
