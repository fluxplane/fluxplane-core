package user

import "testing"

func TestTrustLevels(t *testing.T) {
	levels := TrustLevels()
	if len(levels) != 3 {
		t.Fatalf("TrustLevels() returned %d levels, want 3", len(levels))
	}
	found := map[TrustLevel]bool{}
	for _, l := range levels {
		found[l] = true
	}
	if !found[TrustPublic] {
		t.Errorf("TrustLevels() missing TrustPublic")
	}
	if !found[TrustInternal] {
		t.Errorf("TrustLevels() missing TrustInternal")
	}
	if !found[TrustOperator] {
		t.Errorf("TrustLevels() missing TrustOperator")
	}
}

func TestResolutionStates(t *testing.T) {
	states := ResolutionStates()
	if len(states) != 2 {
		t.Fatalf("ResolutionStates() returned %d states, want 2", len(states))
	}
	found := map[ResolutionState]bool{}
	for _, s := range states {
		found[s] = true
	}
	if !found[ResolutionUnresolved] {
		t.Errorf("ResolutionStates() missing ResolutionUnresolved")
	}
	if !found[ResolutionResolved] {
		t.Errorf("ResolutionStates() missing ResolutionResolved")
	}
}

func TestTrustLevelConstants(t *testing.T) {
	if TrustPublic != "public" {
		t.Errorf("TrustPublic = %q, want %q", TrustPublic, "public")
	}
	if TrustInternal != "internal" {
		t.Errorf("TrustInternal = %q, want %q", TrustInternal, "internal")
	}
	if TrustOperator != "operator" {
		t.Errorf("TrustOperator = %q, want %q", TrustOperator, "operator")
	}
}

func TestResolutionStateConstants(t *testing.T) {
	if ResolutionUnresolved != "unresolved" {
		t.Errorf("ResolutionUnresolved = %q, want %q", ResolutionUnresolved, "unresolved")
	}
	if ResolutionResolved != "resolved" {
		t.Errorf("ResolutionResolved = %q, want %q", ResolutionResolved, "resolved")
	}
}

func TestNormalizeTrustEmpty(t *testing.T) {
	got := NormalizeTrust("")
	if got != TrustPublic {
		t.Fatalf("NormalizeTrust(\"\") = %q, want %q", got, TrustPublic)
	}
}

func TestNormalizeTrustPreserves(t *testing.T) {
	for _, level := range []TrustLevel{TrustPublic, TrustInternal, TrustOperator} {
		got := NormalizeTrust(level)
		if got != level {
			t.Fatalf("NormalizeTrust(%q) = %q, want same", level, got)
		}
	}
}

func TestMinReturnsMoreRestrictive(t *testing.T) {
	cases := []struct {
		a, b TrustLevel
		want TrustLevel
	}{
		{TrustOperator, TrustInternal, TrustInternal},
		{TrustInternal, TrustOperator, TrustInternal},
		{TrustPublic, TrustOperator, TrustPublic},
		{TrustOperator, TrustPublic, TrustPublic},
		{TrustInternal, TrustInternal, TrustInternal},
		{"", TrustOperator, TrustPublic}, // empty normalizes to public
	}
	for _, tc := range cases {
		got := Min(tc.a, tc.b)
		if got != tc.want {
			t.Errorf("Min(%q, %q) = %q, want %q", tc.a, tc.b, got, tc.want)
		}
	}
}

func TestMaxReturnsLessRestrictive(t *testing.T) {
	cases := []struct {
		a, b TrustLevel
		want TrustLevel
	}{
		{TrustOperator, TrustInternal, TrustOperator},
		{TrustInternal, TrustOperator, TrustOperator},
		{TrustPublic, TrustOperator, TrustOperator},
		{TrustOperator, TrustPublic, TrustOperator},
		{TrustInternal, TrustInternal, TrustInternal},
		{"", TrustPublic, TrustPublic}, // empty normalizes to public
		{"", TrustOperator, TrustOperator},
	}
	for _, tc := range cases {
		got := Max(tc.a, tc.b)
		if got != tc.want {
			t.Errorf("Max(%q, %q) = %q, want %q", tc.a, tc.b, got, tc.want)
		}
	}
}

func TestNormalizeResolution(t *testing.T) {
	tests := []struct {
		input ResolutionState
		want  ResolutionState
	}{
		{ResolutionResolved, ResolutionResolved},
		{ResolutionUnresolved, ResolutionUnresolved},
		{"", ResolutionUnresolved},
	}
	for _, tt := range tests {
		got := NormalizeResolution(tt.input)
		if got != tt.want {
			t.Errorf("NormalizeResolution(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestUser(t *testing.T) {
	u := User{
		ID:          "user-1",
		Username:    "alice",
		DisplayName: "Alice",
		Trust:       TrustInternal,
		Groups:      []ID{"group-1"},
		Emails:      []Email{{Address: "alice@example.com", Verified: true, Primary: true}},
	}
	if u.ID != "user-1" || u.Username != "alice" {
		t.Errorf("User = %#v", u)
	}
}

func TestEmail(t *testing.T) {
	email := Email{Address: "alice@example.com", Verified: true, Primary: true, Source: "google"}
	if email.Address != "alice@example.com" || !email.Verified {
		t.Errorf("Email = %#v", email)
	}
}

func TestIdentity(t *testing.T) {
	id := Identity{
		Provider:    "google",
		ProviderID:  "uid-123",
		Email:       "alice@example.com",
		DisplayName: "Alice",
		Claims:      map[string]string{"hd": "example.com"},
	}
	if id.Provider != "google" || id.ProviderID != "uid-123" {
		t.Errorf("Identity = %#v", id)
	}
}

func TestGroup(t *testing.T) {
	g := Group{
		ID:          "group-1",
		DisplayName: "Admins",
		Members:     []ID{"user-1", "user-2"},
		Trust:       TrustOperator,
	}
	if g.ID != "group-1" || len(g.Members) != 2 {
		t.Errorf("Group = %#v", g)
	}
}

func TestGroupRule(t *testing.T) {
	rule := GroupRule{
		Match: IdentityMatch{
			Provider:   "google",
			ProviderID: "uid-123",
			Resolution: ResolutionResolved,
		},
		Groups: []ID{"group-1"},
	}
	if rule.Match.Provider != "google" || len(rule.Groups) != 1 {
		t.Errorf("GroupRule = %#v", rule)
	}
}

func TestActor(t *testing.T) {
	actor := Actor{
		User:       User{ID: "user-1"},
		Identity:   Identity{Provider: "google", ProviderID: "uid-123"},
		Groups:     []Group{{ID: "group-1"}},
		Trust:      TrustInternal,
		Resolution: ResolutionResolved,
	}
	if actor.User.ID != "user-1" || actor.Resolution != ResolutionResolved {
		t.Errorf("Actor = %#v", actor)
	}
}
