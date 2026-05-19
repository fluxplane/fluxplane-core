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
		"FROM fluxplane/coder-base:local",
		`ENTRYPOINT ["/usr/local/bin/coder"]`,
		`CMD ["app","serve","/app","--connectors-path","/secrets/connectors","--health-addr","127.0.0.1:18080","--provider","openrouter","--model","openai/gpt-5.5","--effort","medium"]`,
		`HEALTHCHECK --interval=10s --timeout=3s --start-period=20s --retries=12 CMD ["/usr/local/bin/coder","app","healthcheck","--url","http://127.0.0.1:18080/control/status"]`,
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

func TestBuildDockerUsesOverriddenBaseImage(t *testing.T) {
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

	result, err := BuildDocker(context.Background(), DockerBuildOptions{
		AppDir:    app,
		BaseImage: "example.com/coder-base:v1",
		DryRun:    true,
	})
	if err != nil {
		t.Fatalf("BuildDocker: %v", err)
	}
	defer func() { _ = os.RemoveAll(result.ContextDir) }()
	dockerfile, err := os.ReadFile(result.Dockerfile)
	if err != nil {
		t.Fatalf("ReadFile Dockerfile: %v", err)
	}
	if !strings.Contains(string(dockerfile), "FROM example.com/coder-base:v1") {
		t.Fatalf("Dockerfile = %s", dockerfile)
	}
}

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
		AppDir: app,
		Image:  "sample:test",
		Force:  true,
		Detach: true,
		Runner: runner,
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
		AppDir: app,
		DryRun: true,
		Out:    &out,
		Runner: runner,
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

func TestGenerateKubernetesManifestsCreatesEnvSecret(t *testing.T) {
	_, app := testRepo(t, `
kind: app
name: sample
runtime:
  workspace:
    env_files:
      - .env
      - .env.local
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
	writeTestFile(t, app, ".env", "OPENROUTER_API_KEY=secret-one\nSHARED=first\n")
	writeTestFile(t, app, ".env.local", "SHARED=last\n")

	result, err := GenerateKubernetesManifests(context.Background(), KubernetesManifestOptions{
		AppDir: app,
		Image:  "sample:test",
		DryRun: true,
	})
	if err != nil {
		t.Fatalf("GenerateKubernetesManifests: %v", err)
	}
	for _, want := range []string{
		"kind: Secret",
		"  name: sample-env",
		"  OPENROUTER_API_KEY: \"secret-one\"",
		"  SHARED: \"last\"",
		"          envFrom:",
		"                name: sample-env",
		"          image: sample:test",
		`          args: ["app","serve","/app","--connectors-path","/connectors","--health-addr","0.0.0.0:18080","--provider","openrouter","--model","openai/gpt-5.5","--effort","medium"]`,
	} {
		if !strings.Contains(result.Content, want) {
			t.Fatalf("kubernetes manifest missing %q:\n%s", want, result.Content)
		}
	}
}

func TestGenerateKubernetesManifestsIncludesRuntimeBackends(t *testing.T) {
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
distribution:
  build:
    assets: [agentsdk.app.yaml]
    docker:
      image: support-bot
      tags: [local]
---
kind: agent
name: assistant
`)
	result, err := GenerateKubernetesManifests(context.Background(), KubernetesManifestOptions{
		AppDir: app,
		Image:  "support-bot:test",
		DryRun: true,
	})
	if err != nil {
		t.Fatalf("GenerateKubernetesManifests: %v", err)
	}
	for _, want := range []string{
		"kind: StatefulSet",
		"  name: mysql",
		"          image: mysql:8.4",
		"              value: \"agentruntime:agentruntime@tcp(mysql:3306)/agentruntime?parseTime=true&multiStatements=true\"",
		"  name: nats",
		"          image: nats:2.11-alpine",
		"              value: \"nats://nats:4222\"",
	} {
		if !strings.Contains(result.Content, want) {
			t.Fatalf("kubernetes manifest missing %q:\n%s", want, result.Content)
		}
	}
}

func TestGenerateKubernetesManifestsRejectsNamedRootEnvFiles(t *testing.T) {
	_, app := testRepo(t, `
kind: app
name: sample
runtime:
  workspace:
    roots:
      - name: tmp
        path: /tmp/agentruntime-test
        env_files: [.env.tmp]
distribution:
  build:
    assets: [agentsdk.app.yaml]
    docker: {}
---
kind: agent
name: assistant
`)
	_, err := GenerateKubernetesManifests(context.Background(), KubernetesManifestOptions{
		AppDir: app,
		DryRun: true,
	})
	if err == nil || !strings.Contains(err.Error(), `does not support env_files on workspace root "tmp"`) {
		t.Fatalf("GenerateKubernetesManifests error = %v, want named root env_files error", err)
	}
}

func TestDeployKubernetesDryRunRedactsEnvSecretValues(t *testing.T) {
	_, app := testRepo(t, `
kind: app
name: sample
runtime:
  workspace:
    env_files: [.env]
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
	writeTestFile(t, app, ".env", "OPENROUTER_API_KEY=supersecret\n")
	var out bytes.Buffer
	result, err := DeployKubernetes(context.Background(), KubernetesOptions{
		AppDir: app,
		Image:  "sample:test",
		DryRun: true,
		Out:    &out,
	})
	if err != nil {
		t.Fatalf("DeployKubernetes dry-run: %v", err)
	}
	if !result.DryRun || result.SecretName != "sample-env" {
		t.Fatalf("result = %#v", result)
	}
	manifest := filepath.Join(app, ".deploy", "kubernetes.yaml")
	for _, want := range []string{
		"write=" + manifest,
		"secret=sample-env keys=OPENROUTER_API_KEY",
		"command=k3d image import sample:test --cluster <current-k3d-cluster>",
		"command=kubectl delete deployment/coder-registry service/coder-registry pvc/coder-registry-data -n sample --ignore-not-found",
		"command=kubectl apply -f " + manifest,
	} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("dry-run output missing %q:\n%s", want, out.String())
		}
	}
	if strings.Contains(out.String(), "supersecret") {
		t.Fatalf("dry-run output leaked secret:\n%s", out.String())
	}
	if strings.Contains(out.String(), ".svc.cluster.local") || strings.Contains(out.String(), "127.0.0.1:5000") {
		t.Fatalf("dry-run output used registry pull path:\n%s", out.String())
	}
}

func TestDeployKubernetesNamespaceRegistryFlow(t *testing.T) {
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
	originalDetect := detectK3DClusterName
	detectK3DClusterName = func(context.Context) (string, error) { return "sample-cluster", nil }
	defer func() { detectK3DClusterName = originalDetect }()

	runner := &recordingRunner{}
	result, err := DeployKubernetes(context.Background(), KubernetesOptions{
		AppDir: app,
		Image:  "sample:test",
		Force:  true,
		Runner: runner,
	})
	if err != nil {
		t.Fatalf("DeployKubernetes: %v", err)
	}
	if result.Image != "sample:test" {
		t.Fatalf("image = %q", result.Image)
	}
	manifest := filepath.Join(app, ".deploy", "kubernetes.yaml")
	joined := strings.Join(runner.calls, "\n")
	for _, want := range []string{
		"docker build -t fluxplane/coder-base:local ",
		"docker build -t sample:test -f " + filepath.Join(app, "Dockerfile") + " " + app,
		"k3d image import sample:test --cluster sample-cluster",
		"kubectl delete deployment/coder-registry service/coder-registry pvc/coder-registry-data -n sample --ignore-not-found",
		"kubectl apply -f " + manifest,
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("commands missing %q:\n%s", want, joined)
		}
	}
	if _, err := os.Stat(filepath.Join(app, "kubernetes.yaml")); !os.IsNotExist(err) {
		t.Fatalf("root kubernetes.yaml exists after deploy: %v", err)
	}
	gitignore, err := os.ReadFile(filepath.Join(app, ".gitignore"))
	if err != nil {
		t.Fatalf("ReadFile .gitignore: %v", err)
	}
	if !strings.Contains(string(gitignore), ".deploy/") {
		t.Fatalf(".gitignore = %q, want .deploy/", gitignore)
	}
	if strings.Contains(joined, ".svc.cluster.local") || strings.Contains(joined, "127.0.0.1:5000") {
		t.Fatalf("commands used registry pull path:\n%s", joined)
	}
}

func TestDeployKubernetesExternalRegistryDryRunSkipsNamespaceRegistry(t *testing.T) {
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
	var out bytes.Buffer
	result, err := DeployKubernetes(context.Background(), KubernetesOptions{
		AppDir:       app,
		Image:        "sample:test",
		RegistryMode: "external",
		Registry:     "123456789012.dkr.ecr.us-east-1.amazonaws.com/ai-bots",
		DryRun:       true,
		Out:          &out,
	})
	if err != nil {
		t.Fatalf("DeployKubernetes external dry-run: %v", err)
	}
	if result.Image != "123456789012.dkr.ecr.us-east-1.amazonaws.com/ai-bots/sample:test" {
		t.Fatalf("image = %q", result.Image)
	}
	if strings.Contains(out.String(), "coder-registry") || strings.Contains(out.String(), "<registry-manifest>") {
		t.Fatalf("external registry dry-run included namespace registry:\n%s", out.String())
	}
	manifest := filepath.Join(app, ".deploy", "kubernetes.yaml")
	for _, want := range []string{
		"command=docker tag sample:test 123456789012.dkr.ecr.us-east-1.amazonaws.com/ai-bots/sample:test",
		"command=docker push 123456789012.dkr.ecr.us-east-1.amazonaws.com/ai-bots/sample:test",
		"command=kubectl apply -f " + manifest,
	} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("dry-run output missing %q:\n%s", want, out.String())
		}
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

func TestUndeployKubernetesDryRunPreservesPVCsAndSkipsEnvFiles(t *testing.T) {
	_, app := testRepo(t, `
kind: app
name: support-bot
runtime:
  workspace:
    env_files: [.env]
  data:
    store:
      kind: mysql
  events:
    store:
      kind: nats
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

	result, err := UndeployKubernetes(context.Background(), KubernetesUndeployOptions{
		AppDir:    app,
		Namespace: "ai-bots",
		DryRun:    true,
		Out:       &out,
		Runner:    runner,
	})
	if err != nil {
		t.Fatalf("UndeployKubernetes dry-run: %v", err)
	}
	if len(calls) != 0 {
		t.Fatalf("dry-run calls = %#v, want none", calls)
	}
	if result.Namespace != "ai-bots" {
		t.Fatalf("namespace = %q, want ai-bots", result.Namespace)
	}
	if !strings.Contains(out.String(), "command=kubectl delete -f <kubernetes-teardown-manifest> --ignore-not-found") {
		t.Fatalf("dry-run output = %q", out.String())
	}
	if strings.Contains(out.String(), "delete pvc") {
		t.Fatalf("dry-run should preserve PVCs:\n%s", out.String())
	}
}

func TestUndeployKubernetesDryRunDeletesPVCsWithVolumes(t *testing.T) {
	_, app := testRepo(t, `
kind: app
name: support-bot
runtime:
  data:
    store:
      kind: mysql
  events:
    store:
      kind: nats
distribution:
  build:
    assets: [agentsdk.app.yaml]
    docker: {}
---
kind: agent
name: assistant
`)
	var out bytes.Buffer
	result, err := UndeployKubernetes(context.Background(), KubernetesUndeployOptions{
		AppDir:    app,
		Namespace: "ai-bots",
		DryRun:    true,
		Volumes:   true,
		Out:       &out,
	})
	if err != nil {
		t.Fatalf("UndeployKubernetes dry-run: %v", err)
	}
	want := "command=kubectl delete pvc data-mysql-0 data-nats-0 coder-registry-data -n ai-bots --ignore-not-found"
	if !strings.Contains(out.String(), want) {
		t.Fatalf("dry-run output = %q, want %q", out.String(), want)
	}
	if !result.Volumes || len(result.Commands) != 2 {
		t.Fatalf("result = %#v, want volume teardown command", result)
	}
}

func TestUndeployKubernetesRunsDeleteWithGeneratedManifest(t *testing.T) {
	_, app := testRepo(t, `
kind: app
name: support-bot
runtime:
  workspace:
    env_files: [.env]
  data:
    store:
      kind: mysql
distribution:
  build:
    assets: [agentsdk.app.yaml]
    docker: {}
---
kind: agent
name: assistant
`)
	var manifestPath string
	var calls []string
	runner := CommandRunnerFunc(func(_ context.Context, _ string, name string, args []string, _, _ io.Writer) error {
		calls = append(calls, name+" "+strings.Join(args, " "))
		if len(calls) == 1 {
			if name != "kubectl" || strings.Join(args[:2], " ") != "delete -f" {
				t.Fatalf("first command = %s %v, want kubectl delete -f", name, args)
			}
			manifestPath = args[2]
			data, err := os.ReadFile(manifestPath)
			if err != nil {
				t.Fatalf("ReadFile teardown manifest: %v", err)
			}
			text := string(data)
			for _, want := range []string{
				"kind: Secret",
				"  name: support-bot-env",
				"kind: StatefulSet",
				"  name: mysql",
				"kind: Deployment",
				"  name: support-bot",
			} {
				if !strings.Contains(text, want) {
					t.Fatalf("teardown manifest missing %q:\n%s", want, text)
				}
			}
		}
		return nil
	})

	result, err := UndeployKubernetes(context.Background(), KubernetesUndeployOptions{
		AppDir:    app,
		Namespace: "ai-bots",
		Volumes:   true,
		Runner:    runner,
	})
	if err != nil {
		t.Fatalf("UndeployKubernetes: %v", err)
	}
	if len(calls) != 2 {
		t.Fatalf("calls = %#v, want manifest delete and pvc delete", calls)
	}
	if !strings.Contains(calls[1], "kubectl delete pvc data-mysql-0 coder-registry-data -n ai-bots --ignore-not-found") {
		t.Fatalf("pvc command = %q", calls[1])
	}
	if manifestPath == "" {
		t.Fatalf("manifest path was not captured")
	}
	if _, err := os.Stat(manifestPath); !os.IsNotExist(err) {
		t.Fatalf("teardown manifest was not removed: %v", err)
	}
	if len(result.Commands) != 2 {
		t.Fatalf("commands = %#v, want two commands", result.Commands)
	}
}

func TestBuildCoderBaseDockerDefaultsToCurrentExecutable(t *testing.T) {
	dir := t.TempDir()
	coderPath := filepath.Join(dir, "coder")
	writeTestFile(t, dir, "coder", "#!/bin/sh\n")
	if err := os.Chmod(coderPath, 0o755); err != nil {
		t.Fatalf("Chmod coder: %v", err)
	}
	var gotName string
	var gotArgs []string
	runner := CommandRunnerFunc(func(_ context.Context, _ string, name string, args []string, _, _ io.Writer) error {
		gotName = name
		gotArgs = append([]string(nil), args...)
		return nil
	})

	result, err := BuildCoderBaseDocker(context.Background(), BaseImageOptions{
		CoderPath: coderPath,
		Tags:      []string{"fluxplane/coder-base:test"},
		DryRun:    true,
		Runner:    runner,
	})
	if err != nil {
		t.Fatalf("BuildCoderBaseDocker: %v", err)
	}
	defer func() { _ = os.RemoveAll(result.ContextDir) }()
	dockerfile, err := os.ReadFile(result.Dockerfile)
	if err != nil {
		t.Fatalf("ReadFile Dockerfile: %v", err)
	}
	text := string(dockerfile)
	for _, want := range []string{
		"FROM debian:bookworm-slim AS runtime",
		"COPY coder /usr/local/bin/coder",
		`ENTRYPOINT ["/usr/local/bin/coder"]`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("Dockerfile missing %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "go build") {
		t.Fatalf("Dockerfile = %s, want binary base image without source build", text)
	}
	if _, err := os.Stat(filepath.Join(result.ContextDir, "coder")); err != nil {
		t.Fatalf("copied coder executable: %v", err)
	}
	if gotName != "" || len(gotArgs) != 0 {
		t.Fatalf("dry run executed runner: %s %v", gotName, gotArgs)
	}
}

func TestBuildCoderBaseDockerFromRepoCopiesLocalReplaceModules(t *testing.T) {
	parent := t.TempDir()
	repo := filepath.Join(parent, "projects", "agentsdk", "rewrite")
	app := filepath.Join(repo, "examples", "sample")
	writeTestFile(t, repo, "go.mod", "module github.com/fluxplane/agentruntime\n\nreplace github.com/codewandler/axon => ../../axon\n")
	writeTestFile(t, repo, "cmd/coder/main.go", "package main\nfunc main() {}\n")
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

	result, err := BuildCoderBaseDocker(context.Background(), BaseImageOptions{
		RepoRoot: repo,
		DryRun:   true,
	})
	if err != nil {
		t.Fatalf("BuildCoderBaseDocker: %v", err)
	}
	defer func() { _ = os.RemoveAll(result.ContextDir) }()
	dockerfile, err := os.ReadFile(result.Dockerfile)
	if err != nil {
		t.Fatalf("ReadFile Dockerfile: %v", err)
	}
	if !strings.Contains(string(dockerfile), "COPY localmods/axon /axon") {
		t.Fatalf("Dockerfile = %s", dockerfile)
	}
	if !strings.Contains(string(dockerfile), "go build -trimpath -ldflags=\"-s -w\" -o /out/coder ./cmd/coder") {
		t.Fatalf("Dockerfile = %s", dockerfile)
	}
	if _, err := os.Stat(filepath.Join(result.ContextDir, "localmods", "axon", "go.mod")); err != nil {
		t.Fatalf("local replace copy: %v", err)
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
		"  sample-app:",
		"    image: sample:latest",
		`    command: ["app","serve","/app","--connectors-path","/connectors","--health-addr","127.0.0.1:18080","--provider","openrouter","--model","openai/gpt-5.5","--effort","medium"]`,
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
		`    command: ["app","serve","/app","--connectors-path","/connectors","--health-addr","127.0.0.1:18080","--provider","codex","--model","gpt-5.5","--effort","high"]`,
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
		`    command: ["app","serve","/app","--connectors-path","/connectors","--health-addr","127.0.0.1:18080","--provider","openrouter","--model","openai/gpt-5.5","--effort","high"]`,
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
		"    image: support-bot:test",
		"      AGENTRUNTIME_DATASTORE_MYSQL_DSN: agentruntime:agentruntime@tcp(mysql:3306)/agentruntime?parseTime=true&multiStatements=true",
		"      AGENTRUNTIME_EVENTSTORE_NATS_DSN: nats://nats:4222",
		"      OPENROUTER_API_KEY: ${OPENROUTER_API_KEY:?OPENROUTER_API_KEY is required}",
		"      mysql:",
		"        condition: service_healthy",
		"      nats:",
		"  mysql:",
		"    image: mysql:8.4",
		"  nats:",
		"    image: nats:2.11-alpine",
		"    command: [\"-js\", \"-sd\", \"/data\", \"-m\", \"8222\"]",
		"      test: [\"CMD-SHELL\", \"wget -q -O - http://127.0.0.1:8222/healthz | grep -q ok\"]",
		"volumes:",
		"  mysql-data:",
		"  nats-data:",
	} {
		if !strings.Contains(result.Content, want) {
			t.Fatalf("compose missing %q:\n%s", want, result.Content)
		}
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
	writeTestFile(t, repo, "cmd/coder/main.go", "package main\nfunc main() {}\n")
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

type recordingRunner struct {
	calls []string
}

func (r *recordingRunner) Run(_ context.Context, _ string, name string, args []string, _, _ io.Writer) error {
	r.calls = append(r.calls, name+" "+strings.Join(args, " "))
	return nil
}
