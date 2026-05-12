package tool

// Set groups related model-facing tools into one capability surface.
//
// Tool sets are projections over operations, commands, workflows, or other
// invocation targets. They are useful for enabling a capability like
// "filesystem" or "browser" without treating each atomic tool as unrelated.
type Set struct {
	Name        string            `json:"name"`
	Description string            `json:"description,omitempty"`
	Tools       []Name            `json:"tools,omitempty"`
	Annotations map[string]string `json:"annotations,omitempty"`
}
