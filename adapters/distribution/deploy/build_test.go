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

func TestBuildDockerGeneratesContextAndRunsDockerBuild(t *testing.T) {
	repo, app := testRepo(t, `
kind: app
name: sample
distribution:
  name: sample
  build:
    assets:
      - agentsdk.app.yaml
      - docs/**/*.md
    docker:
      image: sample
      tags: [latest]
---
kind: agent
name: assistant
`)
	writeTestFile(t, app, "docs/nested/readme.md", "hello")
	var gotName string
	var gotArgs []string
	runner := CommandRunnerFunc(func(_ context.Context, _ string, name string, args []string, _, _ io.Writer) error {
		gotName = name
		gotArgs = append([]string(nil), args...)
		return nil
	})

	result, err := BuildDocker(context.Background(), DockerBuildOptions{
		AppDir: app,
		Runner: runner,
	})
	if err != nil {
		t.Fatalf("BuildDocker: %v", err)
	}
	if gotName != "docker" || strings.Join(gotArgs, " ") != "build -t sample:latest "+result.ContextDir {
		t.Fatalf("docker command = %s %s", gotName, strings.Join(gotArgs, " "))
	}
	if _, err := os.Stat(filepath.Join(result.ContextDir, "app")); !os.IsNotExist(err) {
		t.Fatalf("context dir was not cleaned after build: %v", err)
	}
	if repo == "" {
		t.Fatalf("repo root is empty")
	}
}

func TestBuildDockerDryRunKeepsExpectedGeneratedFiles(t *testing.T) {
	_, app := testRepo(t, `
kind: app
name: sample
distribution:
  build:
    assets:
      - agentsdk.app.yaml
      - docs/**/*.md
    docker: {}
---
kind: agent
name: assistant
`)
	writeTestFile(t, app, "docs/nested/readme.md", "hello")
	var out bytes.Buffer

	result, err := BuildDocker(context.Background(), DockerBuildOptions{
		AppDir:         app,
		Tags:           []string{"example.com/sample:v1"},
		Platforms:      []string{"linux/amd64"},
		ConnectorsPath: "/secrets/connectors",
		DryRun:         true,
		Out:            &out,
	})
	if err != nil {
		t.Fatalf("BuildDocker dry run: %v", err)
	}
	defer func() { _ = os.RemoveAll(result.ContextDir) }()
	dockerfile, err := os.ReadFile(result.Dockerfile)
	if err != nil {
		t.Fatalf("ReadFile Dockerfile: %v", err)
	}
	text := string(dockerfile)
	for _, want := range []string{
		"FROM golang:1.26-bookworm AS builder",
		"FROM debian:bookworm-slim AS runtime",
		`CMD ["serve","/app","--connectors-path","/secrets/connectors"]`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("Dockerfile missing %q:\n%s", want, text)
		}
	}
	if _, err := os.Stat(filepath.Join(result.ContextDir, "app", "docs", "nested", "readme.md")); err != nil {
		t.Fatalf("asset copy: %v", err)
	}
	if !strings.Contains(out.String(), "docker buildx build --platform linux/amd64 -t example.com/sample:v1 --load") {
		t.Fatalf("dry run output = %s", out.String())
	}
}

func TestBuildDockerCopiesLocalReplaceModules(t *testing.T) {
	parent := t.TempDir()
	repo := filepath.Join(parent, "projects", "agentsdk", "rewrite")
	app := filepath.Join(repo, "examples", "sample")
	writeTestFile(t, repo, "go.mod", "module github.com/fluxplane/agentruntime\n\nreplace github.com/codewandler/axon => ../../axon\n")
	writeTestFile(t, repo, "cmd/agentsdk/main.go", "package main\nfunc main() {}\n")
	writeTestFile(t, filepath.Join(parent, "projects"), "axon/go.mod", "module github.com/codewandler/axon\n")
	writeTestFile(t, app, "agentsdk.app.yaml", `
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

	result, err := BuildDocker(context.Background(), DockerBuildOptions{
		AppDir: app,
		DryRun: true,
	})
	if err != nil {
		t.Fatalf("BuildDocker: %v", err)
	}
	defer func() { _ = os.RemoveAll(result.ContextDir) }()
	dockerfile, err := os.ReadFile(result.Dockerfile)
	if err != nil {
		t.Fatalf("ReadFile Dockerfile: %v", err)
	}
	if !strings.Contains(string(dockerfile), "COPY localmods/axon /axon") {
		t.Fatalf("Dockerfile = %s", dockerfile)
	}
	if _, err := os.Stat(filepath.Join(result.ContextDir, "localmods", "axon", "go.mod")); err != nil {
		t.Fatalf("local replace copy: %v", err)
	}
}

func TestDockerCommandPlatformModes(t *testing.T) {
	tests := []struct {
		name      string
		platforms []string
		push      bool
		want      string
		wantErr   string
	}{
		{name: "default", want: "docker build -t sample:latest"},
		{name: "single load", platforms: []string{"linux/amd64"}, want: "docker buildx build --platform linux/amd64 -t sample:latest --load"},
		{name: "single push", platforms: []string{"linux/amd64"}, push: true, want: "docker buildx build --platform linux/amd64 -t sample:latest --push"},
		{name: "multi push", platforms: []string{"linux/amd64", "linux/arm64"}, push: true, want: "docker buildx build --platform linux/amd64,linux/arm64 -t sample:latest --push"},
		{name: "multi without push", platforms: []string{"linux/amd64", "linux/arm64"}, wantErr: "multiple platforms require --push"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := dockerCommand([]string{"sample:latest"}, tt.platforms, tt.push)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("dockerCommand error = %v, want %s", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("dockerCommand: %v", err)
			}
			if strings.Join(got, " ") != tt.want {
				t.Fatalf("dockerCommand = %s, want %s", strings.Join(got, " "), tt.want)
			}
		})
	}
}

func TestBuildDockerRejectsUnsafeAssets(t *testing.T) {
	_, app := testRepo(t, `
kind: app
name: sample
distribution:
  build:
    assets:
      - ../secret.txt
    docker: {}
---
kind: agent
name: assistant
`)
	_, err := BuildDocker(context.Background(), DockerBuildOptions{AppDir: app, DryRun: true})
	if err == nil || !strings.Contains(err.Error(), "escapes the app root") {
		t.Fatalf("BuildDocker error = %v, want asset escape error", err)
	}
}

func testRepo(t *testing.T, manifest string) (string, string) {
	t.Helper()
	repo := t.TempDir()
	writeTestFile(t, repo, "go.mod", "module github.com/fluxplane/agentruntime\n")
	writeTestFile(t, repo, "cmd/agentsdk/main.go", "package main\nfunc main() {}\n")
	app := filepath.Join(repo, "examples", "sample")
	writeTestFile(t, app, "agentsdk.app.yaml", manifest)
	return repo, app
}

func writeTestFile(t *testing.T, root, rel, data string) {
	t.Helper()
	filename := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(filename), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filename, []byte(data), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
}
