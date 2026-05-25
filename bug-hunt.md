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
- **Commit:** `75f8409` — "fix: write secret files atomically".

## Iteration 6 — `stringField` leaked non-string JSON values into policy names

- **Where:** `runtime/operation/authorization.go` `stringField`.
- **Bug:** Extracted a named field from an operation input by JSON-encoding
  the input, decoding into `map[string]any`, then `fmt.Sprint(value)`. For
  non-string JSON values that's catastrophic for downstream policy lookups:
  - `null` → `"<nil>"`
  - `true` → `"true"`
  - `42` → `"42"`
  These then get wrapped by `wildcardName` (which only substitutes `*` for
  the empty string), so they leak into `policy.ResourceRef.Name` for
  channel/task/datasource authorization. The policy engine then searches
  for a resource literally named `"<nil>"`, producing confusing deny
  messages instead of treating the field as absent.
- **Fix:** Replaced `fmt.Sprint(value)` with a type assertion
  `str, _ := values[name].(string)` so any non-string (including JSON
  null) collapses to "" and `wildcardName` substitutes `*` as intended.
- **Regression test:** `TestStringFieldIgnoresNonStringValues` covers
  string, whitespace, nil, bool, number, and missing-key cases. Verified
  to fail on the old code (`"<nil>"`, `"true"`, `"42"`) and pass on the fix.
- **Commit:** `d903d72` — "fix: stringField returns empty for non-string JSON values".

## Iteration 7 — oauth2 callback handler can leak goroutines on second callback

- **Where:** `adapters/auth/oauth2flow/flow.go` `callbackHandler`.
- **Bug:** `Authorize` allocated a buffer-size-1 channel and the HTTP
  callback handler sent to it unconditionally. If the OAuth provider (or
  a double-click on the consent page) delivered a second callback before
  `Authorize` consumed the first, the second handler blocked forever on
  `out <- ...` because nothing else ever reads. That parked goroutine
  then stalled the deferred `server.Shutdown(1s)` for the full grace
  window and leaked.
- **Reproduction:** Five concurrent goroutines call the handler with
  valid params. Without the fix, the first delivers, the other four
  block on the channel send, so `wg.Wait()` never returns and the test
  times out (confirmed: 4s timeout hits, goroutine dump shows four
  handlers parked in `chansend1`).
- **Fix:** Wrapped the channel send in a `sync.Once` so only the first
  callback delivers; later callbacks complete their HTTP response and
  return immediately without touching the channel.
- **Regression test:** `TestCallbackHandlerDoesNotBlockOnSecondCallback`
  drives five concurrent callbacks at the handler and asserts they all
  complete and exactly one result is delivered.
- **Commit:** `087229c` — "fix: stop oauth2flow callbackHandler from blocking on a second callback".

## Iteration 8 — `normalizedIndexValue` truncates UTF-8 in the middle of a rune

- **Where:** `adapters/storage/data/sqlstore/store.go` `normalizedIndexValue`.
- **Bug:** When the normalized value exceeded 191 bytes, the function
  returned `value[:191]` unconditionally. If byte 191 fell inside a
  multi-byte UTF-8 rune (e.g. `"€"` is 3 bytes), the result was an
  invalid UTF-8 string ending in a dangling continuation byte.
- **Impact:** This value gets written to `data_store_record_field.value_norm`
  and compared against in WHERE clauses for filter queries. A MySQL
  column with utf8mb4 charset rejects invalid sequences (strict mode)
  or silently replaces them, putting the INSERT and SEARCH paths
  out of sync — searches stop finding rows whose normalized value
  was truncated.
- **Reproduction:** A 200-byte input shaped `"aaa…aaa€bbb…"` with the
  `€` placed so byte 191 is inside it. Old code returns
  `"a"×189 + 0xe2 0x82` (invalid UTF-8); new code returns `"a"×189`.
- **Fix:** Scan backwards from byte 191 to the nearest `utf8.RuneStart`
  byte and truncate there, so the result never ends inside a rune.
- **Regression test:** `TestNormalizedIndexValuePreservesUTF8RuneBoundaries`
  feeds the multi-byte-boundary input and checks `utf8.ValidString` plus
  the expected byte length. Verified to fail on the old code
  (`invalid UTF-8: "...\xe2\x82"`).
- **Commit:** `4f4cf41` — "fix: keep normalizedIndexValue at a UTF-8 rune boundary".

## Iteration 9 — `ValidateContinuity` reported open tool calls non-deterministically

- **Where:** `runtime/conversation/projector.go` `ValidateContinuity`.
- **Bug:** Three places returned a `ContinuityError` from inside a
  `for callID, index := range open { ... return ... }` block, where
  `open` is a `map[string]int`. Go map iteration is intentionally
  randomized, so when more than one tool call was simultaneously open,
  the call ID and `opened_at` index reported in the error were picked
  at random — same invalid transcript, different error text every run.
- **Impact:** Hostile to debugging and a flake source for any test that
  matches on the reported call ID with multiple opens. The existing
  `left open` tests happened to use a single open call so they didn't
  trip it.
- **Reproduction:** A single assistant output with three tool calls
  (`call_a`, `call_b`, `call_c`) followed by a user input. Across 10
  runs on the old code the error randomly reported any of the three.
- **Fix:** Added an `earliestOpen` helper that picks the smallest
  opened-at index with the call ID as a deterministic tie-breaker. All
  three random-iteration returns now route through it.
- **Regression test:** `TestValidateContinuityReportsEarliestOpenCall`
  runs the validator 50 times and asserts `call_a` is reported every
  time. Verified to fail on the old code with "call_b"/"call_c"
  appearing across iterations; passes on the fix.
- **Commit:** `531e90b` — "fix: report continuity errors deterministically when multiple tool calls are open".

## Iteration 10 — same UTF-8 truncation bug in the datasource-mirror sqlstore

- **Where:** `adapters/storage/datasourcemirror/sqlstore/store.go` `truncate`.
- **Bug:** Same class as iteration 8: returned `value[:limit]` without
  respecting UTF-8 rune boundaries. The function is used at insert time
  for `data_store_record_field`-style filter rows (limit 191) and at
  query time for the WHERE-clause filter value. A multi-byte rune that
  straddles byte 191 would be left as a dangling continuation byte,
  invalid UTF-8 — utf8mb4 inserts then fail under strict mode, or the
  insert and search paths each truncate differently and disagree.
- **Reproduction:** 189 ASCII bytes + one 3-byte rune (`"€"`) + ASCII
  trailer. Old code returns `"a"×189 + 0xe2 0x82` (invalid UTF-8); new
  code returns `"a"×189`.
- **Fix:** Same approach as iteration 8 — scan backwards from the
  byte limit to the nearest `utf8.RuneStart` before slicing.
- **Regression test:** `TestTruncatePreservesUTF8RuneBoundaries` in the
  mirror sqlstore package, mirrors the iteration 8 test. Verified to
  fail on the old code (`invalid UTF-8: "...\xe2\x82"`).
- **Notes:** Two other `truncate` helpers exist in the tree
  (`runtime/datasource/detect.go` `truncateBytes` cap 64KB regex input,
  `plugins/native/datasource/filesystem.go` `truncate` cap 1200 byte
  body preview). Those touch user-visible text rather than indexed
  values, so the impact is cosmetic and they're left for a separate
  pass.
- **Commit:** `435dde3` — "fix: keep mirror sqlstore truncate at a UTF-8 rune boundary".

## Iteration 11 — `CompactTranscript.SummarizedItems` under-counted `NewItems`

- **Where:** `runtime/conversation/compact.go` `CompactTranscript`.
- **Bug:** When the transcript still exceeded its budget after omitting
  old items, the function called `compactLargeItems` on both `out.Items`
  and `out.NewItems`. Only the first call's return was captured into
  `summarized`; the second was checked only for "did anything change?"
  and the count was discarded. `CompactResult.SummarizedItems` then
  reported only the `Items` count even when `NewItems` had also been
  summarized.
- **Impact:** Wrong telemetry/audit count for compaction passes —
  downstream dashboards that sum the field undercount, and callers
  inspecting `SummarizedItems` can't tell how much work actually ran.
- **Reproduction:** A transcript with one large tool result in `Items`
  and one large tool result in `NewItems`. Old code returned
  `SummarizedItems = 1`; new code returns `2`.
- **Fix:** Add the `compactLargeItems(out.NewItems, ...)` return into
  `summarized` instead of testing it for truthiness.
- **Regression test:** `TestCompactTranscriptSummarizedItemsCoversBothItemsAndNewItems`
  feeds large items into both lists and asserts the count is 2.
  Verified to fail on the old code (`SummarizedItems = 1`).
- **Commit:** `12e65ae` — "fix: count NewItems summarization in CompactResult.SummarizedItems".

## Iteration 12 — `BindLocalRuntimeFlags` discarded caller-supplied defaults

- **Where:** `apps/launch/flags.go` `BindLocalRuntimeFlags`.
- **Bug:** The function bound four flags but used hard-coded zero values
  (`false`, `false`, `false`, `""`) as the `pflag.*Var` defaults instead
  of threading the current `opts.<field>` values through. The sibling
  helpers in the same file (`BindModelFlags`,
  `BindLaunchEnvironmentFlags`) correctly use `opts.<field>`, so any
  caller that initialised the opts struct with non-zero defaults and
  then called `BindLocalRuntimeFlags` had those defaults silently
  overwritten the moment the flag set was parsed.
- **Impact:** A wrapper that wants to default `Dev=true` (or pin
  `AllowMaxToolRisk` to a safe value) cannot do so via the struct; the
  pre-set value is dropped on parse and only `--dev=true` on the
  command line restores it.
- **Fix:** Pass `opts.Debug` / `opts.Yolo` / `opts.Dev` /
  `opts.AllowMaxToolRisk` as the flag defaults instead of the hard-coded
  zero values, matching the other `Bind*` helpers.
- **Regression tests:**
  `TestBindLocalRuntimeFlagsRespectsPreSetDefaults` initialises `opts`
  with all-truthy values, parses with no args, and asserts every field
  survives. Verified to fail on the old code (all four assertions
  trip). Companion `TestBindLocalRuntimeFlagsHonorsCommandLineOverride`
  proves the cli flag still wins when present.
- **Commit:** `a9954e7` — "fix: BindLocalRuntimeFlags must honor caller-supplied defaults".

## Iteration 13 — same UTF-8 truncation pattern in Slack `compactText`

- **Where:** `plugins/integrations/slack/run_observer.go` `compactText`.
- **Bug:** `text[:max] + "..."` — slices at byte `max` regardless of
  UTF-8 rune boundaries. With a multi-byte rune straddling the cut, the
  result was an invalid-UTF-8 string with a dangling continuation byte
  followed by `"..."`. Iteration 10 noted this helper as "cosmetic and
  left for a separate pass" — this is that pass.
- **Impact:** `compactText` feeds Slack message bodies via
  `postError` (`compactText(err.Error(), 600)`) wrapped in a code
  fence. With non-ASCII content in an error message — paths,
  usernames, plugin-returned text — the invalid bytes round-trip
  through Slack's API as replacement characters in the displayed
  message.
- **Reproduction:** 9 ASCII bytes + one 3-byte rune (`"€"`) with
  `max = 10`. Old code returns `"aaaaaaaaa\xe2..."` (invalid UTF-8);
  new code returns `"aaaaaaaaa..."`.
- **Fix:** Same approach as iterations 8 and 10 — scan backwards from
  the byte limit to the nearest `utf8.RuneStart` before slicing.
- **Regression test:** `TestCompactTextPreservesUTF8RuneBoundaries`
  feeds the multi-byte-boundary input and checks `utf8.ValidString`.
  Companion `TestCompactTextLeavesShortValidStrings` confirms the
  short-string path is untouched.
