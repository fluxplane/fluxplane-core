package operation

import "context"

// CallID identifies one concrete operation invocation. Repeated calls to the
// same operation ref within a run must use distinct call IDs.
type CallID string

type callIDContextKey struct{}

// WithCallID returns a child operation context carrying callID while preserving
// the operation event sink.
func WithCallID(ctx Context, callID CallID) Context {
	if ctx == nil || callID == "" {
		return ctx
	}
	return NewContext(context.WithValue(ctx, callIDContextKey{}, callID), ctx.Events())
}

// CallIDFromContext returns the operation call ID carried by ctx, if any.
func CallIDFromContext(ctx context.Context) CallID {
	if ctx == nil {
		return ""
	}
	callID, _ := ctx.Value(callIDContextKey{}).(CallID)
	return callID
}
