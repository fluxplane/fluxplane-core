# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- Added `coder app run`, `coder app serve`, `coder app build`, and
  `coder app config show` as area/action app lifecycle commands backed by the
  existing local app launch, serve, and Docker build paths.
- Added `coder datasource index ...` using the shared datasource index command
  path so datasource management is available through coder.
- Added `coder remote` and `coder evaluator` so the remaining agentsdk
  terminal surfaces are available from the coder root command.
- Added `coder config edit` for `.coder.yaml` and `coder app config edit` for
  opening the resolved app manifest in the user's editor.
- Added `cmd/build/main.go` and switched repository install/build tasks to use
  the shared build entrypoint for `apps/agentsdk` and `apps/coder`.
- Added explicit `coder op run`, `coder workflow run`, and `coder agent run`
  area/action commands for running app-scoped resources without importing them
  into the coder session.
- Added `/run` handling in coder prompts so the REPL and `--input` can run the
  current app facet, app operations, workflows, and agents explicitly.
- Added `coderapp.App.Run` and wired `cmd/coder`'s `coder app run` path through
  the configured coder app wrapper so `.coder.yaml` workspace defaults apply.
- Added `apps/coderapp` with initial `.coder.yaml` discovery, explicit
  `--config` support, `coder config show`, and workspace/env-file defaults for
  coder sessions.
- Added project inventory facets for agentruntime app manifests, `.coder.yaml`
  configs, and generalized AI config files such as `AGENTS.md`, `CLAUDE.md`,
  `MEMORY.md`, and `.claude/agents/*.md`.
- Added `coder app init` using the shared local app initialization path.
- Added unified coder app surface and coder shell design notes covering the
  planned area/action command consolidation, `.coder.yaml` role, app facet
  behavior, agentsdk parity milestone, and programmatic `coderapp` API.
- Added a datasource mirror architecture design that separates live datasource
  access, local mirrored entity storage, secondary indexes, semantic retrieval,
  and future MySQL-backed mirror storage.
- Added initial `core/data` and `runtime/data` primitives for scoped records,
  relations, blobs, materialized views, store queries, and GitLab/Slack data
  view declarations.
- Added reflected `runtime/data` helpers for deriving data source entities and
  materialized view fields from struct tags, including nested view field paths.
- Added `runtime/datasource/mirror` as the structured datasource mirror
  boundary while preserving existing datasource index APIs.
- Added SQL-backed datasource mirror and `core/data` store adapters with SQLite
  tests and gated MySQL testcontainers coverage for scoped GitLab/Slack
  materialized query shapes.
- Added app-level `data.store` configuration and wired datasource indexing and
  tools to use `core/data.Store` as the primary materialized datasource mirror,
  with MySQL available through the SQL data store adapter.
- Added a synthetic `datasource` datasource in the generic datasource plugin so
  agents can list/search visible datasource sources, entities, and relations
  through the same tool surface used for provider data.
- Added native Slack bot-token auth, connector-free Slack channel credential
  loading, and native Slack datasource reads for users, channels, message
  search, thread messages, and channel/thread relations.
- Added native Jira Atlassian auth and datasource support, including stored
  OAuth2 credentials, token-based service account auth, and connector-free Jira
  issue/project access.
- Added native Confluence Atlassian auth and datasource support, including
  stored OAuth2 credentials, token-based service account auth, and
  connector-free Confluence page/space read access.
- Added typed runtime authorization policy grants for users, groups, services,
  systems, and agents, including wildcard resource/action matching, operation
  safety enforcement, projection filtering, local coder defaults, and
  canonical `<user>@localhost` local caller identity.
- Added typed operation access descriptors so built-in datasource, filesystem,
  process, network, connector, and task operations declare the concrete
  resource/action they need before execution authorization falls back to legacy
  operation-name and intent heuristics.
- Added typed access descriptor helper builders for common path, datasource,
  process, network, connector, task, and static resource mappings, reducing
  repetitive operation authorization boilerplate.
- Added `runtime/system` HTTP client and round-tripper helpers so official SDK
  clients can route requests through the runtime network boundary.
- Added generic typed plugin config decoding in `pluginhost.Context` for
  configurable plugin instances.
- Added initial `core/secret` and `runtime/secret` primitives for plugin auth
  method declarations, plugin-instance auth requests, env-backed secret
  resolution, scoped opaque secret handles, and `secret.use` authorization.
- Added `adapters/natseventstore` as a NATS JetStream-backed `event.Store`
  implementation with testcontainer integration coverage.
- Added system-boundary authorization checks for workspace, network, process,
  and environment access, plus `secret.*` policy resources for environment
  variable backed secrets, browser network authorization, canonical workspace
  path authorization, and terminal authz decision traces in debug mode.
- Added approval evidence events for requested, granted, and denied operation
  approvals, including authorization checks for `approval.grant` when a
  security policy is active.

- Added canonical inbound actor plumbing with `core/user` groups, identities,
  resolved actors, an orchestration identity resolver hook, and context-provider
  scope metadata for current user, identity, caller, and trust.
- Added `identity.current` context so local and Slack apps can show the current
  canonical user or an unresolved channel identity without exposing raw channel
  claims.
- Added app manifest `identity` configuration for canonical users, provider
  identities, groups, and trust-aware identity resolution.
- Added app manifest `identity.rules` so resolved or unresolved provider
  identities can receive app-defined groups and group trust without enumerating
  every canonical user.
- Added Slack API-backed identity resolution and `/whoami`, so Slack users can
  resolve to canonical email users while app identity config remains an
  additive group/trust overlay.
- Added verified user email aliases in app identity config, allowing canonical
  users to link provider accounts that expose different email addresses.
- Added named plugin instances via `plugins[].instance`, allowing multiple
  configured instances of the same plugin type such as `gitlab/company-a`.
- Added GitLab merge request review datasource entities and a native
  `gitlab_mr` action operation for MR creation, comments, review actions,
  merge/rebase, and pipeline retry/cancel.
- Added GitLab project, user, and group datasource relations so agents can
  traverse project members, project groups, user groups, and group projects.
- Added a generic `datasource_list` operation and GitLab project, user, and
  group listing support for agents that need to enumerate resources.
- Added GitLab user membership modeling, including field-indexed membership
  lookups, service-account visible group/project resolution, and explicit group
  hierarchy relations.
- Added lazy GitLab Code and CI datasource reads for branches, tags, commits,
  repository tree entries, repository files, jobs, and bounded job traces.
- Added reusable datasource field-index lookup helpers for indexed record search,
  exact lookup, pagination, and shared index readiness errors.
- Added multi-root local workspace startup via repeated `--workspace-root` flags
  for local distribution runs and `coder serve`, plus workspace root summary
  context and project inventory discovery under named `@root/...` prefixes.
- Added opt-in workspace-scoped env file loading via runtime workspace config
  and repeated `--env-file` flags for local run and serve commands.
- Added reusable runtime language-support descriptors for project-signal and
  toolchain-gated operation activation.

### Changed

- GitLab user membership indexing now streams group and project member pages
  into the field index instead of waiting for a full membership scan, and
  datasource index warmup logs when it is scheduled or skipped.
- GitLab project records now use the numeric GitLab project id as the stable
  datasource id, expose `project_id` metadata, and resolve path ids through
  project lookup before related Code/CI requests.
- HTTP/SSE remote submissions now use explicit downgrade-only trust delegation
  instead of accepting raw caller/trust authority fields, and listener-derived
  authority is applied before session execution.
- Model tool projection and operation execution are now scoped to each inbound
  turn's resolved caller/trust, so downgraded or public turns cannot use tools
  that were not projected for that authority.
- GitLab now uses native typed plugin instances backed by the official GitLab
  Go client instead of the connector plugin bridge.
- Slack message context now separates resolved sender identity from
  Slack-specific audience trust, so sender trust is provided by core identity
  context while channel sharing remains conservative; one-to-one DMs omit
  audience trust.
- `identity.current` now renders all identities known for the current actor,
  including plugin-enriched external accounts such as `gitlab/main:<username>`.
- Slack API identity resolution now records the resolved Slack account in the
  actor identity list so multi-identity context includes the entry identity.
- GitLab personal access tokens are resolved through plugin-instance auth
  requests. A configured `auth.token_env` is used directly; otherwise the
  resolver probes advertised env aliases including `GITLAB_PERSONAL_TOKEN`.
- GitLab identity enrichment now tries configured verified user email aliases
  against public GitLab user lookup before falling back to provider identity
  emails.
- The Slack bot example now demonstrates configuring a custom GitLab plugin
  instance base URL.
- Slack identity resolution now prefers connector user-token profile access for
  email lookup before falling back to bot-token `users.info`, so Slack users can
  resolve to canonical email users when the connected workspace grants profile
  email access.
- Datasource search now lets providers own `index.enabled` behavior; native
  GitLab indexed search reads the local datasource index instead of falling
  back to provider APIs, launch starts a background index warmup for indexed
  datasources, and index builds emit progress output.
- Datasource indexing now separates structured field records from semantic
  vector documents. GitLab indexes projects and users through structured fields,
  so project slug and user searches hit the local index without embedding
  GitLab content.
- GitLab datasources now expose `gitlab.group`, including structured field
  indexing and a `gitlab.user` to `gitlab.group` relationship.
- App manifests now group datasource configuration under `datasource`, with
  global `datasource.index` defaults and per-datasource `index.enabled` and
  `index.freshness` options.
- Datasource index warmup now runs datasource/entity jobs concurrently and
  skips fresh per-entity index runs based on persisted run metadata.
- Semantic datasource embedding is now queued during normal index builds and
  drained by a separate embed worker/CLI command, so field indexing is not
  blocked by embedding work.
- App manifests now declare plugin type with `plugins[].kind` instead of
  `plugins[].name`.
- GitLab datasources now initialize their API client lazily, and datasource
  provider matching no longer masks provider-specific errors with unrelated
  fallback provider errors.
- `agentsdk serve` now accepts and forwards daemon-wide `--provider` and
  `--model` overrides again.
- Coder language operation activation now consumes Go and Markdown support
  descriptors instead of hardcoding language-specific operation set selection.

### Fixed

- Fixed datasource index clearing so it also invalidates matching mirror/index
  run checkpoints instead of leaving stale completed runs behind.
- Fixed SQL-backed datasource mirror filtering to re-check full filter values
  after indexed prefiltering, avoiding false matches for long truncated filter
  values.
- Fixed MySQL initialization for SQL-backed datasource mirror and `core/data`
  stores so reopening an existing database tolerates already-created indexes.
- Fixed local app runs so repeated `--workspace-root` flags are forwarded into
  the launched app workspace, including through `coder app run`.
- Fixed `.coder.yaml` workspace defaults so structured root access, create, and
  env-file metadata are preserved when launching coder sessions, app runs, and
  coder serve.
- Fixed relative `.coder.yaml` workspace paths and env files so they resolve
  from the config file location instead of the process working directory.
- Fixed coder sessions with project or user skills loaded so the skill
  activation wrapper preserves per-turn projected tools such as `shell_exec`.

## [0.13.0] - 2026-05-17

### Added

- Added a first-class event-sourced task system: `core/task`, `runtime/task`,
  `plugins/taskplugin`, `/task`, typed task operations, artifact readback,
  task validation, task listing/search, and durable task store projection over
  the event store.
- Added local task scheduling and execution: ready-task claiming, step-DAG
  execution, role-based worker pools, manual `task_run`, scheduler status and
  pause/resume controls, worker leases, worker registration snapshots, lease
  renewal, stale-result rejection, retry/requeue behavior, and recovery from
  expired local workers.
- Added `/plan` as a task-planning command backed by `taskplugin`; approved
  plans now become ready tasks and are scheduled through
  `orchestration/taskexecutor` instead of the old plan execution plugin.
- Added task runtime feedback across terminal and Slack renderers, including
  task cards, step state, artifacts, scheduler diagnostics, finalization, and
  mirrored background events back to the originating or watching session.
- Added task finalization for multi-step tasks: after all declared steps are
  terminal, the scheduler can synthesize missing required task-level outputs
  from step evidence before completing the parent task.
- Added workspace modeling through `core/workspace`, workspace declarations,
  workspace-scoped project inventory, project signals, project task discovery,
  and workspace-scoped `project_task_run` for Taskfile, Makefile, and
  `package.json` scripts.
- Added Go and Markdown language tooling: project and Go summaries, Go package
  discovery, definition/symbol/reference/import/implementation/call-hierarchy
  operations, Markdown outlines/links/diagnostics, and split language contracts
  under `core/language/golang` and `core/language/markdown`.
- Added process-backed Go toolchain operations: `go_info`, `go_env`,
  `go_version`, `go_doc`, `go_list`, `go_test`, `go_fmt`, `go_vet`,
  `go_build`, and `go_install`, with structured results and bounded output.
- Added neutral `core/testrun` events plus compact terminal rendering for
  test-run feedback and actionable `go_test` failure summaries.
- Added `file_edit` for atomic existing-file edits with dry-run support and
  configurable diff output, while leaving create/copy/move/delete as separate
  filesystem operations.
- Added compact `git_diff` projections, native `web_search`, typed
  `code_execute` results, and compact code execution terminal rendering.
- Added appconfig support for top-level or standalone `commands`,
  `workflows`, and declaration-only `operations`; workflow commands now execute
  through orchestration and markdown prompt commands run through normal session
  input turns.
- Added local persistence, session-history datasource support, goal-driven
  runs, provider-neutral transcript compaction, `/compact`, startup resource
  loading from `.agents` and `.claude`, and skill-trigger activation before
  model context rendering.
- Added the evaluator app and `cmd/evaluator`, local Unix-socket coder serving,
  deterministic launch test hooks, and repository-local live-test helpers.
- Added architecture and planning docs for concepts, agent-loop behavior,
  constant self-evolvement, event-store follow-ups, task-system reliability,
  external distributed workers, OpenSpec, and BMad.

### Changed

- Replaced the legacy plan execution path with the task system and local task
  scheduler. Coder/local launch, event catalog, terminal rendering, and Slack
  progress now use task events and task worker profiles.
- Replaced the legacy sub-agent supervisor for command helper sessions with
  neutral `session_agent.*` events through `orchestration/sessionagent`.
  `/task`, `/plan`, and workflow agent steps now run through that session-agent
  boundary, while scheduled work remains on `taskexecutor`.
- Coder now exposes the new task, Go, Markdown, project, web, git diff, code
  execution, and editing operations to the main agent and delegated sessions.
- `task_list` now defaults to current-session tasks when called from a session,
  with `scope=all` for global history.
- One-shot terminal runs now wait, within a bounded deadline, for tasks started
  by the submitted turn; REPL turns briefly watch background tasks and then
  return the prompt.
- `task_run` now returns structured `started`, `running`,
  `waiting_for_capacity`, and `background` flags and records the caller as a
  watcher so approval turns receive later scheduler feedback.
- `coder` now uses `file_edit` for existing-file edits, `--yolo` approves
  local command-risk gates while retaining hard-deny checks, and launch/serve
  paths can inject deterministic model resolvers for tests.
- Agent loop configuration now uses nested `turns` settings for inner step
  budgets and outer continuation limits; `/goal` defaults to a capped
  continuation budget.
- Architecture composition was split across focused orchestration packages and
  architecture reports now explain score penalties.

### Fixed

- Fixed task scheduler correctness around concurrent task index writes,
  watcher metadata for missing task IDs, worker stream naming collisions,
  stale worker completions, stale diagnostics, step terminal metadata,
  interrupted execution resume, runtime shutdown cancellation, and publishing
  to multiple task event destinations.
- Fixed task validation and projection edge cases including cyclic step DAGs,
  revised step sets, duplicate task IDs, forced completion semantics,
  artifact scoping, artifact value readback, and oversized worker result
  references.
- Fixed `/plan` profile availability, prompt command session-agent resolution,
  workflow command output persistence, workflow agent step resolution, and
  multi-segment appconfig command resource naming.
- Fixed Go tooling edge cases: type-checked zero implementation matches are
  authoritative, virtual workspaces skip host `go/packages` loading, `go_test`
  parse tails are handled, regex alternation works, and `go_list`, `go_build`,
  and `go_install` use `-buildvcs=false`.
- Fixed direct-channel run event races that could panic with `send on closed
  channel`, and made `Wait` independent of undrained run-event forwarding.
- Fixed event-store and thread-store append conflict diagnostics and retries,
  including classified SQLite busy/locked/constraint errors and payload-free
  stream/sequence/attempt context.
- Fixed appconfig and agentdir schema validation, Claude-style user resource
  frontmatter loading, shared HTTP retry behavior, transcript replay/repair,
  browser CDP reuse, OpenRouter/Responses orphan tool results, and OpenAI
  image generation request compatibility.

### Removed

- Removed the old `plugins/planexec` runtime assembly path.
- Removed the legacy `orchestration/subagent` package and `subagent.*` event
  registration/rendering/classification.

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

[Unreleased]: https://github.com/fluxplane/agentruntime/compare/v0.13.0...HEAD
[0.13.0]: https://github.com/fluxplane/agentruntime/compare/v0.12.0...v0.13.0
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
