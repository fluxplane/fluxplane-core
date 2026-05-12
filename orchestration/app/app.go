package app

import (
	"context"
	"fmt"
	"strings"

	"github.com/fluxplane/agentruntime/core/agent"
	"github.com/fluxplane/agentruntime/core/command"
	"github.com/fluxplane/agentruntime/core/event"
	"github.com/fluxplane/agentruntime/core/invocation"
	"github.com/fluxplane/agentruntime/core/operation"
	"github.com/fluxplane/agentruntime/core/resource"
	coresession "github.com/fluxplane/agentruntime/core/session"
	corethread "github.com/fluxplane/agentruntime/core/thread"
	"github.com/fluxplane/agentruntime/orchestration/pluginhost"
	"github.com/fluxplane/agentruntime/orchestration/session"
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
	ResourceIndex     *resource.ResourceIndex
	Resolver          *resource.Resolver
	CommandCatalog    session.CommandCatalog
	OperationCatalog  session.OperationCatalog
	SessionCatalog    session.SessionCatalog
	OperationSpecs    []operation.Spec
	SessionSpecs      []coresession.Spec
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

	index := resource.NewResourceIndex()
	resolver := resource.NewResolver(resource.ResolverConfig{Index: index})

	operationSpecContributions, opSpecDiagnostic, err := collectOperationSpecs(bundles)
	if err != nil {
		diagnostics = append(diagnostics, opSpecDiagnostic)
		return Composition{Diagnostics: diagnostics}, err
	}
	operationSpecs := make([]operation.Spec, 0, len(operationSpecContributions))
	for _, contribution := range operationSpecContributions {
		index.Add(contribution.ID)
		operationSpecs = append(operationSpecs, contribution.Spec)
	}

	operationCatalog := session.OperationCatalog{}
	operationByName := map[operation.Name][]operation.Operation{}
	for _, op := range cfg.Operations {
		id := resource.DeriveResourceID(resource.SourceRef{Scope: resource.ScopeExplicit}, "operation", string(op.Spec().Ref.Name))
		if err := addOperation(operationCatalog, index, id, op); err != nil {
			diagnostics = append(diagnostics, diagnostic(resource.SourceRef{}, err))
			return Composition{Diagnostics: diagnostics}, err
		}
		operationByName[op.Spec().Ref.Name] = append(operationByName[op.Spec().Ref.Name], op)
	}
	for _, op := range pluginOperations {
		id := resource.DeriveResourceID(op.Source, "operation", string(op.Operation.Spec().Ref.Name))
		if err := addOperation(operationCatalog, index, id, op.Operation); err != nil {
			diagnostics = append(diagnostics, diagnostic(op.Source, err))
			return Composition{Diagnostics: diagnostics}, err
		}
		operationByName[op.Operation.Spec().Ref.Name] = append(operationByName[op.Operation.Spec().Ref.Name], op.Operation)
	}

	operations := operation.NewRegistry()
	for name, candidates := range operationByName {
		if len(candidates) != 1 {
			continue
		}
		if err := operations.Register(candidates[0]); err != nil {
			err := fmt.Errorf("app: register operation %q: %w", name, err)
			diagnostics = append(diagnostics, diagnostic(resource.SourceRef{}, err))
			return Composition{Diagnostics: diagnostics}, err
		}
	}

	commands := command.NewRegistry()
	commandCatalog := session.CommandCatalog{}
	pathCounts := map[string]int{}
	for _, bundle := range bundles {
		diagnostics = append(diagnostics, bundle.Diagnostics...)
		for _, spec := range bundle.Commands {
			id := resource.DeriveResourceID(bundle.Source, "command", commandName(spec))
			index.Add(id)
			operationID, err := resolveCommandOperation(operationCatalog, id, spec)
			if err != nil {
				diagnostics = append(diagnostics, diagnostic(bundle.Source, err))
				return Composition{Diagnostics: diagnostics}, err
			}
			if err := addCommand(commandCatalog, id, session.CommandBinding{ID: id, Spec: spec, OperationID: operationID}); err != nil {
				diagnostics = append(diagnostics, diagnostic(bundle.Source, err))
				return Composition{Diagnostics: diagnostics}, err
			}
			pathCounts[spec.Path.String()]++
		}
	}
	for _, binding := range commandCatalog {
		if pathCounts[binding.Spec.Path.String()] != 1 {
			continue
		}
		if err := commands.Register(binding.Spec); err != nil {
			err := fmt.Errorf("app: register command %s: %w", binding.Spec.Path.String(), err)
			diagnostics = append(diagnostics, diagnostic(resource.SourceRef{ID: binding.ID.Address()}, err))
			return Composition{Diagnostics: diagnostics}, err
		}
	}

	sessionCatalog, sessionSpecs, sessionDiagnostic, err := collectSessions(bundles, index)
	if err != nil {
		diagnostics = append(diagnostics, sessionDiagnostic)
		return Composition{Diagnostics: diagnostics}, err
	}

	return Composition{
		Agent:             cfg.Agent,
		Commands:          commands,
		Operations:        operations,
		ResourceIndex:     index,
		Resolver:          resolver,
		CommandCatalog:    commandCatalog,
		OperationCatalog:  operationCatalog,
		SessionCatalog:    sessionCatalog,
		OperationSpecs:    operationSpecs,
		SessionSpecs:      sessionSpecs,
		OperationExecutor: cfg.OperationExecutor,
		Events:            cfg.Events,
		ThreadStore:       cfg.ThreadStore,
		Bundles:           bundles,
		Diagnostics:       diagnostics,
	}, nil
}

func collectSessions(bundles []resource.ContributionBundle, index *resource.ResourceIndex) (session.SessionCatalog, []coresession.Spec, resource.Diagnostic, error) {
	catalog := session.SessionCatalog{}
	var specs []coresession.Spec
	for _, bundle := range bundles {
		for _, spec := range bundle.Sessions {
			if err := spec.Validate(); err != nil {
				err := fmt.Errorf("app: session spec: %w", err)
				return nil, nil, diagnostic(bundle.Source, err), err
			}
			id := resource.DeriveResourceID(bundle.Source, "session", string(spec.Name))
			if id.Name == "" {
				err := fmt.Errorf("app: session resource id name is empty")
				return nil, nil, diagnostic(bundle.Source, err), err
			}
			if _, exists := catalog[id.Address()]; exists {
				err := fmt.Errorf("app: duplicate session resource %q", id.Address())
				return nil, nil, diagnostic(bundle.Source, err), err
			}
			catalog[id.Address()] = session.SessionBinding{ID: id, Spec: spec}
			index.Add(id)
			specs = append(specs, spec)
		}
	}
	return catalog, specs, resource.Diagnostic{}, nil
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

type operationSpecContribution struct {
	ID   resource.ResourceID
	Spec operation.Spec
}

func collectOperationSpecs(bundles []resource.ContributionBundle) ([]operationSpecContribution, resource.Diagnostic, error) {
	seen := map[string]resource.SourceRef{}
	var specs []operationSpecContribution
	for _, bundle := range bundles {
		for _, spec := range bundle.Operations {
			name := spec.Ref.Name
			if name == "" {
				err := fmt.Errorf("app: operation spec name is empty")
				return nil, diagnostic(bundle.Source, err), err
			}
			id := resource.DeriveResourceID(bundle.Source, "operation", string(name))
			if previous, exists := seen[id.Address()]; exists {
				err := fmt.Errorf("app: duplicate operation spec %q from %s and %s", id.Address(), sourceLabel(previous), sourceLabel(bundle.Source))
				return nil, diagnostic(bundle.Source, err), err
			}
			seen[id.Address()] = bundle.Source
			specs = append(specs, operationSpecContribution{ID: id, Spec: spec})
		}
	}
	return specs, resource.Diagnostic{}, nil
}

func addOperation(catalog session.OperationCatalog, index *resource.ResourceIndex, id resource.ResourceID, op operation.Operation) error {
	if op == nil {
		return fmt.Errorf("app: operation %s is nil", id.Address())
	}
	if id.Name == "" {
		return fmt.Errorf("app: operation resource id name is empty")
	}
	if _, exists := catalog[id.Address()]; exists {
		return fmt.Errorf("app: duplicate operation resource %q", id.Address())
	}
	catalog[id.Address()] = session.OperationBinding{ID: id, Operation: op}
	index.Add(id)
	return nil
}

func addCommand(catalog session.CommandCatalog, id resource.ResourceID, binding session.CommandBinding) error {
	if id.Name == "" {
		return fmt.Errorf("app: command resource id name is empty")
	}
	if _, exists := catalog[id.Address()]; exists {
		return fmt.Errorf("app: duplicate command resource %q", id.Address())
	}
	catalog[id.Address()] = binding
	return nil
}

func resolveCommandOperation(operations session.OperationCatalog, commandID resource.ResourceID, spec command.Spec) (resource.ResourceID, error) {
	switch spec.Target.Kind {
	case invocation.TargetOperation:
		if spec.Target.Operation.Name == "" {
			return resource.ResourceID{}, fmt.Errorf("app: command %s targets an empty operation", spec.Path.String())
		}
		binding, err := operations.Resolve(spec.Target.Operation.String(), commandID)
		if err != nil {
			return resource.ResourceID{}, fmt.Errorf("app: command %s target operation: %w", spec.Path.String(), err)
		}
		return binding.ID, nil
	case "":
		return resource.ResourceID{}, fmt.Errorf("app: command %s target kind is empty", spec.Path.String())
	default:
		return resource.ResourceID{}, nil
	}
}

func commandName(spec command.Spec) string {
	if name := strings.TrimSpace(spec.Annotations["name"]); name != "" {
		return name
	}
	for i := len(spec.Path) - 1; i >= 0; i-- {
		if part := strings.TrimSpace(spec.Path[i]); part != "" {
			return part
		}
	}
	return ""
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
