package gitlab

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	coredata "github.com/fluxplane/fluxplane-core/core/data"
	coredatasource "github.com/fluxplane/fluxplane-core/core/datasource"
	"github.com/fluxplane/fluxplane-core/core/operation"
	"github.com/fluxplane/fluxplane-core/core/resource"
	coresecret "github.com/fluxplane/fluxplane-core/core/secret"
	coreuser "github.com/fluxplane/fluxplane-core/core/user"
	"github.com/fluxplane/fluxplane-core/orchestration/identity"
	"github.com/fluxplane/fluxplane-core/orchestration/pluginhost"
	runtimesecret "github.com/fluxplane/fluxplane-core/runtime/secret"
	"github.com/fluxplane/fluxplane-core/runtime/system"
	gitlab "gitlab.com/gitlab-org/api/client-go/v2"
	"golang.org/x/oauth2"
)

const (
	Name           = "gitlab"
	OperationSet   = Name
	defaultBaseURL = "https://gitlab.com"

	PersonalAccessTokenMethod = "personal_access_token"
	OAuth2Method              = "oauth2"

	accessTokenPurpose           = "access_token"
	personalAccessTokenMethod    = PersonalAccessTokenMethod
	oauth2Method                 = OAuth2Method
	gitlabTokenField             = "token"
	gitlabURLField               = "url"
	gitlabURLEnv                 = "GITLAB_URL"
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
	secrets       runtimesecret.Resolver
	clientFactory gitlabClientFactory
}

// Config is the per-instance GitLab plugin configuration accepted from an app
// manifest.
type Config struct {
	BaseURL string     `json:"base_url,omitempty" jsonschema:"description=GitLab base URL. Defaults to https://gitlab.com."`
	Auth    AuthConfig `json:"auth,omitempty" jsonschema:"description=GitLab authentication source."`
}

type AuthConfig struct {
	Method   string `json:"method,omitempty" jsonschema:"description=GitLab auth method. personal_access_token uses stored or environment token material; oauth2 uses stored OAuth2 token material.,enum=personal_access_token,enum=oauth2"`
	TokenEnv string `json:"token_env,omitempty" jsonschema:"description=Environment variable containing a GitLab personal access token. Defaults to GITLAB_ACCESS_TOKEN or compatible GitLab token env vars."`
}

type gitlabClient interface {
	ListProjects(context.Context, *gitlab.ListProjectsOptions) ([]*gitlab.Project, error)
	ListGroups(context.Context, *gitlab.ListGroupsOptions) ([]*gitlab.Group, error)
	GetGroup(context.Context, any, *gitlab.GetGroupOptions) (*gitlab.Group, error)
	ListSubGroups(context.Context, any, *gitlab.ListSubGroupsOptions) ([]*gitlab.Group, error)
	ListDescendantGroups(context.Context, any, *gitlab.ListDescendantGroupsOptions) ([]*gitlab.Group, error)
	ListGroupProjects(context.Context, any, *gitlab.ListGroupProjectsOptions) ([]*gitlab.Project, error)
	ListGroupMembers(context.Context, any, *gitlab.ListGroupMembersOptions) ([]*gitlab.GroupMember, error)
	ListUsers(context.Context, *gitlab.ListUsersOptions) ([]*gitlab.User, error)
	GetUser(context.Context, int64, *gitlab.GetUserOptions) (*gitlab.User, error)
	CurrentUser(context.Context) (*gitlab.User, error)
	GetProject(context.Context, any, *gitlab.GetProjectOptions) (*gitlab.Project, error)
	GetProjectLanguages(context.Context, any) (*gitlab.ProjectLanguages, error)
	ListProjectUsers(context.Context, any, *gitlab.ListProjectUserOptions) ([]*gitlab.ProjectUser, error)
	ListProjectGroups(context.Context, any, *gitlab.ListProjectGroupOptions) ([]*gitlab.ProjectGroup, error)
	ListProjectMembers(context.Context, any, *gitlab.ListProjectMembersOptions) ([]*gitlab.ProjectMember, error)
	ListMergeRequests(context.Context, *gitlab.ListMergeRequestsOptions) ([]*gitlab.BasicMergeRequest, error)
	ListProjectMergeRequests(context.Context, any, *gitlab.ListProjectMergeRequestsOptions) ([]*gitlab.BasicMergeRequest, error)
	GetMergeRequest(context.Context, any, int64, *gitlab.GetMergeRequestsOptions) (*gitlab.MergeRequest, error)
	ListMergeRequestDiffs(context.Context, any, int64, *gitlab.ListMergeRequestDiffsOptions) ([]*gitlab.MergeRequestDiff, error)
	ListMergeRequestNotes(context.Context, any, int64, *gitlab.ListMergeRequestNotesOptions) ([]*gitlab.Note, error)
	GetMergeRequestApprovals(context.Context, any, int64) (*gitlab.MergeRequestApprovals, error)
	GetMergeRequestCommits(context.Context, any, int64, *gitlab.GetMergeRequestCommitsOptions) ([]*gitlab.Commit, error)
	GetMergeRequestChanges(context.Context, any, int64, *gitlab.GetMergeRequestChangesOptions) (*gitlab.MergeRequest, error)
	GetMergeRequestDiffVersions(context.Context, any, int64, *gitlab.GetMergeRequestDiffVersionsOptions) ([]*gitlab.MergeRequestDiffVersion, error)
	ListMergeRequestDiscussions(context.Context, any, int64, *gitlab.ListMergeRequestDiscussionsOptions) ([]*gitlab.Discussion, error)
	CreateMergeRequestDiscussion(context.Context, any, int64, *gitlab.CreateMergeRequestDiscussionOptions) (*gitlab.Discussion, error)
	AddMergeRequestDiscussionNote(context.Context, any, int64, string, *gitlab.AddMergeRequestDiscussionNoteOptions) (*gitlab.Note, error)
	ResolveMergeRequestDiscussion(context.Context, any, int64, string, *gitlab.ResolveMergeRequestDiscussionOptions) (*gitlab.Discussion, error)
	ListMergeRequestAwardEmoji(context.Context, any, int64, *gitlab.ListAwardEmojiOptions) ([]*gitlab.AwardEmoji, error)
	ListMergeRequestAwardEmojiOnNote(context.Context, any, int64, int64, *gitlab.ListAwardEmojiOptions) ([]*gitlab.AwardEmoji, error)
	CreateMergeRequestAwardEmoji(context.Context, any, int64, *gitlab.CreateAwardEmojiOptions) (*gitlab.AwardEmoji, error)
	CreateMergeRequestAwardEmojiOnNote(context.Context, any, int64, int64, *gitlab.CreateAwardEmojiOptions) (*gitlab.AwardEmoji, error)
	ListMergeRequestPipelines(context.Context, any, int64) ([]*gitlab.PipelineInfo, error)
	GetMergeRequestParticipants(context.Context, any, int64) ([]*gitlab.BasicUser, error)
	GetMergeRequestReviewers(context.Context, any, int64) ([]*gitlab.MergeRequestReviewer, error)
	ListProjectPipelines(context.Context, any, *gitlab.ListProjectPipelinesOptions) ([]*gitlab.PipelineInfo, error)
	GetPipeline(context.Context, any, int64) (*gitlab.Pipeline, error)
	ListBranches(context.Context, any, *gitlab.ListBranchesOptions) ([]*gitlab.Branch, error)
	GetBranch(context.Context, any, string) (*gitlab.Branch, error)
	ListTags(context.Context, any, *gitlab.ListTagsOptions) ([]*gitlab.Tag, error)
	GetTag(context.Context, any, string) (*gitlab.Tag, error)
	ListCommits(context.Context, any, *gitlab.ListCommitsOptions) ([]*gitlab.Commit, error)
	GetCommit(context.Context, any, string, *gitlab.GetCommitOptions) (*gitlab.Commit, error)
	ListMergeRequestsByCommit(context.Context, any, string) ([]*gitlab.BasicMergeRequest, error)
	ListTree(context.Context, any, *gitlab.ListTreeOptions) ([]*gitlab.TreeNode, error)
	ListProjectContributors(context.Context, any, *gitlab.ListContributorsOptions) ([]*gitlab.Contributor, error)
	CompareRefs(context.Context, any, *gitlab.CompareOptions) (*gitlab.Compare, error)
	GetFile(context.Context, any, string, *gitlab.GetFileOptions) (*gitlab.File, error)
	GetFileBlame(context.Context, any, string, *gitlab.GetFileBlameOptions) ([]*gitlab.FileBlameRange, error)
	SearchBlobsByProject(context.Context, any, string, *gitlab.SearchOptions) ([]*gitlab.Blob, error)
	ListProjectJobs(context.Context, any, *gitlab.ListJobsOptions) ([]*gitlab.Job, error)
	ListPipelineJobs(context.Context, any, int64, *gitlab.ListJobsOptions) ([]*gitlab.Job, error)
	GetJob(context.Context, any, int64) (*gitlab.Job, error)
	GetTraceFile(context.Context, any, int64) ([]byte, error)
	CreateMergeRequest(context.Context, any, *gitlab.CreateMergeRequestOptions) (*gitlab.MergeRequest, error)
	UpdateMergeRequest(context.Context, any, int64, *gitlab.UpdateMergeRequestOptions) (*gitlab.MergeRequest, error)
	CreateMergeRequestNote(context.Context, any, int64, *gitlab.CreateMergeRequestNoteOptions) (*gitlab.Note, error)
	ApproveMergeRequest(context.Context, any, int64, *gitlab.ApproveMergeRequestOptions) (*gitlab.MergeRequestApprovals, error)
	UnapproveMergeRequest(context.Context, any, int64) error
	AcceptMergeRequest(context.Context, any, int64, *gitlab.AcceptMergeRequestOptions) (*gitlab.MergeRequest, error)
	RebaseMergeRequest(context.Context, any, int64, *gitlab.RebaseMergeRequestOptions) error
	CreatePipeline(context.Context, any, *gitlab.CreatePipelineOptions) (*gitlab.Pipeline, error)
	RetryPipelineBuild(context.Context, any, int64) (*gitlab.Pipeline, error)
	CancelPipelineBuild(context.Context, any, int64) (*gitlab.Pipeline, error)
	CreateFile(context.Context, any, string, *gitlab.CreateFileOptions) (*gitlab.FileInfo, error)
	UpdateFile(context.Context, any, string, *gitlab.UpdateFileOptions) (*gitlab.FileInfo, error)
	DeleteFile(context.Context, any, string, *gitlab.DeleteFileOptions) error
	CreateBranch(context.Context, any, *gitlab.CreateBranchOptions) (*gitlab.Branch, error)
	DeleteBranch(context.Context, any, string) error
	DeleteMergedBranches(context.Context, any) error
	CreateTag(context.Context, any, *gitlab.CreateTagOptions) (*gitlab.Tag, error)
	DeleteTag(context.Context, any, string) error
	CreateCommit(context.Context, any, *gitlab.CreateCommitOptions) (*gitlab.Commit, error)
	CreateVariable(context.Context, any, *gitlab.CreateProjectVariableOptions) (*gitlab.ProjectVariable, error)
	UpdateVariable(context.Context, any, string, *gitlab.UpdateProjectVariableOptions) (*gitlab.ProjectVariable, error)
	RemoveVariable(context.Context, any, string, *gitlab.RemoveProjectVariableOptions) error
	ListSnippets(context.Context, *gitlab.ListSnippetsOptions) ([]*gitlab.Snippet, error)
	GetSnippet(context.Context, int64) (*gitlab.Snippet, error)
	GetSnippetContent(context.Context, int64) ([]byte, error)
	CreateSnippet(context.Context, *gitlab.CreateSnippetOptions) (*gitlab.Snippet, error)
	DeleteSnippet(context.Context, int64) error
}

type gitlabClientFactory func(context.Context, system.System, resource.PluginRef, Config) (gitlabClient, error)

type resolvedGitLabAuth struct {
	runtimesecret.Resolution
	BaseURL string
}

var _ pluginhost.Plugin = Plugin{}
var _ pluginhost.InstanceFactory = Plugin{}
var _ pluginhost.OperationContributor = Plugin{}
var _ pluginhost.DatasourceProviderContributor = Plugin{}
var _ pluginhost.AuthMethodContributor = Plugin{}
var _ pluginhost.AuthTestContributor = Plugin{}
var _ pluginhost.ExternalIdentityContributor = Plugin{}

func New(sys system.System) Plugin {
	return Plugin{system: sys}
}

func NewWithResolver(sys system.System, resolver runtimesecret.Resolver) Plugin {
	return Plugin{system: sys, secrets: resolver}
}

func (Plugin) Manifest() pluginhost.Manifest {
	return pluginhost.Manifest{Name: Name, Description: "GitLab datasource and merge request operations."}
}

func (p Plugin) Instantiate(_ context.Context, ctx pluginhost.Context) (pluginhost.Plugin, error) {
	cfg, err := pluginhost.ConfigAs[Config](ctx)
	if err != nil {
		return nil, err
	}
	return Plugin{system: p.system, ref: ctx.Ref, cfg: normalizeConfig(cfg), secrets: p.secrets, clientFactory: p.clientFactory}, nil
}

func (p Plugin) Contributions(_ context.Context, ctx pluginhost.Context) (resource.ContributionBundle, error) {
	p = p.withRef(ctx.Ref)
	return resource.ContributionBundle{
		Operations: p.operationSpecs(),
		OperationSets: []operation.Set{{
			Name:        OperationSet,
			Description: "GitLab repository, merge request, CI, branch, tag, and commit operations.",
			Operations:  []operation.Ref{{Name: operation.Name(Name + "_*")}},
		}},
		DataSources: []coredata.SourceSpec{DataSourceSpec()},
	}, nil
}

func (p Plugin) Operations(_ context.Context, ctx pluginhost.Context) ([]operation.Operation, error) {
	p = p.withRef(ctx.Ref)
	return p.operations(), nil
}

func (p Plugin) DatasourceProviders(_ context.Context, ctx pluginhost.Context) ([]coredatasource.Provider, error) {
	p = p.withRef(ctx.Ref)
	return []coredatasource.Provider{gitlabDatasourceProvider{
		system:        p.system,
		ref:           p.ref,
		config:        p.config(),
		secrets:       p.secrets,
		clientFactory: p.clientFactory,
	}}, nil
}

func (p Plugin) AuthMethods(_ context.Context, ctx pluginhost.Context) ([]coresecret.AuthMethodSpec, error) {
	p = p.withRef(ctx.Ref)
	return p.authMethods(), nil
}

func (p Plugin) TestConnection(ctx context.Context, pluginCtx pluginhost.Context, req pluginhost.AuthTestRequest, reports chan<- pluginhost.AuthTestReport) error {
	ref := req.Ref
	if ref.Name == "" {
		ref = pluginCtx.Ref
	}
	p = p.withRef(ref)
	cfg := p.config()
	if method := strings.TrimSpace(req.Method); method != "" {
		cfg.Auth.Method = method
	}
	p.cfg = normalizeConfig(cfg)
	resolver := req.Secrets
	if resolver == nil {
		if p.system == nil {
			reports <- p.authTestReport(p.cfg.Auth.Method, "current_user", "failed", "gitlabplugin: system is nil", nil)
			return nil
		}
		resolver = runtimesecret.EnvResolver{Environment: p.system.Environment()}
	}
	auth, err := authFromResolver(ctx, resolver, p.ref, p.cfg)
	method := firstNonEmpty(auth.Method.Name, p.cfg.Auth.Method)
	if err != nil {
		reports <- p.authTestReport(method, "current_user", "failed", err.Error(), nil)
		return nil
	}
	var client gitlabClient
	if p.clientFactory != nil {
		client, err = p.client(ctx)
	} else {
		client, err = newOfficialClientFromAuth(p.system, p.cfg, auth)
	}
	if err != nil {
		reports <- p.authTestReport(method, "current_user", "failed", err.Error(), nil)
		return nil
	}
	user, err := client.CurrentUser(ctx)
	if err != nil {
		reports <- p.authTestReport(method, "current_user", "failed", err.Error(), nil)
		return nil
	}
	if user == nil {
		reports <- p.authTestReport(method, "current_user", "failed", "current user response is empty", nil)
		return nil
	}
	details := map[string]string{}
	if user.ID != 0 {
		details["id"] = strconv.FormatInt(user.ID, 10)
	}
	if username := strings.TrimSpace(user.Username); username != "" {
		details["username"] = username
	}
	if name := strings.TrimSpace(user.Name); name != "" {
		details["name"] = name
	}
	if state := strings.TrimSpace(user.State); state != "" {
		details["state"] = state
	}
	if webURL := strings.TrimSpace(user.WebURL); webURL != "" {
		details["web_url"] = webURL
	}
	message := firstNonEmpty(user.Username, user.Name)
	reports <- p.authTestReport(method, "current_user", "ok", message, details)
	return nil
}

func (p Plugin) authTestReport(method, check, status, message string, details map[string]string) pluginhost.AuthTestReport {
	return pluginhost.AuthTestReport{
		Plugin:   Name,
		Instance: p.ref.InstanceName(),
		Method:   strings.TrimSpace(method),
		Check:    strings.TrimSpace(check),
		Status:   strings.TrimSpace(status),
		Message:  strings.TrimSpace(message),
		Details:  details,
	}
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

func (p Plugin) config() Config {
	return p.cfg
}

func (p Plugin) client(ctx context.Context) (gitlabClient, error) {
	if p.system == nil {
		return nil, fmt.Errorf("gitlabplugin: system is nil")
	}
	factory := p.clientFactory
	if factory == nil {
		if p.secrets != nil {
			return newOfficialClientWithResolver(ctx, p.system, p.secrets, p.ref, p.config())
		}
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
	emails := actorEmails(actor)
	if len(emails) == 0 {
		return identity.ExternalResult{}, nil
	}
	client, err := r.plugin.client(ctx)
	if err != nil {
		return identity.ExternalResult{}, nil
	}
	var gitlabUser *gitlab.User
	email := ""
	for _, candidate := range emails {
		users, err := client.ListUsers(ctx, &gitlab.ListUsersOptions{
			ListOptions: gitlab.ListOptions{PerPage: 2},
			PublicEmail: gitlab.Ptr(candidate),
		})
		if err != nil || len(users) == 0 || users[0] == nil {
			continue
		}
		gitlabUser = users[0]
		email = candidate
		break
	}
	if gitlabUser == nil {
		return identity.ExternalResult{}, nil
	}
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

func actorEmails(actor coreuser.Actor) []string {
	var out []string
	add := func(email string) {
		email = strings.ToLower(strings.TrimSpace(email))
		if email == "" || !strings.Contains(email, "@") {
			return
		}
		for _, existing := range out {
			if existing == email {
				return
			}
		}
		out = append(out, email)
	}
	for _, email := range actor.User.Emails {
		if email.Verified && email.Primary {
			add(email.Address)
		}
	}
	for _, email := range actor.User.Emails {
		if email.Verified && !email.Primary {
			add(email.Address)
		}
	}
	add(string(actor.User.ID))
	for _, identity := range actor.Identities {
		add(identity.Email)
	}
	for _, identity := range actor.User.Identities {
		add(identity.Email)
	}
	return out
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
	if authMethod == "" || authMethod == "env" || authMethod == "token" || authMethod == "personal_access_token" || authMethod == "personal-access-token" {
		methods = append(methods, personalAccessTokenAuthMethod(ref, c.Auth.TokenEnv, c.BaseURL))
	}
	if authMethod == "" || authMethod == "oauth2" {
		methods = append(methods, oauth2AuthMethod(ref, c.baseURL()))
	}
	return methods
}

func (p Plugin) authMethods() []coresecret.AuthMethodSpec {
	return p.config().authMethods(p.ref)
}

func personalAccessTokenAuthMethod(ref resource.PluginRef, tokenEnv, baseURL string) coresecret.AuthMethodSpec {
	urlRequired := strings.TrimSpace(baseURL) == ""
	env := tokenEnvSpec(tokenEnv)
	return coresecret.AuthMethodSpec{
		Name:        personalAccessTokenMethod,
		Method:      coresecret.AuthMethodStored,
		Kind:        coresecret.KindAPIKey,
		DisplayName: "GitLab personal access token",
		Description: "GitLab personal access token and GitLab URL resolved from stored fields or environment variables.",
		Secret:      coresecret.Plugin(Name, ref.InstanceName(), gitlabTokenField),
		Env:         env,
		Header:      coresecret.HeaderSpec{Name: "Private-Token"},
		SetupFields: []coresecret.SetupFieldSpec{
			{
				Name:        gitlabTokenField,
				DisplayName: "GitLab token",
				Required:    true,
				Sensitive:   true,
				Env:         env,
			},
			{
				Name:        gitlabURLField,
				DisplayName: "GitLab URL",
				Required:    urlRequired,
				Env:         coresecret.EnvSpec{Name: gitlabURLEnv},
			},
		},
	}
}

func tokenEnvSpec(tokenEnv string) coresecret.EnvSpec {
	if tokenEnv = strings.TrimSpace(tokenEnv); tokenEnv != "" {
		return coresecret.EnvSpec{Name: tokenEnv}
	}
	return coresecret.EnvSpec{Aliases: tokenEnvAliases()}
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
	auth, err := authFromSecrets(ctx, sys, ref, cfg)
	if err != nil {
		return nil, err
	}
	return newOfficialClientFromAuth(sys, cfg, auth)
}

func newOfficialClientWithResolver(ctx context.Context, sys system.System, resolver runtimesecret.Resolver, ref resource.PluginRef, cfg Config) (gitlabClient, error) {
	auth, err := authFromResolver(ctx, resolver, ref, cfg)
	if err != nil {
		return nil, err
	}
	return newOfficialClientFromAuth(sys, cfg, auth)
}

func newOfficialClientFromAuth(sys system.System, cfg Config, auth resolvedGitLabAuth) (gitlabClient, error) {
	if sys == nil {
		return nil, fmt.Errorf("gitlabplugin: system is nil")
	}
	if sys.Network() == nil {
		return nil, fmt.Errorf("gitlabplugin: system network is nil")
	}
	options := []gitlab.ClientOptionFunc{
		gitlab.WithBaseURL(firstNonEmpty(auth.BaseURL, cfg.baseURL())),
		gitlab.WithHTTPClient(system.NewHTTPClient(sys.Network())),
		gitlab.WithoutRetries(),
	}
	var client *gitlab.Client
	var err error
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

func authFromSecrets(ctx context.Context, sys system.System, ref resource.PluginRef, cfg Config) (resolvedGitLabAuth, error) {
	if sys == nil {
		return resolvedGitLabAuth{}, fmt.Errorf("gitlabplugin: system is nil")
	}
	env := sys.Environment()
	if env == nil {
		return resolvedGitLabAuth{}, fmt.Errorf("gitlabplugin: system environment is nil")
	}
	return authFromResolver(ctx, runtimesecret.EnvResolver{Environment: env}, ref, cfg)
}

func authFromResolver(ctx context.Context, resolver runtimesecret.Resolver, ref resource.PluginRef, cfg Config) (resolvedGitLabAuth, error) {
	if resolver == nil {
		return resolvedGitLabAuth{}, fmt.Errorf("gitlabplugin: secret resolver is nil")
	}
	broker := runtimesecret.NewBroker(resolver)
	methods := cfg.authMethods(ref)
	if len(methods) == 0 {
		return resolvedGitLabAuth{}, fmt.Errorf("gitlabplugin: unsupported auth method %q", cfg.Auth.Method)
	}
	for _, method := range methods {
		if strings.EqualFold(strings.TrimSpace(method.Name), personalAccessTokenMethod) {
			auth, ok, err := tokenAuthFromSetupFields(ctx, broker, resolver, ref, cfg, method)
			if err != nil || ok {
				return auth, err
			}
			continue
		}
		resolution, ok, err := broker.UseAvailable(ctx, coresecret.AuthRequest{
			Plugin:   Name,
			Instance: ref.InstanceName(),
			Purpose:  accessTokenPurpose,
			Methods:  []coresecret.AuthMethodSpec{method},
		})
		if err != nil {
			return resolvedGitLabAuth{}, fmt.Errorf("gitlabplugin: use auth secret: %w", err)
		}
		if ok && strings.TrimSpace(resolution.Material.Value) != "" {
			return resolvedGitLabAuth{Resolution: resolution, BaseURL: cfg.baseURL()}, nil
		}
	}
	if strings.EqualFold(strings.TrimSpace(cfg.Auth.Method), oauth2Method) {
		return resolvedGitLabAuth{}, fmt.Errorf("gitlabplugin: oauth2 auth secret is not configured for instance %s", ref.InstanceName())
	}
	if cfg.Auth.TokenEnv == "" {
		return resolvedGitLabAuth{}, fmt.Errorf("gitlabplugin: auth secret is not configured; set auth.token_env to one of %s", strings.Join(tokenEnvAliases(), ", "))
	}
	return resolvedGitLabAuth{}, fmt.Errorf("gitlabplugin: auth secret is not configured; set %s", cfg.Auth.TokenEnv)
}

func tokenAuthFromSetupFields(ctx context.Context, broker *runtimesecret.Broker, resolver runtimesecret.Resolver, ref resource.PluginRef, cfg Config, method coresecret.AuthMethodSpec) (resolvedGitLabAuth, bool, error) {
	request := coresecret.AuthRequest{
		Plugin:   Name,
		Instance: ref.InstanceName(),
		Purpose:  accessTokenPurpose,
		Methods:  []coresecret.AuthMethodSpec{method},
	}
	if _, _, err := broker.Use(ctx, request.SecretRef()); err != nil {
		return resolvedGitLabAuth{}, false, fmt.Errorf("gitlabplugin: use auth secret: %w", err)
	}
	token, tokenRef, ok, err := resolveSetupField(ctx, resolver, ref, method, gitlabTokenField)
	if err != nil {
		return resolvedGitLabAuth{}, false, err
	}
	if !ok || strings.TrimSpace(token.Value) == "" {
		if cfg.Auth.TokenEnv == "" {
			return resolvedGitLabAuth{}, false, nil
		}
		return resolvedGitLabAuth{}, false, nil
	}
	if method.Kind != "" {
		token.Kind = method.Kind
	}
	baseURL := normalizeBaseURL(cfg.BaseURL)
	if baseURL == "" {
		urlMaterial, _, urlOK, err := resolveSetupField(ctx, resolver, ref, method, gitlabURLField)
		if err != nil {
			return resolvedGitLabAuth{}, false, err
		}
		baseURL = normalizeBaseURL(urlMaterial.Value)
		if !urlOK || baseURL == "" {
			return resolvedGitLabAuth{}, true, fmt.Errorf("gitlabplugin: gitlab url is not configured; set %s or provide field %q", gitlabURLEnv, gitlabURLField)
		}
	}
	return resolvedGitLabAuth{
		Resolution: runtimesecret.Resolution{
			Ref:      tokenRef,
			Method:   method,
			Material: token,
		},
		BaseURL: baseURL,
	}, true, nil
}

func resolveSetupField(ctx context.Context, resolver runtimesecret.Resolver, ref resource.PluginRef, method coresecret.AuthMethodSpec, name string) (coresecret.Material, coresecret.Ref, bool, error) {
	field, ok := setupField(method.SetupFields, name)
	if !ok {
		return coresecret.Material{}, coresecret.Ref{}, false, nil
	}
	refs := []coresecret.Ref{coresecret.Plugin(ref.Name, ref.InstanceName(), name)}
	refs = append(refs, envRefs(field.Env)...)
	for _, candidate := range refs {
		material, found, err := resolver.ResolveSecret(ctx, candidate)
		if err != nil || found {
			return material, candidate, found, err
		}
	}
	return coresecret.Material{}, coresecret.Ref{}, false, nil
}

func setupField(fields []coresecret.SetupFieldSpec, name string) (coresecret.SetupFieldSpec, bool) {
	for _, field := range fields {
		if strings.EqualFold(strings.TrimSpace(field.Name), strings.TrimSpace(name)) {
			return field, true
		}
	}
	return coresecret.SetupFieldSpec{}, false
}

func envRefs(spec coresecret.EnvSpec) []coresecret.Ref {
	names := append([]string{spec.Name}, spec.Aliases...)
	refs := make([]coresecret.Ref, 0, len(names))
	seen := map[string]bool{}
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		refs = append(refs, coresecret.Env(name))
	}
	return refs
}

func normalizeBaseURL(value string) string {
	value = strings.TrimRight(strings.TrimSpace(value), "/")
	if value == "" {
		return ""
	}
	if !strings.Contains(value, "://") {
		return "https://" + value
	}
	return value
}

type officialClient struct {
	client *gitlab.Client
}

func (c officialClient) ListProjects(ctx context.Context, opts *gitlab.ListProjectsOptions) ([]*gitlab.Project, error) {
	projects, _, err := c.client.Projects.ListProjects(opts, gitlab.WithContext(ctx))
	return projects, err
}

func (c officialClient) ListGroups(ctx context.Context, opts *gitlab.ListGroupsOptions) ([]*gitlab.Group, error) {
	groups, _, err := c.client.Groups.ListGroups(opts, gitlab.WithContext(ctx))
	return groups, err
}

func (c officialClient) GetGroup(ctx context.Context, id any, opts *gitlab.GetGroupOptions) (*gitlab.Group, error) {
	group, _, err := c.client.Groups.GetGroup(id, opts, gitlab.WithContext(ctx))
	return group, err
}

func (c officialClient) ListSubGroups(ctx context.Context, id any, opts *gitlab.ListSubGroupsOptions) ([]*gitlab.Group, error) {
	groups, _, err := c.client.Groups.ListSubGroups(id, opts, gitlab.WithContext(ctx))
	return groups, err
}

func (c officialClient) ListDescendantGroups(ctx context.Context, id any, opts *gitlab.ListDescendantGroupsOptions) ([]*gitlab.Group, error) {
	groups, _, err := c.client.Groups.ListDescendantGroups(id, opts, gitlab.WithContext(ctx))
	return groups, err
}

func (c officialClient) ListGroupProjects(ctx context.Context, id any, opts *gitlab.ListGroupProjectsOptions) ([]*gitlab.Project, error) {
	projects, _, err := c.client.Groups.ListGroupProjects(id, opts, gitlab.WithContext(ctx))
	return projects, err
}

func (c officialClient) ListGroupMembers(ctx context.Context, id any, opts *gitlab.ListGroupMembersOptions) ([]*gitlab.GroupMember, error) {
	members, _, err := c.client.Groups.ListGroupMembers(id, opts, gitlab.WithContext(ctx))
	return members, err
}

func (c officialClient) ListUsers(ctx context.Context, opts *gitlab.ListUsersOptions) ([]*gitlab.User, error) {
	users, _, err := c.client.Users.ListUsers(opts, gitlab.WithContext(ctx))
	return users, err
}

func (c officialClient) GetUser(ctx context.Context, id int64, opts *gitlab.GetUserOptions) (*gitlab.User, error) {
	user, _, err := c.client.Users.GetUser(id, opts, gitlab.WithContext(ctx))
	return user, err
}

func (c officialClient) CurrentUser(ctx context.Context) (*gitlab.User, error) {
	user, _, err := c.client.Users.CurrentUser(gitlab.WithContext(ctx))
	return user, err
}

func (c officialClient) GetProject(ctx context.Context, id any, opts *gitlab.GetProjectOptions) (*gitlab.Project, error) {
	project, _, err := c.client.Projects.GetProject(id, opts, gitlab.WithContext(ctx))
	return project, err
}

func (c officialClient) GetProjectLanguages(ctx context.Context, id any) (*gitlab.ProjectLanguages, error) {
	languages, _, err := c.client.Projects.GetProjectLanguages(id, gitlab.WithContext(ctx))
	return languages, err
}

func (c officialClient) ListProjectUsers(ctx context.Context, id any, opts *gitlab.ListProjectUserOptions) ([]*gitlab.ProjectUser, error) {
	users, _, err := c.client.Projects.ListProjectsUsers(id, opts, gitlab.WithContext(ctx))
	return users, err
}

func (c officialClient) ListProjectGroups(ctx context.Context, id any, opts *gitlab.ListProjectGroupOptions) ([]*gitlab.ProjectGroup, error) {
	groups, _, err := c.client.Projects.ListProjectsGroups(id, opts, gitlab.WithContext(ctx))
	return groups, err
}

func (c officialClient) ListProjectMembers(ctx context.Context, id any, opts *gitlab.ListProjectMembersOptions) ([]*gitlab.ProjectMember, error) {
	members, _, err := c.client.ProjectMembers.ListProjectMembers(id, opts, gitlab.WithContext(ctx))
	return members, err
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

func (c officialClient) GetMergeRequestApprovals(ctx context.Context, id any, mr int64) (*gitlab.MergeRequestApprovals, error) {
	out, _, err := c.client.MergeRequests.GetMergeRequestApprovals(id, mr, gitlab.WithContext(ctx))
	return out, err
}

func (c officialClient) GetMergeRequestCommits(ctx context.Context, id any, mr int64, opts *gitlab.GetMergeRequestCommitsOptions) ([]*gitlab.Commit, error) {
	out, _, err := c.client.MergeRequests.GetMergeRequestCommits(id, mr, opts, gitlab.WithContext(ctx))
	return out, err
}

func (c officialClient) GetMergeRequestChanges(ctx context.Context, id any, mr int64, opts *gitlab.GetMergeRequestChangesOptions) (*gitlab.MergeRequest, error) {
	out, _, err := c.client.MergeRequests.GetMergeRequestChanges(id, mr, opts, gitlab.WithContext(ctx))
	return out, err
}

func (c officialClient) GetMergeRequestDiffVersions(ctx context.Context, id any, mr int64, opts *gitlab.GetMergeRequestDiffVersionsOptions) ([]*gitlab.MergeRequestDiffVersion, error) {
	out, _, err := c.client.MergeRequests.GetMergeRequestDiffVersions(id, mr, opts, gitlab.WithContext(ctx))
	return out, err
}

func (c officialClient) ListMergeRequestDiscussions(ctx context.Context, id any, mr int64, opts *gitlab.ListMergeRequestDiscussionsOptions) ([]*gitlab.Discussion, error) {
	out, _, err := c.client.Discussions.ListMergeRequestDiscussions(id, mr, opts, gitlab.WithContext(ctx))
	return out, err
}

func (c officialClient) CreateMergeRequestDiscussion(ctx context.Context, id any, mr int64, opts *gitlab.CreateMergeRequestDiscussionOptions) (*gitlab.Discussion, error) {
	out, _, err := c.client.Discussions.CreateMergeRequestDiscussion(id, mr, opts, gitlab.WithContext(ctx))
	return out, err
}

func (c officialClient) AddMergeRequestDiscussionNote(ctx context.Context, id any, mr int64, discussionID string, opts *gitlab.AddMergeRequestDiscussionNoteOptions) (*gitlab.Note, error) {
	out, _, err := c.client.Discussions.AddMergeRequestDiscussionNote(id, mr, discussionID, opts, gitlab.WithContext(ctx))
	return out, err
}

func (c officialClient) ResolveMergeRequestDiscussion(ctx context.Context, id any, mr int64, discussionID string, opts *gitlab.ResolveMergeRequestDiscussionOptions) (*gitlab.Discussion, error) {
	out, _, err := c.client.Discussions.ResolveMergeRequestDiscussion(id, mr, discussionID, opts, gitlab.WithContext(ctx))
	return out, err
}

func (c officialClient) ListMergeRequestAwardEmoji(ctx context.Context, id any, mr int64, opts *gitlab.ListAwardEmojiOptions) ([]*gitlab.AwardEmoji, error) {
	out, _, err := c.client.AwardEmoji.ListMergeRequestAwardEmoji(id, mr, opts, gitlab.WithContext(ctx))
	return out, err
}

func (c officialClient) ListMergeRequestAwardEmojiOnNote(ctx context.Context, id any, mr int64, noteID int64, opts *gitlab.ListAwardEmojiOptions) ([]*gitlab.AwardEmoji, error) {
	out, _, err := c.client.AwardEmoji.ListMergeRequestAwardEmojiOnNote(id, mr, noteID, opts, gitlab.WithContext(ctx))
	return out, err
}

func (c officialClient) CreateMergeRequestAwardEmoji(ctx context.Context, id any, mr int64, opts *gitlab.CreateAwardEmojiOptions) (*gitlab.AwardEmoji, error) {
	out, _, err := c.client.AwardEmoji.CreateMergeRequestAwardEmoji(id, mr, opts, gitlab.WithContext(ctx))
	return out, err
}

func (c officialClient) CreateMergeRequestAwardEmojiOnNote(ctx context.Context, id any, mr int64, noteID int64, opts *gitlab.CreateAwardEmojiOptions) (*gitlab.AwardEmoji, error) {
	out, _, err := c.client.AwardEmoji.CreateMergeRequestAwardEmojiOnNote(id, mr, noteID, opts, gitlab.WithContext(ctx))
	return out, err
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

func (c officialClient) ListBranches(ctx context.Context, id any, opts *gitlab.ListBranchesOptions) ([]*gitlab.Branch, error) {
	out, _, err := c.client.Branches.ListBranches(id, opts, gitlab.WithContext(ctx))
	return out, err
}

func (c officialClient) GetBranch(ctx context.Context, id any, branch string) (*gitlab.Branch, error) {
	out, _, err := c.client.Branches.GetBranch(id, branch, gitlab.WithContext(ctx))
	return out, err
}

func (c officialClient) ListTags(ctx context.Context, id any, opts *gitlab.ListTagsOptions) ([]*gitlab.Tag, error) {
	out, _, err := c.client.Tags.ListTags(id, opts, gitlab.WithContext(ctx))
	return out, err
}

func (c officialClient) GetTag(ctx context.Context, id any, tag string) (*gitlab.Tag, error) {
	out, _, err := c.client.Tags.GetTag(id, tag, gitlab.WithContext(ctx))
	return out, err
}

func (c officialClient) ListCommits(ctx context.Context, id any, opts *gitlab.ListCommitsOptions) ([]*gitlab.Commit, error) {
	out, _, err := c.client.Commits.ListCommits(id, opts, gitlab.WithContext(ctx))
	return out, err
}

func (c officialClient) GetCommit(ctx context.Context, id any, sha string, opts *gitlab.GetCommitOptions) (*gitlab.Commit, error) {
	out, _, err := c.client.Commits.GetCommit(id, sha, opts, gitlab.WithContext(ctx))
	return out, err
}

func (c officialClient) ListMergeRequestsByCommit(ctx context.Context, id any, sha string) ([]*gitlab.BasicMergeRequest, error) {
	out, _, err := c.client.Commits.ListMergeRequestsByCommit(id, sha, gitlab.WithContext(ctx))
	return out, err
}

func (c officialClient) ListTree(ctx context.Context, id any, opts *gitlab.ListTreeOptions) ([]*gitlab.TreeNode, error) {
	out, _, err := c.client.Repositories.ListTree(id, opts, gitlab.WithContext(ctx))
	return out, err
}

func (c officialClient) ListProjectContributors(ctx context.Context, id any, opts *gitlab.ListContributorsOptions) ([]*gitlab.Contributor, error) {
	out, _, err := c.client.Repositories.Contributors(id, opts, gitlab.WithContext(ctx))
	return out, err
}

func (c officialClient) CompareRefs(ctx context.Context, id any, opts *gitlab.CompareOptions) (*gitlab.Compare, error) {
	out, _, err := c.client.Repositories.Compare(id, opts, gitlab.WithContext(ctx))
	return out, err
}

func (c officialClient) GetFile(ctx context.Context, id any, fileName string, opts *gitlab.GetFileOptions) (*gitlab.File, error) {
	out, _, err := c.client.RepositoryFiles.GetFile(id, fileName, opts, gitlab.WithContext(ctx))
	return out, err
}

func (c officialClient) GetFileBlame(ctx context.Context, id any, fileName string, opts *gitlab.GetFileBlameOptions) ([]*gitlab.FileBlameRange, error) {
	out, _, err := c.client.RepositoryFiles.GetFileBlame(id, fileName, opts, gitlab.WithContext(ctx))
	return out, err
}

func (c officialClient) SearchBlobsByProject(ctx context.Context, id any, query string, opts *gitlab.SearchOptions) ([]*gitlab.Blob, error) {
	out, _, err := c.client.Search.BlobsByProject(id, query, opts, gitlab.WithContext(ctx))
	return out, err
}

func (c officialClient) ListProjectJobs(ctx context.Context, id any, opts *gitlab.ListJobsOptions) ([]*gitlab.Job, error) {
	out, _, err := c.client.Jobs.ListProjectJobs(id, opts, gitlab.WithContext(ctx))
	return out, err
}

func (c officialClient) ListPipelineJobs(ctx context.Context, id any, pipeline int64, opts *gitlab.ListJobsOptions) ([]*gitlab.Job, error) {
	out, _, err := c.client.Jobs.ListPipelineJobs(id, pipeline, opts, gitlab.WithContext(ctx))
	return out, err
}

func (c officialClient) GetJob(ctx context.Context, id any, jobID int64) (*gitlab.Job, error) {
	out, _, err := c.client.Jobs.GetJob(id, jobID, gitlab.WithContext(ctx))
	return out, err
}

func (c officialClient) GetTraceFile(ctx context.Context, id any, jobID int64) ([]byte, error) {
	reader, _, err := c.client.Jobs.GetTraceFile(id, jobID, gitlab.WithContext(ctx))
	if err != nil {
		return nil, err
	}
	if reader == nil {
		return nil, nil
	}
	data := make([]byte, reader.Len())
	_, _ = reader.ReadAt(data, 0)
	return data, nil
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

func (c officialClient) CreatePipeline(ctx context.Context, id any, opts *gitlab.CreatePipelineOptions) (*gitlab.Pipeline, error) {
	out, _, err := c.client.Pipelines.CreatePipeline(id, opts, gitlab.WithContext(ctx))
	return out, err
}

func (c officialClient) RetryPipelineBuild(ctx context.Context, id any, pipeline int64) (*gitlab.Pipeline, error) {
	out, _, err := c.client.Pipelines.RetryPipelineBuild(id, pipeline, gitlab.WithContext(ctx))
	return out, err
}

func (c officialClient) CancelPipelineBuild(ctx context.Context, id any, pipeline int64) (*gitlab.Pipeline, error) {
	out, _, err := c.client.Pipelines.CancelPipelineBuild(id, pipeline, gitlab.WithContext(ctx))
	return out, err
}

func (c officialClient) CreateFile(ctx context.Context, id any, fileName string, opts *gitlab.CreateFileOptions) (*gitlab.FileInfo, error) {
	out, _, err := c.client.RepositoryFiles.CreateFile(id, fileName, opts, gitlab.WithContext(ctx))
	return out, err
}

func (c officialClient) UpdateFile(ctx context.Context, id any, fileName string, opts *gitlab.UpdateFileOptions) (*gitlab.FileInfo, error) {
	out, _, err := c.client.RepositoryFiles.UpdateFile(id, fileName, opts, gitlab.WithContext(ctx))
	return out, err
}

func (c officialClient) DeleteFile(ctx context.Context, id any, fileName string, opts *gitlab.DeleteFileOptions) error {
	_, err := c.client.RepositoryFiles.DeleteFile(id, fileName, opts, gitlab.WithContext(ctx))
	return err
}

func (c officialClient) CreateBranch(ctx context.Context, id any, opts *gitlab.CreateBranchOptions) (*gitlab.Branch, error) {
	out, _, err := c.client.Branches.CreateBranch(id, opts, gitlab.WithContext(ctx))
	return out, err
}

func (c officialClient) DeleteBranch(ctx context.Context, id any, branch string) error {
	_, err := c.client.Branches.DeleteBranch(id, branch, gitlab.WithContext(ctx))
	return err
}

func (c officialClient) DeleteMergedBranches(ctx context.Context, id any) error {
	_, err := c.client.Branches.DeleteMergedBranches(id, gitlab.WithContext(ctx))
	return err
}

func (c officialClient) CreateTag(ctx context.Context, id any, opts *gitlab.CreateTagOptions) (*gitlab.Tag, error) {
	out, _, err := c.client.Tags.CreateTag(id, opts, gitlab.WithContext(ctx))
	return out, err
}

func (c officialClient) DeleteTag(ctx context.Context, id any, tag string) error {
	_, err := c.client.Tags.DeleteTag(id, tag, gitlab.WithContext(ctx))
	return err
}

func (c officialClient) CreateCommit(ctx context.Context, id any, opts *gitlab.CreateCommitOptions) (*gitlab.Commit, error) {
	out, _, err := c.client.Commits.CreateCommit(id, opts, gitlab.WithContext(ctx))
	return out, err
}

func (c officialClient) CreateVariable(ctx context.Context, id any, opts *gitlab.CreateProjectVariableOptions) (*gitlab.ProjectVariable, error) {
	out, _, err := c.client.ProjectVariables.CreateVariable(id, opts, gitlab.WithContext(ctx))
	return out, err
}

func (c officialClient) UpdateVariable(ctx context.Context, id any, key string, opts *gitlab.UpdateProjectVariableOptions) (*gitlab.ProjectVariable, error) {
	out, _, err := c.client.ProjectVariables.UpdateVariable(id, key, opts, gitlab.WithContext(ctx))
	return out, err
}

func (c officialClient) RemoveVariable(ctx context.Context, id any, key string, opts *gitlab.RemoveProjectVariableOptions) error {
	_, err := c.client.ProjectVariables.RemoveVariable(id, key, opts, gitlab.WithContext(ctx))
	return err
}

func (c officialClient) ListSnippets(ctx context.Context, opts *gitlab.ListSnippetsOptions) ([]*gitlab.Snippet, error) {
	out, _, err := c.client.Snippets.ListSnippets(opts, gitlab.WithContext(ctx))
	return out, err
}

func (c officialClient) GetSnippet(ctx context.Context, snippetID int64) (*gitlab.Snippet, error) {
	out, _, err := c.client.Snippets.GetSnippet(snippetID, gitlab.WithContext(ctx))
	return out, err
}

func (c officialClient) GetSnippetContent(ctx context.Context, snippetID int64) ([]byte, error) {
	out, _, err := c.client.Snippets.SnippetContent(snippetID, gitlab.WithContext(ctx))
	return out, err
}

func (c officialClient) CreateSnippet(ctx context.Context, opts *gitlab.CreateSnippetOptions) (*gitlab.Snippet, error) {
	out, _, err := c.client.Snippets.CreateSnippet(opts, gitlab.WithContext(ctx))
	return out, err
}

func (c officialClient) DeleteSnippet(ctx context.Context, snippetID int64) error {
	_, err := c.client.Snippets.DeleteSnippet(snippetID, gitlab.WithContext(ctx))
	return err
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
