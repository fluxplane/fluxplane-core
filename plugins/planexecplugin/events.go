package planexecplugin

import (
	"github.com/fluxplane/agentruntime/core/event"
	"github.com/fluxplane/agentruntime/orchestration/subagent"
)

const (
	EventPlanCreated          event.Name = "plan.created"
	EventPlanRevised          event.Name = "plan.revised"
	EventPlanExecutionStarted event.Name = "plan.execution_started"
	EventPlanCompleted        event.Name = "plan.completed"
	EventPlanFailed           event.Name = "plan.failed"
	EventPlanCancelled        event.Name = "plan.cancelled"
	EventStepDispatched       event.Name = "plan.step.dispatched"
	EventStepProgressed       event.Name = "plan.step.progressed"
	EventStepCompleted        event.Name = "plan.step.completed"
	EventStepFailed           event.Name = "plan.step.failed"
	EventStepCancelled        event.Name = "plan.step.cancelled"
)

type PlanCreated struct {
	PlanID string   `json:"plan_id"`
	Spec   PlanSpec `json:"spec"`
}

func (PlanCreated) EventName() event.Name { return EventPlanCreated }

type PlanRevised struct {
	PlanID string   `json:"plan_id"`
	Spec   PlanSpec `json:"spec"`
	Reason string   `json:"reason,omitempty"`
}

func (PlanRevised) EventName() event.Name { return EventPlanRevised }

type PlanExecutionStarted struct {
	PlanID string `json:"plan_id"`
}

func (PlanExecutionStarted) EventName() event.Name { return EventPlanExecutionStarted }

type StepDispatched struct {
	PlanID   string             `json:"plan_id"`
	StepID   string             `json:"step_id"`
	Title    string             `json:"title,omitempty"`
	WorkerID subagent.ID        `json:"worker_id,omitempty"`
	Profile  string             `json:"profile,omitempty"`
	Cause    subagent.Causation `json:"cause,omitempty"`
}

func (StepDispatched) EventName() event.Name { return EventStepDispatched }

type StepProgressed struct {
	PlanID  string `json:"plan_id"`
	StepID  string `json:"step_id"`
	Message string `json:"message,omitempty"`
}

func (StepProgressed) EventName() event.Name { return EventStepProgressed }

type StepCompleted struct {
	PlanID string `json:"plan_id"`
	StepID string `json:"step_id"`
	Output string `json:"output,omitempty"`
}

func (StepCompleted) EventName() event.Name { return EventStepCompleted }

type StepFailed struct {
	PlanID string `json:"plan_id"`
	StepID string `json:"step_id"`
	Error  string `json:"error,omitempty"`
}

func (StepFailed) EventName() event.Name { return EventStepFailed }

type StepCancelled struct {
	PlanID string `json:"plan_id"`
	StepID string `json:"step_id"`
	Reason string `json:"reason,omitempty"`
}

func (StepCancelled) EventName() event.Name { return EventStepCancelled }

type PlanCompleted struct {
	PlanID  string `json:"plan_id"`
	Summary string `json:"summary,omitempty"`
}

func (PlanCompleted) EventName() event.Name { return EventPlanCompleted }

type PlanFailed struct {
	PlanID string `json:"plan_id"`
	Reason string `json:"reason,omitempty"`
}

func (PlanFailed) EventName() event.Name { return EventPlanFailed }

type PlanCancelled struct {
	PlanID string `json:"plan_id"`
	Reason string `json:"reason,omitempty"`
}

func (PlanCancelled) EventName() event.Name { return EventPlanCancelled }
