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
- `adapters/httpssechannel` — remote channel adapter using JSON endpoints and
  SSE events while preserving the same client/session/run API.

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

`apps/devclient` can run either in-process or against HTTP/SSE:

```bash
go run ./apps/devclient input hello
go run ./apps/devclient serve -addr 127.0.0.1:8080
go run ./apps/devclient -url http://127.0.0.1:8080 input hello
```

The rewrite is intentionally not source-compatible with `agentsdk`. Concepts
are being reintroduced package by package with clear ownership boundaries.
