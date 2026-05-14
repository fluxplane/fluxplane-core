package resource

import (
	"testing"

	"github.com/fluxplane/agentruntime/core/agent"
	coreapp "github.com/fluxplane/agentruntime/core/app"
	corellm "github.com/fluxplane/agentruntime/core/llm"
	"github.com/fluxplane/agentruntime/core/skill"
	"github.com/fluxplane/agentruntime/core/workflow"
)

func TestContributionBundleAppendIncludesAppAndSkillSpecs(t *testing.T) {
	var bundle ContributionBundle
	bundle.Append(ContributionBundle{
		Apps:            []coreapp.Spec{{Name: "dev"}},
		Agents:          []agent.Spec{{Name: "main"}},
		LLMProviders:    []corellm.ProviderSpec{{Name: "openai"}},
		LLMModelAliases: []corellm.ModelAliasSpec{{Name: "codex", Target: corellm.ModelRef{Provider: "codex", Name: "gpt-5.5"}}},
		Skills:          []skill.Spec{{Name: "architecture"}},
		Workflows:       []workflow.Spec{{Name: "feature"}},
	})

	if len(bundle.Apps) != 1 || bundle.Apps[0].Name != "dev" {
		t.Fatalf("apps = %#v, want dev", bundle.Apps)
	}
	if len(bundle.Skills) != 1 || bundle.Skills[0].Name != "architecture" {
		t.Fatalf("skills = %#v, want architecture", bundle.Skills)
	}
	if len(bundle.LLMProviders) != 1 || bundle.LLMProviders[0].Name != "openai" {
		t.Fatalf("llm providers = %#v, want openai", bundle.LLMProviders)
	}
	if len(bundle.LLMModelAliases) != 1 || bundle.LLMModelAliases[0].Name != "codex" {
		t.Fatalf("llm model aliases = %#v, want codex", bundle.LLMModelAliases)
	}
}

func TestContributionBundleAppendMultiple(t *testing.T) {
	var bundle ContributionBundle
	bundle.Append(ContributionBundle{
		Apps: []coreapp.Spec{{Name: "app1"}},
	})
	bundle.Append(ContributionBundle{
		Apps: []coreapp.Spec{{Name: "app2"}},
	})

	if len(bundle.Apps) != 2 {
		t.Fatalf("apps = %d, want 2", len(bundle.Apps))
	}
}

func TestContributionBundleAppendEmpty(t *testing.T) {
	var bundle ContributionBundle
	bundle.Append(ContributionBundle{})

	if bundle.Apps != nil || bundle.Skills != nil {
		t.Fatal("empty append should not modify bundle")
	}
}
