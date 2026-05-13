package planexecplugin

import "time"

type PlanPhase string

const (
	PhaseDrafting    PlanPhase = "drafting"
	PhaseExecuting   PlanPhase = "executing"
	PhaseCompleted   PlanPhase = "completed"
	PhaseFailed      PlanPhase = "failed"
	PhaseCancelled   PlanPhase = "cancelled"
	PhaseInterrupted PlanPhase = "interrupted"
)

type StepStatus string

const (
	StepStatusWaiting   StepStatus = "waiting"
	StepStatusRunning   StepStatus = "running"
	StepStatusCompleted StepStatus = "completed"
	StepStatusFailed    StepStatus = "failed"
	StepStatusCancelled StepStatus = "cancelled"
)

type PlanSpec struct {
	Title       string     `json:"title,omitempty"`
	Description string     `json:"description,omitempty"`
	Steps       []StepSpec `json:"steps,omitempty"`
}

type StepSpec struct {
	ID          string   `json:"id" jsonschema:"required"`
	Title       string   `json:"title" jsonschema:"required"`
	Description string   `json:"description,omitempty"`
	DependsOn   []string `json:"depends_on,omitempty"`
	Profile     string   `json:"profile,omitempty"`
	Acceptance  string   `json:"acceptance,omitempty"`
	Scope       []string `json:"scope,omitempty"`
}

type PlanState struct {
	ID        string              `json:"id,omitempty"`
	Phase     PlanPhase           `json:"phase,omitempty"`
	Spec      PlanSpec            `json:"spec,omitempty"`
	Steps     map[string]StepExec `json:"steps,omitempty"`
	CreatedAt time.Time           `json:"created_at,omitempty"`
	Error     string              `json:"error,omitempty"`
}

type StepExec struct {
	Status    StepStatus `json:"status,omitempty"`
	WorkerID  string     `json:"worker_id,omitempty"`
	Profile   string     `json:"profile,omitempty"`
	StartedAt time.Time  `json:"started_at,omitempty"`
	DoneAt    time.Time  `json:"done_at,omitempty"`
	Output    string     `json:"output,omitempty"`
	Error     string     `json:"error,omitempty"`
}

func cloneState(in PlanState) PlanState {
	out := in
	out.Spec = cloneSpec(in.Spec)
	if len(in.Steps) > 0 {
		out.Steps = make(map[string]StepExec, len(in.Steps))
		for k, v := range in.Steps {
			out.Steps[k] = v
		}
	}
	return out
}

func cloneSpec(in PlanSpec) PlanSpec {
	out := in
	if len(in.Steps) > 0 {
		out.Steps = make([]StepSpec, len(in.Steps))
		for i, step := range in.Steps {
			out.Steps[i] = step
			out.Steps[i].DependsOn = append([]string(nil), step.DependsOn...)
			out.Steps[i].Scope = append([]string(nil), step.Scope...)
		}
	}
	return out
}
