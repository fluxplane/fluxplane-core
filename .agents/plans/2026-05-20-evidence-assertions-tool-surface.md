# Evidence, Assertions, and Coder Tool-Surface Reduction

Date: 2026-05-20
Status: implemented and verified in this working tree

## Goal

Reduce the default model-facing tool surface in `apps/coder` by replacing
always-on capability exposure with evidence-driven activation.

The implemented flow is:

```text
rich evidence -> typed assertions -> session-local reactions -> projected tools
```

This is an evolutionary refactor of the old observation path, not a new
parallel concept. `core/evidence` now owns the observation/assertion contracts.
`core/environment` remains for actual environment boundary/effect concepts such
as `EffectRequest` and `EffectResult`.

## Concept Model

### Evidence

Evidence is rich, inspectable runtime knowledge. It answers: what do we know?

Examples:

- project inventory was detected for a workspace;
- Docker CLI/daemon status was observed;
- Kubernetes context and namespace were observed;
- endpoint registry entries were configured, discovered, or probed;
- browser/image/memory capability availability was observed.

Implemented ownership:

- `core/evidence.Observation`
- `core/evidence.ObserverSpec`
- `runtime/evidence.Observer`
- `runtime/evidence.RunObservers`

### Assertion

An assertion is a normalized claim derived from evidence. It answers: what does
this evidence mean for matching and activation?

Examples:

- `language.detected` for subject `language/go`;
- `toolchain.available` for subject `toolchain/go`;
- `integration.available` for subject `integration/docker`;
- `endpoint.available` for subject `endpoint/loki`;
- `capability.available` for subject `capability/browser`.

Implemented ownership:

- `core/evidence.Assertion`
- `core/evidence.Subject`
- `core/evidence.AssertionDeriverSpec`
- `runtime/evidence.AssertionDeriver`
- `runtime/evidence.DeriveAssertions`

Assertions carry a typed `Subject{Kind, Name, ID}`. Activation keys and
fingerprints include the subject so reaction idempotency is not based only on
free-form `Kind + Target` strings.

### Reaction and Activation

Reactions bridge assertions to active session resources:

- operation sets;
- datasources;
- context providers;
- skills/references.

The invariant is:

```text
registered != projected
```

Plugins still compose operations, operation sets, observers, assertion derivers,
datasources, context providers, and reaction rules. The model only sees the
baseline tools plus session-local tools activated by assertions.

## Completed Implementation Steps

1. **Shrank the default coder surface.**
   `apps/coder` now uses `BaseLocalCodingFeature` for the always-on surface.
   Large language/toolchain, discovery, endpoint, browser, image, memory
   mutation, and Docker-backed code execution groups are no longer default
   projected just because their plugins are composed.

2. **Kept capabilities registered but not projected.**
   Operation catalogs and operation sets still include gated tools. Tests
   distinguish composed operations from projected tools so activation can expose
   a tool without making it baseline.

3. **Moved observation/assertion contracts into `core/evidence`.**
   Concrete `Observation`, `Assertion`, `Subject`, observer specs, assertion
   templates, and assertion-deriver specs now live in `core/evidence`.
   `core/environment` no longer owns those concrete evidence shapes.

4. **Moved runtime evidence execution into `runtime/evidence`.**
   Observer and assertion-deriver runtime contracts, baseline observer logic,
   template assertion derivation, diagnostics, and batch helpers now live under
   `runtime/evidence`. The old `runtime/environment` package was removed.

5. **Kept `core/environment` focused on boundaries/effects.**
   The package now owns environment names/refs, scopes, persistence,
   boundaries, observables, effects, effect requests, and effect results.
   `EffectResult.Observation` points at `core/evidence.Observation`.

6. **Renamed reaction matching to assertions.**
   `core/reaction.Matcher` now matches `Assertion`, `Target`, typed `Subject`,
   scope, source, and metadata. Reaction event provenance records assertion
   fields and subject identity.

7. **Migrated session and harness execution.**
   Session startup evidence, per-turn observers, assertion derivation, reaction
   planning, `/env/explain`, and harness startup state all use evidence
   vocabulary and `runtime/evidence`.

8. **Migrated pluginhost and first-party plugins.**
   Plugin contribution interfaces now expose `Observer` and `AssertionDeriver`
   from `runtime/evidence`. Project, Go, Docker, Kubernetes, AWS, discovery,
   browser, image, memory, datasource, and coding bundle paths use
   `core/evidence` / `runtime/evidence`.

9. **Renamed manifest/config shapes.**
   App config and `.coder.yaml` use `assertion_derivers`, `assertions`, and
   reaction `when.assertion`. Multi-document app resources use
   `kind: assertion_deriver`.

10. **Renamed project inventory signals to hints.**
    `core/project.Inventory` and `Project` now expose `Hints`, and
    `core/project.Hint` represents inert project inventory hints. Project hints
    are evidence inputs; project assertion derivers convert them into
    activation assertions.

11. **Renamed boundary client signals to triggers.**
    The channel client submission payload formerly named `Signal` is now
    `Trigger`. This removes the last API-level collision with evidence
    assertions while preserving the separate boundary-trigger concept.

12. **Renamed assertion constants and deriver names.**
    First-party assertion constants now use assertion vocabulary. Deriver names
    and descriptions use `.assertions` / "assertions" rather than stale
    "signals" naming, except for unrelated standard library `os/signal`.

13. **Language/toolchain activation remains evidence-driven.**
    Project inventory hints derive language assertions. Go parser, Markdown,
    and Go toolchain operation sets activate from those assertions instead of
    being baseline tools.

14. **Docker/code execution activation remains evidence-driven.**
    Docker evidence derives configured/available assertions. The `code`
    operation set activates only when Docker availability is asserted.

15. **Endpoint activation gates Loki/MySQL/discovery.**
    Endpoint registry evidence derives `endpoint.available` assertions only for
    configured or ready/probed endpoints. Loki/MySQL tools and datasources are
    enabled by endpoint assertions. Endpoint activation also enables discovery
    introspection so users can inspect/refresh after evidence appears.

16. **Browser activation uses stable availability.**
    Browser tools are exposed from a stable `capability.available` assertion
    when browser automation is configured for the runtime. They are not gated by
    the current user turn.

17. **Image activation is capability-specific.**
    Image generation and image understanding are separate operation sets.
    Provider availability activates only the configured capability set, so a
    generation provider does not expose understanding tools. Image tools are not
    gated by the current user turn.

18. **Memory mutation uses stable store availability.**
    Low-noise memory retrieval can remain available where configured, while
    mutation tools activate from configured memory storage. They are not gated
    by the current user turn.

19. **Explainability covers evidence and reactions.**
    `/env/explain` reports observers, assertion derivers, observations,
    derived assertions, matched reaction actions, active resources, and applied
    reaction provenance.

20. **Verification coverage was updated.**
    Focused tests cover assertion fingerprints, reaction matching, config
    decoding, composition, session activation, plugin derivers, project hints,
    client triggers, and coder shell rendering. `go test ./...` passes in this
    working tree.

## Current Coder Baseline

The baseline remains useful but smaller:

- project inventory/docs/files/tasks;
- workspace and filesystem read/edit/search operations;
- basic git operations used in coding workflows;
- task planning/execution/review operations;
- essential skill/reference and clarification operations;
- web search/request.

Gated groups:

- Go parser/toolchain and Markdown tools;
- Docker-backed code execution;
- endpoint discovery, Loki, MySQL, and product datasources;
- browser automation;
- image generation/understanding;
- memory mutation.

## Acceptance Status

- Default coder sessions expose materially fewer tools before evidence:
  complete.
- Gated tools remain registered in catalogs and operation sets: complete.
- Go, Markdown, Docker/code, Loki, MySQL, browser, image, and memory mutation
  activate from evidence assertions: complete.
- Rich evidence remains available to context/explainability paths: complete.
- Project inventory is no longer a second "signal" concept: complete.
- `core/environment` no longer owns observation/assertion shapes: complete.
- First-party code no longer imports `runtime/environment`: complete.
- Verification: `go test ./...` and `task verify` passed.

## Follow-Up Disposition

No additional core concept is needed right now. The follow-up items from this
plan have been handled as follows:

- `/env/explain` now includes unmatched reaction rules with the expected
  assertion/subject and a reason such as `no_assertions` or
  `no_matching_assertion`.
- Turn-intent derivation was removed for tool projection. Browser, image, and
  memory mutation tools now activate from stable availability/configuration
  evidence so the model-facing tool set does not vary by prompt.
- Reaction conjunctions remain intentionally out of scope. Stable availability
  assertions cover browser, image, and memory gating without adding matcher
  complexity.
- Memory retrieval remains baseline/low-noise where configured; memory mutation
  is gated only by memory-store availability.
