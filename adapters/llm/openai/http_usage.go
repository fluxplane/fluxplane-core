package openai

import (
	"context"
	"io"
	"net/http"
	"sync/atomic"

	coreconversation "github.com/fluxplane/agentruntime/core/conversation"
	"github.com/fluxplane/agentruntime/core/usage"
	"github.com/openai/openai-go/v3/option"
)

type httpUsageContextKey struct{}

type httpUsageCollector struct {
	uploadBytes   atomic.Int64
	downloadBytes atomic.Int64
}

func newHTTPUsageCollector() *httpUsageCollector {
	return &httpUsageCollector{}
}

func contextWithHTTPUsage(ctx context.Context, collector *httpUsageCollector) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if collector == nil {
		return ctx
	}
	return context.WithValue(ctx, httpUsageContextKey{}, collector)
}

func httpUsageFromContext(ctx context.Context) *httpUsageCollector {
	if ctx == nil {
		return nil
	}
	collector, _ := ctx.Value(httpUsageContextKey{}).(*httpUsageCollector)
	return collector
}

func httpUsageMiddleware() option.Middleware {
	return func(req *http.Request, next option.MiddlewareNext) (*http.Response, error) {
		collector := httpUsageFromContext(req.Context())
		if collector == nil {
			return next(req)
		}
		if req.Body != nil {
			req.Body = countingReadCloser{
				ReadCloser: req.Body,
				add: func(n int64) {
					collector.uploadBytes.Add(n)
				},
			}
		}
		resp, err := next(req)
		if err != nil || resp == nil || resp.Body == nil {
			return resp, err
		}
		resp.Body = countingReadCloser{
			ReadCloser: resp.Body,
			add: func(n int64) {
				collector.downloadBytes.Add(n)
			},
		}
		return resp, nil
	}
}

type countingReadCloser struct {
	io.ReadCloser
	add func(int64)
}

func (r countingReadCloser) Read(p []byte) (int, error) {
	n, err := r.ReadCloser.Read(p)
	if n > 0 && r.add != nil {
		r.add(int64(n))
	}
	return n, err
}

func httpUsageRecord(provider coreconversation.ProviderIdentity, collector *httpUsageCollector) []usage.Recorded {
	if collector == nil {
		return nil
	}
	upload := collector.uploadBytes.Load()
	download := collector.downloadBytes.Load()
	if upload == 0 && download == 0 {
		return nil
	}
	recorded := usage.Recorded{
		Source: sourceName(provider.Provider, "http"),
		Subject: usage.Subject{
			Kind:     usage.SubjectNetwork,
			Provider: provider.Provider,
			Name:     provider.Model,
			Attributes: map[string]string{
				"api": provider.API,
			},
		},
	}
	if upload > 0 {
		recorded.Measurements = append(recorded.Measurements, usage.Measurement{
			Metric:    usage.MetricNetworkBytes,
			Quantity:  float64(upload),
			Unit:      usage.UnitByte,
			Direction: usage.DirectionUpload,
		})
	}
	if download > 0 {
		recorded.Measurements = append(recorded.Measurements, usage.Measurement{
			Metric:    usage.MetricNetworkBytes,
			Quantity:  float64(download),
			Unit:      usage.UnitByte,
			Direction: usage.DirectionDownload,
		})
	}
	return []usage.Recorded{recorded}
}

func sourceName(provider, suffix string) string {
	if provider == "" {
		return suffix
	}
	if suffix == "" {
		return provider
	}
	return provider + "." + suffix
}
