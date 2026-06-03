package fluxplane

import (
	"context"
	"testing"

	"github.com/fluxplane/fluxplane-core/core/agent"
	"github.com/fluxplane/fluxplane-core/orchestration/agentfactory"
	"github.com/fluxplane/fluxplane-core/orchestration/session"
	llmagent "github.com/fluxplane/fluxplane-core/runtime/agent/llmagent"
	"github.com/fluxplane/fluxplane-operation"
)

func TestResolverStopEvaluatorUsesParentAgentInference(t *testing.T) {
	var resolved agent.Spec
	resolver := agentfactory.ModelResolverFunc(func(_ context.Context, spec agent.Spec) (llmagent.Model, error) {
		resolved = spec
		return llmagent.ModelFunc(func(_ context.Context, _ llmagent.Request) (llmagent.Response, error) {
			return llmagent.Response{Operations: []agent.OperationRequest{{
				Operation: operation.Ref{Name: "continuation_decision"},
				Input:     session.StopEvaluation{Action: session.StopActionStop, Reason: "done"},
			}}}, nil
		}), nil
	})

	_, err := (resolverStopEvaluator{resolver: resolver}).EvaluateStopCondition(context.Background(), session.StopEvaluationInput{
		Agent: agent.Spec{
			Name:      "assistant",
			Inference: agent.InferenceSpec{Model: "expensive-model", Thinking: "enabled"},
		},
		Condition: agent.StopConditionSpec{Type: "prompt", Prompt: "Stop when complete."},
	})
	if err != nil {
		t.Fatalf("EvaluateStopCondition: %v", err)
	}
	if resolved.Inference.Model != "expensive-model" {
		t.Fatalf("resolved model = %q, want parent model", resolved.Inference.Model)
	}
	if resolved.Inference.Thinking != "enabled" {
		t.Fatalf("resolved inference = %#v, want parent inference unchanged", resolved.Inference)
	}
}
