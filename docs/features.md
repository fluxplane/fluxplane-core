# Features

Fluxplane core provides the runtime pieces for durable agent applications. This
repository is the Fluxplane engine: reusable Go packages plus the generic
`fluxplane` app CLI. Use it when you want an agent to keep state, call tools
through explicit safety boundaries, and be packaged as a reusable app instead of
a one-off script.

If you want a ready-to-use coding assistant, start with
[`coder`](https://github.com/fluxplane/coder). If you want to build an agent app
or product, use the generic `fluxplane` CLI and the core packages in this
repository.

## At a glance

| Feature | What it gives you | Where to start |
|---|---|---|
| Durable sessions | Event-backed conversations, run state, replay, and continuation after interruptions. | [Architecture](architecture.md), [Agent loop](agent-loop.md) |
| Safe tool execution | Typed operations for filesystem, shell, browser, network, processes, approvals, and secrets behind a safety envelope. | [Security](security.md), [Process tools](tools/process.md) |
| App configuration | `fluxplane.yaml` plus `.agents/` resources for agents, commands, workflows, skills, and sessions. | [Configuration](configuration.md) |
| Workflows and commands | Repeatable multi-step flows and slash-command targets that reuse the same runtime dispatch path as agent turns. | [Configuration](configuration.md#agentdir) |
| Datasources | Structured records and searchable project or integration context exposed through datasource plugins. | [Datasource embeddings](embeddings.md), [GitLab plugin](plugins/gitlab.md) |
| Language tooling | Go and Markdown-aware navigation, formatting, tests, diagnostics, and review helpers through language plugins. | [Configuration](configuration.md#plugins-and-auth) |
| Task execution | Long-running tasks with steps, artifacts, acceptance criteria, scheduler events, and review handoffs. | [Concepts](concepts.md#task) |
| Daemon deployments | HTTP/SSE, direct channel, Slack-style plugin channels, scheduled triggers, and startup triggers in `serve` mode. | [Fluxplane CLI](fluxplane.md#serve-as-a-daemon) |
| Distribution builds | Dockerfile, Docker image, Docker Compose, and Kubernetes manifest generation for app deployments. | [Fluxplane CLI](fluxplane.md#build-and-deploy) |
| Plugin system | Optional capability bundles for native tools, integrations, language support, datasources, and auth. | [Architecture](architecture.md#plugin-contribution-resolution) |

## What users can do today

### Run the separate coding agent

The `coder` CLI is no longer part of this repository. Install it from
[`github.com/fluxplane/coder`](https://github.com/fluxplane/coder) when you want
the ready-to-use coding assistant:

```bash
go install github.com/fluxplane/coder/cmd/coder@latest
export OPENAI_API_KEY=...
coder --input "Summarize this repository"
```

`coder` depends on Fluxplane core and contributes its own product defaults,
terminal UX, provider setup, goal mode, debugging, and safety expectations in
that repository. Fluxplane core does not publish `cmd/coder` or an in-tree
`apps/coder` package.

### Create an app

Use the generic CLI to create a minimal app manifest:

```bash
go install github.com/fluxplane/engine/cmd/fluxplane@latest
fluxplane init ./my-app
fluxplane run ./my-app --input "Hello"
```

A Fluxplane app can be configured with model providers, agents, commands,
workflows, context providers, datasources, plugins, daemon listeners, and build
targets.

### Package and serve an app

Run the same app as a daemon or generate deployment artifacts:

```bash
fluxplane serve ./my-app --verbose
fluxplane build ./my-app --target dockerfile,docker-compose --image my-app:local
fluxplane deploy ./my-app --target docker-compose --image my-app:local
```

`serve` mode supports channel clients and background automation, including
scheduled and startup triggers configured in the app manifest.

## Built-in capability areas

### Native tools

Fluxplane ships native plugins for common agent capabilities:

- filesystem reads, writes, globbing, and bounded file edits;
- managed processes and shell execution;
- browser automation through a controlled browser boundary;
- human clarification and notification operations;
- memory, goals, tasks, session history, project inventory, and skills;
- image generation, text utilities, and workspace context.

### Integrations

Integration plugins add datasource and operation support for external systems.
Current first-party areas include Git, GitLab, Jira, Confluence, Slack, Loki,
MySQL, Kubernetes, Docker, OpenAPI, AWS, web search, OpenAI-compatible model
providers, and related auth/status helpers.

Availability depends on the app configuration and credentials present in the
runtime environment.

### Language support

Language plugins expose project-aware development tools. Go support includes
package and symbol discovery, docs, references, callers/callees,
implementations, formatting, tests, vet, build checks, dependency operations,
and architecture review gates. Markdown support includes outlines, links, and
local link diagnostics.

## How the pieces fit

A typical product built on Fluxplane core has four layers of user-visible behavior:

1. **App manifest**: `fluxplane.yaml` selects runtime settings, agents, sessions,
   daemon channels, plugins, datasources, and deployment options.
2. **Resources**: `.agents/` contains reusable agents, commands, workflows, and
   skills that can travel with a project or app bundle.
3. **Plugins**: optional capability bundles contribute operations, context,
   commands, datasource providers, auth methods, and language tools.
4. **Channels**: terminal, direct, HTTP/SSE, or plugin-backed channels submit
   user inputs into the same durable session runtime.

This keeps product assembly separate from the stable runtime concepts: sessions,
events, operations, workflows, tasks, commands, resources, and datasources.

## Choosing an entry point

- **I want the coding assistant**: use the separate
  [`coder`](https://github.com/fluxplane/coder) repository.
- **I want to author an app**: read [Fluxplane CLI](fluxplane.md) and
  [Configuration](configuration.md).
- **I want to integrate a system**: start with [Plugins and auth](configuration.md#plugins-and-auth)
  and the existing [plugin docs](plugins/).
- **I want to understand safety**: read [Security](security.md) and
  [Process tools](tools/process.md).
- **I want to contribute to core**: read [Architecture](architecture.md),
  [Concepts](concepts.md), and the root [`AGENTS.md`](../AGENTS.md).
