package policy

import (
	"context"
	"path"
	"strings"
)

// Action names one protected action over a resource.
type Action string

const (
	ActionDatasourceRead   Action = "datasource.read"
	ActionDatasourceSearch Action = "datasource.search"
	ActionDatasourceIndex  Action = "datasource.index"
	ActionDatasourceAdmin  Action = "datasource.admin"

	ActionWorkspaceRead  Action = "workspace.read"
	ActionWorkspaceWrite Action = "workspace.write"
	ActionWorkspaceAdmin Action = "workspace.admin"

	ActionProcessExec  Action = "process.exec"
	ActionProcessAdmin Action = "process.admin"

	ActionNetworkFetch   Action = "network.fetch"
	ActionNetworkConnect Action = "network.connect"

	ActionConnectorUse    Action = "connector.use"
	ActionConnectorManage Action = "connector.manage"

	ActionChannelSend  Action = "channel.send"
	ActionChannelAdmin Action = "channel.admin"

	ActionTaskRead  Action = "task.read"
	ActionTaskWrite Action = "task.write"
	ActionTaskRun   Action = "task.run"
	ActionTaskAdmin Action = "task.admin"

	ActionSessionRead  Action = "session.read"
	ActionSessionWrite Action = "session.write"
	ActionSessionAdmin Action = "session.admin"

	ActionModelInvoke     Action = "model.invoke"
	ActionOperationInvoke Action = "operation.invoke"
	ActionApprovalGrant   Action = "approval.grant"
)

// SubjectKind classifies a policy subject.
type SubjectKind string

const (
	SubjectUser    SubjectKind = "user"
	SubjectGroup   SubjectKind = "group"
	SubjectService SubjectKind = "service"
	SubjectSystem  SubjectKind = "system"
	SubjectAgent   SubjectKind = "agent"
)

// SubjectRef identifies one policy subject.
type SubjectRef struct {
	Kind SubjectKind `json:"kind" yaml:"kind"`
	ID   string      `json:"id" yaml:"id"`
}

// ResourceKind classifies a protected resource.
type ResourceKind string

const (
	ResourceDatasource ResourceKind = "datasource"
	ResourceWorkspace  ResourceKind = "workspace"
	ResourcePath       ResourceKind = "path"
	ResourceProcess    ResourceKind = "process"
	ResourceNetwork    ResourceKind = "network"
	ResourceConnector  ResourceKind = "connector"
	ResourceChannel    ResourceKind = "channel"
	ResourceTask       ResourceKind = "task"
	ResourceSession    ResourceKind = "session"
	ResourceAdmin      ResourceKind = "admin"
	ResourceModel      ResourceKind = "model"
	ResourceOperation  ResourceKind = "operation"
)

// ResourceRef identifies one protected resource target.
type ResourceRef struct {
	Kind ResourceKind `json:"kind" yaml:"kind"`
	ID   string       `json:"id,omitempty" yaml:"id,omitempty"`
	Name string       `json:"name,omitempty" yaml:"name,omitempty"`
	Path string       `json:"path,omitempty" yaml:"path,omitempty"`
}

// Grant gives subjects actions over resources, optionally constrained by trust
// and invocation scopes.
type Grant struct {
	Subjects         []SubjectRef  `json:"subjects,omitempty" yaml:"subjects,omitempty"`
	Resources        []ResourceRef `json:"resources,omitempty" yaml:"resources,omitempty"`
	Actions          []Action      `json:"actions,omitempty" yaml:"actions,omitempty"`
	RequiredTrust    TrustLevel    `json:"required_trust,omitempty" yaml:"required_trust,omitempty"`
	RequiredScopes   []Scope       `json:"required_scopes,omitempty" yaml:"required_scopes,omitempty"`
	RequiresApproval bool          `json:"requires_approval,omitempty" yaml:"requires_approval,omitempty"`
}

// AuthorizationPolicy is an explicit grant list. Empty policy means no
// authorization policy is configured. Configured policies are default-deny.
type AuthorizationPolicy struct {
	Grants []Grant `json:"grants,omitempty" yaml:"grants,omitempty"`
}

// IsZero reports whether no authorization policy was configured.
func (p AuthorizationPolicy) IsZero() bool {
	return len(p.Grants) == 0
}

// AuthorizationRequest asks whether subjects may perform action on resource.
type AuthorizationRequest struct {
	Subjects []SubjectRef `json:"subjects,omitempty"`
	Trust    Trust        `json:"trust,omitempty"`
	Resource ResourceRef  `json:"resource"`
	Action   Action       `json:"action"`
}

// AuthorizationContext is the turn-local policy evaluation context.
type AuthorizationContext struct {
	Policy   AuthorizationPolicy `json:"policy,omitempty"`
	Subjects []SubjectRef        `json:"subjects,omitempty"`
	Trust    Trust               `json:"trust,omitempty"`
}

type authorizationContextKey struct{}

// ContextWithAuthorization stores authorization context on ctx.
func ContextWithAuthorization(ctx context.Context, auth AuthorizationContext) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, authorizationContextKey{}, auth)
}

// AuthorizationFromContext returns authorization context from ctx.
func AuthorizationFromContext(ctx context.Context) (AuthorizationContext, bool) {
	if ctx == nil {
		return AuthorizationContext{}, false
	}
	auth, ok := ctx.Value(authorizationContextKey{}).(AuthorizationContext)
	return auth, ok
}

// EvaluateAuthorization evaluates req against policy. Empty policy denies; use
// AuthorizationPolicy.IsZero before calling when no policy should be enforced.
func EvaluateAuthorization(policy AuthorizationPolicy, req AuthorizationRequest) Evaluation {
	if len(policy.Grants) == 0 {
		return Evaluation{Decision: DecisionDeny, Reason: "no_grants"}
	}
	for _, grant := range policy.Grants {
		if !grantMatchesSubjects(grant.Subjects, req.Subjects) {
			continue
		}
		if !grantMatchesAction(grant.Actions, req.Action) {
			continue
		}
		if !grantMatchesResource(grant.Resources, req.Resource) {
			continue
		}
		if !TrustSatisfies(req.Trust.Level, grant.RequiredTrust) {
			continue
		}
		if missing := missingScopes(grant.RequiredScopes, req.Trust.Scopes); len(missing) > 0 {
			return Evaluation{Decision: DecisionDeny, Reason: "missing_scopes", MissingScopes: missing}
		}
		if grant.RequiresApproval {
			return Evaluation{Decision: DecisionApprovalRequired, Reason: "approval_required"}
		}
		return Evaluation{Decision: DecisionAllow}
	}
	return Evaluation{Decision: DecisionDeny, Reason: "no_matching_grant"}
}

func grantMatchesSubjects(grantSubjects, actual []SubjectRef) bool {
	if len(grantSubjects) == 0 || len(actual) == 0 {
		return false
	}
	for _, grant := range grantSubjects {
		for _, subject := range actual {
			if grant.Kind == subject.Kind && wildcardMatch(grant.ID, subject.ID) {
				return true
			}
		}
	}
	return false
}

func grantMatchesAction(grantActions []Action, actual Action) bool {
	if len(grantActions) == 0 || actual == "" {
		return false
	}
	for _, grant := range grantActions {
		if actionMatches(string(grant), string(actual)) {
			return true
		}
	}
	return false
}

func grantMatchesResource(grantResources []ResourceRef, actual ResourceRef) bool {
	if len(grantResources) == 0 || actual.Kind == "" {
		return false
	}
	for _, grant := range grantResources {
		if grant.Kind != actual.Kind {
			continue
		}
		if resourceFieldMatches(grant.ID, actual.ID) &&
			resourceFieldMatches(grant.Name, actual.Name) &&
			pathFieldMatches(grant.Path, actual.Path) {
			return true
		}
	}
	return false
}

func actionMatches(pattern, actual string) bool {
	pattern = strings.TrimSpace(pattern)
	actual = strings.TrimSpace(actual)
	if pattern == "*" || pattern == actual {
		return true
	}
	if strings.HasSuffix(pattern, ".*") {
		return strings.HasPrefix(actual, strings.TrimSuffix(pattern, "*"))
	}
	return false
}

func resourceFieldMatches(pattern, actual string) bool {
	pattern = strings.TrimSpace(pattern)
	actual = strings.TrimSpace(actual)
	if pattern == "" {
		return true
	}
	return wildcardMatch(pattern, actual)
}

func pathFieldMatches(pattern, actualPath string) bool {
	pattern = strings.TrimSpace(pattern)
	actualPath = strings.TrimSpace(actualPath)
	if pattern == "" {
		return true
	}
	if pattern == "**" {
		return true
	}
	if strings.HasSuffix(pattern, "/**") {
		prefix := strings.TrimSuffix(pattern, "/**")
		return actualPath == prefix || strings.HasPrefix(actualPath, prefix+"/")
	}
	ok, err := path.Match(pattern, actualPath)
	if err == nil && ok {
		return true
	}
	return pattern == actualPath
}

func wildcardMatch(pattern, actual string) bool {
	pattern = strings.TrimSpace(pattern)
	actual = strings.TrimSpace(actual)
	return pattern == "*" || pattern == actual
}
