package resourcediscovery

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/fluxplane/agentruntime/core/agent"
	"github.com/fluxplane/agentruntime/core/resource"
)

// RenderTree renders discovered resources as a compact source/kind tree.
func RenderTree(out io.Writer, result Result) error {
	ids := resourceIDs(result.Bundles)
	w := treeWriter{out: out}
	w.printf("Root: %s\n\n", result.Root)
	w.println("Sources:")
	if len(result.Bundles) == 0 {
		w.println("  (none)")
	}
	for _, bundle := range result.Bundles {
		w.printf("  %s\n", sourceLabel(bundle.Source))
	}
	if len(ids) == 0 {
		w.println("\n(no resources)")
		renderDiagnostics(&w, result.Diagnostics)
		return w.err
	}
	renderResources(&w, result.Bundles, ids)
	renderResolution(&w, ids)
	renderDiagnostics(&w, result.Diagnostics)
	return w.err
}

// RenderJSON renders machine-readable discovery JSON.
func RenderJSON(out io.Writer, result Result) error {
	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")
	return enc.Encode(output(result))
}

// RenderYAML renders machine-readable discovery YAML.
func RenderYAML(out io.Writer, result Result) error {
	enc := yaml.NewEncoder(out)
	enc.SetIndent(2)
	return enc.Encode(output(result))
}

type Output struct {
	Root        string                `json:"root"`
	Sources     []resource.SourceRef  `json:"sources"`
	Resources   []resource.ResourceID `json:"resources"`
	Agents      []agent.Spec          `json:"agents,omitempty"`
	Skills      []string              `json:"skills,omitempty"`
	Diagnostics []resource.Diagnostic `json:"diagnostics,omitempty"`
}

func output(result Result) Output {
	out := Output{Root: result.Root, Resources: resourceIDs(result.Bundles), Diagnostics: result.Diagnostics}
	for _, bundle := range result.Bundles {
		out.Sources = append(out.Sources, bundle.Source)
		out.Agents = append(out.Agents, bundle.Agents...)
		for _, spec := range bundle.Skills {
			out.Skills = append(out.Skills, string(spec.Name))
		}
	}
	sort.Strings(out.Skills)
	return out
}

func resourceIDs(bundles []resource.ContributionBundle) []resource.ResourceID {
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
		for _, spec := range bundle.Operations {
			ids = append(ids, resource.DeriveResourceID(bundle.Source, "operation", string(spec.Ref.Name)))
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
	kinds := []string{"app", "session", "agent", "command", "workflow", "operation", "datasource", "skill", "context_provider"}
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
			out.printf("├── %ss\n", kind)
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
			seen[key] = id
		}
	}
	sort.Strings(keys)
	for _, key := range keys {
		out.printf("  %-24s -> %s\n", key, seen[key].Address())
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

func sourceLabel(source resource.SourceRef) string {
	if source.ID != "" {
		return source.ID
	}
	if source.Location != "" {
		return source.Location
	}
	return "(unknown)"
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

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
