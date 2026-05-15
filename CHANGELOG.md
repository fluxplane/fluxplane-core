# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- `coder` now loads startup resources declared by its app discovery policy,
  including `<cwd>/.agents`, `$HOME/.agents`, and `$HOME/.claude`; `coder
  discover` uses the shared resource discovery command renderer.
- Skills with matching trigger phrases are activated before model context
  rendering, so triggered skill bodies and references are visible in the same
  turn.
- Sub-agents spawned via `delegate` or `plan` now inherit the parent session's
  approval gate (e.g. `--yolo`) through the spawn chain, so child sessions do
  not re-prompt for approvals already granted by the parent.
- Added goal-driven local runs through the built-in `/goal` command plus
  `coder --goal` and `agentsdk run --goal`, with a configurable continuation
  cap.
- Added provider-neutral end-of-turn auto-compaction that checkpoints large
  transcripts after they exceed the configured model context threshold.
- Model tool-call results over 10 KiB are now replaced with `/tmp` result-file
  references plus size, digest, and replacement metadata.
- Added `/compact` and `/compact --dry-run` built-in session commands for
  checkpointing or previewing deterministic provider transcript compaction.
- Added opt-in runtime workspace roots so local launches can expose named
  directories such as `@tmp` while keeping filesystem operations confined by
  default.
- Local launches now persist thread/event history to SQLite by default.
- Added `--dev` for local run surfaces to expose a `session_history`
  datasource with searchable session threads, messages, operations, model calls,
  continuations, subagents, and usage.
- Added `--dev` for datasource index commands so local session history can be
  indexed semantically, with `session://` URLs for session-history corpus
  records.
- Added `docs/agent-loop.md` with an architecture summary of the agent
  execution and continuation loops.
- Added nested agent `turns` configuration for inner step budgets and
  stop-condition-driven outer continuation loops.
- Added `claudecode` as a Claude Code-compatible LLM provider using local
  Claude OAuth credentials and Claude Code request headers/preflight behavior.
- Non-blocking `task quality:metrics` reports coverage plus optional
  cyclomatic and cognitive complexity hotspots for refactoring guidance.

### Changed

- `--yolo` now auto-approves local command-risk gates, including commands that
  exceed the default risk threshold, while ACL, sandbox, secret, syntax, and
  plugin hard-deny checks remain enforced.
- Reflection-based command binding now supports `default=...` tag values, and
  `/goal` defaults its continuation cap to 10 when no max is provided.
- Agent loop config now uses `turns.max_steps` and
  `turns.continuation.max_continuations` instead of flat agent policy fields.
- Continuation prompt stop checks now require the typed
  `continuation_decision` tool; context policy controls whether raw operation
  effect details are included in evaluator prompts.
- `step_limit_exceeded` session errors now report the inner-loop
  `turns.max_steps` model-decision-call limit in their message and structured
  details, distinguishing the inner agent loop from outer continuation.

### Fixed

- `coder` now accepts common Claude-style user resource frontmatter and reports
  user resource load diagnostics, so `$HOME/.claude` skills are available for
  explicit REPL activation instead of failing with `skill_state_missing`.
- Appconfig manifests and agentdir frontmatter now validate raw YAML/JSON
  against generated JSON Schemas before decoding, and continuation caps now
  require an explicit stop condition.
- SDK agent builders now include fluent continuation stop-condition helpers, and
  `WithMaxContinuations` produces a valid capped continuation spec by default.
- Outbound provider, channel, and guarded system HTTP clients now share the
  runtime transport for extended compression handling and transient retry
  behavior.
- Shared HTTP retries no longer replay generic non-idempotent system-network
  requests, no longer compress `Retry-After` waits to the exponential backoff
  cap, and avoid retrying permanent transport errors.
- Removed OpenAI-specific request-local transcript compaction so compaction
  behavior is durable and session-owned across providers.
- Conversation continuity repair now records durable diagnostics and preserves
  provider transcript data when tool-call assembly fails.
- Read-only workspace roots can no longer be used as process working
  directories, closing a write bypass through process-backed operations.
- Browser CDP sessions no longer cancel immediately after `browser_open` when
  later operations reuse the session.
- Failed model turns now preserve pending transcript input, and replay uses
  concrete provider model identities so resolved model IDs do not drop context.
- OpenRouter/Responses replay now repairs orphan tool-result transcript items
  instead of sending provider-invalid tool messages without matching calls.
- OpenAI image generation no longer sends unsupported `response_format` for
  `gpt-image-1` and can consume URL image responses.

### Changed

- Local launches can set `AGENTRUNTIME_BROWSER_HEADLESS=false` to run browser
  automation with a visible Chrome window for smoke testing.
- Event registry assembly now lives in a dedicated orchestration package,
  reducing app composition fan-out and improving the architecture score.
- Resource catalog collection, executable app resource binding, agent spec
  filtering, and session environment wiring now live in focused orchestration
  packages, bringing the architecture score target to 80.
- Session control-plane helpers now live in `orchestration/sessioncontrol`,
  keeping stop-condition evaluation, built-in command policy/target wiring,
  resource aliases, and LLM-driver control helpers out of the main session
  loop and bringing the architecture score target to 90.
- Improved architecture score by moving pure event codec helpers into core and
  removing unused runtime pass-through fields from app composition.
- Architecture reports now include score penalty explanations so improvement
  targets are visible without reading the scoring code.

## [0.12.0] - 2026-05-14

### Added

- `agentsdk run` and distribution CLIs such as `coder` now support `--yolo`
  to approve all operation approval prompts for that local run.

### Changed

- The tracked pre-push hook now runs the security scan and `task verify` without
  also running the cross-platform binary build.

## [0.11.0] - 2026-05-14

### Added

- Git plugin now includes `git_tag` and `git_push` operations with typed safety
  intent for local tag writes and explicit remote pushes.

### Changed

- Pre-push security scans now skip the repository-local `.cache/` directory so
  hidden Go build cache files are not scanned.

## [0.10.0] - 2026-05-14

### Added

- Project branding in the README with a new Fluxplane logo SVG asset.
- Type-safe operation safety intents let concrete operations describe expected
  process, filesystem, and network effects from typed inputs before execution.
- Terminal approval prompts for local `agentsdk run` and `coder` operation
  approvals, with fail-closed behavior when no approval input is available.
- First-party `plugins/imageplugin` with `image_generate`, `image_understand`,
  and `image_providers` operations, plus an opt-in single `image` action tool
  projection.
- `coder` now enables the image plugin by default and exposes the single
  `image` action tool for generation, understanding, and provider inspection.
- `agentsdk run` and `coder` now accept `--thinking auto|on|off` and
  `--effort low|medium|high|max` to control provider reasoning behavior for
  local runs.

### Changed

- Agent worktree instructions now remind agents to update `CHANGELOG.md` for
  user-visible, documentation, removal, or release-affecting changes before
  committing.
- Tracked Git hooks can now be installed with `task hooks:install`; pre-commit
  checks staged secrets and whitespace, while pre-push runs the full security
  scan and `task verify`.
- `task build` now cross-compiles `agentsdk` and `coder` for Linux, macOS, and
  Windows on amd64 and arm64 into ignored `bin/` outputs; tracked pre-push hooks
  run this build after verification.
- Task-managed Go build and lint caches now live under ignored `.cache/` so
  cross-compilation does not fill `/tmp`.
- Tool projection and provider adapters now support dispatch-backed action
  tools without modeling tool sets as invocation targets.

### Fixed

- Reasoning flag resolution no longer forces an implicit `medium` effort for
  thinking-only requests, and explicit `--effort` values are validated and
  propagated in auto mode.
- Image plugin operation and action-tool schemas now derive from typed
  operation contracts instead of hand-written JSON Schema.
- Command-risk approvals now route through the runtime approval gate, and
  cmdrisk now classifies generic operation intents instead of plugin-specific
  operation names or raw input maps.
- Read-only Git status checks no longer request approval.

## [0.9.0] - 2026-05-14

### Added

- Dedicated `docs/agentsdk.md`, `docs/coder.md`, and `docs/configuration.md`
  onboarding and configuration guides.

### Changed

- README onboarding is now concise and links to focused guides for CLI usage,
  coder usage, and configuration.

### Removed

- Legacy `agentruntime.json` resource filesystem loader path.

## [0.8.0] - 2026-05-14

### Added

- `agentsdk init [path]` creates a minimal secure local app manifest with a
  default no-tool agent, default session, and Unix-socket direct channel.
- The repository root now includes a minimal local `agentsdk.app.yaml` manifest
  for direct dogfooding.
- The repository-local app manifest now enables a `local-docs` filesystem
  datasource with semantic search settings for markdown documentation.
- `task index` builds the repository-local datasource semantic index.
- `task security:scan` and local Git hook support scan for secrets and the
  repository's banned internal keyword before commits and pushes.

### Changed

- Uninitialized local `run` and `serve` paths now fail with guidance to run
  `agentsdk init` instead of silently starting an empty daemon or reporting a
  missing default session.
- `agentsdk run` and `agentsdk serve` now default to the current directory when
  no path argument is provided.
- Local distribution loading now exposes the generated `default` session when
  an app manifest declares `default_agent` without an explicit default session.
- Filesystem datasource corpus traversal now skips runtime and dependency
  directories such as `.git`, `.agents`, `.codex`, `node_modules`, and
  `vendor`.
- LLM agent construction now normalizes an empty driver kind to the LLM driver
  kind so session limit defaults apply consistently.

## [0.7.0] - 2026-05-14

### Added

- `agentsdk run [path]` for running local app distributions from app manifests
  or `.agents` directories, with REPL and `--input` one-shot modes.
- Distribution describe rendering for metadata and bundled resources, including
  detailed bundled agent descriptions.
- A standalone `cmd/coder` entrypoint built from the `apps/coder`
  distribution.
- Architecture documentation describing the rewrite layers, package
  responsibilities, common flows, and boundary checks.

### Changed

- Local distribution run and serve now share launch/runtime assembly through
  `apps/launch` and focused `adapters/distribution/*` packages.
- Remote session targeting and terminal execution moved out of `cmd/agentsdk`
  into `adapters/distribution/remote`.
- `cmd/agentsdk` is now a thin executable launcher; product assembly lives in
  `apps/agentsdk`, connector auth CLI behavior in `adapters/connectors/cli`,
  and run/serve command wiring in `apps/launch`.

## [0.6.0] - 2026-05-13

### Added

- `agentsdk remote` for opening local or URL-backed daemon sessions, including
  app-manifest listener discovery and Unix-socket HTTP/SSE connections.
- `plugins/planexec` with `delegate` and `plan` operations for supervised
  sub-agent tasks and DAG step execution.
- Generic `orchestration/subagent` supervisor with delegation-policy checks,
  capacity limits, cancellation, result lookup, and typed progress events.
- Terminal and Slack rendering hooks for sub-agent and plan progress events.
- Generic datasource relationship and batch-get operations, with exact Slack
  channel membership via `slack.channel` `members -> slack.user`.
- Web search datasource provider with `web.search_result` records, wired into
  `examples/slack-bot` without exposing the direct `web_request` tool.
- Change-only context provider materialization with provider/block fingerprints,
  placement-aware system/developer/user rendering, and committed render-state
  events.
- Built-in `/context` session command for previewing context that would be sent
  to the LLM, including `--fresh` and provider `--key` filtering.
- `agents.md` coding context provider that renders workspace `AGENTS.md` into
  system-scoped model context.
- Distribution specs and CLI adapters for packaging runnable agent bundles as
  reusable local command surfaces.

### Changed

- Clarified LLM loop budgets: `max_steps` now limits inner tool-loop model
  decisions, while `max_continuations` only caps stop-condition-driven outer
  follow-up turns.
- `agentsdk coder` and `agentsdk remote` now share the same terminal turn
  rendering path for streamed model events and final markdown fallback.
- `plan execute` now starts background plan execution; `plan wait` is the
  synchronous plan action for callers that need to block for completion.
- Slack datasource channel/user discovery now paginates before local filtering,
  and `examples/slack-bot` prefers exact membership relations over inferred
  message participation.
- `agentsdk coder` now opens the REPL by default; use `--input` for one-shot
  turns.
- Slack run rendering now uses a generic working status and streams markdown
  through chunk-based appends.
- `agentsdk coder` now lives in `apps/coder`, with `cmd/agentsdk` reduced to
  command wiring and terminal turn handling shared through `adapters/terminalui`.

### Fixed

- Terminal assistant streaming now writes deltas directly through the live
  markdown renderer without duplicating final output or leaking thinking text.
- Remote channel event delivery now uses bounded lossless buffering so streamed
  model output is not dropped during bursty runs.
- Context rendering now omits unchanged provider blocks after a successful
  render commit, reloads prior render records across sessions, and uses current
  tool-followup observations.
- Slack status updates no longer expose operation details and are cleared before
  content streaming starts.

## [0.5.0] - 2026-05-13

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

## [0.4.0] - 2026-05-13

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

## [0.3.0] - 2026-05-13

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

## [0.2.0] - 2026-05-13

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

## [0.1.0] - 2026-05-12

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

[Unreleased]: https://github.com/fluxplane/agentruntime/compare/v0.12.0...HEAD
[0.12.0]: https://github.com/fluxplane/agentruntime/compare/v0.11.0...v0.12.0
[0.11.0]: https://github.com/fluxplane/agentruntime/compare/v0.10.0...v0.11.0
[0.10.0]: https://github.com/fluxplane/agentruntime/compare/v0.9.0...v0.10.0
[0.9.0]: https://github.com/fluxplane/agentruntime/compare/v0.8.0...v0.9.0
[0.8.0]: https://github.com/fluxplane/agentruntime/compare/v0.7.0...v0.8.0
[0.7.0]: https://github.com/fluxplane/agentruntime/compare/v0.6.0...v0.7.0
[0.6.0]: https://github.com/fluxplane/agentruntime/compare/v0.5.0...v0.6.0
[0.5.0]: https://github.com/fluxplane/agentruntime/compare/v0.4.0...v0.5.0
[0.4.0]: https://github.com/fluxplane/agentruntime/compare/v0.3.0...v0.4.0
[0.3.0]: https://github.com/fluxplane/agentruntime/compare/v0.2.0...v0.3.0
[0.2.0]: https://github.com/fluxplane/agentruntime/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/fluxplane/agentruntime/releases/tag/v0.1.0
