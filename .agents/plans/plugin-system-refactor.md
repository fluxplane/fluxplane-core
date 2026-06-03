# Plugin System Refactor Plan

## Goal

Make `fluxplane-core` the central agent-runtime repository: agentic domain model, agent loop/session/conversation runtime, contribution composition, policy, projection, and runtime orchestration. Move product tools and third-party integrations into plugin modules that can be used either directly in-process or installed/run externally through the shared plugin SDK/protocol.

Target outcome:

- `fluxplane-core` no longer carries heavy optional integration dependencies.
- `coder` and `fluxplane-apps/slack-bot` are products built on top of `fluxplane-core` plus selected plugins.
- Third-party operation/datasource integrations such as GitLab, Slack API surfaces, Jira, Confluence, Kubernetes, Docker, Loki, SQL, OpenAI, etc. live outside core. Runtime/channel adapters such as the Slack channel belong to Core.
- A plugin implementation can expose the same manifest and behavior through:
  - direct Go binding, for embedded product use;
  - stdio/CLI protocol binding, for install-on-demand or external execution.
- `fluxplane-plugin` owns the reusable plugin SDK, protocol, state provider contracts, local state implementation, and reusable plugin CLI surface.
- `fluxplane-core` is allowed to depend on `fluxplane-plugin` to bridge plugin manifests/runtime calls into Core Contributions.
- `dex` is not an end-state dependency target for products. Treat it as a compatibility/drain target until all useful plugin-management/auth/manifest/datasource/operation behavior is available through `fluxplane-plugin` and product/Core integration paths.

## Current Architectural Direction

The current dependency direction is:

```text
fluxplane-plugin  -> leaf modules only
fluxplane-plugins -> fluxplane-plugin + leaf modules + provider SDKs
fluxplane-core    -> fluxplane-plugin + leaf modules
products          -> fluxplane-core, optionally selected fluxplane-plugins
dex               -> fluxplane-plugin while useful, then compatibility wrapper or deletion target
```

Rules:

- `fluxplane-plugin` must not import `fluxplane-core` or `fluxplane-dex`.
- `fluxplane-core` may import `fluxplane-plugin`.
- Product apps must not import `fluxplane-dex`.
- Product apps should consume plugin capabilities through Core's agent runtime/contribution bridge where possible.
- Concrete plugin implementations belong in `fluxplane-plugins`, not Dex and not Core.
- Default plugin state for the reusable CLI/backend is under `~/.fluxplane/plugins/*`.

## Current Problem

The repository currently mixes several categories of code under `fluxplane-core/plugins`:

1. Runtime/kernel-adjacent abstractions.
2. Native coding/product tools.
3. Language support tools.
4. Heavy third-party integrations.
5. Transitional dex-style plugins and older embedded plugins.

This creates dependency and architecture pressure:

- `fluxplane-core/go.mod` pulls in large optional dependency trees, including Kubernetes, Docker/Moby, GitLab, Slack, OpenAI, MySQL, OpenAPI, browser, image/model, and cloud SDK packages.
- Products get dependencies for integrations they may not use.
- Core releases become coupled to third-party API client churn.
- Plugin boundaries are inconsistent: some plugins receive broad runtime/system access, while dex-style plugins are moving toward explicit host capabilities.
- It is unclear whether a plugin is intended to be embedded, installed externally, or both.

## Design Principle

Use a hybrid plugin model:

- **Direct/in-process plugins** for product-native, high-frequency, local capabilities.
- **External stdio/CLI plugins** for optional, heavy, third-party, or independently versioned integrations.

The external binary/protocol approach is not overkill for optional integrations. It is the correct tool for dependency isolation, install-on-demand, process isolation, and independent lifecycle management. It would be overkill for always-on local coding primitives such as filesystem reads, project inventory, and Go navigation.

## Proposed Repository Roles

### 1. `fluxplane-core`

Purpose: lean agent runtime/kernel.

Keep only:

- agent/session runtime;
- operation execution model, projection, safety, and agent-runtime use of
  `github.com/fluxplane/fluxplane-operation`;
- evidence-aware context materialization in `runtime/context`, using portable
  contracts from `github.com/fluxplane/fluxplane-context`;
- contribution/resource model;
- evidence, observations, assertions, reactions;
- policy/access-control integration points using
  `github.com/fluxplane/fluxplane-policy`;
- workspace/process/network/environment abstractions where they are runtime boundaries;
- contribution resolver/loading contracts;
- minimal test/example plugins only if needed.

Remove or migrate from core:

- `plugins/integrations/*`;
- `plugins/languages/*`;
- `contrib/*`;
- `plugins/bundles/coding`;
- provider SDK dependencies and integration-specific auth/client code.

### 2. `fluxplane-plugin`

Purpose: small stable plugin SDK and protocol module.

This should be a new low-dependency module. It should not import `fluxplane-core`, `fluxplane-dex`, or provider SDKs.

Suggested contents:

```text
fluxplane-plugin/
  go.mod
  manifest/
  protocol/
  sdk/
  host/
  binding/
    direct/
    stdio/
  operation/
  datasource/
  context/
  schema/
  testkit/
```

Responsibilities:

- plugin manifest model;
- operation specs and operation call/result types;
- datasource specs and datasource call/result types;
- context provider specs;
- auth and secret-purpose declarations;
- endpoint declarations;
- host capability interfaces:
  - HTTP;
  - environment lookup;
  - secret/auth material;
  - endpoint resolution;
  - blob read/write/info;
  - generic provider calls;
- stdio/framed protocol;
- direct binding adapter;
- manifest/schema lint testkit;
- fake host testkit.

### 3. `fluxplane-dex`

Purpose: temporary compatibility and inventory/drain target.

Do not make products depend on Dex. Useful behavior should be migrated to:

- `fluxplane-plugin` for reusable plugin CLI/state/runtime/auth/manifest/invoke surfaces;
- `fluxplane-core` for agent-runtime Contribution bridging;
- `fluxplane-plugins` for concrete implementations;
- leaf modules for reusable domain contracts.

Delete, shrink, or wrap Dex once its useful end-user behavior exists through those surfaces.

### 4. `fluxplane-plugins`

Purpose: actual plugin implementations.

Use a monorepo initially, with individual Go modules for heavy plugins or plugin families.

Suggested layout:

```text
fluxplane-plugins/
  go.work
  marketplace.json

  coding/
    go.mod
    plugin.go

  native/
    filesystem/
      go.mod
    shell/
      go.mod
    project/
      go.mod
    browser/
      go.mod
    human/
      go.mod
    code/
      go.mod

  languages/
    golang/
      go.mod
    markdown/
      go.mod

  integrations/
    git/
      go.mod
    gitlab/
      go.mod
    slack/
      go.mod
    jira/
      go.mod
    confluence/
      go.mod
    kubernetes/
      go.mod
    loki/
      go.mod
    docker/
      go.mod
    sql/
      go.mod
    openai/
      go.mod
    openapi/
      go.mod
    tavily/
      go.mod
    duckduckgo/
      go.mod
```

Each plugin should expose a direct constructor and a stdio entrypoint, for example:

```go
package gitlab

func New() plugin.Plugin
```

and:

```go
package main

func main() {
    stdio.Serve(gitlab.New())
}
```

## Plugin Categories

### Kernel/Core

Belongs in `fluxplane-core`:

- operation model;
- session/agent runtime;
- contribution/resource model;
- contribution resolver abstractions;
- policy/evidence/context contracts;
- workspace/process/network/env boundaries;
- no provider SDKs.

### Product-Native Coding Plugins

Belong outside core, but are embedded directly by `coder` by default:

- filesystem;
- project inventory;
- shell/process;
- git;
- Go support;
- Markdown support;
- browser, if product default;
- code execution;
- human clarification/notification;
- memory/task/skills when product features require them.

Potential module:

```text
github.com/fluxplane/fluxplane-plugins/coding
```

or a separate repo:

```text
github.com/fluxplane/fluxplane-coding
```

### External Integration Plugins

Belong in `fluxplane-plugins/integrations/*` and run through the shared
`fluxplane-plugin` runtime/CLI path by default:

- GitLab;
- Slack;
- Jira;
- Confluence;
- Kubernetes;
- Docker;
- Loki;
- Grafana;
- Prometheus;
- SQL/MySQL/Postgres;
- OpenAI/Ollama;
- OpenAPI-generated integrations;
- Tavily/DuckDuckGo;
- AWS and other cloud integrations.

### Product-Essential Integration Plugins

A product may embed a plugin directly when the integration is core to the product.

Example:

- `fluxplane-apps/slack-bot` uses Core's Slack channel adapter for transport/runtime integration.
- `fluxplane-apps/slack-bot` may also directly import the standalone Slack plugin for Slack operations/datasources.
- `coder` should usually use GitLab/Jira/Slack via managed external plugins unless a distribution intentionally embeds them.

## Direct Binding + Stdio Binding

A plugin implementation should be written once and exposed in two ways:

1. Direct Go binding:

```go
bundle := gitlab.New()
host.Register(pluginbinding.Direct(bundle))
```

2. Stdio/CLI binding:

```sh
fluxplane-plugin install gitlab
fluxplane-plugin operation invoke gitlab gitlab.merge_request_search --input '{...}'
```

Same plugin:

- same manifest;
- same operation names;
- same datasource contracts;
- same context providers;
- same auth declarations;
- same schema validation;
- same bounded result behavior.

## Host Capability Model

Plugins should be IO-free by default and use host capabilities rather than direct IO.

Plugin code should avoid:

```go
os.Getenv(...)
http.DefaultClient.Do(...)
os.Open(...)
exec.Command(...)
```

Instead, use SDK-provided host capabilities:

```go
host.Env.Lookup(...)
host.HTTP.Do(...)
host.Secret.Use(...)
host.Endpoint.Resolve(...)
host.Blob.Read(...)
host.Provider.Call(...)
```

Benefits:

- no secrets in model-visible params/results;
- consistent endpoint and auth handling;
- policy enforcement in the host;
- better auditability;
- direct and stdio modes behave the same;
- plugins remain testable with fake hosts.

## Dependency Rules

Target dependency graph:

```text
fluxplane-plugin
  ↑
  ├── fluxplane-core
  ├── fluxplane-dex
  ├── fluxplane-contrib
  ├── fluxplane-plugins/integrations/gitlab
  └── ...

fluxplane-core
  ↑
  ├── coder
  └── fluxplane-apps/slack-bot

fluxplane-plugins/*
  ↑
  ├── coder, when directly embedded
  ├── fluxplane-apps/slack-bot, when directly embedded
  ├── fluxplane-core, only for selected runtime-level default plugins if intentionally chosen
  └── marketplace-aware fluxplane-plugin CLI/runtime, when installed/run externally
```

Forbidden dependencies:

```text
fluxplane-plugin -> fluxplane-core
fluxplane-plugin -> fluxplane-dex
fluxplane-plugin -> provider SDKs
fluxplane-core -> fluxplane-plugins/integrations/*
fluxplane-core -> slack-go
fluxplane-core -> gitlab client
fluxplane-core -> k8s client-go
fluxplane-core -> Docker SDK
fluxplane-core -> MySQL/Postgres drivers
fluxplane-core -> OpenAI SDK
```

## Module Strategy

Use individual modules for plugins that need independent install/build/versioning.

Standalone module required when a plugin:

- has heavy third-party dependencies;
- is installed by the shared marketplace-aware plugin CLI/runtime;
- should be independently versioned;
- is optional for products;
- has provider-specific live tests;
- may be built as a standalone binary.

This applies to:

- GitLab;
- Slack;
- Jira;
- Confluence;
- Kubernetes;
- Docker;
- Loki/Grafana/Prometheus;
- SQL;
- OpenAI/Ollama;
- OpenAPI;
- Tavily/DuckDuckGo;
- browser if it pulls browser automation dependencies;
- image/model plugins if they pull model/image dependencies.

Reasonable compromise:

- one module per heavy integration;
- one module or small set of modules for native/coding tools;
- one module for Go language tools if dependency isolation is useful.

## Migration Plan

### Phase 0: Freeze Core Plugin Growth

Rules:

- No new third-party integration code in `fluxplane-core`.
- No new provider SDK dependencies in `fluxplane-core`.
- New integration work happens in `fluxplane-plugins`.

Acceptance criteria:

- Architecture rule documented.
- CI warns or fails when forbidden provider SDKs are imported by core.

### Phase 1: Extract Plugin SDK/Protocol

Create `github.com/fluxplane/fluxplane-plugin`.

Move/extract from dex:

- protocol frame/request/response types;
- manifest types;
- operation/datasource/context plugin contracts;
- host capability interfaces;
- direct binding adapter;
- stdio binding adapter;
- testkit helpers.

Acceptance criteria:

- `fluxplane-plugin` has a small `go.mod`.
- `fluxplane-plugin` does not import `fluxplane-core`.
- `fluxplane-plugin` does not import `fluxplane-dex`.
- `fluxplane-plugin` does not import provider SDKs.
- One existing dex plugin can compile against the new SDK.

### Phase 2: Create `fluxplane-plugins`

Create the plugin monorepo with `go.work` and marketplace metadata.

Start by moving or copying dex-style plugins that already follow the external plugin model:

- GitLab;
- Slack;
- Jira;
- Confluence;
- Kubernetes;
- Loki;
- Docker;
- SQL;
- Tavily;
- DuckDuckGo;
- OpenAI/Ollama/Prometheus/Grafana where applicable.

Acceptance criteria:

- Each migrated plugin has its own `go.mod` if heavy.
- Each plugin exposes `New() plugin.Plugin`.
- Each plugin has a stdio `main` package.
- Each plugin can run direct unit tests without live credentials.
- The marketplace-aware `fluxplane-plugin` CLI can install/run at least one migrated plugin from the new repo.

### Phase 3: Move Heavy Integrations Out of `fluxplane-core`

Remove or deprecate these core packages:

```text
fluxplane-core/plugins/integrations/gitlab
fluxplane-core/adapters/channels/slack
fluxplane-core/plugins/integrations/jira
fluxplane-core/plugins/integrations/confluence
fluxplane-core/plugins/integrations/kubernetes
fluxplane-core/plugins/integrations/loki
fluxplane-core/plugins/integrations/mysql
fluxplane-core/plugins/integrations/docker
fluxplane-core/plugins/integrations/aws
fluxplane-core/plugins/integrations/openai
fluxplane-core/plugins/integrations/openapi
fluxplane-core/plugins/integrations/web/search providers
fluxplane-core/plugins/internal/atlassian
```

Temporary compatibility options:

- keep deprecated adapter packages for one release that import the new plugin modules;
- or remove immediately after product references are updated.

Acceptance criteria:

- `fluxplane-core/go.mod` no longer requires heavy provider SDKs unless still needed by non-plugin core code.
- `go mod why` in `fluxplane-core` shows no dependency for:
  - `k8s.io/client-go`;
  - `gitlab.com/gitlab-org/api/client-go/v2`;
  - `github.com/slack-go/slack`;
  - Docker/Moby clients;
  - MySQL driver;
  - OpenAI SDK.
- Core tests pass.

### Phase 4: Move Product Plugins Out of Core

Move coding-product tools into coder-owned packages:

```text
coder/internal/plugins/coding
coder/internal/contrib/filesystem
coder/internal/contrib/shell
coder/internal/contrib/project
coder/internal/contrib/browser
coder/internal/contrib/code
coder/internal/plugins/languages/golang
coder/internal/plugins/languages/markdown
```

Move reusable external integrations into `fluxplane-plugins`:

```text
fluxplane-plugins/integrations/git
```

Source packages:

```text
fluxplane-core/plugins/bundles/coding
fluxplane-core/contrib/filesystem
fluxplane-core/contrib/shell
fluxplane-core/contrib/project
fluxplane-core/contrib/browser
fluxplane-core/contrib/code
fluxplane-core/plugins/languages/golang
fluxplane-core/plugins/languages/markdown
fluxplane-core/plugins/integrations/git
```

Product decision:

- `coder` directly embeds coding/native/language plugins.
- `fluxplane-core` keeps runtime/session/domain plugins such as `human` until
  that boundary is intentionally redesigned.
- Do not create a shared `fluxplane-language` module unless a second non-coder
  consumer needs the same language DTOs.

Acceptance criteria:

- `coder` has the same default coding tools after migration.
- `fluxplane-core` no longer exports product-level coding bundles.
- Core dependency tree shrinks further.

### Phase 5: Make Product Plugin Policy Explicit

Define product configuration for plugin loading.

For `coder`:

```yaml
plugins:
  builtin:
    - coding
    - golang
    - markdown
  managed:
    runtime: fluxplane-plugin
    auto_install: true
    allow:
      - gitlab
      - jira
      - confluence
      - slack
      - loki
      - kubernetes
      - docker
      - sql
```

Suggested defaults:

- Built-in direct: coding tools.
- Managed external: heavy optional integrations.
- Product-specific direct: only when the product cannot function without that integration.

Acceptance criteria:

- `coder` can expose built-in tools without dex.
- `coder` can call managed external plugins when installed/configured.
- Optional integrations are not linked into the default `coder` binary unless intentionally selected.

### Phase 6: Parity and Safety Tests

For each plugin supporting both direct and stdio modes, add tests for:

- manifest validity;
- operation schema validity;
- field descriptions;
- effects/risk annotations;
- direct operation call;
- stdio/protocol operation call;
- datasource search/list/get compatibility;
- context provider behavior, if any;
- no secrets in params/results/errors/logs;
- host capability use;
- denied/missing capability errors;
- endpoint/auth defaulting behavior;
- bounded result sizes.

Add architecture tests:

- `fluxplane-core` forbidden import check;
- `fluxplane-plugin` forbidden import check;
- plugin IO-free checks where applicable;
- `go mod why` or equivalent dependency audit.

## Product Migration Notes

### `coder`

Target:

- imports `fluxplane-core` for runtime;
- imports coding/native/language plugins directly;
- uses the shared plugin runtime/CLI path for optional third-party plugins;
- does not import GitLab/Slack/Jira/Kubernetes/etc. by default.

Default direct plugin set:

- filesystem;
- project inventory;
- shell/process;
- git;
- Go;
- Markdown;
- human clarification;
- sleep/clock if still needed;
- browser/code execution depending on product config.

Optional managed plugin set:

- GitLab;
- Jira;
- Confluence;
- Slack;
- Loki;
- Kubernetes;
- Docker;
- SQL;
- web search providers;
- OpenAI/Ollama.

### `fluxplane-apps/slack-bot`

Target:

- imports `fluxplane-core` for runtime;
- uses Core's Slack channel adapter for channel transport and runtime IO;
- may import the standalone Slack plugin directly for Slack operations/datasources, or use it as a managed external plugin.

Recommendation:

- Keep channel transport in Core because channels are part of the agent runtime boundary.
- Keep Slack API operation/datasource implementation outside Core unless it is required by the channel adapter itself.

## Release and Compatibility Strategy

Recommended staged release path:

1. Release `fluxplane-plugin` initial SDK/protocol.
2. Release dex version that uses `fluxplane-plugin` while preserving compatibility with current plugins.
3. Release first `fluxplane-plugins` modules.
4. Update `coder` to consume direct coding plugins from outside core.
5. Remove or deprecate core integration packages.
6. Cut a core major/minor release with dependency cleanup notes.

Compatibility options:

- Keep deprecated adapter packages in core for one release if needed.
- Mark old embedded integration packages as deprecated in Go docs.
- Provide migration mapping from old package paths to new plugin modules.
- Avoid long-term duplicate implementations.

## CI Gates

Add these checks before completing the migration:

### Core forbidden dependencies

Fail if `fluxplane-core` imports or requires:

- `github.com/slack-go/slack`;
- `gitlab.com/gitlab-org/api/client-go/v2`;
- `k8s.io/client-go` or broad `k8s.io/*` runtime clients;
- Docker/Moby clients;
- SQL drivers;
- OpenAI SDK;
- AWS SDK;
- provider-specific integration SDKs.

### Plugin SDK forbidden dependencies

Fail if `fluxplane-plugin` imports:

- `fluxplane-core`;
- `fluxplane-dex`;
- provider SDKs;
- heavyweight runtime packages.

### Plugin quality gates

For each plugin module:

- `go test ./...` passes;
- manifest lint passes;
- operation/datasource schemas have descriptions;
- direct and stdio parity tests pass;
- no direct secret leakage;
- IO-free checks pass where expected.

## Success Criteria

The refactor is complete when:

- `fluxplane-core` is a lean runtime/kernel module.
- `fluxplane-core/go.mod` no longer pulls optional integration dependency trees.
- A new `fluxplane-plugin` module owns stable plugin SDK/protocol contracts.
- `dex` installs and runs external plugins through the shared protocol.
- `fluxplane-plugins` contains migrated plugin implementations.
- `coder` embeds coding tools directly from plugin modules, not from core.
- Optional integrations are managed externally through the shared plugin CLI/runtime by default.
- Product-essential integrations can still be embedded directly without duplicating implementation logic.
- Every migrated plugin can be used through both direct binding and stdio binding where appropriate.
- CI prevents core from regrowing heavy provider dependencies.

## Current Progress Snapshot

Completed in the first extraction batch:

- Created a new sibling repository at `../fluxplane-plugin` / workspace path `fluxplane-plugin/`.
- Initialized it as an independent git repository from the start.
- Seeded it with a literal copy of `fluxplane-dex/fluxplaneplugin`:

  ```sh
  cp -R fluxplane-dex/fluxplaneplugin/. fluxplane-plugin/
  ```

- Committed the seed copy in `fluxplane-plugin`:

  ```text
  416f21a chore: seed plugin sdk from dex fluxplaneplugin
  ```

- Renamed the module path from:

  ```text
  github.com/fluxplane/fluxplane-dex/fluxplaneplugin
  ```

  to:

  ```text
  github.com/fluxplane/fluxplane-plugin
  ```

- Copied the dex protocol package into `fluxplane-plugin/protocol` as a staging location for shared protocol types.
- Updated internal tests to import `github.com/fluxplane/fluxplane-plugin`.
- Added a staging `README.md` describing the current transitional state and target package layout.
- Committed the rename/protocol staging work in `fluxplane-plugin`:

  ```text
  8bc9245 chore: rename module and stage protocol package
  ```

- Added `./fluxplane-plugin` to the root `go.work` workspace.
- Verified the new module:

  ```sh
  cd fluxplane-plugin
  go test ./...
  ```

  Result:

  ```text
  ok github.com/fluxplane/fluxplane-plugin
  ok github.com/fluxplane/fluxplane-plugin/protocol
  ```

- Committed this plan in `fluxplane-core`:

  ```text
  58f5bc3 docs: add plugin system refactor plan
  ```

Current important caveat:

- `fluxplane-plugin` is only seeded and staged. It still depends on both `fluxplane-core` and `fluxplane-dex` and therefore is not yet the final lean SDK/protocol module.

## Complete `fluxplane-plugin` Decoupling Checklist

### Planning Update: Prefer Breaking Moves Over Compatibility Layers

Project preference:

- Avoid backwards-compatibility shims, alias packages, and long-lived adapters wherever possible.
- Prefer coordinated breaking migrations that update import paths directly and delete obsolete duplicate packages.
- Temporary adapters are acceptable only as short-lived implementation staging when there is no practical single-step move, and they should have an explicit deletion target.

Implications for the plan:

- When moving dex protocol to `fluxplane-plugin/protocol`, update dex and plugin imports directly, then delete/stop using `fluxplane-dex/protocol` rather than maintaining aliases.
- When moving `pluginbinding` contracts out of dex, update plugin imports directly rather than preserving a long-lived dex `pluginbinding` facade.
- When moving core contracts to standalone modules, update core/product/plugin imports directly. Avoid compatibility alias packages unless a change is too large to land safely in one pass.

### Planning Update: Reusable Plugin Management CLI Package Belongs In `fluxplane-plugin`

`fluxplane-plugin` should provide backend contracts, an importable
plugin-management CLI package, and a standalone default CLI entrypoint. The
standalone entrypoint must remain registry-agnostic: it discovers marketplace
metadata through explicit files, `FLUXPLANE_PLUGIN_MARKETPLACE`, local
development paths such as `../fluxplane-plugins/marketplace.json`, and the
user-level `~/.fluxplane/plugins/marketplace.json` location.

`fluxplane-plugins` may still provide a convenience marketplace-aware wrapper or
registry package for distributions that want to embed the canonical plugin
catalog, but that wrapper is not the reusable CLI source of truth.

Target layout:

```text
fluxplane-plugin/
  management/             # backend-neutral plugin management interfaces and DTOs
  cli/                    # importable Cobra command tree
  cmd/fluxplane-plugin/    # registry-agnostic standalone CLI entrypoint

fluxplane-plugins/
  marketplace.json
  registry.go             # embedded canonical marketplace
  cmd/fluxplane-plugin/   # optional canonical-registry wrapper/distribution binary
```

Expected future UX:

```sh
fluxplane-plugin install gitlab
fluxplane-plugin list
fluxplane-plugin remove gitlab
fluxplane-plugin run gitlab
fluxplane-plugin manifest gitlab
```

Architecture:

- `fluxplane-plugin/management` defines backend-neutral interfaces such as installer, registry, store, remover, runner, and manifest inspector.
- `fluxplane-plugin/cli` owns the reusable Cobra command tree and depends only on `management`, not dex.
- `fluxplane-plugin/cmd/fluxplane-plugin` wires `fluxplane-plugin/cli` to the local/default backend without importing Dex, Core, or concrete plugin implementations.
- `fluxplane-plugins/cmd/fluxplane-plugin`, if retained, imports `fluxplane-plugin/cli`, wires the local/default backend, and injects the canonical `fluxplane-plugins` registry for distribution convenience.
- `fluxplane-dex`, while it exists, imports `fluxplane-plugin/cli` or SDK/runtime packages only as a compatibility wrapper. Products must not import Dex.

Dependency rule:

```text
fluxplane-plugin/cli -> fluxplane-plugin/management
fluxplane-plugin/cmd -> fluxplane-plugin/cli + fluxplane-plugin/management/local
fluxplane-plugins    -> fluxplane-plugin/cli + fluxplane-plugin/management/local, only for optional registry wrapper
fluxplane-dex        -> fluxplane-plugin and fluxplane-plugins only while retained
fluxplane-plugin     -> not fluxplane-dex
fluxplane-plugin     -> not fluxplane-plugins
```

### Planning Update: Do Not Extract `core/resource` Wholesale

`fluxplane-core/core/resource` currently contains both low-level resource reference types and the cross-domain `ContributionBundle` aggregate. `ContributionBundle` imports many core domains:

```text
core/activation
core/agent
core/app
core/command
core/context
core/data
core/evidence
core/language
core/llm
core/operation
core/reaction
core/session
core/skill
core/tool
core/workflow
```

Therefore, moving `core/resource` wholesale into `../fluxplane-resource` would pull much of core along with it.

Preferred split:

1. Create `../fluxplane-resource` for low-level leaf types only:
   - `ID`;
   - `Scope`;
   - `SourceRef`;
   - `Severity`;
   - `Diagnostic`;
   - `PluginRef`;
   - maybe `LoadError` only if the event dependency is acceptable.
2. Keep `ContributionBundle` in core until its member specs are standalone.
3. Later, after operation/context/agent/skill/reaction/evidence/etc. contracts are extracted, move a normalized aggregate to a dedicated module such as `../fluxplane-contribution` or to a plugin manifest/contribution package.

### Planning Update: Extraction Order Before Touching Agent/Contribution Bundles

Do not extract `core/agent` or `ContributionBundle` first. Extract their dependencies first.

Recommended order:

1. Use existing `../fluxplane-operation` as the canonical operation contract module.
2. Create minimal `../fluxplane-resource` for leaf resource refs only.
3. Move contribution resolver/contributor contracts to `fluxplane-plugin/contributions` unless a separate `../fluxplane-contributions` becomes clearly necessary.
4. Create `../fluxplane-context` or `fluxplane-plugin/context` for context provider contracts.
5. Create `../fluxplane-evidence` for observation/assertion/evidence DTOs.
6. Create `../fluxplane-reaction` for reaction rule contracts.
7. Create/extract `../fluxplane-skill` if agent/resource specs require skill contracts outside core.
8. Only then evaluate `../fluxplane-agent` for reusable agent specs.
9. Only after that evaluate a standalone contribution bundle module.

Rationale:

- `agent.Spec` currently depends on operation, context, evidence, skill, datasource, and event contracts.
- `ContributionBundle` depends on even more core domains.
- Extracting low-level contracts first avoids creating a standalone module that immediately depends back on core.

### Progress Update: Plugin Management CLI And Protocol Ownership

Completed in this batch:

- `fluxplane-plugin` now owns a backend-neutral plugin management API under `management`.
- `fluxplane-plugin` now owns an importable Cobra command tree under `cli`.
- Added a `management/local` filesystem-backed backend owned by `fluxplane-plugin`, so consumers can install/list/search/remove local plugin metadata without dex.
- The local backend stores plugin metadata in a JSON state file and executes configured stdio/CLI plugin processes for runtime-backed calls.
- The reusable CLI/backend now covers plugin install/list/search/status/update/enable/disable/remove, auth methods/connect/test/status/disconnect, manifest inspection, operation list/invoke/batch, datasource list/search/get/lookup/records/batch-get, context list/build, endpoint discover/list/get/save/remove, and plugin `run`.
- `fluxplane-plugin/protocol` is now consumed directly by `fluxplane-dex`.
- Removed the duplicate `fluxplane-dex/protocol` package instead of adding a compatibility shim.
- Updated dex test fixture module setup to replace/import `fluxplane-plugin` directly.

Validation completed:

```sh
cd fluxplane-plugin
GOWORK=off go test ./...

cd ../fluxplane-dex
go test ./...
```

Additional dependency check:

```sh
cd fluxplane-plugin
go list -deps ./management/... ./cli | grep -E 'github.com/fluxplane/fluxplane-(core|dex)'
```

Result: no matches for the new management/CLI packages.

Current caveat:

- This snapshot predates the final registry split. The current target is that
  `fluxplane-plugin` remains core-free/dex-free and registry-agnostic, while
  `fluxplane-plugins/cmd/fluxplane-plugin` owns the default marketplace-aware
  binary.

### Progress Update: Reusable Endpoint Store Moved Into Plugin CLI Backend

Completed in the current uncommitted batch:

- Extended `fluxplane-plugin/management` with backend-neutral endpoint store contracts:
  - list stored endpoint refs with optional product filtering;
  - get endpoint refs by bare ID or `@endpoint/...` ref;
  - save validated endpoint refs;
  - remove endpoint refs, including dry-run support.
- Implemented endpoint persistence in `fluxplane-plugin/management/local` using the local `~/.fluxplane/plugins/*` state model.
- Added reusable CLI commands:

  ```sh
  fluxplane-plugin endpoint list --product loki
  fluxplane-plugin endpoint get @endpoint/loki-dev
  fluxplane-plugin endpoint save loki-dev http://loki:3100 --product loki
  fluxplane-plugin endpoint remove loki-dev
  ```

- Added CLI fake-backend coverage for endpoint list/get/save/remove wiring.
- Added local backend coverage proving endpoint save/list/get/remove persists through the state backend and normalizes endpoint URLs.

Validation completed:

```sh
cd fluxplane-plugin
go test ./management ./management/local ./cli
go test ./...
```

Result: all `fluxplane-plugin` tests passed.

### Progress Update: Endpoint Records And Health Are Now Reusable

Completed in the current uncommitted batch:

- Extended `fluxplane-plugin/management` endpoint store results to include full `fluxplane-endpoint.Record` values while preserving simple endpoint refs for existing CLI consumers.
- Added reusable endpoint health storage contracts:
  - `EndpointHealthRequest`;
  - `EndpointHealthResult`;
  - `EndpointStore.SaveEndpointHealth`.
- Updated `fluxplane-plugin/management/local` to persist endpoint records with created/updated timestamps and `LastHealth`.
- Preserved existing endpoint health when an endpoint ref is updated.
- Added a reusable CLI command:

  ```sh
  fluxplane-plugin endpoint health @endpoint/loki-dev --ok --method tcp_connect --duration-ms 42
  ```

- Added CLI fake-backend coverage for endpoint health command wiring.
- Added local backend coverage for endpoint records, health save, health preservation on update, and removal.

Validation completed:

```sh
cd fluxplane-plugin
go test ./management ./management/local ./cli
go test ./...
go test . -run 'Test.*Boundary|Test.*Import|Test.*Depend'
rg "github.com/fluxplane/fluxplane-(core|dex)" . -g'*.go'
```

Result: all `fluxplane-plugin` tests passed, the boundary test passed, and the import scan found no Core/Dex Go imports.

### Progress Update: Context Boundary Tightened Without Breaking Core APIs

Completed in the current uncommitted batch:

- Confirmed `fluxplane-context` is the canonical portable context DTO module for provider specs, request fields, block metadata, placement, freshness, and sensitivity.
- Kept `fluxplane-core/core/context` as Core's agent-runtime materialization layer for evidence-aware requests, render records/diffs, committed render state, and context render events.
- Added non-breaking conversion helpers in Core:
  - `core/context.RequestFromPortable(fpcontext.Request)`;
  - `core/context.Request.Portable()`;
  - `core/context.BuildRequest.Portable()`.
- Updated Core's plugin bridge to build SDK context payloads from the portable request projection instead of manually treating Core's evidence-aware request as the SDK shape.
- Added tests proving portable conversion preserves runtime-neutral fields and copies mutable scope maps.

Validation completed:

```sh
cd fluxplane-core
go test ./core/context ./orchestration/pluginbridge

cd ../fluxplane-context
go test ./...
```

Result: all focused context and pluginbridge tests passed.

### Progress Update: Coder No Longer Imports Core Integration Plugins In Tests

Completed in the current uncommitted batch:

- Removed remaining `github.com/fluxplane/fluxplane-core/plugins/integrations/*` imports from `coder` test code.
- Replaced test-only dependency on old Core integration constants with local string constants for legacy operation/plugin names under assertion.
- Added a `coder` architecture gate that fails if any Go source imports old Core integration plugin packages.

Validation completed:

```sh
cd coder
go test . ./internal/product ./internal/runtime
rg "github.com/fluxplane/fluxplane-core/plugins/integrations/" . -g'*.go'
```

Result: the focused coder tests passed. The import scan found only the forbidden prefix literal inside the new architecture test.

### Progress Update: Pure Datasource And Host SDK Packages

Completed in the next uncommitted batch:

- Added `fluxplane-plugin/datasource` as a core/dex-free plugin SDK wrapper around `github.com/fluxplane/fluxplane-datasource`:
  - datasource source/record aliases;
  - search/lookup/get input and result aliases;
  - lookup candidate/match helpers;
  - record option helpers;
  - datasource view constants.
- Added `fluxplane-plugin/host` as a core/dex-free plugin SDK host-capability contract package:
  - host capability DTOs for HTTP, blob read/write/info, env lookup, and provider calls;
  - secret material wire shape;
  - higher-level `Client` interface;
  - `NewClient(protocol.HostCaller)` adapter for host calls.
- Added focused tests for datasource lookup/record helpers and host client command dispatch.

Validation completed:

```sh
cd fluxplane-plugin
GOWORK=off go test ./...
go list -deps ./datasource ./host | grep -E 'github.com/fluxplane/fluxplane-(core|dex)'
```

Result: tests/build passed; dependency grep had no matches.

Current caveat:

- Dex has not yet been switched from `core/pluginbinding` to these new SDK packages. This batch only establishes the core/dex-free SDK targets needed for the next direct import migration.

### Progress Update: Dex Pluginbinding Uses Datasource And Host SDK Packages

Completed in the next uncommitted batch:

- Updated `fluxplane-dex/core/pluginbinding` to reuse the canonical SDK packages for host/datasource contracts:
  - `capabilities.go` now aliases `fluxplane-plugin/host` capability constants and DTOs.
  - `host.go` now aliases the SDK host client and host command constants.
  - `secrets.go` now aliases `fluxplane-plugin/host.SecretMaterial`.
  - `datasource.go` now aliases and delegates through `fluxplane-plugin/datasource` for datasource record/input/output/lookup helpers.
- Updated internal default host fallback construction to use the SDK host client adapter instead of dex-local host client implementations.

Validation completed:

```sh
cd fluxplane-dex
go test ./core/pluginbinding
go test ./...

cd ../fluxplane-plugin
GOWORK=off go test ./...
```

Result: all tests passed.

Current caveat:

- `fluxplane-dex/core/pluginbinding/datasource_meta.go` still imports `github.com/fluxplane/fluxplane-datasource` directly for declaration normalization because the new plugin SDK datasource package does not yet expose declaration normalization helpers that operate on dex/core manifest specs.

### Progress Update: Operation Manifest Contracts Moved To Dedicated Module

Completed in the next uncommitted batch:

- Added reusable plugin-facing operation manifest contracts to the dedicated `github.com/fluxplane/fluxplane-operation` module:
  - `PluginSpec`;
  - `PluginEffect` and legacy plugin effect constants;
  - `PluginRisk` and risk constants;
  - `PluginIdempotency` and idempotency constants;
  - `Access` aliases to the shared datasource access vocabulary;
  - `PluginRenderSpec`.
- Updated `fluxplane-dex/core` operation manifest types/constants to alias the dedicated `fluxplane-operation` contracts instead of owning local duplicate definitions.
- Updated dex pluginbinding datasource access conversion and protocol tests to account for the new dedicated operation module dependency.

Validation completed:

```sh
cd fluxplane-operation
go test ./...

cd ../fluxplane-dex
go test ./...

cd ../fluxplane-plugin
GOWORK=off go test ./...
```

Result: all tests passed.

Current caveat:

- These are still manifest-oriented operation declaration contracts, not the final full replacement for the richer `operation.Spec` execution model.

### Progress Update: Operation Contracts Use Domain Naming

Completed in the next uncommitted batch:

- Reworked `fluxplane-operation` operation manifest contracts to use canonical domain names instead of plugin-prefixed names:
  - `Declaration` replaces the previously added plugin-prefixed declaration type;
  - `Effect` is reused for manifest effect values, with compact `read`, `write`, `browser`, and `local_system` constants added alongside the existing semantic effect vocabulary;
  - `RiskLevel` is reused for manifest risk values, with `RiskDestructive` added;
  - `Idempotency` is reused for manifest idempotency values, with conditional/textual-unknown manifest constants added;
  - `RenderSpec` replaces the plugin-prefixed render type.
- Updated `fluxplane-dex/core` compatibility aliases to point at the neutral operation-domain names.

Validation completed:

```sh
cd fluxplane-operation
go test ./...

cd ../fluxplane-dex
go test ./core/... ./runtime
```

Result: all tests passed.

Correction after Option B cleanup:

- Removed the accidental `fluxplane-operation -> fluxplane-datasource` dependency.
- `fluxplane-operation` now owns its own `Access` vocabulary instead of aliasing datasource access types.
- `fluxplane-dex` now compares datasource declarations against `fluxplane-datasource` access constants at datasource boundaries and keeps operation access constants for operation declarations.
- Removed the uncommitted `fluxplane-plugin/manifest` draft package so no additional SDK package work remains staged by this cleanup.

Validation for this cleanup:

```sh
cd fluxplane-operation
GOWORK=off go test ./...

cd ../fluxplane-dex
go test ./...
```

Result: all tests passed after the dependency cleanup.

### Progress Update: Plugin SDK No Longer Carries Dex/Core Adapter Copy

Completed in the next uncommitted batch:

- Removed the copied dex-to-core adapter implementation from `github.com/fluxplane/fluxplane-plugin`.
- Moved runtime-specific Core plugin bridging into `fluxplane-core`.
- Reduced `fluxplane-plugin` to reusable SDK/protocol packages only:
  - root package docs;
  - `protocol`;
  - `host`;
  - `datasource`;
  - `management`;
  - `management/local`;
  - `cli`.
- Removed direct `fluxplane-core`, `fluxplane-dex`, and `fluxplane-system` module dependencies from `fluxplane-plugin/go.mod`.
- Updated `fluxplane-plugin/README.md` to describe the lean SDK boundary and the dedicated module rule: reusable domain concepts live in dedicated modules first and SDK packages re-export only for ergonomics.

Validation completed:

```sh
cd fluxplane-plugin
GOWORK=off go test ./...
```

Result: all tests passed.

Audit result:

- `fluxplane-plugin` no longer imports `github.com/fluxplane/fluxplane-core` or `github.com/fluxplane/fluxplane-dex` in Go source.
- Only docs mention `github.com/fluxplane/fluxplane-dex/fluxplaneplugin` as the runtime-specific adapter home.

This is the full remaining task list to make `github.com/fluxplane/fluxplane-plugin` independent from both `github.com/fluxplane/fluxplane-core` and `github.com/fluxplane/fluxplane-dex`.

### Current `fluxplane-core` Imports To Eliminate

The copied `fluxplaneplugin` adapter currently imports these core packages:

```text
github.com/fluxplane/fluxplane-core/core/activation
github.com/fluxplane/fluxplane-core/core/evidence
github.com/fluxplane/fluxplane-core/core/operation
github.com/fluxplane/fluxplane-core/core/policy
github.com/fluxplane/fluxplane-core/core/reaction
github.com/fluxplane/fluxplane-core/core/resource
github.com/fluxplane/fluxplane-core/orchestration/contributions
github.com/fluxplane/fluxplane-core/runtime/evidence
github.com/fluxplane/fluxplane-core/runtime/workspace
```

Each of these imports needs either a new standalone contract module, migration to an existing standalone module, or relocation into an adapter package that is not part of the lean plugin SDK.

### Current `fluxplane-dex` Imports To Eliminate

The copied adapter currently imports these dex packages:

```text
github.com/fluxplane/fluxplane-dex
github.com/fluxplane/fluxplane-dex/core
github.com/fluxplane/fluxplane-dex/core/pluginbinding
github.com/fluxplane/fluxplane-dex/runtime
```

The desired end state is:

- `fluxplane-plugin` owns shared protocol and contract types.
- `fluxplane-dex` imports `fluxplane-plugin`, not the other way around.
- Any dex-engine-specific adapter lives in dex or in a separate adapter module, not in the base plugin SDK.

### Step 1: Split The Seeded Package Into SDK, Protocol, And Adapter Layers

Create clear package boundaries inside `fluxplane-plugin`:

```text
protocol/       # framed stdio protocol; no core/dex imports
manifest/       # manifest DTOs; no core/dex imports
schema/         # JSON schema helpers; no core/dex imports
host/           # host capability interfaces and DTOs; no core/dex imports
operation/      # operation specs/calls/results; no core/dex imports
datasource/     # datasource specs/calls/results; may depend on fluxplane-datasource only
context/        # context provider specs/contracts; no core/dex imports
binding/direct/ # direct binding contracts/adapters; no dex imports
binding/stdio/  # stdio serve/client; depends on protocol and sdk only
testkit/        # fake hosts, manifest lints, protocol parity helpers
adapter/dex/    # transitional dex-engine adapter, temporary or moved out later
adapter/core/   # transitional core contribution resolver adapter, temporary or moved out later
```

Acceptance criteria:

- `protocol`, `manifest`, `host`, `operation`, `datasource`, `context`, and `schema` compile without core or dex imports.
- Any remaining core/dex imports are isolated under clearly named adapter packages.

### Step 2: Move Dex Protocol Ownership Into `fluxplane-plugin/protocol`

Actions:

1. Keep the copied `protocol` package in `fluxplane-plugin/protocol` as canonical.
2. Update `fluxplane-dex/protocol` to become either:
   - a compatibility wrapper with type aliases to `fluxplane-plugin/protocol`; or
   - removed after dex imports are updated.
3. Update dex root/runtime/plugin code to import `github.com/fluxplane/fluxplane-plugin/protocol`.
4. Add protocol compatibility tests in dex and plugin modules.

Acceptance criteria:

- `fluxplane-plugin/protocol` does not import dex or core.
- `fluxplane-dex` consumes protocol from `fluxplane-plugin`.
- There is no remaining protocol duplication except temporary aliases.

### Step 3: Move `pluginbinding` Contracts Out Of Dex

Current blocker:

- `system_host.go` imports `github.com/fluxplane/fluxplane-dex/core/pluginbinding`.
- Existing plugin modules use dex `pluginbinding` for manifests, typed operation specs, datasource specs, auth declarations, host capability DTOs, and stdio serving.

Actions:

1. Move pure DTOs/helpers from `fluxplane-dex/core/pluginbinding` into `fluxplane-plugin` packages:
   - manifest spec;
   - operation spec;
   - datasource spec;
   - indexed datasource spec;
   - auth/secret purpose declarations;
   - endpoint declarations;
   - capability declarations;
   - typed schema helpers;
   - context provider declarations;
   - stdio plugin main helpers if present.
2. Keep dex runtime-specific code in dex.
3. Add temporary alias package in dex:

   ```go
   package pluginbinding

   type ManifestSpec = plugin.ManifestSpec
   type OperationSpec = plugin.OperationSpec
   // etc.
   ```

4. Migrate plugin modules from dex `pluginbinding` imports to `fluxplane-plugin` imports.
5. Remove the alias package after all plugin modules migrate.

Acceptance criteria:

- Plugin implementation modules can compile without importing `fluxplane-dex/core/pluginbinding`.
- Dex still runs old plugins during the compatibility window.
- `fluxplane-plugin` owns the manifest and binding contract names.

### Step 4: Replace Root `dex.Engine` Dependency With A Narrow Runtime Interface

Current blockers:

- `assembly.go`, `bundle.go`, `datasource.go`, `discovery.go`, `intent.go`, and tests use `*dex.Engine` directly.
- This makes the SDK depend on dex implementation details.

Actions:

1. Define narrow interfaces in `fluxplane-plugin` for the exact behavior the adapter needs, for example:

   ```go
   type Registry interface {
       Manifest(ctx context.Context, plugin string) (manifest.Manifest, error)
       List(ctx context.Context) ([]manifest.PluginEntry, error)
   }

   type OperationRunner interface {
       CallOperation(ctx context.Context, req operation.CallRequest) (operation.CallResult, error)
   }

   type DatasourceRunner interface {
       Search(ctx context.Context, req datasource.SearchRequest) (datasource.SearchResult, error)
       Get(ctx context.Context, req datasource.GetRequest) (datasource.GetResult, error)
       Lookup(ctx context.Context, req datasource.LookupRequest) (datasource.LookupResult, error)
   }

   type EndpointDiscoverer interface {
       DiscoverEndpoints(ctx context.Context, req host.EndpointDiscoveryRequest) (host.EndpointDiscoveryResult, error)
   }
   ```

2. Update dex to implement these narrow interfaces.
3. Move dex-specific construction (`dex.New`, dex workdir/dev plugin config, dex runtime provider setup) into dex or an `adapter/dex` package.
4. Keep `fluxplane-plugin` base packages accepting interfaces only.

Acceptance criteria:

- No base package in `fluxplane-plugin` imports `github.com/fluxplane/fluxplane-dex`.
- Dex can still register/run plugins through the new interfaces.

### Step 5: Move Dex Runtime Host Config Out Of SDK

Current blockers:

- `assembly.go` and `system_host.go` refer to `dexruntime.Config`, `dexruntime.HostProvider`, and related runtime internals.

Actions:

1. Define SDK-level host capability interfaces in `fluxplane-plugin/host`:
   - HTTP do;
   - blob read/write/info;
   - environment lookup;
   - endpoint resolution/discovery;
   - secret/auth material resolution;
   - provider calls.
2. Update dex runtime to adapt its existing runtime host/provider types to these SDK host interfaces.
3. Move `SystemCapabilityHost` into either:
   - dex, if it adapts fluxplane-system to dex runtime; or
   - `fluxplane-plugin/adapter/system`, if it only depends on standalone `fluxplane-system`, `fluxplane-endpoint`, `fluxplane-secret`, etc., not core/dex.
4. Remove `dexruntime` imports from the SDK packages.

Acceptance criteria:

- SDK host packages are pure interfaces/DTOs.
- Dex owns dex-runtime wiring.
- System-host wiring imports no dex package.

### Step 6: Extract `contribution resolver` Contracts From Core

Current blockers:

- Most adapter files import `github.com/fluxplane/fluxplane-core/orchestration/contributions`.
- `contribution resolver` is a core orchestration package but contains reusable plugin-facing contracts.

Actions:

1. Create a standalone plugin-host contract package, preferably inside `fluxplane-plugin`:

   ```text
   github.com/fluxplane/fluxplane-plugin/contributions
   ```

   or, if it has broader runtime use:

   ```text
   github.com/fluxplane/fluxplane-contributions
   ```

2. Move/alias these kinds of contracts from core `orchestration/contributions`:
   - `Plugin`;
   - `Context`;
   - operation contributor interfaces;
   - datasource provider contributor interfaces;
   - context provider contributor interfaces;
   - discovery provider contributor interfaces;
   - auth target contributor interfaces;
   - plugin config metadata that is not core-runtime-specific.
3. Update `fluxplane-core/orchestration/contributions` to alias or adapt the standalone contracts.
4. Update `fluxplane-plugin` to use standalone contracts.
5. Update `coder` and apps gradually.

Acceptance criteria:

- `fluxplane-plugin` no longer imports `fluxplane-core/orchestration/contributions`.
- Core remains source-compatible through aliases during migration.

### Step 7: Extract Resource Reference Types From Core

Current blockers:

- `assembly.go`, `bundle.go`, `intent_deriver.go`, tests, and adapter code import `core/resource`.

Actions:

1. Create `../fluxplane-resource` or add these types to an existing lower-level module if appropriate.
2. Move pure reference/value types:
   - `PluginRef`;
   - `OperationRef` if present;
   - datasource/resource refs;
   - endpoint/resource refs if not already in `fluxplane-endpoint`;
   - canonical resource ID helpers;
   - resource metadata structs that do not require core runtime.
3. Update core `core/resource` to alias the standalone module types.
4. Update `fluxplane-plugin` and `contribution resolver` contracts to import standalone resource types.

Acceptance criteria:

- No `fluxplane-plugin` import of `fluxplane-core/core/resource`.
- Core and products continue compiling via aliases.

### Step 8: Migrate Operation Contracts To The Existing `fluxplane-operation` Module

Current blockers:

- `plugin.go` and tests import `fluxplane-core/core/operation`.

Actions:

1. Audit the existing sibling module `../fluxplane-operation`.
2. Move or alias core operation contract types there:
   - operation spec/name/ref;
   - call/result types;
   - input schema metadata;
   - effects/risk/safety metadata;
   - operation events if they are cross-module contracts.
3. Keep runtime execution, middleware, approval, authorization, validation in `fluxplane-core/runtime/operation` unless it proves reusable.
4. Update `fluxplane-plugin` operation contracts to depend on `fluxplane-operation` rather than core.
5. Update core `core/operation` to alias `fluxplane-operation` where possible.

Acceptance criteria:

- `fluxplane-plugin` imports `github.com/fluxplane/fluxplane-operation`, not core operation.
- Operation execution runtime remains outside the SDK.

### Step 9: Extract Context Provider Contracts

Current blocker and explicit requirement:

- Context providers need to move from core-owned contracts to a real standalone module/package so plugin modules can expose context without depending on core.

Actions:

1. Create `../fluxplane-context` or `fluxplane-plugin/context` depending on how broadly the contracts are used.
2. Move pure contracts from `fluxplane-core/core/context` and relevant `contribution resolver` context-provider contributor interfaces:
   - context provider spec;
   - context build request/result;
   - context item/block/message DTOs;
   - context metadata/freshness/placement fields;
   - provider contributor interfaces;
   - context selection/filter types.
3. Keep runtime materialization/rendering in `fluxplane-core/runtime/context` unless it becomes reusable by products outside core.
4. Update plugins to expose context through standalone context contracts.
5. Update core to alias or adapt the standalone context contracts.

Acceptance criteria:

- Plugins can declare/build context providers without importing `fluxplane-core`.
- `fluxplane-plugin` context package has no dex/core imports.

### Step 10: Extract Evidence And Reaction Contracts

Current blockers:

- `intent_deriver.go`, tests, and assembly reaction rules import:
  - `core/evidence`;
  - `core/reaction`;
  - `runtime/evidence`.

Actions:

1. Create or use standalone modules:
   - `../fluxplane-evidence` for evidence/assertion/observation DTOs;
   - `../fluxplane-reaction` for reaction rules and intent matching contracts.
2. Move pure contract types:
   - evidence records/assertions;
   - assertion kind constants;
   - reaction rule specs;
   - reaction trigger/condition DTOs;
   - intent-derived assertion metadata;
   - evidence renderer/normalizer interfaces if needed by plugins.
3. Keep runtime evidence storage/projectors in `fluxplane-core/runtime/evidence` unless they are generic enough for a standalone runtime module.
4. Update `fluxplane-plugin` intent derivation to depend on standalone evidence/reaction contracts, or move intent derivation out of base SDK into `adapter/core` if it is core-specific.
5. Update core packages to alias/adapt.

Acceptance criteria:

- `fluxplane-plugin` no longer imports `core/evidence`, `core/reaction`, or `runtime/evidence`.

### Step 11: Extract Policy Contract Or Use Existing `fluxplane-policy`

Current blocker:

- `bundle.go` imports `fluxplane-core/core/policy`.

Actions:

1. Audit existing `../fluxplane-policy`.
2. Move/alias missing core policy value types there:
   - access metadata;
   - risk/effect policy metadata;
   - policy decision DTOs if shared by plugin manifests;
   - permission strings/scopes if plugin-visible.
3. Keep policy enforcement runtime in core/orchestration/security.
4. Update plugin manifest/access metadata to import `fluxplane-policy` directly.

Acceptance criteria:

- `fluxplane-plugin` does not import `fluxplane-core/core/policy`.

### Step 12: Extract Activation Contracts Or Move Activation Use Out Of SDK

Current blocker:

- `plugin.go` imports `core/activation`.

Actions:

1. Decide whether activation is a plugin SDK contract or only a core session/runtime concern.
2. If SDK-level, create `../fluxplane-activation` or include small activation DTOs in `fluxplane-plugin/context` or `fluxplane-plugin/host`.
3. Move only pure DTOs:
   - activation target/ref;
   - activation request/result;
   - activation contribution metadata.
4. Keep activation read models and runtime behavior in core.
5. If not SDK-level, isolate activation-specific logic in `adapter/core` and keep base SDK free of activation imports.

Acceptance criteria:

- Base `fluxplane-plugin` packages do not import `core/activation`.

### Step 13: Extract Workspace Boundary Types Or Depend On `fluxplane-system`

Current blockers:

- `assembly.go` and `system_host.go` import `fluxplane-core/runtime/workspace`.

Actions:

1. Identify the exact workspace dependency: launch workspace, blob root, path policy, or workspace metadata.
2. Prefer using existing `../fluxplane-system` workspace/blob interfaces if they cover the need.
3. If not covered, create `../fluxplane-workspace` for pure workspace boundary contracts:
   - workspace ref/root metadata;
   - path policy interfaces;
   - blob read/write/stat abstractions;
   - workspace-scoped capability config.
4. Keep concrete filesystem workspace implementation in core or a product/native plugin module.
5. Update `SystemCapabilityHost` to depend on standalone workspace/system interfaces.

Acceptance criteria:

- No `fluxplane-plugin` import of `runtime/workspace`.

### Step 14: Move Datasource Adapter Logic To Shared Datasource Contracts

Current state:

- `fluxplane-plugin` already imports `github.com/fluxplane/fluxplane-datasource`, which is acceptable as a standalone module.
- The adapter still bridges dex datasource specs into core contribution resolver contributor surfaces.

Actions:

1. Keep datasource core contracts in `fluxplane-datasource`.
2. Move any remaining dex datasource DTOs into `fluxplane-plugin/datasource` or map them onto `fluxplane-datasource` types directly.
3. Keep dex datasource runtime execution in dex.
4. Make datasource provider contribution interfaces use standalone contribution resolver/resource/context contracts.

Acceptance criteria:

- Datasource plugin contracts do not require core or dex.

### Step 15: Delete Dex As An Integration Bridge

The end state does not need a Dex-to-Core adapter. Dex is, at most, a temporary
CLI compatibility/drain target. Products and Core should use `fluxplane-plugin`
contracts and selected concrete `fluxplane-plugins` modules directly.

Actions:

1. Move any remaining useful Dex behavior into `fluxplane-plugin` reusable CLI,
   management, auth, datasource, endpoint, manifest, and operation packages.
2. Keep Core plugin integration in Core bridge packages that adapt
   `fluxplane-plugin` runtime contracts into Core contribution/runtime
   surfaces.
3. Remove Dex-facing adapter packages once parity is reached.
4. Keep products free of Dex imports permanently.

Acceptance criteria:

- `github.com/fluxplane/fluxplane-plugin` root/base packages do not import Dex
  or Core.
- `fluxplane-core` does not import Dex.
- `coder` and `fluxplane-apps/slack-bot` do not import Dex.
- No adapter imports both Dex and Core.

### Step 16: Update Dex To Consume `fluxplane-plugin`

Actions:

1. Add dependency from dex to `github.com/fluxplane/fluxplane-plugin`.
2. Replace dex-local protocol imports with plugin protocol imports.
3. Replace dex-local `pluginbinding` contracts with plugin SDK contracts or aliases.
4. Update dex CLI/plugin runtime tests.
5. Ensure existing plugins still install/run.

Acceptance criteria:

- Direction is `dex -> plugin`, not `plugin -> dex`.

### Step 17: Update Core To Consume Standalone Contracts

Actions:

1. Add dependencies from core to standalone modules such as `fluxplane-resource`, `fluxplane-context`, `fluxplane-evidence`, `fluxplane-reaction`, and existing `fluxplane-operation`/`fluxplane-policy` as needed.
2. Replace internal contract definitions with aliases/adapters.
3. Keep runtime/projector/orchestration implementation code in core.
4. Run core tests after each alias migration.

Acceptance criteria:

- Core becomes a consumer of shared contracts rather than their only home.
- Products can use shared contracts without importing core.

### Step 18: Update Products And Plugins

Actions:

1. Update `coder` imports from core-owned plugin contracts to standalone contracts where necessary.
2. Update `fluxplane-apps/slack-bot` similarly.
3. Update dex plugin modules to use `fluxplane-plugin` contracts.
4. Update generated plugin templates and docs.

Acceptance criteria:

- No product has to import dex to use plugin SDK contracts.
- No plugin module has to import core to declare manifest/operations/datasources/context providers.

### Step 19: Add Hard CI Guards

Add checks that fail if `fluxplane-plugin` imports:

```text
github.com/fluxplane/fluxplane-core
github.com/fluxplane/fluxplane-dex
```

Also fail if `fluxplane-plugin` pulls provider SDKs such as Slack, GitLab, Kubernetes, Docker, SQL drivers, OpenAI, AWS, etc.

Acceptance criteria:

- `go list -deps ./...` in `fluxplane-plugin` has no core/dex/provider SDK dependencies.
- `go test ./...` passes in `fluxplane-plugin` with `GOWORK=off` and inside the root workspace.

## Core Extraction Candidates For Standalone Modules/Repos

This is the list of core-owned packages/concepts that deserve their own module or repo under `../` so products/plugins can share contracts without depending on `fluxplane-core`.

### Already Existing Sibling Modules To Reuse Or Finish Migrating

These already exist and should become the canonical home for their domains where possible:

```text
../fluxplane-auth        # auth contracts and auth material references
../fluxplane-browser     # browser capability/plugin implementation boundary
../fluxplane-datasource  # datasource provider/accessor/entity/search/get/list contracts
../fluxplane-endpoint    # endpoint refs, endpoint discovery, endpoint metadata
../fluxplane-event       # event envelope/store contracts where shared
../fluxplane-operation   # operation specs/calls/results; migrate core/operation contracts here
../fluxplane-policy      # policy/access/risk/effect contracts
../fluxplane-secret      # secret refs/purposes/broker contracts
../fluxplane-system      # host/system IO capability contracts
```

Required follow-up for existing modules:

- Audit each module for overlap with `fluxplane-core/core/*` and `fluxplane-core/runtime/*`.
- Move pure DTOs/contracts from core into the sibling module.
- Leave runtime implementations/projectors/orchestration in core.
- Add alias packages in core for compatibility during migration.

### New Standalone Modules Strongly Recommended

#### `../fluxplane-resource`

Move pure resource/reference types out of `core/resource`:

- plugin refs;
- operation refs;
- datasource refs;
- resource IDs;
- resource metadata;
- canonical naming helpers.

Reason:

- Plugin SDK and products need resource references without importing core.

#### `../fluxplane-context`

Move context provider contracts out of `core/context` and `orchestration/contributions`:

- context provider specs;
- context block/item DTOs;
- context build request/result;
- context placement/freshness metadata;
- provider selection/filter contracts;
- context contributor interfaces.

Keep in core:

- runtime context materializer;
- rendering policy if session-specific;
- product/session context assembly.

Reason:

- Plugin modules need to expose context providers directly.

#### `../fluxplane-contributions` Or `fluxplane-plugin/contributions`

Move reusable contribution resolver/contributor interfaces out of `orchestration/contributions`:

- `Plugin` interface;
- contributor interfaces for operations, datasources, context, discovery, auth targets;
- plugin registration metadata;
- contribution resolver context DTOs.

Recommendation:

- If the interfaces are only meaningful for the plugin SDK, put them under `fluxplane-plugin/contributions`.
- If products need them independently from the SDK package, create `../fluxplane-contributions`.

#### `../fluxplane-evidence`

Move evidence/assertion contracts out of `core/evidence` and `runtime/evidence`:

- evidence record DTOs;
- assertion kinds;
- observation DTOs;
- evidence metadata;
- evidence normalization/rendering interfaces if shared.

Keep in core:

- evidence store implementations;
- runtime evidence projection;
- session-specific evidence collection.

#### `../fluxplane-reaction`

Move reaction rule contracts out of `core/reaction`:

- reaction specs;
- trigger/condition DTOs;
- intent reaction mappings;
- assertion match conditions.

Keep in core:

- reaction planner/runtime;
- session-specific reaction execution.

#### `../fluxplane-activation`

Move pure activation contracts out of `core/activation` if plugins/products need them:

- activation request/result DTOs;
- activation target refs;
- activation contribution metadata.

Keep in core:

- activation read model;
- activation events/projectors;
- session activation runtime.

Alternative:

- If activation remains purely session-runtime-specific, do not create this module; move activation-dependent plugin adapter code into a core adapter instead.

#### `../fluxplane-workspace`

Move pure workspace boundary contracts out of `runtime/workspace` if `fluxplane-system` is not enough:

- workspace refs/root metadata;
- path policy interfaces;
- workspace-scoped blob/file capability interfaces;
- launch workspace config contracts.

Keep in core or product/native plugin modules:

- concrete filesystem workspace implementation;
- workspace scanning/project management implementation.

#### `../fluxplane-llm`

Move model/provider/catalog contracts out of `core/llm` if more than core uses them:

- model refs;
- provider refs;
- capability metadata;
- pricing metadata;
- model catalog contracts.

Keep in product/provider plugins:

- OpenAI/Ollama/provider-specific clients;
- model routing policy if product-specific.

#### `../fluxplane-command`

Move command parsing/binding contracts out of `core/command` if CLIs/apps share them:

- command specs;
- argument binding;
- parse results;
- registry interfaces.

Keep product-specific command sets outside the shared module.

#### `../fluxplane-conversation`

Move conversation DTO contracts out of `core/conversation` if products/plugins need them:

- message DTOs;
- participant/session refs;
- conversation event DTOs.

Keep in core:

- conversation compaction/projectors;
- session-specific persistence/runtime.

#### `../fluxplane-memory`

Move memory contracts out of `core/memory` if memory tools/plugins should not import core:

- memory record DTOs;
- memory query/filter contracts;
- subject refs;
- sensitivity/access metadata.

Keep in core/product plugins:

- memory store implementations;
- indexing/projection/runtime behavior.

#### `../fluxplane-task`

Move task domain contracts out of `runtime/task`, `orchestration/taskexecutor`, and any core task DTOs if task tools/plugins should be standalone:

- task spec/status DTOs;
- step spec/status DTOs;
- artifact DTOs;
- task event contracts;
- task query/filter contracts.

Keep in core/product apps:

- task executor;
- scheduler;
- projectors;
- product-specific task workflows.

#### `../fluxplane-skill`

Move skill contracts out of `runtime/skill` if skill packs/tools are externalized:

- skill manifest DTOs;
- skill activation DTOs;
- skill repository interfaces.

Keep in core/product apps:

- skill activation runtime;
- session-specific skill context injection.

#### `../fluxplane-identity`

Move identity principal/directory contracts out of `orchestration/identity` if products/plugins share them:

- identity refs;
- principal/user/team DTOs;
- directory interfaces.

Keep in core/apps:

- local identity resolution;
- session authorization wiring.

#### `../fluxplane-project`

Move project/workspace inventory contracts out of `runtime/project` and native project plugins if they are product-shared:

- project refs;
- project manifest/facet DTOs;
- project file tree contracts;
- task entrypoint metadata.

Keep in coding/native plugin modules:

- concrete project inventory scanning;
- language-specific project detection.

#### Coding Language Contracts

Do not extract a generic `fluxplane-language` module by default. The current Go
and Markdown language surfaces are coding-product capabilities, not broad
runtime primitives.

Keep coding language contracts and implementations with the product that owns
the coding experience:

- move Go language DTOs/operations and implementation into `coder`;
- move Markdown language DTOs/operations and implementation into `coder`;
- move coding bundle composition into `coder`;
- only revisit a standalone language contract if a non-coder product needs the
  same language DTOs without importing coder.

#### `../fluxplane-data` Or Continue `../fluxplane-datasource`

Evaluate `core/data`, `runtime/data`, and `runtime/datasource`:

- if these are datasource records/entity/index contracts, fold into `fluxplane-datasource`;
- if they are broader data-store contracts, create `../fluxplane-data`.

Move only pure DTOs/interfaces. Keep stores, mirrors, field indexes, semantic indexes, and runtime implementation where they belong.

### Product/Plugin Implementation Modules To Move Out Of Core

These are not just contracts. They split by ownership: coding-specific
capabilities move into `coder`; reusable external integrations move into
`fluxplane-plugins`; Core-runtime capabilities stay in Core.

```text
coder/internal/plugins/coding
coder/internal/plugins/filesystem
coder/internal/plugins/shell
coder/internal/plugins/project
coder/internal/plugins/browser
coder/internal/plugins/code
coder/internal/plugins/languages/golang
coder/internal/plugins/languages/markdown

../fluxplane-plugins/integrations/git
../fluxplane-plugins/integrations/gitlab
../fluxplane-plugins/integrations/slack        # operations/datasources only
../fluxplane-plugins/integrations/jira
../fluxplane-plugins/integrations/confluence
../fluxplane-plugins/integrations/kubernetes
../fluxplane-plugins/integrations/loki
../fluxplane-plugins/integrations/docker
../fluxplane-plugins/integrations/sql
../fluxplane-plugins/integrations/aws
../fluxplane-plugins/integrations/openai
../fluxplane-plugins/openapi                       # generated operations/datasources from OpenAPI specs
../fluxplane-plugins/integrations/websearch

fluxplane-core/adapters/channels/slack      # channel adapter stays in Core
fluxplane-core/contrib/identity         # runtime/domain plugin stays
fluxplane-core/contrib/goal             # runtime/domain plugin stays
fluxplane-core/contrib/loop             # runtime/domain plugin stays
fluxplane-core/contrib/sessionhistory   # runtime/domain plugin stays
fluxplane-core/contrib/usage            # runtime/domain plugin stays
fluxplane-core/contrib/task             # runtime/domain plugin stays
```

Notes:

- Coding, language, and coding bundle plugins are product-owned by `coder`.
- Native filesystem/shell/project/browser/code plugins should move with coder
  while they model coding workspace behavior. Extract them later only if another
  product needs the same non-coding behavior.
- Git is a reusable external integration and should move to
  `fluxplane-plugins`.
- Slack has two surfaces: Core owns the channel adapter; `fluxplane-plugins`
  owns Slack operation/datasource behavior.
- Core keeps agent-runtime, channel, session, loop, goal, identity, task, usage,
  memory, skills, datasource/discovery, and contribution-domain plugins until a
  deliberate leaf contract exists for each boundary.

## Updated Immediate Next Actions

### Progress Update: Context Module And SDK Pluginbinding

Completed in this batch:

- Created `../fluxplane-context` as a standalone reusable context contract module.
- Moved plugin manifest/context aggregate ownership into `fluxplane-plugin/manifest`, with context types re-exported from `fluxplane-context`.
- Added `fluxplane-plugin/pluginbinding` and `fluxplane-plugin/pluginbinding/plugintest` as SDK-owned plugin authoring/test packages.
- Updated dex `core` manifest/auth/context/index types to alias SDK manifest contracts.
- Updated dex plugin implementations and provider helpers to import SDK `pluginbinding` and SDK manifest contracts instead of dex `core/pluginbinding` / dex `core`.
- Updated each dex plugin module to require local SDK contracts during development.

Validation completed:

```sh
cd ../fluxplane-context && GOWORK=off go test ./...
cd ../fluxplane-operation && GOWORK=off go test ./...
cd ../fluxplane-plugin && GOWORK=off go test ./...
cd ../fluxplane-dex && go test ./...
cd ../fluxplane-dex/fluxplaneplugin && go test ./...
cd ../fluxplane-dex && for d in plugins/*; do (cd "$d" && go test ./...); done
```

Result: all tests passed.

### Progress Update: Removed Dex Pluginbinding And Migrated First Plugin

Completed in this batch:

- Removed `fluxplane-dex/core/pluginbinding` instead of keeping a compatibility layer.
- Updated the last generated dex fixture to import SDK `manifest` and `pluginbinding` directly.
- Created `../fluxplane-plugins` as a plugin implementation monorepo with:
  - `go.work`;
  - `marketplace.json`;
  - initial `README.md`;
  - migrated `system` plugin module.
- Moved `fluxplane-dex/plugins/system` to `../fluxplane-plugins/system`.
- Renamed the system plugin module path to `github.com/fluxplane/fluxplane-plugins/system`.
- Updated the system plugin command entrypoint to import the new module path.
- Updated dex workspace, parent workspace, marketplace defaults, marketplace JSON, maintainer docs, and CLI tests to use `../fluxplane-plugins/system`.

Validation completed:

```sh
cd ../fluxplane-plugins/system && GOWORK=off go test ./...
cd ../fluxplane-plugins && go test ./system/...
cd ../fluxplane-dex && go test ./...
cd ../fluxplane-dex/fluxplaneplugin && go test ./...
cd ../fluxplane-dex && for d in plugins/*; do (cd "$d" && go test ./...); done
cd .. && go list -m all
```

Result: all tests passed.

### Progress Update: Moved Self-Contained Plugin Modules

Completed in this batch:

- Moved ten self-contained plugin modules from `fluxplane-dex/plugins` to `../fluxplane-plugins`:
  - `asterisk`;
  - `docker`;
  - `gitlab`;
  - `grafana`;
  - `kubernetes`;
  - `loki`;
  - `ollama`;
  - `prometheus`;
  - `slack`;
  - `sql`.
- Renamed their module paths from `github.com/fluxplane/fluxplane-dex/plugins/<name>` to `github.com/fluxplane/fluxplane-plugins/<name>`.
- Updated command entrypoints, local SDK replaces, dex workspace entries, parent workspace entries, and the `fluxplane-plugins` workspace.
- Updated dex marketplace defaults and checked-in marketplace JSON to install moved plugins from `github.com/fluxplane/fluxplane-plugins/...` and load local development plugins from `../fluxplane-plugins/<name>`.
- Expanded `../fluxplane-plugins/marketplace.json` and `README.md` to include the migrated plugin set.
- Updated dex tests and active docs to use the new external plugin paths.
- Left only plugins that still depend on dex-internal provider helper packages in `fluxplane-dex/plugins`:
  - `duckduckgo` / `tavily` depend on `internal/websearch`;
  - `openai` depends on `internal/vision`.

Validation completed:

```sh
cd ../fluxplane-context && go test ./...
cd ../fluxplane-plugin && go test ./...
cd ../fluxplane-plugins && go test ./asterisk/... ./docker/... ./gitlab/... ./grafana/... ./kubernetes/... ./loki/... ./ollama/... ./prometheus/... ./slack/... ./sql/... ./system/...
cd ../fluxplane-dex && go test ./...
cd ../fluxplane-dex && for d in plugins/*; do [ -d "$d" ] || continue; (cd "$d" && go test ./...) || exit 1; done
cd .. && go list -m all
```

Result: all tests passed.

### Progress Update: Moved Atlassian As One Module With Two Plugins

Completed in this batch:

- Created `../fluxplane-plugins/atlassian` as one Go module that serves two dex plugins.
- Moved Jira and Confluence into sibling packages inside that module:
  - `jira`;
  - `confluence`.
- Moved shared Atlassian helpers from dex `internal/atlassian` into `atlassian/internal/atlassian`.
- Kept separate command entrypoints and plugin identities:
  - `cmd/fluxplane-plugin-jira`;
  - `cmd/fluxplane-plugin-confluence`.
- Updated dex workspace, parent workspace, plugin workspace, marketplace defaults, checked-in marketplace JSON, plugin marketplace JSON, docs, and IO-free tests.
- Removed dex-local Jira, Confluence, and Atlassian helper modules without compatibility packages.

Validation completed:

```sh
cd ../fluxplane-plugins/atlassian && go test ./...
cd ../fluxplane-dex && go test ./...
cd ../fluxplane-dex && for d in plugins/*; do [ -d "$d" ] || continue; (cd "$d" && go test ./...) || exit 1; done
cd ../fluxplane-plugins && go test ./atlassian/... ./asterisk/... ./docker/... ./gitlab/... ./grafana/... ./kubernetes/... ./loki/... ./ollama/... ./prometheus/... ./slack/... ./sql/... ./system/...
cd .. && go list -m all
```

Result: all tests passed.

### Progress Update: Reusable Plugin CLI/State, Runtime Execution, Core Bridge, Product Wiring, Registry Split

Completed in the current uncommitted batch:

- Expanded `fluxplane-plugin/management` into a reusable plugin state API:
  - install/update/remove/list/status;
  - enable/disable activation state;
  - runtime metadata;
  - manifest payload/reference;
  - instances;
  - auth status/methods/connect/test/disconnect;
  - operation list/invoke;
  - datasource list/search/get/lookup.
- Updated `fluxplane-plugin/management/local`:
  - default state file under `~/.fluxplane/plugins/state.json`;
  - marketplace resolution from explicit marketplace files, `FLUXPLANE_PLUGIN_MARKETPLACE`, local dev `../fluxplane-plugins/marketplace.json`, and `~/.fluxplane/plugins/marketplace.json`;
  - bare `install NAME` now fails when `NAME` is not known in the marketplace;
  - explicit local/dev installs still require `--source`, `--command`, or `--manifest`;
  - `run NAME` now fails if no runnable runtime is configured;
  - marketplace installs in a local workspace prefer `../fluxplane-plugins/<plugin>` and build a cached binary under `~/.fluxplane/plugins/bin`;
  - marketplace installs without local source fall back to an existing `PATH` binary or `go install` into the same bin directory;
  - marketplace updates refresh cached artifacts when the plugin source is still marketplace-backed;
  - marketplace removes delete owned cached artifacts and refuse to delete paths outside the configured plugin bin directory;
  - `manifest` invokes the installed plugin runtime when no stored manifest is present;
  - `auth connect` and `auth test` invoke the installed plugin runtime before recording local state;
  - `operation list/invoke` and `datasource list/search/get/lookup` invoke the installed plugin runtime through the framed stdio protocol;
  - `run NAME` performs a protocol manifest handshake for stdio plugins when no extra args are passed;
  - `run NAME -- ARGS...` still executes the configured command and returns stdout/stderr/exit status for raw command use.
- Normalized active plugin binary names away from Dex-prefixed entrypoints:
  - renamed every Dex-prefixed command entrypoint directory in `fluxplane-plugins/*/cmd/` to `cmd/fluxplane-plugin-*`;
  - updated `fluxplane-plugins/marketplace.json`;
  - updated Dex compatibility marketplace metadata and defaults;
  - updated active docs/tests/plan references, leaving only changelog history with old binary names.
- Added a small standalone CLI host caller in `fluxplane-plugin/management/local`:
  - supports environment lookup;
  - supports the local `system.info` provider call used by the system plugin;
  - leaves secret, endpoint, index, HTTP, blob, and arbitrary provider calls as explicit unsupported host capabilities unless a product/Core host supplies them.
- Expanded the reusable `fluxplane-plugin/cli` command tree:
  - `install`;
  - `update`;
  - `list`;
  - `status`;
  - `enable` / `disable`;
  - `remove`;
  - `search`;
  - `manifest`;
  - `auth status|methods|connect|test|disconnect`;
  - `operation list|invoke`;
  - `datasource list|search|get|lookup`;
  - `run`.
- Split default registry ownership out of the SDK:
  - added a root `github.com/fluxplane/fluxplane-plugins` module;
  - embedded `fluxplane-plugins/marketplace.json` through a root registry package;
  - moved the marketplace-aware `cmd/fluxplane-plugin` binary into `fluxplane-plugins`;
  - removed the SDK-owned standalone `cmd/fluxplane-plugin` entrypoint so `fluxplane-plugin` remains registry-agnostic;
  - switched Dex's default marketplace fallback to the canonical `fluxplane-plugins` registry;
  - removed duplicated Dex marketplace defaults and checked-in duplicate marketplace JSON.
- Cleaned active `fluxplane-plugins` user-facing strings and HTTP user agents
  that still described plugins as Dex-owned, while leaving tests/changelog
  history and the legacy wire protocol key intact.
- Added `fluxplane-core/orchestration/pluginbridge`:
  - Core depends on `fluxplane-plugin`;
  - adapts `fluxplane-plugin/pluginruntime.Plugin` into Core `contributions.Provider`;
  - converts SDK manifests into Core operation/datasource/auth contribution specs;
  - executes Core operations through the plugin protocol/runtime.
- Updated `coder` with Go-level plugin extension points:
  - `Startup.Plugins`;
  - `WithPlugins`;
  - `BridgePlugin` helper for SDK runtime plugins through Core's bridge.
- Updated `fluxplane-apps/slack-bot` with Go-level plugin extension points:
  - `Config.Plugins`;
  - `BridgePlugin` helper for SDK runtime plugins through Core's bridge.
- Verified no product Go imports of `github.com/fluxplane/fluxplane-dex`.

Validation completed:

```sh
cd ../fluxplane-plugin && GOWORK=off go test ./...
cd ../fluxplane-plugins && GOWORK=off go test ./...
cd ../fluxplane-plugins && HOME=$(mktemp -d) GOWORK=off go run ./cmd/fluxplane-plugin install bananawix
# exits non-zero: unknown marketplace plugin
cd ../fluxplane-plugins && tmp=$(mktemp -d); HOME=$tmp GOWORK=off go run ./cmd/fluxplane-plugin install local --source dev >/dev/null && HOME=$tmp GOWORK=off go run ./cmd/fluxplane-plugin run local
# exits non-zero: no runnable runtime configured
cd ../fluxplane-plugins && tmp=$(mktemp -d); HOME=$tmp GOWORK=off go run ./cmd/fluxplane-plugin install local --source dev --command /bin/echo --arg plugin >/dev/null && HOME=$tmp GOWORK=off go run ./cmd/fluxplane-plugin run local ok
# returns stdout: "plugin ok\n"
cd ../fluxplane-plugins && tmp=$(mktemp -d); HOME=$tmp GOWORK=off go run ./cmd/fluxplane-plugin install gitlab && HOME=$tmp GOWORK=off go run ./cmd/fluxplane-plugin run gitlab
# installs from local fluxplane-plugins marketplace, runs the cached binary, and returns a real plugin manifest handshake
cd ../fluxplane-plugins && tmp=$(mktemp -d); HOME=$tmp GOWORK=off go run ./cmd/fluxplane-plugin install system && HOME=$tmp GOWORK=off go run ./cmd/fluxplane-plugin operation invoke system system.info --input '{"categories":["os"]}'
# invokes the system plugin through framed stdio and CLI host provider support
cd ../fluxplane-plugins && tmp=$(mktemp -d); HOME=$tmp GOWORK=off go run ./cmd/fluxplane-plugin install gitlab >/tmp/fp-install-gitlab.json && test -x "$tmp/.fluxplane/plugins/bin/fluxplane-plugin-gitlab" && HOME=$tmp GOWORK=off go run ./cmd/fluxplane-plugin remove gitlab >/tmp/fp-remove-gitlab.json && test ! -e "$tmp/.fluxplane/plugins/bin/fluxplane-plugin-gitlab"
# verifies marketplace install cache creation and owned-artifact cleanup
cd ../fluxplane-plugins && tmp=$(mktemp -d); HOME=$tmp GOWORK=off go run ./cmd/fluxplane-plugin install system && HOME=$tmp GOWORK=off go run ./cmd/fluxplane-plugin auth methods system && HOME=$tmp GOWORK=off go run ./cmd/fluxplane-plugin operation list system && HOME=$tmp GOWORK=off go run ./cmd/fluxplane-plugin datasource list system
cd ../fluxplane-plugins && tmp=$(mktemp -d); HOME=$tmp GOWORK=off go run ./cmd/fluxplane-plugin install gitlab && HOME=$tmp GOWORK=off go run ./cmd/fluxplane-plugin search git && HOME=$tmp GOWORK=off go run ./cmd/fluxplane-plugin list && HOME=$tmp GOWORK=off go run ./cmd/fluxplane-plugin status gitlab && HOME=$tmp GOWORK=off go run ./cmd/fluxplane-plugin manifest gitlab && HOME=$tmp GOWORK=off go run ./cmd/fluxplane-plugin auth connect gitlab --method personal_access_token --field access_token=smoke && HOME=$tmp GOWORK=off go run ./cmd/fluxplane-plugin auth status gitlab && HOME=$tmp GOWORK=off go run ./cmd/fluxplane-plugin auth disconnect gitlab --method personal_access_token && HOME=$tmp GOWORK=off go run ./cmd/fluxplane-plugin remove gitlab
# full CLI smoke over install/search/list/status/manifest/run/auth/remove
cd ../fluxplane-core && GOWORK=off go test ./orchestration/...
cd ../coder && GOWORK=off go test ./internal/runtime
cd ../fluxplane-apps/slack-bot && GOWORK=off go test .
cd ../fluxplane-plugins && for d in atlassian asterisk docker duckduckgo gitlab grafana kubernetes loki ollama openai prometheus slack sql system tavily vision websearch; do (cd "$d" && go test ./...) || exit 1; done
cd ../fluxplane-dex && GOWORK=off go test ./...
cd .. && old=dex-plugin; rg "$old-" fluxplane-plugins fluxplane-plugin fluxplane-core fluxplane-dex coder fluxplane-apps -S | rg -v 'CHANGELOG|plugin-system-refactor'
# no output
cd ../fluxplane-dex && GOWORK=off go list -deps ./... | rg 'github.com/fluxplane/fluxplane-core'
# no output
cd ../fluxplane-plugin && GOWORK=off go list -deps ./... | rg 'github.com/fluxplane/fluxplane-core'
# no output
cd ../fluxplane-plugin && GOWORK=off go list -deps ./... | rg 'github.com/fluxplane/fluxplane-plugins'
# no output
cd ../fluxplane-plugin && GOWORK=off go list -deps ./... | rg 'github.com/fluxplane/fluxplane-(core|dex|plugins)'
# no output
cd .. && rg -n 'github.com/fluxplane/fluxplane-dex' coder fluxplane-apps fluxplane-core fluxplane-plugin fluxplane-plugins --glob '*.go' -S
# no output
cd .. && rg -n 'internal/defaults|defaults\.MarketplaceJSON|plugins/marketplace.json' fluxplane-dex -S
# no output
cd ../fluxplane-plugins && rg -n '\bdex\b|fluxplane-dex|Created by dex|dex secret broker|coreadapter' . -g '!**/go.sum' -g '!**/CHANGELOG*' -g '!**/*_test.go' -S
# no output
```

Result: tests passed. The CLI smoke checks reject the false-success paths and
exercise real installed plugin runtimes through the marketplace-aware reusable
plugin CLI. Active plugin code/docs no longer present plugins as Dex-owned,
while test fixtures, changelog history, and the legacy protocol metadata key may
still mention Dex.

### Progress Update: Core Bridge Runtime Datasource And Context Providers

Completed in this batch:

- Extended `fluxplane-core/orchestration/pluginbridge` beyond inert manifest
  contribution metadata:
  - bridged SDK datasource declarations into Core `fluxplane-datasource`
    providers/accessors;
  - implemented provider-backed `Search` and `Get` calls through
    `fluxplane-plugin` protocol runtime invocation;
  - bridged SDK context provider declarations into Core context providers;
  - implemented Core context `Build` calls through SDK `context.build`.
- Added focused bridge tests proving:
  - Core resolves a real datasource provider from a bridged SDK plugin;
  - datasource `Search` and `Get` call the plugin runtime and normalize records
    into Core datasource records;
  - Core resolves a real context provider from a bridged SDK plugin;
  - context `Build` calls the plugin runtime and maps SDK blocks into Core
    context blocks.
- Audited existing boundary gates:
  - `fluxplane-plugin` already has a boundary test forbidding Core, Dex, and
    `fluxplane-plugins` imports;
  - Dex already has a boundary test forbidding `fluxplane-core` imports;
  - `coder` and Slack Bot already have boundary tests forbidding Dex imports.

Validation completed:

```sh
cd ../fluxplane-core && go test ./orchestration/pluginbridge
cd ../fluxplane-core && go test ./orchestration/contributions ./orchestration/app
cd ../fluxplane-datasource && go test ./...
```

Result: tests passed. `fluxplane-datasource` did not need an extension for this
slice; the current contracts were sufficient for Core bridge search/get
coverage. The Core provider-SDK dependency gate still cannot be made failing
until Core native/language/integration plugin extraction removes the existing
provider SDK imports from `fluxplane-core/go.mod`.

### Progress Update: Core Bridge Host Capabilities And Auth Test

Completed in this batch:

- Added `fluxplane-core/orchestration/pluginbridge.NewSystemHostCaller`:
  - adapts Core's `fluxplane-system` boundary to
    `fluxplane-plugin/protocol.HostCaller`;
  - supports SDK host capability calls for environment lookup, HTTP, blob
    read/write/info, and pluggable provider calls;
  - routes HTTP through Core's `systemkit.DoHTTP` / `fluxplane-system.Network`;
  - routes blob reads/writes through Core's `fluxplane-system.FileSystem`;
  - exposes `NewSystemHostCallerFactory` so products can wire the adapter into
    `WithHostCallerFactory` without duplicating closure glue.
- Extended `pluginbridge.Plugin` to implement Core
  `contributions.AuthTestProvider`:
  - maps Core auth-test requests into SDK `auth.test` runtime calls;
  - resolves declared auth fields from Core `fluxplane-secret.Resolver`
    instances when available;
  - normalizes SDK auth-test responses into Core `AuthTestReport` records.
- Clarified the bridge boundary:
  - Core has an auth-test contributor contract and now bridges SDK auth tests;
  - auth connect remains owned by the reusable `fluxplane-plugin` CLI/backend
    because Core currently has no auth-connect contributor contract.

Validation completed:

```sh
cd ../fluxplane-core && go test ./orchestration/pluginbridge
cd ../fluxplane-core && go test ./orchestration/...
```

Result: tests passed for the focused bridge package and the broader
orchestration packages.

### Progress Update: Concrete Product Wiring To Fluxplane Plugins

Completed in this batch:

- Added concrete product helpers for the canonical
  `github.com/fluxplane/fluxplane-plugins/system` module:
  - `coder/internal/runtime.SystemPlugin`;
  - `fluxplane-apps/slack-bot.SystemPlugin`.
- Updated product `BridgePlugin` helpers to accept Core bridge options, so
  products can pass host capability factories without reimplementing bridge
  glue.
- Wired the system plugin helpers with:
  - `pluginruntime.Direct(systemplugin.NewPlugin())`;
  - `pluginbridge.WithHostCallerFactory`;
  - `pluginbridge.NewSystemHostCallerFactory`;
  - `pluginbridge.WithSystemInfoProvider`.
- Added product tests proving coder and Slack Bot can import a real
  `fluxplane-plugins` module, resolve it through Core's contribution resolver, and execute
  `system.info` through the Core host-capability bridge.
- Added local `go.mod` requirements/replaces for
  `github.com/fluxplane/fluxplane-plugins/system` in coder and Slack Bot.

Validation completed:

```sh
cd ../coder && GOWORK=off go test ./internal/runtime
cd ../fluxplane-apps/slack-bot && GOWORK=off go test .
```

Result: tests passed. Products still do not import Dex; they import Core,
`fluxplane-plugin`, and an explicit concrete plugin module from
`fluxplane-plugins`.

### Progress Update: Core Architecture Gates

Completed in this batch:

- Added a Core module architecture test that forbids Core Go imports of:
  - `github.com/fluxplane/fluxplane-dex`;
  - `github.com/fluxplane/fluxplane-plugins`.
- Added a Core `go.mod` provider-SDK gate:
  - records the current direct provider SDK dependencies as a transitional
    extraction allowlist;
  - fails if new direct provider SDK dependencies are added to Core before the
    extraction plan is updated.

Validation completed:

```sh
cd ../fluxplane-core && go test .
cd ../fluxplane-core && go test ./orchestration/...
```

Result: tests passed. The provider-SDK gate intentionally does not fail on the
existing Core provider SDK debt; it prevents the debt from growing while
native/language/integration plugin extraction proceeds.

### Progress Update: Canonical Context Contract

Completed in this batch:

- Promoted portable context concepts into the leaf `fluxplane-context` module:
  - provider names/refs;
  - block kinds;
  - freshness;
  - placement;
  - render reasons;
  - provider specs with default placement and annotations;
  - blocks with provider, placement, media type, token, sensitivity, and
    freshness metadata.
- Changed `fluxplane-core/core/context` to reuse those portable leaf types via
  aliases instead of redefining parallel DTOs.
- Kept Core-only runtime concerns in `fluxplane-core/core/context`:
  - provider execution interface;
  - request observations;
  - render records/diffs;
  - context render events.
- Updated `fluxplane-plugin/pluginbinding` to handle typed leaf context names
  and block kinds while keeping its caller-facing helper API string-friendly.

Validation completed:

```sh
cd ../fluxplane-context && go test ./...
cd ../fluxplane-plugin && go test ./pluginbinding ./pluginruntime ./management/local ./cli ./manifest ./protocol
cd ../fluxplane-core && go test . ./core/... ./orchestration/...
```

Result: tests passed. Context-heavy plugin extraction can now target the leaf
contract without losing Core placement/freshness metadata.

### Progress Update: First Native Plugin Extracted From Core

Completed in this batch:

- Added `github.com/fluxplane/fluxplane-plugins/sleep`:
  - SDK manifest;
  - direct plugin constructor;
  - stdio binary at `cmd/fluxplane-plugin-sleep`;
  - operation tests for manifest quality, validation, rendered output, and
    direct-runtime cancellation.
- Extended `fluxplane-plugin/pluginbinding` and direct runtime execution so
  direct SDK plugin handlers receive the caller's `context.Context`.
- Added `sleep` to:
  - `fluxplane-plugins/go.work`;
  - root `go.work`;
  - `fluxplane-plugins/README.md`;
  - `fluxplane-plugins/marketplace.json`.
- Switched coder's foundational sleep plugin from
  `fluxplane-core/contrib/sleep` to the concrete
  `fluxplane-plugins/sleep` module through Core's plugin bridge.
- Removed the old Core-native `contrib/sleep` package.
- Updated Core's plugin bridge so SDK operation responses shaped as
  `{text,data}` become Core `operation.Rendered`, and SDK `canceled` errors map
  back to Core canceled operation results.

Validation completed:

```sh
cd ../fluxplane-plugins/sleep && go test ./...
cd ../fluxplane-plugins/sleep && GOWORK=off go test ./...
cd ../fluxplane-plugins && tmp=$(mktemp -d); HOME=$tmp GOWORK=off go run ./cmd/fluxplane-plugin install sleep && HOME=$tmp GOWORK=off go run ./cmd/fluxplane-plugin operation invoke sleep sleep --input '{"duration":0}'
cd ../coder && GOWORK=off go test ./internal/runtime
cd ../fluxplane-core && go test . ./core/... ./orchestration/...
```

Result: tests passed. Coder now imports one concrete product-native plugin from
`fluxplane-plugins` instead of Core, and the corresponding Core-native plugin
package is gone.

### Progress Update: Second Native Plugin Extracted From Core

Implemented in this uncommitted batch:

- Added `github.com/fluxplane/fluxplane-plugins/clock`:
  - SDK manifest;
  - direct plugin constructor with injectable clock/timezone options;
  - stdio binary at `cmd/fluxplane-plugin-clock`;
  - tests for manifest quality, caching, uptime/timezone rendering, and
    context block metadata.
- Added `clock` to:
  - `fluxplane-plugins/go.work`;
  - root `go.work`;
  - `fluxplane-plugins/README.md`;
  - `fluxplane-plugins/marketplace.json`.
- Switched Slack Bot's default wall-clock context provider from
  `fluxplane-core/contrib/clock` to the concrete
  `fluxplane-plugins/clock` module through Core's plugin bridge.
- Removed the old Core-native `contrib/clock` package.
- Updated package documentation to make the context split explicit:
  - `fluxplane-context` owns portable provider specs, requests, and blocks;
  - `fluxplane-core/core/context` owns Core runtime materialization, render
    records/diffs, evidence-aware requests, and events.

Validation completed:

```sh
cd ../fluxplane-plugins/clock && go test ./...
cd ../fluxplane-plugins/clock && GOWORK=off go test ./...
cd ../fluxplane-apps/slack-bot && GOWORK=off go test .
cd ../fluxplane-core && go test . ./core/... ./orchestration/...
cd ../fluxplane-context && GOWORK=off go test ./...
cd ../fluxplane-plugin && go test ./...
cd ../coder && GOWORK=off go test ./internal/runtime && go test .
cd ../fluxplane-plugins && GOWORK=off go test ./...
cd ../fluxplane-plugins && tmp=$(mktemp -d); HOME=$tmp GOWORK=off go run ./cmd/fluxplane-plugin install clock && HOME=$tmp GOWORK=off go run ./cmd/fluxplane-plugin manifest clock
```

Boundary checks completed:

```sh
rg '"github.com/fluxplane/fluxplane-dex' coder fluxplane-apps/slack-bot fluxplane-core fluxplane-plugin fluxplane-plugins -g '*.go' -g 'go.mod'
rg 'contrib/(sleep|clock)|sleep\.New\(|clock\.New\(|github.com/fluxplane/fluxplane-core/contrib/clock|github.com/fluxplane/fluxplane-core/contrib/sleep' fluxplane-core coder fluxplane-apps/slack-bot -g '*.go'
rg 'github.com/fluxplane/fluxplane-plugins' fluxplane-core -g '*.go' -g 'go.mod'
rg 'github.com/fluxplane/fluxplane-core' fluxplane-plugin fluxplane-context fluxplane-plugins/clock fluxplane-plugins/sleep -g '*.go' -g 'go.mod'
```

Result: tests passed. Boundary searches returned no code imports. Slack Bot now
uses the extracted clock plugin through Core's plugin bridge, and the reusable
plugin CLI can install and inspect the `clock` plugin from marketplace metadata.

### Progress Update: Reusable Context CLI Surface

Completed in this batch:

- Extended `fluxplane-plugin/management` with reusable context-provider
  backend contracts:
  - `ListContextProviders`;
  - `BuildContext`.
- Implemented the local backend behavior:
  - `context list` reads provider declarations from the plugin manifest;
  - `context build` invokes the existing SDK `context.build` protocol command.
- Added CLI commands:
  - `fluxplane-plugin context list PLUGIN[@VERSION]`;
  - `fluxplane-plugin context build PLUGIN[@VERSION]`;
  - aliases `ctx`/`contexts` for the command group and `run` for build.
- Added context build flags:
  - `--instance`;
  - `--query`;
  - repeated `--kind`;
  - `--limit`;
  - raw `--input` / `--input-file` JSON.
- Extended unit coverage for:
  - CLI request wiring;
  - local backend stdio runtime context listing/building.

Validation completed:

```sh
cd ../fluxplane-plugin && go test ./...
cd ../fluxplane-plugins && tmp=$(mktemp -d); HOME=$tmp GOWORK=off go run ./cmd/fluxplane-plugin install clock >/tmp/fluxplane-clock-install.json && HOME=$tmp GOWORK=off go run ./cmd/fluxplane-plugin context list clock && HOME=$tmp GOWORK=off go run ./cmd/fluxplane-plugin context build clock --query now --kind data --limit 1
```

Result: tests passed. The reusable `fluxplane-plugin` CLI can now execute
context-provider plugins without Dex.

### Progress Update: Dependency Boundary Gates

Completed in this batch:

- Extended boundary tests so they check both Go imports and `go.mod`
  dependencies:
  - `fluxplane-plugin` must not depend on Core, Dex, or `fluxplane-plugins`;
  - `fluxplane-dex` must not depend on Core;
  - `fluxplane-core` must not depend on Dex or `fluxplane-plugins`;
  - `coder` must not depend on Dex;
  - Slack Bot must not depend on Dex.
- Kept product apps allowed to depend on Core and selected
  `fluxplane-plugins` modules directly.
- Kept Core allowed to depend on `fluxplane-plugin`, but not on concrete
  plugin implementation modules.

Validation completed:

```sh
cd ../fluxplane-plugin && go test .
cd ../fluxplane-dex && go test .
cd ../coder && go test .
cd ../fluxplane-apps/slack-bot && GOWORK=off go test .
cd ../fluxplane-core && go test .
```

Result: tests passed. The dependency gates now catch accidental module-level
dependencies, not only source import statements.

### Progress Update: Product Runtime System Plugin Wiring

Completed in this batch:

- Wired the canonical `fluxplane-plugins/system` plugin into actual product
  default plugin lists when a runtime `fluxplane-system.System` boundary is
  available:
  - coder's `Startup.PluginFactory`;
  - Slack Bot's `buildPlugins`.
- Kept products responsible for the wiring:
  - products import the concrete `fluxplane-plugins/system` module;
  - products pass Core's `NewSystemHostCallerFactory`;
  - Core still does not import concrete `fluxplane-plugins` modules.
- Updated product tests so `system.info` is resolved and executed from the
  default product runtime plugin list, not only from manually injected test
  plugins.

Validation completed:

```sh
cd ../coder && GOWORK=off go test ./internal/runtime
cd ../fluxplane-apps/slack-bot && GOWORK=off go test .
```

Result: tests passed. The Core bridge host-capability adapter is now exercised
through concrete product runtime paths.

### Progress Update: Reusable Operation Batch CLI Surface

Completed in this batch:

- Added reusable operation batch support to `fluxplane-plugin/management`:
  - `OperationBatchRequest`;
  - `OperationBatchResult`;
  - `Backend.BatchOperations`.
- Implemented local backend execution through the existing SDK
  `operations.call_batch` protocol command.
- Added CLI command:
  - `fluxplane-plugin operation batch PLUGIN[@VERSION]`;
  - accepts either a JSON array of calls or `{ "calls": [...] }`;
  - supports `--instance`, `--input`, and `--input-file`.
- Extended tests for:
  - CLI request wiring;
  - stdio-backed local backend batch execution.

Validation completed:

```sh
cd ../fluxplane-plugin && go test ./...
cd ../fluxplane-plugins && tmp=$(mktemp -d); HOME=$tmp GOWORK=off go run ./cmd/fluxplane-plugin install sleep >/tmp/fluxplane-sleep-install.json && HOME=$tmp GOWORK=off go run ./cmd/fluxplane-plugin operation batch sleep --input '{"calls":[{"name":"sleep","input":{"duration":0}},{"name":"sleep","input":{"duration":0}}]}'
```

Result: tests passed. Dex's operation-batch behavior now has a reusable
`fluxplane-plugin` CLI/backend home.

### Progress Update: Reusable Endpoint Discovery CLI Surface

Completed in this batch:

- Added reusable endpoint discovery support to `fluxplane-plugin/management`:
  - `EndpointDiscoverRequest`;
  - `EndpointDiscoverResult`;
  - `Backend.DiscoverEndpoints`.
- Implemented local backend execution through the existing SDK
  `endpoints.discover` protocol command.
- Added CLI command:
  - `fluxplane-plugin endpoint discover PLUGIN[@VERSION] [PRODUCT]`;
  - supports `--instance`, `--context`, `--namespace`, `--limit`,
    `--input`, and `--input-file`.
- Extended tests for:
  - CLI request wiring;
  - stdio-backed local backend endpoint discovery.

Validation completed:

```sh
cd ../fluxplane-plugin && go test ./...
cd ../fluxplane-plugins && tmp=$(mktemp -d); HOME=$tmp GOWORK=off go run ./cmd/fluxplane-plugin install clock >/tmp/fluxplane-clock-install.json && HOME=$tmp GOWORK=off go run ./cmd/fluxplane-plugin endpoint discover clock test --limit 1
```

Result: tests passed. The reusable CLI can now invoke plugin endpoint discovery.
Dex's host-side endpoint store/import/doctor commands still need a separate
state/provider design before they can be fully drained.

### Progress Update: Plugin CLI Entrypoint And Datasource Record Commands

Completed in this batch:

- Restored the reusable standalone CLI entrypoint under
  `fluxplane-plugin/cmd/fluxplane-plugin`:
  - wires `fluxplane-plugin/cli` to `fluxplane-plugin/management/local`;
  - imports neither Dex nor Core nor `fluxplane-plugins`;
  - keeps marketplace discovery file/env/path based through the local backend.
- Extended `fluxplane-plugin/protocol` with datasource runtime commands that do
  not collide with declaration listing:
  - `datasources.records` for provider-backed record listing;
  - `datasources.batch_get` for efficient multi-record retrieval.
- Extended `fluxplane-plugin/pluginbinding`:
  - added `CapabilityBatchGet`;
  - added `RegisterDatasourceList`;
  - added `RegisterDatasourceBatchGet`;
  - routed the new protocol commands to typed datasource handlers.
- Extended `fluxplane-plugin/datasource` and `pluginbinding` aliases so plugin
  authors can use portable datasource list and batch-get DTOs through the SDK.
- Extended the reusable CLI datasource surface:
  - `fluxplane-plugin datasource records PLUGIN[@VERSION]`;
  - `fluxplane-plugin datasource batch-get PLUGIN[@VERSION]`;
  - preserved `fluxplane-plugin datasource list` as manifest/declaration
    listing.
- Extended local backend and CLI tests to cover request wiring and actual stdio
  runtime execution for datasource search, record list, and batch-get.

Validation:

```sh
cd ../fluxplane-plugin && go test ./...
cd ../fluxplane-plugin && go run ./cmd/fluxplane-plugin --help
cd ../fluxplane-plugin && go run ./cmd/fluxplane-plugin datasource --help
cd ../fluxplane-plugin && tmp=$(mktemp -d); HOME=$tmp go run ./cmd/fluxplane-plugin install sleep >/tmp/fluxplane-plugin-install-sleep.json && HOME=$tmp go run ./cmd/fluxplane-plugin operation batch sleep --input '{"calls":[{"name":"sleep","input":{"duration":0}}]}'
```

Result: tests passed. The reusable `fluxplane-plugin` module now owns a working
standalone CLI entrypoint and the datasource record-list/batch-get features
needed to continue draining Dex datasource behavior.

### Progress Update: Slack Bot Uses Standalone Slack Plugin Surface

Completed in this batch:

- Extended Core's `orchestration/pluginbridge` host caller so SDK-hosted HTTP
  calls can resolve auth material through Core/plugin-scoped
  `fluxplane-secret.Resolver`:
  - bearer token purposes map to `Authorization: Bearer ...`;
  - username/password purposes map to HTTP Basic auth;
  - header purposes map to explicit HTTP headers;
  - `NewSystemHostCallerFactory` now derives plugin name, instance, and secret
    resolver from `contributions.Context`.
- Kept `fluxplane-plugin` Core-free. The auth-capable host bridge lives in
  Core, where Core adapts plugin SDK calls into the agent runtime.
- Added `fluxplane-apps/slack-bot` direct wiring for
  `github.com/fluxplane/fluxplane-plugins/slack`:
  - `slack_channel` remains the Core channel adapter for daemon/channel IO;
  - `slack` is now the standalone plugin-owned operation/datasource surface;
  - the app manifest declares both roles explicitly.
- Added Slack Bot coverage proving both `slack_channel` and the standalone
  `slack` plugin resolve through Core's contribution resolver and that the standalone
  Slack plugin exposes operations and datasource providers.

Validation:

```sh
cd ../fluxplane-core && go test ./orchestration/pluginbridge
cd ../fluxplane-apps/slack-bot && GOWORK=off go test .
cd ../fluxplane-apps/slack-bot && rg -n 'github.com/fluxplane/fluxplane-dex' . -g '*.go' -g 'go.mod'
cd ../fluxplane-apps/slack-bot && GOWORK=off go list -deps . | rg 'github.com/fluxplane/fluxplane-plugins/slack|github.com/fluxplane/fluxplane-dex'
```

Result: tests passed. Slack Bot has a product-level direct dependency on the
standalone `fluxplane-plugins/slack` operation plugin without using Dex, while
Core still owns the channel-adapter runtime role.

### Progress Update: Slack Bot Uses Standalone Websearch Plugin Surface

Completed in this batch:

- Replaced Slack Bot's optional direct source import of Core's
  `plugins/integrations/web` runtime with standalone plugin modules:
  - `github.com/fluxplane/fluxplane-plugins/websearch`;
  - `github.com/fluxplane/fluxplane-plugins/duckduckgo`;
  - `github.com/fluxplane/fluxplane-plugins/tavily`.
- Added a product-local `websearch.ProviderRuntime` implementation that:
  - discovers provider metadata from the standalone provider plugin manifests;
  - invokes provider operations through the SDK pluginbinding path;
  - reuses the aggregator's existing result normalization.
- Kept Slack Bot's public web access datasource-centered:
  - app bundle now declares `websearch.results` with kind `websearch`;
  - the surface catalog points the model at
    `datasource_search(datasource="websearch.results", ...)`.
- Added Slack Bot coverage proving:
  - `websearch` is registered when web access is granted;
  - the old Core `web` plugin is not registered by Slack Bot;
  - `websearch.provider.list` and `websearch.search` resolve through
    Core's contribution resolver bridge;
  - the provider list includes the standalone DuckDuckGo and Tavily provider
    plugins.

Validation:

```sh
cd ../fluxplane-apps/slack-bot && GOWORK=off go test .
cd ../fluxplane-apps/slack-bot && rg -n 'github.com/fluxplane/fluxplane-core/plugins/integrations/web|github.com/fluxplane/fluxplane-dex' . -g '*.go' -g 'go.mod'
cd ../fluxplane-apps/slack-bot && GOWORK=off go list -deps . | rg 'github.com/fluxplane/fluxplane-plugins/(websearch|duckduckgo|tavily)|github.com/fluxplane/fluxplane-core/plugins/integrations/web|github.com/fluxplane/fluxplane-dex'
```

Result: tests passed. Slack Bot source no longer imports Core's web integration
or Dex. `go list -deps` still includes Core's web package through the broad
`fluxplane-core` module dependency graph, which will only disappear after the
Core plugin tree itself is drained or split.

### Progress Update: Typed Portable Context Build Payload

Completed in this batch:

- Extended `fluxplane-plugin/pluginbinding.ContextBuildInput` to carry the
  portable `fluxplane-context.Request` fields used by agent runtimes:
  - thread, branch, and turn ids;
  - render reason;
  - current input text and recent context;
  - scope values;
  - token budget.
- Kept the existing `query`, `kinds`, and `limit` fields for plugin CLI and
  compatibility callers.
- Updated Core's `orchestration/pluginbridge` context-provider adapter to send
  a typed SDK context-build payload instead of an ad hoc `map[string]any`.
- Updated Core's bridge to decode `pluginbinding.ContextBuildResult` directly,
  so plugin protocol context blocks remain the standalone
  `fluxplane-context`/SDK block type at the bridge boundary.
- Added regression coverage proving Core request fields survive through the SDK
  bridge into a plugin context provider.
- Updated Slack Bot's manifest/build path to import `fluxplane-context`
  directly for portable provider refs instead of reaching through Core's
  context re-export. Its executable surface catalog provider still implements
  Core's runtime context-provider interface, which remains a deliberate
  Core-runtime concern for now.
- Updated Coder's runtime test helper to import `fluxplane-context` directly for
  portable context blocks instead of Core's context re-export.

Validation:

```sh
cd ../fluxplane-plugin && go test ./pluginbinding
cd ../fluxplane-plugin && go test ./...
cd ../fluxplane-core && go test ./orchestration/pluginbridge ./runtime/context ./core/context
cd ../fluxplane-apps/slack-bot && GOWORK=off go test .
cd ../fluxplane-apps/slack-bot && rg -n 'fluxplane-core/core/context|corecontext\.' bot.go
cd ../coder && GOWORK=off go test ./internal/runtime
cd ../coder && rg -n 'fluxplane-core/core/context|corecontext\.' internal/runtime/session_test.go
```

Result: tests passed. This does not rename `fluxplane-core/core/context` yet;
it makes the portable context contract more real at the SDK/Core bridge and
narrows the remaining reason for Core's context package to runtime provider
interfaces, render records/diffs, materialization state, and Core events.

### Progress Update: Slack Bot Granted Integrations Use Standalone Plugin Modules

Completed in this batch:

- Wired Slack Bot's granted integration surfaces to standalone
  `fluxplane-plugins` modules through Core's plugin bridge:
  - `github.com/fluxplane/fluxplane-plugins/gitlab`;
  - `github.com/fluxplane/fluxplane-plugins/atlassian/jira`;
  - `github.com/fluxplane/fluxplane-plugins/atlassian/confluence`;
  - `github.com/fluxplane/fluxplane-plugins/kubernetes`;
  - `github.com/fluxplane/fluxplane-plugins/loki`;
  - `github.com/fluxplane/fluxplane-plugins/prometheus`.
- Added Slack Bot runtime bridge constructors for those plugins using
  Core's host-caller factory, so HTTP/env/secret/endpoint-capability calls
  stay host mediated.
- Updated Slack Bot manifest building to declare granted standalone plugin
  refs instead of only mentioning the surfaces in the prompt catalog.
- Updated the main agent datasource grants to use the real standalone
  datasource names, for example `gitlab.merge_requests`, `jira.issues`,
  `confluence.pages`, `kubernetes.inventory`, `loki.log_entries`, and
  `prometheus.query_results`.
- Updated the surface catalog hints to use the actual datasource names and
  removed stale Dex wording from the Slack catalog test.
- Removed stale active Slack Bot config/example comments that described
  optional plugin surfaces as Dex marketplace plugins.
- Added regression coverage proving each granted standalone integration plugin
  is registered, resolves through Core's contribution resolver, contributes an expected
  operation, contributes an expected datasource declaration, and exposes
  datasource providers.

Validation:

```sh
cd ../fluxplane-apps/slack-bot && GOWORK=off go mod tidy
cd ../fluxplane-apps/slack-bot && GOWORK=off go test .
cd ../fluxplane-apps/slack-bot && rg -n 'github.com/fluxplane/fluxplane-core/plugins/(integrations/(gitlab|jira|confluence|kubernetes|loki|web|docker|mysql)|native/(clock|sleep))|github.com/fluxplane/fluxplane-dex' . -g '*.go' -g 'go.mod'
cd ../fluxplane-apps/slack-bot && GOWORK=off go list -deps . | rg 'github.com/fluxplane/fluxplane-plugins/(atlassian|gitlab|kubernetes|loki|prometheus)|github.com/fluxplane/fluxplane-dex'
cd ../fluxplane-apps/slack-bot && rg -n 'github.com/fluxplane/fluxplane-core/plugins/integrations' . -g '*.go'
cd ../fluxplane-apps/slack-bot && rg -n '\bDex\b|fluxplane-dex|\bdex\b' . -S
```

Result: tests passed. Slack Bot source no longer imports Core's GitLab, Jira,
Confluence, Kubernetes, Loki, or Web integrations, and it does not import Dex.
The remaining Core integration import in Slack Bot is the `slack` channel
adapter; standalone Slack operations/datasources are already provided by
`fluxplane-plugins/slack`. The remaining Dex text in Slack Bot is limited to
boundary tests and historical changelog entries.

### Progress Update: Product And Core Architecture Gates Tightened

Completed in this batch:

- Added a Slack Bot import boundary gate that prevents regressions to migrated
  Core plugin imports:
  - no direct Core GitLab, Jira, Confluence, Kubernetes, Loki, Web, Docker, or
    MySQL integration imports;
  - no direct Core native clock/sleep imports;
  - current Core integration imports are limited to the Slack channel adapter.
- Added a Core provider-SDK import placement gate:
  - provider SDK imports are allowed only in the explicit transitional Core
    plugin tree or pinned adapter paths that still own provider integration
    work today;
  - this keeps provider SDK usage from spreading while extraction continues.
- Confirmed that standalone OpenAPI is not a mechanical package move:
  - Core OpenAPI currently generates Core `operation.Spec`,
    `resource.ContributionBundle`, Core datasource/data-source specs, and
    runtime operations from workspace/config state;
  - a final standalone OpenAPI plugin should be an SDK plugin that uses
    `fluxplane-plugin` host capabilities for HTTP/blob/env/secret access and
    exposes generated operations/datasources through a bridge-aware manifest
    lifecycle.

Validation:

```sh
cd ../fluxplane-apps/slack-bot && GOWORK=off go test .
cd ../fluxplane-core && go test .
```

Result: tests passed. The new gates protect the product/Core boundaries already
migrated while documenting the remaining OpenAPI design issue instead of hiding
it behind a transitional copy.

### Progress Update: Reusable Endpoint Test And Doctor CLI Surface

Completed in this batch:

- Added reusable endpoint health probing to `fluxplane-plugin/cli`:
  - `fluxplane-plugin endpoint test ID`;
  - `fluxplane-plugin endpoint doctor [PRODUCT]`.
- The shared CLI now probes stored endpoints and persists health through the
  backend-neutral `management.EndpointStore.SaveEndpointHealth` contract.
- Generic endpoints use a TCP connect probe with scheme default ports for HTTP,
  HTTPS, MySQL/MariaDB, and PostgreSQL-style URLs.
- Kubernetes and SQL endpoints are routed through backend operation invocation
  instead of Dex runtime internals:
  - Kubernetes uses `kubernetes.cluster.test`;
  - SQL uses `sql.query` with `select 1 as ok`.
- Probe output redacts URL password material before rendering or storing
  details.
- Added CLI coverage proving:
  - `endpoint doctor` performs a real TCP probe against a local listener and
    persists an OK `tcp_connect` health result;
  - `endpoint test` for Kubernetes endpoints invokes the plugin operation
    through the backend and stores the resulting health.

Validation completed:

```sh
cd ../fluxplane-plugin && go test ./cli
cd ../fluxplane-plugin && go test ./management ./management/local
cd ../fluxplane-plugin && go test ./...
cd ../fluxplane-plugin && rg "github.com/fluxplane/fluxplane-(core|dex)" . -g'*.go' -g'go.mod'
```

Result: all `fluxplane-plugin` tests passed. The import scan found no Core or
Dex dependencies in `fluxplane-plugin`.

### Progress Update: Slack Bot Context Imports Split By Responsibility

Completed in this batch:

- Updated Slack Bot's surface catalog provider to import portable context DTOs
  and constants from `fluxplane-context`:
  - provider name;
  - provider spec;
  - block kind;
  - placement;
  - freshness;
  - rendered blocks.
- Kept the remaining `fluxplane-core/core/context` import only where Slack Bot
  must satisfy Core's runtime provider interface:
  - `corecontext.Provider`;
  - `corecontext.Request`.
- Updated Slack Bot tests to compare portable placement/freshness values through
  `fluxplane-context` while still passing Core runtime requests to Core-hosted
  providers.

Validation completed:

```sh
cd ../fluxplane-apps/slack-bot && GOWORK=off go test .
cd .. && rg -n "corecontext\\.|fluxplane-core/core/context|fpcontext\\." fluxplane-apps/slack-bot/surface_catalog.go fluxplane-apps/slack-bot/surface_catalog_test.go fluxplane-apps/slack-bot/pluginbridge_test.go
```

Result: Slack Bot tests passed. Remaining Core context references in the touched
files are runtime interface/request references, not portable DTO/spec/block
references.

### Progress Update: Reusable Endpoint Import CLI Surface

Completed in this batch:

- Added reusable endpoint candidate import to `fluxplane-plugin/cli`:

  ```sh
  fluxplane-plugin endpoint import [JSON|-]
  ```

- The import command can read from:
  - a JSON argument;
  - stdin with `-`;
  - `--from <file>`.
- It accepts the useful non-interactive Dex import controls:
  - `--candidate <index>`;
  - `--id <override>`;
  - `--source <override>`;
  - `--label key=value`;
  - `--annotation key=value`;
  - `--dry-run`.
- The parser accepts both Dex-style discovery JSON and the reusable CLI's own
  `EndpointDiscoverResult` wrapper, so this pipeline works without hand-editing
  JSON:

  ```sh
  fluxplane-plugin endpoint discover kubernetes loki | fluxplane-plugin endpoint import -
  ```

- Imported candidates are converted through `fluxplane-endpoint.Candidate` into
  stored `EndpointRef` values and persisted through
  `management.EndpointStore.SaveEndpoint`.
- Added CLI coverage proving:
  - selected candidates preserve URL, product, protocol, source, credential ref,
    and merged labels/annotations;
  - reusable discover-result wrapper output can be imported directly.

Validation completed:

```sh
cd ../fluxplane-plugin && go test ./cli
cd ../fluxplane-plugin && go test ./...
cd ../fluxplane-plugin && rg "github.com/fluxplane/fluxplane-(core|dex)" . -g'*.go' -g'go.mod'
cd ../fluxplane-plugin && tmp=$(mktemp -d); HOME=$tmp go run ./cmd/fluxplane-plugin endpoint import '{"id":"loki-dev","url":"http://loki.example.test:3100","product":"loki","protocol":"http"}' >/tmp/fluxplane-endpoint-import.json && HOME=$tmp go run ./cmd/fluxplane-plugin endpoint get loki-dev
```

Result: all `fluxplane-plugin` tests passed, the boundary import scan found no
Core/Dex dependencies, and the standalone CLI smoke test imported and read back
a stored endpoint from the local state store.

### Progress Update: Portable Workspace DTOs Extracted

Completed in this batch:

- Added a new leaf module:

  ```text
  github.com/fluxplane/fluxplane-workspace
  ```

- Moved inert workspace identity and selection DTOs into the leaf module:
  - workspace IDs;
  - root/origin/alias shapes;
  - durability;
  - selected workspace;
  - validation helpers.
- Updated `fluxplane-core/core/workspace` to re-export the leaf types and
  constants for existing Core APIs.
- Kept runtime path resolution, filesystem IO, declaration loading, and
  workspace manager behavior in Core runtime packages.
- Added the leaf module to the root Go workspace and Core's local replace list.

Validation completed:

```sh
cd ../fluxplane-workspace && go test ./...
cd ../fluxplane-core && go test ./core/workspace ./runtime/workspace
cd ../fluxplane-core && go test ./core/...
cd .. && rg -n "github.com/fluxplane/fluxplane-workspace|core/workspace" fluxplane-workspace fluxplane-core -g'*.go' -g'go.mod' -g'go.work'
cd .. && rg -n "fluxplane-core/core/workspace|coreworkspace\\.|workspace\\." coder fluxplane-apps/slack-bot -g'*.go'
```

Result: focused leaf/Core tests and all Core domain package tests passed.
Product code does not import Core workspace DTOs directly; remaining product
workspace references are runtime workspace wiring.

### Progress Update: Core Operation Contract Aliased To Leaf Module

Completed in this batch:

- Removed Core's duplicate implementation of the portable operation contract.
- Updated `fluxplane-core/core/operation` to re-export
  `github.com/fluxplane/fluxplane-operation` for existing Core APIs:
  - operation refs;
  - operation specs;
  - sets;
  - semantics/effects/risk/idempotency;
  - executable operation interface;
  - operation context;
  - results/rendered outputs;
  - lifecycle events;
  - intent models;
  - registry helpers.
- Kept Core tests in place against the Core import path so existing consumers
  continue to exercise compatibility.
- Adjusted one package-local marker test to assert the public marker-interface
  contract instead of calling the leaf package's unexported marker method
  through Core's re-export.

Validation completed:

```sh
cd ../fluxplane-core && go test ./core/operation
cd ../fluxplane-core && go test ./core/...
cd ../fluxplane-core && go test ./runtime/...
cd ../fluxplane-core && go test ./orchestration/pluginbridge ./orchestration/contributions
cd ../fluxplane-core && go test ./orchestration/...
```

Result: Core operation compatibility tests, all Core domain package tests,
runtime package tests, and orchestration package tests passed with the leaf
operation contract.

### Progress Update: Plugin Ownership Map Corrected

Completed in this batch:

- Abandoned the interrupted shared `fluxplane-language` extraction. Go and
  Markdown language surfaces are currently coding-product behavior and should
  move with `coder`, not into a shared platform module.
- Clarified that coding bundle/code/native workspace tools are coder-owned
  while they model coding workspace behavior.
- Clarified that Core owns runtime/channel surfaces, including the Slack channel
  adapter used by `fluxplane-apps/slack-bot`.
- Clarified that `fluxplane-plugins/slack` remains the standalone Slack
  operation/datasource plugin surface, separate from Core channel transport.
- Clarified that reusable external integrations such as Git, GitLab, Jira,
  Confluence, Kubernetes, Loki, Docker, SQL, AWS, OpenAI, OpenAPI, and websearch
  belong in `fluxplane-plugins`.
- Added local product-module `fluxplane-workspace` requirements/replaces for
  `coder` and `fluxplane-apps/slack-bot`, so their `GOWORK=off` validation can
  resolve Core's new workspace leaf dependency.

Validation completed:

```sh
find ../fluxplane-language -maxdepth 3 -type f
cd ../fluxplane-core && go test ./core/operation ./core/workspace
cd ../fluxplane-plugin && go test ./cli
cd ../fluxplane-apps/slack-bot && GOWORK=off go test .
cd ../coder && GOWORK=off go test ./internal/runtime
rg -n 'fluxplane-plugins/languages|Slack plugin remains outside core|dexcore|move coding/native/language plugins out of Core into `fluxplane-plugins`|extracting `fluxplane-language`|Slack plugin directly because Slack' ../fluxplane-core/.agents/plans/plugin-system-refactor.md
```

Result: no `fluxplane-language` files remain from the abandoned extraction,
focused Core/plugin/product tests passed, and the stale wording scan found no
matches.

### Progress Update: Coder Owns Its Coding Plugin Implementations

Completed in this batch:

- Moved coder's coding-product plugin implementations into
  `coder/internal/plugins`:
  - coding bundle;
  - browser/code/filesystem/project/shell workspace tools;
  - Go and Markdown language plugins;
  - Git and web operations needed by the coder coding bundle.
- Updated coder runtime and product activation/feature wiring to import those
  coder-owned packages instead of Core plugin packages.
- Added a coder architecture gate that forbids reintroducing direct imports of
  Core's old coding bundle, language plugins, and coder-owned native workspace
  plugin packages.
- Preserved Core runtime/domain plugin usage for task, memory, image, workspace,
  identity, goal, loop, discovery, skills, and related runtime surfaces.

Validation completed:

```sh
cd ../coder && GOWORK=off go test . ./internal/product ./internal/runtime ./internal/plugins/...
cd ../coder && GOWORK=off go test ./...
cd ../coder && rg -n "github.com/fluxplane/fluxplane-core/plugins/(bundles/coding|languages/(golang|markdown)|native/(browser|code|filesystem|project|shell)|integrations/)" . -g'*.go'
cd ../coder && GOWORK=off go list -deps ./internal/product ./internal/plugins/coding | rg 'github.com/fluxplane/fluxplane-core/plugins/(bundles/coding|languages/(golang|markdown)|native/(browser|code|filesystem|project|shell)|integrations/(git|web))'
```

Result: the full coder test suite passed; the source import scan found only
boundary-test literals; product and coding bundle dependency scans resolve the
coder-owned plugin paths, not the old Core plugin paths.

Remaining Core-side issue:

- `coder/internal/runtime` still depends on `fluxplane-core/apps/launch`.
  That package no longer wires Core's old coding defaults, but it remains a
  broad Core runtime entrypoint and should keep shrinking toward runtime-only
  concerns.

### Progress Update: Core Launch Defaults No Longer Wire Coding Product Plugins

Completed in this batch:

- Removed Core launch's default construction of the old Core coding bundle.
- Removed the launch-only browser/human helper construction that existed only
  to feed that coding bundle.
- Updated launch tests to validate product plugin injection with local test
  plugins instead of importing Core's old coding/web plugin packages.
- Verified `coder/internal/runtime` no longer sees Core's old coding bundle,
  language plugins, coder-owned native workspace plugins, or Core git/web
  integration packages through `go list -deps`.

Validation completed:

```sh
cd ../fluxplane-core && go test ./apps/launch
cd ../fluxplane-core && go test .
cd ../coder && GOWORK=off go test ./...
cd ../coder && GOWORK=off go list -deps ./internal/runtime > /tmp/coder-runtime-deps.txt
rg 'github.com/fluxplane/fluxplane-core/plugins/(bundles/coding|languages/(golang|markdown)|native/(browser|code|filesystem|project|shell)|integrations/(git|web))' /tmp/coder-runtime-deps.txt
rg -n 'plugins/(bundles/coding|languages/(golang|markdown)|native/(browser|code|filesystem|project|shell)|integrations/(git|web))|coding\.New|browserHeadless' ../fluxplane-core/apps/launch -g'*.go'
```

Result: Core launch tests, Core architecture tests, and the full coder suite
passed. Both scans returned no matches.

### Progress Update: Old Core Coding Product Packages Removed

Completed in this batch:

- Deleted Core's old product-level coding package files:
  - `plugins/bundles/coding`;
  - `plugins/languages/golang`;
  - `plugins/languages/markdown`;
  - `contrib/browser`;
  - `contrib/code`;
  - `contrib/filesystem`;
  - `contrib/project`;
  - `contrib/shell`.
- Kept Core runtime/domain/native packages such as workspace, datasource,
  discovery, goal, human, identity, image, loop, memory, sessionhistory, skills,
  task, text, and usage.
- Removed the remaining Core support-test dependency on product-owned project
  and Go plugin event contributions.

Validation completed:

```sh
cd ../coder && GOWORK=off go test ./...
cd ../fluxplane-core && go test ./apps/launch ./plugins/support/eventcatalog ./contrib/... ./core/... ./runtime/... ./orchestration/pluginbridge ./orchestration/contributions
cd ../fluxplane-core && go list ./plugins/bundles/coding ./plugins/languages/golang ./plugins/languages/markdown ./contrib/browser ./contrib/code ./contrib/filesystem ./contrib/project ./contrib/shell
rg -n 'github.com/fluxplane/fluxplane-core/plugins/(bundles/coding|languages/(golang|markdown)|native/(browser|code|filesystem|project|shell))' ../fluxplane-core ../coder ../fluxplane-apps/slack-bot -g'*.go'
```

Result: focused Core/runtime/contribution resolver tests and the full coder suite passed.
The removed Core packages report no Go files. The import scan found only the
coder architecture-test forbidden-prefix literals.

Note: `cd ../fluxplane-core && go test ./...` was also attempted. It compiled
past the removed package set but failed in unrelated packages that create local
HTTP/Unix socket listeners, which this sandbox rejects with `operation not
permitted`.

### Progress Update: SDK Host Process Capability Added

Completed in this batch:

- Added a bounded host process-run capability to `fluxplane-plugin`:
  - protocol command `host.capability.process.run`;
  - SDK host `ProcessRunRequest` / `ProcessRunResponse`;
  - `host.Client.ProcessRun`;
  - `pluginbinding` aliases for plugin authors.
- Added Core bridge support for the capability through
  `fluxplane-system.ProcessManager.Run`.
- Added SDK host-client coverage and Core pluginbridge host-caller coverage.

Why this matters:

- Reusable process-backed plugins such as Git can now move to
  `fluxplane-plugins` without importing Core.
- Product-owned process-backed plugins can also use the SDK host capability when
  they need a stdio/runtime shape rather than a direct Core contribution resolver
  implementation.

Validation completed:

```sh
cd ../fluxplane-plugin && go test ./host ./pluginbinding ./protocol ./...
cd ../fluxplane-core && go test ./orchestration/pluginbridge ./apps/launch ./plugins/support/eventcatalog
cd ../coder && GOWORK=off go test ./...
```

Result: all focused SDK, Core bridge/launch/catalog, and coder tests passed.

### Progress Update: Reusable Git Plugin Extracted From Core

Completed in this batch:

- Added `github.com/fluxplane/fluxplane-plugins/git` as a standalone SDK plugin
  module with:
  - operations `git_status`, `git_diff`, `git_add`, `git_commit`, `git_tag`,
    and `git_push`;
  - a stdio command entrypoint at `cmd/fluxplane-plugin-git`;
  - marketplace and workspace registration.
- Implemented Git execution through the `fluxplane-plugin` host process-run
  capability instead of direct Core or `fluxplane-system` imports.
- Preserved the old Core Git plugin's input validation and command construction
  behavior, including:
  - bounded diff output;
  - commit hook suppression;
  - explicit staging rules;
  - safe tag/refspec validation;
  - raw force-push rejection in favor of `force_with_lease`.
- Deleted Core's old `plugins/integrations/git` package files.

Validation completed:

```sh
cd ../fluxplane-plugins/git && go test ./...
rg "plugins/integrations/git|fluxplane-core/plugins/integrations/git" ../fluxplane-core ../coder ../fluxplane-apps ../fluxplane-plugins -g'*.go'
```

Result: the new Git plugin module and command package tests passed. The import
scan found no Git package consumers; only unrelated GitLab references remain.

### Progress Update: Legacy Core Integration Packages Removed

Completed in this batch:

- Removed Core integration packages that now have standalone
  `fluxplane-plugins` replacements:
  - Confluence and Jira (`fluxplane-plugins/atlassian`);
  - Docker;
  - GitLab;
  - Kubernetes;
  - Loki;
  - MySQL (`fluxplane-plugins/sql`);
  - OpenAI;
  - web search/request behavior (`fluxplane-plugins/websearch`,
    `duckduckgo`, and `tavily`).
- Simplified the Core `examples/go/slack-bot-coded` example so it no longer
  imports those legacy Core integration packages. It now keeps only Core-owned
  Slack channel/plugin wiring and native runtime plugins.
- Ran `go mod tidy` in Core and removed the now-unused direct GitLab SDK module
  requirement.
- Tightened Core's provider-SDK direct dependency allowlist by removing the
  GitLab SDK exception.
- Left Core-owned integration surfaces in place:
  - Slack channel/runtime adapter;
  - AWS environment observer/assertion deriver.

Validation completed:

```sh
cd ../fluxplane-core && go test . ./examples/go/slack-bot-coded ./apps/launch ./plugins/support/eventcatalog ./orchestration/pluginbridge ./orchestration/contributions ./plugins/integrations/aws ./adapters/channels/slack
rg 'github.com/fluxplane/fluxplane-core/plugins/integrations/(confluence|docker|git|gitlab|jira|kubernetes|loki|mysql|openai|web)' ../fluxplane-core ../coder ../fluxplane-apps ../fluxplane-plugin ../fluxplane-plugins -g'*.go' -g'go.mod'
find ../fluxplane-core/plugins/integrations -maxdepth 2 -type f
```

Result: focused Core tests passed, no product or Core Go imports remain for the
removed legacy integration packages, and Core's integration tree then contained
only AWS, OpenAPI, and Slack Go files. The later OpenAPI extraction below
removes OpenAPI from that remaining set.

### Progress Update: Boundary Validation After Integration Cleanup

Validation completed:

```sh
cd ../fluxplane-dex && GOWORK=off go test ./...
cd ../fluxplane-plugin && go test ./host ./pluginbinding ./protocol ./cli ./management/... ./pluginruntime
cd ../fluxplane-plugins && go test .
cd ../fluxplane-plugins/git && go test ./...
cd ../fluxplane-apps/slack-bot && GOWORK=off go test .
cd ../coder && GOWORK=off go test ./...
rg 'github.com/fluxplane/fluxplane-dex' ../coder ../fluxplane-apps ../fluxplane-core ../fluxplane-plugin ../fluxplane-plugins -g'*.go' -g'go.mod'
rg 'github.com/fluxplane/fluxplane-plugins' ../fluxplane-core -g'*.go' -g'go.mod'
rg 'github.com/fluxplane/fluxplane-core' ../fluxplane-dex -g'*.go' -g'go.mod'
```

Result: Dex, plugin SDK, plugin registry/Git plugin, Slack Bot, and coder tests
passed. Boundary scans confirmed:

- Dex does not import Core.
- No checked product/Core/plugin source imports Dex.
- Core does not import `fluxplane-plugins`.

### Progress Update: OpenAPI Extracted To Standalone SDK Plugin

Completed in this batch:

- Added `github.com/fluxplane/fluxplane-plugins/openapi` as a standalone SDK
  plugin module:
  - generates SDK operation declarations and datasource specs from configured
    OpenAPI 3.x documents;
  - exposes direct construction through `NewPlugin(ctx, cfg, opts)`;
  - exposes a stdio command entrypoint at
    `cmd/fluxplane-plugin-openapi`;
  - loads local specs relative to the configured product/workspace root;
  - keeps generated docs as plugin datasource search/list/get records.
- Moved OpenAPI runtime calls onto SDK host capabilities:
  - HTTP operations call `pluginbinding.HostClient.HTTP`;
  - bearer/basic/header auth uses host-mediated HTTP auth requests;
  - query/cookie API keys resolve through a new Core bridge
    `host.secret.get` capability.
- Extended Core's `orchestration/pluginbridge` host caller with SDK
  `host.secret.get`, backed by Core's plugin-scoped
  `fluxplane-secret.Resolver`.
- Wired Slack Bot's configurable OpenAPI access through
  `fluxplane-plugins/openapi`:
  - Slack Bot still uses Core's Slack channel adapter for channel/runtime IO;
  - OpenAPI operation/datasource behavior now comes from the standalone SDK
    plugin through Core's bridge;
  - Slack Bot's boundary gate no longer allows Core OpenAPI imports.
- Removed Core's old `plugins/integrations/openapi` package.
- Removed OpenAPI from Core launch defaults and simplified the Core coded Slack
  example so it remains a Core Slack-channel/runtime example.
- Ran module tidy in Core, Slack Bot, and the new OpenAPI plugin module.

Validation completed:

```sh
cd ../fluxplane-plugins/openapi && go test ./...
cd ../fluxplane-apps/slack-bot && GOWORK=off go test .
cd ../fluxplane-core && go test . ./apps/launch ./examples/go/slack-bot-coded ./orchestration/pluginbridge ./adapters/channels/slack ./plugins/integrations/aws
rg "plugins/integrations/openapi|openapi\\." ../fluxplane-core -g'*.go'
rg "github.com/fluxplane/fluxplane-plugins" ../fluxplane-core -g'*.go'
```

Result: focused tests passed. The Core OpenAPI import scan returned no matches,
Core still has no `fluxplane-plugins` imports, and Slack Bot continues to keep
Slack channel transport in Core while importing standalone plugin modules for
operation/datasource surfaces.

### Progress Update: Core Integration Tail Audited And Dead Atlassian Helpers Removed

Superseded note: the AWS decision in this historical checkpoint was reversed by
the later "AWS Extracted To Standalone SDK Plugin" checkpoint. AWS
implementation code now belongs in `fluxplane-plugins/aws`; portable evidence
bridging is separate Core bridge work.

Completed in this batch:

- Audited the remaining Core integration tree after OpenAPI extraction:
  - `adapters/channels/slack` is the Core-owned Slack channel/runtime
    adapter and remains in Core;
  - `plugins/integrations/aws` is not an operation/datasource provider plugin
    and does not import the AWS SDK. It contributes Core evidence observers and
    assertion derivers over non-secret AWS environment configuration.
- Decided not to move AWS into `fluxplane-plugins` in this slice because the
  plugin SDK does not yet expose portable evidence observer/assertion-deriver
  contribution contracts. Moving it now would either pull Core evidence
  contracts into a plugin implementation or invent a partial evidence bridge
  before the leaf contract exists.
- Removed the stale `plugins/internal/atlassian` helper package from Core. Jira
  and Confluence auth/client helpers now live with the standalone
  `fluxplane-plugins/atlassian` module.
- Ran Core `go mod tidy`, which removed additional stale dependencies left by
  the deleted integration/product plugin packages, including old OpenAPI,
  GitLab, browser, markdown-to-ADF, and codegate entries.
- Fixed standalone Atlassian test host doubles to embed the SDK
  `pluginbinding.HostClient` for unexercised host capabilities, so the tests
  compile after the SDK process-run host capability addition.

Validation completed:

```sh
cd ../fluxplane-core && go test . ./plugins/integrations/aws ./adapters/channels/slack ./apps/launch ./orchestration/pluginbridge ./contrib/datasource
cd ../fluxplane-plugins/atlassian && go test ./...
cd ../fluxplane-apps/slack-bot && GOWORK=off go test .
rg "github.com/fluxplane/fluxplane-core/plugins/(internal/atlassian|integrations/(confluence|jira|openapi))|plugins/internal/atlassian|github.com/fluxplane/fluxplane-dex" ../fluxplane-core ../coder ../fluxplane-apps ../fluxplane-plugin ../fluxplane-plugins -g'*.go' -g'go.mod'
```

Result: focused Core, standalone Atlassian, and Slack Bot tests passed. The
boundary scan returned no matches. The only Core integration packages with Go
files are now AWS evidence observation and the Slack channel adapter.

### Progress Update: Reusable Index Build/Status CLI Surface

Completed in this batch:

- Added backend-neutral plugin index contracts to `fluxplane-plugin/management`:
  - `IndexBuildRequest`;
  - `IndexBuildResult`;
  - `IndexStatusRequest`;
  - `IndexStatusResult`;
  - `IndexStatus` / `IndexStatusEntry`;
  - `IndexManager`.
- Implemented local index persistence in `fluxplane-plugin/management/local`:
  - invokes the plugin's conventional `<plugin>.index.build` operation through
    the existing stdio protocol runtime;
  - stores opaque index snapshots under the local plugin state root in an
    `indexes/` directory;
  - records index names, record counts, metadata, update timestamps, plugin
    refs, and instances;
  - reports status for one plugin instance or all locally installed plugins.
- Added reusable CLI commands:

  ```sh
  fluxplane-plugin index build PLUGIN[@VERSION] --index NAME --entity ENTITY
  fluxplane-plugin index status [PLUGIN[@VERSION]]
  ```

- Added CLI fake-backend coverage for index build/status request wiring.
- Extended the local stdio runtime test plugin with a real index-build
  operation and added local backend coverage proving build output is persisted
  and status reflects the stored snapshot.

Validation completed:

```sh
cd ../fluxplane-plugin && go test ./management ./management/local ./cli ./pluginbinding ./protocol
cd ../fluxplane-plugin && go test ./...
cd ../fluxplane-dex && GOWORK=off go test ./...
cd ../fluxplane-apps/slack-bot && GOWORK=off go test .
cd ../coder && GOWORK=off go test ./internal/runtime
cd ../fluxplane-plugin && tmp=$(mktemp -d); HOME=$tmp go run ./cmd/fluxplane-plugin index status
```

Result: tests passed. The standalone `fluxplane-plugin` CLI now owns the
reusable `index build/status` surface that was previously Dex-only. Indexed
datasource search/get/lookup over persisted snapshots remains a follow-up
reuse target.

### Progress Update: Reusable Indexed Datasource Search/Get/Lookup

Completed in this batch:

- Added host-index datasource routing to `fluxplane-plugin/management/local`.
  The local backend now checks persisted index snapshots before invoking a
  plugin runtime for:
  - `datasources.search`;
  - `datasources.lookup`;
  - `datasources.get`.
- Kept the routing narrow and conservative:
  - only search/lookup/get are served from local snapshots;
  - datasource-filtered calls only use a matching snapshot name;
  - entity-filtered calls only use snapshots that contain that entity;
  - all unhandled calls still fall through to the plugin runtime.
- Added generic snapshot record normalization/enrichment in
  `fluxplane-plugin`:
  - stable `host_index` origin metadata;
  - record id/entity/title/url/link extraction;
  - ranked search results;
  - lookup results using the shared `fluxplane-datasource` scoring helpers.
- Extended local backend coverage to prove an installed stdio plugin can build
  an index and then satisfy indexed search, lookup, and get from the persisted
  snapshot.

Validation completed:

```sh
cd ../fluxplane-plugin && go test ./management/local
cd ../fluxplane-plugin && go test ./...
```

Result: tests passed. The reusable plugin backend now owns Dex's core indexed
datasource read path over local snapshots.

### Progress Update: Reusable Auth Auto

Completed in this batch:

- Added backend-neutral auth auto contracts to `fluxplane-plugin/management`:
  - `AuthAutoRequest`;
  - `AuthAutoResult`;
  - `AuthManager.AuthAuto`.
- Implemented local auth auto in `fluxplane-plugin/management/local`:
  - reads auth fields from plugin-declared environment variables;
  - inherits method-level env declarations when a field does not define its own;
  - deduplicates auth fields by name;
  - records only manifest-declared field names in output, not secret values;
  - stores imported values through the existing local auth state path.
- Added reusable CLI support mirroring the Dex behavior:

  ```sh
  fluxplane-plugin auth connect auto [PLUGIN[@VERSION]]
  ```

  When no plugin is provided, the command attempts auth auto for all locally
  known plugins.
- Added CLI fake-backend coverage and local stdio runtime coverage proving env
  import saves the declared auth field without requiring manual `--field`
  input.

Validation completed:

```sh
cd ../fluxplane-plugin && go test ./cli ./management/local ./management
cd ../fluxplane-plugin && go test ./...
cd ../fluxplane-dex && GOWORK=off go test ./...
cd ../fluxplane-apps/slack-bot && GOWORK=off go test .
cd ../coder && GOWORK=off go test ./internal/runtime
```

Result: tests passed. The reusable plugin CLI/backend now owns the `auth auto`
surface that was previously Dex-only.

### Progress Update: Dex Drain Decisions For Grants And Endpoint Prompts

Completed in this batch:

- Decided that secret grant inspection stays host/runtime-internal for now:
  - grant DTOs already live in `fluxplane-secret`;
  - grant creation/validation is tied to short-lived runtime invocation state;
  - grants carry capability/purpose authorization and no secret material;
  - exposing grant inspection in the reusable end-user CLI would make an
    internal runtime authorization artifact part of the public plugin
    management surface before there is a real product use case.
- Decided not to preserve Dex's interactive endpoint selection prompt as a
  reusable CLI feature:
  - `fluxplane-plugin` already owns non-interactive `endpoint discover`,
    `endpoint import`, `endpoint save`, `endpoint test`, and `endpoint doctor`;
  - the final reusable surface should remain automation-friendly and scriptable;
  - products may add their own interactive UI on top of discovery/import if
    their users need it.

Result: the remaining Dex drain work is now focused on deleting or shrinking Dex
after one final feature comparison, not on adding new reusable grant or prompt
commands.

### Progress Update: Plugin Implementation Boundary Gate

Completed in this batch:

- Added a `fluxplane-plugins` architecture test that fails if any standalone
  plugin implementation imports or depends on:
  - `github.com/fluxplane/fluxplane-core`;
  - `github.com/fluxplane/fluxplane-dex`.
- The gate checks both Go imports and every `go.mod` in the plugin repo,
  including nested plugin modules.

Validation completed:

```sh
cd ../fluxplane-plugins && go test ./...
cd ../fluxplane-plugins && for mod in atlassian clock git openapi sleep; do (cd "$mod" && go test ./...); done
rg "github.com/fluxplane/fluxplane-dex" ../coder ../fluxplane-apps ../fluxplane-core ../fluxplane-plugin ../fluxplane-plugins -g'*.go' -g'go.mod'
rg "github.com/fluxplane/fluxplane-core" ../fluxplane-plugin ../fluxplane-plugins ../fluxplane-dex -g'*.go' -g'go.mod'
```

Result: tests passed. The import scans returned no matches.

### Progress Update: Final Dex Feature Comparison And Fanout Commands

Completed in this batch:

- Compared Dex's remaining CLI feature surface against the reusable
  `fluxplane-plugin` CLI.
- Confirmed reusable coverage for the plugin primitives Dex previously owned:
  - install/list/status/update/enable/disable/remove/search;
  - manifest inspection;
  - auth methods/connect/auto/test/status/disconnect;
  - operation list/invoke/batch;
  - datasource list/search/get/records/batch-get/lookup;
  - context provider list/build;
  - index build/status plus local indexed datasource search/get/lookup;
  - endpoint discover/import/save/list/get/test/doctor/remove;
  - direct stdio runtime execution.
- Added reusable fanout convenience commands for the useful Dex shortcuts that
  were still missing:

  ```sh
  fluxplane-plugin datasource search-all QUERY
  fluxplane-plugin lookup TEXT
  fluxplane-plugin datasource lookup-all TEXT
  fluxplane-plugin context build-all QUERY
  ```

- Left Dex-generated integration command shortcuts out of the reusable CLI.
  They are product/convenience wrappers over plugin manifests and operations,
  not a separate end-state plugin-management primitive.

Validation completed:

```sh
cd ../fluxplane-plugin && go test ./cli
cd ../fluxplane-plugin && go test ./...
cd ../fluxplane-plugin && go run ./cmd/fluxplane-plugin lookup --help
cd ../fluxplane-plugin && go run ./cmd/fluxplane-plugin datasource --help
cd ../fluxplane-plugin && go run ./cmd/fluxplane-plugin context --help
```

Result: the reusable plugin CLI now has a scriptable equivalent for the
remaining useful Dex search/lookup/context fanout behavior.

### Progress Update: Dex Marked As Legacy Compatibility CLI

Completed in this batch:

- Updated `fluxplane-dex/README.md` to lead with the end-state boundary:
  - Dex is legacy compatibility only;
  - new plugin management/auth/manifest/operation/datasource/context/endpoint/
    index/runtime work belongs in `fluxplane-plugin`;
  - concrete plugin implementations belong in `fluxplane-plugins`;
  - product applications must not import Dex.
- The README now points new users at `fluxplane-plugin` commands and keeps the
  old Dex command reference only as legacy documentation during the archive
  window.

Result: Dex is now archived in-place from the user-facing documentation
perspective. The remaining Dex action is repository administration: remove it,
archive it upstream, or leave it as a legacy standalone repo with no product
dependencies.

### Completion Audit: Current Next Large Steps

Status for the five tracked next steps:

1. Product/plugin wiring is complete for this migration slice:
   - concrete provider plugins live in `fluxplane-plugins`;
   - Slack Bot wires standalone operation/datasource plugins through Core's
     plugin bridge;
   - Slack channel/runtime IO remains Core-owned;
   - AWS environment inspection is now handled by `fluxplane-plugins/aws`;
   - portable evidence observer/assertion-deriver bridging remains future Core
     bridge work, not a reason for AWS implementation code to live in Core;
   - products remain free of Dex imports.
2. Dex feature drain is complete for reusable end-user plugin behavior:
   - plugin management/auth/manifest/operation/datasource/context/endpoint/
     index/runtime surfaces live in `fluxplane-plugin`;
   - Dex is documented as legacy compatibility only;
   - final deletion or upstream repository archival is repository
     administration, not an architecture dependency.
3. Core extraction is complete for current boundaries:
   - Core integration packages are limited to Slack channel/runtime IO;
   - runtime/channel/session/domain plugins stay in Core because no leaf
     runtime contract has been intentionally introduced for them;
   - coding/native/language product plugins are owned by `coder`.
4. Architecture gates are in place and tightened:
   - `fluxplane-plugin` rejects Core/Dex/plugin-registry imports;
   - Dex rejects Core imports;
   - products reject Dex imports and migrated Core plugin imports;
   - `fluxplane-plugins` rejects Core/Dex imports in imports and nested
     `go.mod` files;
   - Core rejects Dex and `fluxplane-plugins` dependencies and only allows
     provider SDK imports in approved runtime infrastructure paths.
5. Product-facing contract split is complete for current needs:
   - `fluxplane-context` owns portable context specs/blocks/request fields;
   - `fluxplane-workspace` owns portable workspace DTOs;
   - `fluxplane-operation` owns portable operation contracts and events;
   - Core re-exports aliases for compatibility while retaining Core-only
     runtime request/provider/render/diff/evidence types;
   - no `fluxplane-language` extraction is needed without a second non-coder
     consumer.

Verification commands used during the audit:

```sh
rg "github.com/fluxplane/fluxplane-dex" ../coder ../fluxplane-apps ../fluxplane-core ../fluxplane-plugin ../fluxplane-plugins -g'*.go' -g'go.mod'
rg "github.com/fluxplane/fluxplane-core|github.com/fluxplane/fluxplane-dex" ../fluxplane-plugin ../fluxplane-plugins -g'*.go' -g'go.mod'
rg "github.com/fluxplane/fluxplane-plugins|github.com/fluxplane/fluxplane-dex" . -g'*.go' -g'go.mod'
find ../fluxplane-plugins -mindepth 2 -maxdepth 2 -name go.mod -printf '%h\n'
find plugins -maxdepth 3 -type f -name '*.go'
```

Result: all five current next steps are implemented for this architecture
checkpoint. Future work should be tracked as new next steps, not as carry-over
from this migration batch.

### Progress Update: Core Installed Plugin Loader

Completed in this batch:

- Added Core-side installed plugin loading in
  `orchestration/pluginbridge`:
  - reads enabled installed plugins from a `fluxplane-plugin/management.Store`;
  - defaults to the local `~/.fluxplane/plugins/state.json` backend;
  - supports product-supplied state stores and runtime factories;
  - adapts installed stdio runtimes through the existing Core plugin bridge;
  - emits explicit `default` plugin refs so Core instance identity matches
    `fluxplane-plugin` state.
- Added launch integration:
  - `RuntimeOptions`, `LocalRuntimeConfig`, and `ServeDistributionOptions`
    now support `EnableInstalledPlugins` and `InstalledPluginNames`;
  - Core launch merges installed plugins into the available plugin set and
    appends an installed-plugin declaration bundle;
  - name collisions keep the existing Core/product plugin and do not silently
    activate the installed duplicate.
- Wired products to the Core loader without importing Dex or reimplementing
  plugin management:
  - Slack Bot maps its existing `[runtime].plugins` allow-list to Core's
    installed plugin loader;
  - Coder startup can opt into installed plugins via
    `WithInstalledPlugins(...)`, and its main distribution/channel launch
    paths pass that state to Core launch.
- Added tests proving:
  - enabled installed plugins resolve through Core contribution resolver contributions;
  - bridged installed operations invoke through `fluxplane-plugin` runtime
    paths;
  - disabled plugins and disabled instances are skipped;
  - launch only declares installed plugin refs whose implementations were
    actually added, preserving built-in collisions.

Architectural result: Core is now the agent runtime bridge for installed
`fluxplane-plugin` plugins. Products can opt into installed plugin state through
Core launch options while Dex remains outside every product dependency path.

### Progress Update: Installed Plugin Product UX

Completed in this batch:

- Added a Core launch-level integration test for installed plugins:
  - injects a fake `fluxplane-plugin/management.Store`;
  - injects a direct in-memory plugin runtime;
  - launches the real Core local runtime path;
  - verifies the installed plugin manifest contributes an operation;
  - resolves and executes the operation through the composed Core runtime.
- Extended Core launch options with injectable installed-plugin state/runtime
  hooks:
  - `InstalledPluginStore`;
  - `InstalledRuntime`;
  - pass-through on `RuntimeOptions`, `LocalRuntimeConfig`, and
    `ServeDistributionOptions`.
- Added Coder user-facing installed plugin controls:
  - `.coder.toml` `[plugins].installed = ["name"]`;
  - `.coder.toml` `[plugins].installed_all = true`;
  - `coder --installed-plugin=name`;
  - `coder --installed-plugins` for all enabled installed plugins.
- Updated Coder docs/help so installed plugins are described as
  `fluxplane-plugin` state loaded through Core, not as Dex-backed defaults.
- Updated Slack Bot config comments and example config so `[runtime].plugins`
  clearly means installed `fluxplane-plugin` plugin names loaded through Core.
- Added Coder static inspection wiring for installed plugins:
  - `coder discover` now reads installed plugin manifests through Core's
    `pluginbridge.LoadInstalled`;
  - static discovery appends the installed plugin declaration bundle and
    bridged plugin implementations;
  - `coder serve` now forwards installed-plugin options to Core serve launch.
- Smoke-tested the real installed-plugin path using an isolated default state
  directory:
  - `fluxplane-plugin install sleep` built a marketplace plugin into
    `~/.fluxplane/plugins/bin`;
  - `fluxplane-plugin run sleep` started the cached stdio runtime and returned
    its manifest;
  - `fluxplane-plugin operation invoke sleep sleep --input '{"duration":0.001}'`
    executed the plugin operation successfully;
  - `fluxplane-plugin install clock` plus
    `coder --installed-plugin clock discover` showed
    `fluxplane-plugin:installed`, `plugin:clock/default`, and the clock context
    provider in Coder's static resource view.

Architectural result: end-user products can now turn on installed provider
plugins without importing Dex, without importing `fluxplane-plugins`, and
without hand-rolling the installed state/runtime bridge.

### Updated Next Large Steps After Installed Plugin Checkpoint

The core migration target is now structurally in place: `fluxplane-plugin`
owns reusable plugin management/runtime/CLI behavior, `fluxplane-plugins` owns
concrete plugin implementations, Core owns the agent runtime bridge, and
products opt in through Core without importing Dex.

Recommended next work:

1. Remove legacy Dex naming from the plugin protocol and manifests:
   - replace manifest metadata such as `dex.protocol` with a
     `fluxplane-plugin`/protocol-owned key;
   - keep a compatibility reader only if old installed manifests need to keep
     working during local state migration;
   - update tests and smoke outputs so new examples do not mention Dex.
2. Add automated installed-plugin product smoke coverage:
   - keep the existing Core launch injected-store test as the runtime proof;
   - add a Coder test around static installed-plugin discovery using an
     injectable store/runtime path or a small test-only installed state;
   - add a Slack Bot runtime config test that asserts configured installed
     plugin names reach Core serve/launch options.
3. Decide whether installed plugin selection should become distribution config:
   - current products wire `EnableInstalledPlugins` and explicit names
     manually;
   - if multiple products need the same config shape, move the config model
     into Core distribution/launch helpers;
   - keep `fluxplane-plugin` as the owner of state storage and CLI behavior.
4. Finish Dex repository administration:
   - either delete Dex from the workspace, archive it upstream, or leave it as
     a standalone legacy CLI with no product dependency path;
   - keep the no-Dex-import gates in product/Core/plugin repos.
5. Continue Core dependency reduction only where the boundary is clear:
   - Slack channel/runtime IO remains Core-owned;
   - AWS environment inspection belongs in `fluxplane-plugins/aws`;
   - portable evidence observer/assertion contracts should be introduced through
     Core/plugin bridge work if agent-runtime reactions need plugin-produced
     observations;
   - do not move Coder-specific language/coding plugins out of Coder unless a
     second product needs them.

Best next chunk: legacy Dex naming cleanup in `fluxplane-plugin` and
`fluxplane-plugins`, followed by automated product smoke tests for installed
plugins. That removes the most visible architectural inconsistency while
protecting the new installed-plugin path from regression.

### Progress Update: Fluxplane Protocol Naming And Product Smoke Tests

Completed in this batch:

- Removed the Dex-owned protocol names from the reusable plugin SDK:
  - protocol versions are now `fluxplane.plugin.v2` and
    `fluxplane.plugin.v1`;
  - manifest protocol metadata now uses `fluxplane.plugin.protocol`;
  - old `dex.plugin.*` and `dex.protocol` names are not accepted as legacy
    aliases.
- Replaced SDK-generated user-facing Dex command guidance with
  `fluxplane-plugin` command guidance:
  - auth status/connect helpers point at `fluxplane-plugin auth ...`;
  - operation/index helper text points at
    `fluxplane-plugin operation invoke ...`.
- Updated concrete plugin manifest assertions to use the shared
  `pluginbinding.ManifestProtocolKey` constant, so plugin tests track the
  protocol-owned metadata key.
- Added product regression coverage for installed plugin loading:
  - Coder static discovery now has a test-backed injected installed state and
    direct runtime path, proving installed plugin declarations and operations
    appear in static resource views;
  - Slack Bot now has a test-backed Core local runtime config helper, proving
    `[runtime].plugins` forwards installed plugin names into Core launch
    options.

Validation completed:

```sh
cd ../fluxplane-plugin && go test ./...
cd ../fluxplane-plugins && go test ./...
cd ../fluxplane-plugins && for mod in $(find . -mindepth 2 -maxdepth 2 -name go.mod -printf '%h\n' | sort); do (cd "$mod" && go test ./...) || exit 1; done
cd ../coder && GOWORK=off go test ./...
cd ../fluxplane-apps/slack-bot && GOWORK=off go test ./...
cd ../fluxplane-core && go test ./...
rg "dex\.protocol|dex\.plugin|Use dex|dex auth|dex op" ../fluxplane-plugin ../fluxplane-plugins ../fluxplane-core ../coder ../fluxplane-apps -n
cd ../fluxplane-plugins && HOME=/tmp/fluxplane-plugin-protocol-smoke go run ../fluxplane-plugin/cmd/fluxplane-plugin install sleep
cd ../fluxplane-plugins && HOME=/tmp/fluxplane-plugin-protocol-smoke go run ../fluxplane-plugin/cmd/fluxplane-plugin run sleep
cd ../fluxplane-plugins && HOME=/tmp/fluxplane-plugin-protocol-smoke go run ../fluxplane-plugin/cmd/fluxplane-plugin operation invoke sleep sleep --input '{"duration":0.001}'
```

Result: active SDK/plugin/product code no longer emits or accepts Dex protocol
names. The remaining scan matches are historical changelog text or test fixture
content, not protocol compatibility or product wiring. The fresh CLI smoke
reported manifest metadata `fluxplane.plugin.protocol=fluxplane.plugin.v2` and
successfully invoked the installed `sleep` operation.

### Progress Update: Removed Core Leaf Alias Packages

Completed in this batch:

- Removed the deprecated compatibility packages:
  - `fluxplane-core/core/context`;
  - `fluxplane-core/core/operation`;
  - `fluxplane-core/core/policy`.
- Moved Core-specific context materialization contracts to
  `fluxplane-core/runtime/context`:
  - evidence-aware provider request/build request types;
  - provider/fingerprinting interfaces;
  - render records/diffs;
  - context render events.
- Routed portable contract imports directly to leaf modules:
  - operation contracts use `github.com/fluxplane/fluxplane-operation`;
  - policy contracts use `github.com/fluxplane/fluxplane-policy`;
  - portable context contracts remain in
    `github.com/fluxplane/fluxplane-context`;
  - Core runtime context providers use
    `github.com/fluxplane/fluxplane-core/runtime/context`.
- Added a Core architecture boundary test that fails if the deleted alias
  directories or import paths return.
- Updated current docs to describe the leaf-module ownership and the Core
  runtime context package.

Validation completed:

```sh
cd ../fluxplane-core && go test ./...
cd ../fluxplane-context && go test ./...
cd ../coder && GOWORK=off go test ./...
cd ../fluxplane-apps/slack-bot && GOWORK=off go test ./...
rg "github.com/fluxplane/fluxplane-core/core/(context|operation|policy)" ../fluxplane-core ../coder ../fluxplane-apps -g'*.go' -g'go.mod'
```

### Progress Update: Slack Channel Adapter Moved Out Of Plugins

Completed in this batch:

- Removed empty Core plugin shell directories for drained coding, language,
  OpenAPI, and product-native plugin packages.
- Moved the Core-owned Slack channel/runtime adapter from
  `plugins/integrations/slack` to `adapters/channels/slack`.
- Updated Core launch, serve, datasource indexing, examples, and tests to
  import `github.com/fluxplane/fluxplane-core/adapters/channels/slack`.
- Clarified that the Slack adapter is channel transport plus active-thread
  operations; richer Slack operation/datasource surfaces belong in
  `fluxplane-plugins/slack`.
- Added a Core architecture boundary test that fails if drained plugin
  directories or import paths return.
- Updated the provider-SDK import allowlist so `slack-go/slack` is only
  accepted under the Core Slack channel adapter path.

Validation completed:

```sh
cd ../fluxplane-core && go test ./...
cd ../fluxplane-apps/slack-bot && GOWORK=off go test ./...
rg "github.com/fluxplane/fluxplane-core/plugins/integrations/slack|plugins/integrations/slack" ../fluxplane-core ../coder ../fluxplane-apps ../fluxplane-plugin ../fluxplane-plugins -g'*.go' -g'go.mod'
find ../fluxplane-core/plugins -maxdepth 3 -type d | sort
```

### Progress Update: Core Contributions And `contrib` Umbrella

Completed in this batch:

- Removed the Core `orchestration/pluginhost` package.
- Moved the Core contribution resolver/contracts to
  `orchestration/contributions` with the neutral `Provider` vocabulary.
- Moved Core-bundled contribution providers from the misleading
  `plugins/native/*` bucket into `contrib/*`.
- Added the root `contrib` package with `Runtime(...)` as the default bundled
  provider entrypoint for product/runtime wiring.
- Kept runtime/domain primitives in their direct packages, for example
  `runtime/goal` and `runtime/workspace`, while provider wiring lives in
  `contrib/goal`, `contrib/workspace`, `contrib/identity`, `contrib/loop`, and
  `contrib/text`.
- Updated launch, datasource indexing, examples, plugin bridge, auth connect,
  support packages, and tests to import contribution providers from `contrib`.
- Updated architecture guards so deleted plugin buckets and the removed Core
  host package cannot return through Go imports.

Validation completed:

```sh
cd ../fluxplane-core && go test ./...
rg "github.com/fluxplane/fluxplane-core/orchestration/pluginhost|github.com/fluxplane/fluxplane-core/plugins/native" ../fluxplane-core -g'*.go'
```

### Progress Update: `contrib` Cleanup Checkpoint

Completed in this batch:

- Renamed the remaining contribution resolver files from `pluginhost.go` /
  `pluginhost_test.go` to `host.go` / `host_test.go`.
- Moved the smoke-test echo provider from `plugins/examples/echo` to
  `contrib/echo`.
- Moved the bundled provider event catalog from `plugins/support/eventcatalog`
  to `contrib/eventcatalog`.
- Updated launch, devclient, evaluator, operation-command tests, service tests,
  and docs to use the new `contrib` paths.
- Extended architecture guards so `plugins/examples` and `plugins/support`
  cannot return as Core package buckets.

Validation completed:

```sh
cd ../fluxplane-core && go test ./...
rg "plugins/examples|plugins/support|pluginhost|orchestration/contributions/pluginhost" ../fluxplane-core -g'*.go' -g'*.md'
```

### Progress Update: AWS Extracted To Standalone SDK Plugin

Completed in this batch:

- Added `github.com/fluxplane/fluxplane-plugins/aws` as a standalone
  `fluxplane-plugin` SDK plugin module:
  - operation `aws.environment.inspect`;
  - context provider `aws.environment`;
  - stdio command entrypoint at `cmd/fluxplane-plugin-aws`;
  - marketplace and workspace registration.
- Preserved the old Core AWS behavior that matters outside Core evidence:
  profile/region discovery, credential-presence booleans, region-only
  configured-but-unavailable handling, and no secret value exposure.
- Removed Core's old `plugins/integrations/aws` package and deleted the now-empty
  `plugins/integrations` tree.
- Removed the stale Core legacy integrations doc and updated current GitLab,
  Atlassian, and observations/reactions docs so they no longer point users at
  deleted Core integration packages.
- Extended Core architecture guards so any `plugins/integrations` directory or
  import path fails tests.

Architectural result: Core no longer contains reusable provider implementation
packages. AWS is now a normal external plugin implementation. Core can still
bridge installed plugin operations, context providers, observers, and assertion
deriver templates through `fluxplane-plugin`; evidence bridging is not a reason
to keep AWS implementation code inside Core.

### Progress Update: Portable Evidence Contracts Extracted

Completed in this batch:

- Added `github.com/fluxplane/fluxplane-evidence` as a standalone leaf module for
  inert evidence contracts:
  - observation records and observer specs;
  - assertion records, assertion templates, and assertion-deriver specs;
  - evidence environment refs, phases, subject vocabulary, activation keys, and
    stable assertion fingerprints.
- Updated Core to import `fluxplane-evidence` directly and removed the old
  `core/evidence` package instead of leaving aliases behind.
- Kept runtime execution in Core:
  - `runtime/evidence` still owns observer and assertion-deriver execution
    interfaces;
  - orchestration/session code still schedules observers, derives assertions,
    and applies reaction planning.
- Updated Coder and Slack Bot to the current Core contribution package names
  while adding local `fluxplane-evidence` module wiring, so product
  `GOWORK=off` tests compile against the extracted Core contracts.
- Extended Core architecture guards so `core/evidence` cannot return as a
  compatibility package.

Architectural result: plugin and product code can now reference portable
observation/assertion DTOs without importing Core.

### Progress Update: Plugin-Produced Evidence Bridged Into Core

Completed in this batch:

- Extended `fluxplane-plugin` manifest, protocol, and binding layers with
  portable evidence observer specs, assertion-deriver specs, and an
  `evidence.observe` command.
- Updated `fluxplane-plugins/aws` to contribute non-secret AWS environment
  evidence:
  - `aws.environment.configured` is emitted only when AWS is configured;
  - `aws.environment.available` is emitted only when AWS is usable;
  - assertion templates map those observation kinds to
    `integration.configured` and `integration.available` without inspecting
    secrets or overclaiming availability.
- Updated Core's `orchestration/pluginbridge` so SDK plugin manifests contribute
  observer and assertion-deriver specs, direct/stdio runtimes can be invoked as
  Core evidence observers, and manifest assertion templates execute as Core
  runtime assertion derivers.

Validation completed:

```sh
cd ../fluxplane-plugin && go test ./...
cd ../fluxplane-plugins/aws && go test ./...
cd ../fluxplane-core && go test ./...
```

Result: all three test suites passed in the workspace.
