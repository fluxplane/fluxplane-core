package jiraplugin

import (
	"context"
	"encoding/base64"
	"strings"
	"testing"

	coredatasource "github.com/fluxplane/agentruntime/core/datasource"
	coreoperation "github.com/fluxplane/agentruntime/core/operation"
	"github.com/fluxplane/agentruntime/core/resource"
	coresecret "github.com/fluxplane/agentruntime/core/secret"
	"github.com/fluxplane/agentruntime/orchestration/pluginhost"
	"github.com/fluxplane/agentruntime/plugins/atlassianplugin"
	operationruntime "github.com/fluxplane/agentruntime/runtime/operation"
	runtimesecret "github.com/fluxplane/agentruntime/runtime/secret"
	"github.com/fluxplane/agentruntime/runtime/system"
)

func TestPluginIsNotConnectorProvider(t *testing.T) {
	if _, ok := any(New(nil)).(pluginhost.ConnectorProviderContributor); ok {
		t.Fatal("Jira plugin must not contribute connector providers")
	}
}

func TestPluginContributesJiraDatasourceEntities(t *testing.T) {
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
	for _, want := range []coredatasource.EntityType{IssueEntity, ProjectEntity} {
		if !got[want] {
			t.Fatalf("entities = %#v, missing %s", got, want)
		}
	}
}

func TestPluginContributesJiraIssueDetectors(t *testing.T) {
	providers, err := New(nil).DatasourceProviders(context.Background(), pluginhost.Context{})
	if err != nil {
		t.Fatalf("DatasourceProviders: %v", err)
	}
	var issue coredatasource.EntitySpec
	for _, entity := range providers[0].Entities() {
		if entity.Type == IssueEntity {
			issue = entity
		}
	}
	if len(issue.Detectors) != 2 {
		t.Fatalf("detectors = %#v, want key and url detectors", issue.Detectors)
	}
	if issue.Detectors[0].Kind != coredatasource.DetectorRegex || issue.Detectors[0].IDTemplate == "" {
		t.Fatalf("detector = %#v, want generic regex detector with id template", issue.Detectors[0])
	}
}

func TestPluginMaterializesIssueSearch(t *testing.T) {
	plugin := New(nil)
	bundle, err := plugin.Contributions(context.Background(), pluginhost.Context{Ref: resource.PluginRef{Name: Name, Instance: "jira-prod"}})
	if err != nil {
		t.Fatalf("Contributions: %v", err)
	}
	if len(bundle.Operations) != 1 {
		t.Fatalf("operations len = %d, want 1", len(bundle.Operations))
	}
	if got := string(bundle.Operations[0].Ref.Name); got != "jira_prod_issue_search" {
		t.Fatalf("operation name = %q, want jira_prod_issue_search", got)
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
	if methods[1].Name != atlassianplugin.OAuth2Method || methods[1].Secret.ResourceName() != "plugin/jira/main/oauth2_token" {
		t.Fatalf("oauth method = %#v", methods[1])
	}
}

func TestIssueSearchAccessUsesStoredCloudID(t *testing.T) {
	store := runtimesecret.NewFileStore(t.TempDir())
	ref := resource.PluginRef{Name: Name, Instance: "main"}
	if err := store.SaveSecret(context.Background(), runtimesecret.StoredSecret{
		Ref:      atlassianplugin.OAuthSecretRef(Name, ref),
		Kind:     coresecret.KindOAuth2Token,
		Value:    "access",
		Metadata: map[string]string{"cloud_id": "cloud-1"},
	}); err != nil {
		t.Fatalf("SaveSecret: %v", err)
	}
	plugin := New(nil, store)
	plugin.ref = ref
	ops, err := plugin.Operations(context.Background(), pluginhost.Context{})
	if err != nil {
		t.Fatalf("Operations: %v", err)
	}
	access, ok, err := operationruntime.AccessFor(coreoperation.NewContext(context.Background(), nil), ops[0], map[string]any{"jql": "project = DEV"})
	if err != nil || !ok {
		t.Fatalf("AccessFor = %#v, %v, %v; want access", access, ok, err)
	}
	if got := access[0].Resource.Name; got != "https://api.atlassian.com/ex/jira/cloud-1/rest/api/3" {
		t.Fatalf("access network = %q", got)
	}
}

func TestJiraDatasourceDefaultsToAllEntities(t *testing.T) {
	providers, err := New(nil).DatasourceProviders(context.Background(), pluginhost.Context{})
	if err != nil {
		t.Fatalf("DatasourceProviders: %v", err)
	}
	accessor, err := providers[0].Open(context.Background(), coredatasource.Spec{Name: "jira", Kind: Name})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if len(accessor.Entities()) != 2 {
		t.Fatalf("entities len = %d, want 2", len(accessor.Entities()))
	}
}

func TestIssueSearchUsesNativeHTTPAndTokenAuth(t *testing.T) {
	network := &recordingNetwork{response: system.HTTPResponse{
		StatusCode: 200,
		Headers:    map[string][]string{"Content-Type": {"application/json"}},
		Body:       []byte(`{"issues":[{"id":"100","key":"DEV-381","self":"https://api.example/issue/100","fields":{"summary":"Native Jira","status":{"name":"Open"},"description":"Useful"}}],"total":1}`),
	}}
	plugin := New(fakeSystem{
		network: network,
		env:     fakeEnvironment{values: map[string]string{"JIRA_TOKEN": "jira-token"}},
	})
	plugin.ref = resource.PluginRef{Name: Name, Instance: "main"}
	plugin.cfg = atlassianplugin.Config{CloudID: "cloud-1", Auth: atlassianplugin.AuthConfig{Method: atlassianplugin.TokenMethod}}
	ops, err := plugin.Operations(context.Background(), pluginhost.Context{})
	if err != nil {
		t.Fatalf("Operations: %v", err)
	}
	result := ops[0].Run(coreoperation.NewContext(context.Background(), nil), map[string]any{"jql": "project = DEV", "max_results": 3})
	if result.Status != coreoperation.StatusOK {
		t.Fatalf("status = %s error = %#v", result.Status, result.Error)
	}
	if !strings.HasPrefix(network.request.URL, "https://api.atlassian.com/ex/jira/cloud-1/rest/api/3/search/jql") {
		t.Fatalf("request URL = %q", network.request.URL)
	}
	if got := network.request.Headers["Authorization"]; got != "Bearer jira-token" {
		t.Fatalf("authorization = %q, want bearer token", got)
	}
	if !strings.Contains(network.request.URL, "maxResults=3") {
		t.Fatalf("request URL = %q, want maxResults=3", network.request.URL)
	}
}

func TestIssueSearchUsesAtlassianServiceAccountAPITokenAuth(t *testing.T) {
	network := &recordingNetwork{response: system.HTTPResponse{
		StatusCode: 200,
		Headers:    map[string][]string{"Content-Type": {"application/json"}},
		Body:       []byte(`{"issues":[],"total":0}`),
	}}
	plugin := New(fakeSystem{
		network: network,
		env:     fakeEnvironment{values: map[string]string{"JIRA_API_TOKEN": "jira-api-token"}},
	})
	plugin.ref = resource.PluginRef{Name: Name, Instance: "main"}
	plugin.cfg = atlassianplugin.Config{CloudID: "cloud-1", Auth: atlassianplugin.AuthConfig{Method: atlassianplugin.TokenMethod}}
	ops, err := plugin.Operations(context.Background(), pluginhost.Context{})
	if err != nil {
		t.Fatalf("Operations: %v", err)
	}
	result := ops[0].Run(coreoperation.NewContext(context.Background(), nil), map[string]any{"jql": "project = DEV"})
	if result.Status != coreoperation.StatusOK {
		t.Fatalf("status = %s error = %#v", result.Status, result.Error)
	}
	if !strings.HasPrefix(network.request.URL, "https://api.atlassian.com/ex/jira/cloud-1/rest/api/3/search/jql") {
		t.Fatalf("request URL = %q", network.request.URL)
	}
	if got := network.request.Headers["Authorization"]; got != "Bearer jira-api-token" {
		t.Fatalf("authorization = %q, want bearer API token auth", got)
	}
}

func TestIssueSearchUsesAtlassianBasicAPITokenAuth(t *testing.T) {
	network := &recordingNetwork{response: system.HTTPResponse{
		StatusCode: 200,
		Headers:    map[string][]string{"Content-Type": {"application/json"}},
		Body:       []byte(`{"issues":[],"total":0}`),
	}}
	plugin := New(fakeSystem{
		network: network,
		env: fakeEnvironment{values: map[string]string{
			"JIRA_API_TOKEN": "jira-api-token",
			"JIRA_EMAIL":     "user@example.com",
		}},
	})
	plugin.ref = resource.PluginRef{Name: Name, Instance: "main"}
	plugin.cfg = atlassianplugin.Config{SiteURL: "https://company.atlassian.net", Auth: atlassianplugin.AuthConfig{Method: "basic"}}
	ops, err := plugin.Operations(context.Background(), pluginhost.Context{})
	if err != nil {
		t.Fatalf("Operations: %v", err)
	}
	result := ops[0].Run(coreoperation.NewContext(context.Background(), nil), map[string]any{"jql": "project = DEV"})
	if result.Status != coreoperation.StatusOK {
		t.Fatalf("status = %s error = %#v", result.Status, result.Error)
	}
	if !strings.HasPrefix(network.request.URL, "https://company.atlassian.net/rest/api/3/search/jql") {
		t.Fatalf("request URL = %q", network.request.URL)
	}
	want := "Basic " + base64.StdEncoding.EncodeToString([]byte("user@example.com:jira-api-token"))
	if got := network.request.Headers["Authorization"]; got != want {
		t.Fatalf("authorization = %q, want basic API token auth", got)
	}
}

func TestIssueGetUsesDiscoveredCanonicalWebURL(t *testing.T) {
	network := &sequenceNetwork{responses: []system.HTTPResponse{
		{
			StatusCode: 200,
			Headers:    map[string][]string{"Content-Type": {"application/json"}},
			Body:       []byte(`[{"id":"cloud-1","url":"https://company.atlassian.net","name":"Company"}]`),
		},
		{
			StatusCode: 200,
			Headers:    map[string][]string{"Content-Type": {"application/json"}},
			Body:       []byte(`{"id":"48997","key":"DEV-380","self":"https://api.atlassian.com/ex/jira/cloud-1/rest/api/3/issue/48997","fields":{"summary":"Native Jira","status":{"name":"Open"},"description":"Useful"}}`),
		},
	}}
	plugin := New(fakeSystem{
		network: network,
		env:     fakeEnvironment{values: map[string]string{"JIRA_TOKEN": "jira-token"}},
	})
	plugin.ref = resource.PluginRef{Name: Name, Instance: "main"}
	plugin.cfg = atlassianplugin.Config{CloudID: "cloud-1", Auth: atlassianplugin.AuthConfig{Method: atlassianplugin.TokenMethod}}
	providers, err := plugin.DatasourceProviders(context.Background(), pluginhost.Context{})
	if err != nil {
		t.Fatalf("DatasourceProviders: %v", err)
	}
	accessor, err := providers[0].Open(context.Background(), coredatasource.Spec{Name: "jira", Kind: Name})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	record, err := accessor.(coredatasource.Getter).Get(context.Background(), coredatasource.GetRequest{Entity: IssueEntity, ID: "DEV-380"})
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if record.URL != "https://company.atlassian.net/browse/DEV-380" {
		t.Fatalf("record url = %q, want canonical Jira web URL", record.URL)
	}
	if record.Metadata["api_url"] != "https://api.atlassian.com/ex/jira/cloud-1/rest/api/3/issue/48997" {
		t.Fatalf("metadata api_url = %q", record.Metadata["api_url"])
	}
	if len(network.requests) != 2 || network.requests[0].URL != "https://api.atlassian.com/oauth/token/accessible-resources" {
		t.Fatalf("requests = %#v, want discovery before issue get", network.requests)
	}
}

func TestIssueGetLeavesURLBlankWhenSiteDiscoveryDoesNotMatch(t *testing.T) {
	network := &sequenceNetwork{responses: []system.HTTPResponse{
		{
			StatusCode: 200,
			Headers:    map[string][]string{"Content-Type": {"application/json"}},
			Body:       []byte(`[{"id":"other-cloud","url":"https://other.atlassian.net","name":"Other"}]`),
		},
		{
			StatusCode: 200,
			Headers:    map[string][]string{"Content-Type": {"application/json"}},
			Body:       []byte(`{"id":"48997","key":"DEV-380","self":"https://api.atlassian.com/ex/jira/cloud-1/rest/api/3/issue/48997","fields":{"summary":"Native Jira","status":{"name":"Open"},"description":"Useful"}}`),
		},
	}}
	plugin := New(fakeSystem{
		network: network,
		env:     fakeEnvironment{values: map[string]string{"JIRA_TOKEN": "jira-token"}},
	})
	plugin.ref = resource.PluginRef{Name: Name, Instance: "main"}
	plugin.cfg = atlassianplugin.Config{CloudID: "cloud-1", Auth: atlassianplugin.AuthConfig{Method: atlassianplugin.TokenMethod}}
	providers, err := plugin.DatasourceProviders(context.Background(), pluginhost.Context{})
	if err != nil {
		t.Fatalf("DatasourceProviders: %v", err)
	}
	accessor, err := providers[0].Open(context.Background(), coredatasource.Spec{Name: "jira", Kind: Name})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	record, err := accessor.(coredatasource.Getter).Get(context.Background(), coredatasource.GetRequest{Entity: IssueEntity, ID: "DEV-380"})
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if record.URL != "" {
		t.Fatalf("record url = %q, want no fabricated canonical URL", record.URL)
	}
	if record.Metadata["api_url"] == "" {
		t.Fatalf("metadata = %#v, want api_url preserved", record.Metadata)
	}
}

func TestProjectGetUsesCanonicalWebURL(t *testing.T) {
	network := &recordingNetwork{response: system.HTTPResponse{
		StatusCode: 200,
		Headers:    map[string][]string{"Content-Type": {"application/json"}},
		Body:       []byte(`{"id":"10000","key":"DEV","name":"Development","projectTypeKey":"software","self":"https://api.example/rest/api/3/project/10000"}`),
	}}
	plugin := New(fakeSystem{
		network: network,
		env:     fakeEnvironment{values: map[string]string{"JIRA_TOKEN": "jira-token"}},
	})
	plugin.ref = resource.PluginRef{Name: Name, Instance: "main"}
	plugin.cfg = atlassianplugin.Config{CloudID: "cloud-1", SiteURL: "https://company.atlassian.net", Auth: atlassianplugin.AuthConfig{Method: atlassianplugin.TokenMethod}}
	providers, err := plugin.DatasourceProviders(context.Background(), pluginhost.Context{})
	if err != nil {
		t.Fatalf("DatasourceProviders: %v", err)
	}
	accessor, err := providers[0].Open(context.Background(), coredatasource.Spec{Name: "jira", Kind: Name})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	record, err := accessor.(coredatasource.Getter).Get(context.Background(), coredatasource.GetRequest{Entity: ProjectEntity, ID: "DEV"})
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if record.URL != "https://company.atlassian.net/browse/DEV" {
		t.Fatalf("record url = %q, want canonical Jira project URL", record.URL)
	}
	if record.Metadata["api_url"] != "https://api.example/rest/api/3/project/10000" {
		t.Fatalf("metadata api_url = %q", record.Metadata["api_url"])
	}
}

func TestDescriptionTextExtractsAtlassianDocumentText(t *testing.T) {
	doc := map[string]any{
		"type": "doc",
		"content": []any{
			map[string]any{
				"type": "paragraph",
				"content": []any{
					map[string]any{"type": "text", "text": "First"},
					map[string]any{"type": "text", "text": "second"},
				},
			},
		},
	}
	if got := descriptionText(doc); got != "First second" {
		t.Fatalf("descriptionText = %q", got)
	}
}

func TestJiraDatasourceJQLBuildsUsefulDefaultQueries(t *testing.T) {
	tests := map[string]string{
		"DEV-381":             `issuekey = DEV-381 OR text ~ "DEV-381"`,
		"lyse":                `text ~ "lyse"`,
		`project = DEV`:       `project = DEV`,
		`summary ~ "billing"`: `summary ~ "billing"`,
		`lyse "quoted" value`: `text ~ "lyse \"quoted\" value"`,
	}
	for input, want := range tests {
		if got := jiraDatasourceJQL(input); got != want {
			t.Fatalf("jiraDatasourceJQL(%q) = %q, want %q", input, got, want)
		}
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
