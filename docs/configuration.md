# Configuration

Fluxplane Agent Runtime loads configuration from two filesystem-backed sources:
appconfig manifests and `.agents` resource trees. Both decode into resource
contribution bundles that the runtime composes into apps, sessions, agents,
commands, workflows, operations, datasources, model providers, and plugin
capabilities.

Use appconfig for runnable apps. Use agentdir when you want a portable resource
tree of authored agents, commands, workflows, and skills.

## Appconfig

Appconfig is the primary format for local apps and daemon distributions. A
project can define one of these files at its root:

- `agentsdk.app.json`
- `agentsdk.app.yaml`
- `agentsdk.app.yml`

YAML is the usual choice because appconfig supports multi-document files. The
first document is the app document. Additional documents can define agents,
sessions, commands, workflows, operation declarations, datasources, and model
providers.

```yaml
kind: app
name: demo
description: Local demo app.
default_agent:
  name: default
daemon:
  listeners:
    - name: local
      type: http
      addr: agentsdk-demo.sock
      auth:
        mode: local_socket
  channels:
    - name: local
      type: direct
      listener: local
      session: default
      access:
        mode: open
---
kind: session
name: default
agent: default
---
kind: agent
name: default
system: |
  You are a helpful assistant for this local app.
```

Create this minimal shape with:

```bash
agentsdk init .
```

### App Document

The `kind: app` document declares the app identity and the outer product wiring.
Common fields are:

- `name` and `description` for app identity.
- `default_agent` for the agent used when a session does not choose one.
- `plugins` for first-party plugin contribution bundles.
- `connectors` for named connector instances used by channels or datasources.
- `datasources` for configured data sources available to agents.
- `commands`, `workflows`, and `operations` for resource declarations embedded
  directly in the app document.
- `daemon` for listeners and channels used by `agentsdk serve` and
  `agentsdk remote`.
- `runtime` for local runtime wiring; see [Runtime](#runtime).
- `distribution` for runnable/deployable package metadata and Docker build
  inputs.
- `semantic_search` for app-wide datasource indexing defaults.
- `identity` for canonical app users, provider identities, groups, and
  group/user trust used by identity resolution and authorization.
- `llm_providers` for app-local model provider and model catalog entries.

Identity entries map channel-specific users to stable `core/user` IDs, and can
also act as an overlay on users resolved by a channel provider. Slack channels
try `users.info` first and use the profile email as the canonical user ID; the
manifest can then add groups or stronger app trust without listing every Slack
ID. Explicit `identities` entries are still useful when an email is unavailable
or when you want a pinned provider-ID mapping.

```yaml
identity:
  users:
    - id: timo@company.org
      display_name: Timo
      identities:
        - provider: slack
          provider_id: U0123456789
      groups: [admins]
  groups:
    - id: admins
      trust: operator
  rules:
    - match:
        provider: slack
        resolution: resolved
      groups: [slack-bot-users]
```

Resolved users contribute `user:<id>` and `group:<id>` authorization subjects.
Rules can add groups after provider-specific resolution, such as assigning every
resolved Slack email user to `slack-bot-users` while keeping unresolved channel
identities in an `anonymous` group. Use `/whoami` in a session to inspect the
caller, resolved user, trust, and authorization subjects that the runtime sees
for the current turn.

For Slack apps, sender identity and trust should come from resolved core
identity context, not from Slack message metadata. Slack-specific audience trust
is a sharing constraint for the conversation and stays separate from the
sender's effective trust.

Commands, workflows, and operation declarations can also be separate
multi-document resources:

```yaml
kind: command
name: feature
target:
  workflow: feature
---
kind: workflow
name: feature
steps:
  - id: implement
    agent: coder
---
kind: operation
name: echo
description: Declaration-only operation metadata.
```

### Runtime

The top-level `runtime` section configures local runtime boundaries. These
settings are launch-time wiring, not agent resources, and are consumed by
`agentsdk run`, `agentsdk serve`, and distribution CLIs such as `coder`.

Filesystem operations are secure by default: without extra configuration, they
can only access the app workspace root. Additional workspace roots are opt-in
and should point at specific directories, not broad host locations such as all
of `/tmp`.

Use `runtime.workspace.roots` to expose named filesystem roots. Tools can
address named roots with `@name/path`; absolute paths are accepted only when
they resolve inside the app root or a configured extra root.

```yaml
runtime:
  workspace:
    roots:
      - name: tmp
        path: /tmp/agentruntime-demo
        access: read_write
        create: true
    scratch_root: tmp
```

Fields:

- `roots[].name` is the logical root name used in paths such as
  `@tmp/report.txt`.
- `roots[].path` is the host directory exposed to the runtime.
- `roots[].access` accepts `read_write` or `read_only`; omitted access defaults
  to `read_write`.
- `roots[].create` creates the directory at launch when it does not exist.
- `scratch_root` chooses the named root used by runtime-owned scratch
  directories, such as generated image outputs.

With the example above, `file_create` may write `@tmp/report.txt` or
`/tmp/agentruntime-demo/report.txt`. It still cannot write arbitrary files
elsewhere under `/tmp`, and symlinks that escape the configured root are
rejected.

### Agents

Agent documents define LLM-backed runtime agents.

```yaml
---
kind: agent
name: support
model: openai/gpt-5.5
turns:
  max_steps: 50
tools:
  - datasource_search
  - datasource_get
context:
  - datasource.catalog
datasources:
  - local-docs
skills:
  - triage
system: |
  Help the user answer questions from the configured documentation.
```

Common fields are `name`, `description`, `model`, `max_tokens`, `turns`,
`thinking`, `effort`, `tools`, `context`, `datasources`, `skills`, and
`system`.

`thinking` accepts `auto`, `on`, `off`, and the agentdir-friendly alias
`enabled`. At runtime `enabled` is normalized to `on`. `effort` accepts `low`,
`medium`, `high`, or `max` when the selected provider/model exposes that
reasoning effort.

#### Turns

`turns` configures the agent turn loop. The inner loop is controlled by
`turns.max_steps`: it limits model decision calls in one user turn. Tool
executions do not count directly; the model decision that requested them does.

The outer loop is `turns.continuation`. It runs only after the agent emits a
terminal response, and only when `turns.continuation.stop_condition` asks for
another follow-up turn. `turns.continuation.max_continuations` is only a cap;
setting it without a stop condition is invalid configuration.

```yaml
turns:
  max_steps: 50
  continuation:
    max_continuations: 8
    context_policy: inherit
    stop_condition:
      type: prompt
      prompt: |
        Stop when test coverage is above 90%, or when there is no reasonable
        remaining test to add.
```

`context_policy` controls what the continuation evaluator sees:

- `inherit`: include the task summary plus operation effect details from the
  current turn.
- `summary`: include a compact task/progress summary and effect counts, without
  raw operation payloads.
- `new`: use clean evaluator context plus the same compact summary.

Local CLI sessions can also enable continuation for one submission without
changing agent configuration. `coder --goal "..."`, `agentsdk run . --goal
"..."`, and `/goal --max 20 "..."` submit the built-in `/goal` command, which
runs the goal text as the task and installs a prompt stop condition for that run
only. The command stops when the evaluator decides the goal is complete,
impossible, blocked, or no reasonable next action remains; otherwise it
continues until the requested cap is reached. `--goal` defaults to
`--max-continuations 20`.

### Sessions

Session documents name an executable profile and bind it to an agent.

```yaml
---
kind: session
name: support
description: Documentation support session.
agent: support
```

`agentsdk run . --session support` opens the named profile.

### Plugins And Connectors

Plugins contribute optional commands, operations, context providers,
datasources, and channel integrations. Connector instances name external auth
targets for connector-backed plugins. Native plugins can also be declared more
than once with `instance` and per-instance `config`.

```yaml
plugins:
  - kind: slack
  - kind: jira
  - kind: web
  - kind: gitlab
    instance: company-a
    config:
      base_url: https://gitlab.company-a.example
      auth:
        token_env: GITLAB_COMPANY_A_TOKEN

connectors:
  slack-main:
    kind: slack
  jira:
    kind: jira
```

Connector credentials live outside the app manifest. Manage connector-backed
credentials with `agentsdk connect`. Native GitLab instances declare a
`personal_access_token` env auth method and OAuth2 metadata. When
`auth.token_env` is set, it is the configured environment-variable address for
that instance. When it is omitted, the resolver probes the advertised aliases:
`GITLAB_ACCESS_TOKEN`, `GITLAB_PERSONAL_ACCESS_TOKEN`,
`GITLAB_PERSONAL_TOKEN`, and `GITLAB_TOKEN`. The runtime secret broker
authorizes `secret.use` on the logical plugin secret before an env resolver
reads any variable. The actual token is never part of the app manifest or model
context.

`instance` lets the same plugin type be declared more than once. The runtime
resolves each declaration independently, so `gitlab/company-a` and
`gitlab/company-b` can carry different config and contribute separately scoped
resources.

### Datasources

Datasources define searchable or retrievable entity sets.

```yaml
datasources:
  - name: local-docs
    kind: filesystem
    entities:
      - file.document
    description: Local markdown and text files.
    path: .
    include: ["*.md", "*.txt"]
    semantic:
      enabled: true
```

Connector-backed datasources reference a connector instance:

```yaml
datasources:
  - name: slack-main
    connector: slack-main
    kind: slack
    entities:
      - slack.user
      - slack.channel
      - slack.message
```

Native GitLab datasources reference the named GitLab plugin instance:

```yaml
datasources:
  - name: company-a-gitlab
    kind: gitlab
    entities:
      - gitlab.project
      - gitlab.merge_request
      - gitlab.merge_request_diff
      - gitlab.merge_request_note
      - gitlab.pipeline
      - gitlab.user
    config:
      instance: company-a
```

### Daemon Channels

The `daemon` block wires listeners to channels for `agentsdk serve`.

```yaml
daemon:
  listeners:
    - name: control
      type: http
      addr: agentsdk-local.sock
      auth:
        mode: local_socket
  channels:
    - name: local
      type: direct
      listener: control
      session: support
      access:
        mode: open
```

Slack channels use a connector instead of a local listener:

```yaml
daemon:
  channels:
    - name: slack-main
      type: slack
      connector: slack-main
      session: support
      access:
        mode: open
        allow_kinds: [dm, mention, thread_reply]
        default_trust: public
        sharing: strict
```

### Distribution And Builds

Distribution metadata describes a runnable package and optional Docker build
inputs.

```yaml
distribution:
  name: support-bot
  title: Support Bot
  default_session: support
  build:
    assets:
      - agentsdk.app.yaml
      - docs/**/*
    docker:
      image: support-bot
      tags: [latest]
```

Build with:

```bash
agentsdk build . --docker --tag support-bot:local
```

### Model Providers

Use `llm_providers` when an app needs local provider or model catalog entries
in addition to built-in providers.

```yaml
llm_providers:
  - name: localai
    display_name: Local AI
    models:
      - ref:
          name: local-model
        context_tokens: 1234
```

Inspect the merged catalog with:

```bash
agentsdk models .
```

## Agentdir

Agentdir loads a `.agents` resource tree. It is narrower than appconfig: it
does not describe daemon listeners, distribution builds, connector instances, or
app-level defaults. It is best for portable authored resources that an app can
discover or include.

Supported directories:

```text
.agents/
  agents/
  commands/
  workflows/
  skills/
```

### Agents

Agent files live at `.agents/agents/*.md`. YAML frontmatter configures the
agent, and the markdown body becomes the system prompt.

```markdown
---
name: reviewer
description: Review code changes.
model: openai/gpt-5.5
turns:
  max-steps: 30
tools: [file_read, git_diff]
skills: [code-review]
---
Review the current changes with attention to correctness, tests, and maintainability.
```

If `name` is omitted, the filename stem is used.

Agentdir uses kebab-case inside `turns`. The equivalent of an appconfig prompt
stop condition is:

```markdown
---
name: tester
model: openai/gpt-5.5
turns:
  max-steps: 50
  continuation:
    max-continuations: 8
    context-policy: inherit
    stop-condition:
      type: prompt
      prompt: |
        Stop when test coverage is above 90%, or when there is no reasonable
        remaining test to add.
tools: [file_read, git_diff, shell_exec]
---
Improve test coverage with focused, maintainable tests.
```

### Commands

Markdown prompt commands live at `.agents/commands/*.md`.

```markdown
---
description: Draft a release summary.
argument-hint: version
---
Write release notes for the requested version.
```

When a prompt command is invoked with positional arguments, the arguments are
appended to the prompt. Prompt bodies can also use Go template fields:
`{{ .Argument }}` for all arguments joined with spaces, `{{ index .Args 0 }}`
for one argument, and `{{ .Input }}` for structured command input.

YAML commands can target workflows:

```yaml
name: feature
description: Run the feature workflow.
policy:
  agent_callable: true
target:
  workflow: feature
```

### Workflows

Workflow files live at `.agents/workflows/*.yaml` or `.yml`.

```yaml
name: feature
description: Implement a feature in steps.
steps:
  - id: plan
    agent: reviewer
  - id: implement
    agent: coder
    depends-on: [plan]
```

Operation steps use the same DAG shape and execute through the composed
operation catalog:

```yaml
name: ops
steps:
  - id: fetch
    operation: web_fetch
    input:
      url: https://example.com
  - id: summarize
    operation: summarize
    depends-on: [fetch]
    error-policy: continue
```

### Skills

Skills live at `.agents/skills/<name>/SKILL.md`. Optional references live in
`.agents/skills/<name>/references/*.md`.

The standalone `coder` app requests project and user resource roots at startup.
It includes `<cwd>/.agents` plus global `$HOME/.agents` and `$HOME/.claude`
resources through the same local app discovery path used by appconfig. Use
`coder discover` to inspect the resulting resource set.

```markdown
---
name: code-review
description: Review code changes for defects and missing tests.
triggers: [review, code review]
allowed-tools: [git_diff, file_read]
---
Focus on concrete behavioral risks before style comments.
```

Reference files can add trigger-specific supporting material:

```markdown
---
trigger: concurrency
---
Check cancellation, shared state, and blocking behavior.
```

Skill and reference `triggers` are matched as case-insensitive text phrases
against incoming user turns. Matching skills and references are activated before
model context rendering, so their bodies are available to the agent in the same
turn.

## Choosing A Format

- Use appconfig for anything runnable with `agentsdk run`, `agentsdk serve`, or
  `agentsdk build`.
- Use appconfig for daemon channels, connectors, datasources, and distribution
  metadata.
- Use agentdir for portable authoring resources: markdown agents, prompt
  commands, workflows, and skills.
- Keep secrets out of both formats. Store connector credentials through
  `agentsdk connect` or provider-specific environment/auth files.
