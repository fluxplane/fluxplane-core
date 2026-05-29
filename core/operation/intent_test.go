package operation

import (
	"context"
	"testing"

	"github.com/fluxplane/fluxplane-event"
)

// intentOp is an Operation that also implements IntentProvider.
type intentOp struct {
	spec    Spec
	intents IntentSet
}

func (o intentOp) Spec() Spec                    { return o.spec }
func (o intentOp) Run(_ Context, _ Value) Result { return OK(nil) }
func (o intentOp) Intent(_ Context, _ Value) (IntentSet, error) {
	return o.intents, nil
}

// TestIntentSetNotEmptyOnly tests non-empty branch only;
// the empty branch is already in registry_test.go.
func TestIntentSetNotEmptyOnly(t *testing.T) {
	s := IntentSet{Operations: []IntentOperation{{Behavior: IntentFilesystemRead}}}
	if s.Empty() {
		t.Fatal("non-empty IntentSet.Empty() = true, want false")
	}
}

func TestIntentForNonProvider(t *testing.T) {
	op := New(Spec{Ref: Ref{Name: "noop"}}, nil)
	ctx := NewContext(context.Background(), event.Discard())
	_, provided, err := IntentFor(ctx, op, nil)
	if err != nil || provided {
		t.Fatalf("IntentFor(non-provider): provided=%v err=%v, want false/nil", provided, err)
	}
}

func TestIntentForProvider(t *testing.T) {
	expected := IntentSet{Operations: []IntentOperation{
		{Behavior: IntentFilesystemWrite, Target: PathTarget{Path: "/tmp/out"}},
	}}
	op := intentOp{spec: Spec{Ref: Ref{Name: "writer"}}, intents: expected}
	ctx := NewContext(context.Background(), event.Discard())
	got, provided, err := IntentFor(ctx, op, nil)
	if err != nil {
		t.Fatalf("IntentFor: %v", err)
	}
	if !provided {
		t.Fatal("IntentFor: provided = false, want true")
	}
	if got.Empty() || got.Operations[0].Behavior != IntentFilesystemWrite {
		t.Fatalf("IntentFor: got=%+v, want filesystem_write", got)
	}
}

func TestIntentTargetMarkerMethods(t *testing.T) {
	// Ensure all intentTarget() marker implementations compile and are callable.
	var _ IntentTarget = PathTarget{}
	var _ IntentTarget = URLTarget{}
	var _ IntentTarget = ProcessTarget{}
	var _ IntentTarget = BrowserTarget{}
	// Call each to confirm they don't panic.
	PathTarget{}.intentTarget()
	URLTarget{}.intentTarget()
	ProcessTarget{}.intentTarget()
	BrowserTarget{}.intentTarget()
}
