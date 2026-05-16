package coder

import (
	"testing"

	"github.com/fluxplane/agentruntime/core/resource"
	"github.com/fluxplane/agentruntime/orchestration/app"
	"github.com/fluxplane/agentruntime/orchestration/pluginhost"
	"github.com/fluxplane/agentruntime/plugins/codingplugin"
	"github.com/fluxplane/agentruntime/plugins/imageplugin"
	"github.com/fluxplane/agentruntime/plugins/planexecplugin"
	"github.com/fluxplane/agentruntime/plugins/skillplugin"
	"github.com/fluxplane/agentruntime/runtime/system"
)

func TestBundleComposes(t *testing.T) {
	sys, err := system.NewHost(system.Config{Root: t.TempDir(), AllowPrivateNetwork: true})
	if err != nil {
		t.Fatalf("NewHost: %v", err)
	}
	composition, err := app.Compose(app.Config{
		Bundles: []resource.ContributionBundle{Bundle()},
		Plugins: []pluginhost.Plugin{codingplugin.New(sys), planexecplugin.New(), skillplugin.New(), imageplugin.New(sys)},
	})
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	if len(composition.AgentSpecs) != 3 {
		t.Fatalf("agent specs len = %d, want 3", len(composition.AgentSpecs))
	}
	if got := composition.AgentSpecs[0].Turns.MaxSteps; got != 50 {
		t.Fatalf("max steps = %d, want 50", got)
	}
	if len(composition.OperationSpecs) != 59 {
		t.Fatalf("operation specs len = %d, want 59", len(composition.OperationSpecs))
	}
	session := composition.SessionSpecs[0]
	if len(session.Delegation.Commands) != 0 {
		t.Fatalf("delegation commands len = %d, want 0", len(session.Delegation.Commands))
	}
	if len(session.Delegation.Operations) == 0 {
		t.Fatal("delegation operations len = 0, want child operation caps")
	}
	worker := composition.AgentSpecs[1]
	if len(worker.Commands) != 0 {
		t.Fatalf("worker commands len = %d, want 0", len(worker.Commands))
	}
	if len(worker.Operations) == 0 {
		t.Fatal("worker operations len = 0, want operation-projected tools")
	}
}
