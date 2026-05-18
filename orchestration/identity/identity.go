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

// ExternalRequest describes the resolved actor available for plugin-specific
// account linking after inbound identity resolution.
type ExternalRequest struct {
	Actor user.Actor
}

// ExternalResult returns provider identities linked to a resolved canonical
// actor. It must not raise trust or change the canonical user.
type ExternalResult struct {
	Identities []user.Identity
}

// ExternalResolver enriches a resolved canonical actor with provider identities
// such as gitlab/main:<username>.
type ExternalResolver interface {
	ResolveExternalIdentities(context.Context, ExternalRequest) (ExternalResult, error)
}

// ExternalResolverFunc adapts a function into an ExternalResolver.
type ExternalResolverFunc func(context.Context, ExternalRequest) (ExternalResult, error)

// ResolveExternalIdentities calls f.
func (f ExternalResolverFunc) ResolveExternalIdentities(ctx context.Context, req ExternalRequest) (ExternalResult, error) {
	if f == nil {
		return ExternalResult{}, nil
	}
	return f(ctx, req)
}

// ChainExternalResolver calls every resolver and merges all successful
// identities. Resolver errors are ignored so account-linking failures do not
// block the user turn.
type ChainExternalResolver struct {
	Resolvers []ExternalResolver
}

// ResolveExternalIdentities resolves external identities through the chain.
func (c ChainExternalResolver) ResolveExternalIdentities(ctx context.Context, req ExternalRequest) (ExternalResult, error) {
	var out ExternalResult
	for _, resolver := range c.Resolvers {
		if resolver == nil {
			continue
		}
		result, err := resolver.ResolveExternalIdentities(ctx, req)
		if err != nil {
			continue
		}
		out.Identities = MergeIdentities(out.Identities, result.Identities...)
	}
	return out, nil
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

// ChainResolver tries provider-specific resolvers in order and returns the
// first resolved actor. If none resolves the identity, the last non-empty
// fallback result is returned.
type ChainResolver struct {
	Resolvers []Resolver
	Fallback  Resolver
}

// ResolveIdentity resolves identity through the configured chain.
func (c ChainResolver) ResolveIdentity(ctx context.Context, req Request) (Result, error) {
	fallback := c.Fallback
	if fallback == nil {
		fallback = DefaultResolver{}
	}
	best, err := fallback.ResolveIdentity(ctx, req)
	if err != nil {
		return Result{}, err
	}
	for _, resolver := range c.Resolvers {
		if resolver == nil {
			continue
		}
		result, err := resolver.ResolveIdentity(ctx, req)
		if err != nil {
			return Result{}, err
		}
		if result.Actor.Resolution == user.ResolutionResolved {
			return result, nil
		}
		if result.Actor.User.ID != "" {
			best = result
		}
	}
	return best, nil
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

// EnrichActor appends external identities to an actor and its user record.
func EnrichActor(ctx context.Context, actor user.Actor, resolver ExternalResolver) user.Actor {
	actor = NormalizeActorIdentities(actor)
	if resolver == nil {
		return actor
	}
	result, err := resolver.ResolveExternalIdentities(ctx, ExternalRequest{Actor: actor})
	if err != nil {
		return actor
	}
	actor.Identities = MergeIdentities(actor.Identities, result.Identities...)
	actor.User.Identities = MergeIdentities(actor.User.Identities, actor.Identities...)
	return actor
}

// NormalizeActorIdentities ensures Actor.Identities contains the entry identity
// and configured user identities without duplicates.
func NormalizeActorIdentities(actor user.Actor) user.Actor {
	actor.Identities = MergeIdentities(actor.Identities, actor.Identity)
	actor.Identities = MergeIdentities(actor.Identities, actor.User.Identities...)
	actor.User.Identities = MergeIdentities(actor.User.Identities, actor.Identities...)
	return actor
}

// MergeIdentities returns base plus non-empty identities that are unique by
// provider and provider id.
func MergeIdentities(base []user.Identity, identities ...user.Identity) []user.Identity {
	out := append([]user.Identity(nil), base...)
	for _, identity := range identities {
		if identity.Provider == "" && identity.ProviderID == "" {
			continue
		}
		var exists bool
		for _, existing := range out {
			if strings.EqualFold(strings.TrimSpace(existing.Provider), strings.TrimSpace(identity.Provider)) &&
				strings.TrimSpace(existing.ProviderID) == strings.TrimSpace(identity.ProviderID) {
				exists = true
				break
			}
		}
		if !exists {
			out = append(out, identity)
		}
	}
	return out
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
		Identities: []user.Identity{identity},
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
