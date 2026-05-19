---
triggers:
  - agentsdk.app.yaml
  - app manifest
  - app config
  - default_agent
  - daemon
  - datasource
---

# `agentsdk.app.yaml` configuration

`agentsdk.app.yaml` is the app manifest filename. Do not rename it unless the product explicitly plans a manifest rename. The manifest is YAML and may contain multiple documents separated by `---`.

## Document kinds

The first document is usually the app manifest:

```yaml
kind: app
name: my-app
description: Short description.
default_agent: default
```

Additional documents may define resources:

- `kind: agent`
- `kind: session`
- `kind: command`
- `kind: workflow`
- `kind: operation`
- `kind: llm_provider`

App-level arrays (`commands`, `workflows`, `operations`, `llm_providers`) can also declare those resources inline, but separate YAML documents are easier to read for non-trivial apps.

## Minimal local app

```yaml
kind: app
name: my-app
description: Local app with one assistant.
default_agent: default
---
kind: session
name: default
agent: default
---
kind: agent
name: default
model: openai/gpt-5.5
tools: []
system: |
  You are a helpful assistant for this app.
```

If `default_agent` is set and no default session is declared, local distribution loading may expose a generated `default` session. Prefer declaring the session when you need stable session metadata or channel wiring.

## App manifest fields

Top-level app fields:

```yaml
kind: app
name: my-app
description: Short description.
default_agent: default # string or {name: default}
sources: []
discovery: {}
model_policy: {}
models: {}
llm_providers: []
distribution: {}
plugins: []
commands: []
workflows: []
operations: []
datasource: {}
semantic_search: {}
security: {}
identity: {}
runtime: {}
daemon: {}
connectors: {}
```

Use only the fields needed by the app. Unknown fields are rejected by schema validation.

## Agents

Agents define model-facing behavior and the capabilities they can call.

```yaml
kind: agent
name: coder
description: Local coding assistant.
model: openai/gpt-5.5
max_tokens: 4096
thinking: enabled
effort: high
tools:
  - file_read
  - file_edit
context:
  - datasource.catalog
datasources:
  - local-docs
skills:
  - app-configuration
turns:
  max_steps: 50
  continuation:
    max_continuations: 3
    context_policy: compact
    stop_condition:
      type: max
      max: 3
system: |
  You are concise and concrete.
```

Notes:

- `tools` names operations/tools available to the agent.
- `context` names context providers added to the prompt.
- `datasources` names configured datasources the agent may use.
- `skills` names skill resources available to the agent.
- `model` can be a provider/model ref such as `openai/gpt-5.5` or an alias declared under `models.available`.

## Sessions

Sessions bind user conversations to an agent, and optionally to a channel.

```yaml
kind: session
name: default
description: Default local session.
agent: coder
channel: local
metadata:
  purpose: local-dev
```

## Models and LLM providers

Use `models` for app-level model aliases and defaults:

```yaml
models:
  default: openai/gpt-5.5
  available:
    - provider: openai
      model: gpt-5.5
      aliases: [default, coding]
      params:
        thinking: enabled
        effort: high
```

Use `llm_providers` documents or app-level entries when you need provider specs directly. `models.default` overrides the older `model_policy.model` default when both are set.

`model_policy` can constrain model selection:

```yaml
model_policy:
  provider: openai
  model: gpt-5.5
  use_case: coding
  source_api: responses
  approved_only: true
  allow_degraded: false
  allow_untested: false
  evidence_path: docs/model-evidence.md
  annotations:
    owner: platform
```

## Datasources and semantic search

Configure search/indexable data under `datasource`:

```yaml
semantic_search:
  enabled: true
  embeddings:
    provider: axon
    model: text-embedding-model
  store:
    kind: json
    path: .agents/index/datasources.json
  defaults:
    chunking:
      strategy: tokens
      target_tokens: 350
      overlap_tokens: 50
    retrieval:
      mode: hybrid
      limit: 8
      min_score: 0.2

datasource:
  index:
    concurrency: 4
    freshness: 15m
  datasources:
    - name: local-docs
      kind: filesystem
      description: Markdown documentation in this checkout.
      entities: [file.document]
      path: .
      include: ["*.md", "*.markdown"]
      index:
        enabled: true
        freshness: 15m
      semantic:
        enabled: true
        entities:
          file.document:
            corpus:
              title_fields: [path]
              body_fields: [content]
              metadata_fields: [path]
            chunking:
              target_tokens: 350
              overlap_tokens: 50
            retrieval:
              mode: hybrid
              limit: 8
            incremental:
              updated_at_field: updated_at
```

Datasource fields:

- `name`: stable datasource name agents reference.
- `kind`/`type`: datasource implementation kind. `type` is accepted as a legacy synonym when `kind` is empty.
- `entities`: entity types the datasource provides.
- `connector`: connector instance name for connector-backed datasources.
- `path` and `include`: filesystem datasource shortcuts; they are translated into config values.
- `config`: string key/value implementation-specific settings.

## Runtime workspace and stores

Use `runtime` for local runtime wiring:

```yaml
runtime:
  workspace:
    roots:
      - name: repo
        path: .
        access: read_write
        create: false
        env_files: [.env]
    scratch_root: .agents/scratch
    env_files: [.env]
  data:
    store:
      kind: sqlite
      dsn: file:.agents/runtime.db
      dsn_env: RUNTIME_DATA_DSN
  events:
    store:
      kind: nats_jetstream
      dsn: nats://localhost:4222
      dsn_env: RUNTIME_EVENTS_DSN
      stream: AGENTRUNTIME
      subject: agentruntime.events
      create_stream: true
```

Prefer `dsn_env` for secrets or deployment-specific connection strings.

## Daemon, listeners, channels, and connectors

`daemon` wires long-running serving surfaces. Keep channel HTTP/SSE and daemon/control HTTP conceptually separate.

Local direct-channel example:

```yaml
daemon:
  listeners:
    - name: local
      type: http
      addr: coder.sock
      auth:
        mode: local_socket
  channels:
    - name: local
      type: direct
      listener: local
      session: default
      access:
        mode: open
```

Connector-backed example shape:

```yaml
connectors:
  slack-main:
    kind: slack

daemon:
  channels:
    - name: slack
      type: connector
      connector: slack-main
      instance: workspace-a
      session: default
      access:
        mode: allowlist
        allow_users: [user:alice@example.com]
        allow_channels: [C123]
        deny_users: []
        deny_channels: []
        allow_kinds: [message]
        default_trust: verified
        operators: [user:ops@example.com]
        internal_users: []
        internal_channels: []
        sharing: private
```

## Commands

Commands expose structured slash-style actions. They target either an operation or a workflow.

```yaml
kind: command
name: review
path: [review]
description: Review the current change.
policy:
  agent_callable: false
input_schema:
  type: object
  properties:
    focus:
      type: string
target:
  operation: code_review
  input:
    severity: normal
annotations:
  owner: engineering
```

Rules:

- Use either `target.operation` or `target.workflow`, not both.
- `path` controls the command path. If omitted, `name` is used.
- `policy.agent_callable: true` allows both users and agents to invoke it; otherwise commands are user-facing by default.

## Workflows

Workflows declare multi-step processes. Keep workflow specs inert; runtime owns execution state.

```yaml
kind: workflow
name: verify-change
description: Run verification and summarize results.
steps:
  - id: test
    kind: operation
    operation: go_test
    input:
      patterns: [./...]
  - id: summarize
    kind: agent
    agent: coder
    depends_on: [test]
    input:
      prompt: Summarize the test result.
    error_policy: fail
```

## Operations

Operations declare callable capability contracts. Runtime/plugins/adapters provide implementations.

```yaml
kind: operation
name: code_review
description: Review a code change.
input:
  schema:
    format: json-schema
    data:
      type: object
      properties:
        severity:
          type: string
output:
  schema:
    format: json-schema
    data:
      type: object
      properties:
        summary:
          type: string
semantics:
  read_only: true
examples:
  - input:
      severity: normal
    output:
      summary: Looks good.
annotations:
  implementation: plugin
```

Prefer typed operation implementations in Go and let typed helpers produce JSON Schema. Hand-written schemas should stay close to the input/output type and match runtime validation.

## Distribution metadata and build

Use `distribution` for packaged product metadata and build/deploy hints:

```yaml
distribution:
  name: my-app
  title: My App
  description: Packaged assistant.
  author: Team
  version: 0.1.0
  default_session: default
  default_conversation: default
  default_model:
    provider: openai
    model: gpt-5.5
    use_case: coding
  surfaces:
    cli: true
    repl: true
    one_shot: true
    serve: true
    deploy: false
    validate: true
    status: true
    discover: true
  build:
    assets:
      - agentsdk.app.yaml
      - docs/**/*.md
    docker:
      image: my-app
      tags: [latest]
      dockerfile: Dockerfile
      context: .
      platforms: [linux/amd64]
  deploy:
    model: openai/gpt-5.5
  commands:
    - name: review
      description: Review the current change.
  metadata:
    owner: platform
```

## Identity

Use `identity` to map provider identities to canonical users, groups, and trust.

```yaml
identity:
  users:
    - id: user:alice@example.com
      username: alice
      display_name: Alice
      trust: verified
      groups: [developers]
      emails:
        - address: alice@example.com
          verified: true
          primary: true
          source: company
      identities:
        - provider: slack
          subject: U123
          username: alice
      annotations:
        team: platform
  groups:
    - id: developers
      display_name: Developers
      trust: verified
      members: [user:alice@example.com]
      annotations: {}
  rules:
    - provider: slack
      subject: "*"
      groups: [developers]
      trust: verified
      annotations: {}
```

## Discovery, plugins, sources, and security

Discovery controls external resource lookup:

```yaml
discovery:
  include_global_user_resources: false
  include_external_ecosystems: false
  allow_remote: false
  trust_store_dir: .agents/trust
```

Plugins are optional first-party capability bundles selected explicitly:

```yaml
plugins:
  - coding
  - task
  - web
```

Sources describe where resources came from:

```yaml
sources:
  - location: apps/coder/resources/.agents
    scope: embedded
    ecosystem: agentdir
```

`security` is an authorization policy. Keep grants least-privilege for deployed apps. For local privileged coder-style apps, make broad grants explicit rather than hidden.

## Authoring checklist

- Keep the top-level app document first.
- Use `---` between resource documents.
- Prefer stable lowercase names and reference those names exactly.
- Declare every referenced `agent`, `session`, `channel`, `listener`, `datasource`, `connector`, `command`, `workflow`, and `operation`.
- Do not use compatibility shims or stale `agentsdk` binary assumptions; only the manifest filename remains `agentsdk.app.yaml`.
- Put deployment-specific secrets in environment variables and reference them with `dsn_env` or connector configuration, not inline YAML.
- For user-visible app changes, update `CHANGELOG.md` in the same change unless explicitly told to skip it.
