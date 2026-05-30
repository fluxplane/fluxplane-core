package filesystem

import (
	"context"
	"testing"

	"github.com/fluxplane/fluxplane-core/core/operation"
	"github.com/fluxplane/fluxplane-core/orchestration/pluginhost"
	runtimeworkspace "github.com/fluxplane/fluxplane-core/runtime/workspace"
	fpsystem "github.com/fluxplane/fluxplane-system"
)

type filesystemTestEnv struct {
	name      string
	root      string
	sys       fpsystem.System
	workspace runtimeworkspace.Workspace
	host      bool
}

func runFilesystemBackends(t *testing.T, fn func(*testing.T, *filesystemTestEnv)) {
	t.Helper()
	t.Run("host", func(t *testing.T) {
		root := t.TempDir()
		sys, err := runtimeworkspace.NewHost(runtimeworkspace.Config{Root: root})
		if err != nil {
			t.Fatalf("NewHost: %v", err)
		}
		fn(t, &filesystemTestEnv{name: "host", root: root, sys: sys, workspace: sys.Workspace(), host: true})
	})
}

func newHostFilesystemTestEnv(t *testing.T, root string) *filesystemTestEnv {
	t.Helper()
	sys, err := runtimeworkspace.NewHost(runtimeworkspace.Config{Root: root})
	if err != nil {
		t.Fatalf("NewHost: %v", err)
	}
	return &filesystemTestEnv{name: "host", root: root, sys: sys, workspace: sys.Workspace(), host: true}
}

func (e *filesystemTestEnv) Operation(t *testing.T, name string) operation.Operation {
	t.Helper()
	ops, err := New(e.workspace).Operations(context.Background(), pluginhost.Context{})
	if err != nil {
		t.Fatalf("Operations: %v", err)
	}
	for _, op := range ops {
		if string(op.Spec().Ref.Name) == name {
			return op
		}
	}
	t.Fatalf("%s operation not found", name)
	return nil
}

func (e *filesystemTestEnv) WriteFile(t *testing.T, path string, data []byte) {
	t.Helper()
	if _, err := writeWorkspaceFile(context.Background(), e.workspace, path, data, 0644, true); err != nil {
		t.Fatalf("WriteFile(%s): %v", path, err)
	}
}

func (e *filesystemTestEnv) ReadFile(t *testing.T, path string) []byte {
	t.Helper()
	data, truncated, _, err := readWorkspaceFile(context.Background(), e.workspace, path, 0)
	if err != nil {
		t.Fatalf("ReadFile(%s): %v", path, err)
	}
	if truncated {
		t.Fatalf("ReadFile(%s): unexpectedly truncated", path)
	}
	return data
}

func (e *filesystemTestEnv) Mkdir(t *testing.T, path string) {
	t.Helper()
	if _, err := mkdirWorkspace(context.Background(), e.workspace, path, 0755); err != nil {
		t.Fatalf("MkdirAll(%s): %v", path, err)
	}
}

func (e *filesystemTestEnv) MustNotExist(t *testing.T, path string) {
	t.Helper()
	_, _, err := statWorkspacePath(context.Background(), e.workspace, path)
	if err == nil {
		t.Fatalf("Stat(%s): path exists, want not exist", path)
	}
}

func (e *filesystemTestEnv) MustExist(t *testing.T, path string) {
	t.Helper()
	if _, _, err := statWorkspacePath(context.Background(), e.workspace, path); err != nil {
		t.Fatalf("Stat(%s): %v", path, err)
	}
}
