package image

import (
	"context"

	"github.com/fluxplane/engine/runtime/system"
)

const defaultPrompt = "Describe this image in detail. Include any text, diagrams, UI elements, or notable visual features."

// GenerationProvider generates images from text prompts.
type GenerationProvider interface {
	Info(context.Context, system.System) ProviderInfo
	Generate(context.Context, system.System, GenerateRequest) (GenerateResult, error)
}

// UnderstandingProvider analyzes image inputs.
type UnderstandingProvider interface {
	Info(context.Context, system.System) ProviderInfo
	Understand(context.Context, system.System, UnderstandRequest) (UnderstandResult, error)
}

// ProviderInfo describes provider capability and configuration state.
type ProviderInfo struct {
	Name         string   `json:"name" jsonschema:"description=Provider name.,required"`
	Capabilities []string `json:"capabilities" jsonschema:"description=Supported image capabilities.,required"`
	Models       []string `json:"models,omitempty" jsonschema:"description=Supported model ids."`
	DefaultModel string   `json:"default_model,omitempty" jsonschema:"description=Default provider model."`
	Configured   bool     `json:"configured" jsonschema:"description=Whether required configuration is available.,required"`
	Missing      []string `json:"missing,omitempty" jsonschema:"description=Missing configuration keys."`
}

// GenerateRequest is the image generation operation input.
type GenerateRequest struct {
	Action       string `json:"action,omitempty" jsonschema:"description=Action discriminator for the image tool.,enum=generate"`
	Provider     string `json:"provider,omitempty" jsonschema:"description=Optional provider name: pollinations, openai, or openrouter."`
	Model        string `json:"model,omitempty" jsonschema:"description=Optional provider model."`
	Prompt       string `json:"prompt" jsonschema:"description=Text description of the image to generate.,required"`
	Width        int    `json:"width,omitempty" jsonschema:"description=Image width for providers that support dimensions."`
	Height       int    `json:"height,omitempty" jsonschema:"description=Image height for providers that support dimensions."`
	Size         string `json:"size,omitempty" jsonschema:"description=Provider-specific size string, such as 1024x1024."`
	Quality      string `json:"quality,omitempty" jsonschema:"description=Provider-specific quality setting."`
	OutputFormat string `json:"output_format,omitempty" jsonschema:"description=Provider-specific output format."`
}

// GenerateResult is the image generation operation output.
type GenerateResult struct {
	Provider    string `json:"provider" jsonschema:"description=Provider that generated the image.,required"`
	Model       string `json:"model,omitempty" jsonschema:"description=Provider model that generated the image."`
	FilePath    string `json:"file_path" jsonschema:"description=Workspace scratch path containing the generated image.,required"`
	ContentType string `json:"content_type" jsonschema:"description=Image content type.,required"`
	SizeBytes   int    `json:"size_bytes" jsonschema:"description=Image byte size.,required"`
}

// UnderstandRequest is the image understanding operation input.
type UnderstandRequest struct {
	Action   string   `json:"action,omitempty" jsonschema:"description=Action discriminator for the image tool.,enum=understand"`
	Provider string   `json:"provider,omitempty" jsonschema:"description=Optional provider name: anthropic or openrouter."`
	Model    string   `json:"model,omitempty" jsonschema:"description=Optional provider model."`
	Images   []string `json:"images" jsonschema:"description=Image sources: workspace file paths, HTTP(S) URLs, or data: URIs.,required,minItems=1"`
	Prompt   string   `json:"prompt,omitempty" jsonschema:"description=What to analyze. Defaults to a detailed description."`
}

// UnderstandResult is the image understanding operation output.
type UnderstandResult struct {
	Provider string `json:"provider" jsonschema:"description=Provider that analyzed the images.,required"`
	Model    string `json:"model,omitempty" jsonschema:"description=Provider model that analyzed the images."`
	Text     string `json:"text" jsonschema:"description=Image analysis text.,required"`
}

type providersOutput struct {
	Generation    []ProviderInfo `json:"generation" jsonschema:"description=Image generation providers.,required"`
	Understanding []ProviderInfo `json:"understanding" jsonschema:"description=Image understanding providers.,required"`
}

type providersInput struct {
	Action string `json:"action,omitempty" jsonschema:"description=Action discriminator for the image tool.,enum=info"`
}

type imageActionInput struct {
	Action       string   `json:"action" jsonschema:"description=Image action to perform.,enum=generate,enum=understand,enum=info,required"`
	Provider     string   `json:"provider,omitempty" jsonschema:"description=Optional provider name. Generation: pollinations, openai, or openrouter. Understanding: anthropic or openrouter."`
	Model        string   `json:"model,omitempty" jsonschema:"description=Optional provider model."`
	Prompt       string   `json:"prompt,omitempty" jsonschema:"description=Generation prompt, or understanding prompt. Required for generate."`
	Width        int      `json:"width,omitempty" jsonschema:"description=Image width for generation providers that support dimensions."`
	Height       int      `json:"height,omitempty" jsonschema:"description=Image height for generation providers that support dimensions."`
	Size         string   `json:"size,omitempty" jsonschema:"description=Provider-specific generation size string, such as 1024x1024."`
	Quality      string   `json:"quality,omitempty" jsonschema:"description=Provider-specific generation quality setting."`
	OutputFormat string   `json:"output_format,omitempty" jsonschema:"description=Provider-specific generation output format."`
	Images       []string `json:"images,omitempty" jsonschema:"description=Image sources for understand: workspace file paths, HTTP(S) URLs, or data: URIs. Required for understand.,minItems=1"`
}
