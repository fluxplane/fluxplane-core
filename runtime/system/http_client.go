package system

import (
	"bytes"
	"crypto/tls"
	"fmt"
	"io"
	"net/http"
	"time"
)

const (
	defaultHTTPClientTimeout  = 30 * time.Second
	defaultHTTPClientMaxBytes = 10 * 1024 * 1024
)

// HTTPClientOption configures HTTP clients backed by System network access.
type HTTPClientOption func(*httpClientConfig)

type httpClientConfig struct {
	timeout   time.Duration
	maxBytes  int
	tlsConfig *tls.Config
}

func defaultHTTPClientConfig() httpClientConfig {
	return httpClientConfig{
		timeout:  defaultHTTPClientTimeout,
		maxBytes: defaultHTTPClientMaxBytes,
	}
}

// WithHTTPClientTimeout sets the fallback request timeout when the request
// context does not already have a deadline.
func WithHTTPClientTimeout(timeout time.Duration) HTTPClientOption {
	return func(cfg *httpClientConfig) {
		if timeout > 0 {
			cfg.timeout = timeout
		}
	}
}

// WithHTTPClientMaxBytes sets the maximum response body size requested from the
// System network boundary.
func WithHTTPClientMaxBytes(maxBytes int) HTTPClientOption {
	return func(cfg *httpClientConfig) {
		if maxBytes > 0 {
			cfg.maxBytes = maxBytes
		}
	}
}

// WithHTTPClientTLSConfig sets the TLS configuration forwarded to the System
// network boundary.
func WithHTTPClientTLSConfig(tlsConfig *tls.Config) HTTPClientOption {
	return func(cfg *httpClientConfig) {
		if tlsConfig != nil {
			cfg.tlsConfig = secureTLSConfig(tlsConfig)
		}
	}
}

// NewHTTPClient returns an http.Client that routes requests through network.
func NewHTTPClient(network Network, opts ...HTTPClientOption) *http.Client {
	return &http.Client{Transport: NewRoundTripper(network, opts...)}
}

// NewRoundTripper returns an http.RoundTripper that routes requests through
// network.
func NewRoundTripper(network Network, opts ...HTTPClientOption) http.RoundTripper {
	cfg := defaultHTTPClientConfig()
	for _, opt := range opts {
		if opt != nil {
			opt(&cfg)
		}
	}
	return networkRoundTripper{network: network, cfg: cfg}
}

type networkRoundTripper struct {
	network Network
	cfg     httpClientConfig
}

func (t networkRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	if t.network == nil {
		return nil, fmt.Errorf("system: network is nil")
	}
	var body []byte
	if req.Body != nil {
		var err error
		body, err = io.ReadAll(req.Body)
		if err != nil {
			return nil, err
		}
	}
	headers := map[string]string{}
	for key, values := range req.Header {
		if len(values) > 0 {
			headers[key] = values[0]
		}
	}
	timeout := t.cfg.timeout
	if deadline, ok := req.Context().Deadline(); ok {
		if remaining := time.Until(deadline); remaining > 0 {
			timeout = remaining
		}
	}
	resp, err := t.network.DoHTTP(req.Context(), HTTPRequest{
		URL:       req.URL.String(),
		Method:    req.Method,
		Headers:   headers,
		Body:      string(body),
		Timeout:   timeout,
		MaxBytes:  t.cfg.maxBytes,
		UserAgent: req.UserAgent(),
		TLSConfig: t.cfg.tlsConfig,
	})
	if err != nil {
		return nil, err
	}
	httpResp := &http.Response{
		Status:        resp.Status,
		StatusCode:    resp.StatusCode,
		Header:        http.Header(resp.Headers),
		Body:          io.NopCloser(bytes.NewReader(resp.Body)),
		ContentLength: int64(len(resp.Body)),
		Request:       req,
	}
	if httpResp.Status == "" {
		httpResp.Status = fmt.Sprintf("%d %s", resp.StatusCode, http.StatusText(resp.StatusCode))
	}
	return httpResp, nil
}
