package skill

import (
	"testing"

	"github.com/fluxplane/fluxplane-core/core/agent"
	coreskill "github.com/fluxplane/fluxplane-core/core/skill"
	"github.com/fluxplane/fluxplane-core/core/tool"
)

func TestStatefulAgentForwardsStepWithTools(t *testing.T) {
	base := &toolAwareAgent{}
	repo, err := NewRepository([]coreskill.Spec{{Name: "coding", Description: "Coding skill."}})
	if err != nil {
		t.Fatalf("NewRepository: %v", err)
	}
	state, err := NewActivationState(repo, nil)
	if err != nil {
		t.Fatalf("NewActivationState: %v", err)
	}
	wrapped := WrapAgent(base, state)
	toolAgent, ok := wrapped.(interface {
		StepWithTools(agent.Context, agent.StepInput, []tool.Spec) agent.StepResult
	})
	if !ok {
		t.Fatalf("wrapped agent does not expose StepWithTools")
	}
	toolAgent.StepWithTools(nil, agent.StepInput{}, []tool.Spec{{Name: "shell_exec"}})
	if len(base.tools) != 1 || base.tools[0].Name != "shell_exec" {
		t.Fatalf("forwarded tools = %#v, want shell_exec", base.tools)
	}
}

type toolAwareAgent struct {
	tools []tool.Spec
}

func (a *toolAwareAgent) Spec() agent.Spec { return agent.Spec{Name: "assistant"} }

func (a *toolAwareAgent) Step(agent.Context, agent.StepInput) agent.StepResult {
	return agent.StepResult{Status: agent.StatusOK}
}

func (a *toolAwareAgent) StepWithTools(_ agent.Context, _ agent.StepInput, tools []tool.Spec) agent.StepResult {
	a.tools = append([]tool.Spec(nil), tools...)
	return agent.StepResult{Status: agent.StatusOK}
}
