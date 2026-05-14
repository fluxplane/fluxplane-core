// Package httptransport provides runtime-wide HTTP transport defaults.
package httptransport

import (
	"compress/flate"
	"compress/gzip"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/andybalholm/brotli"
	"github.com/klauspost/compress/zstd"
)

const ExtendedAcceptEncoding = "gzip, deflate, br, zstd"

const (
	defaultRetryAttempts     = 3
	defaultRetryBaseWait     = 200 * time.Millisecond
	defaultRetryMaxWait      = 2 * time.Second
	defaultRetryAfterMaxWait = 30 * time.Second
)

var defaultHTTPTransport http.RoundTripper = NewDefaultTransport(defaultBaseTransport())

var defaultHTTPClient = &http.Client{Transport: defaultHTTPTransport}

// RetryConfig configures transient HTTP retry behavior.
type RetryConfig struct {
	MaxAttempts       int
	BaseWait          time.Duration
	MaxWait           time.Duration
	MaxRetryAfterWait time.Duration
	Jitter            bool

	// RetryNonIdempotent permits retries for non-idempotent methods when the
	// request body is replayable. Provider clients use this for model API POSTs.
	RetryNonIdempotent bool

	// Sleep overrides retry sleeping. Tests use this to avoid wall-clock waits.
	Sleep func(context.Context, time.Duration) error
}

// DefaultRetryConfig returns conservative retry defaults for outbound model and
// adapter HTTP traffic.
func DefaultRetryConfig() RetryConfig {
	return RetryConfig{
		MaxAttempts:        defaultRetryAttempts,
		BaseWait:           defaultRetryBaseWait,
		MaxWait:            defaultRetryMaxWait,
		MaxRetryAfterWait:  defaultRetryAfterMaxWait,
		Jitter:             true,
		RetryNonIdempotent: true,
	}
}

func DefaultHTTPTransport() http.RoundTripper {
	return defaultHTTPTransport
}

func DefaultHTTPClient() *http.Client {
	return defaultHTTPClient
}

func CloneDefaultHTTPClient() *http.Client {
	cloned := &http.Client{}
	*cloned = *defaultHTTPClient
	return cloned
}

// NewDefaultTransport wraps base with the runtime default retry and
// decompression behavior.
func NewDefaultTransport(base http.RoundTripper) http.RoundTripper {
	return NewDefaultTransportWithRetry(base, DefaultRetryConfig())
}

// NewDefaultTransportWithRetry wraps base with custom retry behavior and the
// default decompression behavior.
func NewDefaultTransportWithRetry(base http.RoundTripper, cfg RetryConfig) http.RoundTripper {
	return NewDecompressingTransport(NewRetryingTransport(base, cfg))
}

func NewRetryingTransport(base http.RoundTripper, cfg RetryConfig) http.RoundTripper {
	if base == nil {
		base = http.DefaultTransport
	}
	cfg = cfg.withDefaults()
	return &retryingTransport{wrapped: base, cfg: cfg}
}

func NewDecompressingTransport(base http.RoundTripper) http.RoundTripper {
	if base == nil {
		base = http.DefaultTransport
	}
	return &decompressingTransport{wrapped: base}
}

func defaultBaseTransport() http.RoundTripper {
	return &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		MaxIdleConnsPerHost:   10,
		IdleConnTimeout:       90 * time.Second,
		TLSClientConfig:       &tls.Config{MinVersion: tls.VersionTLS12},
		TLSHandshakeTimeout:   30 * time.Second,
		ExpectContinueTimeout: time.Second,
		DisableCompression:    true,
	}
}

func (c RetryConfig) withDefaults() RetryConfig {
	if c.MaxAttempts <= 0 {
		c.MaxAttempts = defaultRetryAttempts
	}
	if c.BaseWait <= 0 {
		c.BaseWait = defaultRetryBaseWait
	}
	if c.MaxWait <= 0 {
		c.MaxWait = defaultRetryMaxWait
	}
	if c.MaxRetryAfterWait <= 0 {
		c.MaxRetryAfterWait = defaultRetryAfterMaxWait
	}
	if c.Sleep == nil {
		c.Sleep = sleepContext
	}
	return c
}

type retryingTransport struct {
	wrapped http.RoundTripper
	cfg     RetryConfig
}

func (t *retryingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req == nil {
		return nil, errors.New("httptransport: nil request")
	}
	if !canReplay(req, t.cfg.RetryNonIdempotent) {
		return t.wrapped.RoundTrip(req)
	}
	var lastErr error
	for attempt := 0; attempt < t.cfg.MaxAttempts; attempt++ {
		attemptReq, err := requestForAttempt(req, attempt)
		if err != nil {
			return nil, err
		}
		resp, err := t.wrapped.RoundTrip(attemptReq)
		if !shouldRetry(req.Context(), resp, err) || attempt == t.cfg.MaxAttempts-1 {
			return resp, err
		}
		lastErr = err
		wait := retryWait(resp, t.cfg, attempt)
		if resp != nil && resp.Body != nil {
			_ = resp.Body.Close()
		}
		if err := t.cfg.Sleep(req.Context(), wait); err != nil {
			if lastErr != nil {
				return nil, lastErr
			}
			return nil, err
		}
	}
	return nil, lastErr
}

func requestForAttempt(req *http.Request, attempt int) (*http.Request, error) {
	if attempt == 0 {
		return req, nil
	}
	clone := req.Clone(req.Context())
	if req.Body != nil {
		if req.GetBody == nil {
			return nil, errors.New("httptransport: retry request body is not replayable")
		}
		body, err := req.GetBody()
		if err != nil {
			return nil, fmt.Errorf("httptransport: reopen retry body: %w", err)
		}
		clone.Body = body
	}
	return clone, nil
}

func canReplay(req *http.Request, retryNonIdempotent bool) bool {
	if req == nil {
		return false
	}
	if isIdempotentMethod(req.Method) {
		return req.Body == nil || req.GetBody != nil
	}
	if !retryNonIdempotent {
		return false
	}
	return req.GetBody != nil
}

func isIdempotentMethod(method string) bool {
	switch strings.ToUpper(method) {
	case http.MethodGet, http.MethodHead, http.MethodOptions, http.MethodTrace, http.MethodPut, http.MethodDelete:
		return true
	default:
		return false
	}
}

func shouldRetry(ctx context.Context, resp *http.Response, err error) bool {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return false
	}
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return false
		}
		return isRetryableTransportError(err)
	}
	if resp == nil {
		return false
	}
	switch resp.StatusCode {
	case http.StatusRequestTimeout, http.StatusConflict, http.StatusTooEarly, http.StatusTooManyRequests:
		return true
	default:
		return resp.StatusCode >= 500
	}
}

func retryWait(resp *http.Response, cfg RetryConfig, attempt int) time.Duration {
	if resp != nil {
		if wait, ok := parseRetryAfter(resp.Header.Get("Retry-After"), time.Now()); ok {
			return clampDuration(wait, 0, cfg.MaxRetryAfterWait)
		}
	}
	multiplier := math.Pow(2, float64(attempt))
	wait := time.Duration(float64(cfg.BaseWait) * multiplier)
	if cfg.Jitter && wait > 0 {
		wait += time.Duration(time.Now().UnixNano() % int64(wait/4+1))
	}
	return clampDuration(wait, cfg.BaseWait, cfg.MaxWait)
}

func isRetryableTransportError(err error) bool {
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return true
	}
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return dnsErr.IsTimeout || dnsErr.IsTemporary
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		return netErr.Timeout()
	}
	return false
}

func parseRetryAfter(raw string, now time.Time) (time.Duration, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, false
	}
	if seconds, err := strconv.Atoi(raw); err == nil {
		if seconds < 0 {
			return 0, true
		}
		return time.Duration(seconds) * time.Second, true
	}
	when, err := http.ParseTime(raw)
	if err != nil {
		return 0, false
	}
	wait := when.Sub(now)
	if wait < 0 {
		wait = 0
	}
	return wait, true
}

func clampDuration(v, min, max time.Duration) time.Duration {
	if max > 0 && v > max {
		return max
	}
	if v < min {
		return min
	}
	return v
}

func sleepContext(ctx context.Context, wait time.Duration) error {
	if wait <= 0 {
		return nil
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

type decompressingTransport struct {
	wrapped http.RoundTripper
}

func (t *decompressingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Header.Get("Accept-Encoding") == "" {
		req = req.Clone(req.Context())
		req.Header.Set("Accept-Encoding", ExtendedAcceptEncoding)
	}
	resp, err := t.wrapped.RoundTrip(req)
	if err != nil || resp == nil {
		return resp, err
	}

	switch resp.Header.Get("Content-Encoding") {
	case "br":
		markDecompressed(resp)
		resp.Body = &brotliReadCloser{underlying: resp.Body}
	case "zstd":
		markDecompressed(resp)
		decoder, err := zstd.NewReader(resp.Body)
		if err != nil {
			_ = resp.Body.Close()
			return nil, fmt.Errorf("zstd decompression: %w", err)
		}
		resp.Body = &zstdReadCloser{decoder: decoder, underlying: resp.Body}
	case "gzip":
		markDecompressed(resp)
		gz, err := gzip.NewReader(resp.Body)
		if err != nil {
			_ = resp.Body.Close()
			return nil, fmt.Errorf("gzip decompression: %w", err)
		}
		resp.Body = &gzipReadCloser{Reader: gz, underlying: resp.Body}
	case "deflate":
		markDecompressed(resp)
		resp.Body = &flateReadCloser{underlying: resp.Body}
	}
	return resp, nil
}

func markDecompressed(resp *http.Response) {
	resp.Header.Del("Content-Encoding")
	resp.Header.Del("Content-Length")
	resp.ContentLength = -1
	resp.Uncompressed = true
}

type brotliReadCloser struct {
	underlying io.ReadCloser
	reader     *brotli.Reader
}

func (b *brotliReadCloser) Read(p []byte) (int, error) {
	if b.reader == nil {
		b.reader = brotli.NewReader(b.underlying)
	}
	return b.reader.Read(p)
}

func (b *brotliReadCloser) Close() error {
	b.reader = nil
	return b.underlying.Close()
}

type zstdReadCloser struct {
	decoder    *zstd.Decoder
	underlying io.ReadCloser
}

func (z *zstdReadCloser) Read(p []byte) (int, error) {
	return z.decoder.Read(p)
}

func (z *zstdReadCloser) Close() error {
	z.decoder.Close()
	return z.underlying.Close()
}

type gzipReadCloser struct {
	*gzip.Reader
	underlying io.ReadCloser
}

func (g *gzipReadCloser) Close() error {
	_ = g.Reader.Close()
	return g.underlying.Close()
}

type flateReadCloser struct {
	underlying io.ReadCloser
	reader     io.ReadCloser
}

func (f *flateReadCloser) Read(p []byte) (int, error) {
	if f.reader == nil {
		f.reader = flate.NewReader(f.underlying)
	}
	return f.reader.Read(p)
}

func (f *flateReadCloser) Close() error {
	if f.reader != nil {
		_ = f.reader.Close()
		f.reader = nil
	}
	return f.underlying.Close()
}
