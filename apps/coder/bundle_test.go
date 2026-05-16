package coder

import (
	"testing"

	"github.com/fluxplane/agentruntime/core/agent"
	coredatasource "github.com/fluxplane/agentruntime/core/datasource"
	"github.com/fluxplane/agentruntime/core/operation"
	"github.com/fluxplane/agentruntime/core/resource"
	"github.com/fluxplane/agentruntime/orchestration/app"
	"github.com/fluxplane/agentruntime/orchestration/pluginhost"
	"github.com/fluxplane/agentruntime/plugins/codingplugin"
	"github.com/fluxplane/agentruntime/plugins/datasourceplugin"
	"github.com/fluxplane/agentruntime/plugins/imageplugin"
	"github.com/fluxplane/agentruntime/plugins/planexecplugin"
	"github.com/fluxplane/agentruntime/plugins/skillplugin"
	"github.com/fluxplane/agentruntime/plugins/webplugin"
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
	if len(composition.OperationSpecs) != 65 {
		t.Fatalf("operation specs len = %d, want 65", len(composition.OperationSpecs))
	}
	if !agentHasOperation(composition.AgentSpecs[0], webplugin.SearchOp) {
		t.Fatalf("coder agent operations missing %s", webplugin.SearchOp)
	}
	for _, name := range []string{datasourceplugin.SearchOperation, datasourceplugin.GetOperation, datasourceplugin.BatchGetOperation} {
		if !agentHasOperation(composition.AgentSpecs[0], name) {
			t.Fatalf("coder agent operations missing %s", name)
		}
	}
	if !agentHasDatasource(composition.AgentSpecs[0], "web_search") {
		t.Fatalf("coder agent datasources = %#v, want web_search", composition.AgentSpecs[0].Datasources)
	}
	if !hasDatasourceSpec(composition.DatasourceSpecs, "web_search", "web_search") {
		t.Fatalf("datasource specs = %#v, want web_search", composition.DatasourceSpecs)
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

func agentHasOperation(spec agent.Spec, name string) bool {
	for _, ref := range spec.Operations {
		if ref.Name == operation.Name(name) {
			return true
		}
	}
	return false
}

func agentHasDatasource(spec agent.Spec, name string) bool {
	for _, ref := range spec.Datasources {
		if ref.Name == coredatasource.Name(name) {
			return true
		}
	}
	return false
}

func hasDatasourceSpec(specs []coredatasource.Spec, name, kind string) bool {
	for _, spec := range specs {
		if spec.Name == coredatasource.Name(name) && spec.Kind == kind {
			return true
		}
	}
	return false
}
