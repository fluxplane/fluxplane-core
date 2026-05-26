// Package distribution assembles runnable distribution declarations.
package distribution

import (
	"context"
	"fmt"
	"strings"

	"github.com/fluxplane/fluxplane-core/core/channel"
	coredistribution "github.com/fluxplane/fluxplane-core/core/distribution"
	"github.com/fluxplane/fluxplane-core/core/operation"
	"github.com/fluxplane/fluxplane-core/core/resource"
	coresession "github.com/fluxplane/fluxplane-core/core/session"
	coretrigger "github.com/fluxplane/fluxplane-core/core/trigger"
	clientapi "github.com/fluxplane/fluxplane-core/orchestration/client"
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
	Profile      string
	Profiles     []string
	Distribution Distribution
	Launch       LaunchConfig
	Diagnostics  []resource.Diagnostic
}

// LaunchConfig carries neutral launch metadata for daemon/runtime adapters.
type LaunchConfig struct {
	Listeners []Listener
	Channels  []Channel
	Triggers  []coretrigger.Spec
	Workspace WorkspaceConfig
	Data      DataConfig
	Events    EventsConfig
}

// DataConfig carries runtime-owned durable data store settings.
type DataConfig struct {
	Store DataStoreConfig
}

// DataStoreConfig declares where materialized runtime data is stored.
type DataStoreConfig struct {
	Kind   string
	DSN    string
	DSNEnv string
}

// EventsConfig carries runtime-owned durable event store settings.
type EventsConfig struct {
	Store EventStoreConfig
}

// EventStoreConfig declares where append-only runtime events are stored.
type EventStoreConfig struct {
	Kind         string
	DSN          string
	DSNEnv       string
	Stream       string
	Subject      string
	CreateStream bool
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

// CloneWorkspaceConfig copies a workspace config deeply enough for callers to
// append roots and env files without mutating the source config.
func CloneWorkspaceConfig(cfg WorkspaceConfig) WorkspaceConfig {
	return WorkspaceConfig{
		Roots:       CloneWorkspaceRoots(cfg.Roots),
		ScratchRoot: strings.TrimSpace(cfg.ScratchRoot),
		EnvFiles:    append([]string(nil), cfg.EnvFiles...),
	}
}

// CloneWorkspaceRoots copies workspace roots, including each root's env files.
func CloneWorkspaceRoots(roots []WorkspaceRoot) []WorkspaceRoot {
	if len(roots) == 0 {
		return nil
	}
	out := make([]WorkspaceRoot, 0, len(roots))
	for _, root := range roots {
		root.EnvFiles = append([]string(nil), root.EnvFiles...)
		out = append(out, root)
	}
	return out
}

// MergeWorkspaceConfig overlays request-time workspace fields onto defaults.
func MergeWorkspaceConfig(base, override WorkspaceConfig) WorkspaceConfig {
	out := CloneWorkspaceConfig(base)
	out.Roots = append(out.Roots, CloneWorkspaceRoots(override.Roots)...)
	out.EnvFiles = append(out.EnvFiles, override.EnvFiles...)
	if strings.TrimSpace(override.ScratchRoot) != "" {
		out.ScratchRoot = strings.TrimSpace(override.ScratchRoot)
	}
	return out
}

// TrimStrings trims values and drops empty entries.
func TrimStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	out := make([]string, 0, len(values))
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			out = append(out, value)
		}
	}
	return out
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

// Listener describes one daemon listener expected at launch.
type Listener struct {
	Name string
	Type string
	Addr string
	Auth map[string]any
}

// Channel describes one daemon channel expected at launch.
type Channel struct {
	Name     string
	Type     string
	Instance string
	Listener string
	Session  string
	Access   Access
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
	Launch              LaunchConfig
	Session             coresession.Ref
	Conversation        channel.ConversationRef
	Provider            string
	Model               string
	Thinking            string
	ThinkingSet         bool
	Effort              string
	EffortSet           bool
	MaxToolRisk         operation.RiskLevel
	Debug               bool
	Yolo                bool
	Dev                 bool
	AllowPluginAuthEnv  bool
	AllowPrivateNetwork bool
}
