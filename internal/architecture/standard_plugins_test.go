package architecture

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStandardPluginsDoNotImportHostIO(t *testing.T) {
	root := repoRoot(t)
	forbidden := map[string]bool{
		"os":       true,
		"os/exec":  true,
		"syscall":  true,
		"net":      true,
		"net/http": true,
		"net/url":  true,
	}
	plugins := []string{
		"plugins/native/browser",
		"plugins/native/code",
		"plugins/native/filesystem",
		"plugins/native/human",
		"plugins/native/shell",
		"plugins/integrations/web",
	}
	for _, dir := range plugins {
		dir := filepath.Join(root, dir)
		err := filepath.WalkDir(dir, func(path string, entry os.DirEntry, err error) error {
			if err != nil || entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return err
			}
			file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
			if err != nil {
				return err
			}
			for _, spec := range file.Imports {
				importPath := strings.Trim(spec.Path.Value, `"`)
				if forbidden[importPath] {
					t.Errorf("%s imports forbidden host IO package %q", rel(t, root, path), importPath)
				}
			}
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", dir, err)
		}
	}
}

func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		next := filepath.Dir(dir)
		if next == dir {
			t.Fatal("go.mod not found")
		}
		dir = next
	}
}

func rel(t *testing.T, root, path string) string {
	t.Helper()
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return path
	}
	return rel
}
