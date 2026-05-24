package authstatus

import (
	"context"
	"testing"

	coreevidence "github.com/fluxplane/fluxplane-core/core/evidence"
	"github.com/fluxplane/fluxplane-core/core/resource"
	coresecret "github.com/fluxplane/fluxplane-core/core/secret"
	runtimeevidence "github.com/fluxplane/fluxplane-core/runtime/evidence"
	runtimesecret "github.com/fluxplane/fluxplane-core/runtime/secret"
)

func TestEvaluateUsesRequiredGroupSetupField(t *testing.T) {
	store := runtimesecret.NewFileStore(t.TempDir())
	ref := resource.PluginRef{Name: "slack", Instance: "work"}
	if err := store.SaveSecret(context.Background(), runtimesecret.StoredSecret{
		Ref:   coresecret.Plugin("slack", "work", "user_token"),
		Kind:  coresecret.KindBearerToken,
		Value: "slack-user-token",
	}); err != nil {
		t.Fatalf("SaveSecret: %v", err)
	}
	status := Evaluate(context.Background(), store, Target{
		Ref: ref,
		Methods: []coresecret.AuthMethodSpec{{
			Name:   "token",
			Method: coresecret.AuthMethodStored,
			Kind:   coresecret.KindBearerToken,
			SetupFields: []coresecret.SetupFieldSpec{
				{Name: "bot_token", RequiredGroup: "api_token"},
				{Name: "user_token", RequiredGroup: "api_token"},
			},
		}},
	})
	if !status.Connected || status.Method != "token" {
		t.Fatalf("status = %#v", status)
	}
}

func TestEvaluateReportsPartialRequiredGroupFields(t *testing.T) {
	store := runtimesecret.NewFileStore(t.TempDir())
	ref := resource.PluginRef{Name: "jira", Instance: "work"}
	for _, secret := range []runtimesecret.StoredSecret{
		{Ref: coresecret.Plugin("jira", "work", "email"), Kind: coresecret.KindBasic, Value: "user@example.invalid"},
		{Ref: coresecret.Plugin("jira", "work", "token"), Kind: coresecret.KindBasic, Value: "api-token"},
	} {
		if err := store.SaveSecret(context.Background(), secret); err != nil {
			t.Fatalf("SaveSecret: %v", err)
		}
	}
	status := Evaluate(context.Background(), store, Target{
		Ref: ref,
		Methods: []coresecret.AuthMethodSpec{{
			Name:   "api_token",
			Method: coresecret.AuthMethodStored,
			Kind:   coresecret.KindBasic,
			SetupFields: []coresecret.SetupFieldSpec{
				{Name: "email", Required: true},
				{Name: "token", Required: true},
				{Name: "cloud_id"},
				{Name: "site_url", RequiredGroup: "site_locator"},
				{Name: "base_url", RequiredGroup: "site_locator"},
			},
		}},
	})
	if status.Connected || status.Method != "token" {
		t.Fatalf("status = %#v, want partial token readiness", status)
	}
	got := map[string]bool{}
	for _, field := range status.Fields {
		got[field.Name] = field.Set
	}
	if !got["email"] || !got["token"] || got["cloud_id"] || got["site_url"] || got["base_url"] {
		t.Fatalf("fields = %#v", status.Fields)
	}
}

func TestEvaluateUsesEnvironmentAlias(t *testing.T) {
	env := fakeEnvironment{values: map[string]string{"GITLAB_TOKEN": "glpat-test"}}
	status := Evaluate(context.Background(), runtimesecret.EnvResolver{Environment: env}, Target{
		Ref: resource.PluginRef{Name: "gitlab", Instance: "gitlab"},
		Methods: []coresecret.AuthMethodSpec{{
			Name:   "personal_access_token",
			Method: coresecret.AuthMethodEnv,
			Kind:   coresecret.KindAPIKey,
			Env:    coresecret.EnvSpec{Aliases: []string{"GITLAB_TOKEN"}},
		}},
	})
	if !status.Connected || status.Method != "token" {
		t.Fatalf("status = %#v", status)
	}
}

func TestAssertionDeriverEmitsAuthenticatedOnlyWhenConnected(t *testing.T) {
	deriver := NewAssertionDeriver()
	assertions, err := deriver.Derive(context.Background(), runtimeevidence.AssertionDeriveRequest{
		Observations: []coreevidence.Observation{{
			ID:      "auth:gitlab:work",
			Kind:    ObservationKind,
			Content: Status{Plugin: "gitlab", Instance: "work", Status: StatusConnected, Connected: true, Method: "token"},
		}, {
			ID:      "auth:slack:slack",
			Kind:    ObservationKind,
			Content: Status{Plugin: "slack", Instance: "slack", Status: StatusNotConnected},
		}},
	})
	if err != nil {
		t.Fatalf("Derive: %v", err)
	}
	if len(assertions) != 1 {
		t.Fatalf("assertions = %#v", assertions)
	}
	assertion := assertions[0]
	if assertion.Kind != AssertionAuthenticated || assertion.Target != "gitlab" || assertion.Subject.ID != "gitlab/work" || assertion.Metadata["method"] != "token" {
		t.Fatalf("assertion = %#v", assertion)
	}
}

type fakeEnvironment struct {
	values map[string]string
}

func (e fakeEnvironment) Lookup(_ context.Context, key string) (string, bool, error) {
	value, ok := e.values[key]
	return value, ok, nil
}
