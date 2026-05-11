package event

// Name identifies one event payload type.
type Name string

// Event is a typed domain event payload.
//
// Implementations should be plain serializable structs. They should not carry
// delivery metadata such as record IDs, timestamps, stream IDs, or correlation
// IDs; that metadata belongs to Record.
type Event interface {
	EventName() Name
}

// Sink receives typed event payloads during execution.
//
// Core does not define where emitted events go. Runtime and orchestration
// layers decide whether to stream, persist, redact, transform, or drop them.
type Sink interface {
	Emit(Event)
}

// SinkFunc adapts a function into a Sink.
type SinkFunc func(Event)

// Emit sends event to f when f is non-nil.
func (f SinkFunc) Emit(event Event) {
	if f != nil {
		f(event)
	}
}

type discardSink struct{}

func (discardSink) Emit(Event) {}

// Discard returns a sink that drops every event.
func Discard() Sink {
	return discardSink{}
}
