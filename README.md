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
- `orchestration/app` — resource/app composition into executable runtime
  configuration.
- `orchestration/pluginhost` — plugin refs resolved into resource
  contributions.
- `orchestration/harness` — channel-to-session use-case facade.
- `adapters/directchannel` — in-process proof channel used by `apps/devclient`.
- `adapters/httpssechannel` — remote channel adapter using JSON endpoints and
  SSE events while preserving the same client/session/run API.
- `adapters/resourcefs` — first local filesystem manifest loader.
- `adapters/httpcontrol` — separate daemon/control-plane HTTP surface.

The current executable path supports command submissions and conversational
input submissions through the same session/run handle API:

```go
svc, err := agentruntime.New(agentruntime.Config{Agent: agent})
session, err := svc.Open(ctx, agentruntime.OpenRequest{Conversation: conversation})
run, err := session.SendInput(ctx, agentruntime.Input{Text: "hello"})
result, err := run.Wait(ctx)
```

Direct and HTTP/SSE clients are expected to expose the same logical contract:
session handles are authoritative for thread identity, submissions return run
handles, run handles expose semantic events, and terminal results preserve
session/submission identity even on failure.

Event and signal submissions are modeled for future daemon triggers,
schedulers, and file watchers, but are not executed yet.

Session event subscriptions use `client.Event` for both live delivery and
replay. Replayed events carry an `EventCursor` backed by thread event sequence
numbers, so future HTTP/SSE clients can resume from the last seen cursor.

`apps/devclient` can run either in-process or against HTTP/SSE:

```bash
go run ./apps/devclient input hello
go run ./apps/devclient -app ./path/to/app echo hello
go run ./apps/devclient -app ./path/to/app text/upper hello
go run ./apps/devclient serve -addr 127.0.0.1:8080
go run ./apps/devclient -url http://127.0.0.1:8080 input hello
```

Local resource apps currently use an `agentruntime.json` manifest. The first
supported shape is command declarations over operation implementations supplied
by the host application, plus plugin refs that contribute commands and
operations.
Composition preserves manifest and plugin-ref order, but ambiguous overrides
are rejected: duplicate command paths, operation declarations, and executable
operation names fail with diagnostics instead of silently shadowing each other.

```json
{
  "commands": [
    {
      "path": ["echo"],
      "operation": "echo",
      "policy": {
        "allowed_callers": ["user"],
        "required_trust": "verified"
      }
    }
  ]
}
```

The first migrated plugin is `plugins/echoplugin`, so a plugin-backed manifest
can be as small as:

```json
{
  "plugins": [
    {"name": "echo"}
  ]
}
```

`plugins/textplugin` is the second migrated low-risk plugin. It proves plugin
configuration and multiple contribution ordering without introducing IO:

```json
{
  "plugins": [
    {
      "name": "text",
      "config": {
        "commands": ["upper", "trim"]
      }
    }
  ]
}
```

Next migration work is tracked in `docs/migration-from-agent-sdk.md`. Resource
composition and the first control-plane boundary now exist; the next step is to
expand plugin contributions, signal/trigger execution, and context-provider
runtime without bypassing the channel client path.

The rewrite is intentionally not source-compatible with `agentsdk`. Concepts
are being reintroduced package by package with clear ownership boundaries.
