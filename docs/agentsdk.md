# agentsdk

`agentsdk` is the Fluxplane Agent Runtime product CLI. It creates and runs local
agent apps, serves daemon sessions, connects to remote sessions, manages
connector auth, inspects available models, and includes the `coder` agent as a
subcommand.

## Getting Started

### CLI Surface

```text
agentsdk [command]
```

Top-level commands:

- `init` creates a minimal local app manifest.
- `run` runs a local app distribution.
- `serve` runs an app daemon.
- `remote` connects to a running agentsdk daemon session.
- `build` builds a local app distribution.
- `models` lists available model providers and models.
- `connect` manages connector auth.
- `datasource` manages configured datasources.
- `discover` discovers configured resources.
- `coder` runs the bundled coding agent.

Use command help for the current flags:

```bash
agentsdk --help
agentsdk run --help
agentsdk serve --help
agentsdk remote --help
```

### Install

```bash
go install github.com/fluxplane/agentruntime/cmd/agentsdk@latest
```

From a local checkout:

```bash
go install ./cmd/agentsdk
```

### Create And Run A Local App

Create a minimal app manifest in the current directory:

```bash
agentsdk init .
```

See [Configuration](configuration.md) for the supported app manifest and
`.agents` resource formats.

Start an interactive local session:

```bash
agentsdk run .
```

Run one prompt and exit:

```bash
agentsdk run . --input "Hello"
```

Run a local app until a goal is satisfied or the continuation cap is reached:

```bash
agentsdk run . --goal "Test coverage has increased to 90%" --max-continuations 20
```

In an interactive local session, `/goal --max 20 "Test coverage has increased
to 90%"` submits the same built-in goal command.

Serve the app as a daemon:

```bash
agentsdk serve .
```

### Model Selection

`agentsdk run` and `agentsdk remote` accept provider/model options:

```bash
agentsdk run . --provider openai --model gpt-5.5
agentsdk run . --model openrouter/anthropic/claude-sonnet-4.6
agentsdk run . --model anthropic/claude-haiku-4-5-20251001
agentsdk run . --provider claudecode --model claude-sonnet-4-6
agentsdk run . --model minimax/MiniMax-M2.7
```

Control provider reasoning behavior for local runs:

```bash
agentsdk run . --thinking auto
agentsdk run . --thinking on --effort high
agentsdk run . --thinking off
```

`--thinking` accepts `auto`, `on`, or `off`. `--effort` accepts `low`,
`medium`, `high`, or `max`; unsupported provider/model combinations fail with a
clear error.

List available providers and models:

```bash
agentsdk models
agentsdk models -o json
agentsdk models . -o yaml
```

Provider credentials are read from the provider-specific environment or local
auth files:

- `OPENAI_API_KEY` for OpenAI.
- local Codex OAuth at `~/.codex/auth.json` or `CODEX_AUTH_PATH`.
- `OPENROUTER_API_KEY` for OpenRouter.
- `ANTHROPIC_API_KEY` for Anthropic.
- local Claude Code OAuth at `$CLAUDE_CONFIG_DIR/.credentials.json` or
  `~/.claude/.credentials.json` for `claudecode`.
- `MINIMAX_API_KEY` for MiniMax.

### Connectors And Datasources

Inspect and configure connector auth:

```bash
agentsdk connect slack --info
agentsdk connect --help
```

Manage configured datasource indexes:

```bash
agentsdk datasource index build .
agentsdk datasource index status .
agentsdk datasource index clear .
```

### Remote Sessions

Connect to a daemon through local app discovery, a URL, or a Unix socket:

```bash
agentsdk remote --local .
agentsdk remote --url http://127.0.0.1:8080 --input "Hello"
agentsdk remote --socket /path/to/agentsdk.sock --session default
```

### Docker Builds

Build a local app as a Docker distribution:

```bash
agentsdk build . --docker --tag my-agent:local
agentsdk build . --docker --dry-run
```

Use `--push` and `--platform` when publishing build output.

## Verification

For runtime development, use the repository-local quality gate:

```bash
task verify
```

Install the tracked Git hooks once per clone:

```bash
task hooks:install
```

The pre-commit hook checks staged secrets and whitespace. The pre-push hook runs
the full security scan, `task verify`, and the cross-platform binary build.

Build release-style local binaries into the ignored `bin/` directory:

```bash
task build
```

For publishing hygiene, run the repository security scan:

```bash
task security:scan
```
