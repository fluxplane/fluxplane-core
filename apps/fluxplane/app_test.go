package fluxplaneapp

import (
	"bytes"
	"io"
	"strings"
	"testing"
)

func TestCommandExposesTopLevelAppCommands(t *testing.T) {
	cmd := NewCommand()
	out := bytes.Buffer{}
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"--help"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	help := out.String()
	for _, want := range []string{"init", "run", "serve", "build", "deploy", "undeploy", "config", "describe", "healthcheck", "auth", "op", "datasource", "discover"} {
		if !strings.Contains(help, want) {
			t.Fatalf("help = %q, want %s", help, want)
		}
	}
}

func TestCommandDoesNotExposeAppGroup(t *testing.T) {
	cmd := NewCommand()
	for _, child := range cmd.Commands() {
		if child.Name() == "app" {
			t.Fatalf("fluxplane command unexpectedly exposes app command")
		}
	}
}

func TestAuthStatusRequiresManifestScope(t *testing.T) {
	cmd := NewCommand()
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"auth", "status", "--no-test"})
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "no app manifest found") {
		t.Fatalf("Execute error = %v, want missing manifest scope", err)
	}
}

func TestDatasourceIndexRequiresManifestScope(t *testing.T) {
	cmd := NewCommand()
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"datasource", "index", "build", "--dry-run"})
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "no app manifest found") {
		t.Fatalf("Execute error = %v, want missing manifest scope", err)
	}
}
