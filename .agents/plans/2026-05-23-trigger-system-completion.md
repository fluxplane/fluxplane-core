# Trigger System Completion Plan

## Goal

Make Fluxplane daemon triggers complete enough for production-style background
execution: schedules, startup events, webhooks, lifecycle visibility, reliable
shutdown, explicit execution context, and predictable delivery semantics.

## Current State

- `serve` can run daemon startup and interval schedule triggers.
- Triggers submit structured events into configured sessions.
- Trigger actions can run workflows, operations, or commands through reactions.
- Workflow steps support `input_map` and `when` conditions.
- `serve --verbose` can show trigger, workflow, operation, and notification
  progress.
- The scheduled-trigger example demonstrates command-collected metrics,
  classifier-based notification gating, OS notifications, and startup TTS.

## Recommended Default

For monitoring and watchdog workloads, scheduled triggers should default to an
isolated execution context:

```yaml
context:
  mode: isolated
```

Each fire should get fresh execution context, use only declared inputs and
bounded bootstrap context, emit audit records, and remain silent on success.
Stable or persistent context should be opt-in for workflows that intentionally
build history across runs.

## Completion Items

### 1. Trigger Context Policy

- Add explicit trigger context modes:
  - `isolated`: fresh conversation/thread per fire.
  - `stable`: stable conversation/thread per trigger.
  - `session`: explicit configured session/conversation key.
- Add history controls:
  - `history: none`
  - `history: window`
  - `history: full`
- Add lightweight bootstrap controls for background runs so monitoring jobs do
  not inherit large unrelated chat history.
- Document compaction behavior for stable and windowed trigger contexts.

### 2. Schedule Policy

- Support cron expressions in addition to fixed intervals.
- Support one-shot timers.
- Add timezone handling for wall-clock schedules.
- Add active-hours windows.
- Add jitter/stagger to avoid load spikes.
- Add overlap policy:
  - `skip`
  - `queue`
  - `cancel_previous`
  - `parallel`
- Add max runtime / timeout per trigger fire.

### 3. Lifecycle Management

- Add CLI management:
  - `fluxplane trigger list`
  - `fluxplane trigger status`
  - `fluxplane trigger run`
  - `fluxplane trigger pause`
  - `fluxplane trigger resume`
  - `fluxplane trigger remove` for runtime-created triggers.
- Show last run, next run, status, fire count, last duration, and last error.
- Add manual "run now" support that shares normal trigger execution semantics.

### 4. Run Ledger And Audit

- Persist trigger run records.
- Record run id, trigger name, scheduled time, actual fire time, context mode,
  actions, status, duration, emitted notifications, and errors.
- Add retention policy for trigger run history.
- Make `serve --verbose` a live view over the same event/run data rather than a
  separate best-effort stream.

### 5. Reliability

- Persist `next_run_at` so restarts do not lose schedule state.
- Add lock/no-double-fire behavior for concurrent daemon instances.
- Add retry/backoff for transient failures.
- Define permanent-failure behavior for recurring triggers.
- Ensure Ctrl+C and daemon shutdown cancel active trigger work and clean up
  managed child processes.
- Add startup recovery: resume schedules, reconcile running records, and mark
  orphaned runs lost after a grace period.

### 6. Delivery Semantics

- Make delivery runner-owned, not model-owned.
- Support delivery modes:
  - `none`
  - `notify`
  - `channel`
  - `webhook`
  - `event_only`
- Support silent success for monitoring:
  - no delivery when output is empty or classified as no action.
  - failure delivery still occurs unless explicitly disabled.
- Separate notification text from TTS text and channel text.
- Add failure destinations and escalation policy.

### 7. Script-Only / No-Agent Mode

- Add operation/script-only triggers for deterministic watchdogs.
- Treat empty stdout as silent success.
- Treat non-zero exit or timeout as alert-worthy failure.
- Allow script output to become workflow input when an agent is needed only for
  summarization or classification.

### 8. Trigger Sources

- Keep startup and schedule triggers.
- Add webhook triggers with authentication and payload mapping.
- Add file/event bus hooks for local runtime events.
- Add message-listener triggers where channel adapters can expose them safely.
- Add lifecycle hooks for serve startup, shutdown, session open, session close,
  compaction, and workflow completion.

### 9. Safety And Policy

- Add per-trigger trust/scope policy.
- Add per-trigger allowed operations/tool projection.
- Add per-trigger model override with allowed-model validation.
- Add approval policy for scheduled side effects.
- Add loop and spam protections for self-created or frequently firing triggers.
- Add explicit limits for concurrency, delivery frequency, and resource usage.

### 10. Documentation And Examples

- Document when to use isolated triggers, stable triggers, and heartbeat-style
  session context.
- Extend `examples/scheduled-trigger` with the recommended isolated monitoring
  mode once implemented.
- Add examples for:
  - silent watchdog
  - daily report
  - webhook-to-workflow
  - startup hook
  - durable multi-step trigger workflow

## Suggested Implementation Order

1. Add trigger context policy with `isolated` and `stable` modes.
2. Add run ledger with last/next/status/error visibility.
3. Add trigger CLI status/run/pause/resume.
4. Add cron/timezone/overlap policy.
5. Add delivery modes and silent-success contract.
6. Add script-only/no-agent mode.
7. Add webhook triggers.
8. Add lifecycle hooks and compaction-aware documentation.
