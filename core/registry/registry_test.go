package registry

import "testing"

func TestRegistryRegisterGetAll(t *testing.T) {
	r := New[string, string](func(value string) (string, error) { return value, nil })
	if err := r.Register("a", "b"); err != nil {
		t.Fatalf("register: %v", err)
	}
	if value, ok := r.Get("a"); !ok || value != "a" {
		t.Fatalf("Get(a) = %q, %v; want a, true", value, ok)
	}
	if got := len(r.All()); got != 2 {
		t.Fatalf("All len = %d, want 2", got)
	}
}

func TestRegistryRejectsDuplicateKey(t *testing.T) {
	r := New[string, string](func(value string) (string, error) { return value, nil })
	if err := r.Register("a"); err != nil {
		t.Fatalf("register first: %v", err)
	}
	if err := r.Register("a"); err == nil {
		t.Fatal("register duplicate succeeded, want error")
	}
}

func TestRegistryRejectsZeroKey(t *testing.T) {
	r := New[string, string](func(value string) (string, error) { return value, nil })
	if err := r.Register(""); err == nil {
		t.Fatal("register zero key succeeded, want error")
	}
}

func TestNilRegistryGetAllAreSafe(t *testing.T) {
	var r *Registry[string, string]
	if value, ok := r.Get("a"); ok || value != "" {
		t.Fatalf("nil Get = %q, %v; want zero, false", value, ok)
	}
	if got := r.All(); got != nil {
		t.Fatalf("nil All = %#v, want nil", got)
	}
}
