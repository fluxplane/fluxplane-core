package httptransport

import (
	"bytes"
	"compress/flate"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/andybalholm/brotli"
	"github.com/klauspost/compress/zstd"
)

func TestDefaultHTTPClientUsesDefaultTransport(t *testing.T) {
	client := DefaultHTTPClient()
	if client == nil || client.Transport == nil {
		t.Fatalf("expected default client with transport")
	}
	if _, ok := client.Transport.(*decompressingTransport); !ok {
		t.Fatalf("expected decompressing transport, got %T", client.Transport)
	}
}

func TestDecompressingTransportSetsAcceptEncoding(t *testing.T) {
	rt := NewDecompressingTransport(roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.Header.Get("Accept-Encoding") != ExtendedAcceptEncoding {
			t.Fatalf("Accept-Encoding = %q, want %q", req.Header.Get("Accept-Encoding"), ExtendedAcceptEncoding)
		}
		return textResponse("", []byte("ok")), nil
	}))
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, "https://example.test", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := rt.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if req.Header.Get("Accept-Encoding") != "" {
		t.Fatalf("transport mutated caller request headers: %v", req.Header)
	}
}

func TestDecompressingTransportDecodesSupportedEncodings(t *testing.T) {
	tests := []struct {
		name   string
		enc    string
		encode func([]byte) ([]byte, error)
	}{
		{"gzip", "gzip", gzipEncode},
		{"deflate", "deflate", deflateEncode},
		{"br", "br", brotliEncode},
		{"zstd", "zstd", zstdEncode},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body, err := tt.encode([]byte("data: hello\n\n"))
			if err != nil {
				t.Fatal(err)
			}
			rt := NewDecompressingTransport(roundTripFunc(func(req *http.Request) (*http.Response, error) {
				return textResponse(tt.enc, body), nil
			}))
			req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, "https://example.test", nil)
			if err != nil {
				t.Fatal(err)
			}
			resp, err := rt.RoundTrip(req)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = resp.Body.Close() }()
			got, err := io.ReadAll(resp.Body)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != "data: hello\n\n" {
				t.Fatalf("body = %q", got)
			}
			if resp.Header.Get("Content-Length") != "" || resp.ContentLength != -1 {
				t.Fatalf("expected decompressed response length to be unknown: header=%q length=%d", resp.Header.Get("Content-Length"), resp.ContentLength)
			}
			if resp.Header.Get("Content-Encoding") != "" || !resp.Uncompressed {
				t.Fatalf("expected response to be marked decompressed: header=%q uncompressed=%v", resp.Header.Get("Content-Encoding"), resp.Uncompressed)
			}
		})
	}
}

func TestRetryingTransportRetriesTransientStatus(t *testing.T) {
	var calls int
	rt := NewRetryingTransport(roundTripFunc(func(req *http.Request) (*http.Response, error) {
		calls++
		if calls == 1 {
			return textStatusResponse(http.StatusTooManyRequests, "retry"), nil
		}
		return textStatusResponse(http.StatusOK, "ok"), nil
	}), RetryConfig{
		MaxAttempts: 2,
		BaseWait:    time.Millisecond,
		MaxWait:     time.Millisecond,
		Sleep:       noSleep,
	})
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, "https://example.test", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := rt.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if calls != 2 {
		t.Fatalf("calls = %d, want 2", calls)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
}

func TestRetryingTransportRequiresReplayableBody(t *testing.T) {
	var calls int
	rt := NewRetryingTransport(roundTripFunc(func(req *http.Request) (*http.Response, error) {
		calls++
		return nil, errors.New("temporary")
	}), RetryConfig{
		MaxAttempts: 2,
		BaseWait:    time.Millisecond,
		MaxWait:     time.Millisecond,
		Sleep:       noSleep,
	})
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, "https://example.test", io.NopCloser(bytes.NewReader([]byte("body"))))
	if err != nil {
		t.Fatal(err)
	}
	_, err = rt.RoundTrip(req)
	if err == nil {
		t.Fatal("expected error")
	}
	if calls != 1 {
		t.Fatalf("calls = %d, want 1", calls)
	}
}

func TestRetryingTransportReplaysPostWithGetBody(t *testing.T) {
	var calls int
	rt := NewRetryingTransport(roundTripFunc(func(req *http.Request) (*http.Response, error) {
		calls++
		body, err := io.ReadAll(req.Body)
		if err != nil {
			t.Fatal(err)
		}
		if string(body) != "body" {
			t.Fatalf("body = %q", body)
		}
		if calls == 1 {
			return textStatusResponse(http.StatusServiceUnavailable, "retry"), nil
		}
		return textStatusResponse(http.StatusOK, "ok"), nil
	}), RetryConfig{
		MaxAttempts: 2,
		BaseWait:    time.Millisecond,
		MaxWait:     time.Millisecond,
		Sleep:       noSleep,
	})
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, "https://example.test", bytes.NewReader([]byte("body")))
	if err != nil {
		t.Fatal(err)
	}
	resp, err := rt.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if calls != 2 {
		t.Fatalf("calls = %d, want 2", calls)
	}
}

func noSleep(context.Context, time.Duration) error { return nil }

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func textResponse(encoding string, body []byte) *http.Response {
	header := http.Header{"Content-Type": {"text/event-stream"}, "Content-Length": {"999"}}
	if encoding != "" {
		header.Set("Content-Encoding", encoding)
	}
	return &http.Response{
		StatusCode:    http.StatusOK,
		Header:        header,
		Body:          io.NopCloser(bytes.NewReader(body)),
		ContentLength: int64(len(body)),
	}
}

func textStatusResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode:    status,
		Status:        http.StatusText(status),
		Header:        http.Header{"Content-Type": {"text/plain"}},
		Body:          io.NopCloser(bytes.NewReader([]byte(body))),
		ContentLength: int64(len(body)),
	}
}

func gzipEncode(data []byte) ([]byte, error) {
	var buf bytes.Buffer
	w := gzip.NewWriter(&buf)
	if _, err := w.Write(data); err != nil {
		return nil, err
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func deflateEncode(data []byte) ([]byte, error) {
	var buf bytes.Buffer
	w, err := flate.NewWriter(&buf, flate.DefaultCompression)
	if err != nil {
		return nil, err
	}
	if _, err := w.Write(data); err != nil {
		return nil, err
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func brotliEncode(data []byte) ([]byte, error) {
	var buf bytes.Buffer
	w := brotli.NewWriter(&buf)
	if _, err := w.Write(data); err != nil {
		return nil, err
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func zstdEncode(data []byte) ([]byte, error) {
	var buf bytes.Buffer
	w, err := zstd.NewWriter(&buf)
	if err != nil {
		return nil, err
	}
	if _, err := w.Write(data); err != nil {
		_ = w.Close()
		return nil, err
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
