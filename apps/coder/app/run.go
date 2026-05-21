package coderapp

import (
	"context"
	"io"
	"strings"

	distlocal "github.com/fluxplane/engine/adapters/distribution/local"
	"github.com/fluxplane/engine/apps/launch"
	"github.com/fluxplane/engine/orchestration/distribution"
)

// RunOptions configures a programmatic app run through the coder product.
type RunOptions struct {
	Path                string
	Session             string
	Conversation        string
	Provider            string
	Model               string
	Thinking            string
	ThinkingSet         bool
	Effort              string
	EffortSet           bool
	Input               string
	Goal                string
	GoalSet             bool
	MaxContinuations    int
	MaxContinuationsSet bool
	Debug               bool
	Usage               bool
	Yolo                bool
	Dev                 bool
	MaxToolRisk         string
	AuthPath            string
	AllowPluginAuthEnv  bool
	WorkspaceRoots      []string
	EnvFiles            []string
	Loader              launch.Loader
	In                  io.Reader
	Out                 io.Writer
	Err                 io.Writer
}

// Run runs the selected Fluxplane app facet with coder configuration
// defaults applied.
func (a *App) Run(ctx context.Context, opts RunOptions) error {
	if a == nil {
		a = &App{}
	}
	path := strings.TrimSpace(opts.Path)
	if path == "" {
		path = "."
	}
	workspace, err := mergedRunWorkspace(a.config.Workspace, opts.WorkspaceRoots, opts.EnvFiles)
	if err != nil {
		return err
	}
	return launch.RunPathWithLoader(ctx, a.loaderWithCoderConfig(opts.Loader), path, launch.RunPathOptions{
		Session:             opts.Session,
		Conversation:        opts.Conversation,
		Provider:            opts.Provider,
		Model:               opts.Model,
		Thinking:            opts.Thinking,
		ThinkingSet:         opts.ThinkingSet,
		Effort:              opts.Effort,
		EffortSet:           opts.EffortSet,
		Input:               opts.Input,
		Goal:                opts.Goal,
		GoalSet:             opts.GoalSet,
		MaxContinuations:    opts.MaxContinuations,
		MaxContinuationsSet: opts.MaxContinuationsSet,
		Debug:               opts.Debug,
		Usage:               opts.Usage,
		Yolo:                opts.Yolo,
		Dev:                 opts.Dev,
		MaxToolRisk:         opts.MaxToolRisk,
		AuthPath:            opts.AuthPath,
		AllowPluginAuthEnv:  opts.AllowPluginAuthEnv,
		Workspace:           workspace,
		In:                  opts.In,
		Out:                 opts.Out,
		Err:                 opts.Err,
	})
}

func (a *App) loaderWithCoderConfig(loader launch.Loader) launch.Loader {
	bundles := coderConfigBundles(a.config)
	if len(bundles) == 0 {
		return loader
	}
	return func(ctx context.Context, path string) (distribution.Loaded, error) {
		load := loader
		if load == nil {
			load = distlocal.Load
		}
		loaded, err := load(ctx, path)
		if err != nil {
			return distribution.Loaded{}, err
		}
		loaded.Distribution.Bundles = append(loaded.Distribution.Bundles, bundles...)
		return loaded, nil
	}
}

func mergedRunWorkspace(workspace distribution.WorkspaceConfig, rootOverrides, envFileOverrides []string) (distribution.WorkspaceConfig, error) {
	roots, err := distribution.ParseWorkspaceRoots(rootOverrides)
	if err != nil {
		return distribution.WorkspaceConfig{}, err
	}
	return mergeWorkspace(workspace, distribution.WorkspaceConfig{
		Roots:    roots,
		EnvFiles: trimStrings(envFileOverrides),
	}), nil
}
