# Event-store Backend Strategy

Status: accepted for Phase 7
Date: 2026-05-17

## Context

Coder runs multiple local sessions that append conversation, thread, task, and
projection events. The storage backend must preserve per-stream sequence
ordering, support atomic multi-stream writes where the thread runtime updates a
thread stream and the shared `thread.index`, and remain usable for local/offline
work without a required server.

Recent fixes changed the concurrency posture that this decision is based on:

- `adapters/sqleventstore` opens direct SQLite stores with a single pooled
  connection, uses `BEGIN IMMEDIATE` for append transactions, classifies SQLite
  `BUSY`/`LOCKED` separately from append conflicts, and retries busy/locked
  writes with bounded backoff.
- `event.Store.AppendBatch` is used for atomic multi-stream writes.
- `runtime/thread.Store` retries optimistic append conflicts on the shared
  thread index and same-thread streams, uses stable record IDs for retry
  idempotency, and recovers duplicate committed records by reloading.

Stress evidence from this phase:

```text
go test ./adapters/sqleventstore -run \
  TestConcurrent|TestRuntimeThreadStoreConcurrent|TestConcurrentStoresSameFileSameStreamWithoutExpectedSequenceStress -count=1
ok github.com/fluxplane/agentruntime/adapters/sqleventstore 0.749s
```

The tested cases cover concurrent direct SQLite appends across one store, two
store handles opened on the same database file, same-stream writes without an
expected sequence, expected-sequence conflict classification, multi-stream batch
atomicity, concurrent thread creation contending on `thread.index`, and
concurrent appends to one thread. The explicit stress case runs 10 iterations of
32 goroutines with two direct SQLite store handles appending 640 records per
iteration to the same stream and verifies contiguous sequences.

## Decision

Use **direct SQLite as the default local/offline event-store backend for coder
multi-session workloads**, with documented concurrency assumptions. Do not make
a single-writer daemon or Postgres a prerequisite for local coder use in this
phase.

Add a **Postgres backend as the recommended future server/multi-user backend**
when the product needs remote daemon deployments, multiple OS users or hosts,
centralized backup/restore, or higher sustained write concurrency than SQLite
can provide.

Keep the **single-writer daemon** as an optional local topology, not the storage
backend strategy. It is useful for lifecycle management and for normalizing
access through one process, but it should wrap the same event-store contract and
not be required to make local SQLite correctness acceptable.

Do not pursue a **per-session store plus merge** design for the primary coder
event log. It can be useful for export/import or branch-like workflows, but it
adds merge semantics for globally ordered thread/task/session projections and
weakens the single append log model.

## Direct SQLite Suitability And Assumptions

Direct SQLite is suitable for local coder multi-session use when all of the
following assumptions hold:

1. Writers are processes on the same local machine writing to a local filesystem
   path, not a network-mounted database file.
2. Expected write concurrency is small-to-moderate: several interactive coder
   sessions and tool operations, not many independent users generating sustained
   high-throughput writes.
3. The event-store implementation remains the only writer to the database schema
   and all writes use append transactions with `BEGIN IMMEDIATE`, unique record
   IDs, expected sequence checks, and retry/idempotency behavior.
4. Runtime stores treat append conflicts as normal optimistic contention and
   retry only after reloading state.
5. Busy/locked retries remain bounded and observable; repeated busy failures are
   surfaced as operational saturation rather than hidden indefinitely.
6. Long-running read transactions should not be introduced on the write path.

Under those assumptions, direct SQLite gives durable local storage, no external
service setup, deterministic offline behavior, and a simple migration path. It
serializes writes at the database level, so it is correctness-oriented rather
than high-write-throughput-oriented.

## Alternatives Considered

### Direct SQLite

Pros:

- No daemon or database server required; best local/offline developer
  experience.
- Durable file-backed storage with simple backup/copy semantics.
- Existing implementation now serializes append transactions deliberately and
  has stress coverage for same-file, same-stream, and thread-index contention.
- Lowest operational complexity for coder bundles and examples.

Cons:

- One writer at a time; high write concurrency becomes latency and potential
  busy/locked retry exhaustion.
- Correctness depends on all writers using the event-store contract, not ad hoc
  SQL.
- Not appropriate for a shared server used by many users or many hosts.
- Needs continued stress testing as session/task/projector workloads grow.

Recommendation: default local backend.

### SQLite Behind A Single-writer Daemon

Pros:

- Centralizes local process lifecycle, cleanup, observability, and policy.
- Ensures one process owns SQLite writes, which can reduce cross-process lock
  churn and make backpressure easier to expose.
- Natural fit if local coder sessions already connect to a long-running daemon
  for orchestration.

Cons:

- Adds daemon startup, discovery, health, upgrade, and crash-recovery behavior
  to the local path.
- Does not change SQLite physical write concurrency; it serializes earlier in
  the stack.
- Makes offline/local embedding harder if the daemon becomes mandatory.

Recommendation: optional local deployment mode and future reliability/UX work,
not required as the default storage backend.

### Postgres

Pros:

- Better fit for server, multi-user, and multi-host deployments.
- Operational tooling for backups, replication, observability, pooling, and
  migrations is mature.
- Row locks/transactions can implement the same event-store invariants while
  supporting higher sustained concurrency than SQLite.

Cons:

- Requires server setup, credentials, migrations, and operational ownership.
- Poor default for local/offline coder usage.
- Needs a new adapter, migration tests, and deployment configuration.

Recommendation: implement when server/multi-user daemon work starts; do not
block local coder on it.

### Per-session Stores With Merge

Pros:

- Reduces write contention because sessions write independently.
- Useful for exports, imports, branch-like review, or disconnected replication.

Cons:

- Requires first-class merge/conflict semantics for thread index, task
  lifecycle, projections, and global ordering.
- Complicates queries that expect one canonical event log.
- Makes acceptance and recovery harder for normal multi-session coder use.

Recommendation: do not use as the primary backend strategy. Revisit only for
explicit replication/export workflows.

## Operational Tradeoffs

| Strategy | Setup complexity | Durability | Concurrency | Local/offline | Migration path |
| --- | --- | --- | --- | --- | --- |
| Direct SQLite | Low | Local file durability | Single SQLite writer; bounded retries | Excellent | Current default; add schema migrations as needed |
| SQLite daemon | Medium | Local file durability plus daemon-managed lifecycle | Serialized by daemon and SQLite | Good, but daemon required if mandatory | Wrap existing store behind control/session APIs |
| Postgres | High | Server-managed durability, backup, replication options | Best for multi-user/server workloads | Poor unless bundled separately | New adapter using same `event.Store` contract; data export/import from SQLite |
| Per-session + merge | Medium/high | Many local logs | Low write contention, high merge complexity | Good | Requires new merge model and projection reconciliation |

## Follow-up Backlog

| ID | Title | Recommended for | Priority |
| --- | --- | --- | --- |
| `storage-sqlite-concurrency-docs` | Document direct SQLite concurrency assumptions in coder configuration docs | local/offline coder default | high |
| `storage-sqlite-observability` | Add metrics/logging for SQLite busy/locked retries and thread write retry exhaustion | local multi-session reliability | high |
| `storage-local-single-writer-daemon` | Design optional local single-writer daemon mode around the `event.Store` contract | daemon lifecycle and local backpressure UX | normal |
| `storage-postgres-event-store` | Implement a Postgres event-store adapter with the same append/load concurrency tests | server and multi-user deployments | normal |
| `storage-sqlite-to-postgres-migration` | Define SQLite export/import or migration path to Postgres | server migration path | normal |

## Next Steps

1. Document direct SQLite assumptions in user-facing coder/configuration docs
   when storage configuration is exposed.
2. Keep the current SQLite stress tests in the normal package test suite and add
   a longer stress profile to the verification or CI matrix if runtime cost is
   acceptable.
3. Add structured metrics/logging around SQLite busy/locked retry exhaustion,
   append conflicts, and thread write retry exhaustion before increasing default
   concurrency.
4. For daemon work, design a local single-writer mode that is optional and keeps
   direct SQLite available for embedding/offline use.
5. For server/multi-user work, add a Postgres event-store adapter and migration
   plan using the same append/load contract and concurrency tests.
