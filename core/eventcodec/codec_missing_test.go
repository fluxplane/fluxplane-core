package eventcodec

import (
	"testing"

	"github.com/fluxplane/engine/core/event"
)

func TestEncodePayloadNil(t *testing.T) {
	raw, err := EncodePayload(nil)
	if err != nil {
		t.Fatalf("EncodePayload(nil): %v", err)
	}
	if raw != nil {
		t.Fatalf("EncodePayload(nil) = %v, want nil", raw)
	}
}

func TestDecodePayloadEmpty(t *testing.T) {
	got, err := DecodePayload(event.NewRegistry(), "test.event", nil)
	if err != nil {
		t.Fatalf("DecodePayload(nil payload): %v", err)
	}
	if got != nil {
		t.Fatalf("DecodePayload(nil) = %v, want nil", got)
	}
}

func TestDecodePayloadNilRegistry(t *testing.T) {
	_, err := DecodePayload(nil, "test.event", []byte(`{}`))
	if err == nil {
		t.Fatal("DecodePayload(nil registry): want error")
	}
}

func TestCloneStringMapNil(t *testing.T) {
	out := CloneStringMap(nil)
	if out != nil {
		t.Fatalf("CloneStringMap(nil) = %v, want nil", out)
	}
}

func TestCloneStringMapEmpty(t *testing.T) {
	out := CloneStringMap(map[string]string{})
	if out == nil {
		t.Fatal("CloneStringMap({}) = nil, want empty map")
	}
	if len(out) != 0 {
		t.Fatalf("CloneStringMap({}) len = %d, want 0", len(out))
	}
}

func TestEncodeAttributesNonNil(t *testing.T) {
	raw, err := EncodeAttributes(map[string]string{"key": "val"})
	if err != nil || len(raw) == 0 {
		t.Fatalf("EncodeAttributes: err=%v raw=%v", err, raw)
	}
}
