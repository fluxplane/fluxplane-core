package echo

import (
	"context"
	"testing"

	"github.com/fluxplane/engine/core/operation"
	"github.com/fluxplane/engine/core/resource"
	appcomposition "github.com/fluxplane/engine/orchestration/app"
	"github.com/fluxplane/engine/orchestration/pluginhost"
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
