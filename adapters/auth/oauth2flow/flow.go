// Package oauth2flow adapts OAuth2 authorization-code setup to local CLI IO.
package oauth2flow

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

type Config struct {
	AuthorizeURL string
	ClientID     string
	Scopes       []string
	ExtraParams  map[string]string
	Out          io.Writer
}

type Result struct {
	Code        string
	RedirectURI string
}

// Authorize starts a loopback callback listener, prints the authorization URL,
// and waits for a successful code callback.
func Authorize(ctx context.Context, cfg Config) (Result, error) {
	if strings.TrimSpace(cfg.AuthorizeURL) == "" {
		return Result{}, fmt.Errorf("oauth2flow: authorize URL is required")
	}
	if strings.TrimSpace(cfg.ClientID) == "" {
		return Result{}, fmt.Errorf("oauth2flow: client_id is required")
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return Result{}, err
	}
	defer func() { _ = listener.Close() }()
	state, err := randomState()
	if err != nil {
		return Result{}, err
	}
	redirectURI := "http://" + listener.Addr().String() + "/callback"
	resultCh := make(chan callbackResult, 1)
	server := &http.Server{Handler: callbackHandler(state, resultCh)}
	go func() {
		_ = server.Serve(listener)
	}()
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}()
	if cfg.Out != nil {
		_, _ = fmt.Fprintf(cfg.Out, "Open this URL to authorize:\n%s\n", authorizeURL(cfg, redirectURI, state))
	}
	select {
	case result := <-resultCh:
		if result.err != "" {
			return Result{}, fmt.Errorf("oauth2flow: callback: %s", result.err)
		}
		return Result{Code: result.code, RedirectURI: redirectURI}, nil
	case <-ctx.Done():
		return Result{}, ctx.Err()
	}
}

type callbackResult struct {
	code string
	err  string
}

func callbackHandler(state string, out chan<- callbackResult) http.Handler {
	var once sync.Once
	deliver := func(result callbackResult) {
		once.Do(func() { out <- result })
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query()
		if query.Get("state") != state {
			http.Error(w, "invalid state", http.StatusBadRequest)
			deliver(callbackResult{err: "invalid state"})
			return
		}
		if errText := query.Get("error"); errText != "" {
			http.Error(w, errText, http.StatusBadRequest)
			deliver(callbackResult{err: errText})
			return
		}
		code := strings.TrimSpace(query.Get("code"))
		if code == "" {
			http.Error(w, "missing code", http.StatusBadRequest)
			deliver(callbackResult{err: "missing code"})
			return
		}
		_, _ = io.WriteString(w, "Authorization complete. You can close this window.\n")
		deliver(callbackResult{code: code})
	})
}

func authorizeURL(cfg Config, redirectURI, state string) string {
	values := url.Values{
		"client_id":     {cfg.ClientID},
		"redirect_uri":  {redirectURI},
		"response_type": {"code"},
		"state":         {state},
	}
	if len(cfg.Scopes) > 0 {
		values.Set("scope", strings.Join(cfg.Scopes, " "))
	}
	for key, value := range cfg.ExtraParams {
		if strings.TrimSpace(key) != "" {
			values.Set(key, value)
		}
	}
	return strings.TrimSpace(cfg.AuthorizeURL) + "?" + values.Encode()
}

func randomState() (string, error) {
	var data [24]byte
	if _, err := rand.Read(data[:]); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(data[:]), nil
}
