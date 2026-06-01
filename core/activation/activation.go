package activation

import (
	"fmt"
	"strings"

	"github.com/fluxplane/fluxplane-core/core/command"
	corecontext "github.com/fluxplane/fluxplane-core/core/context"
	"github.com/fluxplane/fluxplane-core/core/operation"
	"github.com/fluxplane/fluxplane-core/core/resourceaddr"
	"github.com/fluxplane/fluxplane-core/core/skill"
	"github.com/fluxplane/fluxplane-core/core/workflow"
	"github.com/fluxplane/fluxplane-datasource"
)

// Set is an authored bundle of resources that can be prepared together for a
// work surface. It is inert; runtime and orchestration decide if and how it is
// applied.
type Set struct {
	Name        string            `json:"name"`
	Description string            `json:"description,omitempty"`
	Aliases     []string          `json:"aliases,omitempty"`
	Targets     []Target          `json:"targets,omitempty"`
	Annotations map[string]string `json:"annotations,omitempty"`
}

// Ref identifies an activation set by name.
type Ref struct {
	Name string `json:"name"`
}

// AnnotationIncludeConfiguredDatasources marks an activation set that should
// also grant access to configured datasource specs in the normalized app.
const AnnotationIncludeConfiguredDatasources = "fluxplane.include_configured_datasources"

// TargetKind identifies the kind of resource a target prepares.
type TargetKind string

const (
	TargetOperation       TargetKind = "operation"
	TargetOperationSet    TargetKind = "operation_set"
	TargetCommand         TargetKind = "command"
	TargetWorkflow        TargetKind = "workflow"
	TargetSkill           TargetKind = "skill"
	TargetReference       TargetKind = "reference"
	TargetContextProvider TargetKind = "context_provider"
	TargetDatasource      TargetKind = "datasource"
	TargetResource        TargetKind = "resource"
	TargetInlineContext   TargetKind = "inline_context"
)

// Target points at one inert resource that can become part of a prepared
// surface. Exactly one ref field must be populated and it must match Kind.
type Target struct {
	Kind            TargetKind              `json:"kind"`
	Operation       operation.Ref           `json:"operation,omitempty"`
	OperationSet    string                  `json:"operation_set,omitempty"`
	Command         command.Path            `json:"command,omitempty"`
	Workflow        workflow.Name           `json:"workflow,omitempty"`
	Skill           skill.Ref               `json:"skill,omitempty"`
	Reference       ReferenceTarget         `json:"reference,omitempty"`
	ContextProvider corecontext.ProviderRef `json:"context_provider,omitempty"`
	Datasource      datasource.Ref          `json:"datasource,omitempty"`
	ResourceAddr    resourceaddr.Address    `json:"resource_addr,omitempty"`
	InlineContext   *ContextTarget          `json:"inline_context,omitempty"`
	Annotations     map[string]string       `json:"annotations,omitempty"`
}

// ReferenceTarget prepares one skill-local reference.
type ReferenceTarget struct {
	Skill skill.Ref `json:"skill"`
	Path  string    `json:"path"`
}

// ContextTarget prepares an inert inline context block.
type ContextTarget struct {
	ID          string                `json:"id"`
	Title       string                `json:"title,omitempty"`
	Content     string                `json:"content,omitempty"`
	Template    string                `json:"template,omitempty"`
	Placement   corecontext.Placement `json:"placement,omitempty"`
	MediaType   string                `json:"media_type,omitempty"`
	Annotations map[string]string     `json:"annotations,omitempty"`
}

// Validate checks the set's structural shape without resolving refs.
func (s Set) Validate() error {
	name := strings.TrimSpace(s.Name)
	if name == "" {
		return fmt.Errorf("activation: set name is empty")
	}
	seenAliases := map[string]bool{}
	for i, alias := range s.Aliases {
		alias = strings.TrimSpace(alias)
		if alias == "" {
			return fmt.Errorf("activation: set %q aliases[%d] is empty", s.Name, i)
		}
		if alias == name {
			return fmt.Errorf("activation: set %q aliases[%d] duplicates set name", s.Name, i)
		}
		if seenAliases[alias] {
			return fmt.Errorf("activation: set %q alias %q is duplicated", s.Name, alias)
		}
		seenAliases[alias] = true
	}
	if len(s.Targets) == 0 {
		return fmt.Errorf("activation: set %q has no targets", s.Name)
	}
	for i, target := range s.Targets {
		if err := target.Validate(); err != nil {
			return fmt.Errorf("activation: set %q targets[%d]: %w", s.Name, i, err)
		}
	}
	return nil
}

// Validate checks that exactly one target ref is populated and matches Kind.
func (t Target) Validate() error {
	if !validKind(t.Kind) {
		return fmt.Errorf("kind %q is invalid", t.Kind)
	}
	populated := t.populatedRefs()
	if len(populated) != 1 {
		return fmt.Errorf("kind %q requires exactly one populated ref, got %d", t.Kind, len(populated))
	}
	expected := string(t.Kind)
	if populated[0] != expected {
		return fmt.Errorf("kind %q requires %s ref, got %s", t.Kind, expected, populated[0])
	}
	switch t.Kind {
	case TargetOperation:
		if strings.TrimSpace(string(t.Operation.Name)) == "" {
			return fmt.Errorf("operation name is empty")
		}
	case TargetOperationSet:
		if strings.TrimSpace(t.OperationSet) == "" {
			return fmt.Errorf("operation_set is empty")
		}
	case TargetCommand:
		if !validCommandPath(t.Command) {
			return fmt.Errorf("command path is empty")
		}
	case TargetWorkflow:
		if strings.TrimSpace(string(t.Workflow)) == "" {
			return fmt.Errorf("workflow name is empty")
		}
	case TargetSkill:
		if strings.TrimSpace(string(t.Skill.Name)) == "" {
			return fmt.Errorf("skill name is empty")
		}
	case TargetReference:
		if err := t.Reference.Validate(); err != nil {
			return err
		}
	case TargetContextProvider:
		if strings.TrimSpace(string(t.ContextProvider.Name)) == "" {
			return fmt.Errorf("context_provider name is empty")
		}
	case TargetDatasource:
		if strings.TrimSpace(string(t.Datasource.Name)) == "" {
			return fmt.Errorf("datasource name is empty")
		}
	case TargetResource:
		if t.ResourceAddr.IsZero() {
			return fmt.Errorf("resource address is empty")
		}
	case TargetInlineContext:
		if err := t.InlineContext.Validate(); err != nil {
			return err
		}
	}
	return nil
}

func (t Target) populatedRefs() []string {
	var refs []string
	if !t.Operation.IsZero() {
		refs = append(refs, string(TargetOperation))
	}
	if strings.TrimSpace(t.OperationSet) != "" {
		refs = append(refs, string(TargetOperationSet))
	}
	if validCommandPath(t.Command) {
		refs = append(refs, string(TargetCommand))
	}
	if strings.TrimSpace(string(t.Workflow)) != "" {
		refs = append(refs, string(TargetWorkflow))
	}
	if strings.TrimSpace(string(t.Skill.Name)) != "" {
		refs = append(refs, string(TargetSkill))
	}
	if !t.Reference.IsZero() {
		refs = append(refs, string(TargetReference))
	}
	if strings.TrimSpace(string(t.ContextProvider.Name)) != "" {
		refs = append(refs, string(TargetContextProvider))
	}
	if strings.TrimSpace(string(t.Datasource.Name)) != "" {
		refs = append(refs, string(TargetDatasource))
	}
	if !t.ResourceAddr.IsZero() {
		refs = append(refs, string(TargetResource))
	}
	if t.InlineContext != nil {
		refs = append(refs, string(TargetInlineContext))
	}
	return refs
}

func validKind(kind TargetKind) bool {
	switch kind {
	case TargetOperation,
		TargetOperationSet,
		TargetCommand,
		TargetWorkflow,
		TargetSkill,
		TargetReference,
		TargetContextProvider,
		TargetDatasource,
		TargetResource,
		TargetInlineContext:
		return true
	default:
		return false
	}
}

func validCommandPath(path command.Path) bool {
	for _, part := range path {
		if strings.TrimSpace(part) == "" {
			return false
		}
	}
	return len(path) > 0
}

// IsZero reports whether the reference target is empty.
func (t ReferenceTarget) IsZero() bool {
	return strings.TrimSpace(string(t.Skill.Name)) == "" && strings.TrimSpace(t.Path) == ""
}

// Validate checks that the reference target names a valid skill reference path.
func (t ReferenceTarget) Validate() error {
	if strings.TrimSpace(string(t.Skill.Name)) == "" {
		return fmt.Errorf("reference skill name is empty")
	}
	if !skill.ValidReferencePath(t.Path) {
		return fmt.Errorf("reference path %q is invalid", t.Path)
	}
	return nil
}

// Validate checks that inline context has a stable identity and content.
func (t *ContextTarget) Validate() error {
	if t == nil {
		return fmt.Errorf("inline_context is empty")
	}
	if strings.TrimSpace(t.ID) == "" {
		return fmt.Errorf("inline_context id is empty")
	}
	if strings.TrimSpace(t.Content) == "" && strings.TrimSpace(t.Template) == "" {
		return fmt.Errorf("inline_context content is empty")
	}
	switch t.Placement {
	case "", corecontext.PlacementUser, corecontext.PlacementSystem, corecontext.PlacementDeveloper:
		return nil
	default:
		return fmt.Errorf("inline_context placement %q is invalid", t.Placement)
	}
}
