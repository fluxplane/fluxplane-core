package operationruntime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/fluxplane/agentruntime/core/operation"
)

const (
	// DefaultToolResultReplacementThresholdBytes is the provider-facing tool
	// result size limit before the original result is spooled to a temp file.
	DefaultToolResultReplacementThresholdBytes int64 = 10 * 1024

	resultReplacementKind = "tool_result_file"
	resultReplacementType = "application/json"
)

// ResultReplacement is the model-facing stand-in for an oversized tool result.
type ResultReplacement struct {
	Replaced       bool             `json:"replaced"`
	Kind           string           `json:"kind"`
	Path           string           `json:"path"`
	SizeBytes      int64            `json:"size_bytes"`
	ThresholdBytes int64            `json:"threshold_bytes"`
	Operation      string           `json:"operation,omitempty"`
	CallID         string           `json:"call_id,omitempty"`
	Status         operation.Status `json:"status,omitempty"`
	MediaType      string           `json:"media_type,omitempty"`
	Digest         string           `json:"digest,omitempty"`
	Message        string           `json:"message,omitempty"`
}

// ModelText returns the compact text sent back to the model as the tool result.
func (r ResultReplacement) ModelText() string {
	message := strings.TrimSpace(r.Message)
	if message == "" {
		message = "Tool result replaced because it exceeded the provider-facing size limit."
	}
	return fmt.Sprintf("%s Full result: %s (%d bytes, sha256:%s). Threshold: %d bytes.",
		message, r.Path, r.SizeBytes, r.Digest, r.ThresholdBytes)
}

// ReplacementOptions configures oversized result replacement.
type ReplacementOptions struct {
	ThresholdBytes int64
	Operation      operation.Ref
	CallID         operation.CallID
	TempDir        string
}

// ReplaceLargeResult replaces oversized operation results with a temp-file
// reference. It is intended for model tool-call results, not every operation
// execution path.
func ReplaceLargeResult(ctx context.Context, result operation.Result, opts ReplacementOptions) (operation.Result, *ResultReplacement, error) {
	threshold := opts.ThresholdBytes
	if threshold <= 0 {
		threshold = DefaultToolResultReplacementThresholdBytes
	}
	data, err := json.Marshal(result)
	if err != nil {
		return result, nil, fmt.Errorf("marshal result: %w", err)
	}
	if int64(len(data)) <= threshold {
		return result, nil, nil
	}
	if err := contextErr(ctx); err != nil {
		return result, nil, err
	}
	path, err := writeReplacementFile(opts.TempDir, data)
	if err != nil {
		return result, nil, err
	}
	digest := sha256.Sum256(data)
	replacement := ResultReplacement{
		Replaced:       true,
		Kind:           resultReplacementKind,
		Path:           path,
		SizeBytes:      int64(len(data)),
		ThresholdBytes: threshold,
		Operation:      opts.Operation.String(),
		CallID:         string(opts.CallID),
		Status:         result.Status,
		MediaType:      resultReplacementType,
		Digest:         hex.EncodeToString(digest[:]),
		Message:        "Tool result replaced because it exceeded the provider-facing size limit.",
	}
	if result.IsError() {
		return replaceErrorResult(result, replacement), &replacement, nil
	}
	return operation.OK(operation.Rendered{
		Text:  replacement.ModelText(),
		Model: replacement.ModelText(),
		Data:  replacement,
	}), &replacement, nil
}

func replaceErrorResult(result operation.Result, replacement ResultReplacement) operation.Result {
	out := result
	if out.Error == nil {
		out.Error = &operation.Error{Message: replacement.Message}
	}
	details := map[string]any{}
	for key, value := range out.Error.Details {
		details[key] = value
	}
	details["replacement"] = replacement
	out.Error = &operation.Error{
		Code:    out.Error.Code,
		Message: out.Error.Message,
		Details: details,
	}
	return out
}

func writeReplacementFile(root string, data []byte) (string, error) {
	if strings.TrimSpace(root) == "" {
		root = os.TempDir()
	}
	dir, err := os.MkdirTemp(root, "agentruntime-tool-result-*")
	if err != nil {
		return "", fmt.Errorf("create replacement dir: %w", err)
	}
	path := filepath.Join(dir, "result.json")
	if err := os.WriteFile(path, data, 0600); err != nil {
		return "", fmt.Errorf("write replacement result: %w", err)
	}
	return path, nil
}

func contextErr(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	return ctx.Err()
}
