package llm

import "testing"

func TestCatalogRegisterNil(t *testing.T) {
	var c *ProviderCatalog
	err := c.Register(ProviderSpec{Name: "openai"})
	if err == nil {
		t.Fatal("Register on nil catalog: want error")
	}
}

func TestCatalogProviderEmptyName(t *testing.T) {
	c, _ := NewProviderCatalog()
	_, ok := c.Provider("")
	if ok {
		t.Fatal("Provider(''): want false for empty name")
	}
}

func TestCatalogProviderNilMap(t *testing.T) {
	c := ProviderCatalog{} // providers is nil
	_, ok := c.Provider("openai")
	if ok {
		t.Fatal("Provider on empty catalog: want false")
	}
}

func TestCatalogFindMissingProvider(t *testing.T) {
	c, _ := NewProviderCatalog()
	_, _, ok := c.Find("ghost", "model")
	if ok {
		t.Fatal("Find(ghost,...): want false for missing provider")
	}
}

func TestCatalogFindEmptyModelName(t *testing.T) {
	c, _ := NewProviderCatalog()
	_ = c.Register(ProviderSpec{Name: "openai"})
	_, _, ok := c.Find("openai", "")
	if ok {
		t.Fatal("Find(openai,''): want false for empty model name")
	}
}

func TestCatalogFindMissingModel(t *testing.T) {
	c, _ := NewProviderCatalog()
	_ = c.Register(ProviderSpec{Name: "openai", Models: []ModelSpec{{Ref: ModelRef{Name: "gpt-4"}}}})
	_, _, ok := c.Find("openai", "gpt-3")
	if ok {
		t.Fatal("Find(openai,gpt-3): want false for missing model")
	}
}

func TestCatalogFindSuccess(t *testing.T) {
	c, _ := NewProviderCatalog()
	_ = c.Register(ProviderSpec{Name: "openai", Models: []ModelSpec{{Ref: ModelRef{Name: "gpt-4"}}}})
	provider, model, ok := c.Find("openai", "gpt-4")
	if !ok {
		t.Fatal("Find(openai,gpt-4): want true")
	}
	if provider.Name != "openai" {
		t.Fatalf("provider.Name = %q, want openai", provider.Name)
	}
	if model.Ref.Name != "gpt-4" {
		t.Fatalf("model.Ref.Name = %q, want gpt-4", model.Ref.Name)
	}
}

func TestCatalogRegisterInvalidSpec(t *testing.T) {
	c, _ := NewProviderCatalog()
	err := c.Register(ProviderSpec{Name: ""}) // empty name
	if err == nil {
		t.Fatal("Register(invalid): want error")
	}
}

func TestProviderSpecValidateDuplicateModel(t *testing.T) {
	spec := ProviderSpec{
		Name: "openai",
		Models: []ModelSpec{
			{Ref: ModelRef{Name: "gpt-4"}},
			{Ref: ModelRef{Name: "gpt-4"}},
		},
	}
	if err := spec.Validate(); err == nil {
		t.Fatal("ProviderSpec.Validate: want error for duplicate model")
	}
}

func TestProviderSpecValidateMismatchedProvider(t *testing.T) {
	spec := ProviderSpec{
		Name: "openai",
		Models: []ModelSpec{
			{Ref: ModelRef{Provider: "anthropic", Name: "claude"}},
		},
	}
	if err := spec.Validate(); err == nil {
		t.Fatal("ProviderSpec.Validate: want error for mismatched provider")
	}
}

func TestNormalizeProviderSetsRefProvider(t *testing.T) {
	spec := ProviderSpec{
		Name:   "openai",
		Models: []ModelSpec{{Ref: ModelRef{Name: "gpt-4"}}},
	}
	normalized := normalizeProvider(spec)
	if normalized.Models[0].Ref.Provider != "openai" {
		t.Fatalf("normalizeProvider: Ref.Provider = %q, want openai", normalized.Models[0].Ref.Provider)
	}
}
