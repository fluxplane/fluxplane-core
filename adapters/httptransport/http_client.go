// Package httptransport provides HTTP client defaults for adapter packages.
package httptransport

import (
	"compress/flate"
	"compress/gzip"
	"crypto/tls"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/andybalholm/brotli"
	"github.com/klauspost/compress/zstd"
)

const ExtendedAcceptEncoding = "gzip, deflate, br, zstd"

var defaultHTTPTransport http.RoundTripper = &decompressingTransport{wrapped: &http.Transport{
	Proxy:                 http.ProxyFromEnvironment,
	ForceAttemptHTTP2:     true,
	MaxIdleConns:          100,
	MaxIdleConnsPerHost:   10,
	IdleConnTimeout:       90 * time.Second,
	TLSClientConfig:       &tls.Config{MinVersion: tls.VersionTLS12},
	TLSHandshakeTimeout:   30 * time.Second,
	ExpectContinueTimeout: time.Second,
	DisableCompression:    true,
}}

var defaultHTTPClient = &http.Client{Transport: defaultHTTPTransport}

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

func NewDecompressingTransport(base http.RoundTripper) http.RoundTripper {
	if base == nil {
		base = http.DefaultTransport
	}
	return &decompressingTransport{wrapped: base}
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
