// Package openrouter adapts OpenRouter's OpenAI-compatible Responses API.
package openrouter

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	adapterllm "github.com/fluxplane/agentruntime/adapters/llm"
	"github.com/fluxplane/agentruntime/adapters/llm/openai"
	corellm "github.com/fluxplane/agentruntime/core/llm"
	"github.com/openai/openai-go/v3/option"
)

const (
	DefaultBaseURL = "https://openrouter.ai/api/v1"
	defaultTitle   = "coder"
)

// Config configures an OpenRouter Responses model.
type Config struct {
	Model             string
	APIKey            string
	BaseURL           string
	HTTPClient        *http.Client
	HTTPReferer       string
	Title             string
	ParallelToolCalls bool
	Redactor          adapterllm.Redactor
	Runtime           openai.ResponsesRuntimeConfig
	Pricing           []corellm.PricingSpec
	ReasoningEffort   string
	ReasoningSummary  string
}

// New returns an OpenRouter-backed Responses model.
func New(cfg Config) (*openai.Model, error) {
	apiKey := strings.TrimSpace(cfg.APIKey)
	if apiKey == "" {
		apiKey = strings.TrimSpace(os.Getenv("OPENROUTER_API_KEY"))
	}
	if apiKey == "" {
		return nil, fmt.Errorf("openrouter: OPENROUTER_API_KEY is not set")
	}
	baseURL := strings.TrimSpace(cfg.BaseURL)
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	runtime := cfg.Runtime
	if runtime.Transport == "" {
		runtime.Transport = openai.ResponsesTransportAuto
	}
	if runtime.Cache == "" {
		runtime.Cache = openai.ResponsesCacheAuto
	}
	if runtime.Continuation == "" || runtime.Continuation == openai.ResponsesContinuationAuto {
		runtime.Continuation = openai.ResponsesContinuationReplay
	}
	title := strings.TrimSpace(cfg.Title)
	if title == "" {
		title = defaultTitle
	}
	options := []option.RequestOption{
		option.WithHeader("X-Title", title),
		option.WithMiddleware(responsesReliabilityMiddleware()),
		option.WithMiddleware(errorBodyMiddleware()),
	}
	if referer := strings.TrimSpace(cfg.HTTPReferer); referer != "" {
		options = append(options, option.WithHeader("HTTP-Referer", referer))
	}
	return openai.New(openai.Config{
		Model:             cfg.Model,
		ProviderName:      "openrouter",
		APIName:           "openrouter.responses",
		BaseURL:           baseURL,
		APIKey:            apiKey,
		HTTPClient:        cfg.HTTPClient,
		Runtime:           runtime,
		Pricing:           cfg.Pricing,
		ReasoningEffort:   cfg.ReasoningEffort,
		ReasoningSummary:  cfg.ReasoningSummary,
		ParallelToolCalls: cfg.ParallelToolCalls,
		Redactor:          cfg.Redactor,
		RequestOptions:    options,
	})
}

func errorBodyMiddleware() option.Middleware {
	return func(req *http.Request, next option.MiddlewareNext) (*http.Response, error) {
		resp, err := next(req)
		if err != nil {
			return nil, err
		}
		if resp == nil || resp.StatusCode < 400 {
			return resp, nil
		}
		body := readAndReplaceBody(resp)
		return nil, fmt.Errorf("openrouter: HTTP %s: %s", resp.Status, body)
	}
}

func readAndReplaceBody(resp *http.Response) string {
	if resp == nil || resp.Body == nil {
		return ""
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	_ = resp.Body.Close()
	resp.Body = io.NopCloser(bytes.NewReader(data))
	if err != nil {
		return "read response body: " + err.Error()
	}
	body := strings.TrimSpace(string(data))
	if body == "" {
		return "<empty response body>"
	}
	return body
}
