package secret

import "testing"

func TestRefResourceName(t *testing.T) {
	if got := Env("GITLAB_PERSONAL_ACCESS_TOKEN").ResourceName(); got != "env/GITLAB_PERSONAL_ACCESS_TOKEN" {
		t.Fatalf("env resource = %q", got)
	}
	if got := Env("GITLAB_PERSONAL_ACCESS_TOKEN").Scheme; got != SchemeEnv {
		t.Fatalf("env scheme = %q, want %q", got, SchemeEnv)
	}
	if got := Plugin("gitlab", "company-a", "personal_access_token").ResourceName(); got != "plugin/gitlab/company-a/personal_access_token" {
		t.Fatalf("plugin resource = %q", got)
	}
	if got := Kubernetes("latest", "backend-db", "dsn").ResourceName(); got != "kubernetes/latest/backend-db/dsn" {
		t.Fatalf("kubernetes resource = %q", got)
	}
}

func TestParseRef(t *testing.T) {
	ref := ParseRef("kubernetes/latest/backend-db/dsn")
	if ref.Scheme != SchemeKubernetes || ref.Plugin != "latest" || ref.Instance != "backend-db" || ref.Name != "dsn" {
		t.Fatalf("ParseRef kubernetes = %#v", ref)
	}
}

func TestAuthRequestSecretRef(t *testing.T) {
	req := AuthRequest{Plugin: " gitlab ", Instance: " company-a ", Purpose: " access_token "}
	if got := req.SecretRef().ResourceName(); got != "plugin/gitlab/company-a/access_token" {
		t.Fatalf("secret ref = %q", got)
	}
}

func TestValidateAuthMethod(t *testing.T) {
	method := AuthMethodSpec{
		Name:   "personal_access_token",
		Method: AuthMethodEnv,
		Kind:   KindAPIKey,
		Env:    EnvSpec{Name: "GITLAB_PERSONAL_ACCESS_TOKEN", Aliases: []string{"GITLAB_TOKEN"}},
	}
	if err := ValidateAuthMethod(method); err != nil {
		t.Fatalf("ValidateAuthMethod env: %v", err)
	}
	method = AuthMethodSpec{
		Name:   "oauth2",
		Method: AuthMethodOAuth2,
		Kind:   KindOAuth2Token,
		Secret: Plugin("gitlab", "company-a", "oauth2_token"),
		OAuth2: OAuth2Spec{AuthorizeURL: "https://gitlab.example/oauth/authorize", TokenURL: "https://gitlab.example/oauth/token"},
	}
	if err := ValidateAuthMethod(method); err != nil {
		t.Fatalf("ValidateAuthMethod oauth2: %v", err)
	}
}

func TestPlaceholderParseReplaceAndRedact(t *testing.T) {
	placeholder := string(PlaceholderFor("abc123"))
	handle, ok := ParsePlaceholder(placeholder)
	if !ok || handle != "abc123" {
		t.Fatalf("ParsePlaceholder = %q, %v", handle, ok)
	}
	replaced, err := ReplacePlaceholders("Bearer "+placeholder, func(handle string) (string, error) {
		if handle != "abc123" {
			t.Fatalf("handle = %q", handle)
		}
		return "secret-token", nil
	})
	if err != nil {
		t.Fatalf("ReplacePlaceholders: %v", err)
	}
	if replaced != "Bearer secret-token" {
		t.Fatalf("replaced = %q", replaced)
	}
	if redacted := RedactPlaceholders("Bearer " + placeholder); redacted != "Bearer ${secret:redacted}" {
		t.Fatalf("redacted = %q", redacted)
	}
}
