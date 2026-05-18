// Package distribution assembles runnable distribution declarations.
package distribution

import (
	"context"
	"fmt"
	"strings"

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
	Workspace  WorkspaceConfig
}

// WorkspaceConfig carries filesystem roots used by local launch adapters.
type WorkspaceConfig struct {
	Roots       []WorkspaceRoot
	ScratchRoot string
	EnvFiles    []string
}

// WorkspaceRoot describes one additional local workspace root.
type WorkspaceRoot struct {
	Name     string
	Path     string
	Access   string
	Create   bool
	EnvFiles []string
}

// ParseWorkspaceRoots parses repeated local workspace root flag values. Values
// may be PATH or NAME=PATH. Unnamed roots are assigned names from the path base.
func ParseWorkspaceRoots(values []string) ([]WorkspaceRoot, error) {
	if len(values) == 0 {
		return nil, nil
	}
	roots := make([]WorkspaceRoot, 0, len(values))
	seen := map[string]struct{}{}
	for _, raw := range values {
		value := strings.TrimSpace(raw)
		if value == "" {
			return nil, fmt.Errorf("workspace-root: value is empty")
		}
		name, path, hasName := strings.Cut(value, "=")
		if hasName {
			name = strings.TrimSpace(name)
			path = strings.TrimSpace(path)
			if name == "" {
				return nil, fmt.Errorf("workspace-root: name is empty in %q", raw)
			}
			if strings.Contains(name, "/") || strings.Contains(name, "\\") || strings.HasPrefix(name, "@") {
				return nil, fmt.Errorf("workspace-root: invalid name %q", name)
			}
		} else {
			path = value
			name = workspaceRootNameFromPath(path)
		}
		if path == "" {
			return nil, fmt.Errorf("workspace-root: path is empty in %q", raw)
		}
		key := strings.ToLower(name)
		if _, ok := seen[key]; ok {
			return nil, fmt.Errorf("workspace-root: duplicate name %q", name)
		}
		seen[key] = struct{}{}
		roots = append(roots, WorkspaceRoot{Name: name, Path: path, Access: "read_write"})
	}
	return roots, nil
}

func workspaceRootNameFromPath(value string) string {
	trimmed := strings.TrimSpace(value)
	for strings.HasSuffix(trimmed, "/") || strings.HasSuffix(trimmed, "\\") {
		trimmed = strings.TrimSuffix(strings.TrimSuffix(trimmed, "/"), "\\")
	}
	if trimmed == "" || trimmed == "." {
		return "root"
	}
	parts := strings.FieldsFunc(trimmed, func(r rune) bool { return r == '/' || r == '\\' })
	if len(parts) == 0 {
		return "root"
	}
	name := strings.TrimSpace(parts[len(parts)-1])
	if name == "" || name == "." || name == ".." {
		return "root"
	}
	return name
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
	Instance  string
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
	Launch       LaunchConfig
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
	Dev          bool
}
