# DESIGN: Transactional Tool Continuity

## Status

Draft design plan.

## Problem

Provider-visible conversations have a strict continuity invariant around tool use:

```text
assistant tool call -> exactly one terminal tool result
```

Today the runtime can persist these pieces across separate phases:

1. model transcript items, including assistant tool calls;
2. semantic agent decision events;
3. operation requested/completed events;
4. provider-visible tool result transcript items;
5. continuation handles.

Those phases are not committed as one coherent unit. In particular, operation results can be durable as semantic events while the corresponding provider-visible `tool_result` transcript item is still only in memory awaiting the next model call. A crash or append failure in that window can leave provider replay with open tool calls or orphan tool results.

Replay-time repair can make some malformed transcripts provider-acceptable, but that is a compatibility fallback, not a correctness model. The correct model should make continuity a write-time and recovery-time invariant.

## Goals

- Make provider transcript continuity durable by construction.
- Ensure every durable assistant tool call reaches one durable terminal provider-visible result.
- Avoid replay-time synthetic assistant calls during normal operation.
- Support crash recovery without duplicating non-idempotent side effects.
- Keep provider transcript items, semantic operation events, and continuation handles coherent.
- Preserve layer boundaries: core defines inert event/spec shapes, runtime implements projection/reconciliation/storage logic, orchestration owns session flow, adapters only render provider wire formats.

## Non-goals

- Add a delegated `map`/fan-out API.
- Make tool execution parallel in this design. The design should not preclude it, but current execution can remain sequential.
- Solve idempotency for every existing operation immediately. The design should expose operation state clearly enough to make safe policy decisions.
- Keep replay repair as a primary path. Repair may remain for legacy/migration histories only.

## Current Behavior Summary

For a turn where the model streams two tool calls:

```text
1. InputReceived is appended.
2. Pending user input is projected into the model transcript.
3. Model streams assistant tool calls call_1 and call_2.
4. Model response returns operations plus transcript items.
5. llmagent emits ItemsAppended for sent input and assistant tool-call items.
6. conversationEventSink appends those transcript items to the thread event log.
7. AgentStepCompleted is appended.
8. applyAgentOperations executes operations synchronously and sequentially:
   a. OperationRequested(call_1) append
   b. execute call_1
   c. OperationCompleted(call_1) append
   d. build provider-visible tool_result(call_1) in memory
   e. OperationRequested(call_2) append
   f. execute call_2
   g. OperationCompleted(call_2) append
   h. build provider-visible tool_result(call_2) in memory
9. pending = [tool_result(call_1), tool_result(call_2)]
10. On the next model request, pending tool results are sent.
11. After that model response, the sent tool results are persisted via ItemsAppended.
```

Exception: if the next loop iteration would hit the step budget, the session currently persists pending tool results before exiting.

## Continuity Failure Windows

### Model and transcript phase

- Model streams tool calls, then process crashes before final transcript items are persisted.
- Model stream fails after partial tool-call arguments.
- Model returns operations but `ItemsAppended` persistence fails.
- Assistant calls are persisted but `AgentStepCompleted` persistence fails, leaving open calls with no execution record.
- Continuation handle persists without matching transcript items, or transcript items persist without matching continuation handle.
- Local in-memory transcript diverges from durable transcript after append failure.

### Operation phase

- `OperationRequested` persists, then process crashes during execution.
- Operation side effect happens, then `OperationCompleted` persistence fails.
- `OperationCompleted` persists, then process crashes before provider-visible `tool_result` is persisted.
- With multiple calls, the first result may be semantically durable while the second is pending/running/unknown.
- Tool result replacement or provider encoding fails after the operation already completed.
- Provider call ID is missing or mismatched, so the result cannot match the assistant call.

### Replay/projection phase

- Full replay sees assistant calls without results.
- Full replay sees tool results without matching calls due to legacy corruption or earlier bugs.
- Multiple tool calls in one provider item are not normalized consistently for providers that require one call per replay item.
- Ephemeral repair artifacts get included in durable `NewItems` and become visible as real tool calls.

## Design Invariants

### Provider continuity invariant

For every durable provider-visible assistant tool call item:

```text
there must eventually be exactly one durable provider-visible terminal tool result item with the same provider call id.
```

Terminal result kinds include:

- success;
- operation failed;
- operation rejected by policy/safety;
- operation canceled;
- operation outcome unknown after crash;
- operation could not be rendered for the model.

### No execution without durable call

An operation derived from a provider tool call must not execute until the assistant tool-call transcript item is durable.

### No completed operation without durable result

After an operation reaches a terminal semantic state, the corresponding provider-visible tool result must be appended durably before the session advances to the next model decision.

### No duplicate side effects by default

After recovery, incomplete operation executions must not be silently retried unless the operation is explicitly resumable/idempotent under policy.

### Repair is not normal control flow

Replay-time orphan repair may exist only for legacy data or emergency tolerance. Normal session flow and recovery must produce structurally valid transcripts without synthetic assistant repair calls.

## Proposed Architecture

### 1. Add a transactional conversation commit boundary

Introduce a single append operation for coherent conversation changes. Conceptually:

```go
type Commit struct {
    TurnID       string
    Provider     conversation.ProviderIdentity
    Items        []conversation.Item
    Continuation *conversation.ContinuationHandle
    Diagnostics  []conversation.Diagnostic
}
```

This can initially be implemented as a runtime/orchestration helper around existing event types, but the API should express one logical commit.

Commit rules:

- items and continuation are committed together when both are present;
- local transcript is updated only after durable append succeeds;
- failed commits do not mutate local transcript state;
- commit diagnostics are durable but not provider-visible transcript items.

Layering:

- core/conversation: inert commit/event shapes if needed;
- runtime/conversation: validation/reconciliation helpers;
- orchestration/session: invokes commits at session boundaries;
- adapters: no transaction ownership.

### 2. Persist assistant tool calls before operation execution

The current order mostly does this via model response artifacts. Make it explicit:

```text
model response received
-> commit provider transcript items containing assistant tool calls
-> append AgentStepCompleted
-> execute operations
```

If the transcript commit fails, do not execute tools.

If `AgentStepCompleted` fails after transcript commit, close the durable tool calls with terminal canceled/error tool results before returning, if possible.

### 3. Persist provider-visible tool results immediately after each terminal operation

Change the operation loop so each operation terminal state commits its provider-visible result immediately.

Desired order for each call:

```text
OperationRequested durable
execute operation
OperationCompleted durable
commit tool_result durable
only then move to the next model decision
```

For two sequential calls:

```text
assistant calls committed: call_1, call_2
AgentStepCompleted committed
OperationRequested(call_1)
execute call_1
OperationCompleted(call_1)
commit tool_result(call_1)
OperationRequested(call_2)
execute call_2
OperationCompleted(call_2)
commit tool_result(call_2)
next model request uses already-durable tool results as pending input
```

This removes the current crash window where `OperationCompleted` is durable but the provider-visible tool result is only in memory.

### 4. Distinguish pending-to-send from pending-to-persist

Projection currently uses `NewItems` ambiguously. Introduce explicit semantics:

```text
Items: provider input to send now
NewItems: real provider-visible items newly sent and not yet durable
AlreadyCommittedItems: provider-visible items to send now but already durable
EphemeralItems: provider-only compatibility items, never durable
```

A smaller initial version:

```go
type PendingTranscript struct {
    Items            []conversation.Item
    AlreadyCommitted bool
}
```

When tool results are committed immediately after operation completion, the next model call still sends them, but the model response must not persist them a second time.

### 5. Add a recovery reconciler

Before projection, and optionally at session startup, reconcile durable semantic operation events with durable provider transcript items.

Rules:

1. Assistant call exists and matching result exists: OK.
2. Assistant call exists and `OperationCompleted` exists but transcript result missing: synthesize result item from `OperationCompleted`, then commit it.
3. Assistant call exists and `OperationRequested` exists but no completion: commit terminal `operation_unknown` or `operation_canceled` result unless operation policy says it can resume.
4. Assistant call exists and no operation request exists: commit terminal `operation_not_started` result.
5. Tool result exists without assistant call: mark legacy/corrupt diagnostic; do not create synthetic assistant calls during normal operation.

The reconciler should be deterministic and idempotent.

### 6. Add operation execution leases

To avoid duplicate side effects after crash, record operation lifecycle explicitly:

```text
requested -> leased/running -> completed | failed | canceled | unknown
```

Recovery behavior:

- resumable/idempotent operation: may resume or retry under policy;
- non-idempotent unknown operation: do not retry silently; close provider call with `operation_unknown` and surface diagnostic;
- canceled context/shutdown: close with `operation_canceled` if execution did not complete.

This can start as metadata on existing `OperationRequested`/`OperationCompleted` events, then evolve into explicit operation lifecycle events if needed.

### 7. Make provider call IDs mandatory for tool-call operations

For operations derived from model tool calls, require:

- `ProviderCallID` non-empty;
- `ProviderCallType` set when provider rendering needs it;
- exact match between assistant tool-call item and result item.

If an adapter cannot provide a provider call ID, the operation should not execute. Instead, commit a model/adapter error response or fail the turn before side effects.

### 8. Keep repair artifacts ephemeral

Replay repair output must never be written through `NewItems` or shown as normal tool calls.

Immediate rule:

```text
metadata["repair"] != "" implies provider-request-only or diagnostic-only; never execute, never persist as normal history.
```

Long-term rule: remove orphan tool-result repair from normal projection after reconciliation is in place.

## Proposed Implementation Plan

The full reliability model is large. Implement it in slices that preserve the final architecture instead of introducing temporary semantics. Each slice must move the system toward atomic durable transitions, explicit lifecycle state, and replay-valid canonical transcript history.

### Slice 1: Same-thread transactional conversation commits

Goal: make provider-visible conversation writes atomic within the active thread stream and prevent local/durable divergence.

Introduce one logical commit boundary for provider-visible conversation state. It may append existing event types, but callers must use one API rather than mutating transcript state piecemeal.

Commit responsibilities:

- append transcript items, continuation handles, and diagnostics as one logical thread append;
- update local in-memory transcript/continuation only after durable append succeeds;
- keep diagnostics separate from provider-visible transcript items;
- reject or quarantine `metadata["repair"]` items from normal durable history;
- validate that the commit itself does not introduce impossible provider-visible structure.

Candidate API:

```go
type ConversationCommit struct {
    TurnID       string
    Provider     conversation.ProviderIdentity
    Items        []conversation.Item
    Continuation *conversation.ContinuationHandle
    Diagnostics  []conversation.Diagnostic
}
```

Implementation notes:

- Use one same-thread append for all records in the commit.
- Put inert commit/diagnostic shapes in `core/conversation` only if they become durable event payloads.
- Put validation and reconciliation helpers in `runtime/conversation`.
- Put append orchestration in `orchestration/session`, because it knows the active thread, turn, and event store.
- Keep adapters out of commit ownership; adapters only render and parse provider wire shapes.

Acceptance:

- Conversation append failure does not mutate local transcript state.
- Model response transcript items and continuation handles cannot be partially committed from the session point of view.
- Repair artifacts are never appended as ordinary durable assistant calls during normal operation.
- Existing thread/event storage guarantees all records in the same commit are durable together or not durable at all.

### Slice 2: Atomic terminal operation transition

Goal: close the main correctness gap without yet building the full lease/recovery framework.

Change the operation path so terminal semantic completion and the matching provider-visible tool result are one same-thread atomic append.

Target flow:

```text
commit assistant tool-call transcript items
append AgentStepCompleted
for each operation:
    append OperationRequested
    execute operation synchronously
    atomic append:
        OperationCompleted
        ItemsAppended(tool_result)
continue to next model decision using already-durable tool results
```

For two sequential calls:

```text
commit assistant call_1 + call_2
append AgentStepCompleted
append OperationRequested(call_1)
execute call_1
atomic append OperationCompleted(call_1) + tool_result(call_1)
append OperationRequested(call_2)
execute call_2
atomic append OperationCompleted(call_2) + tool_result(call_2)
next model request sends call_1/call_2 results, but does not append them again
```

Required model changes:

- distinguish provider input items that still need durable append from provider input items already committed;
- make `ProviderCallID` mandatory for model-derived tool operations;
- validate provider-call type before committing a result item;
- store provider call index/order so projection is independent of execution completion timing;
- avoid duplicate transcript appends when already-durable tool results are sent to the next model call.

Acceptance:

- No terminal operation result is only in memory.
- Tool execution never starts until its assistant tool-call item is durable.
- `OperationCompleted` and matching provider-visible `tool_result` cannot be split by a crash within the same thread stream.
- The next model decision sees all completed tool results.
- Tool results are not appended twice.

### Slice 3: Minimal recovery reconciler for synchronous operations

Goal: handle crash/restart without full leases while preserving safe semantics.

Add an idempotent recovery pass before projection/session continuation for the current synchronous operation model.

Recovery rules:

1. Assistant call plus matching durable tool result: no-op.
2. Assistant call plus durable `OperationCompleted` but missing transcript result: derive and commit the real provider-visible tool result. This should only occur for legacy/pre-slice-2 history or storage corruption.
3. Assistant call plus `OperationRequested` but no completion: commit terminal `operation_unknown` and block autonomous continuation unless operation policy explicitly permits continuation.
4. Assistant call with no operation request: commit terminal `operation_not_started`.
5. Tool result without assistant call: record a corruption/legacy diagnostic; do not create a synthetic assistant tool call in normal mode.

After this slice:

- disable normal orphan-tool-result synthetic assistant repair;
- keep legacy repair only behind explicit read-only migration/corruption inspection mode;
- make compaction refuse open tool-call boundaries unless an explicit recovery operation first closes them.

Acceptance:

- Crash after any single same-thread append/execution boundary can recover to provider-valid history.
- Reconciliation is deterministic and idempotent.
- Normal current-version sessions do not rely on `orphan_tool_result` repair.
- Unknown non-idempotent outcomes are not retried automatically.

### Slice 4: Full operation lifecycle, leases, and resumability

Goal: generalize from synchronous local execution to robust operation lifecycle management.

Add explicit lifecycle state and policy:

```text
requested -> leased/running -> completed | failed | canceled | unknown
```

Required capabilities:

- durable execution lease/running state;
- heartbeat or lease expiry for long-running/background operations;
- operation capability metadata for idempotency, resumability, and recovery confirmation;
- idempotency keys for retryable operations;
- recovery policy that can distinguish `not_started`, `possibly_running`, `interrupted`, `completed`, and `unknown`.

Acceptance:

- Long-running or background operation recovery is deterministic.
- Non-idempotent unknown operations are never retried automatically.
- Retry/resume is only allowed when the operation declares the capability and supplies the required idempotency key.
- Provider-visible transcript continuity remains valid across restart, cancellation, and lease expiry.

### Slice 5: Cross-stream event transactions, if operation state moves out of the thread stream

Goal: preserve the same atomicity if future storage splits thread transcript, operation lifecycle, indexes, or external operation state into separate streams.

Requirement:

- the store must support atomic append across all streams that participate in one logical transition;
- optimistic concurrency checks must cover every touched stream;
- partial success across streams is not allowed.

This slice is unnecessary if the relevant conversation and operation lifecycle records remain in the same thread stream. It becomes mandatory before moving any participant of the atomic transition to another stream.

Acceptance:

- `OperationCompleted` and provider-visible `tool_result` remain one atomic transition even if stored in separate streams.
- Recovery never has to compensate for partial storage success across streams.

## Decisions and Remaining Open Questions

### 1. Conversation commit event vs helper

Options:

- **A. New core event:** add a durable `ConversationCommitted` event carrying items, continuation, and diagnostics.
- **B. Helper over existing events:** keep `ItemsAppended` and `ContinuationStored`, but only write them through a commit helper.
- **C. Event-store transaction:** extend the store to atomically append multiple existing event records.

Recommendation: make **C** the architectural requirement from the beginning: the storage contract must support an atomic append of all records that make up one logical conversation/operation transition. Use existing event types inside that transaction rather than replacing them with a monolithic event. A new `ConversationCommitted` event is only justified if it represents a distinct domain fact; it must not be used as a shortcut around atomic append semantics. The fundamental rule is: either every record in the logical transition is durable, or none of them are.

### 2. Tool result commit before or after `OperationCompleted`

Options:

- **A. OperationCompleted first, then transcript result.** Semantic completion is source of truth; recovery can reconstruct missing transcript result.
- **B. Transcript result first, then OperationCompleted.** Provider continuity improves first, but semantic operation history can lag.
- **C. One logical batch containing both.** Best final shape when the event store supports it.

Recommendation: require **C** as the correctness model: `OperationCompleted` and the matching provider-visible `tool_result` are one logical atomic transition. Within the transaction, record semantic completion first and provider transcript result second, because the transcript result is derived from the semantic completion. Avoid both standalone **A** and standalone **B** as final behavior; either ordering leaves a crash-visible contradiction unless recovery is compensating for a lower-level storage failure.

### 3. Non-idempotent unknown operation outcomes

Options:

- **A. Return `operation_unknown` to the model and require human/user follow-up if needed.**
- **B. Fail the whole session and block further model continuation.**
- **C. Ask for clarification before committing a terminal tool result.**
- **D. Retry automatically.**

Recommendation: commit a terminal provider result with `operation_unknown`, then block autonomous continuation unless policy explicitly says the operation is safe to continue after an unknown outcome. This combines protocol continuity with safety. The model must not be allowed to assume success, failure, or retryability. Use human/user clarification for operations marked `requires_recovery_confirmation`. Never retry a non-idempotent unknown operation automatically. A session-level failure may be surfaced to the user, but the provider transcript still needs a terminal result item if the conversation can ever be replayed.

Recommended result content:

```json
{
  "code": "operation_unknown",
  "message": "The operation was interrupted before its final outcome could be recorded. It was not retried automatically.",
  "details": {
    "operation": "...",
    "call_id": "...",
    "retry_policy": "not_retried_non_idempotent"
  }
}
```

### 4. Ordering for multiple tool results

Options:

- **A. Preserve provider call order from assistant output.**
- **B. Preserve operation completion order.**
- **C. Provider-specific ordering.**

Recommendation: use **A** as the canonical transcript order because it preserves causal order from the assistant message. Store an explicit provider call index on each operation/result so ordering is independent of execution timing. If execution becomes parallel, completion events may occur in completion order, but provider-visible result projection must remain in assistant call order unless a provider contract explicitly requires a different order. Provider-specific deviations must be encoded at the adapter boundary, not in the core lifecycle model.

### 5. Streaming partial tool-call durability

Options:

- **A. Persist only complete assistant tool-call items after model response finalization.**
- **B. Persist partial streaming tool-call starts/deltas.**
- **C. Persist partials as diagnostics/telemetry, never as replay transcript.**

Recommendation: **A** for canonical replay transcript. A durable provider transcript should contain only validated, complete semantic items. Partial stream deltas may be persisted under **C** as telemetry/live UI diagnostics, but they must not be replay inputs, operation triggers, or continuity anchors. Avoid **B** because partial tool calls are not stable facts: they may be superseded, malformed, or incomplete.

### 6. Compaction with open calls

Options:

- **A. Refuse compaction while any tool call is open.**
- **B. Auto-close open calls with terminal repair/error results before compacting.**
- **C. Compact open calls as-is.**

Recommendation: **A** as the normal invariant: compaction must only run over a closed, replay-valid transcript. **B** is allowed only as an explicit recovery operation that first writes real terminal result events, then compacts the now-closed transcript. Never **C**. Compaction must not hide or summarize away an unresolved protocol boundary.

### 7. Operation leases and retries

Options:

- **A. Minimal lifecycle with requested/completed only, plus recovery closes unknowns.**
- **B. Add explicit leased/running heartbeat events.**
- **C. Require idempotency keys and resumability declarations for retryable operations.**

Recommendation: design for **B and C** as the correct model. A durable operation lifecycle needs an execution lease/running state so recovery can distinguish `not started`, `possibly running elsewhere`, `interrupted`, and `completed`. Retry/resume requires explicit operation capability metadata plus an idempotency key. The minimal requested/completed lifecycle is acceptable only for operations that are guaranteed local, synchronous, and non-resumable; it should not be the general abstraction.

### 8. What to do with legacy corrupt histories

Options:

- **A. Continue replay-time synthetic orphan repair globally.**
- **B. Gate synthetic repair behind explicit legacy/corruption mode.**
- **C. Run a migration/reconciliation pass and then reject remaining corrupt histories.**

Recommendation: **C** as the target behavior: run reconciliation/migration for histories that can be repaired from semantic facts, and quarantine or reject histories that remain structurally corrupt. Synthetic orphan repair should not be part of normal projection. If kept at all, it should be an explicit read-only legacy inspection mode, not a path that writes new durable transcript items or drives execution.

## Success Criteria

- Provider replay never needs synthetic assistant calls for current-version normal operation.
- Crash after any single append/execution boundary can recover to a provider-valid transcript.
- Every durable assistant tool call has exactly one terminal durable tool result.
- Tool execution never starts unless its provider call is durable.
- A terminal operation result is never only in memory.
- Repair diagnostics are inspectable but not executable or user-visible as normal tool calls.
