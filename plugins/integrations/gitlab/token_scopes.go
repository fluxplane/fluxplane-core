package gitlab

import (
	"context"
	"fmt"
	fpsystem "github.com/fluxplane/fluxplane-system"

	coresecret "github.com/fluxplane/fluxplane-auth/authsecret"
	"github.com/fluxplane/fluxplane-core/core/resource"
	"github.com/fluxplane/fluxplane-system/systemkit"
	gitlab "gitlab.com/gitlab-org/api/client-go/v2"
	"golang.org/x/oauth2"
)

// TokenScopes resolves the current GitLab token and returns the scopes reported by GitLab.
func TokenScopes(ctx context.Context, sys fpsystem.System, resolver coresecret.Resolver, ref resource.PluginRef, cfg Config) ([]string, bool, error) {
	return TokenScopesWithBoundaries(ctx, BoundariesFromSystem(sys), resolver, ref, cfg)
}

func TokenScopesWithBoundaries(ctx context.Context, boundaries Boundaries, resolver coresecret.Resolver, ref resource.PluginRef, cfg Config) ([]string, bool, error) {
	cfg = normalizeConfig(cfg)
	client, err := tokenScopeClient(ctx, boundaries.Network, resolver, ref, cfg)
	if err != nil {
		return nil, false, err
	}
	token, _, err := client.PersonalAccessTokens.GetSinglePersonalAccessToken(gitlab.WithContext(ctx))
	if err != nil {
		return nil, false, err
	}
	if token == nil {
		return nil, false, nil
	}
	return append([]string(nil), token.Scopes...), true, nil
}

func tokenScopeClient(ctx context.Context, network fpsystem.Network, resolver coresecret.Resolver, ref resource.PluginRef, cfg Config) (*gitlab.Client, error) {
	if network == nil {
		return nil, fmt.Errorf("gitlabplugin: network is nil")
	}
	if resolver == nil {
		return nil, fmt.Errorf("gitlabplugin: secret resolver is nil")
	}
	auth, err := authFromResolver(ctx, resolver, ref, cfg)
	if err != nil {
		return nil, err
	}
	options := []gitlab.ClientOptionFunc{
		gitlab.WithBaseURL(firstNonEmpty(auth.BaseURL, cfg.baseURL())),
		gitlab.WithHTTPClient(systemkit.NewHTTPClient(network)),
		gitlab.WithoutRetries(),
	}
	switch auth.Material.Kind {
	case coresecret.KindAPIKey:
		return gitlab.NewClient(auth.Material.String(), options...)
	case coresecret.KindBearerToken, coresecret.KindOAuth2Token:
		return gitlab.NewAuthSourceClient(gitlab.OAuthTokenSource{
			TokenSource: oauth2.StaticTokenSource(&oauth2.Token{AccessToken: auth.Material.String()}),
		}, options...)
	default:
		return nil, fmt.Errorf("gitlabplugin: unsupported auth material kind %q", auth.Material.Kind)
	}
}
