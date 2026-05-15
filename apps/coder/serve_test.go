package coder

import (
	"path/filepath"
	"strings"
	"testing"

	agentruntime "github.com/fluxplane/agentruntime"
	"github.com/fluxplane/agentruntime/adapters/distribution/run"
)

func TestServeCommandHasAutomaticSocketDefault(t *testing.T) {
	cmd := newServeCommand(startupResources{Root: t.TempDir(), Bundles: []agentruntime.ResourceBundle{Bundle()}})
	if cmd.Name() != "serve" {
		t.Fatalf("command name = %q, want serve", cmd.Name())
	}
	flag := cmd.Flags().Lookup("socket")
	if flag == nil {
		t.Fatalf("serve command missing --socket flag")
	}
	if flag.DefValue != "auto" {
		t.Fatalf("socket default = %q, want auto", flag.DefValue)
	}
	modelFlag := cmd.Flags().Lookup("model")
	if modelFlag == nil {
		t.Fatalf("serve command missing --model flag")
	}
	yoloFlag := cmd.Flags().Lookup("yolo")
	if yoloFlag == nil {
		t.Fatalf("serve command missing --yolo flag")
	}
}

func TestCoderServeModelResolvesProviderQualifiedModel(t *testing.T) {
	selection := run.ResolveModelSelection("openai", "codex/gpt-5.5")
	if selection.Provider != "codex" || selection.Model != "gpt-5.5" {
		t.Fatalf("ResolveModelSelection = %q/%q, want codex/gpt-5.5", selection.Provider, selection.Model)
	}
}

func TestCoderServeModelResolvesAlias(t *testing.T) {
	selection := run.ResolveModelSelection("openai", "codex")
	if selection.Provider == "openai" || selection.Model == "" {
		t.Fatalf("ResolveModelSelection(codex) = %q/%q, want non-openai alias target", selection.Provider, selection.Model)
	}
}

func TestCoderServeSocketPathAuto(t *testing.T) {
	path := coderServeSocketPath("auto")
	if !strings.HasSuffix(path, ".sock") {
		t.Fatalf("auto socket path = %q, want .sock suffix", path)
	}
	if filepath.Base(path) == path {
		t.Fatalf("auto socket path = %q, want directory-qualified path", path)
	}
}

func TestCoderServeSocketPathExplicit(t *testing.T) {
	if got := coderServeSocketPath("custom.sock"); filepath.Base(got) != "custom.sock" || filepath.Base(got) == got {
		t.Fatalf("explicit socket path = %q, want resolved custom.sock path", got)
	}
}

func TestValidateCoderServeSocketRejectsTCP(t *testing.T) {
	if err := validateCoderServeSocket("127.0.0.1:8080"); err == nil {
		t.Fatalf("validateCoderServeSocket TCP error = nil")
	}
	if err := validateCoderServeSocket("custom.sock"); err != nil {
		t.Fatalf("validateCoderServeSocket custom.sock = %v", err)
	}
}
