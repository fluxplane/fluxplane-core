package appconfig

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/fluxplane/fluxplane-core/core/agent"
	coreapp "github.com/fluxplane/fluxplane-core/core/app"
	"github.com/fluxplane/fluxplane-core/core/invocation"
	corellm "github.com/fluxplane/fluxplane-core/core/llm"
	"github.com/fluxplane/fluxplane-core/core/operation"
	"github.com/fluxplane/fluxplane-core/core/policy"
	"github.com/fluxplane/fluxplane-core/core/resource"
	"github.com/fluxplane/fluxplane-core/core/workflow"
	appcompose "github.com/fluxplane/fluxplane-core/orchestration/app"
)

func TestLoadFSLoadsManifestFromFilesystem(t *testing.T) {
	file, err := LoadFSFile(context.Background(), fstest.MapFS{
		"resources/agents.yaml": &fstest.MapFile{Data: []byte(`kind: agent
name: coder
operations: [web_search]
`)},
	}, "resources/agents.yaml")
	if err != nil {
		t.Fatalf("LoadFSFile: %v", err)
	}
	if len(file.Bundle.Agents) != 1 || file.Bundle.Agents[0].Name != "coder" {
		t.Fatalf("agents = %#v", file.Bundle.Agents)
	}
	if len(file.Bundle.Agents[0].Operations) != 1 || file.Bundle.Agents[0].Operations[0].Name != "web_search" {
		t.Fatalf("agent operations = %#v", file.Bundle.Agents[0].Operations)
	}
}

func TestDecodeManifestRejectsDuplicateKeysBeforeBinding(t *testing.T) {
	_, err := DecodeManifest("fluxplane.yaml", []byte(`
kind: app
name: first
name: second
`))
	if err == nil || !strings.Contains(err.Error(), `mapping key "name" already defined`) {
		t.Fatalf("DecodeManifest error = %v, want duplicate key validation", err)
	}
}

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
  "plugins": {
    "git": null,
    "browser-ci": {"kind": "browser", "headless": true}
  }
}`)

	bundle, err := DecodeManifest("/repo/fluxplane.json", data)
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
	pluginsByInstance := map[string]coreapp.PluginRef{}
	for _, plugin := range app.Plugins {
		pluginsByInstance[plugin.Instance] = plugin
	}
	if pluginsByInstance["git"].Kind != "git" {
		t.Fatalf("plugins = %#v, want git", app.Plugins)
	}
	if plugin := pluginsByInstance["browser-ci"]; plugin.Kind != "browser" || plugin.Instance != "browser-ci" || plugin.Config["headless"] != true {
		t.Fatalf("browser plugin = %#v, want browser-ci with config", plugin)
	}
	if len(bundle.Plugins) != 2 {
		t.Fatalf("bundle plugins len = %d, want 2", len(bundle.Plugins))
	}
	bundlePluginsByInstance := map[string]resource.PluginRef{}
	for _, plugin := range bundle.Plugins {
		bundlePluginsByInstance[plugin.InstanceName()] = plugin
	}
	if plugin := bundlePluginsByInstance["browser-ci"]; plugin.Name != "browser" || plugin.Instance != "browser-ci" || plugin.Config["headless"] != true {
		t.Fatalf("bundle browser plugin = %#v, want browser-ci with config", plugin)
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
  memory:
    scope: project
`)

	bundle, err := DecodeManifest("fluxplane.yaml", data)
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
	bundle, err := DecodeManifest("fluxplane.yaml", []byte(`
name: engineer
default_agent: main
sources: [.agents]
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
}

func TestDecodeManifestLoadsMapStylePlugins(t *testing.T) {
	bundle, err := DecodeManifest("fluxplane.yaml", []byte(`
name: engineer
plugins:
  slack: ~
  slack-work:
    kind: slack
    auth: bot_token
  disabled:
    kind: web
    enabled: false
`))
	if err != nil {
		t.Fatalf("DecodeManifest: %v", err)
	}
	app := bundle.Apps[0]
	if len(app.Plugins) != 2 {
		t.Fatalf("plugins = %#v, want two enabled plugins", app.Plugins)
	}
	if app.Plugins[0].Kind != "slack" || app.Plugins[0].Instance != "slack" {
		t.Fatalf("plugin[0] = %#v, want default slack instance", app.Plugins[0])
	}
	if app.Plugins[1].Kind != "slack" || app.Plugins[1].Instance != "slack-work" || app.Plugins[1].Config["auth"] != "bot_token" {
		t.Fatalf("plugin[1] = %#v, want slack-work with auth config", app.Plugins[1])
	}
	if len(bundle.Plugins) != 2 {
		t.Fatalf("bundle plugins = %#v, want two enabled plugin refs", bundle.Plugins)
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

	file, err := DecodeFile("fluxplane.yaml", data)
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

	_, err := DecodeManifest("fluxplane.yaml", data)
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

	_, err := DecodeManifest("fluxplane.yaml", data)
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
      dsn_env: FLUXPLANE_DATASTORE_MYSQL_DSN
  events:
    store:
      kind: nats
      dsn_env: FLUXPLANE_EVENTSTORE_NATS_DSN
      stream: FLUXPLANE_EVENTS
      subject: fluxplane.events.log
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

	file, err := DecodeFile("fluxplane.yaml", data)
	if err != nil {
		t.Fatalf("DecodeFile: %v", err)
	}
	bundle := file.Bundle
	app := bundle.Apps[0]
	if file.Runtime.Data.Store.Kind != "mysql" || file.Runtime.Data.Store.DSNEnv != "FLUXPLANE_DATASTORE_MYSQL_DSN" {
		t.Fatalf("runtime data store = %#v, want mysql dsn env", file.Runtime.Data.Store)
	}
	if file.Runtime.Events.Store.Kind != "nats" || file.Runtime.Events.Store.DSNEnv != "FLUXPLANE_EVENTSTORE_NATS_DSN" || file.Runtime.Events.Store.Stream != "FLUXPLANE_EVENTS" || file.Runtime.Events.Store.Subject != "fluxplane.events.log" || !file.Runtime.Events.Store.CreateStream {
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
      - fluxplane.yaml
      - .agents/**
      - docs/**/*.md
    docker: {}
    targets:
      capabilities:
        kind: documentation
        description: Capability documentation.
        output: docs/capabilities.md
      chart:
        kind: helm-chart
        dockerfile: build/Dockerfile
        image: support-bot
        tags: [prod]
        image_pull_policy: Always
        env_secret_name: support-bot-runtime
        node_selectors: [pool=agents]
        values:
          replicaCount: "2"
  deploy:
    model: smart_model
    targets:
      local:
        kind: docker-compose
        description: Local compose deployment.
        build: [capabilities]
        compose_file: docker-compose.yaml
        detach: true
  metadata:
    tier: local
  commands:
    - name: ask
      description: Ask the engineer agent.
`)

	file, err := DecodeFile("fluxplane.yaml", data)
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
	if spec.Build.Targets["capabilities"].Kind != "documentation" || spec.Build.Targets["capabilities"].Description != "Capability documentation." || spec.Build.Targets["chart"].Kind != "helm-chart" {
		t.Fatalf("build targets = %#v", spec.Build.Targets)
	}
	if chart := spec.Build.Targets["chart"]; chart.Dockerfile != "build/Dockerfile" || chart.ImagePullPolicy != "Always" || chart.EnvSecretName != "support-bot-runtime" || len(chart.NodeSelectors) != 1 || chart.Values["replicaCount"] != "2" {
		t.Fatalf("chart target = %#v", chart)
	}
	if spec.Deploy.Model != "smart_model" {
		t.Fatalf("deploy = %#v, want smart_model", spec.Deploy)
	}
	if spec.Deploy.Targets["local"].Kind != "docker-compose" || spec.Deploy.Targets["local"].Description != "Local compose deployment." || !spec.Deploy.Targets["local"].Detach {
		t.Fatalf("deploy targets = %#v", spec.Deploy.Targets)
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
  slack-bot:
    kind: slack
    auth:
      method: token
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
  triggers:
    - name: nightly-summary
      kind: schedule
      schedule:
        every: 1h
      session: slack-main
      actions:
        - kind: run_workflow
          workflow:
            name: feature
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

	file, err := DecodeFile("/repo/examples/slack-bot/fluxplane.yaml", data)
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
	if file.Daemon.Channels[0].Instance != "slack-bot" {
		t.Fatalf("channel instance = %q, want slack-bot", file.Daemon.Channels[0].Instance)
	}
	if len(file.Daemon.Triggers) != 1 || file.Daemon.Triggers[0].Name != "nightly-summary" {
		t.Fatalf("triggers = %#v, want nightly-summary", file.Daemon.Triggers)
	}
	if len(file.Daemon.Triggers[0].Actions) != 1 || file.Daemon.Triggers[0].Actions[0].Workflow.Name != "feature" {
		t.Fatalf("trigger actions = %#v, want feature workflow", file.Daemon.Triggers[0].Actions)
	}
}

func TestDecodeFileExpandsAgentTriggers(t *testing.T) {
	file, err := DecodeFile("fluxplane.yaml", []byte(`
kind: app
name: health
default_agent: health
---
kind: agent
name: health
triggers:
  - every: 1m
    prompt: |
      Summarize system health and notify only on actionable anomalies.
  - startup:
      prompt: System monitoring active.
system: |
  You are an operator alert assistant.
`))
	if err != nil {
		t.Fatalf("DecodeFile: %v", err)
	}
	if len(file.Bundle.Agents) != 1 || file.Bundle.Agents[0].Name != "health" {
		t.Fatalf("agents = %#v", file.Bundle.Agents)
	}
	if len(file.Daemon.Triggers) != 2 {
		t.Fatalf("triggers = %#v, want 2", file.Daemon.Triggers)
	}
	scheduled := file.Daemon.Triggers[0]
	if scheduled.Name != "health-every-1m" || scheduled.Kind != "schedule" || scheduled.Schedule.Every != "1m" || scheduled.Session != "default" {
		t.Fatalf("scheduled trigger = %#v", scheduled)
	}
	if scheduled.Metadata["agent"] != "health" {
		t.Fatalf("scheduled metadata = %#v, want agent health", scheduled.Metadata)
	}
	if len(scheduled.Actions) != 1 || scheduled.Actions[0].Workflow.Name != "__trigger_health-every-1m" {
		t.Fatalf("scheduled actions = %#v", scheduled.Actions)
	}
	startup := file.Daemon.Triggers[1]
	if startup.Name != "health-startup" || startup.Kind != "startup" || startup.Session != "default" {
		t.Fatalf("startup trigger = %#v", startup)
	}
	if len(file.Bundle.Workflows) != 2 {
		t.Fatalf("generated workflows = %#v, want 2", file.Bundle.Workflows)
	}
	if file.Bundle.Workflows[0].Name != "__trigger_health-every-1m" || file.Bundle.Workflows[0].Steps[0].Agent.Name != "health" {
		t.Fatalf("scheduled workflow = %#v", file.Bundle.Workflows[0])
	}
	if got := file.Bundle.Workflows[0].Steps[0].Input; !strings.Contains(got.(string), "Summarize system health") {
		t.Fatalf("scheduled workflow input = %#v", got)
	}
	if got := file.Bundle.Workflows[1].Steps[0].Input; got != "System monitoring active." {
		t.Fatalf("startup workflow input = %#v", got)
	}
}

func TestDecodeFileInfersDefaultAgentForSingleAgentApp(t *testing.T) {
	file, err := DecodeFile("fluxplane.yaml", []byte(`
kind: app
name: health
---
kind: agent
name: health
system: Monitor health.
`))
	if err != nil {
		t.Fatalf("DecodeFile: %v", err)
	}
	if len(file.Bundle.Apps) != 1 {
		t.Fatalf("apps = %#v, want one app", file.Bundle.Apps)
	}
	if file.Bundle.Apps[0].DefaultAgent.Name != "health" {
		t.Fatalf("default agent = %#v, want health", file.Bundle.Apps[0].DefaultAgent)
	}
}

func TestDecodeFileDoesNotInferDefaultAgentForMultiAgentApp(t *testing.T) {
	file, err := DecodeFile("fluxplane.yaml", []byte(`
kind: app
name: health
---
kind: agent
name: triage
---
kind: agent
name: reporter
`))
	if err != nil {
		t.Fatalf("DecodeFile: %v", err)
	}
	if len(file.Bundle.Apps) != 1 {
		t.Fatalf("apps = %#v, want one app", file.Bundle.Apps)
	}
	if file.Bundle.Apps[0].DefaultAgent.Name != "" {
		t.Fatalf("default agent = %#v, want empty", file.Bundle.Apps[0].DefaultAgent)
	}
}

func TestDecodeAgentDocAcceptsUsesWithoutExpansion(t *testing.T) {
	file, err := DecodeFile("fluxplane.yaml", []byte(`
kind: agent
name: reviewer
uses:
  - slack
  - gitlab.review
`))
	if err != nil {
		t.Fatalf("DecodeFile: %v", err)
	}
	if len(file.Bundle.Agents) != 1 || file.Bundle.Agents[0].Name != "reviewer" {
		t.Fatalf("agents = %#v, want reviewer", file.Bundle.Agents)
	}
	if got, want := agentActivationSetNames(file.Bundle.Agents[0]), []string{"slack", "gitlab.review"}; strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("agent activation sets = %#v, want %#v", got, want)
	}
	if len(file.Bundle.Agents[0].Tools) != 0 || len(file.Bundle.Agents[0].Datasources) != 0 || len(file.Bundle.Agents[0].Context) != 0 {
		t.Fatalf("uses expanded unexpectedly into agent spec: %#v", file.Bundle.Agents[0])
	}
}

func agentActivationSetNames(spec agent.Spec) []string {
	return append([]string(nil), spec.ActivationSets...)
}

func TestDecodeFileRejectsInvalidDurationString(t *testing.T) {
	_, err := DecodeFile("fluxplane.yaml", []byte(`
kind: agent
name: health
triggers:
  - every: soon
    prompt: Check health.
`))
	if err == nil {
		t.Fatal("DecodeFile succeeded, want invalid duration error")
	}
	if !strings.Contains(err.Error(), "every") && !strings.Contains(err.Error(), "duration") {
		t.Fatalf("DecodeFile error = %v, want duration validation", err)
	}
}

func TestDecodeFileAddsDefaultSessionForResourceOnlyAgentTriggers(t *testing.T) {
	file, err := DecodeFile("agent.yaml", []byte(`
kind: agent
name: health
triggers:
  - every: 5m
    workflow: heartbeat
system: |
  You are an operator alert assistant.
`))
	if err != nil {
		t.Fatalf("DecodeFile: %v", err)
	}
	if len(file.Bundle.Sessions) != 1 || file.Bundle.Sessions[0].Name != "default" || file.Bundle.Sessions[0].Agent.Name != "health" {
		t.Fatalf("sessions = %#v, want generated default health session", file.Bundle.Sessions)
	}
	if len(file.Daemon.Triggers) != 1 || file.Daemon.Triggers[0].Actions[0].Workflow.Name != "heartbeat" {
		t.Fatalf("triggers = %#v", file.Daemon.Triggers)
	}
}

func TestDecodeFileLoadsTopLevelResources(t *testing.T) {
	file, err := DecodeFile("fluxplane.yaml", []byte(`
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
        depends_on: [plan]
        error_policy: continue
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
  assertion_derivers:
    - name: kubernetes.assertions
      observation_kinds: [kubernetes.context]
      assertions:
        - kind: integration.available
          target: kubernetes
reactions:
  - name: kubernetes-available
    when:
      assertion: integration.available
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
	if len(file.Bundle.AssertionDerivers) != 1 || file.Bundle.AssertionDerivers[0].Name != "kubernetes.assertions" {
		t.Fatalf("assertion derivers = %#v, want kubernetes.assertions", file.Bundle.AssertionDerivers)
	}
	if len(file.Bundle.Reactions) != 1 || file.Bundle.Reactions[0].Name != "kubernetes-available" {
		t.Fatalf("reactions = %#v, want kubernetes-available", file.Bundle.Reactions)
	}
	if got := file.Bundle.Reactions[0].Actions[1].ContextProvider.Name; got != "kubernetes.context" {
		t.Fatalf("context provider reaction target = %q, want kubernetes.context", got)
	}
}

func TestDecodeFileLoadsResourceDocuments(t *testing.T) {
	file, err := DecodeFile("fluxplane.yaml", []byte(`
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
kind: assertion_deriver
name: kubernetes.assertions
observation_kinds: [kubernetes.context]
assertions:
  - kind: integration.available
    target: kubernetes
---
kind: reaction
name: kubernetes-available
when:
  assertion: integration.available
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
	if len(file.Bundle.AssertionDerivers) != 1 || file.Bundle.AssertionDerivers[0].Name != "kubernetes.assertions" {
		t.Fatalf("assertion derivers = %#v", file.Bundle.AssertionDerivers)
	}
	if len(file.Bundle.Reactions) != 1 || file.Bundle.Reactions[0].Name != "kubernetes-available" {
		t.Fatalf("reactions = %#v", file.Bundle.Reactions)
	}
}

func TestDecodeFileMapsWorkflowConditionStepID(t *testing.T) {
	file, err := DecodeFile("fluxplane.yaml", []byte(`
kind: workflow
name: health
steps:
  - id: classify
    operation: echo
  - id: notify
    operation: echo
    depends_on: [classify]
    when:
      step_id: classify
      equals: ACTION_NEEDED
`))
	if err != nil {
		t.Fatalf("DecodeFile: %v", err)
	}
	if len(file.Bundle.Workflows) != 1 || len(file.Bundle.Workflows[0].Steps) != 2 {
		t.Fatalf("workflows = %#v", file.Bundle.Workflows)
	}
	when := file.Bundle.Workflows[0].Steps[1].When
	if when.StepID != "classify" || when.Equals != "ACTION_NEEDED" {
		t.Fatalf("when = %#v, want step_id classify equals ACTION_NEEDED", when)
	}
}

func TestDecodeFileNamesMultiSegmentCommandsForResourceResolution(t *testing.T) {
	file, err := DecodeFile("fluxplane.yaml", []byte(`
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
	file, err := DecodeFile("fluxplane.yaml", []byte(`
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
	_, err := DecodeFile("fluxplane.yaml", []byte(`
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
	_, err := DecodeFile("fluxplane.yaml", []byte(`
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
			_, err := DecodeFile("fluxplane.yaml", []byte(tc.doc))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("DecodeFile error = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestDecodeFileReadsRuntimeWorkspaceConfig(t *testing.T) {
	file, err := DecodeFile("fluxplane.yaml", []byte(`
kind: app
name: sample
runtime:
  workspace:
    env_files:
      - .env
      - .env.local
    roots:
      - name: tmp
        path: /tmp/fluxplane-sample
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
	if root.Name != "tmp" || root.Path != "/tmp/fluxplane-sample" || root.Access != "read_write" || !root.Create {
		t.Fatalf("root = %#v, want tmp read_write create", root)
	}
	if len(root.EnvFiles) != 1 || root.EnvFiles[0] != ".env.tmp" {
		t.Fatalf("root env files = %#v, want .env.tmp", root.EnvFiles)
	}
}

func TestDecodeFileSelectsProfileScopedRuntimeDocs(t *testing.T) {
	data := []byte(`
kind: app
name: profiled
defaults:
  profile: dev
  agent: assistant
  model: smart_model
profiles:
  dev:
    description: Local development.
  prod:
    description: Production deployment.
models:
  available:
    - provider: openrouter
      model: openai/gpt-5.5
      aliases: [smart_model]
---
kind: runtime
workspace:
  env_files: [.env]
---
kind: runtime
profile: prod
data:
  store:
    kind: mysql
    dsn_env: FLUXPLANE_DATASTORE_MYSQL_DSN
events:
  store:
    kind: nats
    dsn_env: FLUXPLANE_EVENTSTORE_NATS_DSN
    stream: FLUXPLANE_EVENTS
    subject: fluxplane.events.log
    create_stream: true
---
kind: agent
name: assistant
`)
	devFile, err := DecodeFile("fluxplane.yaml", data)
	if err != nil {
		t.Fatalf("DecodeFile dev: %v", err)
	}
	if devFile.Profile != "dev" || len(devFile.ActiveProfiles) != 1 || devFile.ActiveProfiles[0] != "dev" {
		t.Fatalf("dev profiles = %q %#v", devFile.Profile, devFile.ActiveProfiles)
	}
	if devFile.Runtime.Data.Store.Kind != "" || devFile.Runtime.Events.Store.Kind != "" {
		t.Fatalf("dev runtime = %#v, want no prod stores", devFile.Runtime)
	}
	if got := devFile.Runtime.Workspace.EnvFiles; len(got) != 1 || got[0] != ".env" {
		t.Fatalf("dev workspace env files = %#v", got)
	}
	if app := devFile.Bundle.Apps[0]; app.DefaultAgent.Name != "assistant" || app.Model.Model != "smart_model" {
		t.Fatalf("app defaults = %#v", app)
	}

	prodFile, err := DecodeFileWithOptions("fluxplane.yaml", data, DecodeOptions{Profiles: []string{"prod"}})
	if err != nil {
		t.Fatalf("DecodeFile prod: %v", err)
	}
	if prodFile.Profile != "prod" || len(prodFile.ActiveProfiles) != 1 || prodFile.ActiveProfiles[0] != "prod" {
		t.Fatalf("prod profiles = %q %#v", prodFile.Profile, prodFile.ActiveProfiles)
	}
	if prodFile.Runtime.Data.Store.Kind != "mysql" || prodFile.Runtime.Data.Store.DSNEnv != "FLUXPLANE_DATASTORE_MYSQL_DSN" {
		t.Fatalf("prod data store = %#v", prodFile.Runtime.Data.Store)
	}
	if prodFile.Runtime.Events.Store.Kind != "nats" || prodFile.Runtime.Events.Store.DSNEnv != "FLUXPLANE_EVENTSTORE_NATS_DSN" || !prodFile.Runtime.Events.Store.CreateStream {
		t.Fatalf("prod event store = %#v", prodFile.Runtime.Events.Store)
	}
}

func TestDecodeFileAllowsMultipleActiveProfiles(t *testing.T) {
	file, err := DecodeFileWithOptions("fluxplane.yaml", []byte(`
kind: app
name: profiled
profiles:
  prod:
    description: Production deployment.
  debug:
    description: Debugging.
---
kind: runtime
profile: prod
data:
  store:
    kind: mysql
---
kind: runtime
profile: debug
workspace:
  env_files: [.env.debug]
---
kind: agent
name: assistant
profile: [prod, debug]
`), DecodeOptions{Profiles: []string{"prod,debug"}})
	if err != nil {
		t.Fatalf("DecodeFile: %v", err)
	}
	if file.Profile != "prod,debug" || len(file.ActiveProfiles) != 2 || file.ActiveProfiles[0] != "prod" || file.ActiveProfiles[1] != "debug" {
		t.Fatalf("profiles = %q %#v", file.Profile, file.ActiveProfiles)
	}
	if file.Runtime.Data.Store.Kind != "mysql" {
		t.Fatalf("data store = %#v, want mysql", file.Runtime.Data.Store)
	}
	if got := file.Runtime.Workspace.EnvFiles; len(got) != 1 || got[0] != ".env.debug" {
		t.Fatalf("env files = %#v", got)
	}
	if len(file.Bundle.Agents) != 1 || file.Bundle.Agents[0].Name != "assistant" {
		t.Fatalf("agents = %#v, want assistant", file.Bundle.Agents)
	}
}

func TestDecodeManifestRejectsEmptySourceViaValidation(t *testing.T) {
	_, err := DecodeManifest("fluxplane.json", []byte(`{"sources":[{"location":""}]}`))
	if err == nil {
		t.Fatal("DecodeManifest error is nil, want empty source validation error")
	}
}

func TestDecodeManifestRejectsPluginListSyntax(t *testing.T) {
	_, err := DecodeManifest("fluxplane.json", []byte(`{"plugins":[{"kind":"web"}]}`))
	if err == nil || !strings.Contains(err.Error(), "got array, want object") {
		t.Fatalf("DecodeManifest error = %v, want plugin array schema error", err)
	}
}

func TestDecodeManifestRejectsEmptyPluginInstance(t *testing.T) {
	_, err := DecodeManifest("fluxplane.json", []byte(`{"plugins":{"":null}}`))
	if err == nil || !strings.Contains(err.Error(), "empty instance name") {
		t.Fatalf("DecodeManifest error = %v, want empty plugin instance validation error", err)
	}
}

func TestDecodeManifestRejectsConflictingPluginInstanceField(t *testing.T) {
	_, err := DecodeManifest("fluxplane.json", []byte(`{"plugins":{"web":{"instance":"other"}}}`))
	if err == nil || !strings.Contains(err.Error(), "must match map key") {
		t.Fatalf("DecodeManifest error = %v, want plugin instance conflict error", err)
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
	path := filepath.Join(dir, "fluxplane.yaml")
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

func TestLoadDirRejectsDeprecatedYMLManifest(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "agentsdk.app.yml")
	if err := os.WriteFile(path, []byte("default_agent:\n  name: main\nsources:\n  - location: .agents\n"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	_, err := LoadDir(context.Background(), dir)
	if err == nil || !strings.Contains(err.Error(), "no longer supported") || !strings.Contains(err.Error(), DefaultManifestName) {
		t.Fatalf("LoadDir error = %v, want deprecated manifest diagnostic", err)
	}
}

func TestLoadDirPrefersFluxplaneManifestOverDeprecatedManifest(t *testing.T) {
	dir := t.TempDir()
	jsonPath := filepath.Join(dir, "agentsdk.app.json")
	yamlPath := filepath.Join(dir, "fluxplane.yaml")
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
	if bundle.Apps[0].DefaultAgent.Name != "yaml" {
		t.Fatalf("default agent = %q, want yaml", bundle.Apps[0].DefaultAgent.Name)
	}
	if bundle.Source.Location != filepath.Clean(yamlPath) {
		t.Fatalf("source location = %q, want %q", bundle.Source.Location, filepath.Clean(yamlPath))
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
