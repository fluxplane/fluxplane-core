package confluence

import (
	"context"
	"encoding/base64"
	"strings"
	"testing"

	coredatasource "github.com/fluxplane/fluxplane-core/core/datasource"
	"github.com/fluxplane/fluxplane-core/core/resource"
	coresecret "github.com/fluxplane/fluxplane-core/core/secret"
	"github.com/fluxplane/fluxplane-core/orchestration/pluginhost"
	"github.com/fluxplane/fluxplane-core/plugins/internal/atlassian"
	runtimesecret "github.com/fluxplane/fluxplane-core/runtime/secret"
	"github.com/fluxplane/fluxplane-core/runtime/system"
)

func TestPluginContributesConfluenceDatasourceEntities(t *testing.T) {
	providers, err := New(nil).DatasourceProviders(context.Background(), pluginhost.Context{})
	if err != nil {
		t.Fatalf("DatasourceProviders: %v", err)
	}
	if len(providers) != 1 {
		t.Fatalf("providers len = %d, want 1", len(providers))
	}
	got := map[coredatasource.EntityType]bool{}
	for _, entity := range providers[0].Entities() {
		got[entity.Type] = true
	}
	for _, want := range []coredatasource.EntityType{PageEntity, SpaceEntity} {
		if !got[want] {
			t.Fatalf("entities = %#v, missing %s", got, want)
		}
	}
}

func TestPluginDeclaresOAuthAndTokenAuthMethods(t *testing.T) {
	methods, err := New(nil).AuthMethods(context.Background(), pluginhost.Context{Ref: resource.PluginRef{Name: Name, Instance: "main"}})
	if err != nil {
		t.Fatalf("AuthMethods: %v", err)
	}
	if len(methods) != 3 {
		t.Fatalf("methods len = %d, want 3", len(methods))
	}
	if methods[0].Name != atlassian.TokenMethod || methods[0].Method != coresecret.AuthMethodEnv {
		t.Fatalf("token method = %#v", methods[0])
	}
	if methods[1].Name != atlassian.APITokenMethod || methods[1].Method != coresecret.AuthMethodStored {
		t.Fatalf("api token method = %#v", methods[1])
	}
	if methods[2].Name != atlassian.OAuth2Method || methods[2].Secret.ResourceName() != "plugin/confluence/main/oauth2_token" {
		t.Fatalf("oauth method = %#v", methods[2])
	}
	if !contains(methods[2].OAuth2.Scopes, "read:page:confluence") || !contains(methods[2].OAuth2.Scopes, "read:space:confluence") {
		t.Fatalf("oauth scopes = %#v", methods[2].OAuth2.Scopes)
	}
}

func TestConfluenceDatasourceDefaultsToAllEntities(t *testing.T) {
	providers, err := New(nil).DatasourceProviders(context.Background(), pluginhost.Context{})
	if err != nil {
		t.Fatalf("DatasourceProviders: %v", err)
	}
	accessor, err := providers[0].Open(context.Background(), coredatasource.Spec{Name: "confluence", Kind: Name})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if len(accessor.Entities()) != 2 {
		t.Fatalf("entities len = %d, want 2", len(accessor.Entities()))
	}
	if _, ok := accessor.(coredatasource.CorpusProvider); !ok {
		t.Fatalf("accessor does not implement CorpusProvider")
	}
}

func TestPageListUsesConfluenceV1BaseAndTokenAuth(t *testing.T) {
	network := &recordingNetwork{response: system.HTTPResponse{
		StatusCode: 200,
		Headers:    map[string][]string{"Content-Type": {"application/json"}},
		Body:       []byte(`{"results":[{"id":"123","title":"Runbook","status":"current","space":{"id":42,"key":"ENG"},"version":{"number":7},"body":{"storage":{"value":"<p>Hello &amp; welcome</p>"}},"_links":{"webui":"/wiki/spaces/ENG/pages/123/Runbook"}}],"_links":{"next":"/wiki/rest/api/content?start=1"}}`),
	}}
	plugin := newTestPlugin(t, network, map[string]string{"CONFLUENCE_TOKEN": "confluence-token"})
	plugin.ref = resource.PluginRef{Name: Name, Instance: "main"}
	plugin.cfg = atlassian.Config{CloudID: "cloud-1", SiteURL: "https://example.atlassian.invalid", Auth: atlassian.AuthConfig{Method: atlassian.TokenMethod}}
	providers, err := plugin.DatasourceProviders(context.Background(), pluginhost.Context{})
	if err != nil {
		t.Fatalf("DatasourceProviders: %v", err)
	}
	accessor, err := providers[0].Open(context.Background(), coredatasource.Spec{Name: "confluence", Kind: Name})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	result, err := accessor.(coredatasource.Lister).List(context.Background(), coredatasource.ListRequest{Entity: PageEntity, Limit: 3})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if !strings.HasPrefix(network.request.URL, "https://api.atlassian.com/ex/confluence/cloud-1/wiki/rest/api/content") {
		t.Fatalf("request URL = %q", network.request.URL)
	}
	if got := network.request.Headers["Authorization"]; got != "Bearer confluence-token" {
		t.Fatalf("authorization = %q, want bearer token", got)
	}
	for _, want := range []string{"type=page", "expand=body.storage%2Cversion%2Cspace%2Cancestors", "limit=3"} {
		if !strings.Contains(network.request.URL, want) {
			t.Fatalf("request URL = %q, missing %s", network.request.URL, want)
		}
	}
	if result.NextCursor != "1" {
		t.Fatalf("next cursor = %q, want 1", result.NextCursor)
	}
	if len(result.Records) != 1 || result.Records[0].Content != "Hello & welcome" {
		t.Fatalf("records = %#v", result.Records)
	}
	if result.Records[0].URL != "https://example.atlassian.invalid/wiki/spaces/ENG/pages/123/Runbook" {
		t.Fatalf("record url = %q", result.Records[0].URL)
	}
}

func TestPageListUsesAtlassianBasicAPITokenAuth(t *testing.T) {
	network := &recordingNetwork{response: system.HTTPResponse{
		StatusCode: 200,
		Headers:    map[string][]string{"Content-Type": {"application/json"}},
		Body:       []byte(`{"results":[],"_links":{}}`),
	}}
	plugin := newTestPlugin(t, network, map[string]string{
		"CONFLUENCE_API_TOKEN": "confluence-api-token",
		"CONFLUENCE_EMAIL":     "user@example.invalid",
	})
	plugin.ref = resource.PluginRef{Name: Name, Instance: "main"}
	plugin.cfg = atlassian.Config{SiteURL: "https://example.atlassian.invalid", Auth: atlassian.AuthConfig{Method: atlassian.APITokenMethod}}
	providers, err := plugin.DatasourceProviders(context.Background(), pluginhost.Context{})
	if err != nil {
		t.Fatalf("DatasourceProviders: %v", err)
	}
	accessor, err := providers[0].Open(context.Background(), coredatasource.Spec{Name: "confluence", Kind: Name})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if _, err := accessor.(coredatasource.Lister).List(context.Background(), coredatasource.ListRequest{Entity: PageEntity, Limit: 3}); err != nil {
		t.Fatalf("List: %v", err)
	}
	if !strings.HasPrefix(network.request.URL, "https://example.atlassian.invalid/wiki/api/v2/pages") {
		t.Fatalf("request URL = %q", network.request.URL)
	}
	want := "Basic " + base64.StdEncoding.EncodeToString([]byte("user@example.invalid:confluence-api-token"))
	if got := network.request.Headers["Authorization"]; got != want {
		t.Fatalf("authorization = %q, want basic API token auth", got)
	}
}

func TestConnectionReportsCurrentConfluenceUser(t *testing.T) {
	network := &recordingNetwork{response: system.HTTPResponse{
		StatusCode: 200,
		Headers:    map[string][]string{"Content-Type": {"application/json"}},
		Body:       []byte(`{"accountId":"abc-123","displayName":"Timo Friedl","publicName":"Timo"}`),
	}}
	plugin := newTestPlugin(t, network, map[string]string{
		"ATLASSIAN_API_TOKEN": "api-token",
		"ATLASSIAN_EMAIL":     "user@example.invalid",
	})
	plugin.cfg = atlassian.Config{SiteURL: "https://example.atlassian.invalid"}
	ref := resource.PluginRef{Name: Name, Instance: "main"}
	reports := make(chan pluginhost.AuthTestReport, 1)
	if err := plugin.TestConnection(context.Background(), pluginhost.Context{Ref: ref}, pluginhost.AuthTestRequest{Ref: ref, Method: atlassian.APITokenMethod}, reports); err != nil {
		t.Fatalf("TestConnection: %v", err)
	}
	close(reports)
	report := <-reports
	if report.Check != "current_user" || report.Status != "ok" || report.Message != "Timo Friedl" || report.Details["account_id"] != "abc-123" {
		t.Fatalf("report = %#v", report)
	}
	if !strings.HasPrefix(network.request.URL, "https://example.atlassian.invalid/wiki/rest/api/user/current") {
		t.Fatalf("request URL = %q", network.request.URL)
	}
}

func TestPageListUsesDiscoveredSiteURLForCanonicalLinks(t *testing.T) {
	network := &sequenceNetwork{responses: []system.HTTPResponse{
		{
			StatusCode: 200,
			Headers:    map[string][]string{"Content-Type": {"application/json"}},
			Body:       []byte(`[{"id":"cloud-1","url":"https://example.atlassian.invalid","name":"Company"}]`),
		},
		{
			StatusCode: 200,
			Headers:    map[string][]string{"Content-Type": {"application/json"}},
			Body:       []byte(`{"results":[{"id":"123","title":"Runbook","status":"current","space":{"id":42,"key":"ENG"},"version":{"number":7},"body":{"storage":{"value":"<p>Hello</p>"}},"_links":{"webui":"/wiki/spaces/ENG/pages/123/Runbook"}}],"_links":{}}`),
		},
	}}
	plugin := newTestPlugin(t, network, map[string]string{"CONFLUENCE_TOKEN": "confluence-token"})
	plugin.ref = resource.PluginRef{Name: Name, Instance: "main"}
	plugin.cfg = atlassian.Config{CloudID: "cloud-1", Auth: atlassian.AuthConfig{Method: atlassian.TokenMethod}}
	providers, err := plugin.DatasourceProviders(context.Background(), pluginhost.Context{})
	if err != nil {
		t.Fatalf("DatasourceProviders: %v", err)
	}
	accessor, err := providers[0].Open(context.Background(), coredatasource.Spec{Name: "confluence", Kind: Name})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	result, err := accessor.(coredatasource.Lister).List(context.Background(), coredatasource.ListRequest{Entity: PageEntity, Limit: 1})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(result.Records) != 1 || result.Records[0].URL != "https://example.atlassian.invalid/wiki/spaces/ENG/pages/123/Runbook" {
		t.Fatalf("records = %#v, want discovered canonical URL", result.Records)
	}
	if len(network.requests) != 2 || network.requests[0].URL != "https://api.atlassian.com/oauth/token/accessible-resources" {
		t.Fatalf("requests = %#v, want discovery before confluence list", network.requests)
	}
}

func TestSpaceGetUsesConfluenceV1BaseAndTokenAuth(t *testing.T) {
	network := &recordingNetwork{response: system.HTTPResponse{
		StatusCode: 200,
		Headers:    map[string][]string{"Content-Type": {"application/json"}},
		Body:       []byte(`{"results":[{"id":42,"key":"ENG","name":"Engineering","type":"global","status":"current","description":{"plain":{"value":"Team docs"}},"_links":{"webui":"/wiki/spaces/ENG"}}]}`),
	}}
	plugin := newTestPlugin(t, network, map[string]string{"CONFLUENCE_TOKEN": "confluence-token"})
	plugin.ref = resource.PluginRef{Name: Name, Instance: "main"}
	plugin.cfg = atlassian.Config{CloudID: "cloud-1", SiteURL: "https://example.atlassian.invalid", Auth: atlassian.AuthConfig{Method: atlassian.TokenMethod}}
	providers, err := plugin.DatasourceProviders(context.Background(), pluginhost.Context{})
	if err != nil {
		t.Fatalf("DatasourceProviders: %v", err)
	}
	accessor, err := providers[0].Open(context.Background(), coredatasource.Spec{Name: "confluence", Kind: Name, Entities: []coredatasource.EntityType{SpaceEntity}})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	record, err := accessor.(coredatasource.Getter).Get(context.Background(), coredatasource.GetRequest{Entity: SpaceEntity, ID: "ENG"})
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !strings.HasPrefix(network.request.URL, "https://api.atlassian.com/ex/confluence/cloud-1/wiki/rest/api/space?") ||
		!strings.Contains(network.request.URL, "spaceKey=ENG") ||
		!strings.Contains(network.request.URL, "expand=description.plain") {
		t.Fatalf("request URL = %q", network.request.URL)
	}
	if record.ID != "ENG" || record.Title != "Engineering" || record.Metadata["key"] != "ENG" {
		t.Fatalf("record = %#v", record)
	}
}

func TestSpaceGetSupportsDetectedSpaceKeys(t *testing.T) {
	network := &recordingNetwork{response: system.HTTPResponse{
		StatusCode: 200,
		Headers:    map[string][]string{"Content-Type": {"application/json"}},
		Body:       []byte(`{"results":[{"id":42,"key":"ENG","name":"Engineering","description":{"plain":{"value":"Team docs"}}}],"_links":{}}`),
	}}
	plugin := newTestPlugin(t, network, map[string]string{"CONFLUENCE_TOKEN": "confluence-token"})
	plugin.ref = resource.PluginRef{Name: Name, Instance: "main"}
	plugin.cfg = atlassian.Config{CloudID: "cloud-1", Auth: atlassian.AuthConfig{Method: atlassian.TokenMethod}}
	providers, err := plugin.DatasourceProviders(context.Background(), pluginhost.Context{})
	if err != nil {
		t.Fatalf("DatasourceProviders: %v", err)
	}
	accessor, err := providers[0].Open(context.Background(), coredatasource.Spec{Name: "confluence", Kind: Name, Entities: []coredatasource.EntityType{SpaceEntity}})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	record, err := accessor.(coredatasource.Getter).Get(context.Background(), coredatasource.GetRequest{Entity: SpaceEntity, ID: "ENG"})
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !strings.HasPrefix(network.request.URL, "https://api.atlassian.com/ex/confluence/cloud-1/wiki/rest/api/space?") ||
		!strings.Contains(network.request.URL, "spaceKey=ENG") {
		t.Fatalf("request URL = %q", network.request.URL)
	}
	if record.ID != "ENG" || record.Metadata["key"] != "ENG" {
		t.Fatalf("record = %#v", record)
	}
}

func TestStorageTextStripsHTML(t *testing.T) {
	if got := plainText(`<p>First <strong>second</strong>&nbsp;line</p>`); got != "First second line" {
		t.Fatalf("plainText = %q", got)
	}
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
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

func newTestPlugin(t *testing.T, network system.Network, env map[string]string) Plugin {
	t.Helper()
	store := runtimesecret.NewFileStore(t.TempDir())
	resolver := runtimesecret.ChainResolver{
		store,
		runtimesecret.EnvResolver{Environment: fakeEnvironment{values: env}},
	}
	return NewWithResolver(fakeSystem{network: network}, store, resolver)
}

type recordingNetwork struct {
	request  system.HTTPRequest
	response system.HTTPResponse
}

func (n *recordingNetwork) DoHTTP(_ context.Context, req system.HTTPRequest) (system.HTTPResponse, error) {
	n.request = req
	return n.response, nil
}

type sequenceNetwork struct {
	requests  []system.HTTPRequest
	responses []system.HTTPResponse
}

func (n *sequenceNetwork) DoHTTP(_ context.Context, req system.HTTPRequest) (system.HTTPResponse, error) {
	n.requests = append(n.requests, req)
	if len(n.responses) == 0 {
		return system.HTTPResponse{StatusCode: 500, Body: []byte(`unexpected request`)}, nil
	}
	resp := n.responses[0]
	n.responses = n.responses[1:]
	return resp, nil
}

type fakeEnvironment struct {
	values map[string]string
}

func (e fakeEnvironment) Lookup(_ context.Context, key string) (string, bool, error) {
	value, ok := e.values[key]
	return value, ok, nil
}
