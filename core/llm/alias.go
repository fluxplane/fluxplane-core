package llm

import (
	"fmt"
	"strings"
)

// ModelAliasSpec maps a user-facing model alias to a concrete provider model.
type ModelAliasSpec struct {
	Name   string   `json:"name" yaml:"name"`
	Target ModelRef `json:"target" yaml:"target"`
}

// NewModelAliasSpec parses a provider-qualified target into an alias spec.
func NewModelAliasSpec(name, target string) (ModelAliasSpec, error) {
	ref, err := ParseModelRef(target)
	if err != nil {
		return ModelAliasSpec{}, err
	}
	spec := ModelAliasSpec{Name: strings.TrimSpace(name), Target: ref}
	if err := spec.Validate(); err != nil {
		return ModelAliasSpec{}, err
	}
	return spec, nil
}

// Validate checks that the alias has a stable name and concrete target.
func (s ModelAliasSpec) Validate() error {
	if strings.TrimSpace(s.Name) == "" {
		return fmt.Errorf("llm: model alias name is empty")
	}
	if strings.TrimSpace(string(s.Target.Provider)) == "" {
		return fmt.Errorf("llm: model alias %q target provider is empty", s.Name)
	}
	if strings.TrimSpace(string(s.Target.Name)) == "" {
		return fmt.Errorf("llm: model alias %q target model is empty", s.Name)
	}
	return nil
}
