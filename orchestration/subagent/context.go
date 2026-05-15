package subagent

import (
	"context"

	"github.com/fluxplane/agentruntime/core/event"
	"github.com/fluxplane/agentruntime/core/operation"
	coresession "github.com/fluxplane/agentruntime/core/session"
	corethread "github.com/fluxplane/agentruntime/core/thread"
	operationruntime "github.com/fluxplane/agentruntime/runtime/operation"
)

type contextKey struct{}

// Scope is the parent execution scope available to delegate/plan operations.
type Scope struct {
	Supervisor     *Supervisor
	ParentThreadID corethread.ID
	ParentRunID    string
	ParentCallID   operation.CallID
	Policy         coresession.DelegationPolicy
	Events         event.Sink
	ThreadStore    corethread.Store
	// Approver is the parent session's approval gate. It is propagated into
	// child sessions so that runtime approval policy (e.g. --yolo /
	// AutoApprover) is inherited rather than silently reset.
	Approver operationruntime.ApprovalGate
}

// ContextWithScope attaches sub-agent supervisor scope to ctx.
func ContextWithScope(ctx context.Context, scope Scope) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, contextKey{}, scope)
}

// ScopeFromContext returns sub-agent supervisor scope from ctx.
func ScopeFromContext(ctx context.Context) (Scope, bool) {
	if ctx == nil {
		return Scope{}, false
	}
	scope, ok := ctx.Value(contextKey{}).(Scope)
	return scope, ok && scope.Supervisor != nil
}
