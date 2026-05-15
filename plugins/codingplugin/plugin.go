package codingplugin

import (
	"context"
	"fmt"
	"strings"

	corecontext "github.com/fluxplane/agentruntime/core/context"
	"github.com/fluxplane/agentruntime/core/operation"
	"github.com/fluxplane/agentruntime/core/resource"
	"github.com/fluxplane/agentruntime/orchestration/pluginhost"
	"github.com/fluxplane/agentruntime/plugins/browserplugin"
	"github.com/fluxplane/agentruntime/plugins/codeplugin"
	"github.com/fluxplane/agentruntime/plugins/filesystemplugin"
	"github.com/fluxplane/agentruntime/plugins/gitplugin"
	"github.com/fluxplane/agentruntime/plugins/golangplugin"
	"github.com/fluxplane/agentruntime/plugins/humanplugin"
	"github.com/fluxplane/agentruntime/plugins/projectplugin"
	"github.com/fluxplane/agentruntime/plugins/shellplugin"
	"github.com/fluxplane/agentruntime/plugins/webplugin"
	"github.com/fluxplane/agentruntime/runtime/system"
)

const Name = "coding"
const AgentsContextProvider = "agents.md"

// Plugin aggregates the standard day-to-day coding operation sets.
type Plugin struct {
	system  system.System
	plugins []pluginhost.Plugin
}

var _ pluginhost.Plugin = Plugin{}
var _ pluginhost.OperationContributor = Plugin{}
var _ pluginhost.ContextProviderContributor = Plugin{}

// New returns a standard coding plugin bundle.
func New(sys system.System) Plugin {
	var clarifier system.Clarifier
	if sys != nil {
		clarifier = sys.Clarifier()
	}
	return Plugin{system: sys, plugins: []pluginhost.Plugin{
		projectplugin.New(sys),
		filesystemplugin.New(sys),
		golangplugin.New(sys),
		webplugin.New(sys),
		browserplugin.New(sys),
		gitplugin.New(sys),
		shellplugin.New(sys),
		codeplugin.New(sys),
		humanplugin.New(clarifier),
	}}
}

// Manifest returns plugin metadata.
func (Plugin) Manifest() pluginhost.Manifest {
	return pluginhost.Manifest{Name: Name, Description: "Standard coding operation sets."}
}

// Contributions returns aggregate contributions.
func (p Plugin) Contributions(ctx context.Context, pluginCtx pluginhost.Context) (resource.ContributionBundle, error) {
	var out resource.ContributionBundle
	out.ContextProviders = append(out.ContextProviders, agentsContextSpec())
	for _, plugin := range p.plugins {
		bundle, err := plugin.Contributions(ctx, pluginCtx)
		if err != nil {
			return resource.ContributionBundle{}, fmt.Errorf("codingplugin: %s contributions: %w", plugin.Manifest().Name, err)
		}
		out.Append(bundle)
	}
	return out, nil
}

func (p Plugin) ContextProviders(context.Context, pluginhost.Context) ([]corecontext.Provider, error) {
	if p.system == nil || p.system.Workspace() == nil {
		return nil, nil
	}
	return []corecontext.Provider{agentsContextProvider{workspace: p.system.Workspace()}}, nil
}

// Operations returns aggregate operation implementations.
func (p Plugin) Operations(ctx context.Context, pluginCtx pluginhost.Context) ([]operation.Operation, error) {
	var out []operation.Operation
	for _, plugin := range p.plugins {
		contributor, ok := plugin.(pluginhost.OperationContributor)
		if !ok {
			continue
		}
		ops, err := contributor.Operations(ctx, pluginCtx)
		if err != nil {
			return nil, fmt.Errorf("codingplugin: %s operations: %w", plugin.Manifest().Name, err)
		}
		out = append(out, ops...)
	}
	return out, nil
}

func agentsContextSpec() corecontext.ProviderSpec {
	return corecontext.ProviderSpec{
		Name:             AgentsContextProvider,
		Description:      "Project AGENTS.md instructions.",
		Kinds:            []corecontext.BlockKind{corecontext.BlockText},
		DefaultPlacement: corecontext.PlacementSystem,
	}
}

type agentsContextProvider struct {
	workspace system.Workspace
}

func (p agentsContextProvider) Spec() corecontext.ProviderSpec { return agentsContextSpec() }

func (p agentsContextProvider) Build(ctx context.Context, _ corecontext.Request) ([]corecontext.Block, error) {
	if p.workspace == nil {
		return nil, nil
	}
	data, truncated, resolved, err := p.workspace.ReadFile(ctx, "AGENTS.md", 64*1024)
	if err != nil {
		return nil, nil
	}
	content := strings.TrimSpace(string(data))
	if content == "" {
		return nil, nil
	}
	metadata := map[string]string{"path": resolved.Rel}
	if truncated {
		metadata["truncated"] = "true"
	}
	return []corecontext.Block{{
		ID:        "agents.md/root",
		Provider:  AgentsContextProvider,
		Kind:      corecontext.BlockText,
		Placement: corecontext.PlacementSystem,
		Title:     "AGENTS.md",
		Content:   content,
		URI:       resolved.Rel,
		MediaType: "text/markdown",
		Freshness: corecontext.FreshnessStatic,
		Metadata:  metadata,
	}}, nil
}
