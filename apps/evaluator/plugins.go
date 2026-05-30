package evaluator

import (
	"github.com/fluxplane/fluxplane-core/apps/launch"
	"github.com/fluxplane/fluxplane-core/orchestration/pluginhost"
)

func evaluatorPlugins(launch.PluginFactoryContext) []pluginhost.Plugin {
	return []pluginhost.Plugin{
		NewPlugin(),
	}
}
