package fluxplane_test

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/mod/modfile"
)

func TestCoreDoesNotImportDexOrPluginImplementations(t *testing.T) {
	forbidden := []string{
		"github.com/fluxplane/fluxplane-" + "dex",
		"github.com/fluxplane/fluxplane-" + "plugins",
	}
	var bad []string
	err := filepath.WalkDir(".", func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			switch entry.Name() {
			case ".git", "bin", "vendor":
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
		for _, imported := range file.Imports {
			pathValue := strings.Trim(imported.Path.Value, "\"")
			for _, prefix := range forbidden {
				if strings.HasPrefix(pathValue, prefix) {
					bad = append(bad, path+" imports forbidden module "+pathValue)
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk core module: %v", err)
	}
	if len(bad) > 0 {
		t.Fatalf("core import boundary violations:\n%s", strings.Join(bad, "\n"))
	}
}

func TestCoreGoModDoesNotDependOnDexOrPluginImplementations(t *testing.T) {
	raw, err := os.ReadFile("go.mod")
	if err != nil {
		t.Fatal(err)
	}
	content := string(raw)
	for _, forbidden := range []string{
		"github.com/fluxplane/fluxplane-" + "dex",
		"github.com/fluxplane/fluxplane-" + "plugins",
	} {
		if strings.Contains(content, forbidden) {
			t.Fatalf("go.mod contains forbidden dependency %s", forbidden)
		}
	}
}

func TestCoreProviderSDKDirectDependenciesArePinnedForExtraction(t *testing.T) {
	raw, err := os.ReadFile("go.mod")
	if err != nil {
		t.Fatal(err)
	}
	mod, err := modfile.Parse("go.mod", raw, nil)
	if err != nil {
		t.Fatal(err)
	}
	transitionalAllowlist := map[string]bool{
		"github.com/go-sql-driver/mysql":         true,
		"github.com/moby/go-archive":             true,
		"github.com/moby/moby/api":               true,
		"github.com/moby/moby/client":            true,
		"github.com/openai/openai-go/v3":         true,
		"github.com/slack-go/slack":              true,
		"k8s.io/client-go":                       true,
	}
	providerPrefixes := []string{
		"github.com/aws/aws-sdk-go-v2",
		"github.com/docker/",
		"github.com/go-sql-driver/mysql",
		"github.com/moby/",
		"github.com/openai/openai-go",
		"github.com/slack-go/slack",
		"gitlab.com/gitlab-org/api/client-go",
		"k8s.io/client-go",
	}
	var bad []string
	for _, req := range mod.Require {
		if req.Indirect {
			continue
		}
		path := req.Mod.Path
		if !hasAnyPrefix(path, providerPrefixes) {
			continue
		}
		if !transitionalAllowlist[path] {
			bad = append(bad, path)
		}
	}
	if len(bad) > 0 {
		t.Fatalf("new direct provider SDK dependencies in core go.mod:\n%s\nmove provider code to fluxplane-plugins or update the extraction plan before expanding this allowlist", strings.Join(bad, "\n"))
	}
}

func TestCoreProviderSDKImportsStayInTransitionalPluginTree(t *testing.T) {
	providerPrefixes := []string{
		"github.com/aws/aws-sdk-go-v2",
		"github.com/docker/",
		"github.com/go-sql-driver/mysql",
		"github.com/moby/",
		"github.com/openai/openai-go",
		"github.com/slack-go/slack",
		"gitlab.com/gitlab-org/api/client-go",
		"k8s.io/client-go",
	}
	transitionalPathPrefixes := []string{
		"plugins/",
		"adapters/distribution/deploy/",
		"adapters/llm/codex/",
		"adapters/llm/openai/",
		"adapters/llm/openrouter/",
		"adapters/storage/data/sqlstore/",
		"adapters/storage/datasourcemirror/sqlstore/",
		"apps/launch/data_store.go",
	}
	var bad []string
	err := filepath.WalkDir(".", func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			switch entry.Name() {
			case ".git", "bin", "vendor":
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
		for _, imported := range file.Imports {
			pathValue := strings.Trim(imported.Path.Value, "\"")
			if !hasAnyPrefix(pathValue, providerPrefixes) {
				continue
			}
			if hasAnyPrefix(path, transitionalPathPrefixes) {
				continue
			}
			bad = append(bad, path+" imports provider SDK "+pathValue)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk core module: %v", err)
	}
	if len(bad) > 0 {
		t.Fatalf("provider SDK imports outside transitional Core plugin tree:\n%s\nmove provider code to fluxplane-plugins or add a narrower host/SDK contract", strings.Join(bad, "\n"))
	}
}

func hasAnyPrefix(value string, prefixes []string) bool {
	for _, prefix := range prefixes {
		if strings.HasPrefix(value, prefix) {
			return true
		}
	}
	return false
}
