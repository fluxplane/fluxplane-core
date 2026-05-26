package policy

import (
	"context"
	"reflect"
	"testing"
)

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

func TestAuthorizationVocabulariesAndContext(t *testing.T) {
	if got := Actions(); len(got) != 26 || got[0] != ActionDatasourceRead || got[len(got)-1] != ActionSecretAdmin {
		t.Fatalf("Actions() = %#v", got)
	}
	if got, want := SubjectKinds(), []SubjectKind{SubjectUser, SubjectGroup, SubjectService, SubjectSystem, SubjectAgent}; !reflect.DeepEqual(got, want) {
		t.Fatalf("SubjectKinds() = %#v, want %#v", got, want)
	}
	if got := ResourceKinds(); len(got) != 12 || got[0] != ResourceDatasource || got[len(got)-1] != ResourceSecret {
		t.Fatalf("ResourceKinds() = %#v", got)
	}
	if !(AuthorizationPolicy{}).IsZero() || (AuthorizationPolicy{Grants: []Grant{{}}}).IsZero() {
		t.Fatal("AuthorizationPolicy.IsZero returned unexpected value")
	}
	if _, ok := AuthorizationFromContext(nil); ok {
		t.Fatal("AuthorizationFromContext(nil) ok = true")
	}
	auth := AuthorizationContext{Subjects: []SubjectRef{{Kind: SubjectUser, ID: "u"}}, TraceAllows: true}
	ctx := ContextWithAuthorization(nil, auth)
	got, ok := AuthorizationFromContext(ctx)
	if !ok || !reflect.DeepEqual(got, auth) {
		t.Fatalf("AuthorizationFromContext() = %#v, %v; want %#v, true", got, ok, auth)
	}
	if _, ok := AuthorizationFromContext(context.Background()); ok {
		t.Fatal("AuthorizationFromContext(background) ok = true")
	}
}

func TestEvaluateAuthorizationRejectsNonMatchingGrantFields(t *testing.T) {
	baseGrant := Grant{
		Subjects:  []SubjectRef{{Kind: SubjectUser, ID: "user-*"}},
		Resources: []ResourceRef{{Kind: ResourcePath, Path: "docs/*.md"}},
		Actions:   []Action{ActionWorkspaceRead},
	}
	tests := []struct {
		name   string
		grant  Grant
		req    AuthorizationRequest
		reason string
	}{
		{name: "empty grant subjects", grant: Grant{Resources: baseGrant.Resources, Actions: baseGrant.Actions}, req: AuthorizationRequest{Subjects: []SubjectRef{{Kind: SubjectUser, ID: "user-a"}}, Resource: ResourceRef{Kind: ResourcePath, Path: "docs/readme.md"}, Action: ActionWorkspaceRead}, reason: "no_matching_grant"},
		{name: "empty actual subjects", grant: baseGrant, req: AuthorizationRequest{Resource: ResourceRef{Kind: ResourcePath, Path: "docs/readme.md"}, Action: ActionWorkspaceRead}, reason: "no_matching_grant"},
		{name: "subject kind mismatch", grant: baseGrant, req: AuthorizationRequest{Subjects: []SubjectRef{{Kind: SubjectGroup, ID: "user-a"}}, Resource: ResourceRef{Kind: ResourcePath, Path: "docs/readme.md"}, Action: ActionWorkspaceRead}, reason: "no_matching_grant"},
		{name: "empty action", grant: baseGrant, req: AuthorizationRequest{Subjects: []SubjectRef{{Kind: SubjectUser, ID: "user-a"}}, Resource: ResourceRef{Kind: ResourcePath, Path: "docs/readme.md"}}, reason: "no_matching_grant"},
		{name: "resource kind mismatch", grant: baseGrant, req: AuthorizationRequest{Subjects: []SubjectRef{{Kind: SubjectUser, ID: "user-a"}}, Resource: ResourceRef{Kind: ResourceDatasource, Path: "docs/readme.md"}, Action: ActionWorkspaceRead}, reason: "no_matching_grant"},
		{name: "path mismatch", grant: baseGrant, req: AuthorizationRequest{Subjects: []SubjectRef{{Kind: SubjectUser, ID: "user-a"}}, Resource: ResourceRef{Kind: ResourcePath, Path: "src/main.go"}, Action: ActionWorkspaceRead}, reason: "no_matching_grant"},
		{name: "insufficient trust skips grant", grant: Grant{Subjects: baseGrant.Subjects, Resources: baseGrant.Resources, Actions: baseGrant.Actions, RequiredTrust: TrustPrivileged}, req: AuthorizationRequest{Subjects: []SubjectRef{{Kind: SubjectUser, ID: "user-a"}}, Trust: Trust{Level: TrustVerified}, Resource: ResourceRef{Kind: ResourcePath, Path: "docs/readme.md"}, Action: ActionWorkspaceRead}, reason: "no_matching_grant"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := EvaluateAuthorization(AuthorizationPolicy{Grants: []Grant{tt.grant}}, tt.req)
			if got.Decision != DecisionDeny || got.Reason != tt.reason {
				t.Fatalf("EvaluateAuthorization() = %#v, want deny %q", got, tt.reason)
			}
		})
	}
}

func TestEvaluateAuthorizationWildcardAndPathForms(t *testing.T) {
	policy := AuthorizationPolicy{Grants: []Grant{
		{Subjects: []SubjectRef{{Kind: SubjectUser, ID: "*"}}, Resources: []ResourceRef{{Kind: ResourcePath, Path: "docs/**"}}, Actions: []Action{"*"}},
		{Subjects: []SubjectRef{{Kind: SubjectUser, ID: "u"}}, Resources: []ResourceRef{{Kind: ResourcePath, Path: "README.md"}}, Actions: []Action{ActionWorkspaceRead}},
	}}
	for _, path := range []string{"docs", "docs/readme.md", "docs/sub/readme.md"} {
		got := EvaluateAuthorization(policy, AuthorizationRequest{Subjects: []SubjectRef{{Kind: SubjectUser, ID: "anyone"}}, Resource: ResourceRef{Kind: ResourcePath, Path: path}, Action: ActionWorkspaceWrite})
		if got.Decision != DecisionAllow {
			t.Fatalf("docs/** path %q decision = %#v, want allow", path, got)
		}
	}
	got := EvaluateAuthorization(policy, AuthorizationRequest{Subjects: []SubjectRef{{Kind: SubjectUser, ID: "u"}}, Resource: ResourceRef{Kind: ResourcePath, Path: "README.md"}, Action: ActionWorkspaceRead})
	if got.Decision != DecisionAllow {
		t.Fatalf("literal path decision = %#v, want allow", got)
	}
}
