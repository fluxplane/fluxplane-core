# Repository Split Readiness

Fluxplane now has two product/module identities staged in this checkout:

- **Engine**: `github.com/fluxplane/engine`, the reusable runtime module in the
  repository root.
- **Coder**: `github.com/fluxplane/coder`, the coder product module staged under
  `apps/coder`.

The current physical checkout may still live at an older local path while the
repository rename is coordinated. Release and install documentation should use
the module paths above, not the local checkout path.

## Published Packages

Engine publishes reusable packages and the generic app CLI:

```bash
go install github.com/fluxplane/engine/cmd/fluxplane@latest
```

Coder publishes the coding-agent product CLI:

```bash
go install github.com/fluxplane/coder/cmd/coder@latest
```

## Local Development

Run engine checks from the repository root:

```bash
go test ./...
go run ./apps/archreport
go run ./cmd/fluxplane --help
```

Run coder checks from the nested module:

```bash
cd apps/coder
go test ./...
CODER_ROOT=../.. go run ./cmd/coder --help
```

The root `Taskfile.yaml` runs both modules for the full gate:

```bash
task verify
```

## Extraction Contract

The nested coder module currently contains:

```go
replace github.com/fluxplane/engine => ../..
```

That replace is a staging-only local development contract. When `apps/coder` is
physically extracted to `github.com/fluxplane/coder`, release builds should
remove the replace and depend on a tagged `github.com/fluxplane/engine` version.
Developers who need source checkouts of both repositories can use a local
`go.work` file outside the published modules.

Coder may import public engine packages such as `core`, `runtime`,
`orchestration`, `adapters`, `plugins`, and `apps/launch`. It must not import
`github.com/fluxplane/engine/internal/...`, engine command packages, or the old
in-engine `github.com/fluxplane/engine/apps/coder` path. The coder module has a
package-level architecture test that enforces this boundary.
