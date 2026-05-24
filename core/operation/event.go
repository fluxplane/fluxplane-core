package operation

import "github.com/fluxplane/fluxplane-core/core/event"

const (
	EventStartedName   event.Name = "operation.started"
	EventCompletedName event.Name = "operation.completed"
	EventFailedName    event.Name = "operation.failed"
	EventRejectedName  event.Name = "operation.rejected"
	EventCanceledName  event.Name = "operation.canceled"
)

// OperationStarted is emitted when operation execution starts.
type OperationStarted struct {
	CallID    CallID    `json:"call_id,omitempty"`
	Operation Ref       `json:"operation"`
	Input     *ValueRef `json:"input,omitempty"`
}

// EventName returns the event name.
func (OperationStarted) EventName() event.Name { return EventStartedName }

// OperationCompleted is emitted when operation execution succeeds.
type OperationCompleted struct {
	CallID    CallID    `json:"call_id,omitempty"`
	Operation Ref       `json:"operation"`
	Output    *ValueRef `json:"output,omitempty"`
}

// EventName returns the event name.
func (OperationCompleted) EventName() event.Name { return EventCompletedName }

// OperationFailed is emitted when operation execution fails.
type OperationFailed struct {
	CallID    CallID `json:"call_id,omitempty"`
	Operation Ref    `json:"operation"`
	Error     *Error `json:"error,omitempty"`
}

// EventName returns the event name.
func (OperationFailed) EventName() event.Name { return EventFailedName }

// OperationRejected is emitted when operation execution is rejected by policy.
type OperationRejected struct {
	CallID    CallID `json:"call_id,omitempty"`
	Operation Ref    `json:"operation"`
	Error     *Error `json:"error,omitempty"`
}

// EventName returns the event name.
func (OperationRejected) EventName() event.Name { return EventRejectedName }

// OperationCanceled is emitted when operation execution is canceled.
type OperationCanceled struct {
	CallID    CallID `json:"call_id,omitempty"`
	Operation Ref    `json:"operation"`
	Error     *Error `json:"error,omitempty"`
}

// EventName returns the event name.
func (OperationCanceled) EventName() event.Name { return EventCanceledName }

// ValueRef references an input or output value without forcing large values
// into every event payload.
type ValueRef struct {
	Kind      string `json:"kind,omitempty"`
	URI       string `json:"uri,omitempty"`
	MediaType string `json:"media_type,omitempty"`
	Digest    string `json:"digest,omitempty"`
}
