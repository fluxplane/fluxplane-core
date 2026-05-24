package run

import (
	"fmt"
	"sort"
	"strings"

	"github.com/codewandler/modeldb"
	"github.com/fluxplane/fluxplane-core/adapters/llm/anthropic"
	"github.com/fluxplane/fluxplane-core/adapters/llm/claudecode"
	"github.com/fluxplane/fluxplane-core/adapters/llm/codex"
	"github.com/fluxplane/fluxplane-core/adapters/llm/minimax"
	"github.com/fluxplane/fluxplane-core/adapters/llm/modelcatalog"
	"github.com/fluxplane/fluxplane-core/adapters/llm/openai"
	"github.com/fluxplane/fluxplane-core/adapters/llm/openrouter"
	"github.com/fluxplane/fluxplane-core/core/agent"
	corellm "github.com/fluxplane/fluxplane-core/core/llm"
	llmagent "github.com/fluxplane/fluxplane-core/runtime/agent/llmagent"
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

type modelFactory func(corellm.ModelSpec, ModelOptions) (llmagent.Model, error)

// ModelOptions configures runtime model construction for one resolved model.
type ModelOptions struct {
	Debug     bool
	Reasoning ReasoningConfig
}

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
	aliases   map[string]corellm.ModelRef
}

// NewModelRegistry builds a provider registry from runtime-backed providers
// and optional inert provider specs.
func NewModelRegistry(providers []ModelProvider, specs ...corellm.ProviderSpec) (ModelRegistry, error) {
	return NewModelRegistryWithAliases(providers, specs, nil)
}

// NewModelRegistryWithAliases builds a provider registry with explicit model
// alias overlays. Explicit aliases override provider/modeldb aliases.
func NewModelRegistryWithAliases(providers []ModelProvider, specs []corellm.ProviderSpec, aliases []corellm.ModelAliasSpec) (ModelRegistry, error) {
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
		if existing, ok := byName[spec.Name]; ok {
			byName[spec.Name] = mergeProviderSpec(existing, spec)
		} else {
			byName[spec.Name] = spec
		}
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
	aliasMap, err := modelAliases(catalog, aliases)
	if err != nil {
		return ModelRegistry{}, err
	}
	return ModelRegistry{catalog: catalog, factories: factories, hints: hints, aliases: aliasMap}, nil
}

func mergeProviderSpec(base, overlay corellm.ProviderSpec) corellm.ProviderSpec {
	if overlay.DisplayName != "" {
		base.DisplayName = overlay.DisplayName
	}
	if overlay.Description != "" {
		base.Description = overlay.Description
	}
	if len(overlay.Annotations) > 0 {
		base.Annotations = cloneStringMap(overlay.Annotations)
	}
	byName := map[corellm.ModelName]corellm.ModelSpec{}
	var order []corellm.ModelName
	for _, model := range base.Models {
		name := model.Ref.Name
		byName[name] = model
		order = append(order, name)
	}
	for _, model := range overlay.Models {
		name := model.Ref.Name
		if existing, ok := byName[name]; ok {
			byName[name] = mergeModelSpec(existing, model)
			continue
		}
		byName[name] = model
		order = append(order, name)
	}
	base.Models = make([]corellm.ModelSpec, 0, len(order))
	for _, name := range order {
		base.Models = append(base.Models, byName[name])
	}
	return base
}

func mergeModelSpec(base, overlay corellm.ModelSpec) corellm.ModelSpec {
	if overlay.DisplayName != "" {
		base.DisplayName = overlay.DisplayName
	}
	if overlay.Description != "" {
		base.Description = overlay.Description
	}
	base.Aliases = mergeModelAliases(base.Aliases, overlay.Aliases)
	if overlay.Params.Thinking != "" {
		base.Params.Thinking = overlay.Params.Thinking
	}
	if overlay.Params.ReasoningEffort != "" {
		base.Params.ReasoningEffort = overlay.Params.ReasoningEffort
	}
	if len(overlay.Annotations) > 0 {
		base.Annotations = cloneStringMap(overlay.Annotations)
	}
	return base
}

func mergeModelAliases(groups ...[]corellm.ModelName) []corellm.ModelName {
	seen := map[corellm.ModelName]bool{}
	var out []corellm.ModelName
	for _, group := range groups {
		for _, alias := range group {
			if alias == "" || seen[alias] {
				continue
			}
			seen[alias] = true
			out = append(out, alias)
		}
	}
	return out
}

func cloneStringMap(input map[string]string) map[string]string {
	if len(input) == 0 {
		return nil
	}
	out := make(map[string]string, len(input))
	for key, value := range input {
		out[key] = value
	}
	return out
}

// DefaultModelRegistry returns the built-in providers projected from modeldb,
// plus optional provider specs contributed by the loaded distribution.
func DefaultModelRegistry(specs ...corellm.ProviderSpec) (ModelRegistry, error) {
	return DefaultModelRegistryWithAliases(specs, nil)
}

// DefaultModelRegistryWithAliases returns the built-in providers with alias
// overlays contributed by the loaded distribution.
func DefaultModelRegistryWithAliases(specs []corellm.ProviderSpec, aliases []corellm.ModelAliasSpec) (ModelRegistry, error) {
	providers, err := defaultModelProviders()
	if err != nil {
		return ModelRegistry{}, err
	}
	return NewModelRegistryWithAliases(providers, specs, aliases)
}

func (r ModelRegistry) ResolveModelSelection(provider, model string) ModelSelection {
	provider = strings.TrimSpace(provider)
	if provider == "" {
		provider = "openai"
	}
	model = strings.TrimSpace(model)
	if target, ok := r.resolveAlias(provider, model); ok {
		return ModelSelection{Provider: string(target.Provider), Model: string(target.Name)}
	}
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

func (r ModelRegistry) resolveAlias(provider, model string) (corellm.ModelRef, bool) {
	model = strings.TrimSpace(model)
	if model == "" || len(r.aliases) == 0 {
		return corellm.ModelRef{}, false
	}
	if target, ok := r.aliases[model]; ok {
		return target, true
	}
	provider = strings.TrimSpace(provider)
	if provider == "" || strings.Contains(model, "/") {
		return corellm.ModelRef{}, false
	}
	target, ok := r.aliases[provider+"/"+model]
	return target, ok
}

func (r ModelRegistry) NewModel(selection ModelSelection, debug bool) (llmagent.Model, error) {
	if _, modelSpec, ok := r.ModelSpec(selection.Provider, selection.Model); ok {
		reasoning, err := ResolveReasoning(selection.Provider, modelSpec, agent.InferenceSpec{}, ReasoningOverrides{})
		if err != nil {
			return nil, err
		}
		return r.NewModelWithOptions(selection, ModelOptions{Debug: debug, Reasoning: reasoning})
	}
	return r.NewModelWithOptions(selection, ModelOptions{Debug: debug})
}

// NewModelWithOptions constructs a provider model with explicit runtime
// options.
func (r ModelRegistry) NewModelWithOptions(selection ModelSelection, opts ModelOptions) (llmagent.Model, error) {
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
	return factory(modelSpec, opts)
}

func (r ModelRegistry) ModelSpec(providerName, modelName string) (corellm.ProviderSpec, corellm.ModelSpec, bool) {
	return r.catalog.Find(providerName, modelName)
}

// Providers returns the resolved provider specs sorted by provider name.
func (r ModelRegistry) Providers() []corellm.ProviderSpec {
	return r.catalog.Providers()
}

// Aliases returns the effective alias table sorted by alias name.
func (r ModelRegistry) Aliases() []corellm.ModelAliasSpec {
	out := make([]corellm.ModelAliasSpec, 0, len(r.aliases))
	for name, target := range r.aliases {
		out = append(out, corellm.ModelAliasSpec{Name: name, Target: target})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func (r ModelRegistry) providerNames() []string {
	providers := r.catalog.Providers()
	out := make([]string, 0, len(providers))
	for _, provider := range providers {
		out = append(out, string(provider.Name))
	}
	return out
}

func modelAliases(catalog corellm.ProviderCatalog, overlays []corellm.ModelAliasSpec) (map[string]corellm.ModelRef, error) {
	aliases := map[string]corellm.ModelRef{}
	for _, provider := range catalog.Providers() {
		for _, model := range provider.Models {
			for _, alias := range model.Aliases {
				name := strings.TrimSpace(string(alias))
				if name == "" {
					continue
				}
				aliases[string(provider.Name)+"/"+name] = corellm.ModelRef{Provider: provider.Name, Name: model.Ref.Name}
			}
		}
	}
	addBuiltInModelAliases(aliases, catalog)
	for _, alias := range overlays {
		if err := alias.Validate(); err != nil {
			return nil, err
		}
		aliases[strings.TrimSpace(alias.Name)] = alias.Target
	}
	return aliases, nil
}

func addBuiltInModelAliases(aliases map[string]corellm.ModelRef, catalog corellm.ProviderCatalog) {
	addAliasIfModelExists(aliases, catalog, "codex", "codex", defaultModel)
	for _, provider := range []string{"anthropic", "claudecode"} {
		for _, family := range []string{"opus", "sonnet", "haiku"} {
			if model, ok := latestClaudeFamilyModel(catalog, provider, family); ok {
				addAlias(aliases, provider+"/"+family, provider, model)
				if provider == "anthropic" {
					addAlias(aliases, "claude/"+family, provider, model)
				}
			}
		}
	}
	if model, ok := preferredModel(catalog, "minimax", "MiniMax-M2.7"); ok {
		addAlias(aliases, "minimax", "minimax", model)
		addAlias(aliases, "minimax/latest", "minimax", model)
	}
}

func addAliasIfModelExists(aliases map[string]corellm.ModelRef, catalog corellm.ProviderCatalog, name, provider, model string) {
	if _, _, ok := catalog.Find(provider, model); !ok {
		return
	}
	addAlias(aliases, name, provider, model)
}

func addAlias(aliases map[string]corellm.ModelRef, name, provider, model string) {
	name = strings.TrimSpace(name)
	provider = strings.TrimSpace(provider)
	model = strings.TrimSpace(model)
	if name == "" || provider == "" || model == "" {
		return
	}
	aliases[name] = corellm.ModelRef{Provider: corellm.ProviderName(provider), Name: corellm.ModelName(model)}
}

func latestClaudeFamilyModel(catalog corellm.ProviderCatalog, providerName, family string) (string, bool) {
	provider, ok := catalog.Provider(providerName)
	if !ok {
		return "", false
	}
	prefix := "claude-" + family + "-"
	var names []string
	for _, model := range provider.Models {
		name := string(model.Ref.Name)
		if strings.HasPrefix(name, prefix) {
			names = append(names, name)
		}
	}
	if len(names) == 0 {
		return "", false
	}
	sort.Strings(names)
	return names[len(names)-1], true
}

func preferredModel(catalog corellm.ProviderCatalog, providerName, preferred string) (string, bool) {
	if _, _, ok := catalog.Find(providerName, preferred); ok {
		return preferred, true
	}
	provider, ok := catalog.Provider(providerName)
	if !ok || len(provider.Models) == 0 {
		return "", false
	}
	names := make([]string, 0, len(provider.Models))
	for _, model := range provider.Models {
		names = append(names, string(model.Ref.Name))
	}
	sort.Strings(names)
	return names[len(names)-1], true
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
	claudeCodeSpec := rebindProviderSpec(anthropicSpec, "claudecode", "Claude Code")
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
		{Spec: claudeCodeSpec, New: newClaudeCodeModel},
		{Spec: minimaxSpec, New: newMinimaxModel},
	}, nil
}

func rebindProviderSpec(spec corellm.ProviderSpec, name, displayName string) corellm.ProviderSpec {
	spec.Name = corellm.ProviderName(name)
	spec.DisplayName = displayName
	spec.Models = append([]corellm.ModelSpec(nil), spec.Models...)
	for i := range spec.Models {
		spec.Models[i].Ref.Provider = spec.Name
	}
	return spec
}

func newOpenAIModel(modelSpec corellm.ModelSpec, opts ModelOptions) (llmagent.Model, error) {
	return openai.New(openai.Config{
		Model:             string(modelSpec.Ref.Name),
		Runtime:           openai.DefaultResponsesRuntimeConfig(),
		Pricing:           modelSpec.Pricing,
		ReasoningEffort:   opts.Reasoning.Effort,
		ReasoningSummary:  firstNonEmptyString(opts.Reasoning.Summary, "auto"),
		ParallelToolCalls: true,
		Redactor:          debugRedactor(opts.Debug),
	})
}

func newCodexModel(modelSpec corellm.ModelSpec, opts ModelOptions) (llmagent.Model, error) {
	return codex.New(codex.Config{
		Model:             string(modelSpec.Ref.Name),
		Runtime:           openai.DefaultResponsesRuntimeConfig(),
		Pricing:           modelSpec.Pricing,
		ReasoningEffort:   opts.Reasoning.Effort,
		ReasoningSummary:  firstNonEmptyString(opts.Reasoning.Summary, "auto"),
		ParallelToolCalls: true,
		Redactor:          debugRedactor(opts.Debug),
	})
}

func newOpenRouterModel(modelSpec corellm.ModelSpec, opts ModelOptions) (llmagent.Model, error) {
	if !modelcatalog.SupportsAPI(modelSpec, string(modeldb.APITypeOpenAIResponses)) {
		return nil, fmt.Errorf("openrouter model %q does not expose OpenAI Responses in modeldb", modelSpec.Ref.Name)
	}
	return openrouter.New(openrouter.Config{
		Model:             string(modelSpec.Ref.Name),
		Pricing:           modelSpec.Pricing,
		ReasoningEffort:   opts.Reasoning.Effort,
		ReasoningSummary:  opts.Reasoning.Summary,
		ParallelToolCalls: true,
		Redactor:          debugRedactor(opts.Debug),
	})
}

func newAnthropicModel(modelSpec corellm.ModelSpec, opts ModelOptions) (llmagent.Model, error) {
	if err := requireMessagesModel("anthropic", modelSpec); err != nil {
		return nil, err
	}
	return anthropic.New(anthropic.Config{
		Model:           string(modelSpec.Ref.Name),
		Pricing:         modelSpec.Pricing,
		MaxOutputTokens: maxOutputTokens(modelSpec),
		PromptCache:     modelSpec.Capabilities.Has(corellm.CapabilityPromptCaching),
		Thinking:        opts.Reasoning.Thinking,
		ReasoningEffort: opts.Reasoning.Effort,
		Redactor:        debugRedactor(opts.Debug),
	})
}

func newClaudeCodeModel(modelSpec corellm.ModelSpec, opts ModelOptions) (llmagent.Model, error) {
	if err := requireMessagesModel("claudecode", modelSpec); err != nil {
		return nil, err
	}
	return claudecode.New(claudecode.Config{
		Model:           string(modelSpec.Ref.Name),
		Pricing:         modelSpec.Pricing,
		MaxOutputTokens: maxOutputTokens(modelSpec),
		PromptCache:     modelSpec.Capabilities.Has(corellm.CapabilityPromptCaching),
		Thinking:        opts.Reasoning.Thinking,
		ReasoningEffort: opts.Reasoning.Effort,
		Redactor:        debugRedactor(opts.Debug),
	})
}

func newMinimaxModel(modelSpec corellm.ModelSpec, opts ModelOptions) (llmagent.Model, error) {
	if err := requireMessagesModel("minimax", modelSpec); err != nil {
		return nil, err
	}
	return minimax.New(minimax.Config{
		Model:           string(modelSpec.Ref.Name),
		Pricing:         modelSpec.Pricing,
		MaxOutputTokens: maxOutputTokens(modelSpec),
		PromptCache:     modelSpec.Capabilities.Has(corellm.CapabilityPromptCaching),
		Thinking:        opts.Reasoning.Thinking,
		ReasoningEffort: opts.Reasoning.Effort,
		Redactor:        debugRedactor(opts.Debug),
	})
}
