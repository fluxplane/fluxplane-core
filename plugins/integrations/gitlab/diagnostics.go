package gitlab

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/fluxplane/agentruntime/core/resource"
	coresecret "github.com/fluxplane/agentruntime/core/secret"
	runtimesecret "github.com/fluxplane/agentruntime/runtime/secret"
	"github.com/fluxplane/agentruntime/runtime/system"
	gitlab "gitlab.com/gitlab-org/api/client-go/v2"
	"golang.org/x/oauth2"
)

// AccessCheckRequest configures a GitLab datasource access diagnostic.
type AccessCheckRequest struct {
	MergeRequest string `json:"merge_request,omitempty"`
}

// AccessCheckResult contains redacted GitLab access diagnostics.
type AccessCheckResult struct {
	BaseURL      string            `json:"base_url"`
	Auth         AuthCheck         `json:"auth"`
	User         UserCheck         `json:"user"`
	Token        TokenCheck        `json:"token"`
	MergeRequest MergeRequestCheck `json:"merge_request,omitempty"`
}

// AuthCheck reports which configured auth method supplied credentials.
type AuthCheck struct {
	Method string `json:"method,omitempty"`
	Kind   string `json:"kind,omitempty"`
	Env    string `json:"env,omitempty"`
}

// APICheck reports one GitLab API call status without exposing credential data.
type APICheck struct {
	OK     bool   `json:"ok"`
	Status int    `json:"status,omitempty"`
	Error  string `json:"error,omitempty"`
}

// UserCheck reports the authenticated GitLab user.
type UserCheck struct {
	APICheck
	ID       int64  `json:"id,omitempty"`
	Username string `json:"username,omitempty"`
	Name     string `json:"name,omitempty"`
	State    string `json:"state,omitempty"`
	IsAdmin  bool   `json:"is_admin"`
}

// TokenCheck reports metadata for the token used by the request.
type TokenCheck struct {
	APICheck
	ID        int64    `json:"id,omitempty"`
	Name      string   `json:"name,omitempty"`
	Active    bool     `json:"active,omitempty"`
	Revoked   bool     `json:"revoked,omitempty"`
	Scopes    []string `json:"scopes,omitempty"`
	ExpiresAt string   `json:"expires_at,omitempty"`
}

// MergeRequestCheck reports project, MR, and diff access for one MR reference.
type MergeRequestCheck struct {
	Requested  string        `json:"requested,omitempty"`
	ProjectRef string        `json:"project_ref,omitempty"`
	IID        int64         `json:"iid,omitempty"`
	RecordID   string        `json:"record_id,omitempty"`
	Project    ProjectCheck  `json:"project,omitempty"`
	Lookup     MRLookupCheck `json:"lookup,omitempty"`
	Diffs      DiffCheck     `json:"diffs,omitempty"`
	Error      string        `json:"error,omitempty"`
}

// ProjectCheck reports project lookup status.
type ProjectCheck struct {
	APICheck
	ID                int64  `json:"id,omitempty"`
	PathWithNamespace string `json:"path_with_namespace,omitempty"`
	WebURL            string `json:"web_url,omitempty"`
}

// MRLookupCheck reports MR lookup status.
type MRLookupCheck struct {
	APICheck
	ID        int64  `json:"id,omitempty"`
	ProjectID int64  `json:"project_id,omitempty"`
	Title     string `json:"title,omitempty"`
	State     string `json:"state,omitempty"`
	WebURL    string `json:"web_url,omitempty"`
}

// DiffCheck reports MR diff API status and per-file diff flags.
type DiffCheck struct {
	APICheck
	Count     int             `json:"count,omitempty"`
	Collapsed int             `json:"collapsed,omitempty"`
	TooLarge  int             `json:"too_large,omitempty"`
	Files     []DiffFileCheck `json:"files,omitempty"`
}

// DiffFileCheck reports one changed file without including the diff body.
type DiffFileCheck struct {
	Path      string `json:"path,omitempty"`
	OldPath   string `json:"old_path,omitempty"`
	NewPath   string `json:"new_path,omitempty"`
	HasDiff   bool   `json:"has_diff"`
	Collapsed bool   `json:"collapsed,omitempty"`
	TooLarge  bool   `json:"too_large,omitempty"`
}

// CheckAccess checks GitLab auth, user, token metadata, and optional MR diff access.
func CheckAccess(ctx context.Context, sys system.System, ref resource.PluginRef, cfg Config, req AccessCheckRequest) (AccessCheckResult, error) {
	cfg = normalizeConfig(cfg)
	auth, client, err := diagnosticClient(ctx, sys, ref, cfg)
	if err != nil {
		return AccessCheckResult{}, err
	}
	out := AccessCheckResult{
		BaseURL: cfg.baseURL(),
		Auth: AuthCheck{
			Method: auth.Method.Name,
			Kind:   string(auth.Material.Kind),
			Env:    auth.Method.Env.Name,
		},
	}
	out.User = checkCurrentUser(ctx, client)
	out.Token = checkCurrentToken(ctx, client)
	if strings.TrimSpace(req.MergeRequest) != "" {
		out.MergeRequest = checkMergeRequest(ctx, client, req.MergeRequest)
	}
	return out, nil
}

func diagnosticClient(ctx context.Context, sys system.System, ref resource.PluginRef, cfg Config) (runtimesecret.Resolution, *gitlab.Client, error) {
	if sys == nil {
		return runtimesecret.Resolution{}, nil, fmt.Errorf("gitlabplugin: system is nil")
	}
	auth, err := authFromSecrets(ctx, sys, ref, cfg)
	if err != nil {
		return runtimesecret.Resolution{}, nil, err
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
		return runtimesecret.Resolution{}, nil, fmt.Errorf("gitlabplugin: unsupported auth material kind %q", auth.Material.Kind)
	}
	if err != nil {
		return runtimesecret.Resolution{}, nil, err
	}
	return auth, client, nil
}

func checkCurrentUser(ctx context.Context, client *gitlab.Client) UserCheck {
	user, resp, err := client.Users.CurrentUser(gitlab.WithContext(ctx))
	check := apiCheck(resp, err)
	out := UserCheck{APICheck: check}
	if err != nil || user == nil {
		return out
	}
	out.ID = user.ID
	out.Username = user.Username
	out.Name = user.Name
	out.State = user.State
	out.IsAdmin = user.IsAdmin
	return out
}

func checkCurrentToken(ctx context.Context, client *gitlab.Client) TokenCheck {
	token, resp, err := client.PersonalAccessTokens.GetSinglePersonalAccessToken(gitlab.WithContext(ctx))
	check := apiCheck(resp, err)
	out := TokenCheck{APICheck: check}
	if err != nil || token == nil {
		return out
	}
	out.ID = token.ID
	out.Name = token.Name
	out.Active = token.Active
	out.Revoked = token.Revoked
	out.Scopes = append([]string(nil), token.Scopes...)
	if token.ExpiresAt != nil {
		out.ExpiresAt = token.ExpiresAt.String()
	}
	return out
}

func checkMergeRequest(ctx context.Context, client *gitlab.Client, raw string) MergeRequestCheck {
	projectRef, iid, recordID, err := parseMergeRequestRef(raw)
	out := MergeRequestCheck{
		Requested:  strings.TrimSpace(raw),
		ProjectRef: projectRef,
		IID:        iid,
		RecordID:   recordID,
	}
	if err != nil {
		out.Error = err.Error()
		return out
	}
	project, resp, err := client.Projects.GetProject(projectIDForAPI(projectRef), nil, gitlab.WithContext(ctx))
	out.Project = ProjectCheck{APICheck: apiCheck(resp, err)}
	if err == nil && project != nil {
		out.Project.ID = int64(project.ID)
		out.Project.PathWithNamespace = project.PathWithNamespace
		out.Project.WebURL = project.WebURL
	}
	mr, resp, err := client.MergeRequests.GetMergeRequest(projectIDForAPI(projectRef), iid, nil, gitlab.WithContext(ctx))
	out.Lookup = MRLookupCheck{APICheck: apiCheck(resp, err)}
	if err == nil && mr != nil {
		out.Lookup.ID = mr.ID
		out.Lookup.ProjectID = mr.ProjectID
		out.Lookup.Title = mr.Title
		out.Lookup.State = mr.State
		out.Lookup.WebURL = mr.WebURL
	}
	diffs, resp, err := client.MergeRequests.ListMergeRequestDiffs(projectIDForAPI(projectRef), iid, &gitlab.ListMergeRequestDiffsOptions{
		ListOptions: gitlab.ListOptions{PerPage: 100},
	}, gitlab.WithContext(ctx))
	out.Diffs = DiffCheck{APICheck: apiCheck(resp, err)}
	if err == nil {
		out.Diffs.Count = len(diffs)
		for _, diff := range diffs {
			if diff == nil {
				continue
			}
			file := DiffFileCheck{
				Path:      firstNonEmpty(diff.NewPath, diff.OldPath),
				OldPath:   diff.OldPath,
				NewPath:   diff.NewPath,
				HasDiff:   strings.TrimSpace(diff.Diff) != "",
				Collapsed: diff.Collapsed,
				TooLarge:  diff.TooLarge,
			}
			if diff.Collapsed {
				out.Diffs.Collapsed++
			}
			if diff.TooLarge {
				out.Diffs.TooLarge++
			}
			out.Diffs.Files = append(out.Diffs.Files, file)
		}
	}
	return out
}

func parseMergeRequestRef(raw string) (string, int64, string, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return "", 0, "", fmt.Errorf("merge request ref is empty")
	}
	if parsed, err := url.Parse(value); err == nil && parsed.Scheme != "" && parsed.Host != "" {
		parts := strings.Split(strings.Trim(parsed.Path, "/"), "/")
		for i := 0; i+2 < len(parts); i++ {
			if parts[i] != "-" || parts[i+1] != "merge_requests" {
				continue
			}
			if i == 0 {
				return "", 0, "", fmt.Errorf("merge request URL is missing a project path")
			}
			iid, err := strconv.ParseInt(parts[i+2], 10, 64)
			if err != nil || iid <= 0 {
				return "", 0, "", fmt.Errorf("merge request URL has invalid iid %q", parts[i+2])
			}
			project := strings.Join(parts[:i], "/")
			return project, iid, mergeRequestID(project, iid), nil
		}
		return "", 0, "", fmt.Errorf("merge request URL must contain /-/merge_requests/{iid}")
	}
	project, iidRaw, ok := strings.Cut(value, "!")
	if !ok {
		return "", 0, "", fmt.Errorf("merge request ref must be project!iid or a GitLab MR URL")
	}
	project = strings.TrimSpace(project)
	if project == "" {
		return "", 0, "", fmt.Errorf("merge request ref %q is missing the project path or id before the separator", value)
	}
	iid, err := strconv.ParseInt(strings.TrimSpace(iidRaw), 10, 64)
	if err != nil || iid <= 0 {
		return "", 0, "", fmt.Errorf("merge request ref %q has invalid iid", value)
	}
	return project, iid, mergeRequestID(project, iid), nil
}

func projectIDForAPI(project string) any {
	if id, err := strconv.ParseInt(project, 10, 64); err == nil {
		return id
	}
	return project
}

func apiCheck(resp *gitlab.Response, err error) APICheck {
	status := 0
	if resp != nil && resp.Response != nil {
		status = resp.StatusCode
	}
	if err == nil {
		return APICheck{OK: true, Status: status}
	}
	var gitlabErr *gitlab.ErrorResponse
	if errors.As(err, &gitlabErr) && gitlabErr.Response != nil {
		status = gitlabErr.Response.StatusCode
	}
	return APICheck{OK: false, Status: status, Error: err.Error()}
}
