package deploy

import (
	"bytes"
	"context"
	"io"
	"path/filepath"
	"strings"
	"testing"
)

func TestDeployTargetDryRunBuildsAndRunsNamedComposeTarget(t *testing.T) {
	_, app := testRepo(t, `
kind: app
name: sample
distribution:
  name: sample
  build:
    assets: [fluxplane.yaml]
    targets:
      image-local:
        kind: docker-image
        image: sample:local
      compose-local:
        kind: docker-compose
        image: sample:local
        output: docker-compose.yaml
  deploy:
    targets:
      local:
        kind: docker-compose
        build: [image-local, compose-local]
        compose_file: docker-compose.yaml
        detach: true
---
kind: agent
name: assistant
`)
	var out bytes.Buffer
	if err := DeployTarget(context.Background(), TargetDeployOptions{
		AppDir: app,
		Target: "local",
		DryRun: true,
		Out:    &out,
	}); err != nil {
		t.Fatalf("DeployTarget: %v", err)
	}
	for _, want := range []string{
		"write=" + filepath.Join(app, "docker-compose.yaml"),
		"command=docker build -t sample:local",
		"command=docker compose -f " + filepath.Join(app, "docker-compose.yaml") + " up -d --wait --wait-timeout 30",
	} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("dry-run output missing %q:\n%s", want, out.String())
		}
	}
}

func TestDeployTargetNeverBuildPolicyRequiresArtifacts(t *testing.T) {
	_, app := testRepo(t, `
kind: app
name: sample
distribution:
  deploy:
    targets:
      local:
        kind: docker-compose
        build: [compose-local]
        compose_file: docker-compose.yaml
---
kind: agent
name: assistant
`)
	err := DeployTarget(context.Background(), TargetDeployOptions{
		AppDir:      app,
		Target:      "local",
		BuildPolicy: BuildPolicyNever,
		DryRun:      true,
	})
	if err == nil || !strings.Contains(err.Error(), "build artifacts are missing") {
		t.Fatalf("DeployTarget error = %v, want missing artifacts", err)
	}
}

func TestDeployTargetAutoRebuildsDockerImageArtifacts(t *testing.T) {
	_, app := testRepo(t, `
kind: app
name: sample
distribution:
  build:
    targets:
      image-local:
        kind: docker-image
        image: sample
        tags: [local]
      compose-local:
        kind: docker-compose
        image: sample:local
        output: docker-compose.yaml
  deploy:
    targets:
      local:
        kind: docker-compose
        build: [image-local, compose-local]
        compose_file: docker-compose.yaml
---
kind: agent
name: assistant
`)
	writeTestFile(t, app, "docker-compose.yaml", "services: {}\n")
	if err := writeArtifactIndex(app, ArtifactIndex{
		Version: artifactIndexVersion,
		AppDir:  app,
		Targets: []BuildArtifact{
			{Target: "image-local", Kind: buildKindDockerImage, Image: "sample:local"},
			{Target: "compose-local", Kind: buildKindDockerCompose, Path: "docker-compose.yaml", Image: "sample:local"},
		},
	}, false, io.Discard); err != nil {
		t.Fatalf("writeArtifactIndex: %v", err)
	}
	var out bytes.Buffer
	if err := DeployTarget(context.Background(), TargetDeployOptions{
		AppDir: app,
		DryRun: true,
		Out:    &out,
	}); err != nil {
		t.Fatalf("DeployTarget: %v", err)
	}
	if !strings.Contains(out.String(), "command=docker build -t fluxplane/fluxplane-base:local") || !strings.Contains(out.String(), "command=docker build -t sample:local") {
		t.Fatalf("dry-run output missing image rebuilds:\n%s", out.String())
	}
}

func TestDeployTargetRequiresConfiguredDefaultLocalTarget(t *testing.T) {
	_, app := testRepo(t, `
kind: app
name: sample
---
kind: agent
name: assistant
`)
	err := DeployTarget(context.Background(), TargetDeployOptions{
		AppDir: app,
		DryRun: true,
	})
	if err == nil || !strings.Contains(err.Error(), `unknown deploy target "local"`) || !strings.Contains(err.Error(), "distribution.deploy.targets.local") {
		t.Fatalf("DeployTarget error = %v, want missing configured local target", err)
	}
}

func TestDeployTargetRejectsLegacyImplicitTargetNames(t *testing.T) {
	_, app := testRepo(t, `
kind: app
name: sample
---
kind: agent
name: assistant
`)
	for _, target := range []string{"docker-compose", "kubernetes"} {
		err := DeployTarget(context.Background(), TargetDeployOptions{
			AppDir: app,
			Target: target,
			DryRun: true,
		})
		if err == nil || !strings.Contains(err.Error(), `unknown deploy target "`+target+`"`) {
			t.Fatalf("DeployTarget(%q) error = %v, want unknown target", target, err)
		}
	}
}

func TestListTargetsShowsBuildAndDeployTargets(t *testing.T) {
	_, app := testRepo(t, `
kind: app
name: sample
distribution:
  name: sample
  build:
    targets:
      docs:
        kind: documentation
        description: Agent capability document.
        output: docs/capabilities.md
      compose:
        kind: docker-compose
        image: sample
        tags: [local]
  deploy:
    targets:
      local:
        kind: docker-compose
        description: Local compose stack.
        build: [compose]
        compose_file: docker-compose.yaml
---
kind: agent
name: assistant
`)
	result, err := ListTargets(context.Background(), TargetListOptions{AppDir: app})
	if err != nil {
		t.Fatalf("ListTargets: %v", err)
	}
	if len(result.Build) != 2 || result.Build[0].Name != "compose" || result.Build[1].Name != "docs" {
		t.Fatalf("build targets = %#v", result.Build)
	}
	if result.Build[1].Description != "Agent capability document." || result.Build[1].Output != filepath.ToSlash(filepath.Join("docs", "capabilities.md")) {
		t.Fatalf("docs target = %#v", result.Build[1])
	}
	if len(result.Deploy) != 1 || result.Deploy[0].Name != "local" || result.Deploy[0].Artifact != "docker-compose.yaml" || result.Deploy[0].Description != "Local compose stack." {
		t.Fatalf("deploy targets = %#v", result.Deploy)
	}
}

func TestListTargetsDoesNotInventDeployTargets(t *testing.T) {
	_, app := testRepo(t, `
kind: app
name: sample
distribution:
  build:
    targets:
      docs:
        kind: documentation
---
kind: agent
name: assistant
`)
	result, err := ListTargets(context.Background(), TargetListOptions{AppDir: app})
	if err != nil {
		t.Fatalf("ListTargets: %v", err)
	}
	if len(result.Deploy) != 0 {
		t.Fatalf("deploy targets = %#v, want none", result.Deploy)
	}
}
