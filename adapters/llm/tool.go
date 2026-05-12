package llm

import (
	"fmt"
	"strings"

	"github.com/fluxplane/agentruntime/core/invocation"
	"github.com/fluxplane/agentruntime/core/operation"
	"github.com/fluxplane/agentruntime/core/resource"
	"github.com/fluxplane/agentruntime/core/tool"
)

// ToolSpec is a provider-facing tool descriptor with enough metadata to map a
// provider tool call back to a canonical projected tool.
type ToolSpec struct {
	Name        tool.Name           `json:"name"`
	Description string              `json:"description,omitempty"`
	InputSchema operation.Schema    `json:"input_schema,omitempty"`
	Target      invocation.Target   `json:"target"`
	TargetID    resource.ResourceID `json:"target_id,omitempty"`
	Source      tool.Spec           `json:"source,omitempty"`
}

// ToolFromCore converts a projected core tool descriptor into the
// provider-facing adapter shape.
func ToolFromCore(spec tool.Spec) (ToolSpec, error) {
	if err := spec.Validate(); err != nil {
		return ToolSpec{}, err
	}
	return ToolSpec{
		Name:        spec.Name,
		Description: spec.Description,
		InputSchema: spec.Input.Schema,
		Target:      spec.Target,
		TargetID:    spec.TargetID,
		Source:      spec,
	}, nil
}

// ToolsFromCore converts projected tool specs into adapter tool specs.
func ToolsFromCore(specs []tool.Spec) ([]ToolSpec, error) {
	out := make([]ToolSpec, 0, len(specs))
	seen := map[tool.Name]struct{}{}
	for _, spec := range specs {
		converted, err := ToolFromCore(spec)
		if err != nil {
			return nil, err
		}
		if _, exists := seen[converted.Name]; exists {
			return nil, fmt.Errorf("llm: duplicate tool name %q", converted.Name)
		}
		seen[converted.Name] = struct{}{}
		out = append(out, converted)
	}
	return out, nil
}

func toolByName(tools []ToolSpec) map[tool.Name]ToolSpec {
	out := make(map[tool.Name]ToolSpec, len(tools))
	for _, spec := range tools {
		if strings.TrimSpace(string(spec.Name)) != "" {
			out[spec.Name] = spec
		}
	}
	return out
}
