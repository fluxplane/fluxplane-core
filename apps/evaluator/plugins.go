package evaluator

import (
	"github.com/fluxplane/agentruntime/orchestration/pluginhost"
	"github.com/fluxplane/agentruntime/runtime/system"
)

func evaluatorPlugins(system.System) []pluginhost.Plugin {
	return []pluginhost.Plugin{
		NewPlugin(),
	}
}
