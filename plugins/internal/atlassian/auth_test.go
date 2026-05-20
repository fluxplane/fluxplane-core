package atlassian

import (
	"context"
	"testing"

	"github.com/fluxplane/agentruntime/core/resource"
	coresecret "github.com/fluxplane/agentruntime/core/secret"
	runtimesecret "github.com/fluxplane/agentruntime/runtime/secret"
	"github.com/fluxplane/agentruntime/runtime/system"
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

func TestBaseURLUsesProductRESTPath(t *testing.T) {
	jira := Product{Name: "jira", ResourcePath: "jira"}
	if got := BaseURL(jira, "cloud-1"); got != "https://api.atlassian.com/ex/jira/cloud-1/rest/api/3" {
		t.Fatalf("jira base url = %q", got)
	}
	confluence := Product{Name: "confluence", ResourcePath: "confluence", RESTPath: "/wiki/api/v2"}
	if got := BaseURL(confluence, "cloud-1"); got != "https://api.atlassian.com/ex/confluence/cloud-1/wiki/api/v2" {
		t.Fatalf("confluence base url = %q", got)
	}
}

func TestResolveTokenDiscoversSiteURLForCloudID(t *testing.T) {
	product := Product{Name: "jira", DisplayName: "Jira Cloud", ResourcePath: "jira"}
	ref := resource.PluginRef{Name: "jira", Instance: "main"}
	network := &recordingNetwork{response: system.HTTPResponse{
		StatusCode: 200,
		Headers:    map[string][]string{"Content-Type": {"application/json"}},
		Body:       []byte(`[{"id":"cloud-1","url":"https://company.atlassian.net","name":"Company"}]`),
	}}
	session, err := Resolve(context.Background(), fakeSystem{
		network: network,
		env:     fakeEnvironment{values: map[string]string{"JIRA_TOKEN": "token"}},
	}, runtimesecret.NewFileStore(t.TempDir()), "jira", ref, product, Config{
		CloudID: "cloud-1",
		Auth:    AuthConfig{Method: TokenMethod},
	})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if session.SiteURL != "https://company.atlassian.net" || session.SiteName != "Company" {
		t.Fatalf("session site = %q/%q, want discovered site", session.SiteURL, session.SiteName)
	}
	if network.request.URL != accessibleResources {
		t.Fatalf("discovery URL = %q", network.request.URL)
	}
}

type fakeSystem struct {
	network system.Network
	env     system.Environment
}

func (s fakeSystem) Workspace() system.Workspace     { return nil }
func (s fakeSystem) Network() system.Network         { return s.network }
func (s fakeSystem) Process() system.ProcessManager  { return nil }
func (s fakeSystem) Browser() system.BrowserManager  { return nil }
func (s fakeSystem) Clarifier() system.Clarifier     { return nil }
func (s fakeSystem) Environment() system.Environment { return s.env }

type recordingNetwork struct {
	request  system.HTTPRequest
	response system.HTTPResponse
}

func (n *recordingNetwork) DoHTTP(_ context.Context, req system.HTTPRequest) (system.HTTPResponse, error) {
	n.request = req
	return n.response, nil
}

type fakeEnvironment struct {
	values map[string]string
}

func (e fakeEnvironment) Lookup(_ context.Context, key string) (string, bool, error) {
	value, ok := e.values[key]
	return value, ok, nil
}
