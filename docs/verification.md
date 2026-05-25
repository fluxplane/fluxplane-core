# Verification

This document covers the local quality gate, Git hooks, and codegate review
commands. AGENTS.md only states the rule; this document covers the how.

## Quality Gate

```bash
task verify
```

`task verify` runs:

- `task fmt:check` (gofmt)
- `task mod:check` (`go mod tidy -diff`)
- `task whitespace:check` (`git diff --check`)
- `task vet` (`go vet ./...`)
- `task lint` (`golangci-lint run ./...`)
- `task test` (`go test -timeout=30s ./...`)
- `task quality:go` (codegate Go assessment with hard failure on architecture
  boundary, side-effect, and unknown-package violations)

The local gate intentionally uses conservative CPU defaults so it does not
consume every logical core on developer machines:

- `FLUXPLANE_VERIFY_GOMAXPROCS` defaults to `4`.
- `FLUXPLANE_VERIFY_GO_PACKAGES` defaults to `2` for Go package build/test
  parallelism.
- `FLUXPLANE_VERIFY_TEST_PARALLEL` defaults to `2` for tests that call
  `t.Parallel`.

Override these per run when needed:

```bash
FLUXPLANE_VERIFY_GOMAXPROCS=8 FLUXPLANE_VERIFY_GO_PACKAGES=4 task verify
```

Do not run the old repository root CI for rewrite work unless explicitly
asked.

## Git Hooks

New clones should enable the tracked Git hooks:

```bash
task hooks:install
```

The tracked pre-commit hook runs the staged security scan and staged
whitespace check. The tracked pre-push hook runs the full security scan,
`task verify`, and the cross-platform binary build.

## Codegate Go Review

The repo-owned codegate architecture policy lives in
`engine-architecture.rules.json`. Codegate is the quality gate and review tool
for architecture boundary health, side-effect drift, maintainability, safety,
coverage, coupling, and refactoring decisions. Production architecture boundary,
side-effect, and unknown-package violations are hard gate failures; scores and
findings guide review.

Three variants are available:

- `task quality:go` — the quality gate used by `task verify`. Runs the
  codegate assessment with `--fail-on boundary,effects,unknown` and a
  `summary` view, so it fails the build on production architecture-boundary,
  side-effect, or unknown-package violations and prints minimal evidence.
- `task quality:go:review` — non-failing review evidence with the `compact`
  view and up to 10 suggestions. Use this when iterating on a change and
  reviewing findings.
- `task quality:go:full` — non-failing review evidence with the `full` view
  and up to 20 suggestions. Use this for a deep dive when investigating
  architecture or maintainability findings in detail.

```bash
task quality:go         # hard gate, summary view (used by task verify)
task quality:go:review  # compact review evidence, up to 10 suggestions
task quality:go:full    # full review evidence, up to 20 suggestions
```

## Security Scan

```bash
task security:scan
```

Scans the working tree and Git history for secrets and banned keywords. The
pre-commit hook runs the staged variant; the pre-push hook runs the full
scan.
