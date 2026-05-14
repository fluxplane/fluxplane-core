package imageplugin

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/fluxplane/agentruntime/runtime/system"
)

func doJSON(ctx context.Context, sys system.System, targetURL, authorization string, body any, maxBytes int) (system.HTTPResponse, error) {
	data, err := json.Marshal(body)
	if err != nil {
		return system.HTTPResponse{}, err
	}
	headers := map[string]string{"content-type": "application/json"}
	if authorization != "" {
		headers["authorization"] = authorization
	}
	resp, err := sys.Network().DoHTTP(ctx, system.HTTPRequest{
		URL:       targetURL,
		Method:    "POST",
		Headers:   headers,
		Body:      string(data),
		Timeout:   60 * time.Second,
		MaxBytes:  maxBytes,
		UserAgent: "agentruntime/0.1",
	})
	if err != nil {
		return system.HTTPResponse{}, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return system.HTTPResponse{}, fmt.Errorf("HTTP %s: %s", resp.Status, string(resp.Body))
	}
	return resp, nil
}
