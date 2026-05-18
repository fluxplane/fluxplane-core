package language

import (
	"testing"

	corelanguage "github.com/fluxplane/agentruntime/core/language"
	"github.com/fluxplane/agentruntime/core/operation"
	coreproject "github.com/fluxplane/agentruntime/core/project"
)

func TestActivatedOperationSetsRequiresProjectSignalAndToolchainStatus(t *testing.T) {
	support := StaticSupport{Spec: SupportSpec{
		Provider: corelanguage.ProviderSpec{Name: "go", Language: corelanguage.LanguageGo},
		Signals:  []SignalMatcher{{Language: corelanguage.LanguageGo}},
		OperationSets: []operation.Set{{
			Name:       "go.parser",
			Operations: []operation.Ref{{Name: "go_outline"}},
		}},
		ToolchainOperationSets: []operation.Set{{
			Name:       "go.toolchain",
			Operations: []operation.Ref{{Name: "go_test"}},
		}},
		Toolchains: []corelanguage.ToolchainSpec{{ID: "go"}},
	}}
	signals := []coreproject.Signal{{Language: corelanguage.LanguageGo}}

	parserOnly := ActivatedOperationSets([]Support{support}, signals, nil)
	if got, want := setNames(parserOnly), []string{"go.parser"}; !sameStrings(got, want) {
		t.Fatalf("parserOnly sets = %#v, want %#v", got, want)
	}

	withToolchain := ActivatedOperationSets([]Support{support}, signals, []corelanguage.ToolchainStatus{{ID: "go", Available: true}})
	if got, want := setNames(withToolchain), []string{"go.parser", "go.toolchain"}; !sameStrings(got, want) {
		t.Fatalf("withToolchain sets = %#v, want %#v", got, want)
	}

	withoutSignal := ActivatedOperationSets([]Support{support}, nil, []corelanguage.ToolchainStatus{{ID: "go", Available: true}})
	if len(withoutSignal) != 0 {
		t.Fatalf("withoutSignal sets = %#v, want none", setNames(withoutSignal))
	}
}

func setNames(sets []operation.Set) []string {
	out := make([]string, 0, len(sets))
	for _, set := range sets {
		out = append(out, set.Name)
	}
	return out
}

func sameStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
