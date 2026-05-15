package command

import "testing"

func TestCommandRegistryNewEmpty(t *testing.T) {
	r := NewRegistry()
	if r == nil {
		t.Fatal("NewRegistry returned nil")
	}
	if got := r.All(); len(got) != 0 {
		t.Fatalf("All() = %v, want empty", got)
	}
}

func TestCommandRegistryRegisterAndResolve(t *testing.T) {
	r := NewRegistry()
	spec := Spec{Path: Path{"agent", "run"}}
	if err := r.Register(spec); err != nil {
		t.Fatalf("Register: %v", err)
	}
	got, ok := r.Resolve(Path{"agent", "run"})
	if !ok {
		t.Fatal("Resolve returned false")
	}
	if len(got.Path) != 2 || got.Path[0] != "agent" {
		t.Fatalf("Resolve path = %v, want [agent run]", got.Path)
	}
}

func TestCommandRegistryResolveMissing(t *testing.T) {
	r := NewRegistry()
	_, ok := r.Resolve(Path{"no", "such"})
	if ok {
		t.Fatal("Resolve returned true for missing command")
	}
}

func TestCommandRegistryAll(t *testing.T) {
	r := NewRegistry()
	_ = r.Register(Spec{Path: Path{"a"}}, Spec{Path: Path{"b"}})
	all := r.All()
	if len(all) != 2 {
		t.Fatalf("All() len = %d, want 2", len(all))
	}
}

func TestCommandRegistryEmptyPath(t *testing.T) {
	r := NewRegistry()
	if err := r.Register(Spec{Path: Path{}}); err == nil {
		t.Fatal("Register with empty path should return error")
	}
}

func TestNilCommandRegistryResolve(t *testing.T) {
	var r *Registry
	_, ok := r.Resolve(Path{"x"})
	if ok {
		t.Fatal("nil Registry.Resolve should return false")
	}
}

func TestNilCommandRegistryAll(t *testing.T) {
	var r *Registry
	if got := r.All(); got != nil {
		t.Fatalf("nil Registry.All() = %v, want nil", got)
	}
}

func TestNilCommandRegistryRegister(t *testing.T) {
	var r *Registry
	if err := r.Register(Spec{Path: Path{"x"}}); err == nil {
		t.Fatal("nil Registry.Register should return error")
	}
}
