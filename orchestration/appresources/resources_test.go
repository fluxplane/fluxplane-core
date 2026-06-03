package appresources

import (
	"strings"
	"testing"

	"github.com/fluxplane/fluxplane-core/core/activation"
	"github.com/fluxplane/fluxplane-core/core/resource"
	"github.com/fluxplane/fluxplane-operation"
)

func TestCollectExposesActivationSets(t *testing.T) {
	index := resource.NewResourceIndex()
	resources, diag, err := Collect(Config{
		Bundles: []resource.ContributionBundle{{
			Source: resource.SourceRef{Scope: resource.ScopeProject},
			ActivationSets: []activation.Set{{
				Name: "assistant.local_editing",
				Targets: []activation.Target{{
					Kind:      activation.TargetOperation,
					Operation: operation.Ref{Name: "file_read"},
				}},
			}},
		}},
		Index: index,
	})
	if err != nil {
		t.Fatalf("Collect() error = %v diag = %#v", err, diag)
	}
	if len(resources.ActivationSets) != 1 || resources.ActivationSets[0].Name != "assistant.local_editing" {
		t.Fatalf("ActivationSets = %#v", resources.ActivationSets)
	}
	if _, ok := resources.ActivationSetCatalog["local:assistant.local_editing"]; !ok {
		t.Fatalf("ActivationSetCatalog = %#v, missing local:assistant.local_editing", resources.ActivationSetCatalog)
	}
}

func TestCollectRejectsInvalidActivationSet(t *testing.T) {
	_, diag, err := Collect(Config{
		Bundles: []resource.ContributionBundle{{
			Source: resource.SourceRef{Scope: resource.ScopeProject},
			ActivationSets: []activation.Set{{
				Name: "bad",
			}},
		}},
		Index: resource.NewResourceIndex(),
	})
	if err == nil {
		t.Fatal("Collect() error = nil, want invalid activation set error")
	}
	if diag.Severity != resource.SeverityError || !strings.Contains(diag.Message, "activation_set spec") {
		t.Fatalf("diagnostic = %#v", diag)
	}
}
