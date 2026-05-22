
<p align="center">
  <img src="assets/logo.png" alt="Fluxplane Engine logo" width="350">
</p>

<p align="center">
  <strong>Build coding agents that remember what happened, use tools safely, and keep working across sessions.</strong>
</p>

<p align="center">
  <em>A Go runtime for durable agent systems and the generic <code>fluxplane</code> app CLI.</em>
</p>

<p align="center">
  <a href="https://pkg.go.dev/github.com/fluxplane/engine"><img src="https://pkg.go.dev/badge/github.com/fluxplane/engine.svg" alt="Go Reference"></a>
  <img src="https://img.shields.io/badge/Go-1.26%2B-00ADD8?logo=go" alt="Go 1.26+">
  <img src="https://img.shields.io/badge/status-pre--1.0-orange" alt="Status: pre-1.0">
</p>

---

## Why Fluxplane?

Most agent prototypes are easy to demo and hard to trust in real work. They lose
context, hide important decisions in chat history, call tools through ad hoc
code, and become difficult to resume after a crash or deploy.

Fluxplane Engine is built for the less glamorous parts that make agents
usable every day:

- **Durable sessions**: resume work from event-backed state instead of starting
  over.
- **Safe tools**: shell, filesystem, browser, network, process, approval, and
  secret boundaries go through a runtime safety envelope.
- **Composable capabilities**: add operations, commands, datasources, context
  providers, skills, and workflows through plugins.
- **Real product assembly**: package agents into apps and distributions instead
  of wiring everything in one-off scripts.
- **A working product path**: use the separate `coder` product immediately,
  then reuse the same runtime primitives in your own agents.

## Try `coder` in 30 seconds

Requires Go 1.26+ and a model credential such as `OPENAI_API_KEY`.

```bash
go install github.com/fluxplane/coder/cmd/coder@latest
go install github.com/fluxplane/engine/cmd/fluxplane@latest
export OPENAI_API_KEY=...
coder --input "Summarize this repository"
```

Open an interactive REPL:

```bash
coder
```

Run a goal until it is satisfied or the continuation cap is reached:

```bash
coder --goal "Find the failing tests, fix them, and summarize the patch"
```

`coder` defaults to OpenAI (`gpt-5.5`) and also supports Codex, OpenRouter,
Anthropic, Claude Code, and MiniMax:

```bash
coder --model codex/gpt-5.5 --input "Explain the current diff"
coder --model openrouter/anthropic/claude-sonnet-4.6
coder --provider claudecode --model claude-sonnet-4-6
```

See the `github.com/fluxplane/coder` repository for provider setup, goal mode,
debugging, usage accounting, and safety expectations.

See [repository split](docs/repository-split.md) for the current Engine/Coder
module boundaries, publish paths, and local development commands.

## What you can build

### Local coding agents

Ship a terminal coding assistant with project discovery, language-aware tools,
web search, file editing, tests, task execution, skills, and review workflows.
`coder` is the reference product for this path and lives in
`github.com/fluxplane/coder`.

### Durable agent apps

Define agents, sessions, commands, workflows, operations, context providers,
datasources, and resources as app specs. Run them in-process or over channel
adapters while preserving the same session contract.

### Plugin-powered capabilities

Bundle reusable capabilities as plugins: project inventory, Go and Markdown
language tooling, browser automation, code execution, task management,
datasources, integrations, and more.

### Safer automation surfaces

Put side effects behind typed operations and policy checks instead of letting
model output directly touch the host. The runtime keeps tool contracts,
execution boundaries, and event records explicit.

## Runtime building blocks

| Need | Fluxplane primitive |
|---|---|
| Durable conversation and run state | sessions, threads, events, projections |
| Callable tools | typed operations with schemas and safety policy |
| Long-running work | tasks, steps, artifacts, scheduler events |
| Repeatable processes | workflows and command targets |
| Project-specific context | resources, context providers, skills, datasources |
| Product packaging | apps, plugins, distributions, launch adapters |
| External surfaces | terminal, HTTP/SSE, direct channel, provider adapters |

## A tiny app shape

Agent apps are assembled from inert specs and plugin contributions. A project can
keep agent resources in `.agents/`, then let the runtime load and compose them:

```text
.agents/
├── agents/
├── commands/
├── workflows/
└── skills/
```

Example command resource:

```yaml
name: review
description: Review the current change.
target:
  prompt: |
    Review the current diff for correctness, tests, and maintainability.
```

That same resource model is used by the bundled `coder` app and by custom
Fluxplane applications.

## Start here

- **Use the coding agent**: `github.com/fluxplane/coder`
- **Explore the CLI**: [Fluxplane CLI](docs/fluxplane.md)
- **Configure providers and apps**: [Configuration](docs/configuration.md)
- **Understand the runtime**: [Architecture](docs/architecture.md)
- **Review the safety model**: [Security](docs/security.md)
- **See what changed**: [Changelog](CHANGELOG.md)

## Project status

Fluxplane Engine is a **pre-1.0 rewrite**. The core ideas are active, but
module APIs, resource shapes, and command surfaces may change quickly. During
this phase we prefer clean replacements over compatibility shims.

If you are evaluating the project, start with `coder`. If you are building on
the runtime APIs, expect breaking changes and follow the
[migration notes](docs/migration-from-agent-sdk.md).
