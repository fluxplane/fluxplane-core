package deploy

import (
	"bytes"
	"context"
	"io"
	"path/filepath"
	"strings"
	"testing"
)

func TestDeployDockerComposeBuildsAndStartsCompose(t *testing.T) {
	_, app := testRepo(t, `
kind: app
name: sample
distribution:
  name: sample
  build:
    assets: [agentsdk.app.yaml]
    docker:
      image: sample
      tags: [latest]
---
kind: agent
name: assistant
`)
	var calls []string
	runner := CommandRunnerFunc(func(_ context.Context, _ string, name string, args []string, _, _ io.Writer) error {
		calls = append(calls, name+" "+strings.Join(args, " "))
		return nil
	})

	result, err := DeployDockerCompose(context.Background(), ComposeDeployOptions{
		AppDir:  app,
		TempDir: t.TempDir(),
		Image:   "sample:test",
		Force:   true,
		Detach:  true,
		Runner:  runner,
	})
	if err != nil {
		t.Fatalf("DeployDockerCompose: %v", err)
	}
	if len(calls) != 3 {
		t.Fatalf("calls = %#v, want base build, app build, compose up", calls)
	}
	for _, want := range []string{
		"docker build -t fluxplane/coder-base:local ",
		"docker build -t sample:test -f " + filepath.Join(app, "Dockerfile") + " " + app,
		"docker compose -f " + filepath.Join(app, "docker-compose.yaml") + " up -d",
	} {
		found := false
		for _, call := range calls {
			if strings.Contains(call, want) {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("calls = %#v, missing %q", calls, want)
		}
	}
	if result.AppBuild.Compose != filepath.Join(app, "docker-compose.yaml") {
		t.Fatalf("compose = %q", result.AppBuild.Compose)
	}
}

func TestDeployDockerComposeDryRunRunsNothing(t *testing.T) {
	_, app := testRepo(t, `
kind: app
name: sample
distribution:
  build:
    assets: [agentsdk.app.yaml]
    docker: {}
---
kind: agent
name: assistant
`)
	var calls []string
	var out bytes.Buffer
	runner := CommandRunnerFunc(func(_ context.Context, _ string, name string, args []string, _, _ io.Writer) error {
		calls = append(calls, name+" "+strings.Join(args, " "))
		return nil
	})
	result, err := DeployDockerCompose(context.Background(), ComposeDeployOptions{
		AppDir:  app,
		TempDir: t.TempDir(),
		DryRun:  true,
		Out:     &out,
		Runner:  runner,
	})
	if err != nil {
		t.Fatalf("DeployDockerCompose dry-run: %v", err)
	}
	if len(calls) != 0 {
		t.Fatalf("dry-run calls = %#v, want none", calls)
	}
	for _, want := range []string{
		"command=docker build -t fluxplane/coder-base:local",
		"write=" + filepath.Join(app, "Dockerfile"),
		"write=" + filepath.Join(app, "docker-compose.yaml"),
		"command=docker compose -f " + filepath.Join(app, "docker-compose.yaml") + " up",
	} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("dry-run output missing %q:\n%s", want, out.String())
		}
	}
	if !result.DryRun {
		t.Fatalf("result dry-run = false")
	}
}

func TestUndeployDockerComposeDryRunPreservesVolumes(t *testing.T) {
	_, app := testRepo(t, `
kind: app
name: sample
distribution:
  build:
    assets: [agentsdk.app.yaml]
    docker: {}
---
kind: agent
name: assistant
`)
	var calls []string
	var out bytes.Buffer
	runner := CommandRunnerFunc(func(_ context.Context, _ string, name string, args []string, _, _ io.Writer) error {
		calls = append(calls, name+" "+strings.Join(args, " "))
		return nil
	})

	result, err := UndeployDockerCompose(context.Background(), ComposeUndeployOptions{
		AppDir: app,
		DryRun: true,
		Out:    &out,
		Runner: runner,
	})
	if err != nil {
		t.Fatalf("UndeployDockerCompose dry-run: %v", err)
	}
	if len(calls) != 0 {
		t.Fatalf("dry-run calls = %#v, want none", calls)
	}
	want := "command=docker compose -f " + filepath.Join(app, "docker-compose.yaml") + " down"
	if !strings.Contains(out.String(), want) {
		t.Fatalf("dry-run output = %q, want %q", out.String(), want)
	}
	if strings.Contains(out.String(), " -v") || result.Volumes {
		t.Fatalf("dry-run should preserve volumes: output=%q result=%#v", out.String(), result)
	}
}

func TestUndeployDockerComposeDryRunDeletesVolumesWhenRequested(t *testing.T) {
	_, app := testRepo(t, `
kind: app
name: sample
distribution:
  build:
    assets: [agentsdk.app.yaml]
    docker: {}
---
kind: agent
name: assistant
`)
	var out bytes.Buffer
	result, err := UndeployDockerCompose(context.Background(), ComposeUndeployOptions{
		AppDir:  app,
		DryRun:  true,
		Volumes: true,
		Out:     &out,
	})
	if err != nil {
		t.Fatalf("UndeployDockerCompose dry-run: %v", err)
	}
	want := "command=docker compose -f " + filepath.Join(app, "docker-compose.yaml") + " down -v"
	if !strings.Contains(out.String(), want) {
		t.Fatalf("dry-run output = %q, want %q", out.String(), want)
	}
	if !result.Volumes {
		t.Fatalf("result.Volumes = false, want true")
	}
}

func TestGenerateDockerComposeDryRun(t *testing.T) {
	_, app := testRepo(t, `
kind: app
name: Sample App
distribution:
  name: Sample App
  build:
    assets: [agentsdk.app.yaml]
    docker:
      image: sample
      tags: [latest]
---
kind: agent
name: assistant
`)
	var out bytes.Buffer
	result, err := GenerateDockerCompose(context.Background(), ComposeOptions{
		AppDir: app,
		DryRun: true,
		Out:    &out,
	})
	if err != nil {
		t.Fatalf("GenerateDockerCompose: %v", err)
	}
	for _, want := range []string{
		"services:",
		"    sample-app:",
		"        image: sample:latest",
		"        command: [app, serve, /app, --connectors-path, /connectors, --health-addr, '127.0.0.1:18080', --provider, openrouter, --model, openai/gpt-5.5, --effort, medium]",
		"        environment:",
		"            OPENROUTER_API_KEY: ${OPENROUTER_API_KEY:?OPENROUTER_API_KEY is required}",
	} {
		if !strings.Contains(result.Content, want) {
			t.Fatalf("compose missing %q:\n%s", want, result.Content)
		}
		if !strings.Contains(out.String(), want) {
			t.Fatalf("dry-run output missing %q:\n%s", want, out.String())
		}
	}
}

func TestGenerateDockerComposeUsesModelRegistryDeployOverride(t *testing.T) {
	_, app := testRepo(t, `
kind: app
name: sample
models:
  default: smart_model
  available:
    - provider: openrouter
      model: openai/gpt-5.5
      aliases: [smart_model]
      params:
        effort: medium
    - provider: codex
      model: gpt-5.5
      aliases: [deploy_model]
      params:
        effort: high
distribution:
  deploy:
    model: deploy_model
  build:
    assets: [agentsdk.app.yaml]
    docker:
      image: sample
      tags: [latest]
---
kind: agent
name: assistant
`)
	result, err := GenerateDockerCompose(context.Background(), ComposeOptions{
		AppDir: app,
		DryRun: true,
	})
	if err != nil {
		t.Fatalf("GenerateDockerCompose: %v", err)
	}
	for _, want := range []string{
		"        command: [app, serve, /app, --connectors-path, /connectors, --health-addr, '127.0.0.1:18080', --provider, codex, --model, gpt-5.5, --effort, high]",
	} {
		if !strings.Contains(result.Content, want) {
			t.Fatalf("compose missing %q:\n%s", want, result.Content)
		}
	}
	if strings.Contains(result.Content, "OPENROUTER_API_KEY") {
		t.Fatalf("compose = %s, want no OpenRouter env for codex deploy override", result.Content)
	}
}

func TestGenerateDockerComposeFallsBackToModelsDefault(t *testing.T) {
	_, app := testRepo(t, `
kind: app
name: sample
models:
  default: smart_model
  available:
    - provider: openrouter
      model: openai/gpt-5.5
      aliases: [smart_model]
      params:
        effort: high
distribution:
  build:
    assets: [agentsdk.app.yaml]
    docker:
      image: sample
      tags: [latest]
---
kind: agent
name: assistant
`)
	result, err := GenerateDockerCompose(context.Background(), ComposeOptions{
		AppDir: app,
		DryRun: true,
	})
	if err != nil {
		t.Fatalf("GenerateDockerCompose: %v", err)
	}
	for _, want := range []string{
		"        command: [app, serve, /app, --connectors-path, /connectors, --health-addr, '127.0.0.1:18080', --provider, openrouter, --model, openai/gpt-5.5, --effort, high]",
		"            OPENROUTER_API_KEY: ${OPENROUTER_API_KEY:?OPENROUTER_API_KEY is required}",
	} {
		if !strings.Contains(result.Content, want) {
			t.Fatalf("compose missing %q:\n%s", want, result.Content)
		}
	}
}

func TestGenerateDockerComposeIncludesRuntimeBackends(t *testing.T) {
	_, app := testRepo(t, `
kind: app
name: support-bot
runtime:
  data:
    store:
      kind: mysql
      dsn_env: AGENTRUNTIME_DATASTORE_MYSQL_DSN
  events:
    store:
      kind: nats
      dsn_env: AGENTRUNTIME_EVENTSTORE_NATS_DSN
      stream: AGENTRUNTIME_EVENTS
      subject: agentruntime.events.log
      create_stream: true
distribution:
  name: support-bot
  build:
    assets: [agentsdk.app.yaml]
    docker:
      image: support-bot
      tags: [local]
---
kind: agent
name: assistant
`)
	result, err := GenerateDockerCompose(context.Background(), ComposeOptions{
		AppDir: app,
		Image:  "support-bot:test",
		DryRun: true,
	})
	if err != nil {
		t.Fatalf("GenerateDockerCompose: %v", err)
	}
	for _, want := range []string{
		"        image: support-bot:test",
		"            AGENTRUNTIME_DATASTORE_MYSQL_DSN: agentruntime:agentruntime@tcp(mysql:3306)/agentruntime?parseTime=true&multiStatements=true",
		"            AGENTRUNTIME_EVENTSTORE_NATS_DSN: nats://nats:4222",
		"            OPENROUTER_API_KEY: ${OPENROUTER_API_KEY:?OPENROUTER_API_KEY is required}",
		"            mysql:",
		"                condition: service_healthy",
		"            nats:",
		"    mysql:",
		"        image: mysql:8.4",
		"    nats:",
		"        image: nats:2.11-alpine",
		"        command: [-js, -sd, /data, -m, \"8222\"]",
		"            test: [CMD-SHELL, 'wget -q -O - http://127.0.0.1:8222/healthz | grep -q ok']",
		"volumes:",
		"    mysql-data: {}",
		"    nats-data: {}",
	} {
		if !strings.Contains(result.Content, want) {
			t.Fatalf("compose missing %q:\n%s", want, result.Content)
		}
	}
}
