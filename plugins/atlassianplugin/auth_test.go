package atlassianplugin

import (
	"context"
	"testing"

	"github.com/fluxplane/agentruntime/core/resource"
	coresecret "github.com/fluxplane/agentruntime/core/secret"
	runtimesecret "github.com/fluxplane/agentruntime/runtime/secret"
)

func TestAuthMethodsDeclareTokenAndOAuth(t *testing.T) {
	product := Product{Name: "jira", DisplayName: "Jira Cloud", ResourcePath: "jira", Scopes: []string{"read:jira-work", "offline_access"}}
	methods := AuthMethods("jira", resource.PluginRef{Name: "jira", Instance: "main"}, product, Config{})
	if len(methods) != 2 {
		t.Fatalf("methods len = %d, want 2", len(methods))
	}
	if methods[0].Name != TokenMethod || methods[0].Method != coresecret.AuthMethodEnv {
		t.Fatalf("token method = %#v", methods[0])
	}
	if methods[1].Name != OAuth2Method || methods[1].Secret.ResourceName() != "plugin/jira/main/oauth2_token" {
		t.Fatalf("oauth method = %#v", methods[1])
	}
	if len(methods[1].SetupFields) != 4 {
		t.Fatalf("oauth setup fields len = %d, want 4", len(methods[1].SetupFields))
	}
	if methods[1].OAuth2.ExtraParams["audience"] != "api.atlassian.com" {
		t.Fatalf("oauth extra params = %#v", methods[1].OAuth2.ExtraParams)
	}
}

func TestStoreOAuthTokenPersistsSiteMetadataAndRefreshSecret(t *testing.T) {
	store := runtimesecret.NewFileStore(t.TempDir())
	product := Product{Name: "jira", DisplayName: "Jira Cloud", ResourcePath: "jira"}
	ref := resource.PluginRef{Name: "jira", Instance: "main"}
	err := StoreOAuthToken(context.Background(), store, "jira", ref, product, OAuthToken{AccessToken: "access", RefreshToken: "refresh"}, Site{
		ID:   "cloud-1",
		URL:  "https://company.atlassian.net",
		Name: "Company",
	})
	if err != nil {
		t.Fatalf("StoreOAuthToken: %v", err)
	}
	stored, ok, err := store.LoadSecret(context.Background(), coresecret.Plugin("jira", "main", "oauth2_token"))
	if err != nil || !ok {
		t.Fatalf("LoadSecret = %#v, %v, %v; want stored", stored, ok, err)
	}
	if stored.Metadata["cloud_id"] != "cloud-1" || stored.Metadata["site_url"] != "https://company.atlassian.net" {
		t.Fatalf("metadata = %#v", stored.Metadata)
	}
	refresh, ok, err := store.LoadSecret(context.Background(), coresecret.Plugin("jira", "main", "oauth2_refresh_token"))
	if err != nil || !ok {
		t.Fatalf("LoadSecret refresh = %#v, %v, %v; want stored", refresh, ok, err)
	}
	if refresh.Value != "refresh" {
		t.Fatalf("refresh value = %q", refresh.Value)
	}
}
