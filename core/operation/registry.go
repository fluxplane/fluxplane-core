package operation

import (
	"fmt"

	"github.com/fluxplane/fluxplane-core/core/registry"
)

// Registry stores executable operations by name.
//
// The registry performs pure lookup only. It does not execute, instantiate,
// wrap, validate, observe, or own operation lifecycle.
type Registry struct {
	inner *registry.Registry[Name, Operation]
}

// NewRegistry returns an empty operation registry.
func NewRegistry() *Registry {
	return &Registry{inner: registry.New[Name, Operation](func(op Operation) (Name, error) {
		if op == nil {
			return "", fmt.Errorf("operation: operation is nil")
		}
		name := op.Spec().Ref.Name
		if name == "" {
			return "", fmt.Errorf("operation: name is empty")
		}
		return name, nil
	})}
}

// Register adds operations to the registry.
func (r *Registry) Register(ops ...Operation) error {
	if r == nil || r.inner == nil {
		return fmt.Errorf("operation: registry is nil")
	}
	return r.inner.Register(ops...)
}

// Get returns the operation registered under name.
func (r *Registry) Get(name Name) (Operation, bool) {
	if r == nil || r.inner == nil {
		return nil, false
	}
	return r.inner.Get(name)
}

// Resolve returns the operation identified by ref.
func (r *Registry) Resolve(ref Ref) (Operation, bool) {
	return r.Get(ref.Name)
}

// All returns registered operations in unspecified order.
func (r *Registry) All() []Operation {
	if r == nil || r.inner == nil {
		return nil
	}
	return r.inner.All()
}
