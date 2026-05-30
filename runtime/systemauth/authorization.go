package systemauth

import (
	"context"
	"fmt"
	"net"
	"os"
	"strings"
	"time"

	"github.com/fluxplane/fluxplane-core/core/policy"
	"github.com/fluxplane/fluxplane-core/runtime/system"
	fpsystem "github.com/fluxplane/fluxplane-system"
)

// Config controls policy enforcement at system boundaries.
type Config struct {
	TraceAllows bool
}

// System wraps sys so context-bearing filesystem, network, process, and
// environment accesses are checked against policy.AuthorizationContext when one
// is present on the call context. Calls without an authorization context keep
// the existing embedding behavior and are allowed.
func System(sys system.System, cfg Config) system.System {
	if sys == nil {
		return nil
	}
	return authorizedSystem{base: sys, cfg: cfg}
}

type authorizedSystem struct {
	base system.System
	cfg  Config
}

func (s authorizedSystem) Workspace() system.Workspace {
	if workspace := s.base.Workspace(); workspace != nil {
		return authorizedWorkspace{base: workspace, cfg: s.cfg}
	}
	return nil
}

func (s authorizedSystem) Network() system.Network {
	if network := s.base.Network(); network != nil {
		return Network(network, s.cfg)
	}
	return nil
}

func (s authorizedSystem) Process() system.ProcessManager {
	if process := s.base.Process(); process != nil {
		return Process(process, s.cfg)
	}
	return nil
}

func (s authorizedSystem) Environment() system.Environment {
	if env := s.base.Environment(); env != nil {
		return Environment(env, s.cfg)
	}
	return nil
}

type authorizedWorkspace struct {
	base system.Workspace
	cfg  Config
}

func (w authorizedWorkspace) Root() string                  { return w.base.Root() }
func (w authorizedWorkspace) Roots() []system.WorkspaceRoot { return w.base.Roots() }
func (w authorizedWorkspace) System() fpsystem.System       { return w.base.System() }

func (w authorizedWorkspace) read(ctx context.Context, resolved system.ResolvedPath) error {
	return Authorize(ctx, w.cfg, policy.ResourceRef{Kind: policy.ResourcePath, Path: workspaceResourcePath(resolved.Rel)}, policy.ActionWorkspaceRead)
}
func (w authorizedWorkspace) write(ctx context.Context, resolved system.ResolvedPath) error {
	return Authorize(ctx, w.cfg, policy.ResourceRef{Kind: policy.ResourcePath, Path: workspaceResourcePath(resolved.Rel)}, policy.ActionWorkspaceWrite)
}

func (w authorizedWorkspace) ResolveExisting(ctx context.Context, raw string) (system.ResolvedPath, error) {
	resolved, err := w.base.ResolveExisting(ctx, raw)
	if err != nil {
		return system.ResolvedPath{}, err
	}
	if err := w.read(ctx, resolved); err != nil {
		return system.ResolvedPath{}, err
	}
	return resolved, nil
}

func (w authorizedWorkspace) ResolveCreate(ctx context.Context, raw string) (system.ResolvedPath, error) {
	resolved, err := w.base.ResolveCreate(ctx, raw)
	if err != nil {
		return system.ResolvedPath{}, err
	}
	if err := w.write(ctx, resolved); err != nil {
		return system.ResolvedPath{}, err
	}
	return resolved, nil
}

func (w authorizedWorkspace) CreateScratch(ctx context.Context, prefix string) (system.ScratchDir, error) {
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
	base system.ScratchDir
	cfg  Config
}

func (s authorizedScratchDir) Root() string { return s.base.Root() }

func (s authorizedScratchDir) WriteFile(ctx context.Context, raw string, data []byte, mode os.FileMode) (system.ResolvedPath, error) {
	if err := Authorize(ctx, s.cfg, policy.ResourceRef{Kind: policy.ResourcePath, Path: workspaceResourcePath("@scratch/" + strings.TrimLeft(raw, "/"))}, policy.ActionWorkspaceWrite); err != nil {
		return system.ResolvedPath{}, err
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
	cfg  Config
}

// Network wraps a network boundary with policy authorization.
func Network(base fpsystem.Network, cfg Config) fpsystem.Network {
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
	cfg  Config
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
	base system.ProcessManager
	cfg  Config
}

// Process wraps a process manager with policy authorization.
func Process(base system.ProcessManager, cfg Config) system.ProcessManager {
	if base == nil {
		return nil
	}
	return authorizedProcessManager{base: base, cfg: cfg}
}

func (p authorizedProcessManager) Run(ctx context.Context, req system.ProcessRequest) (system.ProcessResult, error) {
	if err := p.exec(ctx, req.Command); err != nil {
		return system.ProcessResult{}, err
	}
	return p.base.Run(ctx, req)
}

func (p authorizedProcessManager) Start(ctx context.Context, req system.ProcessRequest) (system.ProcessHandle, error) {
	if err := p.exec(ctx, req.Command); err != nil {
		return nil, err
	}
	return p.base.Start(ctx, req)
}

func (p authorizedProcessManager) Ensure(ctx context.Context, req system.ProcessRequest) (system.ProcessHandle, bool, error) {
	if err := p.exec(ctx, req.Command); err != nil {
		return nil, false, err
	}
	return p.base.Ensure(ctx, req)
}

func (p authorizedProcessManager) List(ctx context.Context) ([]system.ProcessInfo, error) {
	if err := Authorize(ctx, p.cfg, policy.ResourceRef{Kind: policy.ResourceProcess, Name: "*"}, policy.ActionProcessExec); err != nil {
		return nil, err
	}
	return p.base.List(ctx)
}

func (p authorizedProcessManager) Status(ctx context.Context, id string) (system.ProcessInfo, error) {
	if err := Authorize(ctx, p.cfg, policy.ResourceRef{Kind: policy.ResourceProcess, ID: strings.TrimSpace(id)}, policy.ActionProcessExec); err != nil {
		return system.ProcessInfo{}, err
	}
	return p.base.Status(ctx, id)
}

func (p authorizedProcessManager) Output(ctx context.Context, id string) (system.ProcessOutput, error) {
	if err := Authorize(ctx, p.cfg, policy.ResourceRef{Kind: policy.ResourceProcess, ID: strings.TrimSpace(id)}, policy.ActionProcessExec); err != nil {
		return system.ProcessOutput{}, err
	}
	return p.base.Output(ctx, id)
}

func (p authorizedProcessManager) Wait(ctx context.Context, id string, timeout time.Duration) (system.ProcessResult, error) {
	if err := Authorize(ctx, p.cfg, policy.ResourceRef{Kind: policy.ResourceProcess, ID: strings.TrimSpace(id)}, policy.ActionProcessExec); err != nil {
		return system.ProcessResult{}, err
	}
	return p.base.Wait(ctx, id, timeout)
}

func (p authorizedProcessManager) Stop(ctx context.Context, id string) error {
	if err := Authorize(ctx, p.cfg, policy.ResourceRef{Kind: policy.ResourceProcess, ID: strings.TrimSpace(id)}, policy.ActionProcessAdmin); err != nil {
		return err
	}
	return p.base.Stop(ctx, id)
}

func (p authorizedProcessManager) Kill(ctx context.Context, id string) error {
	if err := Authorize(ctx, p.cfg, policy.ResourceRef{Kind: policy.ResourceProcess, ID: strings.TrimSpace(id)}, policy.ActionProcessAdmin); err != nil {
		return err
	}
	return p.base.Kill(ctx, id)
}

func (p authorizedProcessManager) exec(ctx context.Context, command string) error {
	return Authorize(ctx, p.cfg, policy.ResourceRef{Kind: policy.ResourceProcess, Name: strings.TrimSpace(command)}, policy.ActionProcessExec)
}

// NetworkURLAuthorizer returns a URL authorization callback for network-backed
// adapters.
func NetworkURLAuthorizer(cfg Config) func(context.Context, string) error {
	return func(ctx context.Context, target string) error {
		return authorizeNetworkURL(ctx, cfg, target)
	}
}

func authorizeNetworkURL(ctx context.Context, cfg Config, target string) error {
	target = strings.TrimSpace(target)
	if target == "" {
		target = "*"
	}
	return Authorize(ctx, cfg, policy.ResourceRef{Kind: policy.ResourceNetwork, Name: target}, policy.ActionNetworkFetch)
}

type authorizedEnvironment struct {
	base system.Environment
	cfg  Config
}

// Environment wraps an environment boundary with policy authorization.
func Environment(base system.Environment, cfg Config) system.Environment {
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
	resolver, ok := e.base.(system.ExecutableResolver)
	if !ok {
		return "", false, nil
	}
	return resolver.ResolveExecutable(ctx, name)
}

// Authorize evaluates one policy authorization request from ctx.
func Authorize(ctx context.Context, cfg Config, resource policy.ResourceRef, action policy.Action) error {
	if ctx == nil {
		ctx = context.Background()
	}
	auth, ok := policy.AuthorizationFromContext(ctx)
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
	policy.EmitAuthorizationDecision(ctx, auth, req, evaluation)
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
