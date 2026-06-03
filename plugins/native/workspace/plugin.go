package workspace

import (
	"context"
	"fmt"
	"strings"

	"github.com/fluxplane/fluxplane-core/core/resource"
	"github.com/fluxplane/fluxplane-core/orchestration/pluginhost"
	corecontext "github.com/fluxplane/fluxplane-core/runtime/context"
	runtimeworkspace "github.com/fluxplane/fluxplane-core/runtime/workspace"
)

const (
	// Name identifies the workspace plugin.
	Name = "workspace"
	// SummaryProvider identifies the auto-context provider that renders workspace roots.
	SummaryProvider = "workspace.summary"
)

// Plugin contributes generic workspace context available to all local apps.
type Plugin struct {
	workspace runtimeworkspace.Workspace
}

var _ pluginhost.Plugin = Plugin{}
var _ pluginhost.ContextProviderContributor = Plugin{}

// New returns a workspace plugin bound to a runtime system.
func New(workspace runtimeworkspace.Workspace) Plugin {
	return Plugin{workspace: workspace}
}

// Manifest returns plugin metadata.
func (Plugin) Manifest() pluginhost.Manifest {
	return pluginhost.Manifest{Name: Name, Description: "Runtime workspace context."}
}

// Contributions returns workspace context provider specs.
func (Plugin) Contributions(context.Context, pluginhost.Context) (resource.ContributionBundle, error) {
	return resource.ContributionBundle{ContextProviders: []corecontext.ProviderSpec{summaryContextSpec()}}, nil
}

// ContextProviders returns workspace context providers.
func (p Plugin) ContextProviders(context.Context, pluginhost.Context) ([]corecontext.Provider, error) {
	if p.workspace == nil {
		return nil, nil
	}
	return []corecontext.Provider{summaryProvider(p)}, nil
}

func summaryContextSpec() corecontext.ProviderSpec {
	return corecontext.ProviderSpec{
		Name:             SummaryProvider,
		Description:      "Runtime workspace roots and path prefixes.",
		Kinds:            []corecontext.BlockKind{corecontext.BlockText},
		DefaultPlacement: corecontext.PlacementSystem,
		Annotations: map[string]string{
			corecontext.AnnotationAutoContext: "true",
		},
	}
}

type summaryProvider struct {
	workspace runtimeworkspace.Workspace
}

func (p summaryProvider) Spec() corecontext.ProviderSpec { return summaryContextSpec() }

func (p summaryProvider) Build(context.Context, corecontext.Request) ([]corecontext.Block, error) {
	if p.workspace == nil {
		return nil, nil
	}
	content := renderWorkspaceSummary(p.workspace)
	if strings.TrimSpace(content) == "" {
		return nil, nil
	}
	return []corecontext.Block{{
		ID:        SummaryProvider,
		Provider:  SummaryProvider,
		Kind:      corecontext.BlockText,
		Placement: corecontext.PlacementSystem,
		Title:     "Workspace Summary",
		Content:   content,
		MediaType: "text/plain",
		Freshness: corecontext.FreshnessDynamic,
	}}, nil
}

func renderWorkspaceSummary(workspace runtimeworkspace.Workspace) string {
	if workspace == nil {
		return ""
	}
	roots := workspace.Roots()
	if len(roots) == 0 {
		root := strings.TrimSpace(workspace.Root())
		if root == "" {
			return ""
		}
		roots = []runtimeworkspace.Root{{Path: root, Rel: ".", Read: true, Write: true}}
	}
	var lines []string
	lines = append(lines, "Workspace:")
	lines = append(lines, fmt.Sprintf("- primary root: %s", roots[0].Path))
	if len(roots) > 1 {
		lines = append(lines, "- additional roots:")
		for _, root := range roots[1:] {
			name := strings.TrimSpace(root.Name)
			if name == "" && strings.HasPrefix(root.Rel, "@") {
				name = strings.TrimPrefix(root.Rel, "@")
			}
			label := root.Rel
			if label == "" {
				label = "@" + name
			}
			access := "read-write"
			switch {
			case root.Read && !root.Write:
				access = "read-only"
			case !root.Read && root.Write:
				access = "write-only"
			case !root.Read && !root.Write:
				access = "no-access"
			}
			lines = append(lines, fmt.Sprintf("  - %s: %s (%s)", label, root.Path, access))
		}
	}
	lines = append(lines, "Use workspace-relative paths; named roots are addressed as @name/path.")
	return strings.Join(lines, "\n")
}
