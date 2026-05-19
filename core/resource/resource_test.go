package resource

import (
	"testing"

	"github.com/fluxplane/agentruntime/core/datasource"
	"github.com/fluxplane/agentruntime/core/environment"
	"github.com/fluxplane/agentruntime/core/operation"
	"github.com/fluxplane/agentruntime/core/reaction"
	"github.com/fluxplane/agentruntime/core/session"
)

func TestContributionBundleAppendObservationReactionFields(t *testing.T) {
	bundle := ContributionBundle{}
	bundle.Append(ContributionBundle{
		Observers: []environment.ObserverSpec{{
			Name:  "kubernetes.context",
			Phase: environment.PhaseTurn,
		}},
		SignalDerivers: []environment.SignalDeriverSpec{{
			Name:             "kubernetes.signals",
			ObservationKinds: []string{"kubernetes.context"},
		}},
		Reactions: []reaction.Rule{{
			Name: "kubernetes-available",
			When: reaction.Matcher{Signal: "integration.available", Target: "kubernetes"},
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
	if len(bundle.SignalDerivers) != 1 || bundle.SignalDerivers[0].Name != "kubernetes.signals" {
		t.Fatalf("signal derivers = %#v", bundle.SignalDerivers)
	}
	if len(bundle.Reactions) != 1 || bundle.Reactions[0].Name != "kubernetes-available" {
		t.Fatalf("reactions = %#v", bundle.Reactions)
	}
	if len(bundle.PostEditChecks) != 1 || bundle.PostEditChecks[0].Name != "golang.fmt" {
		t.Fatalf("post edit checks = %#v", bundle.PostEditChecks)
	}
}
