package skill

import (
	"fmt"
	"sort"
	"strings"

	coreskill "github.com/fluxplane/agentruntime/core/skill"
)

// Repository is an immutable catalog of composed skill resources.
type Repository struct {
	skills map[string]coreskill.Spec
	order  []string
}

// NewRepository builds a deterministic skill repository. First contribution wins.
func NewRepository(specs []coreskill.Spec) (*Repository, error) {
	r := &Repository{skills: map[string]coreskill.Spec{}}
	for _, spec := range specs {
		if err := spec.Validate(); err != nil {
			return nil, err
		}
		name := strings.TrimSpace(string(spec.Name))
		if name == "" {
			continue
		}
		if _, exists := r.skills[name]; exists {
			continue
		}
		r.skills[name] = cloneSpec(spec)
		r.order = append(r.order, name)
	}
	sort.Strings(r.order)
	return r, nil
}

// List returns all skills ordered by name.
func (r *Repository) List() []coreskill.Spec {
	if r == nil {
		return nil
	}
	out := make([]coreskill.Spec, 0, len(r.order))
	for _, name := range r.order {
		out = append(out, cloneSpec(r.skills[name]))
	}
	return out
}

// Get returns one skill by name.
func (r *Repository) Get(name string) (coreskill.Spec, bool) {
	if r == nil {
		return coreskill.Spec{}, false
	}
	spec, ok := r.skills[strings.TrimSpace(name)]
	return cloneSpec(spec), ok
}

// GetReference returns one reference by exact relative path.
func (r *Repository) GetReference(name, path string) (coreskill.ReferenceSpec, bool) {
	spec, ok := r.Get(name)
	if !ok {
		return coreskill.ReferenceSpec{}, false
	}
	path = strings.TrimSpace(strings.ReplaceAll(path, "\\", "/"))
	for _, ref := range spec.References {
		if ref.Path == path {
			return cloneReference(ref), true
		}
	}
	return coreskill.ReferenceSpec{}, false
}

// MustGet returns a skill or an error suitable for user-facing operation output.
func (r *Repository) MustGet(name string) (coreskill.Spec, error) {
	spec, ok := r.Get(name)
	if !ok {
		return coreskill.Spec{}, fmt.Errorf("skill: %q not found", strings.TrimSpace(name))
	}
	return spec, nil
}

func cloneSpec(spec coreskill.Spec) coreskill.Spec {
	spec.Triggers = append([]string(nil), spec.Triggers...)
	spec.References = cloneReferences(spec.References)
	spec.Annotations = cloneMap(spec.Annotations)
	spec.Metadata = cloneMap(spec.Metadata)
	spec.Source.Annotations = cloneMap(spec.Source.Annotations)
	return spec
}

func cloneReferences(refs []coreskill.ReferenceSpec) []coreskill.ReferenceSpec {
	out := make([]coreskill.ReferenceSpec, 0, len(refs))
	for _, ref := range refs {
		out = append(out, cloneReference(ref))
	}
	return out
}

func cloneReference(ref coreskill.ReferenceSpec) coreskill.ReferenceSpec {
	ref.Triggers = append([]string(nil), ref.Triggers...)
	ref.Annotations = cloneMap(ref.Annotations)
	ref.Metadata = cloneMap(ref.Metadata)
	return ref
}

func cloneMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
