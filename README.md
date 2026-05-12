# Fluxplane Agent Runtime

Fluxplane Agent Runtime is a rewrite of the current `agentsdk` ideas as a
layered agent runtime plus SDK.

The repository is organized around a strict dependency direction:

```text
core -> runtime -> orchestration -> adapters/plugins -> apps
```

Current seed packages:

- root `agentruntime` — public in-process service facade for library
  consumers.
- `core/event` — typed domain event payloads, sinks, records, and decode
  registry.
- `core/operation` — executable operation specs, semantics, results, and the
  minimal operation contract.
- `orchestration/client` — user-facing channel client, session handle, run
  handle, submission, and event contracts.
- `orchestration/harness` — channel-to-session use-case facade.
- `adapters/directchannel` — in-process proof channel used by `apps/devclient`.

The current executable path supports command submissions and conversational
input submissions through the same session/run handle API:

```go
svc, err := agentruntime.New(agentruntime.Config{Agent: agent})
session, err := svc.Open(ctx, agentruntime.OpenRequest{Conversation: conversation})
run, err := session.SendInput(ctx, agentruntime.Input{Text: "hello"})
result, err := run.Wait(ctx)
```

Event and signal submissions are modeled for future daemon triggers,
schedulers, and file watchers, but are not executed yet.

Session event subscriptions use `client.Event` for both live delivery and
replay. Replayed events carry an `EventCursor` backed by thread event sequence
numbers, so future HTTP/SSE clients can resume from the last seen cursor.

The rewrite is intentionally not source-compatible with `agentsdk`. Concepts
are being reintroduced package by package with clear ownership boundaries.
