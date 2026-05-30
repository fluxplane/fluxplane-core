package policy

import (
	"context"

	"github.com/fluxplane/fluxplane-event"
)

const EventAuthorizationDecisionName event.Name = "policy.authorization_decision"

// AuthorizationDecision records one policy decision without exposing protected
// values.
type AuthorizationDecision struct {
	Subjects []SubjectRef `json:"subjects,omitempty"`
	Trust    TrustLevel   `json:"trust,omitempty"`
	Resource ResourceRef  `json:"resource"`
	Action   Action       `json:"action"`
	Decision Decision     `json:"decision"`
	Reason   string       `json:"reason,omitempty"`
}

// EventName implements event.Event.
func (AuthorizationDecision) EventName() event.Name { return EventAuthorizationDecisionName }

type authorizationEventSink interface {
	Events() event.Sink
}

// EmitAuthorizationDecision emits decision on ctx when there is an event sink
// available. Allow decisions are emitted only when TraceAllows is true.
func EmitAuthorizationDecision(ctx context.Context, auth AuthorizationContext, req AuthorizationRequest, evaluation Evaluation) {
	if evaluation.Decision == DecisionAllow && !auth.TraceAllows {
		return
	}
	sink, ok := ctx.(authorizationEventSink)
	if !ok || sink.Events() == nil {
		return
	}
	sink.Events().Emit(AuthorizationDecision{
		Subjects: append([]SubjectRef(nil), req.Subjects...),
		Trust:    req.Trust.Level,
		Resource: req.Resource,
		Action:   req.Action,
		Decision: evaluation.Decision,
		Reason:   evaluation.Reason,
	})
}
