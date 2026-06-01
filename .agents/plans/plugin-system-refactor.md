# Plugin System Refactor Plan

## Goal

Make `fluxplane-core` a lean agent-runtime repository that contains only the minimum base contracts and runtime primitives needed to build products with agent capabilities. Move product tools and third-party integrations into plugin modules that can be used either directly in-process or installed/run externally through the dex CLI/protocol.

Target outcome:

- `fluxplane-core` no longer carries heavy optional integration dependencies.
- `coder` and `fluxplane-apps/slack-bot` are products built on top of `fluxplane-core` plus selected plugins.
- Third-party integrations such as GitLab, Slack, Jira, Confluence, Kubernetes, Docker, Loki, SQL, OpenAI, etc. live outside core.
- A plugin implementation can expose the same manifest and behavior through:
  - direct Go binding, for embedded product use;
  - stdio/CLI protocol binding, for dex-managed install-on-demand use.
- `dex` becomes the plugin manager/runtime/installer, not the long-term home of all plugin contracts and implementations.

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

Purpose: plugin manager, installer, registry, CLI, and host bridge.

Keep dex focused on:

- plugin installation;
- marketplace/index resolution;
- plugin binary execution;
- stdio protocol host implementation;
- `dex op run`;
- `dex datasource search/get/list`;
- `dex auth connect/status`;
- endpoint management;
- capability grants;
- plugin runtime state;
- compatibility with products that call managed plugins.

Move long-term shared SDK/protocol contracts out of dex into `fluxplane-plugin`.

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

## Immediate Next Actions

1. Create `fluxplane-plugin` module skeleton.
2. Extract protocol and manifest contracts from dex into `fluxplane-plugin`.
3. Add forbidden dependency checks for `fluxplane-core`.
4. Create `fluxplane-plugins` monorepo skeleton.
5. Migrate one plugin end-to-end as the reference implementation, preferably a medium-complexity plugin such as Tavily or GitLab.
6. Update dex to install/run the migrated reference plugin from the new module.
7. Move remaining heavy integrations out of core.
8. Move coding bundle/native/language plugins out of core and update `coder`.
9. Remove deprecated core integration packages after compatibility window.
