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
    assets: [agentsdk.app.yaml]
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
		"FROM fluxplane/coder-base:local",
		"COPY . /app",
		`CMD ["app","serve","/app","--connectors-path","/connectors","--health-addr","127.0.0.1:18080","--provider","openrouter","--model","openai/gpt-5.5","--effort","medium"]`,
		`HEALTHCHECK --interval=10s --timeout=3s --start-period=20s --retries=12 CMD ["/usr/local/bin/coder","app","healthcheck","--url","http://127.0.0.1:18080/control/status"]`,
	} {
		if !strings.Contains(string(dockerfile), want) {
			t.Fatalf("Dockerfile missing %q:\n%s", want, dockerfile)
		}
	}
	launcher, err := os.ReadFile(filepath.Join(app, "bin", "sample"))
	if err != nil {
		t.Fatalf("ReadFile launcher: %v", err)
	}
	if !strings.Contains(string(launcher), "exec coder app run '"+app+"'") {
		t.Fatalf("launcher = %s", launcher)
	}
	if _, err := os.Stat(filepath.Join(app, "docker-compose.yaml")); err != nil {
		t.Fatalf("compose artifact: %v", err)
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
    assets: [agentsdk.app.yaml]
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
	for _, want := range []string{"write=" + result.Binary, "write=" + result.Dockerfile, "write=" + result.Compose, "command=docker build"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("dry-run output missing %q:\n%s", want, out.String())
		}
	}
}

func TestBuildAppDoesNotOverwriteWithoutForce(t *testing.T) {
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
    assets: [agentsdk.app.yaml]
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
