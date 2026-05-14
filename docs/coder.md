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
- `--provider` selects the model provider.
- `--model` selects the model name or `provider/model`.
- `--debug` prints run events as highlighted JSON markdown.
- `--usage` prints usage events after each response.
- `describe` describes bundled distribution metadata and resources.
- `describe agent <name-or-ref>` describes a bundled agent.
- `models` lists available model providers and models.

Use command help for current flags:

```bash
coder --help
coder describe --help
coder models --help
```

### Install

```bash
go install github.com/fluxplane/agentruntime/cmd/coder@latest
```

From a local checkout:

```bash
go install ./cmd/coder
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

Print usage accounting after the response:

```bash
coder --usage --input "Run the focused tests"
```

Inspect the bundled app and agent resources:

```bash
coder describe
coder describe agent coder
coder models
```

### Model Selection

`coder` defaults to OpenAI with `gpt-5.5`.

```bash
coder --provider openai --model gpt-5.5
coder --model codex/gpt-5.5
coder --model openrouter/anthropic/claude-sonnet-4.6
coder --model anthropic/claude-haiku-4-5-20251001
coder --model minimax/MiniMax-M2.7
```

Provider credentials are read from the provider-specific environment or local
auth files:

- `OPENAI_API_KEY` for OpenAI.
- local Codex OAuth at `~/.codex/auth.json` or `CODEX_AUTH_PATH`.
- `OPENROUTER_API_KEY` for OpenRouter.
- `ANTHROPIC_API_KEY` for Anthropic.
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
