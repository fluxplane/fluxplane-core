package resourcefs

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/fluxplane/agentruntime/core/command"
	"github.com/fluxplane/agentruntime/core/policy"
)

func TestLoadDirLoadsManifestCommands(t *testing.T) {
	dir := t.TempDir()
	data := []byte(`{
  "commands": [
    {
      "path": ["echo"],
      "operation": "echo",
      "policy": {
        "allowed_callers": ["user"],
        "required_trust": "verified"
      }
    }
  ]
}`)
	if err := os.WriteFile(filepath.Join(dir, DefaultManifestName), data, 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	bundle, err := LoadDir(context.Background(), dir)
	if err != nil {
		t.Fatalf("LoadDir: %v", err)
	}
	if len(bundle.Commands) != 1 {
		t.Fatalf("commands len = %d, want 1", len(bundle.Commands))
	}
	spec := bundle.Commands[0]
	if spec.Path.String() != "/echo" {
		t.Fatalf("path = %s, want /echo", spec.Path.String())
	}
	if spec.Target.Operation.Name != "echo" {
		t.Fatalf("operation = %q, want echo", spec.Target.Operation.Name)
	}
	if spec.Policy.RequiredTrust != policy.TrustVerified {
		t.Fatalf("required trust = %q, want verified", spec.Policy.RequiredTrust)
	}
}

func TestCommandSpecRejectsEmptyOperation(t *testing.T) {
	_, err := Command{Path: []string{"echo"}}.Spec()
	if err == nil {
		t.Fatal("Spec error is nil, want empty operation error")
	}
}

func TestCommandSpecNormalizesPath(t *testing.T) {
	spec, err := Command{Path: []string{"/tools/", "", "echo"}, Operation: "echo"}.Spec()
	if err != nil {
		t.Fatalf("Spec: %v", err)
	}
	if got := command.Path(spec.Path).String(); got != "/tools/echo" {
		t.Fatalf("path = %s, want /tools/echo", got)
	}
}

func TestLoadDirLoadsPluginRefsWithConfig(t *testing.T) {
	dir := t.TempDir()
	data := []byte(`{
  "plugins": [
    {
      "name": "text",
      "config": {
        "commands": ["upper"]
      }
    }
  ]
}`)
	if err := os.WriteFile(filepath.Join(dir, DefaultManifestName), data, 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	bundle, err := LoadDir(context.Background(), dir)
	if err != nil {
		t.Fatalf("LoadDir: %v", err)
	}
	if len(bundle.Plugins) != 1 {
		t.Fatalf("plugins len = %d, want 1", len(bundle.Plugins))
	}
	ref := bundle.Plugins[0]
	if ref.Name != "text" {
		t.Fatalf("plugin name = %q, want text", ref.Name)
	}
	commands, ok := ref.Config["commands"].([]any)
	if !ok {
		t.Fatalf("commands config = %#v, want []any", ref.Config["commands"])
	}
	if len(commands) != 1 || commands[0] != "upper" {
		t.Fatalf("commands config = %#v, want [upper]", commands)
	}
}
