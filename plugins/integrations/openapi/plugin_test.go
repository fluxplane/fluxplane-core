package openapi

import (
	"context"
	runtimeworkspace "github.com/fluxplane/fluxplane-core/runtime/workspace"
	"os"
	"path/filepath"
	"strings"
	"testing"

	auth "github.com/fluxplane/fluxplane-auth"
	coredatasource "github.com/fluxplane/fluxplane-core/core/datasource"
	"github.com/fluxplane/fluxplane-core/core/operation"
	"github.com/fluxplane/fluxplane-core/core/resource"
	"github.com/fluxplane/fluxplane-core/orchestration/pluginhost"
	fpsystem "github.com/fluxplane/fluxplane-system"
	"github.com/fluxplane/fluxplane-system/systemkit"
	fpsystemtest "github.com/fluxplane/fluxplane-system/systemtest"
)

func TestPluginGeneratesOperationsDatasourceAndAuthMethods(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	writeFixture(t, dir)
	sys := newTestSystem(t, dir, nil, nil)
	host, err := pluginhost.New(New(sys))
	if err != nil {
		t.Fatalf("pluginhost.New: %v", err)
	}
	resolved, err := host.Resolve(ctx, testPluginRef())
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(resolved.Bundles) != 1 {
		t.Fatalf("bundles = %d, want 1", len(resolved.Bundles))
	}
	bundle := resolved.Bundles[0]
	if len(bundle.Operations) != 2 {
		t.Fatalf("operations = %d, want 2", len(bundle.Operations))
	}
	if findSpec(bundle.Operations, "users_get_user").Ref.Name == "" {
		t.Fatalf("users_get_user was not generated: %#v", bundle.Operations)
	}
	if len(bundle.Datasources) != 1 || bundle.Datasources[0].Name != "users_api_docs" {
		t.Fatalf("datasources = %#v, want users_api_docs", bundle.Datasources)
	}
	if len(resolved.AuthMethods) != 1 {
		t.Fatalf("auth methods = %d, want 1", len(resolved.AuthMethods))
	}
	method := resolved.AuthMethods[0].Method
	if method.Name != "bearerAuth" || method.Method != auth.MethodEnv || method.Env.Name != "TESTUSER_PASSWORD" {
		t.Fatalf("auth method = %#v", method)
	}
}

func TestGeneratedOperationExecutesWithBearerAuth(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	writeFixture(t, dir)
	network := &recordingNetwork{response: systemkit.HTTPResponse{StatusCode: 200, Headers: map[string][]string{"Content-Type": {"application/json"}}, Body: []byte(`{"id":"42","name":"Ada"}`)}}
	sys := newTestSystem(t, dir, network, map[string]string{"TESTUSER_PASSWORD": "secret-token"})
	host, err := pluginhost.New(New(sys))
	if err != nil {
		t.Fatalf("pluginhost.New: %v", err)
	}
	resolved, err := host.Resolve(ctx, testPluginRef())
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	op := findOperation(t, resolved.Operations, "users_get_user")
	result := op.Run(operation.NewContext(ctx, nil), map[string]any{
		"path":  map[string]any{"id": "42"},
		"query": map[string]any{"verbose": true},
	})
	if result.Status != operation.StatusOK {
		t.Fatalf("status = %s error=%#v", result.Status, result.Error)
	}
	if got, want := network.request.URL, "https://api.example.test/v1/users/42?verbose=true"; got != want {
		t.Fatalf("url = %q, want %q", got, want)
	}
	if got := network.request.Headers["Authorization"]; got != "Bearer secret-token" {
		t.Fatalf("authorization = %q", got)
	}
}

func TestGeneratedOperationExecutesWithOAuthBearerAuth(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	writeOAuthFixture(t, dir)
	network := &recordingNetwork{response: systemkit.HTTPResponse{StatusCode: 200, Body: []byte(`{}`)}}
	sys := newTestSystem(t, dir, network, map[string]string{"OAUTH_API_TOKEN": "oauth-token"})
	host, err := pluginhost.New(New(sys))
	if err != nil {
		t.Fatalf("pluginhost.New: %v", err)
	}
	ref := resource.PluginRef{
		Name:     Name,
		Instance: "manager",
		Config: map[string]any{
			"specs": []map[string]any{{
				"file":       "oauth-openapi.yaml",
				"operations": map[string]any{"prefix": "manager", "include": []string{"listAccounts"}},
				"auth": map[string]any{"schemes": map[string]any{"oauth2": map[string]any{
					"method": "env",
					"env":    map[string]any{"name": "OAUTH_API_TOKEN"},
				}}},
			}},
		},
	}
	resolved, err := host.Resolve(ctx, ref)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	op := findOperation(t, resolved.Operations, "manager_list_accounts")
	result := op.Run(operation.NewContext(ctx, nil), nil)
	if result.Status != operation.StatusOK {
		t.Fatalf("status = %s error=%#v", result.Status, result.Error)
	}
	if got := network.request.URL; got != "https://api.oauth.example.test/api/v2/accounts" {
		t.Fatalf("url = %q", got)
	}
	if got := network.request.Headers["Authorization"]; got != "Bearer oauth-token" {
		t.Fatalf("authorization = %q", got)
	}
}

func TestDocumentationDatasourceSearchAndCorpus(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	writeFixture(t, dir)
	sys := newTestSystem(t, dir, nil, nil)
	host, err := pluginhost.New(New(sys))
	if err != nil {
		t.Fatalf("pluginhost.New: %v", err)
	}
	resolved, err := host.Resolve(ctx, testPluginRef())
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	provider := resolved.DatasourceProviders[0].Provider
	accessor, err := provider.Open(ctx, resolved.Bundles[0].Datasources[0])
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	searcher := accessor.(coredatasource.Searcher)
	result, err := searcher.Search(ctx, coredatasource.SearchRequest{Entity: OperationEntity, Query: "Get user", Limit: 10})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(result.Records) != 1 || result.Records[0].ID != "operation:get_user" {
		t.Fatalf("records = %#v, want operation:get_user", result.Records)
	}
	corpus, err := accessor.(coredatasource.CorpusProvider).Corpus(ctx, coredatasource.CorpusRequest{Entity: SchemaEntity})
	if err != nil {
		t.Fatalf("Corpus: %v", err)
	}
	if len(corpus.Documents) != 1 || corpus.Documents[0].Ref.ID != "schema:User" {
		t.Fatalf("corpus = %#v, want schema:User", corpus.Documents)
	}
}

func testPluginRef() resource.PluginRef {
	return resource.PluginRef{
		Name:     Name,
		Instance: "users",
		Config: map[string]any{
			"specs": []map[string]any{{
				"file": "openapi.yaml",
				"operations": map[string]any{
					"prefix":  "users",
					"include": []string{"getUser", "listUsers"},
				},
				"datasource": map[string]any{"name": "users_api_docs", "index": map[string]any{"enabled": true}},
				"auth": map[string]any{"schemes": map[string]any{"bearerAuth": map[string]any{
					"method": "env",
					"kind":   "bearer_token",
					"env":    map[string]any{"name": "TESTUSER_PASSWORD"},
				}}},
			}},
		},
	}
}

func TestPluginLoadsSpecWithRefSiblingDescription(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	spec := `openapi: 3.0.3
info:
  title: Ref Sibling API
  version: "1.0"
paths:
  /apps:
    get:
      operationId: listApps
      responses:
        "200":
          description: OK
components:
  schemas:
    Application:
      $ref: "#/components/schemas/BaseApplication"
      description: Application metadata.
    BaseApplication:
      type: object
      properties:
        id:
          type: string
`
	if err := os.WriteFile(filepath.Join(dir, "ref-sibling.yaml"), []byte(spec), 0644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	sys := newTestSystem(t, dir, nil, nil)
	host, err := pluginhost.New(New(sys))
	if err != nil {
		t.Fatalf("pluginhost.New: %v", err)
	}
	resolved, err := host.Resolve(ctx, resource.PluginRef{
		Name:     Name,
		Instance: "ref-sibling",
		Config: map[string]any{
			"specs": []map[string]any{{
				"file": "ref-sibling.yaml",
				"operations": map[string]any{
					"prefix": "ref",
				},
				"datasource": map[string]any{"name": "ref_api_docs"},
			}},
		},
	})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if findSpec(resolved.Bundles[0].Operations, "ref_list_apps").Ref.Name == "" {
		t.Fatalf("ref_list_apps was not generated")
	}
}

func writeFixture(t *testing.T, dir string) {
	t.Helper()
	spec := `openapi: 3.0.3
info:
  title: Users API
  version: "1.0"
servers:
  - url: https://api.example.test/v1
security:
  - bearerAuth: []
paths:
  /users:
    get:
      operationId: listUsers
      summary: List users
      responses:
        "200":
          description: User list
  /users/{id}:
    get:
      operationId: getUser
      summary: Get user
      parameters:
        - name: id
          in: path
          required: true
          schema:
            type: string
        - name: verbose
          in: query
          schema:
            type: boolean
      responses:
        "200":
          description: User response
          content:
            application/json:
              schema:
                $ref: "#/components/schemas/User"
components:
  securitySchemes:
    bearerAuth:
      type: http
      scheme: bearer
  schemas:
    User:
      type: object
      required: [id]
      properties:
        id:
          type: string
        name:
          type: string
`
	if err := os.WriteFile(filepath.Join(dir, "openapi.yaml"), []byte(spec), 0644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
}

func writeOAuthFixture(t *testing.T, dir string) {
	t.Helper()
	spec := `openapi: 3.0.3
info:
  title: OAuth API
  version: "1.0"
servers:
  - url: https://api.oauth.example.test/
security:
  - oauth2:
      - "*"
  - accessId: []
    accessToken: []
paths:
  /api/v2/accounts:
    get:
      operationId: listAccounts
      responses:
        "200":
          description: Response
components:
  securitySchemes:
    oauth2:
      type: oauth2
      flows:
        password:
          tokenUrl: /oauth/token
          scopes:
            "*": All Access
    accessId:
      type: apiKey
      in: header
      name: X-Auth-Access-Id
    accessToken:
      type: apiKey
      in: header
      name: X-Auth-Access-Token
`
	if err := os.WriteFile(filepath.Join(dir, "oauth-openapi.yaml"), []byte(spec), 0644); err != nil {
		t.Fatalf("write oauth fixture: %v", err)
	}
}

type testSystem struct {
	workspace runtimeworkspace.Workspace
	network   fpsystem.Network
	env       fpsystem.Environment
}

func newTestSystem(t *testing.T, dir string, network fpsystem.Network, env map[string]string) fpsystem.System {
	t.Helper()
	host, err := runtimeworkspace.NewHost(runtimeworkspace.Config{Root: dir, AllowPrivateNetwork: true})
	if err != nil {
		t.Fatalf("NewHost: %v", err)
	}
	if network == nil {
		network = &recordingNetwork{response: systemkit.HTTPResponse{StatusCode: 200, Body: []byte(`{}`)}}
	}
	return testSystem{workspace: host.Workspace(), network: network, env: testEnv{values: env}}
}

func (s testSystem) Workspace() runtimeworkspace.Workspace { return s.workspace }
func (s testSystem) Network() fpsystem.Network             { return s.network }
func (s testSystem) Process() fpsystem.ProcessManager      { return nil }
func (s testSystem) Environment() fpsystem.Environment     { return s.env }
func (s testSystem) FileSystem() fpsystem.FileSystem {
	if s.workspace == nil {
		return nil
	}
	return s.workspace.System().FileSystem()
}
func (s testSystem) Clock() fpsystem.Clock {
	sys, _ := systemkit.NewSystem().WithRealClock().Build()
	return sys.Clock()
}

type testEnv struct {
	values map[string]string
}

func (e testEnv) Lookup(_ context.Context, key string) (string, bool, error) {
	value, ok := e.values[key]
	return value, ok, nil
}

type recordingNetwork struct {
	fpsystemtest.UnsupportedNetwork
	request  systemkit.HTTPRequest
	response systemkit.HTTPResponse
}

func (n *recordingNetwork) DoHTTP(_ context.Context, req systemkit.HTTPRequest) (systemkit.HTTPResponse, error) {
	n.request = req
	if n.response.StatusCode == 0 {
		n.response.StatusCode = 200
	}
	return n.response, nil
}

func findOperation(t *testing.T, ops []pluginhost.OperationContribution, name string) operation.Operation {
	t.Helper()
	for _, op := range ops {
		if string(op.Operation.Spec().Ref.Name) == name {
			return op.Operation
		}
	}
	var names []string
	for _, op := range ops {
		names = append(names, string(op.Operation.Spec().Ref.Name))
	}
	t.Fatalf("operation %q not found in %s", name, strings.Join(names, ", "))
	return nil
}

func findSpec(specs []operation.Spec, name string) operation.Spec {
	for _, spec := range specs {
		if string(spec.Ref.Name) == name {
			return spec
		}
	}
	return operation.Spec{}
}
