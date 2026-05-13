package local

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestLoadManifestCreatesEphemeralDistribution(t *testing.T) {
	dir := t.TempDir()
	writeManifest(t, dir, `
kind: app
name: sample
description: Sample app.
model_policy:
  provider: openai
  model: gpt-test
  use_case: coding
connectors:
  slack-prod:
    kind: slack
daemon:
  listeners:
    - name: control
      type: http
      addr: agentsdk-local.sock
      auth: {mode: local_socket}
  channels:
    - name: local
      type: direct
      listener: control
      session: main
---
kind: session
name: main
agent: assistant
---
kind: agent
name: assistant
`)

	loaded, err := Load(context.Background(), dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.Root != filepath.Clean(dir) {
		t.Fatalf("root = %q, want %q", loaded.Root, dir)
	}
	if loaded.Manifest == "" {
		t.Fatalf("manifest path is empty")
	}
	if loaded.Distribution.Spec.Name != "sample" {
		t.Fatalf("distribution name = %q, want sample", loaded.Distribution.Spec.Name)
	}
	if loaded.Distribution.Spec.DefaultSession.Name != "main" {
		t.Fatalf("default session = %q, want main", loaded.Distribution.Spec.DefaultSession.Name)
	}
	if loaded.Distribution.Spec.DefaultModel.Model != "gpt-test" {
		t.Fatalf("default model = %q, want gpt-test", loaded.Distribution.Spec.DefaultModel.Model)
	}
	if loaded.Distribution.Runtime == nil {
		t.Fatalf("runtime is nil")
	}
	if len(loaded.Launch.Connectors) != 1 || loaded.Launch.Connectors["slack-prod"].Kind != "slack" {
		t.Fatalf("connectors = %#v, want slack-prod", loaded.Launch.Connectors)
	}
	if len(loaded.Launch.Listeners) != 1 || loaded.Launch.Listeners[0].Name != "control" {
		t.Fatalf("listeners = %#v, want control", loaded.Launch.Listeners)
	}
	if len(loaded.Launch.Channels) != 1 || loaded.Launch.Channels[0].Session != "main" {
		t.Fatalf("channels = %#v, want main direct channel", loaded.Launch.Channels)
	}
}

func TestLoadUsesPathNameAndReportsNoDefaultSessionWhenAbsent(t *testing.T) {
	dir := t.TempDir()
	loaded, err := Load(context.Background(), dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.Distribution.Spec.Name != filepath.Base(dir) {
		t.Fatalf("name = %q, want path base", loaded.Distribution.Spec.Name)
	}
	if loaded.Distribution.Spec.DefaultSession.Name != "" {
		t.Fatalf("default session = %q, want empty", loaded.Distribution.Spec.DefaultSession.Name)
	}
}

func TestLoadExposesGeneratedDefaultSessionForDefaultAgent(t *testing.T) {
	dir := t.TempDir()
	writeManifest(t, dir, `
kind: app
name: sample
default_agent: assistant
---
kind: agent
name: assistant
`)

	loaded, err := Load(context.Background(), dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.Distribution.Spec.DefaultSession.Name != "default" {
		t.Fatalf("default session = %q, want default", loaded.Distribution.Spec.DefaultSession.Name)
	}
}

func writeManifest(t *testing.T, dir, data string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "agentsdk.app.yaml"), []byte(data), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
}
