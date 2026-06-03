package resourcecatalog

import (
	"strings"
	"testing"

	"github.com/fluxplane/fluxplane-core/core/activation"
	"github.com/fluxplane/fluxplane-core/core/resource"
	"github.com/fluxplane/fluxplane-operation"
)

func TestCollectIndexesActivationSets(t *testing.T) {
	index := resource.NewResourceIndex()
	catalogs, specs, diag, err := Collect([]resource.ContributionBundle{{
		Source: resource.SourceRef{ID: "incident", Scope: resource.ScopeProject},
		ActivationSets: []activation.Set{{
			Name: "incident.slack",
			Targets: []activation.Target{{
				Kind:      activation.TargetOperation,
				Operation: operation.Ref{Name: "slack_thread_read"},
			}},
		}},
	}}, index)
	if err != nil {
		t.Fatalf("Collect() error = %v diag = %#v", err, diag)
	}
	if len(specs.ActivationSets) != 1 || specs.ActivationSets[0].Name != "incident.slack" {
		t.Fatalf("ActivationSets = %#v", specs.ActivationSets)
	}
	binding, ok := catalogs.ActivationSetCatalog["local:incident.slack"]
	if !ok {
		t.Fatalf("ActivationSetCatalog keys = %#v, missing local:incident.slack", catalogs.ActivationSetCatalog)
	}
	if binding.ID.Kind != "activation_set" || binding.Spec.Name != "incident.slack" {
		t.Fatalf("binding = %#v", binding)
	}
	if got := index.Lookup("activation_set", "incident.slack"); len(got) != 1 {
		t.Fatalf("index lookup = %#v, want one activation_set", got)
	}
}

func TestCollectRejectsInvalidActivationSet(t *testing.T) {
	index := resource.NewResourceIndex()
	_, _, diag, err := Collect([]resource.ContributionBundle{{
		Source: resource.SourceRef{ID: "incident", Scope: resource.ScopeProject},
		ActivationSets: []activation.Set{{
			Name: "incident.empty",
		}},
	}}, index)
	if err == nil {
		t.Fatal("Collect() error = nil, want invalid activation set error")
	}
	if diag.Severity != resource.SeverityError || !strings.Contains(diag.Message, "activation_set spec") {
		t.Fatalf("diagnostic = %#v", diag)
	}
}

func TestCollectRejectsDuplicateActivationSet(t *testing.T) {
	index := resource.NewResourceIndex()
	_, _, diag, err := Collect([]resource.ContributionBundle{{
		Source: resource.SourceRef{ID: "a", Scope: resource.ScopeProject},
		ActivationSets: []activation.Set{{
			Name: "incident",
			Targets: []activation.Target{{
				Kind:      activation.TargetOperation,
				Operation: operation.Ref{Name: "op"},
			}},
		}},
	}, {
		Source: resource.SourceRef{ID: "b", Scope: resource.ScopeProject},
		ActivationSets: []activation.Set{{
			Name: "incident",
			Targets: []activation.Target{{
				Kind:      activation.TargetOperation,
				Operation: operation.Ref{Name: "op"},
			}},
		}},
	}}, index)
	if err == nil {
		t.Fatal("Collect() error = nil, want duplicate activation set error")
	}
	if diag.Severity != resource.SeverityError || !strings.Contains(diag.Message, "duplicate activation_set resource") {
		t.Fatalf("diagnostic = %#v", diag)
	}
}
