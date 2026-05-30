package launch

import (
	"bytes"
	"context"
	"encoding/json"
	sharedsecret "github.com/fluxplane/fluxplane-secret"
	"os"
	"path/filepath"
	"strings"
	"testing"

	coresecret "github.com/fluxplane/fluxplane-auth/authsecret"
	"github.com/fluxplane/fluxplane-core/orchestration/pluginhost"
	"github.com/fluxplane/fluxplane-core/plugins/examples/echo"
)

func TestOperationRunCommandRunsConfiguredOperation(t *testing.T) {
	dir := t.TempDir()
	writeOperationRunManifest(t, dir)

	cmd := NewOperationCommandWithLoader(nil, func(ctx PluginFactoryContext) []pluginhost.Plugin {
		return append(availablePluginsWithAuth(ctx.System, nil, ctx.Dispatcher, ctx.TaskRunner, ctx.NativeAuthStore, ctx.NativeAuthResolver), echo.New())
	})
	out := bytes.Buffer{}
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"run", "--app", dir, "echo", `{"message":"hi"}`})
	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("Execute: %v\n%s", err, out.String())
	}
	var result struct {
		Status string         `json:"status"`
		Output map[string]any `json:"output"`
	}
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, out.String())
	}
	if result.Status != "ok" || result.Output["message"] != "hi" {
		t.Fatalf("result = %#v", result)
	}
}

func TestOperationRunCommandReportsUnknownOperation(t *testing.T) {
	dir := t.TempDir()
	writeOperationRunManifest(t, dir)

	cmd := NewOperationCommandWithLoader(nil, func(ctx PluginFactoryContext) []pluginhost.Plugin {
		return append(availablePluginsWithAuth(ctx.System, nil, ctx.Dispatcher, ctx.TaskRunner, ctx.NativeAuthStore, ctx.NativeAuthResolver), echo.New())
	})
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"run", "--app", dir, "missing"})
	err := cmd.ExecuteContext(context.Background())
	if err == nil || !strings.Contains(err.Error(), `unknown operation "missing"`) || !strings.Contains(err.Error(), "echo") {
		t.Fatalf("error = %v, want unknown operation with available names", err)
	}
}

func TestOperationRunCommandHelpIncludesEnvironmentFlags(t *testing.T) {
	cmd := NewOperationCommandWithLoader(nil, nil)
	out := bytes.Buffer{}
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"run", "--help"})
	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	for _, want := range []string{"--auth-path", "--allow-plugin-auth-env", "--allow-private-network"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("help = %q, want %s", out.String(), want)
		}
	}
}

func TestOperationRunCommandPassesAuthPathToPluginFactory(t *testing.T) {
	dir := t.TempDir()
	writeOperationRunManifest(t, dir)
	authPath := t.TempDir()
	ref := coresecret.Plugin("test", "main", "token")
	store := sharedsecret.NewFileStore(authPath)
	if err := store.SaveSecret(context.Background(), sharedsecret.StoredSecret{Ref: ref, Kind: coresecret.KindAPIKey, Value: "stored-token"}); err != nil {
		t.Fatalf("SaveSecret: %v", err)
	}
	var resolved bool

	cmd := NewOperationCommandWithLoader(nil, func(ctx PluginFactoryContext) []pluginhost.Plugin {
		material, ok, err := ctx.NativeAuthResolver.ResolveSecret(context.Background(), ref)
		if err != nil {
			t.Fatalf("ResolveSecret: %v", err)
		}
		resolved = ok && material.String() == "stored-token"
		return append(availablePluginsWithAuth(ctx.System, nil, ctx.Dispatcher, ctx.TaskRunner, ctx.NativeAuthStore, ctx.NativeAuthResolver), echo.New())
	})
	out := bytes.Buffer{}
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"run", "--app", dir, "--auth-path", authPath, "echo", `{"message":"hi"}`})
	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("Execute: %v\n%s", err, out.String())
	}
	if !resolved {
		t.Fatal("plugin factory did not resolve stored auth-path secret")
	}
}

func TestOperationRunCommandProcessAuthEnvRequiresOptIn(t *testing.T) {
	dir := t.TempDir()
	writeOperationRunManifest(t, dir)
	t.Setenv("OP_RUN_TEST_SECRET", "from-process")
	ref := coresecret.Env("OP_RUN_TEST_SECRET")
	for _, tc := range []struct {
		name      string
		args      []string
		wantFound bool
	}{
		{name: "default", args: []string{"run", "--app", dir, "echo", `{"message":"hi"}`}},
		{name: "allowed", args: []string{"run", "--app", dir, "--allow-plugin-auth-env", "echo", `{"message":"hi"}`}, wantFound: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var found bool
			cmd := NewOperationCommandWithLoader(nil, func(ctx PluginFactoryContext) []pluginhost.Plugin {
				material, ok, err := ctx.NativeAuthResolver.ResolveSecret(context.Background(), ref)
				if err != nil {
					t.Fatalf("ResolveSecret: %v", err)
				}
				found = ok && material.String() == "from-process"
				return append(availablePluginsWithAuth(ctx.System, nil, ctx.Dispatcher, ctx.TaskRunner, ctx.NativeAuthStore, ctx.NativeAuthResolver), echo.New())
			})
			out := bytes.Buffer{}
			cmd.SetOut(&out)
			cmd.SetErr(&out)
			cmd.SetArgs(tc.args)
			if err := cmd.ExecuteContext(context.Background()); err != nil {
				t.Fatalf("Execute: %v\n%s", err, out.String())
			}
			if found != tc.wantFound {
				t.Fatalf("process env resolved = %v, want %v", found, tc.wantFound)
			}
		})
	}
}

func writeOperationRunManifest(t *testing.T, dir string) {
	t.Helper()
	data := []byte(`
kind: app
name: op-smoke
default_agent:
  name: default
plugins:
  echo: ~
---
kind: session
name: default
agent: default
---
kind: agent
name: default
tools: [echo]
`)
	if err := os.WriteFile(filepath.Join(dir, "fluxplane.yaml"), data, 0o600); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
}
