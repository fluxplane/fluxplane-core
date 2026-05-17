# Go Test Diagnostics and Generic Test-Run Feedback

Date: 2026-05-17

## Implementation status

Status: implemented for the Go toolchain and terminal coder surface.

Implemented in this branch:

- Added inert `core/testrun` event/status/failure/summary contracts and helper
  coverage.
- Extended `core/language/golang.GoTestResult` with `TestRunEvent`.
- Updated `plugins/golangplugin` `go_test` to return concise PASS/FAIL text,
  summary counters, bounded failure details, truncation notes, and a finished
  generic `testrun.Event` in operation data.
- Preserved actionable build diagnostics, including a non-JSON diagnostic rerun
  for `go test -json` build failures that hide compiler stderr.
- Classified failures as build, assertion, panic, timeout, setup, or unknown.
- Added generic terminal rendering in `adapters/terminalui` and wired operation
  completion rendering to use `test_run_event` data when present.
- Added a `coder:live-test` task and AGENTS live-test guidance so future live
  checks use the Codex provider directly.

Known follow-ups / not implemented in this iteration:

- `test.run.started` and `test.run.progress` are modeled but not emitted as
  separate runtime stream events; `go_test` currently returns the final
  `test.run.finished` event in operation result data.
- Panic, timeout, setup, and truncation handling are implemented heuristically,
  but dedicated fixture regression tests for each case remain future hardening.
- Maven/npm/pytest producers are not implemented; the schema is intended to
  support them without changes.

Verification performed:

- `go test ./adapters/terminalui ./plugins/golangplugin ./core/testrun ./core/language/golang`
- `go build -trimpath ./...`
- Live coder smoke: `task coder:live-test -- 'Run native go_test for ./core/testrun with run=TestHasFailureKind. Keep the final answer very short.'`
  produced `✓ ✅ tests passed  ./core/testrun ...` in the terminal operation
  completion line.

## Context

The native `go_test` tool is useful for structured package execution, but its
failure output can be too terse. A reproduced failure only reported package-level
failure while hiding the actual compiler diagnostics; rerunning with shell
`go test` exposed the actionable file/line errors.

At the same time, test execution should produce user-facing progress and result
feedback that is not specific to Go. Future operations such as `maven_test`,
`npm_test`, `pytest`, or other toolchain-specific test runners should all feed a
shared UI notification path.

This design covers two related improvements:

1. make `go_test` compact but actionable by default, and
2. introduce a generic test-run event shape that any test operation can emit and
   UI adapters can render with concise emoji/status feedback.

## Goals

- Show the shortest output that answers: what failed, where, and why.
- Avoid shell fallback for ordinary compile, assertion, panic, timeout, and setup
  failures.
- Preserve token savings by omitting passing chatter by default.
- Keep emoji and presentation choices out of core contracts.
- Let Go, Maven, npm, pytest, and future test operations emit the same normalized
  event shape.
- Give terminal/web/Slack adapters one rendering path for test-run feedback.

## Non-goals

- Do not make `go_test` verbose by default.
- Do not put terminal emoji or formatted UI strings into core event contracts.
- Do not define Go-specific concepts in the generic event shape.
- Do not replace full logs or artifacts; this design only improves compact
  defaults and normalized status notifications.

## Current pain point

A failing native test run can currently produce output like:

```text
Go test: fail
- patterns: ./adapters/sqleventstore
- github.com/fluxplane/agentruntime/adapters/sqleventstore: fail
```

That is not enough to act on. In the reproduced case, shell `go test` showed the
real failure:

```text
runtime/thread/store.go:9:2: "strings" imported and not used
runtime/thread/store.go:121:14: no new variables on left side of :=
runtime/thread/store.go:171:15: undefined: threadIdempotencyKey
runtime/thread/store.go:172:14: undefined: threadIdempotencyKey
```

The native tool should have included those diagnostics directly.

## Desired compact `go_test` output

### Compile/build failure

```text
go_test: FAIL ./adapters/sqleventstore

build failed:
runtime/thread/store.go:9:2: "strings" imported and not used
runtime/thread/store.go:121:14: no new variables on left side of :=
runtime/thread/store.go:171:15: undefined: threadIdempotencyKey
runtime/thread/store.go:172:14: undefined: threadIdempotencyKey

summary: packages=1 failed=1
```

### Assertion failure

```text
go_test: FAIL ./runtime/thread

failed tests:
- TestStoreAppendRetriesSameThreadConflict
  store_test.go:262: Append returned error after retry: thread: append failed thread_id="thread-1" attempt=16/16: append conflict

summary: tests=6 passed=5 failed=1
```

### Panic

```text
go_test: FAIL ./orchestration/taskexecutor

panic:
- TestExecutorPersistsEvents
  panic: runtime error: invalid memory address or nil pointer dereference
  executor_test.go:88 +0x34
  executor.go:142 +0x91

summary: tests=12 passed=11 failed=1
```

### Multiple packages

```text
go_test: FAIL ./...

failed packages:
- runtime/thread
  build failed:
  runtime/thread/store.go:171:15: undefined: threadIdempotencyKey

- adapters/sqleventstore
  depends on failed package runtime/thread

summary: packages=40 passed=38 failed=2
```

### Truncation-aware output

```text
go_test: FAIL ./...

failed tests:
- TestTaskExecutorRun
  executor_test.go:144: got status "running", want "completed"

stderr tail:
task verify: lint failed: 3 diagnostics

summary: packages=40 passed=39 failed=1
truncated: omitted 184 passing output lines; use verbose=true for full output
```

## Default output shape

```text
go_test: PASS|FAIL <patterns>

<only if failed>
build failed:
<file:line errors, bounded>

failed tests:
- <TestName>
  <file:line>: <message, bounded>

panic:
- <TestName/package>
  <panic line>
  <top useful stack frames, bounded>

summary: packages=N passed=N failed=N skipped=N; tests=N passed=N failed=N skipped=N
<truncation note if applicable>
```

Defaults should be concise-first:

- no passing test logs,
- no raw JSON,
- no repeated package metadata,
- no duplicate compiler errors under every dependent package,
- failure context survives truncation.

Possible future options:

```json
{
  "diagnostics": "compact",
  "max_failures": 5,
  "max_lines_per_failure": 5,
  "include_passing_output": false
}
```

`diagnostics` could support values such as `compact`, `full`, or `summary` later.

## Diagnostic collection rules

`go_test` should preserve a compact failure buffer from `go test -json` and raw
stderr/stdout.

Collect:

- build and compile errors,
- package setup failures,
- failing test names,
- `t.Fatal` / `t.Errorf` assertion output,
- panic messages,
- top useful stack frames,
- timeout messages,
- stderr tail.

Prioritize output budget in this order:

1. package/test failure headers,
2. compiler/build errors,
3. failing assertion lines,
4. panic message and top stack frames,
5. timeout/setup errors,
6. stderr tail,
7. final package/test summary.

If output must be truncated, say what was omitted:

```text
truncated: omitted 184 passing output lines; use verbose=true for full output
```

## Generic test-run feedback event

Test execution should also dispatch a normalized event that UI adapters can
render consistently. The event must be independent of Go so future test
operations can reuse it.

Suggested package placement:

```text
core/testrun
```

Reasoning:

- It is an inert cross-toolchain event contract.
- It contains no IO, execution, terminal formatting, or emoji.
- Runtime/plugin/tool operations can produce it.
- UI adapters can render it.

## Proposed event model

```go
package testrun

import "time"

type EventKind string

const (
	EventStarted  EventKind = "test.run.started"
	EventProgress EventKind = "test.run.progress"
	EventFinished EventKind = "test.run.finished"
)

type Status string

const (
	StatusRunning Status = "running"
	StatusPassed  Status = "passed"
	StatusFailed  Status = "failed"
	StatusSkipped Status = "skipped"
	StatusError   Status = "error"
)

type FailureKind string

const (
	FailureAssertion FailureKind = "assertion"
	FailureBuild     FailureKind = "build"
	FailurePanic     FailureKind = "panic"
	FailureTimeout   FailureKind = "timeout"
	FailureSetup     FailureKind = "setup"
	FailureUnknown   FailureKind = "unknown"
)

type Event struct {
	Kind      EventKind `json:"kind"`
	RunID     string    `json:"run_id"`
	Toolchain string    `json:"toolchain"`
	Command   string    `json:"command,omitempty"`
	Target    string    `json:"target,omitempty"`

	Status Status `json:"status"`

	Summary Summary `json:"summary"`

	Failures []Failure `json:"failures,omitempty"`

	StartedAt  time.Time `json:"started_at,omitempty"`
	FinishedAt time.Time `json:"finished_at,omitempty"`
	DurationMS int64     `json:"duration_ms,omitempty"`

	Truncated bool   `json:"truncated,omitempty"`
	Note      string `json:"note,omitempty"`
}

type Summary struct {
	PackagesTotal   int `json:"packages_total,omitempty"`
	PackagesPassed  int `json:"packages_passed,omitempty"`
	PackagesFailed  int `json:"packages_failed,omitempty"`
	PackagesSkipped int `json:"packages_skipped,omitempty"`

	TestsTotal   int `json:"tests_total,omitempty"`
	TestsPassed  int `json:"tests_passed,omitempty"`
	TestsFailed  int `json:"tests_failed,omitempty"`
	TestsSkipped int `json:"tests_skipped,omitempty"`
}

type Failure struct {
	Kind    FailureKind `json:"kind"`
	Package string      `json:"package,omitempty"`
	Test    string      `json:"test,omitempty"`

	File   string `json:"file,omitempty"`
	Line   int    `json:"line,omitempty"`
	Column int    `json:"column,omitempty"`

	Message string   `json:"message"`
	Details []string `json:"details,omitempty"`
}
```

## Example events and UI rendering

### Passing Go test event

```json
{
  "kind": "test.run.finished",
  "run_id": "test_01HX...",
  "toolchain": "go",
  "command": "go test ./runtime/thread",
  "target": "./runtime/thread",
  "status": "passed",
  "summary": {
    "packages_total": 1,
    "packages_passed": 1,
    "tests_total": 6,
    "tests_passed": 6
  },
  "duration_ms": 420
}
```

Terminal UI rendering:

```text
✅ tests passed  ./runtime/thread
6 tests · 1 package · 0.42s
```

### Compile failure event

```json
{
  "kind": "test.run.finished",
  "run_id": "test_01HX...",
  "toolchain": "go",
  "command": "go test ./adapters/sqleventstore",
  "target": "./adapters/sqleventstore",
  "status": "failed",
  "summary": {
    "packages_total": 1,
    "packages_failed": 1
  },
  "failures": [
    {
      "kind": "build",
      "package": "github.com/fluxplane/agentruntime/runtime/thread",
      "file": "runtime/thread/store.go",
      "line": 171,
      "column": 15,
      "message": "undefined: threadIdempotencyKey",
      "details": [
        "runtime/thread/store.go:172:14: undefined: threadIdempotencyKey"
      ]
    }
  ],
  "duration_ms": 310
}
```

Terminal UI rendering:

```text
🛠️ tests failed to build  ./adapters/sqleventstore
runtime/thread/store.go:171:15 undefined: threadIdempotencyKey
1 package failed · 0.31s
```

### Assertion failure event

```json
{
  "kind": "test.run.finished",
  "run_id": "test_01HX...",
  "toolchain": "go",
  "target": "./runtime/thread",
  "status": "failed",
  "summary": {
    "packages_total": 1,
    "packages_failed": 1,
    "tests_total": 6,
    "tests_passed": 5,
    "tests_failed": 1
  },
  "failures": [
    {
      "kind": "assertion",
      "package": "github.com/fluxplane/agentruntime/runtime/thread",
      "test": "TestStoreAppendRetriesSameThreadConflict",
      "file": "runtime/thread/store_test.go",
      "line": 262,
      "message": "Append returned error after retry: thread: append failed thread_id=\"thread-1\""
    }
  ],
  "duration_ms": 440
}
```

Terminal UI rendering:

```text
❌ tests failed  ./runtime/thread
TestStoreAppendRetriesSameThreadConflict
store_test.go:262 Append returned error after retry
5 passed · 1 failed · 0.44s
```

### Maven example using the same event

```json
{
  "kind": "test.run.finished",
  "run_id": "test_01HY...",
  "toolchain": "maven",
  "command": "mvn test",
  "target": "billing-service",
  "status": "failed",
  "summary": {
    "tests_total": 184,
    "tests_passed": 183,
    "tests_failed": 1
  },
  "failures": [
    {
      "kind": "assertion",
      "package": "com.example.billing.InvoiceServiceTest",
      "test": "calculatesTax",
      "file": "src/test/java/com/example/billing/InvoiceServiceTest.java",
      "line": 88,
      "message": "expected <19.99> but was <20.01>"
    }
  ],
  "duration_ms": 9210
}
```

Terminal UI rendering:

```text
❌ tests failed  billing-service
InvoiceServiceTest.calculatesTax
InvoiceServiceTest.java:88 expected <19.99> but was <20.01>
183 passed · 1 failed · 9.21s
```

## Rendering rules

Emoji and formatted text belong in adapters, not in the event contract.

Example mapping:

```go
func IconForTestRun(e testrun.Event) string {
	switch {
	case e.Status == testrun.StatusPassed:
		return "✅"
	case hasFailureKind(e, testrun.FailureBuild):
		return "🛠️"
	case hasFailureKind(e, testrun.FailurePanic):
		return "💥"
	case e.Status == testrun.StatusFailed:
		return "❌"
	case e.Status == testrun.StatusRunning:
		return "🧪"
	case e.Status == testrun.StatusSkipped:
		return "⚪"
	default:
		return "🧪"
	}
}
```

Suggested compact renderings:

```text
🧪 running tests  ./...
✅ tests passed  ./runtime/thread
🛠️ tests failed to build  ./adapters/sqleventstore
❌ tests failed  ./runtime/thread
💥 test panic  ./orchestration/taskexecutor
⚪ no tests  ./plugins/memory
```

## Implementation sketch

### 1. Add core event contract

Add an inert package such as:

```text
core/testrun
```

It should contain only stable event/status/failure/summary shapes and small
helpers if needed. No IO, no terminal rendering, no execution.

### 2. Improve `go_test` parsing and summarization

Update the Go test operation to:

- preserve raw build/setup diagnostics,
- associate JSON output lines with failing tests when possible,
- retain package-level output when no test name exists,
- classify failures as build, assertion, panic, timeout, setup, or unknown,
- deduplicate repeated compiler errors from dependent packages,
- produce a compact human summary by default.

### 3. Emit normalized test-run events

When `go_test` starts and finishes, emit generic `testrun.Event` records:

- `test.run.started` with status `running`,
- optional `test.run.progress` for long runs,
- `test.run.finished` with final status, summary, failures, duration, and
  truncation note.

The operation result can include both:

- compact model-facing text, and
- structured `testrun.Event` data for UI/adapters.

### 4. Add terminal UI rendering

Add adapter-side rendering that listens for or receives `testrun.Event` and maps
it to concise notification lines. Keep this rendering generic so Maven/npm/pytest
can reuse it later.

### 5. Add tests

Regression coverage should include:

1. compile failure includes file/line compiler diagnostics,
2. assertion failure includes test name and assertion text,
3. panic includes panic message and bounded stack frames,
4. large passing output does not hide the failure,
5. package setup failure is shown,
6. event status and failure classification are correct,
7. terminal renderer maps generic events to expected concise strings.

## Architecture notes

Expected dependency flow:

```text
core/testrun
  inert event contract

Go test operation/plugin
  parses go test output -> testrun.Event + compact text

adapters/terminalui or other UI adapters
  renders testrun.Event -> emoji/status text
```

This keeps the layer rules intact:

- core owns the stable cross-toolchain shape,
- runtime/plugin/tool implementation owns execution and parsing,
- adapters own rendering and emoji.

## Acceptance criteria

- `go_test` compile failures include actionable file/line diagnostics by default.
- `go_test` assertion failures include test name and failure text by default.
- `go_test` panic failures include panic message and bounded stack context.
- Passing test output remains compact.
- Truncation preserves failure details and states what was omitted.
- `go_test` emits or returns a generic `testrun.Event` shape.
- Terminal/UI rendering of `testrun.Event` is generic and not Go-specific.
- The same event shape can represent Maven/npm/pytest test results without schema changes.

## Closing checklist

- [x] `go_test` compile failures include actionable file/line diagnostics by default.
- [x] `go_test` assertion failures include test name and failure text by default.
- [x] `go_test` panic failures include panic message and bounded stack context heuristically.
- [x] Passing test output remains compact.
- [x] Truncation preserves failure details and states what was omitted.
- [x] `go_test` returns a generic `testrun.Event` shape in operation result data.
- [x] Terminal/UI rendering of `testrun.Event` is generic and not Go-specific.
- [x] The same event shape can represent Maven/npm/pytest test results without schema changes.
- [ ] Emit started/progress events as live runtime events for long-running test operations.
- [ ] Add dedicated panic/timeout/setup/truncation fixture tests.
