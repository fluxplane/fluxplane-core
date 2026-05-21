package codershell

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestParseConnectEndpoint(t *testing.T) {
	for _, tc := range []struct {
		value    string
		wantMode ClientMode
		want     string
	}{
		{"", ClientModeDirect, "direct"},
		{"direct", ClientModeDirect, "direct"},
		{"fake", ClientModeFake, "fake"},
		{"unix:///tmp/coder.sock", ClientModeLocal, "unix:///tmp/coder.sock"},
		{"https://localhost:4321", ClientModeRemote, "https://localhost:4321"},
		{"a2a://my-agents.com/foo", ClientModeRemote, "a2a://my-agents.com/foo"},
	} {
		got, err := parseConnectEndpoint(tc.value)
		if err != nil {
			t.Fatalf("parseConnectEndpoint(%q) error = %v", tc.value, err)
		}
		if got.Mode != tc.wantMode || got.Endpoint != tc.want {
			t.Fatalf("parseConnectEndpoint(%q) = %+v, want mode=%q endpoint=%q", tc.value, got, tc.wantMode, tc.want)
		}
	}
	if _, err := parseConnectEndpoint("bogus"); err == nil {
		t.Fatal("parseConnectEndpoint(bogus) error is nil")
	}
}

func TestNewCommandExposesConnectFlag(t *testing.T) {
	cmd := NewCommand()
	if cmd.Use != "shell [path]" {
		t.Fatalf("Use = %q", cmd.Use)
	}
	if cmd.Flags().Lookup("connect") == nil {
		t.Fatal("--connect flag missing")
	}
}

func TestNewCommandHelpDescribesShellModes(t *testing.T) {
	cmd := NewCommand()
	for _, want := range []string{"ask mode", "shell mode", "/ for coder commands", "@ to mention"} {
		if !strings.Contains(cmd.Long, want) {
			t.Fatalf("Long help missing %q:\n%s", want, cmd.Long)
		}
	}
	for _, want := range []string{"coder shell ~/src/project", "--connect=fake"} {
		if !strings.Contains(cmd.Example, want) {
			t.Fatalf("Examples missing %q:\n%s", want, cmd.Example)
		}
	}
}

func TestNewCommandExposesLocalLaunchFlags(t *testing.T) {
	cmd := NewCommand()
	for _, name := range []string{
		"connect",
		"provider",
		"model",
		"thinking",
		"effort",
		"debug",
		"yolo",
		"dev",
		"connectors-path",
		"allow-plugin-auth-env",
		"env-file",
		"workspace-root",
	} {
		if cmd.Flags().Lookup(name) == nil {
			t.Fatalf("--%s flag missing", name)
		}
	}
}

func TestCommandPassesLocalLaunchFlagsToClientFactory(t *testing.T) {
	sentinel := errors.New("stop after capture")
	var captured ClientFactoryRequest
	cmd := NewCommandWithOptions(CommandOptions{
		ClientFactory: func(_ context.Context, req ClientFactoryRequest) (ClientFactoryResult, error) {
			captured = req
			return ClientFactoryResult{}, sentinel
		},
	})
	cmd.SetArgs([]string{
		"--provider", "codex",
		"--model", "gpt-5.5",
		"--thinking", "on",
		"--effort", "high",
		"--debug",
		"--yolo",
		"--dev",
		"--connectors-path", "/tmp/connectors",
		"--allow-plugin-auth-env",
		"--env-file", ".env",
		"--workspace-root", "extra=/tmp/extra",
		"/workspace",
	})

	err := cmd.Execute()
	if !errors.Is(err, sentinel) {
		t.Fatalf("Execute error = %v, want sentinel", err)
	}
	if captured.Path != "/workspace" {
		t.Fatalf("path = %q, want /workspace", captured.Path)
	}
	if captured.Provider != "codex" || captured.Model != "gpt-5.5" {
		t.Fatalf("provider/model = %q/%q, want codex/gpt-5.5", captured.Provider, captured.Model)
	}
	if captured.Thinking != "on" || !captured.ThinkingSet || captured.Effort != "high" || !captured.EffortSet {
		t.Fatalf("reasoning = %#v", captured)
	}
	if !captured.Debug || !captured.Yolo || !captured.Dev {
		t.Fatalf("runtime flags = %#v, want debug/yolo/dev", captured)
	}
	if captured.AuthPath != "/tmp/connectors" {
		t.Fatalf("auth path = %q, want /tmp/connectors", captured.AuthPath)
	}
	if !captured.AllowPluginAuthEnv {
		t.Fatalf("allow plugin auth env = false, want true")
	}
	if len(captured.EnvFiles) != 1 || captured.EnvFiles[0] != ".env" {
		t.Fatalf("env files = %#v, want .env", captured.EnvFiles)
	}
	if len(captured.WorkspaceRoots) != 1 || captured.WorkspaceRoots[0] != "extra=/tmp/extra" {
		t.Fatalf("workspace roots = %#v, want extra=/tmp/extra", captured.WorkspaceRoots)
	}
}

func TestCommandRejectsLocalLaunchFlagsForRemoteConnect(t *testing.T) {
	cmd := NewCommandWithOptions(CommandOptions{
		ClientFactory: func(context.Context, ClientFactoryRequest) (ClientFactoryResult, error) {
			t.Fatal("ClientFactory called for remote connect")
			return ClientFactoryResult{}, nil
		},
	})
	cmd.SetArgs([]string{"--connect", "http://example.test:4321", "--model", "gpt-5.5"})

	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "--model is only supported with --connect=direct") {
		t.Fatalf("Execute error = %v, want local-only model error", err)
	}
}

func TestCommandValidatesReasoningFlags(t *testing.T) {
	cmd := NewCommand()
	cmd.SetArgs([]string{"--thinking", "auth"})

	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "invalid --thinking") {
		t.Fatalf("Execute error = %v, want invalid thinking", err)
	}
}
