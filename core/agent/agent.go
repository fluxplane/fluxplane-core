package agent

import (
	"context"

	corecontext "github.com/fluxplane/agentruntime/core/context"
	"github.com/fluxplane/agentruntime/core/environment"
	"github.com/fluxplane/agentruntime/core/event"
	"github.com/fluxplane/agentruntime/core/operation"
)

// Name identifies an agent spec.
type Name string

// Ref identifies an agent by name.
type Ref struct {
	Name Name `json:"name"`
}

// Objective describes what the agent is trying to accomplish.
type Objective struct {
	Role         string `json:"role,omitempty"`
	Instructions string `json:"instructions,omitempty"`
	Success      string `json:"success,omitempty"`
}

// DriverKind identifies the kind of runtime implementation that can run an
// agent spec. Examples: "llmagent", "workflow", "rule", "remote", "human".
type DriverKind string

// DriverSpec describes the runtime driver without embedding driver-specific
// semantics in the generic agent model.
type DriverSpec struct {
	Kind        DriverKind        `json:"kind"`
	Config      map[string]any    `json:"config,omitempty"`
	Annotations map[string]string `json:"annotations,omitempty"`
}

// AutonomyLevel describes how much initiative an agent may take.
type AutonomyLevel string

const (
	AutonomyNone       AutonomyLevel = "none"
	AutonomyReactive   AutonomyLevel = "reactive"
	AutonomyGoalDriven AutonomyLevel = "goal_driven"
	AutonomyAutonomous AutonomyLevel = "autonomous"
)

// AgencyProfile describes agentic properties for policy, routing, and
// documentation. These are declarations, not enforcement by themselves.
type AgencyProfile struct {
	Autonomy  AutonomyLevel `json:"autonomy,omitempty"`
	Reactive  bool          `json:"reactive,omitempty"`
	Proactive bool          `json:"proactive,omitempty"`
	Social    bool          `json:"social,omitempty"`
	Stateful  bool          `json:"stateful,omitempty"`
	Learning  bool          `json:"learning,omitempty"`
}

// Policy describes generic runtime boundaries for an agent.
type Policy struct {
	MaxSteps         int            `json:"max_steps,omitempty"`
	MaxContinuations int            `json:"max_continuations,omitempty"`
	AllowedDecisions []DecisionKind `json:"allowed_decisions,omitempty"`
}

// Spec is an inert agent definition. It is intentionally not LLM-specific.
type Spec struct {
	Name        Name                      `json:"name"`
	Description string                    `json:"description,omitempty"`
	Objective   Objective                 `json:"objective,omitempty"`
	Driver      DriverSpec                `json:"driver,omitempty"`
	Agency      AgencyProfile             `json:"agency,omitempty"`
	Policy      Policy                    `json:"policy,omitempty"`
	Operations  []operation.Ref           `json:"operations,omitempty"`
	Context     []corecontext.ProviderRef `json:"context,omitempty"`
	Annotations map[string]string         `json:"annotations,omitempty"`
}

// Context is the execution context passed to an agent step.
type Context interface {
	context.Context
	Events() event.Sink
}

// Agent is a runnable actor that advances one observe-decide-act step.
type Agent interface {
	Spec() Spec
	Step(Context, StepInput) StepResult
}

// StateRef references durable or external agent state.
type StateRef struct {
	Kind   string `json:"kind,omitempty"`
	URI    string `json:"uri,omitempty"`
	Digest string `json:"digest,omitempty"`
}

// StateUpdate describes how an agent step changed or replaced state.
type StateUpdate struct {
	Ref     StateRef `json:"ref,omitempty"`
	Summary string   `json:"summary,omitempty"`
}

// StepInput is the input to one agent decision step.
type StepInput struct {
	Goal         string                    `json:"goal,omitempty"`
	Objective    Objective                 `json:"objective,omitempty"`
	Observations []environment.Observation `json:"observations,omitempty"`
	Context      []corecontext.Block       `json:"context,omitempty"`
	State        StateRef                  `json:"state,omitempty"`
}

// DecisionKind classifies what an agent chose to do.
type DecisionKind string

const (
	DecisionNone      DecisionKind = ""
	DecisionOperation DecisionKind = "operation"
	DecisionMessage   DecisionKind = "message"
	DecisionComplete  DecisionKind = "complete"
	DecisionWait      DecisionKind = "wait"
	DecisionDelegate  DecisionKind = "delegate"
	DecisionReject    DecisionKind = "reject"
)

// OperationRequest asks orchestration/runtime to execute an operation.
type OperationRequest struct {
	Operation operation.Ref   `json:"operation"`
	Input     operation.Value `json:"input,omitempty"`
}

// Message is a communication emitted by an agent.
type Message struct {
	To       string         `json:"to,omitempty"`
	Content  any            `json:"content,omitempty"`
	Metadata map[string]any `json:"metadata,omitempty"`
}

// Completion describes a completed objective or step.
type Completion struct {
	Output any    `json:"output,omitempty"`
	Reason string `json:"reason,omitempty"`
}

// Decision is the chosen next move from one agent step.
type Decision struct {
	Kind      DecisionKind      `json:"kind"`
	Operation *OperationRequest `json:"operation,omitempty"`
	Message   *Message          `json:"message,omitempty"`
	Complete  *Completion       `json:"complete,omitempty"`
}

// Status classifies the outcome of an agent step.
type Status string

const (
	StatusOK       Status = "ok"
	StatusFailed   Status = "failed"
	StatusRejected Status = "rejected"
	StatusCanceled Status = "canceled"
)

// Error describes a structured agent failure.
type Error struct {
	Code    string         `json:"code,omitempty"`
	Message string         `json:"message"`
	Details map[string]any `json:"details,omitempty"`
}

// StepResult is the outcome of one observe-decide-act step.
type StepResult struct {
	Status   Status      `json:"status"`
	Decision Decision    `json:"decision,omitempty"`
	State    StateUpdate `json:"state,omitempty"`
	Error    *Error      `json:"error,omitempty"`
}
