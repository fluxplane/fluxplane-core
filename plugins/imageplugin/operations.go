package imageplugin

import (
	"context"
	"fmt"
	"strings"

	"github.com/fluxplane/agentruntime/core/operation"
)

func (p Plugin) generate(ctx operation.Context, req GenerateRequest) operation.Result {
	req.Prompt = strings.TrimSpace(req.Prompt)
	if req.Prompt == "" {
		return operation.Failed("invalid_image_generate_input", "prompt is required", nil)
	}
	provider, err := selectGenerationProvider(ctx, p.system, p.generators, req.Provider)
	if err != nil {
		return operation.Failed("image_generate_provider_unavailable", err.Error(), map[string]any{"providers": p.providerOutput(ctx)})
	}
	result, err := provider.Generate(ctx, p.system, req)
	if err != nil {
		return operation.Failed("image_generate_failed", err.Error(), map[string]any{"provider": provider.Info(ctx, p.system).Name})
	}
	text := fmt.Sprintf("Generated image with %s", result.Provider)
	if result.Model != "" {
		text += "/" + result.Model
	}
	text += fmt.Sprintf("\nFile: %s\nContent-Type: %s\nBytes: %d", result.FilePath, result.ContentType, result.SizeBytes)
	return operation.OK(operation.Rendered{Text: text, Data: result})
}

func (p Plugin) understand(ctx operation.Context, req UnderstandRequest) operation.Result {
	if len(req.Images) == 0 {
		return operation.Failed("invalid_image_understand_input", "at least one image is required", nil)
	}
	provider, err := selectUnderstandingProvider(ctx, p.system, p.understanders, req.Provider)
	if err != nil {
		return operation.Failed("image_understand_provider_unavailable", err.Error(), map[string]any{"providers": p.providerOutput(ctx)})
	}
	result, err := provider.Understand(ctx, p.system, req)
	if err != nil {
		return operation.Failed("image_understand_failed", err.Error(), map[string]any{"provider": provider.Info(ctx, p.system).Name})
	}
	text := strings.TrimSpace(result.Text)
	if text == "" {
		text = "(no text response from image understanding provider)"
	}
	return operation.OK(operation.Rendered{Text: text, Data: result})
}

func (p Plugin) providers(ctx operation.Context, _ providersInput) operation.Result {
	out := p.providerOutput(ctx)
	var lines []string
	lines = append(lines, "Image generation providers:")
	for _, provider := range out.Generation {
		lines = append(lines, providerLine(provider))
	}
	lines = append(lines, "Image understanding providers:")
	for _, provider := range out.Understanding {
		lines = append(lines, providerLine(provider))
	}
	return operation.OK(operation.Rendered{Text: strings.Join(lines, "\n"), Data: out})
}

func (p Plugin) providerOutput(ctx context.Context) providersOutput {
	out := providersOutput{}
	for _, provider := range p.generators {
		if provider != nil {
			out.Generation = append(out.Generation, provider.Info(ctx, p.system))
		}
	}
	for _, provider := range p.understanders {
		if provider != nil {
			out.Understanding = append(out.Understanding, provider.Info(ctx, p.system))
		}
	}
	sortProviderInfos(out.Generation)
	sortProviderInfos(out.Understanding)
	return out
}

func providerLine(info ProviderInfo) string {
	status := "configured"
	if !info.Configured {
		status = "missing " + strings.Join(info.Missing, ", ")
	}
	model := info.DefaultModel
	if model == "" {
		model = "(none)"
	}
	return fmt.Sprintf("- %s [%s] default=%s capabilities=%s", info.Name, status, model, strings.Join(info.Capabilities, ","))
}
