package golang

import (
	"testing"

	"github.com/fluxplane/fluxplane-core/core/language"
)

func TestNavigationModels(t *testing.T) {
	result := NavigationResult{
		Target: NavigationTarget{
			Text:     "Run",
			NodeKind: "ident",
			Location: language.Location{Path: "service.go", Range: language.Range{
				Start: language.Position{Line: 10, Column: 5},
				End:   language.Position{Line: 10, Column: 8},
			}},
		},
		Symbols:        []language.Symbol{{Kind: language.SymbolMethod, Name: "Service.Run", Language: language.LanguageGo}},
		ResolutionMode: "ast",
		Warnings:       []string{"no type checking"},
	}
	if result.Target.Location.Path != "service.go" || result.Symbols[0].Kind != language.SymbolMethod {
		t.Fatalf("navigation result = %#v", result)
	}
	if NavigationScopePackage != "package" || DefinitionOp != "go_definition" || SymbolInfoOp != "go_symbol_info" {
		t.Fatalf("navigation constants changed")
	}
}
