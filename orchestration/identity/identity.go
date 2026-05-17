package identity

import (
	"context"
	"fmt"
	"strings"

	"github.com/fluxplane/agentruntime/core/channel"
	"github.com/fluxplane/agentruntime/core/policy"
	"github.com/fluxplane/agentruntime/core/user"
)

// Request describes the evidence available when resolving an inbound actor.
type Request struct {
	Inbound channel.Inbound
}

// Result is the canonical actor and invocation authority used by the session.
type Result struct {
	Actor  user.Actor
	Caller policy.Caller
	Trust  policy.Trust
}

// Resolver maps transport/channel identity evidence to a canonical user actor.
type Resolver interface {
	ResolveIdentity(context.Context, Request) (Result, error)
}

// ResolverFunc adapts a function into a Resolver.
type ResolverFunc func(context.Context, Request) (Result, error)

// ResolveIdentity calls f.
func (f ResolverFunc) ResolveIdentity(ctx context.Context, req Request) (Result, error) {
	if f == nil {
		return DefaultResolver{}.ResolveIdentity(ctx, req)
	}
	return f(ctx, req)
}

// DefaultResolver preserves existing caller/trust authority while attaching a
// conservative canonical actor.
type DefaultResolver struct{}

// ResolveIdentity returns a canonical actor derived from the inbound caller.
func (DefaultResolver) ResolveIdentity(_ context.Context, req Request) (Result, error) {
	inbound := req.Inbound
	caller := inbound.Caller
	trust := normalizeInvocationTrust(inbound.Trust)
	if caller.Kind == "" {
		caller.Kind = policy.CallerUser
	}
	actor := actorFromCaller(caller, trust)
	return Result{Actor: actor, Caller: caller, Trust: trust}, nil
}

func normalizeInvocationTrust(trust policy.Trust) policy.Trust {
	if trust.Kind == "" {
		trust.Kind = policy.TrustInvocation
	}
	if trust.Level == "" {
		trust.Level = policy.TrustUntrusted
	}
	return trust
}

func actorFromCaller(caller policy.Caller, trust policy.Trust) user.Actor {
	principal := caller.Principal
	id := strings.TrimSpace(principal.ID)
	principalKind := strings.TrimSpace(principal.Kind)
	identity := user.Identity{
		Provider:    principalKind,
		ProviderID:  id,
		DisplayName: strings.TrimSpace(principal.Name),
	}
	if identity.Provider == "" {
		identity.Provider = strings.TrimSpace(caller.Source)
	}
	if identity.Provider == "" {
		identity.Provider = string(caller.Kind)
	}
	canonicalID := id
	if principal.Kind != "user" && id != "" {
		canonicalID = fmt.Sprintf("%s:%s", identity.Provider, id)
	}
	if canonicalID == "" {
		canonicalID = string(caller.Kind)
	}
	actorTrust := userTrustFromPolicy(trust.Level)
	resolution := user.ResolutionUnresolved
	if caller.Kind != policy.CallerUser || (principalKind == "user" && id != "") {
		resolution = user.ResolutionResolved
	}
	return user.Actor{
		User: user.User{
			ID:          user.ID(canonicalID),
			Username:    canonicalID,
			DisplayName: firstNonEmpty(principal.Name, canonicalID),
			Trust:       actorTrust,
			Identities:  []user.Identity{identity},
		},
		Identity:   identity,
		Trust:      actorTrust,
		Resolution: resolution,
	}
}

func userTrustFromPolicy(level policy.TrustLevel) user.TrustLevel {
	switch level {
	case policy.TrustPrivileged, policy.TrustSystem:
		return user.TrustOperator
	case policy.TrustVerified:
		return user.TrustInternal
	default:
		return user.TrustPublic
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
