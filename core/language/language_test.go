package language

import "testing"

func TestProviderSpecValidate(t *testing.T) {
	if err := (ProviderSpec{}).Validate(); err == nil {
		t.Fatal("Validate: want error for empty provider")
	}
	if err := (ProviderSpec{Name: "go", Language: LanguageGo}).Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestSharedSymbolKinds(t *testing.T) {
	s := Symbol{Kind: SymbolFunction, Name: "Run", Language: LanguageGo}
	if s.Kind != "function" || s.Language != "go" {
		t.Fatalf("symbol = %#v", s)
	}
}
