package architecture

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestPluginHostEffectDiagnosticsScanAllPlugins(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	packages, err := LoadGoList(ctx, "../..")
	if err != nil {
		t.Fatal(err)
	}
	report := Analyze(packages, Config{
		ModulePath: DefaultModulePath,
	})
	scannedPlugins := 0
	for _, pkg := range packages {
		if layerOf(DefaultModulePath, pkg.ImportPath) == LayerPlugins && len(pkg.GoFiles) > 0 {
			scannedPlugins++
		}
	}
	if scannedPlugins == 0 {
		t.Fatal("no plugin packages found to scan")
	}
	for _, diagnostic := range report.Diagnostics {
		if diagnostic.Kind == DiagnosticPluginHostEffect && strings.Contains(diagnostic.Reason, "could not be parsed") {
			t.Fatalf("unexpected plugin parse diagnostic: %#v", diagnostic)
		}
	}
}

func TestRuntimeHostIOAllowlistEntriesAreUsedAndExplained(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	packages, err := LoadGoList(ctx, "../..")
	if err != nil {
		t.Fatal(err)
	}
	knownRuntime := map[string]bool{}
	for _, pkg := range packages {
		if layerOf(DefaultModulePath, pkg.ImportPath) != LayerRuntime {
			continue
		}
		shortPkg := strings.TrimPrefix(pkg.ImportPath, DefaultModulePath+"/")
		knownRuntime[shortPkg] = true
	}
	for pkg, reason := range runtimeHostIOAllowlist {
		if strings.TrimSpace(reason) == "" {
			t.Fatalf("runtime host IO allowlist entry %q has no reason", pkg)
		}
		if !knownRuntime[pkg] {
			t.Fatalf("runtime host IO allowlist entry %q does not match a runtime package", pkg)
		}
	}
}
