package policy

import "github.com/fluxplane/fluxplane-policy/policyauth"

const EventAuthorizationDecisionName = policyauth.EventAuthorizationDecisionName

// AuthorizationDecision records one policy decision without exposing protected
// values.
type AuthorizationDecision = policyauth.AuthorizationDecision

// EmitAuthorizationDecision emits decision on ctx when there is an event sink
// available. Allow decisions are emitted only when TraceAllows is true.
var EmitAuthorizationDecision = policyauth.EmitAuthorizationDecision
