package deploy

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/fluxplane/fluxplane-core/orchestration/distribution"
)

func TestDeployDockerComposeBuildsAndStartsCompose(t *testing.T) {
	_, app := testRepo(t, `
kind: app
name: sample
distribution:
  name: sample
  build:
    assets: [fluxplane.yaml]
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
		"docker build -t fluxplane/fluxplane-base:local ",
		"docker build -t sample:test -f " + filepath.Join(app, "Dockerfile") + " " + app,
		"docker compose -f " + filepath.Join(app, "docker-compose.yaml") + " up -d --wait --wait-timeout 30",
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
    assets: [fluxplane.yaml]
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
		"command=docker build -t fluxplane/fluxplane-base:local",
		"write=" + filepath.Join(app, "Dockerfile"),
		"write=" + filepath.Join(app, "docker-compose.yaml"),
		"command=docker compose -f " + filepath.Join(app, "docker-compose.yaml") + " up --wait --wait-timeout 30",
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
    assets: [fluxplane.yaml]
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
    assets: [fluxplane.yaml]
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

func TestUndeployDockerComposeDryRunPassesRuntimeEnvFile(t *testing.T) {
	_, app := testRepo(t, `
kind: app
name: sample
runtime:
  data:
    store:
      kind: mysql
  events:
    store:
      kind: nats
distribution:
  build:
    assets: [fluxplane.yaml]
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
	envFile := filepath.Join(app, dockerComposeRuntimeEnvFile)
	for _, want := range []string{
		"write=" + envFile,
		"command=docker compose --env-file " + envFile + " -f " + filepath.Join(app, "docker-compose.yaml") + " down -v",
	} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("dry-run output missing %q:\n%s", want, out.String())
		}
	}
	if !slices.Equal(result.Command, dockerComposeDownCommand(filepath.Join(app, "docker-compose.yaml"), envFile, true)) {
		t.Fatalf("command = %#v", result.Command)
	}
}

func TestEnsureComposeRuntimeEnvFileMergesMissingKeys(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".deploy"), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	envFile := filepath.Join(root, dockerComposeRuntimeEnvFile)
	if err := os.WriteFile(envFile, []byte(strings.Join([]string{
		"MYSQL_DATABASE=customdb",
		"MYSQL_USER=customuser",
		"MYSQL_PASSWORD=existing-password",
		"MYSQL_ROOT_PASSWORD=existing-root",
		"",
	}, "\n")), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	launch := distribution.LaunchConfig{
		Data: distribution.DataConfig{Store: distribution.DataStoreConfig{
			Kind:   "mysql",
			DSNEnv: "CUSTOM_MYSQL_DSN",
		}},
		Events: distribution.EventsConfig{Store: distribution.EventStoreConfig{Kind: "nats"}},
	}
	if _, err := ensureComposeRuntimeEnvFile(root, launch, false, io.Discard); err != nil {
		t.Fatalf("ensureComposeRuntimeEnvFile: %v", err)
	}
	merged, err := readEnvFile(envFile)
	if err != nil {
		t.Fatalf("readEnvFile: %v", err)
	}
	for key, want := range map[string]string{
		"MYSQL_DATABASE":                "customdb",
		"MYSQL_USER":                    "customuser",
		"MYSQL_PASSWORD":                "existing-password",
		"MYSQL_ROOT_PASSWORD":           "existing-root",
		"CUSTOM_MYSQL_DSN":              "customuser:existing-password@tcp(mysql:3306)/customdb?parseTime=true&multiStatements=true",
		"FLUXPLANE_EVENTSTORE_NATS_DSN": "nats://nats:4222",
	} {
		if merged[key] != want {
			t.Fatalf("merged[%s] = %q, want %q; all=%#v", key, merged[key], want, merged)
		}
	}
}

func TestGenerateDockerComposeDryRun(t *testing.T) {
	_, app := testRepo(t, `
kind: app
name: Sample App
distribution:
  name: Sample App
  build:
    assets: [fluxplane.yaml]
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
		"  sample-app:",
		"    image: sample:latest",
		"    command: [serve, /app, --auth-path, /auth, --health-addr, '127.0.0.1:18080', --provider, openrouter, --model, openai/gpt-5.5, --effort, medium]",
		"    environment:",
		"      OPENROUTER_API_KEY: ${OPENROUTER_API_KEY:?OPENROUTER_API_KEY is required}",
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
    assets: [fluxplane.yaml]
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
		"    command: [serve, /app, --auth-path, /auth, --health-addr, '127.0.0.1:18080', --provider, codex, --model, gpt-5.5, --effort, high]",
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
    assets: [fluxplane.yaml]
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
		"    command: [serve, /app, --auth-path, /auth, --health-addr, '127.0.0.1:18080', --provider, openrouter, --model, openai/gpt-5.5, --effort, high]",
		"      OPENROUTER_API_KEY: ${OPENROUTER_API_KEY:?OPENROUTER_API_KEY is required}",
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
profiles:
  dev:
    description: Local development.
  prod:
    description: Production deployment.
distribution:
  name: support-bot
  build:
    assets: [fluxplane.yaml]
    docker:
      image: support-bot
      tags: [local]
---
kind: runtime
profile: prod
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
---
kind: agent
name: assistant
`)
	result, err := GenerateDockerCompose(context.Background(), ComposeOptions{
		AppDir:   app,
		Profiles: []string{"prod"},
		Image:    "support-bot:test",
		DryRun:   true,
	})
	if err != nil {
		t.Fatalf("GenerateDockerCompose: %v", err)
	}
	for _, want := range []string{
		"    image: support-bot:test",
		"      FLUXPLANE_DATASTORE_MYSQL_DSN: ${FLUXPLANE_DATASTORE_MYSQL_DSN:?FLUXPLANE_DATASTORE_MYSQL_DSN is required}",
		"      FLUXPLANE_EVENTSTORE_NATS_DSN: ${FLUXPLANE_EVENTSTORE_NATS_DSN:?FLUXPLANE_EVENTSTORE_NATS_DSN is required}",
		"      OPENROUTER_API_KEY: ${OPENROUTER_API_KEY:?OPENROUTER_API_KEY is required}",
		"      mysql:",
		"        condition: service_healthy",
		"      nats:",
		"  mysql:",
		"    image: mysql:8.4",
		"      MYSQL_PASSWORD: ${MYSQL_PASSWORD:?MYSQL_PASSWORD is required}",
		"  nats:",
		"    image: nats:2.11-alpine",
		"    command: [-js, -sd, /data, -m, \"8222\"]",
		"      test: [CMD-SHELL, 'wget -q -O - http://127.0.0.1:8222/healthz | grep -q ok']",
		"volumes:",
		"  mysql-data: {}",
		"  nats-data: {}",
	} {
		if !strings.Contains(result.Content, want) {
			t.Fatalf("compose missing %q:\n%s", want, result.Content)
		}
	}
}
