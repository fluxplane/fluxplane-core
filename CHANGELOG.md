# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [0.21.2] - 2026-05-29

### Changed
- Parallelized plugin contribution resolution during app composition so `fluxplane run`, local REPL launches, and embedded distribution runtimes do not pay cumulative startup latency for independent plugin bundles.

## [0.21.1] - 2026-05-29

### Added
- `launch.ServeDistributionOptions.SessionToolProjection` so daemon callers
  (e.g. `slack-bot serve`) can opt into
  `session.ToolProjectionContextBlocksOnly` the same way
  `launch.LocalRuntimeConfig` and `launch.RuntimeOptions` already could.
  Without it, only callers that went through `Launch`/`NewLocalRuntime`
  directly could keep activated operation schemas out of the LLM tools
  array, which leaked every dex op's reflected JSON Schema to the model
  and tripped provider validators on malformed schemas.

## [0.21.0] - 2026-05-29

### Changed
- BREAKING: Native Slack plugin registers as `slack_channel` (was `slack`).
  This frees the canonical `slack` name for the dex marketplace plugin so
  consumers can load both the channel adapter (this plugin) and the
  richer dex slack op surface (slack.message.send, slack.reaction.add,
  slack.message.edit, …) side by side without a pluginhost
  duplicate-name collision. Plugin refs, bundle wiring, and explicit
  PluginRef literals targeting the native channel adapter must switch to
  `slack.Name` / `slack_channel`. The user-identity Provider value stays
  `slack` and is exposed as the new `slack.IdentityProvider` constant so
  admin allowlists and stored identities continue to match.

## [0.20.2] - 2026-05-29

### Added
- New `channel_post` operation on the native Slack plugin. Unlike
  `channel_send`, it does not require an active Slack channel turn —
  callers pass `channel_id` (and optional `thread_ts`) directly, so
  background agents and scheduled triggers can post unsolicited
  messages. `channel_send` stays the right tool inside active turns
  because it auto-glues to the current thread.

## [0.20.1] - 2026-05-29

### Changed
- `launch` Verbose mode now surfaces the `rule`, `action`, and `message`
  of `reaction.diagnostic` runtime events in the per-line log instead of
  emitting only the event name. Reaction action failures (e.g. workflow
  trigger drops, unsupported action kinds) now carry their actual cause
  into pod logs without having to attach a debugger.

## [0.20.0] - 2026-05-29

### Added
- Added `session.ToolProjectionMode` (`ToolProjectionDefault`,
  `ToolProjectionContextBlocksOnly`) with a matching
  `SessionToolProjection` field on `harness.Config`, `fluxplane.Config`,
  `launch.LocalRuntimeConfig`, and `launch.RuntimeOptions`. In
  `ContextBlocksOnly` mode the LLM request tools list stays stable across
  `surface_prepare` events — activated operation schemas reach the model
  only via the `surface.schema` developer-context provider and dispatch
  goes through `surface_call`. Preserves the Anthropic prompt cache
  breakpoint that sits on the last tool entry.
- Sharpened `surface_call`'s description with the developer-context block
  ID format (`surface/schema/<ref>`) and explicit guidance to call
  `surface_prepare`/`session_focus` first when the target operation isn't
  active.

### Changed
- Disabled the default registration for `gitlab`, `jira`, `confluence`,
  `kubernetes`, `loki`, `mysql`, `openai`, `image`, and `web` natives —
  they are no longer added by `availablePluginsWithAuth`,
  `AuthPluginRegistry`, or `datasourceIndexPlugins`. Embedders bring those
  capabilities via `github.com/fluxplane/fluxplane-dex/fluxplaneplugin`.
- Slack reduced to a pure channel-path plugin. The slack plugin no longer
  contributes a datasource, the `slack.users`/`slack.channels`/etc. entity
  catalog, or the `slack_thread_reply` operation. The only operations the
  slack plugin contributes are `channel_send` and `slack_report_progress`,
  both of which require an active Slack channel turn (`TargetFromContext`).
  Deleted `data.go`, `data_test.go`, `datasource.go`, `datasource_test.go`
  along with their test coverage in `plugin_test.go` (~1900 LOC net removed
  from the slack package).
- Removed the no-op `gitlabNamedPluginInstances` tool-projection probe and
  its helpers (`gitLabPluginRefs`, `gitlabConfigForRef`, `stringIn`) from
  `apps/launch/run.go`. With native gitlab disabled, the named-instance
  allow-list gate it produced was effectively unreachable.

### Removed
- Tests pinned to native-plugin shape:
  `TestDatasourceRegistryOpensNativeGitLabDatasource`,
  `TestLaunchSlackDatasourceUsesRuntimeAuthPath`,
  `TestDatasourceIndexRuntimeSlackDatasourceUsesAuthPath`,
  `TestBundlesWithStaticPluginContributionsUsesNativeSlackAndDatasourcePlugin`,
  and their helper functions (`saveSlackBotToken`, `slackDatasourceBundle`,
  `assertSlackDatasourceLoadedToken`, `runtimeHasContextProvider`).

## [0.19.0] - 2026-05-29

### Added
- Added a `clock` native plugin contributing a `time` context provider that
  injects the current UTC time (optionally also a configured local time zone)
  and the process uptime on each turn. The provider caches inside `Build()`
  for 60 seconds so the value refreshes at most once per minute.

## [0.18.1] - 2026-05-28

### Added
- Added a GitHub Actions workflow that builds and publishes the
  `ghcr.io/fluxplane/fluxplane-go` base image for linux/amd64 and linux/arm64
  on `main` pushes and version tags.

### Notes
- Shipped the `appconfig.NewManifest` builder source previewed in the 0.18.0
  release notes.

## [0.18.0] - 2026-05-28

### Added
- Added real Go examples for a minimal LLM-backed agent loop and a typed Slack
  bot-style app configuration.
- Added a typed plugin config helper for producing manifest-compatible plugin
  config maps from Go structs.
- Added `appconfig.NewManifest` for building app manifest contribution bundles
  from Go while reusing manifest decode semantics.

### Changed
- Made `task build` only build for the host platform by default; cross-platform
  binaries remain available via the `--target GOOS/GOARCH` flag on
  `cmd/build`.
- Renamed the local Docker base-image build surface to Fluxplane terminology and
  removed stale legacy manifest filename compatibility.
- Classified Go examples under the app layer in the architecture gate.
- Made the tracked pre-push hook run full security and verification checks with
  gentler CPU, test, and IO scheduling defaults.
- Tightened gitleaks allowlists for local IDE metadata and Claude Code beta
  header test fixtures so pre-push scans keep catching real leaks.
- Applied distribution default session and conversation values to direct serve
  HTTP/SSE opens and resumes that omit them while preserving verbose event
  logging.
- Moved the default datasource index JSON store out of app roots and into
  Fluxplane-owned local state.
- Deduplicated project inventory scans for workspace roots that resolve to the
  same physical directory.
- Omitted configured scratch workspace roots from project inventory scans.
- Populated normalized `go_test` run timestamps from `go test -json` events.
- Made `git_diff` failures outside Git repositories report concise errors
  instead of returning the full Git usage text.
- Honored the SSE `Last-Event-ID` header when resuming session event streams.
- Rendered `file_edit` replacement diffs with deletions before insertions.
- Avoided duplicate post-edit check notices in model-facing operation text.
- Made post-edit check notices describe fix-mode execution without implying
  files changed when a formatter made no edits.
- Removed the duplicate top-level `go_test` `test_run_event` payload copy.
- Kept terminal test-run rendering wired to the canonical nested `go_test`
  result event.
- Removed duplicate top-level `go_test` package and diagnostic payload copies.
- Rendered direct operation outbound messages as text instead of embedding the
  full structured operation result.
- Prevented direct channel resume and SSE event subscriptions from creating
  missing session threads while still allowing durable thread resume after a
  serve restart.
- Included durable direct-channel threads in session listings after serve
  restart and preserved canonical distribution default session identities in
  those summaries.

## [0.17.0] - 2026-05-26

### Added
- Added shared product integration helpers for contribution bundles, workspace
  launch config merging, plugin datasource catalog derivation, resource fragment
  decoding, and the native interruptible sleep plugin.
- Added shared session-run orchestration for fresh-context helper sessions and
  a native `/loop` command plugin for repeating prompts sequentially.
- Added named build/deploy targets, build artifact indexes, target listing
  commands, `--build-policy`, and build target kinds for Docker, Docker
  Compose, Kubernetes manifests, Helm charts, embedded binaries, generated
  capability docs, and reusable `runtime-stack` dependencies.
- Added `fluxplane config schema`, context-aware manifest completions,
  datasource config schemas, typed plugin config schemas, and schema-backed
  validation for raw manifest documents.
- Added agent `uses` activation syntax, app defaults/profiles, profile-scoped
  runtime documents, `--profile` selection, and agent-level trigger shorthand
  for scheduled/startup prompts.
- Added Slack bot-mode indexing policy, canonical Slack permalinks in indexed
  records, and the native usage datasource for local dev launches.
- Added OpenAI Responses WebSocket streaming with Codex defaults, warmup,
  active-session continuation, Codex live tests, and `cmd/claude-middleman` for
  comparing Claude Code subscription requests.

### Changed
- Renamed the published Go module and repository references to
  `github.com/fluxplane/fluxplane-core`.
- Raised the default agentic coding max output token budget to 32768.
- Replaced list-style app manifest plugin declarations with map-style plugin
  instances; `plugins.<name>` is now the instance name, `kind` defaults to that
  name, and `enabled: false` omits the plugin from runtime refs.
- Reworked `fluxplane build` and `fluxplane deploy` around declared targets and
  build artifacts, moving target-specific image, runtime, model, platform, and
  Helm/Kubernetes settings out of command-line flags.
- Made Docker Compose the default Docker Compose deploy backend, added health
  waits, reused Fluxplane-managed Docker networks, refreshed
  `fluxplane/fluxplane-base:local` automatically, and kept local base-image
  refreshes local even when app images are pushed.
- Standardized generated runtime backend environment names, MySQL credentials,
  database names, NATS subjects, Docker Compose runtime env files, and the
  namespace-shared `fluxplane-stack` runtime dependency name.
- Changed Kubernetes manifest and Helm artifacts to reference external Secrets
  for env files and runtime backend DSNs, use `.Values.namespace`, and omit
  generated `Namespace` resources.
- Changed generated Docker Compose artifacts to use environment placeholders,
  root workspace `env_files`, and two-space YAML indentation.
- Inferred `default_agent` for single-agent manifests and moved appconfig enum,
  default, and duration validation onto typed Go fields.
- Increased the OpenAI-compatible Responses stream idle watchdog default to
  five minutes, preserved nested provider error details, and made prompt cache
  keys prefer runtime `ConversationKey` values.
- Hardened Codex/OpenAI Responses transport with strict Codex protocol shaping,
  Codex identity metadata, full request replay when continuation cannot be
  proven, continuous WebSocket control-frame reads, per-message deflate,
  stale-socket reconnects, and separate Responses item ids for tool results.
- Updated Claude Code provider headers, preflight identity, and model aliases
  so generic Claude family aliases route through the subscription provider
  while explicit `anthropic/<family>` aliases keep Anthropic API-key behavior.
- Made the distribution REPL collect open-quote slash-command continuation
  lines so pasted multi-line slash commands submit as one command.

### Removed
- Removed dashed workflow manifest field aliases such as `depends-on`,
  `error-policy`, and `idempotency-key`; use snake_case fields instead.

### Fixed
- Fixed host process stdout/stderr capture so short-lived commands cannot exit
  before their output is drained, avoiding flaky shell output events and empty
  git command results.
- Fixed streamed tool-call/result continuity across OpenAI-compatible, Codex,
  Claude, and OpenRouter projections, including provider-prefixed model ids,
  reused call ids, committed tool-result follow-ups, and compaction of paired
  tool-call/result groups.
- Fixed task finalizer evidence, artifact text, datasource detection, mirror
  storage, verbose logs, session-control text, Slack observer text, and other
  truncation paths to preserve valid UTF-8 boundaries.
- Fixed LLM usage accounting so Anthropic cache creation is reported as
  cache-write input tokens and OpenAI cached input is excluded from standard
  input tokens before usage events and cost enrichment.
- Fixed lower-level hardening issues in OpenRouter and Anthropic non-streaming
  response body limits, OAuth2 token response limits, nil outbound message
  content, YAML frontmatter list coercion, SQL row error checks, trigger and
  subscriber watcher cleanup, and atomic secret file writes.
- Fixed agentdir loading so recoverable resource errors do not abort the whole
  load and malformed optional list items are ignored safely.
- Fixed semantic datasource result ordering, memory pagination, compacted-item
  counts, blocked task dependency projection, and deterministic continuity
  error reporting.
- Fixed Docker and deploy edge cases including custom Dockerfile path
  resolution, Docker login auth parsing, app-level Kubernetes teardown of
  shared runtime resources, Docker Compose undeploy runtime env merging, and
  nil Kubernetes deployment replica counts.
- Fixed secret and operation runtime cleanup by propagating secret resolver
  contexts, cleaning scratch directories after replacement failures, preserving
  backslashes for unknown env-value escapes, and returning clear missing
  instance-selector errors.
- Rendered `/loop` session-run lifecycle events in the terminal REPL and made
  loop summaries use markdown lists so status counts and target sessions stay
  readable.

### Documentation
- Documented additional `fluxplane` CLI commands and clarified the Go quality
  task variants used for hard-gate and review workflows.
- Reworked the root README around Fluxplane core as the reusable runtime and
  generic `fluxplane` CLI, with `coder` documented as a separate sibling product
  rather than an integrated app.
- Added `docs/features.md`, a user-facing feature tour that maps Fluxplane core
  capabilities to entry points for coding agents, app authors, integrations,
  language tooling, daemon automation, and plugin-based extensions.
- Reworked `docs/configuration.md`, `docs/embeddings.md`, and
  `docs/evaluation.md` so generic Fluxplane docs use `fluxplane` commands and
  runtime concepts instead of product-specific `coder` commands where the
  behavior belongs to core.
- Removed `docs/repository-split.md` and moved the remaining product-boundary
  guidance into the README and architecture documentation.

## [0.16.0] - 2026-05-23

### Added
- Added daemon scheduled and startup triggers, `fluxplane serve --verbose`, and
  structured trigger execution through workflow, operation, and command actions.
- Added workflow `input_map` and `when` condition support so scheduled workflows
  can consume prior step outputs and skip notification steps when appropriate.
- Added native `notify_send`/`notify` desktop notification operations with
  preset tones and local text-to-speech.
- Added a native usage datasource exposing persisted `usage.recorded` events.
- Added durable thread goals, the native goal plugin, isolated goal-reviewer
  verification sessions, activation sets, `/surface`, `/activate`, and
  model-facing surface inspection/preparation/call tools.
- Added GitLab merge request review context, GitLab datasource and operation
  parity for review, pipeline, snippet, repository, CI, discussion, approval,
  blame, compare, and job-trace workflows.
- Added `fluxplane op run --allow-private-network` for direct operation smoke
  tests against private or VPN-hosted integrations.

### Changed
- Replaced the architecture score gate with codegate rules, hard failure
  categories, and review-oriented Go assessment operations.
- Centralized local launch plugin auth context construction across run, serve,
  datasource indexing, and direct operation execution.
- Made background shell/process starts detach from the initiating operation
  context and let `process_wait` control wait time without killing the process.
- Hardened task scheduling with stale worker recovery, stricter running-task
  status transitions, web-search access for task agents, and better task phrase
  detection.
- Refreshed Go dependencies and task verification guidance for the repository
  after the product split.

### Fixed
- Fixed CLI REPL interrupt handling so Ctrl+C cancels only the active turn.
- Fixed direct-channel event draining so local REPL sessions cannot hang after a
  turn result is available.
- Fixed Kubernetes and Loki datasource connectivity for local runs, including
  request-scoped Kubernetes context/namespace filters and managed Loki
  port-forwards.
- Fixed Jira Markdown conversion for native issue and comment operations.
- Fixed daemon trigger activity tracking, notification environment forwarding,
  bounded notification speech text, embedded Piper TTS use, and zero-value
  session event forwarding.
- Fixed GitLab datasource smoke paths for MR refs, blob search, pathless diff
  lines, and repository-file colon ids.

### Documentation
- Added `docs/README.md` as the documentation index, expanded `docs/fluxplane.md`,
  tightened the README quickstart, and marked migration/design notes as such.
- Clarified `docs/evaluation.md` for developers smoke-testing a running app over
  the HTTP/SSE channel.

## [0.15.0] - 2026-05-22

### Added
- Added the generic `fluxplane` app-manifest CLI, renamed authored manifests to
  `fluxplane.yaml`, and moved generic app lifecycle commands out of `coder app`.
- Added GitLab write operations, GitLab membership mirroring, GitLab plugin
  default enablement for the embedded coder app, and auth-evidence-gated
  integration activation for GitLab, Jira, Confluence, and Slack.
- Added reusable native plugin auth commands/status, non-secret auth method
  metadata, Atlassian API-token support, and scoped `--allow-plugin-auth-env`
  handling for runtime and datasource indexing.
- Added observations and reactions, evidence assertion vocabulary, environment
  observers, signal derivation, reaction planning/execution, `/env explain`, and
  reaction-activated operations, datasources, skills, context providers,
  workflows, and commands.
- Added endpoint discovery, Kubernetes port-forwarding, Loki, OpenAPI, MySQL,
  Kubernetes cluster, memory, datasource mirror, semantic datasource context,
  shell/process lifecycle, post-edit checks, glob scan limits, and `file_delete`
  projection support.
- Added Docker/Kubernetes app deploy and undeploy support, Kubernetes app RBAC,
  generated runtime backend resources, and Docker build/deploy target plumbing.
- Added `scripts/coder-self-improve.sh`, the `go-refactor` developer CLI,
  embedded coder resources, coder shell UX improvements, and the coder app
  configuration skill.

### Changed
- Renamed the reusable module toward the engine/Fluxplane split and extracted
  the coder product into its own module/repository boundary.
- Split and reorganized first-party plugin and adapter packages into domain
  directories, and refactored deploy internals around Docker, Docker Compose,
  and Kubernetes boundaries.
- Replaced connector-based auth/data paths with native plugin auth and runtime
  system boundaries, including private-network opt-ins and resolver-aware plugin
  assembly.
- Reduced coder's default always-on tool surface by moving language, Docker,
  endpoint, browser, image, and memory tools behind evidence/reaction
  activation.
- Reworked coder shell rendering, prompt editing, completion, input history,
  stream repainting, mode hints, and live activity display.
- Moved the `go-refactor` developer CLI under `apps/go-refactor` so reusable Go
  refactor logic stays in the language plugin tree.

### Removed
- Removed the legacy external connectors runtime, connector manifest fields,
  connector authorization resources, and `--connectors-path`; native plugin auth
  now uses `--auth-path`.
- Removed the default low-risk model tool projection cap in favor of explicit
  `--allow-max-tool-risk` opt-in.
- Removed the stale provider transcript repair path in favor of fail-fast
  conversation continuity validation.

### Fixed
- Fixed conversation continuity, assistant tool-call/result persistence, and
  provider transcript handling for failed or interrupted operation turns.
- Fixed raw slash-command submissions, prompt-target slash commands, task worker
  registration freshness, generated directory glob scans, shell discovery
  through authorized systems, and `file_delete` tool projection.
- Fixed Slack conversation resumption, datasource mirror batching/indexing,
  canonical Jira/Confluence links, GitLab nested MR URL parsing, Confluence v1
  bearer-token reads, and Atlassian site URL discovery.
- Fixed Docker/Kubernetes deploy handling, generated Docker build cleanup,
  Docker build temp roots, namespace registry deploys, host env substitution,
  Kubernetes undeploy safety, and runtime-boundary routing for Kubernetes and
  Atlassian access.
- Fixed coder shell lifecycle, input filtering, prompt editing, mode rendering,
  terminal mouse/modifier handling, and default agentic input mode.

### Documentation
- Documented repository extraction readiness, product split planning, coder app
  configuration, Kubernetes port-forwarding, and removal of local Go cache
  overrides.

## [0.14.1] - 2026-05-19

### Fixed
- Preserved `SSH_AUTH_SOCK` in the bounded host process environment so git
  operations, including `git_push`, can authenticate through the user's SSH
  agent instead of hanging on passkey prompts.

## 0.14.0 - 2026-05-19

### Added
- Added Go dependency toolchain operations `go_get` and `go_mod_tidy`, both
  defaulting to dry-run previews before updating module files.
- Added a Kubernetes datasource plugin and enabled coder to list, search, and
  retrieve live Kubernetes namespaces, pods, services, and containers through
  the default datasource tool surface.
- Added NATS JetStream-backed launch event store selection for app runtime
  sessions through `runtime.events.store` configuration.
- Added first-class channel operation submissions and wired `coder shell`
  command execution through direct `shell_exec` operation calls.
- Added OpenRouter defaults for generated Docker Compose app deployments,
  including `OPENROUTER_API_KEY` process-env injection and medium reasoning
  effort in generated app serve commands.
- Added `models.default` and `models.available` appconfig model registry
  support so agents and deployment config can refer to provider-agnostic model
  aliases with centrally declared provider/model params.
- Added generated Docker healthchecks for app images and condition-based Docker
  Compose startup ordering for MySQL and NATS JetStream deployments.
- Added coder-first distribution build targets: `coder build --target
  docker-base`, `coder app build --target docker-image`, and `coder app
  deploy --target docker-compose --dry-run`.
- Added app runtime event store configuration and backend-aware Docker Compose
  generation for MySQL data stores and NATS JetStream event stores.
- Added `coder app run`, `coder app serve`, `coder app build`, and
  `coder app config show` as area/action app lifecycle commands backed by the
  existing local app launch, serve, and Docker build paths.
- Added `coder datasource index ...` using the shared datasource index command
  path so datasource management is available through coder.
- Added `coder remote` and `coder evaluator` so the remaining app-management
  terminal surfaces are available from the coder root command.
- Added `coder config edit` for `.coder.yaml` and `coder app config edit` for
  opening the resolved app manifest in the user's editor.
- Added `cmd/build/main.go` and switched repository install/build tasks to use
  the shared build entrypoint for `apps/coder`.
- Added explicit `coder op run`, `coder workflow run`, and `coder agent run`
  area/action commands for running app-scoped resources without importing them
  into the coder session.
- Added `/run` handling in coder prompts so the REPL and `--input` can run the
  current app facet, app operations, workflows, and agents explicitly.
- Added `coderapp.App.Run` and wired `cmd/coder`'s `coder app run` path through
  the configured coder app wrapper so `.coder.yaml` workspace defaults apply.
- Added `apps/coder/app` with initial `.coder.yaml` discovery, explicit
  `--config` support, `coder config show`, and workspace/env-file defaults for
  coder sessions.
- Added project inventory facets for fluxplane app manifests, `.coder.yaml`
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
- Coder app configuration now lives under `apps/coder/app` package
  `coderapp`, shell code uses package `codershell`, and `coder shell` local
  mode uses an in-process direct channel client instead of starting a local
  HTTP or Unix-socket daemon.
- `coder app build` now defaults to app-local artifact generation, writing a
  launcher, `Dockerfile`, `docker-compose.yaml`, and building the app image
  unless a narrower `--target` is selected.
- `coder app deploy --target docker-compose` now performs the full local Docker
  Compose deploy path: base image build, app artifact generation, app image
  build, and `docker compose up`.
- Generated app Docker images now use the `coder` binary and inherit from a
  reusable coder base image instead of building or invoking the legacy
  `agentsdk` binary.
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
- Fixed coder shell agent-mode Enter handling so prompts submit immediately,
  clear the input before the agent response returns, and append async failures
  back into the transcript.
- Updated `github.com/codewandler/modeldb` to `v0.15.1` so OpenRouter
  `openai/gpt-5.5` resolves with OpenAI Responses metadata and medium
  reasoning effort support.
- Fixed Slack channel datasource warmup so missing DM or MPIM discovery scopes
  skip only those conversation types instead of aborting app startup indexing.
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
- Redacted Kubernetes datasource raw pod and container records so environment
  variable values and value sources are not exposed when retrieving resources.

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

### Removed
- Removed the legacy `apps/agentsdk` product package and `docs/agentsdk.md`;
  `coder` is now the only product CLI surface.
- Removed the legacy `cmd/agentsdk` and `cmd/codershell` launchers; repository
  build and install tasks now produce only the `coder` binary.
- Removed the old `plugins/planexec` runtime assembly path.
- Removed the legacy `orchestration/subagent` package and `subagent.*` event
  registration/rendering/classification.

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
- Legacy `fluxplane.json` resource filesystem loader path.

## [0.8.0] - 2026-05-14

### Added
- `agentsdk init [path]` creates a minimal secure local app manifest with a
  default no-tool agent, default session, and Unix-socket direct channel.
- The repository root now includes a minimal local `fluxplane.yaml` manifest
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

[Unreleased]: https://github.com/fluxplane/fluxplane-core/compare/v0.17.0...HEAD
[0.17.0]: https://github.com/fluxplane/fluxplane-core/compare/v0.16.0...v0.17.0
[0.16.0]: https://github.com/fluxplane/fluxplane-core/compare/v0.15.0...v0.16.0
[0.15.0]: https://github.com/fluxplane/fluxplane-core/compare/v0.14.1...v0.15.0
[0.14.1]: https://github.com/fluxplane/fluxplane-core/compare/v0.13.0...v0.14.1
[0.13.0]: https://github.com/fluxplane/fluxplane-core/compare/v0.12.0...v0.13.0
[0.12.0]: https://github.com/fluxplane/fluxplane-core/compare/v0.11.0...v0.12.0
[0.11.0]: https://github.com/fluxplane/fluxplane-core/compare/v0.10.0...v0.11.0
[0.10.0]: https://github.com/fluxplane/fluxplane-core/compare/v0.9.0...v0.10.0
[0.9.0]: https://github.com/fluxplane/fluxplane-core/compare/v0.8.0...v0.9.0
[0.8.0]: https://github.com/fluxplane/fluxplane-core/compare/v0.7.0...v0.8.0
[0.7.0]: https://github.com/fluxplane/fluxplane-core/compare/v0.6.0...v0.7.0
[0.6.0]: https://github.com/fluxplane/fluxplane-core/compare/v0.5.0...v0.6.0
[0.5.0]: https://github.com/fluxplane/fluxplane-core/compare/v0.4.0...v0.5.0
[0.4.0]: https://github.com/fluxplane/fluxplane-core/compare/v0.3.0...v0.4.0
[0.3.0]: https://github.com/fluxplane/fluxplane-core/compare/v0.2.0...v0.3.0
[0.2.0]: https://github.com/fluxplane/fluxplane-core/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/fluxplane/fluxplane-core/releases/tag/v0.1.0
