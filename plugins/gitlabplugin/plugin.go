package gitlabplugin

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	coredatasource "github.com/fluxplane/agentruntime/core/datasource"
	"github.com/fluxplane/agentruntime/core/operation"
	"github.com/fluxplane/agentruntime/core/resource"
	coresecret "github.com/fluxplane/agentruntime/core/secret"
	coreuser "github.com/fluxplane/agentruntime/core/user"
	"github.com/fluxplane/agentruntime/orchestration/identity"
	"github.com/fluxplane/agentruntime/orchestration/pluginhost"
	runtimesecret "github.com/fluxplane/agentruntime/runtime/secret"
	"github.com/fluxplane/agentruntime/runtime/system"
	gitlab "gitlab.com/gitlab-org/api/client-go/v2"
	"golang.org/x/oauth2"
)

const (
	Name           = "gitlab"
	defaultBaseURL = "https://gitlab.com"

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
	ListUsers(context.Context, *gitlab.ListUsersOptions) ([]*gitlab.User, error)
	GetUser(context.Context, int64, *gitlab.GetUserOptions) (*gitlab.User, error)
	GetProject(context.Context, any, *gitlab.GetProjectOptions) (*gitlab.Project, error)
	ListMergeRequests(context.Context, *gitlab.ListMergeRequestsOptions) ([]*gitlab.BasicMergeRequest, error)
	ListProjectMergeRequests(context.Context, any, *gitlab.ListProjectMergeRequestsOptions) ([]*gitlab.BasicMergeRequest, error)
	GetMergeRequest(context.Context, any, int64, *gitlab.GetMergeRequestsOptions) (*gitlab.MergeRequest, error)
	ListMergeRequestDiffs(context.Context, any, int64, *gitlab.ListMergeRequestDiffsOptions) ([]*gitlab.MergeRequestDiff, error)
	ListMergeRequestNotes(context.Context, any, int64, *gitlab.ListMergeRequestNotesOptions) ([]*gitlab.Note, error)
	ListMergeRequestPipelines(context.Context, any, int64) ([]*gitlab.PipelineInfo, error)
	GetMergeRequestParticipants(context.Context, any, int64) ([]*gitlab.BasicUser, error)
	GetMergeRequestReviewers(context.Context, any, int64) ([]*gitlab.MergeRequestReviewer, error)
	ListProjectPipelines(context.Context, any, *gitlab.ListProjectPipelinesOptions) ([]*gitlab.PipelineInfo, error)
	GetPipeline(context.Context, any, int64) (*gitlab.Pipeline, error)
	CreateMergeRequest(context.Context, any, *gitlab.CreateMergeRequestOptions) (*gitlab.MergeRequest, error)
	UpdateMergeRequest(context.Context, any, int64, *gitlab.UpdateMergeRequestOptions) (*gitlab.MergeRequest, error)
	CreateMergeRequestNote(context.Context, any, int64, *gitlab.CreateMergeRequestNoteOptions) (*gitlab.Note, error)
	ApproveMergeRequest(context.Context, any, int64, *gitlab.ApproveMergeRequestOptions) (*gitlab.MergeRequestApprovals, error)
	UnapproveMergeRequest(context.Context, any, int64) error
	AcceptMergeRequest(context.Context, any, int64, *gitlab.AcceptMergeRequestOptions) (*gitlab.MergeRequest, error)
	RebaseMergeRequest(context.Context, any, int64, *gitlab.RebaseMergeRequestOptions) error
	RetryPipelineBuild(context.Context, any, int64) (*gitlab.Pipeline, error)
	CancelPipelineBuild(context.Context, any, int64) (*gitlab.Pipeline, error)
}

type gitlabClientFactory func(context.Context, system.System, resource.PluginRef, Config) (gitlabClient, error)

var _ pluginhost.Plugin = Plugin{}
var _ pluginhost.InstanceFactory = Plugin{}
var _ pluginhost.OperationContributor = Plugin{}
var _ pluginhost.DatasourceProviderContributor = Plugin{}
var _ pluginhost.AuthMethodContributor = Plugin{}
var _ pluginhost.ExternalIdentityContributor = Plugin{}

func New(sys system.System) Plugin {
	return Plugin{system: sys, clientFactory: newOfficialClient}
}

func (Plugin) Manifest() pluginhost.Manifest {
	return pluginhost.Manifest{Name: Name, Description: "GitLab datasource and merge request operations."}
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
	return resource.ContributionBundle{Operations: []operation.Spec{p.mrOperationSpec()}}, nil
}

func (p Plugin) Operations(_ context.Context, ctx pluginhost.Context) ([]operation.Operation, error) {
	p = p.withRef(ctx.Ref)
	return []operation.Operation{p.mrOperation()}, nil
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

func (p Plugin) ExternalIdentityResolvers(_ context.Context, ctx pluginhost.Context) ([]identity.ExternalResolver, error) {
	p = p.withRef(ctx.Ref)
	return []identity.ExternalResolver{gitlabExternalIdentityResolver{plugin: p}}, nil
}

func (p Plugin) withRef(ref resource.PluginRef) Plugin {
	if p.ref.Name == "" && ref.Name != "" {
		p.ref = ref
	}
	return p
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

type gitlabExternalIdentityResolver struct {
	plugin Plugin
}

func (r gitlabExternalIdentityResolver) ResolveExternalIdentities(ctx context.Context, req identity.ExternalRequest) (identity.ExternalResult, error) {
	actor := req.Actor
	if coreuser.NormalizeResolution(actor.Resolution) != coreuser.ResolutionResolved || actor.User.ID == "" {
		return identity.ExternalResult{}, nil
	}
	provider := r.plugin.identityProvider()
	if existing := firstProviderIdentity(actor, provider); existing.Provider != "" || existing.ProviderID != "" {
		return identity.ExternalResult{Identities: []coreuser.Identity{existing}}, nil
	}
	email := actorEmail(actor)
	if email == "" {
		return identity.ExternalResult{}, nil
	}
	client, err := r.plugin.client(ctx)
	if err != nil {
		return identity.ExternalResult{}, nil
	}
	users, err := client.ListUsers(ctx, &gitlab.ListUsersOptions{
		ListOptions: gitlab.ListOptions{PerPage: 2},
		PublicEmail: gitlab.Ptr(email),
	})
	if err != nil || len(users) == 0 || users[0] == nil {
		return identity.ExternalResult{}, nil
	}
	gitlabUser := users[0]
	providerID := strings.TrimSpace(gitlabUser.Username)
	if providerID == "" && gitlabUser.ID != 0 {
		providerID = strconv.FormatInt(gitlabUser.ID, 10)
	}
	if providerID == "" {
		return identity.ExternalResult{}, nil
	}
	claims := map[string]string{"instance": r.plugin.ref.InstanceName()}
	if gitlabUser.ID != 0 {
		claims["gitlab_id"] = strconv.FormatInt(gitlabUser.ID, 10)
	}
	return identity.ExternalResult{Identities: []coreuser.Identity{{
		Provider:    provider,
		ProviderID:  providerID,
		Email:       email,
		DisplayName: firstNonEmpty(gitlabUser.Name, gitlabUser.Username),
		Claims:      claims,
	}}}, nil
}

func (p Plugin) identityProvider() string {
	key := p.ref.Key()
	if key == "" {
		return Name
	}
	return key
}

func firstProviderIdentity(actor coreuser.Actor, provider string) coreuser.Identity {
	for _, identity := range actor.Identities {
		if strings.EqualFold(strings.TrimSpace(identity.Provider), provider) {
			return identity
		}
	}
	for _, identity := range actor.User.Identities {
		if strings.EqualFold(strings.TrimSpace(identity.Provider), provider) {
			return identity
		}
	}
	return coreuser.Identity{}
}

func actorEmail(actor coreuser.Actor) string {
	if email := strings.ToLower(strings.TrimSpace(string(actor.User.ID))); strings.Contains(email, "@") {
		return email
	}
	for _, identity := range append(append([]coreuser.Identity(nil), actor.Identities...), actor.User.Identities...) {
		if email := strings.ToLower(strings.TrimSpace(identity.Email)); strings.Contains(email, "@") {
			return email
		}
	}
	return ""
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
			Scopes:       []string{"api"},
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

func (c officialClient) ListUsers(ctx context.Context, opts *gitlab.ListUsersOptions) ([]*gitlab.User, error) {
	users, _, err := c.client.Users.ListUsers(opts, gitlab.WithContext(ctx))
	return users, err
}

func (c officialClient) GetUser(ctx context.Context, id int64, opts *gitlab.GetUserOptions) (*gitlab.User, error) {
	user, _, err := c.client.Users.GetUser(id, opts, gitlab.WithContext(ctx))
	return user, err
}

func (c officialClient) GetProject(ctx context.Context, id any, opts *gitlab.GetProjectOptions) (*gitlab.Project, error) {
	project, _, err := c.client.Projects.GetProject(id, opts, gitlab.WithContext(ctx))
	return project, err
}

func (c officialClient) ListMergeRequests(ctx context.Context, opts *gitlab.ListMergeRequestsOptions) ([]*gitlab.BasicMergeRequest, error) {
	mrs, _, err := c.client.MergeRequests.ListMergeRequests(opts, gitlab.WithContext(ctx))
	return mrs, err
}

func (c officialClient) ListProjectMergeRequests(ctx context.Context, id any, opts *gitlab.ListProjectMergeRequestsOptions) ([]*gitlab.BasicMergeRequest, error) {
	mrs, _, err := c.client.MergeRequests.ListProjectMergeRequests(id, opts, gitlab.WithContext(ctx))
	return mrs, err
}

func (c officialClient) GetMergeRequest(ctx context.Context, id any, mr int64, opts *gitlab.GetMergeRequestsOptions) (*gitlab.MergeRequest, error) {
	out, _, err := c.client.MergeRequests.GetMergeRequest(id, mr, opts, gitlab.WithContext(ctx))
	return out, err
}

func (c officialClient) ListMergeRequestDiffs(ctx context.Context, id any, mr int64, opts *gitlab.ListMergeRequestDiffsOptions) ([]*gitlab.MergeRequestDiff, error) {
	diffs, _, err := c.client.MergeRequests.ListMergeRequestDiffs(id, mr, opts, gitlab.WithContext(ctx))
	return diffs, err
}

func (c officialClient) ListMergeRequestNotes(ctx context.Context, id any, mr int64, opts *gitlab.ListMergeRequestNotesOptions) ([]*gitlab.Note, error) {
	notes, _, err := c.client.Notes.ListMergeRequestNotes(id, mr, opts, gitlab.WithContext(ctx))
	return notes, err
}

func (c officialClient) ListMergeRequestPipelines(ctx context.Context, id any, mr int64) ([]*gitlab.PipelineInfo, error) {
	pipelines, _, err := c.client.MergeRequests.ListMergeRequestPipelines(id, mr, gitlab.WithContext(ctx))
	return pipelines, err
}

func (c officialClient) GetMergeRequestParticipants(ctx context.Context, id any, mr int64) ([]*gitlab.BasicUser, error) {
	users, _, err := c.client.MergeRequests.GetMergeRequestParticipants(id, mr, gitlab.WithContext(ctx))
	return users, err
}

func (c officialClient) GetMergeRequestReviewers(ctx context.Context, id any, mr int64) ([]*gitlab.MergeRequestReviewer, error) {
	users, _, err := c.client.MergeRequests.GetMergeRequestReviewers(id, mr, gitlab.WithContext(ctx))
	return users, err
}

func (c officialClient) ListProjectPipelines(ctx context.Context, id any, opts *gitlab.ListProjectPipelinesOptions) ([]*gitlab.PipelineInfo, error) {
	pipelines, _, err := c.client.Pipelines.ListProjectPipelines(id, opts, gitlab.WithContext(ctx))
	return pipelines, err
}

func (c officialClient) GetPipeline(ctx context.Context, id any, pipeline int64) (*gitlab.Pipeline, error) {
	out, _, err := c.client.Pipelines.GetPipeline(id, pipeline, gitlab.WithContext(ctx))
	return out, err
}

func (c officialClient) CreateMergeRequest(ctx context.Context, id any, opts *gitlab.CreateMergeRequestOptions) (*gitlab.MergeRequest, error) {
	out, _, err := c.client.MergeRequests.CreateMergeRequest(id, opts, gitlab.WithContext(ctx))
	return out, err
}

func (c officialClient) UpdateMergeRequest(ctx context.Context, id any, mr int64, opts *gitlab.UpdateMergeRequestOptions) (*gitlab.MergeRequest, error) {
	out, _, err := c.client.MergeRequests.UpdateMergeRequest(id, mr, opts, gitlab.WithContext(ctx))
	return out, err
}

func (c officialClient) CreateMergeRequestNote(ctx context.Context, id any, mr int64, opts *gitlab.CreateMergeRequestNoteOptions) (*gitlab.Note, error) {
	out, _, err := c.client.Notes.CreateMergeRequestNote(id, mr, opts, gitlab.WithContext(ctx))
	return out, err
}

func (c officialClient) ApproveMergeRequest(ctx context.Context, id any, mr int64, opts *gitlab.ApproveMergeRequestOptions) (*gitlab.MergeRequestApprovals, error) {
	out, _, err := c.client.MergeRequestApprovals.ApproveMergeRequest(id, mr, opts, gitlab.WithContext(ctx))
	return out, err
}

func (c officialClient) UnapproveMergeRequest(ctx context.Context, id any, mr int64) error {
	_, err := c.client.MergeRequestApprovals.UnapproveMergeRequest(id, mr, gitlab.WithContext(ctx))
	return err
}

func (c officialClient) AcceptMergeRequest(ctx context.Context, id any, mr int64, opts *gitlab.AcceptMergeRequestOptions) (*gitlab.MergeRequest, error) {
	out, _, err := c.client.MergeRequests.AcceptMergeRequest(id, mr, opts, gitlab.WithContext(ctx))
	return out, err
}

func (c officialClient) RebaseMergeRequest(ctx context.Context, id any, mr int64, opts *gitlab.RebaseMergeRequestOptions) error {
	_, err := c.client.MergeRequests.RebaseMergeRequest(id, mr, opts, gitlab.WithContext(ctx))
	return err
}

func (c officialClient) RetryPipelineBuild(ctx context.Context, id any, pipeline int64) (*gitlab.Pipeline, error) {
	out, _, err := c.client.Pipelines.RetryPipelineBuild(id, pipeline, gitlab.WithContext(ctx))
	return out, err
}

func (c officialClient) CancelPipelineBuild(ctx context.Context, id any, pipeline int64) (*gitlab.Pipeline, error) {
	out, _, err := c.client.Pipelines.CancelPipelineBuild(id, pipeline, gitlab.WithContext(ctx))
	return out, err
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
