package echoplugin

import (
	"context"
	"testing"

	"github.com/fluxplane/agentruntime/core/operation"
	"github.com/fluxplane/agentruntime/core/resource"
	appcomposition "github.com/fluxplane/agentruntime/orchestration/app"
	"github.com/fluxplane/agentruntime/orchestration/pluginhost"
)

func TestPluginComposesExecutableEchoCommand(t *testing.T) {
	composition, err := appcomposition.Compose(appcomposition.Config{
		Plugins: []pluginhost.Plugin{New()},
		Bundles: []resource.ContributionBundle{{
			Plugins: []resource.PluginRef{{Name: Name}},
		}},
	})
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	op, ok := composition.Operations.Resolve(operation.Ref{Name: "echo"})
	if !ok {
		t.Fatal("echo operation not registered")
	}
	result := op.Run(operation.NewContext(context.Background(), nil), "hello")
	if result.Status != operation.StatusOK || result.Output != "hello" {
		t.Fatalf("result = %#v", result)
	}
}
