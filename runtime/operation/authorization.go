package operationruntime

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/fluxplane/fluxplane-core/core/event"
	"github.com/fluxplane/fluxplane-core/core/operation"
	"github.com/fluxplane/fluxplane-core/core/policy"
)

// AuthorizationGate enforces the policy.AuthorizationContext carried on the
// operation context. When no authorization policy is configured, it preserves
// existing behavior.
type AuthorizationGate struct{}

// AuthorizationApprovalRequired reports that authorization matched an
// approval-required grant. The safety envelope is responsible for routing this
// to the configured approval gate.
type AuthorizationApprovalRequired struct {
	Subjects []policy.SubjectRef
	Resource policy.ResourceRef
	Action   policy.Action
	Reason   string
}

// Error implements error.
func (e AuthorizationApprovalRequired) Error() string {
	reason := strings.TrimSpace(e.Reason)
	if reason == "" {
		reason = "approval_required"
	}
	return fmt.Sprintf("%s: %s %s", reason, e.Action, resourceLabel(e.Resource))
}

// Authorize implements AccessController.
func (AuthorizationGate) Authorize(ctx operation.Context, op operation.Operation, input operation.Value) error {
	auth, ok := policy.AuthorizationFromContext(ctx)
	if !ok || auth.Policy.IsZero() {
		return nil
	}
	targets, err := authorizationTargets(ctx, op, input)
	if err != nil {
		return fmt.Errorf("access_descriptor_failed: %w", err)
	}
	for _, target := range targets {
		req := policy.AuthorizationRequest{
			Subjects: auth.Subjects,
			Trust:    auth.Trust,
			Resource: target.Resource,
			Action:   target.Action,
		}
		evaluation := policy.EvaluateAuthorization(auth.Policy, req)
		if evaluation.Decision == policy.DecisionAllow {
			event.EmitAuthorizationDecision(ctx, auth, req, evaluation)
			continue
		}
		if evaluation.Decision == policy.DecisionApprovalRequired {
			event.EmitAuthorizationDecision(ctx, auth, req, evaluation)
			return AuthorizationApprovalRequired{Subjects: auth.Subjects, Resource: target.Resource, Action: target.Action, Reason: evaluation.Reason}
		}
		if target.Resource.Kind == policy.ResourceDatasource && target.Resource.Name == "*" {
			fallback := authorizeAnyDatasource(auth, target.Action)
			event.EmitAuthorizationDecision(ctx, auth, req, fallback)
			switch fallback.Decision {
			case policy.DecisionAllow:
				continue
			case policy.DecisionApprovalRequired:
				return AuthorizationApprovalRequired{Subjects: auth.Subjects, Resource: target.Resource, Action: target.Action, Reason: fallback.Reason}
			}
		}
		event.EmitAuthorizationDecision(ctx, auth, req, evaluation)
		return fmt.Errorf("%s: %s %s", evaluation.Reason, target.Action, resourceLabel(target.Resource))
	}
	return nil
}

func authorizeAnyDatasource(auth policy.AuthorizationContext, action policy.Action) policy.Evaluation {
	approval := policy.Evaluation{}
	for _, grant := range auth.Policy.Grants {
		for _, resource := range grant.Resources {
			if resource.Kind != policy.ResourceDatasource {
				continue
			}
			evaluation := policy.EvaluateAuthorization(auth.Policy, policy.AuthorizationRequest{
				Subjects: auth.Subjects,
				Trust:    auth.Trust,
				Resource: resource,
				Action:   action,
			})
			if evaluation.Decision == policy.DecisionAllow {
				return evaluation
			}
			if evaluation.Decision == policy.DecisionApprovalRequired {
				approval = evaluation
			}
		}
	}
	if approval.Decision != "" {
		return approval
	}
	return policy.Evaluation{Decision: policy.DecisionDeny, Reason: "no_matching_grant"}
}

type authorizationTarget struct {
	Resource policy.ResourceRef
	Action   policy.Action
}

func authorizationTargets(ctx operation.Context, op operation.Operation, input operation.Value) ([]authorizationTarget, error) {
	if descriptors, ok, err := AccessFor(ctx, op, input); ok {
		if err != nil {
			return nil, err
		}
		return descriptorTargets(descriptors), nil
	}
	spec := op.Spec()
	if targets := datasourceTargets(spec, input); len(targets) > 0 {
		return targets, nil
	}
	if targets := namedOperationTargets(spec, input); len(targets) > 0 {
		return targets, nil
	}
	if intents, ok, err := operation.IntentFor(ctx, op, input); err == nil && ok && !intents.Empty() {
		return intentTargets(intents), nil
	} else if err != nil {
		return nil, err
	}
	return []authorizationTarget{{
		Resource: policy.ResourceRef{Kind: policy.ResourceOperation, Name: string(spec.Ref.Name)},
		Action:   policy.ActionOperationInvoke,
	}}, nil
}

func descriptorTargets(descriptors []AccessDescriptor) []authorizationTarget {
	out := make([]authorizationTarget, 0, len(descriptors))
	for _, descriptor := range descriptors {
		out = append(out, authorizationTarget{
			Resource: descriptor.Resource,
			Action:   descriptor.Action,
		})
	}
	return out
}

func datasourceTargets(spec operation.Spec, input operation.Value) []authorizationTarget {
	name := stringField(input, "datasource")
	switch spec.Ref.Name {
	case "datasource_search":
		return []authorizationTarget{{Resource: policy.ResourceRef{Kind: policy.ResourceDatasource, Name: wildcardName(name)}, Action: policy.ActionDatasourceSearch}}
	case "datasource_get", "datasource_batch_get", "datasource_relation":
		return []authorizationTarget{{Resource: policy.ResourceRef{Kind: policy.ResourceDatasource, Name: wildcardName(name)}, Action: policy.ActionDatasourceRead}}
	default:
		return nil
	}
}

func namedOperationTargets(spec operation.Spec, input operation.Value) []authorizationTarget {
	name := string(spec.Ref.Name)
	switch {
	case name == "channel_send":
		return []authorizationTarget{{Resource: policy.ResourceRef{Kind: policy.ResourceChannel, Name: wildcardName(stringField(input, "channel"))}, Action: policy.ActionChannelSend}}
	case strings.HasPrefix(name, "task_"):
		action := policy.ActionTaskRead
		if strings.Contains(name, "run") {
			action = policy.ActionTaskRun
		} else if strings.Contains(name, "create") || strings.Contains(name, "modify") || strings.Contains(name, "set") || strings.Contains(name, "validate") {
			action = policy.ActionTaskWrite
		}
		return []authorizationTarget{{Resource: policy.ResourceRef{Kind: policy.ResourceTask, Name: wildcardName(stringField(input, "id"))}, Action: action}}
	default:
		return nil
	}
}

func intentTargets(intents operation.IntentSet) []authorizationTarget {
	var out []authorizationTarget
	for _, intent := range intents.Operations {
		switch intent.Behavior {
		case operation.IntentFilesystemRead:
			out = append(out, authorizationTarget{Resource: pathResource(intent.Target), Action: policy.ActionWorkspaceRead})
		case operation.IntentFilesystemWrite, operation.IntentPersistenceModify:
			out = append(out, authorizationTarget{Resource: pathResource(intent.Target), Action: policy.ActionWorkspaceWrite})
		case operation.IntentCommandExecution:
			out = append(out, authorizationTarget{Resource: processResource(intent.Target), Action: policy.ActionProcessExec})
		case operation.IntentNetworkFetch, operation.IntentBrowserAccess:
			out = append(out, authorizationTarget{Resource: networkResource(intent.Target), Action: policy.ActionNetworkFetch})
		case operation.IntentNetworkWrite:
			out = append(out, authorizationTarget{Resource: networkResource(intent.Target), Action: policy.ActionNetworkConnect})
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func pathResource(target operation.IntentTarget) policy.ResourceRef {
	if typed, ok := target.(operation.PathTarget); ok {
		return policy.ResourceRef{Kind: policy.ResourcePath, Path: string(typed.Path)}
	}
	return policy.ResourceRef{Kind: policy.ResourcePath, Path: "**"}
}

func processResource(target operation.IntentTarget) policy.ResourceRef {
	if typed, ok := target.(operation.ProcessTarget); ok {
		return policy.ResourceRef{Kind: policy.ResourceProcess, Name: string(typed.Command)}
	}
	return policy.ResourceRef{Kind: policy.ResourceProcess, Name: "*"}
}

func networkResource(target operation.IntentTarget) policy.ResourceRef {
	switch typed := target.(type) {
	case operation.URLTarget:
		return policy.ResourceRef{Kind: policy.ResourceNetwork, Name: string(typed.URL)}
	case operation.BrowserTarget:
		return policy.ResourceRef{Kind: policy.ResourceNetwork, Name: string(typed.URL)}
	default:
		return policy.ResourceRef{Kind: policy.ResourceNetwork, Name: "*"}
	}
}

func wildcardName(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "*"
	}
	return value
}

func stringField(input operation.Value, name string) string {
	if input == nil {
		return ""
	}
	var values map[string]any
	data, err := json.Marshal(input)
	if err != nil {
		return ""
	}
	if err := json.Unmarshal(data, &values); err != nil {
		return ""
	}
	value, ok := values[name]
	if !ok {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(value))
}

func resourceLabel(resource policy.ResourceRef) string {
	switch {
	case resource.Path != "":
		return string(resource.Kind) + ":" + resource.Path
	case resource.Name != "":
		return string(resource.Kind) + ":" + resource.Name
	case resource.ID != "":
		return string(resource.Kind) + ":" + resource.ID
	default:
		return string(resource.Kind)
	}
}
