package llm

import "testing"

func TestModelRefString(t *testing.T) {
	cases := []struct {
		ref  ModelRef
		want string
	}{
		{ModelRef{Provider: "openai", Name: "gpt-4"}, "openai/gpt-4"},
		{ModelRef{Provider: "", Name: "gpt-4"}, "gpt-4"},
		{ModelRef{Provider: "openai", Name: ""}, "openai"},
		{ModelRef{}, ""},
	}
	for _, tc := range cases {
		got := tc.ref.String()
		if got != tc.want {
			t.Errorf("ModelRef{%q,%q}.String() = %q, want %q", tc.ref.Provider, tc.ref.Name, got, tc.want)
		}
	}
}

func TestModelSpecValidateMissingName(t *testing.T) {
	s := ModelSpec{Ref: ModelRef{Name: ""}}
	if err := s.Validate(); err == nil {
		t.Fatal("Validate with empty name should fail")
	}
}

func TestModelSpecValidateNegativeContext(t *testing.T) {
	s := ModelSpec{Ref: ModelRef{Name: "m"}, ContextTokens: -1}
	if err := s.Validate(); err == nil {
		t.Fatal("Validate with negative ContextTokens should fail")
	}
}

func TestModelSpecValidateNegativeOutput(t *testing.T) {
	s := ModelSpec{Ref: ModelRef{Name: "m"}, MaxOutputTokens: -1}
	if err := s.Validate(); err == nil {
		t.Fatal("Validate with negative MaxOutputTokens should fail")
	}
}

func TestModelSpecValidateEmptyAlias(t *testing.T) {
	s := ModelSpec{Ref: ModelRef{Name: "m"}, Aliases: []ModelName{""}}
	if err := s.Validate(); err == nil {
		t.Fatal("Validate with empty alias should fail")
	}
}

func TestModelSpecValidateBadPricing(t *testing.T) {
	s := ModelSpec{
		Ref:     ModelRef{Name: "m"},
		Pricing: []PricingSpec{{Metric: "", Unit: "token", Currency: "USD", Per: 1}},
	}
	if err := s.Validate(); err == nil {
		t.Fatal("Validate with bad pricing should fail")
	}
}

func TestPricingSpecValidate(t *testing.T) {
	valid := PricingSpec{Metric: "llm.tokens", Unit: "token", Currency: "USD", Price: 0.01, Per: 1000}
	if err := valid.Validate(); err != nil {
		t.Fatalf("Validate valid pricing: %v", err)
	}
}

func TestPricingSpecValidateMissingFields(t *testing.T) {
	cases := []struct {
		name string
		spec PricingSpec
	}{
		{"empty metric", PricingSpec{Unit: "token", Currency: "USD", Per: 1}},
		{"empty unit", PricingSpec{Metric: "m", Currency: "USD", Per: 1}},
		{"empty currency", PricingSpec{Metric: "m", Unit: "token", Per: 1}},
		{"zero per", PricingSpec{Metric: "m", Unit: "token", Currency: "USD", Per: 0}},
		{"negative price", PricingSpec{Metric: "m", Unit: "token", Currency: "USD", Per: 1, Price: -1}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.spec.Validate(); err == nil {
				t.Fatalf("Validate(%s) should fail", tc.name)
			}
		})
	}
}

func TestModelAliasSpecValidateMissingName(t *testing.T) {
	s := ModelAliasSpec{Name: "", Target: ModelRef{Provider: "openai", Name: "gpt-4"}}
	if err := s.Validate(); err == nil {
		t.Fatal("Validate with empty alias name should fail")
	}
}

func TestModelAliasSpecValidateMissingTargetProvider(t *testing.T) {
	s := ModelAliasSpec{Name: "alias", Target: ModelRef{Provider: "", Name: "gpt-4"}}
	if err := s.Validate(); err == nil {
		t.Fatal("Validate with empty target provider should fail")
	}
}

func TestModelAliasSpecValidateMissingTargetModel(t *testing.T) {
	s := ModelAliasSpec{Name: "alias", Target: ModelRef{Provider: "openai", Name: ""}}
	if err := s.Validate(); err == nil {
		t.Fatal("Validate with empty target model should fail")
	}
}

func TestNewModelAliasSpecInvalidTarget(t *testing.T) {
	_, err := NewModelAliasSpec("alias", "no-slash")
	if err == nil {
		t.Fatal("NewModelAliasSpec with invalid target should fail")
	}
}

func TestCatalogHasProvider(t *testing.T) {
	cat, err := NewProviderCatalog()
	if err != nil {
		t.Fatalf("NewProviderCatalog: %v", err)
	}
	if cat.HasProvider("openai") {
		t.Fatal("HasProvider should be false before registration")
	}
	if err := cat.Register(ProviderSpec{Name: "openai"}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if !cat.HasProvider("openai") {
		t.Fatal("HasProvider should be true after registration")
	}
}

func TestCatalogProviders(t *testing.T) {
	cat, _ := NewProviderCatalog()
	_ = cat.Register(ProviderSpec{Name: "openai"})
	_ = cat.Register(ProviderSpec{Name: "anthropic"})
	all := cat.Providers()
	if len(all) != 2 {
		t.Fatalf("Providers() len = %d, want 2", len(all))
	}
}

func TestCatalogDuplicateProviderFails(t *testing.T) {
	cat, _ := NewProviderCatalog()
	_ = cat.Register(ProviderSpec{Name: "openai"})
	if err := cat.Register(ProviderSpec{Name: "openai"}); err == nil {
		t.Fatal("Registering duplicate provider should fail")
	}
}
