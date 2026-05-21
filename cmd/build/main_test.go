package main

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"testing"
)

func TestAppFromPathResolvesCommandPackage(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "cmd", "coder"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(root)
	app, err := appFromPath("apps/coder")
	if err != nil {
		t.Fatalf("appFromPath: %v", err)
	}
	if app.name != "coder" || app.dir != "." || app.pkg != "./cmd/coder" {
		t.Fatalf("app = %#v, want root coder command", app)
	}
}

func TestAppFromPathResolvesNestedModuleCommandPackage(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "apps", "coder", "cmd", "coder"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(root)
	app, err := appFromPath("apps/coder")
	if err != nil {
		t.Fatalf("appFromPath: %v", err)
	}
	if app.name != "coder" || app.dir != filepath.Join("apps", "coder") || app.pkg != "./cmd/coder" {
		t.Fatalf("app = %#v, want nested coder command", app)
	}
}

func TestDefaultTargetsIncludesHostOnce(t *testing.T) {
	targets := defaultTargets()
	host := runtime.GOOS + "/" + runtime.GOARCH
	if !slices.Contains(targets, host) {
		t.Fatalf("targets = %#v, want host %s", targets, host)
	}
	seen := map[string]bool{}
	for _, target := range targets {
		if seen[target] {
			t.Fatalf("targets = %#v, duplicate %s", targets, target)
		}
		seen[target] = true
	}
}
