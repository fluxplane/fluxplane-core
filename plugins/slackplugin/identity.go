package slackplugin

import (
	"context"
	"strings"
	"sync"

	"github.com/fluxplane/agentruntime/core/user"
	"github.com/fluxplane/agentruntime/orchestration/identity"
	"github.com/slack-go/slack"
)

// IdentityResolverConfig configures Slack users.info based identity resolution.
type IdentityResolverConfig struct {
	ChannelName string
	BotToken    string
	AppToken    string
	API         *slack.Client
	Fallback    identity.Resolver
}

// NewIdentityResolver resolves slack_user principals to canonical users by
// reading their Slack profile email. Trust is not raised here; app identity
// overlays decide whether a resolved user or group carries higher trust.
func NewIdentityResolver(cfg IdentityResolverConfig) identity.Resolver {
	api := cfg.API
	if api == nil && strings.TrimSpace(cfg.BotToken) != "" {
		options := []slack.Option{}
		if strings.TrimSpace(cfg.AppToken) != "" {
			options = append(options, slack.OptionAppLevelToken(strings.TrimSpace(cfg.AppToken)))
		}
		api = slack.New(strings.TrimSpace(cfg.BotToken), options...)
	}
	if api == nil {
		return cfg.Fallback
	}
	fallback := cfg.Fallback
	if fallback == nil {
		fallback = identity.DefaultResolver{}
	}
	return &slackIdentityResolver{
		channelName: strings.TrimSpace(cfg.ChannelName),
		api:         api,
		fallback:    fallback,
		cache:       map[string]identity.Result{},
	}
}

type slackIdentityResolver struct {
	channelName string
	api         *slack.Client
	fallback    identity.Resolver
	mu          sync.RWMutex
	cache       map[string]identity.Result
}

func (r *slackIdentityResolver) ResolveIdentity(ctx context.Context, req identity.Request) (identity.Result, error) {
	base, err := r.fallback.ResolveIdentity(ctx, req)
	if err != nil {
		return identity.Result{}, err
	}
	if !r.matches(req) {
		return base, nil
	}
	slackID := strings.TrimSpace(req.Inbound.Caller.Principal.ID)
	if slackID == "" {
		return base, nil
	}
	if cached, ok := r.cached(slackID); ok {
		cached.Caller = base.Caller
		cached.Trust = base.Trust
		return cached, nil
	}
	slackUser, err := r.api.GetUserInfoContext(ctx, slackID)
	if err != nil || slackUser == nil || slackUser.Deleted {
		return base, nil
	}
	email := strings.ToLower(strings.TrimSpace(slackUser.Profile.Email))
	if email == "" {
		return base, nil
	}
	displayName := firstNonEmptySlack(slackUser.Profile.DisplayName, slackUser.Profile.RealName, slackUser.RealName, slackUser.Name, email)
	claims := slackClaims(slackUser)
	actor := user.Actor{
		User: user.User{
			ID:          user.ID(email),
			Username:    email,
			DisplayName: displayName,
			Trust:       base.Actor.Trust,
			Identities: []user.Identity{{
				Provider:    "slack",
				ProviderID:  slackID,
				Email:       email,
				DisplayName: displayName,
				Claims:      claims,
			}},
		},
		Identity: user.Identity{
			Provider:    "slack",
			ProviderID:  slackID,
			Email:       email,
			DisplayName: displayName,
			Claims:      claims,
		},
		Trust:      base.Actor.Trust,
		Resolution: user.ResolutionResolved,
	}
	result := identity.Result{Actor: actor, Caller: base.Caller, Trust: base.Trust}
	r.store(slackID, result)
	return result, nil
}

func (r *slackIdentityResolver) matches(req identity.Request) bool {
	if principalKind := strings.TrimSpace(req.Inbound.Caller.Principal.Kind); principalKind != "slack_user" && principalKind != "slack" {
		return false
	}
	if r.channelName == "" {
		return true
	}
	return strings.TrimSpace(req.Inbound.Caller.Source) == "slack:"+r.channelName
}

func (r *slackIdentityResolver) cached(slackID string) (identity.Result, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	result, ok := r.cache[slackID]
	return result, ok
}

func (r *slackIdentityResolver) store(slackID string, result identity.Result) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.cache[slackID] = result
}

func slackClaims(slackUser *slack.User) map[string]string {
	claims := map[string]string{
		"team_id": slackUser.TeamID,
	}
	addBoolClaim(claims, "is_admin", slackUser.IsAdmin)
	addBoolClaim(claims, "is_owner", slackUser.IsOwner)
	addBoolClaim(claims, "is_primary_owner", slackUser.IsPrimaryOwner)
	addBoolClaim(claims, "is_restricted", slackUser.IsRestricted)
	addBoolClaim(claims, "is_ultra_restricted", slackUser.IsUltraRestricted)
	addBoolClaim(claims, "is_bot", slackUser.IsBot)
	for key, value := range claims {
		if strings.TrimSpace(value) == "" {
			delete(claims, key)
		}
	}
	return claims
}

func addBoolClaim(claims map[string]string, key string, value bool) {
	if value {
		claims[key] = "true"
	}
}

func firstNonEmptySlack(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

var _ identity.Resolver = (*slackIdentityResolver)(nil)
