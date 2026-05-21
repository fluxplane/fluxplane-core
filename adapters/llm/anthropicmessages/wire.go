package anthropicmessages

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	adapterllm "github.com/fluxplane/engine/adapters/llm"
	"github.com/fluxplane/engine/core/operation"
)

type messageRequest struct {
	Model             string           `json:"model"`
	MaxTokens         int              `json:"max_tokens"`
	Messages          []message        `json:"messages"`
	System            []contentBlock   `json:"system,omitempty"`
	Tools             []toolDefinition `json:"tools,omitempty"`
	Stream            bool             `json:"stream,omitempty"`
	Temperature       *float64         `json:"temperature,omitempty"`
	Thinking          *thinkingConfig  `json:"thinking,omitempty"`
	Effort            string           `json:"reasoning_effort,omitempty"`
	OutputConfig      *outputConfig    `json:"output_config,omitempty"`
	CacheControl      *cacheControl    `json:"cache_control,omitempty"`
	ContextManagement json.RawMessage  `json:"context_management,omitempty"`
	Metadata          map[string]any   `json:"metadata,omitempty"`
	Betas             []string         `json:"-"`
	ClaudeSessionID   string           `json:"-"`
}

type thinkingConfig struct {
	Type         string `json:"type"`
	BudgetTokens int    `json:"budget_tokens,omitempty"`
}

type outputConfig struct {
	Effort string          `json:"effort,omitempty"`
	Format json.RawMessage `json:"format,omitempty"`
}

type message struct {
	Role    string         `json:"role"`
	Content []contentBlock `json:"content"`
}

type contentBlock struct {
	Type         string          `json:"type"`
	Text         string          `json:"text,omitempty"`
	ID           string          `json:"id,omitempty"`
	Name         string          `json:"name,omitempty"`
	Input        json.RawMessage `json:"input,omitempty"`
	ToolUseID    string          `json:"tool_use_id,omitempty"`
	Content      any             `json:"content,omitempty"`
	IsError      bool            `json:"is_error,omitempty"`
	Thinking     string          `json:"thinking,omitempty"`
	Signature    string          `json:"signature,omitempty"`
	CacheControl *cacheControl   `json:"cache_control,omitempty"`
}

type cacheControl struct {
	Type string `json:"type"`
	TTL  string `json:"ttl,omitempty"`
}

type MessageRequest = messageRequest
type Message = message
type ContentBlock = contentBlock
type ThinkingConfig = thinkingConfig
type OutputConfig = outputConfig
type CacheControl = cacheControl
type ToolDefinition = toolDefinition

type toolDefinition struct {
	Name         string         `json:"name"`
	Description  string         `json:"description,omitempty"`
	InputSchema  map[string]any `json:"input_schema"`
	CacheControl *cacheControl  `json:"cache_control,omitempty"`
}

type messageResponse struct {
	ID      string         `json:"id"`
	Type    string         `json:"type"`
	Role    string         `json:"role"`
	Model   string         `json:"model"`
	Content []contentBlock `json:"content"`
	Usage   usageWire      `json:"usage"`
}

type usageWire struct {
	InputTokens              int64 `json:"input_tokens,omitempty"`
	CacheCreationInputTokens int64 `json:"cache_creation_input_tokens,omitempty"`
	CacheReadInputTokens     int64 `json:"cache_read_input_tokens,omitempty"`
	OutputTokens             int64 `json:"output_tokens,omitempty"`
	ReasoningOutputTokens    int64 `json:"reasoning_output_tokens,omitempty"`
}

func toolDefinitions(tools []adapterllm.ToolSpec) ([]toolDefinition, error) {
	out := make([]toolDefinition, 0, len(tools))
	for _, spec := range tools {
		params, err := schemaParams(spec.InputSchema)
		if err != nil {
			return nil, fmt.Errorf("anthropic messages: tool %q schema: %w", spec.Name, err)
		}
		out = append(out, toolDefinition{
			Name:        string(spec.Name),
			Description: spec.Description,
			InputSchema: params,
		})
	}
	return out, nil
}

func schemaParams(schema operation.Schema) (map[string]any, error) {
	if len(schema.Data) == 0 {
		return map[string]any{
			"type":                 "object",
			"additionalProperties": true,
		}, nil
	}
	var params map[string]any
	if err := json.Unmarshal(schema.Data, &params); err != nil {
		return nil, err
	}
	if params == nil {
		params = map[string]any{"type": "object"}
	}
	return params, nil
}

func applyPromptCache(req *messageRequest) {
	cache := &cacheControl{Type: "ephemeral", TTL: "1h"}
	if len(req.System) > 0 {
		req.System[len(req.System)-1].CacheControl = cache
	}
	for i := len(req.Messages) - 1; i >= 0; i-- {
		if len(req.Messages[i].Content) == 0 {
			continue
		}
		last := len(req.Messages[i].Content) - 1
		req.Messages[i].Content[last].CacheControl = cache
		break
	}
	if len(req.Tools) > 0 {
		req.Tools[len(req.Tools)-1].CacheControl = cache
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func normalizeThinking(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "on", "enabled":
		return "on"
	case "off":
		return "off"
	default:
		return "auto"
	}
}

func mergeAnthropicBetaHeader(headers http.Header, betas []string) {
	if len(betas) == 0 {
		return
	}
	seen := map[string]bool{}
	var merged []string
	for _, beta := range strings.Split(headers.Get("Anthropic-Beta"), ",") {
		beta = strings.TrimSpace(beta)
		if beta == "" || seen[beta] {
			continue
		}
		seen[beta] = true
		merged = append(merged, beta)
	}
	for _, beta := range betas {
		beta = strings.TrimSpace(beta)
		if beta == "" || seen[beta] {
			continue
		}
		seen[beta] = true
		merged = append(merged, beta)
	}
	if len(merged) > 0 {
		headers.Set("Anthropic-Beta", strings.Join(merged, ","))
	}
}
