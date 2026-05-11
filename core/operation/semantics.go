package operation

// Determinism describes whether the same input and same ambient state should
// produce the same output.
type Determinism string

const (
	DeterminismUnknown          Determinism = ""
	DeterminismDeterministic    Determinism = "deterministic"
	DeterminismNonDeterministic Determinism = "non_deterministic"
)

// Idempotency describes whether repeating an operation with the same input is
// expected to have the same external effect as running it once.
type Idempotency string

const (
	IdempotencyUnknown       Idempotency = ""
	IdempotencyIdempotent    Idempotency = "idempotent"
	IdempotencyNonIdempotent Idempotency = "non_idempotent"
)

// RiskLevel is a coarse declaration used by runtime policy and approval gates.
type RiskLevel string

const (
	RiskUnknown  RiskLevel = ""
	RiskLow      RiskLevel = "low"
	RiskMedium   RiskLevel = "medium"
	RiskHigh     RiskLevel = "high"
	RiskCritical RiskLevel = "critical"
)

// Effect declares one kind of observable side effect or external dependency.
type Effect string

const (
	EffectNone Effect = "none"

	EffectReadExternal  Effect = "read_external"
	EffectWriteExternal Effect = "write_external"

	EffectFilesystem Effect = "filesystem"
	EffectNetwork    Effect = "network"
	EffectProcess    Effect = "process"

	EffectCreate Effect = "create"
	EffectUpdate Effect = "update"
	EffectDelete Effect = "delete"

	EffectDestructive   Effect = "destructive"
	EffectIrreversible  Effect = "irreversible"
	EffectSensitiveData Effect = "sensitive_data"
)

// EffectSet is a serializable set-like list of effect declarations.
type EffectSet []Effect

// Has reports whether the set contains effect.
func (s EffectSet) Has(effect Effect) bool {
	for _, existing := range s {
		if existing == effect {
			return true
		}
	}
	return false
}

// Empty reports whether the set declares no side effects.
func (s EffectSet) Empty() bool {
	return len(s) == 0 || s.Only(EffectNone)
}

// Only reports whether every declared effect is the provided effect.
func (s EffectSet) Only(effect Effect) bool {
	if len(s) == 0 {
		return effect == EffectNone
	}
	for _, existing := range s {
		if existing != effect {
			return false
		}
	}
	return true
}

// Semantics describes the operation's execution claims. These claims are not
// enforcement by themselves; runtime policy can use them for validation,
// approval, planning, workflow eligibility, retries, and audit.
type Semantics struct {
	Determinism Determinism `json:"determinism,omitempty"`
	Effects     EffectSet   `json:"effects,omitempty"`
	Idempotency Idempotency `json:"idempotency,omitempty"`
	Risk        RiskLevel   `json:"risk,omitempty"`
}

// Pure reports whether the operation claims to be deterministic and free of
// side effects.
func (s Semantics) Pure() bool {
	return s.Determinism == DeterminismDeterministic && s.Effects.Empty()
}

// ReadOnly reports whether the operation declares no writes, mutation, or
// destructive effects.
func (s Semantics) ReadOnly() bool {
	return !s.Effects.Has(EffectWriteExternal) &&
		!s.Effects.Has(EffectCreate) &&
		!s.Effects.Has(EffectUpdate) &&
		!s.Effects.Has(EffectDelete) &&
		!s.Effects.Has(EffectDestructive) &&
		!s.Effects.Has(EffectIrreversible)
}
