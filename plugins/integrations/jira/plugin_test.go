package jira

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	coredatasource "github.com/fluxplane/fluxplane-core/core/datasource"
	coreoperation "github.com/fluxplane/fluxplane-core/core/operation"
	"github.com/fluxplane/fluxplane-core/core/resource"
	coresecret "github.com/fluxplane/fluxplane-core/core/secret"
	"github.com/fluxplane/fluxplane-core/orchestration/pluginhost"
	"github.com/fluxplane/fluxplane-core/plugins/internal/atlassian"
	operationruntime "github.com/fluxplane/fluxplane-core/runtime/operation"
	runtimesecret "github.com/fluxplane/fluxplane-core/runtime/secret"
	"github.com/fluxplane/fluxplane-core/runtime/system"
	"github.com/fluxplane/fluxplane-system/systemkit"
	fpsystemtest "github.com/fluxplane/fluxplane-system/systemtest"
)

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
	if len(bundle.Operations) != 3 {
		t.Fatalf("operations len = %d, want 3", len(bundle.Operations))
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
	if len(methods) != 3 {
		t.Fatalf("methods len = %d, want 3", len(methods))
	}
	if methods[0].Name != atlassian.TokenMethod || methods[0].Method != coresecret.AuthMethodEnv {
		t.Fatalf("token method = %#v", methods[0])
	}
	if methods[1].Name != atlassian.APITokenMethod || methods[1].Method != coresecret.AuthMethodStored {
		t.Fatalf("api token method = %#v", methods[1])
	}
	if methods[2].Name != atlassian.OAuth2Method || methods[2].Secret.ResourceName() != "plugin/jira/main/oauth2_token" {
		t.Fatalf("oauth method = %#v", methods[2])
	}
}

func TestIssueSearchAccessUsesStoredCloudID(t *testing.T) {
	store := runtimesecret.NewFileStore(t.TempDir())
	ref := resource.PluginRef{Name: Name, Instance: "main"}
	if err := store.SaveSecret(context.Background(), runtimesecret.StoredSecret{
		Ref:      atlassian.OAuthSecretRef(Name, ref),
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
	network := &recordingNetwork{response: systemkit.HTTPResponse{
		StatusCode: 200,
		Headers:    map[string][]string{"Content-Type": {"application/json"}},
		Body:       []byte(`{"issues":[{"id":"100","key":"DEV-381","self":"https://api.example/issue/100","fields":{"summary":"Native Jira","status":{"name":"Open"},"description":"Useful"}}],"total":1}`),
	}}
	plugin := newTestPlugin(t, network, map[string]string{"JIRA_TOKEN": "jira-token"})
	plugin.ref = resource.PluginRef{Name: Name, Instance: "main"}
	plugin.cfg = atlassian.Config{CloudID: "cloud-1", Auth: atlassian.AuthConfig{Method: atlassian.TokenMethod}}
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

func TestIssueCreateUsesNativeHTTPAndTokenAuth(t *testing.T) {
	network := &recordingNetwork{response: systemkit.HTTPResponse{
		StatusCode: 201,
		Headers:    map[string][]string{"Content-Type": {"application/json"}},
		Body:       []byte(`{"id":"101","key":"DEV-488","self":"https://api.example/issue/101"}`),
	}}
	plugin := newTestPlugin(t, network, map[string]string{"JIRA_TOKEN": "jira-token"})
	plugin.ref = resource.PluginRef{Name: Name, Instance: "main"}
	plugin.cfg = atlassian.Config{CloudID: "cloud-1", Auth: atlassian.AuthConfig{Method: atlassian.TokenMethod}}
	ops, err := plugin.Operations(context.Background(), pluginhost.Context{})
	if err != nil {
		t.Fatalf("Operations: %v", err)
	}
	result := ops[1].Run(coreoperation.NewContext(context.Background(), nil), map[string]any{
		"project_key": "DEV",
		"issue_type":  "Task",
		"summary":     "Create issue from plugin",
		"description": "Line one with **bold**\n\n- Line two",
		"labels":      []any{"ivr", "realtime"},
	})
	if result.Status != coreoperation.StatusOK {
		t.Fatalf("status = %s error = %#v", result.Status, result.Error)
	}
	if network.request.Method != "POST" {
		t.Fatalf("method = %q, want POST", network.request.Method)
	}
	if network.request.URL != "https://api.atlassian.com/ex/jira/cloud-1/rest/api/3/issue" {
		t.Fatalf("request URL = %q", network.request.URL)
	}
	if got := network.request.Headers["Authorization"]; got != "Bearer jira-token" {
		t.Fatalf("authorization = %q, want bearer token", got)
	}
	body := string(network.request.Body)
	for _, want := range []string{`"project":{"key":"DEV"}`, `"issuetype":{"name":"Task"}`, `"summary":"Create issue from plugin"`, `"labels":["ivr","realtime"]`, `"type":"doc"`, `"type":"strong"`, `"type":"bulletList"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("body = %s, missing %s", body, want)
		}
	}
}

func TestIssueCommentUsesMarkdownConverter(t *testing.T) {
	network := &recordingNetwork{response: systemkit.HTTPResponse{
		StatusCode: 201,
		Headers:    map[string][]string{"Content-Type": {"application/json"}},
		Body:       []byte(`{"id":"10042"}`),
	}}
	plugin := newTestPlugin(t, network, map[string]string{"JIRA_TOKEN": "jira-token"})
	plugin.ref = resource.PluginRef{Name: Name, Instance: "main"}
	plugin.cfg = atlassian.Config{CloudID: "cloud-1", Auth: atlassian.AuthConfig{Method: atlassian.TokenMethod}}
	ops, err := plugin.Operations(context.Background(), pluginhost.Context{})
	if err != nil {
		t.Fatalf("Operations: %v", err)
	}
	result := ops[2].Run(coreoperation.NewContext(context.Background(), nil), map[string]any{
		"issue_key": "DEV-488",
		"body":      "## Update\n\nSee `worker.go`.",
	})
	if result.Status != coreoperation.StatusOK {
		t.Fatalf("status = %s error = %#v", result.Status, result.Error)
	}
	if network.request.Method != "POST" {
		t.Fatalf("method = %q, want POST", network.request.Method)
	}
	if network.request.URL != "https://api.atlassian.com/ex/jira/cloud-1/rest/api/3/issue/DEV-488/comment" {
		t.Fatalf("request URL = %q", network.request.URL)
	}
	body := string(network.request.Body)
	for _, want := range []string{`"body":{"content"`, `"type":"heading"`, `"level":2`, `"type":"code"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("body = %s, missing %s", body, want)
		}
	}
}

func TestJiraMarkdownToADFLinkifiesKnownIssueKeys(t *testing.T) {
	network := &recordingNetwork{response: systemkit.HTTPResponse{
		StatusCode: 200,
		Headers:    map[string][]string{"Content-Type": {"application/json"}},
		Body:       []byte(`{"values":[{"key":"DEV","name":"Development"}],"total":1}`),
	}}
	session := atlassian.Session{
		SiteURL:       "https://example.atlassian.invalid",
		BaseURL:       "https://api.atlassian.invalid/rest/api/3",
		Authorization: "Bearer token",
	}
	doc := jiraMarkdownToADF(context.Background(), fakeSystem{network: network}, session, "Related to DEV-381.")
	data, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	body := string(data)
	if !strings.Contains(body, `"type":"inlineCard"`) || !strings.Contains(body, `"url":"https://example.atlassian.invalid/browse/DEV-381"`) {
		t.Fatalf("doc = %s, want inline card link to known issue", body)
	}
}

func TestIssueSearchUsesAtlassianServiceAccountAPITokenAuth(t *testing.T) {
	network := &recordingNetwork{response: systemkit.HTTPResponse{
		StatusCode: 200,
		Headers:    map[string][]string{"Content-Type": {"application/json"}},
		Body:       []byte(`{"issues":[],"total":0}`),
	}}
	plugin := newTestPlugin(t, network, map[string]string{"JIRA_API_TOKEN": "jira-api-token"})
	plugin.ref = resource.PluginRef{Name: Name, Instance: "main"}
	plugin.cfg = atlassian.Config{CloudID: "cloud-1", Auth: atlassian.AuthConfig{Method: atlassian.TokenMethod, TokenEnv: "JIRA_API_TOKEN"}}
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
	network := &recordingNetwork{response: systemkit.HTTPResponse{
		StatusCode: 200,
		Headers:    map[string][]string{"Content-Type": {"application/json"}},
		Body:       []byte(`{"issues":[],"total":0}`),
	}}
	plugin := newTestPlugin(t, network, map[string]string{
		"JIRA_API_TOKEN": "jira-api-token",
		"JIRA_EMAIL":     "user@example.invalid",
	})
	plugin.ref = resource.PluginRef{Name: Name, Instance: "main"}
	plugin.cfg = atlassian.Config{SiteURL: "https://example.atlassian.invalid", Auth: atlassian.AuthConfig{Method: atlassian.APITokenMethod}}
	ops, err := plugin.Operations(context.Background(), pluginhost.Context{})
	if err != nil {
		t.Fatalf("Operations: %v", err)
	}
	result := ops[0].Run(coreoperation.NewContext(context.Background(), nil), map[string]any{"jql": "project = DEV"})
	if result.Status != coreoperation.StatusOK {
		t.Fatalf("status = %s error = %#v", result.Status, result.Error)
	}
	if !strings.HasPrefix(network.request.URL, "https://example.atlassian.invalid/rest/api/3/search/jql") {
		t.Fatalf("request URL = %q", network.request.URL)
	}
	want := "Basic " + base64.StdEncoding.EncodeToString([]byte("user@example.invalid:jira-api-token"))
	if got := network.request.Headers["Authorization"]; got != want {
		t.Fatalf("authorization = %q, want basic API token auth", got)
	}
}

func TestConnectionReportsCurrentJiraUser(t *testing.T) {
	network := &recordingNetwork{response: systemkit.HTTPResponse{
		StatusCode: 200,
		Headers:    map[string][]string{"Content-Type": {"application/json"}},
		Body:       []byte(`{"accountId":"abc-123","displayName":"Timo Friedl","emailAddress":"user@example.invalid"}`),
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
	if !strings.HasPrefix(network.request.URL, "https://example.atlassian.invalid/rest/api/3/myself") {
		t.Fatalf("request URL = %q", network.request.URL)
	}
}

func TestIssueGetUsesDiscoveredCanonicalWebURL(t *testing.T) {
	network := &sequenceNetwork{responses: []systemkit.HTTPResponse{
		{
			StatusCode: 200,
			Headers:    map[string][]string{"Content-Type": {"application/json"}},
			Body:       []byte(`[{"id":"cloud-1","url":"https://example.atlassian.invalid","name":"Company"}]`),
		},
		{
			StatusCode: 200,
			Headers:    map[string][]string{"Content-Type": {"application/json"}},
			Body:       []byte(`{"id":"48997","key":"DEV-380","self":"https://api.atlassian.com/ex/jira/cloud-1/rest/api/3/issue/48997","fields":{"summary":"Native Jira","status":{"name":"Open"},"description":"Useful"}}`),
		},
	}}
	plugin := newTestPlugin(t, network, map[string]string{"JIRA_TOKEN": "jira-token"})
	plugin.ref = resource.PluginRef{Name: Name, Instance: "main"}
	plugin.cfg = atlassian.Config{CloudID: "cloud-1", Auth: atlassian.AuthConfig{Method: atlassian.TokenMethod}}
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
	if record.URL != "https://example.atlassian.invalid/browse/DEV-380" {
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
	network := &sequenceNetwork{responses: []systemkit.HTTPResponse{
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
	plugin := newTestPlugin(t, network, map[string]string{"JIRA_TOKEN": "jira-token"})
	plugin.ref = resource.PluginRef{Name: Name, Instance: "main"}
	plugin.cfg = atlassian.Config{CloudID: "cloud-1", Auth: atlassian.AuthConfig{Method: atlassian.TokenMethod}}
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
	network := &recordingNetwork{response: systemkit.HTTPResponse{
		StatusCode: 200,
		Headers:    map[string][]string{"Content-Type": {"application/json"}},
		Body:       []byte(`{"id":"10000","key":"DEV","name":"Development","projectTypeKey":"software","self":"https://api.example/rest/api/3/project/10000"}`),
	}}
	plugin := newTestPlugin(t, network, map[string]string{"JIRA_TOKEN": "jira-token"})
	plugin.ref = resource.PluginRef{Name: Name, Instance: "main"}
	plugin.cfg = atlassian.Config{CloudID: "cloud-1", SiteURL: "https://example.atlassian.invalid", Auth: atlassian.AuthConfig{Method: atlassian.TokenMethod}}
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
	if record.URL != "https://example.atlassian.invalid/browse/DEV" {
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
	fpsystemtest.UnsupportedNetwork
	request  systemkit.HTTPRequest
	response systemkit.HTTPResponse
}

func (n *recordingNetwork) DoHTTP(_ context.Context, req systemkit.HTTPRequest) (systemkit.HTTPResponse, error) {
	n.request = req
	return n.response, nil
}

type sequenceNetwork struct {
	fpsystemtest.UnsupportedNetwork
	requests  []systemkit.HTTPRequest
	responses []systemkit.HTTPResponse
}

func (n *sequenceNetwork) DoHTTP(_ context.Context, req systemkit.HTTPRequest) (systemkit.HTTPResponse, error) {
	n.requests = append(n.requests, req)
	if len(n.responses) == 0 {
		return systemkit.HTTPResponse{StatusCode: 500, Body: []byte(`unexpected request`)}, nil
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
