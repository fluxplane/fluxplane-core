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

```bash
task quality:go
task quality:go:review
task quality:go:full
```

## Security Scan

```bash
task security:scan
```

Scans the working tree and Git history for secrets and banned keywords. The
pre-commit hook runs the staged variant; the pre-push hook runs the full
scan.
