package policy

// CallerKind classifies who or what initiated an invocation.
type CallerKind string

const (
	CallerUser   CallerKind = "user"
	CallerAgent  CallerKind = "agent"
	CallerSystem CallerKind = "system"
)

// Principal identifies the concrete actor behind a caller.
type Principal struct {
	Kind string `json:"kind,omitempty"`
	ID   string `json:"id,omitempty"`
	Name string `json:"name,omitempty"`
}

// Caller describes an invocation initiator.
type Caller struct {
	Kind      CallerKind `json:"kind"`
	Principal Principal  `json:"principal,omitempty"`
	Source    string     `json:"source,omitempty"`
}

// Scope names one permission or authority scope.
type Scope string

// TrustLevel describes the authority/confidence assigned to a caller in a
// channel/session context.
type TrustLevel string

const (
	TrustUntrusted  TrustLevel = "untrusted"
	TrustVerified   TrustLevel = "verified"
	TrustPrivileged TrustLevel = "privileged"
	TrustSystem     TrustLevel = "system"
)

// TrustKind describes what trust is about.
type TrustKind string

const (
	TrustInvocation TrustKind = "invocation"
	TrustSource     TrustKind = "source"
	TrustTarget     TrustKind = "target"
)

// Trust describes authority/confidence assigned to an invocation, source, or
// target boundary.
type Trust struct {
	Kind       TrustKind  `json:"kind,omitempty"`
	Level      TrustLevel `json:"level,omitempty"`
	Scopes     []Scope    `json:"scopes,omitempty"`
	VerifiedBy string     `json:"verified_by,omitempty"`
	Reason     string     `json:"reason,omitempty"`
}

// Sensitivity classifies how carefully a value or record should be exposed.
type Sensitivity string

const (
	SensitivityPublic       Sensitivity = "public"
	SensitivityInternal     Sensitivity = "internal"
	SensitivityRestricted   Sensitivity = "restricted"
	SensitivityConfidential Sensitivity = "confidential"
	SensitivitySecret       Sensitivity = "secret"
)

// NormalizeSensitivity returns a safe default for missing sensitivity.
func NormalizeSensitivity(sensitivity Sensitivity) Sensitivity {
	if sensitivity == "" {
		return SensitivityRestricted
	}
	return sensitivity
}

// InvocationPolicy describes who may invoke one projection and under which
// authority.
type InvocationPolicy struct {
	AllowedCallers   []CallerKind `json:"allowed_callers,omitempty"`
	RequiredTrust    TrustLevel   `json:"required_trust,omitempty"`
	RequiredScopes   []Scope      `json:"required_scopes,omitempty"`
	RequiresApproval bool         `json:"requires_approval,omitempty"`
}

// Decision classifies a pure policy evaluation.
type Decision string

const (
	DecisionAllow            Decision = "allow"
	DecisionDeny             Decision = "deny"
	DecisionApprovalRequired Decision = "approval_required"
)

// Evaluation reports the result of evaluating one invocation policy.
type Evaluation struct {
	Decision      Decision `json:"decision"`
	Reason        string   `json:"reason,omitempty"`
	MissingScopes []Scope  `json:"missing_scopes,omitempty"`
}

// EvaluateInvocation evaluates policy against caller and trust. It is a pure
// helper, not an enforcement mechanism.
func EvaluateInvocation(invocation InvocationPolicy, caller Caller, trust Trust) Evaluation {
	if trust.Kind != TrustInvocation {
		return Evaluation{Decision: DecisionDeny, Reason: "wrong_trust_kind"}
	}
	if len(invocation.AllowedCallers) > 0 && !callerAllowed(invocation.AllowedCallers, caller.Kind) {
		return Evaluation{Decision: DecisionDeny, Reason: "caller_not_allowed"}
	}
	if !TrustSatisfies(trust.Level, invocation.RequiredTrust) {
		return Evaluation{Decision: DecisionDeny, Reason: "insufficient_trust"}
	}
	if missing := missingScopes(invocation.RequiredScopes, trust.Scopes); len(missing) > 0 {
		return Evaluation{Decision: DecisionDeny, Reason: "missing_scopes", MissingScopes: missing}
	}
	if invocation.RequiresApproval {
		return Evaluation{Decision: DecisionApprovalRequired, Reason: "approval_required"}
	}
	return Evaluation{Decision: DecisionAllow}
}

// TrustSatisfies reports whether actual meets or exceeds required.
func TrustSatisfies(actual, required TrustLevel) bool {
	if required == "" {
		return true
	}
	return trustRank(actual) >= trustRank(required)
}

func callerAllowed(allowed []CallerKind, actual CallerKind) bool {
	for _, kind := range allowed {
		if kind == actual {
			return true
		}
	}
	return false
}

func missingScopes(required []Scope, actual []Scope) []Scope {
	if len(required) == 0 {
		return nil
	}
	have := map[Scope]bool{}
	for _, scope := range actual {
		have[scope] = true
	}
	var missing []Scope
	for _, scope := range required {
		if !have[scope] {
			missing = append(missing, scope)
		}
	}
	return missing
}

func trustRank(level TrustLevel) int {
	switch level {
	case TrustSystem:
		return 4
	case TrustPrivileged:
		return 3
	case TrustVerified:
		return 2
	case TrustUntrusted:
		return 1
	default:
		return 0
	}
}
