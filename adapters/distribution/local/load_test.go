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
daemon:
  listeners:
    - name: control
      type: http
      addr: coder-local.sock
      auth: {mode: local_socket}
  channels:
    - name: local
      type: direct
      listener: control
      session: main
runtime:
  data:
    store:
      kind: mysql
      dsn_env: FLUXPLANE_DATASTORE_MYSQL_DSN
  events:
    store:
      kind: nats
      dsn_env: FLUXPLANE_EVENTSTORE_NATS_DSN
      stream: FLUXPLANE_EVENTS
      subject: fluxplane.events.log
      create_stream: true
  workspace:
    env_files:
      - .env
    roots:
      - name: tmp
        path: /tmp/agentruntime-sample
        access: read_write
        create: true
        env_files:
          - tmp.env
    scratch_root: tmp
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
	if len(loaded.Launch.Listeners) != 1 || loaded.Launch.Listeners[0].Name != "control" {
		t.Fatalf("listeners = %#v, want control", loaded.Launch.Listeners)
	}
	if len(loaded.Launch.Channels) != 1 || loaded.Launch.Channels[0].Session != "main" {
		t.Fatalf("channels = %#v, want main direct channel", loaded.Launch.Channels)
	}
	if loaded.Launch.Workspace.ScratchRoot != "tmp" || len(loaded.Launch.Workspace.Roots) != 1 || loaded.Launch.Workspace.Roots[0].Name != "tmp" {
		t.Fatalf("workspace = %#v, want tmp scratch root", loaded.Launch.Workspace)
	}
	if loaded.Launch.Data.Store.Kind != "mysql" || loaded.Launch.Data.Store.DSNEnv != "FLUXPLANE_DATASTORE_MYSQL_DSN" {
		t.Fatalf("data store = %#v, want mysql dsn env", loaded.Launch.Data.Store)
	}
	if loaded.Launch.Events.Store.Kind != "nats" || loaded.Launch.Events.Store.DSNEnv != "FLUXPLANE_EVENTSTORE_NATS_DSN" || loaded.Launch.Events.Store.Stream != "FLUXPLANE_EVENTS" || loaded.Launch.Events.Store.Subject != "fluxplane.events.log" || !loaded.Launch.Events.Store.CreateStream {
		t.Fatalf("event store = %#v, want nats dsn env", loaded.Launch.Events.Store)
	}
	if len(loaded.Launch.Workspace.EnvFiles) != 1 || loaded.Launch.Workspace.EnvFiles[0] != ".env" {
		t.Fatalf("root env files = %#v, want .env", loaded.Launch.Workspace.EnvFiles)
	}
	if len(loaded.Launch.Workspace.Roots[0].EnvFiles) != 1 || loaded.Launch.Workspace.Roots[0].EnvFiles[0] != "tmp.env" {
		t.Fatalf("named root env files = %#v, want tmp.env", loaded.Launch.Workspace.Roots[0].EnvFiles)
	}
}

func TestLoadOverlaysDistributionManifestMetadata(t *testing.T) {
	dir := t.TempDir()
	writeManifest(t, dir, `
kind: app
name: sample
description: Sample app.
default_agent:
  name: assistant
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
      - fluxplane.yaml
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
default_agent:
  name: assistant
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
	if err := os.WriteFile(filepath.Join(dir, "fluxplane.yaml"), []byte(data), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
}
