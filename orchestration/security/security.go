package security

import (
	"context"
	"strings"

	"github.com/fluxplane/agentruntime/core/agent"
	"github.com/fluxplane/agentruntime/core/channel"
	"github.com/fluxplane/agentruntime/core/policy"
	"github.com/fluxplane/agentruntime/core/user"
)

// ContextForInbound builds the effective authorization context for one inbound
// turn.
func ContextForInbound(ctx context.Context, configured policy.AuthorizationPolicy, inbound channel.Inbound, agentSpec agent.Spec, traceAllows bool) context.Context {
	if configured.IsZero() {
		return ctx
	}
	return policy.ContextWithAuthorization(ctx, policy.AuthorizationContext{
		Policy:      configured,
		Subjects:    SubjectsForInbound(inbound, agentSpec),
		Trust:       inbound.Trust,
		TraceAllows: traceAllows,
	})
}

// SubjectsForInbound returns canonical policy subjects derived from inbound
// identity and the current agent.
func SubjectsForInbound(inbound channel.Inbound, agentSpec agent.Spec) []policy.SubjectRef {
	seen := map[policy.SubjectRef]struct{}{}
	var out []policy.SubjectRef
	add := func(kind policy.SubjectKind, id string) {
		id = strings.TrimSpace(id)
		if id == "" {
			return
		}
		subject := policy.SubjectRef{Kind: kind, ID: id}
		if _, ok := seen[subject]; ok {
			return
		}
		seen[subject] = struct{}{}
		out = append(out, subject)
	}
	if inbound.Actor != nil {
		add(policy.SubjectUser, string(inbound.Actor.User.ID))
		for _, groupID := range inbound.Actor.User.Groups {
			add(policy.SubjectGroup, string(groupID))
		}
		for _, group := range inbound.Actor.Groups {
			add(policy.SubjectGroup, string(group.ID))
		}
	}
	addSubjectForCaller(add, inbound.Caller)
	if agentSpec.Name != "" {
		add(policy.SubjectAgent, string(agentSpec.Name))
	}
	return out
}

func addSubjectForCaller(add func(policy.SubjectKind, string), caller policy.Caller) {
	id := strings.TrimSpace(caller.Principal.ID)
	switch caller.Kind {
	case policy.CallerAgent:
		add(policy.SubjectAgent, id)
	case policy.CallerSystem:
		add(policy.SubjectSystem, id)
	default:
		if caller.Principal.Kind == "service" {
			add(policy.SubjectService, id)
			return
		}
		add(policy.SubjectUser, id)
	}
}

// LocalActor is the canonical local actor used by local distributions such as
// coder.
func LocalActor(username, rawID, hostname, uid string) user.Actor {
	username = strings.TrimSpace(username)
	if username == "" {
		username = "local"
	}
	canonical := username
	if !strings.Contains(canonical, "@") {
		canonical += "@localhost"
	}
	claims := map[string]string{}
	if hostname != "" {
		claims["hostname"] = hostname
	}
	if uid != "" {
		claims["uid"] = uid
	}
	return user.Actor{
		User: user.User{
			ID:       user.ID(canonical),
			Username: canonical,
			Groups:   []user.ID{"local_users", "local_operators"},
			Trust:    user.TrustOperator,
		},
		Identity: user.Identity{
			Provider:   "local",
			ProviderID: firstNonEmpty(rawID, username),
			Claims:     claims,
		},
		Groups: []user.Group{
			{ID: "local_users"},
			{ID: "local_operators", Trust: user.TrustOperator},
		},
		Trust: user.TrustOperator,
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
