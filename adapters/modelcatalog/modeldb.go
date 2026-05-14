// Package modelcatalog bridges codewandler/modeldb into agentsdk's inert LLM
// provider catalog types.
package modelcatalog

import (
	"sort"
	"strings"

	"github.com/codewandler/modeldb"
	corellm "github.com/fluxplane/agentruntime/core/llm"
	"github.com/fluxplane/agentruntime/core/usage"
)

// ProviderProjection selects the provider-visible slice of a modeldb catalog.
type ProviderProjection struct {
	ServiceID string
	APIType   modeldb.APIType
	ModelIDs  []string
}

// BuiltIn loads the embedded modeldb catalog and converts one entry per
// service/API exposure into agentsdk provider specs.
func BuiltIn() ([]corellm.ProviderSpec, error) {
	catalog, err := modeldb.LoadBuiltIn()
	if err != nil {
		return nil, err
	}
	return FromModelDB(catalog), nil
}

// BuiltInProvider projects one provider from the embedded modeldb catalog.
func BuiltInProvider(projection ProviderProjection) (corellm.ProviderSpec, bool, error) {
	catalog, err := modeldb.LoadBuiltIn()
	if err != nil {
		return corellm.ProviderSpec{}, false, err
	}
	spec, ok := ProjectProvider(catalog, projection)
	return spec, ok, nil
}

// ProjectProvider converts the selected modeldb service view into one provider
// spec. ModelIDs is a provider-local allowlist; when empty all visible models
// matching APIType are included.
func ProjectProvider(catalog modeldb.Catalog, projection ProviderProjection) (corellm.ProviderSpec, bool) {
	serviceID := strings.TrimSpace(projection.ServiceID)
	if serviceID == "" {
		return corellm.ProviderSpec{}, false
	}
	allowed := stringSet(projection.ModelIDs)
	items := modeldb.ServiceView(catalog, serviceID, modeldb.ViewOptions{VisibleOnly: true}).List()
	spec := corellm.ProviderSpec{Name: corellm.ProviderName(serviceID), DisplayName: serviceID}
	if service, ok := catalog.Services[serviceID]; ok {
		spec.DisplayName = firstNonEmpty(service.Name, service.ID)
		spec.Description = service.DocsURL
	}
	for _, item := range items {
		if item.Model.Deprecated {
			continue
		}
		if projection.APIType != "" && !item.Offering.HasExposure(projection.APIType) {
			continue
		}
		if len(allowed) > 0 && !allowed[item.Offering.WireModelID] {
			continue
		}
		spec.Models = append(spec.Models, modelSpec(item.Model, item.Offering))
	}
	sort.Slice(spec.Models, func(i, j int) bool {
		return spec.Models[i].Ref.Name < spec.Models[j].Ref.Name
	})
	return spec, true
}

// FromModelDB converts modeldb service offerings into agentsdk provider specs.
func FromModelDB(catalog modeldb.Catalog) []corellm.ProviderSpec {
	providers := map[string]*corellm.ProviderSpec{}
	for _, service := range catalog.Services {
		spec := provider(providers, service.ID)
		spec.DisplayName = firstNonEmpty(service.Name, service.ID)
		spec.Description = service.DocsURL
	}
	for _, offering := range catalog.Offerings {
		model, ok := catalog.ModelByKey(offering.ModelKey)
		if !ok || model.Deprecated {
			continue
		}
		spec := provider(providers, offering.ServiceID)
		spec.Models = append(spec.Models, modelSpec(model, offering))
	}
	out := make([]corellm.ProviderSpec, 0, len(providers))
	for _, spec := range providers {
		sort.Slice(spec.Models, func(i, j int) bool {
			return spec.Models[i].Ref.Name < spec.Models[j].Ref.Name
		})
		out = append(out, *spec)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Find returns a provider/model entry from the built-in catalog.
func Find(providerName, modelName string) (corellm.ProviderSpec, corellm.ModelSpec, bool) {
	specs, err := BuiltIn()
	if err != nil {
		return corellm.ProviderSpec{}, corellm.ModelSpec{}, false
	}
	providerName = strings.TrimSpace(providerName)
	modelName = strings.TrimSpace(modelName)
	for _, provider := range specs {
		if string(provider.Name) != providerName {
			continue
		}
		for _, model := range provider.Models {
			if string(model.Ref.Name) == modelName {
				return provider, model, true
			}
		}
	}
	return corellm.ProviderSpec{}, corellm.ModelSpec{}, false
}

// SupportsAPI reports whether a catalog model exposes the named modeldb API
// type, for example "openai-responses".
func SupportsAPI(model corellm.ModelSpec, apiType string) bool {
	apiType = strings.TrimSpace(apiType)
	if apiType == "" {
		return false
	}
	for _, value := range strings.Split(model.Annotations["modeldb.api_types"], ",") {
		if strings.TrimSpace(value) == apiType {
			return true
		}
	}
	return false
}

func provider(providers map[string]*corellm.ProviderSpec, name string) *corellm.ProviderSpec {
	name = strings.TrimSpace(name)
	if name == "" {
		name = "unknown"
	}
	if current := providers[name]; current != nil {
		return current
	}
	spec := &corellm.ProviderSpec{Name: corellm.ProviderName(name), DisplayName: name}
	providers[name] = spec
	return spec
}

func stringSet(values []string) map[string]bool {
	out := map[string]bool{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			out[value] = true
		}
	}
	return out
}

func modelSpec(model modeldb.ModelRecord, offering modeldb.Offering) corellm.ModelSpec {
	limits := model.Limits
	if offering.LimitsOverride != nil {
		if offering.LimitsOverride.ContextWindow > 0 {
			limits.ContextWindow = offering.LimitsOverride.ContextWindow
		}
		if offering.LimitsOverride.MaxOutput > 0 {
			limits.MaxOutput = offering.LimitsOverride.MaxOutput
		}
	}
	annotations := map[string]string{
		"modeldb.model_key": modeldb.LineID(model.Key),
		"modeldb.api_types": strings.Join(apiTypes(offering.Exposures), ","),
	}
	addOpenAIResponsesAnnotations(annotations, offering.Exposures)
	addAnthropicMessagesAnnotations(annotations, offering.Exposures)
	return corellm.ModelSpec{
		Ref: corellm.ModelRef{
			Provider: corellm.ProviderName(offering.ServiceID),
			Name:     corellm.ModelName(offering.WireModelID),
		},
		DisplayName:      firstNonEmpty(model.Name, offering.WireModelID),
		Description:      model.Description,
		InputModalities:  modalities(model.InputModalities),
		OutputModalities: modalities(model.OutputModalities),
		ContextTokens:    int64(limits.ContextWindow),
		MaxOutputTokens:  int64(limits.MaxOutput),
		Capabilities:     capabilities(model.Capabilities, offering.Exposures),
		Pricing:          pricing(firstPricing(offering.Pricing, model.ReferencePricing)),
		Aliases:          modelAliases(model.Aliases, offering.Aliases),
		Annotations:      annotations,
	}
}

func modelAliases(values ...[]string) []corellm.ModelName {
	seen := map[string]bool{}
	var out []corellm.ModelName
	for _, group := range values {
		for _, value := range group {
			value = strings.TrimSpace(value)
			if value == "" || seen[value] {
				continue
			}
			seen[value] = true
			out = append(out, corellm.ModelName(value))
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func addAnthropicMessagesAnnotations(annotations map[string]string, exposures []modeldb.OfferingExposure) {
	for _, exposure := range exposures {
		if exposure.APIType != modeldb.APITypeAnthropicMessages {
			continue
		}
		if values := sortedValues(exposure.ParameterValues[string(modeldb.ParamReasoningEffort)]); len(values) > 0 {
			annotations["modeldb.anthropic_messages.reasoning_efforts"] = strings.Join(values, ",")
		}
		if values := sortedValues(exposure.ParameterValues[string(modeldb.ParamThinkingMode)]); len(values) > 0 {
			annotations["modeldb.anthropic_messages.thinking_modes"] = strings.Join(values, ",")
		}
		if exposure.SupportsParameter(modeldb.ParamCacheControl) || exposure.SupportsParameter(modeldb.ParamBlockCacheControl) || exposure.SupportsParameter(modeldb.ParamTopLevelCacheControl) {
			annotations["modeldb.anthropic_messages.cache_control"] = "true"
		}
		return
	}
}

func addOpenAIResponsesAnnotations(annotations map[string]string, exposures []modeldb.OfferingExposure) {
	for _, exposure := range exposures {
		if exposure.APIType != modeldb.APITypeOpenAIResponses {
			continue
		}
		if values := sortedValues(exposure.ParameterValues[string(modeldb.ParamReasoningEffort)]); len(values) > 0 {
			annotations["modeldb.openai_responses.reasoning_efforts"] = strings.Join(values, ",")
		}
		if values := sortedValues(exposure.ParameterValues[string(modeldb.ParamReasoningSummary)]); len(values) > 0 {
			annotations["modeldb.openai_responses.reasoning_summaries"] = strings.Join(values, ",")
		}
		return
	}
}

func sortedValues(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			out = append(out, strings.TrimSpace(value))
		}
	}
	sort.Strings(out)
	return out
}

func modalities(values []string) []corellm.Modality {
	out := make([]corellm.Modality, 0, len(values))
	for _, value := range values {
		switch strings.TrimSpace(value) {
		case "text":
			out = append(out, corellm.ModalityText)
		case "image":
			out = append(out, corellm.ModalityImage)
		case "audio":
			out = append(out, corellm.ModalityAudio)
		case "video":
			out = append(out, corellm.ModalityVideo)
		}
	}
	return out
}

func capabilities(modelCaps modeldb.Capabilities, exposures []modeldb.OfferingExposure) corellm.CapabilitySet {
	caps := map[corellm.Capability]bool{}
	addCaps(caps, modelCaps)
	for _, exposure := range exposures {
		if exposure.ExposedCapabilities != nil {
			addCaps(caps, *exposure.ExposedCapabilities)
		}
	}
	out := make(corellm.CapabilitySet, 0, len(caps))
	for cap := range caps {
		out = append(out, cap)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func addCaps(out map[corellm.Capability]bool, caps modeldb.Capabilities) {
	if caps.ToolUse {
		out[corellm.CapabilityToolCalling] = true
	}
	if caps.ParallelToolCalls {
		out[corellm.CapabilityParallelTools] = true
	}
	if caps.Streaming {
		out[corellm.CapabilityStreaming] = true
	}
	if caps.Reasoning != nil && caps.Reasoning.Available {
		out[corellm.CapabilityReasoning] = true
	}
	if caps.Caching != nil && caps.Caching.Available {
		out[corellm.CapabilityPromptCaching] = true
	}
	if caps.StructuredOutput || caps.StructuredOutputs {
		out[corellm.CapabilityStructuredJSON] = true
	}
	if caps.Vision {
		out[corellm.CapabilityVision] = true
	}
	if caps.WebSearch {
		out[corellm.CapabilityWebSearch] = true
	}
}

func firstPricing(values ...*modeldb.Pricing) *modeldb.Pricing {
	for _, value := range values {
		if value != nil {
			return value
		}
	}
	return nil
}

func pricing(price *modeldb.Pricing) []corellm.PricingSpec {
	if price == nil {
		return nil
	}
	const perMillion = 1000000
	var out []corellm.PricingSpec
	add := func(metric usage.MetricName, direction usage.Direction, value float64) {
		if value <= 0 {
			return
		}
		out = append(out, corellm.PricingSpec{
			Metric:    metric,
			Unit:      usage.UnitToken,
			Direction: direction,
			Currency:  "USD",
			Price:     value,
			Per:       perMillion,
		})
	}
	add(usage.MetricLLMInputTokens, usage.DirectionInput, price.Input)
	add(usage.MetricLLMCachedTokens, usage.DirectionCached, price.CachedInput)
	add(usage.MetricLLMOutputTokens, usage.DirectionOutput, price.Output)
	add(usage.MetricLLMReasoningTokens, usage.DirectionOutput, price.Reasoning)
	return out
}

func apiTypes(exposures []modeldb.OfferingExposure) []string {
	out := make([]string, 0, len(exposures))
	for _, exposure := range exposures {
		if exposure.APIType != "" {
			out = append(out, string(exposure.APIType))
		}
	}
	sort.Strings(out)
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
