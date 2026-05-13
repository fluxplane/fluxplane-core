package agentsdk

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fluxplane/agentruntime/plugins/eventcatalog"
)

func TestRootCommandHasExpectedCommands(t *testing.T) {
	cmd := NewCommand()
	var names []string
	for _, child := range cmd.Commands() {
		names = append(names, child.Name())
	}
	got := strings.Join(names, ",")
	for _, want := range []string{"coder", "run", "serve", "connect", "remote", "discover"} {
		if !strings.Contains(got, want) {
			t.Fatalf("commands = %s, want %s", got, want)
		}
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
	for _, want := range []string{"run [path]", "--session", "--conversation", "--provider", "--model", "--input", "--debug", "--usage"} {
		if !strings.Contains(help, want) {
			t.Fatalf("help = %q, want %s", help, want)
		}
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
