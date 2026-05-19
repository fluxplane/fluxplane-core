package agentsdk

import (
	"github.com/fluxplane/agentruntime/apps/launch"
	"github.com/spf13/cobra"
)

type InitOptions = launch.InitOptions

func newInitCommand() *cobra.Command {
	return launch.NewInitCommand()
}

func Init(path string, opts InitOptions) (string, error) {
	return launch.Init(path, opts)
}
