package imageplugin

import (
	"context"
	"crypto/sha256"
	"fmt"
	"strings"

	"github.com/fluxplane/agentruntime/runtime/system"
)

func writeGeneratedImage(ctx context.Context, sys system.System, provider, model, prompt, contentType string, data []byte) (GenerateResult, error) {
	if sys == nil || sys.Workspace() == nil {
		return GenerateResult{}, fmt.Errorf("system workspace is not configured")
	}
	scratch, err := sys.Workspace().CreateScratch(ctx, "agentruntime-image-*")
	if err != nil {
		return GenerateResult{}, err
	}
	hash := sha256.Sum256([]byte(provider + ":" + model + ":" + prompt))
	name := fmt.Sprintf("%s_%x%s", provider, hash[:8], extensionForContentType(contentType))
	resolved, err := scratch.WriteFile(ctx, name, data, 0644)
	if err != nil {
		return GenerateResult{}, err
	}
	return GenerateResult{Provider: provider, Model: model, FilePath: resolved.Abs, ContentType: contentType, SizeBytes: len(data)}, nil
}

func extensionForContentType(contentType string) string {
	contentType = strings.ToLower(strings.TrimSpace(strings.Split(contentType, ";")[0]))
	switch contentType {
	case "image/png":
		return ".png"
	case "image/jpeg":
		return ".jpg"
	case "image/webp":
		return ".webp"
	case "image/gif":
		return ".gif"
	default:
		return ".bin"
	}
}

func detectContentType(data []byte) string {
	if len(data) >= 4 && data[0] == 0x89 && data[1] == 0x50 && data[2] == 0x4e && data[3] == 0x47 {
		return "image/png"
	}
	if len(data) >= 3 && data[0] == 0xff && data[1] == 0xd8 && data[2] == 0xff {
		return "image/jpeg"
	}
	if len(data) >= 12 && string(data[0:4]) == "RIFF" && string(data[8:12]) == "WEBP" {
		return "image/webp"
	}
	if len(data) >= 3 && string(data[0:3]) == "GIF" {
		return "image/gif"
	}
	return "application/octet-stream"
}
