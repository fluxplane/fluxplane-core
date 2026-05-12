package app

import (
	"context"
	"testing"

	"github.com/fluxplane/agentruntime/core/command"
	"github.com/fluxplane/agentruntime/core/invocation"
	"github.com/fluxplane/agentruntime/core/operation"
	"github.com/fluxplane/agentruntime/core/resource"
	"github.com/fluxplane/agentruntime/orchestration/pluginhost"
	"github.com/fluxplane/agentruntime/plugins/textplugin"
)

func TestComposeRegistersResourceCommandsAgainstProvidedOperations(t *testing.T) {
	echo := operation.New(operation.Spec{Ref: operation.Ref{Name: "echo"}}, func(_ operation.Context, input operation.Value) operation.Result {
		return operation.OK(input)
	})
	composition, err := Compose(Config{
		Operations: []operation.Operation{echo},
		Bundles: []resource.ContributionBundle{{
			Commands: []command.Spec{{
				Path: command.Path{"echo"},
				Target: invocation.Target{
					Kind:      invocation.TargetOperation,
					Operation: operation.Ref{Name: "echo"},
				},
			}},
		}},
	})
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	if _, ok := composition.Commands.Resolve(command.Path{"echo"}); !ok {
		t.Fatal("command was not registered")
	}
	if op, ok := composition.Operations.Resolve(operation.Ref{Name: "echo"}); !ok || op == nil {
		t.Fatal("operation was not registered")
	}
}

func TestComposeRejectsCommandTargetingUnknownOperation(t *testing.T) {
	_, err := Compose(Config{
		Bundles: []resource.ContributionBundle{{
			Commands: []command.Spec{{
				Path: command.Path{"missing"},
				Target: invocation.Target{
					Kind:      invocation.TargetOperation,
					Operation: operation.Ref{Name: "missing"},
				},
			}},
		}},
	})
	if err == nil {
		t.Fatal("Compose error is nil, want unknown operation error")
	}
}

func TestComposeResolvesPluginContributions(t *testing.T) {
	composition, err := Compose(Config{
		Plugins: []pluginhost.Plugin{echoPlugin{}},
		Bundles: []resource.ContributionBundle{{
			Plugins: []resource.PluginRef{{Name: "echo-plugin"}},
		}},
	})
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	if _, ok := composition.Commands.Resolve(command.Path{"echo"}); !ok {
		t.Fatal("plugin command was not registered")
	}
	if op, ok := composition.Operations.Resolve(operation.Ref{Name: "echo"}); !ok || op == nil {
		t.Fatal("plugin operation was not registered")
	}
}

func TestComposeResolvesConfiguredPluginContributions(t *testing.T) {
	composition, err := Compose(Config{
		Plugins: []pluginhost.Plugin{textplugin.New()},
		Bundles: []resource.ContributionBundle{{
			Plugins: []resource.PluginRef{{
				Name: textplugin.Name,
				Config: map[string]any{
					"commands": []any{"upper"},
				},
			}},
		}},
	})
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	if _, ok := composition.Commands.Resolve(command.Path{"text", "upper"}); !ok {
		t.Fatal("configured plugin command was not registered")
	}
	if _, ok := composition.Commands.Resolve(command.Path{"text", "lower"}); ok {
		t.Fatal("unconfigured plugin command was registered")
	}
	if op, ok := composition.Operations.Resolve(operation.Ref{Name: "text.upper"}); !ok || op == nil {
		t.Fatal("configured plugin operation was not registered")
	}
	if len(composition.OperationSpecs) != 1 {
		t.Fatalf("operation specs len = %d, want 1", len(composition.OperationSpecs))
	}
	if composition.OperationSpecs[0].Ref.Name != "text.upper" {
		t.Fatalf("operation spec = %#v, want text.upper", composition.OperationSpecs[0].Ref)
	}
}

func TestComposeRejectsDuplicatePluginOperation(t *testing.T) {
	echo := operation.New(operation.Spec{Ref: operation.Ref{Name: "echo"}}, func(_ operation.Context, input operation.Value) operation.Result {
		return operation.OK(input)
	})
	composition, err := Compose(Config{
		Operations: []operation.Operation{echo},
		Plugins:    []pluginhost.Plugin{echoPlugin{}},
		Bundles: []resource.ContributionBundle{{
			Plugins: []resource.PluginRef{{Name: "echo-plugin"}},
		}},
	})
	if err == nil {
		t.Fatal("Compose error is nil, want duplicate operation error")
	}
	if len(composition.Diagnostics) != 1 {
		t.Fatalf("diagnostics len = %d, want 1", len(composition.Diagnostics))
	}
	if composition.Diagnostics[0].Source.ID != "plugin:echo-plugin" {
		t.Fatalf("diagnostic source = %q, want plugin:echo-plugin", composition.Diagnostics[0].Source.ID)
	}
}

func TestComposeRejectsDuplicateCommandPathWithSourceDiagnostic(t *testing.T) {
	echo := operation.New(operation.Spec{Ref: operation.Ref{Name: "echo"}}, func(_ operation.Context, input operation.Value) operation.Result {
		return operation.OK(input)
	})
	sourceA := resource.SourceRef{ID: "bundle:a"}
	sourceB := resource.SourceRef{ID: "bundle:b"}
	composition, err := Compose(Config{
		Operations: []operation.Operation{echo},
		Bundles: []resource.ContributionBundle{
			{
				Source: sourceA,
				Commands: []command.Spec{{
					Path: command.Path{"echo"},
					Target: invocation.Target{
						Kind:      invocation.TargetOperation,
						Operation: operation.Ref{Name: "echo"},
					},
				}},
			},
			{
				Source: sourceB,
				Commands: []command.Spec{{
					Path: command.Path{"echo"},
					Target: invocation.Target{
						Kind:      invocation.TargetOperation,
						Operation: operation.Ref{Name: "echo"},
					},
				}},
			},
		},
	})
	if err == nil {
		t.Fatal("Compose error is nil, want duplicate command error")
	}
	if len(composition.Diagnostics) != 1 {
		t.Fatalf("diagnostics len = %d, want 1", len(composition.Diagnostics))
	}
	if composition.Diagnostics[0].Source.ID != sourceB.ID {
		t.Fatalf("diagnostic source = %q, want %s", composition.Diagnostics[0].Source.ID, sourceB.ID)
	}
}

func TestComposeRejectsDuplicateOperationSpecsWithSourceDiagnostic(t *testing.T) {
	sourceA := resource.SourceRef{ID: "bundle:a"}
	sourceB := resource.SourceRef{ID: "bundle:b"}
	composition, err := Compose(Config{
		Bundles: []resource.ContributionBundle{
			{
				Source:     sourceA,
				Operations: []operation.Spec{{Ref: operation.Ref{Name: "echo"}}},
			},
			{
				Source:     sourceB,
				Operations: []operation.Spec{{Ref: operation.Ref{Name: "echo"}}},
			},
		},
	})
	if err == nil {
		t.Fatal("Compose error is nil, want duplicate operation spec error")
	}
	if len(composition.Diagnostics) != 1 {
		t.Fatalf("diagnostics len = %d, want 1", len(composition.Diagnostics))
	}
	if composition.Diagnostics[0].Source.ID != sourceB.ID {
		t.Fatalf("diagnostic source = %q, want %s", composition.Diagnostics[0].Source.ID, sourceB.ID)
	}
}

type echoPlugin struct{}

func (echoPlugin) Manifest() pluginhost.Manifest {
	return pluginhost.Manifest{Name: "echo-plugin"}
}

func (echoPlugin) Contributions(context.Context, pluginhost.Context) (resource.ContributionBundle, error) {
	return resource.ContributionBundle{
		Commands: []command.Spec{{
			Path: command.Path{"echo"},
			Target: invocation.Target{
				Kind:      invocation.TargetOperation,
				Operation: operation.Ref{Name: "echo"},
			},
		}},
	}, nil
}

func (echoPlugin) Operations(context.Context, pluginhost.Context) ([]operation.Operation, error) {
	return []operation.Operation{
		operation.New(operation.Spec{Ref: operation.Ref{Name: "echo"}}, func(_ operation.Context, input operation.Value) operation.Result {
			return operation.OK(input)
		}),
	}, nil
}
