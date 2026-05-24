package evaluator

import (
	"github.com/fluxplane/fluxplane-core/orchestration/pluginhost"
	"github.com/fluxplane/fluxplane-core/runtime/system"
)

func evaluatorPlugins(system.System) []pluginhost.Plugin {
	return []pluginhost.Plugin{
		NewPlugin(),
	}
}
