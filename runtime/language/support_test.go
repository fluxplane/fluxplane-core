package language

import (
	"testing"

	corelanguage "github.com/fluxplane/fluxplane-core/core/language"
	"github.com/fluxplane/fluxplane-core/core/operation"
)

func TestOperationSetsIncludesStaticAndToolchainSets(t *testing.T) {
	support := StaticSupport{Spec: SupportSpec{
		Provider: corelanguage.ProviderSpec{Name: "go", Language: corelanguage.LanguageGo},
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
	sets := OperationSets([]Support{support})
	if got, want := setNames(sets), []string{"go.parser", "go.toolchain"}; !sameStrings(got, want) {
		t.Fatalf("sets = %#v, want %#v", got, want)
	}
	toolchains := Toolchains([]Support{support})
	if len(toolchains) != 1 || toolchains[0].ID != "go" {
		t.Fatalf("toolchains = %#v, want go", toolchains)
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
