package identity

import (
	"context"
	"fmt"
	"strings"

	coreapp "github.com/fluxplane/agentruntime/core/app"
	"github.com/fluxplane/agentruntime/core/channel"
	"github.com/fluxplane/agentruntime/core/policy"
	"github.com/fluxplane/agentruntime/core/user"
)

// DirectoryResolver resolves inbound channel identities against app-declared
// canonical users and groups.
type DirectoryResolver struct {
	fallback Resolver
	users    map[user.ID]user.User
	groups   map[user.ID]user.Group
	rules    []user.GroupRule
	byID     map[identityKey]user.ID
	byEmail  map[string]user.ID
}

type identityKey struct {
	provider string
	id       string
}

// NewDirectoryResolver returns a resolver backed by an inert app identity spec.
// App identity is an overlay: it can map provider identities to canonical users,
// and it can add configured groups/trust to users that were resolved by another
// provider-specific resolver such as Slack users.info.
func NewDirectoryResolver(spec coreapp.IdentitySpec, fallback Resolver) (DirectoryResolver, error) {
	if fallback == nil {
		fallback = DefaultResolver{}
	}
	r := DirectoryResolver{
		fallback: fallback,
		users:    map[user.ID]user.User{},
		groups:   map[user.ID]user.Group{},
		rules:    nil,
		byID:     map[identityKey]user.ID{},
		byEmail:  map[string]user.ID{},
	}
	for _, group := range spec.Groups {
		group = normalizeGroup(group)
		if group.ID == "" {
			continue
		}
		r.groups[group.ID] = mergeGroups(r.groups[group.ID], group)
	}
	for _, rule := range spec.Rules {
		rule = normalizeRule(rule)
		if len(rule.Groups) == 0 {
			continue
		}
		r.rules = append(r.rules, rule)
	}
	for _, configured := range spec.Users {
		configured = normalizeUser(configured)
		if configured.ID == "" {
			continue
		}
		r.users[configured.ID] = mergeUsers(r.users[configured.ID], configured)
	}
	for _, configured := range r.users {
		for _, identity := range configured.Identities {
			if err := r.indexIdentity(configured.ID, identity); err != nil {
				return DirectoryResolver{}, err
			}
		}
		if email := strings.TrimSpace(configured.Annotations["email"]); email != "" {
			if err := r.indexEmail(configured.ID, email); err != nil {
				return DirectoryResolver{}, err
			}
		}
	}
	for _, group := range r.groups {
		for _, member := range group.Members {
			configured := r.users[member]
			if configured.ID == "" {
				continue
			}
			if !containsUserID(configured.Groups, group.ID) {
				configured.Groups = append(configured.Groups, group.ID)
				r.users[member] = configured
			}
		}
	}
	return r, nil
}

// Empty reports whether the resolver has no configured users or groups.
func (r DirectoryResolver) Empty() bool {
	return len(r.users) == 0 && len(r.groups) == 0 && len(r.rules) == 0
}

// ResolveIdentity resolves inbound caller evidence to a canonical actor when a
// configured user matches; otherwise it preserves the fallback result.
func (r DirectoryResolver) ResolveIdentity(ctx context.Context, req Request) (Result, error) {
	base, err := r.fallback.ResolveIdentity(ctx, req)
	if err != nil {
		return Result{}, err
	}
	userID, matched := r.matchInbound(req.Inbound, base.Actor)
	if !matched {
		if base.Actor.Resolution == user.ResolutionResolved && base.Actor.User.ID != "" && r.hasOverlay(base.Actor.User.ID) {
			base.Actor = r.actorFor(base.Actor.User.ID, base.Actor)
		}
		base.Actor = r.applyRules(base.Actor)
		base.Trust = r.raiseTrust(base.Trust, base.Actor.Trust)
		return base, nil
	}
	base.Actor = r.applyRules(r.actorFor(userID, base.Actor))
	base.Trust = r.raiseTrust(base.Trust, base.Actor.Trust)
	return base, nil
}

func (r *DirectoryResolver) indexIdentity(id user.ID, identity user.Identity) error {
	provider := normalizeProvider(identity.Provider)
	providerID := strings.TrimSpace(identity.ProviderID)
	if provider != "" && providerID != "" {
		if err := r.indexIdentityKey(id, identityKey{provider: provider, id: providerID}); err != nil {
			return err
		}
		if provider == "slack" {
			if err := r.indexIdentityKey(id, identityKey{provider: "slack_user", id: providerID}); err != nil {
				return err
			}
		}
		if provider == "slack_user" {
			if err := r.indexIdentityKey(id, identityKey{provider: "slack", id: providerID}); err != nil {
				return err
			}
		}
	}
	if email := strings.TrimSpace(identity.Email); email != "" {
		if err := r.indexEmail(id, email); err != nil {
			return err
		}
	}
	return nil
}

func (r *DirectoryResolver) indexIdentityKey(id user.ID, key identityKey) error {
	if existing, ok := r.byID[key]; ok && existing != id {
		return fmt.Errorf("identity directory: %s identity %q is mapped to both %q and %q", key.provider, key.id, existing, id)
	}
	r.byID[key] = id
	return nil
}

func (r *DirectoryResolver) indexEmail(id user.ID, email string) error {
	email = strings.ToLower(strings.TrimSpace(email))
	if email == "" {
		return nil
	}
	if existing, ok := r.byEmail[email]; ok && existing != id {
		return fmt.Errorf("identity directory: email %q is mapped to both %q and %q", email, existing, id)
	}
	r.byEmail[email] = id
	return nil
}

func (r DirectoryResolver) matchInbound(inbound channel.Inbound, actor user.Actor) (user.ID, bool) {
	provider := normalizeProvider(firstNonEmpty(actor.Identity.Provider, inbound.Caller.Principal.Kind))
	providerID := strings.TrimSpace(firstNonEmpty(actor.Identity.ProviderID, inbound.Caller.Principal.ID))
	if provider != "" && providerID != "" {
		if id, ok := r.byID[identityKey{provider: provider, id: providerID}]; ok {
			return id, true
		}
	}
	if provider == "user" && providerID != "" {
		if r.hasOverlay(user.ID(providerID)) {
			return user.ID(providerID), true
		}
	}
	for _, email := range []string{actor.Identity.Email, inbound.Caller.Principal.Name} {
		email = strings.ToLower(strings.TrimSpace(email))
		if email == "" || !strings.Contains(email, "@") {
			continue
		}
		if id, ok := r.byEmail[email]; ok {
			return id, true
		}
	}
	return "", false
}

func (r DirectoryResolver) hasOverlay(id user.ID) bool {
	if _, ok := r.users[id]; ok {
		return true
	}
	for _, group := range r.groups {
		if containsUserID(group.Members, id) {
			return true
		}
	}
	return false
}

func (r DirectoryResolver) actorFor(id user.ID, base user.Actor) user.Actor {
	configured := overlayUser(base.User, r.users[id], id)
	groups := r.groupsFor(configured)
	configured.Groups = mergeGroupIDs(configured.Groups, groups)
	trust := user.NormalizeTrust(configured.Trust)
	for _, group := range groups {
		trust = user.Max(trust, group.Trust)
	}
	identity := matchedIdentity(configured, base.Identity)
	configured.Trust = trust
	configured.Identities = mergeIdentities(configured.Identities, identity)
	return user.Actor{
		User:       configured,
		Identity:   identity,
		Groups:     groups,
		Trust:      trust,
		Resolution: user.ResolutionResolved,
	}
}

func (r DirectoryResolver) raiseTrust(trust policy.Trust, actorTrust user.TrustLevel) policy.Trust {
	if trust.Downgraded {
		return trust
	}
	raised := maxPolicyTrust(trust.Level, policyTrustFromUser(actorTrust))
	if raised == trust.Level {
		return trust
	}
	trust.Level = raised
	if trust.Reason == "" {
		trust.Reason = "identity_directory"
	}
	if trust.VerifiedBy == "" {
		trust.VerifiedBy = "identity_directory"
	}
	return trust
}

func (r DirectoryResolver) groupsFor(configured user.User) []user.Group {
	seen := map[user.ID]bool{}
	var out []user.Group
	for _, id := range configured.Groups {
		if group, ok := r.groups[id]; ok && !seen[group.ID] {
			seen[group.ID] = true
			out = append(out, group)
		}
	}
	for _, group := range r.groups {
		if containsUserID(group.Members, configured.ID) && !seen[group.ID] {
			seen[group.ID] = true
			out = append(out, group)
		}
	}
	return out
}

func (r DirectoryResolver) applyRules(actor user.Actor) user.Actor {
	for _, rule := range r.rules {
		if !ruleMatches(rule.Match, actor) {
			continue
		}
		for _, id := range rule.Groups {
			if id == "" || containsUserID(actor.User.Groups, id) {
				continue
			}
			actor.User.Groups = append(actor.User.Groups, id)
		}
	}
	groups := r.groupsFor(actor.User)
	actor.Groups = mergeGroupsByID(actor.Groups, groups)
	actor.User.Groups = mergeGroupIDs(actor.User.Groups, actor.Groups)
	trust := user.NormalizeTrust(actor.User.Trust)
	if actor.Trust != "" {
		trust = user.Max(trust, actor.Trust)
	}
	for _, group := range actor.Groups {
		trust = user.Max(trust, group.Trust)
	}
	actor.Trust = trust
	actor.User.Trust = trust
	return actor
}

func ruleMatches(match user.IdentityMatch, actor user.Actor) bool {
	provider := normalizeProvider(match.Provider)
	if provider != "" && !providerEquivalent(provider, normalizeProvider(actor.Identity.Provider)) {
		return false
	}
	if id := strings.TrimSpace(match.ProviderID); id != "" && id != strings.TrimSpace(actor.Identity.ProviderID) {
		return false
	}
	if match.Resolution != "" && user.NormalizeResolution(match.Resolution) != user.NormalizeResolution(actor.Resolution) {
		return false
	}
	return true
}

func mergeGroupsByID(base, overlay []user.Group) []user.Group {
	out := append([]user.Group(nil), base...)
	seen := map[user.ID]bool{}
	for _, group := range out {
		if group.ID != "" {
			seen[group.ID] = true
		}
	}
	for _, group := range overlay {
		if group.ID == "" || seen[group.ID] {
			continue
		}
		seen[group.ID] = true
		out = append(out, group)
	}
	return out
}

func matchedIdentity(configured user.User, inbound user.Identity) user.Identity {
	inProvider := normalizeProvider(inbound.Provider)
	inID := strings.TrimSpace(inbound.ProviderID)
	for _, identity := range configured.Identities {
		if providerEquivalent(normalizeProvider(identity.Provider), inProvider) && strings.TrimSpace(identity.ProviderID) == inID {
			return identity
		}
	}
	if inbound.Provider != "" || inbound.ProviderID != "" {
		return user.Identity{Provider: inbound.Provider, ProviderID: inbound.ProviderID, Email: inbound.Email, DisplayName: inbound.DisplayName}
	}
	if len(configured.Identities) > 0 {
		return configured.Identities[0]
	}
	return user.Identity{Provider: "user", ProviderID: string(configured.ID), DisplayName: configured.DisplayName}
}

func mergeIdentities(identities []user.Identity, identity user.Identity) []user.Identity {
	if identity.Provider == "" && identity.ProviderID == "" {
		return identities
	}
	for _, existing := range identities {
		if normalizeProvider(existing.Provider) == normalizeProvider(identity.Provider) &&
			strings.TrimSpace(existing.ProviderID) == strings.TrimSpace(identity.ProviderID) {
			return identities
		}
	}
	return append(append([]user.Identity(nil), identities...), identity)
}

func normalizeUser(configured user.User) user.User {
	configured.ID = user.ID(strings.TrimSpace(string(configured.ID)))
	if configured.ID == "" {
		return configured
	}
	if configured.Username == "" {
		configured.Username = string(configured.ID)
	}
	configured.Groups = cleanUserIDs(configured.Groups)
	return configured
}

func normalizeGroup(group user.Group) user.Group {
	group.ID = user.ID(strings.TrimSpace(string(group.ID)))
	group.Members = cleanUserIDs(group.Members)
	return group
}

func normalizeRule(rule user.GroupRule) user.GroupRule {
	rule.Match.Provider = strings.TrimSpace(rule.Match.Provider)
	rule.Match.ProviderID = strings.TrimSpace(rule.Match.ProviderID)
	rule.Groups = cleanUserIDs(rule.Groups)
	return rule
}

func mergeUsers(base, overlay user.User) user.User {
	if base.ID == "" {
		return overlay
	}
	if overlay.Username != "" {
		base.Username = overlay.Username
	}
	if overlay.DisplayName != "" {
		base.DisplayName = overlay.DisplayName
	}
	if overlay.Trust != "" {
		base.Trust = overlay.Trust
	}
	base.Groups = mergeUserIDs(base.Groups, overlay.Groups)
	base.Identities = mergeUserIdentities(base.Identities, overlay.Identities)
	base.Annotations = mergeStringMap(base.Annotations, overlay.Annotations)
	return base
}

func mergeGroups(base, overlay user.Group) user.Group {
	if base.ID == "" {
		return overlay
	}
	if overlay.DisplayName != "" {
		base.DisplayName = overlay.DisplayName
	}
	if overlay.Trust != "" {
		base.Trust = overlay.Trust
	}
	base.Members = mergeUserIDs(base.Members, overlay.Members)
	base.Annotations = mergeStringMap(base.Annotations, overlay.Annotations)
	return base
}

func overlayUser(base, overlay user.User, id user.ID) user.User {
	if overlay.ID == "" {
		overlay = user.User{ID: id}
	}
	if overlay.Username == "" {
		overlay.Username = firstNonEmpty(base.Username, string(id))
	}
	if overlay.DisplayName == "" {
		overlay.DisplayName = firstNonEmpty(base.DisplayName, overlay.Username)
	}
	if overlay.Trust == "" {
		overlay.Trust = base.Trust
	}
	overlay.Groups = mergeUserIDs(base.Groups, overlay.Groups)
	overlay.Identities = mergeUserIdentities(overlay.Identities, base.Identities)
	overlay.Annotations = mergeStringMap(base.Annotations, overlay.Annotations)
	return overlay
}

func mergeGroupIDs(existing []user.ID, groups []user.Group) []user.ID {
	out := append([]user.ID(nil), existing...)
	for _, group := range groups {
		if group.ID != "" && !containsUserID(out, group.ID) {
			out = append(out, group.ID)
		}
	}
	return out
}

func mergeUserIDs(base, overlay []user.ID) []user.ID {
	out := append([]user.ID(nil), base...)
	for _, value := range overlay {
		value = user.ID(strings.TrimSpace(string(value)))
		if value != "" && !containsUserID(out, value) {
			out = append(out, value)
		}
	}
	return out
}

func mergeUserIdentities(base, overlay []user.Identity) []user.Identity {
	out := append([]user.Identity(nil), base...)
	for _, identity := range overlay {
		out = mergeIdentities(out, identity)
	}
	return out
}

func mergeStringMap(base, overlay map[string]string) map[string]string {
	if len(base) == 0 && len(overlay) == 0 {
		return nil
	}
	out := make(map[string]string, len(base)+len(overlay))
	for key, value := range base {
		out[key] = value
	}
	for key, value := range overlay {
		out[key] = value
	}
	return out
}

func cleanUserIDs(values []user.ID) []user.ID {
	out := make([]user.ID, 0, len(values))
	for _, value := range values {
		value = user.ID(strings.TrimSpace(string(value)))
		if value != "" && !containsUserID(out, value) {
			out = append(out, value)
		}
	}
	return out
}

func containsUserID(values []user.ID, target user.ID) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func normalizeProvider(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	switch value {
	case "slack":
		return "slack"
	case "slack_user":
		return "slack_user"
	default:
		return value
	}
}

func providerEquivalent(a, b string) bool {
	if a == b {
		return true
	}
	return (a == "slack" && b == "slack_user") || (a == "slack_user" && b == "slack")
}

func maxPolicyTrust(a, b policy.TrustLevel) policy.TrustLevel {
	if policy.TrustSatisfies(a, b) {
		return a
	}
	return b
}

func policyTrustFromUser(level user.TrustLevel) policy.TrustLevel {
	switch user.NormalizeTrust(level) {
	case user.TrustOperator:
		return policy.TrustPrivileged
	case user.TrustInternal:
		return policy.TrustVerified
	default:
		return policy.TrustUntrusted
	}
}
