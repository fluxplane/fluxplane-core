// Package identityplugin renders the current resolved actor for model context.
package identity

import (
	"context"
	"strings"

	"github.com/fluxplane/engine/core/activation"
	corecontext "github.com/fluxplane/engine/core/context"
	"github.com/fluxplane/engine/core/resource"
	"github.com/fluxplane/engine/core/user"
	"github.com/fluxplane/engine/orchestration/pluginhost"
)

const (
	// Name identifies the identity plugin.
	Name = "identity"
	// CurrentProvider identifies the current-user context provider.
	CurrentProvider = "identity.current"
)

// Plugin contributes normalized current-user context.
type Plugin struct{}

var _ pluginhost.Plugin = Plugin{}
var _ pluginhost.ContextProviderContributor = Plugin{}

// New returns the identity plugin.
func New() Plugin { return Plugin{} }

// Manifest returns plugin metadata.
func (Plugin) Manifest() pluginhost.Manifest {
	return pluginhost.Manifest{Name: Name, Description: "Current user and channel identity context."}
}

// Contributions returns identity context provider specs.
func (Plugin) Contributions(_ context.Context, ctx pluginhost.Context) (resource.ContributionBundle, error) {
	name := ctx.Ref.InstanceName()
	if name == "" {
		name = Name
	}
	return resource.ContributionBundle{
		ActivationSets: []activation.Set{{
			Name:        name,
			Aliases:     []string{name + ".default"},
			Description: "Current user and channel identity context.",
			Targets: []activation.Target{{
				Kind:            activation.TargetContextProvider,
				ContextProvider: corecontext.ProviderRef{Name: CurrentProvider},
			}},
		}},
		ContextProviders: []corecontext.ProviderSpec{currentContextSpec()},
	}, nil
}

// ContextProviders returns identity context providers.
func (Plugin) ContextProviders(context.Context, pluginhost.Context) ([]corecontext.Provider, error) {
	return []corecontext.Provider{currentProvider{}}, nil
}

func currentContextSpec() corecontext.ProviderSpec {
	return corecontext.ProviderSpec{
		Name:             CurrentProvider,
		Description:      "Current canonical user or unresolved channel identity.",
		Kinds:            []corecontext.BlockKind{corecontext.BlockText},
		DefaultPlacement: corecontext.PlacementSystem,
	}
}

type currentProvider struct{}

func (currentProvider) Spec() corecontext.ProviderSpec { return currentContextSpec() }

func (currentProvider) Build(_ context.Context, req corecontext.Request) ([]corecontext.Block, error) {
	content := renderCurrentIdentity(req.Scope)
	if content == "" {
		return nil, nil
	}
	return []corecontext.Block{{
		ID:        CurrentProvider,
		Provider:  CurrentProvider,
		Kind:      corecontext.BlockText,
		Placement: corecontext.PlacementSystem,
		Title:     "Current User",
		Content:   content,
		MediaType: "text/plain",
		Freshness: corecontext.FreshnessDynamic,
	}}, nil
}

func renderCurrentIdentity(scope map[string]string) string {
	if len(scope) == 0 {
		return ""
	}
	resolution := user.NormalizeResolution(user.ResolutionState(scope["user.resolution"]))
	trust := strings.TrimSpace(scope["trust.level"])
	if trust == "" {
		trust = "untrusted"
	}
	var lines []string
	lines = append(lines, "Current user:")
	lines = append(lines, "- resolved: "+resolvedText(resolution))
	if resolution == user.ResolutionResolved {
		if value := strings.TrimSpace(scope["user.id"]); value != "" {
			lines = append(lines, "- user: "+value)
		}
		if value := strings.TrimSpace(scope["user.username"]); value != "" && value != scope["user.id"] {
			lines = append(lines, "- username: "+value)
		}
	} else {
		identity := providerIdentity(scope)
		if identity != "" {
			lines = append(lines, "- channel identity: "+identity)
		}
		lines = append(lines, "- note: no canonical core user has been resolved for this turn")
	}
	if value := strings.TrimSpace(scope["user.groups"]); value != "" {
		lines = append(lines, "- groups: "+strings.ReplaceAll(value, ",", ", "))
	}
	if identity := providerIdentity(scope); identity != "" && resolution == user.ResolutionResolved {
		lines = append(lines, "- entry identity: "+identity)
	}
	if emails := emailList(scope); len(emails) > 0 {
		lines = append(lines, "emails:")
		for _, email := range emails {
			lines = append(lines, "- "+email)
		}
	}
	if identities := identityList(scope); len(identities) > 0 {
		lines = append(lines, "identities:")
		for _, identity := range identities {
			lines = append(lines, "- "+identity)
		}
	}
	if source := strings.TrimSpace(scope["caller.source"]); source != "" {
		lines = append(lines, "- source: "+source)
	}
	lines = append(lines, "- trust: "+trust)
	return strings.Join(lines, "\n")
}

func resolvedText(state user.ResolutionState) string {
	if state == user.ResolutionResolved {
		return "true"
	}
	return "false"
}

func providerIdentity(scope map[string]string) string {
	provider := strings.TrimSpace(scope["identity.provider"])
	id := strings.TrimSpace(scope["identity.provider_id"])
	switch {
	case provider != "" && id != "":
		return provider + ":" + id
	case id != "":
		return id
	default:
		return provider
	}
}

func identityList(scope map[string]string) []string {
	raw := strings.TrimSpace(scope["identity.all"])
	return splitScopeList(raw)
}

func emailList(scope map[string]string) []string {
	raw := strings.TrimSpace(scope["user.email.all"])
	return splitScopeList(raw)
}

func splitScopeList(raw string) []string {
	if raw == "" {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	for _, part := range strings.Split(raw, ";") {
		part = strings.TrimSpace(part)
		if part == "" || seen[part] {
			continue
		}
		seen[part] = true
		out = append(out, part)
	}
	return out
}
