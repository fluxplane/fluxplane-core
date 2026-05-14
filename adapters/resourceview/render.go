// Package resourceview renders contribution bundles for inspection surfaces.
package resourceview

import (
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/fluxplane/agentruntime/core/agent"
	coreapp "github.com/fluxplane/agentruntime/core/app"
	corecommand "github.com/fluxplane/agentruntime/core/command"
	corecontext "github.com/fluxplane/agentruntime/core/context"
	coredatasource "github.com/fluxplane/agentruntime/core/datasource"
	corellm "github.com/fluxplane/agentruntime/core/llm"
	"github.com/fluxplane/agentruntime/core/operation"
	"github.com/fluxplane/agentruntime/core/resource"
	coresession "github.com/fluxplane/agentruntime/core/session"
	"github.com/fluxplane/agentruntime/core/skill"
	"github.com/fluxplane/agentruntime/core/workflow"
)

// Output is the structured static view of contribution bundles.
type Output struct {
	Sources          []resource.SourceRef       `json:"sources,omitempty" yaml:"sources,omitempty"`
	Resources        []resource.ResourceID      `json:"resources,omitempty" yaml:"resources,omitempty"`
	Apps             []coreapp.Spec             `json:"apps,omitempty" yaml:"apps,omitempty"`
	Sessions         []coresession.Spec         `json:"sessions,omitempty" yaml:"sessions,omitempty"`
	Agents           []agent.Spec               `json:"agents,omitempty" yaml:"agents,omitempty"`
	Commands         []corecommand.Spec         `json:"commands,omitempty" yaml:"commands,omitempty"`
	Workflows        []workflow.Spec            `json:"workflows,omitempty" yaml:"workflows,omitempty"`
	OperationSets    []operation.Set            `json:"operation_sets,omitempty" yaml:"operation_sets,omitempty"`
	Operations       []operation.Spec           `json:"operations,omitempty" yaml:"operations,omitempty"`
	Datasources      []coredatasource.Spec      `json:"datasources,omitempty" yaml:"datasources,omitempty"`
	LLMProviders     []corellm.ProviderSpec     `json:"llm_providers,omitempty" yaml:"llm_providers,omitempty"`
	Skills           []skill.Spec               `json:"skills,omitempty" yaml:"skills,omitempty"`
	ContextProviders []corecontext.ProviderSpec `json:"context_providers,omitempty" yaml:"context_providers,omitempty"`
	EventTypes       []string                   `json:"event_types,omitempty" yaml:"event_types,omitempty"`
	Plugins          []resource.PluginRef       `json:"plugins,omitempty" yaml:"plugins,omitempty"`
	Diagnostics      []resource.Diagnostic      `json:"diagnostics,omitempty" yaml:"diagnostics,omitempty"`
}

// TreeOptions configures human tree rendering without changing the structured
// contribution data.
type TreeOptions struct {
	ImplicitPlugins map[string]bool
}

// NewOutput builds a structured static view from bundles.
func NewOutput(bundles []resource.ContributionBundle, diagnostics []resource.Diagnostic) Output {
	out := Output{
		Resources:   ResourceIDs(bundles),
		Diagnostics: append([]resource.Diagnostic(nil), diagnostics...),
	}
	for _, bundle := range bundles {
		out.Sources = append(out.Sources, bundle.Source)
		out.Apps = append(out.Apps, bundle.Apps...)
		out.Sessions = append(out.Sessions, bundle.Sessions...)
		out.Agents = append(out.Agents, bundle.Agents...)
		out.Commands = append(out.Commands, bundle.Commands...)
		out.Workflows = append(out.Workflows, bundle.Workflows...)
		out.OperationSets = append(out.OperationSets, bundle.OperationSets...)
		out.Operations = append(out.Operations, bundle.Operations...)
		out.Datasources = append(out.Datasources, bundle.Datasources...)
		out.LLMProviders = append(out.LLMProviders, bundle.LLMProviders...)
		out.Skills = append(out.Skills, bundle.Skills...)
		out.ContextProviders = append(out.ContextProviders, bundle.ContextProviders...)
		for _, eventType := range bundle.EventTypes {
			if eventType != nil {
				out.EventTypes = append(out.EventTypes, string(eventType.EventName()))
			}
		}
		out.Plugins = append(out.Plugins, bundle.Plugins...)
		out.Diagnostics = append(out.Diagnostics, bundle.Diagnostics...)
	}
	sort.Strings(out.EventTypes)
	return out
}

// ResourceIDs derives canonical resource IDs for every static bundle resource.
func ResourceIDs(bundles []resource.ContributionBundle) []resource.ResourceID {
	var ids []resource.ResourceID
	for _, bundle := range bundles {
		for _, spec := range bundle.Apps {
			ids = append(ids, resource.DeriveResourceID(bundle.Source, "app", firstNonEmpty(string(spec.Name), "app")))
		}
		for _, spec := range bundle.Sessions {
			ids = append(ids, resource.DeriveResourceID(bundle.Source, "session", string(spec.Name)))
		}
		for _, spec := range bundle.Agents {
			ids = append(ids, resource.DeriveResourceID(bundle.Source, "agent", string(spec.Name)))
		}
		for _, spec := range bundle.Commands {
			ids = append(ids, resource.DeriveResourceID(bundle.Source, "command", spec.Path.String()))
		}
		for _, spec := range bundle.Workflows {
			ids = append(ids, resource.DeriveResourceID(bundle.Source, "workflow", string(spec.Name)))
		}
		for _, spec := range bundle.OperationSets {
			ids = append(ids, resource.DeriveResourceID(bundle.Source, "operation_set", spec.Name))
		}
		for _, spec := range bundle.Operations {
			ids = append(ids, resource.DeriveResourceID(bundle.Source, "operation", spec.Ref.String()))
		}
		for _, spec := range bundle.Datasources {
			ids = append(ids, resource.DeriveResourceID(bundle.Source, "datasource", string(spec.Name)))
		}
		for _, spec := range bundle.LLMProviders {
			ids = append(ids, resource.DeriveResourceID(bundle.Source, "llm_provider", string(spec.Name)))
		}
		for _, spec := range bundle.Skills {
			ids = append(ids, resource.DeriveResourceID(bundle.Source, "skill", string(spec.Name)))
		}
		for _, spec := range bundle.ContextProviders {
			ids = append(ids, resource.DeriveResourceID(bundle.Source, "context_provider", string(spec.Name)))
		}
		for _, eventType := range bundle.EventTypes {
			if eventType != nil {
				ids = append(ids, resource.DeriveResourceID(bundle.Source, "event_type", string(eventType.EventName())))
			}
		}
		for _, spec := range bundle.Plugins {
			ids = append(ids, resource.DeriveResourceID(bundle.Source, "plugin", spec.Name))
		}
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i].Address() < ids[j].Address() })
	return ids
}

// RenderTree renders contribution bundles as a compact source/kind tree.
func RenderTree(out io.Writer, bundles []resource.ContributionBundle, diagnostics []resource.Diagnostic) error {
	return RenderTreeWithOptions(out, bundles, diagnostics, TreeOptions{})
}

// RenderTreeWithOptions renders contribution bundles as a compact source/kind
// tree with presentation-only options.
func RenderTreeWithOptions(out io.Writer, bundles []resource.ContributionBundle, diagnostics []resource.Diagnostic, opts TreeOptions) error {
	desc := NewOutput(bundles, diagnostics)
	w := treeWriter{out: out}
	renderSources(&w, desc.Sources, opts)
	if len(desc.Resources) == 0 {
		w.println("\n(no resources)")
		renderDiagnostics(&w, desc.Diagnostics)
		return w.err
	}
	regular, pluginContributions := splitPluginContributionBundles(bundles)
	regularIDs := ResourceIDs(regular)
	if len(regularIDs) > 0 {
		renderResources(&w, regular, regularIDs, opts)
	}
	pluginIDs := ResourceIDs(pluginContributions)
	if len(pluginIDs) > 0 {
		w.println("\nPlugin contributions:")
		renderResources(&w, pluginContributions, pluginIDs, opts)
	}
	renderResolution(&w, desc.Resources)
	renderDiagnostics(&w, desc.Diagnostics)
	return w.err
}

func renderSources(out *treeWriter, sources []resource.SourceRef, opts TreeOptions) {
	out.println("Sources:")
	if len(sources) == 0 {
		out.println("  (none)")
		return
	}
	for _, source := range sources {
		out.printf("  %s\n", sourceLabel(source, opts))
	}
}

type resourceGroupKey struct {
	origin    string
	namespace string
}

func renderResources(out *treeWriter, bundles []resource.ContributionBundle, ids []resource.ResourceID, opts TreeOptions) {
	groups := map[resourceGroupKey]map[string][]string{}
	groupLabels := resourceGroupLabels(bundles, opts)
	operationSetMembers := operationSetMembersByGroup(bundles)
	var order []resourceGroupKey
	for _, id := range ids {
		key := resourceGroupKey{origin: id.Origin, namespace: id.Namespace.String()}
		if id.Kind == "operation" && operationSetMembers[key][id.Name] {
			continue
		}
		if groups[key] == nil {
			groups[key] = map[string][]string{}
			order = append(order, key)
		}
		groups[key][id.Kind] = append(groups[key][id.Kind], id.Name)
	}
	sort.Slice(order, func(i, j int) bool {
		if order[i].origin != order[j].origin {
			return order[i].origin < order[j].origin
		}
		return order[i].namespace < order[j].namespace
	})
	for _, key := range order {
		label := resourceGroupLabel(key, groupLabels)
		out.printf("\n%s\n", label)
		for _, kind := range contributionKinds() {
			names := groups[key][kind]
			if len(names) == 0 {
				continue
			}
			sort.Strings(names)
			out.printf("├── %s\n", pluralKind(kind))
			for i, name := range names {
				connector := "│   ├── "
				if i == len(names)-1 {
					connector = "│   └── "
				}
				out.printf("%s%s\n", connector, resourceNameLabel(kind, name, opts))
				if kind == "agent" {
					renderAgentRefs(out, bundles, name, i == len(names)-1)
				}
				if kind == "operation_set" {
					renderOperationSetRefs(out, bundles, key, name, i == len(names)-1)
				}
				if kind == "skill" {
					renderSkillRefs(out, bundles, name, i == len(names)-1)
				}
				if kind == "llm_provider" {
					renderLLMProviderRefs(out, bundles, name, i == len(names)-1)
				}
			}
		}
	}
}

func operationSetMembersByGroup(bundles []resource.ContributionBundle) map[resourceGroupKey]map[string]bool {
	out := map[resourceGroupKey]map[string]bool{}
	for _, bundle := range bundles {
		key := bundleGroupKey(bundle)
		for _, set := range bundle.OperationSets {
			for _, ref := range set.Operations {
				name := ref.String()
				if name == "" {
					continue
				}
				if out[key] == nil {
					out[key] = map[string]bool{}
				}
				out[key][name] = true
			}
		}
	}
	return out
}

func splitPluginContributionBundles(bundles []resource.ContributionBundle) ([]resource.ContributionBundle, []resource.ContributionBundle) {
	var regular []resource.ContributionBundle
	var plugins []resource.ContributionBundle
	for _, bundle := range bundles {
		if _, ok := pluginNameFromSource(bundle.Source); ok {
			plugins = append(plugins, bundle)
			continue
		}
		regular = append(regular, bundle)
	}
	return regular, plugins
}

func resourceGroupLabels(bundles []resource.ContributionBundle, opts TreeOptions) map[resourceGroupKey]string {
	labels := map[resourceGroupKey]string{}
	for _, bundle := range bundles {
		key := bundleGroupKey(bundle)
		labels[key] = groupLabel(key)
		if name, ok := pluginNameFromSource(bundle.Source); ok && opts.ImplicitPlugins[name] {
			labels[key] += " (implicit)"
		}
	}
	return labels
}

func bundleGroupKey(bundle resource.ContributionBundle) resourceGroupKey {
	return resourceGroupKey{
		origin:    resource.DeriveOrigin(bundle.Source),
		namespace: resource.DeriveNamespace(bundle.Source).String(),
	}
}

func resourceGroupLabel(key resourceGroupKey, labels map[resourceGroupKey]string) string {
	if label := labels[key]; label != "" {
		return label
	}
	return groupLabel(key)
}

func groupLabel(key resourceGroupKey) string {
	label := key.origin
	if key.namespace != "" {
		label += ":" + key.namespace
	}
	return label
}

func contributionKinds() []string {
	return []string{
		"app",
		"session",
		"agent",
		"command",
		"workflow",
		"operation_set",
		"operation",
		"datasource",
		"llm_provider",
		"skill",
		"context_provider",
		"event_type",
		"plugin",
	}
}

func renderAgentRefs(out *treeWriter, bundles []resource.ContributionBundle, name string, last bool) {
	indent := "│   │   "
	if last {
		indent = "│       "
	}
	for _, bundle := range bundles {
		for _, spec := range bundle.Agents {
			if string(spec.Name) != name {
				continue
			}
			if len(spec.Tools) > 0 {
				out.printf("%stools: %s\n", indent, strings.Join(agentToolNames(spec), ", "))
			}
			if len(spec.Operations) > 0 {
				out.printf("%soperations: %s\n", indent, strings.Join(agentOperationNames(spec), ", "))
			}
			if len(spec.Commands) > 0 {
				out.printf("%scommands: %s\n", indent, strings.Join(agentCommandNames(spec), ", "))
			}
			if len(spec.Skills) > 0 {
				out.printf("%sskills: %s\n", indent, strings.Join(agentSkillNames(spec), ", "))
			}
		}
	}
}

func renderOperationSetRefs(out *treeWriter, bundles []resource.ContributionBundle, key resourceGroupKey, name string, last bool) {
	indent := "│   │   "
	if last {
		indent = "│       "
	}
	var refs []string
	for _, bundle := range bundles {
		if bundleGroupKey(bundle) != key {
			continue
		}
		for _, spec := range bundle.OperationSets {
			if spec.Name != name {
				continue
			}
			for _, ref := range spec.Operations {
				if value := ref.String(); value != "" {
					refs = append(refs, value)
				}
			}
		}
	}
	sort.Strings(refs)
	for i, ref := range refs {
		connector := "├── "
		if i == len(refs)-1 {
			connector = "└── "
		}
		out.printf("%s%s%s\n", indent, connector, ref)
	}
}

func renderSkillRefs(out *treeWriter, bundles []resource.ContributionBundle, name string, last bool) {
	indent := "│   │   "
	if last {
		indent = "│       "
	}
	for _, bundle := range bundles {
		for _, spec := range bundle.Skills {
			if string(spec.Name) != name || len(spec.References) == 0 {
				continue
			}
			paths := make([]string, 0, len(spec.References))
			for _, ref := range spec.References {
				paths = append(paths, ref.Path)
			}
			sort.Strings(paths)
			out.printf("%sreferences: %s\n", indent, strings.Join(paths, ", "))
		}
	}
}

func renderLLMProviderRefs(out *treeWriter, bundles []resource.ContributionBundle, name string, last bool) {
	indent := "│   │   "
	if last {
		indent = "│       "
	}
	var models []string
	for _, bundle := range bundles {
		for _, spec := range bundle.LLMProviders {
			if string(spec.Name) != name {
				continue
			}
			for _, model := range spec.Models {
				if model.Ref.Name != "" {
					models = append(models, string(model.Ref.Name))
				}
			}
		}
	}
	sort.Strings(models)
	for i, model := range models {
		connector := "├── "
		if i == len(models)-1 {
			connector = "└── "
		}
		out.printf("%s%s%s\n", indent, connector, model)
	}
}

func renderResolution(out *treeWriter, ids []resource.ResourceID) {
	out.println("\nResolution:")
	seen := map[string]resource.ResourceID{}
	var keys []string
	for _, id := range ids {
		key := id.Kind + ":" + id.Name
		if _, ok := seen[key]; !ok {
			keys = append(keys, key)
		}
		seen[key] = id
	}
	sort.Strings(keys)
	for _, key := range keys {
		out.printf("  %-32s -> %s\n", key, seen[key].Address())
	}
}

func renderDiagnostics(out *treeWriter, diagnostics []resource.Diagnostic) {
	if len(diagnostics) == 0 {
		return
	}
	out.println("\nDiagnostics:")
	for _, diag := range diagnostics {
		out.printf("  %s %s\n", diag.Severity, diag.Message)
	}
}

// SourceLabel returns a compact human label for a resource source.
func SourceLabel(source resource.SourceRef) string {
	return sourceLabel(source, TreeOptions{})
}

func sourceLabel(source resource.SourceRef, opts TreeOptions) string {
	label := ""
	if source.ID != "" {
		label = source.ID
	} else if source.Location != "" {
		label = source.Location
	} else {
		label = "(unknown)"
	}
	if name, ok := pluginNameFromSource(source); ok && opts.ImplicitPlugins[name] {
		label += " (implicit)"
	}
	return label
}

func resourceNameLabel(kind, name string, opts TreeOptions) string {
	if kind == "plugin" && opts.ImplicitPlugins[name] {
		return name + " (implicit)"
	}
	return name
}

func pluginNameFromSource(source resource.SourceRef) (string, bool) {
	if name := strings.TrimPrefix(source.ID, "plugin:"); name != source.ID && strings.TrimSpace(name) != "" {
		return strings.TrimSpace(name), true
	}
	name := strings.TrimSpace(source.Ref)
	if name == "" {
		return "", false
	}
	location := strings.Trim(strings.TrimSpace(source.Location), "/")
	if location == "plugins/"+name || strings.HasSuffix(location, "/plugins/"+name) {
		return name, true
	}
	return "", false
}

type treeWriter struct {
	out io.Writer
	err error
}

func (w *treeWriter) printf(format string, args ...any) {
	if w.err != nil {
		return
	}
	_, w.err = fmt.Fprintf(w.out, format, args...)
}

func (w *treeWriter) println(args ...any) {
	if w.err != nil {
		return
	}
	_, w.err = fmt.Fprintln(w.out, args...)
}

func pluralKind(kind string) string {
	switch kind {
	case "app":
		return "apps"
	case "datasource":
		return "datasources"
	case "llm_provider":
		return "llm providers"
	default:
		return kind + "s"
	}
}

func agentToolNames(spec agent.Spec) []string {
	out := make([]string, 0, len(spec.Tools))
	for _, ref := range spec.Tools {
		out = append(out, ref.Name)
	}
	return out
}

func agentCommandNames(spec agent.Spec) []string {
	out := make([]string, 0, len(spec.Commands))
	for _, ref := range spec.Commands {
		out = append(out, ref.Name)
	}
	return out
}

func agentOperationNames(spec agent.Spec) []string {
	out := make([]string, 0, len(spec.Operations))
	for _, ref := range spec.Operations {
		out = append(out, ref.String())
	}
	return out
}

func agentSkillNames(spec agent.Spec) []string {
	out := make([]string, 0, len(spec.Skills))
	for _, ref := range spec.Skills {
		out = append(out, string(ref.Name))
	}
	return out
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
