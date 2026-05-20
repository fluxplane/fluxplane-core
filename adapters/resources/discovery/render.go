package discovery

import (
	"encoding/json"
	"fmt"
	"io"

	"gopkg.in/yaml.v3"

	"github.com/fluxplane/agentruntime/adapters/resources/resourceview"
)

// RenderTree renders discovered resources as a compact source/kind tree.
func RenderTree(out io.Writer, result Result) error {
	if _, err := fmt.Fprintf(out, "Root: %s\n\n", result.Root); err != nil {
		return err
	}
	return resourceview.RenderTreeWithOptions(out, result.Bundles, result.Diagnostics, resourceview.TreeOptions{
		ImplicitPlugins: result.ImplicitPlugins,
	})
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
	Root                string `json:"root" yaml:"root"`
	resourceview.Output `yaml:",inline"`
}

func output(result Result) Output {
	return Output{Root: result.Root, Output: resourceview.NewOutput(result.Bundles, result.Diagnostics)}
}
