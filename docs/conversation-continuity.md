# Conversation Continuity

Conversation continuity is the rule set that keeps provider-visible transcript
history valid across replay, native continuation, compaction, and tool
execution. The runtime must prevent malformed transcript state from being
written. It must not repair malformed replay after the fact.

## Transcript Sources

The runtime keeps two related histories:

- Semantic session events in `core/thread` describe channel input, agent
  decisions, operation execution, outbound messages, and runtime events.
- Provider transcript events in `core/conversation` record the exact items that
  a model provider saw or returned.

Provider adapters consume `core/conversation.Transcript`. They must not rebuild
provider-visible model history from semantic session summaries. The session
runtime projects transcript items with `runtime/conversation.Project`, using
full replay by default or a provider-native continuation handle when one is
valid for the current branch and provider.

## Tool-Call Invariant

Provider-visible tool calls are a strict pair:

1. An assistant output item opens one or more tool calls with stable provider
   call IDs.
2. Each opened call receives exactly one matching tool-result item.
3. Additional assistant tool-call output items may open more calls in the same
   group before results arrive.
4. No non-tool-call provider-visible item may appear while a tool call is open.
5. A tool result without a matching open assistant tool call is invalid.
6. Duplicate call IDs, duplicate results, missing call IDs, and durable repair
   artifacts are invalid.

The session runtime enforces this by validating model operation requests against
the assistant tool-call items emitted by the model step before executing
operations. Assistant tool-call items, operation completions, and matching
provider tool-result items are appended together after operation execution and
post-edit checks succeed, so replay cannot observe an open call or only the
result side of the pair.

Failed model steps do not persist partial provider transcript items. A failed
turn may still persist semantic failure events, but it must not create provider
history that a later replay would need to repair.

## Replay And Continuation

Full replay sends the projected canonical provider transcript plus the pending
new items. Native continuation sends the provider continuation handle and only
the pending delta items. In both modes, the canonical transcript must validate
before the adapter receives it.

Committed pending tool results are a special continuation case: the result is
already durable because operation execution committed it, but the next model
call still needs to send it as pending provider input. Projection tracks the
committed count so adapters do not record those items a second time.

## Compaction

Compaction stores a provider transcript checkpoint, not a semantic summary.
Checkpoint items must already satisfy the same continuity invariant as normal
replay. Compaction may shorten large tool-result content or omit old complete
tool-call groups, but it must not leave an unmatched tool call or orphan result.

After a compatible compaction checkpoint, replay starts from the checkpoint and
clears older continuation handles. Incompatible provider checkpoints are
ignored.

## Test Fixtures

Tests that create provider transcript items directly must create valid
provider-visible history unless the test is explicitly asserting rejection.
Valid tool-call fixtures include the assistant output item and the matching
tool-result item in order:

```go
[]coreconversation.Item{
	{
		Kind:   coreconversation.ItemOutput,
		CallID: "call_1",
		Name:   "file_read",
		ToolCalls: []coreconversation.ToolCallRef{{
			CallID: "call_1",
			Name:   "file_read",
		}},
	},
	{
		Kind:    coreconversation.ItemToolResult,
		CallID:  "call_1",
		Name:    "file_read",
		Content: "result",
	},
}
```

Invalid fixtures should assert a continuity error from projection, compaction,
or append validation. They should not assert synthesized tool calls, synthesized
tool results, or repair metadata.
