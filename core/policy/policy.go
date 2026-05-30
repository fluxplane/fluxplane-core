package policy

import base "github.com/fluxplane/fluxplane-policy"

// CallerKind classifies who or what initiated an invocation.
type CallerKind = base.CallerKind

const (
	CallerUser   = base.CallerUser
	CallerAgent  = base.CallerAgent
	CallerSystem = base.CallerSystem
)

// CallerKinds returns the stable invocation caller vocabulary.
var CallerKinds = base.CallerKinds

// Principal identifies the concrete actor behind a caller.
type Principal = base.Principal

// Caller describes an invocation initiator.
type Caller = base.Caller

// Scope names one permission or authority scope.
type Scope = base.Scope

// TrustLevel describes the authority/confidence assigned to a caller in a
// channel/session context.
type TrustLevel = base.TrustLevel

const (
	TrustUntrusted  = base.TrustUntrusted
	TrustVerified   = base.TrustVerified
	TrustPrivileged = base.TrustPrivileged
	TrustSystem     = base.TrustSystem
)

// TrustLevels returns the stable policy trust vocabulary.
var TrustLevels = base.TrustLevels

// TrustKind describes what trust is about.
type TrustKind = base.TrustKind

const (
	TrustInvocation = base.TrustInvocation
	TrustSource     = base.TrustSource
	TrustTarget     = base.TrustTarget
)

// TrustKinds returns the stable trust target vocabulary.
var TrustKinds = base.TrustKinds

// Trust describes authority/confidence assigned to an invocation, source, or
// target boundary.
type Trust = base.Trust

// Sensitivity classifies how carefully a value or record should be exposed.
type Sensitivity = base.Sensitivity

const (
	SensitivityPublic       = base.SensitivityPublic
	SensitivityInternal     = base.SensitivityInternal
	SensitivityRestricted   = base.SensitivityRestricted
	SensitivityConfidential = base.SensitivityConfidential
	SensitivitySecret       = base.SensitivitySecret
)

// Sensitivities returns the stable sensitivity vocabulary.
var Sensitivities = base.Sensitivities

// NormalizeSensitivity returns a safe default for missing sensitivity.
var NormalizeSensitivity = base.NormalizeSensitivity

// InvocationPolicy describes who may invoke one projection and under which
// authority.
type InvocationPolicy = base.InvocationPolicy

// Decision classifies a pure policy evaluation.
type Decision = base.Decision

const (
	DecisionAllow            = base.DecisionAllow
	DecisionDeny             = base.DecisionDeny
	DecisionApprovalRequired = base.DecisionApprovalRequired
)

// Decisions returns the stable policy decision vocabulary.
var Decisions = base.Decisions

// Evaluation reports the result of evaluating one invocation policy.
type Evaluation = base.Evaluation

// EvaluateInvocation evaluates policy against caller and trust. It is a pure
// helper, not an enforcement mechanism.
var EvaluateInvocation = base.EvaluateInvocation

// TrustSatisfies reports whether actual meets or exceeds required.
var TrustSatisfies = base.TrustSatisfies
