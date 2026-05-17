package operationruntime

import (
	"github.com/fluxplane/agentruntime/core/operation"
	"github.com/fluxplane/agentruntime/core/policy"
)

// AccessDescriptor describes one protected resource/action an operation will
// touch for a concrete input.
type AccessDescriptor struct {
	Action   policy.Action      `json:"action"`
	Resource policy.ResourceRef `json:"resource"`
	Reason   string             `json:"reason,omitempty"`
}

// AccessProvider is implemented by operations that can describe authorization
// targets directly from their typed input.
type AccessProvider interface {
	Access(operation.Context, operation.Value) ([]AccessDescriptor, error)
}

// AccessFor returns typed access descriptors for op when it provides them.
func AccessFor(ctx operation.Context, op operation.Operation, input operation.Value) ([]AccessDescriptor, bool, error) {
	provider, ok := op.(AccessProvider)
	if !ok {
		return nil, false, nil
	}
	access, err := provider.Access(ctx, input)
	return access, true, err
}
