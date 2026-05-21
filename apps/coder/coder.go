package coder

import (
	"context"
	"strings"

	agentruntime "github.com/fluxplane/agentruntime"
	"github.com/fluxplane/agentruntime/adapters/distribution/run"
	"github.com/fluxplane/agentruntime/apps/launch"
	"github.com/fluxplane/agentruntime/core/command"
	"github.com/fluxplane/agentruntime/core/operation"
	"github.com/fluxplane/agentruntime/core/resource"
	clientapi "github.com/fluxplane/agentruntime/orchestration/client"
	"github.com/fluxplane/agentruntime/orchestration/distribution"
	"github.com/fluxplane/agentruntime/orchestration/session"
)

// Config configures a reusable coder product instance.
type Config struct {
	WorkspaceRoots     []string
	EnvFiles           []string
	Workspace          distribution.WorkspaceConfig
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
	Bundles            []resource.ContributionBundle
}

// Coder is the reusable coder product assembly. Presentation layers such as
// the classic CLI and shell TUI use this instead of rebuilding coder resources.
type Coder struct {
	startup startupResources
	config  Config
}

// ChannelClientResult is an opened local coder channel plus cleanup.
type ChannelClientResult struct {
	Client   agentruntime.ChannelClient
	Cleanup  func()
	Commands []command.Spec
}

// ChannelClientOptions configures a local in-process shell/channel client.
type ChannelClientOptions struct {
	Path               string
	WorkspaceRoots     []string
	EnvFiles           []string
	Workspace          distribution.WorkspaceConfig
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

// NewCoder creates a reusable coder product instance.
func NewCoder(ctx context.Context, cfg Config) (*Coder, error) {
	startup := loadStartupResources(ctx)
	startup.Bundles = append(startup.Bundles, cloneContributionBundles(cfg.Bundles)...)
	return &Coder{
		startup: startup,
		config:  cloneCoderConfig(cfg),
	}, nil
}

// Distribution returns the runnable/deployable coder distribution.
func (c *Coder) Distribution() distribution.Distribution {
	if c == nil {
		return distributionFromStartup(loadStartupResources(context.Background()))
	}
	return distributionFromStartup(c.startup)
}

// OpenSession opens a session against the coder distribution.
func (c *Coder) OpenSession(ctx context.Context, req distribution.OpenRequest) (clientapi.SessionHandle, error) {
	if c != nil {
		workspace, err := c.channelWorkspace(ChannelClientOptions{Workspace: req.Launch.Workspace})
		if err != nil {
			return nil, err
		}
		req.Launch.Workspace = workspace
	}
	return c.Distribution().Runtime.OpenSession(ctx, req)
}

// CommandOptions returns the legacy command construction options for this coder.
func (c *Coder) CommandOptions() CommandOptions {
	if c == nil {
		return CommandOptions{}
	}
	return CommandOptions{
		WorkspaceRoots: append([]string(nil), c.config.WorkspaceRoots...),
		EnvFiles:       append([]string(nil), c.config.EnvFiles...),
		Workspace:      cloneCoderServeWorkspace(c.config.Workspace),
		Bundles:        cloneContributionBundles(c.config.Bundles),
	}
}

// ChannelClient opens an in-process coder runtime and returns its direct channel
// client. No HTTP listener or socket is started for this local foreground mode.
func (c *Coder) ChannelClient(ctx context.Context, opts ChannelClientOptions) (ChannelClientResult, error) {
	if c == nil {
		var err error
		c, err = NewCoder(ctx, Config{})
		if err != nil {
			return ChannelClientResult{}, err
		}
	}
	workspace, err := c.channelWorkspace(opts)
	if err != nil {
		return ChannelClientResult{}, err
	}
	model := firstNonEmpty(opts.Model, c.config.Model, DefaultModel)
	provider := firstNonEmpty(opts.Provider, c.config.Provider, "openai")
	modelSelection := run.ResolveModelSelection(provider, model)
	runtime, err := launch.Launch(ctx, launch.RuntimeOptions{
		Root:           c.startup.Root,
		Spec:           coderServeSpec(c.startup),
		Bundles:        c.startup.Bundles,
		Launch:         distribution.LaunchConfig{Workspace: workspace},
		PluginFactory:  localPluginsWithAuth,
		ToolProjection: mergeCoderToolProjection(ToolProjectionConfig(), firstRisk(opts.MaxToolRisk, c.config.MaxToolRisk)),
		ModelResolver: run.ModelResolver{
			Provider:        modelSelection.Provider,
			Model:           modelSelection.Model,
			Thinking:        firstNonEmpty(opts.Thinking, c.config.Thinking),
			ThinkingSet:     opts.ThinkingSet || c.config.ThinkingSet,
			Effort:          firstNonEmpty(opts.Effort, c.config.Effort),
			EffortSet:       opts.EffortSet || c.config.EffortSet,
			DefaultProvider: "codex",
			DefaultModel:    DefaultModel,
			Debug:           opts.Debug || c.config.Debug,
		},
		AllowPrivateNetwork: true,
		Debug:               opts.Debug || c.config.Debug,
		Yolo:                opts.Yolo || c.config.Yolo,
		Dev:                 opts.Dev || c.config.Dev,
		AuthPath:            firstNonEmpty(opts.AuthPath, c.config.AuthPath),
		AllowPluginAuthEnv:  opts.AllowPluginAuthEnv || c.config.AllowPluginAuthEnv,
	})
	if err != nil {
		return ChannelClientResult{}, err
	}
	return ChannelClientResult{
		Client:   runtime.Service,
		Cleanup:  runtime.Close,
		Commands: session.AvailableCommandSpecs(runtime.Composition.Commands, runtime.Composition.CommandCatalog),
	}, nil
}

func (c *Coder) channelWorkspace(opts ChannelClientOptions) (distribution.WorkspaceConfig, error) {
	workspace := cloneCoderServeWorkspace(c.config.Workspace)
	workspace = mergeCoderWorkspace(workspace, opts.Workspace)
	roots, err := distribution.ParseWorkspaceRoots(append(append([]string(nil), c.config.WorkspaceRoots...), opts.WorkspaceRoots...))
	if err != nil {
		return distribution.WorkspaceConfig{}, err
	}
	workspace.Roots = append(workspace.Roots, roots...)
	path := strings.TrimSpace(opts.Path)
	if path != "" {
		workspace.Roots = append(workspace.Roots, distribution.WorkspaceRoot{Name: "root", Path: path, Access: "read_write"})
	}
	workspace.EnvFiles = append(workspace.EnvFiles, trimCoderServeStrings(c.config.EnvFiles)...)
	workspace.EnvFiles = append(workspace.EnvFiles, trimCoderServeStrings(opts.EnvFiles)...)
	return workspace, nil
}

func cloneCoderConfig(cfg Config) Config {
	cfg.WorkspaceRoots = append([]string(nil), cfg.WorkspaceRoots...)
	cfg.EnvFiles = append([]string(nil), cfg.EnvFiles...)
	cfg.Workspace = cloneCoderServeWorkspace(cfg.Workspace)
	cfg.Bundles = cloneContributionBundles(cfg.Bundles)
	return cfg
}

func mergeCoderWorkspace(base, override distribution.WorkspaceConfig) distribution.WorkspaceConfig {
	out := cloneCoderServeWorkspace(base)
	out.Roots = append(out.Roots, cloneCoderServeWorkspaceRoots(override.Roots)...)
	out.EnvFiles = append(out.EnvFiles, override.EnvFiles...)
	if strings.TrimSpace(override.ScratchRoot) != "" {
		out.ScratchRoot = strings.TrimSpace(override.ScratchRoot)
	}
	return out
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func firstRisk(values ...operation.RiskLevel) operation.RiskLevel {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
