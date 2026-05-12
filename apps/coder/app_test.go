package coder

import (
	"testing"

	"github.com/fluxplane/agentruntime/core/operation"
	"github.com/fluxplane/agentruntime/core/resource"
	"github.com/fluxplane/agentruntime/orchestration/app"
)

func TestBundleComposes(t *testing.T) {
	composition, err := app.Compose(app.Config{
		Bundles: []resource.ContributionBundle{Bundle()},
		Operations: []operation.Operation{
			operation.New(ShellSpec(), func(operation.Context, operation.Value) operation.Result { return operation.OK(nil) }),
			operation.New(HTTPRequestSpec(), func(operation.Context, operation.Value) operation.Result { return operation.OK(nil) }),
		},
	})
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	if len(composition.AgentSpecs) != 1 {
		t.Fatalf("agent specs len = %d, want 1", len(composition.AgentSpecs))
	}
	if len(composition.OperationSpecs) != 2 {
		t.Fatalf("operation specs len = %d, want 2", len(composition.OperationSpecs))
	}
}
