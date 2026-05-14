package appconfig

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fluxplane/agentruntime/core/agent"
	"github.com/fluxplane/agentruntime/core/policy"
	"github.com/fluxplane/agentruntime/core/resource"
)

func TestDecodeManifestLoadsEngineerStyleManifest(t *testing.T) {
	data := []byte(`{
  "name": "engineer",
  "description": "Coding agent app",
  "default_agent": "main",
  "sources": [".agents"],
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
    "git",
    {"name": "browser", "config": {"headless": true}}
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
	if app.Plugins[0].Name != "git" {
		t.Fatalf("plugin[0] = %#v, want git", app.Plugins[0])
	}
	if app.Plugins[1].Name != "browser" || app.Plugins[1].Config["headless"] != true {
		t.Fatalf("plugin[1] = %#v, want browser with config", app.Plugins[1])
	}
	if len(bundle.Plugins) != 2 {
		t.Fatalf("bundle plugins len = %d, want 2", len(bundle.Plugins))
	}
	if bundle.Plugins[1].Name != "browser" || bundle.Plugins[1].Config["headless"] != true {
		t.Fatalf("bundle plugin[1] = %#v, want browser with config", bundle.Plugins[1])
	}
}

func TestDecodeManifestLoadsYAMLManifest(t *testing.T) {
	data := []byte(`
name: engineer
default_agent:
  name: main
sources:
  - .agents
model_policy:
  provider: openai
  approved_only: true
plugins:
  - name: memory
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
	if got := bundle.Plugins[0].Config["scope"]; got != "project" {
		t.Fatalf("plugin scope = %#v, want project", got)
	}
}

func TestDecodeManifestLoadsModelAliases(t *testing.T) {
	data := []byte(`
kind: app
name: engineer
models:
  alias:
    codex: codex/gpt-5.5
    claude/sonnet: anthropic/claude-sonnet-4-6
`)

	bundle, err := DecodeManifest("agentsdk.app.yaml", data)
	if err != nil {
		t.Fatalf("DecodeManifest: %v", err)
	}
	if len(bundle.LLMModelAliases) != 2 {
		t.Fatalf("aliases len = %d, want 2", len(bundle.LLMModelAliases))
	}
	got := map[string]string{}
	for _, alias := range bundle.LLMModelAliases {
		got[alias.Name] = alias.Target.String()
	}
	if got["codex"] != "codex/gpt-5.5" {
		t.Fatalf("codex alias = %q, want codex/gpt-5.5", got["codex"])
	}
	if got["claude/sonnet"] != "anthropic/claude-sonnet-4-6" {
		t.Fatalf("claude/sonnet alias = %q, want anthropic/claude-sonnet-4-6", got["claude/sonnet"])
	}
}

func TestDecodeManifestLoadsSemanticSearchConfig(t *testing.T) {
	data := []byte(`
kind: app
name: engineer
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
datasources:
  - name: local-docs
    kind: filesystem
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

	bundle, err := DecodeManifest("agentsdk.app.yaml", data)
	if err != nil {
		t.Fatalf("DecodeManifest: %v", err)
	}
	app := bundle.Apps[0]
	if !app.SemanticSearch.Enabled || app.SemanticSearch.Embeddings.Model != "text-embedding-3-large" {
		t.Fatalf("semantic search = %#v, want enabled embedding model", app.SemanticSearch)
	}
	if app.SemanticSearch.Defaults.Chunking.TargetTokens != 900 || app.SemanticSearch.Defaults.Retrieval.Mode != "hybrid" {
		t.Fatalf("semantic defaults = %#v", app.SemanticSearch.Defaults)
	}
	ds := bundle.Datasources[0]
	if !ds.Semantic.Enabled {
		t.Fatalf("datasource semantic = %#v, want enabled", ds.Semantic)
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
default_agent: slack_bot
plugins:
  - name: slack
connectors:
  slack-bot:
    kind: slack
datasources:
  - name: slack-bot
    connector: slack-bot
    kind: slack
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
      addr: agentsdk-slack-bot.sock
  channels:
    - name: slack-main
      type: slack
      connector: slack
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
max_steps: 50
max_continuations: 50
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
	if file.Bundle.Agents[0].Policy.MaxContinuations != 50 {
		t.Fatalf("max continuations = %d, want 50", file.Bundle.Agents[0].Policy.MaxContinuations)
	}
	if file.Bundle.Agents[0].Policy.MaxSteps != 50 {
		t.Fatalf("max steps = %d, want 50", file.Bundle.Agents[0].Policy.MaxSteps)
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
	if len(file.Daemon.Listeners) != 1 || file.Daemon.Listeners[0].Addr != "agentsdk-slack-bot.sock" {
		t.Fatalf("listeners = %#v", file.Daemon.Listeners)
	}
	if len(file.Daemon.Channels) != 1 || file.Daemon.Channels[0].Access.AllowKinds[2] != "thread_reply" {
		t.Fatalf("channels = %#v", file.Daemon.Channels)
	}
	if len(file.Connectors) != 1 || file.Connectors["slack-bot"].Kind != "slack" {
		t.Fatalf("connectors = %#v, want slack-bot slack", file.Connectors)
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
    roots:
      - name: tmp
        path: /tmp/agentruntime-sample
        access: read_write
        create: true
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
	if len(file.Runtime.Workspace.Roots) != 1 {
		t.Fatalf("roots = %#v, want one root", file.Runtime.Workspace.Roots)
	}
	root := file.Runtime.Workspace.Roots[0]
	if root.Name != "tmp" || root.Path != "/tmp/agentruntime-sample" || root.Access != "read_write" || !root.Create {
		t.Fatalf("root = %#v, want tmp read_write create", root)
	}
}

func TestDecodeManifestRejectsEmptySourceViaValidation(t *testing.T) {
	_, err := DecodeManifest("agentsdk.app.json", []byte(`{"sources":[""]}`))
	if err == nil {
		t.Fatal("DecodeManifest error is nil, want empty source validation error")
	}
}

func TestDecodeManifestRejectsEmptyPluginViaValidation(t *testing.T) {
	_, err := DecodeManifest("agentsdk.app.json", []byte(`{"plugins":[""]}`))
	if err == nil {
		t.Fatal("DecodeManifest error is nil, want empty plugin validation error")
	}
}

func TestLoadDirReadsDefaultManifest(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, DefaultManifestName)
	if err := os.WriteFile(path, []byte(`{"default_agent":"main","sources":[".agents"]}`), 0o600); err != nil {
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
	if err := os.WriteFile(path, []byte("default_agent: main\nsources:\n  - .agents\n"), 0o600); err != nil {
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
	if err := os.WriteFile(path, []byte("default_agent: main\nsources:\n  - .agents\n"), 0o600); err != nil {
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
	if err := os.WriteFile(jsonPath, []byte(`{"default_agent":"json","sources":[".agents"]}`), 0o600); err != nil {
		t.Fatalf("WriteFile json: %v", err)
	}
	if err := os.WriteFile(yamlPath, []byte("default_agent: yaml\nsources:\n  - .agents\n"), 0o600); err != nil {
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
