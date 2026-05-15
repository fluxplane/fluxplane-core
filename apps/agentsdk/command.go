// Package agentsdk assembles the first-party agentsdk CLI product.
package agentsdk

import (
	"context"
	"fmt"

	connectcli "github.com/fluxplane/agentruntime/adapters/connectors/cli"
	distremote "github.com/fluxplane/agentruntime/adapters/distribution/remote"
	"github.com/fluxplane/agentruntime/apps/coder"
	"github.com/fluxplane/agentruntime/apps/evaluator"
	"github.com/fluxplane/agentruntime/apps/launch"
	coreevent "github.com/fluxplane/agentruntime/core/event"
	"github.com/fluxplane/agentruntime/orchestration/eventregistry"
	"github.com/fluxplane/agentruntime/orchestration/pluginhost"
	"github.com/fluxplane/agentruntime/plugins/eventcatalog"
	"github.com/fluxplane/agentruntime/plugins/gitlabplugin"
	"github.com/fluxplane/agentruntime/plugins/jiraplugin"
	"github.com/fluxplane/agentruntime/plugins/openaiplugin"
	"github.com/fluxplane/agentruntime/plugins/slackplugin"
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
		Events:              mustTerminalEventRegistry(),
	}))
	cmd.AddCommand(connectcli.NewCommand(connectorPluginRegistry))
	cmd.AddCommand(newDatasourceCommand())
	cmd.AddCommand(newDiscoverCommand())
	return cmd
}

func connectorPluginRegistry(context.Context) ([]pluginhost.Plugin, error) {
	return []pluginhost.Plugin{
		openaiplugin.New(),
		slackplugin.New(nil),
		gitlabplugin.New(nil, nil),
		jiraplugin.New(nil, nil),
	}, nil
}

func mustTerminalEventRegistry() *coreevent.Registry {
	registry, err := terminalEventRegistry()
	if err != nil {
		panic(fmt.Sprintf("agentsdk: build terminal event registry: %v", err))
	}
	return registry
}

func terminalEventRegistry() (*coreevent.Registry, error) {
	registry, err := eventregistry.New(eventregistry.Config{EventTypes: eventcatalog.All()})
	if err != nil {
		return nil, err
	}
	return registry, nil
}
