// Package system defines runtime IO boundaries used by concrete operations.
package system

import (
	"os"
	"path/filepath"
	"strings"

	runtimeworkspace "github.com/fluxplane/fluxplane-core/runtime/workspace"
	fpsystem "github.com/fluxplane/fluxplane-system"
)

// Config configures the host-backed system implementation.
type Config struct {
	Root                string
	Workspace           WorkspaceConfig
	AllowPrivateNetwork bool
}

// WorkspaceConfig configures additional workspace filesystem roots.
type WorkspaceConfig struct {
	Roots       []WorkspaceRootConfig
	ScratchRoot string
	EnvFiles    []string
}

// WorkspaceRootConfig configures one named workspace root.
type WorkspaceRootConfig struct {
	Name     string
	Path     string
	Access   WorkspaceAccess
	Create   bool
	EnvFiles []string
}

// WorkspaceAccess controls permitted operations for a workspace root.
type WorkspaceAccess string

const (
	WorkspaceAccessReadOnly  WorkspaceAccess = "read_only"
	WorkspaceAccessReadWrite WorkspaceAccess = "read_write"
)

// Host is the default host-guarded system implementation.
type Host struct {
	workspace *HostWorkspace
	network   *HostNetwork
	process   *HostProcess
	env       *WorkspaceEnvironment
}

// NewHost returns a host-backed system rooted at cfg.Root.
func NewHost(cfg Config) (*Host, error) {
	root := cfg.Root
	if strings.TrimSpace(root) == "" {
		wd, err := os.Getwd()
		if err != nil {
			return nil, err
		}
		root = wd
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	realRoot, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return nil, err
	}
	workspace, err := newHostWorkspace(realRoot, cfg.Workspace)
	if err != nil {
		return nil, err
	}
	env, err := newWorkspaceEnvironment(workspace)
	if err != nil {
		return nil, err
	}
	return &Host{
		workspace: workspace,
		network:   &HostNetwork{allowPrivate: cfg.AllowPrivateNetwork},
		process:   NewHostProcessWithEnvironment(workspace, env),
		env:       env,
	}, nil
}

// Workspace returns the workspace boundary.
func (h *Host) Workspace() runtimeworkspace.Workspace { return h.workspace }

// FileSystem returns the host filesystem boundary.
func (h *Host) FileSystem() fpsystem.FileSystem { return h.workspace.System().FileSystem() }

// Network returns the network boundary.
func (h *Host) Network() fpsystem.Network { return h.network }

// Process returns the process boundary.
func (h *Host) Process() fpsystem.ProcessManager { return h.process }

// Environment returns the host environment boundary.
func (h *Host) Environment() fpsystem.Environment { return h.env }

// Clock returns the host clock boundary.
func (h *Host) Clock() fpsystem.Clock { return h.workspace.System().Clock() }
