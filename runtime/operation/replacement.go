package operationruntime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/fluxplane/fluxplane-operation"
)

const (
	// DefaultToolResultReplacementThresholdBytes is the provider-facing tool
	// result size limit before the original result is spooled to a temp file.
	DefaultToolResultReplacementThresholdBytes int64 = 512 * 1024

	defaultReplacementPreviewBytes = 4096
	defaultReplacementTailBytes    = 2048
	resultReplacementKind          = "tool_result_file"
	resultReplacementType          = "application/json"
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
	Preview        string           `json:"preview,omitempty"`
	Tail           string           `json:"tail,omitempty"`
	OmittedBytes   int64            `json:"omitted_bytes,omitempty"`
	Message        string           `json:"message,omitempty"`
}

// ModelText returns the compact text sent back to the model as the tool result.
func (r ResultReplacement) ModelText() string {
	message := strings.TrimSpace(r.Message)
	if message == "" {
		message = "Tool result replaced because it exceeded the provider-facing size limit."
	}
	message = fmt.Sprintf("%s\nFull result: %s (%d bytes, sha256:%s). Threshold: %d bytes. Omitted: %d bytes.",
		message, r.Path, r.SizeBytes, r.Digest, r.ThresholdBytes, r.OmittedBytes)
	preview := strings.TrimSpace(r.Preview)
	if preview != "" {
		message += "\nPreview:\n" + preview
	}
	tail := strings.TrimSpace(r.Tail)
	if tail != "" {
		message += "\nTail:\n" + tail
	}
	return message
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
	preview, tail, omitted := replacementPreview(data)
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
		Preview:        preview,
		Tail:           tail,
		OmittedBytes:   omitted,
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

func replacementPreview(data []byte) (string, string, int64) {
	if len(data) == 0 {
		return "", "", 0
	}
	previewLen := min(len(data), defaultReplacementPreviewBytes)
	tailLen := 0
	if len(data) > previewLen {
		tailLen = min(len(data)-previewLen, defaultReplacementTailBytes)
	}
	preview := strings.ToValidUTF8(string(data[:previewLen]), "")
	tail := ""
	if tailLen > 0 {
		tail = strings.ToValidUTF8(string(data[len(data)-tailLen:]), "")
	}
	omitted := int64(len(data) - previewLen - tailLen)
	if omitted < 0 {
		omitted = 0
	}
	return preview, tail, omitted
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
	dir, err := os.MkdirTemp(root, "fluxplane-tool-result-*")
	if err != nil {
		return "", fmt.Errorf("create replacement dir: %w", err)
	}
	path := filepath.Join(dir, "result.json")
	if err := os.WriteFile(path, data, 0600); err != nil {
		_ = os.RemoveAll(dir)
		return "", fmt.Errorf("write replacement result: %w", err)
	}
	return path, nil
}

// ReadReplacementFile reads a bounded replacement result file created by
// ReplaceLargeResult. It only accepts files in the runtime replacement temp
// layout.
func ReadReplacementFile(ctx context.Context, path string, maxBytes int64) ([]byte, bool, error) {
	if err := contextErr(ctx); err != nil {
		return nil, false, err
	}
	if maxBytes <= 0 {
		maxBytes = DefaultToolResultReplacementThresholdBytes
	}
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, false, fmt.Errorf("replacement path is empty")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, false, err
	}
	if filepath.Base(abs) != "result.json" || !strings.HasPrefix(filepath.Base(filepath.Dir(abs)), "fluxplane-tool-result-") {
		return nil, false, fmt.Errorf("path is not an fluxplane replacement result")
	}
	file, err := os.Open(abs)
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = file.Close() }()
	limit := maxBytes + 1
	data, err := io.ReadAll(io.LimitReader(file, limit))
	if err != nil {
		return nil, false, err
	}
	truncated := int64(len(data)) > maxBytes
	if truncated {
		data = data[:maxBytes]
	}
	return data, truncated, nil
}

func contextErr(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	return ctx.Err()
}
