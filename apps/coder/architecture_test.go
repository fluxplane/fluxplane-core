package coder

import (
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
)

func TestCoderImportsOnlyPublicEnginePackages(t *testing.T) {
	root := "."
	var bad []string
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			switch entry.Name() {
			case ".git", "bin":
				return filepath.SkipDir
			default:
				return nil
			}
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		for _, spec := range file.Imports {
			imp := strings.Trim(spec.Path.Value, `"`)
			switch {
			case strings.HasPrefix(imp, "github.com/fluxplane/engine/internal/"):
				bad = append(bad, path+" imports engine internal package "+imp)
			case strings.HasPrefix(imp, "github.com/fluxplane/engine/apps/coder"):
				bad = append(bad, path+" imports old in-engine coder package "+imp)
			case strings.HasPrefix(imp, "github.com/fluxplane/engine/cmd/"):
				bad = append(bad, path+" imports engine command package "+imp)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk coder module: %v", err)
	}
	if len(bad) > 0 {
		t.Fatalf("coder module import boundary violations:\n%s", strings.Join(bad, "\n"))
	}
}
