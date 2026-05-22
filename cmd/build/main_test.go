package main

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"testing"
)

func TestAppFromPathResolvesRootCommandPackage(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "cmd", "fluxplane"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(root)
	app, err := appFromPath("apps/fluxplane")
	if err != nil {
		t.Fatalf("appFromPath: %v", err)
	}
	if app.name != "fluxplane" || app.dir != "." || app.pkg != "./cmd/fluxplane" {
		t.Fatalf("app = %#v, want root fluxplane command", app)
	}
}

func TestAppFromPathResolvesNestedModuleCommandPackage(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "apps", "example", "cmd", "example"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(root)
	app, err := appFromPath("apps/example")
	if err != nil {
		t.Fatalf("appFromPath: %v", err)
	}
	if app.name != "example" || app.dir != filepath.Join("apps", "example") || app.pkg != "./cmd/example" {
		t.Fatalf("app = %#v, want nested command", app)
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
