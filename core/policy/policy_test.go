package policy

import (
	"reflect"
	"testing"
)

func TestNormalizeSensitivityEmpty(t *testing.T) {
	got := NormalizeSensitivity("")
	if got != SensitivityRestricted {
		t.Fatalf("NormalizeSensitivity(\"\") = %q, want %q", got, SensitivityRestricted)
	}
}

func TestNormalizeSensitivityPreserves(t *testing.T) {
	for _, s := range []Sensitivity{
		SensitivityPublic, SensitivityInternal, SensitivityRestricted,
		SensitivityConfidential, SensitivitySecret,
	} {
		got := NormalizeSensitivity(s)
		if got != s {
			t.Fatalf("NormalizeSensitivity(%q) = %q, want same", s, got)
		}
	}
}

func TestTrustSatisfiesEmptyRequired(t *testing.T) {
	if !TrustSatisfies(TrustUntrusted, "") {
		t.Fatal("TrustSatisfies(_, \"\") should return true")
	}
}

func TestTrustSatisfiesRankOrdering(t *testing.T) {
	cases := []struct {
		actual   TrustLevel
		required TrustLevel
		want     bool
	}{
		{TrustSystem, TrustSystem, true},
		{TrustSystem, TrustPrivileged, true},
		{TrustSystem, TrustVerified, true},
		{TrustSystem, TrustUntrusted, true},
		{TrustPrivileged, TrustSystem, false},
		{TrustVerified, TrustPrivileged, false},
		{TrustUntrusted, TrustVerified, false},
		{TrustUntrusted, TrustUntrusted, true},
	}
	for _, tc := range cases {
		got := TrustSatisfies(tc.actual, tc.required)
		if got != tc.want {
			t.Errorf("TrustSatisfies(%q, %q) = %v, want %v", tc.actual, tc.required, got, tc.want)
		}
	}
}

func TestEvaluateInvocationWrongTrustKind(t *testing.T) {
	policy := InvocationPolicy{}
	caller := Caller{Kind: CallerUser}
	trust := Trust{Kind: TrustSource, Level: TrustSystem}
	result := EvaluateInvocation(policy, caller, trust)
	if result.Decision != DecisionDeny {
		t.Fatalf("Decision = %q, want deny", result.Decision)
	}
	if result.Reason != "wrong_trust_kind" {
		t.Fatalf("Reason = %q, want wrong_trust_kind", result.Reason)
	}
}

func TestEvaluateInvocationCallerNotAllowed(t *testing.T) {
	policy := InvocationPolicy{AllowedCallers: []CallerKind{CallerSystem}}
	caller := Caller{Kind: CallerUser}
	trust := Trust{Kind: TrustInvocation, Level: TrustSystem}
	result := EvaluateInvocation(policy, caller, trust)
	if result.Decision != DecisionDeny {
		t.Fatalf("Decision = %q, want deny", result.Decision)
	}
	if result.Reason != "caller_not_allowed" {
		t.Fatalf("Reason = %q, want caller_not_allowed", result.Reason)
	}
}

func TestEvaluateInvocationInsufficientTrust(t *testing.T) {
	policy := InvocationPolicy{RequiredTrust: TrustPrivileged}
	caller := Caller{Kind: CallerUser}
	trust := Trust{Kind: TrustInvocation, Level: TrustUntrusted}
	result := EvaluateInvocation(policy, caller, trust)
	if result.Decision != DecisionDeny {
		t.Fatalf("Decision = %q, want deny", result.Decision)
	}
	if result.Reason != "insufficient_trust" {
		t.Fatalf("Reason = %q, want insufficient_trust", result.Reason)
	}
}

func TestEvaluateInvocationMissingScopes(t *testing.T) {
	policy := InvocationPolicy{RequiredScopes: []Scope{"read", "write"}}
	caller := Caller{Kind: CallerUser}
	trust := Trust{Kind: TrustInvocation, Level: TrustVerified, Scopes: []Scope{"read"}}
	result := EvaluateInvocation(policy, caller, trust)
	if result.Decision != DecisionDeny {
		t.Fatalf("Decision = %q, want deny", result.Decision)
	}
	if result.Reason != "missing_scopes" {
		t.Fatalf("Reason = %q, want missing_scopes", result.Reason)
	}
	if len(result.MissingScopes) != 1 || result.MissingScopes[0] != "write" {
		t.Fatalf("MissingScopes = %v, want [write]", result.MissingScopes)
	}
}

func TestEvaluateInvocationApprovalRequired(t *testing.T) {
	policy := InvocationPolicy{RequiresApproval: true}
	caller := Caller{Kind: CallerUser}
	trust := Trust{Kind: TrustInvocation, Level: TrustVerified}
	result := EvaluateInvocation(policy, caller, trust)
	if result.Decision != DecisionApprovalRequired {
		t.Fatalf("Decision = %q, want approval_required", result.Decision)
	}
}

func TestEvaluateInvocationAllow(t *testing.T) {
	policy := InvocationPolicy{
		AllowedCallers: []CallerKind{CallerUser},
		RequiredTrust:  TrustVerified,
		RequiredScopes: []Scope{"read"},
	}
	caller := Caller{Kind: CallerUser}
	trust := Trust{Kind: TrustInvocation, Level: TrustVerified, Scopes: []Scope{"read", "write"}}
	result := EvaluateInvocation(policy, caller, trust)
	if result.Decision != DecisionAllow {
		t.Fatalf("Decision = %q, want allow (reason: %s)", result.Decision, result.Reason)
	}
}

func TestEvaluateInvocationAllowOpenPolicy(t *testing.T) {
	// Empty policy should allow any invocation caller with invocation trust.
	caller := Caller{Kind: CallerAgent}
	trust := Trust{Kind: TrustInvocation}
	result := EvaluateInvocation(InvocationPolicy{}, caller, trust)
	if result.Decision != DecisionAllow {
		t.Fatalf("Decision = %q, want allow", result.Decision)
	}
}

func TestPolicyVocabularies(t *testing.T) {
	if got, want := CallerKinds(), []CallerKind{CallerUser, CallerAgent, CallerSystem}; !reflect.DeepEqual(got, want) {
		t.Fatalf("CallerKinds() = %#v, want %#v", got, want)
	}
	if got, want := TrustLevels(), []TrustLevel{TrustUntrusted, TrustVerified, TrustPrivileged, TrustSystem}; !reflect.DeepEqual(got, want) {
		t.Fatalf("TrustLevels() = %#v, want %#v", got, want)
	}
	if got, want := TrustKinds(), []TrustKind{TrustInvocation, TrustSource, TrustTarget}; !reflect.DeepEqual(got, want) {
		t.Fatalf("TrustKinds() = %#v, want %#v", got, want)
	}
	if got, want := Sensitivities(), []Sensitivity{SensitivityPublic, SensitivityInternal, SensitivityRestricted, SensitivityConfidential, SensitivitySecret}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Sensitivities() = %#v, want %#v", got, want)
	}
	if got, want := Decisions(), []Decision{DecisionAllow, DecisionDeny, DecisionApprovalRequired}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Decisions() = %#v, want %#v", got, want)
	}
}

func TestEvaluateInvocationWildcardScopesAndUnknownTrust(t *testing.T) {
	result := EvaluateInvocation(
		InvocationPolicy{RequiredScopes: []Scope{"read", "write"}},
		Caller{Kind: CallerAgent},
		Trust{Kind: TrustInvocation, Level: TrustVerified, Scopes: []Scope{"*"}},
	)
	if result.Decision != DecisionAllow {
		t.Fatalf("Decision = %#v, want allow", result)
	}
	if TrustSatisfies("mystery", TrustUntrusted) {
		t.Fatal("unknown trust should not satisfy untrusted")
	}
}
