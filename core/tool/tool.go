package tool

import (
	"fmt"
	"strings"

	"github.com/fluxplane/agentruntime/core/invocation"
	"github.com/fluxplane/agentruntime/core/operation"
	"github.com/fluxplane/agentruntime/core/policy"
	"github.com/fluxplane/agentruntime/core/resource"
)

// Name identifies a model-facing tool projection.
type Name string

// Spec describes one model-visible tool after policy and safety projection.
type Spec struct {
	Name        Name                    `json:"name"`
	Description string                  `json:"description,omitempty"`
	Target      invocation.Target       `json:"target"`
	TargetID    resource.ResourceID     `json:"target_id,omitempty"`
	Input       operation.Type          `json:"input,omitempty"`
	Output      operation.Type          `json:"output,omitempty"`
	Semantics   operation.Semantics     `json:"semantics,omitempty"`
	Policy      policy.InvocationPolicy `json:"policy,omitempty"`
	Annotations map[string]string       `json:"annotations,omitempty"`
}

// Validate checks the projected tool has a stable model-facing identity and
// target.
func (s Spec) Validate() error {
	if strings.TrimSpace(string(s.Name)) == "" {
		return fmt.Errorf("tool: spec name is empty")
	}
	if s.Target.Kind == "" {
		return fmt.Errorf("tool: target kind is empty")
	}
	return nil
}
