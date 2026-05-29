package operation

import (
	"context"
	"testing"

	"github.com/fluxplane/fluxplane-event"
)

func makeOp(name Name) Operation {
	return New(Spec{Ref: Ref{Name: name}}, func(ctx Context, v Value) Result {
		return OK(nil)
	})
}

func TestNewRegistryEmpty(t *testing.T) {
	r := NewRegistry()
	if r == nil {
		t.Fatal("NewRegistry returned nil")
	}
	if got := r.All(); len(got) != 0 {
		t.Fatalf("All() = %v, want empty", got)
	}
}

func TestRegistryRegisterAndGet(t *testing.T) {
	r := NewRegistry()
	op := makeOp("test-op")
	if err := r.Register(op); err != nil {
		t.Fatalf("Register: %v", err)
	}
	got, ok := r.Get("test-op")
	if !ok {
		t.Fatal("Get returned false, want true")
	}
	if got.Spec().Ref.Name != "test-op" {
		t.Fatalf("Spec.Ref.Name = %q, want test-op", got.Spec().Ref.Name)
	}
}

func TestRegistryGetMissing(t *testing.T) {
	r := NewRegistry()
	_, ok := r.Get("no-such-op")
	if ok {
		t.Fatal("Get returned true for missing op")
	}
}

func TestRegistryResolve(t *testing.T) {
	r := NewRegistry()
	_ = r.Register(makeOp("my-op"))
	got, ok := r.Resolve(Ref{Name: "my-op"})
	if !ok {
		t.Fatal("Resolve returned false")
	}
	if got.Spec().Ref.Name != "my-op" {
		t.Fatalf("resolved op name = %q, want my-op", got.Spec().Ref.Name)
	}
}

func TestRegistryAll(t *testing.T) {
	r := NewRegistry()
	_ = r.Register(makeOp("op-a"), makeOp("op-b"))
	all := r.All()
	if len(all) != 2 {
		t.Fatalf("All() len = %d, want 2", len(all))
	}
}

func TestRegistryRegisterNil(t *testing.T) {
	r := NewRegistry()
	if err := r.Register(nil); err == nil {
		t.Fatal("Register(nil) succeeded, want error")
	}
}

func TestRegistryRegisterEmptyName(t *testing.T) {
	r := NewRegistry()
	op := New(Spec{Ref: Ref{Name: ""}}, nil)
	if err := r.Register(op); err == nil {
		t.Fatal("Register with empty name succeeded, want error")
	}
}

func TestNilRegistryGet(t *testing.T) {
	var r *Registry
	_, ok := r.Get("anything")
	if ok {
		t.Fatal("nil Registry.Get should return false")
	}
}

func TestNilRegistryAll(t *testing.T) {
	var r *Registry
	if got := r.All(); got != nil {
		t.Fatalf("nil Registry.All() = %v, want nil", got)
	}
}

func TestNilRegistryRegister(t *testing.T) {
	var r *Registry
	if err := r.Register(makeOp("x")); err == nil {
		t.Fatal("nil Registry.Register should return error")
	}
}

func TestOperationEventNames(t *testing.T) {
	started := OperationStarted{}
	if started.EventName() != EventStartedName {
		t.Fatal("OperationStarted EventName mismatch")
	}
	completed := OperationCompleted{}
	if completed.EventName() != EventCompletedName {
		t.Fatal("OperationCompleted EventName mismatch")
	}
	failed := OperationFailed{}
	if failed.EventName() != EventFailedName {
		t.Fatal("OperationFailed EventName mismatch")
	}
	rejected := OperationRejected{}
	if rejected.EventName() != EventRejectedName {
		t.Fatal("OperationRejected EventName mismatch")
	}
	canceled := OperationCanceled{}
	if canceled.EventName() != EventCanceledName {
		t.Fatal("OperationCanceled EventName mismatch")
	}
}

func TestIntentSetEmpty(t *testing.T) {
	s := IntentSet{}
	if !s.Empty() {
		t.Fatal("empty IntentSet.Empty() = false, want true")
	}
	s.Operations = []IntentOperation{{Behavior: IntentFilesystemRead}}
	if s.Empty() {
		t.Fatal("non-empty IntentSet.Empty() = true, want false")
	}
}

func TestIntentTargetInterfaces(t *testing.T) {
	var _ IntentTarget = PathTarget{}
	var _ IntentTarget = URLTarget{}
	var _ IntentTarget = ProcessTarget{}
	var _ IntentTarget = BrowserTarget{}
}

func TestIntentForNoProvider(t *testing.T) {
	op := makeOp("plain-op")
	ctx := NewContext(context.Background(), event.Discard())
	_, ok, err := IntentFor(ctx, op, nil)
	if err != nil {
		t.Fatalf("IntentFor error: %v", err)
	}
	if ok {
		t.Fatal("IntentFor returned ok=true for non-IntentProvider")
	}
}

func TestEffectSetHas(t *testing.T) {
	s := EffectSet{EffectFilesystem, EffectNetwork}
	if !s.Has(EffectFilesystem) {
		t.Fatal("Has(EffectFilesystem) = false, want true")
	}
	if s.Has(EffectProcess) {
		t.Fatal("Has(EffectProcess) = true, want false")
	}
}

func TestEffectSetEmpty(t *testing.T) {
	if !(EffectSet{}).Empty() {
		t.Fatal("empty EffectSet.Empty() = false, want true")
	}
	if !(EffectSet{EffectNone}).Empty() {
		t.Fatal("EffectSet{EffectNone}.Empty() = false, want true")
	}
	if (EffectSet{EffectFilesystem}).Empty() {
		t.Fatal("non-none EffectSet.Empty() = true, want false")
	}
}

func TestEffectSetOnly(t *testing.T) {
	if !(EffectSet{}).Only(EffectNone) {
		t.Fatal("empty EffectSet.Only(EffectNone) = false, want true")
	}
	if !(EffectSet{EffectFilesystem, EffectFilesystem}).Only(EffectFilesystem) {
		t.Fatal("Only(EffectFilesystem) = false, want true")
	}
	if (EffectSet{EffectFilesystem, EffectNetwork}).Only(EffectFilesystem) {
		t.Fatal("mixed EffectSet.Only(EffectFilesystem) = true, want false")
	}
}

func TestSemanticsPure(t *testing.T) {
	pure := Semantics{
		Determinism: DeterminismDeterministic,
		Effects:     EffectSet{EffectNone},
	}
	if !pure.Pure() {
		t.Fatal("Pure() = false for deterministic + no-effects")
	}
	notPure := Semantics{
		Determinism: DeterminismDeterministic,
		Effects:     EffectSet{EffectFilesystem},
	}
	if notPure.Pure() {
		t.Fatal("Pure() = true for deterministic + filesystem effect")
	}
}

func TestSemanticsReadOnly(t *testing.T) {
	ro := Semantics{Effects: EffectSet{EffectReadExternal, EffectFilesystem}}
	if !ro.ReadOnly() {
		t.Fatal("ReadOnly() = false for read-only effects")
	}
	for _, effect := range []Effect{
		EffectWriteExternal, EffectCreate, EffectUpdate, EffectDelete,
		EffectDestructive, EffectIrreversible,
	} {
		s := Semantics{Effects: EffectSet{effect}}
		if s.ReadOnly() {
			t.Fatalf("ReadOnly() = true for effect %q, want false", effect)
		}
	}
}

func TestSetValidate(t *testing.T) {
	if err := (Set{Name: "my-set"}).Validate(); err != nil {
		t.Fatalf("Validate valid set: %v", err)
	}
	if err := (Set{}).Validate(); err == nil {
		t.Fatal("Validate empty-name set should fail")
	}
}

func TestTypeIsZero(t *testing.T) {
	if !(Type{}).IsZero() {
		t.Fatal("zero Type.IsZero() = false, want true")
	}
	if (Type{Name: "x"}).IsZero() {
		t.Fatal("named Type.IsZero() = true, want false")
	}
}
