package atlassian

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/fluxplane/agentruntime/core/resource"
	coresecret "github.com/fluxplane/agentruntime/core/secret"
	"github.com/fluxplane/agentruntime/runtime/oauth2client"
	runtimesecret "github.com/fluxplane/agentruntime/runtime/secret"
	"github.com/fluxplane/agentruntime/runtime/system"
)

const (
	DefaultAuthStorePath = runtimesecret.DefaultFileStorePath

	OAuth2Method   = "oauth2"
	TokenMethod    = "token"
	APITokenMethod = "api_token"

	AccessTokenPurpose = "access_token"

	atlassianAuthorizeURL = "https://auth.atlassian.com/authorize"
	atlassianTokenURL     = "https://auth.atlassian.com/oauth/token"
	accessibleResources   = "https://api.atlassian.com/oauth/token/accessible-resources"
)

type Product struct {
	Name         string
	DisplayName  string
	ResourcePath string
	RESTPath     string
	Scopes       []string
}

type Config struct {
	CloudID string     `json:"cloud_id,omitempty"`
	SiteURL string     `json:"site_url,omitempty"`
	BaseURL string     `json:"base_url,omitempty"`
	Auth    AuthConfig `json:"auth,omitempty"`
}

type AuthConfig struct {
	Method   string `json:"method,omitempty"`
	TokenEnv string `json:"token_env,omitempty"`
	Email    string `json:"email,omitempty"`
	EmailEnv string `json:"email_env,omitempty"`
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

func AuthMethods(pluginName string, ref resource.PluginRef, product Product, cfg Config) []coresecret.AuthMethodSpec {
	cfg = NormalizeConfig(cfg)
	method := cfg.Auth.Method
	var out []coresecret.AuthMethodSpec
	if method == "" || method == TokenMethod || method == "bearer" || method == "env" || isAPITokenMethod(method) {
		out = append(out, tokenAuthMethod(product, cfg.Auth.TokenEnv, cfg.Auth.EmailEnv))
	}
	if method == "" || method == OAuth2Method {
		out = append(out, oauth2AuthMethod(pluginName, ref, product))
	}
	return out
}

func Resolve(ctx context.Context, sys system.System, store runtimesecret.FileStore, pluginName string, ref resource.PluginRef, product Product, cfg Config) (Session, error) {
	cfg = NormalizeConfig(cfg)
	if sys == nil {
		return Session{}, fmt.Errorf("atlassianplugin: system is nil")
	}
	method := cfg.Auth.Method
	switch method {
	case "", OAuth2Method:
		if session, ok, err := resolveOAuth(ctx, sys, store, pluginName, ref, product, cfg); err != nil || ok || method == OAuth2Method {
			return session, err
		}
		return resolveToken(ctx, sys, pluginName, ref, product, cfg)
	case TokenMethod, "bearer", "env", APITokenMethod, "api-token", "basic":
		return resolveToken(ctx, sys, pluginName, ref, product, cfg)
	default:
		return Session{}, fmt.Errorf("atlassianplugin: unsupported auth method %q", method)
	}
}

func DoJSON(ctx context.Context, sys system.System, session Session, method, path string, body any, out any) (int, error) {
	if sys == nil || sys.Network() == nil {
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
	resp, err := system.NewHTTPClient(sys.Network()).Do(req)
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

func StoreOAuthToken(ctx context.Context, store runtimesecret.FileStore, pluginName string, ref resource.PluginRef, product Product, token OAuthToken, site Site) error {
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
	if err := store.SaveSecret(ctx, runtimesecret.StoredSecret{
		Ref:       oauthSecretRef(pluginName, ref),
		Kind:      coresecret.KindOAuth2Token,
		Value:     access,
		Metadata:  metadata,
		ExpiresAt: expiresAt,
	}); err != nil {
		return err
	}
	if strings.TrimSpace(token.RefreshToken) == "" {
		return nil
	}
	return store.SaveSecret(ctx, runtimesecret.StoredSecret{
		Ref:   oauthRelatedSecretRef(pluginName, ref, "refresh_token"),
		Kind:  coresecret.KindOAuth2Token,
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

func resolveOAuth(ctx context.Context, sys system.System, store runtimesecret.FileStore, pluginName string, ref resource.PluginRef, product Product, cfg Config) (Session, bool, error) {
	stored, ok, err := store.LoadSecret(ctx, oauthSecretRef(pluginName, ref))
	if err != nil || !ok {
		return Session{}, false, err
	}
	session := sessionFromStored(product, cfg, stored)
	if session.Token == "" {
		return Session{}, false, nil
	}
	if needsRefresh(stored) {
		refreshed, refreshedStored, err := refreshStored(ctx, sys, store, pluginName, ref, product, stored)
		if err != nil {
			return Session{}, true, err
		}
		stored = refreshedStored
		session = sessionFromStored(product, cfg, stored)
		session.Token = refreshed.AccessToken
	}
	if session.BaseURL == "" && cfg.BaseURL == "" {
		var err error
		_, session, err = discoverAndStoreSite(ctx, sys, store, pluginName, ref, product, cfg, stored)
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

func resolveToken(ctx context.Context, sys system.System, pluginName string, ref resource.PluginRef, product Product, cfg Config) (Session, error) {
	if sys.Environment() == nil {
		return Session{}, fmt.Errorf("atlassianplugin: system environment is nil")
	}
	broker := runtimesecret.NewBroker(runtimesecret.EnvResolver{Environment: sys.Environment(), Kind: coresecret.KindBearerToken})
	methods := []coresecret.AuthMethodSpec{tokenAuthMethod(product, cfg.Auth.TokenEnv, cfg.Auth.EmailEnv)}
	resolution, ok, err := broker.UseAvailable(ctx, coresecret.AuthRequest{
		Plugin:   pluginName,
		Instance: ref.InstanceName(),
		Purpose:  AccessTokenPurpose,
		Methods:  methods,
	})
	if err != nil {
		return Session{}, fmt.Errorf("atlassianplugin: use token auth secret: %w", err)
	}
	if !ok || strings.TrimSpace(resolution.Material.Value) == "" {
		if cfg.Auth.TokenEnv == "" {
			return Session{}, fmt.Errorf("atlassianplugin: auth token is not configured; set auth.token_env to one of %s", strings.Join(TokenEnvAliases(product), ", "))
		}
		return Session{}, fmt.Errorf("atlassianplugin: auth token is not configured; set %s", cfg.Auth.TokenEnv)
	}
	token := strings.TrimSpace(resolution.Material.Value)
	if shouldUseBasicTokenAuth(cfg) {
		email, err := resolveAPIEmail(ctx, sys, product, cfg)
		if err != nil {
			return Session{}, err
		}
		baseURL := firstNonEmpty(cfg.BaseURL, SiteBaseURL(product, cfg.SiteURL))
		if baseURL == "" {
			return Session{}, fmt.Errorf("atlassianplugin: site_url or base_url is required for %s API token auth", product.DisplayName)
		}
		return Session{
			Token:         token,
			Authorization: basicAuthorization(email, token),
			CloudID:       cfg.CloudID,
			SiteURL:       cfg.SiteURL,
			BaseURL:       strings.TrimRight(baseURL, "/"),
			Method:        "basic",
		}, nil
	}
	session := Session{
		Token:   token,
		CloudID: cfg.CloudID,
		SiteURL: cfg.SiteURL,
		BaseURL: firstNonEmpty(cfg.BaseURL, BaseURL(product, cfg.CloudID)),
		Method:  TokenMethod,
	}
	if session.BaseURL == "" || strings.Contains(session.BaseURL, "//rest/") {
		return Session{}, fmt.Errorf("atlassianplugin: cloud_id or base_url is required for %s token auth", product.DisplayName)
	}
	session = discoverTokenSiteURL(ctx, sys, product, session)
	return session, nil
}

func tokenAuthMethod(product Product, tokenEnv, emailEnv string) coresecret.AuthMethodSpec {
	return coresecret.AuthMethodSpec{
		Name:        TokenMethod,
		Method:      coresecret.AuthMethodEnv,
		Kind:        coresecret.KindBearerToken,
		DisplayName: product.DisplayName + " token",
		Description: product.DisplayName + " bearer token or Atlassian API token resolved from a configured environment variable or known aliases.",
		Env: coresecret.EnvSpec{
			Name:    strings.TrimSpace(tokenEnv),
			Aliases: TokenEnvAliases(product),
		},
		Header: coresecret.HeaderSpec{Name: "Authorization", Scheme: "Bearer"},
		SetupFields: []coresecret.SetupFieldSpec{
			{
				Name:        "email_env",
				DisplayName: "Email env",
				Description: "Environment variable containing the Atlassian account email when using Basic API-token auth.",
				Env: coresecret.EnvSpec{
					Name:    strings.TrimSpace(emailEnv),
					Aliases: EmailEnvAliases(product),
				},
			},
		},
	}
}

func oauth2AuthMethod(pluginName string, ref resource.PluginRef, product Product) coresecret.AuthMethodSpec {
	return coresecret.AuthMethodSpec{
		Name:        OAuth2Method,
		Method:      coresecret.AuthMethodOAuth2,
		Kind:        coresecret.KindOAuth2Token,
		DisplayName: product.DisplayName + " OAuth2",
		Description: product.DisplayName + " Atlassian OAuth2 authorization-code credentials stored for this plugin instance.",
		Secret:      oauthSecretRef(pluginName, ref),
		Header:      coresecret.HeaderSpec{Name: "Authorization", Scheme: "Bearer"},
		OAuth2: coresecret.OAuth2Spec{
			AuthorizeURL: atlassianAuthorizeURL,
			TokenURL:     atlassianTokenURL,
			RefreshURL:   atlassianTokenURL,
			Scopes:       product.Scopes,
			ExtraParams: map[string]string{
				"audience": "api.atlassian.com",
				"prompt":   "consent",
			},
		},
		SetupFields: []coresecret.SetupFieldSpec{
			{
				Name:        "client_id",
				DisplayName: "Client ID",
				Required:    true,
				Env: coresecret.EnvSpec{
					Aliases: ClientIDEnvAliases(product),
				},
			},
			{
				Name:        "client_secret",
				DisplayName: "Client Secret",
				Required:    true,
				Sensitive:   true,
				Env: coresecret.EnvSpec{
					Aliases: ClientSecretEnvAliases(product),
				},
			},
			{
				Name:        "cloud_id",
				DisplayName: "Cloud ID",
				Description: "Optional Atlassian cloud/site ID. If omitted, the first accessible site is discovered on first use.",
				Env: coresecret.EnvSpec{
					Aliases: CloudIDEnvAliases(product),
				},
			},
			{
				Name:        "site_url",
				DisplayName: "Site URL",
				Description: "Optional Atlassian site URL used for human-facing links.",
				Env: coresecret.EnvSpec{
					Aliases: SiteURLEnvAliases(product),
				},
			},
		},
	}
}

func oauthSecretRef(pluginName string, ref resource.PluginRef) coresecret.Ref {
	return coresecret.Plugin(pluginName, ref.InstanceName(), OAuth2Method+"_token")
}

func OAuthSecretRef(pluginName string, ref resource.PluginRef) coresecret.Ref {
	return oauthSecretRef(pluginName, ref)
}

func oauthRelatedSecretRef(pluginName string, ref resource.PluginRef, name string) coresecret.Ref {
	return coresecret.Plugin(pluginName, ref.InstanceName(), OAuth2Method+"_"+strings.TrimSpace(name))
}

func TokenEnvAliases(product Product) []string {
	prefix := strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(product.Name), "-", "_"))
	if prefix == "" {
		prefix = "ATLASSIAN"
	}
	return []string{prefix + "_TOKEN", prefix + "_ACCESS_TOKEN", prefix + "_API_TOKEN", "ATLASSIAN_TOKEN", "ATLASSIAN_ACCESS_TOKEN", "ATLASSIAN_API_TOKEN"}
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

func discoverTokenSiteURL(ctx context.Context, sys system.System, product Product, session Session) Session {
	if strings.TrimSpace(session.SiteURL) != "" || strings.TrimSpace(session.CloudID) == "" || strings.TrimSpace(session.Token) == "" {
		return session
	}
	if sys == nil || sys.Network() == nil {
		return session
	}
	sites, err := DiscoverSites(ctx, system.NewHTTPClient(sys.Network()), session.Token)
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

func sessionFromStored(product Product, cfg Config, stored runtimesecret.StoredSecret) Session {
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

func needsRefresh(stored runtimesecret.StoredSecret) bool {
	if stored.ExpiresAt.IsZero() {
		return false
	}
	return time.Now().UTC().Add(2 * time.Minute).After(stored.ExpiresAt)
}

func refreshStored(ctx context.Context, sys system.System, store runtimesecret.FileStore, pluginName string, ref resource.PluginRef, product Product, stored runtimesecret.StoredSecret) (OAuthToken, runtimesecret.StoredSecret, error) {
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
	token, err := RefreshToken(ctx, system.NewHTTPClient(sys.Network()), clientID, clientSecret, refresh)
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

func discoverAndStoreSite(ctx context.Context, sys system.System, store runtimesecret.FileStore, pluginName string, ref resource.PluginRef, product Product, cfg Config, stored runtimesecret.StoredSecret) (runtimesecret.StoredSecret, Session, error) {
	sites, err := DiscoverSites(ctx, system.NewHTTPClient(sys.Network()), stored.Value)
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

func shouldUseBasicTokenAuth(cfg Config) bool {
	return strings.EqualFold(strings.TrimSpace(cfg.Auth.Method), "basic")
}

func resolveAPIEmail(ctx context.Context, sys system.System, product Product, cfg Config) (string, error) {
	if email := strings.TrimSpace(cfg.Auth.Email); email != "" {
		return email, nil
	}
	if sys.Environment() == nil {
		return "", fmt.Errorf("atlassianplugin: system environment is nil")
	}
	names := []string{}
	if name := strings.TrimSpace(cfg.Auth.EmailEnv); name != "" {
		names = append(names, name)
	} else {
		names = EmailEnvAliases(product)
	}
	for _, name := range names {
		value, ok, err := sys.Environment().Lookup(ctx, name)
		if err != nil {
			return "", err
		}
		if ok && strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value), nil
		}
	}
	if cfg.Auth.EmailEnv != "" {
		return "", fmt.Errorf("atlassianplugin: account email is not configured; set %s", cfg.Auth.EmailEnv)
	}
	return "", fmt.Errorf("atlassianplugin: account email is not configured; set auth.email, auth.email_env, or one of %s", strings.Join(EmailEnvAliases(product), ", "))
}

func basicAuthorization(email, token string) string {
	raw := strings.TrimSpace(email) + ":" + strings.TrimSpace(token)
	return "Basic " + base64.StdEncoding.EncodeToString([]byte(raw))
}
