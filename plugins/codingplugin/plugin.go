package codingplugin

import (
	"context"
	"fmt"

	"github.com/fluxplane/agentruntime/core/operation"
	"github.com/fluxplane/agentruntime/core/resource"
	"github.com/fluxplane/agentruntime/orchestration/pluginhost"
	"github.com/fluxplane/agentruntime/plugins/browserplugin"
	"github.com/fluxplane/agentruntime/plugins/codeplugin"
	"github.com/fluxplane/agentruntime/plugins/filesystemplugin"
	"github.com/fluxplane/agentruntime/plugins/gitplugin"
	"github.com/fluxplane/agentruntime/plugins/humanplugin"
	"github.com/fluxplane/agentruntime/plugins/shellplugin"
	"github.com/fluxplane/agentruntime/plugins/webplugin"
	"github.com/fluxplane/agentruntime/runtime/system"
)

const Name = "coding"

// Plugin aggregates the standard day-to-day coding operation sets.
type Plugin struct {
	plugins []pluginhost.Plugin
}

var _ pluginhost.Plugin = Plugin{}
var _ pluginhost.OperationContributor = Plugin{}

// New returns a standard coding plugin bundle.
func New(sys system.System) Plugin {
	return Plugin{plugins: []pluginhost.Plugin{
		filesystemplugin.New(sys),
		webplugin.New(sys),
		browserplugin.New(sys),
		gitplugin.New(sys),
		shellplugin.New(sys),
		codeplugin.New(sys),
		humanplugin.New(sys.Clarifier()),
	}}
}

// Manifest returns plugin metadata.
func (Plugin) Manifest() pluginhost.Manifest {
	return pluginhost.Manifest{Name: Name, Description: "Standard coding operation sets."}
}

// Contributions returns aggregate contributions.
func (p Plugin) Contributions(ctx context.Context, pluginCtx pluginhost.Context) (resource.ContributionBundle, error) {
	var out resource.ContributionBundle
	for _, plugin := range p.plugins {
		bundle, err := plugin.Contributions(ctx, pluginCtx)
		if err != nil {
			return resource.ContributionBundle{}, fmt.Errorf("codingplugin: %s contributions: %w", plugin.Manifest().Name, err)
		}
		out.Append(bundle)
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
