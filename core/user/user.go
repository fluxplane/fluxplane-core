package user

// ID identifies one canonical person across external identities.
type ID string

// TrustLevel describes how much user-visible context a person may receive.
type TrustLevel string

const (
	TrustPublic   TrustLevel = "public"
	TrustInternal TrustLevel = "internal"
	TrustOperator TrustLevel = "operator"
)

// ResolutionState records whether an inbound channel identity has been mapped
// to a canonical system user.
type ResolutionState string

const (
	ResolutionUnresolved ResolutionState = "unresolved"
	ResolutionResolved   ResolutionState = "resolved"
)

// User is a stable person record.
type User struct {
	ID          ID                `json:"id"`
	Username    string            `json:"username,omitempty"`
	DisplayName string            `json:"display_name,omitempty"`
	Trust       TrustLevel        `json:"trust,omitempty"`
	Groups      []ID              `json:"groups,omitempty"`
	Emails      []Email           `json:"emails,omitempty"`
	Identities  []Identity        `json:"identities,omitempty"`
	Annotations map[string]string `json:"annotations,omitempty"`
}

// Email is a verified canonical email alias for a user.
type Email struct {
	Address     string            `json:"address"`
	Verified    bool              `json:"verified,omitempty"`
	Primary     bool              `json:"primary,omitempty"`
	Source      string            `json:"source,omitempty"`
	Annotations map[string]string `json:"annotations,omitempty"`
}

// Identity is a provider-specific proof or claim about a user.
type Identity struct {
	Provider    string            `json:"provider"`
	ProviderID  string            `json:"provider_id"`
	Email       string            `json:"email,omitempty"`
	DisplayName string            `json:"display_name,omitempty"`
	Claims      map[string]string `json:"claims,omitempty"`
}

// Group is a stable, app-defined user set that can carry trust outside any
// channel-specific identity model.
type Group struct {
	ID          ID                `json:"id"`
	DisplayName string            `json:"display_name,omitempty"`
	Members     []ID              `json:"members,omitempty"`
	Trust       TrustLevel        `json:"trust,omitempty"`
	Annotations map[string]string `json:"annotations,omitempty"`
}

// IdentityMatch selects an inbound or resolved provider identity.
type IdentityMatch struct {
	Provider   string          `json:"provider,omitempty"`
	ProviderID string          `json:"provider_id,omitempty"`
	Resolution ResolutionState `json:"resolution,omitempty"`
}

// GroupRule adds groups to actors whose final identity matches.
type GroupRule struct {
	Match  IdentityMatch `json:"match,omitempty"`
	Groups []ID          `json:"groups,omitempty"`
}

// Actor is the resolved user identity for one inbound interaction.
type Actor struct {
	User       User            `json:"user,omitempty"`
	Identity   Identity        `json:"identity,omitempty"`
	Identities []Identity      `json:"identities,omitempty"`
	Groups     []Group         `json:"groups,omitempty"`
	Trust      TrustLevel      `json:"trust,omitempty"`
	Resolution ResolutionState `json:"resolution,omitempty"`
}

// NormalizeTrust returns a conservative default for empty trust.
func NormalizeTrust(level TrustLevel) TrustLevel {
	if level == "" {
		return TrustPublic
	}
	return level
}

// Min returns the more restrictive of two trust levels.
func Min(a, b TrustLevel) TrustLevel {
	a = NormalizeTrust(a)
	b = NormalizeTrust(b)
	if trustRank(a) <= trustRank(b) {
		return a
	}
	return b
}

// Max returns the less restrictive of two trust levels.
func Max(a, b TrustLevel) TrustLevel {
	a = NormalizeTrust(a)
	b = NormalizeTrust(b)
	if trustRank(a) >= trustRank(b) {
		return a
	}
	return b
}

func trustRank(level TrustLevel) int {
	switch level {
	case TrustOperator:
		return 3
	case TrustInternal:
		return 2
	default:
		return 1
	}
}

// NormalizeResolution returns the conservative default for missing actor
// resolution state.
func NormalizeResolution(state ResolutionState) ResolutionState {
	if state == ResolutionResolved {
		return ResolutionResolved
	}
	return ResolutionUnresolved
}
