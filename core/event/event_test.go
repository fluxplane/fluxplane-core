package event

import (
	"testing"
)

// testEvent implements Event for testing purposes.
type testEvent struct {
	data string
}

func (te testEvent) EventName() Name {
	return "test_event"
}

func TestSinkFuncEmit(t *testing.T) {
	var emitted []Event
	sink := SinkFunc(func(e Event) {
		emitted = append(emitted, e)
	})

	event := testEvent{data: "test"}
	sink.Emit(event)

	if len(emitted) != 1 {
		t.Fatalf("len(emitted) = %d, want 1", len(emitted))
	}
	if emitted[0] != event {
		t.Fatalf("emitted[0] = %v, want %v", emitted[0], event)
	}
}

func TestSinkFuncEmitNil(t *testing.T) {
	var sink SinkFunc
	// Should not panic even though sink is nil
	sink.Emit(testEvent{data: "test"})
}

func TestDiscardSinkEmit(t *testing.T) {
	sink := Discard()
	// Should not panic and not store anything
	sink.Emit(testEvent{data: "test"})
	// Verify it's the right type
	if _, ok := sink.(discardSink); !ok {
		t.Fatalf("Discard() returned %T, want discardSink", sink)
	}
}

func TestDiscardSinkMultipleEmits(t *testing.T) {
	sink := Discard()
	for i := 0; i < 100; i++ {
		sink.Emit(testEvent{data: "test"})
	}
	// Should not panic
}
