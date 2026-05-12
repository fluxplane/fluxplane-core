package operation

import "fmt"

// Set groups related atomic operations into a named capability surface.
//
// A set is descriptive: it does not execute anything and does not imply that
// every operation is projected as an LLM tool. Runtime and orchestration layers
// decide which operations are available for a caller/session.
type Set struct {
	Name        string            `json:"name"`
	Description string            `json:"description,omitempty"`
	Operations  []Ref             `json:"operations,omitempty"`
	Annotations map[string]string `json:"annotations,omitempty"`
}

// Validate checks the set's structural identity.
func (s Set) Validate() error {
	if s.Name == "" {
		return fmt.Errorf("operation set name is empty")
	}
	return nil
}
