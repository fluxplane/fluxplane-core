package command

import (
	"fmt"
	"strings"

	"github.com/fluxplane/fluxplane-core/core/invocation"
	"github.com/fluxplane/fluxplane-core/core/operation"
	"github.com/fluxplane/fluxplane-core/core/policy"
)

const (
	// CompletionFlagsAnnotation names a comma-separated list of command flag
	// names for presentation-layer completion.
	CompletionFlagsAnnotation = "completion.flags"
)

// Path identifies a command in channel-facing command space.
type Path []string

// String returns a stable slash-like display form for the path.
func (p Path) String() string {
	if len(p) == 0 {
		return ""
	}
	return "/" + strings.Join(p, "/")
}

// Spec is an inert channel-facing invocation descriptor.
type Spec struct {
	Path        Path                    `json:"path"`
	Description string                  `json:"description,omitempty"`
	Target      invocation.Target       `json:"target"`
	Input       operation.Type          `json:"input,omitempty"`
	Output      operation.Type          `json:"output,omitempty"`
	Policy      policy.InvocationPolicy `json:"policy,omitempty"`
	Annotations map[string]string       `json:"annotations,omitempty"`
}

// Invocation is a parsed command invocation. Caller and trust belong to channel
// inbound envelopes, not to the command itself.
type Invocation struct {
	Path  Path            `json:"path"`
	Args  []string        `json:"args,omitempty"`
	Input operation.Value `json:"input,omitempty"`
}

// Validate checks that the invocation has a usable command path.
func (i Invocation) Validate() error {
	if pathKey(i.Path) == "" {
		return fmt.Errorf("command: invocation path is empty")
	}
	return nil
}
