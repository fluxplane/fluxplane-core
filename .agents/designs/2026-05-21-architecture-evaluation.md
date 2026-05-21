# Architecture Evaluation

## Status

Implemented. The evaluator now reports component scores, diagnostics for
test-only boundary violations, host IO and plugin side-effect findings, runtime
host IO allowlist coverage, unknown package diagnostics, and explicit
`-fail-on` gates while keeping production boundary violations as the hard
`task arch:check` gate.

## Problem

The current architecture checker correctly enforces the production layer
matrix, but the report's single numeric score no longer communicates severity.
The current production report has zero layer violations while still scoring
`0/100`:

```text
go run ./apps/archreport
Architecture score: 0/100
Packages: 182  Edges: 1176  Violations: 0
```

The score bottoms out because soft review penalties exceed 100 before any hard
violation is present:

- runtime sibling edges are penalized globally;
- inner-layer fan-out above 12 is penalized uniformly;
- known composition or aggregation packages are treated the same as ordinary
  concept packages.

This makes a zero-violation architecture look as severe as one with boundary
breaks. It also creates pressure to move code for the metric instead of for the
architecture, which is wrong. Concrete IO must not move into `core`, and package
layout should not be changed solely to make the score green.

With test imports included, the report currently finds three test-only layer
violations:

```text
go run ./apps/archreport --tests
Violations: 3
  . -> plugins/examples/echo
  adapters/channels/httpsse -> .
  orchestration/app -> plugins/native/text
```

Those may be intentional integration or black-box test shapes, but they are not
currently tracked as explicit exceptions.

The evaluator also misses important architecture signals outside internal module
import direction:

- direct host IO in `core`, `sdk`, and `orchestration`;
- plugin side effects that bypass `runtime/system.System`;
- runtime packages expanding host IO responsibilities without review;
- unknown top-level Go packages ignored by `layerOf`;
- same-layer coupling that is structurally different by package role.

## Goals

- Keep production layer violations as the hard architecture gate.
- Make the report severity legible when production violations are zero.
- Separate boundary correctness from coupling, side effects, and test-only
  dependencies.
- Make intentional exceptions visible through allowlists with reasons.
- Detect host side effects in packages where they are architecturally forbidden.
- Detect runtime side-effect expansion through explicit allowlists.
- Cover all plugin packages in side-effect checks, not just a handpicked subset.
- Report unknown in-module top-level packages so new roots cannot bypass the
  architecture model.
- Preserve existing JSON/report consumers during migration where practical.

## Non-goals

- Do not move concrete IO packages into `core`.
- Do not change the layer matrix to make the score pass.
- Do not ban all plugin `net/http` imports blindly. Request construction may be
  acceptable when execution still goes through `runtime/system.System`.
- Do not make test-only violations a hard gate until the current test imports
  are classified and intentional exceptions are documented.
- Do not make app or distribution assembly fan-out a problem; those layers are
  expected to compose many packages.

## Current Model

`internal/architecture` currently reports:

- packages known to one of the configured layers;
- internal module edges between known packages;
- layer violations according to `allowedImport`;
- layer fan-in/fan-out summaries;
- a single score derived from hard violations plus soft penalties.

`apps/archreport` exposes this with:

- `-format text|json|dot|mermaid`;
- `-tests` to include test imports;
- `-fail` to exit non-zero when violations exist.

`task arch:check` runs only:

```bash
go run ./apps/archreport -fail
```

That is the right hard production gate, but it does not expose test-only
violations or side-effect boundary drift.

## Proposed Evaluation Model

The report should keep one concise top-level summary, but the evaluator should
separate concerns into named components:

- **Boundary**: production layer direction. This is the hard gate.
- **Test boundary**: test-only layer direction. This is initially report-only
  with allowlists for intentional exceptions.
- **Coupling**: fan-out, fan-in, same-layer coupling, and runtime sibling edges.
  This is a review signal, not a release gate by itself.
- **Side effects**: host IO imports and high-risk calls in layers or packages
  where direct effects are forbidden or require explicit allowlist review.
- **Coverage**: unknown top-level package roots and packages skipped by the
  evaluator.

The existing `score` field can remain during transition, but it should become a
summary review signal derived from component scores. Hard violations should
remain obvious and dominant, while soft coupling penalties should be capped so a
zero-production-violation architecture cannot collapse to `0/100` solely from
expected composition packages.

Recommended component scoring:

```text
boundary_score      100 - min(100, production_violations * 25)
test_boundary_score 100 - min(100, unallowed_test_violations * 10)
coupling_score      100 - capped coupling budget penalties
side_effect_score   100 - capped side-effect diagnostic penalties
coverage_score      100 - unknown package / skipped package penalties
```

The top-level score should be the minimum of required components plus bounded
soft influence, or a weighted score that cannot drop below a floor when
`boundary_score == 100`. The exact formula should be simple and visible in the
report. The important invariant is:

```text
production violations > 0 -> hard failure
production violations == 0 -> score retains useful resolution
```

## Diagnostics

### Production Layer Violations

Production layer violations remain the primary failure:

- evaluated from non-test imports;
- rendered first in text output;
- returned in the existing `violations` JSON field;
- used by `-fail` and `task arch:check`.

### Test Layer Violations

Test imports should be evaluated separately from production imports:

- include `TestImports` and `XTestImports`;
- mark diagnostics as test-only;
- support explicit allowlist entries with package, imported package, and reason;
- render a separate "Test violations" section in text output.

The current three test-only violations should be classified before gating:

- facade tests importing `plugins/examples/echo`;
- HTTPSSE adapter tests importing the facade;
- orchestration app tests importing `plugins/native/text`.

### Host IO In Inner Layers

Production `core`, `sdk`, and `orchestration` packages should fail on direct
host IO imports unless explicitly allowed for architecture tooling itself:

```text
os
os/exec
syscall
net
net/http
net/url
database/sql
github.com/spf13/cobra
terminal/TUI packages
provider SDK packages
```

`path/filepath` should be reported in `core` as a review diagnostic rather than
an immediate hard failure, because logical resource paths should usually use
`path`, but existing usage may need a careful migration.

### Plugin Side Effects

The plugin side-effect check should walk all `plugins/**` Go packages rather
than a hardcoded subset.

Import bans alone are too crude for plugins because some packages may construct
HTTP requests while still executing through `runtime/system.System`. The
diagnostic should therefore detect high-risk symbols and calls, including:

```text
http.DefaultClient
http.Get
http.Post
net.Dial
net.DialTimeout
os.Getenv
os.LookupEnv
os.UserHomeDir
os.ReadFile
os.WriteFile
os.Stat
exec.Command
exec.CommandContext
```

Each intentional exception must be allowlisted with a package, symbol or import,
and reason. The default rule is that reusable plugins use
`runtime/system.System` for filesystem, process, network, browser, and
clarification effects.

### Runtime Side Effects

Runtime may implement side effects, but the surface area should be explicit.
The evaluator should maintain an allowlist of runtime packages permitted to
import host IO packages.

Initial expected allowlist examples:

```text
runtime/system
runtime/httptransport
runtime/sqlclient
runtime/secret
runtime/datasource/semantic
```

New runtime packages importing `os`, `os/exec`, `net`, `net/http`,
`database/sql`, or similar host-effect packages should produce diagnostics until
the package is intentionally added to the allowlist with a reason.

### Unknown Top-level Packages

`layerOf` currently ignores in-module packages outside:

```text
core sdk runtime orchestration adapters plugins apps cmd facade
```

The report should include unknown in-module packages and fail or warn unless
they are allowlisted. `internal/architecture` itself is an expected allowlisted
unknown because it implements the evaluator.

### Same-layer Coupling

Same-layer edges should be classified rather than only counted. Package roles
should be introduced as evaluator metadata:

```text
concept
aggregate
composition
adapter
testfixture
tooling
```

Examples:

- `core/resource` is an aggregate over inert resource contribution shapes.
- `orchestration/session` and `orchestration/app` are composition packages.
- `runtime/httptransport` is runtime infrastructure.
- ordinary core concepts should not import aggregate packages by accident.

Runtime sibling edges should be grouped by role. A dependency like
`runtime/system -> runtime/httptransport` is not the same risk as one runtime
feature package depending on another feature package.

## Data Model

Extend the report model without removing existing fields immediately:

```go
type Report struct {
    ModulePath string
    Summary    Summary
    Layers     []LayerSummary
    Packages   []PackageReport
    Edges      []Edge
    Violations []Violation

    Diagnostics []Diagnostic
    Scores      Scores
}

type Scores struct {
    Overall      int
    Boundary     int
    TestBoundary int
    Coupling     int
    SideEffect   int
    Coverage     int
}

type Diagnostic struct {
    Kind      string
    Severity  string
    Package   string
    Import    string
    Symbol    string
    TestOnly  bool
    Allowed   bool
    Reason    string
}
```

The existing `Summary.Score`, `Summary.ScorePenalties`, and `Violations` should
remain until callers and docs migrate to the new fields. Text output should show
component scores before detailed penalties.

## Configuration

Keep evaluator configuration in Go near `internal/architecture` initially.
Avoid introducing a separate config file until the rules stabilize.

The configuration should include:

- known layer roots;
- allowed layer matrix;
- test violation allowlist;
- host IO forbidden imports for inert layers;
- plugin high-risk symbol list and allowlist;
- runtime host IO package allowlist;
- unknown package allowlist;
- package role metadata for coupling classification.

Every allowlist entry must carry a reason string. Tests should fail if an
allowlist entry does not match anything, so stale exceptions are removed.

## CLI And Taskfile Plan

Keep the current commands working:

```bash
go run ./apps/archreport
go run ./apps/archreport -fail
go run ./apps/archreport -tests
```

Add explicit gate controls after diagnostics exist:

```bash
go run ./apps/archreport -fail-on boundary
go run ./apps/archreport -fail-on boundary,side-effects,unknown
go run ./apps/archreport -tests -fail-on test-boundary
```

`-fail` should remain an alias for `-fail-on boundary` during transition.

Taskfile additions:

```yaml
arch:check:
  desc: Fail on production architecture boundary violations.
  cmds:
    - go run ./apps/archreport -fail-on boundary

arch:report:tests:
  desc: Print architecture report including test imports.
  cmds:
    - go run ./apps/archreport -tests

arch:check:side-effects:
  desc: Fail on side-effect boundary violations.
  cmds:
    - go run ./apps/archreport -fail-on side-effects,unknown
```

Do not add the test-boundary gate to `task verify` until the current test-only
violations are classified and allowlisted or fixed.

## Rollout Plan

1. Add diagnostics and component scores while preserving the current hard
   production gate.
2. Add text and JSON rendering for component scores and diagnostic groups.
3. Add tests for production boundary, test boundary, side-effect diagnostics,
   runtime allowlist enforcement, plugin symbol scanning, and unknown package
   reporting.
4. Add Taskfile report targets for tests and side effects.
5. Update `docs/architecture.md` and `docs/verification.md` to describe the new
   interpretation: production boundary is the gate; component scores guide
   review.
6. Classify current test-only violations and plugin/runtime side-effect
   findings with fixes or allowlist reasons.
7. Only after diagnostics are stable, decide which checks join `task verify`.

## Acceptance Criteria

- `go run ./apps/archreport` with zero production violations no longer reports
  `0/100` solely because soft coupling penalties exceed 100.
- Production layer violations still fail `task arch:check`.
- `go run ./apps/archreport -tests` reports test-only violations in their own
  diagnostic group.
- Direct host IO imports in production `core`, `sdk`, and `orchestration` are
  detected.
- Runtime host IO imports are allowed only for explicitly listed runtime
  packages.
- Plugin high-risk side-effect calls are scanned across all plugin packages.
- Unknown in-module top-level Go packages are reported unless allowlisted.
- Allowlist entries require reasons and are tested for staleness.
- Documentation makes clear that the score is a review signal, not the hard
  release gate.
