package deploy

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildAppDefaultGeneratesArtifactsAndBuildsImage(t *testing.T) {
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
	var gotName string
	var gotArgs []string
	runner := CommandRunnerFunc(func(_ context.Context, _ string, name string, args []string, _, _ io.Writer) error {
		gotName = name
		gotArgs = append([]string(nil), args...)
		return nil
	})

	result, err := BuildApp(context.Background(), AppBuildOptions{
		AppDir: app,
		Runner: runner,
	})
	if err != nil {
		t.Fatalf("BuildApp: %v", err)
	}
	if result.Binary != filepath.Join(app, "bin", "sample") || result.Dockerfile != filepath.Join(app, "Dockerfile") || result.Compose != filepath.Join(app, "docker-compose.yaml") {
		t.Fatalf("artifacts = %#v", result)
	}
	if gotName != "docker" || strings.Join(gotArgs, " ") != "build -t sample:latest -f "+filepath.Join(app, "Dockerfile")+" "+app {
		t.Fatalf("docker command = %s %s", gotName, strings.Join(gotArgs, " "))
	}
	dockerfile, err := os.ReadFile(filepath.Join(app, "Dockerfile"))
	if err != nil {
		t.Fatalf("ReadFile Dockerfile: %v", err)
	}
	for _, want := range []string{
		"FROM fluxplane/fluxplane-base:local",
		"COPY . /app",
		`CMD ["serve","/app","--auth-path","/auth","--health-addr","127.0.0.1:18080","--provider","openrouter","--model","openai/gpt-5.5","--effort","medium"]`,
		`HEALTHCHECK --interval=10s --timeout=3s --start-period=20s --retries=12 CMD ["/usr/local/bin/fluxplane","healthcheck","--url","http://127.0.0.1:18080/control/status"]`,
	} {
		if !strings.Contains(string(dockerfile), want) {
			t.Fatalf("Dockerfile missing %q:\n%s", want, dockerfile)
		}
	}
	if _, err := os.Stat(filepath.Join(app, "docker-compose.yaml")); err != nil {
		t.Fatalf("compose artifact: %v", err)
	}
	if _, err := os.Stat(filepath.Join(app, "sample.md")); err != nil {
		t.Fatalf("documentation artifact: %v", err)
	}
	if _, err := os.Stat(filepath.Join(app, ".deploy", "fluxplane-build.json")); err != nil {
		t.Fatalf("artifact index: %v", err)
	}
}

func TestBuildAppIncludesPluginAuthEnvFlagWhenRequested(t *testing.T) {
	_, app := testRepo(t, `
kind: app
name: sample
distribution:
  name: sample
---
kind: agent
name: assistant
`)
	result, err := BuildApp(context.Background(), AppBuildOptions{
		AppDir:             app,
		Targets:            []string{"dockerfile"},
		AllowPluginAuthEnv: true,
	})
	if err != nil {
		t.Fatalf("BuildApp: %v", err)
	}
	dockerfile, err := os.ReadFile(result.Dockerfile)
	if err != nil {
		t.Fatalf("ReadFile Dockerfile: %v", err)
	}
	if !strings.Contains(string(dockerfile), `"--allow-plugin-auth-env"`) {
		t.Fatalf("Dockerfile missing --allow-plugin-auth-env:\n%s", dockerfile)
	}
}

func TestBuildAppDryRunWritesNothing(t *testing.T) {
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
	result, err := BuildApp(context.Background(), AppBuildOptions{
		AppDir: app,
		DryRun: true,
		Out:    &out,
	})
	if err != nil {
		t.Fatalf("BuildApp dry-run: %v", err)
	}
	if _, err := os.Stat(result.Dockerfile); !os.IsNotExist(err) {
		t.Fatalf("Dockerfile exists after dry-run: %v", err)
	}
	for _, want := range []string{"write=" + result.Binary, "write=" + result.Dockerfile, "write=" + result.Compose, "write=" + result.Documentation, "write=" + result.ArtifactIndex, "command=docker build"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("dry-run output missing %q:\n%s", want, out.String())
		}
	}
}

func TestBuildAppNamedDocumentationTarget(t *testing.T) {
	_, app := testRepo(t, `
kind: app
name: sample
distribution:
  name: sample
  build:
    targets:
      capabilities:
        kind: documentation
        output: docs/capabilities.md
plugins:
  memory: ~
---
kind: agent
name: assistant
description: Helps users.
`)
	result, err := BuildApp(context.Background(), AppBuildOptions{
		AppDir:  app,
		Targets: []string{"capabilities"},
	})
	if err != nil {
		t.Fatalf("BuildApp: %v", err)
	}
	if result.Documentation != filepath.Join(app, "docs", "capabilities.md") {
		t.Fatalf("documentation = %q", result.Documentation)
	}
	data, err := os.ReadFile(result.Documentation)
	if err != nil {
		t.Fatalf("ReadFile documentation: %v", err)
	}
	for _, want := range []string{"# sample", "## Agents", "assistant - Helps users.", "## Plugins", "memory"} {
		if !strings.Contains(string(data), want) {
			t.Fatalf("documentation missing %q:\n%s", want, data)
		}
	}
}

func TestBuildAppNamedHelmChartTarget(t *testing.T) {
	_, app := testRepo(t, `
kind: app
name: sample
distribution:
  name: sample
  build:
    targets:
      chart:
        kind: helm-chart
        image: example.com/sample
        tags: [v1]
        values:
          replicaCount: "2"
        output: chart
---
kind: agent
name: assistant
`)
	result, err := BuildApp(context.Background(), AppBuildOptions{
		AppDir:  app,
		Targets: []string{"chart"},
	})
	if err != nil {
		t.Fatalf("BuildApp: %v", err)
	}
	for _, rel := range []string{"Chart.yaml", "values.yaml", filepath.Join("templates", "app.yaml")} {
		if _, err := os.Stat(filepath.Join(result.HelmChart, rel)); err != nil {
			t.Fatalf("helm chart %s: %v", rel, err)
		}
	}
	values, err := os.ReadFile(filepath.Join(result.HelmChart, "values.yaml"))
	if err != nil {
		t.Fatalf("ReadFile values: %v", err)
	}
	if !strings.Contains(string(values), "repository: example.com/sample") || !strings.Contains(string(values), "tag: v1") || !strings.Contains(string(values), "replicaCount: 2") {
		t.Fatalf("values.yaml =\n%s", values)
	}
}

func TestBuildAppNamedHelmChartUsesExternalEnvSecret(t *testing.T) {
	_, app := testRepo(t, `
kind: app
name: sample
runtime:
  workspace:
    env_files: [.env]
distribution:
  name: sample
  build:
    targets:
      chart:
        kind: helm-chart
        image: example.com/sample
        env_secret_name: platform-managed-secret
        output: chart
---
kind: agent
name: assistant
`)
	writeTestFile(t, app, ".env", "OPENROUTER_API_KEY=supersecret\n")
	result, err := BuildApp(context.Background(), AppBuildOptions{
		AppDir:  app,
		Targets: []string{"chart"},
	})
	if err != nil {
		t.Fatalf("BuildApp: %v", err)
	}
	values, err := os.ReadFile(filepath.Join(result.HelmChart, "values.yaml"))
	if err != nil {
		t.Fatalf("ReadFile values: %v", err)
	}
	appTemplate, err := os.ReadFile(filepath.Join(result.HelmChart, "templates", "app.yaml"))
	if err != nil {
		t.Fatalf("ReadFile app template: %v", err)
	}
	for _, want := range []string{"envSecret:", "enabled: true", "name: platform-managed-secret"} {
		if !strings.Contains(string(values), want) {
			t.Fatalf("values.yaml missing %q:\n%s", want, values)
		}
	}
	for _, want := range []string{`{{ if .Values.envSecret.enabled }}`, `name: {{ .Values.envSecret.name | quote }}`} {
		if !strings.Contains(string(appTemplate), want) {
			t.Fatalf("app.yaml missing %q:\n%s", want, appTemplate)
		}
	}
	for _, leaked := range []string{"supersecret", "stringData:", "kind: Secret"} {
		if strings.Contains(string(appTemplate), leaked) || strings.Contains(string(values), leaked) {
			t.Fatalf("helm chart leaked %q:\nvalues:\n%s\ntemplate:\n%s", leaked, values, appTemplate)
		}
	}
}

func TestBuildAppMergesArtifactIndexAcrossIncrementalBuilds(t *testing.T) {
	_, app := testRepo(t, `
kind: app
name: sample
distribution:
  build:
    targets:
      docs:
        kind: documentation
        output: docs/capabilities.md
      compose:
        kind: docker-compose
        output: docker-compose.yaml
---
kind: agent
name: assistant
`)
	if _, err := BuildApp(context.Background(), AppBuildOptions{
		AppDir:  app,
		Targets: []string{"docs"},
	}); err != nil {
		t.Fatalf("BuildApp docs: %v", err)
	}
	if _, err := BuildApp(context.Background(), AppBuildOptions{
		AppDir:  app,
		Targets: []string{"compose"},
		Force:   true,
	}); err != nil {
		t.Fatalf("BuildApp compose: %v", err)
	}
	index, err := readArtifactIndex(app)
	if err != nil {
		t.Fatalf("readArtifactIndex: %v", err)
	}
	if _, ok := artifactByTarget(index, "docs"); !ok {
		t.Fatalf("artifact index missing docs: %#v", index.Targets)
	}
	if _, ok := artifactByTarget(index, "compose"); !ok {
		t.Fatalf("artifact index missing compose: %#v", index.Targets)
	}
}

func TestBuildAppDockerBaseBuildsFluxplaneBase(t *testing.T) {
	_, app := testRepo(t, `
kind: app
name: sample
---
kind: agent
name: assistant
`)
	result, err := BuildApp(context.Background(), AppBuildOptions{
		AppDir:    app,
		Targets:   []string{"docker-base"},
		BaseImage: "example.com/fluxplane-base:test",
		DryRun:    true,
	})
	if err != nil {
		t.Fatalf("BuildApp docker-base: %v", err)
	}
	if len(result.Command) == 0 || result.Command[0] != "docker" || !strings.Contains(strings.Join(result.Command, " "), "-t example.com/fluxplane-base:test") {
		t.Fatalf("command = %#v", result.Command)
	}
}

func TestBuildAppDockerImageBuildsManagedFluxplaneBaseFirst(t *testing.T) {
	_, app := testRepo(t, `
kind: app
name: sample
distribution:
  build:
    targets:
      image:
        kind: docker-image
        image: sample
        tags: [local]
---
kind: agent
name: assistant
`)
	var commands []string
	runner := CommandRunnerFunc(func(_ context.Context, _ string, name string, args []string, _, _ io.Writer) error {
		commands = append(commands, name+" "+strings.Join(args, " "))
		return nil
	})

	if _, err := BuildApp(context.Background(), AppBuildOptions{
		AppDir:  app,
		Targets: []string{"image"},
		Runner:  runner,
	}); err != nil {
		t.Fatalf("BuildApp: %v", err)
	}
	if len(commands) != 2 {
		t.Fatalf("commands = %#v, want base and app image builds", commands)
	}
	if !strings.Contains(commands[0], "-t fluxplane/fluxplane-base:local") {
		t.Fatalf("base command = %q", commands[0])
	}
	if !strings.Contains(commands[1], "-t sample:local") {
		t.Fatalf("app command = %q", commands[1])
	}
}

func TestBuildAppDockerImageDoesNotBuildCustomBase(t *testing.T) {
	_, app := testRepo(t, `
kind: app
name: sample
distribution:
  build:
    targets:
      image:
        kind: docker-image
        image: sample
        tags: [local]
        base_image: example.com/fluxplane-base:pinned
---
kind: agent
name: assistant
`)
	var commands []string
	runner := CommandRunnerFunc(func(_ context.Context, _ string, name string, args []string, _, _ io.Writer) error {
		commands = append(commands, name+" "+strings.Join(args, " "))
		return nil
	})

	if _, err := BuildApp(context.Background(), AppBuildOptions{
		AppDir:  app,
		Targets: []string{"image"},
		Runner:  runner,
	}); err != nil {
		t.Fatalf("BuildApp: %v", err)
	}
	if len(commands) != 1 || !strings.Contains(commands[0], "-t sample:local") {
		t.Fatalf("commands = %#v, want only app image build", commands)
	}
}

func TestBuildAppDoesNotOverwriteWithoutForce(t *testing.T) {
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
	writeTestFile(t, app, "Dockerfile", "custom\n")
	_, err := BuildApp(context.Background(), AppBuildOptions{
		AppDir:  app,
		Targets: []string{"dockerfile"},
	})
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("BuildApp error = %v, want overwrite guard", err)
	}
	if _, err := BuildApp(context.Background(), AppBuildOptions{
		AppDir:  app,
		Targets: []string{"dockerfile"},
		Force:   true,
	}); err != nil {
		t.Fatalf("BuildApp force: %v", err)
	}
}

func TestBuildAppNativeDockerUsesGeneratedDockerfilePath(t *testing.T) {
	_, app := testRepo(t, `
kind: app
name: sample
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
	outDir := filepath.Join(t.TempDir(), "deploy")
	dockerClient := &recordingDockerClient{}
	result, err := BuildApp(context.Background(), AppBuildOptions{
		AppDir:       app,
		OutDir:       outDir,
		Targets:      []string{"docker-image"},
		Force:        true,
		dockerClient: dockerClient,
	})
	if err != nil {
		t.Fatalf("BuildApp: %v", err)
	}
	if result.Dockerfile != filepath.Join(outDir, "Dockerfile") {
		t.Fatalf("Dockerfile = %q, want outDir Dockerfile", result.Dockerfile)
	}
	if dockerClient.contextDir != app {
		t.Fatalf("contextDir = %q, want app root %q", dockerClient.contextDir, app)
	}
	if dockerClient.dockerfile != filepath.Join(outDir, "Dockerfile") {
		t.Fatalf("dockerfile = %q, want generated Dockerfile", dockerClient.dockerfile)
	}
}
