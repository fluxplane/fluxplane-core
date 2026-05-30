package human

import (
	"context"
	"encoding/json"
)

// Clarifier collects human input through a channel or terminal adapter.
type Clarifier interface {
	Clarify(context.Context, ClarifyRequest) (ClarifyResult, error)
}

type ClarifyRequest struct {
	Prompt   string          `json:"prompt"`
	Schema   json.RawMessage `json:"schema,omitempty"`
	Defaults map[string]any  `json:"defaults,omitempty"`
}

type ClarifyResult struct {
	Answer any `json:"answer,omitempty"`
}
