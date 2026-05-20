package system

import (
	"context"
	"crypto/tls"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestNewRoundTripperRoutesThroughNetwork(t *testing.T) {
	network := &recordingHTTPNetwork{
		response: HTTPResponse{
			StatusCode: 201,
			Headers:    map[string][]string{"Content-Type": {"application/json"}},
			Body:       []byte(`{"ok":true}`),
		},
	}
	req, err := http.NewRequest(http.MethodPost, "https://example.com/api?q=runtime", strings.NewReader("payload"))
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("X-Test", "yes")
	req.Header.Set("User-Agent", "test-agent")

	resp, err := NewRoundTripper(
		network,
		WithHTTPClientTimeout(2*time.Second),
		WithHTTPClientMaxBytes(42),
	).RoundTrip(req)
	if err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if got, want := network.request.URL, "https://example.com/api?q=runtime"; got != want {
		t.Fatalf("request URL = %q, want %q", got, want)
	}
	if got := network.request.Method; got != http.MethodPost {
		t.Fatalf("method = %q, want POST", got)
	}
	if got := network.request.Body; got != "payload" {
		t.Fatalf("body = %q, want payload", got)
	}
	if got := network.request.Headers["X-Test"]; got != "yes" {
		t.Fatalf("X-Test = %q, want yes", got)
	}
	if got := network.request.UserAgent; got != "test-agent" {
		t.Fatalf("user agent = %q, want test-agent", got)
	}
	if got := network.request.Timeout; got != 2*time.Second {
		t.Fatalf("timeout = %s, want 2s", got)
	}
	if got := network.request.MaxBytes; got != 42 {
		t.Fatalf("max bytes = %d, want 42", got)
	}
	tlsConfig := &tls.Config{ServerName: "example.com", MinVersion: tls.VersionTLS13}
	resp2, err := NewRoundTripper(network, WithHTTPClientTLSConfig(tlsConfig)).RoundTrip(req)
	if err != nil {
		t.Fatalf("RoundTrip with TLS config: %v", err)
	}
	_ = resp2.Body.Close()
	if network.request.TLSConfig == nil {
		t.Fatalf("TLSConfig was not forwarded")
	}
	if network.request.TLSConfig.ServerName != "example.com" {
		t.Fatalf("server name = %q, want example.com", network.request.TLSConfig.ServerName)
	}
	if resp.StatusCode != 201 {
		t.Fatalf("status = %d, want 201", resp.StatusCode)
	}
	if string(body) != `{"ok":true}` {
		t.Fatalf("response body = %q", string(body))
	}
}

func TestNewRoundTripperRejectsNilNetwork(t *testing.T) {
	req, err := http.NewRequest(http.MethodGet, "https://example.com", nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	if _, err := NewRoundTripper(nil).RoundTrip(req); err == nil {
		t.Fatalf("RoundTrip error = nil, want error")
	}
}

type recordingHTTPNetwork struct {
	request  HTTPRequest
	response HTTPResponse
}

func (n *recordingHTTPNetwork) DoHTTP(_ context.Context, req HTTPRequest) (HTTPResponse, error) {
	n.request = req
	return n.response, nil
}
