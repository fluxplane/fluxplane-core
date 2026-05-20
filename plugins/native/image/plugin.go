package image

import (
	"context"
	"fmt"
	"strings"
	"time"

	coreenvironment "github.com/fluxplane/agentruntime/core/environment"
	"github.com/fluxplane/agentruntime/core/operation"
	"github.com/fluxplane/agentruntime/core/resource"
	"github.com/fluxplane/agentruntime/core/tool"
	"github.com/fluxplane/agentruntime/orchestration/pluginhost"
	runtimeenvironment "github.com/fluxplane/agentruntime/runtime/environment"
	operationruntime "github.com/fluxplane/agentruntime/runtime/operation"
	"github.com/fluxplane/agentruntime/runtime/system"
)

const (
	Name               = "image"
	GenerationSet      = "image.generation"
	UnderstandingSet   = "image.understanding"
	GenerateOp         = "image_generate"
	UnderstandOp       = "image_understand"
	ProvidersOp        = "image_providers"
	defaultMaxFileSize = 10 * 1024 * 1024

	ObservationImageProviders = "image.providers"
	SignalImageReadyRequested = "capability.ready_and_requested"
	imageChannelMessageKind   = "channel.message"
	imageProviderObserverName = "image.providers"
	imageIntentSignalDeriver  = "image.intent"
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
var _ pluginhost.SignalDeriverContributor = Plugin{}

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
		Observers: []coreenvironment.ObserverSpec{{
			Name:            imageProviderObserverName,
			Description:     "Observes configured image generation and understanding providers.",
			Environment:     coreenvironment.Ref{Name: Name},
			Phase:           coreenvironment.PhaseTurn,
			ObservableKinds: []string{ObservationImageProviders},
			Dynamic:         true,
		}},
		SignalDerivers: []coreenvironment.SignalDeriverSpec{{
			Name:             imageIntentSignalDeriver,
			Description:      "Derives image activation when provider availability and turn intent are both present.",
			ObservationKinds: []string{ObservationImageProviders, imageChannelMessageKind},
			Signals: []coreenvironment.SignalTemplate{
				{Kind: SignalImageReadyRequested, Target: GenerationSet, Subject: coreenvironment.Subject{Kind: coreenvironment.SubjectCapability, Name: GenerationSet}},
				{Kind: SignalImageReadyRequested, Target: UnderstandingSet, Subject: coreenvironment.Subject{Kind: coreenvironment.SubjectCapability, Name: UnderstandingSet}},
			},
		}},
	}, nil
}

// EnvironmentObservers returns image provider availability observers.
func (p Plugin) EnvironmentObservers(context.Context, pluginhost.Context) ([]runtimeenvironment.Observer, error) {
	return []runtimeenvironment.Observer{imageProviderObserver{plugin: p}}, nil
}

// SignalDerivers returns image intent derivers.
func (Plugin) SignalDerivers(context.Context, pluginhost.Context) ([]runtimeenvironment.SignalDeriver, error) {
	return []runtimeenvironment.SignalDeriver{imageIntentDeriver{}}, nil
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

func (o imageProviderObserver) Spec() coreenvironment.ObserverSpec {
	return coreenvironment.ObserverSpec{
		Name:            imageProviderObserverName,
		Description:     "Observes configured image generation and understanding providers.",
		Environment:     coreenvironment.Ref{Name: Name},
		Phase:           coreenvironment.PhaseTurn,
		ObservableKinds: []string{ObservationImageProviders},
		Dynamic:         true,
	}
}

func (o imageProviderObserver) Observe(ctx context.Context, _ runtimeenvironment.ObservationRequest) ([]coreenvironment.Observation, error) {
	out := o.plugin.providerOutput(ctx)
	evidence := ImageProviderEvidence{
		Generation:    append([]ProviderInfo(nil), out.Generation...),
		Understanding: append([]ProviderInfo(nil), out.Understanding...),
	}
	return []coreenvironment.Observation{{
		ID:          "image:providers",
		Environment: coreenvironment.Ref{Name: Name},
		Kind:        ObservationImageProviders,
		Scope:       "runtime",
		Content:     evidence,
		At:          time.Now().UTC(),
	}}, nil
}

type imageIntentDeriver struct{}

func (imageIntentDeriver) Spec() coreenvironment.SignalDeriverSpec {
	return coreenvironment.SignalDeriverSpec{
		Name:             imageIntentSignalDeriver,
		Description:      "Derives image activation when provider availability and turn intent are both present.",
		ObservationKinds: []string{ObservationImageProviders, imageChannelMessageKind},
	}
}

func (imageIntentDeriver) Derive(_ context.Context, req runtimeenvironment.SignalDeriveRequest) ([]coreenvironment.Signal, error) {
	var providers ImageProviderEvidence
	var sawProviders bool
	var intent imageIntent
	var ids []string
	var scope string
	for _, observation := range req.Observations {
		switch observation.Kind {
		case ObservationImageProviders:
			if evidence, ok := imageProvidersFromObservation(observation.Content); ok {
				providers = evidence
				sawProviders = true
				ids = appendImageObservationID(ids, observation.ID)
			}
		case imageChannelMessageKind:
			if detected := detectImageTurnIntent(observation.Content); !detected.isZero() {
				intent = intent.merge(detected)
				ids = appendImageObservationID(ids, observation.ID)
				if scope == "" {
					scope = observation.Scope
				}
			}
		}
	}
	if !sawProviders {
		return nil, nil
	}
	var signals []coreenvironment.Signal
	if intent.Generate && providerListAvailable(providers.Generation) {
		signals = append(signals, imageReadySignal(GenerationSet, "generation", scope, ids))
	}
	if intent.Understand && providerListAvailable(providers.Understanding) {
		signals = append(signals, imageReadySignal(UnderstandingSet, "understanding", scope, ids))
	}
	return signals, nil
}

func imageReadySignal(target, mode, scope string, ids []string) coreenvironment.Signal {
	return coreenvironment.Signal{
		Kind:           SignalImageReadyRequested,
		Target:         target,
		Subject:        coreenvironment.Subject{Kind: coreenvironment.SubjectCapability, Name: target},
		Scope:          scope,
		Environment:    coreenvironment.Ref{Name: Name},
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

type imageIntent struct {
	Generate   bool
	Understand bool
}

func (i imageIntent) isZero() bool {
	return !i.Generate && !i.Understand
}

func (i imageIntent) merge(other imageIntent) imageIntent {
	return imageIntent{Generate: i.Generate || other.Generate, Understand: i.Understand || other.Understand}
}

func detectImageTurnIntent(content any) imageIntent {
	text := strings.ToLower(strings.TrimSpace(fmt.Sprint(content)))
	if text == "" {
		return imageIntent{}
	}
	intent := imageIntent{}
	for _, phrase := range []string{
		"generate an image",
		"create an image",
		"make an image",
		"draw an image",
		"generate a picture",
		"create a picture",
		"make a picture",
		"image generation",
	} {
		if strings.Contains(text, phrase) {
			intent.Generate = true
		}
	}
	for _, phrase := range []string{
		"describe this image",
		"understand this image",
		"analyze this image",
		"inspect this image",
	} {
		if strings.Contains(text, phrase) {
			intent.Understand = true
		}
	}
	if strings.Contains(text, "image") {
		for _, token := range []string{"generate", "create", "make", "draw"} {
			if strings.Contains(text, token) {
				intent.Generate = true
			}
		}
		for _, token := range []string{"describe", "understand", "analyze", "inspect"} {
			if strings.Contains(text, token) {
				intent.Understand = true
			}
		}
	}
	return intent
}

func appendImageObservationID(ids []string, id string) []string {
	if strings.TrimSpace(id) == "" {
		return ids
	}
	return append(ids, id)
}
