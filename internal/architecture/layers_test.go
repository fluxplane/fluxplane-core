package architecture_test

import (
	"context"
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
			Imports:    []string{"github.com/fluxplane/agentruntime/plugins/slackplugin"},
		},
		{
			ImportPath: "github.com/fluxplane/agentruntime/plugins/slackplugin",
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
}
