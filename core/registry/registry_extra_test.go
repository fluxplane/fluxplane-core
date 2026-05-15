package registry

import (
	"fmt"
	"testing"
)

func TestRegisterNilRegistry(t *testing.T) {
	var r *Registry[string, string]
	err := r.Register("value")
	if err == nil {
		t.Fatal("Register on nil registry: want error")
	}
}

func TestRegisterNilKeyFunc(t *testing.T) {
	r := &Registry[string, string]{}
	err := r.Register("value")
	if err == nil {
		t.Fatal("Register with nil keyFunc: want error")
	}
}

func TestRegisterKeyError(t *testing.T) {
	r := New(func(v string) (string, error) {
		return "", fmt.Errorf("key error")
	})
	err := r.Register("value")
	if err == nil {
		t.Fatal("Register with key error: want error propagated")
	}
}

func TestRegisterZeroKey(t *testing.T) {
	r := New(func(v string) (string, error) {
		return "", nil // zero string key
	})
	err := r.Register("value")
	if err == nil {
		t.Fatal("Register with zero key: want error")
	}
}

func TestGetNilRegistry(t *testing.T) {
	var r *Registry[string, string]
	_, ok := r.Get("key")
	if ok {
		t.Fatal("Get on nil registry: want false")
	}
}

func TestAllNilRegistry(t *testing.T) {
	var r *Registry[string, string]
	if r.All() != nil {
		t.Fatal("All on nil registry: want nil")
	}
}
