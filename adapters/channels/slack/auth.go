package slack

import (
	"context"
	"fmt"
	sharedsecret "github.com/fluxplane/fluxplane-secret"
	fpsystem "github.com/fluxplane/fluxplane-system"
	"strings"

	auth "github.com/fluxplane/fluxplane-auth"
	"github.com/fluxplane/fluxplane-core/core/resource"
)

const (
	DefaultAuthStorePath = sharedsecret.DefaultFileStorePath

	TokenMethod  = "token"
	EnvMethod    = "env"
	OAuth2Method = "oauth2"

	ChannelTokenAuto = "auto"

	BotTokenPurpose    = "bot_token"
	AppTokenPurpose    = "app_token"
	UserTokenPurpose   = "user_token"
	OAuth2TokenPurpose = "oauth2_token"

	defaultBotTokenEnv  = "SLACK_BOT_TOKEN"
	defaultAppTokenEnv  = "SLACK_APP_TOKEN"
	defaultUserTokenEnv = "SLACK_USER_TOKEN"

	slackAuthorizeURL = "https://slack.com/oauth/v2/authorize"
	slackTokenURL     = "https://slack.com/api/oauth.v2.access"
)

var defaultOAuthScopes = []string{
	"app_mentions:read",
	"channels:history",
	"channels:read",
	"chat:write",
	"groups:history",
	"groups:read",
	"im:history",
	"im:read",
	"mpim:history",
	"mpim:read",
	"search:read",
	"users:read",
	"users:read.email",
}

type Config struct {
	Auth   AuthConfig   `json:"auth,omitempty" jsonschema:"description=Slack authentication source and token selection."`
	Search SearchConfig `json:"search,omitempty" jsonschema:"description=Slack search and indexing policy for bot-visible message search."`
}

type AuthConfig struct {
	Method       string `json:"method,omitempty" jsonschema:"description=Slack auth method. token uses Fluxplane stored secrets; env reads process environment variables; oauth2 uses stored OAuth2 token material.,enum=token,enum=env,enum=oauth2"`
	BotTokenEnv  string `json:"bot_token_env,omitempty" jsonschema:"description=Environment variable containing the Slack xoxb bot token when method is env. Defaults to SLACK_BOT_TOKEN."`
	AppTokenEnv  string `json:"app_token_env,omitempty" jsonschema:"description=Environment variable containing the Slack xapp app token when method is env. Defaults to SLACK_APP_TOKEN."`
	UserTokenEnv string `json:"user_token_env,omitempty" jsonschema:"description=Environment variable containing the Slack xoxp user token when method is env. Defaults to SLACK_USER_TOKEN."`
	ChannelToken string `json:"channel_token,omitempty" jsonschema:"description=Token preference for Slack channel posting. auto prefers a bot token and falls back to a user token.,enum=auto,enum=bot_token,enum=user_token"`
}

type SearchConfig struct {
	Channels       []string `json:"channels,omitempty" jsonschema:"description=Public Slack channel names or ids whose history should be indexed for bot-mode broad search."`
	HistoryWindow  string   `json:"history_window,omitempty" jsonschema:"description=Maximum Slack message age to index for bot-mode broad search. Defaults to 90d."`
	IncludeThreads *bool    `json:"include_threads,omitempty" jsonschema:"description=Whether to index thread replies for indexed messages. Defaults to true."`
}

type Session struct {
	BotToken  string
	AppToken  string
	UserToken string
	Method    string
}

func NormalizeConfig(cfg Config) Config {
	cfg.Auth.Method = strings.ToLower(strings.TrimSpace(cfg.Auth.Method))
	cfg.Auth.BotTokenEnv = strings.TrimSpace(cfg.Auth.BotTokenEnv)
	cfg.Auth.AppTokenEnv = strings.TrimSpace(cfg.Auth.AppTokenEnv)
	cfg.Auth.UserTokenEnv = strings.TrimSpace(cfg.Auth.UserTokenEnv)
	cfg.Auth.ChannelToken = strings.ToLower(strings.TrimSpace(cfg.Auth.ChannelToken))
	cfg.Search.HistoryWindow = strings.TrimSpace(cfg.Search.HistoryWindow)
	cfg.Search.Channels = trimNonEmpty(cfg.Search.Channels)
	return cfg
}

func AuthMethods(ref resource.PluginRef, cfg Config) []auth.MethodSpec {
	cfg = NormalizeConfig(cfg)
	method := cfg.Auth.Method
	var out []auth.MethodSpec
	if method == "" || method == TokenMethod {
		out = append(out, storedTokenAuthMethod(ref))
	}
	if method == "" || method == EnvMethod {
		out = append(out, envAuthMethod(cfg))
	}
	if method == "" || method == OAuth2Method {
		out = append(out, oauth2AuthMethod(ref))
	}
	return out
}

func ResolveWithEnvironment(ctx context.Context, environment fpsystem.Environment, resolver sharedsecret.Resolver, ref resource.PluginRef, cfg Config) (Session, error) {
	if resolver == nil {
		resolver = sharedsecret.NewFileStore(DefaultAuthStorePath)
	}
	cfg = NormalizeConfig(cfg)
	method := cfg.Auth.Method
	switch method {
	case "", TokenMethod:
		return resolveStoredOrEnvResolver(ctx, environment, resolver, ref, cfg, TokenMethod)
	case EnvMethod:
		return resolveEnvResolver(ctx, environment, resolver, cfg)
	case OAuth2Method:
		return resolveOAuthResolver(ctx, environment, resolver, ref, cfg)
	default:
		return Session{}, fmt.Errorf("slackplugin: unsupported auth method %q", method)
	}
}

func storedTokenAuthMethod(ref resource.PluginRef) auth.MethodSpec {
	return auth.MethodSpec{
		Name:        TokenMethod,
		Method:      auth.MethodStored,
		Kind:        sharedsecret.KindBearerToken,
		DisplayName: "Slack token",
		Description: "Store Slack token material for native Slack API access.",
		Secret:      BotTokenSecretRef(ref),
		Header:      auth.HeaderSpec{Name: "Authorization", Scheme: "Bearer"},
		SetupFields: []auth.FieldSpec{
			{
				Slot:          BotTokenPurpose,
				DisplayName:   "Bot token",
				Description:   "Slack xoxb bot token.",
				RequiredGroup: "api_token",
				Sensitive:     true,
				Env:           auth.EnvSpec{Name: defaultBotTokenEnv},
			},
			{
				Slot:        AppTokenPurpose,
				DisplayName: "App token",
				Description: "Slack xapp app-level token. Required for daemon channels using Socket Mode.",
				Sensitive:   true,
				Env:         auth.EnvSpec{Name: defaultAppTokenEnv},
			},
			{
				Slot:          UserTokenPurpose,
				DisplayName:   "User token",
				Description:   "Slack xoxp user token. Required when bot token is omitted.",
				RequiredGroup: "api_token",
				Sensitive:     true,
				Env:           auth.EnvSpec{Name: defaultUserTokenEnv},
			},
		},
	}
}

func envAuthMethod(cfg Config) auth.MethodSpec {
	return auth.MethodSpec{
		Name:        EnvMethod,
		Method:      auth.MethodEnv,
		Kind:        sharedsecret.KindBearerToken,
		DisplayName: "Slack environment token",
		Description: "Resolve Slack tokens from runtime environment variables.",
		Secret:      sharedsecret.Env(firstNonEmpty(cfg.Auth.BotTokenEnv, defaultBotTokenEnv)),
		Env: auth.EnvSpec{
			Name:    firstNonEmpty(cfg.Auth.BotTokenEnv, defaultBotTokenEnv),
			Aliases: []string{defaultUserTokenEnv},
		},
		Header: auth.HeaderSpec{Name: "Authorization", Scheme: "Bearer"},
		SetupFields: []auth.FieldSpec{
			{Slot: "bot_token_env", DisplayName: "Bot token env", Env: auth.EnvSpec{Name: defaultBotTokenEnv}},
			{Slot: "app_token_env", DisplayName: "App token env", Env: auth.EnvSpec{Name: defaultAppTokenEnv}},
			{Slot: "user_token_env", DisplayName: "User token env", Env: auth.EnvSpec{Name: defaultUserTokenEnv}},
		},
	}
}

func oauth2AuthMethod(ref resource.PluginRef) auth.MethodSpec {
	return auth.MethodSpec{
		Name:        OAuth2Method,
		Method:      auth.MethodOAuth2AuthCode,
		Kind:        sharedsecret.KindOAuth2Token,
		DisplayName: "Slack OAuth2",
		Description: "Prepare Slack OAuth2 bot-token setup. Socket Mode channels still need an app token.",
		Secret:      OAuth2SecretRef(ref),
		OAuth2: auth.OAuth2Spec{
			AuthorizeURL: slackAuthorizeURL,
			TokenURL:     slackTokenURL,
			Scopes:       append([]string(nil), defaultOAuthScopes...),
		},
		SetupFields: []auth.FieldSpec{
			{Slot: "client_id", DisplayName: "Client ID", Required: true, Env: auth.EnvSpec{Name: "SLACK_CLIENT_ID"}},
			{Slot: "client_secret", DisplayName: "Client secret", Required: true, Sensitive: true, Env: auth.EnvSpec{Name: "SLACK_CLIENT_SECRET"}},
		},
	}
}

func resolveStoredOrEnvResolver(ctx context.Context, environment fpsystem.Environment, resolver sharedsecret.Resolver, ref resource.PluginRef, cfg Config, method string) (Session, error) {
	session := Session{Method: method}
	session.BotToken = loadResolvedValue(ctx, resolver, BotTokenSecretRef(ref))
	session.AppToken = loadResolvedValue(ctx, resolver, AppTokenSecretRef(ref))
	session.UserToken = loadResolvedValue(ctx, resolver, UserTokenSecretRef(ref))
	envSession := envSession(ctx, environment, cfg)
	session.BotToken = firstNonEmpty(session.BotToken, envSession.BotToken, loadResolvedValue(ctx, resolver, sharedsecret.Env(firstNonEmpty(cfg.Auth.BotTokenEnv, defaultBotTokenEnv))))
	session.AppToken = firstNonEmpty(session.AppToken, envSession.AppToken, loadResolvedValue(ctx, resolver, sharedsecret.Env(firstNonEmpty(cfg.Auth.AppTokenEnv, defaultAppTokenEnv))))
	session.UserToken = firstNonEmpty(session.UserToken, envSession.UserToken, loadResolvedValue(ctx, resolver, sharedsecret.Env(firstNonEmpty(cfg.Auth.UserTokenEnv, defaultUserTokenEnv))))
	if session.BotToken == "" && session.UserToken == "" {
		return Session{}, fmt.Errorf("slackplugin: bot token or user token is not configured; run fluxplane auth connect --plugin slack --method %s or configure %s/%s", TokenMethod, firstNonEmpty(cfg.Auth.BotTokenEnv, defaultBotTokenEnv), firstNonEmpty(cfg.Auth.UserTokenEnv, defaultUserTokenEnv))
	}
	return session, nil
}

func resolveEnvResolver(ctx context.Context, environment fpsystem.Environment, resolver sharedsecret.Resolver, cfg Config) (Session, error) {
	session := envSession(ctx, environment, cfg)
	session.Method = EnvMethod
	session.BotToken = firstNonEmpty(session.BotToken, loadResolvedValue(ctx, resolver, sharedsecret.Env(firstNonEmpty(cfg.Auth.BotTokenEnv, defaultBotTokenEnv))))
	session.AppToken = firstNonEmpty(session.AppToken, loadResolvedValue(ctx, resolver, sharedsecret.Env(firstNonEmpty(cfg.Auth.AppTokenEnv, defaultAppTokenEnv))))
	session.UserToken = firstNonEmpty(session.UserToken, loadResolvedValue(ctx, resolver, sharedsecret.Env(firstNonEmpty(cfg.Auth.UserTokenEnv, defaultUserTokenEnv))))
	if session.BotToken == "" && session.UserToken == "" {
		return Session{}, fmt.Errorf("slackplugin: bot token environment variable %s or user token environment variable %s is not set", firstNonEmpty(cfg.Auth.BotTokenEnv, defaultBotTokenEnv), firstNonEmpty(cfg.Auth.UserTokenEnv, defaultUserTokenEnv))
	}
	return session, nil
}

func resolveOAuthResolver(ctx context.Context, environment fpsystem.Environment, resolver sharedsecret.Resolver, ref resource.PluginRef, cfg Config) (Session, error) {
	session := Session{Method: OAuth2Method}
	session.BotToken = loadResolvedValue(ctx, resolver, OAuth2SecretRef(ref))
	session.AppToken = loadResolvedValue(ctx, resolver, AppTokenSecretRef(ref))
	session.UserToken = loadResolvedValue(ctx, resolver, UserTokenSecretRef(ref))
	envSession := envSession(ctx, environment, cfg)
	session.AppToken = firstNonEmpty(session.AppToken, envSession.AppToken)
	session.UserToken = firstNonEmpty(session.UserToken, envSession.UserToken, loadResolvedValue(ctx, resolver, sharedsecret.Env(firstNonEmpty(cfg.Auth.UserTokenEnv, defaultUserTokenEnv))))
	if session.BotToken == "" && session.UserToken == "" {
		return Session{}, fmt.Errorf("slackplugin: oauth2 token or user token is not configured; run fluxplane auth connect --plugin slack --method %s", OAuth2Method)
	}
	return session, nil
}

func envSession(ctx context.Context, environment fpsystem.Environment, cfg Config) Session {
	if environment == nil {
		return Session{}
	}
	return Session{
		BotToken:  lookupEnv(ctx, environment, firstNonEmpty(cfg.Auth.BotTokenEnv, defaultBotTokenEnv)),
		AppToken:  lookupEnv(ctx, environment, firstNonEmpty(cfg.Auth.AppTokenEnv, defaultAppTokenEnv)),
		UserToken: lookupEnv(ctx, environment, firstNonEmpty(cfg.Auth.UserTokenEnv, defaultUserTokenEnv)),
	}
}

func lookupEnv(ctx context.Context, environment fpsystem.Environment, name string) string {
	if strings.TrimSpace(name) == "" || environment == nil {
		return ""
	}
	value, ok, err := environment.Lookup(ctx, strings.TrimSpace(name))
	if err != nil || !ok {
		return ""
	}
	return strings.TrimSpace(value)
}

func loadResolvedValue(ctx context.Context, resolver sharedsecret.Resolver, ref sharedsecret.Ref) string {
	if resolver == nil {
		return ""
	}
	material, ok, err := resolver.ResolveSecret(ctx, ref)
	if err != nil || !ok {
		return ""
	}
	return strings.TrimSpace(material.String())
}

func BotTokenSecretRef(ref resource.PluginRef) sharedsecret.Ref {
	return sharedsecret.Plugin(Name, ref.InstanceName(), BotTokenPurpose)
}

func AppTokenSecretRef(ref resource.PluginRef) sharedsecret.Ref {
	return sharedsecret.Plugin(Name, ref.InstanceName(), AppTokenPurpose)
}

func UserTokenSecretRef(ref resource.PluginRef) sharedsecret.Ref {
	return sharedsecret.Plugin(Name, ref.InstanceName(), UserTokenPurpose)
}

func trimNonEmpty(values []string) []string {
	out := make([]string, 0, len(values))
	for _, v := range values {
		if v = strings.TrimSpace(v); v != "" {
			out = append(out, v)
		}
	}
	return out
}

func OAuth2SecretRef(ref resource.PluginRef) sharedsecret.Ref {
	return sharedsecret.Plugin(Name, ref.InstanceName(), OAuth2TokenPurpose)
}
