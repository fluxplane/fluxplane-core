# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added
- Added native Jira issue creation and comment posting operations that convert
  Markdown bodies to Jira ADF using the same converter as `dex jira`.
- Added a native `usage` datasource plugin that exposes persisted
  `usage.recorded` runtime events for token, cost, request, byte, and wall-time
  inspection.

### Documentation
- Added `docs/README.md` as an audience-grouped index of all documentation.
- Expanded `docs/fluxplane.md` from a flat command list into per-command
  sections with common flows for `init`, `run`, `serve`, `build/deploy`,
  `config`, `describe`, `discover`, `auth`, and `datasource index`.
- Tightened `README.md`: the 30-second quickstart no longer installs both
  `coder` and `fluxplane`, and the "Start here" links point at the docs
  index and the external `coder` repository.
- Trimmed the AGENTS.md preamble: the duplicated architecture-reference list
  now appears once under `Architecture References`, and the missing blank
  line before `## Layer Rules` is fixed.
- Marked `docs/migration-from-agent-sdk.md`,
  `docs/constant-self-evolvement.md`, and `docs/observations-and-reactions.md`
  as design notes so users do not read them as user guides.
- Clarified `docs/evaluation.md` audience as developers smoke-testing a
  running app over the HTTP/SSE channel.

### Changed
- Restored live Kubernetes and Loki observability connectivity for local coder
  runs: Kubernetes datasource calls now honor request-scoped context/namespace
  filters and list deployments, while Loki discovery can use managed local
  port-forwards for in-cluster Kubernetes service candidates.
- Replaced the active architecture score gate with a released codegate
  assessment gate, added root `engine-architecture.rules.json`, and exposed
  codegate-backed Go assessment/review operations from the Go language plugin.
- Added a detailed goal-refinement design and the first durable thread-goal
  implementation: the native goal plugin now owns `/goal`
  set/status/pause/resume/clear lifecycle state, contributes ambient goal
  context, and goal continuation writes review results back to the thread.
- Added isolated goal verification: active durable goals now spawn a
  `goal-reviewer` helper session whose bound review decision controls whether
  the parent session stops or continues with reviewer suggestions.
- Raised the default large tool-result replacement threshold to 512 KiB so
  bounded reads can return their advertised payload size before spooling.
- Added datasource prewarming for plugin-declared detectors and introduced an
  explicit Slack thread reply operation for permalink-targeted replies.
- Gave native task worker and explorer agents the `web_search` operation so
  web-search tasks can use the dedicated search tool instead of generic HTTP
  fetches.
- Added native task phrase detection for parallel-work requests such as "at the
  same time" or "in parallel", enabling task scheduling operations and guiding
  read-only threads into separate explorer-assigned scheduled tasks.
- Hardened task scheduler recovery so stale worker capacity registrations do
  not interrupt executions while their execution lease is still valid.
- Rejected model-side `task_modify set_status` resets from running tasks back
  to ready or draft so stale worker outputs are not caused by premature retries.
- Made background shell/process starts detach from the initiating operation
  context and ignore `timeout_ms` as a process lifetime, so `process_wait`
  controls wait time without killing the started process.
- Made one-shot terminal runs print completed background task result artifacts
  after waiting for task completion.
- Made GitLab datasource filters accept common model-produced shapes for merge
  request pipelines, merge-request listing, and repository file lookups.
- Added inert activation set specs, resource catalog wiring, focus/surface trace
  events, `/surface`, and `/activate` as the foundation for prepared work
  surfaces.
- Added model-facing `session_focus`, `surface_info`, `surface_prepare`, and
  `surface_call` tools for inspecting, preparing, and invoking the active work
  surface.
- Added native GitLab pipeline and personal snippet operations, moved pipeline
  retry/cancel out of the merge-request operation, and documented the native
  GitLab datasource/operation workflow.
- Centralized local launch plugin auth context construction so run, serve,
  datasource indexing, and `op run` share stored-credential and opt-in
  process-environment resolution.
- Added `fluxplane op run --allow-private-network` so direct operation smoke
  tests can intentionally reach private/VPN integration hosts.
- Added a focused TruffleHog false-positive allowlist for the GitLab award
  emoji test fixture so staged secret scanning remains enabled.
- Added GitLab personal snippet datasource entities with metadata-only snippet
  records and explicit bounded file-content retrieval.
- Added GitLab Dex-parity read surfaces for activity, project languages and
  contributors, compare, blame, blob search, MR approvals, MR changes,
  discussions, award emoji, parsed diff lines, and searchable job traces.
- Made GitLab merge-request review records listable by MR filters, made MR
  approvals/changes directly gettable, and normalized scalar datasource filter
  values.
- Hardened GitLab smoke-test ergonomics for MR-ref pipeline searches, blob
  search result retrieval, pathless diff-line searches, and repository-file
  colon id variants.
- Extended the GitLab merge-request operation with edit controls, inline
  comments with diff-position validation, discussion replies/resolution, and
  award emoji reactions.
- Refreshed Go module dependencies to current compatible releases.
- Updated the README branding to use a centered transparent Fluxplane logo
  asset.
- Added a focused gitleaks allowlist for Kubernetes redaction test fixtures so
  release pushes keep scanning active without flagging intentional dummy values.
- Renamed the reusable Go module from `github.com/fluxplane/agentruntime` to
  `github.com/fluxplane/engine` and renamed the root facade package to
  `fluxplane`.
- Added the `fluxplane` app-manifest CLI and moved generic app lifecycle
  commands out of `coder app`, while keeping coder-scoped auth, datasource, and
  inspection commands on the coder product.
- Renamed authored app manifests to `fluxplane.yaml`, made old
  `agentsdk.app.*` names fail with an explicit rename diagnostic, and moved
  generated app Docker/Compose/Kubernetes launchers to the Fluxplane CLI and
  base image.
- Split the coder product into the nested `github.com/fluxplane/coder` module
  under `apps/coder`, moving its entrypoint to `apps/coder/cmd/coder` while
  keeping engine and coder tests/build tasks explicit.
- Documented Engine/Coder repository extraction readiness and added a
  coder-module architecture test that blocks imports of engine internals,
  engine command packages, and the retired in-engine coder package path.
- Added GitLab write operations for merge requests, repository files, branches,
  tags, commits, and project CI/CD variables, with GitLab named-plugin
  instances projected as a single logical tool that only exposes an `instance`
  selector when more than one configured instance is available.
- Removed the default low-risk model tool projection cap; local runtime
  commands now expose `--allow-max-tool-risk` to opt into a maximum visible tool
  risk when needed.
- Gated GitLab write-operation projection on the current token exposing the
  required `api` scope, hiding those tools when token scopes cannot be
  confirmed.
- Replaced coder's top-level `connect` auth setup command with the reusable
  `auth connect`, `auth info`, and `auth status` command group for native
  plugin credentials.
- Made Slack daemon channel API token selection configurable, defaulting to bot
  token with user-token fallback while keeping Socket Mode app tokens required.
- Renamed Slack's stored auth method to `token`, keeping `bot_token`,
  `user_token`, and `app_token` as setup fields.
- Collapsed `coder auth status` into per-plugin readiness with non-secret field
  presence for the selected method, and added auth readiness evidence for
  integration activation.
- Changed `coder auth status` to run non-interactive live connection checks by
  default, with `--no-test` for readiness-only output.
- Added first-class Atlassian `api_token` auth for Jira and Confluence with
  email/token setup fields and live current-user auth tests, while preserving
  legacy bearer `token` auth for existing service-account manifests.
- Moved Jira and Confluence environment-backed auth resolution out of the
  integrations so they consume injected secret resolvers instead of reading the
  runtime system environment directly.
- Made Atlassian API-token readiness require `site_url` or `base_url` for Basic
  auth while still reporting `ATLASSIAN_CLOUD_ID` and product-specific cloud ID
  env aliases as metadata.
- Added non-secret auth method metadata and documented the difference between
  Atlassian scoped access tokens, Basic API tokens, and OAuth2 token flows.
- Allowed coder's auth-test plugin registry to reach private/VPN network
  targets so GitLab, Jira, and Confluence connection tests work against
  intranet hosts.
- Scoped `coder auth` to the plugins declared by the active coder app resources,
  reusing pluginhost auth target resolution instead of a hard-coded first-party
  auth plugin list.
- Added wildcard operation selectors and auth-evidence-gated coder integration
  activation for GitLab, Jira, Confluence, and Slack datasource/tool surfaces.
- Gated process-environment plugin auth behind `--allow-plugin-auth-env` for
  local coder/app launches and generated app containers.
- Extended `--allow-plugin-auth-env` and resolver-aware integration plugin
  assembly to `coder datasource index` commands so authenticated datasource
  indexing sees the same declared integrations as the coder runtime.
- Added `coder --allow-private-network` so the built-in coder runtime can
  explicitly opt into private/VPN network targets without changing the default
  network boundary.
- Removed the GitLab-specific `coder datasource gitlab check` diagnostic
  command; generic datasource tooling remains under `coder datasource index`.
- Removed the legacy external connectors runtime, connector provider
  contributions, connector manifest fields, connector authorization resources,
  and `--connectors-path`; native plugin auth now uses `--auth-path`.
- Extracted the coder product out of the engine repository; engine tasks and
  docs now target the Fluxplane CLI while `github.com/fluxplane/coder` owns the
  coder binary and product module.
- Clarified agent verification guidance so `task verify` is reserved for
  explicit requests, commit preparation, or broad changes, with focused package
  checks preferred during normal iteration.
- Removed Taskfile-local Go cache and temp-directory overrides so tasks use the
  normal host Go cache configuration.
- Updated the coder self-improvement runner to reuse the host Go build cache
  instead of creating a per-run build cache.
- Documented strict conversation continuity rules and removed the stale
  provider transcript repair path in favor of fail-fast validation.
- Batched assistant tool-call transcript writes with their matching tool
  results so failed operation turns cannot persist open provider tool calls.
- Reworked architecture evaluation from the earlier `archreport` score model to
  codegate rules, hard failure categories, and review-oriented findings, and
  removed the legacy `apps/archreport` and `internal/architecture` checker.
- Improved coder shell responsiveness by batching stream repaints, caching
  transcript rendering incrementally, bounding rendered history, showing
  completion loading state, and passing shell input as raw command text.
- Improved coder shell discoverability by adding mode-aware prompt hints,
  completion picker control hints, and richer `coder shell --help` guidance.
- Further improved coder shell onboarding with empty-prompt placeholders, new-session
  timeline tips, completion counts/details, escape-to-close completion behavior, and
  running-state cancel hints.
- Added coder shell line-editing affordances for word jumps, word deletion,
  delete-under-cursor, kill-to-end, visible edit shortcuts, and quieter empty
  submits.
- Refined coder shell UI with lower-padding Monokai styling, removed empty
  input placeholders, added a compact tab strip, bounded completion selector
  rows with enter-to-accept, and clearer tool/process execution status lines.
- Expanded `/env/explain` to show current observations, derived assertions, and
  matching reaction actions in addition to configured observers and active
  session state.
- Reduced coder's always-on default tool surface by moving Go, Markdown, and
  Docker-backed code execution tools behind existing evidence/reaction
  activation, and by gating Loki/MySQL endpoint tools behind endpoint
  availability evidence, browser/image tools behind stable runtime/provider
  availability, and memory mutation tools behind stable memory-store
  availability while keeping those operations composed for reactive enablement.
- Completed the evidence/assertion vocabulary migration: `core/evidence` owns
  observations, assertions, subjects, observers, and assertion derivers;
  `runtime/evidence` owns observer and assertion-deriver execution; project
  inventory signals are now hints; and channel signal submissions are now
  triggers.
- Reorganized first-party plugin packages from the flat `plugins/*plugin`
  layout into categorized `bundles`, `native`, `languages`, `integrations`,
  `support`, `internal`, and `examples` directories with suffix-free package
  names.
- Reorganized first-party adapter packages from a flat `adapters/*` layout into
  domain directories for LLMs, resources, channels, control, storage, system,
  auth, content, embeddings, and terminal UI.
- Refactored distribution deploy internals into Docker, Docker Compose, and
  Kubernetes boundaries, with native Docker Engine and Kubernetes client paths
  for image, local stack, and manifest deployment operations.
- Moved the `go-refactor` developer CLI into `apps/go-refactor`, leaving the
  Go language plugin tree to carry reusable refactor logic instead of a host
  filesystem entrypoint.

### Fixed
- Stopped session event subscribers from forwarding zero-value events if their
  internal input channel closes, preventing runaway background task event loops.
- Restored coder shell's agentic input mode as the default and kept timeline
  dimensions stable when switching between agent and shell input.
- Cleaned generated Docker build contexts after dry-run app/base image builds
  unless callers explicitly keep the context for inspection.
- Added configurable deploy Docker build temp roots so local and test deploys
  can avoid quota-limited system temp directories.
- Restored Kubernetes namespace-registry deploys for real clusters while
  keeping k3d image-import deploys available through auto-detection or
  `--registry-mode k3d`.
- Resolved host environment substitutions before native Docker Compose deploys
  so required provider credentials are passed to containers correctly.
- Kept Kubernetes undeploy from deleting shared namespace registry resources
  that may be owned by another app or registry mode.
- Routed Kubernetes and Atlassian plugin host access through runtime system
  boundaries instead of direct environment, filesystem, or default HTTP client
  fallbacks.

### Added
- Added `scripts/coder-self-improve.sh` for local coder self-improvement
  batches that run isolated goal/reflection sessions, capture debug JSONL,
  score reports, and distill findings into a plan.
- Added a `go-refactor` developer CLI for moving Go package directories,
  renaming package clauses, and rewriting internal imports during package
  layout migrations.
- Added `core/evidence` and `runtime/evidence` packages with typed assertion
  subjects, assertion fingerprints, baseline observation, and template
  assertion-deriver support.
- Added Kubernetes app deploy RBAC generation for apps that declare the
  Kubernetes plugin, including a dedicated ServiceAccount and read-only access
  to configured namespaces.
- Added a MySQL query plugin for discovered endpoints, with Kubernetes
  Secret-backed credential resolution and read-only SQL execution.
- Added a first-slice OpenAPI plugin that loads OpenAPI 3.x specs and
  generates HTTP operations, documentation datasource records, and auth method
  declarations.
- Added a first-slice Loki plugin with endpoint discovery, connection testing,
  bounded LogQL query operations, recent-log helpers, and Loki datasource
  entities.
- Added shared endpoint discovery registries, Kubernetes-backed Loki endpoint
  providers, and global discovery/endpoint introspection operations.
- Added automatic semantic datasource context injection for allowed indexed
  datasource entities, including a datasource-facing memory bridge for semantic
  indexing and retrieval.
- Added deterministic Slack conversation/thread resumption and bounded raw
  Slack history context injection for first bot entry into an existing Slack
  thread or channel window.
- Added first-slice structured memory support with core memory contracts,
  hybrid event/data-store projection, scoped memory operations, and an opt-in
  memory plugin.
- Enabled the memory plugin by default in the embedded coder app so the main
  `coder` agent has structured memory operations available.
- Added a sanitized `kubernetes.cluster` datasource entity for discovering
  configured Kubernetes contexts and cluster targets without exposing kubeconfig
  credentials.
- Exposed the existing `file_delete` filesystem operation to coder's projected
  tool surface alongside `file_create` and `file_edit` by treating plain delete
  effects as approval-free medium-risk operations while keeping destructive and
  irreversible effects approval-gated.


- Enabled the GitLab plugin by default in the embedded coder app so GitLab
  datasource access and merge request operations are available automatically.

- Added shell and process lifecycle tooling with script execution, shell
  discovery, labeled background processes, ensure/wait/stop operations, and
  process-management documentation.
- Added an embedded coder skill with a reference guide for writing and
  reviewing `fluxplane.yaml` app manifests.

- Added plugin-contributed post-edit checks so active plugins can run
  formatter or diagnostic operations immediately after matching file edits.

- Added Docker project inventory facets and a dedicated Docker observer plugin
  for CLI/daemon availability signals.

- Added an observations and reactions design note for unifying project signals,
  toolchain probes, skill triggers, environment facts, and configurable signal
  reactions.

- Added core environment signal and reaction rule/action specs as the first
  implementation slice for the observations and reactions model.

- Added a pure runtime reaction planner for matching environment signals,
  on-change/every-turn rule evaluation, and idempotent action planning.

- Added observer, signal-deriver, and reaction contribution surfaces for
  resource bundles and selected plugins.

- Added runtime environment observer and signal-deriver contracts with pure
  helpers for phase-filtered observation and signal derivation.

- Added active observations to context provider build requests so providers can
  consume observations directly during context materialization.

- Carried selected plugin observers, signal derivers, and reaction rules through
  app composition for later session scheduling and reaction application.

- Passed composed observers, signal derivers, and reaction rules through the
  in-process facade and harness into session runtime configuration.

- Ran composed turn-phase environment observers and signal derivers during
  session input execution so plugin observations can feed context providers and
  agent step input.

- Applied planned `activate_skill` and `activate_reference` reactions during
  session input execution through the existing skill activation state and
  replayable skill events.

- Added replayable reaction action-applied events and persisted reaction
  idempotency replay so applied signal reactions are not repeated across inbound
  turns.

- Added Kubernetes plugin environment observations and signal derivation for
  non-secret selected-context and namespace availability facts.

- Added `k8s_port_forward` to the Kubernetes plugin, starting or reusing
  managed `kubectl port-forward` background processes through the runtime
  process boundary and inferring target ports only when Kubernetes resources
  expose exactly one port.

- Routed native Kubernetes client-go datasource requests through the runtime
  system network boundary while preserving client-go authentication wrappers.

- Added replayable reaction planned, skipped, and diagnostic events for
  inspecting why signal reactions did or did not run.

- Added app manifest decoding for observation and reaction resources, including
  top-level `observations`/`reactions` and multi-document observer,
  signal-deriver, and reaction documents.

- Added `.coder.yaml` parsing for top-level observation and reaction
  declarations so local coder config can carry environment observer,
  signal-deriver, and reaction specs.

- Wired bundle-authored reaction rules and `.coder.yaml` reaction declarations
  into session composition for local coder launches.

- Added template-backed signal derivation for app/user-authored
  `SignalDeriverSpec` declarations so declarative observation-to-signal mapping
  can run without plugin-specific code.

- Added a baseline runtime environment observer for cheap non-secret local
  facts such as time, locale, and username.

- Refined the observations and reactions design with explicit plugin authoring,
  conditional activation migration, and existing app manifest impact rules.

- Added composition diagnostics for unavailable environment observers and
  signal derivers, and made plugin-authored signal-deriver templates executable
  when no custom deriver is selected.

- Added reaction-driven context-provider activation so
  `enable_context_provider` actions can expose selected context providers during
  the same turn before context materialization.

- Added `tool_followup` observation scheduling after operation results so
  follow-up observations can derive signals and activate context before the next
  model step.

- Routed `run_operation` reaction actions through the existing session operation
  execution path, including operation events, safety/approval checks, and result
  observations before the agent step.

- Routed `run_command` reaction actions through the existing session command
  dispatcher so command policy and command target execution are reused for
  effectful reactions.

- Routed `run_workflow` reaction actions through the existing workflow executor
  and exposed workflow results as observations before the agent step.

- Added a built-in `env explain` session command that reports configured
  observers, signal derivers, reaction rules, replayed active state, and applied
  reaction count.

- Added `session_open` observation scheduling before the first model turn for a
  stored thread, including signal derivation and reaction application.

- Added `startup` observation scheduling at harness construction, with
  precomputed startup observations and signals passed into session turns.

- Added `lazy` observation scheduling immediately before context materialization
  when context providers are present.

- Added app/user observer override support for selected executable observers,
  including phase narrowing and `observable_kinds` filtering.

- Added `disabled: true` observer config semantics so app/user config can remove
  selected executable observers without enabling missing plugins.

- Added project inventory observations and signal derivation, plus initial Coder
  language-detected reactions for Go parser and Markdown operation-set
  activation.

- Removed the old Coder `ActivationInput` and runtime language
  `SignalMatcher` activation helpers now that language activation is represented
  through environment signals and reactions.

- Replaced session-local skill trigger activation with generated skill trigger
  signal derivation and reaction rules.

- Added Go toolchain status observations and signal derivation, plus a Coder
  `toolchain.available` reaction for the Go toolchain operation set.

- Removed datasource detection context side channels; detected datasource
  context now derives from provider request observations.

- Added an AWS environment observer plugin that derives non-secret integration
  configured/available signals from profile, region, and credential presence.

- Removed the old project signal and language toolchain observation event types
  after moving those facts into environment observations and signals.

- Fixed reaction `every_turn` planning so replayed idempotency keys do not
  suppress stable matching signals on later turns.

- Fixed effectful reaction actions so `require_approval: true` skips
  `run_operation`, `run_command`, and `run_workflow` before execution.

- Improved `/env explain` output so terminal clients show a readable environment
  summary instead of a raw Go struct dump.

- Added composition diagnostics for reaction actions that reference resources
  outside the selected app/plugin/resource graph.

- Added replayable active session state for `enable_operation_set` and
  `enable_datasource` reactions, including datasource access-policy integration.

- Projected reaction-activated operation sets into per-turn model-visible tools.

- Added `coder app deploy --target kubernetes` for plain kubectl manifest
  deployments, including local k3d image import, external registry mode,
  generated Kubernetes resources for app runtime MySQL/NATS backends, and
  Kubernetes Secrets generated from manifest-declared root workspace env files.

- Added `coder app undeploy` for Docker Compose and plain Kubernetes manifest
  teardown, preserving volumes/PVCs by default with opt-in `--volumes` deletion.

- Changed generated Kubernetes manifests to default to `.deploy/kubernetes.yaml`
  and automatically ignore `.deploy/`, keeping rendered Secret values out of
  normal app-root files.

- Added Atlassian API-token env support for native Jira and Confluence auth,
  including `JIRA_API_TOKEN`/`CONFLUENCE_API_TOKEN` aliases for scoped
  service-account bearer tokens and optional account-email Basic auth against
  site REST endpoints.
- Added a `max_scanned` glob operation option and separated glob scan limits
  from returned match limits so low `max_results` values do not hide later
  matches.

### Changed

- Expanded datasource index warmup progress logs with page counters, cumulative
  totals, cursor details, elapsed time, and stale-running checkpoint warnings.

- Cached GitLab membership source discovery during datasource indexing and
  increased default membership corpus pages to reduce tiny GitLab API calls.

- Changed `gitlab.user_membership` indexing to mirror direct GitLab group and
  project membership edges, while indexing archived projects for completeness
  and hiding archived project records and archived project membership edges
  from default datasource search/list results.

- Reworked the experimental coder shell header into a unified workspace,
  session, and observed-environment fact strip.

- Polished the coder shell TUI with a shared Monokai palette and removed the
  placeholder startup copy from the transcript.

- Separated the coder shell prompt input from the footer help so the input reads
  as a distinct field and narrow terminals no longer strand `quit` on its own
  line.

- Kept the coder shell title readable in narrow Ghostty layouts and softened
  facet styling so metadata no longer looks like accidental text selection.

- Streamed live coder shell agent, operation, and process activity into the TUI
  while a run is still in progress.

- Moved coder shell usage totals into the bottom footer, removed the outer TUI
  padding and footer hints, and changed mode switching to typed `!`/`?`
  markers instead of `alt+tab`.

- Added coder shell slash-command completion backed by the session command
  dispatcher's available command specs, including built-in command flags.

- Added per-tab coder shell input history that restores both submitted text and
  input mode when navigating with Up/Down.

### Removed

- Removed the unused declarative agent agency profile from agent specs, SDK
  builders, app bundles, and distribution describe output.

### Fixed
- Preserved kubeconfig TLS settings when Kubernetes datasource/discovery
  requests pass through the runtime network boundary, avoiding spurious x509
  failures against clusters with custom CAs.

- Kept manual/background task scheduler worker registrations fresh while
  long-running worker steps execute so peer schedulers do not interrupt active
  tasks as expired workers.

- Fixed raw slash-command submissions so `coder --input '/task ...'`, terminal
  one-shot turns, and coder shell `/task ...` input route through the session
  command dispatcher and authorize the task helper session agent.

- Retried OpenRouter GPT-5.5 Responses streams that fail before emitting model
  output and fall back to a non-streaming Responses call while preserving
  provider error details.
- Persisted operation tool results before follow-up model turns so interrupted
  Slack continuations do not replay assistant tool calls without matching tool
  outputs.

- Defaulted omitted memory operation access scopes to the current user, falling
  back to the current thread, so separate coder processes share local user
  memory without reopening unscoped cross-scope reads.

- Batched datasource field-index and data-store writes during corpus indexing
  and added a SQLite mirror scan index to reduce GitLab mirror/index runtime.

- Skipped noisy generated directories such as `.cache/` during glob operation
  traversal so repo-local Go build caches do not exhaust `max_scanned` before
  later workspace matches are reached.

- Fixed shell discovery through authorized system wrappers so `shell` and
  `shell_info` operations no longer panic when resolving available shells.

- Filtered leaked mouse scroll escape packets from coder shell prompt input.

- Kept coder shell slash commands routed through command dispatch even when the
  prompt is in agent/ask mode.

- Prevented unhandled Alt-modified key events from writing modifier text into
  the coder shell prompt.

- Stripped leaked terminal modifier artifacts such as `+alt` from coder shell
  prompt input when terminals emit them as literal text during mouse bursts.

- Upgraded the coder shell TUI to Bubble Tea v2 event handling so printable
  input is accepted through `Key.Text` while mouse wheel events are routed as
  mouse messages instead of prompt text.

- Added cursor-aware coder shell prompt editing with Home/End and left/right
  navigation, and limited `!`/`?` mode-switch markers to the cursor-at-start
  position so recalled history can be retargeted without editing the text.

- Fixed prompt-target slash commands so they project the same model-visible tools
  as normal session input before re-entering the input execution path.

- Emitted canonical Jira and Confluence web-app links for datasource records
  when Atlassian token auth is configured with only a cloud ID, and stopped
  rendering Jira REST API `self` URLs and Slack avatar image URLs as record
  links.

- Preserved nested GitLab project paths when detecting merge request URLs so
  links like `/ai/agents/slack-bot/-/merge_requests/2310` resolve to
  `ai/agents/slack-bot!2310` instead of losing the parent namespace.

- Routed native Confluence bearer-token sessions through Confluence's v1 REST
  endpoints so scoped service-account API tokens can read pages and spaces.

## [0.14.1] - 2026-05-19

### Fixed
- Preserved `SSH_AUTH_SOCK` in the bounded host process environment so git
  operations, including `git_push`, can authenticate through the user's SSH
  agent instead of hanging on passkey prompts.

## [0.14.0] - 2026-05-19

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

- Removed the legacy `apps/agentsdk` product package and `docs/agentsdk.md`;
  `coder` is now the only product CLI surface.
- Removed the legacy `cmd/agentsdk` and `cmd/codershell` launchers; repository
  build and install tasks now produce only the `coder` binary.
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

[Unreleased]: https://github.com/fluxplane/engine/compare/v0.14.1...HEAD
[0.14.1]: https://github.com/fluxplane/engine/compare/v0.14.0...v0.14.1
[0.14.0]: https://github.com/fluxplane/engine/compare/v0.13.0...v0.14.0
[0.13.0]: https://github.com/fluxplane/engine/compare/v0.12.0...v0.13.0
[0.12.0]: https://github.com/fluxplane/engine/compare/v0.11.0...v0.12.0
[0.11.0]: https://github.com/fluxplane/engine/compare/v0.10.0...v0.11.0
[0.10.0]: https://github.com/fluxplane/engine/compare/v0.9.0...v0.10.0
[0.9.0]: https://github.com/fluxplane/engine/compare/v0.8.0...v0.9.0
[0.8.0]: https://github.com/fluxplane/engine/compare/v0.7.0...v0.8.0
[0.7.0]: https://github.com/fluxplane/engine/compare/v0.6.0...v0.7.0
[0.6.0]: https://github.com/fluxplane/engine/compare/v0.5.0...v0.6.0
[0.5.0]: https://github.com/fluxplane/engine/compare/v0.4.0...v0.5.0
[0.4.0]: https://github.com/fluxplane/engine/compare/v0.3.0...v0.4.0
[0.3.0]: https://github.com/fluxplane/engine/compare/v0.2.0...v0.3.0
[0.2.0]: https://github.com/fluxplane/engine/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/fluxplane/engine/releases/tag/v0.1.0
