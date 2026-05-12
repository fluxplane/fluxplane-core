package agent

import (
	"context"
	"fmt"
	"strings"

	corecontext "github.com/fluxplane/agentruntime/core/context"
	"github.com/fluxplane/agentruntime/core/environment"
	"github.com/fluxplane/agentruntime/core/event"
	"github.com/fluxplane/agentruntime/core/operation"
	"github.com/fluxplane/agentruntime/core/skill"
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

// ToolRef identifies a driver-facing tool projection by name. Tool projection
// and execution are not core concerns.
type ToolRef struct {
	Name string `json:"name"`
}

// CommandRef identifies a command exposed to an agent by resource name or path.
type CommandRef struct {
	Name string `json:"name"`
}

// InferenceSpec contains inert model-call hints. Provider routing and model
// transport belong outside core.
type InferenceSpec struct {
	Model           string            `json:"model,omitempty"`
	MaxOutputTokens int               `json:"max_output_tokens,omitempty"`
	Temperature     float64           `json:"temperature,omitempty"`
	Thinking        string            `json:"thinking,omitempty"`
	ReasoningEffort string            `json:"reasoning_effort,omitempty"`
	Annotations     map[string]string `json:"annotations,omitempty"`
}

// StopConditionSpec describes when an agent runtime should stop. The shape is
// intentionally declarative so adapters can preserve richer legacy conditions.
type StopConditionSpec struct {
	Type        string              `json:"type,omitempty"`
	Max         int                 `json:"max,omitempty"`
	Prompt      string              `json:"prompt,omitempty"`
	Conditions  []StopConditionSpec `json:"conditions,omitempty"`
	Annotations map[string]string   `json:"annotations,omitempty"`
}

// Spec is an inert agent definition. It is intentionally not LLM-specific.
type Spec struct {
	Name        Name                      `json:"name"`
	Description string                    `json:"description,omitempty"`
	System      string                    `json:"system,omitempty"`
	Objective   Objective                 `json:"objective,omitempty"`
	Driver      DriverSpec                `json:"driver,omitempty"`
	Inference   InferenceSpec             `json:"inference,omitempty"`
	Stop        StopConditionSpec         `json:"stop,omitempty"`
	Agency      AgencyProfile             `json:"agency,omitempty"`
	Policy      Policy                    `json:"policy,omitempty"`
	Operations  []operation.Ref           `json:"operations,omitempty"`
	Tools       []ToolRef                 `json:"tools,omitempty"`
	Commands    []CommandRef              `json:"commands,omitempty"`
	Skills      []skill.Ref               `json:"skills,omitempty"`
	Context     []corecontext.ProviderRef `json:"context,omitempty"`
	Annotations map[string]string         `json:"annotations,omitempty"`
}

// Validate checks the agent spec is structurally useful without resolving refs.
func (s Spec) Validate() error {
	if strings.TrimSpace(string(s.Name)) == "" {
		return fmt.Errorf("agent: spec name is empty")
	}
	if s.Policy.MaxSteps < 0 {
		return fmt.Errorf("agent: max_steps must be >= 0")
	}
	for i, tool := range s.Tools {
		if strings.TrimSpace(tool.Name) == "" {
			return fmt.Errorf("agent: tools[%d] name is empty", i)
		}
	}
	for i, command := range s.Commands {
		if strings.TrimSpace(command.Name) == "" {
			return fmt.Errorf("agent: commands[%d] name is empty", i)
		}
	}
	for i, ref := range s.Skills {
		if strings.TrimSpace(string(ref.Name)) == "" {
			return fmt.Errorf("agent: skills[%d] name is empty", i)
		}
	}
	return nil
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
	Operation      operation.Ref   `json:"operation"`
	Input          operation.Value `json:"input,omitempty"`
	ProviderCallID string          `json:"provider_call_id,omitempty"`
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
	Kind       DecisionKind       `json:"kind"`
	Operations []OperationRequest `json:"operations,omitempty"`
	Message    *Message           `json:"message,omitempty"`
	Complete   *Completion        `json:"complete,omitempty"`
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
