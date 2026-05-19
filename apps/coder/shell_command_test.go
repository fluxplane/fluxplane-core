package coder

import (
	"testing"

	"github.com/fluxplane/agentruntime/core/resource"
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
}
