// Package distribution defines inert runnable product distribution metadata.
package distribution

import (
	"github.com/fluxplane/agentruntime/core/channel"
	coresession "github.com/fluxplane/agentruntime/core/session"
)

// Spec describes a runnable/deployable package around one or more resource
// bundles. Resource bundles describe what exists; distributions describe how a
// product is launched and operated.
type Spec struct {
	Name                string                  `json:"name"`
	Title               string                  `json:"title,omitempty"`
	Description         string                  `json:"description,omitempty"`
	Author              string                  `json:"author,omitempty"`
	Version             string                  `json:"version,omitempty"`
	DefaultSession      coresession.Ref         `json:"default_session,omitempty"`
	DefaultConversation channel.ConversationRef `json:"default_conversation,omitempty"`
	DefaultModel        ModelDefault            `json:"default_model,omitempty"`
	Surfaces            Surfaces                `json:"surfaces,omitempty"`
	Build               BuildSpec              `json:"build,omitempty"`
	Commands            []Command               `json:"commands,omitempty"`
	Metadata            map[string]string       `json:"metadata,omitempty"`
}

// ModelDefault describes the preferred model used when a launcher does not
// receive an explicit override.
type ModelDefault struct {
	Provider string `json:"provider,omitempty"`
	Model    string `json:"model,omitempty"`
	UseCase  string `json:"use_case,omitempty"`
}

// Surfaces describes launch/deploy surfaces supported by the distribution.
type Surfaces struct {
	CLI      bool `json:"cli,omitempty"`
	REPL     bool `json:"repl,omitempty"`
	OneShot  bool `json:"one_shot,omitempty"`
	Serve    bool `json:"serve,omitempty"`
	Deploy   bool `json:"deploy,omitempty"`
	Validate bool `json:"validate,omitempty"`
	Status   bool `json:"status,omitempty"`
	Discover bool `json:"discover,omitempty"`
}

// BuildSpec describes packaging inputs and outputs for a distribution.
type BuildSpec struct {
	Assets []string         `json:"assets,omitempty"`
	Docker *DockerBuildSpec `json:"docker,omitempty"`
}

// DockerBuildSpec describes a future Docker image build target.
type DockerBuildSpec struct {
	Image       string            `json:"image,omitempty"`
	Tags        []string          `json:"tags,omitempty"`
	Dockerfile  string            `json:"dockerfile,omitempty"`
	Context     string            `json:"context,omitempty"`
	Platforms   []string          `json:"platforms,omitempty"`
	BuildArgs   map[string]string `json:"build_args,omitempty"`
	Annotations map[string]string `json:"annotations,omitempty"`
}

// Command describes a distribution-specific command surface.
type Command struct {
	Name        string            `json:"name"`
	Description string            `json:"description,omitempty"`
	Metadata    map[string]string `json:"metadata,omitempty"`
}
