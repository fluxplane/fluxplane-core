package filesystemplugin

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
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

func TestFileReadLineRangeBeyondDefaultReadWindow(t *testing.T) {
	root := t.TempDir()
	var content strings.Builder
	for i := 1; i <= 9000; i++ {
		if i == 8500 {
			content.WriteString("target line\n")
			continue
		}
		content.WriteString("padding padding padding padding padding\n")
	}
	if err := os.WriteFile(filepath.Join(root, "large.txt"), []byte(content.String()), 0644); err != nil {
		t.Fatal(err)
	}
	op := filesystemOperation(t, root, FileReadOp)

	result := op.Run(operation.NewContext(context.Background(), event.Discard()), map[string]any{
		"path":         "large.txt",
		"start_line":   8500,
		"end_line":     8500,
		"line_numbers": true,
	})
	if result.IsError() {
		t.Fatalf("result error = %#v", result.Error)
	}
	rendered, ok := result.Output.(operation.Rendered)
	if !ok {
		t.Fatalf("output = %#v, want rendered", result.Output)
	}
	text := rendered.ModelText()
	if !strings.Contains(text, "8500  target line") {
		t.Fatalf("text = %q, want requested line", text)
	}
	if strings.Contains(text, "truncated") {
		t.Fatalf("text = %q, did not want truncated", text)
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

func TestFilePatchKeepsFullDiffOutOfModelText(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "note.txt"), []byte("one\ntwo\nthree\n"), 0644); err != nil {
		t.Fatal(err)
	}
	op := filesystemOperation(t, root, FilePatchOp)

	result := op.Run(operation.NewContext(context.Background(), event.Discard()), map[string]any{
		"path": "note.txt",
		"old":  "two",
		"new":  "changed",
	})
	if result.IsError() {
		t.Fatalf("result error = %#v", result.Error)
	}
	rendered, ok := result.Output.(operation.Rendered)
	if !ok {
		t.Fatalf("output = %#v, want rendered", result.Output)
	}
	// Text must contain a real unified diff header and the changed line.
	if !strings.Contains(rendered.Text, "--- note.txt") || !strings.Contains(rendered.Text, "-two") || !strings.Contains(rendered.Text, "+changed") {
		t.Fatalf("text = %q, want unified diff", rendered.Text)
	}
	// Model text must omit the diff detail.
	if strings.Contains(rendered.ModelText(), "--- note.txt") || strings.Contains(rendered.ModelText(), "-two") {
		t.Fatalf("model text = %q, did not want full diff", rendered.ModelText())
	}
	data, ok := rendered.Data.(map[string]any)
	if !ok {
		t.Fatalf("data = %#v, want map", rendered.Data)
	}
	diff, _ := data["diff"].(string)
	if !strings.Contains(diff, "-two") || !strings.Contains(diff, "+changed") {
		t.Fatalf("data[diff] = %q, want unified diff with changed line", diff)
	}
	// Data must include per-patch status.
	patches, _ := data["patches"].([]patchStatus)
	if len(patches) != 1 || !patches[0].Matched || patches[0].Line < 1 {
		t.Fatalf("data[patches] = %#v, want 1 matched status with line >= 1", patches)
	}
}

func TestFilePatchNoMatchReturnsStructuredPatchStatus(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "note.txt"), []byte("one\ntwo\nthree\n"), 0644); err != nil {
		t.Fatal(err)
	}
	op := filesystemOperation(t, root, FilePatchOp)

	result := op.Run(operation.NewContext(context.Background(), event.Discard()), map[string]any{
		"path": "note.txt",
		"patches": []map[string]any{
			{"old": "two", "new": "changed"},        // will match
			{"old": "notpresent", "new": "nothing"}, // will not match
		},
	})
	if !result.IsError() || result.Error == nil || result.Error.Code != "file_patch_no_match" {
		t.Fatalf("result = %#v, want file_patch_no_match error", result)
	}
	// Error details must include a patches slice identifying the failure.
	details := result.Error.Details
	if details == nil {
		t.Fatalf("error details = nil, want map with patches")
	}
	patches, ok := details["patches"].([]patchStatus)
	if !ok || len(patches) < 2 {
		t.Fatalf("details[patches] = %#v, want slice of at least 2 patchStatus", details["patches"])
	}
	if !patches[0].Matched {
		t.Errorf("patches[0].Matched = false, want true (first patch matched)")
	}
	if patches[1].Matched || patches[1].Line != -1 || patches[1].Reason == "" {
		t.Errorf("patches[1] = %#v, want unmatched with line=-1 and reason set", patches[1])
	}
	// File must be unchanged.
	after, err := os.ReadFile(filepath.Join(root, "note.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != "one\ntwo\nthree\n" {
		t.Fatalf("file changed after failed patch: %q", after)
	}
}

func TestFilePatchDryRunReturnsDiffOnly(t *testing.T) {
	root := t.TempDir()
	original := "alpha\nbeta\ngamma\n"
	if err := os.WriteFile(filepath.Join(root, "file.txt"), []byte(original), 0644); err != nil {
		t.Fatal(err)
	}
	op := filesystemOperation(t, root, FilePatchOp)

	result := op.Run(operation.NewContext(context.Background(), event.Discard()), map[string]any{
		"path":    "file.txt",
		"old":     "beta",
		"new":     "REPLACED",
		"dry_run": true,
	})
	if result.IsError() {
		t.Fatalf("dry_run error = %#v", result.Error)
	}
	rendered, ok := result.Output.(operation.Rendered)
	if !ok {
		t.Fatalf("output = %#v, want rendered", result.Output)
	}
	// Text must contain the unified diff.
	if !strings.Contains(rendered.Text, "--- file.txt") || !strings.Contains(rendered.Text, "-beta") || !strings.Contains(rendered.Text, "+REPLACED") {
		t.Fatalf("text = %q, want unified diff with changed line", rendered.Text)
	}
	// Text must contain the hunk header (@@), confirming it is a real unified diff.
	if !strings.Contains(rendered.Text, "@@") {
		t.Fatalf("text = %q, want unified diff hunk header @@", rendered.Text)
	}
	// Text must not contain the full file duplicated outside of a diff (no raw "Would patch" summary with all lines
	// without the diff prefix). Verify by confirming the text is shorter than two copies of the original.
	if len(rendered.Text) > 2*len(original)+200 {
		t.Fatalf("text len=%d, looks larger than expected diff; original len=%d", len(rendered.Text), len(original))
	}
	// File must be unchanged (dry_run).
	after, err := os.ReadFile(filepath.Join(root, "file.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != original {
		t.Fatalf("file changed during dry_run: %q", after)
	}
}

func TestFileReadPatternReturnsMatchedRegions(t *testing.T) {
	root := t.TempDir()
	content := "alpha\nbeta\ngamma\ndelta\nepsilon\nzeta\n"
	if err := os.WriteFile(filepath.Join(root, "words.txt"), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	op := filesystemOperation(t, root, FileReadOp)

	// Match "gamma" with 1 line of context → expect "beta", "gamma", "delta"
	result := op.Run(operation.NewContext(context.Background(), event.Discard()), map[string]any{
		"path":          "words.txt",
		"pattern":       "gamma",
		"context_lines": 1,
	})
	if result.IsError() {
		t.Fatalf("result error = %#v", result.Error)
	}
	rendered, ok := result.Output.(operation.Rendered)
	if !ok {
		t.Fatalf("output = %#v, want rendered", result.Output)
	}
	text := rendered.ModelText()
	for _, want := range []string{"beta", "gamma", "delta"} {
		if !strings.Contains(text, want) {
			t.Errorf("text = %q, want %q", text, want)
		}
	}
	// Lines outside the context window should not appear
	for _, notWant := range []string{"alpha", "epsilon", "zeta"} {
		if strings.Contains(text, notWant) {
			t.Errorf("text = %q, did not want %q", text, notWant)
		}
	}
}

func TestFileReadPatternNoMatchReturnsZeroCount(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "file.txt"), []byte("line one\nline two\n"), 0644); err != nil {
		t.Fatal(err)
	}
	op := filesystemOperation(t, root, FileReadOp)

	result := op.Run(operation.NewContext(context.Background(), event.Discard()), map[string]any{
		"path":    "file.txt",
		"pattern": "NOTFOUND",
	})
	if result.IsError() {
		t.Fatalf("result error = %#v", result.Error)
	}
	rendered, ok := result.Output.(operation.Rendered)
	if !ok {
		t.Fatalf("output = %#v, want rendered", result.Output)
	}
	if !strings.Contains(rendered.ModelText(), "matches: 0") {
		t.Fatalf("text = %q, want matches: 0", rendered.ModelText())
	}
}

func TestFileReadPatternAdjacentMatchesMerged(t *testing.T) {
	root := t.TempDir()
	// Lines 1-6; "target" appears on lines 2 and 4. With ctxLines=1 the
	// regions [1-3] and [3-5] overlap and should be merged into [1-5].
	content := "a\ntarget\nc\ntarget\ne\nf\n"
	if err := os.WriteFile(filepath.Join(root, "merge.txt"), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	op := filesystemOperation(t, root, FileReadOp)

	result := op.Run(operation.NewContext(context.Background(), event.Discard()), map[string]any{
		"path":          "merge.txt",
		"pattern":       "target",
		"context_lines": 1,
	})
	if result.IsError() {
		t.Fatalf("result error = %#v", result.Error)
	}
	rendered, ok := result.Output.(operation.Rendered)
	if !ok {
		t.Fatalf("output = %#v, want rendered", result.Output)
	}
	text := rendered.ModelText()
	// All merged lines a..e should be present; f is outside.
	for _, want := range []string{"a", "target", "c", "e"} {
		if !strings.Contains(text, want) {
			t.Errorf("text = %q, want %q in merged region", text, want)
		}
	}
	// There should be no separator "---" because regions merged.
	if strings.Contains(text, "---") {
		t.Errorf("text = %q, did not expect region separator", text)
	}
	if strings.Contains(text, "6: f") {
		t.Errorf("text = %q, did not want 'f' (line 6) outside context", text)
	}
}

func TestGrepDefaultContextLines(t *testing.T) {
	root := t.TempDir()
	// 10 lines; match is on line 5 (0-indexed: 4).
	// With default context of 3 we expect lines 2-8 (1-indexed).
	lines := []string{"L1", "L2", "L3", "L4", "TARGET", "L6", "L7", "L8", "L9", "L10"}
	content := strings.Join(lines, "\n") + "\n"
	if err := os.WriteFile(filepath.Join(root, "ctx.txt"), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	op := filesystemOperation(t, root, GrepOp)

	// No context_lines field → should default to 3.
	result := op.Run(operation.NewContext(context.Background(), event.Discard()), map[string]any{
		"pattern": "TARGET",
		"paths":   []string{"ctx.txt"},
	})
	if result.IsError() {
		t.Fatalf("result error = %#v", result.Error)
	}
	rendered, ok := result.Output.(operation.Rendered)
	if !ok {
		t.Fatalf("output = %#v, want rendered", result.Output)
	}
	text := rendered.ModelText()
	// Lines within 3 of TARGET (line 5) → L2..L8.
	for _, want := range []string{"L2", "L3", "L4", "TARGET", "L6", "L7", "L8"} {
		if !strings.Contains(text, want) {
			t.Errorf("text = %q, want %q (default 3-line context)", text, want)
		}
	}
	// L1 is 4 lines away → should not appear.
	if strings.Contains(text, "L1\n") || strings.HasPrefix(text, "L1") {
		t.Errorf("text = %q, did not want L1 (> 3 lines from match)", text)
	}
}

func TestGrepExplicitZeroContextLines(t *testing.T) {
	root := t.TempDir()
	content := "before\nMATCH\nafter\n"
	if err := os.WriteFile(filepath.Join(root, "z.txt"), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	op := filesystemOperation(t, root, GrepOp)

	zero := 0
	result := op.Run(operation.NewContext(context.Background(), event.Discard()), map[string]any{
		"pattern":       "MATCH",
		"paths":         []string{"z.txt"},
		"context_lines": &zero,
	})
	if result.IsError() {
		t.Fatalf("result error = %#v", result.Error)
	}
	rendered, ok := result.Output.(operation.Rendered)
	if !ok {
		t.Fatalf("output = %#v, want rendered", result.Output)
	}
	text := rendered.ModelText()
	if !strings.Contains(text, "MATCH") {
		t.Errorf("text = %q, want MATCH", text)
	}
	if strings.Contains(text, "before") || strings.Contains(text, "after") {
		t.Errorf("text = %q, did not want context lines when context_lines=0", text)
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
