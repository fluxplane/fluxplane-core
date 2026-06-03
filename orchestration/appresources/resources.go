// Package appresources binds executable resources for app composition.
package appresources

import (
	"fmt"
	"strings"

	"github.com/fluxplane/fluxplane-core/core/activation"
	"github.com/fluxplane/fluxplane-core/core/command"
	"github.com/fluxplane/fluxplane-core/core/invocation"
	"github.com/fluxplane/fluxplane-core/core/resource"
	coresession "github.com/fluxplane/fluxplane-core/core/session"
	"github.com/fluxplane/fluxplane-core/core/tool"
	"github.com/fluxplane/fluxplane-core/orchestration/resourcecatalog"
	"github.com/fluxplane/fluxplane-core/orchestration/session"
	operationruntime "github.com/fluxplane/fluxplane-core/runtime/operation"
	"github.com/fluxplane/fluxplane-operation"
)

// OperationContribution binds a plugin-contributed operation to its source.
type OperationContribution struct {
	Source    resource.SourceRef
	Operation operation.Operation
}

// Config describes executable app resource binding input.
type Config struct {
	Bundles          []resource.ContributionBundle
	Operations       []operation.Operation
	PluginOperations []OperationContribution
	AppCatalog       resourcecatalog.AppCatalog
	Resolver         *resource.Resolver
	Index            *resource.ResourceIndex
}

// Resources groups executable catalogs and their ordered inert specs.
type Resources struct {
	Commands             *command.Registry
	Operations           *operation.Registry
	ActivationSetCatalog resourcecatalog.ActivationSetCatalog
	ToolSetCatalog       session.ToolSetCatalog
	CommandCatalog       session.CommandCatalog
	OperationCatalog     session.OperationCatalog
	SessionCatalog       session.SessionCatalog
	ActivationSets       []activation.Set
	ToolSets             []tool.Set
	OperationSpecs       []operation.Spec
	SessionSpecs         []coresession.Spec
}

// Collect validates and binds executable resources.
func Collect(cfg Config) (Resources, resource.Diagnostic, error) {
	activationCatalog, activationSets, diag, err := resourcecatalog.CollectActivationSets(cfg.Bundles, cfg.Index)
	if err != nil {
		return Resources{}, diag, err
	}

	toolSetCatalog, toolSets, diag, err := collectToolSets(cfg.Bundles, cfg.Index)
	if err != nil {
		return Resources{}, diag, err
	}

	operationSpecContributions, diag, err := collectOperationSpecs(cfg.Bundles)
	if err != nil {
		return Resources{}, diag, err
	}
	operationSpecs := make([]operation.Spec, 0, len(operationSpecContributions))
	for _, contribution := range operationSpecContributions {
		cfg.Index.Add(contribution.ID)
		operationSpecs = append(operationSpecs, contribution.Spec)
	}

	operationCatalog, operations, diag, err := collectOperations(cfg)
	if err != nil {
		return Resources{}, diag, err
	}

	sessionCatalog, sessionSpecs, diag, err := collectSessions(cfg.Bundles, cfg.AppCatalog, cfg.Resolver, cfg.Index)
	if err != nil {
		return Resources{}, diag, err
	}

	commands, commandCatalog, diag, err := collectCommands(cfg.Bundles, cfg.Resolver, operationCatalog, cfg.Index)
	if err != nil {
		return Resources{}, diag, err
	}

	return Resources{
		Commands:             commands,
		Operations:           operations,
		ActivationSetCatalog: activationCatalog,
		ToolSetCatalog:       toolSetCatalog,
		CommandCatalog:       commandCatalog,
		OperationCatalog:     operationCatalog,
		SessionCatalog:       sessionCatalog,
		ActivationSets:       activationSets,
		ToolSets:             toolSets,
		OperationSpecs:       operationSpecs,
		SessionSpecs:         sessionSpecs,
	}, resource.Diagnostic{}, nil
}

func collectToolSets(bundles []resource.ContributionBundle, index *resource.ResourceIndex) (session.ToolSetCatalog, []tool.Set, resource.Diagnostic, error) {
	catalog := session.ToolSetCatalog{}
	var specs []tool.Set
	for _, bundle := range bundles {
		for _, spec := range bundle.ToolSets {
			if err := spec.Validate(); err != nil {
				return nil, nil, diagnostic(bundle.Source, err), err
			}
			id := resource.DeriveResourceID(bundle.Source, "tool_set", spec.Name)
			index.Add(id)
			if _, exists := catalog[id.Address()]; exists {
				err := fmt.Errorf("duplicate tool_set resource %q", id.Address())
				return nil, nil, diagnostic(bundle.Source, err), err
			}
			catalog[id.Address()] = session.ToolSetBinding{ID: id, Spec: spec}
			specs = append(specs, spec)
		}
	}
	return catalog, specs, resource.Diagnostic{}, nil
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

func collectOperations(cfg Config) (session.OperationCatalog, *operation.Registry, resource.Diagnostic, error) {
	catalog := session.OperationCatalog{}
	operationByName := map[operation.Name][]operation.Operation{}
	for _, op := range cfg.Operations {
		id := resource.DeriveResourceID(resource.SourceRef{Scope: resource.ScopeExplicit}, "operation", string(op.Spec().Ref.Name))
		if err := addOperation(catalog, cfg.Index, id, op); err != nil {
			return nil, nil, diagnostic(resource.SourceRef{}, err), err
		}
		operationByName[op.Spec().Ref.Name] = append(operationByName[op.Spec().Ref.Name], op)
	}
	for _, op := range aggregateNamedPluginOperations(cfg.PluginOperations) {
		id := resource.DeriveResourceID(op.Source, "operation", string(op.Operation.Spec().Ref.Name))
		if err := addOperation(catalog, cfg.Index, id, op.Operation); err != nil {
			return nil, nil, diagnostic(op.Source, err), err
		}
		operationByName[op.Operation.Spec().Ref.Name] = append(operationByName[op.Operation.Spec().Ref.Name], op.Operation)
	}

	registry := operation.NewRegistry()
	for name, candidates := range operationByName {
		if len(candidates) != 1 {
			continue
		}
		if err := registry.Register(candidates[0]); err != nil {
			err := fmt.Errorf("app: register operation %q: %w", name, err)
			return nil, nil, diagnostic(resource.SourceRef{}, err), err
		}
	}
	return catalog, registry, resource.Diagnostic{}, nil
}

func aggregateNamedPluginOperations(ops []OperationContribution) []OperationContribution {
	groups := map[string][]OperationContribution{}
	order := make([]string, 0, len(ops))
	for _, op := range ops {
		spec := op.Operation.Spec()
		kind := strings.TrimSpace(spec.Annotations[operationruntime.AnnotationNamedPluginKind])
		if kind == "" {
			key := "\x00" + op.Source.ID + "\x00" + string(spec.Ref.Name)
			groups[key] = append(groups[key], op)
			order = append(order, key)
			continue
		}
		key := kind + "\x00" + string(spec.Ref.Name)
		if _, ok := groups[key]; !ok {
			order = append(order, key)
		}
		groups[key] = append(groups[key], op)
	}
	out := make([]OperationContribution, 0, len(groups))
	seen := map[string]bool{}
	for _, key := range order {
		if seen[key] {
			continue
		}
		seen[key] = true
		group := groups[key]
		if len(group) == 0 {
			continue
		}
		kind := strings.TrimSpace(group[0].Operation.Spec().Annotations[operationruntime.AnnotationNamedPluginKind])
		if kind == "" {
			out = append(out, group...)
			continue
		}
		var instances []operationruntime.NamedInstanceBinding
		for _, contribution := range group {
			provider, ok := contribution.Operation.(operationruntime.NamedInstanceProvider)
			if !ok {
				out = append(out, contribution)
				continue
			}
			instances = append(instances, provider.NamedInstances()...)
		}
		aggregated := operationruntime.AggregateNamedInstances(kind, instances)
		if aggregated == nil {
			continue
		}
		out = append(out, OperationContribution{
			Source: resource.SourceRef{
				ID:        "plugin:" + kind,
				Ecosystem: "embedded",
				Scope:     resource.ScopeEmbedded,
				Location:  "plugins/" + kind,
			},
			Operation: aggregated,
		})
	}
	return out
}

func collectSessions(
	bundles []resource.ContributionBundle,
	apps resourcecatalog.AppCatalog,
	resolver *resource.Resolver,
	index *resource.ResourceIndex,
) (session.SessionCatalog, []coresession.Spec, resource.Diagnostic, error) {
	catalog := session.SessionCatalog{}
	var specs []coresession.Spec
	for _, bundle := range bundles {
		for _, spec := range bundle.Sessions {
			id := resource.DeriveResourceID(bundle.Source, "session", string(spec.Name))
			if err := addSession(catalog, index, id, spec); err != nil {
				return nil, nil, diagnostic(bundle.Source, err), err
			}
			specs = append(specs, spec)
		}
	}
	for _, appBinding := range apps {
		spec, ok, err := resourcecatalog.DefaultSessionSpec(appBinding, resolver)
		if err != nil {
			return nil, nil, diagnostic(appBinding.Source, err), err
		}
		if !ok {
			continue
		}
		id := resource.ResourceID{
			Kind:      "session",
			Origin:    appBinding.ID.Origin,
			Namespace: appBinding.ID.Namespace,
			Name:      string(spec.Name),
		}
		if _, exists := catalog[id.Address()]; exists {
			continue
		}
		if err := addSession(catalog, index, id, spec); err != nil {
			return nil, nil, diagnostic(appBinding.Source, err), err
		}
		specs = append(specs, spec)
	}
	for _, appBinding := range apps {
		if appBinding.Spec.DefaultSession.Name == "" {
			continue
		}
		if _, err := resolver.ResolveInScope("session", string(appBinding.Spec.DefaultSession.Name), appBinding.ID); err != nil {
			err := fmt.Errorf("app: default session %q for app %s: %w", appBinding.Spec.DefaultSession.Name, appBinding.ID.Address(), err)
			return nil, nil, diagnostic(appBinding.Source, err), err
		}
	}
	return catalog, specs, resource.Diagnostic{}, nil
}

func collectCommands(
	bundles []resource.ContributionBundle,
	resolver *resource.Resolver,
	operations session.OperationCatalog,
	index *resource.ResourceIndex,
) (*command.Registry, session.CommandCatalog, resource.Diagnostic, error) {
	registry := command.NewRegistry()
	catalog := session.CommandCatalog{}
	pathCounts := map[string]int{}
	for _, bundle := range bundles {
		for _, spec := range bundle.Commands {
			id := commandResourceID(bundle.Source, spec)
			index.Add(id)
			targetID, operationID, err := resolveCommandTarget(resolver, operations, id, spec)
			if err != nil {
				return nil, nil, diagnostic(bundle.Source, err), err
			}
			if err := addCommand(catalog, id, session.CommandBinding{
				ID:          id,
				Spec:        spec,
				TargetID:    targetID,
				OperationID: operationID,
			}); err != nil {
				return nil, nil, diagnostic(bundle.Source, err), err
			}
			pathCounts[spec.Path.String()]++
		}
	}
	for _, binding := range catalog {
		if pathCounts[binding.Spec.Path.String()] != 1 {
			continue
		}
		if err := registry.Register(binding.Spec); err != nil {
			err := fmt.Errorf("app: register command %s: %w", binding.Spec.Path.String(), err)
			return nil, nil, diagnostic(resource.SourceRef{ID: binding.ID.Address()}, err), err
		}
	}
	return registry, catalog, resource.Diagnostic{}, nil
}

func commandResourceID(source resource.SourceRef, spec command.Spec) resource.ResourceID {
	name := commandName(spec)
	id := resource.DeriveResourceID(source, "command", name)
	parts := resourceNameParts(name)
	if len(parts) <= 1 {
		return id
	}
	id.Name = parts[len(parts)-1]
	id.Namespace = id.Namespace.Append(parts[:len(parts)-1]...)
	return id
}

func addSession(catalog session.SessionCatalog, index *resource.ResourceIndex, id resource.ResourceID, spec coresession.Spec) error {
	if err := spec.Validate(); err != nil {
		return fmt.Errorf("app: session spec: %w", err)
	}
	if id.Name == "" {
		return fmt.Errorf("app: session resource id name is empty")
	}
	if _, exists := catalog[id.Address()]; exists {
		return fmt.Errorf("app: duplicate session resource %q", id.Address())
	}
	catalog[id.Address()] = session.SessionBinding{ID: id, Spec: spec}
	index.Add(id)
	return nil
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

func resolveCommandTarget(
	resolver *resource.Resolver,
	operations session.OperationCatalog,
	commandID resource.ResourceID,
	spec command.Spec,
) (resource.ResourceID, resource.ResourceID, error) {
	switch spec.Target.Kind {
	case invocation.TargetOperation:
		if spec.Target.Operation.Name == "" {
			return resource.ResourceID{}, resource.ResourceID{}, fmt.Errorf("app: command %s targets an empty operation", spec.Path.String())
		}
		binding, err := operations.Resolve(spec.Target.Operation.String(), commandID)
		if err != nil {
			return resource.ResourceID{}, resource.ResourceID{}, fmt.Errorf("app: command %s target operation: %w", spec.Path.String(), err)
		}
		return binding.ID, binding.ID, nil
	case invocation.TargetWorkflow:
		if strings.TrimSpace(string(spec.Target.Workflow)) == "" {
			return resource.ResourceID{}, resource.ResourceID{}, fmt.Errorf("app: command %s targets an empty workflow", spec.Path.String())
		}
		id, err := resolver.ResolveInScope("workflow", string(spec.Target.Workflow), commandID)
		if err != nil {
			return resource.ResourceID{}, resource.ResourceID{}, fmt.Errorf("app: command %s target workflow: %w", spec.Path.String(), err)
		}
		return id, resource.ResourceID{}, nil
	case invocation.TargetAgent:
		if strings.TrimSpace(string(spec.Target.Agent.Name)) == "" {
			return resource.ResourceID{}, resource.ResourceID{}, fmt.Errorf("app: command %s targets an empty agent", spec.Path.String())
		}
		id, err := resolver.ResolveInScope("agent", string(spec.Target.Agent.Name), commandID)
		if err != nil {
			return resource.ResourceID{}, resource.ResourceID{}, fmt.Errorf("app: command %s target agent: %w", spec.Path.String(), err)
		}
		return id, resource.ResourceID{}, nil
	case invocation.TargetSession:
		if strings.TrimSpace(spec.Target.Session) == "" {
			return resource.ResourceID{}, resource.ResourceID{}, fmt.Errorf("app: command %s targets an empty session", spec.Path.String())
		}
		id, err := resolver.ResolveInScope("session", spec.Target.Session, commandID)
		if err != nil {
			return resource.ResourceID{}, resource.ResourceID{}, fmt.Errorf("app: command %s target session: %w", spec.Path.String(), err)
		}
		return id, resource.ResourceID{}, nil
	case invocation.TargetPrompt:
		if strings.TrimSpace(spec.Target.Prompt) == "" {
			return resource.ResourceID{}, resource.ResourceID{}, fmt.Errorf("app: command %s target prompt is empty", spec.Path.String())
		}
		return resource.ResourceID{}, resource.ResourceID{}, nil
	case invocation.TargetMessage:
		if strings.TrimSpace(spec.Target.Message) == "" {
			return resource.ResourceID{}, resource.ResourceID{}, fmt.Errorf("app: command %s target message is empty", spec.Path.String())
		}
		return resource.ResourceID{}, resource.ResourceID{}, nil
	case "":
		return resource.ResourceID{}, resource.ResourceID{}, fmt.Errorf("app: command %s target kind is empty", spec.Path.String())
	default:
		return resource.ResourceID{}, resource.ResourceID{}, fmt.Errorf("app: command %s target kind %q is unsupported", spec.Path.String(), spec.Target.Kind)
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

func resourceNameParts(name string) []string {
	raw := strings.Split(strings.TrimSpace(name), ":")
	parts := make([]string, 0, len(raw))
	for _, part := range raw {
		if part = strings.TrimSpace(part); part != "" {
			parts = append(parts, part)
		}
	}
	return parts
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
