package operationruntime

import "github.com/fluxplane/engine/core/operation"

// Handler is the runtime call shape used by middleware.
type Handler func(operation.Context, operation.Value) operation.Result

// Middleware wraps operation execution. Middleware may observe or replace
// inputs, results, and emitted events through the operation context.
type Middleware func(Handler) Handler

func applyMiddleware(handler Handler, middlewares []Middleware) Handler {
	wrapped := handler
	for i := len(middlewares) - 1; i >= 0; i-- {
		if middlewares[i] != nil {
			wrapped = middlewares[i](wrapped)
		}
	}
	return wrapped
}
