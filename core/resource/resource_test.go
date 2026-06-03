package resource

import (
	"testing"

	"github.com/fluxplane/fluxplane-core/core/activation"
	"github.com/fluxplane/fluxplane-core/core/reaction"
	"github.com/fluxplane/fluxplane-core/core/session"
	"github.com/fluxplane/fluxplane-datasource"
	"github.com/fluxplane/fluxplane-evidence"
	"github.com/fluxplane/fluxplane-operation"
)

func TestContributionBundleAppendObservationReactionFields(t *testing.T) {
	bundle := ContributionBundle{}
	bundle.Append(ContributionBundle{
		Observers: []evidence.ObserverSpec{{
			Name:  "kubernetes.context",
			Phase: evidence.PhaseTurn,
		}},
		AssertionDerivers: []evidence.AssertionDeriverSpec{{
			Name:             "kubernetes.assertions",
			ObservationKinds: []string{"kubernetes.context"},
		}},
		ActivationSets: []activation.Set{{
			Name: "incident.slack",
			Targets: []activation.Target{{
				Kind:         activation.TargetOperationSet,
				OperationSet: "slack.message_read",
			}},
		}},
		Reactions: []reaction.Rule{{
			Name: "kubernetes-available",
			When: reaction.Matcher{Assertion: "integration.available", Target: "kubernetes"},
			Actions: []reaction.Action{{
				Kind:       reaction.ActionEnableDatasource,
				Datasource: datasource.Ref{Name: "kubernetes"},
			}},
		}},
		PostEditChecks: []session.PostEditCheckSpec{{
			Name:      "golang.fmt",
			Operation: operation.Ref{Name: "go_fmt"},
		}},
	})
	if len(bundle.Observers) != 1 || bundle.Observers[0].Name != "kubernetes.context" {
		t.Fatalf("observers = %#v", bundle.Observers)
	}
	if len(bundle.AssertionDerivers) != 1 || bundle.AssertionDerivers[0].Name != "kubernetes.assertions" {
		t.Fatalf("assertion derivers = %#v", bundle.AssertionDerivers)
	}
	if len(bundle.ActivationSets) != 1 || bundle.ActivationSets[0].Name != "incident.slack" {
		t.Fatalf("activation sets = %#v", bundle.ActivationSets)
	}
	if len(bundle.Reactions) != 1 || bundle.Reactions[0].Name != "kubernetes-available" {
		t.Fatalf("reactions = %#v", bundle.Reactions)
	}
	if len(bundle.PostEditChecks) != 1 || bundle.PostEditChecks[0].Name != "golang.fmt" {
		t.Fatalf("post edit checks = %#v", bundle.PostEditChecks)
	}
}

func TestCloneContributionBundlesCopiesResourceSlices(t *testing.T) {
	original := []ContributionBundle{{
		Operations: []operation.Spec{{Ref: operation.Ref{Name: "sleep"}}},
		Observers:  []evidence.ObserverSpec{{Name: "kubernetes.context"}},
		Reactions:  []reaction.Rule{{Name: "kubernetes.available"}},
	}}

	cloned := CloneContributionBundles(original)
	cloned[0].Operations[0].Ref.Name = "changed"
	cloned[0].Observers[0].Name = "changed"
	cloned[0].Reactions[0].Name = "changed"

	if original[0].Operations[0].Ref.Name != "sleep" {
		t.Fatalf("operation mutated original: %#v", original[0].Operations)
	}
	if original[0].Observers[0].Name != "kubernetes.context" {
		t.Fatalf("observer mutated original: %#v", original[0].Observers)
	}
	if original[0].Reactions[0].Name != "kubernetes.available" {
		t.Fatalf("reaction mutated original: %#v", original[0].Reactions)
	}
}
