package evaluator

import (
	"github.com/fluxplane/fluxplane-core/apps/launch"
	"github.com/fluxplane/fluxplane-core/orchestration/contributions"
)

func evaluatorPlugins(launch.PluginFactoryContext) []contributions.Provider {
	return []contributions.Provider{
		NewPlugin(),
	}
}
