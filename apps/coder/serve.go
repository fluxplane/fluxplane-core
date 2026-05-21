package coder

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/fluxplane/agentruntime/adapters/distribution/run"
	distserve "github.com/fluxplane/agentruntime/adapters/distribution/serve"
	"github.com/fluxplane/agentruntime/apps/launch"
	coredistribution "github.com/fluxplane/agentruntime/core/distribution"
	"github.com/fluxplane/agentruntime/orchestration/distribution"
	"github.com/spf13/cobra"
)

type serveOptions struct {
	socket         string
	model          launch.ModelFlags
	runtime        launch.LocalRuntimeFlags
	workspaceRoots []string
	envFiles       []string
	allowAuthEnv   bool
	workspace      distribution.WorkspaceConfig
}

type serveCommandOptions struct {
	workspaceRoots []string
	envFiles       []string
	workspace      distribution.WorkspaceConfig
}

func newServeCommand(startup startupResources) *cobra.Command {
	return newServeCommandWithOptions(startup, serveCommandOptions{})
}

func newServeCommandWithOptions(startup startupResources, defaults serveCommandOptions) *cobra.Command {
	opts := serveOptions{socket: "auto"}
	opts.workspaceRoots = append([]string(nil), defaults.workspaceRoots...)
	opts.envFiles = append([]string(nil), defaults.envFiles...)
	opts.workspace = cloneCoderServeWorkspace(defaults.workspace)
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Serve coder over a local Unix socket",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := opts.runtime.Validate(); err != nil {
				return err
			}
			modelSelection := run.ResolveModelSelection("openai", opts.model.Model)
			roots, err := distribution.ParseWorkspaceRoots(opts.workspaceRoots)
			if err != nil {
				return err
			}
			addr := coderServeSocketPath(opts.socket)
			if err := validateCoderServeSocket(addr); err != nil {
				return err
			}
			launchConfig := coderServeLaunch(addr)
			launchConfig.Workspace = cloneCoderServeWorkspace(opts.workspace)
			launchConfig.Workspace.Roots = append(launchConfig.Workspace.Roots, roots...)
			launchConfig.Workspace.EnvFiles = append(launchConfig.Workspace.EnvFiles, trimCoderServeStrings(opts.envFiles)...)
			_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "coder serve listening on unix:%s\n", addr)
			_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "base_url: http://unix\n")
			_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "session: %s\n", SessionName)
			if err := launch.ServeDistribution(cmd.Context(), launch.ServeDistributionOptions{
				Root:           startup.Root,
				Spec:           coderServeSpec(startup),
				Bundles:        startup.Bundles,
				Launch:         launchConfig,
				PluginFactory:  localPluginsWithAuth,
				ToolProjection: mergeCoderToolProjection(ToolProjectionConfig(), opts.runtime.ToolProjectionMaxRisk()),
				ModelResolver: run.ModelResolver{
					Provider:        modelSelection.Provider,
					Model:           modelSelection.Model,
					DefaultProvider: "codex",
					DefaultModel:    DefaultModel,
					Debug:           opts.runtime.Debug,
				},
				AllowPrivateNetwork: true,
				Debug:               opts.runtime.Debug,
				Yolo:                opts.runtime.Yolo,
				Dev:                 opts.runtime.Dev,
				AllowPluginAuthEnv:  opts.allowAuthEnv,
			}); err != nil {
				return err
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&opts.socket, "socket", opts.socket, "Unix socket path, socket name, or auto")
	launch.BindLocalRuntimeFlags(cmd.Flags(), &opts.runtime, launch.LocalRuntimeFlagHelp{
		Debug: "print serve diagnostics",
		Yolo:  "auto-approve local operation risk gates for served coder sessions",
	})
	launch.BindModelFlags(cmd.Flags(), &opts.model, launch.ModelFlags{Model: DefaultModel})
	cmd.Flags().StringArrayVar(&opts.workspaceRoots, "workspace-root", opts.workspaceRoots, "additional workspace root as PATH or NAME=PATH; may be repeated")
	cmd.Flags().StringArrayVar(&opts.envFiles, "env-file", opts.envFiles, "root workspace env file or glob to load; may be repeated")
	cmd.Flags().BoolVar(&opts.allowAuthEnv, "allow-plugin-auth-env", false, "allow plugin auth methods to resolve credentials from the process environment")
	return cmd
}

func coderServeSpec(startup startupResources) coredistribution.Spec {
	spec := distributionFromStartup(startup).Spec
	return spec
}

func validateCoderServeSocket(addr string) error {
	if distserve.AddrIsTCP(addr) {
		return fmt.Errorf("serve: --socket must be a Unix socket path or name ending in .sock")
	}
	return nil
}

func coderServeLaunch(addr string) distribution.LaunchConfig {
	return distribution.LaunchConfig{
		Listeners: []distribution.Listener{{
			Name: "local",
			Type: "http",
			Addr: addr,
			Auth: map[string]any{"mode": "local_socket"},
		}},
		Channels: []distribution.Channel{{
			Name:     "local",
			Type:     "direct",
			Listener: "local",
			Session:  SessionName,
			Access:   distribution.Access{Mode: "open"},
		}},
	}
}

func coderServeSocketPath(value string) string {
	value = strings.TrimSpace(value)
	if value != "" && value != "auto" {
		return distserve.ResolveSocketPath(value)
	}
	base := os.Getenv("XDG_RUNTIME_DIR")
	if strings.TrimSpace(base) == "" {
		base = os.TempDir()
	}
	name := fmt.Sprintf("agentruntime-coder-%d.sock", os.Getuid())
	return filepath.Join(base, name)
}

func trimCoderServeStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	out := make([]string, 0, len(values))
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			out = append(out, value)
		}
	}
	return out
}

func cloneCoderServeWorkspace(cfg distribution.WorkspaceConfig) distribution.WorkspaceConfig {
	out := distribution.WorkspaceConfig{
		Roots:       cloneCoderServeWorkspaceRoots(cfg.Roots),
		ScratchRoot: strings.TrimSpace(cfg.ScratchRoot),
		EnvFiles:    append([]string(nil), cfg.EnvFiles...),
	}
	return out
}

func cloneCoderServeWorkspaceRoots(roots []distribution.WorkspaceRoot) []distribution.WorkspaceRoot {
	if len(roots) == 0 {
		return nil
	}
	out := make([]distribution.WorkspaceRoot, 0, len(roots))
	for _, root := range roots {
		root.EnvFiles = append([]string(nil), root.EnvFiles...)
		out = append(out, root)
	}
	return out
}
