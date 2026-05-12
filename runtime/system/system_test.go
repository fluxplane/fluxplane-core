package system

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
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
