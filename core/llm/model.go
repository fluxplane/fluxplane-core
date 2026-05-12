package llm

import (
	"fmt"
	"strings"
)

// ProviderName identifies one LLM provider catalog.
type ProviderName string

// ProviderRef references an LLM provider.
type ProviderRef struct {
	Name ProviderName `json:"name"`
}

// ModelName identifies a model within a provider catalog.
type ModelName string

// ModelRef references a concrete provider model.
type ModelRef struct {
	Provider ProviderName `json:"provider,omitempty"`
	Name     ModelName    `json:"name"`
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
	Ref              ModelRef          `json:"ref"`
	DisplayName      string            `json:"display_name,omitempty"`
	Description      string            `json:"description,omitempty"`
	InputModalities  []Modality        `json:"input_modalities,omitempty"`
	OutputModalities []Modality        `json:"output_modalities,omitempty"`
	ContextTokens    int64             `json:"context_tokens,omitempty"`
	MaxOutputTokens  int64             `json:"max_output_tokens,omitempty"`
	Capabilities     CapabilitySet     `json:"capabilities,omitempty"`
	Pricing          []PricingSpec     `json:"pricing,omitempty"`
	Annotations      map[string]string `json:"annotations,omitempty"`
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
	for i, price := range s.Pricing {
		if err := price.Validate(); err != nil {
			return fmt.Errorf("llm: pricing[%d]: %w", i, err)
		}
	}
	return nil
}
