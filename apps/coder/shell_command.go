package coder

import (
	"context"

	agentruntime "github.com/fluxplane/agentruntime"
	codershell "github.com/fluxplane/agentruntime/apps/coder/shell"
	"github.com/spf13/cobra"
)

func newShellCommandWithStartup(startup startupResources, defaults serveCommandOptions) *cobra.Command {
	instance := &Coder{
		startup: startup,
		config: Config{
			WorkspaceRoots: append([]string(nil), defaults.workspaceRoots...),
			EnvFiles:       append([]string(nil), defaults.envFiles...),
			Workspace:      cloneCoderServeWorkspace(defaults.workspace),
		},
	}
	return codershell.NewCommandWithOptions(codershell.CommandOptions{
		ClientFactory: func(ctx context.Context, path string) (agentruntime.ChannelClient, func(), error) {
			result, err := instance.ChannelClient(ctx, ChannelClientOptions{Path: path})
			if err != nil {
				return nil, nil, err
			}
			return result.Client, result.Cleanup, nil
		},
	})
}
