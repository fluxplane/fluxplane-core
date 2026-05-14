// Package modelview renders LLM provider catalogs for inspection surfaces.
package modelview

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"

	corellm "github.com/fluxplane/agentruntime/core/llm"
	"gopkg.in/yaml.v3"
)

// RenderTree renders providers and nested model IDs for humans.
func RenderTree(out io.Writer, providers []corellm.ProviderSpec) error {
	_, err := fmt.Fprintln(out, "Providers:")
	if err != nil {
		return err
	}
	for _, provider := range providers {
		if _, err := fmt.Fprintf(out, "%s\n", providerLabel(provider)); err != nil {
			return err
		}
		models := append([]corellm.ModelSpec(nil), provider.Models...)
		sort.Slice(models, func(i, j int) bool { return models[i].Ref.Name < models[j].Ref.Name })
		for i, model := range models {
			connector := "├── "
			if i == len(models)-1 {
				connector = "└── "
			}
			if _, err := fmt.Fprintf(out, "%s%s%s\n", connector, model.Ref.Name, modelDetails(model)); err != nil {
				return err
			}
		}
	}
	return nil
}

// RenderJSON renders providers as machine-readable JSON.
func RenderJSON(out io.Writer, providers []corellm.ProviderSpec) error {
	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")
	return enc.Encode(providers)
}

// RenderYAML renders providers as machine-readable YAML.
func RenderYAML(out io.Writer, providers []corellm.ProviderSpec) error {
	enc := yaml.NewEncoder(out)
	enc.SetIndent(2)
	return enc.Encode(providers)
}

func providerLabel(provider corellm.ProviderSpec) string {
	name := string(provider.Name)
	display := strings.TrimSpace(provider.DisplayName)
	if display == "" || display == name {
		return fmt.Sprintf("%s (%d models)", name, len(provider.Models))
	}
	return fmt.Sprintf("%s - %s (%d models)", name, display, len(provider.Models))
}

func modelDetails(model corellm.ModelSpec) string {
	var parts []string
	if model.ContextTokens > 0 {
		parts = append(parts, fmt.Sprintf("context %d", model.ContextTokens))
	}
	if model.MaxOutputTokens > 0 {
		parts = append(parts, fmt.Sprintf("max %d", model.MaxOutputTokens))
	}
	if len(model.Capabilities) > 0 {
		caps := make([]string, 0, len(model.Capabilities))
		for _, capability := range model.Capabilities {
			caps = append(caps, string(capability))
		}
		sort.Strings(caps)
		parts = append(parts, "capabilities "+strings.Join(caps, ", "))
	}
	if len(parts) == 0 {
		return ""
	}
	return " [" + strings.Join(parts, "; ") + "]"
}
