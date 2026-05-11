package llmagent

// ModelPolicy describes model selection constraints without binding to a
// concrete provider transport.
type ModelPolicy struct {
	Model        string            `json:"model,omitempty"`
	Provider     string            `json:"provider,omitempty"`
	UseCase      string            `json:"use_case,omitempty"`
	ApprovedOnly bool              `json:"approved_only,omitempty"`
	Annotations  map[string]string `json:"annotations,omitempty"`
}

// InferencePolicy describes model-call behavior.
type InferencePolicy struct {
	MaxOutputTokens int     `json:"max_output_tokens,omitempty"`
	Temperature     float64 `json:"temperature,omitempty"`
	ReasoningEffort string  `json:"reasoning_effort,omitempty"`
}

// Spec is the pure config payload for an LLM-backed agent driver.
//
// Runtime implementation belongs in runtime/agent/llmagent.
type Spec struct {
	Instructions string            `json:"instructions,omitempty"`
	Model        ModelPolicy       `json:"model,omitempty"`
	Inference    InferencePolicy   `json:"inference,omitempty"`
	Annotations  map[string]string `json:"annotations,omitempty"`
}
