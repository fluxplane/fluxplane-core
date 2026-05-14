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
		Apps:         []coreapp.Spec{{Name: "dev"}},
		Agents:       []agent.Spec{{Name: "main"}},
		LLMProviders: []corellm.ProviderSpec{{Name: "openai"}},
		Skills:       []skill.Spec{{Name: "architecture"}},
		Workflows:    []workflow.Spec{{Name: "feature"}},
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
}
