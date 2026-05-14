package llm

import (
	"fmt"
	"strings"
)

// ProviderSpec is an inert catalog for one LLM provider.
type ProviderSpec struct {
	Name        ProviderName      `json:"name" yaml:"name"`
	DisplayName string            `json:"display_name,omitempty" yaml:"display_name,omitempty"`
	Description string            `json:"description,omitempty" yaml:"description,omitempty"`
	Models      []ModelSpec       `json:"models,omitempty" yaml:"models,omitempty"`
	Annotations map[string]string `json:"annotations,omitempty" yaml:"annotations,omitempty"`
}

// Validate checks that provider and model identities are structurally useful.
func (s ProviderSpec) Validate() error {
	if strings.TrimSpace(string(s.Name)) == "" {
		return fmt.Errorf("llm: provider name is empty")
	}
	seen := map[ModelName]struct{}{}
	for i, model := range s.Models {
		if model.Ref.Provider == "" {
			model.Ref.Provider = s.Name
		}
		if model.Ref.Provider != s.Name {
			return fmt.Errorf("llm: models[%d] provider %q does not match %q", i, model.Ref.Provider, s.Name)
		}
		if _, ok := seen[model.Ref.Name]; ok {
			return fmt.Errorf("llm: duplicate model %q", model.Ref.Name)
		}
		seen[model.Ref.Name] = struct{}{}
		if err := model.Validate(); err != nil {
			return fmt.Errorf("llm: models[%d]: %w", i, err)
		}
	}
	return nil
}
