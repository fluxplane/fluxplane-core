// Package policyevent contains core-owned event types for policy decisions.
package policyevent

import (
	"context"

	"github.com/fluxplane/fluxplane-event"
	"github.com/fluxplane/fluxplane-policy"
	"github.com/fluxplane/fluxplane-policy/policyauth"
)

const EventAuthorizationDecisionName event.Name = "policy.authorization_decision"

// AuthorizationDecision records one policy decision without exposing protected
// values.
type AuthorizationDecision struct {
	Subjects []policy.SubjectRef `json:"subjects,omitempty"`
	Trust    policy.TrustLevel   `json:"trust,omitempty"`
	Resource policy.ResourceRef  `json:"resource"`
	Action   policy.Action       `json:"action"`
	Decision policy.Decision     `json:"decision"`
	Reason   string              `json:"reason,omitempty"`
}

// EventName implements event.Event.
func (AuthorizationDecision) EventName() event.Name { return EventAuthorizationDecisionName }

type authorizationEventSink interface {
	Events() event.Sink
}

// EmitAuthorizationDecision emits decision on ctx when there is an event sink
// available. Allow decisions are emitted only when TraceAllows is true.
func EmitAuthorizationDecision(ctx context.Context, auth policyauth.AuthorizationContext, req policy.AuthorizationRequest, evaluation policy.Evaluation) {
	if evaluation.Decision == policy.DecisionAllow && !auth.TraceAllows {
		return
	}
	sink, ok := ctx.(authorizationEventSink)
	if !ok || sink.Events() == nil {
		return
	}
	sink.Events().Emit(AuthorizationDecision{
		Subjects: append([]policy.SubjectRef(nil), req.Subjects...),
		Trust:    req.Trust.Level,
		Resource: req.Resource,
		Action:   req.Action,
		Decision: evaluation.Decision,
		Reason:   evaluation.Reason,
	})
}
