package gitlab

import (
	"context"
	"fmt"

	"github.com/fluxplane/agentruntime/core/resource"
	coresecret "github.com/fluxplane/agentruntime/core/secret"
	runtimesecret "github.com/fluxplane/agentruntime/runtime/secret"
	"github.com/fluxplane/agentruntime/runtime/system"
	gitlab "gitlab.com/gitlab-org/api/client-go/v2"
	"golang.org/x/oauth2"
)

// TokenScopes resolves the current GitLab token and returns the scopes reported by GitLab.
func TokenScopes(ctx context.Context, sys system.System, resolver runtimesecret.Resolver, ref resource.PluginRef, cfg Config) ([]string, bool, error) {
	cfg = normalizeConfig(cfg)
	client, err := tokenScopeClient(ctx, sys, resolver, ref, cfg)
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

func tokenScopeClient(ctx context.Context, sys system.System, resolver runtimesecret.Resolver, ref resource.PluginRef, cfg Config) (*gitlab.Client, error) {
	if sys == nil {
		return nil, fmt.Errorf("gitlabplugin: system is nil")
	}
	if sys.Network() == nil {
		return nil, fmt.Errorf("gitlabplugin: system network is nil")
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
		gitlab.WithHTTPClient(system.NewHTTPClient(sys.Network())),
		gitlab.WithoutRetries(),
	}
	switch auth.Material.Kind {
	case coresecret.KindAPIKey:
		return gitlab.NewClient(auth.Material.Value, options...)
	case coresecret.KindBearerToken, coresecret.KindOAuth2Token:
		return gitlab.NewAuthSourceClient(gitlab.OAuthTokenSource{
			TokenSource: oauth2.StaticTokenSource(&oauth2.Token{AccessToken: auth.Material.Value}),
		}, options...)
	default:
		return nil, fmt.Errorf("gitlabplugin: unsupported auth material kind %q", auth.Material.Kind)
	}
}
