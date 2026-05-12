package skill

import (
	"fmt"
	"strings"
)

// Name identifies a skill.
type Name string

// Ref identifies a skill by name.
type Ref struct {
	Name Name `json:"name"`
}

// SourceRef describes where a skill can be loaded from without performing IO.
type SourceRef struct {
	URI         string            `json:"uri,omitempty"`
	Kind        string            `json:"kind,omitempty"`
	Annotations map[string]string `json:"annotations,omitempty"`
}

// Spec is an inert skill metadata declaration.
type Spec struct {
	Name        Name              `json:"name"`
	Description string            `json:"description,omitempty"`
	Source      SourceRef         `json:"source,omitempty"`
	Triggers    []string          `json:"triggers,omitempty"`
	Annotations map[string]string `json:"annotations,omitempty"`
}

// Validate checks the skill has a stable identity.
func (s Spec) Validate() error {
	if strings.TrimSpace(string(s.Name)) == "" {
		return fmt.Errorf("skill: spec name is empty")
	}
	return nil
}
