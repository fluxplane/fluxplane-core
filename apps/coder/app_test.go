package coder

import (
	"testing"

	"github.com/fluxplane/agentruntime/core/resource"
	"github.com/fluxplane/agentruntime/orchestration/app"
	"github.com/fluxplane/agentruntime/orchestration/pluginhost"
	"github.com/fluxplane/agentruntime/plugins/codingplugin"
	"github.com/fluxplane/agentruntime/runtime/system"
)

func TestBundleComposes(t *testing.T) {
	sys, err := system.NewHost(system.Config{Root: t.TempDir(), AllowPrivateNetwork: true})
	if err != nil {
		t.Fatalf("NewHost: %v", err)
	}
	composition, err := app.Compose(app.Config{
		Bundles: []resource.ContributionBundle{Bundle()},
		Plugins: []pluginhost.Plugin{codingplugin.New(sys)},
	})
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	if len(composition.AgentSpecs) != 1 {
		t.Fatalf("agent specs len = %d, want 1", len(composition.AgentSpecs))
	}
	if len(composition.OperationSpecs) != 38 {
		t.Fatalf("operation specs len = %d, want 38", len(composition.OperationSpecs))
	}
}
