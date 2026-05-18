package gitlabplugin

import (
	"context"
	"strings"
	"testing"

	coredatasource "github.com/fluxplane/agentruntime/core/datasource"
	coreoperation "github.com/fluxplane/agentruntime/core/operation"
	"github.com/fluxplane/agentruntime/core/resource"
	"github.com/fluxplane/agentruntime/orchestration/pluginhost"
	"github.com/fluxplane/agentruntime/runtime/system"
	gitlab "gitlab.com/gitlab-org/api/client-go/v2"
)

func TestPluginContributesNamedGitLabOperations(t *testing.T) {
	bundle, err := New(nil).Contributions(context.Background(), pluginhost.Context{Ref: resource.PluginRef{Name: Name, Instance: "company-a"}})
	if err != nil {
		t.Fatalf("Contributions: %v", err)
	}
	if len(bundle.Operations) != 2 {
		t.Fatalf("operations len = %d, want 2", len(bundle.Operations))
	}
	names := map[string]bool{}
	for _, spec := range bundle.Operations {
		names[string(spec.Ref.Name)] = true
	}
	for _, want := range []string{"company_a_project_search", "company_a_project_get"} {
		if !names[want] {
			t.Fatalf("operation names = %#v, missing %q", names, want)
		}
	}
}

func TestPluginSearchOperationUsesInjectedClient(t *testing.T) {
	plugin := New(fakeSystem{})
	plugin.ref = resource.PluginRef{Name: Name, Instance: "company-a"}
	plugin.clientFactory = func(context.Context, system.System, Config) (gitlabClient, error) {
		return fakeGitLabClient{projects: []*gitlab.Project{
			{
				ID:                12,
				Name:              "runtime",
				PathWithNamespace: "fluxplane/runtime",
				WebURL:            "https://gitlab.example/fluxplane/runtime",
			},
		}}, nil
	}
	ops, err := plugin.Operations(context.Background(), pluginhost.Context{})
	if err != nil {
		t.Fatalf("Operations: %v", err)
	}
	op := findOperation(ops, "company_a_project_search")
	if op == nil {
		t.Fatalf("company_a_project_search operation not found")
	}
	result := op.Run(coreoperation.NewContext(context.Background(), nil), map[string]any{"query": "runtime"})
	if result.Status != coreoperation.StatusOK {
		t.Fatalf("operation status = %s, error = %#v", result.Status, result.Error)
	}
	output, ok := result.Output.(projectSearchOutput)
	if !ok {
		t.Fatalf("output type = %T, want projectSearchOutput", result.Output)
	}
	if len(output.Projects) != 1 || output.Projects[0].PathWithNamespace != "fluxplane/runtime" {
		t.Fatalf("projects = %#v", output.Projects)
	}
}

func TestOfficialClientUsesSystemNetworkAndTokenEnv(t *testing.T) {
	network := &recordingNetwork{
		response: system.HTTPResponse{
			StatusCode: 200,
			Headers:    map[string][]string{"Content-Type": {"application/json"}},
			Body:       []byte(`[{"id":12,"name":"runtime","path_with_namespace":"fluxplane/runtime","web_url":"https://gitlab.example/fluxplane/runtime"}]`),
		},
	}
	client, err := newOfficialClient(context.Background(), fakeSystem{
		network: network,
		env:     fakeEnvironment{values: map[string]string{"GITLAB_TOKEN": "secret-token"}},
	}, Config{
		BaseURL: "https://gitlab.example",
		Auth:    AuthConfig{TokenEnv: "GITLAB_TOKEN"},
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

func TestDatasourceProviderSearchesProjects(t *testing.T) {
	provider := gitlabDatasourceProvider{
		system: fakeSystem{},
		ref:    resource.PluginRef{Name: Name, Instance: "company-a"},
		clientFactory: func(context.Context, system.System, Config) (gitlabClient, error) {
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

func findOperation(ops []coreoperation.Operation, name string) coreoperation.Operation {
	for _, op := range ops {
		if string(op.Spec().Ref.Name) == name {
			return op
		}
	}
	return nil
}

type fakeGitLabClient struct {
	projects []*gitlab.Project
}

func (c fakeGitLabClient) ListProjects(context.Context, *gitlab.ListProjectsOptions) ([]*gitlab.Project, error) {
	return c.projects, nil
}

func (c fakeGitLabClient) GetProject(context.Context, any, *gitlab.GetProjectOptions) (*gitlab.Project, error) {
	if len(c.projects) == 0 {
		return nil, nil
	}
	return c.projects[0], nil
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
