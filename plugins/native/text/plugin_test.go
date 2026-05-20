package text

import (
	"context"
	"testing"

	"github.com/fluxplane/agentruntime/core/operation"
	"github.com/fluxplane/agentruntime/core/resource"
	"github.com/fluxplane/agentruntime/orchestration/pluginhost"
)

func TestPluginContributesConfiguredOperations(t *testing.T) {
	bundle, err := New().Contributions(context.Background(), pluginContext(resource.PluginRef{
		Name: Name,
		Config: map[string]any{
			"operations": []any{"upper", "trim"},
		},
	}))
	if err != nil {
		t.Fatalf("Contributions: %v", err)
	}
	if len(bundle.Commands) != 0 {
		t.Fatalf("commands len = %d, want 0", len(bundle.Commands))
	}
	if len(bundle.Operations) != 2 {
		t.Fatalf("operations len = %d, want 2", len(bundle.Operations))
	}
	if bundle.Operations[0].Ref.Name != "upper" {
		t.Fatalf("first operation = %s, want upper", bundle.Operations[0].Ref.Name)
	}
	if hasOperation(bundle.Operations, "lower") {
		t.Fatal("lower operation was contributed despite config")
	}
}

func TestPluginOperationTransformsText(t *testing.T) {
	ops, err := New().Operations(context.Background(), pluginContext(resource.PluginRef{
		Name: Name,
		Config: map[string]any{
			"operations": []any{"upper"},
		},
	}))
	if err != nil {
		t.Fatalf("Operations: %v", err)
	}
	if len(ops) != 1 {
		t.Fatalf("operations len = %d, want 1", len(ops))
	}
	result := ops[0].Run(operation.NewContext(context.Background(), nil), "hello")
	if result.Status != operation.StatusOK || result.Output != "HELLO" {
		t.Fatalf("result = %#v, want HELLO", result)
	}
}

func TestPluginRejectsUnknownConfiguredOperation(t *testing.T) {
	_, err := New().Contributions(context.Background(), pluginContext(resource.PluginRef{
		Name: Name,
		Config: map[string]any{
			"operations": []any{"missing"},
		},
	}))
	if err == nil {
		t.Fatal("Contributions error is nil, want unknown operation error")
	}
}

func TestPluginRejectsUnknownConfigKey(t *testing.T) {
	_, err := New().Contributions(context.Background(), pluginContext(resource.PluginRef{
		Name: Name,
		Config: map[string]any{
			"mode": "wide",
		},
	}))
	if err == nil {
		t.Fatal("Contributions error is nil, want unknown config key error")
	}
}

func hasOperation(specs []operation.Spec, name operation.Name) bool {
	for _, spec := range specs {
		if spec.Ref.Name == name {
			return true
		}
	}
	return false
}

func pluginContext(ref resource.PluginRef) pluginhostContext {
	return pluginhostContext{Ref: ref}
}

type pluginhostContext = pluginhost.Context
