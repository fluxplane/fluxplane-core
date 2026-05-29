package skill

import (
	"fmt"
	"strings"

	"github.com/fluxplane/fluxplane-event"
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

// ReferenceSpec is inert metadata and content for one skill-local reference.
type ReferenceSpec struct {
	Path        string            `json:"path"`
	Body        string            `json:"body,omitempty"`
	Triggers    []string          `json:"triggers,omitempty"`
	Annotations map[string]string `json:"annotations,omitempty"`
	Metadata    map[string]string `json:"metadata,omitempty"`
}

// Spec is an inert skill metadata declaration.
type Spec struct {
	Name        Name              `json:"name"`
	Description string            `json:"description,omitempty"`
	Body        string            `json:"body,omitempty"`
	Source      SourceRef         `json:"source,omitempty"`
	Triggers    []string          `json:"triggers,omitempty"`
	References  []ReferenceSpec   `json:"references,omitempty"`
	Annotations map[string]string `json:"annotations,omitempty"`
	Metadata    map[string]string `json:"metadata,omitempty"`
}

// Validate checks the skill has a stable identity.
func (s Spec) Validate() error {
	if strings.TrimSpace(string(s.Name)) == "" {
		return fmt.Errorf("skill: spec name is empty")
	}
	seen := map[string]bool{}
	for i, ref := range s.References {
		path := strings.TrimSpace(ref.Path)
		if path == "" {
			return fmt.Errorf("skill: references[%d] path is empty", i)
		}
		if !ValidReferencePath(path) {
			return fmt.Errorf("skill: references[%d] path %q is invalid", i, ref.Path)
		}
		if seen[path] {
			return fmt.Errorf("skill: duplicate reference path %q", path)
		}
		seen[path] = true
	}
	return nil
}

// ValidReferencePath reports whether path is a safe skill-local reference path.
func ValidReferencePath(refPath string) bool {
	refPath = strings.TrimSpace(strings.ReplaceAll(refPath, "\\", "/"))
	if refPath == "" || strings.HasPrefix(refPath, "/") {
		return false
	}
	parts := strings.Split(refPath, "/")
	if len(parts) < 2 || parts[0] != "references" {
		return false
	}
	for _, part := range parts {
		if part == "" || part == "." || part == ".." {
			return false
		}
	}
	return parts[len(parts)-1] != "SKILL.md"
}

const (
	EventSkillActivated          event.Name = "skill.activated"
	EventSkillReferenceActivated event.Name = "skill.reference_activated"
)

// SkillActivated records that a session activated a skill.
type SkillActivated struct {
	Skill string `json:"skill"`
}

func (SkillActivated) EventName() event.Name { return EventSkillActivated }

// SkillReferenceActivated records that a session activated one skill reference.
type SkillReferenceActivated struct {
	Skill string `json:"skill"`
	Path  string `json:"path"`
}

func (SkillReferenceActivated) EventName() event.Name { return EventSkillReferenceActivated }
