// Package describe renders static distribution metadata and bundled resources.
package describe

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/fluxplane/agentruntime/core/agent"
	coreapp "github.com/fluxplane/agentruntime/core/app"
	corecommand "github.com/fluxplane/agentruntime/core/command"
	corecontext "github.com/fluxplane/agentruntime/core/context"
	coredatasource "github.com/fluxplane/agentruntime/core/datasource"
	coredistribution "github.com/fluxplane/agentruntime/core/distribution"
	"github.com/fluxplane/agentruntime/core/operation"
	"github.com/fluxplane/agentruntime/core/resource"
	coresession "github.com/fluxplane/agentruntime/core/session"
	"github.com/fluxplane/agentruntime/core/skill"
	"github.com/fluxplane/agentruntime/core/workflow"
	"github.com/fluxplane/agentruntime/orchestration/distribution"
)

var (
	// ErrAgentNotFound reports that no bundled static agent matched the ref.
	ErrAgentNotFound = errors.New("distribution describe: agent not found")
	// ErrAgentAmbiguous reports that a ref matched multiple bundled agents.
	ErrAgentAmbiguous = errors.New("distribution describe: agent is ambiguous")
)

// Output is the structured static description of a distribution.
type Output struct {
	Distribution     coredistribution.Spec      `json:"distribution" yaml:"distribution"`
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
	Skills           []skill.Spec               `json:"skills,omitempty" yaml:"skills,omitempty"`
	ContextProviders []corecontext.ProviderSpec `json:"context_providers,omitempty" yaml:"context_providers,omitempty"`
	Plugins          []resource.PluginRef       `json:"plugins,omitempty" yaml:"plugins,omitempty"`
	Diagnostics      []resource.Diagnostic      `json:"diagnostics,omitempty" yaml:"diagnostics,omitempty"`
}

// AgentOutput is the structured static description of one bundled agent.
type AgentOutput struct {
	Distribution coredistribution.Spec `json:"distribution" yaml:"distribution"`
	Resource     resource.ResourceID   `json:"resource" yaml:"resource"`
	Source       resource.SourceRef    `json:"source" yaml:"source"`
	Agent        agent.Spec            `json:"agent" yaml:"agent"`
	Apps         []coreapp.Spec        `json:"apps,omitempty" yaml:"apps,omitempty"`
	Sessions     []coresession.Spec    `json:"sessions,omitempty" yaml:"sessions,omitempty"`
}

// NewOutput builds a static description from distribution metadata and bundles.
func NewOutput(dist distribution.Distribution) Output {
	out := Output{
		Distribution: dist.Spec,
		Resources:    ResourceIDs(dist.Bundles),
	}
	for _, bundle := range dist.Bundles {
		out.Sources = append(out.Sources, bundle.Source)
		out.Apps = append(out.Apps, bundle.Apps...)
		out.Sessions = append(out.Sessions, bundle.Sessions...)
		out.Agents = append(out.Agents, bundle.Agents...)
		out.Commands = append(out.Commands, bundle.Commands...)
		out.Workflows = append(out.Workflows, bundle.Workflows...)
		out.OperationSets = append(out.OperationSets, bundle.OperationSets...)
		out.Operations = append(out.Operations, bundle.Operations...)
		out.Datasources = append(out.Datasources, bundle.Datasources...)
		out.Skills = append(out.Skills, bundle.Skills...)
		out.ContextProviders = append(out.ContextProviders, bundle.ContextProviders...)
		out.Plugins = append(out.Plugins, bundle.Plugins...)
		out.Diagnostics = append(out.Diagnostics, bundle.Diagnostics...)
	}
	return out
}

// RenderTree renders distribution metadata and bundled resources for humans.
func RenderTree(out io.Writer, dist distribution.Distribution) error {
	desc := NewOutput(dist)
	w := treeWriter{out: out}
	renderDistribution(&w, desc.Distribution)
	renderSources(&w, desc.Sources)
	if len(desc.Resources) == 0 {
		w.println("\n(no resources)")
		renderDiagnostics(&w, desc.Diagnostics)
		return w.err
	}
	renderResources(&w, dist.Bundles, desc.Resources)
	renderResolution(&w, desc.Resources)
	renderDiagnostics(&w, desc.Diagnostics)
	return w.err
}

// RenderJSON renders machine-readable distribution description JSON.
func RenderJSON(out io.Writer, dist distribution.Distribution) error {
	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")
	return enc.Encode(NewOutput(dist))
}

// RenderYAML renders machine-readable distribution description YAML.
func RenderYAML(out io.Writer, dist distribution.Distribution) error {
	enc := yaml.NewEncoder(out)
	enc.SetIndent(2)
	return enc.Encode(NewOutput(dist))
}

// RenderAgentTree renders one bundled agent in a detailed human-readable form.
func RenderAgentTree(out io.Writer, dist distribution.Distribution, ref string) error {
	desc, err := Agent(dist, ref)
	if err != nil {
		return err
	}
	w := treeWriter{out: out}
	renderAgentDetail(&w, desc)
	return w.err
}

// RenderAgentJSON renders one bundled agent as machine-readable JSON.
func RenderAgentJSON(out io.Writer, dist distribution.Distribution, ref string) error {
	desc, err := Agent(dist, ref)
	if err != nil {
		return err
	}
	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")
	return enc.Encode(desc)
}

// RenderAgentYAML renders one bundled agent as machine-readable YAML.
func RenderAgentYAML(out io.Writer, dist distribution.Distribution, ref string) error {
	desc, err := Agent(dist, ref)
	if err != nil {
		return err
	}
	enc := yaml.NewEncoder(out)
	enc.SetIndent(2)
	return enc.Encode(desc)
}

// Agent returns one bundled agent description by local name or resource ref.
func Agent(dist distribution.Distribution, ref string) (AgentOutput, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return AgentOutput{}, fmt.Errorf("%w: empty ref", ErrAgentNotFound)
	}
	var matches []AgentOutput
	for _, bundle := range dist.Bundles {
		for _, spec := range bundle.Agents {
			id := resource.DeriveResourceID(bundle.Source, "agent", string(spec.Name))
			if !agentMatches(id, spec, ref) {
				continue
			}
			matches = append(matches, AgentOutput{
				Distribution: dist.Spec,
				Resource:     id,
				Source:       bundle.Source,
				Agent:        spec,
				Apps:         appsForAgent(bundle, spec),
				Sessions:     sessionsForAgent(bundle, spec),
			})
		}
	}
	switch len(matches) {
	case 0:
		return AgentOutput{}, fmt.Errorf("%w: %s", ErrAgentNotFound, ref)
	case 1:
		return matches[0], nil
	default:
		return AgentOutput{}, fmt.Errorf("%w: %s matches %s", ErrAgentAmbiguous, ref, agentAddresses(matches))
	}
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
		for _, spec := range bundle.Skills {
			ids = append(ids, resource.DeriveResourceID(bundle.Source, "skill", string(spec.Name)))
		}
		for _, spec := range bundle.ContextProviders {
			ids = append(ids, resource.DeriveResourceID(bundle.Source, "context_provider", string(spec.Name)))
		}
		for _, spec := range bundle.Plugins {
			ids = append(ids, resource.DeriveResourceID(bundle.Source, "plugin", spec.Name))
		}
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i].Address() < ids[j].Address() })
	return ids
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

func renderDistribution(out *treeWriter, spec coredistribution.Spec) {
	out.println("Distribution:")
	line(out, "name", spec.Name)
	line(out, "title", spec.Title)
	line(out, "description", spec.Description)
	line(out, "author", spec.Author)
	line(out, "version", spec.Version)
	line(out, "default session", string(spec.DefaultSession.Name))
	line(out, "default conversation", spec.DefaultConversation.ID)
	if model := modelLabel(spec.DefaultModel); model != "" {
		line(out, "default model", model)
	}
	if surfaces := surfaceNames(spec.Surfaces); len(surfaces) > 0 {
		line(out, "surfaces", strings.Join(surfaces, ", "))
	}
	if len(spec.Commands) > 0 {
		var names []string
		for _, command := range spec.Commands {
			names = append(names, command.Name)
		}
		sort.Strings(names)
		line(out, "commands", strings.Join(names, ", "))
	}
}

func renderSources(out *treeWriter, sources []resource.SourceRef) {
	out.println("\nSources:")
	if len(sources) == 0 {
		out.println("  (none)")
		return
	}
	for _, source := range sources {
		out.printf("  %s\n", sourceLabel(source))
	}
}

func renderResources(out *treeWriter, bundles []resource.ContributionBundle, ids []resource.ResourceID) {
	type groupKey struct {
		origin    string
		namespace string
	}
	groups := map[groupKey]map[string][]string{}
	var order []groupKey
	for _, id := range ids {
		key := groupKey{origin: id.Origin, namespace: id.Namespace.String()}
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
	kinds := []string{"app", "session", "agent", "command", "workflow", "operation_set", "operation", "datasource", "skill", "context_provider", "plugin"}
	for _, key := range order {
		label := key.origin
		if key.namespace != "" {
			label += ":" + key.namespace
		}
		out.printf("\n%s\n", label)
		for _, kind := range kinds {
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
				out.printf("%s%s\n", connector, name)
				if kind == "agent" {
					renderAgentRefs(out, bundles, name, i == len(names)-1)
				}
				if kind == "skill" {
					renderSkillRefs(out, bundles, name, i == len(names)-1)
				}
			}
		}
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
			if len(spec.Commands) > 0 {
				out.printf("%scommands: %s\n", indent, strings.Join(agentCommandNames(spec), ", "))
			}
			if len(spec.Skills) > 0 {
				out.printf("%sskills: %s\n", indent, strings.Join(agentSkillNames(spec), ", "))
			}
		}
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

func renderAgentDetail(out *treeWriter, desc AgentOutput) {
	spec := desc.Agent
	out.println("Agent:")
	line(out, "name", string(spec.Name))
	line(out, "resource", desc.Resource.Address())
	line(out, "source", sourceLabel(desc.Source))
	line(out, "description", spec.Description)
	if spec.Driver.Kind != "" {
		line(out, "driver", string(spec.Driver.Kind))
	}
	if spec.Inference.Model != "" {
		line(out, "model", spec.Inference.Model)
	}
	if spec.Inference.MaxOutputTokens > 0 {
		line(out, "max output tokens", fmt.Sprint(spec.Inference.MaxOutputTokens))
	}
	if spec.Policy.MaxSteps > 0 {
		line(out, "max steps", fmt.Sprint(spec.Policy.MaxSteps))
	}
	if spec.Policy.MaxContinuations > 0 {
		line(out, "max continuations", fmt.Sprint(spec.Policy.MaxContinuations))
	}
	if agency := agencyLabel(spec.Agency); agency != "" {
		line(out, "agency", agency)
	}
	if spec.Objective.Role != "" || spec.Objective.Instructions != "" || spec.Objective.Success != "" {
		out.println("\nObjective:")
		line(out, "role", spec.Objective.Role)
		line(out, "instructions", spec.Objective.Instructions)
		line(out, "success", spec.Objective.Success)
	}
	renderStringList(out, "Operations", operationNames(spec.Operations))
	renderStringList(out, "Tools", agentToolNames(spec))
	renderStringList(out, "Commands", agentCommandNames(spec))
	renderStringList(out, "Datasources", agentDatasourceNames(spec))
	renderStringList(out, "Skills", agentSkillNames(spec))
	renderStringList(out, "Context Providers", agentContextNames(spec))
	renderRelated(out, desc)
	if strings.TrimSpace(spec.System) != "" {
		out.println("\nSystem:")
		for _, line := range strings.Split(spec.System, "\n") {
			out.printf("  %s\n", line)
		}
	}
}

func renderStringList(out *treeWriter, title string, values []string) {
	if len(values) == 0 {
		return
	}
	sort.Strings(values)
	out.printf("\n%s:\n", title)
	for _, value := range values {
		out.printf("  - %s\n", value)
	}
}

func renderRelated(out *treeWriter, desc AgentOutput) {
	if len(desc.Apps) == 0 && len(desc.Sessions) == 0 {
		return
	}
	out.println("\nRelated:")
	if len(desc.Apps) > 0 {
		var names []string
		for _, spec := range desc.Apps {
			names = append(names, firstNonEmpty(string(spec.Name), "app"))
		}
		sort.Strings(names)
		out.printf("  apps: %s\n", strings.Join(names, ", "))
	}
	if len(desc.Sessions) > 0 {
		var names []string
		for _, spec := range desc.Sessions {
			names = append(names, string(spec.Name))
		}
		sort.Strings(names)
		out.printf("  sessions: %s\n", strings.Join(names, ", "))
	}
}

func line(out *treeWriter, key, value string) {
	if strings.TrimSpace(value) == "" {
		return
	}
	out.printf("  %-20s %s\n", key+":", value)
}

func modelLabel(model coredistribution.ModelDefault) string {
	switch {
	case model.Provider != "" && model.Model != "" && model.UseCase != "":
		return model.Provider + "/" + model.Model + " (" + model.UseCase + ")"
	case model.Provider != "" && model.Model != "":
		return model.Provider + "/" + model.Model
	case model.Model != "":
		return model.Model
	default:
		return model.Provider
	}
}

func surfaceNames(s coredistribution.Surfaces) []string {
	var out []string
	if s.CLI {
		out = append(out, "cli")
	}
	if s.REPL {
		out = append(out, "repl")
	}
	if s.OneShot {
		out = append(out, "one-shot")
	}
	if s.Serve {
		out = append(out, "serve")
	}
	if s.Deploy {
		out = append(out, "deploy")
	}
	if s.Validate {
		out = append(out, "validate")
	}
	if s.Status {
		out = append(out, "status")
	}
	if s.Discover {
		out = append(out, "discover")
	}
	return out
}

func sourceLabel(source resource.SourceRef) string {
	if source.ID != "" {
		return source.ID
	}
	if source.Location != "" {
		return source.Location
	}
	return "(unknown)"
}

func pluralKind(kind string) string {
	switch kind {
	case "app":
		return "apps"
	case "datasource":
		return "datasources"
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

func agentSkillNames(spec agent.Spec) []string {
	out := make([]string, 0, len(spec.Skills))
	for _, ref := range spec.Skills {
		out = append(out, string(ref.Name))
	}
	return out
}

func agentDatasourceNames(spec agent.Spec) []string {
	out := make([]string, 0, len(spec.Datasources))
	for _, ref := range spec.Datasources {
		out = append(out, string(ref.Name))
	}
	return out
}

func agentContextNames(spec agent.Spec) []string {
	out := make([]string, 0, len(spec.Context))
	for _, ref := range spec.Context {
		out = append(out, string(ref.Name))
	}
	return out
}

func operationNames(refs []operation.Ref) []string {
	out := make([]string, 0, len(refs))
	for _, ref := range refs {
		out = append(out, ref.String())
	}
	return out
}

func agencyLabel(agency agent.AgencyProfile) string {
	var parts []string
	if agency.Autonomy != "" {
		parts = append(parts, string(agency.Autonomy))
	}
	if agency.Reactive {
		parts = append(parts, "reactive")
	}
	if agency.Proactive {
		parts = append(parts, "proactive")
	}
	if agency.Social {
		parts = append(parts, "social")
	}
	if agency.Stateful {
		parts = append(parts, "stateful")
	}
	if agency.Learning {
		parts = append(parts, "learning")
	}
	return strings.Join(parts, ", ")
}

func agentMatches(id resource.ResourceID, spec agent.Spec, ref string) bool {
	return string(spec.Name) == ref || id.Address() == ref || id.MatchesRef(ref)
}

func appsForAgent(bundle resource.ContributionBundle, spec agent.Spec) []coreapp.Spec {
	var out []coreapp.Spec
	for _, appSpec := range bundle.Apps {
		if appSpec.DefaultAgent.Name == spec.Name {
			out = append(out, appSpec)
		}
	}
	return out
}

func sessionsForAgent(bundle resource.ContributionBundle, spec agent.Spec) []coresession.Spec {
	var out []coresession.Spec
	for _, sessionSpec := range bundle.Sessions {
		if sessionSpec.Agent.Name == spec.Name {
			out = append(out, sessionSpec)
		}
	}
	return out
}

func agentAddresses(matches []AgentOutput) string {
	out := make([]string, 0, len(matches))
	for _, match := range matches {
		out = append(out, match.Resource.Address())
	}
	sort.Strings(out)
	return strings.Join(out, ", ")
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
