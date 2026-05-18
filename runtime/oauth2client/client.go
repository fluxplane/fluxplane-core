// Package oauth2client provides surface-neutral OAuth2 token HTTP helpers.
package oauth2client

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// TokenRequest describes an OAuth2 token endpoint request.
type TokenRequest struct {
	TokenURL     string
	ClientID     string
	ClientSecret string
	RedirectURI  string
	Code         string
	RefreshToken string
}

// TokenResponse is the standard OAuth2 token endpoint response shape used by
// first-party integrations.
type TokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token,omitempty"`
	TokenType    string `json:"token_type,omitempty"`
	ExpiresIn    int    `json:"expires_in,omitempty"`
	Scope        string `json:"scope,omitempty"`
}

// ExchangeCode exchanges one OAuth2 authorization code for token material.
func ExchangeCode(ctx context.Context, client *http.Client, req TokenRequest) (TokenResponse, error) {
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"client_id":     {req.ClientID},
		"client_secret": {req.ClientSecret},
		"code":          {req.Code},
		"redirect_uri":  {req.RedirectURI},
	}
	return postTokenForm(ctx, client, req.TokenURL, form)
}

// Refresh exchanges a refresh token for fresh OAuth2 token material.
func Refresh(ctx context.Context, client *http.Client, req TokenRequest) (TokenResponse, error) {
	form := url.Values{
		"grant_type":    {"refresh_token"},
		"client_id":     {req.ClientID},
		"client_secret": {req.ClientSecret},
		"refresh_token": {req.RefreshToken},
	}
	return postTokenForm(ctx, client, req.TokenURL, form)
}

func postTokenForm(ctx context.Context, client *http.Client, tokenURL string, form url.Values) (TokenResponse, error) {
	if client == nil {
		client = http.DefaultClient
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return TokenResponse{}, err
	}
	httpReq.Header.Set("Accept", "application/json")
	httpReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := client.Do(httpReq)
	if err != nil {
		return TokenResponse{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return TokenResponse{}, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return TokenResponse{}, fmt.Errorf("oauth2 token exchange failed: status %d: %s", resp.StatusCode, strings.TrimSpace(string(data)))
	}
	var token TokenResponse
	if err := json.Unmarshal(data, &token); err != nil {
		return TokenResponse{}, err
	}
	if strings.TrimSpace(token.AccessToken) == "" {
		return TokenResponse{}, fmt.Errorf("oauth2 token response has no access token")
	}
	return token, nil
}
