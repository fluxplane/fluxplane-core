package slackplugin

import (
	"context"
	"fmt"
	"strings"

	"github.com/fluxplane/agentruntime/core/resource"
	coresecret "github.com/fluxplane/agentruntime/core/secret"
	runtimesecret "github.com/fluxplane/agentruntime/runtime/secret"
	"github.com/fluxplane/agentruntime/runtime/system"
)

const (
	DefaultAuthStorePath = runtimesecret.DefaultFileStorePath

	BotTokenMethod = "bot_token"
	EnvMethod      = "env"
	OAuth2Method   = "oauth2"

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
	Auth AuthConfig `json:"auth,omitempty"`
}

type AuthConfig struct {
	Method       string `json:"method,omitempty"`
	BotTokenEnv  string `json:"bot_token_env,omitempty"`
	AppTokenEnv  string `json:"app_token_env,omitempty"`
	UserTokenEnv string `json:"user_token_env,omitempty"`
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
	return cfg
}

func AuthMethods(ref resource.PluginRef, cfg Config) []coresecret.AuthMethodSpec {
	cfg = NormalizeConfig(cfg)
	method := cfg.Auth.Method
	var out []coresecret.AuthMethodSpec
	if method == "" || method == BotTokenMethod || method == "token" || method == "stored" {
		out = append(out, storedBotTokenAuthMethod(ref))
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
	cfg = NormalizeConfig(cfg)
	method := cfg.Auth.Method
	switch method {
	case "", BotTokenMethod, "token", "stored":
		return resolveStoredOrEnv(ctx, sys, store, ref, cfg, BotTokenMethod)
	case EnvMethod:
		return resolveEnv(ctx, sys, cfg)
	case OAuth2Method:
		return resolveOAuth(ctx, sys, store, ref, cfg)
	default:
		return Session{}, fmt.Errorf("slackplugin: unsupported auth method %q", method)
	}
}

func storedBotTokenAuthMethod(ref resource.PluginRef) coresecret.AuthMethodSpec {
	return coresecret.AuthMethodSpec{
		Name:        BotTokenMethod,
		Method:      coresecret.AuthMethodStored,
		Kind:        coresecret.KindBearerToken,
		DisplayName: "Slack bot token",
		Description: "Store a Slack bot token for native Slack API access.",
		Secret:      BotTokenSecretRef(ref),
		Header:      coresecret.HeaderSpec{Name: "Authorization", Scheme: "Bearer"},
		SetupFields: []coresecret.SetupFieldSpec{
			{
				Name:        BotTokenPurpose,
				DisplayName: "Bot token",
				Description: "Slack xoxb bot token.",
				Required:    true,
				Sensitive:   true,
				Env:         coresecret.EnvSpec{Name: defaultBotTokenEnv},
			},
			{
				Name:        AppTokenPurpose,
				DisplayName: "App token",
				Description: "Slack xapp app-level token. Required for daemon channels using Socket Mode.",
				Sensitive:   true,
				Env:         coresecret.EnvSpec{Name: defaultAppTokenEnv},
			},
			{
				Name:        UserTokenPurpose,
				DisplayName: "User token",
				Description: "Optional Slack xoxp user token for future user-scoped reads.",
				Sensitive:   true,
				Env:         coresecret.EnvSpec{Name: defaultUserTokenEnv},
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
			Aliases: []string{defaultBotTokenEnv},
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

func resolveStoredOrEnv(ctx context.Context, sys system.System, store runtimesecret.FileStore, ref resource.PluginRef, cfg Config, method string) (Session, error) {
	session := Session{Method: method}
	session.BotToken = loadStoredValue(ctx, store, BotTokenSecretRef(ref))
	session.AppToken = loadStoredValue(ctx, store, AppTokenSecretRef(ref))
	session.UserToken = loadStoredValue(ctx, store, UserTokenSecretRef(ref))
	envSession := envSession(ctx, sys, cfg)
	session.BotToken = firstNonEmpty(session.BotToken, envSession.BotToken)
	session.AppToken = firstNonEmpty(session.AppToken, envSession.AppToken)
	session.UserToken = firstNonEmpty(session.UserToken, envSession.UserToken)
	if session.BotToken == "" {
		return Session{}, fmt.Errorf("slackplugin: bot token is not configured; run agentsdk connect slack --auth %s or configure %s", BotTokenMethod, firstNonEmpty(cfg.Auth.BotTokenEnv, defaultBotTokenEnv))
	}
	return session, nil
}

func resolveEnv(ctx context.Context, sys system.System, cfg Config) (Session, error) {
	session := envSession(ctx, sys, cfg)
	session.Method = EnvMethod
	if session.BotToken == "" {
		return Session{}, fmt.Errorf("slackplugin: bot token environment variable %s is not set", firstNonEmpty(cfg.Auth.BotTokenEnv, defaultBotTokenEnv))
	}
	return session, nil
}

func resolveOAuth(ctx context.Context, sys system.System, store runtimesecret.FileStore, ref resource.PluginRef, cfg Config) (Session, error) {
	session := Session{Method: OAuth2Method}
	session.BotToken = loadStoredValue(ctx, store, OAuth2SecretRef(ref))
	session.AppToken = loadStoredValue(ctx, store, AppTokenSecretRef(ref))
	session.UserToken = loadStoredValue(ctx, store, UserTokenSecretRef(ref))
	envSession := envSession(ctx, sys, cfg)
	session.AppToken = firstNonEmpty(session.AppToken, envSession.AppToken)
	session.UserToken = firstNonEmpty(session.UserToken, envSession.UserToken)
	if session.BotToken == "" {
		return Session{}, fmt.Errorf("slackplugin: oauth2 token is not configured; run agentsdk connect slack --auth %s", OAuth2Method)
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

func loadStoredValue(ctx context.Context, store runtimesecret.FileStore, ref coresecret.Ref) string {
	stored, ok, err := store.LoadSecret(ctx, ref)
	if err != nil || !ok {
		return ""
	}
	return strings.TrimSpace(stored.Value)
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
