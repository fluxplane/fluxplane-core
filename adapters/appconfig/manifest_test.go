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
