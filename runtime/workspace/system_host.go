package workspace

import (
	"os"
	"path/filepath"
	"strings"

	fpsystem "github.com/fluxplane/fluxplane-system"
	"github.com/fluxplane/fluxplane-system/systemkit"
)

// Config configures the host-backed workspace/system assembly.
type Config struct {
	Root                string
	Workspace           WorkspaceConfig
	AllowPrivateNetwork bool
}

// Host is a host-backed system assembled around a confined workspace.
type Host struct {
	workspace *HostWorkspace
	network   fpsystem.Network
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
	ws, err := NewHostWorkspace(realRoot, cfg.Workspace)
	if err != nil {
		return nil, err
	}
	env, err := NewEnvironment(ws)
	if err != nil {
		return nil, err
	}
	base, err := systemkit.NewSystem().WithHostNetwork().Build()
	if err != nil {
		return nil, err
	}
	return &Host{
		workspace: ws,
		network:   base.Network(),
		process:   NewProcessManagerWithEnvironment(ws, env),
		env:       env,
	}, nil
}

func (h *Host) Workspace() Workspace              { return h.workspace }
func (h *Host) FileSystem() fpsystem.FileSystem   { return h.workspace.System().FileSystem() }
func (h *Host) Network() fpsystem.Network         { return h.network }
func (h *Host) Process() fpsystem.ProcessManager  { return h.process }
func (h *Host) Environment() fpsystem.Environment { return h.env }
func (h *Host) Clock() fpsystem.Clock             { return h.workspace.System().Clock() }
