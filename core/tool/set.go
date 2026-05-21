package tool

import (
	"fmt"
	"strings"

	"github.com/fluxplane/engine/core/invocation"
	"github.com/fluxplane/engine/core/operation"
)

// Set groups related model-facing tools into one capability surface.
//
// Tool sets are projections over operations, commands, workflows, or other
// invocation targets. They are useful for enabling a capability like
// "filesystem" or "browser" without treating each atomic tool as unrelated.
type Set struct {
	Name        string            `json:"name"`
	Description string            `json:"description,omitempty"`
	Tools       []Name            `json:"tools,omitempty"`
	Action      *ActionProjection `json:"action,omitempty"`
	Annotations map[string]string `json:"annotations,omitempty"`
}

// ActionProjection describes an opt-in projection that exposes a tool set as
// one model-facing tool and dispatches by a required action field.
type ActionProjection struct {
	Tool        Name           `json:"tool,omitempty"`
	Description string         `json:"description,omitempty"`
	ActionField string         `json:"action_field,omitempty"`
	Input       operation.Type `json:"input,omitempty"`
	Output      operation.Type `json:"output,omitempty"`
	Cases       []ActionCase   `json:"cases,omitempty"`
}

// ActionCase maps one action discriminator value to an invocation target.
type ActionCase struct {
	Action string            `json:"action"`
	Target invocation.Target `json:"target"`
}

// Dispatch is copied onto a projected tool so adapters can resolve one
// provider tool call into a concrete invocation target.
type Dispatch struct {
	ActionField string         `json:"action_field,omitempty"`
	Cases       []DispatchCase `json:"cases,omitempty"`
}

// DispatchCase is one model action to invocation target mapping.
type DispatchCase struct {
	Action string            `json:"action"`
	Target invocation.Target `json:"target"`
}

// Validate checks that the set is structurally useful.
func (s Set) Validate() error {
	if strings.TrimSpace(s.Name) == "" {
		return fmt.Errorf("tool set name is empty")
	}
	if s.Action != nil {
		return s.Action.Validate()
	}
	return nil
}

// Validate checks that the action projection can dispatch deterministically.
func (p ActionProjection) Validate() error {
	if strings.TrimSpace(string(p.Tool)) == "" {
		return fmt.Errorf("tool set action projection tool is empty")
	}
	if strings.TrimSpace(p.ActionField) == "" {
		return fmt.Errorf("tool set action projection action field is empty")
	}
	if len(p.Cases) == 0 {
		return fmt.Errorf("tool set action projection has no cases")
	}
	seen := map[string]struct{}{}
	for i, c := range p.Cases {
		action := strings.TrimSpace(c.Action)
		if action == "" {
			return fmt.Errorf("tool set action projection cases[%d] action is empty", i)
		}
		if _, exists := seen[action]; exists {
			return fmt.Errorf("tool set action projection action %q is duplicated", action)
		}
		seen[action] = struct{}{}
		if c.Target.Kind != invocation.TargetOperation {
			return fmt.Errorf("tool set action projection action %q target kind %q is not operation", action, c.Target.Kind)
		}
		if c.Target.Operation.Name == "" {
			return fmt.Errorf("tool set action projection action %q operation name is empty", action)
		}
	}
	return nil
}
