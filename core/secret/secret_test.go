package secret

import (
	"errors"
	"reflect"
	"testing"
)

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
	if ref.Scheme != SchemeKubernetes || ref.Plugin != "latest" || ref.Instance != "backend-db" || ref.Slot != "dsn" {
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

func TestRefResourceNameForms(t *testing.T) {
	tests := []struct {
		name string
		ref  Ref
		want string
	}{
		{name: "empty env", ref: Ref{Scheme: SchemeEnv}, want: "env/*"},
		{name: "plugin no instance", ref: Plugin(" gitlab ", " ", " token "), want: "plugin/gitlab/token"},
		{name: "kubernetes trims empty", ref: Kubernetes(" ns ", " secret ", " key "), want: "kubernetes/ns/secret/key"},
		{name: "custom", ref: Ref{Scheme: " custom ", Plugin: " plug ", Instance: " inst ", Slot: " name "}, want: "custom/plug/inst/name"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.ref.ResourceName(); got != tt.want {
				t.Fatalf("ResourceName() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestParseRefForms(t *testing.T) {
	tests := []struct {
		value string
		want  Ref
	}{
		{value: "", want: Ref{}},
		{value: " / ", want: Ref{}},
		{value: "env", want: Ref{Scheme: SchemeEnv}},
		{value: "env/GITLAB/TOKEN", want: Env("GITLAB/TOKEN")},
		{value: "plugin/gitlab/company/token/with/slashes", want: Plugin("gitlab", "company", "token/with/slashes")},
		{value: "kubernetes/ns/secret/key/with/slashes", want: Kubernetes("ns", "secret", "key/with/slashes")},
		{value: "vault/path/to/secret", want: Ref{Scheme: "vault", Slot: "path/to/secret"}},
	}
	for _, tt := range tests {
		t.Run(tt.value, func(t *testing.T) {
			if got := ParseRef(tt.value); !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("ParseRef() = %#v, want %#v", got, tt.want)
			}
		})
	}
}

func TestAuthRequestNormalize(t *testing.T) {
	got := (AuthRequest{Plugin: " gitlab ", Instance: " main ", Purpose: " token "}).Normalize()
	want := AuthRequest{Plugin: "gitlab", Instance: "main", Purpose: "token"}
	if got.Plugin != want.Plugin || got.Instance != want.Instance || got.Purpose != want.Purpose {
		t.Fatalf("Normalize() = %#v, want %#v", got, want)
	}
}

func TestPlaceholderEdgeCases(t *testing.T) {
	if got := string(PlaceholderFor(" handle ")); got != "${secret:handle}" {
		t.Fatalf("PlaceholderFor() = %q", got)
	}
	for _, value := range []string{"", "prefix ${secret:abc}", "${secret:bad handle}"} {
		if handle, ok := ParsePlaceholder(value); ok || handle != "" {
			t.Fatalf("ParsePlaceholder(%q) = %q, %v; want empty, false", value, handle, ok)
		}
	}
	unchanged, err := ReplacePlaceholders("${secret:abc}", nil)
	if err != nil || unchanged != "${secret:abc}" {
		t.Fatalf("ReplacePlaceholders nil = %q, %v", unchanged, err)
	}
	boom := errors.New("boom")
	if _, err := ReplacePlaceholders("${secret:abc} ${secret:def}", func(string) (string, error) { return "", boom }); !errors.Is(err, boom) {
		t.Fatalf("ReplacePlaceholders error = %v, want boom", err)
	}
}

func TestValidateAuthMethodRejectsInvalidSpecs(t *testing.T) {
	tests := []struct {
		name string
		spec AuthMethodSpec
		want string
	}{
		{name: "empty name", spec: AuthMethodSpec{}, want: "auth method name is empty"},
		{name: "empty method", spec: AuthMethodSpec{Name: "token"}, want: `auth method "token" method is empty`},
		{name: "env empty", spec: AuthMethodSpec{Name: "token", Method: AuthMethodEnv, Kind: KindAPIKey}, want: `auth method "token" env config is empty`},
		{name: "oauth authorize empty", spec: AuthMethodSpec{Name: "oauth", Method: AuthMethodOAuth2, Kind: KindOAuth2Token, OAuth2: OAuth2Spec{TokenURL: "https://example/token"}, Secret: Env("TOKEN")}, want: `auth method "oauth" oauth2 authorize_url is empty`},
		{name: "oauth token empty", spec: AuthMethodSpec{Name: "oauth", Method: AuthMethodOAuth2, Kind: KindOAuth2Token, OAuth2: OAuth2Spec{AuthorizeURL: "https://example/auth"}, Secret: Env("TOKEN")}, want: `auth method "oauth" oauth2 token_url is empty`},
		{name: "stored secret empty", spec: AuthMethodSpec{Name: "stored", Method: AuthMethodStored, Kind: KindAPIKey}, want: `auth method "stored" secret ref or setup_fields is required`},
		{name: "unsupported", spec: AuthMethodSpec{Name: "webauthn", Method: "webauthn", Kind: KindAPIKey}, want: `auth method "webauthn" method "webauthn" is unsupported`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateAuthMethod(tt.spec)
			if err == nil || err.Error() != tt.want {
				t.Fatalf("ValidateAuthMethod() error = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestValidateAuthMethodStored(t *testing.T) {
	err := ValidateAuthMethod(AuthMethodSpec{Name: "stored", Method: AuthMethodStored, Kind: KindAPIKey, Secret: Env("TOKEN")})
	if err != nil {
		t.Fatalf("ValidateAuthMethod stored: %v", err)
	}
}
