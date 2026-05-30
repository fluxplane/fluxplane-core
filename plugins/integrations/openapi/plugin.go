// Package openapiplugin generates operations and documentation datasources
// from OpenAPI 3.x specifications.
package openapi

import (
	"context"
	"fmt"
	runtimeworkspace "github.com/fluxplane/fluxplane-core/runtime/workspace"
	fpsystem "github.com/fluxplane/fluxplane-system"

	"github.com/fluxplane/fluxplane-core/core/operation"
	"github.com/fluxplane/fluxplane-core/core/resource"
	coresecret "github.com/fluxplane/fluxplane-core/core/secret"
	"github.com/fluxplane/fluxplane-core/orchestration/pluginhost"
)

type Boundaries struct {
	Network     fpsystem.Network
	Environment fpsystem.Environment
}

func BoundariesFromSystem(sys fpsystem.System) Boundaries {
	if sys == nil {
		return Boundaries{}
	}
	return Boundaries{Network: sys.Network(), Environment: sys.Environment()}
}

// Plugin contributes OpenAPI-generated resources.
type Plugin struct {
	pluginhost.Configurable[Config]
	boundaries Boundaries
	workspace  runtimeworkspace.Workspace
	ref        resource.PluginRef
	cfg        Config
}

var _ pluginhost.Plugin = Plugin{}
var _ pluginhost.InstanceFactory = Plugin{}
var _ pluginhost.OperationContributor = Plugin{}
var _ pluginhost.DatasourceProviderContributor = Plugin{}
var _ pluginhost.AuthMethodContributor = Plugin{}

// New returns an OpenAPI plugin.
func New(sys fpsystem.System, workspaces ...runtimeworkspace.Workspace) Plugin {
	return NewWithBoundaries(BoundariesFromSystem(sys), workspaceFromSystem(sys, workspaces...))
}

func NewWithBoundaries(boundaries Boundaries, workspaces ...runtimeworkspace.Workspace) Plugin {
	var workspace runtimeworkspace.Workspace
	if len(workspaces) > 0 {
		workspace = workspaces[0]
	}
	return Plugin{boundaries: boundaries, workspace: workspace}
}

func workspaceFromSystem(sys fpsystem.System, workspaces ...runtimeworkspace.Workspace) runtimeworkspace.Workspace {
	if len(workspaces) > 0 {
		return workspaces[0]
	}
	if provider, ok := sys.(interface {
		Workspace() runtimeworkspace.Workspace
	}); ok {
		return provider.Workspace()
	}
	return nil
}

func (Plugin) Manifest() pluginhost.Manifest {
	return pluginhost.Manifest{Name: Name, Description: "OpenAPI-generated operations and documentation datasources."}
}

func (p Plugin) Instantiate(_ context.Context, ctx pluginhost.Context) (pluginhost.Plugin, error) {
	cfg, err := pluginhost.ConfigAs[Config](ctx)
	if err != nil {
		return nil, err
	}
	cfg = normalizeConfig(cfg)
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	p.ref = ctx.Ref
	p.cfg = cfg
	return p, nil
}

func (p Plugin) Contributions(ctx context.Context, _ pluginhost.Context) (resource.ContributionBundle, error) {
	generated, diagnostics, err := p.generatedForContributions(ctx)
	if err != nil {
		return resource.ContributionBundle{}, err
	}
	bundle := resource.ContributionBundle{
		Operations:  generated.Operations,
		Datasources: generated.Datasources,
		DataSources: generated.DataSources,
		Diagnostics: diagnostics,
	}
	if len(generated.OperationSet.Operations) > 0 {
		bundle.OperationSets = []operation.Set{generated.OperationSet}
	}
	return bundle, nil
}

func (p Plugin) Operations(ctx context.Context, _ pluginhost.Context) ([]operation.Operation, error) {
	if p.boundaries.Network == nil {
		return nil, fmt.Errorf("openapiplugin: network is nil")
	}
	generated, err := p.generated(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]operation.Operation, 0, len(generated.Executable))
	for _, def := range generated.Executable {
		out = append(out, openAPIOperation{network: p.boundaries.Network, environment: p.boundaries.Environment, def: def})
	}
	return out, nil
}

func (p Plugin) AuthMethods(ctx context.Context, _ pluginhost.Context) ([]coresecret.AuthMethodSpec, error) {
	generated, err := p.generated(ctx)
	if err != nil {
		return nil, err
	}
	return generated.AuthMethods, nil
}

func (p Plugin) generatedForContributions(ctx context.Context) (generatedSpec, []resource.Diagnostic, error) {
	generated, err := p.generated(ctx)
	if err == nil {
		return generated, nil, nil
	}
	if p.boundaries.Network != nil || p.workspace != nil {
		return generatedSpec{}, nil, err
	}
	return generatedSpec{}, []resource.Diagnostic{{
		Severity: resource.SeverityWarning,
		Message:  "openapi plugin contributions skipped: " + err.Error(),
	}}, nil
}

func (p Plugin) generated(ctx context.Context) (generatedSpec, error) {
	if p.boundaries.Network == nil && p.workspace == nil {
		return generatedSpec{}, fmt.Errorf("openapiplugin: network or workspace is required")
	}
	loaded, errs := loadSpecs(ctx, p.boundaries.Network, p.workspace, p.cfg)
	if len(errs) > 0 {
		return generatedSpec{}, errs[0]
	}
	return generateAll(p.ref, loaded)
}
