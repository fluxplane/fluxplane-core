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
- **Commit:** `9a8e393` — "fix: compactText in slack observer must respect UTF-8 rune boundaries".

## Iteration 14 — same pattern in `runtime/datasource/detect.go` `truncateBytes`

- **Where:** `runtime/datasource/detect.go` `truncateBytes`.
- **Bug:** Final outstanding member of the UTF-8 truncation trilogy
  noted in iteration 10. `truncateBytes(value, max)` returned
  `value[:max]` without respecting rune boundaries, used to cap
  detection source text to 64KB before running the regex detector.
- **Impact:** When source text exceeded the byte cap and a multi-byte
  rune straddled the cut, the truncated buffer ended with a partial
  UTF-8 sequence. If a regex match touched that region, the resulting
  `RecordRef.SourceText` carried invalid UTF-8 to anything that rendered
  it (and `regexp.FindAllStringSubmatchIndex` treats invalid UTF-8 as
  individual U+FFFD runes, so a match could shift unexpectedly).
- **Fix:** Same rune-start scan as the previous two truncation fixes.
- **Regression test:** `TestTruncateBytesPreservesUTF8RuneBoundaries`
  feeds 9 ASCII + 3-byte rune with cap 10. Companion
  `TestTruncateBytesLeavesShortValues` checks the short-string path.
  Verified to fail on the old code with
  `"truncateBytes produced invalid UTF-8: \"aaaaaaaaa\\xe2\""`.
- **Commit:** `3685ddf` — "fix: truncateBytes in datasource detect must respect UTF-8 boundaries".

## Iteration 15 — `selectInstance` produced `unknown instance "<nil>"` on missing key

- **Where:** `runtime/operation/named_instance.go` `selectInstance`.
- **Bug:** Same `fmt.Sprint(value)` foot-gun fixed in iteration 6 for
  `runtime/operation/authorization.go` `stringField`, now in a second
  location.
  `instance := strings.TrimSpace(fmt.Sprint(values["instance"]))`
  produced:
  - `"<nil>"` when the key was missing or the value was JSON null
  - `"true"` / `"false"` for boolean values
  - `"42"` for numeric values
  The "instance is required" check (`instance == ""`) never tripped for
  any of these. The user got the confusing
  `unknown instance "<nil>"` (or `"42"`, etc.) error instead.
- **Fix:** Type-assert `values["instance"]` to `string` so any
  non-string (including JSON null and missing key) collapses to "" and
  the "instance is required" check runs.
- **Regression test:** `TestNamedInstanceRejectsNonStringInstanceWithStableMessage`
  tabulates four cases — missing key, nil value, boolean, number — and
  asserts every one returns an error containing `"instance is required"`.
  All four cases fail on the old code with
  `unknown instance "<nil>"` / `"true"` / `"42"` messages, all pass on
  the fix.
- **Commit:** `215b744` — "fix: selectInstance must error with \"instance is required\" on missing key".

## Iteration 16 — same UTF-8 truncation pattern in `shortLogValue`

- **Where:** `apps/launch/serve_verbose.go` `shortLogValue`.
- **Bug:** Fifth occurrence of the truncation pattern that lost track
  of UTF-8 rune boundaries. `text[:limit-3] + "..."` chops at byte
  117 regardless of where multi-byte runes sit, so verbose serve-event
  log lines could end with a dangling continuation byte followed by
  `"..."`.
- **Impact:** Diagnostic-only — corrupts log output for non-ASCII
  values. Some log aggregators reject lines that aren't valid UTF-8.
- **Reproduction:** 116 ASCII + 3-byte rune `"€"` + filler. Byte 117
  lands inside the rune; the old function returned a string ending
  `"...\xe2..."`.
- **Fix:** Same rune-start scan as iterations 8, 10, 13, 14.
- **Regression tests:** `TestShortLogValuePreservesUTF8RuneBoundaries`
  and `TestShortLogValueLeavesShortStrings`. First fails on the old
  code with `"...aaaaaa\xe2..."`.
- **Commit:** `efa8e8b` — "fix: shortLogValue must respect UTF-8 rune boundaries".

## Iteration 17 — same UTF-8 truncation pattern in `sessioncontrol.truncateText`

- **Where:** `orchestration/sessioncontrol/control.go` `truncateText`.
- **Bug:** Sixth occurrence of the byte-cut-without-rune-boundary
  truncation pattern. `text[:max]` chops at byte `max` regardless of
  multi-byte rune boundaries.
- **Impact:** This one feeds LLM stop-condition prompts —
  `finalizerDescription`-style helpers call
  `truncateText(fmt.Sprint(effect.Result.Output), 2000)` and
  `truncateText(effect.Result.Error.Message, 1000)` and concatenate the
  result into a prompt that is then sent to a model. The provider's
  JSON encoder will escape invalid UTF-8 to `�`, so end-to-end
  it's "safe", but the model sees a replacement character where the
  trailing rune used to be — which can derail short-text matching the
  evaluator was trying to do.
- **Fix:** Same rune-start scan as iterations 8, 10, 13, 14, 16.
- **Regression tests:** `TestTruncateTextPreservesUTF8RuneBoundaries`
  (multi-byte boundary), `TestTruncateTextLeavesShortValues`
  (short-string path), and `TestTruncateTextHonorsZeroAndNegativeMax`
  (defensive cases). First fails on the old code with
  `"truncateText produced invalid UTF-8: \"aaaaaaaaa\\xe2\""`.
- **Commit:** `c7572bb` — "fix: sessioncontrol truncateText must respect UTF-8 rune boundaries".

## Iteration 18 — `unescapeDoubleQuotedEnv` dropped the backslash for unknown escapes

- **Where:** `runtime/system/env.go` `unescapeDoubleQuotedEnv`.
- **Bug:** The `default` case of the escape switch wrote only the
  following rune, silently dropping the leading backslash. Combined
  with the known cases above it, the function turned `\n`/`\r`/`\t`
  into their control characters (correct), `\\` and `\"` into `\` and
  `"` (correct), but `\$`/`\x`/`\<anything-else>` into just the bare
  character (wrong). The dotenv convention — and the user's
  intuition — is that an unknown escape stays literal.
- **Reproduction:** `KEY="hi \$world"` in an env file → old parser
  yields `hi $world`. Same for `KEY="path \% literal"` → `path % literal`.
- **Impact:** Users who write `\$` (or any other dollar sequence) into
  a quoted env value to defend against shell expansion get a different
  value than what's in the file. Anything that round-trips env values
  through this parser similarly mutates content unexpectedly.
- **Fix:** In the `default` branch, write the backslash before the
  following rune so unknown escapes survive verbatim.
- **Regression tests:** `TestUnescapeDoubleQuotedEnvPreservesUnknownEscapes`
  tables seven cases (known escapes, escaped quote, escaped backslash,
  unknown `\$`, unknown `\x`, plain text); the unknown-escape rows fail
  on the old code (`"hi $world"` vs the wanted `"hi \$world"`).
  `TestUnescapeDoubleQuotedEnvRejectsTrailingEscape` confirms the
  defensive trailing-backslash error path still trips.

## Iteration 11 — docker-image target treated custom Dockerfile as out-dir-relative

- **Where:** `adapters/distribution/deploy/build.go` docker-image target handling.
- **Bug:** `distribution.build.targets.<name>.dockerfile` was resolved with
  `targetOutput(outDir, ...)`, so a relative custom Dockerfile path was
  interpreted under `--out-dir` instead of the app root. Builds that used an
  external artifact directory plus `dockerfile: deploy/CustomDockerfile` tried
  to build from `<outDir>/deploy/CustomDockerfile`, even though Docker's build
  context is the app root and existing no-out-dir behavior treated the value as
  app-relative.
- **Reproduction:** `TestBuildAppImageUsesConfiguredDockerfileRelativeToAppRootWithOutDir`
  configures `dockerfile: deploy/CustomDockerfile`, sets `OutDir` elsewhere,
  and records the Dockerfile passed to the Docker client. Old code passed the
  out-dir-relative path; the fix passes the app-root-relative path.
- **Fix:** Added `dockerImageDockerfilePath`: generated/managed Dockerfiles
  still live in `outDir`, absolute paths remain absolute, but explicit relative
  Dockerfile paths resolve under the app root. Target listing now uses the same
  helper so displayed output matches build behavior.
- **Regression test:** `go test ./adapters/distribution/deploy -run TestBuildAppImageUsesConfiguredDockerfileRelativeToAppRootWithOutDir`
  fails on the old code and passes after the fix. Full package tests also pass.
- **Commit:** `cfb83f0` — "fix: preserve backslash for unknown escapes in env value parser".

## Iteration 19 — semantic `Search` non-deterministic when scores tie

- **Where:** `runtime/datasource/semantic/store.go` `JSONStore.Search` and
  `JSONStore.UpsertChunks`.
- **Bug:** Two compounding sources of non-determinism:
  1. `UpsertChunks` rebuilt `state.Chunks` by iterating a
     `map[string]EmbeddedChunk` and appending in iteration order. Go map
     iteration is randomised, so the persisted chunk order varied across
     runs.
  2. `Search` sorted result hits by `Score` with `sort.Slice` (unstable)
     and no tie-breaker, so hits with identical scores landed in
     whichever order the unstable sort happened to leave them.
- **Impact:** Same vector query against the same store returned hits in
  a different order each run when scores tied — flaky tests, surprising
  pagination, and shifting top-K when `req.Limit` truncates a tied
  group.
- **Reproduction:** Three chunks (ids `a`, `b`, `c`) with the *same*
  vector against itself produce three hits with identical cosine
  scores. Across 10 fresh stores, the old code returned random orders
  like `c, a, b`.
- **Fix:** Sort `state.Chunks` by `Chunk.ID` after rebuilding from the
  map in `UpsertChunks`; add `Chunk.ID` as the tie-breaker in
  `Search`'s sort comparator.
- **Regression test:** `TestSearchTieBreaksDeterministically` upserts
  three identical-vector chunks into a fresh store 10 times and
  asserts the result order is `a, b, c` each time. Fails on the old
  code with random orderings.
- **Commit:** `579c74b` — "fix: deterministic ordering for semantic Search ties and UpsertChunks state".

## Iteration 20 — same tie-break missing in `Index.Search` wrapper

- **Where:** `runtime/datasource/semantic/index.go` `Index.Search`.
- **Bug:** Even after iteration 19's `JSONStore.Search` fix, the
  higher-level `Index.Search` re-introduced the same non-determinism:
  it grouped hits by `DocumentKey` into a `map[string]Hit`, then built
  `out` by iterating that map (random order), then ran `sort.Slice` by
  `Score` with no tie-breaker.
- **Impact:** Documents with identical scores returned in random order
  across runs at the call site that callers actually use — `Index.Search`,
  not `JSONStore.Search`. The store-layer fix from iteration 19 was
  invisible to anyone going through the index.
- **Reproduction:** Three docs with identical `Title` and `Body`
  embed to the same vector, so cosine against the same query yields
  three tied scores. Across five fresh indexes the old code
  returned random orders like `[b.md a.md c.md]`, `[b.md c.md a.md]`.
- **Fix:** Tie-break by `Ref.ID`, then `Ref.Datasource`, then
  `Ref.Entity` inside the existing `sort.Slice` comparator. (No need
  to stable-sort or also touch the map iteration — the comparator now
  picks a deterministic winner for any tie.)
- **Regression test:** `TestIndexSearchTieBreaksDeterministically`
  upserts three docs with matching title+body into a fresh index five
  times and asserts the hit order is `a.md, b.md, c.md` each
  iteration. Fails on the old code with random orderings.
- **Commit:** `49a9cfc` — "fix: tie-break Index.Search results deterministically by Ref.ID".

## Iteration 21 — `summarizeEvent` / `targetSubmit` leaked `"<nil>"` outbound text

- **Where:** `apps/evaluator/operations.go` — `targetSubmit` result
  shaping and `summarizeEvent`.
- **Bug:** Third instance of the `fmt.Sprint(value)` foot-gun in this
  trial (after iteration 6's `stringField` and iteration 15's
  `selectInstance`). Both helpers gated only on
  `Outbound != nil && Outbound.Message != nil` and then wrote
  `fmt.Sprint(Outbound.Message.Content)` into the output text field.
  `channel.Message.Content` is `any`, so a `nil` content — a valid
  "the message has no body" state — produced the literal string
  `"<nil>"` in JSON output.
- **Impact:** Evaluator and downstream dashboards see a stray `"<nil>"`
  in the Outbound text of submitted events where they should see the
  empty string. Anyone comparing event summaries between runs has to
  special-case the sentinel.
- **Fix:** Add the third guard
  `Outbound.Message.Content != nil` before the `fmt.Sprint`, so nil
  content leaves the destination field at its zero value.
- **Regression tests:** `TestSummarizeEventNilContentDoesNotLeakNilString`
  builds an event with nil content and asserts `Outbound == ""`;
  fails on the old code with `Outbound = "<nil>"`. Companion
  `TestSummarizeEventPreservesStringContent` pins the happy path.

## Iteration 22 — task finalizer evidence compaction split UTF-8 runes

- **Where:** `orchestration/taskexecutor/executor.go` `compactWorkerEvidence`.
- **Bug:** `compactWorkerEvidence` collapsed worker output and then truncated
  with `text[:max]` or `text[:max-3] + "..."`, slicing by byte count without
  respecting UTF-8 rune boundaries. A multi-byte rune crossing the cut left a
  dangling byte in the finalizer prompt.
- **Impact:** The helper feeds completed step evidence into the task finalizer
  worker prompt. Non-ASCII output from tools, file paths, or user content could
  be corrupted into invalid UTF-8 before the follow-up worker saw it.
- **Reproduction:** 236 ASCII bytes + `"€"` + filler with max 240 made the old
  code cut at byte 237 before appending `"..."`, producing invalid UTF-8.
- **Fix:** Added `trimToRuneBoundary` and used it for both short max values and
  ellipsis truncation.
- **Regression tests:** `TestCompactWorkerEvidencePreservesUTF8RuneBoundaries`
  and `TestCompactWorkerEvidenceLeavesShortValues` in the taskexecutor package.
- **Verification:** `go test ./orchestration/taskexecutor`.

## Iteration 22 — `writeReplacementFile` leaked the temp dir on write failure

- **Where:** `runtime/operation/replacement.go` `writeReplacementFile`.
- **Bug:** The function calls `os.MkdirTemp(root, "fluxplane-tool-result-*")`
  to allocate a unique scratch directory, then immediately writes
  `result.json` inside it. If the write fails (disk full, permission
  flip mid-flight, ENOSPC, etc.), the function returns the error
  without removing the directory it just created. Each failed write
  orphans one `fluxplane-tool-result-*` directory until the host OS
  cleans `TempDir` — which on Linux can mean reboot, not minutes.
- **Impact:** Repeated failed oversized-result replacements (typical
  symptom: tight `/tmp` quota or a buggy filesystem) leak a fresh temp
  directory each call. Over a long-running session those accumulate.
- **Fix:** `_ = os.RemoveAll(dir)` before returning the write error, so
  the cleanup mirrors the standard temp-file/temp-dir cleanup pattern
  (the same one I added to `writeFileAtomic` in iteration 5).
- **No regression test:** Reliably forcing `os.WriteFile` to fail
  without an invasive refactor (e.g., injecting a writer) is awkward
  and racy across platforms. The fix is a one-line cleanup mirroring
  iteration 5's atomic-write helper; an explicit nil-check / RemoveAll
  before the return is mechanical enough that further tooling would
  not add coverage.
- **Commit:** `6f948c7` — "fix: clean up scratch dir when writeReplacementFile fails".

## Iteration 23 — `EnvResolver.ResolveSecret` dropped the caller's context

- **Where:** `runtime/secret/secret.go` `EnvResolver.ResolveSecret`.
- **Bug:** The method took `_ context.Context` (discarded) and then
  called `r.Environment.Lookup(context.Background(), ref.Name)` —
  silently dropping the caller's deadline, cancellation channel, and
  any context-attached values (tracing IDs, scoped policies, etc.)
  before invoking the env lookup.
- **Impact:** Anything wired through `Environment` that's slower than
  an in-process map lookup — a file-backed env reader, a network
  resolver, a sandboxed exec — could not be cancelled by the caller
  and could not see request-scoped metadata. In short timeouts and
  shutdown paths this means the resolver runs to completion regardless
  of what the request lifecycle wants.
- **Fix:** Receive the context as a named parameter, default a nil
  context to `context.Background()`, and pass it through to
  `Environment.Lookup`.
- **Regression test:** `TestEnvResolverPropagatesContext` installs a
  custom `Environment` that records the context it was called with and
  asserts the test's `context.WithValue` marker survives the call.
  Fails on the old code with the empty zero value.
- **Commit:** `76e088d` — "fix: EnvResolver.ResolveSecret must propagate the caller's context".

## Iteration 24 — `postTokenForm` did an unbounded `io.ReadAll(resp.Body)`

- **Where:** `runtime/oauth2client/client.go` `postTokenForm`.
- **Bug:** The OAuth2 token exchange read the entire response body with
  `io.ReadAll(resp.Body)` — no size cap. A malicious, misconfigured, or
  buggy token endpoint could return an arbitrarily large body and the
  process would dutifully buffer it all into memory before failing on
  JSON decode. Real token responses are well under 10KB.
- **Impact:** Unbounded memory growth from a single OAuth refresh /
  exchange request. The token endpoint is whichever URL the caller
  configured, and `runtime/oauth2client` is the helper that
  `claudecode/auth.go`, `codex/auth.go`, and `authconnect` all reach
  for, so any of those can be poisoned by a hostile or compromised
  upstream.
- **Fix:** Cap the read at `maxTokenResponseBytes = 1 MiB` via
  `io.LimitReader(resp.Body, maxTokenResponseBytes)`. The cap is two
  orders of magnitude above realistic token response sizes; well-behaved
  endpoints are unaffected.
- **Regression tests:** `TestPostTokenFormPreservesSmallResponse`
  pins the happy path. `TestPostTokenFormCapsResponseSize` serves a
  2 MiB body padded with whitespace and asserts the function either
  returns the legitimate token or a structured decode error — what
  must NOT happen is buffering the full 2 MiB, which would have OOMed
  the test on small CI runners under the old code.
- **Commit:** `67c20a2` — "fix: cap OAuth2 token endpoint response at 1 MiB".

## Iteration 25 — Anthropic non-streaming response body was unbounded

- **Where:** `adapters/llm/anthropicmessages/model.go` `doJSON`.
- **Bug:** Same class as iteration 24, this time in the LLM client. The
  non-streaming completion path did
  `io.ReadAll(resp.Body)` with no cap. The Anthropic HTTP client is
  built from `httptransport.CloneDefaultHTTPClient()`, which applies
  no transport-level size limit, so a malicious / compromised /
  misbehaving upstream could OOM the client by returning an
  arbitrarily large response body for `json.Unmarshal` to chase.
- **Impact:** Any of the providers that route through this package —
  Anthropic, Claude Code, and anything else using
  `anthropicmessages.Model` directly — can be poisoned by a hostile
  upstream. The streaming error path already wraps in `LimitReader`
  (64 KiB), so only the non-streaming success path was exposed.
- **Fix:** Added `maxNonStreamingResponseBytes = 100 MiB` and wrapped
  the read in `io.LimitReader`. 100 MiB is two orders of magnitude
  above any realistic Anthropic response at the default 4096-token
  output budget, so happy-path behavior is unchanged; the cap exists
  purely to bound a hostile body.
- **No regression test:** Materialising a >100 MiB response in a unit
  test is heavy and would dominate CI time. The fix is mechanical and
  mirrors iteration 24.

## Iteration 26 — OpenRouter non-streaming fallback body was unbounded

- **Where:** `adapters/llm/openrouter/responses_reliability.go`
  `synthesizeStreamFromResponse`.
- **Bug:** Third instance of the same DoS class as iterations 24/25.
  After the OpenRouter `/responses` streaming retries are exhausted,
  the middleware falls back to a non-streaming request and turns the
  body into a synthetic SSE stream. That fallback read used
  `io.ReadAll(resp.Body)` with no size cap. A misbehaving or hostile
  OpenRouter / upstream could return an arbitrarily large body and the
  client would dutifully buffer it all before parsing.
- **Impact:** Affects every OpenRouter Responses-API call that goes
  through the streaming retry path — i.e. the default path used by
  `adapters/llm/openrouter`. The streaming branches already bound
  reads (`bufio.Scanner` with a fixed buffer per line), so only the
  rare-but-reachable fallback was exposed.
- **Fix:** Added `maxNonStreamingResponseBytes = 100 * 1024 * 1024`
  and wrapped the read in `io.LimitReader`. 100 MiB is two orders of
  magnitude above realistic OpenAI/OpenRouter Response bodies, so the
  happy path is unchanged; the cap exists purely to bound a hostile
  body.
- **No regression test:** Same reasoning as iteration 25 — staging a
  >100 MiB response in a unit test is heavy and the fix is mechanical
  and mirrors the two prior iterations.
- **Commit:** `2ae7358` — "fix: cap OpenRouter non-streaming fallback body at 100 MiB".

## Iteration 27 — `artifactValueText` truncated mid-rune

- **Where:** `core/task/task.go` `artifactValueText`.
- **Bug:** The 240-byte truncation in `artifactValueText` did a flat
  `text[:limit]` without checking that `limit` landed on a UTF-8 rune
  boundary. If the value being summarised had a multi-byte rune (e.g.
  a 4-byte emoji) straddling byte 240, the truncated string ended
  with an invalid UTF-8 sequence followed by `...[truncated]`.
- **Impact:** `artifactValueText` feeds `artifactDetail`, which is
  emitted to task-detail logs, dashboards, and anywhere artifact
  contents are summarised. Invalid UTF-8 then either silently mutates
  to `U+FFFD` on JSON encoding, gets rejected by `utf8mb4` MySQL
  columns, or otherwise corrupts downstream consumers. Same class as
  iterations 8 / 10 / 13 / 14 / 16 / 17, this time in
  `core/task`.
- **Fix:** Added `unicode/utf8` import and a backscan that walks
  `end` back to the nearest rune-start byte before slicing. Realistic
  ASCII inputs are unaffected; the only behavior change is that
  truncated tails are now always valid UTF-8.
- **Regression test:** `TestArtifactValueTextTruncatesOnRuneBoundary`
  pads with 238 ASCII bytes, appends a 4-byte emoji (`\xF0\x9F\x8C\x8D`,
  🌍) so its second byte lands at index 240, then appends more bytes.
  It asserts `utf8.ValidString(body)` is true on the truncated body —
  the old code produced `\xF0\x9F\x8C` at the tail, which fails this
  check.
- **Commit:** `5511e6e` — "fix: truncate artifact text on a rune boundary".

## Iteration 28 — `frontmatterStrings` leaked `"<nil>"` for empty YAML list items

- **Where:** `adapters/resources/agentdir/loader.go` `frontmatterStrings`,
  `[]any` branch.
- **Bug:** Same `fmt.Sprint(nil)` foot-gun class as iteration 21,
  this time on the YAML frontmatter ingestion path. A frontmatter
  list with an empty entry —
  ```yaml
  tools:
    - bash
    -
    - read
  ```
  decodes to `[]any{"bash", nil, "read"}`. The old code wrote the
  literal string `"<nil>"` into the result because
  `fmt.Sprint((interface{})(nil)) == "<nil>"`. `cleanStrings` then
  kept `"<nil>"` (non-empty, trimmed identically), so the resulting
  tools / skills / triggers / capabilities list contained a phantom
  `"<nil>"` reference that silently broke agent definitions at
  runtime.
- **Impact:** Affects every agent, command, workflow, and skill that
  uses `frontmatterStrings` for `Tools`, `Commands`, `Skills`,
  `Capabilities`, `AllowedTools`, or `Triggers` lists. A user typo
  (trailing whitespace on a list item, intentional blank for a
  template-substituted value) silently corrupts the spec instead of
  being ignored.
- **Fix:** Skip `nil` items in the `[]any` branch before
  `fmt.Sprint`. Non-nil typed values (ints, bools, strings) still
  get coerced, matching prior behavior.
- **Regression test:** `TestFrontmatterStringsSkipsNilListEntries`
  passes `[]any{"bash", nil, "read"}` and asserts the result is
  exactly `["bash", "read"]` with no `"<nil>"` string anywhere.
- **Commit:** `f0c5d87` — "fix: skip nil items when coercing YAML frontmatter list".
