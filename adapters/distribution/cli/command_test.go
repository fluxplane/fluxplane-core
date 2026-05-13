package cli

import (
	"bytes"
	"strings"
	"testing"

	coredistribution "github.com/fluxplane/agentruntime/core/distribution"
	"github.com/fluxplane/agentruntime/core/resource"
	"github.com/fluxplane/agentruntime/orchestration/distribution"
)

func TestDescribeCommandRendersWithoutRuntime(t *testing.T) {
	cmd := NewCommand(distribution.Distribution{
		Spec: coredistribution.Spec{
			Name:        "sample",
			Title:       "Sample",
			Description: "Sample distribution.",
		},
		Bundles: []resource.ContributionBundle{{
			Source: resource.SourceRef{ID: "sample/source"},
		}},
	})
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"describe", "-o", "json"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	text := out.String()
	for _, want := range []string{`"distribution"`, `"name": "sample"`, `"sources"`} {
		if !strings.Contains(text, want) {
			t.Fatalf("describe output missing %q:\n%s", want, text)
		}
	}
}

func TestDescribeCommandRejectsUnsupportedOutput(t *testing.T) {
	cmd := NewCommand(distribution.Distribution{
		Spec: coredistribution.Spec{Name: "sample"},
	})
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"describe", "-o", "xml"})

	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), `unsupported output "xml"`) {
		t.Fatalf("Execute error = %v, want unsupported output", err)
	}
}

func TestDescribeAgentCommandRejectsUnsupportedOutput(t *testing.T) {
	cmd := NewCommand(distribution.Distribution{
		Spec: coredistribution.Spec{Name: "sample"},
	})
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"describe", "agent", "assistant", "-o", "xml"})

	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), `unsupported output "xml"`) {
		t.Fatalf("Execute error = %v, want unsupported output", err)
	}
}
