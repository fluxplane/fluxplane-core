package policy

import (
	base "github.com/fluxplane/fluxplane-policy"
	"github.com/fluxplane/fluxplane-policy/policyauth"
)

// Action names one protected action over a resource.
type Action = base.Action

const (
	ActionDatasourceRead   = base.ActionDatasourceRead
	ActionDatasourceSearch = base.ActionDatasourceSearch
	ActionDatasourceIndex  = base.ActionDatasourceIndex
	ActionDatasourceAdmin  = base.ActionDatasourceAdmin

	ActionWorkspaceRead  = base.ActionWorkspaceRead
	ActionWorkspaceWrite = base.ActionWorkspaceWrite
	ActionWorkspaceAdmin = base.ActionWorkspaceAdmin

	ActionProcessExec  = base.ActionProcessExec
	ActionProcessAdmin = base.ActionProcessAdmin

	ActionNetworkFetch   = base.ActionNetworkFetch
	ActionNetworkConnect = base.ActionNetworkConnect

	ActionChannelSend  = base.ActionChannelSend
	ActionChannelAdmin = base.ActionChannelAdmin

	ActionTaskRead  = base.ActionTaskRead
	ActionTaskWrite = base.ActionTaskWrite
	ActionTaskRun   = base.ActionTaskRun
	ActionTaskAdmin = base.ActionTaskAdmin

	ActionSessionRead  = base.ActionSessionRead
	ActionSessionWrite = base.ActionSessionWrite
	ActionSessionAdmin = base.ActionSessionAdmin

	ActionModelInvoke     = base.ActionModelInvoke
	ActionOperationInvoke = base.ActionOperationInvoke
	ActionApprovalGrant   = base.ActionApprovalGrant

	ActionSecretRead  = base.ActionSecretRead
	ActionSecretUse   = base.ActionSecretUse
	ActionSecretAdmin = base.ActionSecretAdmin
)

// Actions returns the stable authorization action vocabulary.
var Actions = base.Actions

// SubjectKind classifies a policy subject.
type SubjectKind = base.SubjectKind

const (
	SubjectUser    = base.SubjectUser
	SubjectGroup   = base.SubjectGroup
	SubjectService = base.SubjectService
	SubjectSystem  = base.SubjectSystem
	SubjectAgent   = base.SubjectAgent
)

// SubjectKinds returns the stable authorization subject vocabulary.
var SubjectKinds = base.SubjectKinds

// SubjectRef identifies one policy subject.
type SubjectRef = base.SubjectRef

// ResourceKind classifies a protected resource.
type ResourceKind = base.ResourceKind

const (
	ResourceDatasource = base.ResourceDatasource
	ResourceWorkspace  = base.ResourceWorkspace
	ResourcePath       = base.ResourcePath
	ResourceProcess    = base.ResourceProcess
	ResourceNetwork    = base.ResourceNetwork
	ResourceChannel    = base.ResourceChannel
	ResourceTask       = base.ResourceTask
	ResourceSession    = base.ResourceSession
	ResourceAdmin      = base.ResourceAdmin
	ResourceModel      = base.ResourceModel
	ResourceOperation  = base.ResourceOperation
	ResourceSecret     = base.ResourceSecret
)

// ResourceKinds returns the stable authorization resource vocabulary.
var ResourceKinds = base.ResourceKinds

// ResourceRef identifies one protected resource target.
type ResourceRef = base.ResourceRef

// Grant gives subjects actions over resources, optionally constrained by trust
// and invocation scopes.
type Grant = base.Grant

// AuthorizationPolicy is an explicit grant list. Empty policy means no
// authorization policy is configured. Configured policies are default-deny.
type AuthorizationPolicy = base.AuthorizationPolicy

// AuthorizationRequest asks whether subjects may perform action on resource.
type AuthorizationRequest = base.AuthorizationRequest

// AuthorizationContext is the turn-local policy evaluation context.
type AuthorizationContext = policyauth.AuthorizationContext

// ContextWithAuthorization stores authorization context on ctx.
var ContextWithAuthorization = policyauth.ContextWithAuthorization

// AuthorizationFromContext returns authorization context from ctx.
var AuthorizationFromContext = policyauth.AuthorizationFromContext

// EvaluateAuthorization evaluates req against policy. Empty policy denies; use
// AuthorizationPolicy.IsZero before calling when no policy should be enforced.
var EvaluateAuthorization = base.EvaluateAuthorization
