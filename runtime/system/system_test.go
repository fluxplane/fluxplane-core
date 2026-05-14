package system

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestHostWorkspaceRejectsSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("secret"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "link")); err != nil {
		t.Skipf("symlink not available: %v", err)
	}
	sys, err := NewHost(Config{Root: root})
	if err != nil {
		t.Fatalf("NewHost: %v", err)
	}
	_, _, _, err = sys.Workspace().ReadFile(context.Background(), "link/secret.txt", 1024)
	if err == nil {
		t.Fatal("ReadFile through escaping symlink succeeded, want error")
	}
}

func TestHostWorkspaceCreateRejectsSymlinkParentEscape(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "link")); err != nil {
		t.Skipf("symlink not available: %v", err)
	}
	sys, err := NewHost(Config{Root: root})
	if err != nil {
		t.Fatalf("NewHost: %v", err)
	}
	_, err = sys.Workspace().WriteFile(context.Background(), "link/new.txt", []byte("x"), 0644, false)
	if err == nil {
		t.Fatal("WriteFile through escaping symlink parent succeeded, want error")
	}
}

func TestHostWorkspaceCopyFileCopiesCompleteFile(t *testing.T) {
	root := t.TempDir()
	data := bytes.Repeat([]byte("x"), 1024*1024+17)
	if err := os.WriteFile(filepath.Join(root, "src.bin"), data, 0644); err != nil {
		t.Fatal(err)
	}
	sys, err := NewHost(Config{Root: root})
	if err != nil {
		t.Fatalf("NewHost: %v", err)
	}

	src, dst, written, err := sys.Workspace().CopyFile(context.Background(), "src.bin", "nested/dst.bin", false)
	if err != nil {
		t.Fatalf("CopyFile: %v", err)
	}
	if src.Rel != "src.bin" || dst.Rel != "nested/dst.bin" || written != int64(len(data)) {
		t.Fatalf("src=%#v dst=%#v written=%d, want complete copy", src, dst, written)
	}
	copied, err := os.ReadFile(filepath.Join(root, "nested", "dst.bin"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(copied, data) {
		t.Fatalf("copied data len=%d, want %d identical bytes", len(copied), len(data))
	}
}

func TestHostWorkspaceReadFileLinesPastInitialWindow(t *testing.T) {
	root := t.TempDir()
	var content bytes.Buffer
	for i := 1; i <= 6000; i++ {
		if i == 5500 {
			content.WriteString("target\n")
			continue
		}
		content.WriteString("padding padding padding padding\n")
	}
	if err := os.WriteFile(filepath.Join(root, "large.txt"), content.Bytes(), 0644); err != nil {
		t.Fatal(err)
	}
	sys, err := NewHost(Config{Root: root})
	if err != nil {
		t.Fatalf("NewHost: %v", err)
	}

	data, firstLine, truncated, resolved, err := sys.Workspace().ReadFileLines(context.Background(), "large.txt", 5500, 5500, 1024)
	if err != nil {
		t.Fatalf("ReadFileLines: %v", err)
	}
	if resolved.Rel != "large.txt" || firstLine != 5500 || truncated || string(data) != "target\n" {
		t.Fatalf("resolved=%#v firstLine=%d truncated=%v data=%q", resolved, firstLine, truncated, data)
	}
}

func TestHostWorkspaceMoveFileLeavesSourceWhenDestinationWriteFails(t *testing.T) {
	root := t.TempDir()
	data := []byte("source")
	if err := os.WriteFile(filepath.Join(root, "src.txt"), data, 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "dst.txt"), []byte("existing"), 0644); err != nil {
		t.Fatal(err)
	}
	sys, err := NewHost(Config{Root: root})
	if err != nil {
		t.Fatalf("NewHost: %v", err)
	}

	_, _, _, err = sys.Workspace().MoveFile(context.Background(), "src.txt", "dst.txt", false)
	if err == nil {
		t.Fatal("MoveFile succeeded, want overwrite error")
	}
	remaining, readErr := os.ReadFile(filepath.Join(root, "src.txt"))
	if readErr != nil {
		t.Fatalf("source missing after failed move: %v", readErr)
	}
	if !bytes.Equal(remaining, data) {
		t.Fatalf("source = %q, want %q", remaining, data)
	}
}

func TestHostWorkspaceNamedRootAllowsLogicalAndAbsolutePaths(t *testing.T) {
	root := t.TempDir()
	tmp := filepath.Join(t.TempDir(), "agentruntime-coder")
	sys, err := NewHost(Config{
		Root: root,
		Workspace: WorkspaceConfig{Roots: []WorkspaceRootConfig{{
			Name:   "tmp",
			Path:   tmp,
			Access: WorkspaceAccessReadWrite,
			Create: true,
		}}},
	})
	if err != nil {
		t.Fatalf("NewHost: %v", err)
	}

	logical, err := sys.Workspace().WriteFile(context.Background(), "@tmp/logical.txt", []byte("logical"), 0644, false)
	if err != nil {
		t.Fatalf("WriteFile logical: %v", err)
	}
	if logical.Rel != "@tmp/logical.txt" {
		t.Fatalf("logical rel = %q, want @tmp/logical.txt", logical.Rel)
	}
	absolutePath := filepath.Join(tmp, "absolute.txt")
	absolute, err := sys.Workspace().WriteFile(context.Background(), absolutePath, []byte("absolute"), 0644, false)
	if err != nil {
		t.Fatalf("WriteFile absolute: %v", err)
	}
	if absolute.Rel != "@tmp/absolute.txt" {
		t.Fatalf("absolute rel = %q, want @tmp/absolute.txt", absolute.Rel)
	}
}

func TestHostWorkspaceRejectsUnconfiguredAbsoluteTmpPath(t *testing.T) {
	sys, err := NewHost(Config{Root: t.TempDir()})
	if err != nil {
		t.Fatalf("NewHost: %v", err)
	}
	_, err = sys.Workspace().WriteFile(context.Background(), filepath.Join(t.TempDir(), "out.txt"), []byte("x"), 0644, false)
	if err == nil {
		t.Fatal("WriteFile outside workspace succeeded, want error")
	}
}

func TestHostWorkspaceNamedRootRejectsSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	tmp := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(tmp, "link")); err != nil {
		t.Skipf("symlink not available: %v", err)
	}
	sys, err := NewHost(Config{
		Root: root,
		Workspace: WorkspaceConfig{Roots: []WorkspaceRootConfig{{
			Name:   "tmp",
			Path:   tmp,
			Access: WorkspaceAccessReadWrite,
		}}},
	})
	if err != nil {
		t.Fatalf("NewHost: %v", err)
	}
	_, err = sys.Workspace().WriteFile(context.Background(), "@tmp/link/out.txt", []byte("x"), 0644, false)
	if err == nil {
		t.Fatal("WriteFile through named-root symlink escape succeeded, want error")
	}
}

func TestHostWorkspaceReadOnlyNamedRootRejectsWrite(t *testing.T) {
	root := t.TempDir()
	docs := t.TempDir()
	if err := os.WriteFile(filepath.Join(docs, "README.md"), []byte("docs"), 0644); err != nil {
		t.Fatal(err)
	}
	sys, err := NewHost(Config{
		Root: root,
		Workspace: WorkspaceConfig{Roots: []WorkspaceRootConfig{{
			Name:   "docs",
			Path:   docs,
			Access: WorkspaceAccessReadOnly,
		}}},
	})
	if err != nil {
		t.Fatalf("NewHost: %v", err)
	}
	data, _, resolved, err := sys.Workspace().ReadFile(context.Background(), "@docs/README.md", 1024)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(data) != "docs" || resolved.Rel != "@docs/README.md" {
		t.Fatalf("data=%q resolved=%#v, want docs in @docs", data, resolved)
	}
	_, err = sys.Workspace().WriteFile(context.Background(), "@docs/new.md", []byte("x"), 0644, false)
	if err == nil {
		t.Fatal("WriteFile into read-only root succeeded, want error")
	}
}

func TestHostProcessRejectsReadOnlyNamedRootWorkdir(t *testing.T) {
	root := t.TempDir()
	docs := t.TempDir()
	sys, err := NewHost(Config{
		Root: root,
		Workspace: WorkspaceConfig{Roots: []WorkspaceRootConfig{{
			Name:   "docs",
			Path:   docs,
			Access: WorkspaceAccessReadOnly,
		}}},
	})
	if err != nil {
		t.Fatalf("NewHost: %v", err)
	}
	_, err = sys.Process().Run(context.Background(), ProcessRequest{
		Command: "go",
		Args:    []string{"version"},
		Workdir: "@docs",
		Timeout: time.Second,
	})
	if err == nil || !strings.Contains(err.Error(), "not writable") {
		t.Fatalf("Run error = %v, want read-only workdir rejection", err)
	}
}

func TestHostWorkspaceCreateScratchUsesConfiguredRoot(t *testing.T) {
	root := t.TempDir()
	tmp := filepath.Join(t.TempDir(), "scratch")
	sys, err := NewHost(Config{
		Root: root,
		Workspace: WorkspaceConfig{
			Roots: []WorkspaceRootConfig{{
				Name:   "tmp",
				Path:   tmp,
				Access: WorkspaceAccessReadWrite,
				Create: true,
			}},
			ScratchRoot: "tmp",
		},
	})
	if err != nil {
		t.Fatalf("NewHost: %v", err)
	}
	scratch, err := sys.Workspace().CreateScratch(context.Background(), "agentruntime-test-*")
	if err != nil {
		t.Fatalf("CreateScratch: %v", err)
	}
	defer func() { _ = scratch.RemoveAll(context.Background()) }()
	if err := pathWithin(tmp, scratch.Root()); err != nil {
		t.Fatalf("scratch root = %q, want under %q: %v", scratch.Root(), tmp, err)
	}
	resolved, err := scratch.WriteFile(context.Background(), "out.txt", []byte("x"), 0644)
	if err != nil {
		t.Fatalf("scratch WriteFile: %v", err)
	}
	if !strings.HasPrefix(resolved.Rel, "@tmp/agentruntime-test-") || !strings.HasSuffix(resolved.Rel, "/out.txt") {
		t.Fatalf("scratch rel = %q, want @tmp/agentruntime-test-*/out.txt", resolved.Rel)
	}
}
