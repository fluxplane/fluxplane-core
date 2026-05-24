package eventcodec

import (
	"strings"
	"testing"
	"time"

	"github.com/fluxplane/fluxplane-core/core/event"
)

func TestEncodeAttributesNil(t *testing.T) {
	out, err := EncodeAttributes(nil)
	if err != nil {
		t.Fatalf("EncodeAttributes(nil) error: %v", err)
	}
	if out != nil {
		t.Fatalf("EncodeAttributes(nil) = %v, want nil", out)
	}
}

func TestEncodeDecodeAttributesRoundtrip(t *testing.T) {
	attrs := map[string]string{"key": "value", "foo": "bar"}
	raw, err := EncodeAttributes(attrs)
	if err != nil {
		t.Fatalf("EncodeAttributes: %v", err)
	}
	decoded, err := DecodeAttributes(raw)
	if err != nil {
		t.Fatalf("DecodeAttributes: %v", err)
	}
	if decoded["key"] != "value" || decoded["foo"] != "bar" {
		t.Fatalf("roundtrip mismatch: %v", decoded)
	}
}

func TestDecodeAttributesEmpty(t *testing.T) {
	out, err := DecodeAttributes(nil)
	if err != nil {
		t.Fatalf("DecodeAttributes(nil) error: %v", err)
	}
	if out != nil {
		t.Fatalf("DecodeAttributes(nil) = %v, want nil", out)
	}
}

func TestDecodeAttributesInvalidJSON(t *testing.T) {
	_, err := DecodeAttributes([]byte("not-json"))
	if err == nil {
		t.Fatal("DecodeAttributes(invalid) should return error")
	}
}

func TestCloneStoredRecords(t *testing.T) {
	records := []event.StoredRecord{
		{
			Record: event.Record{
				Name:       "test.event",
				Attributes: map[string]string{"a": "1"},
			},
			Sequence: 1,
		},
	}
	cloned := CloneStoredRecords(records)
	if len(cloned) != 1 {
		t.Fatalf("CloneStoredRecords len = %d, want 1", len(cloned))
	}
	// Mutating original should not affect clone.
	records[0].Record.Attributes["a"] = "changed"
	if cloned[0].Record.Attributes["a"] != "1" {
		t.Fatal("CloneStoredRecords did not deep-clone attributes")
	}
}

func TestNewIDFormat(t *testing.T) {
	id := NewID()
	if !strings.HasPrefix(id, "evt_") {
		t.Fatalf("NewID = %q, want evt_ prefix", id)
	}
	if len(id) < 10 {
		t.Fatalf("NewID = %q, unexpectedly short", id)
	}
}

func TestNormalizeRecordMismatchedName(t *testing.T) {
	// NormalizeRecord with a payload whose EventName differs from Record.Name should error.
	rec := event.Record{
		Name:    "wrong.name",
		Payload: testEvent{Text: "x"}, // testEvent.EventName() returns "test.event"
	}
	_, err := NormalizeRecord(rec, time.Now())
	if err == nil {
		t.Fatal("NormalizeRecord with mismatched name should return error")
	}
}

func TestNormalizeRecordEmptyPayloadEmptyName(t *testing.T) {
	rec := event.Record{Name: "", Payload: nil}
	_, err := NormalizeRecord(rec, time.Now())
	if err == nil {
		t.Fatal("NormalizeRecord with no name should return error")
	}
}
