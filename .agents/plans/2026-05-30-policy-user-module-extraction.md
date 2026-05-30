# Policy and User Module Extraction Plan

Last reviewed: 2026-05-30.

## Goal

Continue the Fluxplane module split by extracting the stable identity and policy
concept models currently embedded in `fluxplane-core` into reusable sibling
modules:

```text
github.com/fluxplane/fluxplane-user
github.com/fluxplane/fluxplane-policy
```

The extraction should let `fluxplane-core`, `fluxplane-dex`, future bridges, and
other Fluxplane apps share identity and authorization vocabulary without making
those consumers depend on the engine.

This is concept-model extraction, not an enforcement rewrite. The shared modules
should define inert models and pure evaluation helpers. `fluxplane-core` remains
responsible for runtime wiring, operation projection, approval flows, app
manifest loading, event emission policy, and side-effect enforcement.

## Current Problem

`fluxplane-core/core/user` and `fluxplane-core/core/policy` contain concepts that
are broader than the engine:

- canonical users, external identities, groups, actors, identity matching, and
  group rules;
- caller, trust, sensitivity, invocation policy, authorization subjects,
  resources, actions, grants, requests, decisions, and pure evaluation helpers.

These are useful outside the engine, especially for dex/plugin hosts and future
bridge modules. Keeping them inside `fluxplane-core` forces consumers either to
import the engine or duplicate policy and identity shapes.

The current fanout is larger for policy than user:

- `core/app` embeds `policy.AuthorizationPolicy` and `user` identity specs in
  app manifests.
- `core/channel`, `core/session`, `core/resource`, `core/context`, and
  `core/memory` use policy/user types in stable event and context shapes.
- `runtime/operation` uses policy action/resource descriptors for operation
  access checks and projection filtering.
- engine authorization wrappers use policy contexts and emit policy decision
  evidence.

That makes `fluxplane-user` a cleaner first extraction and `fluxplane-policy` a
higher-churn extraction that should use compatibility aliases first.

## Target Module Graph

Target dependency direction:

```text
fluxplane-event
  no engine dependency

fluxplane-system
  no engine dependency

fluxplane-user
  no engine dependency

fluxplane-policy
  no engine dependency
  optionally depends on fluxplane-event later if shared policy events move

fluxplane-core
  depends on event, system, user, policy

fluxplane-dex
  may depend on system, user, policy
  must not depend on core

future engine-dex bridge
  depends on core + dex + shared modules
```

Hard rules:

```text
shared modules <- {core, dex, browser, future apps}
shared modules must not import core
```

Avoid this dependency initially:

```text
fluxplane-policy -> fluxplane-user
```

Policy subjects should stay generic (`SubjectRef`) so policy can represent users,
groups, services, systems, agents, plugin instances, API keys, and future subject
kinds without importing the user model. Core can map a resolved `user.Actor` into
policy subjects.

## `fluxplane-user` Scope

`fluxplane-user` owns identity and actor modeling only.

Initial package shape:

```text
user          canonical users, groups, identities, actors
usertest      optional resolver/test helpers later
```

Initial moved concepts from `core/user`:

- `ID`
- `User`
- `Email`
- `Identity`
- `Group`
- `IdentityMatch`
- `GroupRule`
- `Actor`
- `TrustLevel`
- `TrustPublic`
- `TrustInternal`
- `TrustOperator`
- `ResolutionState`
- `ResolutionUnresolved`
- `ResolutionResolved`
- `NormalizeTrust`
- `Min`
- `Max`
- `NormalizeResolution`

Keep out of `fluxplane-user` initially:

- channel-specific identity lookup;
- Slack/GitLab/Jira/etc identity enrichment;
- app manifest loading;
- persistent account storage;
- policy subject conversion;
- authorization decisions;
- event emission.

The module should answer: "Who is this person, identity, group, or resolved
actor?" It should not answer: "What may they do?"

## `fluxplane-policy` Scope

`fluxplane-policy` owns policy vocabulary and pure evaluation.

Initial package shape:

```text
policy        decisions, trust, sensitivity, invocation and authz evaluation
policytest    optional helpers later
```

Initial moved concepts from `core/policy/policy.go`:

- `CallerKind`
- `CallerUser`
- `CallerAgent`
- `CallerSystem`
- `Principal`
- `Caller`
- `Scope`
- `TrustLevel`
- `TrustUntrusted`
- `TrustVerified`
- `TrustPrivileged`
- `TrustSystem`
- `TrustKind`
- `TrustInvocation`
- `TrustSource`
- `TrustTarget`
- `Trust`
- `Sensitivity`
- `SensitivityPublic`
- `SensitivityInternal`
- `SensitivityRestricted`
- `SensitivityConfidential`
- `SensitivitySecret`
- `NormalizeSensitivity`
- `InvocationPolicy`
- `Decision`
- `DecisionAllow`
- `DecisionDeny`
- `DecisionApprovalRequired`
- `Evaluation`
- `EvaluateInvocation`
- `TrustSatisfies`

Initial moved concepts from `core/policy/authorization.go`:

- `Action` and existing action constants;
- `SubjectKind` and existing subject constants;
- `SubjectRef`;
- `ResourceKind` and existing resource constants;
- `ResourceRef`;
- `Grant`;
- `AuthorizationPolicy`;
- `AuthorizationRequest`;
- `AuthorizationContext`;
- `ContextWithAuthorization`;
- `AuthorizationFromContext`;
- `EvaluateAuthorization`.

Preserve current semantics exactly during the move:

- empty `AuthorizationPolicy` means no authorization policy is configured;
- calling `EvaluateAuthorization` with an empty grant list returns deny with
  reason `no_grants`;
- configured policies are default-deny;
- subject matching is by kind and wildcard-capable ID;
- action matching supports exact, `*`, and `prefix.*`;
- resource fields are wildcard-capable and empty fields mean unconstrained;
- path matching supports exact, `**`, `prefix/**`, and `path.Match` patterns;
- missing sensitivity defaults to `restricted`;
- trust rank ordering remains `system > privileged > verified > untrusted`.

Keep out of `fluxplane-policy` initially:

- operation projection behavior;
- approval request/grant workflows;
- terminal/audit rendering;
- event emission policy;
- app manifest parsing;
- channel/listener trust downgrade logic;
- concrete mapping from `user.Actor` to policy subjects;
- engine-specific default policies.

## Concept Boundary Between User and Policy

Keep the two trust vocabularies separate:

```text
user.TrustLevel:
  public/internal/operator

policy.TrustLevel:
  untrusted/verified/privileged/system
```

They answer different questions:

- user trust: how much user-visible context a person/group may receive;
- policy trust: how much authority an invocation has.

Core may map between them where appropriate, but the shared modules should not
merge them.

Suggested core-owned mapping:

```text
user.Actor
  -> policy.SubjectRef{Kind: user, ID: actor.User.ID}
  -> policy.SubjectRef{Kind: group, ID: group.ID}
```

Do not put this mapping in `fluxplane-policy` in the first slice. It would create
an unnecessary `policy -> user` dependency and bake engine interpretation into a
shared module.

## What Remains Engine-Owned

`fluxplane-core` remains the runtime/engine and continues to own:

- app manifest `Spec` and validation;
- `app.IdentitySpec` and `app.Security` placement in manifests;
- identity resolver wiring and provider enrichment;
- channel/listener-derived caller/trust downgrades;
- current-user context provider rendering;
- operation access descriptor extraction;
- model tool projection filtering;
- operation execution gates;
- approval workflows;
- authorization decision event emission policy;
- conversion from resolved actors to authorization subjects;
- default local/dev security policies;
- engine resource/action conventions not yet proven stable outside core.

The shared policy module should evaluate. Core should enforce.

## Phase 0: Inventory and Type Map

Create a type map before moving code.

Output table:

```text
symbol | current package | target owner | move/alias/leave | import fanout | schema impact | risk
```

Sources to inspect:

- `core/user`
- `core/policy`
- `core/app`
- `core/channel`
- `core/session`
- `core/context`
- `core/memory`
- `core/resource`
- `runtime/operation`
- engine authorization wrappers around system/network/process/workspace access
- app schema generation and manifest tests
- Slack/GitLab identity enrichment paths

Acceptance checks:

- no planned import cycle;
- no manifest JSON/YAML field rename;
- no behavior change in policy evaluation;
- no behavior change in identity resolution defaults.

## Phase 1: Extract `fluxplane-user`

Start with user because it is the cleaner, lower-risk concept split.

Work:

1. Create `github.com/fluxplane/fluxplane-user` as a sibling module.
2. Move current `core/user` contents almost unchanged.
3. Copy `core/user` tests to the new module.
4. Keep `fluxplane-core/core/user` as a temporary compatibility package with
   type, const, and function aliases.

Expected compatibility shape:

```go
package user

import fpuser "github.com/fluxplane/fluxplane-user"

type ID = fpuser.ID
type User = fpuser.User
type Group = fpuser.Group
type Actor = fpuser.Actor
type Identity = fpuser.Identity
type Email = fpuser.Email
type TrustLevel = fpuser.TrustLevel
type ResolutionState = fpuser.ResolutionState

const (
	TrustPublic   = fpuser.TrustPublic
	TrustInternal = fpuser.TrustInternal
	TrustOperator = fpuser.TrustOperator
)
```

5. Keep core imports untouched initially.
6. Run focused tests.
7. Gradually rewrite imports from
   `github.com/fluxplane/fluxplane-core/core/user` to
   `github.com/fluxplane/fluxplane-user` in small package slices.

Focused tests:

- `core/user`
- `core/app`
- app manifest schema tests
- identity resolver packages
- Slack identity resolution/enrichment tests
- GitLab identity enrichment tests
- current identity context provider tests
- memory store tests that scope by `user.ID`

Acceptance checks:

- app manifest identity JSON remains byte/shape compatible;
- user trust and resolution defaults are unchanged;
- no core import from `fluxplane-user` back into core;
- no committed module-level `replace` directives after module tags exist.

Risk: low to medium.

Expected churn:

- imports;
- copied tests;
- schema references;
- package docs;
- aliases during transition.

## Phase 2: Extract Pure `fluxplane-policy`

Extract policy behind compatibility aliases before changing direct imports.

Work:

1. Create `github.com/fluxplane/fluxplane-policy` as a sibling module.
2. Move `core/policy/policy.go` and `core/policy/authorization.go` concepts.
3. Copy pure policy tests.
4. Keep `fluxplane-core/core/policy` as a temporary compatibility package with
   type, const, and function aliases.
5. Leave authorization decision event emission in core for the first slice.

Do not change semantics while moving code.

Focused tests:

- `core/policy`
- `core/app`
- `core/context`
- `core/memory`
- `core/channel`
- `core/session`
- `runtime/operation`
- operation projection/filtering tests
- operation authorization tests
- system/network/process/workspace authorization wrapper tests
- app manifest schema tests

Acceptance checks:

- `EvaluateInvocation` behavior unchanged;
- `EvaluateAuthorization` behavior unchanged;
- decision reasons unchanged;
- action/resource vocabulary unchanged;
- app manifest `security` shape unchanged;
- projected tools for downgraded/untrusted turns remain unchanged;
- operation execution gates remain unchanged;
- no dex/core dependency inversion.

Risk: medium to high.

Expected churn:

- many imports;
- compatibility aliases for many constants;
- tests comparing decision reasons;
- app schema generation;
- operation access descriptor code.

## Phase 3: Decide Policy Event Ownership

Authorization decision events are more subtle than pure policy evaluation because
they tie into engine observability and audit behavior.

Preferred first-slice decision:

```text
fluxplane-policy
  pure model + evaluation

fluxplane-core/core/policy
  aliases + engine-owned authorization decision event wrappers
```

Later option:

```text
fluxplane-policy -> fluxplane-event
```

Move shared policy event types only if dex or another non-engine consumer needs
the same audit event vocabulary. Even then, keep event emission policy in core.

Acceptance checks if events move later:

- terminal rendering still recognizes decision events;
- audit traces preserve `trace_allows` behavior;
- approval flows still emit requested/granted/denied evidence consistently;
- `fluxplane-policy` imports only `fluxplane-event`, never core.

Risk if moved early: high.

Recommendation: keep decision events core-owned until the pure policy extraction
is fully stable.

## Phase 4: Rewrite Core Imports in Small Slices

After compatibility aliases are green, rewrite direct imports package by package.

Suggested order:

1. Pure core data packages:
   - `core/context`
   - `core/memory`
   - `core/data`
   - `core/app`
   - `core/channel`
   - `core/session`
2. Runtime descriptors:
   - `runtime/operation/access.go`
   - `runtime/operation/authorization.go`
3. System/auth wrappers:
   - workspace authorization wrappers
   - network authorization wrappers
   - process authorization wrappers
   - secret authorization wrappers
4. App/launch config:
   - manifest parsing
   - schema generation
   - default policy assembly
5. Plugins:
   - native filesystem/process/network operations
   - datasource operations
   - integration operations

Each slice should be independently testable and should not combine import rewrites
with policy behavior changes.

## Phase 5: Update Dex and Bridge Code

After the shared modules are tagged and core has compatibility aliases:

- update dex/plugin host code to use `fluxplane-user` and `fluxplane-policy`
  directly where it currently duplicates or bridges identity/policy concepts;
- keep dex free of `fluxplane-core` imports;
- keep any engine-specific mapping in the bridge module/package;
- remove duplicate policy/user vocabulary from bridge code only after behavior is
  covered by tests.

Acceptance checks:

- dex remains standalone;
- bridge depends on dex + core + shared modules;
- shared modules do not import bridge, dex, or core.

## Phase 6: Remove Compatibility Packages Later

Only after core, dex, browser, examples, and bridge code import the shared modules
directly:

- deprecate or remove `core/user`;
- deprecate or remove pure `core/policy` aliases;
- keep any genuinely engine-owned policy event wrappers under a clearer engine
  package if necessary;
- update docs and changelog;
- tag shared modules;
- remove temporary `go.work` replaces once real versions are available.

Do not remove aliases in the same change that creates the modules. That would
maximize churn and make regressions hard to isolate.

## Risk and Churn Summary

| Area | `fluxplane-user` risk | `fluxplane-policy` risk | Notes |
|---|---:|---:|---|
| Pure type move | Low | Medium | Policy has far more import fanout. |
| App manifest compatibility | Medium | Medium/high | JSON/YAML shape must not change. |
| Runtime behavior | Low | High | Policy gates projection and execution. |
| Dex reuse | Medium | Medium | Useful only if no core imports leak. |
| Tests | Low | Medium/high | Authz decision reasons must be preserved. |
| Event/audit behavior | Low | High | Keep policy events in core initially. |
| Import churn | Medium | High | `policy` is used across core and runtime. |
| Conceptual stability | High | Medium | User is simpler; policy vocabulary may evolve. |

## Main Design Pitfalls

### Do not turn `fluxplane-user` into account storage

It should not own persistence, provider lookup, app manifest parsing, or Slack /
GitLab enrichment. Keep those in engine, dex, plugins, or adapters.

### Do not let `fluxplane-policy` absorb enforcement side effects

Shared policy should provide pure evaluators. Core should decide when and how to
enforce, emit events, request approval, and filter projections.

### Do not merge user trust and policy trust

The two trust vocabularies are intentionally different and should remain explicit.

### Do not create `policy -> user` too early

Keep `SubjectRef` generic. Map `user.Actor` to `[]policy.SubjectRef` in core or a
small adapter only after multiple consumers need it.

### Do not split resource/action vocabulary during the first extraction

The current policy vocabulary includes engine concepts such as datasource,
workspace, path, process, network, channel, task, session, model, operation, and
secret. Move them as-is first to preserve behavior. Revisit a separate
"standard Fluxplane resources" package only after the extraction is stable.

## Recommended First Implementation Slice

1. Create and test `fluxplane-user`.
2. Add `fluxplane-core/core/user` aliases.
3. Run focused identity/app tests.
4. Create and test `fluxplane-policy` with pure model/evaluation only.
5. Add `fluxplane-core/core/policy` aliases while keeping decision events
   engine-owned.
6. Run focused policy/runtime operation tests.
7. Rewrite imports in small slices.
8. Update dex/bridge consumers.
9. Remove aliases only after downstream users settle.
