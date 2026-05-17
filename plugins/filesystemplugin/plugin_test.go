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
)

func TestFileReadReturnsRenderedTextAndUsage(t *testing.T) {
	runFilesystemBackends(t, func(t *testing.T, env *filesystemTestEnv) {
		env.WriteFile(t, "note.txt", []byte("one\ntwo\n"))
		fileRead := env.Operation(t, FileReadOp)

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
	})
}

func TestFileReadLineRangeBeyondDefaultReadWindow(t *testing.T) {
	runFilesystemBackends(t, func(t *testing.T, env *filesystemTestEnv) {
		var content strings.Builder
		for i := 1; i <= 9000; i++ {
			if i == 8500 {
				content.WriteString("target line\n")
				continue
			}
			content.WriteString("padding padding padding padding padding\n")
		}
		env.WriteFile(t, "large.txt", []byte(content.String()))
		op := env.Operation(t, FileReadOp)

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
	})
}

func TestFileCreateWritesAndRejectsExistingDestination(t *testing.T) {
	runFilesystemBackends(t, func(t *testing.T, env *filesystemTestEnv) {
		op := env.Operation(t, FileCreateOp)

		result := op.Run(operation.NewContext(context.Background(), event.Discard()), map[string]any{
			"path":    "nested/out.txt",
			"content": "hello\n",
		})
		if result.IsError() {
			t.Fatalf("create error = %#v", result.Error)
		}
		if got := string(env.ReadFile(t, "nested/out.txt")); got != "hello\n" {
			t.Fatalf("created content = %q", got)
		}

		result = op.Run(operation.NewContext(context.Background(), event.Discard()), map[string]any{
			"path":    "nested/out.txt",
			"content": "replace\n",
		})
		if !result.IsError() || result.Error == nil || result.Error.Code != "file_create_failed" {
			t.Fatalf("result = %#v, want file_create_failed", result)
		}
		if got := string(env.ReadFile(t, "nested/out.txt")); got != "hello\n" {
			t.Fatalf("content changed after rejected create: %q", got)
		}

		result = op.Run(operation.NewContext(context.Background(), event.Discard()), map[string]any{
			"path":      "nested/out.txt",
			"content":   "replace\n",
			"overwrite": true,
		})
		if result.IsError() {
			t.Fatalf("overwrite error = %#v", result.Error)
		}
		if got := string(env.ReadFile(t, "nested/out.txt")); got != "replace\n" {
			t.Fatalf("overwritten content = %q", got)
		}
	})
}

func TestFileCopyCopiesFileLargerThanWriteLimit(t *testing.T) {
	runFilesystemBackends(t, func(t *testing.T, env *filesystemTestEnv) {
		data := bytes.Repeat([]byte("c"), maxWriteBytes+11)
		env.WriteFile(t, "src.bin", data)
		op := env.Operation(t, FileCopyOp)

		result := op.Run(operation.NewContext(context.Background(), event.Discard()), map[string]any{
			"src": "src.bin",
			"dst": "dst.bin",
		})
		if result.IsError() {
			t.Fatalf("result error = %#v", result.Error)
		}
		if copied := env.ReadFile(t, "dst.bin"); !bytes.Equal(copied, data) {
			t.Fatalf("copied len=%d, want %d identical bytes", len(copied), len(data))
		}
	})
}

func TestFileMoveMovesFileLargerThanWriteLimit(t *testing.T) {
	runFilesystemBackends(t, func(t *testing.T, env *filesystemTestEnv) {
		data := bytes.Repeat([]byte("m"), maxWriteBytes+13)
		env.WriteFile(t, "src.bin", data)
		op := env.Operation(t, FileMoveOp)

		result := op.Run(operation.NewContext(context.Background(), event.Discard()), map[string]any{
			"src": "src.bin",
			"dst": "dst.bin",
		})
		if result.IsError() {
			t.Fatalf("result error = %#v", result.Error)
		}
		env.MustNotExist(t, "src.bin")
		if moved := env.ReadFile(t, "dst.bin"); !bytes.Equal(moved, data) {
			t.Fatalf("moved len=%d, want %d identical bytes", len(moved), len(data))
		}
	})
}

func TestFileMoveKeepsSourceWhenDestinationWriteFails(t *testing.T) {
	runFilesystemBackends(t, func(t *testing.T, env *filesystemTestEnv) {
		data := bytes.Repeat([]byte("s"), maxWriteBytes+15)
		env.WriteFile(t, "src.bin", data)
		env.WriteFile(t, "dst.bin", []byte("existing"))
		op := env.Operation(t, FileMoveOp)

		result := op.Run(operation.NewContext(context.Background(), event.Discard()), map[string]any{
			"src": "src.bin",
			"dst": "dst.bin",
		})
		if !result.IsError() {
			t.Fatalf("result = %#v, want error", result)
		}
		if remaining := env.ReadFile(t, "src.bin"); !bytes.Equal(remaining, data) {
			t.Fatalf("source len=%d, want %d identical bytes", len(remaining), len(data))
		}
	})
}

func TestFileEditRejectsFileLargerThanWriteLimitUnchanged(t *testing.T) {
	runFilesystemBackends(t, func(t *testing.T, env *filesystemTestEnv) {
		data := append(bytes.Repeat([]byte("p"), maxWriteBytes+9), []byte("needle")...)
		env.WriteFile(t, "large.txt", data)
		op := env.Operation(t, FileEditOp)

		result := op.Run(operation.NewContext(context.Background(), event.Discard()), map[string]any{
			"path": "large.txt",
			"operations": []map[string]any{
				{"op": "patch", "old": "needle", "new": "changed"},
			},
		})
		if !result.IsError() || result.Error == nil || result.Error.Code != "file_edit_too_large" {
			t.Fatalf("result = %#v, want file_edit_too_large", result)
		}
		if after := env.ReadFile(t, "large.txt"); !bytes.Equal(after, data) {
			t.Fatal("large file changed after rejected edit")
		}
	})
}

func TestFileEditFullDiffIncludesDiffInModelText(t *testing.T) {
	runFilesystemBackends(t, func(t *testing.T, env *filesystemTestEnv) {
		env.WriteFile(t, "note.txt", []byte("one\ntwo\nthree\n"))
		op := env.Operation(t, FileEditOp)

		result := op.Run(operation.NewContext(context.Background(), event.Discard()), map[string]any{
			"path": "note.txt",
			"operations": []map[string]any{
				{"op": "patch", "old": "two", "new": "changed"},
			},
		})
		if result.IsError() {
			t.Fatalf("result error = %#v", result.Error)
		}
		rendered, ok := result.Output.(operation.Rendered)
		if !ok {
			t.Fatalf("output = %#v, want rendered", result.Output)
		}
		if !strings.Contains(rendered.Text, "--- note.txt") || !strings.Contains(rendered.Text, "-two") || !strings.Contains(rendered.Text, "+changed") {
			t.Fatalf("text = %q, want unified diff", rendered.Text)
		}
		if !strings.Contains(rendered.ModelText(), "--- note.txt") || !strings.Contains(rendered.ModelText(), "-two") || !strings.Contains(rendered.ModelText(), "+changed") {
			t.Fatalf("model text = %q, want unified diff", rendered.ModelText())
		}
		data, ok := rendered.Data.(map[string]any)
		if !ok {
			t.Fatalf("data = %#v, want map", rendered.Data)
		}
		diff, _ := data["diff"].(string)
		if !strings.Contains(diff, "-two") || !strings.Contains(diff, "+changed") {
			t.Fatalf("data[diff] = %q, want unified diff with changed line", diff)
		}
		ops, _ := data["operations"].([]editFragment)
		if len(ops) != 1 || !ops[0].Applied || ops[0].Line < 1 {
			t.Fatalf("data[operations] = %#v, want 1 applied status with line >= 1", ops)
		}
	})
}

func TestFileEditNoMatchLeavesFileUnchanged(t *testing.T) {
	runFilesystemBackends(t, func(t *testing.T, env *filesystemTestEnv) {
		original := "one\ntwo\nthree\n"
		env.WriteFile(t, "note.txt", []byte(original))
		op := env.Operation(t, FileEditOp)

		result := op.Run(operation.NewContext(context.Background(), event.Discard()), map[string]any{
			"path": "note.txt",
			"operations": []map[string]any{
				{"op": "patch", "old": "notpresent", "new": "nothing"},
			},
		})
		if !result.IsError() || result.Error == nil || result.Error.Code != "invalid_file_edit_operation" {
			t.Fatalf("result = %#v, want invalid_file_edit_operation error", result)
		}
		if after := string(env.ReadFile(t, "note.txt")); after != original {
			t.Fatalf("file changed after failed patch: %q", after)
		}
	})
}

func TestFileEditDryRunReturnsDiffOnly(t *testing.T) {
	runFilesystemBackends(t, func(t *testing.T, env *filesystemTestEnv) {
		original := "alpha\nbeta\ngamma\n"
		env.WriteFile(t, "file.txt", []byte(original))
		op := env.Operation(t, FileEditOp)

		result := op.Run(operation.NewContext(context.Background(), event.Discard()), map[string]any{
			"path":    "file.txt",
			"dry_run": true,
			"operations": []map[string]any{
				{"op": "patch", "old": "beta", "new": "REPLACED"},
			},
		})
		if result.IsError() {
			t.Fatalf("dry_run error = %#v", result.Error)
		}
		rendered, ok := result.Output.(operation.Rendered)
		if !ok {
			t.Fatalf("output = %#v, want rendered", result.Output)
		}
		if !strings.Contains(rendered.Text, "--- file.txt") || !strings.Contains(rendered.Text, "-beta") || !strings.Contains(rendered.Text, "+REPLACED") {
			t.Fatalf("text = %q, want unified diff with changed line", rendered.Text)
		}
		if !strings.Contains(rendered.Text, "@@") {
			t.Fatalf("text = %q, want unified diff hunk header @@", rendered.Text)
		}
		if len(rendered.Text) > 2*len(original)+200 {
			t.Fatalf("text len=%d, looks larger than expected diff; original len=%d", len(rendered.Text), len(original))
		}
		if after := string(env.ReadFile(t, "file.txt")); after != original {
			t.Fatalf("file changed during dry_run: %q", after)
		}
	})
}

func TestFileEditOperationsUseOriginalCoordinates(t *testing.T) {
	runFilesystemBackends(t, func(t *testing.T, env *filesystemTestEnv) {
		original := "header old\nline2\nline3\nline4\n"
		env.WriteFile(t, "file.txt", []byte(original))
		op := env.Operation(t, FileEditOp)

		result := op.Run(operation.NewContext(context.Background(), event.Discard()), map[string]any{
			"path": "file.txt",
			"operations": []map[string]any{
				{"op": "patch", "old": "header old", "new": "header new\ninserted"},
				{"op": "replace_range", "start_line": 4, "end_line": 4, "content": "line4 changed\n"},
			},
		})
		if result.IsError() {
			t.Fatalf("result error = %#v", result.Error)
		}
		want := "header new\ninserted\nline2\nline3\nline4 changed\n"
		if after := string(env.ReadFile(t, "file.txt")); after != want {
			t.Fatalf("after = %q, want %q", after, want)
		}
	})
}

func TestFileEditAtomicOperationSuccessCases(t *testing.T) {
	runFilesystemBackends(t, func(t *testing.T, env *filesystemTestEnv) {
		op := env.Operation(t, FileEditOp)
		for _, tc := range []struct {
			name      string
			input     map[string]any
			original  string
			wantAfter string
		}{
			{name: "patch", input: map[string]any{"op": "patch", "old": "two", "new": "TWO"}, original: "one\ntwo\nthree\n", wantAfter: "one\nTWO\nthree\n"},
			{name: "insert_after", input: map[string]any{"op": "insert_after", "line": 2, "content": "two point five\n"}, original: "one\ntwo\nthree\n", wantAfter: "one\ntwo\ntwo point five\nthree\n"},
			{name: "insert_before", input: map[string]any{"op": "insert_before", "line": 2, "content": "one point five\n"}, original: "one\ntwo\nthree\n", wantAfter: "one\none point five\ntwo\nthree\n"},
			{name: "replace_range", input: map[string]any{"op": "replace_range", "start_line": 2, "end_line": 3, "content": "changed\n"}, original: "one\ntwo\nthree\nfour\n", wantAfter: "one\nchanged\nfour\n"},
			{name: "delete_range", input: map[string]any{"op": "delete_range", "start_line": 2, "end_line": 3}, original: "one\ntwo\nthree\nfour\n", wantAfter: "one\nfour\n"},
			{name: "append", input: map[string]any{"op": "append", "content": "three\n"}, original: "one\ntwo\n", wantAfter: "one\ntwo\nthree\n"},
			{name: "prepend", input: map[string]any{"op": "prepend", "content": "zero\n"}, original: "one\ntwo\n", wantAfter: "zero\none\ntwo\n"},
		} {
			t.Run(tc.name, func(t *testing.T) {
				env.WriteFile(t, "note.txt", []byte(tc.original))
				result := op.Run(operation.NewContext(context.Background(), event.Discard()), map[string]any{
					"path":       "note.txt",
					"operations": []map[string]any{tc.input},
				})
				if result.IsError() {
					t.Fatalf("result error = %#v", result.Error)
				}
				if after := string(env.ReadFile(t, "note.txt")); after != tc.wantAfter {
					t.Fatalf("after = %q, want %q", after, tc.wantAfter)
				}
			})
		}
	})
}

func TestFileEditRejectsOverlappingOperationsUnchanged(t *testing.T) {
	runFilesystemBackends(t, func(t *testing.T, env *filesystemTestEnv) {
		original := "one\ntwo\nthree\n"
		env.WriteFile(t, "note.txt", []byte(original))
		op := env.Operation(t, FileEditOp)

		result := op.Run(operation.NewContext(context.Background(), event.Discard()), map[string]any{
			"path": "note.txt",
			"operations": []map[string]any{
				{"op": "patch", "old": "two", "new": "changed"},
				{"op": "replace_range", "start_line": 2, "end_line": 2, "content": "other\n"},
			},
		})
		if !result.IsError() || result.Error == nil || result.Error.Code != "file_edit_overlap" {
			t.Fatalf("result = %#v, want file_edit_overlap", result)
		}
		if after := string(env.ReadFile(t, "note.txt")); after != original {
			t.Fatalf("after = %q, want unchanged %q", after, original)
		}
	})
}

func TestFileEditRejectsMissingRequiredFieldsUnchanged(t *testing.T) {
	runFilesystemBackends(t, func(t *testing.T, env *filesystemTestEnv) {
		original := "one\ntwo\nthree\n"
		op := env.Operation(t, FileEditOp)

		for _, tc := range []struct {
			name  string
			input map[string]any
		}{
			{name: "replace_range_missing_content", input: map[string]any{"op": "replace_range", "start_line": 1, "end_line": 1}},
			{name: "insert_after_missing_content", input: map[string]any{"op": "insert_after", "line": 1}},
			{name: "append_missing_content", input: map[string]any{"op": "append"}},
			{name: "patch_missing_new", input: map[string]any{"op": "patch", "old": "two"}},
		} {
			t.Run(tc.name, func(t *testing.T) {
				env.WriteFile(t, "note.txt", []byte(original))
				result := op.Run(operation.NewContext(context.Background(), event.Discard()), map[string]any{
					"path":       "note.txt",
					"operations": []map[string]any{tc.input},
				})
				if !result.IsError() || result.Error == nil || result.Error.Code != "invalid_file_edit_operation" {
					t.Fatalf("result = %#v, want invalid_file_edit_operation", result)
				}
				if after := string(env.ReadFile(t, "note.txt")); after != original {
					t.Fatalf("after = %q, want unchanged %q", after, original)
				}
			})
		}
	})
}

func TestFileEditSameBoundaryInsertsKeepRequestOrder(t *testing.T) {
	runFilesystemBackends(t, func(t *testing.T, env *filesystemTestEnv) {
		env.WriteFile(t, "note.txt", []byte("body\n"))
		op := env.Operation(t, FileEditOp)

		result := op.Run(operation.NewContext(context.Background(), event.Discard()), map[string]any{
			"path": "note.txt",
			"operations": []map[string]any{
				{"op": "prepend", "content": "first\n"},
				{"op": "insert_before", "line": 1, "content": "second\n"},
			},
		})
		if result.IsError() {
			t.Fatalf("result error = %#v", result.Error)
		}
		if after := string(env.ReadFile(t, "note.txt")); after != "first\nsecond\nbody\n" {
			t.Fatalf("after = %q, want ordered same-boundary inserts", after)
		}
	})
}

func TestFileEditAtomicAndNoDiffModes(t *testing.T) {
	runFilesystemBackends(t, func(t *testing.T, env *filesystemTestEnv) {
		env.WriteFile(t, "note.txt", []byte("one\ntwo\n"))
		op := env.Operation(t, FileEditOp)

		atomic := op.Run(operation.NewContext(context.Background(), event.Discard()), map[string]any{
			"path":      "note.txt",
			"dry_run":   true,
			"diff_mode": "atomic",
			"operations": []map[string]any{
				{"op": "patch", "old": "one", "new": "ONE"},
				{"op": "append", "content": "three\n"},
			},
		})
		if atomic.IsError() {
			t.Fatalf("atomic result error = %#v", atomic.Error)
		}
		rendered, ok := atomic.Output.(operation.Rendered)
		if !ok {
			t.Fatalf("output = %#v, want rendered", atomic.Output)
		}
		if !strings.Contains(rendered.Text, "# operation 0 (patch)") || !strings.Contains(rendered.Text, "# operation 1 (append)") {
			t.Fatalf("atomic text = %q, want per-operation diffs", rendered.Text)
		}

		if !strings.Contains(rendered.ModelText(), "# operation 0 (patch)") || !strings.Contains(rendered.ModelText(), "# operation 1 (append)") {
			t.Fatalf("atomic model text = %q, want per-operation diffs", rendered.ModelText())
		}

		none := op.Run(operation.NewContext(context.Background(), event.Discard()), map[string]any{
			"path":      "note.txt",
			"dry_run":   true,
			"diff_mode": "none",
			"operations": []map[string]any{
				{"op": "patch", "old": "one", "new": "ONE"},
			},
		})
		if none.IsError() {
			t.Fatalf("none result error = %#v", none.Error)
		}
		rendered, ok = none.Output.(operation.Rendered)
		if !ok {
			t.Fatalf("output = %#v, want rendered", none.Output)
		}
		if strings.Contains(rendered.ModelText(), "--- note.txt") {
			t.Fatalf("none model text = %q, did not want diff", rendered.ModelText())
		}
	})
}

func TestFileEditSchemaDocumentsOneOfOperations(t *testing.T) {
	spec := specByName(FileEditOp)
	schema := string(spec.Input.Schema.Data)
	for _, want := range []string{`"oneOf"`, `"patch"`, `"replace_range"`, "All line numbers and exact-text patches refer to the original file"} {
		if !strings.Contains(schema, want) {
			t.Fatalf("schema missing %q: %s", want, schema)
		}
	}
}

func TestFileDeleteDeletesFileAndEmptyDirectory(t *testing.T) {
	runFilesystemBackends(t, func(t *testing.T, env *filesystemTestEnv) {
		env.WriteFile(t, "note.txt", []byte("x"))
		env.Mkdir(t, "empty")
		op := env.Operation(t, FileDeleteOp)
		for _, path := range []string{"note.txt", "empty"} {
			result := op.Run(operation.NewContext(context.Background(), event.Discard()), map[string]any{"path": path})
			if result.IsError() {
				t.Fatalf("delete %s error = %#v", path, result.Error)
			}
			env.MustNotExist(t, path)
		}
	})
}

func TestFileDeleteRejectsNonEmptyDirectory(t *testing.T) {
	runFilesystemBackends(t, func(t *testing.T, env *filesystemTestEnv) {
		env.Mkdir(t, "dir")
		env.WriteFile(t, "dir/note.txt", []byte("x"))
		op := env.Operation(t, FileDeleteOp)

		result := op.Run(operation.NewContext(context.Background(), event.Discard()), map[string]any{"path": "dir"})
		if !result.IsError() || result.Error == nil || result.Error.Code != "file_delete_failed" {
			t.Fatalf("result = %#v, want file_delete_failed", result)
		}
		env.MustExist(t, "dir/note.txt")
	})
}

func TestFileDeleteRejectsOutsideWorkspaceSymlink(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	outsideFile := filepath.Join(outside, "secret.txt")
	if err := os.WriteFile(outsideFile, []byte("secret"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outsideFile, filepath.Join(root, "link.txt")); err != nil {
		t.Skipf("symlink not available: %v", err)
	}
	env := newHostFilesystemTestEnv(t, root)
	op := env.Operation(t, FileDeleteOp)

	result := op.Run(operation.NewContext(context.Background(), event.Discard()), map[string]any{"path": "link.txt"})
	if !result.IsError() || result.Error == nil || result.Error.Code != "file_delete_failed" {
		t.Fatalf("result = %#v, want file_delete_failed", result)
	}
	if _, err := os.Stat(outsideFile); err != nil {
		t.Fatalf("outside file missing after rejected delete: %v", err)
	}
}

func TestFileStatReportsFileAndDirectory(t *testing.T) {
	runFilesystemBackends(t, func(t *testing.T, env *filesystemTestEnv) {
		env.Mkdir(t, "dir")
		env.WriteFile(t, "dir/note.txt", []byte("abc"))
		op := env.Operation(t, FileStatOp)

		file := op.Run(operation.NewContext(context.Background(), event.Discard()), map[string]any{"path": "dir/note.txt"})
		if file.IsError() {
			t.Fatalf("file stat error = %#v", file.Error)
		}
		rendered, ok := file.Output.(operation.Rendered)
		if !ok {
			t.Fatalf("output = %#v, want rendered", file.Output)
		}
		data, ok := rendered.Data.(map[string]any)
		if !ok || data["is_dir"] != false || data["size"] != int64(3) {
			t.Fatalf("file stat data = %#v", rendered.Data)
		}

		dir := op.Run(operation.NewContext(context.Background(), event.Discard()), map[string]any{"path": "dir"})
		if dir.IsError() {
			t.Fatalf("dir stat error = %#v", dir.Error)
		}
		rendered, ok = dir.Output.(operation.Rendered)
		if !ok {
			t.Fatalf("output = %#v, want rendered", dir.Output)
		}
		data, ok = rendered.Data.(map[string]any)
		if !ok || data["is_dir"] != true {
			t.Fatalf("dir stat data = %#v", rendered.Data)
		}
	})
}

func TestDirectoryOperationsCreateListAndTree(t *testing.T) {
	runFilesystemBackends(t, func(t *testing.T, env *filesystemTestEnv) {
		create := env.Operation(t, DirCreateOp)
		result := create.Run(operation.NewContext(context.Background(), event.Discard()), map[string]any{"path": "docs/api", "parents": true})
		if result.IsError() {
			t.Fatalf("dir_create error = %#v", result.Error)
		}
		env.WriteFile(t, "docs/api/readme.md", []byte("# API\n"))
		env.WriteFile(t, "docs/api/.hidden.md", []byte("secret\n"))
		env.WriteFile(t, "docs/notes.txt", []byte("notes\n"))

		list := env.Operation(t, DirListOp)
		listed := list.Run(operation.NewContext(context.Background(), event.Discard()), map[string]any{"path": "docs/api", "pattern": "*.md"})
		if listed.IsError() {
			t.Fatalf("dir_list error = %#v", listed.Error)
		}
		rendered, ok := listed.Output.(operation.Rendered)
		if !ok {
			t.Fatalf("output = %#v, want rendered", listed.Output)
		}
		if !strings.Contains(rendered.Text, "readme.md") || strings.Contains(rendered.Text, ".hidden.md") {
			t.Fatalf("dir_list text = %q", rendered.Text)
		}

		tree := env.Operation(t, DirTreeOp)
		treed := tree.Run(operation.NewContext(context.Background(), event.Discard()), map[string]any{"path": "docs", "depth": 2})
		if treed.IsError() {
			t.Fatalf("dir_tree error = %#v", treed.Error)
		}
		rendered, ok = treed.Output.(operation.Rendered)
		if !ok {
			t.Fatalf("output = %#v, want rendered", treed.Output)
		}
		if !strings.Contains(rendered.Text, "api/") || !strings.Contains(rendered.Text, "notes.txt") || strings.Contains(rendered.Text, ".hidden.md") {
			t.Fatalf("dir_tree text = %q", rendered.Text)
		}
	})
}

func TestGlobMatchesRootLevelGlobstarThroughPlugin(t *testing.T) {
	runFilesystemBackends(t, func(t *testing.T, env *filesystemTestEnv) {
		env.WriteFile(t, "README.md", []byte("root"))
		env.WriteFile(t, "eval-review.md", []byte("review"))
		env.WriteFile(t, "docs/README.md", []byte("docs"))
		op := env.Operation(t, GlobOp)

		result := op.Run(operation.NewContext(context.Background(), event.Discard()), map[string]any{
			"pattern":     "**/*.md",
			"max_results": 20,
		})
		if result.IsError() {
			t.Fatalf("glob error = %#v", result.Error)
		}
		rendered, ok := result.Output.(operation.Rendered)
		if !ok {
			t.Fatalf("output = %#v, want rendered", result.Output)
		}
		for _, want := range []string{"README.md", "eval-review.md", "docs/README.md"} {
			if !strings.Contains(rendered.Text, want) {
				t.Fatalf("glob text = %q, want %s", rendered.Text, want)
			}
		}

		result = op.Run(operation.NewContext(context.Background(), event.Discard()), map[string]any{
			"pattern":     "**/eval-review.md",
			"max_results": 20,
		})
		if result.IsError() {
			t.Fatalf("glob error = %#v", result.Error)
		}
		rendered, ok = result.Output.(operation.Rendered)
		if !ok || !strings.Contains(rendered.Text, "eval-review.md") {
			t.Fatalf("glob text = %#v, want eval-review.md", result.Output)
		}
	})
}

func TestGlobMatchesBraceAlternationThroughPlugin(t *testing.T) {
	runFilesystemBackends(t, func(t *testing.T, env *filesystemTestEnv) {
		env.WriteFile(t, ".agents/designs/design.md", []byte("design"))
		env.WriteFile(t, ".agents/plans/plan.md", []byte("plan"))
		env.WriteFile(t, ".agents/reviews/2026/review.md", []byte("review"))
		env.WriteFile(t, ".agents/notes/note.md", []byte("note"))
		op := env.Operation(t, GlobOp)

		result := op.Run(operation.NewContext(context.Background(), event.Discard()), map[string]any{
			"pattern":     ".agents/{designs,plans,reviews}/**/*",
			"max_results": 20,
		})
		if result.IsError() {
			t.Fatalf("glob error = %#v", result.Error)
		}
		rendered, ok := result.Output.(operation.Rendered)
		if !ok {
			t.Fatalf("output = %#v, want rendered", result.Output)
		}
		for _, want := range []string{".agents/designs/design.md", ".agents/plans/plan.md", ".agents/reviews/2026/review.md"} {
			if !strings.Contains(rendered.Text, want) {
				t.Fatalf("glob text = %q, want %s", rendered.Text, want)
			}
		}
		if strings.Contains(rendered.Text, ".agents/notes/note.md") {
			t.Fatalf("glob text = %q, did not want notes match", rendered.Text)
		}
	})
}

func TestFilesystemContributionsUseFileEditNotFilePatch(t *testing.T) {
	bundle, err := (Plugin{}).Contributions(context.Background(), pluginhost.Context{})
	if err != nil {
		t.Fatalf("Contributions: %v", err)
	}
	var sawFileEdit bool
	for _, spec := range bundle.Operations {
		switch spec.Ref.Name {
		case FileEditOp:
			sawFileEdit = true
		case operation.Name("file_patch"):
			t.Fatal("file_patch operation is still contributed")
		}
	}
	if !sawFileEdit {
		t.Fatal("file_edit operation not contributed")
	}
}

func TestFileReadPatternReturnsMatchedRegions(t *testing.T) {
	runFilesystemBackends(t, func(t *testing.T, env *filesystemTestEnv) {
		content := "alpha\nbeta\ngamma\ndelta\nepsilon\nzeta\n"
		env.WriteFile(t, "words.txt", []byte(content))
		op := env.Operation(t, FileReadOp)

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
		for _, notWant := range []string{"alpha", "epsilon", "zeta"} {
			if strings.Contains(text, notWant) {
				t.Errorf("text = %q, did not want %q", text, notWant)
			}
		}
	})
}

func TestFileReadPatternNoMatchReturnsZeroCount(t *testing.T) {
	runFilesystemBackends(t, func(t *testing.T, env *filesystemTestEnv) {
		env.WriteFile(t, "file.txt", []byte("line one\nline two\n"))
		op := env.Operation(t, FileReadOp)

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
	})
}

func TestFileReadPatternAdjacentMatchesMerged(t *testing.T) {
	runFilesystemBackends(t, func(t *testing.T, env *filesystemTestEnv) {
		content := "a\ntarget\nc\ntarget\ne\nf\n"
		env.WriteFile(t, "merge.txt", []byte(content))
		op := env.Operation(t, FileReadOp)

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
		for _, want := range []string{"a", "target", "c", "e"} {
			if !strings.Contains(text, want) {
				t.Errorf("text = %q, want %q in merged region", text, want)
			}
		}
		if strings.Contains(text, "---") {
			t.Errorf("text = %q, did not expect region separator", text)
		}
		if strings.Contains(text, "6: f") {
			t.Errorf("text = %q, did not want 'f' (line 6) outside context", text)
		}
	})
}

func TestGrepDefaultContextLines(t *testing.T) {
	runFilesystemBackends(t, func(t *testing.T, env *filesystemTestEnv) {
		lines := []string{"L1", "L2", "L3", "L4", "TARGET", "L6", "L7", "L8", "L9", "L10"}
		content := strings.Join(lines, "\n") + "\n"
		env.WriteFile(t, "ctx.txt", []byte(content))
		op := env.Operation(t, GrepOp)

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
		for _, want := range []string{"L2", "L3", "L4", "TARGET", "L6", "L7", "L8"} {
			if !strings.Contains(text, want) {
				t.Errorf("text = %q, want %q (default 3-line context)", text, want)
			}
		}
		if strings.Contains(text, "L1\n") || strings.HasPrefix(text, "L1") {
			t.Errorf("text = %q, did not want L1 (> 3 lines from match)", text)
		}
	})
}

func TestGrepExplicitZeroContextLines(t *testing.T) {
	runFilesystemBackends(t, func(t *testing.T, env *filesystemTestEnv) {
		content := "before\nMATCH\nafter\n"
		env.WriteFile(t, "z.txt", []byte(content))
		op := env.Operation(t, GrepOp)

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
	})
}

func TestGrepSearchesDirectoriesAcrossMultipleFiles(t *testing.T) {
	runFilesystemBackends(t, func(t *testing.T, env *filesystemTestEnv) {
		env.WriteFile(t, "src/a.txt", []byte("alpha\nMATCH one\n"))
		env.WriteFile(t, "src/b.txt", []byte("MATCH two\nbeta\n"))
		env.WriteFile(t, "src/c.txt", []byte("nothing\n"))
		op := env.Operation(t, GrepOp)

		result := op.Run(operation.NewContext(context.Background(), event.Discard()), map[string]any{
			"pattern":      "MATCH",
			"paths":        []string{"src"},
			"show_content": true,
		})
		if result.IsError() {
			t.Fatalf("result error = %#v", result.Error)
		}
		rendered, ok := result.Output.(operation.Rendered)
		if !ok {
			t.Fatalf("output = %#v, want rendered", result.Output)
		}
		for _, want := range []string{"src/a.txt", "MATCH one", "src/b.txt", "MATCH two"} {
			if !strings.Contains(rendered.ModelText(), want) {
				t.Fatalf("grep text = %q, want %q", rendered.ModelText(), want)
			}
		}
		if strings.Contains(rendered.ModelText(), "src/c.txt") {
			t.Fatalf("grep text = %q, did not want nonmatching file", rendered.ModelText())
		}
	})
}
