package coding

import (
	"context"
	"fmt"
	"strings"

	corecontext "github.com/fluxplane/fluxplane-core/core/context"
	coredatasource "github.com/fluxplane/fluxplane-core/core/datasource"
	"github.com/fluxplane/fluxplane-core/core/operation"
	corereaction "github.com/fluxplane/fluxplane-core/core/reaction"
	"github.com/fluxplane/fluxplane-core/core/resource"
	"github.com/fluxplane/fluxplane-core/orchestration/pluginhost"
	"github.com/fluxplane/fluxplane-core/plugins/integrations/git"
	"github.com/fluxplane/fluxplane-core/plugins/integrations/web"
	"github.com/fluxplane/fluxplane-core/plugins/languages/golang"
	"github.com/fluxplane/fluxplane-core/plugins/languages/markdown"
	"github.com/fluxplane/fluxplane-core/plugins/native/browser"
	"github.com/fluxplane/fluxplane-core/plugins/native/code"
	"github.com/fluxplane/fluxplane-core/plugins/native/filesystem"
	"github.com/fluxplane/fluxplane-core/plugins/native/human"
	"github.com/fluxplane/fluxplane-core/plugins/native/project"
	"github.com/fluxplane/fluxplane-core/plugins/native/shell"
	runtimeevidence "github.com/fluxplane/fluxplane-core/runtime/evidence"
	"github.com/fluxplane/fluxplane-core/runtime/system"
	runtimeworkspace "github.com/fluxplane/fluxplane-core/runtime/workspace"
	fpsystem "github.com/fluxplane/fluxplane-system"
)

const Name = "coding"
const AgentsContextProvider = "agents.md"

// Plugin aggregates the standard day-to-day coding operation sets.
type Plugin struct {
	system  system.System
	plugins []pluginhost.Plugin
}

type Options struct {
	Browser *browser.Plugin
	Human   *human.Plugin
}

var _ pluginhost.Plugin = Plugin{}
var _ pluginhost.OperationContributor = Plugin{}
var _ pluginhost.ContextProviderContributor = Plugin{}
var _ pluginhost.DatasourceProviderContributor = Plugin{}
var _ pluginhost.ObserverContributor = Plugin{}
var _ pluginhost.AssertionDeriverContributor = Plugin{}
var _ pluginhost.ReactionContributor = Plugin{}

// New returns a standard coding plugin bundle.
func New(sys system.System) Plugin {
	return NewWithOptions(sys, Options{})
}

func NewWithOptions(sys system.System, opts Options) Plugin {
	workspace := systemWorkspace(sys)
	browserPlugin := browser.New(browser.Config{})
	if opts.Browser != nil {
		browserPlugin = *opts.Browser
	}
	humanPlugin := human.NewWithSystem(sys, nil)
	if opts.Human != nil {
		humanPlugin = *opts.Human
	}
	return Plugin{system: sys, plugins: []pluginhost.Plugin{
		project.New(sys),
		filesystem.New(workspace),
		golang.New(sys),
		markdown.New(workspace),
		web.New(sys),
		browserPlugin,
		git.New(sys),
		shell.New(shellConfig(sys)),
		code.New(sys),
		humanPlugin,
	}}
}

func shellConfig(sys system.System) shell.Config {
	if sys == nil {
		return shell.Config{}
	}
	return shell.Config{Process: sys.Process(), Environment: sys.Environment()}
}

func systemWorkspace(sys system.System) runtimeworkspace.Workspace {
	if sys == nil {
		return nil
	}
	return sys.Workspace()
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

func (p Plugin) ContextProviders(ctx context.Context, pluginCtx pluginhost.Context) ([]corecontext.Provider, error) {
	if p.system == nil || p.system.Workspace() == nil {
		return nil, nil
	}
	out := []corecontext.Provider{agentsContextProvider{workspace: p.system.Workspace()}}
	seen := map[corecontext.ProviderName]bool{AgentsContextProvider: true}
	for _, plugin := range p.plugins {
		contributor, ok := plugin.(pluginhost.ContextProviderContributor)
		if !ok {
			continue
		}
		providers, err := contributor.ContextProviders(ctx, pluginCtx)
		if err != nil {
			return nil, fmt.Errorf("codingplugin: %s context providers: %w", plugin.Manifest().Name, err)
		}
		for _, provider := range providers {
			if provider == nil {
				continue
			}
			name := provider.Spec().Name
			if seen[name] {
				continue
			}
			seen[name] = true
			out = append(out, provider)
		}
	}
	return out, nil
}

func (p Plugin) DatasourceProviders(ctx context.Context, pluginCtx pluginhost.Context) ([]coredatasource.Provider, error) {
	var out []coredatasource.Provider
	for _, plugin := range p.plugins {
		contributor, ok := plugin.(pluginhost.DatasourceProviderContributor)
		if !ok {
			continue
		}
		providers, err := contributor.DatasourceProviders(ctx, pluginCtx)
		if err != nil {
			return nil, fmt.Errorf("codingplugin: %s datasource providers: %w", plugin.Manifest().Name, err)
		}
		out = append(out, providers...)
	}
	return out, nil
}

func (p Plugin) EnvironmentObservers(ctx context.Context, pluginCtx pluginhost.Context) ([]runtimeevidence.Observer, error) {
	var out []runtimeevidence.Observer
	for _, plugin := range p.plugins {
		contributor, ok := plugin.(pluginhost.ObserverContributor)
		if !ok {
			continue
		}
		observers, err := contributor.EnvironmentObservers(ctx, pluginCtx)
		if err != nil {
			return nil, fmt.Errorf("codingplugin: %s environment observers: %w", plugin.Manifest().Name, err)
		}
		out = append(out, observers...)
	}
	return out, nil
}

func (p Plugin) AssertionDerivers(ctx context.Context, pluginCtx pluginhost.Context) ([]runtimeevidence.AssertionDeriver, error) {
	var out []runtimeevidence.AssertionDeriver
	for _, plugin := range p.plugins {
		contributor, ok := plugin.(pluginhost.AssertionDeriverContributor)
		if !ok {
			continue
		}
		derivers, err := contributor.AssertionDerivers(ctx, pluginCtx)
		if err != nil {
			return nil, fmt.Errorf("codingplugin: %s assertion derivers: %w", plugin.Manifest().Name, err)
		}
		out = append(out, derivers...)
	}
	return out, nil
}

func (p Plugin) Reactions(ctx context.Context, pluginCtx pluginhost.Context) ([]corereaction.Rule, error) {
	var out []corereaction.Rule
	for _, plugin := range p.plugins {
		contributor, ok := plugin.(pluginhost.ReactionContributor)
		if !ok {
			continue
		}
		rules, err := contributor.Reactions(ctx, pluginCtx)
		if err != nil {
			return nil, fmt.Errorf("codingplugin: %s reactions: %w", plugin.Manifest().Name, err)
		}
		out = append(out, rules...)
	}
	return out, nil
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
	workspace runtimeworkspace.Workspace
}

func (p agentsContextProvider) Spec() corecontext.ProviderSpec { return agentsContextSpec() }

func (p agentsContextProvider) Build(ctx context.Context, _ corecontext.Request) ([]corecontext.Block, error) {
	if p.workspace == nil {
		return nil, nil
	}
	resolved, err := p.workspace.ResolveExisting(ctx, "AGENTS.md")
	if err != nil {
		return nil, nil
	}
	fsys, err := runtimeworkspace.FileSystem(p.workspace)
	if err != nil {
		return nil, nil
	}
	data, truncated, err := fpsystem.ReadFileLimit(ctx, fsys, runtimeworkspace.PathName(resolved), 64*1024)
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
