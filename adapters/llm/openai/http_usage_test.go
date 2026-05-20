package openai

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"testing"

	coreconversation "github.com/fluxplane/agentruntime/core/conversation"
	"github.com/fluxplane/agentruntime/core/usage"
)

func TestHTTPUsageMiddlewareCountsRequestAndResponseBodies(t *testing.T) {
	collector := newHTTPUsageCollector()
	req, err := http.NewRequestWithContext(contextWithHTTPUsage(context.Background(), collector), http.MethodPost, "https://api.openai.com/v1/responses", bytes.NewReader([]byte("upload")))
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	middleware := httpUsageMiddleware()
	resp, err := middleware(req, func(req *http.Request) (*http.Response, error) {
		if _, err := io.Copy(io.Discard, req.Body); err != nil {
			t.Fatalf("read request body: %v", err)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(bytes.NewReader([]byte("download"))),
		}, nil
	})
	if err != nil {
		t.Fatalf("middleware: %v", err)
	}
	if _, err := io.Copy(io.Discard, resp.Body); err != nil {
		t.Fatalf("read response body: %v", err)
	}
	records := httpUsageRecord(coreconversation.ProviderIdentity{Provider: "openai", API: "openai.responses", Model: "gpt-test"}, collector)
	if len(records) != 1 {
		t.Fatalf("records len = %d, want 1", len(records))
	}
	if records[0].Subject.Kind != usage.SubjectNetwork || records[0].Subject.Provider != "openai" {
		t.Fatalf("subject = %#v, want openai network", records[0].Subject)
	}
	if got := quantity(records[0], usage.DirectionUpload); got != 6 {
		t.Fatalf("upload bytes = %v, want 6", got)
	}
	if got := quantity(records[0], usage.DirectionDownload); got != 8 {
		t.Fatalf("download bytes = %v, want 8", got)
	}
}

func quantity(recorded usage.Recorded, direction usage.Direction) float64 {
	for _, measurement := range recorded.Measurements {
		if measurement.Metric == usage.MetricNetworkBytes && measurement.Direction == direction {
			return measurement.Quantity
		}
	}
	return 0
}
