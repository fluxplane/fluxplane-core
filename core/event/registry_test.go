package event

import (
	"encoding/json"
	"testing"
)

// concreteEvent is a concrete event for testing.
type concreteEvent struct {
	Message string `json:"message"`
}

func (ce concreteEvent) EventName() Name {
	return "concrete_event"
}

// pointerEvent is a pointer-based event for testing.
type pointerEvent struct {
	Value int `json:"value"`
}

func (pe *pointerEvent) EventName() Name {
	return "pointer_event"
}

func TestNewRegistry(t *testing.T) {
	reg := NewRegistry()
	if reg == nil {
		t.Fatal("NewRegistry returned nil")
	}
	if reg.decoders == nil {
		t.Fatal("decoders map is nil")
	}
}

func TestRegistryRegister(t *testing.T) {
	reg := NewRegistry()
	event := concreteEvent{Message: "test"}

	err := reg.Register(event)
	if err != nil {
		t.Fatalf("Register failed: %v", err)
	}

	// Try to register the same event again - should fail
	err = reg.Register(event)
	if err == nil {
		t.Fatal("Register duplicate should fail")
	}
}

func TestRegistryRegisterNil(t *testing.T) {
	reg := NewRegistry()
	err := reg.Register(nil)
	if err == nil {
		t.Fatal("Register(nil) should fail")
	}
}

func TestRegistryRegisterEmptyName(t *testing.T) {
	reg := NewRegistry()

	// Create a mock event with empty name
	type emptyNameEvent struct{}
	func (ene emptyNameEvent) EventName() Name { return "" }

	err := reg.Register(emptyNameEvent{})
	if err == nil {
		t.Fatal("Register with empty name should fail")
	}
}

func TestRegistryDecode(t *testing.T) {
	reg := NewRegistry()
	sample := concreteEvent{Message: ""}
	if err := reg.Register(sample); err != nil {
		t.Fatalf("Register failed: %v", err)
	}

	rawJSON := json.RawMessage(`{"message":"hello"}`)
	decoded, err := reg.Decode("concrete_event", rawJSON)
	if err != nil {
		t.Fatalf("Decode failed: %v", err)
	}

	if decoded == nil {
		t.Fatal("decoded is nil")
	}

	ce, ok := decoded.(concreteEvent)
	if !ok {
		t.Fatalf("decoded is %T, want concreteEvent", decoded)
	}

	if ce.Message != "hello" {
		t.Fatalf("Message = %q, want hello", ce.Message)
	}
}

func TestRegistryDecodeNotFound(t *testing.T) {
	reg := NewRegistry()
	rawJSON := json.RawMessage(`{}`)

	_, err := reg.Decode("unknown_event", rawJSON)
	if err == nil {
		t.Fatal("Decode unknown event should fail")
	}
}

func TestRegistryDecodeNilRegistry(t *testing.T) {
	var reg *Registry

	_, err := reg.Decode("test", json.RawMessage(`{}`))
	if err == nil {
		t.Fatal("Decode on nil registry should fail")
	}
}

func TestRegistryTryDecode(t *testing.T) {
	reg := NewRegistry()
	sample := concreteEvent{Message: ""}
	if err := reg.Register(sample); err != nil {
		t.Fatalf("Register failed: %v", err)
	}

	rawJSON := json.RawMessage(`{"message":"test"}`)
	decoded, found, err := reg.TryDecode("concrete_event", rawJSON)

	if !found {
		t.Fatal("found = false, want true")
	}
	if err != nil {
		t.Fatalf("TryDecode failed: %v", err)
	}
	if decoded == nil {
		t.Fatal("decoded is nil")
	}
}

func TestRegistryTryDecodeNotFound(t *testing.T) {
	reg := NewRegistry()
	rawJSON := json.RawMessage(`{}`)

	decoded, found, err := reg.TryDecode("unknown_event", rawJSON)

	if found {
		t.Fatal("found = true, want false")
	}
	if err != nil {
		t.Fatalf("TryDecode should not error: %v", err)
	}
	if decoded != nil {
		t.Fatal("decoded should be nil")
	}
}

func TestRegistryTryDecodeNilRegistry(t *testing.T) {
	var reg *Registry

	decoded, found, err := reg.TryDecode("test", json.RawMessage(`{}`))

	if found {
		t.Fatal("found = true, want false for nil registry")
	}
	if err != nil {
		t.Fatalf("TryDecode should not error on nil registry: %v", err)
	}
	if decoded != nil {
		t.Fatal("decoded should be nil")
	}
}

func TestRegistryDecodeInvalidJSON(t *testing.T) {
	reg := NewRegistry()
	sample := concreteEvent{Message: ""}
	if err := reg.Register(sample); err != nil {
		t.Fatalf("Register failed: %v", err)
	}

	// Invalid JSON
	_, err := reg.Decode("concrete_event", json.RawMessage(`{invalid}`))
	if err == nil {
		t.Fatal("Decode invalid JSON should fail")
	}
}

func TestRegistryDecodePointerEvent(t *testing.T) {
	reg := NewRegistry()
	sample := &pointerEvent{Value: 0}
	if err := reg.Register(sample); err != nil {
		t.Fatalf("Register failed: %v", err)
	}

	rawJSON := json.RawMessage(`{"value":42}`)
	decoded, err := reg.Decode("pointer_event", rawJSON)
	if err != nil {
		t.Fatalf("Decode failed: %v", err)
	}

	if decoded == nil {
		t.Fatal("decoded is nil")
	}

	pe, ok := decoded.(*pointerEvent)
	if !ok {
		t.Fatalf("decoded is %T, want *pointerEvent", decoded)
	}

	if pe.Value != 42 {
		t.Fatalf("Value = %d, want 42", pe.Value)
	}
}
