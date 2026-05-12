package llmagent

import (
	"github.com/fluxplane/agentruntime/core/agent"
	"github.com/fluxplane/agentruntime/core/event"
)

const (
	// EventModelRequestedName is emitted before the model port is called.
	EventModelRequestedName event.Name = "llmagent.model_requested"
	// EventModelStreamedName is emitted for opt-in transient model stream
	// deltas.
	EventModelStreamedName event.Name = "llmagent.model_streamed"
	// EventModelCompletedName is emitted after a successful model call.
	EventModelCompletedName event.Name = "llmagent.model_completed"
	// EventModelFailedName is emitted after a failed model call.
	EventModelFailedName event.Name = "llmagent.model_failed"
)

// ModelRequested describes a model call without carrying prompt content.
type ModelRequested struct {
	Agent agent.Name `json:"agent,omitempty"`
	Model string     `json:"model,omitempty"`
}

// EventName returns the typed event name.
func (ModelRequested) EventName() event.Name { return EventModelRequestedName }

// ModelStreamed carries one opt-in transient model stream delta.
type ModelStreamed struct {
	Agent agent.Name  `json:"agent,omitempty"`
	Model string      `json:"model,omitempty"`
	Event StreamEvent `json:"event"`
}

// EventName returns the typed event name.
func (ModelStreamed) EventName() event.Name { return EventModelStreamedName }

// ModelCompleted describes a successful model call without carrying response
// content.
type ModelCompleted struct {
	Agent    agent.Name         `json:"agent,omitempty"`
	Model    string             `json:"model,omitempty"`
	Decision agent.DecisionKind `json:"decision,omitempty"`
}

// EventName returns the typed event name.
func (ModelCompleted) EventName() event.Name { return EventModelCompletedName }

// ModelFailed describes a failed model call without carrying prompt content.
type ModelFailed struct {
	Agent agent.Name `json:"agent,omitempty"`
	Model string     `json:"model,omitempty"`
	Error string     `json:"error,omitempty"`
}

// EventName returns the typed event name.
func (ModelFailed) EventName() event.Name { return EventModelFailedName }

func emit(ctx agent.Context, payload event.Event) {
	if ctx == nil || ctx.Events() == nil {
		return
	}
	ctx.Events().Emit(payload)
}
