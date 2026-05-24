package appconfig

import (
	"strings"
	"testing"

	"github.com/fluxplane/engine/core/activation"
	"github.com/fluxplane/engine/core/agent"
	corecontext "github.com/fluxplane/engine/core/context"
	coredatasource "github.com/fluxplane/engine/core/datasource"
	"github.com/fluxplane/engine/core/operation"
	"github.com/fluxplane/engine/core/resource"
	coreskill "github.com/fluxplane/engine/core/skill"
)

func TestNormalizeBundleExpandsAgentUsesThroughActivationSets(t *testing.T) {
	bundle := resource.ContributionBundle{
		Agents: []agent.Spec{{
			Name:           "support",
			ActivationSets: []string{"support.default", "datasource"},
			Operations:     []operation.Ref{{Name: "notify"}},
		}},
		Datasources: []coredatasource.Spec{{Name: "slack"}, {Name: "docs"}},
	}
	contributions := []resource.ContributionBundle{{
		ActivationSets: []activation.Set{{
			Name:    "support",
			Aliases: []string{"support.default"},
			Targets: []activation.Target{{
				Kind:      activation.TargetOperation,
				Operation: operation.Ref{Name: "notify"},
			}, {
				Kind:         activation.TargetOperationSet,
				OperationSet: "search",
			}, {
				Kind:            activation.TargetContextProvider,
				ContextProvider: corecontext.ProviderRef{Name: "identity.current"},
			}, {
				Kind:       activation.TargetDatasource,
				Datasource: coredatasource.Ref{Name: "docs"},
			}, {
				Kind:  activation.TargetSkill,
				Skill: coreskill.Ref{Name: "triage"},
			}},
		}, {
			Name: "datasource",
			Targets: []activation.Target{{
				Kind:         activation.TargetOperationSet,
				OperationSet: "datasource",
			}},
			Annotations: map[string]string{
				activation.AnnotationIncludeConfiguredDatasources: "true",
			},
		}},
		OperationSets: []operation.Set{{
			Name:       "search",
			Operations: []operation.Ref{{Name: "datasource_search"}, {Name: "datasource_*"}},
		}, {
			Name:       "datasource",
			Operations: []operation.Ref{{Name: "datasource_search"}, {Name: "datasource_get"}},
		}},
	}}

	got, err := NormalizeBundle(bundle, NormalizeOptions{ContributionBundles: contributions})
	if err != nil {
		t.Fatalf("NormalizeBundle: %v", err)
	}
	spec := got.Agents[0]
	for _, name := range []string{"notify", "datasource_search", "datasource_*", "datasource_get"} {
		if countOperationRef(spec, name) != 1 {
			t.Fatalf("agent operations = %#v, want one %s", spec.Operations, name)
		}
	}
	if countContextRef(spec, "identity.current") != 1 {
		t.Fatalf("agent context = %#v, want identity.current", spec.Context)
	}
	if countSkillRef(spec, "triage") != 1 {
		t.Fatalf("agent skills = %#v, want triage", spec.Skills)
	}
	for _, name := range []string{"slack", "docs"} {
		if countDatasourceRef(spec, name) != 1 {
			t.Fatalf("agent datasources = %#v, want one %s", spec.Datasources, name)
		}
	}
	if len(bundle.Agents[0].Datasources) != 0 || len(bundle.Agents[0].Context) != 0 {
		t.Fatalf("NormalizeBundle mutated input: %#v", bundle.Agents[0])
	}
}

func TestNormalizeBundleRejectsUnknownUses(t *testing.T) {
	_, err := NormalizeBundle(resource.ContributionBundle{
		Agents: []agent.Spec{{Name: "support", ActivationSets: []string{"missing"}}},
	}, NormalizeOptions{})
	if err == nil || !strings.Contains(err.Error(), `unknown activation set "missing"`) {
		t.Fatalf("NormalizeBundle error = %v, want unknown activation set", err)
	}
}

func TestNormalizeBundleAllowsAmbiguousAliasUntilUsed(t *testing.T) {
	contributions := []resource.ContributionBundle{{
		ActivationSets: []activation.Set{{
			Name:    "slack-work",
			Aliases: []string{"channel"},
			Targets: []activation.Target{{
				Kind:      activation.TargetOperation,
				Operation: operation.Ref{Name: "work_send"},
			}},
		}, {
			Name:    "slack-community",
			Aliases: []string{"channel"},
			Targets: []activation.Target{{
				Kind:      activation.TargetOperation,
				Operation: operation.Ref{Name: "community_send"},
			}},
		}},
	}}
	got, err := NormalizeBundle(resource.ContributionBundle{
		Agents: []agent.Spec{{Name: "support", ActivationSets: []string{"slack-work"}}},
	}, NormalizeOptions{ContributionBundles: contributions})
	if err != nil {
		t.Fatalf("NormalizeBundle explicit set: %v", err)
	}
	if countOperationRef(got.Agents[0], "work_send") != 1 {
		t.Fatalf("agent operations = %#v, want work_send", got.Agents[0].Operations)
	}
	_, err = NormalizeBundle(resource.ContributionBundle{
		Agents: []agent.Spec{{Name: "support", ActivationSets: []string{"channel"}}},
	}, NormalizeOptions{ContributionBundles: contributions})
	if err == nil || !strings.Contains(err.Error(), `activation set reference "channel" is ambiguous`) {
		t.Fatalf("NormalizeBundle ambiguous alias error = %v", err)
	}
}

func countOperationRef(spec agent.Spec, name string) int {
	var count int
	for _, ref := range spec.Operations {
		if string(ref.Name) == name {
			count++
		}
	}
	return count
}

func countContextRef(spec agent.Spec, name string) int {
	var count int
	for _, ref := range spec.Context {
		if string(ref.Name) == name {
			count++
		}
	}
	return count
}

func countDatasourceRef(spec agent.Spec, name string) int {
	var count int
	for _, ref := range spec.Datasources {
		if string(ref.Name) == name {
			count++
		}
	}
	return count
}

func countSkillRef(spec agent.Spec, name string) int {
	var count int
	for _, ref := range spec.Skills {
		if string(ref.Name) == name {
			count++
		}
	}
	return count
}
