# Configuration

Fluxplane core loads configuration from two filesystem-backed sources:
appconfig manifests and `.agents` resource trees. Both decode into resource
contribution bundles that the runtime composes into apps, sessions, agents,
commands, workflows, operations, datasources, model providers, and plugin
capabilities.

Use appconfig for runnable apps. Use agentdir when you want a portable resource
tree of authored agents, commands, workflows, and skills.

## Appconfig

Appconfig is the primary format for local apps and daemon distributions. A
project defines this file at its root:

- `fluxplane.yaml`

YAML is the usual choice because appconfig supports multi-document files. The
first document is the app document. Additional documents can define agents,
sessions, commands, workflows, operation declarations, datasources, and model
providers.

```yaml
kind: app
name: demo
description: Local demo app.
daemon:
  listeners:
    - name: local
      type: http
      addr: coder-demo.sock
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
fluxplane init .
```

### App Document

The `kind: app` document declares the app identity and the outer product wiring.
Common fields are:

- `name` and `description` for app identity.
- `default_agent` for the agent used when a session does not choose one.
  If the manifest defines exactly one local agent, this can be omitted and that
  agent is selected automatically.
- `plugins` for first-party plugin contribution bundles.
- `datasources` for configured data sources available to agents.
- `commands`, `workflows`, and `operations` for resource declarations embedded
  directly in the app document.
- `daemon` for listeners and channels used by `fluxplane serve` and remote
  channel clients.
- `runtime` for local runtime wiring; see [Runtime](#runtime).
- `distribution` for runnable/deployable package metadata and Docker build
  inputs.
- `semantic_search` for app-wide datasource indexing defaults.
- `identity` for canonical app users, provider identities, groups, and
  group/user trust used by identity resolution and authorization.
- `models` for app-local provider-agnostic model aliases, defaults, and model
  parameters.

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
      emails:
        - address: timo@company.org
          primary: true
        - address: timo.alias@company.org
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

Canonical users may also carry additional provider identities. For example,
after Slack resolves `timo@company.org`, `identity.current` can show both the
entry Slack identity and a configured or plugin-resolved GitLab identity:

```yaml
identity:
  users:
    - id: timo@company.org
      emails:
        - address: timo@company.org
          primary: true
        - address: timo.alias@company.org
      identities:
        - provider: gitlab/main
          provider_id: tfriedl
```

Configured `emails` are verified aliases by default and can link the same
canonical user across providers whose visible email addresses differ. Set
`verified: false` for an email that should be displayed only and not used for
directory matching or plugin account lookup. GitLab identity lookup only uses
configured verified aliases and public GitLab user search; a normal GitLab token
cannot read another user's private email addresses unless the user made the
email public, the token has administrator access, or the token belongs to that
same user.

For Slack apps, sender identity and trust should come from resolved core
identity context, not from Slack message metadata. Slack-specific audience trust
is a sharing constraint for shared conversations and stays separate from the
sender's effective trust; one-to-one DMs omit audience trust.

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
`fluxplane run`, `fluxplane serve`, and generated deployments.

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
    env_files:
      - .env
      - .env.local
    roots:
      - name: tmp
        path: /tmp/agentruntime-demo
        access: read_write
        create: true
        env_files:
          - .env.tmp
    scratch_root: tmp
```

Fields:

- `roots[].name` is the logical root name used in paths such as
  `@tmp/report.txt`.
- `roots[].path` is the host directory exposed to the runtime.
- `roots[].access` accepts `read_write` or `read_only`; omitted access defaults
  to `read_write`.
- `roots[].create` creates the directory at launch when it does not exist.
- `env_files` lists root workspace env files or globs to load into the runtime
  system boundary. Repeated CLI `--env-file` values are appended to this root
  list only.
- `roots[].env_files` lists env files or globs scoped to that named root.
- `scratch_root` chooses the named root used by runtime-owned scratch
  directories, such as generated image outputs.

With the example above, `file_create` may write `@tmp/report.txt` or
`/tmp/agentruntime-demo/report.txt`. It still cannot write arbitrary files
elsewhere under `/tmp`, and symlinks that escape the configured root are
rejected.

Env files are opt-in. There is no implicit `.env` loading. Within one
workspace, env files are applied in order and later values win; values are not
merged between root and named workspaces. Child processes launched through
`runtime/system.System` receive only the minimal runtime baseline plus the env
set for their resolved workspace root.

### Agents

Agent documents define LLM-backed runtime agents.

```yaml
---
kind: agent
name: support
model: openai/gpt-5.5
turns:
  max_steps: 50
uses:
  - datasource
skills:
  - triage
system: |
  Help the user answer questions from the configured documentation.
```

`uses` references activation sets contributed by the app or selected plugins.
During normalization, each referenced set expands to its declared operation,
context, datasource, and skill targets. Schema autocomplete is populated from
the activation sets available in the current app context.

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

Local CLI sessions can also set a durable thread goal without changing agent
configuration. `fluxplane run . --goal "..."` and `/goal "..."` submit the
native goal plugin's `/goal` command, which records the goal on the current
thread instead of running it as a one-shot prompt. Product CLIs such as `coder`
may expose the same runtime goal feature with product-specific flags.
current goal is rendered into session context until it is cleared, paused goals
do not drive continuation, and reached goals remain visible until `/goal clear`.

The `/goal` command accepts only lifecycle arguments:

- `/goal <goal>` sets or replaces the durable thread goal.
- `/goal` or `/goal status` shows the current goal.
- `/goal pause` pauses goal-driven continuation.
- `/goal resume` resumes goal-driven continuation.
- `/goal clear` clears the current goal from context.

### Sessions

Session documents name an executable profile and bind it to an agent.

```yaml
---
kind: session
name: support
description: Documentation support session.
agent: support
```

`fluxplane run . --session support` opens the named profile.

### Plugins And Auth

Plugins contribute optional commands, operations, context providers,
datasources, and channel integrations. The `plugins` map key is the plugin
instance name. `kind` defaults to that key and is only needed when the instance
name differs from the plugin implementation. Credentials are stored outside the
app manifest and resolved by the runtime auth/secret providers for the selected
app or product scope.

```yaml
plugins:
  slack-main:
    kind: slack
    auth:
      method: token
  jira:
    cloud_id: your-atlassian-cloud-id
    site_url: https://example.atlassian.invalid
    auth:
      method: oauth2
  confluence:
    cloud_id: your-atlassian-cloud-id
    site_url: https://example.atlassian.invalid
    auth:
      method: oauth2
  web: ~
  company-a:
    kind: gitlab
    base_url: https://gitlab.company-a.example
    auth:
      token_env: GITLAB_COMPANY_A_TOKEN

```

Credentials live outside the app manifest. Native Slack can use a stored bot
token or token environment variable selected by the runtime secret provider;
`fluxplane auth status` reports plugin readiness for the selected app without
printing secret values. Product CLIs may add setup commands for their own auth
stores, but Fluxplane app manifests should only declare the auth method and
logical secret address.
Slack daemon channels also require an app token for Socket Mode. Slack channel
API calls use `auth.channel_token: auto` by default, which prefers the bot token
and falls back to the user token; set `auth.channel_token` to `bot_token` or
`user_token` to require one explicitly. Native Jira and Confluence can use
Atlassian OAuth2 stored credentials, service-account bearer tokens, or Basic API
tokens depending on plugin configuration. Service-account deployments can set `auth.method: token` and `auth.token_env` for a bearer
token environment variable. Jira and Confluence scoped service-account API-token
deployments can set `JIRA_API_TOKEN` or `CONFLUENCE_API_TOKEN` and `cloud_id`;
Basic API-token auth additionally needs an account email such as `JIRA_EMAIL`
and `site_url` or `base_url` so requests use the site REST endpoint. Native
GitLab instances declare a
`personal_access_token` env auth method and OAuth2 metadata. When GitLab
`auth.token_env` is set, it is the configured environment-variable address for
that instance. When it is omitted, the resolver probes the advertised aliases:
`GITLAB_ACCESS_TOKEN`, `GITLAB_PERSONAL_ACCESS_TOKEN`,
`GITLAB_PERSONAL_TOKEN`, and `GITLAB_TOKEN`. The runtime secret broker
authorizes `secret.use` on the logical plugin secret before an env resolver
reads any variable. The actual token is never part of the app manifest or model
context.

See [Atlassian Auth](plugins/atlassian.md) for the difference between scoped access
tokens, Basic API tokens, OAuth2, and domain-verification caveats.

`instance` lets the same plugin type be declared more than once. The runtime
resolves each declaration independently, so `gitlab/company-a` and
`gitlab/company-b` can carry different config and contribute separately scoped
resources.

### Datasources

Datasources define searchable or retrievable entity sets.

```yaml
datasource:
  index:
    concurrency: 4
    freshness: 15m
  datasources:
    - name: local-docs
      kind: filesystem
      index:
        enabled: true
        freshness: 15m
      entities:
        - file.document
      description: Local markdown and text files.
      path: .
      include: ["*.md", "*.txt"]
      semantic:
        enabled: true
```

`datasource.index` holds global datasource indexing defaults. Set
`datasource.datasources[*].index.enabled: true` when a provider should use its
local datasource index for search where supported. The per-datasource
`index.freshness` value overrides the global freshness. A freshness of `0`
always rebuilds.

The datasource index can hold structured field records, semantic vector
documents, or both, depending on the entity capabilities declared by the
provider. Build the index with `fluxplane datasource index build`; use
`--phase fields` or `--phase semantic` to run only one indexing phase.
`--force` and `--full` bypass freshness checks. Semantic documents are queued
by build and embedded later with the datasource embed worker or the background
embed worker started by `fluxplane serve`. `fluxplane serve` starts
background warmup for indexed datasources with the configured concurrency and
logs start, fresh-skip, page, complete, and failure progress per entity.

Native Slack datasources reference the named Slack plugin instance:

```yaml
datasource:
  datasources:
    - name: slack-main
      kind: slack
      entities:
        - slack.user
        - slack.channel
        - slack.message
        - slack.thread_message
      config:
        instance: slack-main
```

Native GitLab datasources reference the named GitLab plugin instance:

```yaml
datasource:
  datasources:
    - name: company-a-gitlab
      kind: gitlab
      index:
        enabled: true
        freshness: 15m
      entities:
        - gitlab.project
        - gitlab.activity
        - gitlab.merge_request
        - gitlab.merge_request_diff
        - gitlab.merge_request_diff_line
        - gitlab.merge_request_note
        - gitlab.merge_request_approval
        - gitlab.merge_request_change
        - gitlab.discussion
        - gitlab.award_emoji
        - gitlab.pipeline
        - gitlab.repository_tree
        - gitlab.repository_file
        - gitlab.compare
        - gitlab.blame
        - gitlab.blob_search
        - gitlab.project_language
        - gitlab.project_contributor
        - gitlab.job
        - gitlab.job_trace
        - gitlab.snippet
        - gitlab.snippet_file
        - gitlab.user
        - gitlab.group
      config:
        instance: company-a
```

GitLab currently indexes `gitlab.project`, `gitlab.user`, and `gitlab.group`
through structured fields only. Other GitLab entities remain live/provider
searched until they explicitly declare an index capability.

Native Atlassian datasources reference the matching Jira or Confluence plugin
instance and use the same Atlassian auth setup:

```yaml
datasource:
  datasources:
    - name: jira
      kind: jira
      entities:
        - jira.issue
        - jira.project
    - name: confluence
      kind: confluence
      index:
        enabled: true
      entities:
        - confluence.page
        - confluence.space
```

### Daemon Channels

The `daemon` block wires listeners to channels for `fluxplane serve`.

```yaml
daemon:
  listeners:
    - name: control
      type: http
      addr: coder-local.sock
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

Slack channels use a native plugin instance instead of a local listener:

```yaml
daemon:
  channels:
    - name: slack-main
      type: slack
      instance: slack-main
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
runtime:
  data:
    store:
      kind: mysql
      dsn_env: AGENTRUNTIME_DATASTORE_MYSQL_DSN
  events:
    store:
      kind: nats
      dsn_env: AGENTRUNTIME_EVENTSTORE_NATS_DSN
      stream: AGENTRUNTIME_EVENTS
      subject: agentruntime.events.log
      create_stream: true
models:
  default: smart_model
  available:
    - provider: openrouter
      model: openai/gpt-5.5
      aliases: [smart_model, gpt5]
      params:
        effort: medium
distribution:
  name: support-bot
  title: Support Bot
  deploy:
    model: smart_model
  build:
    assets:
      - fluxplane.yaml
      - docs/**/*
    docker:
      image: support-bot
      tags: [latest]
```

Build with:

```bash
fluxplane build . --target docker-base --base-image fluxplane/fluxplane-base:local
fluxplane build . --image support-bot:local
fluxplane deploy . --target docker-compose --image support-bot:local
fluxplane deploy . --target kubernetes --namespace ai-bots --image support-bot:local
fluxplane undeploy . --target kubernetes --namespace ai-bots
```

For Kubernetes deploys, the default registry mode is `auto`: k3d contexts use
`k3d image import`, while other Kubernetes contexts deploy a temporary registry
service into the target namespace, push the app image through a local
port-forward, and reference that registry from the app Deployment. Use
`--registry-mode k3d` or `--registry-mode namespace` to select either path
explicitly, or `--registry-mode external --registry <registry-prefix>` for ECR
or another registry that cluster nodes can pull from. Root workspace `env_files` are
converted into a Kubernetes Secret and injected into the app container
environment. Runtime backend DSN variables generated for MySQL or NATS are set
directly on the app Deployment and override matching env-file keys. Named root
`env_files` are not supported by Kubernetes deploy until workspace roots are
mounted there. Generated Kubernetes manifests are written to
`.deploy/kubernetes.yaml`, and `.deploy/` is added to the app's `.gitignore`
when the manifest is written.

`fluxplane undeploy` deletes generated app resources and preserves persistent
Docker volumes or Kubernetes PVCs by default. Add `--volumes` only when runtime
backend state should be removed too.

### Model Providers

Use `models` to define provider-agnostic model names for agents and deployment
defaults. Agents should refer to aliases from this registry instead of
embedding provider-specific model settings.

```yaml
models:
  default: smart_model
  available:
    - provider: openrouter
      model: openai/gpt-5.5
      aliases: [smart_model, gpt5]
      params:
        effort: medium

distribution:
  deploy:
    model: smart_model
```

Inspect the merged catalog with:

```bash
fluxplane models .
```

## Agentdir

Agentdir loads a `.agents` resource tree. It is narrower than appconfig: it
does not describe daemon listeners, distribution builds, plugin auth scope, or
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
    depends_on: [plan]
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
    depends_on: [fetch]
    input_map:
      page: fetch
      request: $input
    error_policy: continue
```

`input_map` builds the step input from earlier workflow values. Use `$input` for
the workflow input and a prior step id, such as `fetch`, for that step's output.

### Skills

Skills live at `.agents/skills/<name>/SKILL.md`. Optional references live in
`.agents/skills/<name>/references/*.md`.

The standalone `coder` product requests project and user resource roots at
startup. Generic Fluxplane apps can do the same by including the relevant
resource roots in app discovery. Use `fluxplane discover` to inspect the resource
set that a Fluxplane app will load.

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

- Use appconfig for anything runnable with `fluxplane run`, `fluxplane serve`, or
  `fluxplane build`.
- Use appconfig for daemon channels, datasources, plugins, and distribution
  metadata.
- Use agentdir for portable authoring resources: markdown agents, prompt
  commands, workflows, and skills.
- Keep secrets out of both formats. Store plugin credentials through runtime
  auth providers, environment variables, or provider-specific auth files.
