// Package agentsdk assembles the first-party agentsdk CLI product.
package agentsdk

import (
	"context"

	"github.com/fluxplane/agentruntime/adapters/distribution/authconnect"
	distremote "github.com/fluxplane/agentruntime/adapters/distribution/remote"
	"github.com/fluxplane/agentruntime/apps/coder"
	"github.com/fluxplane/agentruntime/apps/evaluator"
	"github.com/fluxplane/agentruntime/apps/launch"
	"github.com/fluxplane/agentruntime/orchestration/pluginhost"
	"github.com/fluxplane/agentruntime/plugins/openaiplugin"
	"github.com/spf13/cobra"
)

const (
	defaultRemoteSession      = "slack-main"
	defaultRemoteConversation = "agentsdk-remote"
	defaultRemoteSocket       = "agentsdk-local.sock"
)

func NewCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:           "agentsdk",
		Short:         "Run agentsdk tools",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	cmd.AddCommand(coder.NewCommand())
	cmd.AddCommand(evaluator.NewCommand())
	cmd.AddCommand(newInitCommand())
	cmd.AddCommand(newBuildCommand())
	cmd.AddCommand(launch.NewRunCommand())
	cmd.AddCommand(launch.NewServeCommand())
	cmd.AddCommand(newModelsCommand())
	cmd.AddCommand(distremote.NewCommand(distremote.CommandOptions{
		DefaultSession:      defaultRemoteSession,
		DefaultConversation: defaultRemoteConversation,
		DefaultSocket:       defaultRemoteSocket,
		Events:              launch.MustTerminalEventRegistry(),
	}))
	cmd.AddCommand(authconnect.NewCommand(authconnect.CommandOptions{
		NativeRegistry:    launch.AuthPluginRegistry,
		ConnectorRegistry: connectorPluginRegistry,
	}))
	cmd.AddCommand(launch.NewDatasourceCommand())
	cmd.AddCommand(newDiscoverCommand())
	return cmd
}

func connectorPluginRegistry(context.Context) ([]pluginhost.Plugin, error) {
	return []pluginhost.Plugin{
		openaiplugin.New(),
	}, nil
}
