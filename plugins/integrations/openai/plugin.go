package openai

import (
	"context"

	"github.com/fluxplane/engine/core/resource"
	"github.com/fluxplane/engine/orchestration/pluginhost"
)

const Name = "openai"

type Plugin struct{}

var _ pluginhost.Plugin = Plugin{}

func New() Plugin { return Plugin{} }

func (Plugin) Manifest() pluginhost.Manifest {
	return pluginhost.Manifest{Name: Name, Description: "OpenAI integration plugin."}
}

func (Plugin) Contributions(context.Context, pluginhost.Context) (resource.ContributionBundle, error) {
	return resource.ContributionBundle{}, nil
}
