package launch

import (
	"path/filepath"
	"strings"
	"testing"

	coreapp "github.com/fluxplane/fluxplane-core/core/app"
)

func TestDefaultSemanticIndexStorePathUsesFluxplaneStateDir(t *testing.T) {
	stateDir := t.TempDir()
	root := filepath.Join(t.TempDir(), "workspace")
	t.Setenv("XDG_STATE_HOME", stateDir)

	path, err := defaultSemanticIndexStorePath(root)
	if err != nil {
		t.Fatalf("defaultSemanticIndexStorePath: %v", err)
	}
	if !strings.HasPrefix(path, filepath.Join(stateDir, "fluxplane", "datasource-indexes")+string(filepath.Separator)) {
		t.Fatalf("path = %q, want under fluxplane state dir %q", path, stateDir)
	}
	if filepath.Base(path) != "datasources.json" {
		t.Fatalf("path = %q, want datasources.json file", path)
	}
	if strings.Contains(path, root) {
		t.Fatalf("path = %q, must not be inside app root %q", path, root)
	}
}

func TestSemanticIndexStorePathKeepsExplicitRelativePathAppRelative(t *testing.T) {
	root := t.TempDir()
	path, err := semanticIndexStorePath(root, coreapp.SemanticSearchSpec{
		Store: coreapp.SemanticStoreSpec{Path: "custom/index.json"},
	}, "")
	if err != nil {
		t.Fatalf("semanticIndexStorePath: %v", err)
	}
	if path != filepath.Join(root, "custom", "index.json") {
		t.Fatalf("path = %q, want explicit app-relative path", path)
	}
}
