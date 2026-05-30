package jira

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/codewandler/md2adf"
	coredata "github.com/fluxplane/fluxplane-core/core/data"
	coredatasource "github.com/fluxplane/fluxplane-core/core/datasource"
	"github.com/fluxplane/fluxplane-core/core/operation"
	"github.com/fluxplane/fluxplane-core/core/resource"
	coresecret "github.com/fluxplane/fluxplane-core/core/secret"
	"github.com/fluxplane/fluxplane-core/orchestration/pluginhost"
	"github.com/fluxplane/fluxplane-core/plugins/internal/atlassian"
	runtimedatasource "github.com/fluxplane/fluxplane-core/runtime/datasource"
	operationruntime "github.com/fluxplane/fluxplane-core/runtime/operation"
	runtimesecret "github.com/fluxplane/fluxplane-core/runtime/secret"
	"github.com/fluxplane/fluxplane-core/runtime/system"
	"github.com/fluxplane/fluxplane-policy"
)

const (
	Name         = "jira"
	OperationSet = Name

	TokenMethod    = atlassian.TokenMethod
	APITokenMethod = atlassian.APITokenMethod
	OAuth2Method   = atlassian.OAuth2Method
)

const defaultPageSize = 50

type Config = atlassian.Config
type AuthConfig = atlassian.AuthConfig

type Plugin struct {
	pluginhost.Configurable[atlassian.Config]
	system   system.System
	store    runtimesecret.FileStore
	resolver runtimesecret.Resolver
	ref      resource.PluginRef
	cfg      atlassian.Config
}

var _ pluginhost.Plugin = Plugin{}
var _ pluginhost.InstanceFactory = Plugin{}
var _ pluginhost.OperationContributor = Plugin{}
var _ pluginhost.DatasourceProviderContributor = Plugin{}
var _ pluginhost.AuthMethodContributor = Plugin{}
var _ pluginhost.AuthTestContributor = Plugin{}

func New(sys system.System, stores ...runtimesecret.FileStore) Plugin {
	store := runtimesecret.NewFileStore(atlassian.DefaultAuthStorePath)
	if len(stores) > 0 {
		store = stores[0]
	}
	return NewWithResolver(sys, store, store)
}

func NewWithResolver(sys system.System, store runtimesecret.FileStore, resolver runtimesecret.Resolver) Plugin {
	if resolver == nil {
		resolver = store
	}
	return Plugin{system: sys, store: store, resolver: resolver}
}

func (Plugin) Manifest() pluginhost.Manifest {
	return pluginhost.Manifest{Name: Name, Description: "Jira datasource and issue operations."}
}

func (p Plugin) Instantiate(_ context.Context, ctx pluginhost.Context) (pluginhost.Plugin, error) {
	cfg, err := pluginhost.ConfigAs[atlassian.Config](ctx)
	if err != nil {
		return nil, err
	}
	return Plugin{system: p.system, store: p.store, resolver: p.resolver, ref: ctx.Ref, cfg: atlassian.NormalizeConfig(cfg)}, nil
}

func (p Plugin) Contributions(_ context.Context, ctx pluginhost.Context) (resource.ContributionBundle, error) {
	p = p.withRef(ctx.Ref)
	return resource.ContributionBundle{
		Operations: []operation.Spec{p.issueSearchSpec(), p.issueCreateSpec(), p.issueCommentSpec()},
		OperationSets: []operation.Set{{
			Name:        OperationSet,
			Description: "Jira issue operations.",
			Operations:  []operation.Ref{{Name: operation.Name(p.operationName("*"))}},
		}},
		DataSources: []coredata.SourceSpec{DataSourceSpec()},
	}, nil
}

func (p Plugin) Operations(_ context.Context, ctx pluginhost.Context) ([]operation.Operation, error) {
	p = p.withRef(ctx.Ref)
	return []operation.Operation{p.issueSearchOperation(), p.issueCreateOperation(), p.issueCommentOperation()}, nil
}

func (p Plugin) DatasourceProviders(_ context.Context, ctx pluginhost.Context) ([]coredatasource.Provider, error) {
	p = p.withRef(ctx.Ref)
	return []coredatasource.Provider{jiraDatasourceProvider{plugin: p}}, nil
}

func (p Plugin) AuthMethods(_ context.Context, ctx pluginhost.Context) ([]coresecret.AuthMethodSpec, error) {
	p = p.withRef(ctx.Ref)
	return atlassian.AuthMethods(Name, p.ref, AtlassianProduct(), p.cfg), nil
}

func (p Plugin) TestConnection(ctx context.Context, pluginCtx pluginhost.Context, req pluginhost.AuthTestRequest, reports chan<- pluginhost.AuthTestReport) error {
	ref := req.Ref
	if ref.Name == "" {
		ref = pluginCtx.Ref
	}
	p = p.withRef(ref)
	cfg := p.cfg
	if method := strings.TrimSpace(req.Method); method != "" {
		cfg.Auth.Method = method
	}
	resolver := req.Secrets
	if resolver == nil {
		resolver = p.resolver
	}
	session, err := atlassian.ResolveWithResolver(ctx, p.system, p.store, resolver, Name, p.ref, AtlassianProduct(), cfg)
	method := firstNonEmpty(session.Method, cfg.Auth.Method)
	if err != nil {
		reports <- p.authTestReport(method, "current_user", "failed", err.Error(), nil)
		return nil
	}
	var out struct {
		AccountID    string `json:"accountId"`
		DisplayName  string `json:"displayName"`
		EmailAddress string `json:"emailAddress"`
	}
	if _, err := atlassian.DoJSON(ctx, p.system, session, http.MethodGet, "/myself", nil, &out); err != nil {
		reports <- p.authTestReport(method, "current_user", "failed", err.Error(), nil)
		return nil
	}
	message := firstNonEmpty(out.DisplayName, out.EmailAddress, out.AccountID)
	reports <- p.authTestReport(method, "current_user", "ok", message, map[string]string{
		"account_id": out.AccountID,
		"display":    out.DisplayName,
		"email":      out.EmailAddress,
	})
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

func AtlassianProduct() atlassian.Product {
	return atlassian.Product{
		Name:         Name,
		DisplayName:  "Jira Cloud",
		ResourcePath: "jira",
		RESTPath:     "/rest/api/3",
		Scopes:       []string{"read:jira-work", "write:jira-work", "offline_access"},
	}
}

func (p Plugin) withRef(ref resource.PluginRef) Plugin {
	if p.ref.Name == "" && ref.Name != "" {
		p.ref = ref
	}
	if p.ref.Name == "" {
		p.ref.Name = Name
	}
	return p
}

func (p Plugin) session(ctx context.Context) (atlassian.Session, error) {
	return atlassian.ResolveWithResolver(ctx, p.system, p.store, p.resolver, Name, p.ref, AtlassianProduct(), p.cfg)
}

func (p Plugin) operationName(suffix string) string {
	prefix := normalize(p.ref.InstanceName())
	if prefix == "" {
		prefix = Name
	}
	return prefix + "_" + suffix
}

func normalize(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var b strings.Builder
	lastUnderscore := false
	for _, r := range value {
		ok := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')
		if ok {
			b.WriteRune(r)
			lastUnderscore = false
			continue
		}
		if !lastUnderscore {
			b.WriteByte('_')
			lastUnderscore = true
		}
	}
	return strings.Trim(b.String(), "_")
}

type Output struct {
	Status     string `json:"status"`
	HTTPStatus int    `json:"http_status,omitempty"`
	Data       any    `json:"data,omitempty"`
}

type issueSearchInput struct {
	JQL        string `json:"jql" jsonschema:"description=Jira Query Language expression.,required"`
	StartAt    int    `json:"start_at,omitempty" jsonschema:"description=Zero-based result offset. Defaults to 0."`
	MaxResults int    `json:"max_results,omitempty" jsonschema:"description=Maximum issues to return. Defaults to 50."`
}

type issueCreateInput struct {
	ProjectKey  string   `json:"project_key" jsonschema:"description=Jira project key such as DEV.,required"`
	IssueType   string   `json:"issue_type" jsonschema:"description=Jira issue type name such as Task or Bug.,required"`
	Summary     string   `json:"summary" jsonschema:"description=Issue summary.,required"`
	Description string   `json:"description,omitempty" jsonschema:"description=Issue description as Markdown."`
	Labels      []string `json:"labels,omitempty" jsonschema:"description=Labels to apply to the issue."`
	Priority    string   `json:"priority,omitempty" jsonschema:"description=Priority name to apply to the issue."`
	Parent      string   `json:"parent,omitempty" jsonschema:"description=Parent issue key for sub-tasks."`
}

type issueCommentInput struct {
	IssueKey string `json:"issue_key" jsonschema:"description=Jira issue key such as DEV-123.,required"`
	Body     string `json:"body" jsonschema:"description=Comment body as Markdown.,required"`
}

func (p Plugin) issueCreateSpec() operation.Spec {
	return operationruntime.WithTypedContract[issueCreateInput, Output](operation.Spec{
		Ref:         operation.Ref{Name: operation.Name(p.operationName("issue_create"))},
		Description: "Create a Jira issue.",
		Semantics: operation.Semantics{
			Determinism: operation.DeterminismNonDeterministic,
			Effects:     operation.EffectSet{operation.EffectNetwork, operation.EffectWriteExternal, operation.EffectCreate},
			Idempotency: operation.IdempotencyNonIdempotent,
			Risk:        operation.RiskMedium,
		},
	})
}

func (p Plugin) issueCreateOperation() operation.Operation {
	return operationruntime.NewTypedResult[issueCreateInput, Output](
		p.issueCreateSpec(),
		p.runIssueCreate,
		operationruntime.WithAccess(p.issueCreateAccess),
	)
}

func (p Plugin) runIssueCreate(ctx operation.Context, input issueCreateInput) operation.Result {
	session, err := p.session(ctx)
	if err != nil {
		return operation.Failed(p.operationName("issue_create")+"_failed", err.Error(), nil)
	}
	input.ProjectKey = strings.TrimSpace(input.ProjectKey)
	input.IssueType = strings.TrimSpace(input.IssueType)
	input.Summary = strings.TrimSpace(input.Summary)
	if input.ProjectKey == "" || input.IssueType == "" || input.Summary == "" {
		return operation.Failed("invalid_"+p.operationName("issue_create")+"_input", "project_key, issue_type, and summary are required", nil)
	}
	var data map[string]any
	status, err := jiraCreateIssue(ctx, p.system, session, input, &data)
	if err != nil {
		return operation.Failed(p.operationName("issue_create")+"_failed", err.Error(), nil)
	}
	if key, _ := data["key"].(string); key != "" {
		data["url"] = strings.TrimRight(session.SiteURL, "/") + "/browse/" + key
	}
	return operation.OK(Output{Status: "ok", HTTPStatus: status, Data: data})
}

func (p Plugin) issueCreateAccess(ctx operation.Context, _ issueCreateInput) ([]operationruntime.AccessDescriptor, error) {
	return []operationruntime.AccessDescriptor{operationruntime.NetworkDescriptor(p.authzBaseURL(ctx), policy.ActionNetworkFetch)}, nil
}

func (p Plugin) issueCommentSpec() operation.Spec {
	return operationruntime.WithTypedContract[issueCommentInput, Output](operation.Spec{
		Ref:         operation.Ref{Name: operation.Name(p.operationName("issue_comment"))},
		Description: "Add a Markdown comment to a Jira issue.",
		Semantics: operation.Semantics{
			Determinism: operation.DeterminismNonDeterministic,
			Effects:     operation.EffectSet{operation.EffectNetwork, operation.EffectWriteExternal, operation.EffectCreate},
			Idempotency: operation.IdempotencyNonIdempotent,
			Risk:        operation.RiskMedium,
		},
	})
}

func (p Plugin) issueCommentOperation() operation.Operation {
	return operationruntime.NewTypedResult[issueCommentInput, Output](
		p.issueCommentSpec(),
		p.runIssueComment,
		operationruntime.WithAccess(p.issueCommentAccess),
	)
}

func (p Plugin) runIssueComment(ctx operation.Context, input issueCommentInput) operation.Result {
	session, err := p.session(ctx)
	if err != nil {
		return operation.Failed(p.operationName("issue_comment")+"_failed", err.Error(), nil)
	}
	input.IssueKey = strings.TrimSpace(input.IssueKey)
	if input.IssueKey == "" || strings.TrimSpace(input.Body) == "" {
		return operation.Failed("invalid_"+p.operationName("issue_comment")+"_input", "issue_key and body are required", nil)
	}
	var data map[string]any
	status, err := jiraAddComment(ctx, p.system, session, input, &data)
	if err != nil {
		return operation.Failed(p.operationName("issue_comment")+"_failed", err.Error(), nil)
	}
	return operation.OK(Output{Status: "ok", HTTPStatus: status, Data: data})
}

func (p Plugin) issueCommentAccess(ctx operation.Context, _ issueCommentInput) ([]operationruntime.AccessDescriptor, error) {
	return []operationruntime.AccessDescriptor{operationruntime.NetworkDescriptor(p.authzBaseURL(ctx), policy.ActionNetworkFetch)}, nil
}

func (p Plugin) issueSearchSpec() operation.Spec {
	return operationruntime.WithTypedContract[issueSearchInput, Output](operation.Spec{
		Ref:         operation.Ref{Name: operation.Name(p.operationName("issue_search"))},
		Description: "Search Jira issues using JQL.",
		Semantics: operation.Semantics{
			Determinism: operation.DeterminismNonDeterministic,
			Effects:     operation.EffectSet{operation.EffectNetwork, operation.EffectReadExternal},
			Idempotency: operation.IdempotencyIdempotent,
			Risk:        operation.RiskLow,
		},
	})
}

func (p Plugin) issueSearchOperation() operation.Operation {
	return operationruntime.NewTypedResult[issueSearchInput, Output](
		p.issueSearchSpec(),
		p.runIssueSearch,
		operationruntime.WithAccess(p.issueSearchAccess),
	)
}

func (p Plugin) runIssueSearch(ctx operation.Context, input issueSearchInput) operation.Result {
	session, err := p.session(ctx)
	if err != nil {
		return operation.Failed(p.operationName("issue_search")+"_failed", err.Error(), nil)
	}
	jql := strings.TrimSpace(input.JQL)
	if jql == "" {
		return operation.Failed("invalid_"+p.operationName("issue_search")+"_input", "jql is required", nil)
	}
	var data map[string]any
	status, err := jiraSearchIssues(ctx, p.system, session, jql, input.StartAt, input.MaxResults, &data)
	if err != nil {
		return operation.Failed(p.operationName("issue_search")+"_failed", err.Error(), nil)
	}
	return operation.OK(Output{Status: "ok", HTTPStatus: status, Data: data})
}

func (p Plugin) issueSearchAccess(ctx operation.Context, _ issueSearchInput) ([]operationruntime.AccessDescriptor, error) {
	return []operationruntime.AccessDescriptor{operationruntime.NetworkDescriptor(p.authzBaseURL(ctx), policy.ActionNetworkFetch)}, nil
}

const IssueEntity coredatasource.EntityType = "jira.issue"
const ProjectEntity coredatasource.EntityType = "jira.project"

type Issue struct {
	ID          string `json:"id,omitempty" datasource:"filterable" jsonschema:"description=Jira issue id."`
	Key         string `json:"key" datasource:"id,searchable,filterable" jsonschema:"description=Jira issue key.,required"`
	Summary     string `json:"summary,omitempty" datasource:"searchable" jsonschema:"description=Issue summary."`
	Description string `json:"description,omitempty" datasource:"searchable" jsonschema:"description=Issue description."`
	Status      string `json:"status,omitempty" datasource:"filterable" jsonschema:"description=Issue status."`
	Self        string `json:"self,omitempty" jsonschema:"description=Jira API URL."`
}

type Project struct {
	ID             string `json:"id,omitempty" datasource:"filterable" jsonschema:"description=Jira project id."`
	Key            string `json:"key" datasource:"id,searchable,filterable" jsonschema:"description=Jira project key.,required"`
	Name           string `json:"name,omitempty" datasource:"searchable" jsonschema:"description=Project name."`
	Description    string `json:"description,omitempty" datasource:"searchable" jsonschema:"description=Project description."`
	ProjectTypeKey string `json:"projectTypeKey,omitempty" datasource:"filterable" jsonschema:"description=Jira project type."`
	Self           string `json:"self,omitempty" jsonschema:"description=Jira API URL."`
}

type jiraDatasourceProvider struct {
	plugin Plugin
}

func (p jiraDatasourceProvider) Entities() []coredatasource.EntitySpec {
	return entitySpecs()
}

func (p jiraDatasourceProvider) Open(_ context.Context, spec coredatasource.Spec) (coredatasource.Accessor, error) {
	if strings.TrimSpace(spec.Kind) != Name {
		return nil, fmt.Errorf("unsupported datasource kind %q", spec.Kind)
	}
	var selected []coredatasource.EntitySpec
	if len(spec.Entities) == 0 {
		selected = entitySpecs()
	} else {
		var err error
		selected, err = runtimedatasource.SelectEntities(Name, entitySpecs(), spec.Entities)
		if err != nil {
			return nil, err
		}
	}
	instance := strings.TrimSpace(spec.Config["instance"])
	if instance != "" && instance != p.plugin.ref.InstanceName() {
		return nil, fmt.Errorf("jira datasource instance %q does not match plugin instance %q", instance, p.plugin.ref.InstanceName())
	}
	return jiraAccessor{spec: spec, plugin: p.plugin, entities: selected}, nil
}

type jiraAccessor struct {
	spec     coredatasource.Spec
	plugin   Plugin
	entities []coredatasource.EntitySpec
}

func (a jiraAccessor) Spec() coredatasource.Spec { return a.spec }

func (a jiraAccessor) Entities() []coredatasource.EntitySpec {
	return append([]coredatasource.EntitySpec(nil), a.entities...)
}

func (a jiraAccessor) Search(ctx context.Context, req coredatasource.SearchRequest) (coredatasource.SearchResult, error) {
	entity := req.Entity
	if entity == "" {
		entity = IssueEntity
	}
	if !runtimedatasource.HasEntity(a.entities, entity) {
		return coredatasource.SearchResult{}, fmt.Errorf("datasource %q does not expose entity %q", a.spec.Name, entity)
	}
	session, err := a.plugin.session(ctx)
	if err != nil {
		return coredatasource.SearchResult{}, err
	}
	limit := normalizedLimit(req.Limit)
	switch entity {
	case IssueEntity:
		var data issueSearchResponse
		if _, err := jiraSearchIssues(ctx, a.plugin.system, session, jiraDatasourceJQL(req.Query), 0, limit, &data); err != nil {
			return coredatasource.SearchResult{}, err
		}
		return runtimedatasource.SearchResult(a.spec.Name, entity, runtimedatasource.NonEmptyRecordsFrom(issuesFromJira(data.Issues), func(issue Issue) coredatasource.Record {
			return a.issueRecord(session, issue)
		}), data.Total), nil
	case ProjectEntity:
		projects, total, err := jiraListProjects(ctx, a.plugin.system, session, 0, max(limit, 200))
		if err != nil {
			return coredatasource.SearchResult{}, err
		}
		records := runtimedatasource.NonEmptyRecordsFrom(filterProjects(projects, req.Query), func(project Project) coredatasource.Record {
			return a.projectRecord(session, project)
		})
		if len(records) > limit {
			records = records[:limit]
		}
		return runtimedatasource.SearchResult(a.spec.Name, entity, records, total), nil
	default:
		return coredatasource.SearchResult{}, fmt.Errorf("datasource %q entity %q does not support search", a.spec.Name, entity)
	}
}

func (a jiraAccessor) List(ctx context.Context, req coredatasource.ListRequest) (coredatasource.ListResult, error) {
	entity := req.Entity
	if entity == "" {
		entity = ProjectEntity
	}
	if !runtimedatasource.HasEntity(a.entities, entity) {
		return coredatasource.ListResult{}, fmt.Errorf("datasource %q does not expose entity %q", a.spec.Name, entity)
	}
	session, err := a.plugin.session(ctx)
	if err != nil {
		return coredatasource.ListResult{}, err
	}
	limit := normalizedLimit(req.Limit)
	start := cursorOffset(req.Cursor)
	switch entity {
	case IssueEntity:
		var data issueSearchResponse
		if _, err := jiraSearchIssues(ctx, a.plugin.system, session, "updated >= -30d ORDER BY updated DESC", start, limit, &data); err != nil {
			return coredatasource.ListResult{}, err
		}
		return runtimedatasource.ListResultOffset(a.spec.Name, entity, runtimedatasource.NonEmptyRecordsFrom(issuesFromJira(data.Issues), func(issue Issue) coredatasource.Record {
			return a.issueRecord(session, issue)
		}), data.Total, start, limit), nil
	case ProjectEntity:
		projects, total, err := jiraListProjects(ctx, a.plugin.system, session, start, limit)
		if err != nil {
			return coredatasource.ListResult{}, err
		}
		return runtimedatasource.ListResultOffset(a.spec.Name, entity, runtimedatasource.NonEmptyRecordsFrom(projects, func(project Project) coredatasource.Record {
			return a.projectRecord(session, project)
		}), total, start, limit), nil
	default:
		return coredatasource.ListResult{}, fmt.Errorf("datasource %q entity %q does not support list", a.spec.Name, entity)
	}
}

func (a jiraAccessor) Get(ctx context.Context, req coredatasource.GetRequest) (coredatasource.Record, error) {
	if !runtimedatasource.HasEntity(a.entities, req.Entity) {
		return coredatasource.Record{}, fmt.Errorf("datasource %q does not expose entity %q", a.spec.Name, req.Entity)
	}
	session, err := a.plugin.session(ctx)
	if err != nil {
		return coredatasource.Record{}, err
	}
	switch req.Entity {
	case IssueEntity:
		issue, err := jiraGetIssue(ctx, a.plugin.system, session, req.ID)
		if err != nil {
			return coredatasource.Record{}, err
		}
		return a.issueRecord(session, issue), nil
	case ProjectEntity:
		project, err := jiraGetProject(ctx, a.plugin.system, session, req.ID)
		if err != nil {
			return coredatasource.Record{}, err
		}
		return a.projectRecord(session, project), nil
	default:
		return coredatasource.Record{}, fmt.Errorf("datasource %q entity %q does not support get", a.spec.Name, req.Entity)
	}
}

func (a jiraAccessor) BatchGet(ctx context.Context, req coredatasource.BatchGetRequest) (coredatasource.BatchGetResult, error) {
	out := coredatasource.BatchGetResult{Datasource: a.spec.Name, Entity: req.Entity}
	for _, id := range req.IDs {
		record, err := a.Get(ctx, coredatasource.GetRequest{Entity: req.Entity, ID: id})
		if err != nil {
			out.Errors = append(out.Errors, coredatasource.BatchGetError{ID: id, Message: err.Error()})
			continue
		}
		out.Records = append(out.Records, record)
	}
	return out, nil
}

func (a jiraAccessor) issueRecord(session atlassian.Session, issue Issue) coredatasource.Record {
	return coredatasource.Record{
		ID:         issue.Key,
		Datasource: a.spec.Name,
		Entity:     IssueEntity,
		Title:      firstNonEmpty(issue.Summary, issue.Key),
		Content:    issue.Description,
		URL:        jiraIssueURL(session, issue),
		Metadata: map[string]string{
			"api_url": issue.Self,
			"id":      issue.ID,
			"key":     issue.Key,
			"status":  issue.Status,
			"project": projectKey(issue.Key),
		},
		Raw: issue,
	}
}

func (a jiraAccessor) projectRecord(session atlassian.Session, project Project) coredatasource.Record {
	return coredatasource.Record{
		ID:         project.Key,
		Datasource: a.spec.Name,
		Entity:     ProjectEntity,
		Title:      firstNonEmpty(project.Name, project.Key),
		Content:    strings.Join(cleaned([]string{project.Description, project.ProjectTypeKey}), " "),
		URL:        jiraProjectURL(session, project),
		Metadata: map[string]string{
			"api_url":      project.Self,
			"id":           project.ID,
			"key":          project.Key,
			"project_type": project.ProjectTypeKey,
		},
		Raw: project,
	}
}

func jiraIssueURL(session atlassian.Session, issue Issue) string {
	if session.SiteURL != "" && issue.Key != "" {
		return strings.TrimRight(session.SiteURL, "/") + "/browse/" + issue.Key
	}
	return ""
}

func jiraProjectURL(session atlassian.Session, project Project) string {
	if session.SiteURL != "" && project.Key != "" {
		return strings.TrimRight(session.SiteURL, "/") + "/browse/" + project.Key
	}
	return ""
}

func entitySpecs() []coredatasource.EntitySpec {
	issueEntity := runtimedatasource.EntityOf[Issue](IssueEntity, "Jira issue.")
	issueEntity.Capabilities = []coredatasource.EntityCapability{
		coredatasource.EntityCapabilitySearch,
		coredatasource.EntityCapabilityList,
		coredatasource.EntityCapabilityGet,
	}
	issueEntity.Detectors = []coredatasource.DetectorSpec{
		{
			Name:          "jira_issue_key",
			Kind:          coredatasource.DetectorRegex,
			Pattern:       `\b([A-Z][A-Z0-9]+-\d+)\b`,
			IDTemplate:    "$1",
			QueryTemplate: "$1",
			Confidence:    0.95,
		},
		{
			Name:          "jira_issue_url",
			Kind:          coredatasource.DetectorURL,
			Pattern:       `https?://[^\s<>"']+/browse/([A-Z][A-Z0-9]+-\d+)`,
			IDTemplate:    "$1",
			QueryTemplate: "$1",
			URLTemplate:   "$0",
			Confidence:    0.95,
		},
	}
	projectEntity := runtimedatasource.EntityOf[Project](ProjectEntity, "Jira project.")
	projectEntity.Capabilities = []coredatasource.EntityCapability{
		coredatasource.EntityCapabilitySearch,
		coredatasource.EntityCapabilityList,
		coredatasource.EntityCapabilityGet,
	}
	return []coredatasource.EntitySpec{issueEntity, projectEntity}
}

type issueSearchResponse struct {
	Issues     []jiraIssue `json:"issues"`
	Total      int         `json:"total"`
	StartAt    int         `json:"startAt"`
	MaxResults int         `json:"maxResults"`
}

type jiraIssue struct {
	ID     string          `json:"id"`
	Key    string          `json:"key"`
	Self   string          `json:"self"`
	Fields jiraIssueFields `json:"fields"`
}

type jiraIssueFields struct {
	Summary     string         `json:"summary"`
	Description any            `json:"description"`
	Status      jiraNamedValue `json:"status"`
	Project     jiraProjectRef `json:"project"`
}

type jiraNamedValue struct {
	Name string `json:"name"`
}

type jiraProjectRef struct {
	Key            string `json:"key"`
	ProjectTypeKey string `json:"projectTypeKey"`
}

type jiraProjectSearchResponse struct {
	Values     []jiraProject `json:"values"`
	Total      int           `json:"total"`
	StartAt    int           `json:"startAt"`
	MaxResults int           `json:"maxResults"`
}

type jiraProject struct {
	ID             string `json:"id"`
	Key            string `json:"key"`
	Name           string `json:"name"`
	Description    string `json:"description"`
	ProjectTypeKey string `json:"projectTypeKey"`
	Self           string `json:"self"`
}

func jiraCreateIssue(ctx context.Context, sys system.System, session atlassian.Session, input issueCreateInput, out any) (int, error) {
	fields := map[string]any{
		"project":   map[string]string{"key": input.ProjectKey},
		"issuetype": map[string]string{"name": input.IssueType},
		"summary":   input.Summary,
	}
	if strings.TrimSpace(input.Description) != "" {
		fields["description"] = jiraMarkdownToADF(ctx, sys, session, input.Description)
	}
	if labels := cleaned(input.Labels); len(labels) > 0 {
		fields["labels"] = labels
	}
	if priority := strings.TrimSpace(input.Priority); priority != "" {
		fields["priority"] = map[string]string{"name": priority}
	}
	if parent := strings.TrimSpace(input.Parent); parent != "" {
		fields["parent"] = map[string]string{"key": parent}
	}
	return atlassian.DoJSON(ctx, sys, session, http.MethodPost, "/issue", map[string]any{"fields": fields}, out)
}

func jiraAddComment(ctx context.Context, sys system.System, session atlassian.Session, input issueCommentInput, out any) (int, error) {
	body := map[string]any{"body": jiraMarkdownToADF(ctx, sys, session, input.Body)}
	path := "/issue/" + url.PathEscape(input.IssueKey) + "/comment"
	return atlassian.DoJSON(ctx, sys, session, http.MethodPost, path, body, out)
}

func jiraMarkdownToADF(ctx context.Context, sys system.System, session atlassian.Session, text string) md2adf.Node {
	return md2adf.Convert(jiraLinkifyIssueKeys(ctx, sys, session, text))
}

func jiraLinkifyIssueKeys(ctx context.Context, sys system.System, session atlassian.Session, text string) string {
	siteURL := strings.TrimRight(strings.TrimSpace(session.SiteURL), "/")
	if siteURL == "" || strings.TrimSpace(text) == "" || sys == nil || sys.Network() == nil {
		return text
	}
	projects, _, err := jiraListProjects(ctx, sys, session, 0, 200)
	if err != nil || len(projects) == 0 {
		return text
	}
	keys := make([]string, 0, len(projects))
	for _, project := range projects {
		if key := strings.TrimSpace(project.Key); key != "" {
			keys = append(keys, regexp.QuoteMeta(key))
		}
	}
	if len(keys) == 0 {
		return text
	}
	re := regexp.MustCompile(`\b(` + strings.Join(keys, "|") + `)-(\d+)\b`)
	return re.ReplaceAllStringFunc(text, func(match string) string {
		return siteURL + "/browse/" + match
	})
}

func jiraSearchIssues(ctx context.Context, sys system.System, session atlassian.Session, jql string, startAt, maxResults int, out any) (int, error) {
	limit := normalizedLimit(maxResults)
	query := url.Values{}
	query.Set("jql", jql)
	query.Set("startAt", strconv.Itoa(max(0, startAt)))
	query.Set("maxResults", strconv.Itoa(limit))
	query.Set("fields", "summary,status,issuetype,priority,assignee,reporter,created,updated,labels,description,comment,parent,project")
	return atlassian.DoJSON(ctx, sys, session, http.MethodGet, "/search/jql?"+query.Encode(), nil, out)
}

func jiraGetIssue(ctx context.Context, sys system.System, session atlassian.Session, key string) (Issue, error) {
	var raw jiraIssue
	if _, err := atlassian.DoJSON(ctx, sys, session, http.MethodGet, "/issue/"+url.PathEscape(strings.TrimSpace(key))+"?expand=renderedFields,names", nil, &raw); err != nil {
		return Issue{}, err
	}
	return issueFromJira(raw), nil
}

func jiraListProjects(ctx context.Context, sys system.System, session atlassian.Session, startAt, maxResults int) ([]Project, int, error) {
	query := url.Values{}
	query.Set("startAt", strconv.Itoa(max(0, startAt)))
	query.Set("maxResults", strconv.Itoa(normalizedLimit(maxResults)))
	var data jiraProjectSearchResponse
	if _, err := atlassian.DoJSON(ctx, sys, session, http.MethodGet, "/project/search?"+query.Encode(), nil, &data); err != nil {
		return nil, 0, err
	}
	projects := make([]Project, 0, len(data.Values))
	for _, project := range data.Values {
		projects = append(projects, projectFromJira(project))
	}
	return projects, data.Total, nil
}

func jiraGetProject(ctx context.Context, sys system.System, session atlassian.Session, key string) (Project, error) {
	var raw jiraProject
	if _, err := atlassian.DoJSON(ctx, sys, session, http.MethodGet, "/project/"+url.PathEscape(strings.TrimSpace(key)), nil, &raw); err != nil {
		return Project{}, err
	}
	return projectFromJira(raw), nil
}

func issueFromJira(issue jiraIssue) Issue {
	return Issue{
		ID:          issue.ID,
		Key:         issue.Key,
		Summary:     issue.Fields.Summary,
		Description: descriptionText(issue.Fields.Description),
		Status:      issue.Fields.Status.Name,
		Self:        issue.Self,
	}
}

func issuesFromJira(values []jiraIssue) []Issue {
	out := make([]Issue, 0, len(values))
	for _, value := range values {
		out = append(out, issueFromJira(value))
	}
	return out
}

func projectFromJira(project jiraProject) Project {
	return Project(project)
}

func descriptionText(value any) string {
	switch typed := value.(type) {
	case nil:
		return ""
	case string:
		return typed
	case map[string]any:
		if text := adfText(typed); text != "" {
			return text
		}
	}
	data, err := json.Marshal(value)
	if err != nil {
		return fmt.Sprint(value)
	}
	return string(data)
}

func adfText(node map[string]any) string {
	var parts []string
	collectADFText(node, &parts)
	return strings.Join(parts, " ")
}

func collectADFText(value any, parts *[]string) {
	switch typed := value.(type) {
	case map[string]any:
		if text, ok := typed["text"].(string); ok && strings.TrimSpace(text) != "" {
			*parts = append(*parts, strings.TrimSpace(text))
		}
		if content, ok := typed["content"].([]any); ok {
			for _, child := range content {
				collectADFText(child, parts)
			}
		}
	case []any:
		for _, child := range typed {
			collectADFText(child, parts)
		}
	}
}

func filterProjects(projects []Project, query string) []Project {
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		return projects
	}
	var out []Project
	for _, project := range projects {
		values := []string{project.ID, project.Key, project.Name, project.Description, project.ProjectTypeKey}
		for _, value := range values {
			if strings.Contains(strings.ToLower(value), query) {
				out = append(out, project)
				break
			}
		}
	}
	return out
}

func jiraDatasourceJQL(query string) string {
	query = strings.TrimSpace(query)
	if query == "" || looksLikeJQL(query) {
		return query
	}
	if looksLikeIssueKey(query) {
		return "issuekey = " + query + " OR text ~ " + quoteJQL(query)
	}
	return "text ~ " + quoteJQL(query)
}

func looksLikeJQL(query string) bool {
	upper := strings.ToUpper(query)
	return strings.Contains(query, "=") || strings.Contains(query, "~") || strings.Contains(upper, " ORDER BY ") || strings.Contains(upper, " AND ") || strings.Contains(upper, " OR ")
}

func looksLikeIssueKey(query string) bool {
	if query == "" {
		return false
	}
	dash := strings.IndexByte(query, '-')
	if dash <= 0 || dash == len(query)-1 {
		return false
	}
	for _, r := range query[:dash] {
		if r < 'A' || r > 'Z' {
			return false
		}
	}
	for _, r := range query[dash+1:] {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func quoteJQL(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	value = strings.ReplaceAll(value, `"`, `\"`)
	return `"` + value + `"`
}

func normalizedLimit(limit int) int {
	if limit <= 0 {
		return defaultPageSize
	}
	if limit > 200 {
		return 200
	}
	return limit
}

func cursorOffset(cursor string) int {
	cursor = strings.TrimSpace(cursor)
	if cursor == "" {
		return 0
	}
	value, err := strconv.Atoi(cursor)
	if err != nil || value < 0 {
		return 0
	}
	return value
}

func projectKey(issueKey string) string {
	left, _, ok := strings.Cut(issueKey, "-")
	if !ok {
		return ""
	}
	return left
}

func cleaned(values []string) []string {
	out := values[:0]
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			out = append(out, strings.TrimSpace(value))
		}
	}
	return out
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func (p Plugin) authzBaseURL(ctx context.Context) string {
	cfg := atlassian.NormalizeConfig(p.cfg)
	if cfg.BaseURL != "" {
		return cfg.BaseURL
	}
	if cfg.CloudID != "" {
		return atlassian.BaseURL(AtlassianProduct(), cfg.CloudID)
	}
	if stored, ok, err := p.store.LoadSecret(ctx, atlassian.OAuthSecretRef(Name, p.ref)); err == nil && ok {
		if cloudID := strings.TrimSpace(stored.Metadata["cloud_id"]); cloudID != "" {
			return atlassian.BaseURL(AtlassianProduct(), cloudID)
		}
	}
	return "https://api.atlassian.com"
}
