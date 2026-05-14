package llm

import (
	"fmt"
	"sort"
	"strings"
)

// ProviderCatalog is the provider/model source of truth assembled from
// contributed provider specs.
type ProviderCatalog struct {
	providers map[ProviderName]ProviderSpec
}

// NewProviderCatalog validates and indexes provider specs by provider name.
func NewProviderCatalog(specs ...ProviderSpec) (ProviderCatalog, error) {
	catalog := ProviderCatalog{providers: map[ProviderName]ProviderSpec{}}
	for _, spec := range specs {
		if err := catalog.Register(spec); err != nil {
			return ProviderCatalog{}, err
		}
	}
	return catalog, nil
}

// Register adds one provider spec to the catalog.
func (c *ProviderCatalog) Register(spec ProviderSpec) error {
	if c == nil {
		return fmt.Errorf("llm: provider catalog is nil")
	}
	if err := spec.Validate(); err != nil {
		return err
	}
	if c.providers == nil {
		c.providers = map[ProviderName]ProviderSpec{}
	}
	name := spec.Name
	if _, exists := c.providers[name]; exists {
		return fmt.Errorf("llm: duplicate provider %q", name)
	}
	c.providers[name] = normalizeProvider(spec)
	return nil
}

// Provider returns a provider spec by name.
func (c ProviderCatalog) Provider(name string) (ProviderSpec, bool) {
	name = strings.TrimSpace(name)
	if name == "" || c.providers == nil {
		return ProviderSpec{}, false
	}
	provider, ok := c.providers[ProviderName(name)]
	return provider, ok
}

// HasProvider reports whether a provider exists in the catalog.
func (c ProviderCatalog) HasProvider(name string) bool {
	_, ok := c.Provider(name)
	return ok
}

// Find returns a provider and model entry by provider-local model id.
func (c ProviderCatalog) Find(providerName, modelName string) (ProviderSpec, ModelSpec, bool) {
	provider, ok := c.Provider(providerName)
	if !ok {
		return ProviderSpec{}, ModelSpec{}, false
	}
	modelName = strings.TrimSpace(modelName)
	if modelName == "" {
		return ProviderSpec{}, ModelSpec{}, false
	}
	for _, model := range provider.Models {
		if string(model.Ref.Name) == modelName {
			return provider, model, true
		}
	}
	return ProviderSpec{}, ModelSpec{}, false
}

// Providers returns provider specs sorted by provider name.
func (c ProviderCatalog) Providers() []ProviderSpec {
	out := make([]ProviderSpec, 0, len(c.providers))
	for _, provider := range c.providers {
		out = append(out, provider)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func normalizeProvider(spec ProviderSpec) ProviderSpec {
	models := append([]ModelSpec(nil), spec.Models...)
	for i := range models {
		if models[i].Ref.Provider == "" {
			models[i].Ref.Provider = spec.Name
		}
	}
	spec.Models = models
	return spec
}
