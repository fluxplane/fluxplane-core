package image

import (
	"context"
	"encoding/json"
	"fmt"
	fpsystem "github.com/fluxplane/fluxplane-system"
	"github.com/fluxplane/fluxplane-system/systemkit"
	"time"
)

func doJSON(ctx context.Context, sys fpsystem.System, targetURL, authorization string, body any, maxBytes int) (systemkit.HTTPResponse, error) {
	data, err := json.Marshal(body)
	if err != nil {
		return systemkit.HTTPResponse{}, err
	}
	headers := map[string]string{"content-type": "application/json"}
	if authorization != "" {
		headers["authorization"] = authorization
	}
	resp, err := systemkit.DoHTTP(ctx, sys.Network(), systemkit.HTTPRequest{
		URL:       targetURL,
		Method:    "POST",
		Headers:   headers,
		Body:      data,
		Timeout:   60 * time.Second,
		MaxBytes:  maxBytes,
		UserAgent: "fluxplane/0.1",
	})
	if err != nil {
		return systemkit.HTTPResponse{}, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return systemkit.HTTPResponse{}, fmt.Errorf("HTTP %s: %s", resp.Status, string(resp.Body))
	}
	return resp, nil
}
