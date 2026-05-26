package slack

import (
	"context"
	"fmt"
	"strings"

	"github.com/fluxplane/fluxplane-core/core/resource"
	coresecret "github.com/fluxplane/fluxplane-core/core/secret"
	runtimesecret "github.com/fluxplane/fluxplane-core/runtime/secret"
	"github.com/fluxplane/fluxplane-core/runtime/system"
)

const (
	DefaultAuthStorePath = runtimesecret.DefaultFileStorePath

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
	cfg.Search.Channels = cleaned(cfg.Search.Channels)
	return cfg
}

func AuthMethods(ref resource.PluginRef, cfg Config) []coresecret.AuthMethodSpec {
	cfg = NormalizeConfig(cfg)
	method := cfg.Auth.Method
	var out []coresecret.AuthMethodSpec
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

func Resolve(ctx context.Context, sys system.System, store runtimesecret.FileStore, ref resource.PluginRef, cfg Config) (Session, error) {
	return resolve(ctx, sys, store, ref, cfg)
}

func ResolveWithResolver(ctx context.Context, sys system.System, resolver runtimesecret.Resolver, ref resource.PluginRef, cfg Config) (Session, error) {
	if resolver == nil {
		return resolve(ctx, sys, runtimesecret.NewFileStore(DefaultAuthStorePath), ref, cfg)
	}
	cfg = NormalizeConfig(cfg)
	method := cfg.Auth.Method
	switch method {
	case "", TokenMethod:
		return resolveStoredOrEnvResolver(ctx, sys, resolver, ref, cfg, TokenMethod)
	case EnvMethod:
		return resolveEnvResolver(ctx, sys, resolver, cfg)
	case OAuth2Method:
		return resolveOAuthResolver(ctx, sys, resolver, ref, cfg)
	default:
		return Session{}, fmt.Errorf("slackplugin: unsupported auth method %q", method)
	}
}

func resolve(ctx context.Context, sys system.System, store runtimesecret.FileStore, ref resource.PluginRef, cfg Config) (Session, error) {
	return ResolveWithResolver(ctx, sys, store, ref, cfg)
}

func storedTokenAuthMethod(ref resource.PluginRef) coresecret.AuthMethodSpec {
	return coresecret.AuthMethodSpec{
		Name:        TokenMethod,
		Method:      coresecret.AuthMethodStored,
		Kind:        coresecret.KindBearerToken,
		DisplayName: "Slack token",
		Description: "Store Slack token material for native Slack API access.",
		Secret:      BotTokenSecretRef(ref),
		Header:      coresecret.HeaderSpec{Name: "Authorization", Scheme: "Bearer"},
		SetupFields: []coresecret.SetupFieldSpec{
			{
				Name:          BotTokenPurpose,
				DisplayName:   "Bot token",
				Description:   "Slack xoxb bot token.",
				RequiredGroup: "api_token",
				Sensitive:     true,
				Env:           coresecret.EnvSpec{Name: defaultBotTokenEnv},
			},
			{
				Name:        AppTokenPurpose,
				DisplayName: "App token",
				Description: "Slack xapp app-level token. Required for daemon channels using Socket Mode.",
				Sensitive:   true,
				Env:         coresecret.EnvSpec{Name: defaultAppTokenEnv},
			},
			{
				Name:          UserTokenPurpose,
				DisplayName:   "User token",
				Description:   "Slack xoxp user token. Required when bot token is omitted.",
				RequiredGroup: "api_token",
				Sensitive:     true,
				Env:           coresecret.EnvSpec{Name: defaultUserTokenEnv},
			},
		},
	}
}

func envAuthMethod(cfg Config) coresecret.AuthMethodSpec {
	return coresecret.AuthMethodSpec{
		Name:        EnvMethod,
		Method:      coresecret.AuthMethodEnv,
		Kind:        coresecret.KindBearerToken,
		DisplayName: "Slack environment token",
		Description: "Resolve Slack tokens from runtime environment variables.",
		Secret:      coresecret.Env(firstNonEmpty(cfg.Auth.BotTokenEnv, defaultBotTokenEnv)),
		Env: coresecret.EnvSpec{
			Name:    firstNonEmpty(cfg.Auth.BotTokenEnv, defaultBotTokenEnv),
			Aliases: []string{defaultUserTokenEnv},
		},
		Header: coresecret.HeaderSpec{Name: "Authorization", Scheme: "Bearer"},
		SetupFields: []coresecret.SetupFieldSpec{
			{Name: "bot_token_env", DisplayName: "Bot token env", Env: coresecret.EnvSpec{Name: defaultBotTokenEnv}},
			{Name: "app_token_env", DisplayName: "App token env", Env: coresecret.EnvSpec{Name: defaultAppTokenEnv}},
			{Name: "user_token_env", DisplayName: "User token env", Env: coresecret.EnvSpec{Name: defaultUserTokenEnv}},
		},
	}
}

func oauth2AuthMethod(ref resource.PluginRef) coresecret.AuthMethodSpec {
	return coresecret.AuthMethodSpec{
		Name:        OAuth2Method,
		Method:      coresecret.AuthMethodOAuth2,
		Kind:        coresecret.KindOAuth2Token,
		DisplayName: "Slack OAuth2",
		Description: "Prepare Slack OAuth2 bot-token setup. Socket Mode channels still need an app token.",
		Secret:      OAuth2SecretRef(ref),
		OAuth2: coresecret.OAuth2Spec{
			AuthorizeURL: slackAuthorizeURL,
			TokenURL:     slackTokenURL,
			Scopes:       append([]string(nil), defaultOAuthScopes...),
		},
		SetupFields: []coresecret.SetupFieldSpec{
			{Name: "client_id", DisplayName: "Client ID", Required: true, Env: coresecret.EnvSpec{Name: "SLACK_CLIENT_ID"}},
			{Name: "client_secret", DisplayName: "Client secret", Required: true, Sensitive: true, Env: coresecret.EnvSpec{Name: "SLACK_CLIENT_SECRET"}},
		},
	}
}

func resolveStoredOrEnvResolver(ctx context.Context, sys system.System, resolver runtimesecret.Resolver, ref resource.PluginRef, cfg Config, method string) (Session, error) {
	session := Session{Method: method}
	session.BotToken = loadResolvedValue(ctx, resolver, BotTokenSecretRef(ref))
	session.AppToken = loadResolvedValue(ctx, resolver, AppTokenSecretRef(ref))
	session.UserToken = loadResolvedValue(ctx, resolver, UserTokenSecretRef(ref))
	envSession := envSession(ctx, sys, cfg)
	session.BotToken = firstNonEmpty(session.BotToken, envSession.BotToken, loadResolvedValue(ctx, resolver, coresecret.Env(firstNonEmpty(cfg.Auth.BotTokenEnv, defaultBotTokenEnv))))
	session.AppToken = firstNonEmpty(session.AppToken, envSession.AppToken, loadResolvedValue(ctx, resolver, coresecret.Env(firstNonEmpty(cfg.Auth.AppTokenEnv, defaultAppTokenEnv))))
	session.UserToken = firstNonEmpty(session.UserToken, envSession.UserToken, loadResolvedValue(ctx, resolver, coresecret.Env(firstNonEmpty(cfg.Auth.UserTokenEnv, defaultUserTokenEnv))))
	if session.BotToken == "" && session.UserToken == "" {
		return Session{}, fmt.Errorf("slackplugin: bot token or user token is not configured; run fluxplane auth connect --plugin slack --method %s or configure %s/%s", TokenMethod, firstNonEmpty(cfg.Auth.BotTokenEnv, defaultBotTokenEnv), firstNonEmpty(cfg.Auth.UserTokenEnv, defaultUserTokenEnv))
	}
	return session, nil
}

func resolveEnvResolver(ctx context.Context, sys system.System, resolver runtimesecret.Resolver, cfg Config) (Session, error) {
	session := envSession(ctx, sys, cfg)
	session.Method = EnvMethod
	session.BotToken = firstNonEmpty(session.BotToken, loadResolvedValue(ctx, resolver, coresecret.Env(firstNonEmpty(cfg.Auth.BotTokenEnv, defaultBotTokenEnv))))
	session.AppToken = firstNonEmpty(session.AppToken, loadResolvedValue(ctx, resolver, coresecret.Env(firstNonEmpty(cfg.Auth.AppTokenEnv, defaultAppTokenEnv))))
	session.UserToken = firstNonEmpty(session.UserToken, loadResolvedValue(ctx, resolver, coresecret.Env(firstNonEmpty(cfg.Auth.UserTokenEnv, defaultUserTokenEnv))))
	if session.BotToken == "" && session.UserToken == "" {
		return Session{}, fmt.Errorf("slackplugin: bot token environment variable %s or user token environment variable %s is not set", firstNonEmpty(cfg.Auth.BotTokenEnv, defaultBotTokenEnv), firstNonEmpty(cfg.Auth.UserTokenEnv, defaultUserTokenEnv))
	}
	return session, nil
}

func resolveOAuthResolver(ctx context.Context, sys system.System, resolver runtimesecret.Resolver, ref resource.PluginRef, cfg Config) (Session, error) {
	session := Session{Method: OAuth2Method}
	session.BotToken = loadResolvedValue(ctx, resolver, OAuth2SecretRef(ref))
	session.AppToken = loadResolvedValue(ctx, resolver, AppTokenSecretRef(ref))
	session.UserToken = loadResolvedValue(ctx, resolver, UserTokenSecretRef(ref))
	envSession := envSession(ctx, sys, cfg)
	session.AppToken = firstNonEmpty(session.AppToken, envSession.AppToken)
	session.UserToken = firstNonEmpty(session.UserToken, envSession.UserToken, loadResolvedValue(ctx, resolver, coresecret.Env(firstNonEmpty(cfg.Auth.UserTokenEnv, defaultUserTokenEnv))))
	if session.BotToken == "" && session.UserToken == "" {
		return Session{}, fmt.Errorf("slackplugin: oauth2 token or user token is not configured; run fluxplane auth connect --plugin slack --method %s", OAuth2Method)
	}
	return session, nil
}

func envSession(ctx context.Context, sys system.System, cfg Config) Session {
	if sys == nil || sys.Environment() == nil {
		return Session{}
	}
	return Session{
		BotToken:  lookupEnv(ctx, sys, firstNonEmpty(cfg.Auth.BotTokenEnv, defaultBotTokenEnv)),
		AppToken:  lookupEnv(ctx, sys, firstNonEmpty(cfg.Auth.AppTokenEnv, defaultAppTokenEnv)),
		UserToken: lookupEnv(ctx, sys, firstNonEmpty(cfg.Auth.UserTokenEnv, defaultUserTokenEnv)),
	}
}

func lookupEnv(ctx context.Context, sys system.System, name string) string {
	if strings.TrimSpace(name) == "" || sys == nil || sys.Environment() == nil {
		return ""
	}
	value, ok, err := sys.Environment().Lookup(ctx, strings.TrimSpace(name))
	if err != nil || !ok {
		return ""
	}
	return strings.TrimSpace(value)
}

func loadResolvedValue(ctx context.Context, resolver runtimesecret.Resolver, ref coresecret.Ref) string {
	if resolver == nil {
		return ""
	}
	material, ok, err := resolver.ResolveSecret(ctx, ref)
	if err != nil || !ok {
		return ""
	}
	return strings.TrimSpace(material.Value)
}

func BotTokenSecretRef(ref resource.PluginRef) coresecret.Ref {
	return coresecret.Plugin(Name, ref.InstanceName(), BotTokenPurpose)
}

func AppTokenSecretRef(ref resource.PluginRef) coresecret.Ref {
	return coresecret.Plugin(Name, ref.InstanceName(), AppTokenPurpose)
}

func UserTokenSecretRef(ref resource.PluginRef) coresecret.Ref {
	return coresecret.Plugin(Name, ref.InstanceName(), UserTokenPurpose)
}

func OAuth2SecretRef(ref resource.PluginRef) coresecret.Ref {
	return coresecret.Plugin(Name, ref.InstanceName(), OAuth2TokenPurpose)
}
