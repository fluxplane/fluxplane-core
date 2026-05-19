package llm

import (
	"fmt"
	"strings"
)

// ProviderName identifies one LLM provider catalog.
type ProviderName string

// ProviderRef references an LLM provider.
type ProviderRef struct {
	Name ProviderName `json:"name" yaml:"name"`
}

// ModelName identifies a model within a provider catalog.
type ModelName string

// ModelRef references a concrete provider model.
type ModelRef struct {
	Provider ProviderName `json:"provider,omitempty" yaml:"provider,omitempty"`
	Name     ModelName    `json:"name" yaml:"name"`
}

// String returns the provider-qualified model reference when both components
// are available.
func (r ModelRef) String() string {
	provider := strings.TrimSpace(string(r.Provider))
	name := strings.TrimSpace(string(r.Name))
	if provider == "" {
		return name
	}
	if name == "" {
		return provider
	}
	return provider + "/" + name
}

// ParseModelRef parses a provider-qualified model reference. The model portion
// may itself contain slashes, as with OpenRouter model IDs.
func ParseModelRef(raw string) (ModelRef, error) {
	raw = strings.TrimSpace(raw)
	provider, model, ok := strings.Cut(raw, "/")
	provider = strings.TrimSpace(provider)
	model = strings.TrimSpace(model)
	if !ok || provider == "" || model == "" {
		return ModelRef{}, fmt.Errorf("llm: model ref %q must be <provider>/<model>", raw)
	}
	return ModelRef{Provider: ProviderName(provider), Name: ModelName(model)}, nil
}

// Modality describes supported input or output modalities.
type Modality string

const (
	ModalityText  Modality = "text"
	ModalityImage Modality = "image"
	ModalityAudio Modality = "audio"
	ModalityVideo Modality = "video"
)

// ModelSpec is an inert provider model catalog entry.
type ModelSpec struct {
	Ref              ModelRef          `json:"ref" yaml:"ref"`
	DisplayName      string            `json:"display_name,omitempty" yaml:"display_name,omitempty"`
	Description      string            `json:"description,omitempty" yaml:"description,omitempty"`
	InputModalities  []Modality        `json:"input_modalities,omitempty" yaml:"input_modalities,omitempty"`
	OutputModalities []Modality        `json:"output_modalities,omitempty" yaml:"output_modalities,omitempty"`
	ContextTokens    int64             `json:"context_tokens,omitempty" yaml:"context_tokens,omitempty"`
	MaxOutputTokens  int64             `json:"max_output_tokens,omitempty" yaml:"max_output_tokens,omitempty"`
	Capabilities     CapabilitySet     `json:"capabilities,omitempty" yaml:"capabilities,omitempty"`
	Pricing          []PricingSpec     `json:"pricing,omitempty" yaml:"pricing,omitempty"`
	Aliases          []ModelName       `json:"aliases,omitempty" yaml:"aliases,omitempty"`
	Params           ModelParams       `json:"params,omitempty" yaml:"params,omitempty"`
	Annotations      map[string]string `json:"annotations,omitempty" yaml:"annotations,omitempty"`
}

// ModelParams declares provider-neutral default model-call parameters for a
// concrete model catalog entry.
type ModelParams struct {
	Thinking        string `json:"thinking,omitempty" yaml:"thinking,omitempty"`
	ReasoningEffort string `json:"reasoning_effort,omitempty" yaml:"reasoning_effort,omitempty"`
}

// Validate checks that the model has a stable provider-local identity.
func (s ModelSpec) Validate() error {
	if strings.TrimSpace(string(s.Ref.Name)) == "" {
		return fmt.Errorf("llm: model name is empty")
	}
	if s.ContextTokens < 0 {
		return fmt.Errorf("llm: context_tokens must be >= 0")
	}
	if s.MaxOutputTokens < 0 {
		return fmt.Errorf("llm: max_output_tokens must be >= 0")
	}
	for i, alias := range s.Aliases {
		if strings.TrimSpace(string(alias)) == "" {
			return fmt.Errorf("llm: aliases[%d] is empty", i)
		}
	}
	for i, price := range s.Pricing {
		if err := price.Validate(); err != nil {
			return fmt.Errorf("llm: pricing[%d]: %w", i, err)
		}
	}
	return nil
}
