package app

import (
	"context"
	"fmt"

	"github.com/fluxplane/agentruntime/core/agent"
	"github.com/fluxplane/agentruntime/core/command"
	"github.com/fluxplane/agentruntime/core/event"
	"github.com/fluxplane/agentruntime/core/invocation"
	"github.com/fluxplane/agentruntime/core/operation"
	"github.com/fluxplane/agentruntime/core/resource"
	corethread "github.com/fluxplane/agentruntime/core/thread"
	"github.com/fluxplane/agentruntime/orchestration/pluginhost"
	operationruntime "github.com/fluxplane/agentruntime/runtime/operation"
)

// Config describes app composition input.
type Config struct {
	Context           context.Context
	Agent             agent.Agent
	Operations        []operation.Operation
	Plugins           []pluginhost.Plugin
	Bundles           []resource.ContributionBundle
	OperationExecutor operationruntime.Executor
	Events            event.Sink
	ThreadStore       corethread.Store
}

// Composition is executable runtime configuration assembled from resources and
// provided implementations.
type Composition struct {
	Agent             agent.Agent
	Commands          *command.Registry
	Operations        *operation.Registry
	OperationSpecs    []operation.Spec
	OperationExecutor operationruntime.Executor
	Events            event.Sink
	ThreadStore       corethread.Store
	Bundles           []resource.ContributionBundle
	Diagnostics       []resource.Diagnostic
}

// Compose validates and registers resource contributions with supplied and
// plugin-contributed runtime implementations. Resource operation specs are
// declarations; executable operation implementations come from host or plugin
// code.
func Compose(cfg Config) (Composition, error) {
	bundles, pluginOperations, diagnostics, err := resolvePluginContributions(cfg.Context, cfg.Bundles, cfg.Plugins)
	if err != nil {
		return Composition{Diagnostics: diagnostics}, err
	}

	operations := operation.NewRegistry()
	for _, op := range cfg.Operations {
		if err := operations.Register(op); err != nil {
			err := fmt.Errorf("app: register host operation: %w", err)
			diagnostics = append(diagnostics, diagnostic(resource.SourceRef{}, err))
			return Composition{Diagnostics: diagnostics}, err
		}
	}
	for _, op := range pluginOperations {
		if err := operations.Register(op.Operation); err != nil {
			err := fmt.Errorf("app: register plugin operation: %w", err)
			diagnostics = append(diagnostics, diagnostic(op.Source, err))
			return Composition{Diagnostics: diagnostics}, err
		}
	}

	operationSpecs, opSpecDiagnostic, err := collectOperationSpecs(bundles)
	if err != nil {
		diagnostics = append(diagnostics, opSpecDiagnostic)
		return Composition{Diagnostics: diagnostics}, err
	}

	commands := command.NewRegistry()
	for _, bundle := range bundles {
		diagnostics = append(diagnostics, bundle.Diagnostics...)
		for _, spec := range bundle.Commands {
			if err := validateCommandTarget(operations, spec); err != nil {
				diagnostics = append(diagnostics, diagnostic(bundle.Source, err))
				return Composition{Diagnostics: diagnostics}, err
			}
			if err := commands.Register(spec); err != nil {
				err := fmt.Errorf("app: register command %s: %w", spec.Path.String(), err)
				diagnostics = append(diagnostics, diagnostic(bundle.Source, err))
				return Composition{Diagnostics: diagnostics}, err
			}
		}
	}

	return Composition{
		Agent:             cfg.Agent,
		Commands:          commands,
		Operations:        operations,
		OperationSpecs:    operationSpecs,
		OperationExecutor: cfg.OperationExecutor,
		Events:            cfg.Events,
		ThreadStore:       cfg.ThreadStore,
		Bundles:           bundles,
		Diagnostics:       diagnostics,
	}, nil
}

func resolvePluginContributions(ctx context.Context, bundles []resource.ContributionBundle, plugins []pluginhost.Plugin) ([]resource.ContributionBundle, []pluginhost.OperationContribution, []resource.Diagnostic, error) {
	out := append([]resource.ContributionBundle(nil), bundles...)
	var operations []pluginhost.OperationContribution
	var diagnostics []resource.Diagnostic
	host, err := pluginhost.New(plugins...)
	if err != nil {
		diagnostics = append(diagnostics, diagnostic(resource.SourceRef{}, err))
		return out, operations, diagnostics, err
	}
	for _, bundle := range bundles {
		if len(bundle.Plugins) == 0 {
			continue
		}
		contributed, err := host.Resolve(ctx, bundle.Plugins...)
		if err != nil {
			diagnostics = append(diagnostics, diagnostic(bundle.Source, err))
			return out, operations, diagnostics, err
		}
		out = append(out, contributed.Bundles...)
		operations = append(operations, contributed.Operations...)
	}
	return out, operations, diagnostics, nil
}

func collectOperationSpecs(bundles []resource.ContributionBundle) ([]operation.Spec, resource.Diagnostic, error) {
	seen := map[operation.Name]resource.SourceRef{}
	var specs []operation.Spec
	for _, bundle := range bundles {
		for _, spec := range bundle.Operations {
			name := spec.Ref.Name
			if name == "" {
				err := fmt.Errorf("app: operation spec name is empty")
				return nil, diagnostic(bundle.Source, err), err
			}
			if previous, exists := seen[name]; exists {
				err := fmt.Errorf("app: duplicate operation spec %q from %s and %s", name, sourceLabel(previous), sourceLabel(bundle.Source))
				return nil, diagnostic(bundle.Source, err), err
			}
			seen[name] = bundle.Source
			specs = append(specs, spec)
		}
	}
	return specs, resource.Diagnostic{}, nil
}

func sourceLabel(source resource.SourceRef) string {
	if source.ID != "" {
		return source.ID
	}
	if source.Location != "" {
		return source.Location
	}
	if source.Ref != "" {
		return source.Ref
	}
	return "unknown source"
}

func diagnostic(source resource.SourceRef, err error) resource.Diagnostic {
	return resource.Diagnostic{
		Severity: resource.SeverityError,
		Source:   source,
		Message:  err.Error(),
	}
}

func validateCommandTarget(operations *operation.Registry, spec command.Spec) error {
	switch spec.Target.Kind {
	case invocation.TargetOperation:
		if spec.Target.Operation.Name == "" {
			return fmt.Errorf("app: command %s targets an empty operation", spec.Path.String())
		}
		if _, ok := operations.Resolve(spec.Target.Operation); !ok {
			return fmt.Errorf("app: command %s targets unknown operation %q", spec.Path.String(), spec.Target.Operation.Name)
		}
		return nil
	case "":
		return fmt.Errorf("app: command %s target kind is empty", spec.Path.String())
	default:
		return nil
	}
}
