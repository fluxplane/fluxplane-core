package loki

import (
	"context"
	"encoding/json"
	"fmt"
	fpsystem "github.com/fluxplane/fluxplane-system"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/fluxplane/fluxplane-system/systemkit"
)

type lokiClient struct {
	network  fpsystem.Network
	baseURL  string
	tenantID string
	timeout  time.Duration
}

type testResult struct {
	Reachable bool   `json:"reachable"`
	Ready     bool   `json:"ready"`
	Version   string `json:"version,omitempty"`
	LatencyMS int64  `json:"latency_ms,omitempty"`
	Error     string `json:"error,omitempty"`
}

type labelResponse struct {
	Status string   `json:"status"`
	Data   []string `json:"data"`
}

type queryRangeResponse struct {
	Status string `json:"status"`
	Data   struct {
		ResultType string       `json:"resultType"`
		Result     []lokiStream `json:"result"`
		Stats      any          `json:"stats,omitempty"`
	} `json:"data"`
}

type lokiStream struct {
	Stream map[string]string `json:"stream"`
	Values [][]string        `json:"values"`
}

func (c lokiClient) test(ctx context.Context) testResult {
	start := time.Now()
	resp, err := c.get(ctx, "/ready", nil, 64*1024)
	out := testResult{LatencyMS: time.Since(start).Milliseconds()}
	if err != nil {
		out.Error = err.Error()
		return out
	}
	out.Reachable = true
	out.Ready = resp.StatusCode >= 200 && resp.StatusCode < 300
	build, err := c.get(ctx, "/loki/api/v1/status/buildinfo", nil, 128*1024)
	if err == nil && build.StatusCode >= 200 && build.StatusCode < 300 {
		var decoded struct {
			Status string         `json:"status"`
			Data   map[string]any `json:"data"`
		}
		if json.Unmarshal(build.Body, &decoded) == nil {
			if version, ok := decoded.Data["version"].(string); ok {
				out.Version = version
			}
		}
	}
	if !out.Ready {
		out.Error = resp.Status
	}
	return out
}

func (c lokiClient) labels(ctx context.Context, label string, start, end time.Time, limit int) ([]string, error) {
	path := "/loki/api/v1/labels"
	if strings.TrimSpace(label) != "" {
		path = "/loki/api/v1/label/" + url.PathEscape(strings.TrimSpace(label)) + "/values"
	}
	values := url.Values{}
	if !start.IsZero() {
		values.Set("start", strconv.FormatInt(start.UnixNano(), 10))
	}
	if !end.IsZero() {
		values.Set("end", strconv.FormatInt(end.UnixNano(), 10))
	}
	if limit > 0 {
		values.Set("limit", strconv.Itoa(limit))
	}
	resp, err := c.get(ctx, path, values, 512*1024)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("loki labels failed: %s", resp.Status)
	}
	var decoded labelResponse
	if err := json.Unmarshal(resp.Body, &decoded); err != nil {
		return nil, err
	}
	if decoded.Status != "" && decoded.Status != "success" {
		return nil, fmt.Errorf("loki labels status %q", decoded.Status)
	}
	return decoded.Data, nil
}

func (c lokiClient) queryRange(ctx context.Context, query string, start, end time.Time, limit int, direction string) (queryRangeResponse, bool, error) {
	values := url.Values{}
	values.Set("query", query)
	values.Set("start", strconv.FormatInt(start.UnixNano(), 10))
	values.Set("end", strconv.FormatInt(end.UnixNano(), 10))
	values.Set("limit", strconv.Itoa(limit))
	if strings.TrimSpace(direction) != "" {
		values.Set("direction", strings.TrimSpace(direction))
	}
	resp, err := c.get(ctx, "/loki/api/v1/query_range", values, 5*1024*1024)
	if err != nil {
		return queryRangeResponse{}, false, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return queryRangeResponse{}, false, fmt.Errorf("loki query failed: %s", resp.Status)
	}
	var decoded queryRangeResponse
	if err := json.Unmarshal(resp.Body, &decoded); err != nil {
		return queryRangeResponse{}, resp.Truncated, err
	}
	if decoded.Status != "" && decoded.Status != "success" {
		return queryRangeResponse{}, resp.Truncated, fmt.Errorf("loki query status %q", decoded.Status)
	}
	return decoded, resp.Truncated, nil
}

func (c lokiClient) get(ctx context.Context, path string, values url.Values, maxBytes int) (systemkit.HTTPResponse, error) {
	if c.network == nil {
		return systemkit.HTTPResponse{}, fmt.Errorf("loki network is nil")
	}
	base := strings.TrimRight(strings.TrimSpace(c.baseURL), "/")
	if base == "" {
		return systemkit.HTTPResponse{}, fmt.Errorf("loki url is empty")
	}
	target := base + path
	if len(values) > 0 {
		target += "?" + values.Encode()
	}
	headers := map[string]string{}
	if strings.TrimSpace(c.tenantID) != "" {
		headers["X-Scope-OrgID"] = strings.TrimSpace(c.tenantID)
	}
	timeout := c.timeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	return systemkit.DoHTTP(ctx, c.network, systemkit.HTTPRequest{
		URL:       target,
		Method:    http.MethodGet,
		Headers:   headers,
		Timeout:   timeout,
		MaxBytes:  maxBytes,
		UserAgent: "fluxplane-loki/0.1",
	})
}
