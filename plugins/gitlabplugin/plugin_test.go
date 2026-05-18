package gitlabplugin

import (
	"context"
	"strings"
	"testing"

	coredatasource "github.com/fluxplane/agentruntime/core/datasource"
	coreoperation "github.com/fluxplane/agentruntime/core/operation"
	"github.com/fluxplane/agentruntime/core/policy"
	"github.com/fluxplane/agentruntime/core/resource"
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
	if method.Name != personalAccessTokenMethod || method.Method != "env" || method.Kind != "api_key" {
		t.Fatalf("method = %#v", method)
	}
	if method.Env.Name != gitlabPersonalAccessTokenEnv {
		t.Fatalf("env name = %q", method.Env.Name)
	}
	if len(method.Env.Aliases) != 4 || method.Env.Aliases[0] != gitlabAccessTokenEnv || method.Env.Aliases[1] != gitlabPersonalAccessTokenEnv || method.Env.Aliases[2] != gitlabPersonalTokenEnv || method.Env.Aliases[3] != gitlabTokenEnv {
		t.Fatalf("env aliases = %#v", method.Env.Aliases)
	}
	if method.Header.Name != "Private-Token" {
		t.Fatalf("header = %#v", method.Header)
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
	if len(result.Records) != 1 || result.Records[0].ID != "fluxplane/runtime" {
		t.Fatalf("records = %#v", result.Records)
	}
}

func TestDatasourceProviderIndexedSearchUsesFieldIndex(t *testing.T) {
	index, err := semantic.New(semantic.HashEmbedder{ModelName: "test-embedding"}, semantic.NewJSONStore(""), semantic.Config{})
	if err != nil {
		t.Fatalf("semantic.New: %v", err)
	}
	_, err = index.UpdateRecord(context.Background(), coredatasource.CorpusDocument{
		Ref:   coredatasource.RecordRef{Datasource: "company-a-gitlab", Entity: ProjectEntity, ID: "fluxplane/runtime"},
		Title: "fluxplane/runtime",
		Body:  "Runtime repository for agent execution",
		Metadata: map[string]string{
			"id":                  "12",
			"name":                "runtime",
			"path_with_namespace": "fluxplane/runtime",
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
	if len(result.Records) != 1 || result.Records[0].ID != "fluxplane/runtime" {
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
	for _, want := range []coredatasource.EntityType{ProjectEntity, MergeRequestEntity, MergeRequestDiffEntity, MergeRequestNoteEntity, PipelineEntity, UserEntity, GroupEntity} {
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
	for _, typ := range []coredatasource.EntityType{MergeRequestEntity, MergeRequestDiffEntity, MergeRequestNoteEntity, PipelineEntity} {
		if hasCapability(entities[typ], coredatasource.EntityCapabilityList) {
			t.Fatalf("entity %s capabilities = %#v, want no list", typ, entities[typ].Capabilities)
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
	if userRelations["groups"] != GroupEntity {
		t.Fatalf("user groups relation = %s, want %s", userRelations["groups"], GroupEntity)
	}
	projectRelations := map[string]coredatasource.EntityType{}
	for _, relation := range entities[ProjectEntity].Relations {
		projectRelations[relation.Name] = relation.TargetEntity
	}
	for name, target := range map[string]coredatasource.EntityType{
		"merge_requests": MergeRequestEntity,
		"pipelines":      PipelineEntity,
		"users":          UserEntity,
		"groups":         GroupEntity,
	} {
		if projectRelations[name] != target {
			t.Fatalf("project relation %s = %s, want %s", name, projectRelations[name], target)
		}
	}
	groupRelations := map[string]coredatasource.EntityType{}
	for _, relation := range entities[GroupEntity].Relations {
		groupRelations[relation.Name] = relation.TargetEntity
	}
	if groupRelations["projects"] != ProjectEntity {
		t.Fatalf("group projects relation = %s, want %s", groupRelations["projects"], ProjectEntity)
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
	if len(projects.Records) != 1 || projects.Records[0].Entity != ProjectEntity || projects.Records[0].ID != "engineering/runtime" {
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
				memberships: []*gitlab.UserMembership{{
					SourceID:    7,
					SourceName:  "Engineering / Platform",
					SourceType:  "Namespace",
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
	if len(result.Records) != 1 || result.Records[0].Entity != GroupEntity || result.Records[0].ID != "7" {
		t.Fatalf("group records = %#v", result.Records)
	}
	if result.Records[0].Metadata["role"] != "developer" {
		t.Fatalf("group role = %#v, want developer", result.Records[0].Metadata)
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
	if len(result.Records) != 1 || result.Records[0].Entity != ProjectEntity || result.Records[0].ID != "engineering/runtime" {
		t.Fatalf("project records = %#v", result.Records)
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

type fakeGitLabClient struct {
	projects               []*gitlab.Project
	groups                 []*gitlab.Group
	groupProjects          []*gitlab.Project
	users                  []*gitlab.User
	usersByPublicEmail     map[string][]*gitlab.User
	userPublicEmailQueries *[]string
	projectUsers           []*gitlab.ProjectUser
	projectGroups          []*gitlab.ProjectGroup
	memberships            []*gitlab.UserMembership
	mrs                    []*gitlab.BasicMergeRequest
	diffs                  []*gitlab.MergeRequestDiff
	notes                  []*gitlab.Note
	pipelines              []*gitlab.PipelineInfo
	updatedMR              *gitlab.MergeRequest
	calls                  *[]string
}

func (c fakeGitLabClient) ListProjects(context.Context, *gitlab.ListProjectsOptions) ([]*gitlab.Project, error) {
	c.record("list_projects")
	return c.projects, nil
}

func (c fakeGitLabClient) ListGroups(context.Context, *gitlab.ListGroupsOptions) ([]*gitlab.Group, error) {
	c.record("list_groups")
	return c.groups, nil
}

func (c fakeGitLabClient) GetGroup(context.Context, any, *gitlab.GetGroupOptions) (*gitlab.Group, error) {
	c.record("get_group")
	if len(c.groups) == 0 {
		return nil, nil
	}
	return c.groups[0], nil
}

func (c fakeGitLabClient) ListGroupProjects(context.Context, any, *gitlab.ListGroupProjectsOptions) ([]*gitlab.Project, error) {
	c.record("list_group_projects")
	return c.groupProjects, nil
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

func (c fakeGitLabClient) GetUserMemberships(context.Context, int64, *gitlab.GetUserMembershipOptions) ([]*gitlab.UserMembership, error) {
	c.record("get_user_memberships")
	return c.memberships, nil
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
