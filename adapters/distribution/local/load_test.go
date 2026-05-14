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

func TestLoadOverlaysDistributionManifestMetadata(t *testing.T) {
	dir := t.TempDir()
	writeManifest(t, dir, `
kind: app
name: sample
description: Sample app.
default_agent: assistant
model_policy:
  provider: openai
  model: app-model
distribution:
  name: sample-built
  title: Sample Built
  version: 2.0.0
  default_model:
    model: dist-model
  surfaces:
    cli: true
    one_shot: true
  build:
    assets:
      - agentsdk.app.yaml
      - docs/**/*.md
    docker:
      image: sample-built
      tags: [latest]
---
kind: agent
name: assistant
`)

	loaded, err := Load(context.Background(), dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	spec := loaded.Distribution.Spec
	if spec.Name != "sample-built" || spec.Title != "Sample Built" || spec.Version != "2.0.0" {
		t.Fatalf("distribution metadata = %#v", spec)
	}
	if spec.DefaultSession.Name != "default" {
		t.Fatalf("default session = %q, want generated default", spec.DefaultSession.Name)
	}
	if spec.DefaultModel.Provider != "openai" || spec.DefaultModel.Model != "dist-model" {
		t.Fatalf("default model = %#v, want provider from app and model from distribution", spec.DefaultModel)
	}
	if !spec.Surfaces.CLI || !spec.Surfaces.OneShot || spec.Surfaces.Serve {
		t.Fatalf("surfaces = %#v", spec.Surfaces)
	}
	if len(spec.Build.Assets) != 2 || spec.Build.Assets[1] != "docs/**/*.md" {
		t.Fatalf("build assets = %#v", spec.Build.Assets)
	}
	if spec.Build.Docker == nil || spec.Build.Docker.Image != "sample-built" || len(spec.Build.Docker.Tags) != 1 {
		t.Fatalf("docker build = %#v", spec.Build.Docker)
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
