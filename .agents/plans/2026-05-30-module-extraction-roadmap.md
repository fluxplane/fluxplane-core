# Shared Module Extraction Roadmap

Last reviewed: 2026-05-30.

## Goal

Continue splitting reusable concepts out of `fluxplane-core` without creating a
new `common` dumping ground. Each extracted module must have a clear concept
boundary, no dependency on the engine, and at least one plausible non-engine
consumer such as dex, browser, app authoring tools, bridge code, or future
Fluxplane apps.

## Migration Order

Near-term priority:

```text
1. fluxplane-policy
2. fluxplane-operation
3. fluxplane-datasource / fluxplane-secret / fluxplane-context as justified
4. fluxplane-app at the very end
```

`fluxplane-app` should be late because app manifests hold the most dependencies
and will become easier to extract after policy, operation, datasource, user, and
other reusable concepts are already separate.

## Already Started or Planned Foundation

```text
fluxplane-event       generic event contracts
fluxplane-system      primitive host/system capabilities
fluxplane-user        canonical users, identities, groups, actors
fluxplane-policy      policy vocabulary and pure evaluation
```

`fluxplane-policy` is the immediate implementation target. Create it as a
standalone module first, do not wire it into core yet, and preserve current
policy semantics exactly.

## Strong Candidates

### `fluxplane-policy`

Purpose: answer "may this subject do this action on this resource?"

Scope:

- caller, trust, scopes, sensitivity;
- invocation policy;
- authorization subjects/resources/actions/grants;
- authorization request/context;
- pure evaluators.

Keep out:

- operation projection behavior;
- approval workflow;
- event emission policy;
- runtime side-effect enforcement;
- app manifest parsing;
- concrete user-to-subject mapping.

Risk/churn: medium to high because policy gates operation projection and
execution. Extract behind aliases before rewiring core.

### `fluxplane-operation`

Purpose: model a callable capability/tool contract.

Potential scope:

- operation refs and specs;
- schema and safety metadata;
- inert input/output descriptors;
- access descriptors and access field helpers;
- registry contracts if stable.

Likely dependency:

```text
fluxplane-operation -> fluxplane-policy
```

Keep out:

- execution runtime;
- model tool projection internals;
- approval flow;
- concrete filesystem/process/network/browser operations.

Risk/churn: high. Start with inert spec/access types only after policy is stable.

### `fluxplane-datasource`

Purpose: model searchable/gettable external data.

Potential scope:

- datasource specs and provider contracts;
- entity descriptors;
- records and relationships;
- search/get/list request and result DTOs;
- pure access descriptors if stable.

Keep out:

- semantic/vector indexing implementation;
- local mirror store implementation;
- plugin-specific fetchers;
- runtime operation handlers.

Risk/churn: medium-high because datasource mixes pure contracts with indexing and
runtime behavior today.

### `fluxplane-secret`

Purpose: model secret declarations, requests, and opaque handles.

Potential scope:

- secret refs/handles;
- auth method metadata;
- secret request/scope structs;
- pure validation helpers.

Keep out:

- env-backed resolver implementation;
- credential stores;
- OAuth flows;
- engine authorization wrappers.

Risk/churn: medium, but security-sensitive. Keep first slice small and inert.

### `fluxplane-context`

Purpose: model context provider materialization and sensitivity-marked context
blocks.

Potential scope:

- context blocks;
- placement;
- provider refs/metadata;
- render fingerprints/state;
- sensitivity on context content.

Likely dependency:

```text
fluxplane-context -> fluxplane-policy
```

Risk/churn: medium. Useful if plugins/dex contribute context outside the engine.

### `fluxplane-app`

Purpose: model authored Fluxplane app manifests.

Potential scope:

- `app.Spec`;
- source specs;
- plugin refs;
- identity/security placement;
- datasource/model/discovery config;
- validation helpers;
- schema-generation-friendly DTOs.

Likely dependencies after other splits:

```text
fluxplane-app -> fluxplane-user
fluxplane-app -> fluxplane-policy
fluxplane-app -> fluxplane-operation? / fluxplane-datasource? as needed
```

Risk/churn: high. Extract at the very end because it holds the most dependencies
and has external manifest compatibility risk.

## Medium or Later Candidates

### `fluxplane-channel`

Could own inert inbound/outbound channel DTOs, caller/trust/actor attachments,
and recipient/message shapes. Keep dispatch/session logic in core.

Risk: medium. Extract only if non-core adapters need channel contracts.

### `fluxplane-session`

Could own session/thread/run specs and event DTOs. This is close to orchestration
and replay, so it should wait for a second consumer and a stable model.

Risk: high.

### `fluxplane-task`

Could own durable task/step/artifact lifecycle contracts, but task semantics are
still product/runtime-heavy. Extract later only when the model is stable.

Risk: high.

### `fluxplane-model`

Could own provider/model refs, capabilities, and model policy if a clean inert
contract emerges. Keep provider adapters and catalog bridges out.

Risk: medium. Priority: lower.

### `fluxplane-memory`

Could own memory records/scopes/subjects/queries, but current memory semantics
are likely engine/product-shaped. Extract only when multiple apps need the same
contract.

Risk: medium. Priority: lower.

## General Guideline

Avoid broad modules named `fluxplane-common`, `fluxplane-coretypes`, or
`fluxplane-contracts`. They recreate the same coupling under a different name.

Each extracted module should answer one clear question:

- `event`: what is an event?
- `system`: how do we talk to host capabilities?
- `user`: who is the actor?
- `policy`: may this subject do this action on this resource?
- `operation`: what is a callable capability?
- `datasource`: what is searchable/gettable external data?
- `secret`: how is a secret referenced/requested without exposing it?
- `context`: what context is materialized for a turn?
- `app`: what is an authored Fluxplane app?

If the module name cannot be explained that way, it probably should not exist
yet.
