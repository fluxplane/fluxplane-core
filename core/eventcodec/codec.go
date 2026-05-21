package eventcodec

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"github.com/fluxplane/engine/core/event"
	"github.com/fluxplane/engine/core/policy"
)

// NormalizeRecord applies runtime defaults and validates record/payload
// consistency.
func NormalizeRecord(record event.Record, now time.Time) (event.Record, error) {
	if record.Payload != nil {
		payloadName := record.Payload.EventName()
		if payloadName == "" {
			return event.Record{}, fmt.Errorf("eventcodec: payload %T has empty event name", record.Payload)
		}
		if record.Name == "" {
			record.Name = payloadName
		}
		if record.Name != payloadName {
			return event.Record{}, fmt.Errorf("eventcodec: record name %q does not match payload name %q", record.Name, payloadName)
		}
	}
	if record.Name == "" {
		return event.Record{}, fmt.Errorf("eventcodec: record name is empty")
	}
	if record.ID == "" {
		record.ID = NewID()
	}
	if record.Time.IsZero() {
		record.Time = now
	}
	if record.SchemaVersion == 0 {
		record.SchemaVersion = 1
	}
	record.Sensitivity = policy.NormalizeSensitivity(record.Sensitivity)
	record.Attributes = CloneStringMap(record.Attributes)
	return record, nil
}

// EncodePayload serializes a typed event payload as JSON.
func EncodePayload(payload event.Event) ([]byte, error) {
	if payload == nil {
		return nil, nil
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("eventcodec: encode payload: %w", err)
	}
	return raw, nil
}

// DecodePayload decodes a JSON event payload through registry.
func DecodePayload(registry *event.Registry, name event.Name, raw []byte) (event.Event, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	if registry == nil {
		return nil, fmt.Errorf("eventcodec: event registry is nil for payload %q", name)
	}
	decoded, err := registry.Decode(name, raw)
	if err != nil {
		return nil, fmt.Errorf("eventcodec: decode payload %q: %w", name, err)
	}
	return decoded, nil
}

// EncodeAttributes serializes record attributes as JSON.
func EncodeAttributes(attributes map[string]string) ([]byte, error) {
	if attributes == nil {
		return nil, nil
	}
	raw, err := json.Marshal(attributes)
	if err != nil {
		return nil, fmt.Errorf("eventcodec: encode attributes: %w", err)
	}
	return raw, nil
}

// DecodeAttributes decodes JSON record attributes.
func DecodeAttributes(raw []byte) (map[string]string, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var attributes map[string]string
	if err := json.Unmarshal(raw, &attributes); err != nil {
		return nil, fmt.Errorf("eventcodec: decode attributes: %w", err)
	}
	return attributes, nil
}

// CloneStoredRecords shallow-copies stored records and clones mutable record
// maps.
func CloneStoredRecords(records []event.StoredRecord) []event.StoredRecord {
	out := make([]event.StoredRecord, len(records))
	for i, record := range records {
		out[i] = record
		out[i].Record.Attributes = CloneStringMap(record.Record.Attributes)
	}
	return out
}

// CloneStringMap returns a shallow clone of in.
func CloneStringMap(in map[string]string) map[string]string {
	if in == nil {
		return nil
	}
	out := make(map[string]string, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

// NewID returns a new event record ID.
func NewID() string {
	var bytes [16]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return fmt.Sprintf("evt_%d", time.Now().UnixNano())
	}
	return "evt_" + hex.EncodeToString(bytes[:])
}
