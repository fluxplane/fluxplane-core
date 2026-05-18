package confluenceplugin

import (
	"context"
	"strings"
	"testing"

	coredatasource "github.com/fluxplane/agentruntime/core/datasource"
	"github.com/fluxplane/agentruntime/core/resource"
	coresecret "github.com/fluxplane/agentruntime/core/secret"
	"github.com/fluxplane/agentruntime/orchestration/pluginhost"
	"github.com/fluxplane/agentruntime/plugins/atlassianplugin"
	"github.com/fluxplane/agentruntime/runtime/system"
)

func TestPluginIsNotConnectorProvider(t *testing.T) {
	if _, ok := any(New(nil)).(pluginhost.ConnectorProviderContributor); ok {
		t.Fatal("Confluence plugin must not contribute connector providers")
	}
}

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
	if len(methods) != 2 {
		t.Fatalf("methods len = %d, want 2", len(methods))
	}
	if methods[0].Name != atlassianplugin.TokenMethod || methods[0].Method != coresecret.AuthMethodEnv {
		t.Fatalf("token method = %#v", methods[0])
	}
	if methods[1].Name != atlassianplugin.OAuth2Method || methods[1].Secret.ResourceName() != "plugin/confluence/main/oauth2_token" {
		t.Fatalf("oauth method = %#v", methods[1])
	}
	if !contains(methods[1].OAuth2.Scopes, "read:page:confluence") || !contains(methods[1].OAuth2.Scopes, "read:space:confluence") {
		t.Fatalf("oauth scopes = %#v", methods[1].OAuth2.Scopes)
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

func TestPageListUsesConfluenceV2BaseAndTokenAuth(t *testing.T) {
	network := &recordingNetwork{response: system.HTTPResponse{
		StatusCode: 200,
		Headers:    map[string][]string{"Content-Type": {"application/json"}},
		Body:       []byte(`{"results":[{"id":"123","title":"Runbook","status":"current","spaceId":"42","version":{"number":7},"body":{"storage":{"value":"<p>Hello &amp; welcome</p>"}},"_links":{"webui":"/wiki/spaces/ENG/pages/123/Runbook"}}],"_links":{"next":"/wiki/api/v2/pages?cursor=next-1"}}`),
	}}
	plugin := New(fakeSystem{
		network: network,
		env:     fakeEnvironment{values: map[string]string{"CONFLUENCE_TOKEN": "confluence-token"}},
	})
	plugin.ref = resource.PluginRef{Name: Name, Instance: "main"}
	plugin.cfg = atlassianplugin.Config{CloudID: "cloud-1", SiteURL: "https://company.atlassian.net", Auth: atlassianplugin.AuthConfig{Method: atlassianplugin.TokenMethod}}
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
	if !strings.HasPrefix(network.request.URL, "https://api.atlassian.com/ex/confluence/cloud-1/wiki/api/v2/pages") {
		t.Fatalf("request URL = %q", network.request.URL)
	}
	if got := network.request.Headers["Authorization"]; got != "Bearer confluence-token" {
		t.Fatalf("authorization = %q, want bearer token", got)
	}
	if !strings.Contains(network.request.URL, "body-format=storage") || !strings.Contains(network.request.URL, "limit=3") {
		t.Fatalf("request URL = %q, want body-format and limit", network.request.URL)
	}
	if result.NextCursor != "next-1" {
		t.Fatalf("next cursor = %q, want next-1", result.NextCursor)
	}
	if len(result.Records) != 1 || result.Records[0].Content != "Hello & welcome" {
		t.Fatalf("records = %#v", result.Records)
	}
	if result.Records[0].URL != "https://company.atlassian.net/wiki/spaces/ENG/pages/123/Runbook" {
		t.Fatalf("record url = %q", result.Records[0].URL)
	}
}

func TestSpaceGetUsesConfluenceV2BaseAndTokenAuth(t *testing.T) {
	network := &recordingNetwork{response: system.HTTPResponse{
		StatusCode: 200,
		Headers:    map[string][]string{"Content-Type": {"application/json"}},
		Body:       []byte(`{"id":"42","key":"ENG","name":"Engineering","type":"global","status":"current","description":{"plain":{"value":"Team docs"}},"_links":{"webui":"/wiki/spaces/ENG"}}`),
	}}
	plugin := New(fakeSystem{
		network: network,
		env:     fakeEnvironment{values: map[string]string{"CONFLUENCE_TOKEN": "confluence-token"}},
	})
	plugin.ref = resource.PluginRef{Name: Name, Instance: "main"}
	plugin.cfg = atlassianplugin.Config{CloudID: "cloud-1", SiteURL: "https://company.atlassian.net", Auth: atlassianplugin.AuthConfig{Method: atlassianplugin.TokenMethod}}
	providers, err := plugin.DatasourceProviders(context.Background(), pluginhost.Context{})
	if err != nil {
		t.Fatalf("DatasourceProviders: %v", err)
	}
	accessor, err := providers[0].Open(context.Background(), coredatasource.Spec{Name: "confluence", Kind: Name, Entities: []coredatasource.EntityType{SpaceEntity}})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	record, err := accessor.(coredatasource.Getter).Get(context.Background(), coredatasource.GetRequest{Entity: SpaceEntity, ID: "42"})
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if network.request.URL != "https://api.atlassian.com/ex/confluence/cloud-1/wiki/api/v2/spaces/42" {
		t.Fatalf("request URL = %q", network.request.URL)
	}
	if record.ID != "42" || record.Title != "Engineering" || record.Metadata["key"] != "ENG" {
		t.Fatalf("record = %#v", record)
	}
}

func TestSpaceGetSupportsDetectedSpaceKeys(t *testing.T) {
	network := &recordingNetwork{response: system.HTTPResponse{
		StatusCode: 200,
		Headers:    map[string][]string{"Content-Type": {"application/json"}},
		Body:       []byte(`{"results":[{"id":"42","key":"ENG","name":"Engineering","description":{"plain":{"value":"Team docs"}}}],"_links":{}}`),
	}}
	plugin := New(fakeSystem{
		network: network,
		env:     fakeEnvironment{values: map[string]string{"CONFLUENCE_TOKEN": "confluence-token"}},
	})
	plugin.ref = resource.PluginRef{Name: Name, Instance: "main"}
	plugin.cfg = atlassianplugin.Config{CloudID: "cloud-1", Auth: atlassianplugin.AuthConfig{Method: atlassianplugin.TokenMethod}}
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
	if !strings.HasPrefix(network.request.URL, "https://api.atlassian.com/ex/confluence/cloud-1/wiki/api/v2/spaces?") ||
		!strings.Contains(network.request.URL, "keys=ENG") {
		t.Fatalf("request URL = %q", network.request.URL)
	}
	if record.ID != "42" || record.Metadata["key"] != "ENG" {
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
