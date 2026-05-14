// Package distribution assembles runnable distribution declarations.
package distribution

import (
	"context"

	"github.com/fluxplane/agentruntime/core/channel"
	coredistribution "github.com/fluxplane/agentruntime/core/distribution"
	"github.com/fluxplane/agentruntime/core/resource"
	coresession "github.com/fluxplane/agentruntime/core/session"
	clientapi "github.com/fluxplane/agentruntime/orchestration/client"
)

// Distribution is a runnable package declaration plus its local runtime hook.
type Distribution struct {
	Spec    coredistribution.Spec
	Bundles []resource.ContributionBundle
	Runtime Runtime
}

// Loaded is a distribution materialized from an external source, such as a
// local filesystem path, plus launch metadata that is not part of core specs.
type Loaded struct {
	Root         string
	Manifest     string
	Distribution Distribution
	Launch       LaunchConfig
	Diagnostics  []resource.Diagnostic
}

// LaunchConfig carries neutral launch metadata for daemon/runtime adapters.
type LaunchConfig struct {
	Connectors map[string]Connector
	Listeners  []Listener
	Channels   []Channel
}

// Connector describes one configured connector instance expected at launch.
type Connector struct {
	Kind string
}

// Listener describes one daemon listener expected at launch.
type Listener struct {
	Name string
	Type string
	Addr string
	Auth map[string]any
}

// Channel describes one daemon channel expected at launch.
type Channel struct {
	Name      string
	Type      string
	Connector string
	Listener  string
	Session   string
	Access    Access
}

// Access carries neutral channel access policy metadata.
type Access struct {
	Mode             string
	AllowUsers       []string
	DenyUsers        []string
	AllowChannels    []string
	DenyChannels     []string
	AllowKinds       []string
	DefaultTrust     string
	Operators        []string
	InternalUsers    []string
	InternalChannels []string
	Sharing          string
}

// Runtime opens a local session for a distribution.
type Runtime interface {
	OpenSession(context.Context, OpenRequest) (clientapi.SessionHandle, error)
}

// OpenRequest carries launcher-selected runtime options.
type OpenRequest struct {
	Session      coresession.Ref
	Conversation channel.ConversationRef
	Provider     string
	Model        string
	Thinking     string
	ThinkingSet  bool
	Effort       string
	EffortSet    bool
	Debug        bool
	Yolo         bool
}
