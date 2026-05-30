package atlassian

import (
	"context"
	"encoding/base64"
	"strings"
	"testing"

	"github.com/fluxplane/fluxplane-core/core/resource"
	coresecret "github.com/fluxplane/fluxplane-core/core/secret"
	runtimesecret "github.com/fluxplane/fluxplane-core/runtime/secret"
	"github.com/fluxplane/fluxplane-core/runtime/system"
	"github.com/fluxplane/fluxplane-system/systemkit"
	fpsystemtest "github.com/fluxplane/fluxplane-system/systemtest"
)

func TestAuthMethodsDeclareTokenAPITokenAndOAuth(t *testing.T) {
	product := Product{Name: "jira", DisplayName: "Jira Cloud", ResourcePath: "jira", Scopes: []string{"read:jira-work", "offline_access"}}
	methods := AuthMethods("jira", resource.PluginRef{Name: "jira", Instance: "main"}, product, Config{})
	if len(methods) != 3 {
		t.Fatalf("methods len = %d, want 3", len(methods))
	}
	if methods[0].Name != TokenMethod || methods[0].Method != coresecret.AuthMethodEnv {
		t.Fatalf("token method = %#v", methods[0])
	}
	if methods[1].Name != APITokenMethod || methods[1].Method != coresecret.AuthMethodStored || methods[1].Kind != coresecret.KindBasic {
		t.Fatalf("api token method = %#v", methods[1])
	}
	if len(methods[1].SetupFields) != 5 || methods[1].SetupFields[0].Name != apiEmailField || methods[1].SetupFields[1].Name != apiTokenField {
		t.Fatalf("api token setup fields = %#v", methods[1].SetupFields)
	}
	if methods[1].SetupFields[2].RequiredGroup != "" {
		t.Fatalf("api token cloud_id field = %#v, want optional metadata field", methods[1].SetupFields[2])
	}
	for _, field := range methods[1].SetupFields[3:] {
		if field.RequiredGroup != "site_locator" {
			t.Fatalf("api token locator field = %#v, want required site_locator group", field)
		}
	}
	if methods[2].Name != OAuth2Method || methods[2].Secret.ResourceName() != "plugin/jira/main/oauth2_token" {
		t.Fatalf("oauth method = %#v", methods[2])
	}
	if len(methods[2].SetupFields) != 4 {
		t.Fatalf("oauth setup fields len = %d, want 4", len(methods[2].SetupFields))
	}
	if methods[2].OAuth2.ExtraParams["audience"] != "api.atlassian.com" {
		t.Fatalf("oauth extra params = %#v", methods[2].OAuth2.ExtraParams)
	}
}

func TestStoreOAuthTokenPersistsSiteMetadataAndRefreshSecret(t *testing.T) {
	store := runtimesecret.NewFileStore(t.TempDir())
	product := Product{Name: "jira", DisplayName: "Jira Cloud", ResourcePath: "jira"}
	ref := resource.PluginRef{Name: "jira", Instance: "main"}
	err := StoreOAuthToken(context.Background(), store, "jira", ref, product, OAuthToken{AccessToken: "access", RefreshToken: "refresh"}, Site{
		ID:   "cloud-1",
		URL:  "https://example.atlassian.invalid",
		Name: "Company",
	})
	if err != nil {
		t.Fatalf("StoreOAuthToken: %v", err)
	}
	stored, ok, err := store.LoadSecret(context.Background(), coresecret.Plugin("jira", "main", "oauth2_token"))
	if err != nil || !ok {
		t.Fatalf("LoadSecret = %#v, %v, %v; want stored", stored, ok, err)
	}
	if stored.Metadata["cloud_id"] != "cloud-1" || stored.Metadata["site_url"] != "https://example.atlassian.invalid" {
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
	network := &recordingNetwork{response: systemkit.HTTPResponse{
		StatusCode: 200,
		Headers:    map[string][]string{"Content-Type": {"application/json"}},
		Body:       []byte(`[{"id":"cloud-1","url":"https://example.atlassian.invalid","name":"Company"}]`),
	}}
	session, err := ResolveWithResolver(context.Background(), fakeSystem{
		network: network,
	}, runtimesecret.NewFileStore(t.TempDir()), runtimesecret.EnvResolver{Environment: fakeEnvironment{values: map[string]string{"JIRA_TOKEN": "token"}}}, "jira", ref, product, Config{
		CloudID: "cloud-1",
		Auth:    AuthConfig{Method: TokenMethod},
	})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if session.SiteURL != "https://example.atlassian.invalid" || session.SiteName != "Company" {
		t.Fatalf("session site = %q/%q, want discovered site", session.SiteURL, session.SiteName)
	}
	if network.request.URL != accessibleResources {
		t.Fatalf("discovery URL = %q", network.request.URL)
	}
}

func TestResolveAPITokenUsesGenericAtlassianEnv(t *testing.T) {
	product := Product{Name: "jira", DisplayName: "Jira Cloud", ResourcePath: "jira"}
	ref := resource.PluginRef{Name: "jira", Instance: "main"}
	session, err := ResolveWithResolver(context.Background(), fakeSystem{}, runtimesecret.NewFileStore(t.TempDir()), runtimesecret.EnvResolver{Environment: fakeEnvironment{values: map[string]string{
		"ATLASSIAN_API_TOKEN": "api-token",
		"ATLASSIAN_EMAIL":     "user@example.invalid",
		"ATLASSIAN_CLOUD_ID":  "cloud-1",
		"ATLASSIAN_SITE_URL":  "https://example.atlassian.invalid",
	}}}, "jira", ref, product, Config{
		Auth: AuthConfig{Method: APITokenMethod},
	})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	want := "Basic " + base64.StdEncoding.EncodeToString([]byte("user@example.invalid:api-token"))
	if session.Method != APITokenMethod || session.Authorization != want || session.CloudID != "cloud-1" || session.BaseURL != "https://example.atlassian.invalid/rest/api/3" {
		t.Fatalf("session = %#v, want api token basic auth", session)
	}
}

func TestResolveAPITokenRejectsCloudIDWithoutSiteURL(t *testing.T) {
	product := Product{Name: "jira", DisplayName: "Jira Cloud", ResourcePath: "jira"}
	ref := resource.PluginRef{Name: "jira", Instance: "main"}
	_, err := ResolveWithResolver(context.Background(), fakeSystem{}, runtimesecret.NewFileStore(t.TempDir()), runtimesecret.EnvResolver{Environment: fakeEnvironment{values: map[string]string{
		"ATLASSIAN_API_TOKEN": "api-token",
		"ATLASSIAN_EMAIL":     "user@example.invalid",
		"ATLASSIAN_CLOUD_ID":  "atlassian-cloud",
	}}}, "jira", ref, product, Config{
		Auth: AuthConfig{Method: APITokenMethod},
	})
	if err == nil || !strings.Contains(err.Error(), "site_url or base_url is required") {
		t.Fatalf("Resolve error = %v, want site_url/base_url requirement", err)
	}
}

func TestResolveWithResolverUsesCLIResolverForAPIToken(t *testing.T) {
	product := Product{Name: "jira", DisplayName: "Jira Cloud", ResourcePath: "jira"}
	ref := resource.PluginRef{Name: "jira", Instance: "main"}
	session, err := ResolveWithResolver(context.Background(), fakeSystem{
		env: fakeEnvironment{},
	}, runtimesecret.NewFileStore(t.TempDir()), runtimesecret.EnvResolver{Environment: fakeEnvironment{values: map[string]string{
		"ATLASSIAN_API_TOKEN": "api-token",
		"ATLASSIAN_EMAIL":     "user@example.invalid",
		"ATLASSIAN_CLOUD_ID":  "cloud-1",
		"ATLASSIAN_SITE_URL":  "https://example.atlassian.invalid",
	}}}, "jira", ref, product, Config{
		Auth: AuthConfig{Method: APITokenMethod},
	})
	if err != nil {
		t.Fatalf("ResolveWithResolver: %v", err)
	}
	want := "Basic " + base64.StdEncoding.EncodeToString([]byte("user@example.invalid:api-token"))
	if session.Authorization != want {
		t.Fatalf("authorization = %q, want CLI resolver credentials", session.Authorization)
	}
}

func TestResolveAPITokenPrefersProductEnv(t *testing.T) {
	product := Product{Name: "jira", DisplayName: "Jira Cloud", ResourcePath: "jira"}
	ref := resource.PluginRef{Name: "jira", Instance: "main"}
	session, err := ResolveWithResolver(context.Background(), fakeSystem{}, runtimesecret.NewFileStore(t.TempDir()), runtimesecret.EnvResolver{Environment: fakeEnvironment{values: map[string]string{
		"JIRA_API_TOKEN":      "jira-token",
		"JIRA_EMAIL":          "jira@example.com",
		"ATLASSIAN_API_TOKEN": "atlassian-token",
		"ATLASSIAN_EMAIL":     "atlassian@example.invalid",
	}}}, "jira", ref, product, Config{
		SiteURL: "https://example.atlassian.invalid",
		Auth:    AuthConfig{Method: APITokenMethod},
	})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	want := "Basic " + base64.StdEncoding.EncodeToString([]byte("jira@example.com:jira-token"))
	if session.Authorization != want || session.BaseURL != "https://example.atlassian.invalid/rest/api/3" {
		t.Fatalf("session = %#v, want product-specific env", session)
	}
}

func TestResolveAPITokenUsesStoredFields(t *testing.T) {
	product := Product{Name: "confluence", DisplayName: "Confluence Cloud", ResourcePath: "confluence", RESTPath: "/wiki/api/v2"}
	ref := resource.PluginRef{Name: "confluence", Instance: "main"}
	store := runtimesecret.NewFileStore(t.TempDir())
	if err := store.SaveSecret(context.Background(), runtimesecret.StoredSecret{Ref: coresecret.Plugin("confluence", "main", apiEmailField), Value: "stored@example.com"}); err != nil {
		t.Fatalf("SaveSecret email: %v", err)
	}
	if err := store.SaveSecret(context.Background(), runtimesecret.StoredSecret{Ref: coresecret.Plugin("confluence", "main", apiTokenField), Value: "stored-token"}); err != nil {
		t.Fatalf("SaveSecret token: %v", err)
	}
	session, err := Resolve(context.Background(), fakeSystem{env: fakeEnvironment{}}, store, "confluence", ref, product, Config{
		SiteURL: "https://example.atlassian.invalid",
		Auth:    AuthConfig{Method: APITokenMethod},
	})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	want := "Basic " + base64.StdEncoding.EncodeToString([]byte("stored@example.com:stored-token"))
	if session.Authorization != want || session.BaseURL != "https://example.atlassian.invalid/wiki/api/v2" {
		t.Fatalf("session = %#v, want stored api token fields", session)
	}
}

func TestResolveLegacyTokenKeepsBearerForSlackBotShape(t *testing.T) {
	product := Product{Name: "jira", DisplayName: "Jira Cloud", ResourcePath: "jira"}
	ref := resource.PluginRef{Name: "jira", Instance: "main"}
	session, err := ResolveWithResolver(context.Background(), fakeSystem{}, runtimesecret.NewFileStore(t.TempDir()), runtimesecret.EnvResolver{Environment: fakeEnvironment{values: map[string]string{"JIRA_API_TOKEN": "service-token"}}}, "jira", ref, product, Config{
		CloudID: "cloud-1",
		Auth: AuthConfig{
			Method:   TokenMethod,
			TokenEnv: "JIRA_API_TOKEN",
			Email:    "service-bot@example.invalid",
		},
	})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if session.Authorization != "" || session.Token != "service-token" || session.Method != TokenMethod {
		t.Fatalf("session = %#v, want legacy bearer token", session)
	}
}

type fakeSystem struct {
	network system.Network
	env     system.Environment
}

func (s fakeSystem) Workspace() system.Workspace     { return nil }
func (s fakeSystem) Network() system.Network         { return s.network }
func (s fakeSystem) Process() system.ProcessManager  { return nil }
func (s fakeSystem) Environment() system.Environment { return s.env }

type recordingNetwork struct {
	fpsystemtest.UnsupportedNetwork
	request  systemkit.HTTPRequest
	response systemkit.HTTPResponse
}

func (n *recordingNetwork) DoHTTP(_ context.Context, req systemkit.HTTPRequest) (systemkit.HTTPResponse, error) {
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
