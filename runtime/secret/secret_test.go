package secret

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/fluxplane/fluxplane-core/core/policy"
	coresecret "github.com/fluxplane/fluxplane-core/core/secret"
)

func TestEnvResolverFindsEnvSecret(t *testing.T) {
	resolver := EnvResolver{Environment: mapEnvironment{"GITLAB_PERSONAL_ACCESS_TOKEN": "pat"}}
	material, ok, err := resolver.ResolveSecret(context.Background(), coresecret.Env("GITLAB_PERSONAL_ACCESS_TOKEN"))
	if err != nil || !ok || material.Value != "pat" {
		t.Fatalf("ResolveSecret = %#v, %v, %v; want pat", material, ok, err)
	}
}

func TestBrokerMintAndResolveScopedPlaceholder(t *testing.T) {
	broker := NewBroker(EnvResolver{Environment: mapEnvironment{"GITLAB_PERSONAL_ACCESS_TOKEN": "pat"}})
	ctx := ContextWithScope(authorizedContext(), Scope{Session: "s1", Turn: "t1"})
	placeholder, ok, err := broker.Mint(ctx, coresecret.Env("GITLAB_PERSONAL_ACCESS_TOKEN"))
	if err != nil || !ok {
		t.Fatalf("Mint = %q, %v, %v", placeholder, ok, err)
	}
	handle, ok := coresecret.ParsePlaceholder(string(placeholder))
	if !ok {
		t.Fatalf("placeholder = %q", placeholder)
	}
	material, ok, err := broker.ResolveHandle(ctx, handle)
	if err != nil || !ok || material.Value != "pat" {
		t.Fatalf("ResolveHandle = %#v, %v, %v; want pat", material, ok, err)
	}
	_, _, err = broker.ResolveHandle(ContextWithScope(authorizedContext(), Scope{Session: "other", Turn: "t1"}), handle)
	if err == nil || !strings.Contains(err.Error(), "scope mismatch") {
		t.Fatalf("ResolveHandle wrong scope error = %v, want scope mismatch", err)
	}
}

func TestBrokerRequiresSecretUse(t *testing.T) {
	broker := NewBroker(EnvResolver{Environment: mapEnvironment{"GITLAB_PERSONAL_ACCESS_TOKEN": "pat"}})
	ctx := policy.ContextWithAuthorization(context.Background(), policy.AuthorizationContext{
		Subjects: []policy.SubjectRef{{Kind: policy.SubjectUser, ID: "timo@localhost"}},
		Trust:    policy.Trust{Kind: policy.TrustInvocation, Level: policy.TrustPrivileged},
		Policy: policy.AuthorizationPolicy{Grants: []policy.Grant{{
			Subjects:      []policy.SubjectRef{{Kind: policy.SubjectUser, ID: "other"}},
			Resources:     []policy.ResourceRef{{Kind: policy.ResourceSecret, Name: "*"}},
			Actions:       []policy.Action{policy.ActionSecretUse},
			RequiredTrust: policy.TrustPrivileged,
		}}},
	})
	_, _, err := broker.Use(ctx, coresecret.Env("GITLAB_PERSONAL_ACCESS_TOKEN"))
	if err == nil || !strings.Contains(err.Error(), "authorization_deny") {
		t.Fatalf("Use error = %v, want authorization deny", err)
	}
}

func TestBrokerUseFirstSkipsMissingCandidateBeforeAuthorization(t *testing.T) {
	broker := NewBroker(EnvResolver{Environment: mapEnvironment{"GITLAB_TOKEN": "fallback"}})
	ctx := policy.ContextWithAuthorization(context.Background(), policy.AuthorizationContext{
		Subjects: []policy.SubjectRef{{Kind: policy.SubjectUser, ID: "timo@localhost"}},
		Trust:    policy.Trust{Kind: policy.TrustInvocation, Level: policy.TrustPrivileged},
		Policy: policy.AuthorizationPolicy{Grants: []policy.Grant{{
			Subjects:      []policy.SubjectRef{{Kind: policy.SubjectUser, ID: "timo@localhost"}},
			Resources:     []policy.ResourceRef{{Kind: policy.ResourceSecret, Name: "env/GITLAB_TOKEN"}},
			Actions:       []policy.Action{policy.ActionSecretUse},
			RequiredTrust: policy.TrustPrivileged,
		}}},
	})
	ref, material, ok, err := broker.UseFirst(ctx, coresecret.Env("GITLAB_PERSONAL_ACCESS_TOKEN"), coresecret.Env("GITLAB_TOKEN"))
	if err != nil || !ok || ref.Name != "GITLAB_TOKEN" || material.Value != "fallback" {
		t.Fatalf("UseFirst = %#v, %#v, %v, %v; want fallback", ref, material, ok, err)
	}
}

func TestBrokerUseAvailableAuthorizesLogicalPluginSecret(t *testing.T) {
	broker := NewBroker(EnvResolver{Environment: mapEnvironment{"GITLAB_PERSONAL_ACCESS_TOKEN": "pat"}})
	ctx := policy.ContextWithAuthorization(context.Background(), policy.AuthorizationContext{
		Subjects: []policy.SubjectRef{{Kind: policy.SubjectUser, ID: "timo@localhost"}},
		Trust:    policy.Trust{Kind: policy.TrustInvocation, Level: policy.TrustPrivileged},
		Policy: policy.AuthorizationPolicy{Grants: []policy.Grant{{
			Subjects:      []policy.SubjectRef{{Kind: policy.SubjectUser, ID: "timo@localhost"}},
			Resources:     []policy.ResourceRef{{Kind: policy.ResourceSecret, Name: "plugin/gitlab/company-a/access_token"}},
			Actions:       []policy.Action{policy.ActionSecretUse},
			RequiredTrust: policy.TrustPrivileged,
		}}},
	})
	resolution, ok, err := broker.UseAvailable(ctx, coresecret.AuthRequest{
		Plugin:   "gitlab",
		Instance: "company-a",
		Purpose:  "access_token",
		Methods: []coresecret.AuthMethodSpec{{
			Name:   "personal_access_token",
			Method: coresecret.AuthMethodEnv,
			Kind:   coresecret.KindAPIKey,
			Env:    coresecret.EnvSpec{Name: "GITLAB_PERSONAL_ACCESS_TOKEN", Aliases: []string{"GITLAB_TOKEN"}},
		}},
	})
	if err != nil || !ok || resolution.Ref.ResourceName() != "env/GITLAB_PERSONAL_ACCESS_TOKEN" || resolution.Material.Value != "pat" {
		t.Fatalf("UseAvailable = %#v, %v, %v; want configured env token", resolution, ok, err)
	}
}

func TestBrokerUseAvailableConfiguredEnvDoesNotProbeAliases(t *testing.T) {
	broker := NewBroker(EnvResolver{Environment: mapEnvironment{"GITLAB_TOKEN": "fallback"}})
	_, ok, err := broker.UseAvailable(authorizedPluginContext(), coresecret.AuthRequest{
		Plugin:   "gitlab",
		Instance: "company-a",
		Purpose:  "access_token",
		Methods: []coresecret.AuthMethodSpec{{
			Name:   "personal_access_token",
			Method: coresecret.AuthMethodEnv,
			Kind:   coresecret.KindAPIKey,
			Env:    coresecret.EnvSpec{Name: "GITLAB_PERSONAL_ACCESS_TOKEN", Aliases: []string{"GITLAB_TOKEN"}},
		}},
	})
	if err != nil || ok {
		t.Fatalf("UseAvailable configured env aliases = %v, %v; want no material", ok, err)
	}
}

func TestBrokerUseAvailableProbesAliasesWhenEnvNameUnset(t *testing.T) {
	broker := NewBroker(EnvResolver{Environment: mapEnvironment{"GITLAB_TOKEN": "fallback"}})
	resolution, ok, err := broker.UseAvailable(authorizedPluginContext(), coresecret.AuthRequest{
		Plugin:   "gitlab",
		Instance: "company-a",
		Purpose:  "access_token",
		Methods: []coresecret.AuthMethodSpec{{
			Name:   "personal_access_token",
			Method: coresecret.AuthMethodEnv,
			Kind:   coresecret.KindAPIKey,
			Env:    coresecret.EnvSpec{Aliases: []string{"GITLAB_TOKEN"}},
		}},
	})
	if err != nil || !ok || resolution.Ref.ResourceName() != "env/GITLAB_TOKEN" || resolution.Material.Value != "fallback" {
		t.Fatalf("UseAvailable alias probe = %#v, %v, %v; want fallback", resolution, ok, err)
	}
}

func TestBrokerExpiresHandles(t *testing.T) {
	broker := NewBroker(EnvResolver{Environment: mapEnvironment{"GITLAB_PERSONAL_ACCESS_TOKEN": "pat"}}).WithTTL(time.Nanosecond)
	now := time.Now()
	broker.now = func() time.Time { return now }
	ctx := ContextWithScope(authorizedContext(), Scope{Session: "s1", Turn: "t1"})
	placeholder, ok, err := broker.Mint(ctx, coresecret.Env("GITLAB_PERSONAL_ACCESS_TOKEN"))
	if err != nil || !ok {
		t.Fatalf("Mint = %q, %v, %v", placeholder, ok, err)
	}
	handle, _ := coresecret.ParsePlaceholder(string(placeholder))
	now = now.Add(time.Second)
	_, ok, err = broker.ResolveHandle(ctx, handle)
	if err != nil || ok {
		t.Fatalf("ResolveHandle expired = %v, %v; want not found nil", ok, err)
	}
}

func authorizedPluginContext() context.Context {
	return policy.ContextWithAuthorization(context.Background(), policy.AuthorizationContext{
		Subjects: []policy.SubjectRef{{Kind: policy.SubjectUser, ID: "timo@localhost"}},
		Trust:    policy.Trust{Kind: policy.TrustInvocation, Level: policy.TrustPrivileged},
		Policy: policy.AuthorizationPolicy{Grants: []policy.Grant{{
			Subjects:      []policy.SubjectRef{{Kind: policy.SubjectUser, ID: "timo@localhost"}},
			Resources:     []policy.ResourceRef{{Kind: policy.ResourceSecret, Name: "plugin/gitlab/company-a/access_token"}},
			Actions:       []policy.Action{policy.ActionSecretUse},
			RequiredTrust: policy.TrustPrivileged,
		}}},
	})
}

func authorizedContext() context.Context {
	return policy.ContextWithAuthorization(context.Background(), policy.AuthorizationContext{
		Subjects: []policy.SubjectRef{{Kind: policy.SubjectUser, ID: "timo@localhost"}},
		Trust:    policy.Trust{Kind: policy.TrustInvocation, Level: policy.TrustPrivileged},
		Policy: policy.AuthorizationPolicy{Grants: []policy.Grant{{
			Subjects:      []policy.SubjectRef{{Kind: policy.SubjectUser, ID: "timo@localhost"}},
			Resources:     []policy.ResourceRef{{Kind: policy.ResourceSecret, Name: "env/GITLAB_PERSONAL_ACCESS_TOKEN"}},
			Actions:       []policy.Action{policy.ActionSecretUse},
			RequiredTrust: policy.TrustPrivileged,
		}}},
	})
}

type mapEnvironment map[string]string

func (e mapEnvironment) Lookup(_ context.Context, key string) (string, bool, error) {
	value, ok := e[key]
	return value, ok, nil
}

// captureCtxEnv records the context that Lookup was called with so tests
// can verify that EnvResolver propagates the caller's ctx instead of
// substituting context.Background().
type captureCtxEnv struct {
	value      string
	gotContext context.Context
}

func (e *captureCtxEnv) Lookup(ctx context.Context, _ string) (string, bool, error) {
	e.gotContext = ctx
	return e.value, e.value != "", nil
}

// TestEnvResolverPropagatesContext regresses a bug where
// EnvResolver.ResolveSecret used `_ context.Context` (ignoring it) and
// then called r.Environment.Lookup(context.Background(), ...). Any
// caller-supplied deadline / cancellation / metadata key on the
// resolver context was silently dropped.
func TestEnvResolverPropagatesContext(t *testing.T) {
	env := &captureCtxEnv{value: "pat"}
	resolver := EnvResolver{Environment: env}
	type ctxKey struct{}
	ctx := context.WithValue(context.Background(), ctxKey{}, "marker")

	if _, _, err := resolver.ResolveSecret(ctx, coresecret.Env("TOKEN")); err != nil {
		t.Fatalf("ResolveSecret: %v", err)
	}
	if env.gotContext == nil {
		t.Fatal("Environment.Lookup got nil context")
	}
	if got, _ := env.gotContext.Value(ctxKey{}).(string); got != "marker" {
		t.Fatalf("Environment.Lookup got ctx without caller value (was %q), want \"marker\" - resolver is substituting context.Background()", got)
	}
}
