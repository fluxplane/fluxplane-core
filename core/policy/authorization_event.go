package policy

import "github.com/fluxplane/fluxplane-core/core/policyevent"

const EventAuthorizationDecisionName = policyevent.EventAuthorizationDecisionName

// AuthorizationDecision records one policy decision without exposing protected
// values.
type AuthorizationDecision = policyevent.AuthorizationDecision

// EmitAuthorizationDecision emits decision on ctx when there is an event sink
// available. Allow decisions are emitted only when TraceAllows is true.
var EmitAuthorizationDecision = policyevent.EmitAuthorizationDecision
