package reaction

import "github.com/fluxplane/agentruntime/core/event"

const (
	// EventActionPlanned records that one reaction action was selected for
	// possible application.
	EventActionPlanned event.Name = "reaction.action_planned"
	// EventActionApplied records that one reaction action was successfully
	// applied for an idempotency key.
	EventActionApplied event.Name = "reaction.action_applied"
	// EventActionSkipped records that one matching reaction action did not run.
	EventActionSkipped event.Name = "reaction.action_skipped"
	// EventDiagnostic records a reaction planning or application diagnostic.
	EventDiagnostic event.Name = "reaction.diagnostic"
)

// ActionPlanned records one planned reaction action.
type ActionPlanned struct {
	Rule           string     `json:"rule,omitempty"`
	Action         ActionKind `json:"action,omitempty"`
	IdempotencyKey string     `json:"idempotency_key,omitempty"`
}

func (ActionPlanned) EventName() event.Name { return EventActionPlanned }

// ActionApplied records one successful reaction action application.
type ActionApplied struct {
	Rule           string     `json:"rule,omitempty"`
	Action         ActionKind `json:"action,omitempty"`
	IdempotencyKey string     `json:"idempotency_key,omitempty"`
	Target         string     `json:"target,omitempty"`
}

func (ActionApplied) EventName() event.Name { return EventActionApplied }

// ActionSkipped records one skipped reaction action.
type ActionSkipped struct {
	Rule           string     `json:"rule,omitempty"`
	Action         ActionKind `json:"action,omitempty"`
	IdempotencyKey string     `json:"idempotency_key,omitempty"`
	Reason         string     `json:"reason,omitempty"`
}

func (ActionSkipped) EventName() event.Name { return EventActionSkipped }

// Diagnostic records one reaction planning or application diagnostic.
type Diagnostic struct {
	Rule    string     `json:"rule,omitempty"`
	Action  ActionKind `json:"action,omitempty"`
	Message string     `json:"message,omitempty"`
}

func (Diagnostic) EventName() event.Name { return EventDiagnostic }
