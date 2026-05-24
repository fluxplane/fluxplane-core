package image

import (
	"context"
	"fmt"
	"strings"
	"time"

	coreevidence "github.com/fluxplane/fluxplane-core/core/evidence"
	"github.com/fluxplane/fluxplane-core/core/operation"
	"github.com/fluxplane/fluxplane-core/core/resource"
	"github.com/fluxplane/fluxplane-core/core/tool"
	"github.com/fluxplane/fluxplane-core/orchestration/pluginhost"
	runtimeevidence "github.com/fluxplane/fluxplane-core/runtime/evidence"
	operationruntime "github.com/fluxplane/fluxplane-core/runtime/operation"
	"github.com/fluxplane/fluxplane-core/runtime/system"
)

const (
	Name               = "image"
	GenerationSet      = "image.generation"
	UnderstandingSet   = "image.understanding"
	GenerateOp         = "image_generate"
	UnderstandOp       = "image_understand"
	ProvidersOp        = "image_providers"
	defaultMaxFileSize = 10 * 1024 * 1024

	ObservationImageProviders     = "image.providers"
	AssertionImageProviderReady   = "capability.available"
	imageProviderObserverName     = "image.providers"
	imageProviderAssertionDeriver = "image.availability"
)

// Plugin contributes image generation and understanding operations.
type Plugin struct {
	system        system.System
	generators    []GenerationProvider
	understanders []UnderstandingProvider
}

var _ pluginhost.Plugin = Plugin{}
var _ pluginhost.OperationContributor = Plugin{}
var _ pluginhost.ObserverContributor = Plugin{}
var _ pluginhost.AssertionDeriverContributor = Plugin{}

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
		OperationSets: []operation.Set{
			{
				Name:        Name,
				Description: "Image generation, understanding, and provider inspection.",
				Operations:  []operation.Ref{{Name: GenerateOp}, {Name: UnderstandOp}, {Name: ProvidersOp}},
			},
			{
				Name:        GenerationSet,
				Description: "Image generation and provider inspection.",
				Operations:  []operation.Ref{{Name: GenerateOp}, {Name: ProvidersOp}},
			},
			{
				Name:        UnderstandingSet,
				Description: "Image understanding and provider inspection.",
				Operations:  []operation.Ref{{Name: UnderstandOp}, {Name: ProvidersOp}},
			},
		},
		ToolSets:   []tool.Set{imageToolSet()},
		Operations: specs,
		Observers: []coreevidence.ObserverSpec{{
			Name:            imageProviderObserverName,
			Description:     "Observes configured image generation and understanding providers.",
			Environment:     coreevidence.Ref{Name: Name},
			Phase:           coreevidence.PhaseTurn,
			ObservableKinds: []string{ObservationImageProviders},
			Dynamic:         true,
		}},
		AssertionDerivers: []coreevidence.AssertionDeriverSpec{{
			Name:             imageProviderAssertionDeriver,
			Description:      "Derives image activation from stable provider availability.",
			ObservationKinds: []string{ObservationImageProviders},
			Assertions: []coreevidence.AssertionTemplate{
				{Kind: AssertionImageProviderReady, Target: GenerationSet, Subject: coreevidence.Subject{Kind: coreevidence.SubjectCapability, Name: GenerationSet}},
				{Kind: AssertionImageProviderReady, Target: UnderstandingSet, Subject: coreevidence.Subject{Kind: coreevidence.SubjectCapability, Name: UnderstandingSet}},
			},
		}},
	}, nil
}

// EnvironmentObservers returns image provider availability observers.
func (p Plugin) EnvironmentObservers(context.Context, pluginhost.Context) ([]runtimeevidence.Observer, error) {
	return []runtimeevidence.Observer{imageProviderObserver{plugin: p}}, nil
}

// AssertionDerivers returns image provider availability derivers.
func (Plugin) AssertionDerivers(context.Context, pluginhost.Context) ([]runtimeevidence.AssertionDeriver, error) {
	return []runtimeevidence.AssertionDeriver{imageProviderDeriver{}}, nil
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

type ImageProviderEvidence struct {
	Generation    []ProviderInfo `json:"generation,omitempty"`
	Understanding []ProviderInfo `json:"understanding,omitempty"`
}

type imageProviderObserver struct {
	plugin Plugin
}

func (o imageProviderObserver) Spec() coreevidence.ObserverSpec {
	return coreevidence.ObserverSpec{
		Name:            imageProviderObserverName,
		Description:     "Observes configured image generation and understanding providers.",
		Environment:     coreevidence.Ref{Name: Name},
		Phase:           coreevidence.PhaseTurn,
		ObservableKinds: []string{ObservationImageProviders},
		Dynamic:         true,
	}
}

func (o imageProviderObserver) Observe(ctx context.Context, _ runtimeevidence.ObservationRequest) ([]coreevidence.Observation, error) {
	out := o.plugin.providerOutput(ctx)
	evidence := ImageProviderEvidence{
		Generation:    append([]ProviderInfo(nil), out.Generation...),
		Understanding: append([]ProviderInfo(nil), out.Understanding...),
	}
	return []coreevidence.Observation{{
		ID:          "image:providers",
		Environment: coreevidence.Ref{Name: Name},
		Kind:        ObservationImageProviders,
		Scope:       "runtime",
		Content:     evidence,
		At:          time.Now().UTC(),
	}}, nil
}

type imageProviderDeriver struct{}

func (imageProviderDeriver) Spec() coreevidence.AssertionDeriverSpec {
	return coreevidence.AssertionDeriverSpec{
		Name:             imageProviderAssertionDeriver,
		Description:      "Derives image activation from stable provider availability.",
		ObservationKinds: []string{ObservationImageProviders},
	}
}

func (imageProviderDeriver) Derive(_ context.Context, req runtimeevidence.AssertionDeriveRequest) ([]coreevidence.Assertion, error) {
	var providers ImageProviderEvidence
	var sawProviders bool
	var ids []string
	var scope string
	for _, observation := range req.Observations {
		if observation.Kind != ObservationImageProviders {
			continue
		}
		if evidence, ok := imageProvidersFromObservation(observation.Content); ok {
			providers = evidence
			sawProviders = true
			ids = appendImageObservationID(ids, observation.ID)
			if scope == "" {
				scope = observation.Scope
			}
		}
	}
	if !sawProviders {
		return nil, nil
	}
	var assertions []coreevidence.Assertion
	if providerListAvailable(providers.Generation) {
		assertions = append(assertions, imageReadyAssertion(GenerationSet, "generation", scope, ids))
	}
	if providerListAvailable(providers.Understanding) {
		assertions = append(assertions, imageReadyAssertion(UnderstandingSet, "understanding", scope, ids))
	}
	return assertions, nil
}

func imageReadyAssertion(target, mode, scope string, ids []string) coreevidence.Assertion {
	return coreevidence.Assertion{
		Kind:           AssertionImageProviderReady,
		Target:         target,
		Subject:        coreevidence.Subject{Kind: coreevidence.SubjectCapability, Name: target},
		Scope:          scope,
		Environment:    coreevidence.Ref{Name: Name},
		Confidence:     1,
		ObservationIDs: append([]string(nil), ids...),
		Metadata:       map[string]string{"capability": Name, "mode": mode},
	}
}

func imageProvidersFromObservation(content any) (ImageProviderEvidence, bool) {
	switch typed := content.(type) {
	case ImageProviderEvidence:
		return typed, true
	case *ImageProviderEvidence:
		if typed == nil {
			return ImageProviderEvidence{}, false
		}
		return *typed, true
	default:
		return ImageProviderEvidence{}, false
	}
}

func providerListAvailable(providers []ProviderInfo) bool {
	for _, provider := range providers {
		if provider.Configured {
			return true
		}
	}
	return false
}

func appendImageObservationID(ids []string, id string) []string {
	if strings.TrimSpace(id) == "" {
		return ids
	}
	return append(ids, id)
}
