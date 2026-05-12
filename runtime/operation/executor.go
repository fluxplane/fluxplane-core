package operationruntime

import (
	"context"

	"github.com/fluxplane/agentruntime/core/event"
	"github.com/fluxplane/agentruntime/core/operation"
)

// Executor executes operations with runtime validation, middleware, and
// lifecycle event emission.
type Executor struct {
	Validator  Validator
	Middleware []Middleware
	EventSink  event.Sink
	Safety     SafetyGate
}

// Option configures an Executor.
type Option func(*Executor)

// WithValidator installs a validator.
func WithValidator(validator Validator) Option {
	return func(e *Executor) { e.Validator = validator }
}

// WithMiddleware appends middleware to the executor.
func WithMiddleware(middleware ...Middleware) Option {
	return func(e *Executor) { e.Middleware = append(e.Middleware, middleware...) }
}

// WithEventSink sets the fallback event sink used when the provided operation
// context is nil.
func WithEventSink(sink event.Sink) Option {
	return func(e *Executor) { e.EventSink = sink }
}

// WithSafetyGate installs a pre-execution safety gate.
func WithSafetyGate(gate SafetyGate) Option {
	return func(e *Executor) { e.Safety = gate }
}

// NewExecutor returns an operation executor.
func NewExecutor(opts ...Option) Executor {
	executor := Executor{}
	for _, opt := range opts {
		if opt != nil {
			opt(&executor)
		}
	}
	return executor
}

// Execute runs op with input.
func (e Executor) Execute(ctx operation.Context, op operation.Operation, input operation.Value) operation.Result {
	if op == nil {
		return operation.Failed("operation_missing", "operation is nil", nil)
	}
	ctx = e.ensureContext(ctx)
	spec := op.Spec()
	callID := operation.CallIDFromContext(ctx)
	ctx.Events().Emit(operation.OperationStarted{CallID: callID, Operation: spec.Ref})

	base := func(ctx operation.Context, input operation.Value) operation.Result {
		if e.Safety != nil {
			if err := e.Safety.Check(ctx, spec, input); err != nil {
				return operation.Rejected("operation_safety_denied", err.Error(), nil)
			}
		}
		if e.Validator != nil && !spec.Input.IsZero() {
			if err := e.Validator.Validate(spec.Input, input); err != nil {
				return validationFailed("input", err)
			}
		}
		result := normalize(op.Run(ctx, input))
		if e.Validator != nil && result.Status == operation.StatusOK && !spec.Output.IsZero() {
			if err := e.Validator.Validate(spec.Output, result.Output); err != nil {
				return validationFailed("output", err)
			}
		}
		return result
	}

	result := normalize(applyMiddleware(base, e.Middleware)(ctx, input))
	e.emitTerminalEvent(ctx, callID, spec.Ref, result)
	return result
}

func (e Executor) ensureContext(ctx operation.Context) operation.Context {
	if ctx != nil {
		return ctx
	}
	return operation.NewContext(context.Background(), e.EventSink)
}

func (Executor) emitTerminalEvent(ctx operation.Context, callID operation.CallID, ref operation.Ref, result operation.Result) {
	switch result.Status {
	case operation.StatusOK:
		ctx.Events().Emit(operation.OperationCompleted{CallID: callID, Operation: ref})
	case operation.StatusRejected:
		ctx.Events().Emit(operation.OperationRejected{CallID: callID, Operation: ref, Error: result.Error})
	case operation.StatusCanceled:
		ctx.Events().Emit(operation.OperationCanceled{CallID: callID, Operation: ref, Error: result.Error})
	default:
		ctx.Events().Emit(operation.OperationFailed{CallID: callID, Operation: ref, Error: result.Error})
	}
}

func normalize(result operation.Result) operation.Result {
	if result.Status == "" {
		result.Status = operation.StatusOK
	}
	return result
}
