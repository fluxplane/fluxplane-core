package registry

import (
	"fmt"
	"sync"
)

// KeyFunc derives a registry key for value.
type KeyFunc[K comparable, V any] func(V) (K, error)

// Registry stores values by derived key.
type Registry[K comparable, V any] struct {
	mu     sync.RWMutex
	keyOf  KeyFunc[K, V]
	values map[K]V
}

// New returns an empty registry.
func New[K comparable, V any](keyOf KeyFunc[K, V]) *Registry[K, V] {
	return &Registry[K, V]{
		keyOf:  keyOf,
		values: map[K]V{},
	}
}

// Register adds values to the registry.
func (r *Registry[K, V]) Register(values ...V) error {
	var zero K
	if r == nil {
		return fmt.Errorf("registry: registry is nil")
	}
	if r.keyOf == nil {
		return fmt.Errorf("registry: key function is nil")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.values == nil {
		r.values = map[K]V{}
	}
	for _, value := range values {
		key, err := r.keyOf(value)
		if err != nil {
			return err
		}
		if key == zero {
			return fmt.Errorf("registry: key is zero")
		}
		if _, exists := r.values[key]; exists {
			return fmt.Errorf("registry: duplicate key %v", key)
		}
		r.values[key] = value
	}
	return nil
}

// Get returns the value registered under key.
func (r *Registry[K, V]) Get(key K) (V, bool) {
	var zero V
	if r == nil {
		return zero, false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	value, ok := r.values[key]
	return value, ok
}

// All returns registered values in unspecified order.
func (r *Registry[K, V]) All() []V {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]V, 0, len(r.values))
	for _, value := range r.values {
		out = append(out, value)
	}
	return out
}
