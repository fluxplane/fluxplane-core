package run

import (
	"fmt"
	"sort"
	"strings"

	"github.com/codewandler/modeldb"
	"github.com/fluxplane/agentruntime/adapters/anthropic"
	"github.com/fluxplane/agentruntime/adapters/codex"
	"github.com/fluxplane/agentruntime/adapters/minimax"
	"github.com/fluxplane/agentruntime/adapters/modelcatalog"
	openaiadapter "github.com/fluxplane/agentruntime/adapters/openai"
	"github.com/fluxplane/agentruntime/adapters/openrouter"
	corellm "github.com/fluxplane/agentruntime/core/llm"
	llmagent "github.com/fluxplane/agentruntime/runtime/agent/llmagent"
)

var (
	openAIModelIDs = []string{
		"gpt-5.5",
		"gpt-5.5-pro",
		"gpt-4.1-mini",
	}
	codexModelIDs = []string{
		"gpt-5.5",
		"gpt-5.5-pro",
	}
)

type modelFactory func(corellm.ModelSpec, bool) (llmagent.Model, error)

// ModelProvider binds an inert provider catalog entry to a runtime adapter.
type ModelProvider struct {
	Spec             corellm.ProviderSpec
	New              modelFactory
	UnknownModelHint string
}

// ModelRegistry is the run adapter's executable view of contributed LLM
// providers.
type ModelRegistry struct {
	catalog   corellm.ProviderCatalog
	factories map[corellm.ProviderName]modelFactory
	hints     map[corellm.ProviderName]string
}

// NewModelRegistry builds a provider registry from runtime-backed providers
// and optional inert provider specs.
func NewModelRegistry(providers []ModelProvider, specs ...corellm.ProviderSpec) (ModelRegistry, error) {
	byName := map[corellm.ProviderName]corellm.ProviderSpec{}
	factories := map[corellm.ProviderName]modelFactory{}
	hints := map[corellm.ProviderName]string{}
	for _, provider := range providers {
		if err := provider.Spec.Validate(); err != nil {
			return ModelRegistry{}, err
		}
		byName[provider.Spec.Name] = provider.Spec
		if provider.New != nil {
			factories[provider.Spec.Name] = provider.New
		}
		if provider.UnknownModelHint != "" {
			hints[provider.Spec.Name] = provider.UnknownModelHint
		}
	}
	for _, spec := range specs {
		if err := spec.Validate(); err != nil {
			return ModelRegistry{}, err
		}
		byName[spec.Name] = spec
	}
	merged := make([]corellm.ProviderSpec, 0, len(byName))
	for _, spec := range byName {
		merged = append(merged, spec)
	}
	sort.Slice(merged, func(i, j int) bool { return merged[i].Name < merged[j].Name })
	catalog, err := corellm.NewProviderCatalog(merged...)
	if err != nil {
		return ModelRegistry{}, err
	}
	return ModelRegistry{catalog: catalog, factories: factories, hints: hints}, nil
}

// DefaultModelRegistry returns the built-in providers projected from modeldb,
// plus optional provider specs contributed by the loaded distribution.
func DefaultModelRegistry(specs ...corellm.ProviderSpec) (ModelRegistry, error) {
	providers, err := defaultModelProviders()
	if err != nil {
		return ModelRegistry{}, err
	}
	return NewModelRegistry(providers, specs...)
}

func (r ModelRegistry) ResolveModelSelection(provider, model string) ModelSelection {
	provider = strings.TrimSpace(provider)
	if provider == "" {
		provider = "openai"
	}
	model = strings.TrimSpace(model)
	if before, after, ok := strings.Cut(model, "/"); ok && before != "" && after != "" {
		if r.catalog.HasProvider(before) && provider == "openai" {
			provider = before
			model = after
		}
	}
	if model == "" {
		model = defaultModel
	}
	return ModelSelection{Provider: provider, Model: model}
}

func (r ModelRegistry) NewModel(selection ModelSelection, debug bool) (llmagent.Model, error) {
	provider, ok := r.catalog.Provider(selection.Provider)
	if !ok {
		return nil, fmt.Errorf("unknown provider %q; available providers: %s", selection.Provider, strings.Join(r.providerNames(), ", "))
	}
	_, modelSpec, ok := r.catalog.Find(selection.Provider, selection.Model)
	if !ok {
		err := fmt.Errorf("%s model %q was not found in the supported model catalog", selection.Provider, selection.Model)
		if hint := r.hints[provider.Name]; hint != "" {
			err = fmt.Errorf("%w; %s", err, hint)
		}
		return nil, err
	}
	factory := r.factories[provider.Name]
	if factory == nil {
		return nil, fmt.Errorf("provider %q is in the model catalog but has no runtime adapter", provider.Name)
	}
	return factory(modelSpec, debug)
}

func (r ModelRegistry) ModelSpec(providerName, modelName string) (corellm.ProviderSpec, corellm.ModelSpec, bool) {
	return r.catalog.Find(providerName, modelName)
}

// Providers returns the resolved provider specs sorted by provider name.
func (r ModelRegistry) Providers() []corellm.ProviderSpec {
	return r.catalog.Providers()
}

func (r ModelRegistry) providerNames() []string {
	providers := r.catalog.Providers()
	out := make([]string, 0, len(providers))
	for _, provider := range providers {
		out = append(out, string(provider.Name))
	}
	return out
}

func defaultModelProviders() ([]ModelProvider, error) {
	catalog, err := modeldb.LoadBuiltIn()
	if err != nil {
		return nil, err
	}
	project := func(projection modelcatalog.ProviderProjection) (corellm.ProviderSpec, error) {
		spec, ok := modelcatalog.ProjectProvider(catalog, projection)
		if !ok {
			return corellm.ProviderSpec{}, fmt.Errorf("model catalog provider %q was not found", projection.ServiceID)
		}
		return spec, nil
	}
	openAISpec, err := project(modelcatalog.ProviderProjection{
		ServiceID: "openai",
		APIType:   modeldb.APITypeOpenAIResponses,
		ModelIDs:  openAIModelIDs,
	})
	if err != nil {
		return nil, err
	}
	codexSpec, err := project(modelcatalog.ProviderProjection{
		ServiceID: "codex",
		APIType:   modeldb.APITypeOpenAIResponses,
		ModelIDs:  codexModelIDs,
	})
	if err != nil {
		return nil, err
	}
	openRouterSpec, err := project(modelcatalog.ProviderProjection{
		ServiceID: "openrouter",
		APIType:   modeldb.APITypeOpenAIResponses,
	})
	if err != nil {
		return nil, err
	}
	anthropicSpec, err := project(modelcatalog.ProviderProjection{
		ServiceID: "anthropic",
		APIType:   modeldb.APITypeAnthropicMessages,
	})
	if err != nil {
		return nil, err
	}
	minimaxSpec, err := project(modelcatalog.ProviderProjection{
		ServiceID: "minimax",
		APIType:   modeldb.APITypeAnthropicMessages,
	})
	if err != nil {
		return nil, err
	}
	return []ModelProvider{
		{Spec: openAISpec, New: newOpenAIModel},
		{Spec: codexSpec, New: newCodexModel},
		{
			Spec:             openRouterSpec,
			New:              newOpenRouterModel,
			UnknownModelHint: "use an exact OpenRouter model id, for example --model openrouter/anthropic/claude-sonnet-4.6",
		},
		{Spec: anthropicSpec, New: newAnthropicModel},
		{Spec: minimaxSpec, New: newMinimaxModel},
	}, nil
}

func newOpenAIModel(modelSpec corellm.ModelSpec, debug bool) (llmagent.Model, error) {
	return openaiadapter.New(openaiadapter.Config{
		Model:             string(modelSpec.Ref.Name),
		Runtime:           openaiadapter.DefaultResponsesRuntimeConfig(),
		Pricing:           modelSpec.Pricing,
		ParallelToolCalls: true,
		Redactor:          debugRedactor(debug),
	})
}

func newCodexModel(modelSpec corellm.ModelSpec, debug bool) (llmagent.Model, error) {
	return codex.New(codex.Config{
		Model:             string(modelSpec.Ref.Name),
		Runtime:           openaiadapter.DefaultResponsesRuntimeConfig(),
		Pricing:           modelSpec.Pricing,
		ParallelToolCalls: true,
		Redactor:          debugRedactor(debug),
	})
}

func newOpenRouterModel(modelSpec corellm.ModelSpec, debug bool) (llmagent.Model, error) {
	if !modelcatalog.SupportsAPI(modelSpec, string(modeldb.APITypeOpenAIResponses)) {
		return nil, fmt.Errorf("openrouter model %q does not expose OpenAI Responses in modeldb", modelSpec.Ref.Name)
	}
	reasoningEffort, reasoningSummary := OpenRouterReasoningDefaults(modelSpec)
	return openrouter.New(openrouter.Config{
		Model:             string(modelSpec.Ref.Name),
		Pricing:           modelSpec.Pricing,
		ReasoningEffort:   reasoningEffort,
		ReasoningSummary:  reasoningSummary,
		ParallelToolCalls: true,
		Redactor:          debugRedactor(debug),
	})
}

func newAnthropicModel(modelSpec corellm.ModelSpec, debug bool) (llmagent.Model, error) {
	if err := requireMessagesModel("anthropic", modelSpec); err != nil {
		return nil, err
	}
	return anthropic.New(anthropic.Config{
		Model:           string(modelSpec.Ref.Name),
		Pricing:         modelSpec.Pricing,
		MaxOutputTokens: maxOutputTokens(modelSpec),
		PromptCache:     modelSpec.Capabilities.Has(corellm.CapabilityPromptCaching),
		Redactor:        debugRedactor(debug),
	})
}

func newMinimaxModel(modelSpec corellm.ModelSpec, debug bool) (llmagent.Model, error) {
	if err := requireMessagesModel("minimax", modelSpec); err != nil {
		return nil, err
	}
	return minimax.New(minimax.Config{
		Model:           string(modelSpec.Ref.Name),
		Pricing:         modelSpec.Pricing,
		MaxOutputTokens: maxOutputTokens(modelSpec),
		PromptCache:     modelSpec.Capabilities.Has(corellm.CapabilityPromptCaching),
		Redactor:        debugRedactor(debug),
	})
}
