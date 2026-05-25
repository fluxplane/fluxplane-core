# Bug hunt log

Tracking small, obvious, but impactful bugs found and fixed during the `/loop`
session. One bug per iteration: find → reproduce → fix → commit.

## Iteration 1 — goroutine leak in `harness.Subscribe` / `harness.SubscribeAll`

- **Where:** `orchestration/harness/harness.go` Subscribe and SubscribeAll.
- **Bug:** Each subscription spawned `go func() { <-ctx.Done(); cancel() }()`.
  When the caller unsubscribed via the returned `cancel` and the ctx was
  long-lived, that watcher goroutine stayed parked on `<-ctx.Done()` forever.
- **Fix:** Watcher now also selects on `<-sub.done`, which the cancel path
  closes via `sub.close()` — direct unsubscribes release the goroutine.
- **Commit:** `09ba7d8` — "fix: stop leaking subscriber watcher goroutines".

## Iteration 2 — nil-pointer panic in `WaitDeployment`

- **Where:** `adapters/distribution/deploy/kubernetes.go` line 1464.
- **Bug:** Readiness check was
  `if available >= *spec.Replicas || (spec.Replicas == nil && available > 0)`.
  `||` evaluates left-first; if `spec.Replicas` was nil (valid Kubernetes
  encoding for "use the default"), the left-hand deref panicked before the
  nil guard on the right ever ran.
- **Trigger:** Any Deployment manifest that omits `spec.replicas`.
- **Fix:** Branched on `spec.Replicas == nil` first; deref only when non-nil.
- **Commit:** `16589e1` — "fix: guard nil Spec.Replicas in WaitDeployment".

## Iteration 3 — goroutine leak when `trigger.Run` rejects a bad schedule

- **Where:** `orchestration/trigger/trigger.go` `(*Host).Run`.
- **Bug:** `Run` iterates over `h.specs`, spawning startup/schedule
  goroutines as it goes. When a later spec has a bad
  `schedule.every`, the function returns the parse error mid-iteration —
  but the goroutines already spawned for earlier specs are not stopped.
  They only exit if the caller's ctx cancels, so with a long-lived ctx
  (e.g. `context.Background()` in tests/dev) they leak permanently.
- **Reproduction:** Two specs — one startup, one schedule with
  `schedule.every: "garbage"`. Run returns an error, but the startup
  goroutine (calling `h.Fire` against a fake client) keeps running.
- **Fix:** Parse + validate every schedule.every up front, before spawning
  any goroutine. If validation fails, return without spawning anything.
- **Regression test:** `TestHostRunRejectsBadScheduleBeforeSpawningGoroutines`
  in `trigger_test.go` — verified to fail on the old code (`client.opens = 1`)
  and pass on the fix.
- **Commit:** `ae8f6d7` — "fix: stop leaking trigger goroutines on bad schedule.every".

## Iteration 4 — missing `rows.Err()` in `sqlstore` row iterations

- **Where:** `adapters/storage/data/sqlstore/store.go` —
  `DeleteRecords` (~line 112) and `BatchGetRecords` (~line 155).
- **Bug:** Both functions iterate with `for rows.Next() {}` and call
  `rows.Close()` after the loop, but never check `rows.Err()`. If
  iteration exits because of a driver-level read error (not just
  end-of-rows), the error is silently dropped.
  - In `DeleteRecords` the result is a committed transaction that
    deleted only the rows we managed to scan — partial delete masquerading
    as success.
  - In `BatchGetRecords` the result is a quiet "no match found" return,
    even though the scan actually failed mid-stream.
- **Inconsistency in the file:** sibling functions `QueryRecords` and
  `QueryRelations` correctly call `rows.Err()`; these two were the
  outliers.
- **Fix:** Added `rows.Err()` check between the for-loop and `rows.Close()`,
  closing the rows on error to release the connection.
- **Commit:** `0264fca` — "fix: check rows.Err() in sqlstore DeleteRecords and BatchGetRecords".

## Iteration 5 — non-atomic secret file write

- **Where:** `runtime/secret/filestore.go` `SaveSecret`.
- **Bug:** Used `os.WriteFile(s.path(ref), data, 0o600)` to persist the
  secret file directly. If the process is killed mid-write (or the disk
  fills), the file is left truncated and the next `LoadSecret` returns a
  parse error — caller has now lost a credential they thought they had
  just saved.
- **Sibling code uses atomic writes:** `adapters/llm/claudecode/auth.go`
  and `runtime/datasource/semantic/store.go` both write via temp file +
  rename. This one was the outlier.
- **Fix:** Added a small `writeFileAtomic` helper (create temp in same
  dir → chmod → write → sync → close → rename, with deferred cleanup
  of the temp file on any error) and routed `SaveSecret` through it.
- **Regression test:** `TestFileStoreSaveLeavesNoTempFile` writes five
  times and asserts no `.tmp-*` files remain in the directory.
