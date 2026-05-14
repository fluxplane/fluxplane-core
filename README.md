# Fluxplane Agent Runtime

Fluxplane Agent Runtime is a Go runtime and SDK for building agent systems with
durable sessions, resource-declared capabilities, plugin-contributed tools, and
transport-neutral channel clients.

## What It Includes

- Durable agent sessions, run handles, semantic events, and event-backed thread
  state.
- Resource-authored apps, agents, sessions, commands, operations, context, and
  datasource declarations.
- Plugin-contributed tools, commands, datasources, context providers, and
  product capabilities.
- Direct in-process and HTTP/SSE channel clients over the same session contract.
- Provider/model catalog integration for OpenAI, Codex, OpenRouter, Anthropic,
  MiniMax, and local app-defined providers.
- Safety-first operation runtime foundations for shell, filesystem, network,
  browser, process, approval, and secret boundaries.
- Architecture reports and import-direction checks for keeping runtime layers
  clean.
- ... and more.

It also ships with the coding agent `coder`, a ready-to-run terminal coding
assistant built on the same runtime and plugin system.

## Start Here

- [agentsdk CLI](docs/agentsdk.md)
- [coder coding agent](docs/coder.md)
- [Configuration](docs/configuration.md)
- [Architecture](docs/architecture.md)
- [Security model](docs/security.md)
- [Changelog](CHANGELOG.md)
