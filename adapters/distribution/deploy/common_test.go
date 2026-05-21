package deploy

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testRepo(t *testing.T, manifest string) (string, string) {
	t.Helper()
	repo := t.TempDir()
	writeTestFile(t, repo, "go.mod", "module github.com/fluxplane/engine\n")
	writeTestFile(t, repo, "cmd/coder/main.go", "package main\nfunc main() {}\n")
	app := filepath.Join(repo, "examples", "sample")
	writeTestFile(t, app, "fluxplane.yaml", manifest)
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

type recordingPortForwarder struct {
	namespace string
	closed    bool
}

func (r *recordingPortForwarder) Forward(_ context.Context, namespace string, _, _ io.Writer) (PortForward, error) {
	r.namespace = namespace
	return recordingPortForward{close: func() { r.closed = true }}, nil
}

type recordingPortForward struct {
	close func()
}

func (r recordingPortForward) Close() error {
	r.close()
	return nil
}

type recordingDockerClient struct {
	contextDir string
	dockerfile string
	tags       []string
	platforms  []string
	push       bool
}

func (r *recordingDockerClient) BuildImage(_ context.Context, contextDir, dockerfile string, tags, platforms []string, push bool, _, _ io.Writer) error {
	r.contextDir = contextDir
	r.dockerfile = dockerfile
	r.tags = append([]string(nil), tags...)
	r.platforms = append([]string(nil), platforms...)
	r.push = push
	return nil
}

func (r *recordingDockerClient) TagImage(context.Context, string, string, io.Writer, io.Writer) error {
	return nil
}

func (r *recordingDockerClient) PushImage(context.Context, string, io.Writer, io.Writer) error {
	return nil
}

func (r *recordingDockerClient) DeployComposeStack(context.Context, dockerComposeStack, io.Writer, io.Writer) error {
	return nil
}

func (r *recordingDockerClient) UndeployComposeStack(context.Context, dockerComposeStack, bool, io.Writer, io.Writer) error {
	return nil
}
