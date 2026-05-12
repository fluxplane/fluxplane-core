package filesystemplugin

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/fluxplane/agentruntime/core/event"
	"github.com/fluxplane/agentruntime/core/operation"
	"github.com/fluxplane/agentruntime/core/usage"
	"github.com/fluxplane/agentruntime/orchestration/pluginhost"
	"github.com/fluxplane/agentruntime/runtime/system"
)

func TestFileReadReturnsRenderedTextAndUsage(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(root+"/note.txt", []byte("one\ntwo\n"), 0644); err != nil {
		t.Fatal(err)
	}
	sys, err := system.NewHost(system.Config{Root: root})
	if err != nil {
		t.Fatalf("NewHost: %v", err)
	}
	ops, err := New(sys).Operations(context.Background(), pluginhost.Context{})
	if err != nil {
		t.Fatalf("Operations: %v", err)
	}
	var fileRead operation.Operation
	for _, op := range ops {
		if op.Spec().Ref.Name == FileReadOp {
			fileRead = op
		}
	}
	if fileRead == nil {
		t.Fatal("file_read operation not found")
	}
	var events []event.Event
	ctx := operation.NewContext(context.Background(), event.SinkFunc(func(evt event.Event) {
		events = append(events, evt)
	}))
	result := fileRead.Run(ctx, map[string]any{"path": "note.txt", "line_numbers": true})
	if result.IsError() {
		t.Fatalf("result error = %#v", result.Error)
	}
	rendered, ok := result.Output.(operation.Rendered)
	if !ok || rendered.ModelText() == "" {
		t.Fatalf("output = %#v, want rendered model text", result.Output)
	}
	var sawUsage bool
	for _, evt := range events {
		if _, ok := evt.(usage.Recorded); ok {
			sawUsage = true
		}
	}
	if !sawUsage {
		t.Fatal("usage.Recorded event not emitted")
	}
}

func TestFileCopyCopiesFileLargerThanWriteLimit(t *testing.T) {
	root := t.TempDir()
	data := bytes.Repeat([]byte("c"), maxWriteBytes+11)
	if err := os.WriteFile(filepath.Join(root, "src.bin"), data, 0644); err != nil {
		t.Fatal(err)
	}
	op := filesystemOperation(t, root, FileCopyOp)

	result := op.Run(operation.NewContext(context.Background(), event.Discard()), map[string]any{
		"src": "src.bin",
		"dst": "dst.bin",
	})
	if result.IsError() {
		t.Fatalf("result error = %#v", result.Error)
	}
	copied, err := os.ReadFile(filepath.Join(root, "dst.bin"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(copied, data) {
		t.Fatalf("copied len=%d, want %d identical bytes", len(copied), len(data))
	}
}

func TestFileMoveMovesFileLargerThanWriteLimit(t *testing.T) {
	root := t.TempDir()
	data := bytes.Repeat([]byte("m"), maxWriteBytes+13)
	if err := os.WriteFile(filepath.Join(root, "src.bin"), data, 0644); err != nil {
		t.Fatal(err)
	}
	op := filesystemOperation(t, root, FileMoveOp)

	result := op.Run(operation.NewContext(context.Background(), event.Discard()), map[string]any{
		"src": "src.bin",
		"dst": "dst.bin",
	})
	if result.IsError() {
		t.Fatalf("result error = %#v", result.Error)
	}
	if _, err := os.Stat(filepath.Join(root, "src.bin")); !os.IsNotExist(err) {
		t.Fatalf("source stat error = %v, want not exist", err)
	}
	moved, err := os.ReadFile(filepath.Join(root, "dst.bin"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(moved, data) {
		t.Fatalf("moved len=%d, want %d identical bytes", len(moved), len(data))
	}
}

func TestFileMoveKeepsSourceWhenDestinationWriteFails(t *testing.T) {
	root := t.TempDir()
	data := bytes.Repeat([]byte("s"), maxWriteBytes+15)
	if err := os.WriteFile(filepath.Join(root, "src.bin"), data, 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "dst.bin"), []byte("existing"), 0644); err != nil {
		t.Fatal(err)
	}
	op := filesystemOperation(t, root, FileMoveOp)

	result := op.Run(operation.NewContext(context.Background(), event.Discard()), map[string]any{
		"src": "src.bin",
		"dst": "dst.bin",
	})
	if !result.IsError() {
		t.Fatalf("result = %#v, want error", result)
	}
	remaining, err := os.ReadFile(filepath.Join(root, "src.bin"))
	if err != nil {
		t.Fatalf("source missing after failed move: %v", err)
	}
	if !bytes.Equal(remaining, data) {
		t.Fatalf("source len=%d, want %d identical bytes", len(remaining), len(data))
	}
}

func TestFilePatchRejectsFileLargerThanWriteLimitUnchanged(t *testing.T) {
	root := t.TempDir()
	data := append(bytes.Repeat([]byte("p"), maxWriteBytes+9), []byte("needle")...)
	path := filepath.Join(root, "large.txt")
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatal(err)
	}
	op := filesystemOperation(t, root, FilePatchOp)

	result := op.Run(operation.NewContext(context.Background(), event.Discard()), map[string]any{
		"path": "large.txt",
		"old":  "needle",
		"new":  "changed",
	})
	if !result.IsError() || result.Error == nil || result.Error.Code != "file_patch_too_large" {
		t.Fatalf("result = %#v, want file_patch_too_large", result)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(after, data) {
		t.Fatal("large file changed after rejected patch")
	}
}

func filesystemOperation(t *testing.T, root string, name string) operation.Operation {
	t.Helper()
	sys, err := system.NewHost(system.Config{Root: root})
	if err != nil {
		t.Fatalf("NewHost: %v", err)
	}
	ops, err := New(sys).Operations(context.Background(), pluginhost.Context{})
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
