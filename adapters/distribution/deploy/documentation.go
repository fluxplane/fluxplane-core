package deploy

import (
	"fmt"
	"sort"
	"strings"

	"github.com/fluxplane/engine/orchestration/distribution"
)

func distributionDocumentation(loaded distribution.Loaded) string {
	var b strings.Builder
	spec := loaded.Distribution.Spec
	title := firstNonEmpty(spec.Title, spec.Name, "Fluxplane App")
	fmt.Fprintf(&b, "# %s\n\n", title)
	if spec.Description != "" {
		fmt.Fprintf(&b, "%s\n\n", spec.Description)
	}
	writeDocKV(&b, "Name", spec.Name)
	writeDocKV(&b, "Version", spec.Version)
	writeDocKV(&b, "Profile", loaded.Profile)
	if len(loaded.Profiles) > 0 {
		writeDocKV(&b, "Profiles", strings.Join(loaded.Profiles, ", "))
	}
	if spec.DefaultModel.Model != "" || spec.DefaultModel.Provider != "" {
		writeDocKV(&b, "Default model", strings.Trim(strings.Join([]string{spec.DefaultModel.Provider, spec.DefaultModel.Model}, "/"), "/"))
	}
	writeDocSection(&b, "Agents", collectAgents(loaded))
	writeDocSection(&b, "Channels", collectChannels(loaded))
	writeDocSection(&b, "Datasources", collectDatasources(loaded))
	writeDocSection(&b, "Data Sources", collectDataSources(loaded))
	writeDocSection(&b, "Skills", collectSkills(loaded))
	writeDocSection(&b, "Context Providers", collectContextProviders(loaded))
	writeDocSection(&b, "Operations", collectOperations(loaded))
	writeDocSection(&b, "Operation Sets", collectOperationSets(loaded))
	writeDocSection(&b, "Tool Sets", collectToolSets(loaded))
	writeDocSection(&b, "Workflows", collectWorkflows(loaded))
	writeDocSection(&b, "Plugins", collectPlugins(loaded))
	writeDocSection(&b, "Models", collectModels(loaded))
	writeDocSection(&b, "Build Targets", collectBuildTargets(loaded))
	writeDocSection(&b, "Deploy Targets", collectDeployTargets(loaded))
	return b.String()
}

func writeDocKV(b *strings.Builder, key, value string) {
	value = strings.TrimSpace(value)
	if value == "" {
		return
	}
	fmt.Fprintf(b, "- **%s:** %s\n", key, value)
}

func writeDocSection(b *strings.Builder, title string, lines []string) {
	if len(lines) == 0 {
		return
	}
	sort.Strings(lines)
	fmt.Fprintf(b, "\n## %s\n\n", title)
	for _, line := range lines {
		fmt.Fprintf(b, "- %s\n", line)
	}
}

func collectAgents(loaded distribution.Loaded) []string {
	var out []string
	for _, bundle := range loaded.Distribution.Bundles {
		for _, spec := range bundle.Agents {
			out = append(out, docNameDescription(string(spec.Name), spec.Description))
		}
	}
	return out
}

func collectChannels(loaded distribution.Loaded) []string {
	var out []string
	for _, channel := range loaded.Launch.Channels {
		out = append(out, fmt.Sprintf("%s (%s, session %s)", channel.Name, channel.Type, channel.Session))
	}
	return out
}

func collectDatasources(loaded distribution.Loaded) []string {
	var out []string
	for _, bundle := range loaded.Distribution.Bundles {
		for _, spec := range bundle.Datasources {
			var entities []string
			for _, entity := range spec.Entities {
				entities = append(entities, string(entity))
			}
			out = append(out, docNameDescription(string(spec.Name), firstNonEmpty(spec.Description, spec.Kind+" "+strings.Join(entities, ", "))))
		}
	}
	return out
}

func collectDataSources(loaded distribution.Loaded) []string {
	var out []string
	for _, bundle := range loaded.Distribution.Bundles {
		for _, spec := range bundle.DataSources {
			var entities []string
			for _, entity := range spec.Entities {
				entities = append(entities, string(entity.Type))
			}
			out = append(out, docNameDescription(string(spec.Name), firstNonEmpty(spec.Description, spec.Kind+" "+strings.Join(entities, ", "))))
		}
	}
	return out
}

func collectSkills(loaded distribution.Loaded) []string {
	var out []string
	for _, bundle := range loaded.Distribution.Bundles {
		for _, spec := range bundle.Skills {
			out = append(out, docNameDescription(string(spec.Name), spec.Description))
		}
	}
	return out
}

func collectContextProviders(loaded distribution.Loaded) []string {
	var out []string
	for _, bundle := range loaded.Distribution.Bundles {
		for _, spec := range bundle.ContextProviders {
			out = append(out, docNameDescription(string(spec.Name), spec.Description))
		}
	}
	return out
}

func collectOperations(loaded distribution.Loaded) []string {
	var out []string
	for _, bundle := range loaded.Distribution.Bundles {
		for _, spec := range bundle.Operations {
			out = append(out, docNameDescription(spec.Ref.String(), spec.Description))
		}
	}
	return out
}

func collectOperationSets(loaded distribution.Loaded) []string {
	var out []string
	for _, bundle := range loaded.Distribution.Bundles {
		for _, spec := range bundle.OperationSets {
			out = append(out, docNameDescription(spec.Name, spec.Description))
		}
	}
	return out
}

func collectToolSets(loaded distribution.Loaded) []string {
	var out []string
	for _, bundle := range loaded.Distribution.Bundles {
		for _, spec := range bundle.ToolSets {
			out = append(out, docNameDescription(spec.Name, spec.Description))
		}
	}
	return out
}

func collectWorkflows(loaded distribution.Loaded) []string {
	var out []string
	for _, bundle := range loaded.Distribution.Bundles {
		for _, spec := range bundle.Workflows {
			out = append(out, docNameDescription(string(spec.Name), spec.Description))
		}
	}
	return out
}

func collectPlugins(loaded distribution.Loaded) []string {
	var out []string
	for _, bundle := range loaded.Distribution.Bundles {
		for _, spec := range bundle.Plugins {
			out = append(out, docNameDescription(spec.InstanceName(), spec.Name))
		}
	}
	return out
}

func collectModels(loaded distribution.Loaded) []string {
	var out []string
	for _, bundle := range loaded.Distribution.Bundles {
		for _, provider := range bundle.LLMProviders {
			out = append(out, docNameDescription(string(provider.Name), provider.Description))
		}
		for _, alias := range bundle.LLMModelAliases {
			out = append(out, fmt.Sprintf("%s -> %s", alias.Name, alias.Target.String()))
		}
	}
	return out
}

func collectBuildTargets(loaded distribution.Loaded) []string {
	var out []string
	for name, target := range loaded.Distribution.Spec.Build.Targets {
		out = append(out, docNameDescription(name, target.Kind))
	}
	return out
}

func collectDeployTargets(loaded distribution.Loaded) []string {
	var out []string
	for name, target := range loaded.Distribution.Spec.Deploy.Targets {
		out = append(out, docNameDescription(name, target.Kind))
	}
	return out
}

func docNameDescription(name, description string) string {
	name = strings.TrimSpace(name)
	description = strings.TrimSpace(description)
	if description == "" {
		return name
	}
	return name + " - " + description
}
