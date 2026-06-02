# Plugin System Refactor Plan

## Goal

Make `fluxplane-core` the central agent-runtime repository: agentic domain model, agent loop/session/conversation runtime, contribution composition, policy, projection, and runtime orchestration. Move product tools and third-party integrations into plugin modules that can be used either directly in-process or installed/run externally through the shared plugin SDK/protocol.

Target outcome:

- `fluxplane-core` no longer carries heavy optional integration dependencies.
- `coder` and `fluxplane-apps/slack-bot` are products built on top of `fluxplane-core` plus selected plugins.
- Third-party integrations such as GitLab, Slack, Jira, Confluence, Kubernetes, Docker, Loki, SQL, OpenAI, etc. live outside core.
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
- operation contracts and execution model;
- context provider contracts;
- contribution/resource model;
- evidence, observations, assertions, reactions;
- policy/access-control integration points;
- workspace/process/network/environment abstractions where they are runtime boundaries;
- plugin host/loading contracts;
- minimal test/example plugins only if needed.

Remove or migrate from core:

- `plugins/integrations/*`;
- `plugins/languages/*`;
- `plugins/native/*`;
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
- plugin host abstractions;
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

Belong in `fluxplane-plugins/integrations/*` and run through dex by default:

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

- `fluxplane-apps/slack-bot` may directly import the Slack plugin.
- `coder` should usually use GitLab/Jira/Slack via dex-managed plugins unless a distribution intentionally embeds them.

## Direct Binding + Stdio Binding

A plugin implementation should be written once and exposed in two ways:

1. Direct Go binding:

```go
bundle := gitlab.New()
host.Register(pluginbinding.Direct(bundle))
```

2. Stdio/CLI binding:

```sh
dex plugin install gitlab
dex op run gitlab.merge_request_search '{...}'
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
  ├── fluxplane-plugins/native
  ├── fluxplane-plugins/languages/golang
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
  └── dex, when installed/run externally
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
- is installed by dex;
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
- New integration work happens in dex temporarily or, preferably, the new `fluxplane-plugins` repo.

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
- Dex can install/run at least one migrated plugin from the new repo.

### Phase 3: Move Heavy Integrations Out of `fluxplane-core`

Remove or deprecate these core packages:

```text
fluxplane-core/plugins/integrations/gitlab
fluxplane-core/plugins/integrations/slack
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

### Phase 4: Move Coding/Native/Language Plugins Out of Core

Create modules for product-native tools:

```text
fluxplane-plugins/coding
fluxplane-plugins/native/*
fluxplane-plugins/languages/golang
fluxplane-plugins/languages/markdown
```

Move:

```text
fluxplane-core/plugins/bundles/coding
fluxplane-core/plugins/native/filesystem
fluxplane-core/plugins/native/shell
fluxplane-core/plugins/native/project
fluxplane-core/plugins/native/browser
fluxplane-core/plugins/native/code
fluxplane-core/plugins/native/human
fluxplane-core/plugins/languages/golang
fluxplane-core/plugins/languages/markdown
fluxplane-core/plugins/integrations/git
```

Product decision:

- `coder` directly imports the coding bundle.
- `fluxplane-core` does not.

Acceptance criteria:

- `coder` has the same default coding tools after migration.
- `fluxplane-core` no longer exports product-level plugin bundles.
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
    runtime: dex
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
- `coder` can call dex-managed plugins when installed/configured.
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
- uses dex runtime for optional third-party plugins;
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
- either imports Slack plugin directly because Slack is product-essential, or uses dex-managed Slack;
- Slack plugin remains outside core either way.

Recommendation:

- Direct import is acceptable for a Slack bot because Slack is not optional for that product.
- Still keep Slack dependencies outside `fluxplane-core`.

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
- Optional integrations are dex-managed by default.
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

### Planning Update: Reusable Plugin Management CLI Belongs In `fluxplane-plugin`

`fluxplane-plugin` should provide an importable plugin-management CLI package plus a standalone binary.

Target layout:

```text
fluxplane-plugin/
  management/             # backend-neutral plugin management interfaces and DTOs
  cli/                    # importable Cobra command tree
  cmd/fluxplane-plugin/   # standalone CLI binary
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
- `fluxplane-dex` imports `fluxplane-plugin/cli` and provides a dex-backed implementation of the management interfaces.
- The standalone `cmd/fluxplane-plugin` binary can initially wire no backend or a local/default backend, then later use the same backend implementation as dex or a backend selected by config.

Dependency rule:

```text
fluxplane-plugin/cli -> fluxplane-plugin/management
fluxplane-dex        -> fluxplane-plugin/cli
fluxplane-plugin     -> not fluxplane-dex
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
3. Move plugin host/contributor contracts to `fluxplane-plugin/pluginhost` unless a separate `../fluxplane-pluginhost` becomes clearly necessary.
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
- `fluxplane-plugin` now ships a standalone CLI entrypoint under `cmd/fluxplane-plugin`.
- Added a `management/local` filesystem-backed backend owned by `fluxplane-plugin`, so the standalone CLI can install/list/search/remove local plugin metadata without dex.
- The local backend currently stores plugin metadata in a JSON state file and does not yet execute plugin processes; `run` validates installation and reports that process execution is not implemented in the local backend yet.
- `fluxplane-plugin/protocol` is now consumed directly by `fluxplane-dex`.
- Removed the duplicate `fluxplane-dex/protocol` package instead of adding a compatibility shim.
- Updated dex test fixture module setup to replace/import `fluxplane-plugin` directly.

Validation completed:

```sh
cd fluxplane-plugin
GOWORK=off go test ./...
GOWORK=off go build ./cmd/fluxplane-plugin

cd ../fluxplane-dex
go test ./...
```

Additional dependency check:

```sh
cd fluxplane-plugin
go list -deps ./management/... ./cli ./cmd/fluxplane-plugin | grep -E 'github.com/fluxplane/fluxplane-(core|dex)'
```

Result: no matches for the new management/CLI packages.

Current caveat:

- The existing seeded root package in `fluxplane-plugin` still depends on `fluxplane-core` and `fluxplane-dex`. The new `management`, `management/local`, `cli`, `cmd/fluxplane-plugin`, and `protocol` paths are the clean ownership direction, but the copied dex-core adapter still needs to be split/moved.

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
GOWORK=off go build ./cmd/fluxplane-plugin
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
- Kept the runtime-specific adapter in its existing dedicated location: `github.com/fluxplane/fluxplane-dex/fluxplaneplugin`.
- Reduced `fluxplane-plugin` to reusable SDK/protocol packages only:
  - root package docs;
  - `protocol`;
  - `host`;
  - `datasource`;
  - `management`;
  - `management/local`;
  - `cli` and `cmd/fluxplane-plugin`.
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
github.com/fluxplane/fluxplane-core/orchestration/pluginhost
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
adapter/core/   # transitional core pluginhost adapter, temporary or moved out later
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

### Step 6: Extract `pluginhost` Contracts From Core

Current blockers:

- Most adapter files import `github.com/fluxplane/fluxplane-core/orchestration/pluginhost`.
- `pluginhost` is a core orchestration package but contains reusable plugin-facing contracts.

Actions:

1. Create a standalone plugin-host contract package, preferably inside `fluxplane-plugin`:

   ```text
   github.com/fluxplane/fluxplane-plugin/pluginhost
   ```

   or, if it has broader runtime use:

   ```text
   github.com/fluxplane/fluxplane-pluginhost
   ```

2. Move/alias these kinds of contracts from core `orchestration/pluginhost`:
   - `Plugin`;
   - `Context`;
   - operation contributor interfaces;
   - datasource provider contributor interfaces;
   - context provider contributor interfaces;
   - discovery provider contributor interfaces;
   - auth target contributor interfaces;
   - plugin config metadata that is not core-runtime-specific.
3. Update `fluxplane-core/orchestration/pluginhost` to alias or adapt the standalone contracts.
4. Update `fluxplane-plugin` to use standalone contracts.
5. Update `coder` and apps gradually.

Acceptance criteria:

- `fluxplane-plugin` no longer imports `fluxplane-core/orchestration/pluginhost`.
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
4. Update `fluxplane-plugin` and `pluginhost` contracts to import standalone resource types.

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
2. Move pure contracts from `fluxplane-core/core/context` and relevant `pluginhost` context-provider contributor interfaces:
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
- The adapter still bridges dex datasource specs into core pluginhost contributor surfaces.

Actions:

1. Keep datasource core contracts in `fluxplane-datasource`.
2. Move any remaining dex datasource DTOs into `fluxplane-plugin/datasource` or map them onto `fluxplane-datasource` types directly.
3. Keep dex datasource runtime execution in dex.
4. Make datasource provider contribution interfaces use standalone pluginhost/resource/context contracts.

Acceptance criteria:

- Datasource plugin contracts do not require core or dex.

### Step 15: Decide Where The Dex-To-Core Adapter Lives

After the SDK contracts are extracted, the original copied package is really an adapter:

```text
dex plugin registry/runtime -> Fluxplane host plugin/contributor surfaces
```

That adapter should not be in the base SDK long-term.

Options:

1. Move it back to dex:

   ```text
   fluxplane-dex/adapter/fluxplane
   ```

2. Create a separate module:

   ```text
   ../fluxplane-dex-adapter
   ```

3. Keep it temporarily under `fluxplane-plugin/adapter/dexcore` while extracting contracts, then move it out.

Recommendation:

- Keep base SDK pure.
- Move dex-engine-specific adapter code to dex or `fluxplane-dex-adapter` after the standalone contracts exist.

Acceptance criteria:

- `github.com/fluxplane/fluxplane-plugin` root/base packages do not import dex or core.
- Any adapter that imports both dex and core is in a clearly named adapter module/package, not in SDK contract packages.

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

Move context provider contracts out of `core/context` and `orchestration/pluginhost`:

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

#### `../fluxplane-pluginhost` Or `fluxplane-plugin/pluginhost`

Move reusable plugin host/contributor interfaces out of `orchestration/pluginhost`:

- `Plugin` interface;
- contributor interfaces for operations, datasources, context, discovery, auth targets;
- plugin registration metadata;
- plugin host context DTOs.

Recommendation:

- If the interfaces are only meaningful for the plugin SDK, put them under `fluxplane-plugin/pluginhost`.
- If products need them independently from the SDK package, create `../fluxplane-pluginhost`.

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

#### `../fluxplane-language`

Move generic language/toolchain contracts out of `core/language` and `runtime/language`:

- language IDs;
- package/symbol/ref DTOs;
- toolchain metadata;
- build/test/fmt/vet operation contracts.

Keep implementations in plugin modules:

- Go language plugin;
- Markdown plugin;
- future language plugins.

#### `../fluxplane-data` Or Continue `../fluxplane-datasource`

Evaluate `core/data`, `runtime/data`, and `runtime/datasource`:

- if these are datasource records/entity/index contracts, fold into `fluxplane-datasource`;
- if they are broader data-store contracts, create `../fluxplane-data`.

Move only pure DTOs/interfaces. Keep stores, mirrors, field indexes, semantic indexes, and runtime implementation where they belong.

### Product/Plugin Implementation Modules To Move Out Of Core

These are not just contracts; they should become plugin implementation modules or move into a plugin monorepo:

```text
../fluxplane-plugins/coding
../fluxplane-plugins/native/filesystem
../fluxplane-plugins/native/shell
../fluxplane-plugins/native/project
../fluxplane-plugins/native/browser
../fluxplane-plugins/native/code
../fluxplane-plugins/native/human
../fluxplane-plugins/languages/golang
../fluxplane-plugins/languages/markdown
../fluxplane-plugins/integrations/git
../fluxplane-plugins/integrations/gitlab
../fluxplane-plugins/integrations/slack
../fluxplane-plugins/integrations/jira
../fluxplane-plugins/integrations/confluence
../fluxplane-plugins/integrations/kubernetes
../fluxplane-plugins/integrations/loki
../fluxplane-plugins/integrations/docker
../fluxplane-plugins/integrations/sql
../fluxplane-plugins/integrations/aws
../fluxplane-plugins/integrations/openai
../fluxplane-plugins/integrations/openapi
../fluxplane-plugins/integrations/websearch
```

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

### Progress Update: Reusable Plugin CLI/State, Runtime Execution, Core Bridge, Product Wiring

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
- Added `fluxplane-core/orchestration/pluginbridge`:
  - Core depends on `fluxplane-plugin`;
  - adapts `fluxplane-plugin/pluginruntime.Plugin` into Core `pluginhost.Plugin`;
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
cd ../fluxplane-plugin && HOME=$(mktemp -d) GOWORK=off go run ./cmd/fluxplane-plugin install bananawix
# exits non-zero: unknown marketplace plugin
cd ../fluxplane-plugin && tmp=$(mktemp -d); HOME=$tmp GOWORK=off go run ./cmd/fluxplane-plugin install local --source dev >/dev/null && HOME=$tmp GOWORK=off go run ./cmd/fluxplane-plugin run local
# exits non-zero: no runnable runtime configured
cd ../fluxplane-plugin && tmp=$(mktemp -d); HOME=$tmp GOWORK=off go run ./cmd/fluxplane-plugin install local --source dev --command /bin/echo --arg plugin >/dev/null && HOME=$tmp GOWORK=off go run ./cmd/fluxplane-plugin run local ok
# returns stdout: "plugin ok\n"
cd ../fluxplane-plugin && tmp=$(mktemp -d); HOME=$tmp GOWORK=off go run ./cmd/fluxplane-plugin install gitlab && HOME=$tmp GOWORK=off go run ./cmd/fluxplane-plugin run gitlab
# installs from local fluxplane-plugins marketplace, runs the cached binary, and returns a real plugin manifest handshake
cd ../fluxplane-plugin && tmp=$(mktemp -d); HOME=$tmp GOWORK=off go run ./cmd/fluxplane-plugin install system && HOME=$tmp GOWORK=off go run ./cmd/fluxplane-plugin operation invoke system system.info --input '{"categories":["os"]}'
# invokes the system plugin through framed stdio and CLI host provider support
cd ../fluxplane-plugin && tmp=$(mktemp -d); HOME=$tmp GOWORK=off go run ./cmd/fluxplane-plugin install gitlab >/tmp/fp-install-gitlab.json && test -x "$tmp/.fluxplane/plugins/bin/fluxplane-plugin-gitlab" && HOME=$tmp GOWORK=off go run ./cmd/fluxplane-plugin remove gitlab >/tmp/fp-remove-gitlab.json && test ! -e "$tmp/.fluxplane/plugins/bin/fluxplane-plugin-gitlab"
# verifies marketplace install cache creation and owned-artifact cleanup
cd ../fluxplane-plugin && tmp=$(mktemp -d); HOME=$tmp GOWORK=off go run ./cmd/fluxplane-plugin install system && HOME=$tmp GOWORK=off go run ./cmd/fluxplane-plugin auth methods system && HOME=$tmp GOWORK=off go run ./cmd/fluxplane-plugin operation list system && HOME=$tmp GOWORK=off go run ./cmd/fluxplane-plugin datasource list system
cd ../fluxplane-plugin && tmp=$(mktemp -d); HOME=$tmp GOWORK=off go run ./cmd/fluxplane-plugin install gitlab && HOME=$tmp GOWORK=off go run ./cmd/fluxplane-plugin search git && HOME=$tmp GOWORK=off go run ./cmd/fluxplane-plugin list && HOME=$tmp GOWORK=off go run ./cmd/fluxplane-plugin status gitlab && HOME=$tmp GOWORK=off go run ./cmd/fluxplane-plugin manifest gitlab && HOME=$tmp GOWORK=off go run ./cmd/fluxplane-plugin auth connect gitlab --method personal_access_token --field access_token=smoke && HOME=$tmp GOWORK=off go run ./cmd/fluxplane-plugin auth status gitlab && HOME=$tmp GOWORK=off go run ./cmd/fluxplane-plugin auth disconnect gitlab --method personal_access_token && HOME=$tmp GOWORK=off go run ./cmd/fluxplane-plugin remove gitlab
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
```

Result: tests passed. The CLI smoke checks now reject the false-success paths and exercise real installed plugin runtimes through the reusable plugin CLI.

### Current Next Large Steps

1. Make `fluxplane-plugins/marketplace.json` the canonical registry source:
   - remove or generate duplicated Dex compatibility marketplace JSON/defaults;
   - decide whether `fluxplane-plugin` should embed a default registry package or keep filesystem/env discovery only.
2. Complete Core bridge coverage:
   - datasource runtime provider bridge;
   - context provider bridge;
   - host capability adapter from Core runtime services to `fluxplane-plugin/protocol.HostCaller`;
   - auth connect/test integration beyond inert auth method contribution.
3. Move concrete product/plugin wiring to `fluxplane-plugins` modules:
   - start with a small direct plugin module as a real product import;
   - update `coder` / Slack Bot examples to use `BridgePlugin` with concrete modules;
   - keep products free of Dex imports.
4. Drain remaining Dex-only feature surface:
   - compare Dex commands/features against `fluxplane-plugin` CLI;
   - move any missing plugin management/auth/manifest/datasource/operation behavior out;
   - delete or shrink Dex once no unique end-user value remains.
5. Continue core extraction:
   - move coding/native/language plugins out of Core into `fluxplane-plugins`;
   - keep Core's agent runtime central but reduce optional provider SDK dependencies.
6. Add boundary tests across repos:
   - `fluxplane-plugin` must not import Core or Dex;
   - products must not import Dex;
   - Core may import `fluxplane-plugin`;
   - Dex must not import Core unless explicitly kept during a temporary compatibility window.
