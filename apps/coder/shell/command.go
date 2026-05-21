package codershell

import (
	"context"
	"fmt"
	"strings"

	fluxplane "github.com/fluxplane/engine"
	"github.com/fluxplane/engine/apps/launch"
	"github.com/fluxplane/engine/core/command"
	"github.com/fluxplane/engine/core/operation"
	"github.com/spf13/cobra"
)

// CommandOptions configures the shell command.
type CommandOptions struct {
	ClientFactory ClientFactoryFunc
}

// ClientFactoryRequest describes one local direct shell client request.
type ClientFactoryRequest struct {
	Path               string
	WorkspaceRoots     []string
	EnvFiles           []string
	AuthPath           string
	AllowPluginAuthEnv bool
	Provider           string
	Model              string
	Thinking           string
	ThinkingSet        bool
	Effort             string
	EffortSet          bool
	Debug              bool
	Yolo               bool
	Dev                bool
	MaxToolRisk        operation.RiskLevel
}

// ClientFactoryResult carries the local direct channel client and static
// completion metadata resolved during launch.
type ClientFactoryResult struct {
	Client   fluxplane.ChannelClient
	Cleanup  func()
	Commands []command.Spec
}

// ClientFactoryFunc resolves the local direct channel client used when
// --connect is not set.
type ClientFactoryFunc func(context.Context, ClientFactoryRequest) (ClientFactoryResult, error)

// NewCommand returns the standalone coder shell command. It is reusable by the
// cmd/codershell binary and by the main coder CLI as an injected subcommand.
func NewCommand() *cobra.Command {
	return NewCommandWithOptions(CommandOptions{})
}

func NewCommandWithOptions(commandOpts CommandOptions) *cobra.Command {
	var opts Options
	modelFlags := launch.ModelFlags{Thinking: "auto"}
	runtimeFlags := launch.LocalRuntimeFlags{}
	environmentFlags := launch.LaunchEnvironmentFlags{}
	var workspaceRoots []string
	cmd := &cobra.Command{
		Use:   "shell [path]",
		Short: "Start the experimental coder shell TUI",
		Long: strings.TrimSpace(`Start an interactive coder shell for a workspace.

The shell opens in ask mode by default. Type a question and press Enter to ask
the agent, type ! at the start of an empty prompt to switch to shell mode, use
/ for coder commands, and use @ to mention workspace resources.`),
		Example: strings.TrimSpace(`  coder shell
  coder shell ~/src/project --provider codex --model gpt-5.5
  coder shell --connect=fake`),
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			path := "."
			if len(args) > 0 && strings.TrimSpace(args[0]) != "" {
				path = args[0]
			}
			if strings.TrimSpace(opts.Connect) == "" {
				opts.Connect = "direct"
			}
			modelFlags.CaptureChanged(cmd.Flags())
			if err := modelFlags.Validate(); err != nil {
				return err
			}
			if err := runtimeFlags.Validate(); err != nil {
				return err
			}
			opts.Provider = modelFlags.Provider
			opts.Model = modelFlags.Model
			if strings.TrimSpace(opts.Connect) != "direct" {
				if flag := changedLocalOnlyShellFlag(cmd.Flags().Changed); flag != "" {
					return fmt.Errorf("coder shell: --%s is only supported with --connect=direct", flag)
				}
			}
			var cleanup func()
			if commandOpts.ClientFactory != nil && strings.TrimSpace(opts.Connect) == "direct" {
				result, err := commandOpts.ClientFactory(cmd.Context(), ClientFactoryRequest{
					Path:               path,
					WorkspaceRoots:     append([]string(nil), workspaceRoots...),
					EnvFiles:           append([]string(nil), environmentFlags.EnvFiles...),
					AuthPath:           environmentFlags.AuthPath,
					AllowPluginAuthEnv: environmentFlags.AllowPluginAuthEnv,
					Provider:           modelFlags.Provider,
					Model:              modelFlags.Model,
					Thinking:           modelFlags.Thinking,
					ThinkingSet:        modelFlags.ThinkingSet,
					Effort:             modelFlags.Effort,
					EffortSet:          modelFlags.EffortSet,
					Debug:              runtimeFlags.Debug,
					Yolo:               runtimeFlags.Yolo,
					Dev:                runtimeFlags.Dev,
					MaxToolRisk:        runtimeFlags.ToolProjectionMaxRisk(),
				})
				if err != nil {
					return err
				}
				opts.DirectClient = result.Client
				opts.CommandSpecs = append([]command.Spec(nil), result.Commands...)
				cleanup = result.Cleanup
			}
			if cleanup != nil {
				defer cleanup()
			}
			opts.Path = path
			opts.In = cmd.InOrStdin()
			opts.Out = cmd.OutOrStdout()
			return Run(opts)
		},
	}
	cmd.Flags().StringVar(&opts.Connect, "connect", "direct", "shell endpoint: fake, direct, unix://PATH, http(s)://URL, or target URL")
	launch.BindModelFlags(cmd.Flags(), &modelFlags, modelFlags)
	launch.BindLocalRuntimeFlags(cmd.Flags(), &runtimeFlags, launch.LocalRuntimeFlagHelp{
		Debug: "print shell runtime diagnostics",
		Yolo:  "auto-approve local operation risk gates for this shell",
	})
	launch.BindLaunchEnvironmentFlags(cmd.Flags(), &environmentFlags)
	cmd.Flags().StringArrayVar(&workspaceRoots, "workspace-root", nil, "additional workspace root as PATH or NAME=PATH; may be repeated")
	return cmd
}

func changedLocalOnlyShellFlag(changed func(string) bool) string {
	if changed == nil {
		return ""
	}
	for _, name := range []string{
		"provider",
		"model",
		"thinking",
		"effort",
		"debug",
		"yolo",
		"dev",
		"connectors-path",
		"allow-plugin-auth-env",
		"env-file",
		"workspace-root",
	} {
		if changed(name) {
			return name
		}
	}
	return ""
}
