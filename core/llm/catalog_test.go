package llm

import (
	"strings"
	"testing"
)

func TestProviderCatalogFindsProviderModel(t *testing.T) {
	catalog, err := NewProviderCatalog(ProviderSpec{
		Name: "openai",
		Models: []ModelSpec{{
			Ref: ModelRef{Name: "gpt-test"},
		}},
	})
	if err != nil {
		t.Fatalf("NewProviderCatalog: %v", err)
	}
	provider, model, ok := catalog.Find("openai", "gpt-test")
	if !ok {
		t.Fatalf("Find returned false")
	}
	if provider.Name != "openai" {
		t.Fatalf("provider = %q", provider.Name)
	}
	if model.Ref.Provider != "openai" || model.Ref.Name != "gpt-test" {
		t.Fatalf("model ref = %#v", model.Ref)
	}
}

func TestProviderCatalogRejectsDuplicateProvider(t *testing.T) {
	_, err := NewProviderCatalog(
		ProviderSpec{Name: "openai"},
		ProviderSpec{Name: "openai"},
	)
	if err == nil || !strings.Contains(err.Error(), "duplicate provider") {
		t.Fatalf("error = %v, want duplicate provider", err)
	}
}
