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

func TestDeprecatedCoreLeafAliasPackagesDoNotReturn(t *testing.T) {
	forbiddenImports := []string{
		"github.com/fluxplane/fluxplane-core/core/" + "context",
		"github.com/fluxplane/fluxplane-core/core/" + "operation",
		"github.com/fluxplane/fluxplane-core/core/" + "policy",
	}
	forbiddenDirs := []string{
		filepath.Join("core", "context"),
		filepath.Join("core", "operation"),
		filepath.Join("core", "policy"),
	}
	var bad []string
	for _, dir := range forbiddenDirs {
		if _, err := os.Stat(dir); err == nil {
			bad = append(bad, dir+" compatibility package exists")
		} else if !os.IsNotExist(err) {
			t.Fatalf("stat %s: %v", dir, err)
		}
	}
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
			for _, forbidden := range forbiddenImports {
				if pathValue == forbidden {
					bad = append(bad, path+" imports deprecated alias package "+pathValue)
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk core module: %v", err)
	}
	if len(bad) > 0 {
		t.Fatalf("deprecated core leaf alias package boundary violations:\n%s", strings.Join(bad, "\n"))
	}
}

func TestRemovedCorePluginImplementationDirsDoNotReturn(t *testing.T) {
	forbiddenDirs := []string{
		filepath.Join("plugins", "bundles"),
		filepath.Join("plugins", "examples"),
		filepath.Join("plugins", "internal"),
		filepath.Join("plugins", "integrations"),
		filepath.Join("plugins", "languages"),
		filepath.Join("plugins", "support"),
		filepath.Join("plugins", "native", "browser"),
		filepath.Join("plugins", "native", "clock"),
		filepath.Join("plugins", "native", "code"),
		filepath.Join("plugins", "native", "filesystem"),
		filepath.Join("plugins", "native", "project"),
		filepath.Join("plugins", "native", "shell"),
		filepath.Join("plugins", "native", "sleep"),
		filepath.Join("plugins", "native", "text"),
	}
	forbiddenImports := []string{
		"github.com/fluxplane/fluxplane-core/plugins/" + "bundles/",
		"github.com/fluxplane/fluxplane-core/plugins/" + "examples/",
		"github.com/fluxplane/fluxplane-core/plugins/" + "internal/",
		"github.com/fluxplane/fluxplane-core/plugins/" + "integrations/",
		"github.com/fluxplane/fluxplane-core/plugins/" + "languages/",
		"github.com/fluxplane/fluxplane-core/plugins/" + "support/",
		"github.com/fluxplane/fluxplane-core/contrib/" + "browser",
		"github.com/fluxplane/fluxplane-core/contrib/" + "clock",
		"github.com/fluxplane/fluxplane-core/contrib/" + "code",
		"github.com/fluxplane/fluxplane-core/contrib/" + "filesystem",
		"github.com/fluxplane/fluxplane-core/contrib/" + "project",
		"github.com/fluxplane/fluxplane-core/contrib/" + "shell",
		"github.com/fluxplane/fluxplane-core/contrib/" + "sleep",
	}
	var bad []string
	for _, dir := range forbiddenDirs {
		if _, err := os.Stat(dir); err == nil {
			bad = append(bad, dir+" drained plugin implementation directory exists")
		} else if !os.IsNotExist(err) {
			t.Fatalf("stat %s: %v", dir, err)
		}
	}
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
			for _, forbidden := range forbiddenImports {
				if pathValue == forbidden || strings.HasPrefix(pathValue, forbidden+"/") {
					bad = append(bad, path+" imports drained plugin implementation "+pathValue)
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk core module: %v", err)
	}
	if len(bad) > 0 {
		t.Fatalf("drained core plugin implementation boundary violations:\n%s", strings.Join(bad, "\n"))
	}
}

func TestContribProvidersDoNotImportRemovedHostPackage(t *testing.T) {
	surfaceDirs := []string{
		"contrib",
	}
	forbidden := "github.com/fluxplane/fluxplane-core/orchestration/" + "plugin" + "host"
	var bad []string
	for _, root := range surfaceDirs {
		err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() {
				return nil
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
				if pathValue == forbidden {
					bad = append(bad, path+" imports removed host package")
				}
			}
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", root, err)
		}
	}
	if len(bad) > 0 {
		t.Fatalf("migrated Core surface import boundary violations:\n%s", strings.Join(bad, "\n"))
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
		"github.com/go-sql-driver/mysql": true,
		"github.com/moby/go-archive":     true,
		"github.com/moby/moby/api":       true,
		"github.com/moby/moby/client":    true,
		"github.com/openai/openai-go/v3": true,
		"github.com/slack-go/slack":      true,
		"k8s.io/client-go":               true,
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

func TestCoreProviderSDKImportsStayInApprovedRuntimeInfrastructure(t *testing.T) {
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
		"adapters/channels/slack/",
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
		t.Fatalf("provider SDK imports outside approved Core runtime infrastructure:\n%s\nmove provider plugin code to fluxplane-plugins or add a narrower host/SDK contract", strings.Join(bad, "\n"))
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
