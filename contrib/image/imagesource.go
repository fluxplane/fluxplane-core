package image

import (
	"context"
	"encoding/base64"
	"fmt"
	runtimeworkspace "github.com/fluxplane/fluxplane-core/runtime/workspace"
	fpsystem "github.com/fluxplane/fluxplane-system"
	"github.com/fluxplane/fluxplane-system/systemkit"
	"mime"
	"path/filepath"
	"strings"
	"time"
)

func anthropicImageBlocks(ctx context.Context, sys fpsystem.System, images []string) ([]map[string]any, error) {
	blocks := make([]map[string]any, 0, len(images))
	for i, image := range images {
		source, err := imageSource(ctx, sys, image)
		if err != nil {
			return nil, fmt.Errorf("image[%d]: %w", i, err)
		}
		blocks = append(blocks, map[string]any{"type": "image", "source": source.anthropicSource()})
	}
	return blocks, nil
}

func openRouterVisionContent(ctx context.Context, sys fpsystem.System, images []string, prompt string) ([]map[string]any, error) {
	content := make([]map[string]any, 0, len(images)+1)
	for i, image := range images {
		source, err := imageSource(ctx, sys, image)
		if err != nil {
			return nil, fmt.Errorf("image[%d]: %w", i, err)
		}
		content = append(content, map[string]any{
			"type":      "image_url",
			"image_url": map[string]any{"url": source.dataURL()},
		})
	}
	content = append(content, map[string]any{"type": "text", "text": prompt})
	return content, nil
}

type resolvedImage struct {
	contentType string
	data        []byte
}

func (r resolvedImage) anthropicSource() map[string]any {
	return map[string]any{
		"type":       "base64",
		"media_type": r.contentType,
		"data":       base64.StdEncoding.EncodeToString(r.data),
	}
}

func (r resolvedImage) dataURL() string {
	return "data:" + r.contentType + ";base64," + base64.StdEncoding.EncodeToString(r.data)
}

func imageSource(ctx context.Context, sys fpsystem.System, raw string) (resolvedImage, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return resolvedImage{}, fmt.Errorf("image source cannot be empty")
	}
	if strings.HasPrefix(raw, "data:") {
		contentType, data, err := parseDataURL(raw)
		return resolvedImage{contentType: contentType, data: data}, err
	}
	if strings.HasPrefix(raw, "http://") || strings.HasPrefix(raw, "https://") {
		resp, err := systemkit.DoHTTP(ctx, sys.Network(), systemkit.HTTPRequest{
			URL:       raw,
			Method:    "GET",
			Timeout:   30 * time.Second,
			MaxBytes:  defaultMaxFileSize,
			UserAgent: "fluxplane/0.1",
		})
		if err != nil {
			return resolvedImage{}, err
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return resolvedImage{}, fmt.Errorf("HTTP %s", resp.Status)
		}
		contentType := strings.TrimSpace(strings.Split(resp.ContentType, ";")[0])
		if contentType == "" || !strings.HasPrefix(contentType, "image/") {
			contentType = detectContentType(resp.Body)
		}
		return resolvedImage{contentType: contentType, data: resp.Body}, nil
	}
	workspace := workspaceFromSystem(sys)
	if workspace == nil {
		return resolvedImage{}, fmt.Errorf("system workspace is not configured")
	}
	resolved, err := workspace.ResolveExisting(ctx, raw)
	if err != nil {
		return resolvedImage{}, err
	}
	fsys, err := runtimeworkspace.FileSystem(workspace)
	if err != nil {
		return resolvedImage{}, err
	}
	data, truncated, err := fpsystem.ReadFileLimit(ctx, fsys, runtimeworkspace.PathName(resolved), defaultMaxFileSize)
	if err != nil {
		return resolvedImage{}, err
	}
	if truncated {
		return resolvedImage{}, fmt.Errorf("%q exceeds maximum image size of %d bytes", raw, defaultMaxFileSize)
	}
	contentType := mimeTypeForPath(resolved.Rel)
	if contentType == "" {
		contentType = detectContentType(data)
	}
	if !strings.HasPrefix(contentType, "image/") {
		return resolvedImage{}, fmt.Errorf("cannot determine image type for %q", raw)
	}
	return resolvedImage{contentType: contentType, data: data}, nil
}

func parseDataURL(raw string) (string, []byte, error) {
	if !strings.HasPrefix(raw, "data:") {
		return "", nil, fmt.Errorf("not a data URL")
	}
	comma := strings.IndexByte(raw, ',')
	if comma < 0 {
		return "", nil, fmt.Errorf("malformed data URL")
	}
	meta := raw[len("data:"):comma]
	dataPart := raw[comma+1:]
	contentType := meta
	if semi := strings.IndexByte(meta, ';'); semi >= 0 {
		contentType = meta[:semi]
	}
	data, err := base64.StdEncoding.DecodeString(dataPart)
	if err != nil {
		return "", nil, err
	}
	if contentType == "" {
		contentType = detectContentType(data)
	}
	return contentType, data, nil
}

func mimeTypeForPath(path string) string {
	mediaType := mime.TypeByExtension(filepath.Ext(path))
	if strings.HasPrefix(mediaType, "image/") {
		return strings.Split(mediaType, ";")[0]
	}
	return ""
}
