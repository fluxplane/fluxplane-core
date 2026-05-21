package architecture_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fluxplane/agentruntime/internal/architecture"
)

func TestLayerImportsPointInward(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	packages, err := architecture.LoadGoList(ctx, "../..")
	if err != nil {
		t.Fatal(err)
	}
	report := architecture.Analyze(packages, architecture.Config{
		ModulePath: architecture.DefaultModulePath,
	})
	if len(report.Violations) > 0 {
		for _, violation := range report.Violations {
			t.Errorf("%s imports %s: %s", violation.From, violation.To, violation.Reason)
		}
	}
}

func TestCoreDatasourceHasNoProviderSpecificDetectorTerms(t *testing.T) {
	root := filepath.Join("..", "..", "core", "datasource")
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || filepath.Ext(path) != ".go" {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		lower := strings.ToLower(string(data))
		for _, term := range []string{"slack", "jira", "gitlab"} {
			if strings.Contains(lower, term) {
				t.Fatalf("%s contains provider-specific term %q", path, term)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestAnalyzeReportsViolationsAndScore(t *testing.T) {
	packages := []architecture.ListedPackage{
		{
			ImportPath: "github.com/fluxplane/agentruntime/core/operation",
			Imports:    []string{"github.com/fluxplane/agentruntime/runtime/operation"},
		},
		{
			ImportPath: "github.com/fluxplane/agentruntime/runtime/operation",
			Imports:    []string{"github.com/fluxplane/agentruntime/core/operation"},
		},
		{
			ImportPath: "github.com/fluxplane/agentruntime/adapters/slack",
			Imports:    []string{"github.com/fluxplane/agentruntime/plugins/integrations/slack"},
		},
		{
			ImportPath: "github.com/fluxplane/agentruntime/plugins/integrations/slack",
			Imports:    []string{"github.com/fluxplane/agentruntime/adapters/slack"},
		},
		{
			ImportPath: "github.com/fluxplane/agentruntime/apps/devclient",
			Imports:    []string{"github.com/fluxplane/agentruntime"},
		},
		{
			ImportPath: "github.com/fluxplane/agentruntime",
			Imports:    []string{"github.com/fluxplane/agentruntime/apps/devclient"},
		},
	}
	report := architecture.Analyze(packages, architecture.Config{
		ModulePath: architecture.DefaultModulePath,
	})
	if len(report.Violations) != 3 {
		t.Fatalf("violations = %d, want 3", len(report.Violations))
	}
	if report.Summary.Score >= 100 {
		t.Fatalf("score = %d, want penalty", report.Summary.Score)
	}
	if report.Scores.Boundary >= 100 {
		t.Fatalf("boundary score = %d, want penalty", report.Scores.Boundary)
	}
}

func TestAnalyzeSeparatesTestBoundaryViolations(t *testing.T) {
	packages := []architecture.ListedPackage{
		{
			ImportPath:  "github.com/fluxplane/agentruntime/core/operation",
			TestImports: []string{"github.com/fluxplane/agentruntime/runtime/operation"},
		},
		{
			ImportPath: "github.com/fluxplane/agentruntime/runtime/operation",
		},
	}
	report := architecture.Analyze(packages, architecture.Config{
		ModulePath:   architecture.DefaultModulePath,
		IncludeTests: true,
	})
	if report.Scores.Boundary != 100 {
		t.Fatalf("boundary score = %d, want 100", report.Scores.Boundary)
	}
	if report.Scores.TestBoundary != 90 {
		t.Fatalf("test boundary score = %d, want 90", report.Scores.TestBoundary)
	}
	if architecture.HasFailures(report, "boundary") {
		t.Fatal("boundary gate failed on test-only violation")
	}
	if !architecture.HasFailures(report, "test-boundary") {
		t.Fatal("test-boundary gate did not fail")
	}
}

func TestAnalyzeTracksReviewedFanOutWithoutCouplingPenalty(t *testing.T) {
	packages := fanOutPackages("github.com/fluxplane/agentruntime/core/resource")
	report := architecture.Analyze(packages, architecture.Config{ModulePath: architecture.DefaultModulePath})
	if report.Scores.Coupling != 100 {
		t.Fatalf("coupling score = %d, want reviewed fan-out excluded", report.Scores.Coupling)
	}
	if len(report.Summary.ScorePenalties) != 1 || !report.Summary.ScorePenalties[0].Allowed || report.Summary.ScorePenalties[0].Penalty != 0 {
		t.Fatalf("score penalties = %#v, want one allowed fan-out note", report.Summary.ScorePenalties)
	}
}

func TestAnalyzePenalizesUnreviewedFanOut(t *testing.T) {
	packages := fanOutPackages("github.com/fluxplane/agentruntime/core/newhub")
	report := architecture.Analyze(packages, architecture.Config{ModulePath: architecture.DefaultModulePath})
	if report.Scores.Coupling != 98 {
		t.Fatalf("coupling score = %d, want unreviewed fan-out penalty", report.Scores.Coupling)
	}
	if len(report.Summary.ScorePenalties) != 1 || report.Summary.ScorePenalties[0].Allowed || report.Summary.ScorePenalties[0].Penalty != 2 {
		t.Fatalf("score penalties = %#v, want one unreviewed fan-out penalty", report.Summary.ScorePenalties)
	}
}

func TestAnalyzeReportsUnknownPackages(t *testing.T) {
	report := architecture.Analyze([]architecture.ListedPackage{
		{ImportPath: "github.com/fluxplane/agentruntime/experimental/foo"},
	}, architecture.Config{ModulePath: architecture.DefaultModulePath})
	if !hasDiagnostic(report, "unknown_package", "github.com/fluxplane/agentruntime/experimental/foo") {
		t.Fatalf("diagnostics = %#v, want unknown package", report.Diagnostics)
	}
	if !architecture.HasFailures(report, "unknown") {
		t.Fatal("unknown gate did not fail")
	}
}

func TestAnalyzeReportsInnerLayerHostIO(t *testing.T) {
	report := architecture.Analyze([]architecture.ListedPackage{
		{
			ImportPath: "github.com/fluxplane/agentruntime/core/bad",
			Imports:    []string{"os"},
		},
	}, architecture.Config{ModulePath: architecture.DefaultModulePath})
	if !hasDiagnostic(report, "inner_host_io", "github.com/fluxplane/agentruntime/core/bad") {
		t.Fatalf("diagnostics = %#v, want inner host IO", report.Diagnostics)
	}
	if !architecture.HasFailures(report, "side-effects") {
		t.Fatal("side-effects gate did not fail")
	}
}

func TestAnalyzeRequiresRuntimeHostIOAllowlist(t *testing.T) {
	report := architecture.Analyze([]architecture.ListedPackage{
		{
			ImportPath: "github.com/fluxplane/agentruntime/runtime/newsideeffect",
			Imports:    []string{"os"},
		},
		{
			ImportPath: "github.com/fluxplane/agentruntime/runtime/system",
			Imports:    []string{"os"},
		},
	}, architecture.Config{ModulePath: architecture.DefaultModulePath})
	if !hasDiagnostic(report, "runtime_host_io", "github.com/fluxplane/agentruntime/runtime/newsideeffect") {
		t.Fatalf("diagnostics = %#v, want runtime host IO", report.Diagnostics)
	}
	if !hasAllowedDiagnostic(report, "runtime_host_io", "github.com/fluxplane/agentruntime/runtime/system") {
		t.Fatalf("diagnostics = %#v, want allowed runtime host IO", report.Diagnostics)
	}
	if !architecture.HasFailures(report, "side-effects") {
		t.Fatal("side-effects gate did not fail")
	}
}

func TestAnalyzeScansPluginHostEffects(t *testing.T) {
	dir := t.TempDir()
	source := []byte(`package plugin

import "os"

func configure() bool {
	_, ok := os.LookupEnv("TOKEN")
	return ok
}
`)
	if err := os.WriteFile(filepath.Join(dir, "plugin.go"), source, 0644); err != nil {
		t.Fatal(err)
	}
	report := architecture.Analyze([]architecture.ListedPackage{
		{
			ImportPath: "github.com/fluxplane/agentruntime/plugins/test/plugin",
			Dir:        dir,
			GoFiles:    []string{"plugin.go"},
			Imports:    []string{"os"},
		},
	}, architecture.Config{ModulePath: architecture.DefaultModulePath})
	if !hasDiagnostic(report, "plugin_host_effect", "github.com/fluxplane/agentruntime/plugins/test/plugin") {
		t.Fatalf("diagnostics = %#v, want plugin host effect", report.Diagnostics)
	}
}

func fanOutPackages(importPath string) []architecture.ListedPackage {
	packages := []architecture.ListedPackage{
		{
			ImportPath: importPath,
			Imports: []string{
				"github.com/fluxplane/agentruntime/core/a",
				"github.com/fluxplane/agentruntime/core/b",
				"github.com/fluxplane/agentruntime/core/c",
				"github.com/fluxplane/agentruntime/core/d",
				"github.com/fluxplane/agentruntime/core/e",
				"github.com/fluxplane/agentruntime/core/f",
				"github.com/fluxplane/agentruntime/core/g",
				"github.com/fluxplane/agentruntime/core/h",
				"github.com/fluxplane/agentruntime/core/i",
				"github.com/fluxplane/agentruntime/core/j",
				"github.com/fluxplane/agentruntime/core/k",
				"github.com/fluxplane/agentruntime/core/l",
				"github.com/fluxplane/agentruntime/core/m",
			},
		},
	}
	for _, imported := range packages[0].Imports {
		packages = append(packages, architecture.ListedPackage{ImportPath: imported})
	}
	return packages
}

func hasDiagnostic(report architecture.Report, kind, pkg string) bool {
	for _, diagnostic := range report.Diagnostics {
		if diagnostic.Kind == kind && diagnostic.Package == pkg && !diagnostic.Allowed {
			return true
		}
	}
	return false
}

func hasAllowedDiagnostic(report architecture.Report, kind, pkg string) bool {
	for _, diagnostic := range report.Diagnostics {
		if diagnostic.Kind == kind && diagnostic.Package == pkg && diagnostic.Allowed {
			return true
		}
	}
	return false
}
