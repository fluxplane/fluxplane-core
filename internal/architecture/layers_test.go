package architecture_test

import (
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
)

const modulePath = "github.com/fluxplane/agentruntime"

type listedPackage struct {
	ImportPath string
	Imports    []string
}

func TestLayerImportsPointInward(t *testing.T) {
	packages := listPackages(t)
	for _, pkg := range packages {
		layer := layerOf(pkg.ImportPath)
		if layer == "" {
			continue
		}
		for _, imported := range pkg.Imports {
			importedLayer := layerOf(imported)
			if importedLayer == "" {
				continue
			}
			if !allowedImport(layer, importedLayer) {
				t.Fatalf("%s package %s imports outer %s package %s", layer, pkg.ImportPath, importedLayer, imported)
			}
		}
	}
}

func listPackages(t *testing.T) []listedPackage {
	t.Helper()
	cmd := exec.Command("go", "list", "-json", "./...")
	raw, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			t.Fatalf("go list failed: %v\n%s", err, string(exitErr.Stderr))
		}
		t.Fatalf("go list failed: %v", err)
	}
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	var packages []listedPackage
	for decoder.More() {
		var pkg listedPackage
		if err := decoder.Decode(&pkg); err != nil {
			t.Fatalf("decode go list output: %v", err)
		}
		packages = append(packages, pkg)
	}
	return packages
}

func layerOf(importPath string) string {
	if !strings.HasPrefix(importPath, modulePath+"/") {
		return ""
	}
	rest := strings.TrimPrefix(importPath, modulePath+"/")
	layer, _, _ := strings.Cut(rest, "/")
	switch layer {
	case "core", "runtime", "orchestration", "adapters", "plugins", "apps":
		return layer
	default:
		return ""
	}
}

func allowedImport(from, to string) bool {
	rank := map[string]int{
		"core":          0,
		"runtime":       1,
		"orchestration": 2,
		"adapters":      3,
		"plugins":       3,
		"apps":          4,
	}
	return rank[to] <= rank[from]
}
