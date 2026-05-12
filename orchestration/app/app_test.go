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

func TestComposeRejectsCommandTargetingDeclarationOnlyOperation(t *testing.T) {
	_, err := Compose(Config{
		Bundles: []resource.ContributionBundle{{
			Source:     resource.SourceRef{Scope: resource.ScopeEmbedded, Location: "plugins/spec-only"},
			Operations: []operation.Spec{{Ref: operation.Ref{Name: "declared"}}},
			Commands: []command.Spec{{
				Path: command.Path{"declared"},
				Target: invocation.Target{
					Kind:      invocation.TargetOperation,
					Operation: operation.Ref{Name: "declared"},
				},
			}},
		}},
	})
	if err == nil {
		t.Fatal("Compose error is nil, want declaration-only operation error")
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
	if op, ok := composition.Operations.Resolve(operation.Ref{Name: "upper"}); !ok || op == nil {
		t.Fatal("configured plugin operation was not registered")
	}
	if len(composition.OperationSpecs) != 1 {
		t.Fatalf("operation specs len = %d, want 1", len(composition.OperationSpecs))
	}
	if composition.OperationSpecs[0].Ref.Name != "upper" {
		t.Fatalf("operation spec = %#v, want upper", composition.OperationSpecs[0].Ref)
	}
	id, err := composition.Resolver.Resolve("operation", "text:upper")
	if err != nil {
		t.Fatalf("Resolve text:upper: %v", err)
	}
	if got, want := id.Address(), "embedded:plugins/text:upper"; got != want {
		t.Fatalf("resolved operation = %q, want %q", got, want)
	}
}

func TestComposeAllowsDuplicateOperationNamesAcrossResourceIDs(t *testing.T) {
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
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	if len(composition.OperationCatalog) != 2 {
		t.Fatalf("operation catalog len = %d, want 2", len(composition.OperationCatalog))
	}
	id, err := composition.Resolver.Resolve("operation", "echo-plugin:echo")
	if err != nil {
		t.Fatalf("Resolve echo-plugin:echo: %v", err)
	}
	if got, want := id.Address(), "embedded:plugins/echo-plugin:echo"; got != want {
		t.Fatalf("resolved operation = %q, want %q", got, want)
	}
}

func TestComposeAllowsDuplicateCommandPathAcrossResourceIDs(t *testing.T) {
	echo := operation.New(operation.Spec{Ref: operation.Ref{Name: "echo"}}, func(_ operation.Context, input operation.Value) operation.Result {
		return operation.OK(input)
	})
	sourceA := resource.SourceRef{Scope: resource.ScopeEmbedded, Location: "plugins/a"}
	sourceB := resource.SourceRef{Scope: resource.ScopeEmbedded, Location: "plugins/b"}
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
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	if len(composition.CommandCatalog) != 2 {
		t.Fatalf("command catalog len = %d, want 2", len(composition.CommandCatalog))
	}
	id, err := composition.Resolver.Resolve("command", "a:echo")
	if err != nil {
		t.Fatalf("Resolve a:echo: %v", err)
	}
	if got, want := id.Address(), "embedded:plugins/a:echo"; got != want {
		t.Fatalf("resolved command = %q, want %q", got, want)
	}
}

func TestComposeBindsPluginCommandToSiblingOperationWithSameShortName(t *testing.T) {
	composition, err := Compose(Config{
		Plugins: []pluginhost.Plugin{
			sameNamePlugin{name: "foo"},
			sameNamePlugin{name: "bar"},
		},
		Bundles: []resource.ContributionBundle{{
			Plugins: []resource.PluginRef{{Name: "foo"}, {Name: "bar"}},
		}},
	})
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	if len(composition.OperationCatalog) != 2 {
		t.Fatalf("operation catalog len = %d, want 2", len(composition.OperationCatalog))
	}
	fooOp, err := composition.Resolver.Resolve("operation", "foo:run")
	if err != nil {
		t.Fatalf("Resolve foo:run: %v", err)
	}
	barOp, err := composition.Resolver.Resolve("operation", "bar:run")
	if err != nil {
		t.Fatalf("Resolve bar:run: %v", err)
	}
	if fooOp.Equal(barOp) {
		t.Fatalf("foo and bar resolved to same operation: %s", fooOp.Address())
	}
	fooCommand, err := composition.Resolver.Resolve("command", "foo:run")
	if err != nil {
		t.Fatalf("Resolve command foo:run: %v", err)
	}
	binding := composition.CommandCatalog[fooCommand.Address()]
	if !binding.OperationID.Equal(fooOp) {
		t.Fatalf("foo command operation = %s, want %s", binding.OperationID.Address(), fooOp.Address())
	}
	if _, err := composition.Resolver.Resolve("operation", "run"); err == nil {
		t.Fatal("Resolve run error is nil, want ambiguity")
	}
}

func TestComposeRejectsDuplicateOperationSpecsWithSourceDiagnostic(t *testing.T) {
	sourceA := resource.SourceRef{Scope: resource.ScopeEmbedded, Location: "plugins/a"}
	sourceB := resource.SourceRef{Scope: resource.ScopeEmbedded, Location: "plugins/a"}
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
	if composition.Diagnostics[0].Source.Location != sourceB.Location {
		t.Fatalf("diagnostic source location = %q, want %s", composition.Diagnostics[0].Source.Location, sourceB.Location)
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

type sameNamePlugin struct {
	name string
}

func (p sameNamePlugin) Manifest() pluginhost.Manifest {
	return pluginhost.Manifest{Name: p.name}
}

func (p sameNamePlugin) Contributions(context.Context, pluginhost.Context) (resource.ContributionBundle, error) {
	return resource.ContributionBundle{
		Operations: []operation.Spec{{Ref: operation.Ref{Name: "run"}}},
		Commands: []command.Spec{{
			Path: command.Path{p.name, "run"},
			Target: invocation.Target{
				Kind:      invocation.TargetOperation,
				Operation: operation.Ref{Name: "run"},
			},
		}},
	}, nil
}

func (p sameNamePlugin) Operations(context.Context, pluginhost.Context) ([]operation.Operation, error) {
	return []operation.Operation{
		operation.New(operation.Spec{Ref: operation.Ref{Name: "run"}}, func(_ operation.Context, input operation.Value) operation.Result {
			return operation.OK(map[string]any{"plugin": p.name, "input": input})
		}),
	}, nil
}
