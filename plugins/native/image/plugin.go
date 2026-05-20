package image

import (
	"context"
	"fmt"

	"github.com/fluxplane/agentruntime/core/operation"
	"github.com/fluxplane/agentruntime/core/resource"
	"github.com/fluxplane/agentruntime/core/tool"
	"github.com/fluxplane/agentruntime/orchestration/pluginhost"
	operationruntime "github.com/fluxplane/agentruntime/runtime/operation"
	"github.com/fluxplane/agentruntime/runtime/system"
)

const (
	Name               = "image"
	GenerateOp         = "image_generate"
	UnderstandOp       = "image_understand"
	ProvidersOp        = "image_providers"
	defaultMaxFileSize = 10 * 1024 * 1024
)

// Plugin contributes image generation and understanding operations.
type Plugin struct {
	system        system.System
	generators    []GenerationProvider
	understanders []UnderstandingProvider
}

var _ pluginhost.Plugin = Plugin{}
var _ pluginhost.OperationContributor = Plugin{}

// Option customizes the image plugin.
type Option func(*Plugin)

// WithGenerationProvider appends a generation provider.
func WithGenerationProvider(provider GenerationProvider) Option {
	return func(p *Plugin) {
		if provider != nil {
			p.generators = append(p.generators, provider)
		}
	}
}

// WithUnderstandingProvider appends an understanding provider.
func WithUnderstandingProvider(provider UnderstandingProvider) Option {
	return func(p *Plugin) {
		if provider != nil {
			p.understanders = append(p.understanders, provider)
		}
	}
}

// New returns an image plugin with built-in providers.
func New(sys system.System, opts ...Option) Plugin {
	p := Plugin{
		system: sys,
		generators: []GenerationProvider{
			pollinationsProvider{},
			openAIImageProvider{},
			openRouterImageProvider{},
		},
		understanders: []UnderstandingProvider{
			anthropicUnderstandingProvider{},
			openRouterUnderstandingProvider{},
		},
	}
	for _, opt := range opts {
		if opt != nil {
			opt(&p)
		}
	}
	return p
}

// Manifest returns plugin metadata.
func (Plugin) Manifest() pluginhost.Manifest {
	return pluginhost.Manifest{Name: Name, Description: "Image generation and understanding operations."}
}

// Contributions returns image resource specs.
func (Plugin) Contributions(context.Context, pluginhost.Context) (resource.ContributionBundle, error) {
	specs := []operation.Spec{generateSpec(), understandSpec(), providersSpec()}
	return resource.ContributionBundle{
		OperationSets: []operation.Set{{
			Name:        Name,
			Description: "Image generation, understanding, and provider inspection.",
			Operations:  []operation.Ref{{Name: GenerateOp}, {Name: UnderstandOp}, {Name: ProvidersOp}},
		}},
		ToolSets:   []tool.Set{imageToolSet()},
		Operations: specs,
	}, nil
}

// Operations returns executable image operations.
func (p Plugin) Operations(context.Context, pluginhost.Context) ([]operation.Operation, error) {
	if p.system == nil {
		return nil, fmt.Errorf("imageplugin: system is nil")
	}
	return []operation.Operation{
		operationruntime.NewTypedResult[GenerateRequest, operation.Rendered](generateSpec(), p.generate),
		operationruntime.NewTypedResult[UnderstandRequest, operation.Rendered](understandSpec(), p.understand),
		operationruntime.NewTypedResult[providersInput, operation.Rendered](providersSpec(), p.providers),
	}, nil
}
