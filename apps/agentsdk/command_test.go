package agentsdk

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fluxplane/agentruntime/adapters/appconfig"
	corellm "github.com/fluxplane/agentruntime/core/llm"
	"github.com/fluxplane/agentruntime/plugins/eventcatalog"
)

func TestRootCommandHasExpectedCommands(t *testing.T) {
	cmd := NewCommand()
	var names []string
	for _, child := range cmd.Commands() {
		names = append(names, child.Name())
	}
	got := strings.Join(names, ",")
	for _, want := range []string{"coder", "init", "build", "run", "serve", "models", "connect", "remote", "discover"} {
		if !strings.Contains(got, want) {
			t.Fatalf("commands = %s, want %s", got, want)
		}
	}
}

func TestModelsCommandRendersBuiltInProviders(t *testing.T) {
	cmd := NewCommand()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"models"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v\n%s", err, out.String())
	}
	text := out.String()
	for _, want := range []string{"Providers:", "openai", "codex", "gpt-5.5"} {
		if !strings.Contains(text, want) {
			t.Fatalf("models output missing %q:\n%s", want, text)
		}
	}
}

func TestModelsCommandRendersJSON(t *testing.T) {
	cmd := NewCommand()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"models", "-o", "json"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v\n%s", err, out.String())
	}
	var providers []corellm.ProviderSpec
	if err := json.Unmarshal(out.Bytes(), &providers); err != nil {
		t.Fatalf("Unmarshal: %v\n%s", err, out.String())
	}
	if len(providers) == 0 {
		t.Fatalf("providers is empty")
	}
}

func TestModelsCommandIncludesPathLLMProviders(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "agentsdk.app.yaml", `kind: app
name: model-demo
llm_providers:
  - name: localai
    display_name: Local AI
    models:
      - ref:
          name: local-model
        context_tokens: 1234
`)

	cmd := NewCommand()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"models", root})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v\n%s", err, out.String())
	}
	text := out.String()
	for _, want := range []string{"localai", "Local AI", "local-model", "context 1234"} {
		if !strings.Contains(text, want) {
			t.Fatalf("models output missing %q:\n%s", want, text)
		}
	}
}

func TestModelsCommandRejectsUnsupportedOutput(t *testing.T) {
	cmd := NewCommand()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"models", "-o", "xml"})

	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), `models: unsupported output "xml"`) {
		t.Fatalf("Execute error = %v, want unsupported output", err)
	}
}

func TestInitCommandCreatesMinimalSecureManifest(t *testing.T) {
	dir := t.TempDir()
	cmd := NewCommand()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"init", dir})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	manifestPath := filepath.Join(dir, "agentsdk.app.yaml")
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	file, err := appconfig.DecodeFile(manifestPath, data)
	if err != nil {
		t.Fatalf("DecodeFile: %v", err)
	}
	if len(file.Bundle.Apps) != 1 || string(file.Bundle.Apps[0].Name) != filepath.Base(dir) {
		t.Fatalf("apps = %#v, want app named after directory", file.Bundle.Apps)
	}
	if file.Bundle.Apps[0].DefaultAgent.Name != "default" {
		t.Fatalf("default agent = %#v, want default", file.Bundle.Apps[0].DefaultAgent)
	}
	if len(file.Bundle.Plugins) != 0 {
		t.Fatalf("plugins = %#v, want none", file.Bundle.Plugins)
	}
	if len(file.Bundle.Datasources) != 0 {
		t.Fatalf("datasources = %#v, want none", file.Bundle.Datasources)
	}
	if len(file.Bundle.Agents) != 1 || file.Bundle.Agents[0].Name != "default" {
		t.Fatalf("agents = %#v, want default", file.Bundle.Agents)
	}
	if len(file.Bundle.Agents[0].Tools) != 0 || len(file.Bundle.Agents[0].Context) != 0 ||
		len(file.Bundle.Agents[0].Datasources) != 0 || len(file.Bundle.Agents[0].Skills) != 0 {
		t.Fatalf("agent = %#v, want no tools/context/datasources/skills", file.Bundle.Agents[0])
	}
	if len(file.Daemon.Listeners) != 1 {
		t.Fatalf("listeners = %#v, want one", file.Daemon.Listeners)
	}
	listener := file.Daemon.Listeners[0]
	if listener.Addr == "" || !strings.HasSuffix(listener.Addr, ".sock") {
		t.Fatalf("listener addr = %q, want unix socket", listener.Addr)
	}
	if listener.Auth["mode"] != "local_socket" {
		t.Fatalf("listener auth = %#v, want local_socket", listener.Auth)
	}
	if len(file.Daemon.Channels) != 1 || file.Daemon.Channels[0].Type != "direct" ||
		file.Daemon.Channels[0].Session != "default" {
		t.Fatalf("channels = %#v, want direct default channel", file.Daemon.Channels)
	}
	if !strings.Contains(out.String(), "created ") {
		t.Fatalf("output = %q, want created message", out.String())
	}
}

func TestInitRefusesExistingManifestUnlessForced(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "agentsdk.app.yaml", "kind: app\nname: existing\n")

	if _, err := Init(dir, InitOptions{}); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("Init error = %v, want already exists", err)
	}
	if _, err := Init(dir, InitOptions{Force: true}); err != nil {
		t.Fatalf("Init force: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "agentsdk.app.yaml"))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !strings.Contains(string(data), "name: default") {
		t.Fatalf("manifest = %s, want generated minimal manifest", data)
	}
}

func TestRunHelpIncludesLaunchFlags(t *testing.T) {
	cmd := NewCommand()
	out := bytes.Buffer{}
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"run", "--help"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	help := out.String()
	for _, want := range []string{"run [path]", "--session", "--conversation", "--provider", "--model", "--input", "--debug", "--usage", "--yolo", "--connectors-path"} {
		if !strings.Contains(help, want) {
			t.Fatalf("help = %q, want %s", help, want)
		}
	}
}

func TestBuildHelpIncludesDockerFlags(t *testing.T) {
	cmd := NewCommand()
	out := bytes.Buffer{}
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"build", "--help"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	help := out.String()
	for _, want := range []string{"build [app-dir]", "--docker", "--tag", "--platform", "--push", "--dry-run", "--connectors-path"} {
		if !strings.Contains(help, want) {
			t.Fatalf("help = %q, want %s", help, want)
		}
	}
}

func TestBuildRequiresDockerFlag(t *testing.T) {
	cmd := NewCommand()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"build", "."})

	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "only Docker builds are supported") {
		t.Fatalf("Execute error = %v, want docker-only error", err)
	}
}

func TestRemoteHelpIncludesTargetAndRenderingFlags(t *testing.T) {
	cmd := NewCommand()
	out := bytes.Buffer{}
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"remote", "--help"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	help := out.String()
	for _, want := range []string{"--app", "--url", "--socket", "--local", "--session", "--conversation", "--input", "--debug", "--usage"} {
		if !strings.Contains(help, want) {
			t.Fatalf("help = %q, want %s", help, want)
		}
	}
}

func TestConnectProviderInfoUsesProductRegistry(t *testing.T) {
	cmd := NewCommand()
	out := bytes.Buffer{}
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"connect", "slack", "--info", "--connectors-path", t.TempDir()})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "Slack (slack)") || !strings.Contains(got, "Auth methods:") {
		t.Fatalf("info = %q, want slack connect info", got)
	}
}

func TestTerminalEventRegistryDecodesPluginCatalogEvents(t *testing.T) {
	registry, err := terminalEventRegistry()
	if err != nil {
		t.Fatalf("terminalEventRegistry: %v", err)
	}
	for _, sample := range eventcatalog.All() {
		raw, err := json.Marshal(sample)
		if err != nil {
			t.Fatalf("Marshal %s: %v", sample.EventName(), err)
		}
		decoded, ok, err := registry.TryDecode(sample.EventName(), raw)
		if err != nil {
			t.Fatalf("TryDecode %s: %v", sample.EventName(), err)
		}
		if !ok {
			t.Fatalf("event %s was not registered", sample.EventName())
		}
		if decoded.EventName() != sample.EventName() {
			t.Fatalf("decoded event name = %s, want %s", decoded.EventName(), sample.EventName())
		}
	}
}

func TestDiscoverCommandRendersSkillReferences(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, ".agents/agents/main.md", "---\nname: main\nskills: [architecture]\n---\nMain.\n")
	writeTestFile(t, root, ".agents/skills/architecture/SKILL.md", "---\nname: architecture\ndescription: Architecture\n---\nBody.\n")
	writeTestFile(t, root, ".agents/skills/architecture/references/tradeoffs.md", "---\ntrigger: tradeoffs\n---\nRefs.\n")

	cmd := NewCommand()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"discover", root})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v\n%s", err, out.String())
	}
	text := out.String()
	for _, want := range []string{"Sources:", "agents", "skills", "architecture", "references/tradeoffs.md", "Resolution:"} {
		if !strings.Contains(text, want) {
			t.Fatalf("discover output missing %q:\n%s", want, text)
		}
	}
}

func TestDiscoverCommandRendersStaticPluginContributions(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "agentsdk.app.yaml", `kind: app
name: plugin-discovery
default_agent:
  name: main
plugins:
  - name: web
datasources:
  - name: docs
    kind: filesystem
    entities: [file.document]
llm_providers:
  - name: localai
    models:
      - ref:
          name: local-model
---
kind: session
name: main
agent: main
---
kind: agent
name: main
tools: [web_request, datasource_search]
context: [datasource.catalog]
datasources: [docs]
`)

	cmd := NewCommand()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"discover", root})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v\n%s", err, out.String())
	}
	text := out.String()
	for _, want := range []string{
		"agents",
		"tools: web_request, datasource_search",
		"datasources",
		"docs",
		"llm providers",
		"localai",
		"local-model",
		"plugins",
		"web",
		"datasource (implicit)",
		"Plugin contributions:",
		"operations",
		"web_request",
		"context_providers",
		"datasource.catalog",
		"datasource.detected",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("discover output missing %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "contributes:") {
		t.Fatalf("discover output contains nested contribution summary:\n%s", text)
	}
}

func writeTestFile(t *testing.T, root, rel, data string) {
	t.Helper()
	path := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatalf("write %s: %v", rel, err)
	}
}
