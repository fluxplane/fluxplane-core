package coder

import (
	"context"

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
		ClientFactory: func(ctx context.Context, req codershell.ClientFactoryRequest) (codershell.ClientFactoryResult, error) {
			result, err := instance.ChannelClient(ctx, ChannelClientOptions{
				Path:           req.Path,
				WorkspaceRoots: req.WorkspaceRoots,
				EnvFiles:       req.EnvFiles,
				AuthPath:       req.AuthPath,
				Provider:       req.Provider,
				Model:          req.Model,
				Thinking:       req.Thinking,
				ThinkingSet:    req.ThinkingSet,
				Effort:         req.Effort,
				EffortSet:      req.EffortSet,
				Debug:          req.Debug,
				Yolo:           req.Yolo,
				Dev:            req.Dev,
				MaxToolRisk:    req.MaxToolRisk,
			})
			if err != nil {
				return codershell.ClientFactoryResult{}, err
			}
			return codershell.ClientFactoryResult{
				Client:   result.Client,
				Cleanup:  result.Cleanup,
				Commands: result.Commands,
			}, nil
		},
	})
}
