package deploy

import (
	stdtar "archive/tar"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/fluxplane/fluxplane-core/orchestration/distribution"
	apinetwork "github.com/moby/moby/api/types/network"
	dockerclient "github.com/moby/moby/client"
)

func TestBuildDockerGeneratesContextAndRunsDockerBuild(t *testing.T) {
	repo, app := testRepo(t, `
kind: app
name: sample
distribution:
  name: sample
  build:
    assets:
      - fluxplane.yaml
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
      - fluxplane.yaml
      - docs/**/*.md
    docker: {}
---
kind: agent
name: assistant
`)
	writeTestFile(t, app, "docs/nested/readme.md", "hello")
	var out bytes.Buffer

	result, err := BuildDocker(context.Background(), DockerBuildOptions{
		AppDir:      app,
		Tags:        []string{"example.com/sample:v1"},
		Platforms:   []string{"linux/amd64"},
		AuthPath:    "/secrets/auth",
		DryRun:      true,
		KeepContext: true,
		Out:         &out,
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
		"FROM fluxplane/fluxplane-base:local",
		`ENTRYPOINT ["/usr/local/bin/fluxplane"]`,
		`CMD ["serve","/app","--auth-path","/secrets/auth","--health-addr","127.0.0.1:18080","--provider","openrouter","--model","openai/gpt-5.5","--effort","medium"]`,
		`HEALTHCHECK --interval=10s --timeout=3s --start-period=20s --retries=12 CMD ["/usr/local/bin/fluxplane","healthcheck","--url","http://127.0.0.1:18080/control/status"]`,
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
    assets: [fluxplane.yaml]
    docker: {}
---
kind: agent
name: assistant
`)

	result, err := BuildDocker(context.Background(), DockerBuildOptions{
		AppDir:      app,
		BaseImage:   "example.com/fluxplane-base:v1",
		DryRun:      true,
		KeepContext: true,
	})
	if err != nil {
		t.Fatalf("BuildDocker: %v", err)
	}
	defer func() { _ = os.RemoveAll(result.ContextDir) }()
	dockerfile, err := os.ReadFile(result.Dockerfile)
	if err != nil {
		t.Fatalf("ReadFile Dockerfile: %v", err)
	}
	if !strings.Contains(string(dockerfile), "FROM example.com/fluxplane-base:v1") {
		t.Fatalf("Dockerfile = %s", dockerfile)
	}
}

func TestBuildDockerDryRunCleansContextByDefault(t *testing.T) {
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
	result, err := BuildDocker(context.Background(), DockerBuildOptions{
		AppDir: app,
		DryRun: true,
	})
	if err != nil {
		t.Fatalf("BuildDocker: %v", err)
	}
	if _, err := os.Stat(result.ContextDir); !os.IsNotExist(err) {
		t.Fatalf("dry-run context dir was not cleaned: %v", err)
	}
}

func TestBuildFluxplaneBaseDockerDefaultsToCurrentExecutable(t *testing.T) {
	dir := t.TempDir()
	fluxplanePath := filepath.Join(dir, "fluxplane")
	writeTestFile(t, dir, "fluxplane", "#!/bin/sh\n")
	if err := os.Chmod(fluxplanePath, 0o755); err != nil {
		t.Fatalf("Chmod fluxplane: %v", err)
	}
	var gotName string
	var gotArgs []string
	runner := CommandRunnerFunc(func(_ context.Context, _ string, name string, args []string, _, _ io.Writer) error {
		gotName = name
		gotArgs = append([]string(nil), args...)
		return nil
	})

	result, err := BuildFluxplaneBaseDocker(context.Background(), BaseImageOptions{
		BinaryPath:  fluxplanePath,
		Tags:        []string{"fluxplane/fluxplane-base:test"},
		DryRun:      true,
		KeepContext: true,
		Runner:      runner,
	})
	if err != nil {
		t.Fatalf("BuildFluxplaneBaseDocker: %v", err)
	}
	defer func() { _ = os.RemoveAll(result.ContextDir) }()
	dockerfile, err := os.ReadFile(result.Dockerfile)
	if err != nil {
		t.Fatalf("ReadFile Dockerfile: %v", err)
	}
	text := string(dockerfile)
	for _, want := range []string{
		"FROM debian:bookworm-slim AS runtime",
		"COPY fluxplane /usr/local/bin/fluxplane",
		`ENTRYPOINT ["/usr/local/bin/fluxplane"]`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("Dockerfile missing %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "go build") {
		t.Fatalf("Dockerfile = %s, want binary base image without source build", text)
	}
	if _, err := os.Stat(filepath.Join(result.ContextDir, "fluxplane")); err != nil {
		t.Fatalf("copied fluxplane executable: %v", err)
	}
	if gotName != "" || len(gotArgs) != 0 {
		t.Fatalf("dry run executed runner: %s %v", gotName, gotArgs)
	}
}

func TestBuildFluxplaneBaseDockerFromRepoCopiesLocalReplaceModules(t *testing.T) {
	parent := t.TempDir()
	repo := filepath.Join(parent, "projects", "fluxplane", "fluxplane-core")
	app := filepath.Join(repo, "examples", "sample")
	writeTestFile(t, repo, "go.mod", "module github.com/fluxplane/fluxplane-core\n\nreplace github.com/codewandler/axon => ../../axon\n")
	writeTestFile(t, repo, "cmd/fluxplane/main.go", "package main\nfunc main() {}\n")
	writeTestFile(t, filepath.Join(parent, "projects"), "axon/go.mod", "module github.com/codewandler/axon\n")
	writeTestFile(t, app, "fluxplane.yaml", `
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

	result, err := BuildFluxplaneBaseDocker(context.Background(), BaseImageOptions{
		RepoRoot:    repo,
		DryRun:      true,
		KeepContext: true,
	})
	if err != nil {
		t.Fatalf("BuildFluxplaneBaseDocker: %v", err)
	}
	defer func() { _ = os.RemoveAll(result.ContextDir) }()
	dockerfile, err := os.ReadFile(result.Dockerfile)
	if err != nil {
		t.Fatalf("ReadFile Dockerfile: %v", err)
	}
	if !strings.Contains(string(dockerfile), "COPY localmods/axon /axon") {
		t.Fatalf("Dockerfile = %s", dockerfile)
	}
	if !strings.Contains(string(dockerfile), "WORKDIR /src/fluxplane") {
		t.Fatalf("Dockerfile = %s", dockerfile)
	}
	if !strings.Contains(string(dockerfile), "go build -trimpath -ldflags=\"-s -w\" -o /out/fluxplane ./cmd/fluxplane") {
		t.Fatalf("Dockerfile = %s", dockerfile)
	}
	if _, err := os.Stat(filepath.Join(result.ContextDir, "localmods", "axon", "go.mod")); err != nil {
		t.Fatalf("local replace copy: %v", err)
	}
}

func TestBuildFluxplaneBaseDockerDryRunCleansContextByDefault(t *testing.T) {
	dir := t.TempDir()
	fluxplanePath := filepath.Join(dir, "fluxplane")
	writeTestFile(t, dir, "fluxplane", "#!/bin/sh\n")
	if err := os.Chmod(fluxplanePath, 0o755); err != nil {
		t.Fatalf("Chmod fluxplane: %v", err)
	}
	result, err := BuildFluxplaneBaseDocker(context.Background(), BaseImageOptions{
		BinaryPath: fluxplanePath,
		DryRun:     true,
	})
	if err != nil {
		t.Fatalf("BuildFluxplaneBaseDocker: %v", err)
	}
	if _, err := os.Stat(result.ContextDir); !os.IsNotExist(err) {
		t.Fatalf("dry-run base context dir was not cleaned: %v", err)
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

func TestDockerBuildContextOverlaysGeneratedDockerfile(t *testing.T) {
	contextDir := t.TempDir()
	writeTestFile(t, contextDir, "Dockerfile", "FROM stale\n")
	writeTestFile(t, contextDir, "app/fluxplane.yaml", "kind: app\n")
	outDir := t.TempDir()
	dockerfile := filepath.Join(outDir, "Dockerfile")
	writeTestFile(t, outDir, "Dockerfile", "FROM generated\n")

	stream, dockerfileName, err := dockerBuildContext(contextDir, dockerfile)
	if err != nil {
		t.Fatalf("dockerBuildContext: %v", err)
	}
	defer func() { _ = stream.Close() }()
	if dockerfileName != "Dockerfile" {
		t.Fatalf("dockerfileName = %q", dockerfileName)
	}
	entries, err := tarEntries(stream)
	if err != nil {
		t.Fatalf("tarEntries: %v", err)
	}
	if entries["Dockerfile"] != "FROM generated\n" {
		t.Fatalf("Dockerfile = %q, want generated Dockerfile", entries["Dockerfile"])
	}
	if entries["app/fluxplane.yaml"] == "" {
		t.Fatalf("app asset missing from build context: %#v", entries)
	}
}

func TestConsumeDockerJSONStreamReturnsStreamError(t *testing.T) {
	stream := io.NopCloser(strings.NewReader(`{"stream":"Step 1"}
{"errorDetail":{"message":"executor failed"},"error":"executor failed"}
`))
	var out bytes.Buffer
	err := consumeDockerJSONStream(stream, &out, "docker build")
	if err == nil || !strings.Contains(err.Error(), "executor failed") {
		t.Fatalf("consumeDockerJSONStream error = %v", err)
	}
	if !strings.Contains(out.String(), `"Step 1"`) {
		t.Fatalf("output = %s, want copied stream", out.String())
	}
}

func TestDockerRegistryAuthUsesDockerConfigAuth(t *testing.T) {
	configDir := t.TempDir()
	auth := base64.StdEncoding.EncodeToString([]byte("user:pass"))
	writeTestFile(t, configDir, "config.json", `{"auths":{"example.com":{"auth":"`+auth+`"}}}`)
	t.Setenv("DOCKER_CONFIG", configDir)

	header, err := dockerRegistryAuth(context.Background(), "example.com/team/app:latest", true)
	if err != nil {
		t.Fatalf("dockerRegistryAuth: %v", err)
	}
	decoded, err := base64.URLEncoding.DecodeString(header)
	if err != nil {
		t.Fatalf("DecodeString: %v", err)
	}
	var got map[string]string
	if err := json.Unmarshal(decoded, &got); err != nil {
		t.Fatalf("Unmarshal auth: %v", err)
	}
	if got["username"] != "user" || got["password"] != "pass" || got["auth"] != "" || got["serveraddress"] != "example.com" {
		t.Fatalf("auth = %#v", got)
	}
}

func TestDockerRegistryAuthUsesCredentialHelper(t *testing.T) {
	configDir := t.TempDir()
	helperDir := t.TempDir()
	writeTestFile(t, configDir, "config.json", `{"credHelpers":{"example.com":"test"}}`)
	helperPath := filepath.Join(helperDir, "docker-credential-test")
	writeTestFile(t, helperDir, "docker-credential-test", "#!/usr/bin/env sh\ncat >/dev/null\nprintf '{\"Username\":\"helper-user\",\"Secret\":\"helper-pass\",\"ServerURL\":\"example.com\"}'\n")
	if err := os.Chmod(helperPath, 0o755); err != nil {
		t.Fatalf("Chmod helper: %v", err)
	}
	t.Setenv("DOCKER_CONFIG", configDir)
	t.Setenv("PATH", helperDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	header, err := dockerRegistryAuth(context.Background(), "example.com/team/app:latest", true)
	if err != nil {
		t.Fatalf("dockerRegistryAuth: %v", err)
	}
	decoded, err := base64.URLEncoding.DecodeString(header)
	if err != nil {
		t.Fatalf("DecodeString: %v", err)
	}
	var got map[string]string
	if err := json.Unmarshal(decoded, &got); err != nil {
		t.Fatalf("Unmarshal auth: %v", err)
	}
	if got["username"] != "helper-user" || got["password"] != "helper-pass" || got["serveraddress"] != "example.com" {
		t.Fatalf("auth = %#v", got)
	}
}

func TestDockerStackRuntimeServicesRequirePull(t *testing.T) {
	stack := dockerComposeStack{
		Name: "support-bot",
		Launch: distribution.LaunchConfig{
			Data:   distribution.DataConfig{Store: distribution.DataStoreConfig{Kind: "mysql"}},
			Events: distribution.EventsConfig{Store: distribution.EventStoreConfig{Kind: "nats"}},
		},
	}
	containers, err := dockerStackContainers(stack)
	if err != nil {
		t.Fatalf("dockerStackContainers: %v", err)
	}
	pull := map[string]bool{}
	for _, container := range containers {
		pull[container.image] = container.pull
	}
	if !pull["mysql:8.4"] || !pull["nats:2.11-alpine"] {
		t.Fatalf("runtime services pull flags = %#v", pull)
	}
	if pull[""] || pull[stack.Image] {
		t.Fatalf("app image should not be marked for pull: %#v", pull)
	}
}

func TestDockerStackContainersDoesNotInjectProviderEnv(t *testing.T) {
	containers, err := dockerStackContainers(dockerComposeStack{
		Name:       "sample",
		Image:      "sample:test",
		AppRuntime: appRuntimeOptions{Provider: DefaultAppProvider},
	})
	if err != nil {
		t.Fatalf("dockerStackContainers: %v", err)
	}
	var appEnv []string
	for _, container := range containers {
		if strings.HasSuffix(container.name, "-app") {
			appEnv = container.env
			break
		}
	}
	if len(appEnv) != 0 {
		t.Fatalf("app env = %#v, want no provider env injected", appEnv)
	}
}

func TestEnsureDockerNetworkCreatesMissingNetwork(t *testing.T) {
	client := &recordingNetworkClient{inspectErr: cerrdefs.ErrNotFound}
	labels := dockerStackLabels("sample")

	if err := ensureDockerNetwork(context.Background(), client, "fluxplane-sample", labels); err != nil {
		t.Fatalf("ensureDockerNetwork: %v", err)
	}
	if client.createdName != "fluxplane-sample" || client.createdOptions.Driver != "bridge" || client.createdOptions.Labels[deployStackLabel] != "sample" {
		t.Fatalf("created network name=%q options=%#v", client.createdName, client.createdOptions)
	}
}

func TestEnsureDockerNetworkReusesManagedNetwork(t *testing.T) {
	client := &recordingNetworkClient{
		inspectResult: dockerclient.NetworkInspectResult{
			Network: apinetwork.Inspect{Network: apinetwork.Network{Labels: dockerStackLabels("sample")}},
		},
	}

	if err := ensureDockerNetwork(context.Background(), client, "fluxplane-sample", dockerStackLabels("sample")); err != nil {
		t.Fatalf("ensureDockerNetwork: %v", err)
	}
	if client.createdName != "" {
		t.Fatalf("created network %q, want reuse", client.createdName)
	}
}

func TestEnsureDockerNetworkRejectsForeignNetwork(t *testing.T) {
	client := &recordingNetworkClient{
		inspectResult: dockerclient.NetworkInspectResult{
			Network: apinetwork.Inspect{Network: apinetwork.Network{Labels: map[string]string{deployStackLabel: "other"}}},
		},
	}

	err := ensureDockerNetwork(context.Background(), client, "fluxplane-sample", dockerStackLabels("sample"))
	if err == nil || !strings.Contains(err.Error(), "is not managed by this deploy stack") {
		t.Fatalf("ensureDockerNetwork error = %v, want foreign network error", err)
	}
}

type recordingNetworkClient struct {
	inspectResult  dockerclient.NetworkInspectResult
	inspectErr     error
	createdName    string
	createdOptions dockerclient.NetworkCreateOptions
}

func (r *recordingNetworkClient) NetworkInspect(context.Context, string, dockerclient.NetworkInspectOptions) (dockerclient.NetworkInspectResult, error) {
	return r.inspectResult, r.inspectErr
}

func (r *recordingNetworkClient) NetworkCreate(_ context.Context, name string, options dockerclient.NetworkCreateOptions) (dockerclient.NetworkCreateResult, error) {
	r.createdName = name
	r.createdOptions = options
	return dockerclient.NetworkCreateResult{ID: "network-id"}, nil
}

func tarEntries(stream io.Reader) (map[string]string, error) {
	reader := stdtar.NewReader(stream)
	entries := map[string]string{}
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			return entries, nil
		}
		if err != nil {
			return nil, err
		}
		if header.Typeflag != stdtar.TypeReg {
			continue
		}
		data, err := io.ReadAll(reader)
		if err != nil {
			return nil, err
		}
		entries[header.Name] = string(data)
	}
}
