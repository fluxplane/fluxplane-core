# Changelog

## Unreleased

## 0.2.0

### Added

- Standard coding plugins for browser automation, managed background
  processes, terminal `clarify`, and terminal operation lifecycle rendering.
- `runtime/system` boundaries for neutral HTTP requests, managed processes,
  browser sessions, scratch directories, and human clarification.
- Architecture guard preventing standard plugins from importing direct host IO
  packages.

### Changed

- `cmd/agentsdk coder` now uses plugin-contributed operations instead of local
  shell/http implementations.
- `apps/coder` exposes the full coding operation set, including browser and
  process operations, with a continuation budget of 150.

### Fixed

- `cmdrisk` now falls back to declared operation risk for process and browser
  lifecycle operations that do not carry command or URL intent.
- `file_copy` and `file_move` now copy complete files instead of using bounded
  reads, and `file_patch` rejects oversized files before writing.

## 0.1.0

### Added

- Initial Fluxplane Agent Runtime rewrite release.
- Core model for agents, sessions, channels, commands, operations, resources,
  events, threads, workflows, LLM provider specs, and generic usage records.
- Runtime implementations for operation execution, event stores, thread stores,
  projections, LLM agents, and usage cost evaluation.
- Orchestration layer for app composition, configured sessions, channel-facing
  harness, session execution, tool projection, plugin hosting, daemon status,
  and client/run handles.
- Direct and HTTP/SSE channel clients with matching event contracts.
- OpenAI Responses adapter using `openai-go/v3`, including streaming model
  events, tool calls, operation continuation, and token usage events.
- Resource loading for local app manifests and AgentDir-style resources.
- First-party `apps/devclient` diagnostic app.
- First-party `apps/coder` declaration and `cmd/agentsdk coder` CLI with REPL,
  model selection, usage output, shell execution, and HTTP GET operations.
- Architecture report and verification task for import-direction checks.

### Changed

- No backward compatibility is provided with the old agentsdk. Concepts were
  renamed and split by layer: `core`, `runtime`, `orchestration`, `adapters`,
  `plugins`, and `apps`.
