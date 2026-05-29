package operationruntime

import (
	"github.com/fluxplane/fluxplane-core/core/operation"
	"github.com/fluxplane/fluxplane-core/core/policy"
	"github.com/fluxplane/fluxplane-event"
)

const (
	EventApprovalRequestedName event.Name = "operation.approval_requested"
	EventApprovalGrantedName   event.Name = "operation.approval_granted"
	EventApprovalDeniedName    event.Name = "operation.approval_denied"
)

// ApprovalRequested is emitted before an operation approval decision is made.
type ApprovalRequested struct {
	Subjects  []policy.SubjectRef `json:"subjects,omitempty"`
	Resource  policy.ResourceRef  `json:"resource,omitempty"`
	Action    policy.Action       `json:"action,omitempty"`
	Operation operation.Ref       `json:"operation"`
	Risk      CommandRisk         `json:"risk,omitempty"`
	Reason    string              `json:"reason,omitempty"`
}

// EventName returns the runtime event name.
func (ApprovalRequested) EventName() event.Name { return EventApprovalRequestedName }

// ApprovalGranted is emitted after an approval gate allows execution.
type ApprovalGranted struct {
	Subjects  []policy.SubjectRef `json:"subjects,omitempty"`
	Resource  policy.ResourceRef  `json:"resource,omitempty"`
	Action    policy.Action       `json:"action,omitempty"`
	Operation operation.Ref       `json:"operation"`
	Risk      CommandRisk         `json:"risk,omitempty"`
	Reason    string              `json:"reason,omitempty"`
}

// EventName returns the runtime event name.
func (ApprovalGranted) EventName() event.Name { return EventApprovalGrantedName }

// ApprovalDenied is emitted when approval is unavailable, unauthorized, or
// rejected by the configured approval gate.
type ApprovalDenied struct {
	Subjects  []policy.SubjectRef `json:"subjects,omitempty"`
	Resource  policy.ResourceRef  `json:"resource,omitempty"`
	Action    policy.Action       `json:"action,omitempty"`
	Operation operation.Ref       `json:"operation"`
	Risk      CommandRisk         `json:"risk,omitempty"`
	Reason    string              `json:"reason,omitempty"`
	Error     string              `json:"error,omitempty"`
}

// EventName returns the runtime event name.
func (ApprovalDenied) EventName() event.Name { return EventApprovalDeniedName }
