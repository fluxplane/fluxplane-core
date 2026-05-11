package command

import (
	"fmt"
	"strings"

	"github.com/fluxplane/agentruntime/core/registry"
)

// Registry stores command specs by path.
type Registry struct {
	inner *registry.Registry[string, Spec]
}

// NewRegistry returns an empty command registry.
func NewRegistry() *Registry {
	return &Registry{inner: registry.New[string, Spec](func(spec Spec) (string, error) {
		key := pathKey(spec.Path)
		if key == "" {
			return "", fmt.Errorf("command: path is empty")
		}
		return key, nil
	})}
}

// Register adds specs to the registry.
func (r *Registry) Register(specs ...Spec) error {
	if r == nil || r.inner == nil {
		return fmt.Errorf("command: registry is nil")
	}
	return r.inner.Register(specs...)
}

// Resolve returns the command spec registered for path.
func (r *Registry) Resolve(path Path) (Spec, bool) {
	if r == nil || r.inner == nil {
		return Spec{}, false
	}
	return r.inner.Get(pathKey(path))
}

// All returns registered command specs in unspecified order.
func (r *Registry) All() []Spec {
	if r == nil || r.inner == nil {
		return nil
	}
	return r.inner.All()
}

func pathKey(path Path) string {
	return strings.Join(path, "\x00")
}
