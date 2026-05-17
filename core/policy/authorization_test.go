package policy

import "testing"

func TestEvaluateAuthorizationDefaultDeny(t *testing.T) {
	got := EvaluateAuthorization(AuthorizationPolicy{}, AuthorizationRequest{
		Subjects: []SubjectRef{{Kind: SubjectUser, ID: "timo@localhost"}},
		Trust:    Trust{Kind: TrustInvocation, Level: TrustPrivileged},
		Resource: ResourceRef{Kind: ResourceDatasource, Name: "docs"},
		Action:   ActionDatasourceRead,
	})
	if got.Decision != DecisionDeny || got.Reason != "no_grants" {
		t.Fatalf("decision = %#v, want no_grants deny", got)
	}
}

func TestEvaluateAuthorizationMatchesGrant(t *testing.T) {
	policy := AuthorizationPolicy{Grants: []Grant{{
		Subjects: []SubjectRef{{Kind: SubjectGroup, ID: "docs"}},
		Resources: []ResourceRef{{
			Kind: ResourceDatasource,
			Name: "local_docs",
		}},
		Actions: []Action{ActionDatasourceRead},
	}}}
	got := EvaluateAuthorization(policy, AuthorizationRequest{
		Subjects: []SubjectRef{{Kind: SubjectUser, ID: "timo@localhost"}, {Kind: SubjectGroup, ID: "docs"}},
		Trust:    Trust{Kind: TrustInvocation, Level: TrustVerified},
		Resource: ResourceRef{Kind: ResourceDatasource, Name: "local_docs"},
		Action:   ActionDatasourceRead,
	})
	if got.Decision != DecisionAllow {
		t.Fatalf("decision = %#v, want allow", got)
	}
}

func TestEvaluateAuthorizationWildcards(t *testing.T) {
	policy := AuthorizationPolicy{Grants: []Grant{{
		Subjects:  []SubjectRef{{Kind: SubjectGroup, ID: "local_operators"}},
		Resources: []ResourceRef{{Kind: ResourcePath, Path: "docs/**"}},
		Actions:   []Action{"workspace.*"},
	}}}
	got := EvaluateAuthorization(policy, AuthorizationRequest{
		Subjects: []SubjectRef{{Kind: SubjectGroup, ID: "local_operators"}},
		Trust:    Trust{Kind: TrustInvocation, Level: TrustPrivileged},
		Resource: ResourceRef{Kind: ResourcePath, Path: "docs/readme.md"},
		Action:   ActionWorkspaceWrite,
	})
	if got.Decision != DecisionAllow {
		t.Fatalf("decision = %#v, want allow", got)
	}
}

func TestEvaluateAuthorizationTrustAndScopes(t *testing.T) {
	policy := AuthorizationPolicy{Grants: []Grant{{
		Subjects:       []SubjectRef{{Kind: SubjectUser, ID: "timo@localhost"}},
		Resources:      []ResourceRef{{Kind: ResourceDatasource, Name: "*"}},
		Actions:        []Action{ActionDatasourceIndex},
		RequiredTrust:  TrustPrivileged,
		RequiredScopes: []Scope{Scope(ActionDatasourceIndex)},
	}}}
	got := EvaluateAuthorization(policy, AuthorizationRequest{
		Subjects: []SubjectRef{{Kind: SubjectUser, ID: "timo@localhost"}},
		Trust:    Trust{Kind: TrustInvocation, Level: TrustPrivileged, Scopes: []Scope{Scope(ActionDatasourceRead)}},
		Resource: ResourceRef{Kind: ResourceDatasource, Name: "docs"},
		Action:   ActionDatasourceIndex,
	})
	if got.Decision != DecisionDeny || got.Reason != "missing_scopes" {
		t.Fatalf("decision = %#v, want missing scopes", got)
	}
	got = EvaluateAuthorization(policy, AuthorizationRequest{
		Subjects: []SubjectRef{{Kind: SubjectUser, ID: "timo@localhost"}},
		Trust:    Trust{Kind: TrustInvocation, Level: TrustPrivileged, Scopes: []Scope{Scope(ActionDatasourceIndex)}},
		Resource: ResourceRef{Kind: ResourceDatasource, Name: "docs"},
		Action:   ActionDatasourceIndex,
	})
	if got.Decision != DecisionAllow {
		t.Fatalf("decision = %#v, want allow", got)
	}
}

func TestEvaluateAuthorizationApprovalRequired(t *testing.T) {
	policy := AuthorizationPolicy{Grants: []Grant{{
		Subjects:         []SubjectRef{{Kind: SubjectGroup, ID: "operators"}},
		Resources:        []ResourceRef{{Kind: ResourceProcess, Name: "*"}},
		Actions:          []Action{ActionProcessExec},
		RequiresApproval: true,
	}}}
	got := EvaluateAuthorization(policy, AuthorizationRequest{
		Subjects: []SubjectRef{{Kind: SubjectGroup, ID: "operators"}},
		Trust:    Trust{Kind: TrustInvocation, Level: TrustPrivileged},
		Resource: ResourceRef{Kind: ResourceProcess, Name: "git"},
		Action:   ActionProcessExec,
	})
	if got.Decision != DecisionApprovalRequired {
		t.Fatalf("decision = %#v, want approval required", got)
	}
}

func TestEvaluateAuthorizationSecretResource(t *testing.T) {
	policy := AuthorizationPolicy{Grants: []Grant{{
		Subjects:  []SubjectRef{{Kind: SubjectGroup, ID: "local_operators"}},
		Resources: []ResourceRef{{Kind: ResourceSecret, Name: "env/OPENAI_API_KEY"}},
		Actions:   []Action{ActionSecretRead},
	}}}
	got := EvaluateAuthorization(policy, AuthorizationRequest{
		Subjects: []SubjectRef{{Kind: SubjectGroup, ID: "local_operators"}},
		Trust:    Trust{Kind: TrustInvocation, Level: TrustPrivileged},
		Resource: ResourceRef{Kind: ResourceSecret, Name: "env/OPENAI_API_KEY"},
		Action:   ActionSecretRead,
	})
	if got.Decision != DecisionAllow {
		t.Fatalf("decision = %#v, want allow", got)
	}
	got = EvaluateAuthorization(policy, AuthorizationRequest{
		Subjects: []SubjectRef{{Kind: SubjectGroup, ID: "local_operators"}},
		Trust:    Trust{Kind: TrustInvocation, Level: TrustPrivileged},
		Resource: ResourceRef{Kind: ResourceSecret, Name: "env/ANTHROPIC_API_KEY"},
		Action:   ActionSecretRead,
	})
	if got.Decision != DecisionDeny {
		t.Fatalf("decision = %#v, want deny", got)
	}
}
