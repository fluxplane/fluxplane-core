package evaluator

import (
	"github.com/fluxplane/engine/orchestration/pluginhost"
	"github.com/fluxplane/engine/runtime/system"
)

func evaluatorPlugins(system.System) []pluginhost.Plugin {
	return []pluginhost.Plugin{
		NewPlugin(),
	}
}
