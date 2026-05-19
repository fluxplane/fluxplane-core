package coderapp

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	agentruntime "github.com/fluxplane/agentruntime"
	"github.com/fluxplane/agentruntime/core/channel"
	coredistribution "github.com/fluxplane/agentruntime/core/distribution"
	corethread "github.com/fluxplane/agentruntime/core/thread"
	clientapi "github.com/fluxplane/agentruntime/orchestration/client"
	"github.com/fluxplane/agentruntime/orchestration/distribution"
	"github.com/fluxplane/agentruntime/orchestration/session"
)

func TestResolveConfigDiscoversNearestCoderYAML(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, ".coder.yaml", `version: 1
workspace:
  env_files: [.env]
  roots:
    - name: api
      path: ../api
      env_files: [api.env]
imports: {}
`)
	nested := filepath.Join(root, "src", "pkg")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg, err := ResolveConfig(Config{Root: nested})
	if err != nil {
		t.Fatalf("ResolveConfig: %v", err)
	}
	if cfg.Path != filepath.Join(root, ".coder.yaml") {
		t.Fatalf("path = %q, want nearest config", cfg.Path)
	}
	wantRoot := filepath.Clean(filepath.Join(root, "../api"))
	if len(cfg.Workspace.Roots) != 1 || cfg.Workspace.Roots[0].Name != "api" || cfg.Workspace.Roots[0].Path != wantRoot {
		t.Fatalf("roots = %#v, want api=%s", cfg.Workspace.Roots, wantRoot)
	}
	if got := strings.Join(cfg.Workspace.Roots[0].EnvFiles, ","); got != "api.env" {
		t.Fatalf("root env files = %#v, want api.env", cfg.Workspace.Roots[0].EnvFiles)
	}
	wantEnv := filepath.Join(root, ".env")
	if len(cfg.Workspace.EnvFiles) != 1 || cfg.Workspace.EnvFiles[0] != wantEnv {
		t.Fatalf("env files = %#v, want %s", cfg.Workspace.EnvFiles, wantEnv)
	}
}

func TestResolveConfigExplicitPathWins(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, ".coder.yaml", `version: 1
workspace:
  roots:
    - name: parent
      path: ../parent
`)
	writeTestFile(t, root, "nested/custom.yaml", `version: 1
workspace:
  roots:
    - name: custom
      path: ../custom
`)
	cfg, err := ResolveConfig(Config{Root: filepath.Join(root, "nested"), CoderConfigPath: "custom.yaml"})
	if err != nil {
		t.Fatalf("ResolveConfig: %v", err)
	}
	if !cfg.Explicit {
		t.Fatalf("explicit = false, want true")
	}
	if len(cfg.Workspace.Roots) != 1 || cfg.Workspace.Roots[0].Name != "custom" {
		t.Fatalf("roots = %#v, want custom config only", cfg.Workspace.Roots)
	}
}

func TestResolveConfigMergesProgrammaticWorkspace(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, ".coder.yaml", `version: 1
workspace:
  env_files: [.env]
  roots:
    - name: api
      path: ../api
`)
	cfg, err := ResolveConfig(Config{
		Root: root,
		Workspace: distribution.WorkspaceConfig{
			Roots:    []distribution.WorkspaceRoot{{Name: "web", Path: "../web"}},
			EnvFiles: []string{"local.env"},
		},
	})
	if err != nil {
		t.Fatalf("ResolveConfig: %v", err)
	}
	wantAPI := filepath.Clean(filepath.Join(root, "../api"))
	if len(cfg.Workspace.Roots) != 2 {
		t.Fatalf("workspace roots = %#v, want api/web", cfg.Workspace.Roots)
	}
	if cfg.Workspace.Roots[0].Name != "api" || cfg.Workspace.Roots[0].Path != wantAPI {
		t.Fatalf("first root = %#v, want api=%s", cfg.Workspace.Roots[0], wantAPI)
	}
	if cfg.Workspace.Roots[1].Name != "web" || cfg.Workspace.Roots[1].Path != "../web" {
		t.Fatalf("second root = %#v, want web=../web", cfg.Workspace.Roots[1])
	}
	if strings.Join(cfg.Workspace.EnvFiles, ",") != filepath.Join(root, ".env")+",local.env" {
		t.Fatalf("env files = %#v, want merged env files", cfg.Workspace.EnvFiles)
	}
}

func TestCommandAddsConfigShowAndWorkspaceDefaults(t *testing.T) {
	app, err := New(context.Background(), Config{
		Workspace: distribution.WorkspaceConfig{
			Roots: []distribution.WorkspaceRoot{{
				Name:     "api",
				Path:     "../api",
				Access:   "read_only",
				Create:   true,
				EnvFiles: []string{"api.env"},
			}},
			EnvFiles: []string{".env"},
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	cmd := app.Command()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"config", "show", "-o", "json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	text := out.String()
	for _, want := range []string{`"workspace"`, `"Name": "api"`, `"Access": "read_only"`, `"Create": true`, `"EnvFiles": [`} {
		if !strings.Contains(text, want) {
			t.Fatalf("config show missing %q:\n%s", want, text)
		}
	}
}

func TestConfigEditCreatesDefaultConfigAndOpensEditor(t *testing.T) {
	root := t.TempDir()
	var edited string
	app, err := New(context.Background(), Config{
		Root: root,
		Editor: func(_ context.Context, path string, _ io.Reader, _, _ io.Writer) error {
			edited = path
			return nil
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	cmd := app.Command()
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"config", "edit"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	want := filepath.Join(root, ".coder.yaml")
	if edited != want {
		t.Fatalf("edited = %q, want %q", edited, want)
	}
	data, err := os.ReadFile(want)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if strings.TrimSpace(string(data)) != "version: 1" {
		t.Fatalf("config = %q, want default version", string(data))
	}
}

func TestConfigEditAllowsMissingExplicitConfig(t *testing.T) {
	root := t.TempDir()
	explicit := filepath.Join(root, "configs", "coder.yaml")
	var edited string
	app, err := New(context.Background(), Config{
		Root:            root,
		CoderConfigPath: explicit,
		Editor: func(_ context.Context, path string, _ io.Reader, _, _ io.Writer) error {
			edited = path
			return nil
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	cmd := app.Command()
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"config", "edit"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if edited != explicit {
		t.Fatalf("edited = %q, want %q", edited, explicit)
	}
}

func TestRunAppliesCoderWorkspaceDefaults(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, ".coder.yaml", `version: 1
workspace:
  env_files: [.env]
  roots:
    - name: api
      path: api
      access: read_only
      create: true
      env_files: [api.env]
`)
	nested := filepath.Join(root, "src", "pkg")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	runtime := &fakeRunRuntime{}
	app, err := New(context.Background(), Config{
		Root: nested,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	var loadedPath string
	var out bytes.Buffer
	err = app.Run(context.Background(), RunOptions{
		Path:           "./demo",
		Input:          "hello",
		Yolo:           true,
		WorkspaceRoots: []string{"web=../web"},
		EnvFiles:       []string{"local.env"},
		Loader: func(_ context.Context, path string) (distribution.Loaded, error) {
			loadedPath = path
			return distribution.Loaded{
				Distribution: distribution.Distribution{
					Spec: coredistribution.Spec{
						Name:           "sample",
						DefaultSession: agentruntime.SessionRef{Name: "main"},
					},
					Runtime: runtime,
				},
			}, nil
		},
		Out: &out,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if loadedPath != "./demo" {
		t.Fatalf("loaded path = %q, want ./demo", loadedPath)
	}
	roots := runtime.request.Launch.Workspace.Roots
	if len(roots) != 2 || roots[0].Name != "api" || roots[1].Name != "web" {
		t.Fatalf("workspace roots = %#v, want config root plus override", roots)
	}
	if roots[0].Path != filepath.Join(root, "api") || roots[0].Access != "read_only" || !roots[0].Create {
		t.Fatalf("config root = %#v, want path/access/create preserved", roots[0])
	}
	if strings.Join(roots[0].EnvFiles, ",") != "api.env" {
		t.Fatalf("config root env files = %#v, want api.env preserved", roots[0].EnvFiles)
	}
	if roots[1].Path != "../web" || roots[1].Access != "read_write" {
		t.Fatalf("override root = %#v, want parsed read_write override", roots[1])
	}
	envFiles := runtime.request.Launch.Workspace.EnvFiles
	if strings.Join(envFiles, ",") != filepath.Join(root, ".env")+",local.env" {
		t.Fatalf("env files = %#v, want merged config and override env files", envFiles)
	}
	if !runtime.request.Yolo {
		t.Fatalf("runtime request = %#v, want yolo", runtime.request)
	}
	if !strings.Contains(out.String(), "ok") {
		t.Fatalf("output = %q, want rendered app output", out.String())
	}
}

type fakeRunRuntime struct {
	request distribution.OpenRequest
}

func (r *fakeRunRuntime) OpenSession(_ context.Context, req distribution.OpenRequest) (clientapi.SessionHandle, error) {
	r.request = req
	info := clientapi.SessionInfo{
		Session:      req.Session,
		Thread:       corethread.Ref{ID: "thread-1", BranchID: corethread.MainBranch},
		Conversation: req.Conversation,
	}
	return fakeRunSession{info: info}, nil
}

type fakeRunSession struct {
	info clientapi.SessionInfo
}

func (s fakeRunSession) Info() clientapi.SessionInfo { return s.info }

func (s fakeRunSession) Submit(_ context.Context, submission clientapi.Submission) (clientapi.RunHandle, error) {
	return fakeRunHandle{info: s.info, submission: submission}, nil
}

func (s fakeRunSession) Events(context.Context, clientapi.EventOptions) (<-chan clientapi.Event, func(), error) {
	ch := make(chan clientapi.Event)
	close(ch)
	return ch, func() {}, nil
}

func (s fakeRunSession) OnEvent(context.Context, func(clientapi.Event)) (func(), error) {
	return func() {}, nil
}

func (s fakeRunSession) Close(context.Context) error { return nil }

type fakeRunHandle struct {
	info       clientapi.SessionInfo
	submission clientapi.Submission
}

func (r fakeRunHandle) ID() clientapi.RunID { return "run-1" }

func (r fakeRunHandle) Session() clientapi.SessionInfo { return r.info }

func (r fakeRunHandle) Submission() clientapi.Submission { return r.submission }

func (r fakeRunHandle) Events() <-chan clientapi.Event {
	ch := make(chan clientapi.Event)
	close(ch)
	return ch
}

func (r fakeRunHandle) Done() <-chan struct{} {
	ch := make(chan struct{})
	close(ch)
	return ch
}

func (r fakeRunHandle) Err() error { return nil }

func (r fakeRunHandle) Wait(context.Context) (clientapi.Result, error) {
	return clientapi.Result{
		RunID:      r.ID(),
		Session:    r.info,
		Submission: r.submission,
		Input:      &session.InputResult{Status: session.InputStatusOK},
		Outbound: &channel.Outbound{
			Kind:    channel.OutboundMessage,
			Message: &channel.Message{Content: "ok"},
		},
	}, nil
}

func writeTestFile(t *testing.T, root, rel, data string) {
	t.Helper()
	path := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
}
