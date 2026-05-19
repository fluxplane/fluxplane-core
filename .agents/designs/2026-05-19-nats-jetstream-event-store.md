# NATS JetStream Event Store

## Status

Implemented first adapter slice.

## Summary

Add a NATS JetStream-backed implementation of `core/event.Store` in
`adapters/natseventstore`.

This slice is intentionally limited to the storage adapter and testcontainer
coverage. Runtime appconfig selection, generated deployment services, Docker
Compose output, Helm output, and app-specific slack-bot config changes remain
backlog work.

## Storage Model

The first implementation uses a canonical JetStream append log:

- stream: `AGENTRUNTIME_EVENTS` by default;
- subject: `agentruntime.events.log` by default;
- one JetStream message per `event.Store.AppendBatch` call;
- each message stores the normalized `event.StoredRecord`s grouped by logical
  `event.StreamID`.

This preserves the existing event-store contract, including atomic multi-stream
append batches, without requiring a JetStream subject per logical app stream.

## Contract

The adapter must preserve the same semantics as memory and SQLite event stores:

- stream-local sequence numbers;
- `event.ExpectSequence` optimistic append conflicts;
- duplicate record ID rejection across all logical streams;
- atomic `AppendBatch`;
- `Load` support for `After`, `Before`, `Limit`, and backward direction.

The adapter maintains an in-memory projection of the canonical log for stream
heads, duplicate IDs, and load windows. JetStream remains the source of truth.
On append races, the adapter reloads the projection and retries with a
JetStream expected-last-sequence guard.

## Backlog

- Add app manifest config for event backend selection, likely under
  `runtime.events.store` using `kind`, `dsn`, and `dsn_env` for consistency
  with `data.store`.
- Wire launch to select SQLite, memory, or NATS from decoded appconfig.
- Teach `coder deploy --target=docker-compose` to materialize required backing
  services such as MySQL and NATS JetStream from runtime backend requirements.
- Add Helm/Kubernetes target-specific materialization for the same backend
  intent.
- Update external apps after the config and deploy layout are settled.

## Verification

Adapter tests use testcontainers with the official NATS image and JetStream
enabled. They are gated by `TEST_INTEGRATION=1` to keep the normal local verify
path independent of Docker.
