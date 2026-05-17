package imageplugin

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/fluxplane/agentruntime/runtime/system"
)

type pollinationsProvider struct{}

func (pollinationsProvider) Info(context.Context, system.System) ProviderInfo {
	return ProviderInfo{
		Name:         "pollinations",
		Capabilities: []string{"generate"},
		Models:       []string{"flux", "turbo", "kontext"},
		DefaultModel: "flux",
		Configured:   true,
	}
}

func (pollinationsProvider) Generate(ctx context.Context, sys system.System, req GenerateRequest) (GenerateResult, error) {
	model := firstNonEmpty(req.Model, "flux")
	width := intDefault(req.Width, 1024)
	height := intDefault(req.Height, 1024)
	target := fmt.Sprintf(
		"https://image.pollinations.ai/prompt/%s?model=%s&width=%d&height=%d&nologo=true",
		url.PathEscape(req.Prompt),
		url.QueryEscape(model),
		width,
		height,
	)
	resp, err := sys.Network().DoHTTP(ctx, system.HTTPRequest{
		URL:       target,
		Method:    "GET",
		Timeout:   60 * time.Second,
		MaxBytes:  25 * 1024 * 1024,
		UserAgent: "agentruntime/0.1",
	})
	if err != nil {
		return GenerateResult{}, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return GenerateResult{}, fmt.Errorf("pollinations: HTTP %s: %s", resp.Status, string(resp.Body))
	}
	contentType := strings.TrimSpace(strings.Split(resp.ContentType, ";")[0])
	if contentType == "" || !strings.HasPrefix(contentType, "image/") {
		contentType = detectContentType(resp.Body)
	}
	return writeGeneratedImage(ctx, sys, "pollinations", model, req.Prompt, contentType, resp.Body)
}

type openAIImageProvider struct{}

func (openAIImageProvider) Info(ctx context.Context, sys system.System) ProviderInfo {
	configured, missing := configuredByEnv(ctx, sys, "OPENAI_API_KEY")
	return ProviderInfo{
		Name:         "openai",
		Capabilities: []string{"generate"},
		Models:       []string{"gpt-image-1", "dall-e-3", "dall-e-2"},
		DefaultModel: "gpt-image-1",
		Configured:   configured,
		Missing:      missing,
	}
}

func (openAIImageProvider) Generate(ctx context.Context, sys system.System, req GenerateRequest) (GenerateResult, error) {
	model := firstNonEmpty(req.Model, "gpt-image-1")
	body := map[string]any{
		"model":  model,
		"prompt": req.Prompt,
	}
	if strings.HasPrefix(model, "dall-e-") {
		body["response_format"] = "b64_json"
	}
	if req.Size != "" {
		body["size"] = req.Size
	}
	if req.Quality != "" {
		body["quality"] = req.Quality
	}
	if req.OutputFormat != "" {
		body["output_format"] = req.OutputFormat
	}
	resp, err := doJSON(ctx, sys, "https://api.openai.com/v1/images/generations", "Bearer "+env(ctx, sys, "OPENAI_API_KEY"), body, 25*1024*1024)
	if err != nil {
		return GenerateResult{}, err
	}
	var decoded struct {
		Data []struct {
			B64JSON string `json:"b64_json"`
			URL     string `json:"url"`
		} `json:"data"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(resp.Body, &decoded); err != nil {
		return GenerateResult{}, err
	}
	if decoded.Error != nil && decoded.Error.Message != "" {
		return GenerateResult{}, fmt.Errorf("openai: %s", decoded.Error.Message)
	}
	if len(decoded.Data) == 0 {
		return GenerateResult{}, fmt.Errorf("openai: response did not include image data")
	}
	if decoded.Data[0].B64JSON != "" {
		data, err := base64.StdEncoding.DecodeString(decoded.Data[0].B64JSON)
		if err != nil {
			return GenerateResult{}, fmt.Errorf("openai: decode image: %w", err)
		}
		return writeGeneratedImage(ctx, sys, "openai", model, req.Prompt, detectContentType(data), data)
	}
	if decoded.Data[0].URL == "" {
		return GenerateResult{}, fmt.Errorf("openai: response did not include b64_json or url image data")
	}
	imageResp, err := sys.Network().DoHTTP(ctx, system.HTTPRequest{
		URL:       decoded.Data[0].URL,
		Method:    "GET",
		Timeout:   60 * time.Second,
		MaxBytes:  25 * 1024 * 1024,
		UserAgent: "agentruntime/0.1",
	})
	if err != nil {
		return GenerateResult{}, err
	}
	if imageResp.StatusCode < 200 || imageResp.StatusCode >= 300 {
		return GenerateResult{}, fmt.Errorf("openai: image download HTTP %s: %s", imageResp.Status, string(imageResp.Body))
	}
	contentType := strings.TrimSpace(strings.Split(imageResp.ContentType, ";")[0])
	if contentType == "" || !strings.HasPrefix(contentType, "image/") {
		contentType = detectContentType(imageResp.Body)
	}
	return writeGeneratedImage(ctx, sys, "openai", model, req.Prompt, contentType, imageResp.Body)
}

type openRouterImageProvider struct{}

func (openRouterImageProvider) Info(ctx context.Context, sys system.System) ProviderInfo {
	configured, missing := configuredByEnv(ctx, sys, "OPENROUTER_API_KEY")
	return ProviderInfo{
		Name:         "openrouter",
		Capabilities: []string{"generate"},
		Models:       []string{"google/gemini-2.5-flash-image", "black-forest-labs/flux.2-pro", "openai/gpt-5-image"},
		DefaultModel: "google/gemini-2.5-flash-image",
		Configured:   configured,
		Missing:      missing,
	}
}

func (openRouterImageProvider) Generate(ctx context.Context, sys system.System, req GenerateRequest) (GenerateResult, error) {
	model := firstNonEmpty(req.Model, "google/gemini-2.5-flash-image")
	body := map[string]any{
		"model": model,
		"messages": []map[string]any{{
			"role":    "user",
			"content": req.Prompt,
		}},
		"modalities": []string{"image", "text"},
	}
	resp, err := doJSON(ctx, sys, "https://openrouter.ai/api/v1/chat/completions", "Bearer "+env(ctx, sys, "OPENROUTER_API_KEY"), body, 25*1024*1024)
	if err != nil {
		return GenerateResult{}, err
	}
	dataURL := jsonString(resp.Body, "choices", "0", "message", "images", "0", "image_url", "url")
	if dataURL == "" {
		return GenerateResult{}, fmt.Errorf("openrouter: response did not include generated image data")
	}
	contentType, data, err := parseDataURL(dataURL)
	if err != nil {
		return GenerateResult{}, err
	}
	return writeGeneratedImage(ctx, sys, "openrouter", model, req.Prompt, contentType, data)
}

type anthropicUnderstandingProvider struct{}

func (anthropicUnderstandingProvider) Info(ctx context.Context, sys system.System) ProviderInfo {
	configured, missing := configuredByEnv(ctx, sys, "ANTHROPIC_API_KEY")
	return ProviderInfo{
		Name:         "anthropic",
		Capabilities: []string{"understand"},
		Models:       []string{"claude-haiku-4-5-20251001", "claude-sonnet-4-6", "claude-opus-4-6"},
		DefaultModel: "claude-haiku-4-5-20251001",
		Configured:   configured,
		Missing:      missing,
	}
}

func (anthropicUnderstandingProvider) Understand(ctx context.Context, sys system.System, req UnderstandRequest) (UnderstandResult, error) {
	model := firstNonEmpty(req.Model, "claude-haiku-4-5-20251001")
	blocks, err := anthropicImageBlocks(ctx, sys, req.Images)
	if err != nil {
		return UnderstandResult{}, err
	}
	blocks = append(blocks, map[string]any{"type": "text", "text": firstNonEmpty(req.Prompt, defaultPrompt)})
	body := map[string]any{
		"model":      model,
		"max_tokens": 4096,
		"messages": []map[string]any{{
			"role":    "user",
			"content": blocks,
		}},
	}
	data, err := json.Marshal(body)
	if err != nil {
		return UnderstandResult{}, err
	}
	resp, err := sys.Network().DoHTTP(ctx, system.HTTPRequest{
		URL:    "https://api.anthropic.com/v1/messages",
		Method: "POST",
		Headers: map[string]string{
			"x-api-key":         env(ctx, sys, "ANTHROPIC_API_KEY"),
			"anthropic-version": "2023-06-01",
			"content-type":      "application/json",
		},
		Body:      string(data),
		Timeout:   60 * time.Second,
		MaxBytes:  4 * 1024 * 1024,
		UserAgent: "agentruntime/0.1",
	})
	if err != nil {
		return UnderstandResult{}, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return UnderstandResult{}, fmt.Errorf("anthropic: HTTP %s: %s", resp.Status, string(resp.Body))
	}
	return UnderstandResult{Provider: "anthropic", Model: model, Text: textFromContentResponse(resp.Body)}, nil
}

type openRouterUnderstandingProvider struct{}

func (openRouterUnderstandingProvider) Info(ctx context.Context, sys system.System) ProviderInfo {
	configured, missing := configuredByEnv(ctx, sys, "OPENROUTER_API_KEY")
	return ProviderInfo{
		Name:         "openrouter",
		Capabilities: []string{"understand"},
		Models:       []string{"anthropic/claude-haiku-4.5", "anthropic/claude-sonnet-4.6", "openrouter/free"},
		DefaultModel: "anthropic/claude-haiku-4.5",
		Configured:   configured,
		Missing:      missing,
	}
}

func (openRouterUnderstandingProvider) Understand(ctx context.Context, sys system.System, req UnderstandRequest) (UnderstandResult, error) {
	model := firstNonEmpty(req.Model, "anthropic/claude-haiku-4.5")
	content, err := openRouterVisionContent(ctx, sys, req.Images, firstNonEmpty(req.Prompt, defaultPrompt))
	if err != nil {
		return UnderstandResult{}, err
	}
	body := map[string]any{
		"model": model,
		"messages": []map[string]any{{
			"role":    "user",
			"content": content,
		}},
	}
	resp, err := doJSON(ctx, sys, "https://openrouter.ai/api/v1/chat/completions", "Bearer "+env(ctx, sys, "OPENROUTER_API_KEY"), body, 4*1024*1024)
	if err != nil {
		return UnderstandResult{}, err
	}
	text := jsonString(resp.Body, "choices", "0", "message", "content")
	if text == "" {
		return UnderstandResult{}, fmt.Errorf("openrouter: response did not include text content")
	}
	return UnderstandResult{Provider: "openrouter", Model: model, Text: text}, nil
}
