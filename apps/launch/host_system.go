// Package system defines runtime IO boundaries used by concrete operations.
package launch

import (
	"os"
	"path/filepath"
	"strings"
	"time"

	runtimeworkspace "github.com/fluxplane/fluxplane-core/runtime/workspace"
	fpsystem "github.com/fluxplane/fluxplane-system"
	"github.com/fluxplane/fluxplane-system/hostsystem"
)

// Config configures the host-backed system implementation.
type hostConfig struct {
	Root                string
	Workspace           runtimeworkspace.WorkspaceConfig
	AllowPrivateNetwork bool
}

// WorkspaceConfig configures additional workspace filesystem roots.
// Host is the default host-guarded system implementation.
type hostSystem struct {
	workspace *runtimeworkspace.HostWorkspace
	network   *hostsystem.Network
	process   *runtimeworkspace.HostProcess
	env       *runtimeworkspace.WorkspaceEnvironment
}

// NewHost returns a host-backed system rooted at cfg.Root.
func newHost(cfg hostConfig) (*hostSystem, error) {
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
	workspace, err := runtimeworkspace.NewHostWorkspace(realRoot, cfg.Workspace)
	if err != nil {
		return nil, err
	}
	env, err := runtimeworkspace.NewEnvironment(workspace)
	if err != nil {
		return nil, err
	}
	return &hostSystem{
		workspace: workspace,
		network:   hostsystem.NewNetwork(hostsystem.NetworkConfig{AllowPrivate: cfg.AllowPrivateNetwork, DialTimeout: 10 * time.Second}),
		process:   runtimeworkspace.NewProcessManagerWithEnvironment(workspace, env),
		env:       env,
	}, nil
}

// Workspace returns the workspace boundary.
func (h *hostSystem) Workspace() runtimeworkspace.Workspace { return h.workspace }

// FileSystem returns the host filesystem boundary.
func (h *hostSystem) FileSystem() fpsystem.FileSystem { return h.workspace.System().FileSystem() }

// Network returns the network boundary.
func (h *hostSystem) Network() fpsystem.Network { return h.network }

// Process returns the process boundary.
func (h *hostSystem) Process() fpsystem.ProcessManager { return h.process }

// Environment returns the host environment boundary.
func (h *hostSystem) Environment() fpsystem.Environment { return h.env }

// Clock returns the host clock boundary.
func (h *hostSystem) Clock() fpsystem.Clock { return h.workspace.System().Clock() }
