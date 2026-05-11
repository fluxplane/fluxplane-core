package invocation

import (
	"github.com/fluxplane/agentruntime/core/agent"
	"github.com/fluxplane/agentruntime/core/operation"
	"github.com/fluxplane/agentruntime/core/workflow"
)

// TargetKind classifies what an invocation points at.
type TargetKind string

const (
	TargetOperation TargetKind = "operation"
	TargetWorkflow  TargetKind = "workflow"
	TargetAgent     TargetKind = "agent"
	TargetSession   TargetKind = "session"
	TargetMessage   TargetKind = "message"
)

// Target is an inert reference to something orchestration can dispatch.
type Target struct {
	Kind      TargetKind    `json:"kind"`
	Operation operation.Ref `json:"operation,omitempty"`
	Workflow  workflow.Name `json:"workflow,omitempty"`
	Agent     agent.Ref     `json:"agent,omitempty"`
	Session   string        `json:"session,omitempty"`
	Message   string        `json:"message,omitempty"`
}
