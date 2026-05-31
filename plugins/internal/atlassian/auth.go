package atlassian

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	sharedsecret "github.com/fluxplane/fluxplane-secret"
	fpsystem "github.com/fluxplane/fluxplane-system"
	"io"
	"net/http"
	"strings"
	"time"

	auth "github.com/fluxplane/fluxplane-auth"
	"github.com/fluxplane/fluxplane-core/core/resource"
	"github.com/fluxplane/fluxplane-core/runtime/oauth2client"
	"github.com/fluxplane/fluxplane-system/systemkit"
)

const (
	DefaultAuthStorePath = sharedsecret.DefaultFileStorePath

	OAuth2Method   = "oauth2"
	TokenMethod    = "token"
	APITokenMethod = "api_token"

	AccessTokenPurpose = "access_token"

	apiTokenField = "token"
	apiEmailField = "email"
	cloudIDField  = "cloud_id"
	siteURLField  = "site_url"
	baseURLField  = "base_url"

	atlassianAuthorizeURL = "https://auth.atlassian.com/authorize"
	atlassianTokenURL     = "https://auth.atlassian.com/oauth/token"
	accessibleResources   = "https://api.atlassian.com/oauth/token/accessible-resources"
)

// Boundaries are the host capabilities used by Atlassian auth and API helpers.
type Boundaries struct {
	Network fpsystem.Network
}

func BoundariesFromSystem(sys fpsystem.System) Boundaries {
	if sys == nil {
		return Boundaries{}
	}
	return Boundaries{Network: sys.Network()}
}

type Product struct {
	Name         string
	DisplayName  string
	ResourcePath string
	RESTPath     string
	Scopes       []string
}

type Config struct {
	CloudID string     `json:"cloud_id,omitempty" jsonschema:"description=Atlassian cloud id used for OAuth2 or API-token site selection."`
	SiteURL string     `json:"site_url,omitempty" jsonschema:"description=Atlassian site URL, for example https://example.atlassian.net."`
	BaseURL string     `json:"base_url,omitempty" jsonschema:"description=Explicit Atlassian REST API base URL. Usually omitted for cloud apps."`
	Auth    AuthConfig `json:"auth,omitempty" jsonschema:"description=Atlassian authentication source."`
}

type AuthConfig struct {
	Method   string `json:"method,omitempty" jsonschema:"description=Atlassian auth method. token reads a bearer token from the environment; api_token uses email plus API token; oauth2 uses stored OAuth2 token material.,enum=token,enum=api_token,enum=oauth2"`
	TokenEnv string `json:"token_env,omitempty" jsonschema:"description=Environment variable containing the bearer token or API token."`
	Email    string `json:"email,omitempty" jsonschema:"description=Atlassian account email used with api_token authentication."`
	EmailEnv string `json:"email_env,omitempty" jsonschema:"description=Environment variable containing the Atlassian account email used with api_token authentication."`
}

type Session struct {
	Token         string
	Authorization string
	CloudID       string
	SiteURL       string
	SiteName      string
	BaseURL       string
	Method        string
}

type OAuthToken struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token,omitempty"`
	TokenType    string `json:"token_type,omitempty"`
	ExpiresIn    int    `json:"expires_in,omitempty"`
	Scope        string `json:"scope,omitempty"`
}

type Site struct {
	ID   string `json:"id"`
	URL  string `json:"url"`
	Name string `json:"name"`
}

func NormalizeConfig(cfg Config) Config {
	cfg.CloudID = strings.TrimSpace(cfg.CloudID)
	cfg.SiteURL = strings.TrimRight(strings.TrimSpace(cfg.SiteURL), "/")
	cfg.BaseURL = strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	cfg.Auth.Method = strings.ToLower(strings.TrimSpace(cfg.Auth.Method))
	cfg.Auth.TokenEnv = strings.TrimSpace(cfg.Auth.TokenEnv)
	cfg.Auth.Email = strings.TrimSpace(cfg.Auth.Email)
	cfg.Auth.EmailEnv = strings.TrimSpace(cfg.Auth.EmailEnv)
	return cfg
}

func AuthMethods(pluginName string, ref resource.PluginRef, product Product, cfg Config) []auth.MethodSpec {
	cfg = NormalizeConfig(cfg)
	method := cfg.Auth.Method
	var out []auth.MethodSpec
	if method == "" || method == TokenMethod || method == "bearer" || method == "env" {
		out = append(out, tokenAuthMethod(product, cfg.Auth.TokenEnv, cfg.Auth.EmailEnv))
	}
	if method == "" || isAPITokenMethod(method) {
		out = append(out, apiTokenAuthMethod(pluginName, ref, product, cfg))
	}
	if method == "" || method == OAuth2Method {
		out = append(out, oauth2AuthMethod(pluginName, ref, product))
	}
	return out
}

func Resolve(ctx context.Context, sys fpsystem.System, store sharedsecret.FileStore, pluginName string, ref resource.PluginRef, product Product, cfg Config) (Session, error) {
	return ResolveWithBoundaries(ctx, BoundariesFromSystem(sys), store, store, pluginName, ref, product, cfg)
}

func ResolveWithResolver(ctx context.Context, sys fpsystem.System, store sharedsecret.FileStore, resolver sharedsecret.Resolver, pluginName string, ref resource.PluginRef, product Product, cfg Config) (Session, error) {
	return ResolveWithBoundaries(ctx, BoundariesFromSystem(sys), store, resolver, pluginName, ref, product, cfg)
}

func ResolveWithBoundaries(ctx context.Context, boundaries Boundaries, store sharedsecret.FileStore, resolver sharedsecret.Resolver, pluginName string, ref resource.PluginRef, product Product, cfg Config) (Session, error) {
	if resolver == nil {
		resolver = store
	}
	return resolve(ctx, boundaries, store, resolver, pluginName, ref, product, cfg)
}

func resolve(ctx context.Context, boundaries Boundaries, store sharedsecret.FileStore, resolver sharedsecret.Resolver, pluginName string, ref resource.PluginRef, product Product, cfg Config) (Session, error) {
	cfg = NormalizeConfig(cfg)
	method := cfg.Auth.Method
	switch method {
	case "":
		if session, ok, err := resolveAPIToken(ctx, store, resolver, pluginName, ref, product, cfg, false); err != nil || ok {
			return session, err
		}
		if session, ok, err := resolveBearerToken(ctx, boundaries, resolver, pluginName, ref, product, cfg, false); err != nil || ok {
			return session, err
		}
		if session, ok, err := resolveOAuth(ctx, boundaries, store, pluginName, ref, product, cfg); err != nil || ok {
			return session, err
		}
		return Session{}, fmt.Errorf("atlassianplugin: auth token is not configured; set api token fields or one of %s", strings.Join(TokenEnvAliases(product), ", "))
	case OAuth2Method:
		if session, ok, err := resolveOAuth(ctx, boundaries, store, pluginName, ref, product, cfg); err != nil || ok || method == OAuth2Method {
			return session, err
		}
		return Session{}, fmt.Errorf("atlassianplugin: oauth2 auth secret is not configured for instance %s", ref.InstanceName())
	case TokenMethod, "bearer", "env":
		return resolveTokenWithResolver(ctx, boundaries, resolver, pluginName, ref, product, cfg)
	case APITokenMethod, "api-token", "basic":
		session, _, err := resolveAPIToken(ctx, store, resolver, pluginName, ref, product, cfg, true)
		return session, err
	default:
		return Session{}, fmt.Errorf("atlassianplugin: unsupported auth method %q", method)
	}
}

func DoJSON(ctx context.Context, sys fpsystem.System, session Session, method, path string, body any, out any) (int, error) {
	return DoJSONWithNetwork(ctx, BoundariesFromSystem(sys).Network, session, method, path, body, out)
}

func DoJSONWithNetwork(ctx context.Context, network fpsystem.Network, session Session, method, path string, body any, out any) (int, error) {
	if network == nil {
		return 0, fmt.Errorf("atlassianplugin: network is nil")
	}
	u := strings.TrimRight(session.BaseURL, "/") + "/" + strings.TrimLeft(path, "/")
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return 0, err
		}
		reader = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, reader)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Accept", "application/json")
	if strings.TrimSpace(session.Authorization) != "" {
		req.Header.Set("Authorization", session.Authorization)
	} else {
		req.Header.Set("Authorization", "Bearer "+session.Token)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := systemkit.NewHTTPClient(network).Do(req)
	if err != nil {
		return 0, err
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return resp.StatusCode, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return resp.StatusCode, fmt.Errorf("atlassianplugin: %s %s failed: status %d: %s", method, u, resp.StatusCode, strings.TrimSpace(string(data)))
	}
	if out != nil && len(data) > 0 {
		if err := json.Unmarshal(data, out); err != nil {
			return resp.StatusCode, err
		}
	}
	return resp.StatusCode, nil
}

func DiscoverSites(ctx context.Context, client *http.Client, token string) ([]Site, error) {
	if client == nil {
		return nil, fmt.Errorf("atlassianplugin: http client is nil")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, accessibleResources, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(token))
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("atlassianplugin: discover sites failed: status %d: %s", resp.StatusCode, strings.TrimSpace(string(data)))
	}
	var sites []Site
	if err := json.Unmarshal(data, &sites); err != nil {
		return nil, err
	}
	return sites, nil
}

func StoreOAuthToken(ctx context.Context, store sharedsecret.FileStore, pluginName string, ref resource.PluginRef, product Product, token OAuthToken, site Site) error {
	access := strings.TrimSpace(token.AccessToken)
	if access == "" {
		return fmt.Errorf("atlassianplugin: oauth token response has no access token")
	}
	metadata := map[string]string{
		"token_type": strings.TrimSpace(token.TokenType),
		"scope":      strings.TrimSpace(token.Scope),
		"cloud_id":   strings.TrimSpace(site.ID),
		"site_url":   strings.TrimRight(strings.TrimSpace(site.URL), "/"),
		"site_name":  strings.TrimSpace(site.Name),
		"product":    strings.TrimSpace(product.Name),
	}
	expiresAt := time.Time{}
	if token.ExpiresIn > 0 {
		expiresAt = time.Now().UTC().Add(time.Duration(token.ExpiresIn) * time.Second)
	}
	if err := store.SaveSecret(ctx, sharedsecret.StoredSecret{
		Ref:       oauthSecretRef(pluginName, ref),
		Kind:      sharedsecret.KindOAuth2Token,
		Value:     access,
		Metadata:  metadata,
		ExpiresAt: expiresAt,
	}); err != nil {
		return err
	}
	if strings.TrimSpace(token.RefreshToken) == "" {
		return nil
	}
	return store.SaveSecret(ctx, sharedsecret.StoredSecret{
		Ref:   oauthRelatedSecretRef(pluginName, ref, "refresh_token"),
		Kind:  sharedsecret.KindOAuth2Token,
		Value: token.RefreshToken,
	})
}

func RefreshToken(ctx context.Context, client *http.Client, clientID, clientSecret, refreshToken string) (OAuthToken, error) {
	token, err := oauth2client.Refresh(ctx, client, oauth2client.TokenRequest{
		TokenURL:     atlassianTokenURL,
		ClientID:     clientID,
		ClientSecret: clientSecret,
		RefreshToken: refreshToken,
	})
	if err != nil {
		return OAuthToken{}, err
	}
	return OAuthToken{
		AccessToken:  token.AccessToken,
		RefreshToken: token.RefreshToken,
		TokenType:    token.TokenType,
		ExpiresIn:    token.ExpiresIn,
		Scope:        token.Scope,
	}, nil
}

func BaseURL(product Product, cloudID string) string {
	restPath := strings.TrimSpace(product.RESTPath)
	if restPath == "" {
		restPath = "/rest/api/3"
	}
	return "https://api.atlassian.com/ex/" + strings.Trim(strings.TrimSpace(product.ResourcePath), "/") + "/" + strings.TrimSpace(cloudID) + "/" + strings.Trim(restPath, "/")
}

func SiteBaseURL(product Product, siteURL string) string {
	siteURL = strings.TrimRight(strings.TrimSpace(siteURL), "/")
	if siteURL == "" {
		return ""
	}
	restPath := strings.TrimSpace(product.RESTPath)
	if restPath == "" {
		restPath = "/rest/api/3"
	}
	return siteURL + "/" + strings.Trim(restPath, "/")
}

func resolveOAuth(ctx context.Context, boundaries Boundaries, store sharedsecret.FileStore, pluginName string, ref resource.PluginRef, product Product, cfg Config) (Session, bool, error) {
	stored, ok, err := store.LoadSecret(ctx, oauthSecretRef(pluginName, ref))
	if err != nil || !ok {
		return Session{}, false, err
	}
	session := sessionFromStored(product, cfg, stored)
	if session.Token == "" {
		return Session{}, false, nil
	}
	if needsRefresh(stored) {
		refreshed, refreshedStored, err := refreshStored(ctx, boundaries, store, pluginName, ref, product, stored)
		if err != nil {
			return Session{}, true, err
		}
		stored = refreshedStored
		session = sessionFromStored(product, cfg, stored)
		session.Token = refreshed.AccessToken
	}
	if session.BaseURL == "" && cfg.BaseURL == "" {
		var err error
		_, session, err = discoverAndStoreSite(ctx, boundaries, store, pluginName, ref, product, cfg, stored)
		if err != nil {
			return Session{}, true, err
		}
	}
	if session.BaseURL == "" {
		return Session{}, true, fmt.Errorf("atlassianplugin: cloud_id is required for %s", product.DisplayName)
	}
	session.Method = OAuth2Method
	return session, true, nil
}

func resolveTokenWithResolver(ctx context.Context, boundaries Boundaries, resolver sharedsecret.Resolver, pluginName string, ref resource.PluginRef, product Product, cfg Config) (Session, error) {
	session, _, err := resolveBearerToken(ctx, boundaries, resolver, pluginName, ref, product, cfg, true)
	return session, err
}

func resolveBearerToken(ctx context.Context, boundaries Boundaries, resolver sharedsecret.Resolver, pluginName string, ref resource.PluginRef, product Product, cfg Config, required bool) (Session, bool, error) {
	if resolver == nil {
		return Session{}, false, fmt.Errorf("atlassianplugin: secret resolver is nil")
	}
	broker := auth.NewBroker(resolver)
	methods := []auth.MethodSpec{tokenAuthMethod(product, cfg.Auth.TokenEnv, cfg.Auth.EmailEnv)}
	resolution, ok, err := broker.UseAvailable(ctx, auth.Request{
		Plugin:   pluginName,
		Instance: ref.InstanceName(),
		Purpose:  AccessTokenPurpose,
		Methods:  methods,
	})
	if err != nil {
		return Session{}, false, fmt.Errorf("atlassianplugin: use token auth secret: %w", err)
	}
	if !ok || strings.TrimSpace(resolution.Material.String()) == "" {
		if !required {
			return Session{}, false, nil
		}
		if cfg.Auth.TokenEnv == "" {
			return Session{}, false, fmt.Errorf("atlassianplugin: auth token is not configured; set auth.token_env to one of %s", strings.Join(BearerTokenEnvAliases(product), ", "))
		}
		return Session{}, false, fmt.Errorf("atlassianplugin: auth token is not configured; set %s", cfg.Auth.TokenEnv)
	}
	token := strings.TrimSpace(resolution.Material.String())
	cloudID, siteURL, baseURL, err := resolveSiteLocatorFields(ctx, resolver, pluginName, ref, methods[0], cfg)
	if err != nil {
		return Session{}, false, err
	}
	sessionCfg := cfg
	sessionCfg.CloudID = cloudID
	sessionCfg.SiteURL = siteURL
	sessionCfg.BaseURL = baseURL
	session := Session{
		Token:   token,
		CloudID: cloudID,
		SiteURL: siteURL,
		BaseURL: bearerBaseURLFromConfig(product, sessionCfg),
		Method:  TokenMethod,
	}
	if session.BaseURL == "" {
		return Session{}, true, fmt.Errorf("atlassianplugin: cloud_id or base_url is required for %s token auth", product.DisplayName)
	}
	session = discoverTokenSiteURL(ctx, boundaries.Network, product, session)
	return session, true, nil
}

func resolveAPIToken(ctx context.Context, store sharedsecret.FileStore, resolver sharedsecret.Resolver, pluginName string, ref resource.PluginRef, product Product, cfg Config, required bool) (Session, bool, error) {
	method := apiTokenAuthMethod(pluginName, ref, product, cfg)
	if resolver == nil {
		resolver = store
	}
	broker := auth.NewBroker(resolver)
	if _, _, err := broker.Use(ctx, auth.Request{Plugin: pluginName, Instance: ref.InstanceName(), Purpose: AccessTokenPurpose}.SecretRef()); err != nil {
		return Session{}, false, fmt.Errorf("atlassianplugin: use api token auth secret: %w", err)
	}
	token, _, err := resolveSetupField(ctx, resolver, pluginName, ref, method, apiTokenField)
	if err != nil {
		return Session{}, false, err
	}
	email := strings.TrimSpace(cfg.Auth.Email)
	if email == "" {
		var ok bool
		email, ok, err = resolveSetupField(ctx, resolver, pluginName, ref, method, apiEmailField)
		if err != nil {
			return Session{}, false, err
		}
		if !ok {
			email = ""
		}
	}
	token = strings.TrimSpace(token)
	email = strings.TrimSpace(email)
	cloudID, siteURL, baseURL, err := resolveSiteLocatorFields(ctx, resolver, pluginName, ref, method, cfg)
	if err != nil {
		return Session{}, false, err
	}
	if token == "" || email == "" {
		if !required {
			return Session{}, false, nil
		}
		if token == "" && email == "" {
			return Session{}, false, fmt.Errorf("atlassianplugin: api token auth is not configured; set fields %q and %q", apiEmailField, apiTokenField)
		}
		if token == "" {
			return Session{}, false, fmt.Errorf("atlassianplugin: api token is not configured; set field %q or one of %s", apiTokenField, strings.Join(TokenEnvAliases(product), ", "))
		}
		return Session{}, false, fmt.Errorf("atlassianplugin: account email is not configured; set field %q, auth.email, auth.email_env, or one of %s", apiEmailField, strings.Join(EmailEnvAliases(product), ", "))
	}
	sessionCfg := cfg
	sessionCfg.CloudID = cloudID
	sessionCfg.SiteURL = siteURL
	sessionCfg.BaseURL = baseURL
	baseURL = apiTokenBaseURLFromConfig(product, sessionCfg)
	if baseURL == "" {
		if !required {
			return Session{}, false, nil
		}
		return Session{}, true, fmt.Errorf("atlassianplugin: site_url or base_url is required for %s API token auth", product.DisplayName)
	}
	return Session{
		Token:         token,
		Authorization: basicAuthorization(email, token),
		CloudID:       cloudID,
		SiteURL:       siteURL,
		BaseURL:       strings.TrimRight(baseURL, "/"),
		Method:        APITokenMethod,
	}, true, nil
}

func tokenAuthMethod(product Product, tokenEnv, emailEnv string) auth.MethodSpec {
	return auth.MethodSpec{
		Name:        TokenMethod,
		Method:      auth.MethodEnv,
		Kind:        sharedsecret.KindBearerToken,
		DisplayName: product.DisplayName + " scoped access token",
		Description: product.DisplayName + " scoped Atlassian access token resolved from an environment variable and sent to the Atlassian cloud gateway.",
		Annotations: map[string]string{
			"auth_scheme": "Bearer",
			"endpoint":    "api.atlassian.com/ex/{product}/{cloud_id}",
			"token_type":  "Atlassian scoped API/access token",
		},
		Env: auth.EnvSpec{
			Name:    strings.TrimSpace(tokenEnv),
			Aliases: bearerTokenEnvAliases(tokenEnv, product),
		},
		Header: auth.HeaderSpec{Name: "Authorization", Scheme: "Bearer"},
		SetupFields: []auth.FieldSpec{
			{
				Slot:        TokenMethod,
				DisplayName: "Scoped access token",
				Sensitive:   true,
				Env: auth.EnvSpec{
					Name:    strings.TrimSpace(tokenEnv),
					Aliases: bearerTokenEnvAliases(tokenEnv, product),
				},
			},
			{
				Slot:        "email_env",
				DisplayName: "Email env",
				Description: "Environment variable containing the Atlassian account email when using Basic API-token auth.",
				Env: auth.EnvSpec{
					Name:    strings.TrimSpace(emailEnv),
					Aliases: EmailEnvAliases(product),
				},
			},
			{
				Slot:          cloudIDField,
				DisplayName:   "Cloud ID",
				Description:   "Atlassian cloud/site ID for the cloud gateway.",
				RequiredGroup: "site_locator",
				Env: auth.EnvSpec{
					Aliases: CloudIDEnvAliases(product),
				},
			},
			{
				Slot:          baseURLField,
				DisplayName:   "Base URL",
				Description:   "Full REST API base URL override.",
				RequiredGroup: "site_locator",
				Env: auth.EnvSpec{
					Aliases: BaseURLEnvAliases(product),
				},
			},
			{
				Slot:        siteURLField,
				DisplayName: "Site URL",
				Description: "Optional Atlassian site URL used for human-facing links.",
				Env: auth.EnvSpec{
					Aliases: SiteURLEnvAliases(product),
				},
			},
		},
	}
}

func apiTokenAuthMethod(pluginName string, ref resource.PluginRef, product Product, cfg Config) auth.MethodSpec {
	tokenEnv := auth.EnvSpec{Aliases: TokenEnvAliases(product)}
	if name := strings.TrimSpace(cfg.Auth.TokenEnv); name != "" {
		tokenEnv = auth.EnvSpec{Name: name}
	}
	emailEnv := auth.EnvSpec{Aliases: EmailEnvAliases(product)}
	if name := strings.TrimSpace(cfg.Auth.EmailEnv); name != "" {
		emailEnv = auth.EnvSpec{Name: name}
	}
	locatorRequired := apiTokenBaseURLFromConfig(product, cfg) == ""
	return auth.MethodSpec{
		Name:        APITokenMethod,
		Method:      auth.MethodStored,
		Kind:        sharedsecret.KindBasic,
		DisplayName: product.DisplayName + " Basic API token",
		Description: product.DisplayName + " account email plus Atlassian API token sent as Basic auth to the site REST endpoint.",
		Annotations: map[string]string{
			"auth_scheme": "Basic",
			"endpoint":    "site_url or base_url",
			"token_type":  "Atlassian account API token",
		},
		Secret: sharedsecret.Plugin(pluginName, ref.InstanceName(), apiTokenField),
		Header: auth.HeaderSpec{Name: "Authorization", Scheme: "Basic"},
		SetupFields: []auth.FieldSpec{
			{
				Slot:        apiEmailField,
				DisplayName: "Atlassian email",
				Required:    strings.TrimSpace(cfg.Auth.Email) == "",
				Env:         emailEnv,
			},
			{
				Slot:        apiTokenField,
				DisplayName: "Atlassian API token",
				Required:    true,
				Sensitive:   true,
				Env:         tokenEnv,
			},
			{
				Slot:        cloudIDField,
				DisplayName: "Cloud ID",
				Description: "Atlassian cloud/site ID.",
				Env: auth.EnvSpec{
					Aliases: CloudIDEnvAliases(product),
				},
			},
			{
				Slot:          siteURLField,
				DisplayName:   "Site URL",
				Description:   "Atlassian site URL.",
				RequiredGroup: requiredGroup(locatorRequired, "site_locator"),
				Env: auth.EnvSpec{
					Aliases: SiteURLEnvAliases(product),
				},
			},
			{
				Slot:          baseURLField,
				DisplayName:   "Base URL",
				Description:   "Full Atlassian REST API base URL.",
				RequiredGroup: requiredGroup(locatorRequired, "site_locator"),
				Env: auth.EnvSpec{
					Aliases: BaseURLEnvAliases(product),
				},
			},
		},
	}
}

func oauth2AuthMethod(pluginName string, ref resource.PluginRef, product Product) auth.MethodSpec {
	return auth.MethodSpec{
		Name:        OAuth2Method,
		Method:      auth.MethodOAuth2AuthCode,
		Kind:        sharedsecret.KindOAuth2Token,
		DisplayName: product.DisplayName + " OAuth2",
		Description: product.DisplayName + " Atlassian OAuth2 authorization-code credentials stored for this plugin instance.",
		Annotations: map[string]string{
			"auth_scheme": "Bearer",
			"endpoint":    "api.atlassian.com/ex/{product}/{cloud_id}",
			"token_type":  "OAuth2 access token",
		},
		Secret: oauthSecretRef(pluginName, ref),
		Header: auth.HeaderSpec{Name: "Authorization", Scheme: "Bearer"},
		OAuth2: auth.OAuth2Spec{
			AuthorizeURL: atlassianAuthorizeURL,
			TokenURL:     atlassianTokenURL,
			RefreshURL:   atlassianTokenURL,
			Scopes:       product.Scopes,
			ExtraParams: map[string]string{
				"audience": "api.atlassian.com",
				"prompt":   "consent",
			},
		},
		SetupFields: []auth.FieldSpec{
			{
				Slot:        "client_id",
				DisplayName: "Client ID",
				Required:    true,
				Env: auth.EnvSpec{
					Aliases: ClientIDEnvAliases(product),
				},
			},
			{
				Slot:        "client_secret",
				DisplayName: "Client Secret",
				Required:    true,
				Sensitive:   true,
				Env: auth.EnvSpec{
					Aliases: ClientSecretEnvAliases(product),
				},
			},
			{
				Slot:        "cloud_id",
				DisplayName: "Cloud ID",
				Description: "Optional Atlassian cloud/site ID. If omitted, the first accessible site is discovered on first use.",
				Env: auth.EnvSpec{
					Aliases: CloudIDEnvAliases(product),
				},
			},
			{
				Slot:        "site_url",
				DisplayName: "Site URL",
				Description: "Optional Atlassian site URL used for human-facing links.",
				Env: auth.EnvSpec{
					Aliases: SiteURLEnvAliases(product),
				},
			},
		},
	}
}

func oauthSecretRef(pluginName string, ref resource.PluginRef) sharedsecret.Ref {
	return sharedsecret.Plugin(pluginName, ref.InstanceName(), sharedsecret.Slot(OAuth2Method+"_token"))
}

func OAuthSecretRef(pluginName string, ref resource.PluginRef) sharedsecret.Ref {
	return oauthSecretRef(pluginName, ref)
}

func oauthRelatedSecretRef(pluginName string, ref resource.PluginRef, name string) sharedsecret.Ref {
	return sharedsecret.Plugin(pluginName, ref.InstanceName(), sharedsecret.Slot(OAuth2Method+"_"+strings.TrimSpace(name)))
}

func TokenEnvAliases(product Product) []string {
	prefix := strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(product.Name), "-", "_"))
	if prefix == "" {
		prefix = "ATLASSIAN"
	}
	return []string{prefix + "_API_TOKEN", prefix + "_TOKEN", prefix + "_ACCESS_TOKEN", "ATLASSIAN_API_TOKEN", "ATLASSIAN_TOKEN", "ATLASSIAN_ACCESS_TOKEN"}
}

func BearerTokenEnvAliases(product Product) []string {
	prefix := strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(product.Name), "-", "_"))
	if prefix == "" {
		prefix = "ATLASSIAN"
	}
	return []string{prefix + "_TOKEN", prefix + "_ACCESS_TOKEN", "ATLASSIAN_TOKEN", "ATLASSIAN_ACCESS_TOKEN"}
}

func bearerTokenEnvAliases(tokenEnv string, product Product) []string {
	if strings.TrimSpace(tokenEnv) != "" {
		return nil
	}
	return BearerTokenEnvAliases(product)
}

func EmailEnvAliases(product Product) []string {
	prefix := strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(product.Name), "-", "_"))
	if prefix == "" {
		prefix = "ATLASSIAN"
	}
	return []string{prefix + "_EMAIL", prefix + "_USER_EMAIL", prefix + "_ACCOUNT_EMAIL", "ATLASSIAN_EMAIL", "ATLASSIAN_USER_EMAIL", "ATLASSIAN_ACCOUNT_EMAIL"}
}

func ClientIDEnvAliases(product Product) []string {
	prefix := strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(product.Name), "-", "_"))
	if prefix == "" {
		prefix = "ATLASSIAN"
	}
	return []string{prefix + "_CLIENT_ID", "ATLASSIAN_CLIENT_ID"}
}

func ClientSecretEnvAliases(product Product) []string {
	prefix := strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(product.Name), "-", "_"))
	if prefix == "" {
		prefix = "ATLASSIAN"
	}
	return []string{prefix + "_CLIENT_SECRET", "ATLASSIAN_CLIENT_SECRET"}
}

func CloudIDEnvAliases(product Product) []string {
	prefix := strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(product.Name), "-", "_"))
	if prefix == "" {
		prefix = "ATLASSIAN"
	}
	return []string{prefix + "_CLOUD_ID", "ATLASSIAN_CLOUD_ID"}
}

func SiteURLEnvAliases(product Product) []string {
	prefix := strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(product.Name), "-", "_"))
	if prefix == "" {
		prefix = "ATLASSIAN"
	}
	return []string{prefix + "_SITE_URL", "ATLASSIAN_SITE_URL"}
}

func BaseURLEnvAliases(product Product) []string {
	prefix := strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(product.Name), "-", "_"))
	if prefix == "" {
		prefix = "ATLASSIAN"
	}
	return []string{prefix + "_BASE_URL", "ATLASSIAN_BASE_URL"}
}

func discoverTokenSiteURL(ctx context.Context, network fpsystem.Network, product Product, session Session) Session {
	if strings.TrimSpace(session.SiteURL) != "" || strings.TrimSpace(session.CloudID) == "" || strings.TrimSpace(session.Token) == "" {
		return session
	}
	if network == nil {
		return session
	}
	sites, err := DiscoverSites(ctx, systemkit.NewHTTPClient(network), session.Token)
	if err != nil {
		return session
	}
	cloudID := strings.TrimSpace(session.CloudID)
	for _, site := range sites {
		if strings.TrimSpace(site.ID) != cloudID {
			continue
		}
		session.SiteURL = strings.TrimRight(strings.TrimSpace(site.URL), "/")
		session.SiteName = strings.TrimSpace(site.Name)
		if strings.TrimSpace(session.BaseURL) == "" {
			session.BaseURL = BaseURL(product, cloudID)
		}
		return session
	}
	return session
}

func sessionFromStored(product Product, cfg Config, stored sharedsecret.StoredSecret) Session {
	metadata := stored.Metadata
	cloudID := firstNonEmpty(cfg.CloudID, metadata["cloud_id"])
	siteURL := firstNonEmpty(cfg.SiteURL, metadata["site_url"])
	baseURL := firstNonEmpty(cfg.BaseURL, BaseURL(product, cloudID))
	if cloudID == "" && cfg.BaseURL == "" {
		baseURL = ""
	}
	return Session{
		Token:    stored.Value,
		CloudID:  cloudID,
		SiteURL:  strings.TrimRight(siteURL, "/"),
		SiteName: metadata["site_name"],
		BaseURL:  strings.TrimRight(baseURL, "/"),
	}
}

func needsRefresh(stored sharedsecret.StoredSecret) bool {
	if stored.ExpiresAt.IsZero() {
		return false
	}
	return time.Now().UTC().Add(2 * time.Minute).After(stored.ExpiresAt)
}

func refreshStored(ctx context.Context, boundaries Boundaries, store sharedsecret.FileStore, pluginName string, ref resource.PluginRef, product Product, stored sharedsecret.StoredSecret) (OAuthToken, sharedsecret.StoredSecret, error) {
	refreshStored, ok, err := store.LoadSecret(ctx, oauthRelatedSecretRef(pluginName, ref, "refresh_token"))
	if err != nil {
		return OAuthToken{}, stored, err
	}
	clientSecretStored, secretOK, err := store.LoadSecret(ctx, oauthRelatedSecretRef(pluginName, ref, "client_secret"))
	if err != nil {
		return OAuthToken{}, stored, err
	}
	refresh := ""
	if ok {
		refresh = strings.TrimSpace(refreshStored.Value)
	}
	clientID := strings.TrimSpace(stored.Metadata["client_id"])
	clientSecret := ""
	if secretOK {
		clientSecret = strings.TrimSpace(clientSecretStored.Value)
	}
	if refresh == "" || clientID == "" || clientSecret == "" {
		return OAuthToken{}, stored, fmt.Errorf("atlassianplugin: oauth token expired and refresh credentials are unavailable")
	}
	if boundaries.Network == nil {
		return OAuthToken{}, stored, fmt.Errorf("atlassianplugin: network is nil")
	}
	if boundaries.Network == nil {
		return OAuthToken{}, stored, fmt.Errorf("atlassianplugin: network is nil")
	}
	token, err := RefreshToken(ctx, systemkit.NewHTTPClient(boundaries.Network), clientID, clientSecret, refresh)
	if err != nil {
		return OAuthToken{}, stored, err
	}
	if token.RefreshToken == "" {
		token.RefreshToken = refresh
	}
	site := Site{ID: stored.Metadata["cloud_id"], URL: stored.Metadata["site_url"], Name: stored.Metadata["site_name"]}
	if err := StoreOAuthToken(ctx, store, pluginName, ref, product, token, site); err != nil {
		return OAuthToken{}, stored, err
	}
	next, ok, err := store.LoadSecret(ctx, oauthSecretRef(pluginName, ref))
	if err != nil || !ok {
		return OAuthToken{}, stored, err
	}
	if next.Metadata == nil {
		next.Metadata = map[string]string{}
	}
	next.Metadata["client_id"] = clientID
	if err := store.SaveSecret(ctx, next); err != nil {
		return OAuthToken{}, stored, err
	}
	return token, next, nil
}

func discoverAndStoreSite(ctx context.Context, boundaries Boundaries, store sharedsecret.FileStore, pluginName string, ref resource.PluginRef, product Product, cfg Config, stored sharedsecret.StoredSecret) (sharedsecret.StoredSecret, Session, error) {
	if boundaries.Network == nil {
		return stored, Session{}, fmt.Errorf("atlassianplugin: network is nil")
	}
	if boundaries.Network == nil {
		return stored, Session{}, fmt.Errorf("atlassianplugin: network is nil")
	}
	sites, err := DiscoverSites(ctx, systemkit.NewHTTPClient(boundaries.Network), stored.Value)
	if err != nil {
		return stored, Session{}, err
	}
	if len(sites) == 0 {
		return stored, Session{}, fmt.Errorf("atlassianplugin: Atlassian account returned no accessible %s sites", product.DisplayName)
	}
	site := sites[0]
	if stored.Metadata == nil {
		stored.Metadata = map[string]string{}
	}
	stored.Metadata["cloud_id"] = strings.TrimSpace(site.ID)
	stored.Metadata["site_url"] = strings.TrimRight(strings.TrimSpace(site.URL), "/")
	stored.Metadata["site_name"] = strings.TrimSpace(site.Name)
	stored.Metadata["product"] = strings.TrimSpace(product.Name)
	if err := store.SaveSecret(ctx, stored); err != nil {
		return stored, Session{}, err
	}
	return stored, sessionFromStored(product, cfg, stored), nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func isAPITokenMethod(method string) bool {
	switch strings.ReplaceAll(strings.ToLower(strings.TrimSpace(method)), "-", "_") {
	case APITokenMethod, "basic":
		return true
	default:
		return false
	}
}

func resolveSetupField(ctx context.Context, resolver sharedsecret.Resolver, pluginName string, ref resource.PluginRef, method auth.MethodSpec, name string) (string, bool, error) {
	field, ok := setupField(method.SetupFields, name)
	if !ok {
		return "", false, nil
	}
	refs := []sharedsecret.Ref{sharedsecret.Plugin(pluginName, ref.InstanceName(), sharedsecret.Slot(name))}
	refs = append(refs, envRefs(field.Env)...)
	for _, candidate := range refs {
		material, found, err := resolver.ResolveSecret(ctx, candidate)
		if err != nil || found {
			return strings.TrimSpace(material.String()), found, err
		}
	}
	return "", false, nil
}

func resolveSiteLocatorFields(ctx context.Context, resolver sharedsecret.Resolver, pluginName string, ref resource.PluginRef, method auth.MethodSpec, cfg Config) (string, string, string, error) {
	cloudID := strings.TrimSpace(cfg.CloudID)
	siteURL := strings.TrimRight(strings.TrimSpace(cfg.SiteURL), "/")
	baseURL := strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	if cloudID == "" {
		value, _, err := resolveSetupField(ctx, resolver, pluginName, ref, method, cloudIDField)
		if err != nil {
			return "", "", "", err
		}
		cloudID = strings.TrimSpace(value)
	}
	if siteURL == "" {
		value, _, err := resolveSetupField(ctx, resolver, pluginName, ref, method, siteURLField)
		if err != nil {
			return "", "", "", err
		}
		siteURL = strings.TrimRight(strings.TrimSpace(value), "/")
	}
	if baseURL == "" {
		value, _, err := resolveSetupField(ctx, resolver, pluginName, ref, method, baseURLField)
		if err != nil {
			return "", "", "", err
		}
		baseURL = strings.TrimRight(strings.TrimSpace(value), "/")
	}
	return cloudID, siteURL, baseURL, nil
}

func requiredGroup(required bool, group string) string {
	if !required {
		return ""
	}
	return strings.TrimSpace(group)
}

func apiTokenBaseURLFromConfig(product Product, cfg Config) string {
	if baseURL := firstNonEmpty(cfg.BaseURL); baseURL != "" {
		return strings.TrimRight(baseURL, "/")
	}
	if siteURL := SiteBaseURL(product, cfg.SiteURL); siteURL != "" {
		return strings.TrimRight(siteURL, "/")
	}
	return ""
}

func bearerBaseURLFromConfig(product Product, cfg Config) string {
	if baseURL := firstNonEmpty(cfg.BaseURL); baseURL != "" {
		return strings.TrimRight(baseURL, "/")
	}
	if cloudID := strings.TrimSpace(cfg.CloudID); cloudID != "" {
		return strings.TrimRight(BaseURL(product, cloudID), "/")
	}
	if siteURL := SiteBaseURL(product, cfg.SiteURL); siteURL != "" {
		return strings.TrimRight(siteURL, "/")
	}
	return ""
}

func setupField(fields []auth.FieldSpec, name string) (auth.FieldSpec, bool) {
	for _, field := range fields {
		if strings.EqualFold(strings.TrimSpace(string(field.Slot)), strings.TrimSpace(name)) {
			return field, true
		}
	}
	return auth.FieldSpec{}, false
}

func envRefs(spec auth.EnvSpec) []sharedsecret.Ref {
	names := append([]string{spec.Name}, spec.Aliases...)
	refs := make([]sharedsecret.Ref, 0, len(names))
	seen := map[string]bool{}
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		refs = append(refs, sharedsecret.Env(name))
	}
	return refs
}

func basicAuthorization(email, token string) string {
	raw := strings.TrimSpace(email) + ":" + strings.TrimSpace(token)
	return "Basic " + base64.StdEncoding.EncodeToString([]byte(raw))
}
