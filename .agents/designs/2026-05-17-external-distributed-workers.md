# External Distributed Workers

## Status

Follow-up design. This is not the current local task execution model.

Current task execution is local-runtime owned:

- `orchestration/taskexecutor` claims ready tasks.
- `ChannelWorker` opens profiled sessions through the channel client.
- task execution claims record worker id, lease id, and lease expiry.
- active local worker calls renew execution leases.
- worker registration snapshots are durable task-store events.
- reconciliation recovers expired running leases and expired worker
  registrations.
- stale worker results cannot complete superseded executions.

Those pieces are implemented and should not be re-planned here. This design is
for a future protocol where worker processes are independently running
participants rather than calls owned by one local scheduler process.

## Problem

Local workers are enough for the current `coder` use case. External workers are
only useful when the product needs one or more of:

- worker capacity outside the interactive runtime process;
- long-running work that survives the process that accepted the user request;
- isolated execution environments for risky tools or credentials;
- specialized workers with different hardware, language runtimes, connectors,
  or trust levels;
- multiple machines sharing one durable task queue.

The risk is adding a second scheduler model. The future design must extend the
existing event-sourced task and lease model instead of creating a parallel
queue.

## Target Shape

External workers should be modeled as a worker backend behind the existing
task execution boundary:

- `core/task` remains the durable task, execution, lease, worker-status, and
  artifact event contract.
- `runtime/task.Store` remains the event-store wrapper for task streams,
  index summaries, and worker registration streams.
- `orchestration/taskexecutor` remains the scheduler/claim owner for ready
  tasks unless a later design explicitly splits scheduling from assignment.
- local `ChannelWorker` remains the default worker backend.

The external worker protocol should add a backend that can:

- register worker identity, roles, profiles, capacity, version, and lease
  timeout;
- receive or pull assigned task/step execution requests;
- renew its own registration and assigned execution lease;
- report progress as task events;
- submit artifacts and final step/task results with the expected lease id;
- shut down gracefully by releasing or interrupting in-flight assignments;
- tolerate crashes by letting existing reconciliation requeue or interrupt
  expired assignments.

## Assignment Model

Prefer a durable assignment stream over an in-memory handoff.

Minimum future flow:

1. Scheduler claims a ready task execution with the existing task stream
   precondition.
2. Scheduler records an assignment for a compatible external worker or worker
   pool.
3. Worker observes or pulls the assignment.
4. Worker renews both worker registration and execution lease while running.
5. Worker appends progress, artifacts, and terminal result using the current
   execution id and lease id.
6. Scheduler reconciliation ignores stale terminal writes and recovers expired
   assignments with the existing retry policy.

Do not allow an external worker to claim arbitrary ready tasks without the same
optimistic task stream protection used by the scheduler. If workers are ever
allowed to self-claim, that path must use the same task projection,
expected-sequence, and lease checks.

## Security And Isolation

External workers introduce trust boundaries that local workers do not have.

The protocol should carry:

- worker identity and authentication;
- allowed roles/profiles;
- allowed operations/toolsets or execution environment labels;
- workspace/project scope;
- credential/connectors policy;
- result size and artifact reference policy;
- audit metadata tying results to worker identity and version.

External workers must not bypass operation safety. They either run normal
session agents through the same operation executor/safety envelope, or they are
treated as a separate trusted execution adapter with an explicit policy.

## Non-Goals

- Do not replace local `ChannelWorker`.
- Do not add a second task database or queue.
- Do not duplicate task status, execution status, or artifact state outside
  `core/task` events.
- Do not reimplement leases, heartbeats, or stale-result rejection; those are
  already part of the local scheduler foundation.
- Do not make external workers the default for local `coder`.

## Open Decisions

- Push vs pull assignment transport: event stream polling, HTTP/SSE, daemon
  control API, or connector-backed queue.
- Whether scheduler owns all assignment decisions or workers can self-claim
  compatible ready tasks.
- How worker auth is represented in local-only, daemon, and remote deployments.
- Whether worker capabilities are expressed as roles/profiles only or as
  richer toolchain/environment descriptors.
- How much progress should be session-thread mirrored for external workers
  versus task-stream-only.

## Done Criteria For A Future Slice

- One local scheduler can assign work to at least one independently running
  worker process.
- Worker crash or missed heartbeat causes existing reconciliation to requeue or
  interrupt according to retry policy.
- A stale result from a crashed/restarted worker cannot complete a superseded
  execution.
- `task_scheduler_status` shows external worker registrations, capacity, and
  assigned/running work.
- `task verify` and an integration test with two processes pass.
