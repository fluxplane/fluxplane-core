package eventcodec

import (
	"testing"
	"time"

	"github.com/fluxplane/fluxplane-core/core/event"
	"github.com/fluxplane/fluxplane-core/core/policy"
)

type testEvent struct {
	Text string `json:"text,omitempty"`
}

func (testEvent) EventName() event.Name { return "test.event" }

func TestNormalizeRecordDefaults(t *testing.T) {
	now := time.Date(2026, 5, 12, 1, 2, 3, 0, time.UTC)
	record, err := NormalizeRecord(event.Record{
		Payload:    testEvent{Text: "hello"},
		Attributes: map[string]string{"k": "v"},
	}, now)
	if err != nil {
		t.Fatalf("NormalizeRecord returned error: %v", err)
	}
	if record.ID == "" {
		t.Fatal("ID is empty")
	}
	if record.Name != "test.event" {
		t.Fatalf("Name = %q, want test.event", record.Name)
	}
	if !record.Time.Equal(now) {
		t.Fatalf("Time = %v, want %v", record.Time, now)
	}
	if record.SchemaVersion != 1 {
		t.Fatalf("SchemaVersion = %d, want 1", record.SchemaVersion)
	}
	if record.Sensitivity != policy.SensitivityRestricted {
		t.Fatalf("Sensitivity = %q, want restricted", record.Sensitivity)
	}
}

func TestEncodeDecodePayload(t *testing.T) {
	registry := event.NewRegistry()
	if err := registry.Register(testEvent{}); err != nil {
		t.Fatalf("Register returned error: %v", err)
	}
	raw, err := EncodePayload(testEvent{Text: "hello"})
	if err != nil {
		t.Fatalf("EncodePayload returned error: %v", err)
	}
	decoded, err := DecodePayload(registry, "test.event", raw)
	if err != nil {
		t.Fatalf("DecodePayload returned error: %v", err)
	}
	payload, ok := decoded.(testEvent)
	if !ok {
		t.Fatalf("decoded type = %T, want testEvent", decoded)
	}
	if payload.Text != "hello" {
		t.Fatalf("Text = %q, want hello", payload.Text)
	}
}

func TestDecodePayloadUnknownEvent(t *testing.T) {
	registry := event.NewRegistry()
	_, err := DecodePayload(registry, "unknown.event", []byte(`{}`))
	if err == nil {
		t.Fatal("DecodePayload succeeded for unknown event, want error")
	}
}

func TestNormalizeRecordWithCustomValues(t *testing.T) {
	now := time.Date(2026, 5, 12, 1, 2, 3, 0, time.UTC)
	customID := "custom-id"
	record, err := NormalizeRecord(event.Record{
		ID:          customID,
		Payload:     testEvent{Text: "hello"},
		Sensitivity: policy.SensitivityPublic,
		Attributes:  map[string]string{"k": "v"},
	}, now)
	if err != nil {
		t.Fatalf("NormalizeRecord returned error: %v", err)
	}
	if record.ID != customID {
		t.Fatalf("ID = %q, want %q", record.ID, customID)
	}
	if record.Sensitivity != policy.SensitivityPublic {
		t.Fatalf("Sensitivity = %q, want public", record.Sensitivity)
	}
}
