package coder

import (
	"strings"
	"testing"

	"github.com/fluxplane/engine/core/resource"
)

func TestShellCommandUsesLocalDirectClientByDefault(t *testing.T) {
	cmd := newShellCommandWithStartup(startupResources{
		Root:    ".",
		Bundles: []resource.ContributionBundle{Bundle()},
	}, serveCommandOptions{})
	if cmd.Use != "shell [path]" {
		t.Fatalf("Use = %q, want shell [path]", cmd.Use)
	}
	flag := cmd.Flags().Lookup("connect")
	if flag == nil {
		t.Fatal("--connect flag missing")
	}
	if flag.DefValue != "direct" {
		t.Fatalf("--connect default = %q, want direct", flag.DefValue)
	}
	help := cmd.UsageString()
	for _, want := range []string{"--provider", "--model", "--thinking", "--effort", "--debug", "--yolo", "--dev", "--auth-path", "--allow-plugin-auth-env", "--env-file", "--workspace-root"} {
		if !strings.Contains(help, want) {
			t.Fatalf("help missing %q:\n%s", want, help)
		}
	}
}
