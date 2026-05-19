# coder

`coder` is the bundled Fluxplane coding agent. It runs as a terminal REPL by
default, supports one-shot prompts, streams assistant output, and exposes the
runtime's coding, skill, and plan-execution plugins through the same safe
operation layer used by agentruntime apps.

## Getting Started

### CLI Surface

```text
coder [flags]
coder [command]
```

Top-level behavior and commands:

- `coder` opens an interactive REPL.
- `--input` sends one prompt and exits instead of opening the REPL.
- `--goal` runs a goal-driven task and exits after the goal is met or the
  continuation cap is reached.
- `--max-continuations` sets the `--goal` continuation cap; the default is `20`.
- `--provider` selects the model provider.
- `--model` selects the model name or `provider/model`.
- `--debug` prints run events as highlighted JSON markdown.
- `--usage` prints usage events after each response.
- `describe` describes bundled distribution metadata and resources.
- `describe agent <name-or-ref>` describes a bundled agent.
- `build --target docker-base` builds the reusable coder runtime base image.
- `config show` shows the resolved `.coder.yaml` configuration.
- `config edit` opens the resolved `.coder.yaml`, creating one when needed.
- `datasource index build|embed|status|clear` manages app datasource indexes.
- `remote` connects to a running local daemon session.
- `evaluator` evaluates AgentRuntime apps over the public channel protocol.
- `models` lists available model providers and models.
- `agent run <name>` runs an app-scoped agent explicitly.
- `op run <name>` runs an app-scoped operation explicitly.
- `workflow run <id>` runs an app-scoped workflow explicitly.
- `app init [path]` creates a minimal local app manifest.
- `app run [path]` runs a local agentruntime app manifest.
- `app serve [path]` serves a local app daemon.
- `app build --target docker-image [path]` builds a local app distribution.
- `app deploy --target docker-compose [path] --dry-run` renders local compose
  deployment resources.
- `app config show [path]` shows resolved local app configuration.
- `app config edit [path]` opens the resolved local app manifest.

Use command help for current flags:

```bash
coder --help
coder app --help
coder describe --help
coder models --help
```

### Install

```bash
go install github.com/fluxplane/agentruntime/cmd/coder@latest
```

From a local checkout:

```bash
task install
```

### First Run

Open the REPL:

```bash
coder
```

Run one prompt:

```bash
coder --input "Summarize this repository"
```

Run until a goal is satisfied or the cap is reached:

```bash
coder --goal "Test coverage has increased to 90%"
```

Inside the REPL, use `/goal --max <max-continuations> "<goal>"`:

```text
coder> /goal --max 20 "Test coverage has increased to 90%"
```

Use `/run` inside the coder REPL or with `--input` to target the current app
facet explicitly without importing that app's resources into coder:

```text
coder> /run
coder> /run app . --input "Hello"
coder> /run op upper --arg text=hello
coder> /run workflow release --json '{"version":"1.2.3"}'
coder> /run agent planner --input "Plan the release"
```

Use `/whoami` to inspect the local canonical user, trust, and authorization
subjects that policy checks see for the current turn.

Print usage accounting after the response:

```bash
coder --usage --input "Run the focused tests"
```

Inspect the bundled app and agent resources:

```bash
coder describe
coder describe agent coder
coder discover
coder models
coder config show
```

Coder discovers the nearest `.coder.yaml` or `.coder.yml` by walking upward
from the current directory. Pass `--config <path>` to use an explicit config
file. The first supported config slice accepts workspace roots and env files:

```yaml
version: 1
workspace:
  roots:
    - name: api
      path: ../api
  env_files:
    - .env
imports: {}
```

Project inventory reports agentruntime app manifests, `.coder.yaml`, and AI
configuration files as inert facets. Detecting those files does not import app
agents, skills, workflows, or operations into the running coder session.

Programmatic callers can construct the same configured product surface through
`coderapp.New(coderapp.Config)` and call `Run` to execute an app facet with
resolved `.coder.yaml` workspace defaults.

Run or inspect an agentruntime app from the current directory:

```bash
coder app run . --input "Hello"
coder app config show .
coder app config edit .
coder app init ./my-app
```

Run app-scoped resources without importing them into the coder session:

```bash
coder op run upper --app . --input hello
coder op run publish --app . --channel=release --dry_run
coder workflow run release --app . --json '{"version":"1.2.3"}'
coder agent run planner --app . --input "Plan the release"
```

At startup, `coder` includes resources declared by its app discovery policy:
`<cwd>/.agents`, `$HOME/.agents`, and `$HOME/.claude`. Skills from those roots
can activate automatically when user input matches their trigger phrases.

### Model Selection

`coder` defaults to OpenAI with `gpt-5.5`.

```bash
coder --provider openai --model gpt-5.5
coder --model codex/gpt-5.5
coder --model openrouter/anthropic/claude-sonnet-4.6
coder --model anthropic/claude-haiku-4-5-20251001
coder --provider claudecode --model claude-sonnet-4-6
coder --model minimax/MiniMax-M2.7
```

Control provider reasoning behavior:

```bash
coder --thinking auto
coder --thinking on --effort high
coder --thinking off
```

`--thinking` accepts `auto`, `on`, or `off`. `--effort` accepts `low`,
`medium`, `high`, or `max`; unsupported provider/model combinations fail with a
clear error.

Provider credentials are read from the provider-specific environment or local
auth files:

- `OPENAI_API_KEY` for OpenAI.
- local Codex OAuth at `~/.codex/auth.json` or `CODEX_AUTH_PATH`.
- `OPENROUTER_API_KEY` for OpenRouter.
- `ANTHROPIC_API_KEY` for Anthropic.
- local Claude Code OAuth at `$CLAUDE_CONFIG_DIR/.credentials.json` or
  `~/.claude/.credentials.json` for `claudecode`.
- `MINIMAX_API_KEY` for MiniMax.

### Debugging And Usage

Use `--debug` when developing or diagnosing a run:

```bash
coder --debug --input "Explain the current diff"
```

Use `--usage` to show grouped usage and cost-oriented accounting:

```bash
coder --usage --input "Plan the next implementation step"
```

### Safety Expectations

`coder` is intended for local development work. Side-effecting operations pass
through the runtime operation safety envelope and host system boundaries, but it
should still be treated as a local coding assistant, not as a sandbox for
untrusted repositories or untrusted prompts.

Run repository checks before publishing changes:

```bash
task verify
task security:scan
```
