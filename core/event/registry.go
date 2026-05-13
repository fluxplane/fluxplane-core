package event

import (
	"encoding/json"
	"fmt"
	"reflect"
)

// Decoder decodes a serialized event payload.
type Decoder func(json.RawMessage) (Event, error)

// Registry maps event names to typed decoders.
type Registry struct {
	decoders map[Name]Decoder
}

// NewRegistry returns an empty event registry.
func NewRegistry() *Registry {
	return &Registry{decoders: map[Name]Decoder{}}
}

// Register adds the payload type represented by sample to the registry.
func (r *Registry) Register(sample Event) error {
	if sample == nil {
		return fmt.Errorf("event: sample is nil")
	}
	name := sample.EventName()
	if name == "" {
		return fmt.Errorf("event: sample %T has empty name", sample)
	}
	if r.decoders == nil {
		r.decoders = map[Name]Decoder{}
	}
	if _, exists := r.decoders[name]; exists {
		return fmt.Errorf("event: duplicate event name %q", name)
	}
	typ := reflect.TypeOf(sample)
	r.decoders[name] = func(raw json.RawMessage) (Event, error) {
		value := reflect.New(indirectType(typ)).Interface()
		if err := json.Unmarshal(raw, value); err != nil {
			return nil, err
		}
		if typ.Kind() == reflect.Pointer {
			event, ok := value.(Event)
			if !ok {
				return nil, fmt.Errorf("event: decoded %T does not implement Event", value)
			}
			return event, nil
		}
		event, ok := reflect.ValueOf(value).Elem().Interface().(Event)
		if !ok {
			return nil, fmt.Errorf("event: decoded %T does not implement Event", value)
		}
		return event, nil
	}
	return nil
}

// Decode decodes raw as the event payload registered under name.
func (r *Registry) Decode(name Name, raw json.RawMessage) (Event, error) {
	if r == nil || r.decoders == nil {
		return nil, fmt.Errorf("event: registry is empty")
	}
	decoder, ok := r.decoders[name]
	if !ok {
		return nil, fmt.Errorf("event: event name %q is not registered", name)
	}
	return decoder(raw)
}

// TryDecode decodes raw when name is registered.
func (r *Registry) TryDecode(name Name, raw json.RawMessage) (Event, bool, error) {
	if r == nil || r.decoders == nil {
		return nil, false, nil
	}
	decoder, ok := r.decoders[name]
	if !ok {
		return nil, false, nil
	}
	decoded, err := decoder(raw)
	return decoded, true, err
}

func indirectType(typ reflect.Type) reflect.Type {
	for typ.Kind() == reflect.Pointer {
		typ = typ.Elem()
	}
	return typ
}
