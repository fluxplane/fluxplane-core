package textplugin

import (
	"context"
	"testing"

	"github.com/fluxplane/agentruntime/core/command"
	"github.com/fluxplane/agentruntime/core/operation"
	"github.com/fluxplane/agentruntime/core/resource"
	"github.com/fluxplane/agentruntime/orchestration/pluginhost"
)

func TestPluginContributesConfiguredCommands(t *testing.T) {
	bundle, err := New().Contributions(context.Background(), pluginContext(resource.PluginRef{
		Name: Name,
		Config: map[string]any{
			"commands": []any{"upper", "trim"},
		},
	}))
	if err != nil {
		t.Fatalf("Contributions: %v", err)
	}
	if len(bundle.Commands) != 2 {
		t.Fatalf("commands len = %d, want 2", len(bundle.Commands))
	}
	if bundle.Commands[0].Path.String() != "/text/upper" {
		t.Fatalf("first command = %s, want /text/upper", bundle.Commands[0].Path.String())
	}
	if _, ok := findCommand(bundle.Commands, command.Path{"text", "lower"}); ok {
		t.Fatal("lower command was contributed despite config")
	}
}

func TestPluginOperationTransformsText(t *testing.T) {
	ops, err := New().Operations(context.Background(), pluginContext(resource.PluginRef{
		Name: Name,
		Config: map[string]any{
			"commands": []any{"upper"},
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

func TestPluginRejectsUnknownConfiguredCommand(t *testing.T) {
	_, err := New().Contributions(context.Background(), pluginContext(resource.PluginRef{
		Name: Name,
		Config: map[string]any{
			"commands": []any{"missing"},
		},
	}))
	if err == nil {
		t.Fatal("Contributions error is nil, want unknown command error")
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

func findCommand(commands []command.Spec, path command.Path) (command.Spec, bool) {
	for _, spec := range commands {
		if spec.Path.String() == path.String() {
			return spec, true
		}
	}
	return command.Spec{}, false
}

func pluginContext(ref resource.PluginRef) pluginhostContext {
	return pluginhostContext{Ref: ref}
}

type pluginhostContext = pluginhost.Context
