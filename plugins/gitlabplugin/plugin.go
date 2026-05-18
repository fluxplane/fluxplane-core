package gitlabplugin

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	coredatasource "github.com/fluxplane/agentruntime/core/datasource"
	"github.com/fluxplane/agentruntime/core/operation"
	"github.com/fluxplane/agentruntime/core/policy"
	"github.com/fluxplane/agentruntime/core/resource"
	coresecret "github.com/fluxplane/agentruntime/core/secret"
	"github.com/fluxplane/agentruntime/orchestration/pluginhost"
	runtimedatasource "github.com/fluxplane/agentruntime/runtime/datasource"
	operationruntime "github.com/fluxplane/agentruntime/runtime/operation"
	runtimesecret "github.com/fluxplane/agentruntime/runtime/secret"
	"github.com/fluxplane/agentruntime/runtime/system"
	gitlab "gitlab.com/gitlab-org/api/client-go/v2"
	"golang.org/x/oauth2"
)

const (
	Name            = "gitlab"
	projectSearchOp = "project_search"
	projectGetOp    = "project_get"
	defaultBaseURL  = "https://gitlab.com"

	accessTokenPurpose           = "access_token"
	personalAccessTokenMethod    = "personal_access_token"
	oauth2Method                 = "oauth2"
	gitlabAccessTokenEnv         = "GITLAB_ACCESS_TOKEN"
	gitlabPersonalAccessTokenEnv = "GITLAB_PERSONAL_ACCESS_TOKEN"
	gitlabPersonalTokenEnv       = "GITLAB_PERSONAL_TOKEN"
	gitlabTokenEnv               = "GITLAB_TOKEN"
)

type Plugin struct {
	pluginhost.Configurable[Config]
	system        system.System
	ref           resource.PluginRef
	cfg           Config
	clientFactory gitlabClientFactory
}

// Config is the per-instance GitLab plugin configuration accepted from an app
// manifest.
type Config struct {
	BaseURL string     `json:"base_url,omitempty"`
	Auth    AuthConfig `json:"auth,omitempty"`
}

type AuthConfig struct {
	Method   string `json:"method,omitempty"`
	TokenEnv string `json:"token_env,omitempty"`
}

type gitlabClient interface {
	ListProjects(context.Context, *gitlab.ListProjectsOptions) ([]*gitlab.Project, error)
	GetProject(context.Context, any, *gitlab.GetProjectOptions) (*gitlab.Project, error)
}

type gitlabClientFactory func(context.Context, system.System, resource.PluginRef, Config) (gitlabClient, error)

var _ pluginhost.Plugin = Plugin{}
var _ pluginhost.InstanceFactory = Plugin{}
var _ pluginhost.OperationContributor = Plugin{}
var _ pluginhost.DatasourceProviderContributor = Plugin{}
var _ pluginhost.AuthMethodContributor = Plugin{}

func New(sys system.System) Plugin {
	return Plugin{system: sys, clientFactory: newOfficialClient}
}

func (Plugin) Manifest() pluginhost.Manifest {
	return pluginhost.Manifest{Name: Name, Description: "GitLab project operations."}
}

func (p Plugin) Instantiate(_ context.Context, ctx pluginhost.Context) (pluginhost.Plugin, error) {
	cfg, err := pluginhost.ConfigAs[Config](ctx)
	if err != nil {
		return nil, err
	}
	return Plugin{system: p.system, ref: ctx.Ref, cfg: normalizeConfig(cfg), clientFactory: p.clientFactory}, nil
}

func (p Plugin) Contributions(_ context.Context, ctx pluginhost.Context) (resource.ContributionBundle, error) {
	p = p.withRef(ctx.Ref)
	search := operationSpec(p.operationName(projectSearchOp), "Search GitLab projects by name.", projectSearchInput{}, projectSearchOutput{})
	get := operationSpec(p.operationName(projectGetOp), "Get one GitLab project by numeric id or path with namespace.", projectGetInput{}, Project{})
	return resource.ContributionBundle{Operations: []operation.Spec{search, get}}, nil
}

func (p Plugin) Operations(_ context.Context, ctx pluginhost.Context) ([]operation.Operation, error) {
	p = p.withRef(ctx.Ref)
	return []operation.Operation{
		operationruntime.NewTypedResult[projectSearchInput, projectSearchOutput](
			operationSpec(p.operationName(projectSearchOp), "Search GitLab projects by name.", projectSearchInput{}, projectSearchOutput{}),
			p.searchProjects,
			operationruntime.WithAccess(p.searchAccess),
		),
		operationruntime.NewTypedResult[projectGetInput, Project](
			operationSpec(p.operationName(projectGetOp), "Get one GitLab project by numeric id or path with namespace.", projectGetInput{}, Project{}),
			p.getProject,
			operationruntime.WithAccess(p.getAccess),
		),
	}, nil
}

func (p Plugin) DatasourceProviders(_ context.Context, ctx pluginhost.Context) ([]coredatasource.Provider, error) {
	p = p.withRef(ctx.Ref)
	return []coredatasource.Provider{gitlabDatasourceProvider{
		system:        p.system,
		ref:           p.ref,
		config:        p.config(),
		clientFactory: p.clientFactory,
	}}, nil
}

func (p Plugin) AuthMethods(_ context.Context, ctx pluginhost.Context) ([]coresecret.AuthMethodSpec, error) {
	p = p.withRef(ctx.Ref)
	return p.authMethods(), nil
}

func (p Plugin) withRef(ref resource.PluginRef) Plugin {
	if p.ref.Name == "" && ref.Name != "" {
		p.ref = ref
	}
	return p
}

func operationSpec[I, O any](name, description string, _ I, _ O) operation.Spec {
	return operationruntime.WithTypedContract[I, O](operation.Spec{
		Ref:         operation.Ref{Name: operation.Name(name)},
		Description: description,
		Semantics: operation.Semantics{
			Determinism: operation.DeterminismNonDeterministic,
			Effects:     operation.EffectSet{operation.EffectNetwork, operation.EffectReadExternal},
			Idempotency: operation.IdempotencyIdempotent,
			Risk:        operation.RiskLow,
		},
	})
}

func (p Plugin) operationName(suffix string) string {
	prefix := normalize(p.ref.InstanceName())
	if prefix == "" {
		prefix = Name
	}
	return prefix + "_" + suffix
}

func (p Plugin) config() Config {
	return p.cfg
}

func (p Plugin) client(ctx context.Context) (gitlabClient, error) {
	if p.system == nil {
		return nil, fmt.Errorf("gitlabplugin: system is nil")
	}
	factory := p.clientFactory
	if factory == nil {
		factory = newOfficialClient
	}
	return factory(ctx, p.system, p.ref, p.config())
}

func (p Plugin) searchProjects(ctx operation.Context, req projectSearchInput) operation.Result {
	if strings.TrimSpace(req.Query) == "" {
		return operation.Failed("invalid_"+p.operationName(projectSearchOp)+"_input", "query is required", nil)
	}
	client, err := p.client(ctx)
	if err != nil {
		return operation.Failed(p.operationName(projectSearchOp)+"_failed", err.Error(), nil)
	}
	projects, err := searchProjects(ctx, client, req.Query, req.PerPage, req.Page)
	if err != nil {
		return operation.Failed(p.operationName(projectSearchOp)+"_failed", err.Error(), nil)
	}
	return operation.OK(projectSearchOutput{Projects: projects})
}

func (p Plugin) getProject(ctx operation.Context, req projectGetInput) operation.Result {
	id := strings.TrimSpace(req.ID)
	if id == "" {
		return operation.Failed("invalid_"+p.operationName(projectGetOp)+"_input", "id is required", nil)
	}
	client, err := p.client(ctx)
	if err != nil {
		return operation.Failed(p.operationName(projectGetOp)+"_failed", err.Error(), nil)
	}
	project, err := getProject(ctx, client, id)
	if err != nil {
		return operation.Failed(p.operationName(projectGetOp)+"_failed", err.Error(), nil)
	}
	return operation.OK(project)
}

func (p Plugin) searchAccess(operation.Context, projectSearchInput) ([]operationruntime.AccessDescriptor, error) {
	return p.networkAccess()
}

func (p Plugin) getAccess(operation.Context, projectGetInput) ([]operationruntime.AccessDescriptor, error) {
	return p.networkAccess()
}

func (p Plugin) networkAccess() ([]operationruntime.AccessDescriptor, error) {
	return []operationruntime.AccessDescriptor{operationruntime.NetworkDescriptor(p.config().baseURL(), policy.ActionNetworkFetch)}, nil
}

func normalizeConfig(cfg Config) Config {
	cfg.BaseURL = strings.TrimSpace(cfg.BaseURL)
	cfg.Auth.Method = strings.TrimSpace(cfg.Auth.Method)
	cfg.Auth.TokenEnv = strings.TrimSpace(cfg.Auth.TokenEnv)
	return cfg
}

func (c Config) baseURL() string {
	if baseURL := strings.TrimSpace(c.BaseURL); baseURL != "" {
		return baseURL
	}
	return defaultBaseURL
}

func (c Config) authMethods(ref resource.PluginRef) []coresecret.AuthMethodSpec {
	methods := []coresecret.AuthMethodSpec{}
	authMethod := strings.ToLower(strings.TrimSpace(c.Auth.Method))
	if authMethod == "" || authMethod == "env" || authMethod == "personal_access_token" || authMethod == "personal-access-token" {
		methods = append(methods, personalAccessTokenAuthMethod(ref, c.Auth.TokenEnv))
	}
	if authMethod == "" || authMethod == "oauth2" {
		methods = append(methods, oauth2AuthMethod(ref, c.baseURL()))
	}
	return methods
}

func (p Plugin) authMethods() []coresecret.AuthMethodSpec {
	return p.config().authMethods(p.ref)
}

func personalAccessTokenAuthMethod(_ resource.PluginRef, tokenEnv string) coresecret.AuthMethodSpec {
	return coresecret.AuthMethodSpec{
		Name:        personalAccessTokenMethod,
		Method:      coresecret.AuthMethodEnv,
		Kind:        coresecret.KindAPIKey,
		DisplayName: "GitLab personal access token",
		Description: "GitLab personal access token resolved from a configured environment variable or known aliases.",
		Env: coresecret.EnvSpec{
			Name:    strings.TrimSpace(tokenEnv),
			Aliases: tokenEnvAliases(),
		},
		Header: coresecret.HeaderSpec{Name: "Private-Token"},
	}
}

func oauth2AuthMethod(ref resource.PluginRef, baseURL string) coresecret.AuthMethodSpec {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" {
		baseURL = defaultBaseURL
	}
	return coresecret.AuthMethodSpec{
		Name:        oauth2Method,
		Method:      coresecret.AuthMethodOAuth2,
		Kind:        coresecret.KindOAuth2Token,
		DisplayName: "GitLab OAuth2",
		Description: "GitLab OAuth2 authorization-code credentials stored for this plugin instance.",
		Secret:      coresecret.Plugin(Name, ref.InstanceName(), oauth2Method+"_token"),
		Header:      coresecret.HeaderSpec{Name: "Authorization", Scheme: "Bearer"},
		OAuth2: coresecret.OAuth2Spec{
			AuthorizeURL: baseURL + "/oauth/authorize",
			TokenURL:     baseURL + "/oauth/token",
			RefreshURL:   baseURL + "/oauth/token",
			Scopes:       []string{"read_api"},
		},
	}
}

func tokenEnvAliases() []string {
	return []string{gitlabAccessTokenEnv, gitlabPersonalAccessTokenEnv, gitlabPersonalTokenEnv, gitlabTokenEnv}
}

func newOfficialClient(ctx context.Context, sys system.System, ref resource.PluginRef, cfg Config) (gitlabClient, error) {
	if sys == nil {
		return nil, fmt.Errorf("gitlabplugin: system is nil")
	}
	auth, err := authFromSecrets(ctx, sys, ref, cfg)
	if err != nil {
		return nil, err
	}
	options := []gitlab.ClientOptionFunc{
		gitlab.WithBaseURL(cfg.baseURL()),
		gitlab.WithHTTPClient(system.NewHTTPClient(sys.Network())),
		gitlab.WithoutRetries(),
	}
	var client *gitlab.Client
	switch auth.Material.Kind {
	case coresecret.KindAPIKey:
		client, err = gitlab.NewClient(auth.Material.Value, options...)
	case coresecret.KindBearerToken, coresecret.KindOAuth2Token:
		client, err = gitlab.NewAuthSourceClient(gitlab.OAuthTokenSource{
			TokenSource: oauth2.StaticTokenSource(&oauth2.Token{AccessToken: auth.Material.Value}),
		}, options...)
	default:
		return nil, fmt.Errorf("gitlabplugin: unsupported auth material kind %q", auth.Material.Kind)
	}
	if err != nil {
		return nil, err
	}
	return officialClient{client: client}, nil
}

func authFromSecrets(ctx context.Context, sys system.System, ref resource.PluginRef, cfg Config) (runtimesecret.Resolution, error) {
	env := sys.Environment()
	if env == nil {
		return runtimesecret.Resolution{}, fmt.Errorf("gitlabplugin: system environment is nil")
	}
	broker := runtimesecret.NewBroker(runtimesecret.EnvResolver{
		Environment: env,
	})
	methods := cfg.authMethods(ref)
	if len(methods) == 0 {
		return runtimesecret.Resolution{}, fmt.Errorf("gitlabplugin: unsupported auth method %q", cfg.Auth.Method)
	}
	request := coresecret.AuthRequest{
		Plugin:   Name,
		Instance: ref.InstanceName(),
		Purpose:  accessTokenPurpose,
		Methods:  methods,
	}
	auth, ok, err := broker.UseAvailable(ctx, request)
	if err != nil {
		return runtimesecret.Resolution{}, fmt.Errorf("gitlabplugin: use auth secret: %w", err)
	}
	if !ok || strings.TrimSpace(auth.Material.Value) == "" {
		if strings.EqualFold(strings.TrimSpace(cfg.Auth.Method), oauth2Method) {
			return runtimesecret.Resolution{}, fmt.Errorf("gitlabplugin: oauth2 auth secret is not configured for instance %s", ref.InstanceName())
		}
		if cfg.Auth.TokenEnv == "" {
			return runtimesecret.Resolution{}, fmt.Errorf("gitlabplugin: auth secret is not configured; set auth.token_env to one of %s", strings.Join(tokenEnvAliases(), ", "))
		}
		return runtimesecret.Resolution{}, fmt.Errorf("gitlabplugin: auth secret is not configured; set %s", cfg.Auth.TokenEnv)
	}
	return auth, nil
}

type officialClient struct {
	client *gitlab.Client
}

func (c officialClient) ListProjects(ctx context.Context, opts *gitlab.ListProjectsOptions) ([]*gitlab.Project, error) {
	projects, _, err := c.client.Projects.ListProjects(opts, gitlab.WithContext(ctx))
	return projects, err
}

func (c officialClient) GetProject(ctx context.Context, id any, opts *gitlab.GetProjectOptions) (*gitlab.Project, error) {
	project, _, err := c.client.Projects.GetProject(id, opts, gitlab.WithContext(ctx))
	return project, err
}

const ProjectEntity coredatasource.EntityType = "gitlab.project"

type Project struct {
	ID                int64  `json:"id" datasource:"id,filterable" jsonschema:"description=GitLab project id."`
	Name              string `json:"name" datasource:"searchable" jsonschema:"description=Project name."`
	PathWithNamespace string `json:"path_with_namespace" datasource:"searchable,filterable" jsonschema:"description=Full project path with namespace."`
	Description       string `json:"description,omitempty" datasource:"searchable" jsonschema:"description=Project description."`
	WebURL            string `json:"web_url,omitempty" datasource:"url" jsonschema:"description=Project web URL."`
	DefaultBranch     string `json:"default_branch,omitempty" datasource:"filterable" jsonschema:"description=Default branch name."`
	Visibility        string `json:"visibility,omitempty" datasource:"filterable" jsonschema:"description=Project visibility."`
}

type projectSearchInput struct {
	Query   string `json:"query" jsonschema:"description=Project search query.,required"`
	PerPage int    `json:"per_page,omitempty" jsonschema:"description=Maximum projects per page. Defaults to 20."`
	Page    int    `json:"page,omitempty" jsonschema:"description=Result page number. Defaults to 1."`
}

type projectSearchOutput struct {
	Projects []Project `json:"projects,omitempty"`
}

type projectGetInput struct {
	ID string `json:"id" jsonschema:"description=Numeric project id or URL-encoded/path-with-namespace project id.,required"`
}

func searchProjects(ctx context.Context, client gitlabClient, query string, perPage, page int) ([]Project, error) {
	if client == nil {
		return nil, fmt.Errorf("gitlabplugin: client is nil")
	}
	if perPage <= 0 {
		perPage = 20
	}
	if page <= 0 {
		page = 1
	}
	search := strings.TrimSpace(query)
	simple := true
	var searchParam *string
	if search != "" {
		searchParam = &search
	}
	projects, err := client.ListProjects(ctx, &gitlab.ListProjectsOptions{
		ListOptions: gitlab.ListOptions{PerPage: int64(perPage), Page: int64(page)},
		Search:      searchParam,
		Simple:      &simple,
	})
	if err != nil {
		return nil, err
	}
	out := make([]Project, 0, len(projects))
	for _, project := range projects {
		out = append(out, projectFromGitLab(project))
	}
	return out, nil
}

func getProject(ctx context.Context, client gitlabClient, id string) (Project, error) {
	if client == nil {
		return Project{}, fmt.Errorf("gitlabplugin: client is nil")
	}
	pid := projectID(id)
	project, err := client.GetProject(ctx, pid, nil)
	if err != nil {
		return Project{}, err
	}
	return projectFromGitLab(project), nil
}

func projectID(id string) any {
	id = strings.TrimSpace(id)
	if n, err := strconv.ParseInt(id, 10, 64); err == nil {
		return n
	}
	return id
}

func projectFromGitLab(project *gitlab.Project) Project {
	if project == nil {
		return Project{}
	}
	return Project{
		ID:                project.ID,
		Name:              project.Name,
		PathWithNamespace: project.PathWithNamespace,
		Description:       project.Description,
		WebURL:            project.WebURL,
		DefaultBranch:     project.DefaultBranch,
		Visibility:        string(project.Visibility),
	}
}

func projectEntitySpec() coredatasource.EntitySpec {
	entity := runtimedatasource.EntityOf[Project](ProjectEntity, "GitLab project.")
	entity.Capabilities = []coredatasource.EntityCapability{
		coredatasource.EntityCapabilitySearch,
		coredatasource.EntityCapabilityGet,
		coredatasource.EntityCapabilitySemanticSearch,
	}
	entity.Detectors = []coredatasource.DetectorSpec{
		{
			Name:          "gitlab_project_url",
			Kind:          coredatasource.DetectorURL,
			Pattern:       `https?://[^\s<>"']+/([^/\s<>"']+/[^/\s<>"'#?]+)(?:[/?#][^\s<>"']*)?`,
			QueryTemplate: "$1",
			URLTemplate:   "$0",
			Confidence:    0.8,
		},
	}
	return entity
}

func normalize(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var b strings.Builder
	underscore := false
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			underscore = false
			continue
		}
		if !underscore && b.Len() > 0 {
			b.WriteByte('_')
			underscore = true
		}
	}
	return strings.Trim(b.String(), "_")
}
