package appconfig

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fluxplane/agentruntime/core/agent"
	"github.com/fluxplane/agentruntime/core/invocation"
	corellm "github.com/fluxplane/agentruntime/core/llm"
	"github.com/fluxplane/agentruntime/core/operation"
	"github.com/fluxplane/agentruntime/core/policy"
	"github.com/fluxplane/agentruntime/core/resource"
	"github.com/fluxplane/agentruntime/core/workflow"
	appcompose "github.com/fluxplane/agentruntime/orchestration/app"
)

func TestDecodeManifestLoadsEngineerStyleManifest(t *testing.T) {
	data := []byte(`{
  "name": "engineer",
  "description": "Coding agent app",
  "default_agent": {"name": "main"},
  "sources": [{"location": ".agents"}],
  "discovery": {
    "include_global_user_resources": true,
    "include_external_ecosystems": false,
    "allow_remote": false,
    "trust_store_dir": ".agentsdk"
  },
  "model_policy": {
    "use_case": "agentic_coding",
    "source_api": "auto"
  },
  "plugins": [
    {"kind": "git"},
    {"kind": "browser", "instance": "browser-ci", "config": {"headless": true}}
  ]
}`)

	bundle, err := DecodeManifest("/repo/agentsdk.app.json", data)
	if err != nil {
		t.Fatalf("DecodeManifest: %v", err)
	}

	if bundle.Source.Scope != resource.ScopeProject {
		t.Fatalf("source scope = %q, want %q", bundle.Source.Scope, resource.ScopeProject)
	}
	if bundle.Source.Trust.Kind != policy.TrustSource || bundle.Source.Trust.Level != policy.TrustVerified {
		t.Fatalf("source trust = %#v, want verified source trust", bundle.Source.Trust)
	}
	if len(bundle.Apps) != 1 {
		t.Fatalf("apps len = %d, want 1", len(bundle.Apps))
	}

	app := bundle.Apps[0]
	if app.Name != "engineer" {
		t.Fatalf("app name = %q, want engineer", app.Name)
	}
	if app.DefaultAgent != (agent.Ref{Name: "main"}) {
		t.Fatalf("default agent = %#v, want main", app.DefaultAgent)
	}
	if len(app.Sources) != 1 || app.Sources[0].Location != ".agents" {
		t.Fatalf("sources = %#v, want .agents source", app.Sources)
	}
	if !app.Discovery.IncludeGlobalUserResources || app.Discovery.IncludeExternalEcosystems || app.Discovery.AllowRemote {
		t.Fatalf("discovery flags = %#v, want engineer defaults", app.Discovery)
	}
	if app.Discovery.TrustStoreDir != ".agentsdk" {
		t.Fatalf("trust store dir = %q, want .agentsdk", app.Discovery.TrustStoreDir)
	}
	if app.Model.UseCase != "agentic_coding" || app.Model.SourceAPI != "auto" {
		t.Fatalf("model policy = %#v, want use_case/source_api", app.Model)
	}
	if len(app.Plugins) != 2 {
		t.Fatalf("app plugins len = %d, want 2", len(app.Plugins))
	}
	if app.Plugins[0].Kind != "git" {
		t.Fatalf("plugin[0] = %#v, want git", app.Plugins[0])
	}
	if app.Plugins[1].Kind != "browser" || app.Plugins[1].Instance != "browser-ci" || app.Plugins[1].Config["headless"] != true {
		t.Fatalf("plugin[1] = %#v, want browser with config", app.Plugins[1])
	}
	if len(bundle.Plugins) != 2 {
		t.Fatalf("bundle plugins len = %d, want 2", len(bundle.Plugins))
	}
	if bundle.Plugins[1].Name != "browser" || bundle.Plugins[1].Instance != "browser-ci" || bundle.Plugins[1].Config["headless"] != true {
		t.Fatalf("bundle plugin[1] = %#v, want browser with config", bundle.Plugins[1])
	}
}

func TestDecodeManifestLoadsYAMLManifest(t *testing.T) {
	data := []byte(`
name: engineer
default_agent:
  name: main
identity:
  users:
    - id: timo@company.org
      username: timo
      emails:
        - address: timo@company.org
          primary: true
        - address: timo.alias@company.org
          verified: true
      identities:
        - provider: slack
          provider_id: U123
  groups:
    - id: admins
      members: [timo@company.org]
      trust: operator
  rules:
    - match:
        provider: slack
        resolution: resolved
      groups: [users]
sources:
  - location: .agents
model_policy:
  provider: openai
  approved_only: true
plugins:
  - kind: memory
    config:
      scope: project
`)

	bundle, err := DecodeManifest("agentsdk.app.yaml", data)
	if err != nil {
		t.Fatalf("DecodeManifest: %v", err)
	}

	app := bundle.Apps[0]
	if app.DefaultAgent.Name != "main" {
		t.Fatalf("default agent = %#v, want main", app.DefaultAgent)
	}
	if app.Model.Provider != "openai" || app.Model.ApprovedOnly == nil || !*app.Model.ApprovedOnly {
		t.Fatalf("model policy = %#v, want provider and approved_only", app.Model)
	}
	if len(app.Identity.Users) != 1 || app.Identity.Users[0].ID != "timo@company.org" || app.Identity.Users[0].Identities[0].ProviderID != "U123" {
		t.Fatalf("identity users = %#v, want canonical Slack user", app.Identity.Users)
	}
	if len(app.Identity.Users[0].Emails) != 2 || !app.Identity.Users[0].Emails[0].Primary || !app.Identity.Users[0].Emails[0].Verified || app.Identity.Users[0].Emails[1].Address != "timo.alias@company.org" {
		t.Fatalf("identity emails = %#v, want verified primary and alias emails", app.Identity.Users[0].Emails)
	}
	if len(app.Identity.Groups) != 1 || app.Identity.Groups[0].ID != "admins" || app.Identity.Groups[0].Trust != "operator" {
		t.Fatalf("identity groups = %#v, want admins operator group", app.Identity.Groups)
	}
	if len(app.Identity.Rules) != 1 || app.Identity.Rules[0].Match.Provider != "slack" || app.Identity.Rules[0].Match.Resolution != "resolved" || app.Identity.Rules[0].Groups[0] != "users" {
		t.Fatalf("identity rules = %#v, want Slack resolved users rule", app.Identity.Rules)
	}
	if got := bundle.Plugins[0].Config["scope"]; got != "project" {
		t.Fatalf("plugin scope = %#v, want project", got)
	}
}

func TestDecodeManifestPreservesScalarShorthands(t *testing.T) {
	bundle, err := DecodeManifest("agentsdk.app.yaml", []byte(`
name: engineer
default_agent: main
sources: [.agents]
plugins: [git]
`))
	if err != nil {
		t.Fatalf("DecodeManifest: %v", err)
	}
	app := bundle.Apps[0]
	if app.DefaultAgent.Name != "main" {
		t.Fatalf("default agent = %#v, want main", app.DefaultAgent)
	}
	if len(app.Sources) != 1 || app.Sources[0].Location != ".agents" {
		t.Fatalf("sources = %#v, want .agents", app.Sources)
	}
	if len(app.Plugins) != 1 || app.Plugins[0].Kind != "git" {
		t.Fatalf("plugins = %#v, want git", app.Plugins)
	}
}

func TestDecodeManifestLoadsModelRegistry(t *testing.T) {
	data := []byte(`
kind: app
name: engineer
models:
  default: smart_model
  available:
    - provider: openrouter
      model: openai/gpt-5.5
      aliases: [smart_model, gpt5]
      params:
        effort: medium
    - provider: codex
      model: gpt-5.5
      aliases: [codex]
`)

	file, err := DecodeFile("agentsdk.app.yaml", data)
	if err != nil {
		t.Fatalf("DecodeFile: %v", err)
	}
	bundle := file.Bundle
	if len(bundle.Apps) != 1 || bundle.Apps[0].Model.Model != "smart_model" || bundle.Apps[0].Model.Provider != "" {
		t.Fatalf("app model = %#v, want provider-agnostic smart_model", bundle.Apps)
	}
	if len(bundle.LLMProviders) != 2 {
		t.Fatalf("providers len = %d, want 2", len(bundle.LLMProviders))
	}
	var openrouterModel corellm.ModelSpec
	for _, provider := range bundle.LLMProviders {
		if provider.Name == "openrouter" && len(provider.Models) == 1 {
			openrouterModel = provider.Models[0]
		}
	}
	if openrouterModel.Ref.Name != "openai/gpt-5.5" || openrouterModel.Params.ReasoningEffort != "medium" {
		t.Fatalf("openrouter model = %#v, want model params", openrouterModel)
	}
	if len(bundle.LLMModelAliases) != 3 {
		t.Fatalf("aliases len = %d, want smart_model, gpt5, and codex", len(bundle.LLMModelAliases))
	}
	got := map[string]string{}
	for _, alias := range bundle.LLMModelAliases {
		got[alias.Name] = alias.Target.String()
	}
	if got["smart_model"] != "openrouter/openai/gpt-5.5" || got["gpt5"] != "openrouter/openai/gpt-5.5" {
		t.Fatalf("aliases = %#v, want openrouter targets", got)
	}
	if got["codex"] != "codex/gpt-5.5" {
		t.Fatalf("codex alias = %q, want codex/gpt-5.5", got["codex"])
	}
}

func TestDecodeManifestRejectsDuplicateModelAliasTargets(t *testing.T) {
	data := []byte(`
kind: app
name: engineer
models:
  available:
    - provider: openrouter
      model: openai/gpt-5.5
      aliases: [smart_model]
    - provider: codex
      model: gpt-5.5
      aliases: [smart_model]
`)

	_, err := DecodeManifest("agentsdk.app.yaml", data)
	if err == nil || !strings.Contains(err.Error(), `duplicate alias "smart_model"`) {
		t.Fatalf("DecodeManifest error = %v, want duplicate alias", err)
	}
}

func TestDecodeManifestRejectsUnknownDeployModelReference(t *testing.T) {
	data := []byte(`
kind: app
name: engineer
models:
  default: smart_model
  available:
    - provider: openrouter
      model: openai/gpt-5.5
      aliases: [smart_model]
distribution:
  deploy:
    model: typo_model
`)

	_, err := DecodeManifest("agentsdk.app.yaml", data)
	if err == nil || !strings.Contains(err.Error(), `distribution.deploy.model "typo_model"`) {
		t.Fatalf("DecodeManifest error = %v, want unknown deploy model", err)
	}
}

func TestDecodeManifestLoadsSemanticSearchConfig(t *testing.T) {
	data := []byte(`
kind: app
name: engineer
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
semantic_search:
  enabled: true
  embeddings:
    provider: openai
    model: text-embedding-3-large
  store:
    kind: json
    path: .agents/index/datasources.json
  defaults:
    chunking:
      strategy: markdown_or_text
      target_tokens: 900
      overlap_tokens: 120
    retrieval:
      mode: hybrid
      limit: 8
      min_score: 0.3
datasource:
  index:
    concurrency: 4
    freshness: 15m
  datasources:
    - name: local-docs
      kind: filesystem
      index:
        enabled: true
        freshness: 5m
      entities: [file.document]
      semantic:
        enabled: true
        entities:
          file.document:
            corpus:
              title_fields: [title]
              body_fields: [content]
              metadata_fields: [path]
            incremental:
              updated_at_field: updated_at
`)

	file, err := DecodeFile("agentsdk.app.yaml", data)
	if err != nil {
		t.Fatalf("DecodeFile: %v", err)
	}
	bundle := file.Bundle
	app := bundle.Apps[0]
	if file.Runtime.Data.Store.Kind != "mysql" || file.Runtime.Data.Store.DSNEnv != "AGENTRUNTIME_DATASTORE_MYSQL_DSN" {
		t.Fatalf("runtime data store = %#v, want mysql dsn env", file.Runtime.Data.Store)
	}
	if file.Runtime.Events.Store.Kind != "nats" || file.Runtime.Events.Store.DSNEnv != "AGENTRUNTIME_EVENTSTORE_NATS_DSN" || file.Runtime.Events.Store.Stream != "AGENTRUNTIME_EVENTS" || file.Runtime.Events.Store.Subject != "agentruntime.events.log" || !file.Runtime.Events.Store.CreateStream {
		t.Fatalf("runtime event store = %#v, want nats dsn env", file.Runtime.Events.Store)
	}
	if !app.SemanticSearch.Enabled || app.SemanticSearch.Embeddings.Model != "text-embedding-3-large" {
		t.Fatalf("semantic search = %#v, want enabled embedding model", app.SemanticSearch)
	}
	if app.SemanticSearch.Defaults.Chunking.TargetTokens != 900 || app.SemanticSearch.Defaults.Retrieval.Mode != "hybrid" {
		t.Fatalf("semantic defaults = %#v", app.SemanticSearch.Defaults)
	}
	if app.Datasource.Index.Concurrency != 4 || app.Datasource.Index.Freshness != "15m" {
		t.Fatalf("datasource index defaults = %#v", app.Datasource.Index)
	}
	ds := bundle.Datasources[0]
	if !ds.Semantic.Enabled {
		t.Fatalf("datasource semantic = %#v, want enabled", ds.Semantic)
	}
	if !ds.Index.Enabled || ds.Index.Freshness != "5m" {
		t.Fatalf("datasource index = %#v, want enabled freshness", ds.Index)
	}
	entity := ds.Semantic.Entities["file.document"]
	if len(entity.Corpus.BodyFields) != 1 || entity.Corpus.BodyFields[0] != "content" {
		t.Fatalf("entity corpus = %#v, want body content", entity.Corpus)
	}
	if entity.Incremental.UpdatedAtField != "updated_at" {
		t.Fatalf("incremental = %#v, want updated_at", entity.Incremental)
	}
}

func TestDecodeFileLoadsDistributionBuildConfig(t *testing.T) {
	data := []byte(`
kind: app
name: engineer
distribution:
  name: engineer-cli
  title: Engineer CLI
  description: Built engineer distribution.
  author: Fluxplane
  version: 1.2.3
  default_session: main
  default_conversation: engineer-local
  default_model:
    provider: openai
    model: gpt-5.5
    use_case: coding
  surfaces:
    cli: true
    one_shot: true
    serve: true
  build:
    assets:
      - agentsdk.app.yaml
      - .agents/**
      - docs/**/*.md
    docker: {}
  deploy:
    model: smart_model
  metadata:
    tier: local
  commands:
    - name: ask
      description: Ask the engineer agent.
`)

	file, err := DecodeFile("agentsdk.app.yaml", data)
	if err != nil {
		t.Fatalf("DecodeFile: %v", err)
	}
	spec := file.Distribution
	if spec.Name != "engineer-cli" || spec.Title != "Engineer CLI" || spec.Version != "1.2.3" {
		t.Fatalf("distribution metadata = %#v", spec)
	}
	if spec.DefaultSession.Name != "main" || spec.DefaultConversation.ID != "engineer-local" {
		t.Fatalf("distribution defaults = %#v", spec)
	}
	if spec.DefaultModel.Provider != "openai" || spec.DefaultModel.Model != "gpt-5.5" || spec.DefaultModel.UseCase != "coding" {
		t.Fatalf("default model = %#v", spec.DefaultModel)
	}
	if !spec.Surfaces.CLI || !spec.Surfaces.OneShot || !spec.Surfaces.Serve {
		t.Fatalf("surfaces = %#v", spec.Surfaces)
	}
	if len(spec.Build.Assets) != 3 || spec.Build.Assets[2] != "docs/**/*.md" {
		t.Fatalf("build assets = %#v", spec.Build.Assets)
	}
	if spec.Build.Docker == nil {
		t.Fatalf("docker build config is nil, want configured empty docker target")
	}
	if spec.Deploy.Model != "smart_model" {
		t.Fatalf("deploy = %#v, want smart_model", spec.Deploy)
	}
	if spec.Metadata["tier"] != "local" {
		t.Fatalf("metadata = %#v", spec.Metadata)
	}
	if len(spec.Commands) != 1 || spec.Commands[0].Name != "ask" {
		t.Fatalf("commands = %#v", spec.Commands)
	}
}

func TestDecodeFileLoadsRewriteNativeSlackManifest(t *testing.T) {
	data := []byte(`
kind: app
name: slack-bot
default_agent:
  name: slack_bot
plugins:
  - kind: slack
    instance: slack-bot
    config:
      auth:
        method: bot_token
datasource:
  datasources:
    - name: slack-bot
      kind: slack
      config:
        instance: slack-bot
      entities: [slack.user]
    - name: local-docs
      kind: filesystem
      entities: [file.document]
      path: docs
      include: ["*.md"]
daemon:
  listeners:
    - name: control
      type: http
      addr: coder-slack-bot.sock
  channels:
    - name: slack-main
      type: slack
      instance: slack-bot
      session: slack-main
      access:
        mode: open
        allow_kinds: [dm, mention, thread_reply]
        default_trust: public
        sharing: strict
---
kind: session
name: slack-main
agent: slack_bot
---
kind: agent
name: slack_bot
model: openai/gpt-5.5
turns:
  max_steps: 50
  continuation:
    max_continuations: 50
    stop_condition:
      type: prompt
      prompt: Finish when the support answer is complete.
tools: [channel_send]
context: [datasource.catalog]
datasources: [slack-bot, local-docs]
system: |
  You are a Slack bot.
`)

	file, err := DecodeFile("/repo/examples/slack-bot/agentsdk.app.yaml", data)
	if err != nil {
		t.Fatalf("DecodeFile: %v", err)
	}
	if len(file.Bundle.Apps) != 1 || file.Bundle.Apps[0].Name != "slack-bot" {
		t.Fatalf("apps = %#v", file.Bundle.Apps)
	}
	if len(file.Bundle.Sessions) != 1 || file.Bundle.Sessions[0].Name != "slack-main" {
		t.Fatalf("sessions = %#v", file.Bundle.Sessions)
	}
	if len(file.Bundle.Agents) != 1 || file.Bundle.Agents[0].Inference.Model != "openai/gpt-5.5" {
		t.Fatalf("agents = %#v", file.Bundle.Agents)
	}
	if file.Bundle.Agents[0].Turns.Continuation.MaxContinuations != 50 {
		t.Fatalf("max continuations = %d, want 50", file.Bundle.Agents[0].Turns.Continuation.MaxContinuations)
	}
	if file.Bundle.Agents[0].Turns.MaxSteps != 50 {
		t.Fatalf("max steps = %d, want 50", file.Bundle.Agents[0].Turns.MaxSteps)
	}
	if file.Bundle.Agents[0].Turns.Continuation.StopCondition.Type != "prompt" {
		t.Fatalf("stop condition = %#v, want prompt", file.Bundle.Agents[0].Turns.Continuation.StopCondition)
	}
	if len(file.Bundle.Agents[0].Datasources) != 2 || file.Bundle.Agents[0].Datasources[0].Name != "slack-bot" {
		t.Fatalf("agent datasources = %#v", file.Bundle.Agents[0].Datasources)
	}
	if len(file.Bundle.Agents[0].Context) != 1 || file.Bundle.Agents[0].Context[0].Name != "datasource.catalog" {
		t.Fatalf("agent context = %#v", file.Bundle.Agents[0].Context)
	}
	if len(file.Bundle.Datasources) != 2 || file.Bundle.Datasources[1].Config["path"] != "docs" || file.Bundle.Datasources[1].Config["include"] != "*.md" {
		t.Fatalf("datasources = %#v", file.Bundle.Datasources)
	}
	if len(file.Bundle.Datasources[0].Entities) != 1 || file.Bundle.Datasources[0].Entities[0] != "slack.user" {
		t.Fatalf("datasource entities = %#v", file.Bundle.Datasources[0].Entities)
	}
	if len(file.Daemon.Listeners) != 1 || file.Daemon.Listeners[0].Addr != "coder-slack-bot.sock" {
		t.Fatalf("listeners = %#v", file.Daemon.Listeners)
	}
	if len(file.Daemon.Channels) != 1 || file.Daemon.Channels[0].Access.AllowKinds[2] != "thread_reply" {
		t.Fatalf("channels = %#v", file.Daemon.Channels)
	}
	if len(file.Connectors) != 0 {
		t.Fatalf("connectors = %#v, want none", file.Connectors)
	}
	if file.Daemon.Channels[0].Instance != "slack-bot" {
		t.Fatalf("channel instance = %q, want slack-bot", file.Daemon.Channels[0].Instance)
	}
}

func TestDecodeFileLoadsTopLevelResources(t *testing.T) {
	file, err := DecodeFile("agentsdk.app.yaml", []byte(`
kind: app
name: resource-app
commands:
  - name: feature
    description: Run feature workflow.
    policy:
      agent_callable: true
    input_schema:
      type: object
      properties:
        description:
          type: string
    target:
      workflow: feature
      input: "{{ .description }}"
  - name: echo
    target:
      operation: echo
workflows:
  - name: feature
    steps:
      - id: plan
        agent: reviewer
      - id: run
        operation: echo
        input:
          text: hello
        depends-on: [plan]
        error-policy: continue
operations:
  - name: echo
    description: Echo input.
    semantics:
      determinism: deterministic
      effects: [none]
      risk: low
observations:
  observers:
    - name: kubernetes.context
      phase: turn
      observable_kinds: [kubernetes.context]
      disabled: true
  signal_derivers:
    - name: kubernetes.signals
      observation_kinds: [kubernetes.context]
      signals:
        - kind: integration.available
          target: kubernetes
reactions:
  - name: kubernetes-available
    when:
      signal: integration.available
      target: kubernetes
    actions:
      - kind: activate_skill
        skill:
          name: kubernetes
      - kind: enable_context_provider
        context_provider:
          name: kubernetes.context
`))
	if err != nil {
		t.Fatalf("DecodeFile: %v", err)
	}
	if got, want := len(file.Bundle.Commands), 2; got != want {
		t.Fatalf("commands len = %d, want %d", got, want)
	}
	if file.Bundle.Commands[0].Target.Kind != invocation.TargetWorkflow || file.Bundle.Commands[0].Target.Workflow != "feature" {
		t.Fatalf("feature command target = %#v", file.Bundle.Commands[0].Target)
	}
	if file.Bundle.Commands[1].Target.Kind != invocation.TargetOperation || file.Bundle.Commands[1].Target.Operation.Name != "echo" {
		t.Fatalf("echo command target = %#v", file.Bundle.Commands[1].Target)
	}
	if got, want := file.Bundle.Commands[0].Policy.AllowedCallers, 2; len(got) != want {
		t.Fatalf("allowed callers len = %d, want %d", len(got), want)
	}
	if len(file.Bundle.Commands[0].Input.Schema.Data) == 0 {
		t.Fatalf("feature command input schema is empty")
	}
	if got, want := len(file.Bundle.Workflows), 1; got != want {
		t.Fatalf("workflows len = %d, want %d", got, want)
	}
	flow := file.Bundle.Workflows[0]
	if flow.Steps[0].Kind != workflow.StepAgent || flow.Steps[0].Agent.Name != "reviewer" {
		t.Fatalf("first step = %#v, want reviewer agent", flow.Steps[0])
	}
	if flow.Steps[1].Kind != workflow.StepOperation || flow.Steps[1].Operation.Name != "echo" {
		t.Fatalf("second step = %#v, want echo operation", flow.Steps[1])
	}
	if flow.Steps[1].ErrorPolicy != workflow.StepErrorContinue {
		t.Fatalf("error policy = %q, want continue", flow.Steps[1].ErrorPolicy)
	}
	if got, want := len(file.Bundle.Operations), 1; got != want {
		t.Fatalf("operations len = %d, want %d", got, want)
	}
	if file.Bundle.Operations[0].Ref.Name != "echo" || file.Bundle.Operations[0].Semantics.Risk != operation.RiskLow {
		t.Fatalf("operation = %#v", file.Bundle.Operations[0])
	}
	if len(file.Bundle.Observers) != 1 || file.Bundle.Observers[0].Name != "kubernetes.context" {
		t.Fatalf("observers = %#v, want kubernetes.context", file.Bundle.Observers)
	}
	if !file.Bundle.Observers[0].Disabled {
		t.Fatalf("observer disabled = false, want true")
	}
	if len(file.Bundle.SignalDerivers) != 1 || file.Bundle.SignalDerivers[0].Name != "kubernetes.signals" {
		t.Fatalf("signal derivers = %#v, want kubernetes.signals", file.Bundle.SignalDerivers)
	}
	if len(file.Bundle.Reactions) != 1 || file.Bundle.Reactions[0].Name != "kubernetes-available" {
		t.Fatalf("reactions = %#v, want kubernetes-available", file.Bundle.Reactions)
	}
	if got := file.Bundle.Reactions[0].Actions[1].ContextProvider.Name; got != "kubernetes.context" {
		t.Fatalf("context provider reaction target = %q, want kubernetes.context", got)
	}
}

func TestDecodeFileLoadsResourceDocuments(t *testing.T) {
	file, err := DecodeFile("agentsdk.app.yaml", []byte(`
kind: app
name: docs
---
kind: operation
name: echo
description: Echo input.
---
kind: command
name: echo
target:
  operation: echo
---
kind: workflow
name: feature
steps:
  - id: run
    operation: echo
---
kind: observer
name: kubernetes.context
phase: turn
observable_kinds: [kubernetes.context]
---
kind: signal_deriver
name: kubernetes.signals
observation_kinds: [kubernetes.context]
signals:
  - kind: integration.available
    target: kubernetes
---
kind: reaction
name: kubernetes-available
when:
  signal: integration.available
  target: kubernetes
actions:
  - kind: activate_skill
    skill:
      name: kubernetes
`))
	if err != nil {
		t.Fatalf("DecodeFile: %v", err)
	}
	if len(file.Bundle.Operations) != 1 || file.Bundle.Operations[0].Ref.Name != "echo" {
		t.Fatalf("operations = %#v", file.Bundle.Operations)
	}
	if len(file.Bundle.Commands) != 1 || file.Bundle.Commands[0].Target.Operation.Name != "echo" {
		t.Fatalf("commands = %#v", file.Bundle.Commands)
	}
	if len(file.Bundle.Workflows) != 1 || file.Bundle.Workflows[0].Name != "feature" {
		t.Fatalf("workflows = %#v", file.Bundle.Workflows)
	}
	if len(file.Bundle.Observers) != 1 || file.Bundle.Observers[0].Name != "kubernetes.context" {
		t.Fatalf("observers = %#v", file.Bundle.Observers)
	}
	if len(file.Bundle.SignalDerivers) != 1 || file.Bundle.SignalDerivers[0].Name != "kubernetes.signals" {
		t.Fatalf("signal derivers = %#v", file.Bundle.SignalDerivers)
	}
	if len(file.Bundle.Reactions) != 1 || file.Bundle.Reactions[0].Name != "kubernetes-available" {
		t.Fatalf("reactions = %#v", file.Bundle.Reactions)
	}
}

func TestDecodeFileNamesMultiSegmentCommandsForResourceResolution(t *testing.T) {
	file, err := DecodeFile("agentsdk.app.yaml", []byte(`
kind: app
name: resource-app
commands:
  - path: [foo, bar]
    target:
      operation: echo
  - name: baz/qux
    target:
      workflow: rollout
  - name: explicit/path
    annotations:
      name: custom:command
    target:
      operation: echo
`))
	if err != nil {
		t.Fatalf("DecodeFile: %v", err)
	}
	if got, want := len(file.Bundle.Commands), 3; got != want {
		t.Fatalf("commands len = %d, want %d", got, want)
	}
	tests := []struct {
		index int
		path  string
		name  string
	}{
		{index: 0, path: "/foo/bar", name: "foo:bar"},
		{index: 1, path: "/baz/qux", name: "baz:qux"},
		{index: 2, path: "/explicit/path", name: "custom:command"},
	}
	for _, tc := range tests {
		spec := file.Bundle.Commands[tc.index]
		if got := spec.Path.String(); got != tc.path {
			t.Fatalf("command[%d] path = %q, want %q", tc.index, got, tc.path)
		}
		if got := spec.Annotations["name"]; got != tc.name {
			t.Fatalf("command[%d] annotation name = %q, want %q", tc.index, got, tc.name)
		}
	}
}

func TestDecodeFileComposesMultiSegmentCommandsUnderResolverName(t *testing.T) {
	file, err := DecodeFile("agentsdk.app.yaml", []byte(`
kind: app
name: resource-app
commands:
  - path: [foo, bar]
    target:
      operation: echo
`))
	if err != nil {
		t.Fatalf("DecodeFile: %v", err)
	}
	echo := operation.New(operation.Spec{Ref: operation.Ref{Name: "echo"}}, func(_ operation.Context, input operation.Value) operation.Result {
		return operation.OK(input)
	})
	composition, err := appcompose.Compose(appcompose.Config{
		Operations: []operation.Operation{echo},
		Bundles:    []resource.ContributionBundle{file.Bundle},
	})
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	id, err := composition.Resolver.Resolve("command", "foo:bar")
	if err != nil {
		t.Fatalf("Resolve command foo:bar: %v", err)
	}
	binding, ok := composition.CommandCatalog[id.Address()]
	if !ok {
		t.Fatalf("command catalog missing %s", id.Address())
	}
	if got, want := binding.Spec.Path.String(), "/foo/bar"; got != want {
		t.Fatalf("bound command path = %q, want %q", got, want)
	}
}

func TestDecodeFileRejectsUnknownAgentField(t *testing.T) {
	_, err := DecodeFile("agentsdk.app.yaml", []byte(`
kind: agent
name: main
model: openai/gpt-5.5
surprise: true
`))
	if err == nil || !strings.Contains(err.Error(), "schema validation failed") {
		t.Fatalf("DecodeFile error = %v, want schema validation failure", err)
	}
}

func TestDecodeFileRejectsMaxContinuationsWithoutStopCondition(t *testing.T) {
	_, err := DecodeFile("agentsdk.app.yaml", []byte(`
kind: agent
name: main
model: openai/gpt-5.5
turns:
  max_steps: 50
  continuation:
    max_continuations: 3
`))
	if err == nil || !strings.Contains(err.Error(), "stop_condition") {
		t.Fatalf("DecodeFile error = %v, want stop_condition failure", err)
	}
}

func TestDecodeFileRejectsInvalidResourceDocuments(t *testing.T) {
	tests := []struct {
		name string
		doc  string
		want string
	}{
		{
			name: "unknown command field",
			doc: `
kind: command
name: echo
target:
  operation: echo
surprise: true
`,
			want: "schema validation failed",
		},
		{
			name: "empty command target",
			doc: `
kind: command
name: echo
target: {}
`,
			want: "command target is empty",
		},
		{
			name: "empty workflow name",
			doc: `
kind: workflow
steps:
  - id: run
    operation: echo
`,
			want: "workflow: spec name is empty",
		},
		{
			name: "empty operation name",
			doc: `
kind: operation
description: missing name
`,
			want: "operation name is empty",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := DecodeFile("agentsdk.app.yaml", []byte(tc.doc))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("DecodeFile error = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestFileValidateRejectsConnectorWithoutKind(t *testing.T) {
	file := File{Connectors: map[string]ConnectorDoc{"gitlab-prod": {}}}
	if err := file.Validate(); err == nil || !strings.Contains(err.Error(), "connectors[\"gitlab-prod\"].kind is empty") {
		t.Fatalf("Validate error = %v, want connector kind error", err)
	}
}

func TestDecodeFileReadsRuntimeWorkspaceConfig(t *testing.T) {
	file, err := DecodeFile("agentsdk.app.yaml", []byte(`
kind: app
name: sample
runtime:
  workspace:
    env_files:
      - .env
      - .env.local
    roots:
      - name: tmp
        path: /tmp/agentruntime-sample
        access: read_write
        create: true
        env_files:
          - .env.tmp
    scratch_root: tmp
`))
	if err != nil {
		t.Fatalf("DecodeFile: %v", err)
	}
	if err := file.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if file.Runtime.Workspace.ScratchRoot != "tmp" {
		t.Fatalf("scratch root = %q, want tmp", file.Runtime.Workspace.ScratchRoot)
	}
	if got := file.Runtime.Workspace.EnvFiles; len(got) != 2 || got[0] != ".env" || got[1] != ".env.local" {
		t.Fatalf("env files = %#v, want root env files", got)
	}
	if len(file.Runtime.Workspace.Roots) != 1 {
		t.Fatalf("roots = %#v, want one root", file.Runtime.Workspace.Roots)
	}
	root := file.Runtime.Workspace.Roots[0]
	if root.Name != "tmp" || root.Path != "/tmp/agentruntime-sample" || root.Access != "read_write" || !root.Create {
		t.Fatalf("root = %#v, want tmp read_write create", root)
	}
	if len(root.EnvFiles) != 1 || root.EnvFiles[0] != ".env.tmp" {
		t.Fatalf("root env files = %#v, want .env.tmp", root.EnvFiles)
	}
}

func TestDecodeManifestRejectsEmptySourceViaValidation(t *testing.T) {
	_, err := DecodeManifest("agentsdk.app.json", []byte(`{"sources":[{"location":""}]}`))
	if err == nil {
		t.Fatal("DecodeManifest error is nil, want empty source validation error")
	}
}

func TestDecodeManifestRejectsEmptyPluginViaValidation(t *testing.T) {
	_, err := DecodeManifest("agentsdk.app.json", []byte(`{"plugins":[{"kind":""}]}`))
	if err == nil {
		t.Fatal("DecodeManifest error is nil, want empty plugin validation error")
	}
}

func TestDecodeManifestRejectsPluginNameField(t *testing.T) {
	_, err := DecodeManifest("agentsdk.app.json", []byte(`{"plugins":[{"name":"web"}]}`))
	if err == nil {
		t.Fatal("DecodeManifest error is nil, want plugin name field validation error")
	}
}

func TestLoadDirReadsDefaultManifest(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, DefaultManifestName)
	if err := os.WriteFile(path, []byte(`{"default_agent":{"name":"main"},"sources":[{"location":".agents"}]}`), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	bundle, err := LoadDir(context.Background(), dir)
	if err != nil {
		t.Fatalf("LoadDir: %v", err)
	}
	if len(bundle.Apps) != 1 || bundle.Apps[0].DefaultAgent.Name != "main" {
		t.Fatalf("bundle apps = %#v, want default agent main", bundle.Apps)
	}
	if bundle.Source.Location != filepath.Clean(path) {
		t.Fatalf("source location = %q, want %q", bundle.Source.Location, filepath.Clean(path))
	}
}

func TestLoadDirReadsYAMLManifest(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "agentsdk.app.yaml")
	if err := os.WriteFile(path, []byte("default_agent:\n  name: main\nsources:\n  - location: .agents\n"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	bundle, err := LoadDir(context.Background(), dir)
	if err != nil {
		t.Fatalf("LoadDir: %v", err)
	}
	if len(bundle.Apps) != 1 || bundle.Apps[0].DefaultAgent.Name != "main" {
		t.Fatalf("bundle apps = %#v, want default agent main", bundle.Apps)
	}
	if bundle.Source.Location != filepath.Clean(path) {
		t.Fatalf("source location = %q, want %q", bundle.Source.Location, filepath.Clean(path))
	}
}

func TestLoadDirReadsYMLManifest(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "agentsdk.app.yml")
	if err := os.WriteFile(path, []byte("default_agent:\n  name: main\nsources:\n  - location: .agents\n"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	bundle, err := LoadDir(context.Background(), dir)
	if err != nil {
		t.Fatalf("LoadDir: %v", err)
	}
	if len(bundle.Apps) != 1 || bundle.Apps[0].DefaultAgent.Name != "main" {
		t.Fatalf("bundle apps = %#v, want default agent main", bundle.Apps)
	}
	if bundle.Source.Location != filepath.Clean(path) {
		t.Fatalf("source location = %q, want %q", bundle.Source.Location, filepath.Clean(path))
	}
}

func TestLoadDirUsesDeterministicManifestOrder(t *testing.T) {
	dir := t.TempDir()
	jsonPath := filepath.Join(dir, "agentsdk.app.json")
	yamlPath := filepath.Join(dir, "agentsdk.app.yaml")
	if err := os.WriteFile(jsonPath, []byte(`{"default_agent":{"name":"json"},"sources":[{"location":".agents"}]}`), 0o600); err != nil {
		t.Fatalf("WriteFile json: %v", err)
	}
	if err := os.WriteFile(yamlPath, []byte("default_agent:\n  name: yaml\nsources:\n  - location: .agents\n"), 0o600); err != nil {
		t.Fatalf("WriteFile yaml: %v", err)
	}

	bundle, err := LoadDir(context.Background(), dir)
	if err != nil {
		t.Fatalf("LoadDir: %v", err)
	}
	if bundle.Apps[0].DefaultAgent.Name != "json" {
		t.Fatalf("default agent = %q, want json", bundle.Apps[0].DefaultAgent.Name)
	}
	if bundle.Source.Location != filepath.Clean(jsonPath) {
		t.Fatalf("source location = %q, want %q", bundle.Source.Location, filepath.Clean(jsonPath))
	}
}

func TestLoadDirReportsAcceptedManifestNames(t *testing.T) {
	_, err := LoadDir(context.Background(), t.TempDir())
	if err == nil {
		t.Fatal("LoadDir error is nil, want missing manifest error")
	}
	for _, name := range DefaultManifestNames {
		if !strings.Contains(err.Error(), name) {
			t.Fatalf("error %q does not mention %s", err, name)
		}
	}
}
