package gitlab

import (
	"context"
	"encoding/base64"
	"regexp"
	"strconv"
	"strings"
	"testing"

	coredatasource "github.com/fluxplane/agentruntime/core/datasource"
	coreoperation "github.com/fluxplane/agentruntime/core/operation"
	"github.com/fluxplane/agentruntime/core/policy"
	"github.com/fluxplane/agentruntime/core/resource"
	coresecret "github.com/fluxplane/agentruntime/core/secret"
	coreuser "github.com/fluxplane/agentruntime/core/user"
	"github.com/fluxplane/agentruntime/orchestration/identity"
	"github.com/fluxplane/agentruntime/orchestration/pluginhost"
	"github.com/fluxplane/agentruntime/runtime/datasource/semantic"
	"github.com/fluxplane/agentruntime/runtime/system"
	gitlab "gitlab.com/gitlab-org/api/client-go/v2"
)

func TestPluginContributesNamedGitLabMROperation(t *testing.T) {
	bundle, err := New(nil).Contributions(context.Background(), pluginhost.Context{Ref: resource.PluginRef{Name: Name, Instance: "company-a"}})
	if err != nil {
		t.Fatalf("Contributions: %v", err)
	}
	if len(bundle.Operations) != 1 {
		t.Fatalf("operations len = %d, want 1", len(bundle.Operations))
	}
	if got := string(bundle.Operations[0].Ref.Name); got != "company_a_mr" {
		t.Fatalf("operation name = %q, want company_a_mr", got)
	}
	if bundle.Operations[0].Semantics.Risk != coreoperation.RiskHigh {
		t.Fatalf("operation risk = %s, want high", bundle.Operations[0].Semantics.Risk)
	}
}

func TestMergeRequestDetectorKeepsNestedProjectPath(t *testing.T) {
	entity := mergeRequestEntitySpec()
	if len(entity.Detectors) != 1 {
		t.Fatalf("detectors len = %d, want 1", len(entity.Detectors))
	}
	matches := regexp.MustCompile(entity.Detectors[0].Pattern).FindStringSubmatch("https://gitlab.example.com/ai/agents/slack-bot/-/merge_requests/2310")
	if len(matches) != 3 {
		t.Fatalf("matches = %#v, want project path and iid captures", matches)
	}
	if matches[1] != "ai/agents/slack-bot" || matches[2] != "2310" {
		t.Fatalf("captures = %#v, want nested project path and iid", matches)
	}
}

func TestExternalIdentityResolverUsesConfiguredGitLabIdentity(t *testing.T) {
	resolvers, err := New(nil).ExternalIdentityResolvers(context.Background(), pluginhost.Context{Ref: resource.PluginRef{Name: Name, Instance: "main"}})
	if err != nil {
		t.Fatalf("ExternalIdentityResolvers: %v", err)
	}
	result, err := resolvers[0].ResolveExternalIdentities(context.Background(), identity.ExternalRequest{Actor: coreuser.Actor{
		User: coreuser.User{
			ID:         "timo@company.org",
			Identities: []coreuser.Identity{{Provider: "gitlab/main", ProviderID: "tfriedl"}},
		},
		Identity:   coreuser.Identity{Provider: "slack", ProviderID: "U123"},
		Resolution: coreuser.ResolutionResolved,
	}})
	if err != nil {
		t.Fatalf("ResolveExternalIdentities: %v", err)
	}
	if len(result.Identities) != 1 || result.Identities[0].Provider != "gitlab/main" || result.Identities[0].ProviderID != "tfriedl" {
		t.Fatalf("identities = %#v, want configured gitlab identity", result.Identities)
	}
}

func TestExternalIdentityResolverLooksUpGitLabUserByCanonicalEmail(t *testing.T) {
	plugin := New(fakeSystem{})
	plugin.clientFactory = func(context.Context, system.System, resource.PluginRef, Config) (gitlabClient, error) {
		return fakeGitLabClient{users: []*gitlab.User{{ID: 42, Username: "tfriedl", Name: "Timo Friedl"}}}, nil
	}
	resolvers, err := plugin.ExternalIdentityResolvers(context.Background(), pluginhost.Context{Ref: resource.PluginRef{Name: Name, Instance: "main"}})
	if err != nil {
		t.Fatalf("ExternalIdentityResolvers: %v", err)
	}
	result, err := resolvers[0].ResolveExternalIdentities(context.Background(), identity.ExternalRequest{Actor: coreuser.Actor{
		User:       coreuser.User{ID: "timo@company.org"},
		Identity:   coreuser.Identity{Provider: "slack", ProviderID: "U123"},
		Resolution: coreuser.ResolutionResolved,
	}})
	if err != nil {
		t.Fatalf("ResolveExternalIdentities: %v", err)
	}
	if len(result.Identities) != 1 || result.Identities[0].Provider != "gitlab/main" || result.Identities[0].ProviderID != "tfriedl" {
		t.Fatalf("identities = %#v, want looked-up gitlab identity", result.Identities)
	}
	if result.Identities[0].Claims["gitlab_id"] != "42" {
		t.Fatalf("claims = %#v, want gitlab id", result.Identities[0].Claims)
	}
}

func TestExternalIdentityResolverLooksUpGitLabUserByVerifiedEmailAlias(t *testing.T) {
	var queries []string
	plugin := New(fakeSystem{})
	plugin.clientFactory = func(context.Context, system.System, resource.PluginRef, Config) (gitlabClient, error) {
		return fakeGitLabClient{
			usersByPublicEmail: map[string][]*gitlab.User{
				"timo@company.org": []*gitlab.User{{ID: 42, Username: "tfriedl", Name: "Timo Friedl"}},
			},
			userPublicEmailQueries: &queries,
		}, nil
	}
	resolvers, err := plugin.ExternalIdentityResolvers(context.Background(), pluginhost.Context{Ref: resource.PluginRef{Name: Name, Instance: "main"}})
	if err != nil {
		t.Fatalf("ExternalIdentityResolvers: %v", err)
	}
	result, err := resolvers[0].ResolveExternalIdentities(context.Background(), identity.ExternalRequest{Actor: coreuser.Actor{
		User: coreuser.User{
			ID: "timo.friedl@company.org",
			Emails: []coreuser.Email{
				{Address: "timo.friedl@company.org", Primary: true, Verified: true},
				{Address: "timo@company.org", Verified: true},
				{Address: "private@company.org"},
			},
		},
		Identity:   coreuser.Identity{Provider: "slack", ProviderID: "U123"},
		Resolution: coreuser.ResolutionResolved,
	}})
	if err != nil {
		t.Fatalf("ResolveExternalIdentities: %v", err)
	}
	if len(result.Identities) != 1 || result.Identities[0].ProviderID != "tfriedl" || result.Identities[0].Email != "timo@company.org" {
		t.Fatalf("identities = %#v, want looked-up gitlab identity from verified alias", result.Identities)
	}
	if strings.Join(queries, ",") != "timo.friedl@company.org,timo@company.org" {
		t.Fatalf("queries = %#v, want primary then verified alias only", queries)
	}
}

func TestExternalIdentityResolverReturnsNoIdentityWhenGitLabEmailIsPrivate(t *testing.T) {
	plugin := New(fakeSystem{})
	plugin.clientFactory = func(context.Context, system.System, resource.PluginRef, Config) (gitlabClient, error) {
		return fakeGitLabClient{usersByPublicEmail: map[string][]*gitlab.User{}}, nil
	}
	resolvers, err := plugin.ExternalIdentityResolvers(context.Background(), pluginhost.Context{Ref: resource.PluginRef{Name: Name, Instance: "main"}})
	if err != nil {
		t.Fatalf("ExternalIdentityResolvers: %v", err)
	}
	result, err := resolvers[0].ResolveExternalIdentities(context.Background(), identity.ExternalRequest{Actor: coreuser.Actor{
		User: coreuser.User{
			ID:     "timo.friedl@company.org",
			Emails: []coreuser.Email{{Address: "timo.friedl@company.org", Primary: true, Verified: true}},
		},
		Identity:   coreuser.Identity{Provider: "slack", ProviderID: "U123"},
		Resolution: coreuser.ResolutionResolved,
	}})
	if err != nil {
		t.Fatalf("ResolveExternalIdentities: %v", err)
	}
	if len(result.Identities) != 0 {
		t.Fatalf("identities = %#v, want none without public/configured GitLab email", result.Identities)
	}
}

func TestPluginDeclaresAuthMethods(t *testing.T) {
	plugin := New(fakeSystem{})
	plugin.cfg = Config{Auth: AuthConfig{TokenEnv: gitlabPersonalAccessTokenEnv}}
	methods, err := plugin.AuthMethods(context.Background(), pluginhost.Context{Ref: resource.PluginRef{Name: Name, Instance: "company-a"}})
	if err != nil {
		t.Fatalf("AuthMethods: %v", err)
	}
	if len(methods) != 2 {
		t.Fatalf("methods len = %d, want 2", len(methods))
	}
	method := methods[0]
	if method.Name != personalAccessTokenMethod || method.Method != coresecret.AuthMethodStored || method.Kind != coresecret.KindAPIKey {
		t.Fatalf("method = %#v", method)
	}
	if method.Secret.ResourceName() != "plugin/gitlab/company-a/token" {
		t.Fatalf("token secret = %#v", method.Secret)
	}
	if method.Env.Name != gitlabPersonalAccessTokenEnv {
		t.Fatalf("env name = %q", method.Env.Name)
	}
	if len(method.Env.Aliases) != 0 {
		t.Fatalf("env aliases = %#v, want none for configured env", method.Env.Aliases)
	}
	if method.Header.Name != "Private-Token" {
		t.Fatalf("header = %#v", method.Header)
	}
	if len(method.SetupFields) != 2 {
		t.Fatalf("setup fields = %#v, want token and url", method.SetupFields)
	}
	if method.SetupFields[0].Name != gitlabTokenField || !method.SetupFields[0].Required || !method.SetupFields[0].Sensitive || method.SetupFields[0].Env.Name != gitlabPersonalAccessTokenEnv {
		t.Fatalf("token field = %#v", method.SetupFields[0])
	}
	if method.SetupFields[1].Name != gitlabURLField || !method.SetupFields[1].Required || method.SetupFields[1].Sensitive || method.SetupFields[1].Env.Name != gitlabURLEnv {
		t.Fatalf("url field = %#v", method.SetupFields[1])
	}
	oauth := methods[1]
	if oauth.Name != oauth2Method || oauth.Method != "oauth2" || oauth.Kind != "oauth2_token" {
		t.Fatalf("oauth method = %#v", oauth)
	}
	if oauth.Secret.ResourceName() != "plugin/gitlab/company-a/oauth2_token" {
		t.Fatalf("oauth secret = %#v", oauth.Secret)
	}
	if oauth.OAuth2.TokenURL != defaultBaseURL+"/oauth/token" || len(oauth.OAuth2.Scopes) != 1 || oauth.OAuth2.Scopes[0] != "api" {
		t.Fatalf("oauth2 = %#v", oauth.OAuth2)
	}
}

func TestConnectionReportsCurrentGitLabUser(t *testing.T) {
	ref := resource.PluginRef{Name: Name, Instance: "company-a"}
	var calls []string
	plugin := New(fakeSystem{
		env: fakeEnvironment{values: map[string]string{gitlabPersonalAccessTokenEnv: "secret-token", gitlabURLEnv: "https://gitlab.example"}},
	})
	plugin.clientFactory = func(context.Context, system.System, resource.PluginRef, Config) (gitlabClient, error) {
		return fakeGitLabClient{
			currentUser: &gitlab.User{
				ID:       42,
				Username: "tfriedl",
				Name:     "Timo Friedl",
				State:    "active",
				WebURL:   "https://gitlab.example/tfriedl",
			},
			calls: &calls,
		}, nil
	}
	reports := make(chan pluginhost.AuthTestReport, 1)
	if err := plugin.TestConnection(context.Background(), pluginhost.Context{Ref: ref}, pluginhost.AuthTestRequest{Ref: ref, Method: personalAccessTokenMethod}, reports); err != nil {
		t.Fatalf("TestConnection: %v", err)
	}
	close(reports)
	report := <-reports
	if report.Plugin != Name || report.Instance != "company-a" || report.Method != personalAccessTokenMethod {
		t.Fatalf("report target = %#v", report)
	}
	if report.Check != "current_user" || report.Status != "ok" || report.Message != "tfriedl" {
		t.Fatalf("report result = %#v", report)
	}
	if report.Details["id"] != "42" || report.Details["username"] != "tfriedl" || report.Details["state"] != "active" {
		t.Fatalf("report details = %#v", report.Details)
	}
	if strings.Join(calls, ",") != "current_user" {
		t.Fatalf("calls = %#v, want current_user", calls)
	}
}

func TestPluginMROperationUsesInjectedClient(t *testing.T) {
	plugin := New(fakeSystem{})
	plugin.ref = resource.PluginRef{Name: Name, Instance: "company-a"}
	plugin.clientFactory = func(context.Context, system.System, resource.PluginRef, Config) (gitlabClient, error) {
		return &fakeGitLabClient{updatedMR: &gitlab.MergeRequest{BasicMergeRequest: gitlab.BasicMergeRequest{
			ID:        42,
			IID:       7,
			ProjectID: 12,
			Title:     "Runtime MR",
			State:     "closed",
			WebURL:    "https://gitlab.example/fluxplane/runtime/-/merge_requests/7",
		}}}, nil
	}
	ops, err := plugin.Operations(context.Background(), pluginhost.Context{})
	if err != nil {
		t.Fatalf("Operations: %v", err)
	}
	op := findOperation(ops, "company_a_mr")
	if op == nil {
		t.Fatalf("company_a_mr operation not found")
	}
	result := op.Run(coreoperation.NewContext(context.Background(), nil), map[string]any{"op": "close", "project_id": "12", "merge_request_iid": 7})
	if result.Status != coreoperation.StatusOK {
		t.Fatalf("operation status = %s, error = %#v", result.Status, result.Error)
	}
	output, ok := result.Output.(MRActionResult)
	if !ok {
		t.Fatalf("output type = %T, want MRActionResult", result.Output)
	}
	if output.MergeRequestIID != 7 || output.State != "closed" {
		t.Fatalf("output = %#v, want closed MR !7", output)
	}
}

func TestOfficialClientUsesSystemNetworkAndPersonalAccessTokenEnv(t *testing.T) {
	network := &recordingNetwork{
		response: system.HTTPResponse{
			StatusCode: 200,
			Headers:    map[string][]string{"Content-Type": {"application/json"}},
			Body:       []byte(`[{"id":12,"name":"runtime","path_with_namespace":"fluxplane/runtime","web_url":"https://gitlab.example/fluxplane/runtime"}]`),
		},
	}
	client, err := newOfficialClient(context.Background(), fakeSystem{
		network: network,
		env:     fakeEnvironment{values: map[string]string{gitlabPersonalAccessTokenEnv: "secret-token"}},
	}, resource.PluginRef{Name: Name, Instance: "company-a"}, Config{
		BaseURL: "https://gitlab.example",
		Auth:    AuthConfig{TokenEnv: gitlabPersonalAccessTokenEnv},
	})
	if err != nil {
		t.Fatalf("newOfficialClient: %v", err)
	}
	query := "runtime"
	projects, err := client.ListProjects(context.Background(), &gitlab.ListProjectsOptions{Search: &query})
	if err != nil {
		t.Fatalf("ListProjects: %v", err)
	}
	if len(projects) != 1 || projects[0].PathWithNamespace != "fluxplane/runtime" {
		t.Fatalf("projects = %#v", projects)
	}
	if !strings.HasPrefix(network.request.URL, "https://gitlab.example/api/v4/projects") {
		t.Fatalf("request URL = %q", network.request.URL)
	}
	if got := headerValue(network.request.Headers, "Private-Token"); got != "secret-token" {
		t.Fatalf("private token header = %q, want secret-token", got)
	}
}

func TestOfficialClientProbesAliasesWhenTokenEnvUnset(t *testing.T) {
	network := &recordingNetwork{response: system.HTTPResponse{
		StatusCode: 200,
		Headers:    map[string][]string{"Content-Type": {"application/json"}},
		Body:       []byte(`[]`),
	}}
	client, err := newOfficialClient(context.Background(), fakeSystem{
		network: network,
		env:     fakeEnvironment{values: map[string]string{gitlabTokenEnv: "fallback-token"}},
	}, resource.PluginRef{Name: Name, Instance: "company-a"}, Config{
		BaseURL: "https://gitlab.example",
	})
	if err != nil {
		t.Fatalf("newOfficialClient: %v", err)
	}
	query := "runtime"
	if _, err := client.ListProjects(context.Background(), &gitlab.ListProjectsOptions{Search: &query}); err != nil {
		t.Fatalf("ListProjects: %v", err)
	}
	if got := headerValue(network.request.Headers, "Private-Token"); got != "fallback-token" {
		t.Fatalf("private token header = %q, want fallback-token", got)
	}
}

func TestOfficialClientUsesGitLabURLEnv(t *testing.T) {
	network := &recordingNetwork{response: system.HTTPResponse{
		StatusCode: 200,
		Headers:    map[string][]string{"Content-Type": {"application/json"}},
		Body:       []byte(`[]`),
	}}
	client, err := newOfficialClient(context.Background(), fakeSystem{
		network: network,
		env: fakeEnvironment{values: map[string]string{
			gitlabTokenEnv: "fallback-token",
			gitlabURLEnv:   "gitlab.example",
		}},
	}, resource.PluginRef{Name: Name, Instance: "company-a"}, Config{})
	if err != nil {
		t.Fatalf("newOfficialClient: %v", err)
	}
	query := "runtime"
	if _, err := client.ListProjects(context.Background(), &gitlab.ListProjectsOptions{Search: &query}); err != nil {
		t.Fatalf("ListProjects: %v", err)
	}
	if !strings.HasPrefix(network.request.URL, "https://gitlab.example/api/v4/projects") {
		t.Fatalf("request URL = %q", network.request.URL)
	}
}

func TestOfficialClientConfiguredTokenEnvDoesNotProbeAliases(t *testing.T) {
	network := &recordingNetwork{response: system.HTTPResponse{
		StatusCode: 200,
		Headers:    map[string][]string{"Content-Type": {"application/json"}},
		Body:       []byte(`[]`),
	}}
	_, err := newOfficialClient(context.Background(), fakeSystem{
		network: network,
		env:     fakeEnvironment{values: map[string]string{gitlabTokenEnv: "fallback-token"}},
	}, resource.PluginRef{Name: Name, Instance: "company-a"}, Config{
		BaseURL: "https://gitlab.example",
		Auth:    AuthConfig{TokenEnv: gitlabPersonalAccessTokenEnv},
	})
	if err == nil || !strings.Contains(err.Error(), "set "+gitlabPersonalAccessTokenEnv) {
		t.Fatalf("newOfficialClient error = %v, want configured env missing", err)
	}
}

func TestOfficialClientRequiresSecretUseForTokenEnv(t *testing.T) {
	_, err := newOfficialClient(denySecretUseContext(), fakeSystem{
		network: &recordingNetwork{},
		env:     fakeEnvironment{values: map[string]string{gitlabPersonalAccessTokenEnv: "secret-token"}},
	}, resource.PluginRef{Name: Name, Instance: "company-a"}, Config{
		BaseURL: "https://gitlab.example",
		Auth:    AuthConfig{TokenEnv: gitlabPersonalAccessTokenEnv},
	})
	if err == nil || !strings.Contains(err.Error(), "authorization_deny") {
		t.Fatalf("newOfficialClient error = %v, want authorization deny", err)
	}
}

func TestDatasourceProviderSearchesProjects(t *testing.T) {
	provider := gitlabDatasourceProvider{
		system: fakeSystem{},
		ref:    resource.PluginRef{Name: Name, Instance: "company-a"},
		clientFactory: func(context.Context, system.System, resource.PluginRef, Config) (gitlabClient, error) {
			return fakeGitLabClient{projects: []*gitlab.Project{
				{
					ID:                12,
					Name:              "runtime",
					PathWithNamespace: "fluxplane/runtime",
					Description:       "Runtime repository",
					WebURL:            "https://gitlab.example/fluxplane/runtime",
				},
			}}, nil
		},
	}
	accessor, err := provider.Open(context.Background(), coredatasource.Spec{
		Name:     "company-a-gitlab",
		Kind:     Name,
		Entities: []coredatasource.EntityType{ProjectEntity},
		Config:   map[string]string{"instance": "company-a"},
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	searcher, ok := accessor.(coredatasource.Searcher)
	if !ok {
		t.Fatalf("accessor does not implement datasource.Searcher")
	}
	result, err := searcher.Search(context.Background(), coredatasource.SearchRequest{Entity: ProjectEntity, Query: "runtime"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(result.Records) != 1 || result.Records[0].ID != "12" || result.Records[0].Title != "fluxplane/runtime" || result.Records[0].Metadata["project_id"] != "12" {
		t.Fatalf("records = %#v", result.Records)
	}
}

func TestDatasourceProviderIndexedSearchUsesFieldIndex(t *testing.T) {
	index, err := semantic.New(semantic.HashEmbedder{ModelName: "test-embedding"}, semantic.NewJSONStore(""), semantic.Config{})
	if err != nil {
		t.Fatalf("semantic.New: %v", err)
	}
	_, err = index.UpdateRecord(context.Background(), coredatasource.CorpusDocument{
		Ref:   coredatasource.RecordRef{Datasource: "company-a-gitlab", Entity: ProjectEntity, ID: "12"},
		Title: "fluxplane/runtime",
		Body:  "Runtime repository for agent execution",
		Metadata: map[string]string{
			"id":                  "12",
			"name":                "runtime",
			"path_with_namespace": "fluxplane/runtime",
			"archived":            "false",
		},
	}, projectEntitySpec())
	if err != nil {
		t.Fatalf("UpdateRecord: %v", err)
	}
	calls := []string{}
	provider := gitlabDatasourceProvider{
		system: fakeSystem{},
		ref:    resource.PluginRef{Name: Name, Instance: "company-a"},
		clientFactory: func(context.Context, system.System, resource.PluginRef, Config) (gitlabClient, error) {
			return fakeGitLabClient{
				calls: &calls,
				projects: []*gitlab.Project{{
					ID:                99,
					Name:              "live",
					PathWithNamespace: "fluxplane/live",
				}},
			}, nil
		},
	}
	provider = provider.WithSemanticIndex(index).(gitlabDatasourceProvider)
	accessor, err := provider.Open(context.Background(), coredatasource.Spec{
		Name:     "company-a-gitlab",
		Kind:     Name,
		Entities: []coredatasource.EntityType{ProjectEntity},
		Config:   map[string]string{"instance": "company-a"},
		Index:    coredatasource.IndexSpec{Enabled: true},
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	result, err := accessor.(coredatasource.Searcher).Search(context.Background(), coredatasource.SearchRequest{Entity: ProjectEntity, Query: "fluxplane/runtime"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(calls) != 0 {
		t.Fatalf("GitLab API calls = %#v, want none", calls)
	}
	if len(result.Records) != 1 || result.Records[0].ID != "12" {
		t.Fatalf("records = %#v, want indexed runtime project", result.Records)
	}
}

func TestDatasourceProviderIndexedSearchReportsMissingIndex(t *testing.T) {
	index, err := semantic.New(semantic.HashEmbedder{ModelName: "test-embedding"}, semantic.NewJSONStore(""), semantic.Config{})
	if err != nil {
		t.Fatalf("semantic.New: %v", err)
	}
	calls := []string{}
	provider := gitlabDatasourceProvider{
		system: fakeSystem{},
		ref:    resource.PluginRef{Name: Name, Instance: "company-a"},
		clientFactory: func(context.Context, system.System, resource.PluginRef, Config) (gitlabClient, error) {
			return fakeGitLabClient{calls: &calls}, nil
		},
	}
	provider = provider.WithSemanticIndex(index).(gitlabDatasourceProvider)
	accessor, err := provider.Open(context.Background(), coredatasource.Spec{
		Name:     "company-a-gitlab",
		Kind:     Name,
		Entities: []coredatasource.EntityType{ProjectEntity},
		Config:   map[string]string{"instance": "company-a"},
		Index:    coredatasource.IndexSpec{Enabled: true},
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	_, err = accessor.(coredatasource.Searcher).Search(context.Background(), coredatasource.SearchRequest{Entity: ProjectEntity, Query: "runtime"})
	if err == nil || !strings.Contains(err.Error(), "index is not built") {
		t.Fatalf("Search error = %v, want missing index", err)
	}
	if len(calls) != 0 {
		t.Fatalf("GitLab API calls = %#v, want none", calls)
	}
}

func TestDatasourceProviderIndexedSearchFallsBackForNonIndexedEntity(t *testing.T) {
	index, err := semantic.New(semantic.HashEmbedder{ModelName: "test-embedding"}, semantic.NewJSONStore(""), semantic.Config{})
	if err != nil {
		t.Fatalf("semantic.New: %v", err)
	}
	calls := []string{}
	provider := gitlabDatasourceProvider{
		system: fakeSystem{},
		ref:    resource.PluginRef{Name: Name, Instance: "company-a"},
		clientFactory: func(context.Context, system.System, resource.PluginRef, Config) (gitlabClient, error) {
			return fakeGitLabClient{
				calls: &calls,
				commits: []*gitlab.Commit{{
					ID:      "abc123",
					ShortID: "abc123",
					Title:   "Fix runtime",
				}},
			}, nil
		},
	}
	provider = provider.WithSemanticIndex(index).(gitlabDatasourceProvider)
	accessor, err := provider.Open(context.Background(), coredatasource.Spec{
		Name:     "company-a-gitlab",
		Kind:     Name,
		Entities: []coredatasource.EntityType{CommitEntity},
		Config:   map[string]string{"instance": "company-a"},
		Index:    coredatasource.IndexSpec{Enabled: true},
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	result, err := accessor.(coredatasource.Searcher).Search(context.Background(), coredatasource.SearchRequest{
		Entity:  CommitEntity,
		Query:   "runtime",
		Filters: map[string]string{"project_id": "engineering/runtime"},
	})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if !containsCall(calls, "list_commits") {
		t.Fatalf("GitLab API calls = %#v, want live commit lookup", calls)
	}
	if len(result.Records) != 1 || result.Records[0].ID != "engineering/runtime!abc123" {
		t.Fatalf("records = %#v, want live commit record", result.Records)
	}
}

func TestDatasourceProviderSearchesUsers(t *testing.T) {
	calls := []string{}
	provider := gitlabDatasourceProvider{
		system: fakeSystem{},
		ref:    resource.PluginRef{Name: Name, Instance: "company-a"},
		clientFactory: func(context.Context, system.System, resource.PluginRef, Config) (gitlabClient, error) {
			return fakeGitLabClient{
				calls: &calls,
				users: []*gitlab.User{{
					ID:       42,
					Username: "tfriedl",
					Name:     "Timo Friedl",
					State:    "active",
					WebURL:   "https://gitlab.example/tfriedl",
				}},
			}, nil
		},
	}
	accessor, err := provider.Open(context.Background(), coredatasource.Spec{
		Name:     "company-a-gitlab",
		Kind:     Name,
		Entities: []coredatasource.EntityType{UserEntity},
		Config:   map[string]string{"instance": "company-a"},
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	result, err := accessor.(coredatasource.Searcher).Search(context.Background(), coredatasource.SearchRequest{Entity: UserEntity, Query: "timo"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if strings.Join(calls, ",") != "list_users" {
		t.Fatalf("calls = %#v, want list_users", calls)
	}
	if len(result.Records) != 1 || result.Records[0].ID != "42" || result.Records[0].Metadata["username"] != "tfriedl" {
		t.Fatalf("records = %#v, want indexed GitLab user", result.Records)
	}
}

func TestDatasourceProviderIndexedSearchUsesUserFieldIndex(t *testing.T) {
	index, err := semantic.New(semantic.HashEmbedder{ModelName: "test-embedding"}, semantic.NewJSONStore(""), semantic.Config{})
	if err != nil {
		t.Fatalf("semantic.New: %v", err)
	}
	_, err = index.UpdateRecord(context.Background(), coredatasource.CorpusDocument{
		Ref:   coredatasource.RecordRef{Datasource: "company-a-gitlab", Entity: UserEntity, ID: "42"},
		Title: "tfriedl",
		Body:  "Timo Friedl tfriedl",
		Metadata: map[string]string{
			"id":       "42",
			"username": "tfriedl",
			"name":     "Timo Friedl",
			"state":    "active",
			"web_url":  "https://gitlab.example/tfriedl",
		},
	}, userEntitySpec())
	if err != nil {
		t.Fatalf("UpdateRecord: %v", err)
	}
	calls := []string{}
	provider := gitlabDatasourceProvider{
		system: fakeSystem{},
		ref:    resource.PluginRef{Name: Name, Instance: "company-a"},
		clientFactory: func(context.Context, system.System, resource.PluginRef, Config) (gitlabClient, error) {
			return fakeGitLabClient{calls: &calls}, nil
		},
	}
	provider = provider.WithSemanticIndex(index).(gitlabDatasourceProvider)
	accessor, err := provider.Open(context.Background(), coredatasource.Spec{
		Name:     "company-a-gitlab",
		Kind:     Name,
		Entities: []coredatasource.EntityType{UserEntity},
		Config:   map[string]string{"instance": "company-a"},
		Index:    coredatasource.IndexSpec{Enabled: true},
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	result, err := accessor.(coredatasource.Searcher).Search(context.Background(), coredatasource.SearchRequest{Entity: UserEntity, Query: "tfriedl"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(calls) != 0 {
		t.Fatalf("GitLab API calls = %#v, want none", calls)
	}
	if len(result.Records) != 1 || result.Records[0].ID != "42" || result.Records[0].Entity != UserEntity {
		t.Fatalf("records = %#v, want indexed GitLab user", result.Records)
	}
}

func TestDatasourceProviderCorpusIncludesUsers(t *testing.T) {
	provider := gitlabDatasourceProvider{
		system: fakeSystem{},
		ref:    resource.PluginRef{Name: Name, Instance: "company-a"},
		clientFactory: func(context.Context, system.System, resource.PluginRef, Config) (gitlabClient, error) {
			return fakeGitLabClient{users: []*gitlab.User{{
				ID:       42,
				Username: "tfriedl",
				Name:     "Timo Friedl",
				State:    "active",
				WebURL:   "https://gitlab.example/tfriedl",
			}}}, nil
		},
	}
	accessor, err := provider.Open(context.Background(), coredatasource.Spec{
		Name:     "company-a-gitlab",
		Kind:     Name,
		Entities: []coredatasource.EntityType{UserEntity},
		Config:   map[string]string{"instance": "company-a"},
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	page, err := accessor.(coredatasource.CorpusProvider).Corpus(context.Background(), coredatasource.CorpusRequest{Entity: UserEntity})
	if err != nil {
		t.Fatalf("Corpus: %v", err)
	}
	if len(page.Documents) != 1 || page.Documents[0].Ref.ID != "42" || page.Documents[0].Metadata["username"] != "tfriedl" {
		t.Fatalf("documents = %#v, want GitLab user corpus document", page.Documents)
	}
}

func TestDatasourceProviderGetsUser(t *testing.T) {
	provider := gitlabDatasourceProvider{
		system: fakeSystem{},
		ref:    resource.PluginRef{Name: Name, Instance: "company-a"},
		clientFactory: func(context.Context, system.System, resource.PluginRef, Config) (gitlabClient, error) {
			return fakeGitLabClient{users: []*gitlab.User{{
				ID:       42,
				Username: "tfriedl",
				Name:     "Timo Friedl",
			}}}, nil
		},
	}
	accessor, err := provider.Open(context.Background(), coredatasource.Spec{
		Name:     "company-a-gitlab",
		Kind:     Name,
		Entities: []coredatasource.EntityType{UserEntity},
		Config:   map[string]string{"instance": "company-a"},
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	record, err := accessor.(coredatasource.Getter).Get(context.Background(), coredatasource.GetRequest{Entity: UserEntity, ID: "42"})
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if record.ID != "42" || record.Metadata["username"] != "tfriedl" {
		t.Fatalf("record = %#v, want GitLab user", record)
	}
}

func TestDatasourceProviderSearchesGroups(t *testing.T) {
	calls := []string{}
	provider := gitlabDatasourceProvider{
		system: fakeSystem{},
		ref:    resource.PluginRef{Name: Name, Instance: "company-a"},
		clientFactory: func(context.Context, system.System, resource.PluginRef, Config) (gitlabClient, error) {
			return fakeGitLabClient{
				calls: &calls,
				groups: []*gitlab.Group{{
					ID:          7,
					Name:        "Platform",
					Path:        "platform",
					FullPath:    "engineering/platform",
					FullName:    "Engineering / Platform",
					Description: "Platform group",
					WebURL:      "https://gitlab.example/groups/engineering/platform",
				}},
			}, nil
		},
	}
	accessor, err := provider.Open(context.Background(), coredatasource.Spec{
		Name:     "company-a-gitlab",
		Kind:     Name,
		Entities: []coredatasource.EntityType{GroupEntity},
		Config:   map[string]string{"instance": "company-a"},
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	result, err := accessor.(coredatasource.Searcher).Search(context.Background(), coredatasource.SearchRequest{Entity: GroupEntity, Query: "platform"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if strings.Join(calls, ",") != "list_groups" {
		t.Fatalf("calls = %#v, want list_groups", calls)
	}
	if len(result.Records) != 1 || result.Records[0].ID != "engineering/platform" || result.Records[0].Metadata["full_path"] != "engineering/platform" {
		t.Fatalf("records = %#v, want GitLab group", result.Records)
	}
}

func TestDatasourceProviderIndexedSearchUsesGroupFieldIndex(t *testing.T) {
	index, err := semantic.New(semantic.HashEmbedder{ModelName: "test-embedding"}, semantic.NewJSONStore(""), semantic.Config{})
	if err != nil {
		t.Fatalf("semantic.New: %v", err)
	}
	_, err = index.UpdateRecord(context.Background(), coredatasource.CorpusDocument{
		Ref:   coredatasource.RecordRef{Datasource: "company-a-gitlab", Entity: GroupEntity, ID: "engineering/platform"},
		Title: "engineering/platform",
		Body:  "Engineering Platform",
		Metadata: map[string]string{
			"id":        "7",
			"name":      "Platform",
			"path":      "platform",
			"full_path": "engineering/platform",
			"full_name": "Engineering / Platform",
		},
	}, groupEntitySpec())
	if err != nil {
		t.Fatalf("UpdateRecord: %v", err)
	}
	calls := []string{}
	provider := gitlabDatasourceProvider{
		system: fakeSystem{},
		ref:    resource.PluginRef{Name: Name, Instance: "company-a"},
		clientFactory: func(context.Context, system.System, resource.PluginRef, Config) (gitlabClient, error) {
			return fakeGitLabClient{calls: &calls}, nil
		},
	}
	provider = provider.WithSemanticIndex(index).(gitlabDatasourceProvider)
	accessor, err := provider.Open(context.Background(), coredatasource.Spec{
		Name:     "company-a-gitlab",
		Kind:     Name,
		Entities: []coredatasource.EntityType{GroupEntity},
		Config:   map[string]string{"instance": "company-a"},
		Index:    coredatasource.IndexSpec{Enabled: true},
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	result, err := accessor.(coredatasource.Searcher).Search(context.Background(), coredatasource.SearchRequest{Entity: GroupEntity, Query: "engineering/platform"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(calls) != 0 {
		t.Fatalf("GitLab API calls = %#v, want none", calls)
	}
	if len(result.Records) != 1 || result.Records[0].ID != "engineering/platform" || result.Records[0].Entity != GroupEntity {
		t.Fatalf("records = %#v, want indexed GitLab group", result.Records)
	}
}

func TestDatasourceProviderCorpusIncludesGroups(t *testing.T) {
	provider := gitlabDatasourceProvider{
		system: fakeSystem{},
		ref:    resource.PluginRef{Name: Name, Instance: "company-a"},
		clientFactory: func(context.Context, system.System, resource.PluginRef, Config) (gitlabClient, error) {
			return fakeGitLabClient{groups: []*gitlab.Group{{
				ID:       7,
				Name:     "Platform",
				Path:     "platform",
				FullPath: "engineering/platform",
				FullName: "Engineering / Platform",
				WebURL:   "https://gitlab.example/groups/engineering/platform",
			}}}, nil
		},
	}
	accessor, err := provider.Open(context.Background(), coredatasource.Spec{
		Name:     "company-a-gitlab",
		Kind:     Name,
		Entities: []coredatasource.EntityType{GroupEntity},
		Config:   map[string]string{"instance": "company-a"},
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	page, err := accessor.(coredatasource.CorpusProvider).Corpus(context.Background(), coredatasource.CorpusRequest{Entity: GroupEntity})
	if err != nil {
		t.Fatalf("Corpus: %v", err)
	}
	if len(page.Documents) != 1 || page.Documents[0].Ref.ID != "engineering/platform" || page.Documents[0].Metadata["full_path"] != "engineering/platform" {
		t.Fatalf("documents = %#v, want GitLab group corpus document", page.Documents)
	}
}

func TestDatasourceProviderCorpusIncludesMemberships(t *testing.T) {
	provider := gitlabDatasourceProvider{
		system: fakeSystem{},
		ref:    resource.PluginRef{Name: Name, Instance: "company-a"},
		clientFactory: func(context.Context, system.System, resource.PluginRef, Config) (gitlabClient, error) {
			return fakeGitLabClient{
				groups: []*gitlab.Group{{
					ID:       7,
					Name:     "Platform",
					FullPath: "engineering/platform",
					FullName: "Engineering / Platform",
				}},
				descendantGroups: []*gitlab.Group{{
					ID:       8,
					Name:     "Core",
					FullPath: "engineering/platform/core",
					FullName: "Engineering / Platform / Core",
				}},
				groupMembers: []*gitlab.GroupMember{{
					ID:          42,
					Username:    "timo",
					Name:        "Timo",
					AccessLevel: gitlab.DeveloperPermissions,
				}},
				projects: []*gitlab.Project{{
					ID:                12,
					Name:              "Runtime",
					PathWithNamespace: "engineering/runtime",
					Archived:          true,
				}},
				projectMembers: []*gitlab.ProjectMember{{
					ID:          42,
					Username:    "timo",
					Name:        "Timo",
					AccessLevel: gitlab.MaintainerPermissions,
				}},
			}, nil
		},
	}
	accessor, err := provider.Open(context.Background(), coredatasource.Spec{
		Name:     "company-a-gitlab",
		Kind:     Name,
		Entities: []coredatasource.EntityType{MembershipEntity},
		Config:   map[string]string{"instance": "company-a"},
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	page, err := accessor.(coredatasource.CorpusProvider).Corpus(context.Background(), coredatasource.CorpusRequest{Entity: MembershipEntity})
	if err != nil {
		t.Fatalf("Corpus: %v", err)
	}
	if len(page.Documents) != 3 {
		t.Fatalf("documents = %#v, want visible group, descendant group, and project membership documents", page.Documents)
	}
	if page.Documents[0].Metadata["user_id"] != "42" || page.Documents[0].Metadata["source_type"] == "" {
		t.Fatalf("document metadata = %#v, want indexed membership fields", page.Documents[0].Metadata)
	}
	projectDoc := documentByID(page.Documents, "42:project:12")
	if projectDoc.Metadata["direct"] != "true" || projectDoc.Metadata["source_archived"] != "true" {
		t.Fatalf("project membership metadata = %#v, want direct archived project edge", projectDoc.Metadata)
	}
	if !hasDocumentID(page.Documents, "42:namespace:8") {
		t.Fatalf("documents = %#v, want descendant group membership document", page.Documents)
	}
}

func TestDatasourceProviderCorpusStreamsMemberships(t *testing.T) {
	calls := []string{}
	provider := gitlabDatasourceProvider{
		system: fakeSystem{},
		ref:    resource.PluginRef{Name: Name, Instance: "company-a"},
		clientFactory: func(context.Context, system.System, resource.PluginRef, Config) (gitlabClient, error) {
			return fakeGitLabClient{
				calls: &calls,
				groups: []*gitlab.Group{{
					ID:       7,
					Name:     "Platform",
					FullPath: "engineering/platform",
					FullName: "Engineering / Platform",
				}},
				groupMembers: []*gitlab.GroupMember{{
					ID:          42,
					Username:    "timo",
					Name:        "Timo",
					AccessLevel: gitlab.DeveloperPermissions,
				}, {
					ID:          43,
					Username:    "ana",
					Name:        "Ana",
					AccessLevel: gitlab.ReporterPermissions,
				}},
				projects: []*gitlab.Project{{
					ID:                12,
					Name:              "Runtime",
					PathWithNamespace: "engineering/runtime",
				}},
				projectMembers: []*gitlab.ProjectMember{{
					ID:          42,
					Username:    "timo",
					Name:        "Timo",
					AccessLevel: gitlab.MaintainerPermissions,
				}},
			}, nil
		},
	}
	accessor, err := provider.Open(context.Background(), coredatasource.Spec{
		Name:     "company-a-gitlab",
		Kind:     Name,
		Entities: []coredatasource.EntityType{MembershipEntity},
		Config:   map[string]string{"instance": "company-a"},
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	page, err := accessor.(coredatasource.CorpusProvider).Corpus(context.Background(), coredatasource.CorpusRequest{Entity: MembershipEntity, Limit: 1})
	if err != nil {
		t.Fatalf("Corpus: %v", err)
	}
	if len(page.Documents) != 1 || page.NextCursor == "" || page.Complete {
		t.Fatalf("page = %#v, want one streamed membership and next cursor", page)
	}
	if !containsCall(calls, "list_groups") || !containsCall(calls, "list_group_members") {
		t.Fatalf("calls = %#v, want group source and group member page", calls)
	}
	if containsCall(calls, "list_projects") || containsCall(calls, "list_project_members") {
		t.Fatalf("calls = %#v, want first page before project scan", calls)
	}
}

func TestDatasourceProviderListHidesArchivedProjectsByDefault(t *testing.T) {
	provider := gitlabDatasourceProvider{
		system: fakeSystem{},
		ref:    resource.PluginRef{Name: Name, Instance: "company-a"},
		clientFactory: func(context.Context, system.System, resource.PluginRef, Config) (gitlabClient, error) {
			return fakeGitLabClient{projects: []*gitlab.Project{{
				ID:                12,
				Name:              "Runtime",
				PathWithNamespace: "engineering/runtime",
			}, {
				ID:                13,
				Name:              "Old Runtime",
				PathWithNamespace: "engineering/old-runtime",
				Archived:          true,
			}}}, nil
		},
	}
	accessor, err := provider.Open(context.Background(), coredatasource.Spec{
		Name:     "company-a-gitlab",
		Kind:     Name,
		Entities: []coredatasource.EntityType{ProjectEntity},
		Config:   map[string]string{"instance": "company-a"},
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	lister := accessor.(coredatasource.Lister)
	defaults, err := lister.List(context.Background(), coredatasource.ListRequest{Entity: ProjectEntity})
	if err != nil {
		t.Fatalf("default List: %v", err)
	}
	if len(defaults.Records) != 1 || defaults.Records[0].ID != "12" || defaults.Records[0].Metadata["archived"] != "false" {
		t.Fatalf("default records = %#v, want only active project", defaults.Records)
	}
	archived, err := lister.List(context.Background(), coredatasource.ListRequest{Entity: ProjectEntity, Filters: map[string]string{"archived": "true"}})
	if err != nil {
		t.Fatalf("archived List: %v", err)
	}
	if len(archived.Records) != 1 || archived.Records[0].ID != "13" || archived.Records[0].Metadata["archived"] != "true" {
		t.Fatalf("archived records = %#v, want archived project", archived.Records)
	}
}

func TestDatasourceProviderListHidesArchivedProjectMembershipsByDefault(t *testing.T) {
	provider := gitlabDatasourceProvider{
		system: fakeSystem{},
		ref:    resource.PluginRef{Name: Name, Instance: "company-a"},
		clientFactory: func(context.Context, system.System, resource.PluginRef, Config) (gitlabClient, error) {
			return fakeGitLabClient{
				projects: []*gitlab.Project{{
					ID:                12,
					Name:              "Runtime",
					PathWithNamespace: "engineering/runtime",
				}, {
					ID:                13,
					Name:              "Old Runtime",
					PathWithNamespace: "engineering/old-runtime",
					Archived:          true,
				}},
				projectMembers: []*gitlab.ProjectMember{{
					ID:          42,
					Username:    "timo",
					Name:        "Timo",
					AccessLevel: gitlab.MaintainerPermissions,
				}},
			}, nil
		},
	}
	accessor, err := provider.Open(context.Background(), coredatasource.Spec{
		Name:     "company-a-gitlab",
		Kind:     Name,
		Entities: []coredatasource.EntityType{MembershipEntity},
		Config:   map[string]string{"instance": "company-a"},
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	lister := accessor.(coredatasource.Lister)
	defaults, err := lister.List(context.Background(), coredatasource.ListRequest{Entity: MembershipEntity, Filters: map[string]string{"user_id": "42"}})
	if err != nil {
		t.Fatalf("default List: %v", err)
	}
	if len(defaults.Records) != 1 || defaults.Records[0].ID != "42:project:12" || defaults.Records[0].Metadata["source_archived"] != "false" || defaults.Records[0].Metadata["direct"] != "true" {
		t.Fatalf("default records = %#v, want only active direct project membership", defaults.Records)
	}
	archived, err := lister.List(context.Background(), coredatasource.ListRequest{Entity: MembershipEntity, Filters: map[string]string{"user_id": "42", "source_archived": "true"}})
	if err != nil {
		t.Fatalf("archived List: %v", err)
	}
	if len(archived.Records) != 1 || archived.Records[0].ID != "42:project:13" || archived.Records[0].Metadata["source_archived"] != "true" {
		t.Fatalf("archived records = %#v, want archived project membership", archived.Records)
	}
}

func TestDatasourceProviderCorpusCachesMembershipSources(t *testing.T) {
	calls := []string{}
	provider := gitlabDatasourceProvider{
		system: fakeSystem{},
		ref:    resource.PluginRef{Name: Name, Instance: "company-a"},
		clientFactory: func(context.Context, system.System, resource.PluginRef, Config) (gitlabClient, error) {
			return fakeGitLabClient{
				calls: &calls,
				groups: []*gitlab.Group{{
					ID:       7,
					Name:     "Platform",
					FullPath: "engineering/platform",
					FullName: "Engineering / Platform",
				}},
				descendantGroups: []*gitlab.Group{{
					ID:       8,
					Name:     "Core",
					FullPath: "engineering/platform/core",
					FullName: "Engineering / Platform / Core",
				}},
				groupMembers: []*gitlab.GroupMember{{
					ID:          42,
					Username:    "timo",
					Name:        "Timo",
					AccessLevel: gitlab.DeveloperPermissions,
				}, {
					ID:          43,
					Username:    "ana",
					Name:        "Ana",
					AccessLevel: gitlab.ReporterPermissions,
				}},
			}, nil
		},
	}
	accessor, err := provider.Open(context.Background(), coredatasource.Spec{
		Name:     "company-a-gitlab",
		Kind:     Name,
		Entities: []coredatasource.EntityType{MembershipEntity},
		Config:   map[string]string{"instance": "company-a"},
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	first, err := accessor.(coredatasource.CorpusProvider).Corpus(context.Background(), coredatasource.CorpusRequest{Entity: MembershipEntity, Limit: 1})
	if err != nil {
		t.Fatalf("first Corpus: %v", err)
	}
	if first.NextCursor == "" {
		t.Fatalf("first page = %#v, want next cursor", first)
	}
	if _, err := accessor.(coredatasource.CorpusProvider).Corpus(context.Background(), coredatasource.CorpusRequest{Entity: MembershipEntity, Limit: 1, Cursor: first.NextCursor}); err != nil {
		t.Fatalf("second Corpus: %v", err)
	}
	if got := countCall(calls, "list_groups"); got != 1 {
		t.Fatalf("list_groups calls = %d, calls = %#v, want cached source list", got, calls)
	}
	if got := countCall(calls, "list_descendant_groups"); got != 1 {
		t.Fatalf("list_descendant_groups calls = %d, calls = %#v, want cached source list", got, calls)
	}
	if got := countCall(calls, "list_group_members"); got != 2 {
		t.Fatalf("list_group_members calls = %d, calls = %#v, want live member paging", got, calls)
	}
}

func TestDatasourceProviderCorpusUsesLargerDefaultMembershipPages(t *testing.T) {
	var perPages []int64
	provider := gitlabDatasourceProvider{
		system: fakeSystem{},
		ref:    resource.PluginRef{Name: Name, Instance: "company-a"},
		clientFactory: func(context.Context, system.System, resource.PluginRef, Config) (gitlabClient, error) {
			return fakeGitLabClient{
				groupMemberPerPages: &perPages,
				groups: []*gitlab.Group{{
					ID:       7,
					Name:     "Platform",
					FullPath: "engineering/platform",
					FullName: "Engineering / Platform",
				}},
				groupMembers: []*gitlab.GroupMember{{
					ID:          42,
					Username:    "timo",
					Name:        "Timo",
					AccessLevel: gitlab.DeveloperPermissions,
				}},
			}, nil
		},
	}
	accessor, err := provider.Open(context.Background(), coredatasource.Spec{
		Name:     "company-a-gitlab",
		Kind:     Name,
		Entities: []coredatasource.EntityType{MembershipEntity},
		Config:   map[string]string{"instance": "company-a"},
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if _, err := accessor.(coredatasource.CorpusProvider).Corpus(context.Background(), coredatasource.CorpusRequest{Entity: MembershipEntity}); err != nil {
		t.Fatalf("Corpus: %v", err)
	}
	if len(perPages) == 0 || perPages[0] != 100 {
		t.Fatalf("group member per pages = %#v, want default corpus page size 100", perPages)
	}
}

func TestDatasourceRelationsUseMembershipFieldIndex(t *testing.T) {
	index, err := semantic.New(semantic.HashEmbedder{ModelName: "test-embedding"}, semantic.NewJSONStore(""), semantic.Config{})
	if err != nil {
		t.Fatalf("semantic.New: %v", err)
	}
	for _, doc := range []coredatasource.CorpusDocument{
		{
			Ref:   coredatasource.RecordRef{Datasource: "company-a-gitlab", Entity: MembershipEntity, ID: "42:namespace:7"},
			Title: "Namespace Engineering / Platform (developer)",
			Metadata: map[string]string{
				"id":           "42:namespace:7",
				"user_id":      "42",
				"source_id":    "7",
				"source_name":  "Engineering / Platform",
				"source_type":  "Namespace",
				"source_path":  "engineering/platform",
				"access_level": "developer",
				"role":         "developer",
			},
		},
		{
			Ref:   coredatasource.RecordRef{Datasource: "company-a-gitlab", Entity: MembershipEntity, ID: "42:project:12"},
			Title: "Project Runtime (maintainer)",
			Metadata: map[string]string{
				"id":           "42:project:12",
				"user_id":      "42",
				"source_id":    "12",
				"source_name":  "Runtime",
				"source_type":  "Project",
				"source_path":  "engineering/runtime",
				"access_level": "maintainer",
				"role":         "maintainer",
			},
		},
	} {
		if _, err := index.UpdateRecord(context.Background(), doc, membershipEntitySpec()); err != nil {
			t.Fatalf("UpdateRecord: %v", err)
		}
	}
	calls := []string{}
	provider := gitlabDatasourceProvider{
		system: fakeSystem{},
		ref:    resource.PluginRef{Name: Name, Instance: "company-a"},
		clientFactory: func(context.Context, system.System, resource.PluginRef, Config) (gitlabClient, error) {
			return fakeGitLabClient{calls: &calls}, nil
		},
	}
	provider = provider.WithSemanticIndex(index).(gitlabDatasourceProvider)
	accessor, err := provider.Open(context.Background(), coredatasource.Spec{
		Name:     "company-a-gitlab",
		Kind:     Name,
		Entities: []coredatasource.EntityType{UserEntity, GroupEntity, ProjectEntity, MembershipEntity},
		Config:   map[string]string{"instance": "company-a"},
		Index:    coredatasource.IndexSpec{Enabled: true},
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	relationer := accessor.(coredatasource.Relationer)
	groups, err := relationer.Relation(context.Background(), coredatasource.RelationRequest{Entity: UserEntity, ID: "42", Relation: "groups", Limit: 1})
	if err != nil {
		t.Fatalf("user groups Relation: %v", err)
	}
	if len(groups.Records) != 1 || groups.Records[0].Entity != GroupEntity || groups.Records[0].ID != "engineering/platform" {
		t.Fatalf("group records = %#v", groups.Records)
	}
	if groups.NextCursor != "" || !groups.Complete {
		t.Fatalf("group pagination = next %q complete %v, want complete exact page", groups.NextCursor, groups.Complete)
	}
	projects, err := relationer.Relation(context.Background(), coredatasource.RelationRequest{Entity: UserEntity, ID: "42", Relation: "projects"})
	if err != nil {
		t.Fatalf("user projects Relation: %v", err)
	}
	if len(projects.Records) != 1 || projects.Records[0].Entity != ProjectEntity || projects.Records[0].ID != "12" {
		t.Fatalf("project records = %#v", projects.Records)
	}
	if len(calls) != 0 {
		t.Fatalf("GitLab API calls = %#v, want none", calls)
	}
}

func TestDatasourceRelationsReportMissingMembershipIndex(t *testing.T) {
	index, err := semantic.New(semantic.HashEmbedder{ModelName: "test-embedding"}, semantic.NewJSONStore(""), semantic.Config{})
	if err != nil {
		t.Fatalf("semantic.New: %v", err)
	}
	calls := []string{}
	provider := gitlabDatasourceProvider{
		system: fakeSystem{},
		ref:    resource.PluginRef{Name: Name, Instance: "company-a"},
		clientFactory: func(context.Context, system.System, resource.PluginRef, Config) (gitlabClient, error) {
			return fakeGitLabClient{calls: &calls}, nil
		},
	}
	provider = provider.WithSemanticIndex(index).(gitlabDatasourceProvider)
	accessor, err := provider.Open(context.Background(), coredatasource.Spec{
		Name:     "company-a-gitlab",
		Kind:     Name,
		Entities: []coredatasource.EntityType{UserEntity, MembershipEntity},
		Config:   map[string]string{"instance": "company-a"},
		Index:    coredatasource.IndexSpec{Enabled: true},
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	_, err = accessor.(coredatasource.Relationer).Relation(context.Background(), coredatasource.RelationRequest{Entity: UserEntity, ID: "42", Relation: "memberships"})
	if err == nil || !strings.Contains(err.Error(), "field index is not built") {
		t.Fatalf("Relation error = %v, want missing membership index", err)
	}
	if len(calls) != 0 {
		t.Fatalf("GitLab API calls = %#v, want none", calls)
	}
}

func TestDatasourceProviderGetsGroup(t *testing.T) {
	provider := gitlabDatasourceProvider{
		system: fakeSystem{},
		ref:    resource.PluginRef{Name: Name, Instance: "company-a"},
		clientFactory: func(context.Context, system.System, resource.PluginRef, Config) (gitlabClient, error) {
			return fakeGitLabClient{groups: []*gitlab.Group{{
				ID:       7,
				Name:     "Platform",
				Path:     "platform",
				FullPath: "engineering/platform",
			}}}, nil
		},
	}
	accessor, err := provider.Open(context.Background(), coredatasource.Spec{
		Name:     "company-a-gitlab",
		Kind:     Name,
		Entities: []coredatasource.EntityType{GroupEntity},
		Config:   map[string]string{"instance": "company-a"},
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	record, err := accessor.(coredatasource.Getter).Get(context.Background(), coredatasource.GetRequest{Entity: GroupEntity, ID: "engineering/platform"})
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if record.ID != "engineering/platform" || record.Metadata["name"] != "Platform" {
		t.Fatalf("record = %#v, want GitLab group", record)
	}
}

func TestDatasourceProviderExposesMRReviewEntities(t *testing.T) {
	provider := gitlabDatasourceProvider{}
	entities := map[coredatasource.EntityType]coredatasource.EntitySpec{}
	for _, entity := range provider.Entities() {
		entities[entity.Type] = entity
	}
	for _, want := range []coredatasource.EntityType{
		ProjectEntity,
		MergeRequestEntity,
		MergeRequestDiffEntity,
		MergeRequestNoteEntity,
		PipelineEntity,
		BranchEntity,
		TagEntity,
		CommitEntity,
		RepositoryTreeEntity,
		RepositoryFileEntity,
		JobEntity,
		JobTraceEntity,
		UserEntity,
		GroupEntity,
		MembershipEntity,
	} {
		if _, ok := entities[want]; !ok {
			t.Fatalf("entities = %#v, missing %s", entities, want)
		}
	}
	if !hasCapability(entities[ProjectEntity], coredatasource.EntityCapabilityIndex) {
		t.Fatalf("project capabilities = %#v, missing index", entities[ProjectEntity].Capabilities)
	}
	if !hasCapability(entities[ProjectEntity], coredatasource.EntityCapabilityList) {
		t.Fatalf("project capabilities = %#v, missing list", entities[ProjectEntity].Capabilities)
	}
	if !hasCapability(entities[UserEntity], coredatasource.EntityCapabilitySearch) || !hasCapability(entities[UserEntity], coredatasource.EntityCapabilityList) || !hasCapability(entities[UserEntity], coredatasource.EntityCapabilityGet) || !hasCapability(entities[UserEntity], coredatasource.EntityCapabilityIndex) {
		t.Fatalf("user capabilities = %#v, want search/list/get/index", entities[UserEntity].Capabilities)
	}
	if !hasCapability(entities[GroupEntity], coredatasource.EntityCapabilitySearch) || !hasCapability(entities[GroupEntity], coredatasource.EntityCapabilityList) || !hasCapability(entities[GroupEntity], coredatasource.EntityCapabilityGet) || !hasCapability(entities[GroupEntity], coredatasource.EntityCapabilityIndex) {
		t.Fatalf("group capabilities = %#v, want search/list/get/index", entities[GroupEntity].Capabilities)
	}
	if !hasCapability(entities[MembershipEntity], coredatasource.EntityCapabilityList) || !hasCapability(entities[MembershipEntity], coredatasource.EntityCapabilityGet) || !hasCapability(entities[MembershipEntity], coredatasource.EntityCapabilityRelation) || !hasCapability(entities[MembershipEntity], coredatasource.EntityCapabilityIndex) {
		t.Fatalf("membership capabilities = %#v, want list/get/relation/index", entities[MembershipEntity].Capabilities)
	}
	for _, typ := range []coredatasource.EntityType{MergeRequestEntity, MergeRequestDiffEntity, MergeRequestNoteEntity, PipelineEntity} {
		if hasCapability(entities[typ], coredatasource.EntityCapabilityList) {
			t.Fatalf("entity %s capabilities = %#v, want no list", typ, entities[typ].Capabilities)
		}
	}
	for _, typ := range []coredatasource.EntityType{BranchEntity, TagEntity, CommitEntity, RepositoryTreeEntity, RepositoryFileEntity, JobEntity, JobTraceEntity} {
		if hasCapability(entities[typ], coredatasource.EntityCapabilityIndex) {
			t.Fatalf("entity %s capabilities = %#v, want no index", typ, entities[typ].Capabilities)
		}
	}
	for typ, entity := range entities {
		if hasCapability(entity, coredatasource.EntityCapabilitySemanticSearch) {
			t.Fatalf("entity %s capabilities = %#v, want no semantic search", typ, entity.Capabilities)
		}
	}
	mr := entities[MergeRequestEntity]
	relations := map[string]coredatasource.EntityType{}
	for _, relation := range mr.Relations {
		relations[relation.Name] = relation.TargetEntity
	}
	for name, target := range map[string]coredatasource.EntityType{
		"diffs":        MergeRequestDiffEntity,
		"notes":        MergeRequestNoteEntity,
		"pipelines":    PipelineEntity,
		"participants": UserEntity,
		"reviewers":    UserEntity,
	} {
		if relations[name] != target {
			t.Fatalf("relation %s = %s, want %s", name, relations[name], target)
		}
	}
	userRelations := map[string]coredatasource.EntityType{}
	for _, relation := range entities[UserEntity].Relations {
		userRelations[relation.Name] = relation.TargetEntity
	}
	for name, target := range map[string]coredatasource.EntityType{
		"memberships": MembershipEntity,
		"groups":      GroupEntity,
		"projects":    ProjectEntity,
	} {
		if userRelations[name] != target {
			t.Fatalf("user relation %s = %s, want %s", name, userRelations[name], target)
		}
	}
	projectRelations := map[string]coredatasource.EntityType{}
	for _, relation := range entities[ProjectEntity].Relations {
		projectRelations[relation.Name] = relation.TargetEntity
	}
	for name, target := range map[string]coredatasource.EntityType{
		"merge_requests":  MergeRequestEntity,
		"pipelines":       PipelineEntity,
		"branches":        BranchEntity,
		"tags":            TagEntity,
		"commits":         CommitEntity,
		"repository_tree": RepositoryTreeEntity,
		"jobs":            JobEntity,
		"users":           UserEntity,
		"groups":          GroupEntity,
	} {
		if projectRelations[name] != target {
			t.Fatalf("project relation %s = %s, want %s", name, projectRelations[name], target)
		}
	}
	groupRelations := map[string]coredatasource.EntityType{}
	for _, relation := range entities[GroupEntity].Relations {
		groupRelations[relation.Name] = relation.TargetEntity
	}
	for name, target := range map[string]coredatasource.EntityType{
		"parent":            GroupEntity,
		"subgroups":         GroupEntity,
		"descendant_groups": GroupEntity,
		"projects":          ProjectEntity,
		"users":             UserEntity,
	} {
		if groupRelations[name] != target {
			t.Fatalf("group relation %s = %s, want %s", name, groupRelations[name], target)
		}
	}
	membershipRelations := map[string]coredatasource.EntityType{}
	for _, relation := range entities[MembershipEntity].Relations {
		membershipRelations[relation.Name] = relation.TargetEntity
	}
	for name, target := range map[string]coredatasource.EntityType{
		"group":   GroupEntity,
		"project": ProjectEntity,
	} {
		if membershipRelations[name] != target {
			t.Fatalf("membership relation %s = %s, want %s", name, membershipRelations[name], target)
		}
	}
}

func TestDatasourceRelationsReturnCodeAndCIResources(t *testing.T) {
	commit := &gitlab.Commit{ID: "abc123", ShortID: "abc123", Title: "Fix runtime", Message: "Fix runtime tests"}
	calls := []string{}
	provider := gitlabDatasourceProvider{
		system: fakeSystem{},
		ref:    resource.PluginRef{Name: Name, Instance: "company-a"},
		clientFactory: func(context.Context, system.System, resource.PluginRef, Config) (gitlabClient, error) {
			return fakeGitLabClient{
				calls: &calls,
				projects: []*gitlab.Project{{
					ID:                12,
					Name:              "Runtime",
					PathWithNamespace: "engineering/runtime",
				}},
				branches: []*gitlab.Branch{{
					Name:    "main",
					Default: true,
					Commit:  commit,
					WebURL:  "https://gitlab.example/engineering/runtime/-/tree/main",
				}},
				tags: []*gitlab.Tag{{
					Name:   "v1.0.0",
					Target: "abc123",
					Commit: commit,
				}},
				commits: []*gitlab.Commit{commit},
				tree: []*gitlab.TreeNode{{
					ID:   "blob123",
					Name: "main.go",
					Type: "blob",
					Path: "cmd/app/main.go",
					Mode: "100644",
				}},
				file: &gitlab.File{
					FileName: "main.go",
					FilePath: "cmd/app/main.go",
					Ref:      "HEAD",
					Encoding: "base64",
					Content:  base64.StdEncoding.EncodeToString([]byte("package main\n")),
					Size:     13,
				},
				pipelines: []*gitlab.PipelineInfo{{
					ID:        99,
					ProjectID: 12,
					Status:    "success",
					SHA:       "abc123",
					Ref:       "main",
				}},
				jobs: []*gitlab.Job{{
					ID:     123,
					Name:   "test",
					Stage:  "test",
					Status: "failed",
					Ref:    "main",
					Commit: commit,
					Pipeline: gitlab.JobPipeline{
						ID:        99,
						ProjectID: 12,
						Ref:       "main",
						Sha:       "abc123",
						Status:    "failed",
					},
				}},
				trace: []byte("go test ./...\nFAIL\n"),
			}, nil
		},
	}
	accessor, err := provider.Open(context.Background(), coredatasource.Spec{
		Name:     "company-a-gitlab",
		Kind:     Name,
		Entities: []coredatasource.EntityType{ProjectEntity, BranchEntity, TagEntity, CommitEntity, RepositoryTreeEntity, RepositoryFileEntity, PipelineEntity, JobEntity, JobTraceEntity},
		Config:   map[string]string{"instance": "company-a"},
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	relation := accessor.(coredatasource.Relationer)

	branches, err := relation.Relation(context.Background(), coredatasource.RelationRequest{Entity: ProjectEntity, ID: "engineering/runtime", Relation: "branches"})
	if err != nil {
		t.Fatalf("branches Relation: %v", err)
	}
	if len(branches.Records) != 1 || branches.Records[0].ID != "12!main" {
		t.Fatalf("branches = %#v, want main branch", branches.Records)
	}

	tags, err := relation.Relation(context.Background(), coredatasource.RelationRequest{Entity: ProjectEntity, ID: "engineering/runtime", Relation: "tags"})
	if err != nil {
		t.Fatalf("tags Relation: %v", err)
	}
	if len(tags.Records) != 1 || tags.Records[0].ID != "12!v1.0.0" {
		t.Fatalf("tags = %#v, want v1 tag", tags.Records)
	}

	commits, err := relation.Relation(context.Background(), coredatasource.RelationRequest{Entity: ProjectEntity, ID: "engineering/runtime", Relation: "commits"})
	if err != nil {
		t.Fatalf("commits Relation: %v", err)
	}
	if len(commits.Records) != 1 || commits.Records[0].ID != "12!abc123" {
		t.Fatalf("commits = %#v, want commit", commits.Records)
	}

	tree, err := relation.Relation(context.Background(), coredatasource.RelationRequest{Entity: ProjectEntity, ID: "engineering/runtime", Relation: "repository_tree"})
	if err != nil {
		t.Fatalf("repository_tree Relation: %v", err)
	}
	if len(tree.Records) != 1 || tree.Records[0].ID != "12!HEAD!cmd/app/main.go" {
		t.Fatalf("tree = %#v, want HEAD file entry", tree.Records)
	}

	file, err := relation.Relation(context.Background(), coredatasource.RelationRequest{Entity: RepositoryTreeEntity, ID: "12!HEAD!cmd/app/main.go", Relation: "file"})
	if err != nil {
		t.Fatalf("file Relation: %v", err)
	}
	if len(file.Records) != 1 || !strings.Contains(file.Records[0].Content, "package main") {
		t.Fatalf("file = %#v, want decoded content preview", file.Records)
	}

	jobs, err := relation.Relation(context.Background(), coredatasource.RelationRequest{Entity: ProjectEntity, ID: "engineering/runtime", Relation: "jobs"})
	if err != nil {
		t.Fatalf("jobs Relation: %v", err)
	}
	if len(jobs.Records) != 1 || jobs.Records[0].ID != "12!123" {
		t.Fatalf("jobs = %#v, want project job", jobs.Records)
	}

	pipelineJobs, err := relation.Relation(context.Background(), coredatasource.RelationRequest{Entity: PipelineEntity, ID: "engineering/runtime!99", Relation: "jobs"})
	if err != nil {
		t.Fatalf("pipeline jobs Relation: %v", err)
	}
	if len(pipelineJobs.Records) != 1 || pipelineJobs.Records[0].ID != "engineering/runtime!123" {
		t.Fatalf("pipeline jobs = %#v, want pipeline job", pipelineJobs.Records)
	}

	trace, err := relation.Relation(context.Background(), coredatasource.RelationRequest{Entity: JobEntity, ID: "engineering/runtime!123", Relation: "trace"})
	if err != nil {
		t.Fatalf("trace Relation: %v", err)
	}
	if len(trace.Records) != 1 || !strings.Contains(trace.Records[0].Content, "FAIL") {
		t.Fatalf("trace = %#v, want bounded job trace", trace.Records)
	}

	for _, want := range []string{"list_branches", "list_tags", "list_commits", "list_tree", "get_file", "list_project_jobs", "list_pipeline_jobs", "get_trace_file"} {
		if !containsCall(calls, want) {
			t.Fatalf("GitLab API calls = %#v, missing %s", calls, want)
		}
	}
}

func TestDatasourceListsGitLabResources(t *testing.T) {
	provider := gitlabDatasourceProvider{
		system: fakeSystem{},
		ref:    resource.PluginRef{Name: Name, Instance: "company-a"},
		clientFactory: func(context.Context, system.System, resource.PluginRef, Config) (gitlabClient, error) {
			return &fakeGitLabClient{
				projects: []*gitlab.Project{{
					ID:                12,
					Name:              "Runtime",
					PathWithNamespace: "engineering/runtime",
					WebURL:            "https://gitlab.example/engineering/runtime",
				}},
				users: []*gitlab.User{{
					ID:       42,
					Username: "timo",
					Name:     "Timo",
				}},
				groups: []*gitlab.Group{{
					ID:       7,
					Name:     "Platform",
					FullPath: "engineering/platform",
				}},
			}, nil
		},
	}
	accessor, err := provider.Open(context.Background(), coredatasource.Spec{
		Name: "company-a-gitlab",
		Kind: Name,
		Entities: []coredatasource.EntityType{
			ProjectEntity,
			UserEntity,
			GroupEntity,
		},
		Config: map[string]string{"instance": "company-a"},
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	lister := accessor.(coredatasource.Lister)
	projects, err := lister.List(context.Background(), coredatasource.ListRequest{Entity: ProjectEntity})
	if err != nil {
		t.Fatalf("List projects: %v", err)
	}
	if len(projects.Records) != 1 || projects.Records[0].Entity != ProjectEntity || projects.Records[0].ID != "12" {
		t.Fatalf("project records = %#v", projects.Records)
	}
	users, err := lister.List(context.Background(), coredatasource.ListRequest{Entity: UserEntity})
	if err != nil {
		t.Fatalf("List users: %v", err)
	}
	if len(users.Records) != 1 || users.Records[0].Entity != UserEntity || users.Records[0].ID != "42" {
		t.Fatalf("user records = %#v", users.Records)
	}
	groups, err := lister.List(context.Background(), coredatasource.ListRequest{Entity: GroupEntity})
	if err != nil {
		t.Fatalf("List groups: %v", err)
	}
	if len(groups.Records) != 1 || groups.Records[0].Entity != GroupEntity || groups.Records[0].ID != "engineering/platform" {
		t.Fatalf("group records = %#v", groups.Records)
	}
}

func TestDatasourceRelationsReturnMRReviewRecords(t *testing.T) {
	provider := gitlabDatasourceProvider{
		system: fakeSystem{},
		ref:    resource.PluginRef{Name: Name, Instance: "company-a"},
		clientFactory: func(context.Context, system.System, resource.PluginRef, Config) (gitlabClient, error) {
			return &fakeGitLabClient{
				mrs: []*gitlab.BasicMergeRequest{{
					ID:          42,
					IID:         7,
					ProjectID:   12,
					Title:       "Review runtime",
					Description: "Review runtime changes",
					State:       "opened",
				}},
				diffs: []*gitlab.MergeRequestDiff{{
					NewPath: "runtime.go",
					Diff:    "@@ -1 +1 @@\n-old\n+new",
				}},
				notes: []*gitlab.Note{{
					ID:          99,
					ProjectID:   12,
					NoteableIID: 7,
					Body:        "Looks good",
					Author:      gitlab.NoteAuthor{Username: "reviewer"},
				}},
			}, nil
		},
	}
	accessor, err := provider.Open(context.Background(), coredatasource.Spec{
		Name: "company-a-gitlab",
		Kind: Name,
		Entities: []coredatasource.EntityType{
			ProjectEntity,
			MergeRequestEntity,
			MergeRequestDiffEntity,
			MergeRequestNoteEntity,
			PipelineEntity,
			UserEntity,
		},
		Config: map[string]string{"instance": "company-a"},
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	relationer, ok := accessor.(coredatasource.Relationer)
	if !ok {
		t.Fatal("accessor does not implement datasource.Relationer")
	}
	mrs, err := relationer.Relation(context.Background(), coredatasource.RelationRequest{Entity: ProjectEntity, ID: "12", Relation: "merge_requests"})
	if err != nil {
		t.Fatalf("project merge_requests Relation: %v", err)
	}
	if len(mrs.Records) != 1 || mrs.Records[0].Entity != MergeRequestEntity {
		t.Fatalf("merge request records = %#v", mrs.Records)
	}
	diffs, err := relationer.Relation(context.Background(), coredatasource.RelationRequest{Entity: MergeRequestEntity, ID: "12!7", Relation: "diffs"})
	if err != nil {
		t.Fatalf("mr diffs Relation: %v", err)
	}
	if len(diffs.Records) != 1 || diffs.Records[0].Entity != MergeRequestDiffEntity || strings.Contains(diffs.Records[0].Content, "raw") {
		t.Fatalf("diff records = %#v", diffs.Records)
	}
	notes, err := relationer.Relation(context.Background(), coredatasource.RelationRequest{Entity: MergeRequestEntity, ID: "12!7", Relation: "notes"})
	if err != nil {
		t.Fatalf("mr notes Relation: %v", err)
	}
	if len(notes.Records) != 1 || notes.Records[0].Entity != MergeRequestNoteEntity || notes.Records[0].Content != "Looks good" {
		t.Fatalf("note records = %#v", notes.Records)
	}
}

func TestDatasourceRelationsReturnUserGroups(t *testing.T) {
	provider := gitlabDatasourceProvider{
		system: fakeSystem{},
		ref:    resource.PluginRef{Name: Name, Instance: "company-a"},
		clientFactory: func(context.Context, system.System, resource.PluginRef, Config) (gitlabClient, error) {
			return &fakeGitLabClient{
				groups: []*gitlab.Group{{
					ID:       7,
					Name:     "Platform",
					FullPath: "engineering/platform",
					FullName: "Engineering / Platform",
					WebURL:   "https://gitlab.example/groups/engineering/platform",
				}},
				groupMembers: []*gitlab.GroupMember{{
					ID:          42,
					Username:    "timo",
					Name:        "Timo",
					AccessLevel: gitlab.DeveloperPermissions,
				}},
			}, nil
		},
	}
	accessor, err := provider.Open(context.Background(), coredatasource.Spec{
		Name:     "company-a-gitlab",
		Kind:     Name,
		Entities: []coredatasource.EntityType{UserEntity, GroupEntity},
		Config:   map[string]string{"instance": "company-a"},
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	result, err := accessor.(coredatasource.Relationer).Relation(context.Background(), coredatasource.RelationRequest{Entity: UserEntity, ID: "42", Relation: "groups"})
	if err != nil {
		t.Fatalf("user groups Relation: %v", err)
	}
	if len(result.Records) != 1 || result.Records[0].Entity != GroupEntity || result.Records[0].ID != "engineering/platform" {
		t.Fatalf("group records = %#v", result.Records)
	}
	if result.Records[0].Metadata["role"] != "developer" {
		t.Fatalf("group role = %#v, want developer", result.Records[0].Metadata)
	}
}

func TestDatasourceRelationsReturnUserMembershipsAndProjects(t *testing.T) {
	provider := gitlabDatasourceProvider{
		system: fakeSystem{},
		ref:    resource.PluginRef{Name: Name, Instance: "company-a"},
		clientFactory: func(context.Context, system.System, resource.PluginRef, Config) (gitlabClient, error) {
			return &fakeGitLabClient{
				groups: []*gitlab.Group{{
					ID:       7,
					Name:     "Platform",
					FullPath: "engineering/platform",
					FullName: "Engineering / Platform",
				}},
				groupMembers: []*gitlab.GroupMember{{
					ID:          42,
					Username:    "timo",
					Name:        "Timo",
					AccessLevel: gitlab.DeveloperPermissions,
				}},
				projects: []*gitlab.Project{{
					ID:                12,
					Name:              "Runtime",
					PathWithNamespace: "engineering/runtime",
				}},
				projectMembers: []*gitlab.ProjectMember{{
					ID:          42,
					Username:    "timo",
					Name:        "Timo",
					AccessLevel: gitlab.MaintainerPermissions,
				}},
			}, nil
		},
	}
	accessor, err := provider.Open(context.Background(), coredatasource.Spec{
		Name:     "company-a-gitlab",
		Kind:     Name,
		Entities: []coredatasource.EntityType{UserEntity, GroupEntity, ProjectEntity, MembershipEntity},
		Config:   map[string]string{"instance": "company-a"},
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	relationer := accessor.(coredatasource.Relationer)
	memberships, err := relationer.Relation(context.Background(), coredatasource.RelationRequest{Entity: UserEntity, ID: "42", Relation: "memberships"})
	if err != nil {
		t.Fatalf("user memberships Relation: %v", err)
	}
	if len(memberships.Records) != 2 || memberships.Records[0].Entity != MembershipEntity {
		t.Fatalf("membership records = %#v", memberships.Records)
	}
	projects, err := relationer.Relation(context.Background(), coredatasource.RelationRequest{Entity: UserEntity, ID: "42", Relation: "projects"})
	if err != nil {
		t.Fatalf("user projects Relation: %v", err)
	}
	if len(projects.Records) != 1 || projects.Records[0].Entity != ProjectEntity || projects.Records[0].ID != "12" {
		t.Fatalf("project records = %#v", projects.Records)
	}
	group, err := relationer.Relation(context.Background(), coredatasource.RelationRequest{Entity: MembershipEntity, ID: "42:namespace:7", Relation: "group"})
	if err != nil {
		t.Fatalf("membership group Relation: %v", err)
	}
	if len(group.Records) != 1 || group.Records[0].Entity != GroupEntity || group.Records[0].ID != "engineering/platform" {
		t.Fatalf("membership group records = %#v", group.Records)
	}
}

func TestDatasourceRelationsResolveMembershipsFromVisibleServiceAccountGroups(t *testing.T) {
	calls := []string{}
	groupUserIDs := []int64{}
	projectUserIDs := []int64{}
	provider := gitlabDatasourceProvider{
		system: fakeSystem{},
		ref:    resource.PluginRef{Name: Name, Instance: "company-a"},
		clientFactory: func(context.Context, system.System, resource.PluginRef, Config) (gitlabClient, error) {
			return &fakeGitLabClient{
				calls:                &calls,
				groupMemberUserIDs:   &groupUserIDs,
				projectMemberUserIDs: &projectUserIDs,
				groups: []*gitlab.Group{{
					ID:       7,
					Name:     "Platform",
					FullPath: "engineering/platform",
					FullName: "Engineering / Platform",
				}},
				descendantGroups: []*gitlab.Group{{
					ID:       8,
					Name:     "Runtime",
					FullPath: "engineering/platform/runtime",
					FullName: "Engineering / Platform / Runtime",
					ParentID: 7,
				}},
				groupMembers: []*gitlab.GroupMember{{
					ID:          42,
					Username:    "timo",
					Name:        "Timo",
					AccessLevel: gitlab.DeveloperPermissions,
				}},
				projects: []*gitlab.Project{{
					ID:                12,
					Name:              "Runtime",
					PathWithNamespace: "engineering/runtime",
				}},
				projectMembers: []*gitlab.ProjectMember{{
					ID:          42,
					Username:    "timo",
					Name:        "Timo",
					AccessLevel: gitlab.MaintainerPermissions,
				}},
			}, nil
		},
	}
	accessor, err := provider.Open(context.Background(), coredatasource.Spec{
		Name:     "company-a-gitlab",
		Kind:     Name,
		Entities: []coredatasource.EntityType{UserEntity, GroupEntity, ProjectEntity, MembershipEntity},
		Config:   map[string]string{"instance": "company-a"},
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	result, err := accessor.(coredatasource.Relationer).Relation(context.Background(), coredatasource.RelationRequest{Entity: UserEntity, ID: "42", Relation: "memberships"})
	if err != nil {
		t.Fatalf("user memberships Relation: %v", err)
	}
	if len(result.Records) != 3 {
		t.Fatalf("membership records = %#v, want visible group, descendant group, and project", result.Records)
	}
	if !containsCall(calls, "list_groups") || !containsCall(calls, "list_descendant_groups") || !containsCall(calls, "list_group_members") || !containsCall(calls, "list_project_members") {
		t.Fatalf("calls = %#v, want visible group, descendant, and project member checks", calls)
	}
	if len(groupUserIDs) == 0 || groupUserIDs[0] != 42 || len(projectUserIDs) == 0 || projectUserIDs[0] != 42 {
		t.Fatalf("user id filters group=%#v project=%#v, want 42", groupUserIDs, projectUserIDs)
	}
}

func TestDatasourceRelationsReturnNoVisibleMemberships(t *testing.T) {
	provider := gitlabDatasourceProvider{
		system: fakeSystem{},
		ref:    resource.PluginRef{Name: Name, Instance: "company-a"},
		clientFactory: func(context.Context, system.System, resource.PluginRef, Config) (gitlabClient, error) {
			return &fakeGitLabClient{
				groups: []*gitlab.Group{{
					ID:       7,
					Name:     "Platform",
					FullPath: "engineering/platform",
				}},
			}, nil
		},
	}
	accessor, err := provider.Open(context.Background(), coredatasource.Spec{
		Name:     "company-a-gitlab",
		Kind:     Name,
		Entities: []coredatasource.EntityType{UserEntity, MembershipEntity},
		Config:   map[string]string{"instance": "company-a"},
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	result, err := accessor.(coredatasource.Relationer).Relation(context.Background(), coredatasource.RelationRequest{Entity: UserEntity, ID: "42", Relation: "memberships"})
	if err != nil {
		t.Fatalf("user memberships Relation: %v", err)
	}
	if len(result.Records) != 0 {
		t.Fatalf("membership records = %#v, want none", result.Records)
	}
}

func TestDatasourceRelationsReturnProjectUsersAndGroups(t *testing.T) {
	provider := gitlabDatasourceProvider{
		system: fakeSystem{},
		ref:    resource.PluginRef{Name: Name, Instance: "company-a"},
		clientFactory: func(context.Context, system.System, resource.PluginRef, Config) (gitlabClient, error) {
			return &fakeGitLabClient{
				projectUsers: []*gitlab.ProjectUser{{
					ID:       42,
					Username: "timo",
					Name:     "Timo",
					State:    "active",
					WebURL:   "https://gitlab.example/timo",
				}},
				projectGroups: []*gitlab.ProjectGroup{{
					ID:       7,
					Name:     "Platform",
					FullPath: "engineering/platform",
					FullName: "Engineering / Platform",
					WebURL:   "https://gitlab.example/groups/engineering/platform",
				}},
			}, nil
		},
	}
	accessor, err := provider.Open(context.Background(), coredatasource.Spec{
		Name:     "company-a-gitlab",
		Kind:     Name,
		Entities: []coredatasource.EntityType{ProjectEntity, UserEntity, GroupEntity},
		Config:   map[string]string{"instance": "company-a"},
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	relationer := accessor.(coredatasource.Relationer)
	users, err := relationer.Relation(context.Background(), coredatasource.RelationRequest{Entity: ProjectEntity, ID: "engineering/runtime", Relation: "users"})
	if err != nil {
		t.Fatalf("project users Relation: %v", err)
	}
	if len(users.Records) != 1 || users.Records[0].Entity != UserEntity || users.Records[0].ID != "42" || users.Records[0].Metadata["username"] != "timo" {
		t.Fatalf("user records = %#v", users.Records)
	}
	groups, err := relationer.Relation(context.Background(), coredatasource.RelationRequest{Entity: ProjectEntity, ID: "engineering/runtime", Relation: "groups"})
	if err != nil {
		t.Fatalf("project groups Relation: %v", err)
	}
	if len(groups.Records) != 1 || groups.Records[0].Entity != GroupEntity || groups.Records[0].ID != "engineering/platform" {
		t.Fatalf("group records = %#v", groups.Records)
	}
}

func TestDatasourceRelationsReturnGroupProjects(t *testing.T) {
	provider := gitlabDatasourceProvider{
		system: fakeSystem{},
		ref:    resource.PluginRef{Name: Name, Instance: "company-a"},
		clientFactory: func(context.Context, system.System, resource.PluginRef, Config) (gitlabClient, error) {
			return &fakeGitLabClient{
				groupProjects: []*gitlab.Project{{
					ID:                12,
					Name:              "Runtime",
					PathWithNamespace: "engineering/runtime",
					WebURL:            "https://gitlab.example/engineering/runtime",
				}},
				groupMembers: []*gitlab.GroupMember{{
					ID:          42,
					Username:    "timo",
					Name:        "Timo",
					AccessLevel: gitlab.DeveloperPermissions,
				}},
			}, nil
		},
	}
	accessor, err := provider.Open(context.Background(), coredatasource.Spec{
		Name:     "company-a-gitlab",
		Kind:     Name,
		Entities: []coredatasource.EntityType{ProjectEntity, GroupEntity},
		Config:   map[string]string{"instance": "company-a"},
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	result, err := accessor.(coredatasource.Relationer).Relation(context.Background(), coredatasource.RelationRequest{Entity: GroupEntity, ID: "engineering", Relation: "projects"})
	if err != nil {
		t.Fatalf("group projects Relation: %v", err)
	}
	if len(result.Records) != 1 || result.Records[0].Entity != ProjectEntity || result.Records[0].ID != "12" {
		t.Fatalf("project records = %#v", result.Records)
	}
	users, err := accessor.(coredatasource.Relationer).Relation(context.Background(), coredatasource.RelationRequest{Entity: GroupEntity, ID: "engineering", Relation: "users"})
	if err != nil {
		t.Fatalf("group users Relation: %v", err)
	}
	if len(users.Records) != 1 || users.Records[0].Entity != UserEntity || users.Records[0].ID != "42" || users.Records[0].Metadata["role"] != "developer" {
		t.Fatalf("user records = %#v", users.Records)
	}
}

func TestDatasourceRelationsReturnGroupHierarchy(t *testing.T) {
	provider := gitlabDatasourceProvider{
		system: fakeSystem{},
		ref:    resource.PluginRef{Name: Name, Instance: "company-a"},
		clientFactory: func(context.Context, system.System, resource.PluginRef, Config) (gitlabClient, error) {
			return &fakeGitLabClient{
				groups: []*gitlab.Group{{
					ID:       1,
					Name:     "Engineering",
					Path:     "engineering",
					FullPath: "engineering",
				}},
				subGroups: []*gitlab.Group{{
					ID:       7,
					Name:     "Platform",
					Path:     "platform",
					FullPath: "engineering/platform",
					ParentID: 1,
				}},
				descendantGroups: []*gitlab.Group{{
					ID:       8,
					Name:     "Runtime",
					Path:     "runtime",
					FullPath: "engineering/platform/runtime",
					ParentID: 7,
				}},
			}, nil
		},
	}
	accessor, err := provider.Open(context.Background(), coredatasource.Spec{
		Name:     "company-a-gitlab",
		Kind:     Name,
		Entities: []coredatasource.EntityType{GroupEntity},
		Config:   map[string]string{"instance": "company-a"},
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	relationer := accessor.(coredatasource.Relationer)
	parent, err := relationer.Relation(context.Background(), coredatasource.RelationRequest{Entity: GroupEntity, ID: "engineering/platform", Relation: "parent"})
	if err != nil {
		t.Fatalf("group parent Relation: %v", err)
	}
	if len(parent.Records) != 1 || parent.Records[0].Entity != GroupEntity || parent.Records[0].ID != "engineering" {
		t.Fatalf("parent records = %#v", parent.Records)
	}
	subgroups, err := relationer.Relation(context.Background(), coredatasource.RelationRequest{Entity: GroupEntity, ID: "engineering", Relation: "subgroups"})
	if err != nil {
		t.Fatalf("group subgroups Relation: %v", err)
	}
	if len(subgroups.Records) != 1 || subgroups.Records[0].Entity != GroupEntity || subgroups.Records[0].ID != "engineering/platform" {
		t.Fatalf("subgroup records = %#v", subgroups.Records)
	}
	descendants, err := relationer.Relation(context.Background(), coredatasource.RelationRequest{Entity: GroupEntity, ID: "engineering", Relation: "descendant_groups"})
	if err != nil {
		t.Fatalf("group descendants Relation: %v", err)
	}
	if len(descendants.Records) != 1 || descendants.Records[0].Entity != GroupEntity || descendants.Records[0].ID != "engineering/platform/runtime" {
		t.Fatalf("descendant records = %#v", descendants.Records)
	}
}

func TestMROperationDispatchesSupportedActions(t *testing.T) {
	calls := []string{}
	client := &fakeGitLabClient{
		calls: &calls,
		updatedMR: &gitlab.MergeRequest{BasicMergeRequest: gitlab.BasicMergeRequest{
			IID:       7,
			ProjectID: 12,
			State:     "opened",
			WebURL:    "https://gitlab.example/fluxplane/runtime/-/merge_requests/7",
		}},
		notes: []*gitlab.Note{{ID: 99}},
		pipelines: []*gitlab.PipelineInfo{{
			ID:        123,
			ProjectID: 12,
			Status:    "running",
		}},
	}
	plugin := New(fakeSystem{})
	plugin.ref = resource.PluginRef{Name: Name, Instance: "company-a"}
	plugin.clientFactory = func(context.Context, system.System, resource.PluginRef, Config) (gitlabClient, error) {
		return client, nil
	}
	op := plugin.mrOperation()
	base := map[string]any{"project_id": "12", "merge_request_iid": 7}
	inputs := []map[string]any{
		{"op": "create", "project_id": "12", "title": "New MR", "source_branch": "feature", "target_branch": "main"},
		mergeMaps(base, map[string]any{"op": "close"}),
		mergeMaps(base, map[string]any{"op": "reopen"}),
		mergeMaps(base, map[string]any{"op": "comment", "body": "Looks good"}),
		mergeMaps(base, map[string]any{"op": "approve"}),
		mergeMaps(base, map[string]any{"op": "unapprove"}),
		mergeMaps(base, map[string]any{"op": "merge"}),
		mergeMaps(base, map[string]any{"op": "rebase"}),
		mergeMaps(base, map[string]any{"op": "retry_pipeline", "pipeline_id": 123}),
		mergeMaps(base, map[string]any{"op": "cancel_pipeline", "pipeline_id": 123}),
	}
	for _, input := range inputs {
		result := op.Run(coreoperation.NewContext(context.Background(), nil), input)
		if result.Status != coreoperation.StatusOK {
			t.Fatalf("Run(%v) status = %s error = %#v", input, result.Status, result.Error)
		}
	}
	want := []string{"create", "update", "update", "comment", "approve", "unapprove", "merge", "rebase", "retry_pipeline", "cancel_pipeline"}
	if strings.Join(calls, ",") != strings.Join(want, ",") {
		t.Fatalf("calls = %#v, want %#v", calls, want)
	}
}

func findOperation(ops []coreoperation.Operation, name string) coreoperation.Operation {
	for _, op := range ops {
		if string(op.Spec().Ref.Name) == name {
			return op
		}
	}
	return nil
}

func hasCapability(entity coredatasource.EntitySpec, capability coredatasource.EntityCapability) bool {
	for _, candidate := range entity.Capabilities {
		if candidate == capability {
			return true
		}
	}
	return false
}

func containsCall(calls []string, want string) bool {
	for _, call := range calls {
		if call == want {
			return true
		}
	}
	return false
}

func countCall(calls []string, want string) int {
	count := 0
	for _, call := range calls {
		if call == want {
			count++
		}
	}
	return count
}

func hasDocumentID(documents []coredatasource.CorpusDocument, want string) bool {
	for _, document := range documents {
		if document.Ref.ID == want {
			return true
		}
	}
	return false
}

func documentByID(documents []coredatasource.CorpusDocument, want string) coredatasource.CorpusDocument {
	for _, document := range documents {
		if document.Ref.ID == want {
			return document
		}
	}
	return coredatasource.CorpusDocument{}
}

func toString(value any) string {
	switch value := value.(type) {
	case string:
		return value
	case int:
		return strconv.Itoa(value)
	case int64:
		return strconv.FormatInt(value, 10)
	default:
		return ""
	}
}

func groupMatchesID(group *gitlab.Group, id string) bool {
	if group == nil {
		return false
	}
	return id == strconv.FormatInt(group.ID, 10) || id == group.Path || id == group.FullPath
}

type fakeGitLabClient struct {
	projects               []*gitlab.Project
	groups                 []*gitlab.Group
	subGroups              []*gitlab.Group
	descendantGroups       []*gitlab.Group
	groupProjects          []*gitlab.Project
	groupMembers           []*gitlab.GroupMember
	groupMemberPerPages    *[]int64
	groupMemberUserIDs     *[]int64
	users                  []*gitlab.User
	usersByPublicEmail     map[string][]*gitlab.User
	userPublicEmailQueries *[]string
	currentUser            *gitlab.User
	projectUsers           []*gitlab.ProjectUser
	projectGroups          []*gitlab.ProjectGroup
	projectMembers         []*gitlab.ProjectMember
	projectMemberPerPages  *[]int64
	projectMemberUserIDs   *[]int64
	mrs                    []*gitlab.BasicMergeRequest
	diffs                  []*gitlab.MergeRequestDiff
	notes                  []*gitlab.Note
	pipelines              []*gitlab.PipelineInfo
	branches               []*gitlab.Branch
	tags                   []*gitlab.Tag
	commits                []*gitlab.Commit
	tree                   []*gitlab.TreeNode
	file                   *gitlab.File
	jobs                   []*gitlab.Job
	trace                  []byte
	updatedMR              *gitlab.MergeRequest
	calls                  *[]string
}

func (c fakeGitLabClient) ListProjects(_ context.Context, opts *gitlab.ListProjectsOptions) ([]*gitlab.Project, error) {
	c.record("list_projects")
	if opts == nil || opts.Archived == nil {
		return c.projects, nil
	}
	out := make([]*gitlab.Project, 0, len(c.projects))
	for _, project := range c.projects {
		if project != nil && project.Archived == *opts.Archived {
			out = append(out, project)
		}
	}
	return out, nil
}

func (c fakeGitLabClient) ListGroups(context.Context, *gitlab.ListGroupsOptions) ([]*gitlab.Group, error) {
	c.record("list_groups")
	return c.groups, nil
}

func (c fakeGitLabClient) GetGroup(_ context.Context, id any, _ *gitlab.GetGroupOptions) (*gitlab.Group, error) {
	c.record("get_group")
	want := strings.TrimSpace(toString(id))
	for _, group := range c.groups {
		if groupMatchesID(group, want) {
			return group, nil
		}
	}
	for _, group := range c.subGroups {
		if groupMatchesID(group, want) {
			return group, nil
		}
	}
	for _, group := range c.descendantGroups {
		if groupMatchesID(group, want) {
			return group, nil
		}
	}
	return nil, nil
}

func (c fakeGitLabClient) ListSubGroups(context.Context, any, *gitlab.ListSubGroupsOptions) ([]*gitlab.Group, error) {
	c.record("list_subgroups")
	return c.subGroups, nil
}

func (c fakeGitLabClient) ListDescendantGroups(context.Context, any, *gitlab.ListDescendantGroupsOptions) ([]*gitlab.Group, error) {
	c.record("list_descendant_groups")
	return c.descendantGroups, nil
}

func (c fakeGitLabClient) ListGroupProjects(context.Context, any, *gitlab.ListGroupProjectsOptions) ([]*gitlab.Project, error) {
	c.record("list_group_projects")
	return c.groupProjects, nil
}

func (c fakeGitLabClient) ListGroupMembers(_ context.Context, _ any, opts *gitlab.ListGroupMembersOptions) ([]*gitlab.GroupMember, error) {
	c.record("list_group_members")
	if c.groupMemberPerPages != nil && opts != nil {
		*c.groupMemberPerPages = append(*c.groupMemberPerPages, opts.PerPage)
	}
	if c.groupMemberUserIDs != nil {
		*c.groupMemberUserIDs = append(*c.groupMemberUserIDs, optionGroupUserIDs(opts)...)
	}
	return c.groupMembers, nil
}

func (c fakeGitLabClient) ListUsers(_ context.Context, opts *gitlab.ListUsersOptions) ([]*gitlab.User, error) {
	c.record("list_users")
	if opts != nil && opts.PublicEmail != nil {
		email := strings.ToLower(strings.TrimSpace(*opts.PublicEmail))
		if c.userPublicEmailQueries != nil {
			*c.userPublicEmailQueries = append(*c.userPublicEmailQueries, email)
		}
		if c.usersByPublicEmail != nil {
			return c.usersByPublicEmail[email], nil
		}
	}
	return c.users, nil
}

func (c fakeGitLabClient) GetUser(context.Context, int64, *gitlab.GetUserOptions) (*gitlab.User, error) {
	c.record("get_user")
	if len(c.users) == 0 {
		return nil, nil
	}
	return c.users[0], nil
}

func (c fakeGitLabClient) CurrentUser(context.Context) (*gitlab.User, error) {
	c.record("current_user")
	return c.currentUser, nil
}

func (c fakeGitLabClient) GetProject(context.Context, any, *gitlab.GetProjectOptions) (*gitlab.Project, error) {
	if len(c.projects) == 0 {
		return nil, nil
	}
	return c.projects[0], nil
}

func (c fakeGitLabClient) ListProjectUsers(context.Context, any, *gitlab.ListProjectUserOptions) ([]*gitlab.ProjectUser, error) {
	c.record("list_project_users")
	return c.projectUsers, nil
}

func (c fakeGitLabClient) ListProjectGroups(context.Context, any, *gitlab.ListProjectGroupOptions) ([]*gitlab.ProjectGroup, error) {
	c.record("list_project_groups")
	return c.projectGroups, nil
}

func (c fakeGitLabClient) ListProjectMembers(_ context.Context, _ any, opts *gitlab.ListProjectMembersOptions) ([]*gitlab.ProjectMember, error) {
	c.record("list_project_members")
	if c.projectMemberPerPages != nil && opts != nil {
		*c.projectMemberPerPages = append(*c.projectMemberPerPages, opts.PerPage)
	}
	if c.projectMemberUserIDs != nil {
		*c.projectMemberUserIDs = append(*c.projectMemberUserIDs, optionProjectUserIDs(opts)...)
	}
	return c.projectMembers, nil
}

func (c fakeGitLabClient) ListMergeRequests(context.Context, *gitlab.ListMergeRequestsOptions) ([]*gitlab.BasicMergeRequest, error) {
	return c.mrs, nil
}

func (c fakeGitLabClient) ListProjectMergeRequests(context.Context, any, *gitlab.ListProjectMergeRequestsOptions) ([]*gitlab.BasicMergeRequest, error) {
	return c.mrs, nil
}

func (c fakeGitLabClient) GetMergeRequest(context.Context, any, int64, *gitlab.GetMergeRequestsOptions) (*gitlab.MergeRequest, error) {
	if c.updatedMR != nil {
		return c.updatedMR, nil
	}
	if len(c.mrs) == 0 {
		return nil, nil
	}
	return &gitlab.MergeRequest{BasicMergeRequest: *c.mrs[0]}, nil
}

func (c fakeGitLabClient) ListMergeRequestDiffs(context.Context, any, int64, *gitlab.ListMergeRequestDiffsOptions) ([]*gitlab.MergeRequestDiff, error) {
	return c.diffs, nil
}

func (c fakeGitLabClient) ListMergeRequestNotes(context.Context, any, int64, *gitlab.ListMergeRequestNotesOptions) ([]*gitlab.Note, error) {
	return c.notes, nil
}

func (c fakeGitLabClient) ListMergeRequestPipelines(context.Context, any, int64) ([]*gitlab.PipelineInfo, error) {
	return c.pipelines, nil
}

func (c fakeGitLabClient) GetMergeRequestParticipants(context.Context, any, int64) ([]*gitlab.BasicUser, error) {
	return nil, nil
}

func (c fakeGitLabClient) GetMergeRequestReviewers(context.Context, any, int64) ([]*gitlab.MergeRequestReviewer, error) {
	return nil, nil
}

func (c fakeGitLabClient) ListProjectPipelines(context.Context, any, *gitlab.ListProjectPipelinesOptions) ([]*gitlab.PipelineInfo, error) {
	return c.pipelines, nil
}

func (c fakeGitLabClient) GetPipeline(context.Context, any, int64) (*gitlab.Pipeline, error) {
	if len(c.pipelines) == 0 {
		return nil, nil
	}
	return &gitlab.Pipeline{
		ID:        c.pipelines[0].ID,
		IID:       c.pipelines[0].IID,
		ProjectID: c.pipelines[0].ProjectID,
		Status:    c.pipelines[0].Status,
		Ref:       c.pipelines[0].Ref,
		SHA:       c.pipelines[0].SHA,
		Name:      c.pipelines[0].Name,
		WebURL:    c.pipelines[0].WebURL,
	}, nil
}

func (c fakeGitLabClient) ListBranches(context.Context, any, *gitlab.ListBranchesOptions) ([]*gitlab.Branch, error) {
	c.record("list_branches")
	return c.branches, nil
}

func (c fakeGitLabClient) GetBranch(context.Context, any, string) (*gitlab.Branch, error) {
	c.record("get_branch")
	if len(c.branches) == 0 {
		return nil, nil
	}
	return c.branches[0], nil
}

func (c fakeGitLabClient) ListTags(context.Context, any, *gitlab.ListTagsOptions) ([]*gitlab.Tag, error) {
	c.record("list_tags")
	return c.tags, nil
}

func (c fakeGitLabClient) GetTag(context.Context, any, string) (*gitlab.Tag, error) {
	c.record("get_tag")
	if len(c.tags) == 0 {
		return nil, nil
	}
	return c.tags[0], nil
}

func (c fakeGitLabClient) ListCommits(context.Context, any, *gitlab.ListCommitsOptions) ([]*gitlab.Commit, error) {
	c.record("list_commits")
	return c.commits, nil
}

func (c fakeGitLabClient) GetCommit(context.Context, any, string, *gitlab.GetCommitOptions) (*gitlab.Commit, error) {
	c.record("get_commit")
	if len(c.commits) == 0 {
		return nil, nil
	}
	return c.commits[0], nil
}

func (c fakeGitLabClient) ListMergeRequestsByCommit(context.Context, any, string) ([]*gitlab.BasicMergeRequest, error) {
	c.record("list_merge_requests_by_commit")
	return c.mrs, nil
}

func (c fakeGitLabClient) ListTree(context.Context, any, *gitlab.ListTreeOptions) ([]*gitlab.TreeNode, error) {
	c.record("list_tree")
	return c.tree, nil
}

func (c fakeGitLabClient) GetFile(context.Context, any, string, *gitlab.GetFileOptions) (*gitlab.File, error) {
	c.record("get_file")
	return c.file, nil
}

func (c fakeGitLabClient) ListProjectJobs(context.Context, any, *gitlab.ListJobsOptions) ([]*gitlab.Job, error) {
	c.record("list_project_jobs")
	return c.jobs, nil
}

func (c fakeGitLabClient) ListPipelineJobs(context.Context, any, int64, *gitlab.ListJobsOptions) ([]*gitlab.Job, error) {
	c.record("list_pipeline_jobs")
	return c.jobs, nil
}

func (c fakeGitLabClient) GetJob(context.Context, any, int64) (*gitlab.Job, error) {
	c.record("get_job")
	if len(c.jobs) == 0 {
		return nil, nil
	}
	return c.jobs[0], nil
}

func (c fakeGitLabClient) GetTraceFile(context.Context, any, int64) ([]byte, error) {
	c.record("get_trace_file")
	return c.trace, nil
}

func (c fakeGitLabClient) CreateMergeRequest(context.Context, any, *gitlab.CreateMergeRequestOptions) (*gitlab.MergeRequest, error) {
	c.record("create")
	return c.updatedMR, nil
}

func (c fakeGitLabClient) UpdateMergeRequest(context.Context, any, int64, *gitlab.UpdateMergeRequestOptions) (*gitlab.MergeRequest, error) {
	c.record("update")
	return c.updatedMR, nil
}

func (c fakeGitLabClient) CreateMergeRequestNote(context.Context, any, int64, *gitlab.CreateMergeRequestNoteOptions) (*gitlab.Note, error) {
	c.record("comment")
	if len(c.notes) == 0 {
		return &gitlab.Note{}, nil
	}
	return c.notes[0], nil
}

func (c fakeGitLabClient) ApproveMergeRequest(context.Context, any, int64, *gitlab.ApproveMergeRequestOptions) (*gitlab.MergeRequestApprovals, error) {
	c.record("approve")
	return &gitlab.MergeRequestApprovals{}, nil
}

func (c fakeGitLabClient) UnapproveMergeRequest(context.Context, any, int64) error {
	c.record("unapprove")
	return nil
}

func (c fakeGitLabClient) AcceptMergeRequest(context.Context, any, int64, *gitlab.AcceptMergeRequestOptions) (*gitlab.MergeRequest, error) {
	c.record("merge")
	return c.updatedMR, nil
}

func (c fakeGitLabClient) RebaseMergeRequest(context.Context, any, int64, *gitlab.RebaseMergeRequestOptions) error {
	c.record("rebase")
	return nil
}

func (c fakeGitLabClient) RetryPipelineBuild(context.Context, any, int64) (*gitlab.Pipeline, error) {
	c.record("retry_pipeline")
	return c.GetPipeline(context.Background(), nil, 0)
}

func (c fakeGitLabClient) CancelPipelineBuild(context.Context, any, int64) (*gitlab.Pipeline, error) {
	c.record("cancel_pipeline")
	return c.GetPipeline(context.Background(), nil, 0)
}

func (c fakeGitLabClient) record(call string) {
	if c.calls != nil {
		*c.calls = append(*c.calls, call)
	}
}

type fakeSystem struct {
	network system.Network
	env     system.Environment
}

func (s fakeSystem) Workspace() system.Workspace { return nil }

func (s fakeSystem) Network() system.Network { return s.network }

func (s fakeSystem) Process() system.ProcessManager { return nil }

func (s fakeSystem) Browser() system.BrowserManager { return nil }

func (s fakeSystem) Clarifier() system.Clarifier { return nil }

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

func headerValue(headers map[string]string, key string) string {
	for header, value := range headers {
		if strings.EqualFold(header, key) {
			return value
		}
	}
	return ""
}

func denySecretUseContext() context.Context {
	return policy.ContextWithAuthorization(context.Background(), policy.AuthorizationContext{
		Subjects: []policy.SubjectRef{{Kind: policy.SubjectUser, ID: "timo@localhost"}},
		Trust:    policy.Trust{Kind: policy.TrustInvocation, Level: policy.TrustPrivileged},
		Policy: policy.AuthorizationPolicy{Grants: []policy.Grant{{
			Subjects:      []policy.SubjectRef{{Kind: policy.SubjectUser, ID: "someone-else"}},
			Resources:     []policy.ResourceRef{{Kind: policy.ResourceSecret, Name: "plugin/gitlab/company-a/access_token"}},
			Actions:       []policy.Action{policy.ActionSecretUse},
			RequiredTrust: policy.TrustPrivileged,
		}}},
	})
}

func mergeMaps(base, overlay map[string]any) map[string]any {
	out := map[string]any{}
	for key, value := range base {
		out[key] = value
	}
	for key, value := range overlay {
		out[key] = value
	}
	return out
}

func optionGroupUserIDs(opts *gitlab.ListGroupMembersOptions) []int64 {
	if opts == nil || opts.UserIDs == nil {
		return nil
	}
	return append([]int64(nil), (*opts.UserIDs)...)
}

func optionProjectUserIDs(opts *gitlab.ListProjectMembersOptions) []int64 {
	if opts == nil || opts.UserIDs == nil {
		return nil
	}
	return append([]int64(nil), (*opts.UserIDs)...)
}
