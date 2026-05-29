package operation

import (
	"context"

	"github.com/fluxplane/fluxplane-event"
)

// Context is the execution context passed to an operation.
//
// It embeds context.Context for cancellation/deadline propagation and exposes
// an event sink for typed domain events. Core does not define where events go.
type Context interface {
	context.Context
	Events() event.Sink
}

// Operation is the minimal executable contract in core.
//
// Runtime layers may adapt this into richer execution contexts, middleware
// chains, validators, event sinks, retries, timeouts, and policy gates. Core
// keeps the contract request/response-only.
type Operation interface {
	Spec() Spec
	Run(Context, Value) Result
}

// Handler is the function form of an operation implementation.
type Handler func(Context, Value) Result

// HandlerOperation adapts a Handler into an Operation.
type HandlerOperation struct {
	spec    Spec
	handler Handler
}

// New returns an Operation backed by handler.
func New(spec Spec, handler Handler) Operation {
	return HandlerOperation{spec: spec, handler: handler}
}

// Spec returns the operation specification.
func (o HandlerOperation) Spec() Spec {
	return o.spec
}

// Run executes the operation handler.
func (o HandlerOperation) Run(ctx Context, input Value) Result {
	if o.handler == nil {
		return OK(nil)
	}
	return o.handler(ctx, input)
}

type contextAdapter struct {
	context.Context
	events event.Sink
}

// NewContext adapts a context.Context and event sink into an operation Context.
func NewContext(ctx context.Context, events event.Sink) Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if events == nil {
		events = event.Discard()
	}
	return contextAdapter{Context: ctx, events: events}
}

// Events returns the configured event sink.
func (c contextAdapter) Events() event.Sink {
	return c.events
}
