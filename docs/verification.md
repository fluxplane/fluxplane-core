# Verification

This document covers the local quality gate, Git hooks, and architecture
reporting commands. AGENTS.md only states the rule; this document covers the
how.

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
- `task arch:check` (architecture import check via `apps/archreport -fail`)

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

## Architecture Reports

The architecture test (`internal/architecture`) is the hard boundary check.
The report is a review tool for coupling, fan-in/fan-out, and refactoring
decisions.

```bash
go run ./apps/archreport
go run ./apps/archreport -format json
go run ./apps/archreport -format dot
go run ./apps/archreport -format mermaid
task arch:render
```

`task arch:render` writes Mermaid and DOT graph sources into
`.agents/architecture/`. That directory is gitignored; regenerate it when
needed.

## Security Scan

```bash
task security:scan
```

Scans the working tree and Git history for secrets and banned keywords. The
pre-commit hook runs the staged variant; the pre-push hook runs the full
scan.
