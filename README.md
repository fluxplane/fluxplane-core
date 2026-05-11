# Fluxplane Agent Runtime

Fluxplane Agent Runtime is a rewrite of the current `agentsdk` ideas as a
layered agent runtime plus SDK.

The repository is organized around a strict dependency direction:

```text
core -> runtime -> orchestration -> adapters/plugins -> apps
```

Current seed packages:

- `core/event` — typed domain event payloads, sinks, records, and decode
  registry.
- `core/operation` — executable operation specs, semantics, results, and the
  minimal operation contract.
- `orchestration/client` — user-facing channel client, session handle, run
  handle, submission, and event contracts.
- `orchestration/harness` — channel-to-session use-case facade.
- `adapters/directchannel` — in-process proof channel used by `apps/devclient`.

The rewrite is intentionally not source-compatible with `agentsdk`. Concepts
are being reintroduced package by package with clear ownership boundaries.
