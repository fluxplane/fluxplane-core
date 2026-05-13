# Changelog

## Unreleased

### Added

- `agentsdk remote` for opening local or URL-backed daemon sessions, including
  app-manifest listener discovery and Unix-socket HTTP/SSE connections.
- `plugins/planexec` with `delegate` and `plan` operations for supervised
  sub-agent tasks and synchronous DAG step execution.
- Generic `orchestration/subagent` supervisor with delegation-policy checks,
  capacity limits, cancellation, result lookup, and typed progress events.
- Terminal and Slack rendering hooks for sub-agent and plan progress events.

### Changed

- Clarified LLM loop budgets: `max_steps` now limits inner tool-loop model
  decisions, while `max_continuations` only caps stop-condition-driven outer
  follow-up turns.
- `agentsdk coder` and `agentsdk remote` now share the same terminal turn
  rendering path for streamed model events and final markdown fallback.

### Fixed

- Terminal assistant streaming now writes deltas directly through the live
  markdown renderer without duplicating final output or leaking thinking text.
- Remote channel event delivery now uses bounded lossless buffering so streamed
  model output is not dropped during bursty runs.

## 0.5.0

### Added

- First-class datasource specs, registry, access policy, context catalog, and
  generic `datasource_search` / `datasource_get` operations.
- Connector-backed datasource providers for Slack users/channels/messages,
  Jira issues/projects, GitLab projects, and local file documents.
- Datasource entity capabilities, local reference detector specs, turn-scoped
  `datasource.detected` context, and generic record crosslinks without
  pre-turn datasource IO.
- Slack streaming task-card progress for assistant turns, with tool updates and
  final markdown streaming fallback.

### Changed

- App manifests can declare configured connector-backed datasource instances
  and agents can grant datasource access by instance name.
- `examples/slack-bot` now uses datasource instances for Slack, Jira, GitLab,
  and local docs, with entity-filtered search guidance.
- Datasource search rejects ambiguous broad searches when multiple entity types
  are available and reports partial errors alongside successful results.

### Fixed

- Slack progress logging no longer exposes raw thinking text.
- Datasource connector normalization now maps nested record fields explicitly
  for Slack, Jira, and GitLab metadata.

## 0.4.0

### Added

- `core/usage` session tracker for accumulating usage by stable subject and
  measurement keys.
- Grouped `agentsdk coder --usage` totals with human-readable token, network,
  and estimated cost lines after each prompt.
- Shared usage cost enrichment from model pricing for OpenAI-compatible
  providers.
- `agentsdk serve` daemon mode for app manifests with daemon listeners,
  direct channels, and long-running runtime channel startup.
- Native `agentsdk connect` command for offline connection status and
  provider-specific connector setup without embedding the connectors CLI tree.
- Slack channel plugin with Socket Mode support, Slack-derived caller/trust
  propagation, `channel_send`, and `slack_search` for public-channel message
  search.
- App config support for multi-document `kind:` manifests with daemon
  listeners/channels, app/session/agent documents, and the rewrite-native
  `examples/slack-bot` app.
- Connector-backed Slack credential loading and first-party OpenAI/Slack
  connector provider registration.
- Runtime channel host contracts and daemon lifecycle support for channel
  implementations.
- OpenRouter Responses provider support via `OPENROUTER_API_KEY` and
  `--model openrouter/<model-id>`.
- Generic Anthropic-compatible Messages adapter with Anthropic API and MiniMax
  provider wrappers for `--model anthropic/<model>` and `--model minimax/<model>`.

### Changed

- Bare operation tool projection now exposes the operation short name for
  synthetic channel tools.

### Fixed

- Daemon HTTP listeners enforce configured auth and reject unauthenticated TCP
  listeners.
- Slack channel turns submit Slack user identity and trust instead of falling
  back to the local daemon caller.
- Slack connector selection preserves the empty-instance fallback when a Slack
  channel omits a connector instance.

## 0.3.0

### Added

- Markdown-backed terminal streaming for `agentsdk coder` assistant text,
  reasoning summaries, and debug JSON event fences.
- Generic coder provider/model selection with `--provider` and
  `--model provider/model`.
- Codex Responses provider wiring using local Codex OAuth credentials from
  `~/.codex/auth.json` or `CODEX_AUTH_PATH`.
- `codewandler/modeldb` catalog bridge for provider/model capability and
  pricing metadata.
- Network byte usage records for OpenAI-compatible LLM provider HTTP uploads
  and downloads.

### Changed

- `apps/coder` now defaults to `gpt-5.5`.
- OpenAI-compatible provider config now defaults to automatic best-effort
  stored-response continuation and max prompt caching.
- LLM lifecycle events now include the concrete provider name.
- Removed the OpenAI-specific `--openai-store` CLI flag.

### Fixed

- Direct-channel run events are now forwarded while a run is executing instead
  of being drained only after completion, so terminal streaming is live.
- Codex/OpenAI reasoning and commentary-phase deltas now stream through the
  markdown terminal renderer, and final-answer detection ignores commentary
  phase message output.
- Assistant content streaming now writes each markdown delta once instead of
  replaying accumulated content while rendering.

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
