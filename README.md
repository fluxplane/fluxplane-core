
<p align="center">
  <img src="assets/logo.png" alt="Fluxplane core logo" width="350">
</p>

<p align="center">
  <strong>Build durable agent applications with explicit tools, events, plugins, and deployment surfaces.</strong>
</p>

<p align="center">
  <em>This repository is the Fluxplane engine: the core Go runtime behind Fluxplane apps and the generic <code>fluxplane</code> app CLI.</em>
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

Fluxplane core is built for the less glamorous parts that make agents
usable every day:

- **Durable sessions**: resume work from event-backed state instead of starting
  over.
- **Safe tools**: shell, filesystem, browser, network, process, approval, and
  secret boundaries go through a runtime safety envelope.
- **Composable capabilities**: add operations, commands, datasources, context
  providers, skills, and workflows through plugins.
- **Real product assembly**: package agents into apps and distributions instead
  of wiring everything in one-off scripts.
- **Product separation**: Fluxplane provides reusable runtime packages and the
  generic `fluxplane` CLI. The `coder` coding-agent product lives in the
  separate [`github.com/fluxplane/coder`](https://github.com/fluxplane/coder)
  repository.

## Try Fluxplane in 30 seconds

Requires Go 1.26+ and a model credential supported by your app configuration.

```bash
go install github.com/fluxplane/engine/cmd/fluxplane@latest
fluxplane init ./my-app
fluxplane run ./my-app --input "Hello from Fluxplane"
```

Use `fluxplane` when you are authoring, inspecting, serving, building, or
shipping a Fluxplane app:

```bash
fluxplane config show ./my-app
fluxplane discover ./my-app
fluxplane serve ./my-app --verbose
```

Looking for the ready-to-use coding assistant? Install and run `coder` from its
own repository instead:

```bash
go install github.com/fluxplane/coder/cmd/coder@latest
coder --input "Summarize this repository"
```

For local development, keep Fluxplane core and Coder as sibling checkouts when
you need both codebases, for example this repository plus `../coder`. Coder
depends on tagged `github.com/fluxplane/engine` releases and must not import
engine-internal packages or old in-tree coder paths.

### Product boundaries

This repository publishes the `github.com/fluxplane/engine` module, reusable
core/runtime packages, plugins, adapters, and the generic `fluxplane` app CLI. It
does not publish the `coder` CLI. Coder lives in
[`github.com/fluxplane/coder`](https://github.com/fluxplane/coder), should be
checked out as `../coder` for local cross-repository work, and depends on tagged
Fluxplane core releases rather than in-tree product packages.

## What you can build

### Runtime-backed agent products

Build product-specific agents on top of the reusable runtime instead of keeping
product code in this module. `coder` is the reference coding-agent product, but
it now lives in the separate `github.com/fluxplane/coder` repository.

### Durable agent apps

Define agents, sessions, commands, workflows, operations, context providers,
datasources, and resources as app specs. Run them in-process or over channel
adapters while preserving the same session contract.

### Background automation

Serve an app as a daemon with HTTP/SSE or plugin-backed channels, scheduled
triggers, startup triggers, workflow execution, and verbose event streaming for
run progress.

### Plugin-powered capabilities

Bundle reusable capabilities as plugins: project inventory, Go and Markdown
language tooling, browser automation, code execution, task management,
datasources, integrations, and more.

### Safer automation surfaces

Put side effects behind typed operations and policy checks instead of letting
model output directly touch the host. The runtime keeps tool contracts,
execution boundaries, and event records explicit.

See [Features](docs/features.md) for a user-facing tour of the runtime,
built-in capability areas, and entry points.

## Runtime building blocks

| Need | Fluxplane primitive |
|---|---|
| Durable conversation and run state | sessions, threads, events, projections |
| Callable tools | typed operations with schemas and safety policy |
| Long-running work | tasks, steps, artifacts, scheduler events |
| Repeatable processes | workflows and command targets |
| Project-specific context | resources, context providers, skills, datasources |
| Product packaging | apps, plugins, distributions, launch adapters |
| Background automation | daemon triggers, workflows, operation actions, channel submissions |
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

That same resource model can be used by custom Fluxplane applications and by
separate products such as `coder`, without embedding product-specific code in
this core repository.

## Start here

- **Build your own app**: [Features](docs/features.md), [Fluxplane CLI](docs/fluxplane.md), and [Configuration](docs/configuration.md)
- **Use the separate coding-agent product**: [`github.com/fluxplane/coder`](https://github.com/fluxplane/coder)
- **Understand the runtime**: [Architecture](docs/architecture.md) and [Concepts](docs/concepts.md)
- **Review the safety model**: [Security](docs/security.md)
- **Browse all documentation**: [docs/](docs/README.md)
- **See what changed**: [Changelog](CHANGELOG.md)

## Project status

Fluxplane core is a **pre-1.0 rewrite**. The core ideas are active, but
module APIs, resource shapes, and command surfaces may change quickly. During
this phase we prefer clean replacements over compatibility shims.

If you are evaluating Fluxplane, start with the generic `fluxplane` app flow in this
README. If you want the end-user coding assistant, use the separate `coder`
repository. If you are building on the runtime APIs, expect breaking changes and
follow the [migration notes](docs/migration-from-agent-sdk.md).
