package workspaceplugin

import (
	"context"
	"strings"
	"testing"

	corecontext "github.com/fluxplane/agentruntime/core/context"
	"github.com/fluxplane/agentruntime/runtime/system"
)

func TestSummaryProviderRendersWorkspaceRoots(t *testing.T) {
	root := t.TempDir()
	docs := t.TempDir()
	sys, err := system.NewHost(system.Config{
		Root: root,
		Workspace: system.WorkspaceConfig{Roots: []system.WorkspaceRootConfig{{
			Name:   "docs",
			Path:   docs,
			Access: system.WorkspaceAccessReadOnly,
		}}},
	})
	if err != nil {
		t.Fatalf("NewHost: %v", err)
	}

	blocks, err := summaryProvider{workspace: sys.Workspace()}.Build(context.Background(), corecontext.Request{})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(blocks) != 1 {
		t.Fatalf("blocks len = %d, want 1", len(blocks))
	}
	content := blocks[0].Content
	for _, want := range []string{
		"Workspace:",
		"primary root: " + root,
		"@docs: " + docs + " (read-only)",
		"named roots are addressed as @name/path",
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("content = %q, want %q", content, want)
		}
	}
}

func TestSummarySpecIsAutoContext(t *testing.T) {
	spec := summaryContextSpec()
	if spec.Name != SummaryProvider {
		t.Fatalf("provider name = %q, want %q", spec.Name, SummaryProvider)
	}
	if spec.Annotations[corecontext.AnnotationAutoContext] != "true" {
		t.Fatalf("annotations = %#v, want auto context", spec.Annotations)
	}
}
