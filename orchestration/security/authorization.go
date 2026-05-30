package security

import (
	"context"
	"fmt"
	runtimeworkspace "github.com/fluxplane/fluxplane-core/runtime/workspace"
	"github.com/fluxplane/fluxplane-policy"
	"github.com/fluxplane/fluxplane-policy/policyauth"
	fpsystem "github.com/fluxplane/fluxplane-system"
	"net"
	"os"
	"strings"
)

// Config controls policy enforcement at system boundaries.
type AuthConfig struct {
	TraceAllows bool
}

// System wraps sys so context-bearing filesystem, network, process, and
// environment accesses are checked against policyauth.AuthorizationContext when one
// is present on the call context. Calls without an authorization context keep
// the existing embedding behavior and are allowed.
func WithAuthorization(sys fpsystem.System, cfg AuthConfig) fpsystem.System {
	if sys == nil {
		return nil
	}
	return authorizedSystem{base: sys, cfg: cfg}
}

// Workspace wraps workspace so context-bearing workspace accesses are checked
// against policyauth.AuthorizationContext when one is present on the call context.
func WorkspaceWithAuthorization(workspace runtimeworkspace.Workspace, cfg AuthConfig) runtimeworkspace.Workspace {
	if workspace == nil {
		return nil
	}
	return authorizedWorkspace{base: workspace, cfg: cfg}
}

type authorizedSystem struct {
	base fpsystem.System
	cfg  AuthConfig
}

func (s authorizedSystem) FileSystem() fpsystem.FileSystem { return s.base.FileSystem() }

func (s authorizedSystem) Network() fpsystem.Network {
	if network := s.base.Network(); network != nil {
		return NetworkWithAuthorization(network, s.cfg)
	}
	return nil
}

func (s authorizedSystem) Process() fpsystem.ProcessManager {
	if process := s.base.Process(); process != nil {
		return ProcessWithAuthorization(process, s.cfg)
	}
	return nil
}

func (s authorizedSystem) Environment() fpsystem.Environment {
	if env := s.base.Environment(); env != nil {
		return EnvironmentWithAuthorization(env, s.cfg)
	}
	return nil
}

func (s authorizedSystem) Clock() fpsystem.Clock { return s.base.Clock() }

type authorizedWorkspace struct {
	base runtimeworkspace.Workspace
	cfg  AuthConfig
}

func (w authorizedWorkspace) Root() string                   { return w.base.Root() }
func (w authorizedWorkspace) Roots() []runtimeworkspace.Root { return w.base.Roots() }
func (w authorizedWorkspace) System() fpsystem.System        { return w.base.System() }

func (w authorizedWorkspace) read(ctx context.Context, resolved runtimeworkspace.ResolvedPath) error {
	return Authorize(ctx, w.cfg, policy.ResourceRef{Kind: policy.ResourcePath, Path: workspaceResourcePath(resolved.Rel)}, policy.ActionWorkspaceRead)
}
func (w authorizedWorkspace) write(ctx context.Context, resolved runtimeworkspace.ResolvedPath) error {
	return Authorize(ctx, w.cfg, policy.ResourceRef{Kind: policy.ResourcePath, Path: workspaceResourcePath(resolved.Rel)}, policy.ActionWorkspaceWrite)
}

func (w authorizedWorkspace) ResolveExisting(ctx context.Context, raw string) (runtimeworkspace.ResolvedPath, error) {
	resolved, err := w.base.ResolveExisting(ctx, raw)
	if err != nil {
		return runtimeworkspace.ResolvedPath{}, err
	}
	if err := w.read(ctx, resolved); err != nil {
		return runtimeworkspace.ResolvedPath{}, err
	}
	return resolved, nil
}

func (w authorizedWorkspace) ResolveCreate(ctx context.Context, raw string) (runtimeworkspace.ResolvedPath, error) {
	resolved, err := w.base.ResolveCreate(ctx, raw)
	if err != nil {
		return runtimeworkspace.ResolvedPath{}, err
	}
	if err := w.write(ctx, resolved); err != nil {
		return runtimeworkspace.ResolvedPath{}, err
	}
	return resolved, nil
}

func (w authorizedWorkspace) ReadFile(ctx context.Context, raw string, maxBytes int64) ([]byte, bool, runtimeworkspace.ResolvedPath, error) {
	resolved, err := w.ResolveExisting(ctx, raw)
	if err != nil {
		return nil, false, runtimeworkspace.ResolvedPath{}, err
	}
	data, truncated, _, err := w.base.ReadFile(ctx, raw, maxBytes)
	if err != nil {
		return nil, false, runtimeworkspace.ResolvedPath{}, err
	}
	return data, truncated, resolved, nil
}

func (w authorizedWorkspace) CreateScratch(ctx context.Context, prefix string) (runtimeworkspace.ScratchDir, error) {
	if err := Authorize(ctx, w.cfg, policy.ResourceRef{Kind: policy.ResourceWorkspace, Name: "scratch"}, policy.ActionWorkspaceWrite); err != nil {
		return nil, err
	}
	scratch, err := w.base.CreateScratch(ctx, prefix)
	if err != nil || scratch == nil {
		return scratch, err
	}
	return authorizedScratchDir{base: scratch, cfg: w.cfg}, nil
}

type authorizedScratchDir struct {
	base runtimeworkspace.ScratchDir
	cfg  AuthConfig
}

func (s authorizedScratchDir) Root() string { return s.base.Root() }

func (s authorizedScratchDir) WriteFile(ctx context.Context, raw string, data []byte, mode os.FileMode) (runtimeworkspace.ResolvedPath, error) {
	if err := Authorize(ctx, s.cfg, policy.ResourceRef{Kind: policy.ResourcePath, Path: workspaceResourcePath("@scratch/" + strings.TrimLeft(raw, "/"))}, policy.ActionWorkspaceWrite); err != nil {
		return runtimeworkspace.ResolvedPath{}, err
	}
	return s.base.WriteFile(ctx, raw, data, mode)
}

func (s authorizedScratchDir) RemoveAll(ctx context.Context) error {
	if err := Authorize(ctx, s.cfg, policy.ResourceRef{Kind: policy.ResourceWorkspace, Name: "scratch"}, policy.ActionWorkspaceWrite); err != nil {
		return err
	}
	return s.base.RemoveAll(ctx)
}

type authorizedNetwork struct {
	base fpsystem.Network
	cfg  AuthConfig
}

// Network wraps a network boundary with policy authorization.
func NetworkWithAuthorization(base fpsystem.Network, cfg AuthConfig) fpsystem.Network {
	if base == nil {
		return nil
	}
	return authorizedNetwork{base: base, cfg: cfg}
}

func (n authorizedNetwork) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	if err := Authorize(ctx, n.cfg, policy.ResourceRef{Kind: policy.ResourceNetwork, Name: strings.TrimSpace(address)}, policy.ActionNetworkConnect); err != nil {
		return nil, err
	}
	return n.base.DialContext(ctx, network, address)
}

func (n authorizedNetwork) Resolver() fpsystem.Resolver {
	return authorizedResolver{base: n.base.Resolver(), cfg: n.cfg}
}

type authorizedResolver struct {
	base fpsystem.Resolver
	cfg  AuthConfig
}

func (r authorizedResolver) fetch(ctx context.Context, name string) error {
	return Authorize(ctx, r.cfg, policy.ResourceRef{Kind: policy.ResourceNetwork, Name: strings.TrimSpace(name)}, policy.ActionNetworkFetch)
}

func (r authorizedResolver) LookupHost(ctx context.Context, host string) ([]string, error) {
	if err := r.fetch(ctx, host); err != nil {
		return nil, err
	}
	return r.base.LookupHost(ctx, host)
}

func (r authorizedResolver) LookupIPAddr(ctx context.Context, host string) ([]net.IPAddr, error) {
	if err := r.fetch(ctx, host); err != nil {
		return nil, err
	}
	return r.base.LookupIPAddr(ctx, host)
}

func (r authorizedResolver) LookupCNAME(ctx context.Context, host string) (string, error) {
	if err := r.fetch(ctx, host); err != nil {
		return "", err
	}
	return r.base.LookupCNAME(ctx, host)
}

func (r authorizedResolver) LookupMX(ctx context.Context, name string) ([]*net.MX, error) {
	if err := r.fetch(ctx, name); err != nil {
		return nil, err
	}
	return r.base.LookupMX(ctx, name)
}

func (r authorizedResolver) LookupSRV(ctx context.Context, service, proto, name string) (string, []*net.SRV, error) {
	if err := r.fetch(ctx, name); err != nil {
		return "", nil, err
	}
	return r.base.LookupSRV(ctx, service, proto, name)
}

func (r authorizedResolver) LookupTXT(ctx context.Context, name string) ([]string, error) {
	if err := r.fetch(ctx, name); err != nil {
		return nil, err
	}
	return r.base.LookupTXT(ctx, name)
}

type authorizedProcessManager struct {
	base fpsystem.ProcessManager
	cfg  AuthConfig
}

// Process wraps a process manager with policy authorization.
func ProcessWithAuthorization(base fpsystem.ProcessManager, cfg AuthConfig) fpsystem.ProcessManager {
	if base == nil {
		return nil
	}
	return authorizedProcessManager{base: base, cfg: cfg}
}

func (p authorizedProcessManager) Run(ctx context.Context, req fpsystem.ProcessRequest) (fpsystem.ProcessResult, error) {
	if err := p.exec(ctx, req.Command); err != nil {
		return fpsystem.ProcessResult{}, err
	}
	return p.base.Run(ctx, req)
}

func (p authorizedProcessManager) Start(ctx context.Context, req fpsystem.ProcessRequest) (fpsystem.ProcessHandle, error) {
	if err := p.exec(ctx, req.Command); err != nil {
		return nil, err
	}
	handle, err := p.base.Start(ctx, req)
	if err != nil || handle == nil {
		return handle, err
	}
	return authorizedProcessHandle{base: handle, cfg: p.cfg}, nil
}

func (p authorizedProcessManager) Ensure(ctx context.Context, req fpsystem.ProcessRequest) (fpsystem.ProcessHandle, bool, error) {
	if err := p.exec(ctx, req.Command); err != nil {
		return nil, false, err
	}
	handle, created, err := p.base.Ensure(ctx, req)
	if err != nil || handle == nil {
		return handle, created, err
	}
	return authorizedProcessHandle{base: handle, cfg: p.cfg}, created, nil
}

func (p authorizedProcessManager) Group(name string) fpsystem.ProcessGroup {
	return authorizedProcessGroup{base: p.base.Group(name), cfg: p.cfg, name: strings.TrimSpace(name)}
}

func (p authorizedProcessManager) List(ctx context.Context) ([]fpsystem.ProcessInfo, error) {
	if err := Authorize(ctx, p.cfg, policy.ResourceRef{Kind: policy.ResourceProcess, Name: "*"}, policy.ActionProcessExec); err != nil {
		return nil, err
	}
	return p.base.List(ctx)
}

type authorizedProcessGroup struct {
	base fpsystem.ProcessGroup
	cfg  AuthConfig
	name string
}

func (g authorizedProcessGroup) Name() string { return g.base.Name() }

func (g authorizedProcessGroup) List(ctx context.Context) ([]fpsystem.ProcessInfo, error) {
	if err := Authorize(ctx, g.cfg, policy.ResourceRef{Kind: policy.ResourceProcess, Name: g.name}, policy.ActionProcessExec); err != nil {
		return nil, err
	}
	return g.base.List(ctx)
}

func (g authorizedProcessGroup) Subscribe(ctx context.Context) <-chan fpsystem.ProcessEvent {
	return g.base.Subscribe(ctx)
}

func (g authorizedProcessGroup) Wait(ctx context.Context) (fpsystem.ProcessResult, error) {
	if err := Authorize(ctx, g.cfg, policy.ResourceRef{Kind: policy.ResourceProcess, Name: g.name}, policy.ActionProcessExec); err != nil {
		return fpsystem.ProcessResult{}, err
	}
	return g.base.Wait(ctx)
}

func (g authorizedProcessGroup) Stop(ctx context.Context) error {
	if err := Authorize(ctx, g.cfg, policy.ResourceRef{Kind: policy.ResourceProcess, Name: g.name}, policy.ActionProcessAdmin); err != nil {
		return err
	}
	return g.base.Stop(ctx)
}

func (g authorizedProcessGroup) Kill(ctx context.Context) error {
	if err := Authorize(ctx, g.cfg, policy.ResourceRef{Kind: policy.ResourceProcess, Name: g.name}, policy.ActionProcessAdmin); err != nil {
		return err
	}
	return g.base.Kill(ctx)
}

func (g authorizedProcessGroup) Signal(ctx context.Context, signal fpsystem.ProcessSignal) error {
	if err := Authorize(ctx, g.cfg, policy.ResourceRef{Kind: policy.ResourceProcess, Name: g.name}, policy.ActionProcessAdmin); err != nil {
		return err
	}
	return g.base.Signal(ctx, signal)
}

func (g authorizedProcessGroup) Interrupt(ctx context.Context) error {
	return g.Signal(ctx, fpsystem.ProcessSignalInterrupt)
}
func (g authorizedProcessGroup) Reload(ctx context.Context) error {
	return g.Signal(ctx, fpsystem.ProcessSignalReload)
}
func (g authorizedProcessGroup) Pause(ctx context.Context) error {
	return g.Signal(ctx, fpsystem.ProcessSignalPause)
}
func (g authorizedProcessGroup) Resume(ctx context.Context) error {
	return g.Signal(ctx, fpsystem.ProcessSignalResume)
}

func (g authorizedProcessGroup) Write(ctx context.Context, data []byte) (int, error) {
	if err := Authorize(ctx, g.cfg, policy.ResourceRef{Kind: policy.ResourceProcess, Name: g.name}, policy.ActionProcessAdmin); err != nil {
		return 0, err
	}
	return g.base.Write(ctx, data)
}

func (g authorizedProcessGroup) CloseInput(ctx context.Context) error {
	if err := Authorize(ctx, g.cfg, policy.ResourceRef{Kind: policy.ResourceProcess, Name: g.name}, policy.ActionProcessAdmin); err != nil {
		return err
	}
	return g.base.CloseInput(ctx)
}

func (g authorizedProcessGroup) Restart(ctx context.Context) (fpsystem.ProcessHandle, error) {
	if err := Authorize(ctx, g.cfg, policy.ResourceRef{Kind: policy.ResourceProcess, Name: g.name}, policy.ActionProcessAdmin); err != nil {
		return nil, err
	}
	handle, err := g.base.Restart(ctx)
	if err != nil || handle == nil {
		return handle, err
	}
	return authorizedProcessHandle{base: handle, cfg: g.cfg}, nil
}

type authorizedProcessHandle struct {
	base fpsystem.ProcessHandle
	cfg  AuthConfig
}

func (h authorizedProcessHandle) ID() string { return h.base.ID() }

func (h authorizedProcessHandle) Info() fpsystem.ProcessInfo { return h.base.Info() }

func (h authorizedProcessHandle) Subscribe(ctx context.Context) <-chan fpsystem.ProcessEvent {
	if err := h.authorize(ctx, policy.ActionProcessExec); err != nil {
		ch := make(chan fpsystem.ProcessEvent)
		close(ch)
		return ch
	}
	return h.base.Subscribe(ctx)
}

func (h authorizedProcessHandle) Wait(ctx context.Context) (fpsystem.ProcessResult, error) {
	if err := h.authorize(ctx, policy.ActionProcessExec); err != nil {
		return fpsystem.ProcessResult{}, err
	}
	return h.base.Wait(ctx)
}

func (h authorizedProcessHandle) Stop(ctx context.Context) error {
	if err := h.authorize(ctx, policy.ActionProcessAdmin); err != nil {
		return err
	}
	return h.base.Stop(ctx)
}

func (h authorizedProcessHandle) Kill(ctx context.Context) error {
	if err := h.authorize(ctx, policy.ActionProcessAdmin); err != nil {
		return err
	}
	return h.base.Kill(ctx)
}

func (h authorizedProcessHandle) Signal(ctx context.Context, signal fpsystem.ProcessSignal) error {
	if err := h.authorize(ctx, policy.ActionProcessAdmin); err != nil {
		return err
	}
	return h.base.Signal(ctx, signal)
}

func (h authorizedProcessHandle) Interrupt(ctx context.Context) error {
	return h.Signal(ctx, fpsystem.ProcessSignalInterrupt)
}

func (h authorizedProcessHandle) Reload(ctx context.Context) error {
	return h.Signal(ctx, fpsystem.ProcessSignalReload)
}

func (h authorizedProcessHandle) Pause(ctx context.Context) error {
	return h.Signal(ctx, fpsystem.ProcessSignalPause)
}

func (h authorizedProcessHandle) Resume(ctx context.Context) error {
	return h.Signal(ctx, fpsystem.ProcessSignalResume)
}

func (h authorizedProcessHandle) Write(ctx context.Context, data []byte) (int, error) {
	if err := h.authorize(ctx, policy.ActionProcessAdmin); err != nil {
		return 0, err
	}
	return h.base.Write(ctx, data)
}

func (h authorizedProcessHandle) CloseInput(ctx context.Context) error {
	if err := h.authorize(ctx, policy.ActionProcessAdmin); err != nil {
		return err
	}
	return h.base.CloseInput(ctx)
}

func (h authorizedProcessHandle) Restart(ctx context.Context) (fpsystem.ProcessHandle, error) {
	if err := h.authorize(ctx, policy.ActionProcessAdmin); err != nil {
		return nil, err
	}
	handle, err := h.base.Restart(ctx)
	if err != nil || handle == nil {
		return handle, err
	}
	return authorizedProcessHandle{base: handle, cfg: h.cfg}, nil
}

func (h authorizedProcessHandle) Detach(ctx context.Context) error {
	if err := h.authorize(ctx, policy.ActionProcessAdmin); err != nil {
		return err
	}
	return h.base.Detach(ctx)
}

func (h authorizedProcessHandle) authorize(ctx context.Context, action policy.Action) error {
	return Authorize(ctx, h.cfg, policy.ResourceRef{Kind: policy.ResourceProcess, Name: processHandleResourceName(h.base)}, action)
}

func processHandleResourceName(handle fpsystem.ProcessHandle) string {
	if handle == nil {
		return "*"
	}
	info := handle.Info()
	for _, candidate := range []string{info.Label, info.ID, info.Command, handle.ID()} {
		if name := strings.TrimSpace(candidate); name != "" {
			return name
		}
	}
	return "*"
}

func (g authorizedProcessGroup) Detach(ctx context.Context) error {
	if err := Authorize(ctx, g.cfg, policy.ResourceRef{Kind: policy.ResourceProcess, Name: g.name}, policy.ActionProcessAdmin); err != nil {
		return err
	}
	return g.base.Detach(ctx)
}

func (p authorizedProcessManager) exec(ctx context.Context, command string) error {
	return Authorize(ctx, p.cfg, policy.ResourceRef{Kind: policy.ResourceProcess, Name: strings.TrimSpace(command)}, policy.ActionProcessExec)
}

// NetworkURLAuthorizer returns a URL authorization callback for network-backed
// adapters.
func NetworkURLAuthorizer(cfg AuthConfig) func(context.Context, string) error {
	return func(ctx context.Context, target string) error {
		return authorizeNetworkURL(ctx, cfg, target)
	}
}

func authorizeNetworkURL(ctx context.Context, cfg AuthConfig, target string) error {
	target = strings.TrimSpace(target)
	if target == "" {
		target = "*"
	}
	return Authorize(ctx, cfg, policy.ResourceRef{Kind: policy.ResourceNetwork, Name: target}, policy.ActionNetworkFetch)
}

type authorizedEnvironment struct {
	base fpsystem.Environment
	cfg  AuthConfig
}

// Environment wraps an environment boundary with policy authorization.
func EnvironmentWithAuthorization(base fpsystem.Environment, cfg AuthConfig) fpsystem.Environment {
	if base == nil {
		return nil
	}
	return authorizedEnvironment{base: base, cfg: cfg}
}

func (e authorizedEnvironment) Lookup(ctx context.Context, key string) (string, bool, error) {
	key = strings.TrimSpace(key)
	if key == "" {
		return "", false, fmt.Errorf("environment key is empty")
	}
	if err := Authorize(ctx, e.cfg, policy.ResourceRef{Kind: policy.ResourceSecret, Name: "env/" + key}, policy.ActionSecretRead); err != nil {
		return "", false, err
	}
	return e.base.Lookup(ctx, key)
}

func (e authorizedEnvironment) ResolveExecutable(ctx context.Context, name string) (string, bool, error) {
	resolver, ok := e.base.(fpsystem.ExecutableResolver)
	if !ok {
		return "", false, nil
	}
	return resolver.ResolveExecutable(ctx, name)
}

// Authorize evaluates one policy authorization request from ctx.
func Authorize(ctx context.Context, cfg AuthConfig, resource policy.ResourceRef, action policy.Action) error {
	if ctx == nil {
		ctx = context.Background()
	}
	auth, ok := policyauth.AuthorizationFromContext(ctx)
	if !ok || auth.Policy.IsZero() {
		return nil
	}
	auth.TraceAllows = auth.TraceAllows || cfg.TraceAllows
	req := policy.AuthorizationRequest{
		Subjects: auth.Subjects,
		Trust:    auth.Trust,
		Resource: resource,
		Action:   action,
	}
	evaluation := policy.EvaluateAuthorization(auth.Policy, req)
	policyauth.EmitAuthorizationDecision(ctx, auth, req, evaluation)
	if evaluation.Decision == policy.DecisionAllow {
		return nil
	}
	return fmt.Errorf("authorization_%s: %s %s: %s", evaluation.Decision, action, resourceLabel(resource), evaluation.Reason)
}

func workspaceResourcePath(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "."
	}
	return strings.TrimPrefix(raw, "./")
}

func resourceLabel(resource policy.ResourceRef) string {
	switch {
	case resource.Name != "":
		return string(resource.Kind) + ":" + resource.Name
	case resource.Path != "":
		return string(resource.Kind) + ":" + resource.Path
	case resource.ID != "":
		return string(resource.Kind) + ":" + resource.ID
	default:
		return string(resource.Kind)
	}
}
