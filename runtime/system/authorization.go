package system

import (
	"context"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"strings"

	"github.com/fluxplane/agentruntime/core/event"
	"github.com/fluxplane/agentruntime/core/policy"
)

// AuthorizationConfig controls policy enforcement at the System boundary.
type AuthorizationConfig struct {
	TraceAllows bool
}

// WithAuthorization wraps sys so context-bearing filesystem, network, process,
// and environment accesses are checked against policy.AuthorizationContext when
// one is present on the call context. Calls without an authorization context
// keep the existing embedding behavior and are allowed.
func WithAuthorization(sys System, cfg AuthorizationConfig) System {
	if sys == nil {
		return nil
	}
	return authorizedSystem{base: sys, cfg: cfg}
}

type authorizedSystem struct {
	base System
	cfg  AuthorizationConfig
}

func (s authorizedSystem) Workspace() Workspace {
	if workspace := s.base.Workspace(); workspace != nil {
		return authorizedWorkspace{base: workspace, cfg: s.cfg}
	}
	return nil
}

func (s authorizedSystem) Network() Network {
	if network := s.base.Network(); network != nil {
		return authorizedNetwork{base: network, cfg: s.cfg}
	}
	return nil
}

func (s authorizedSystem) Process() ProcessManager {
	if process := s.base.Process(); process != nil {
		return authorizedProcessManager{base: process, cfg: s.cfg}
	}
	return nil
}

func (s authorizedSystem) Browser() BrowserManager {
	if browser := s.base.Browser(); browser != nil {
		return authorizedBrowserManager{base: browser, cfg: s.cfg}
	}
	return nil
}
func (s authorizedSystem) Clarifier() Clarifier { return s.base.Clarifier() }

func (s authorizedSystem) Environment() Environment {
	if env := s.base.Environment(); env != nil {
		return authorizedEnvironment{base: env, cfg: s.cfg}
	}
	return nil
}

type authorizedWorkspace struct {
	base Workspace
	cfg  AuthorizationConfig
}

func (w authorizedWorkspace) Root() string           { return w.base.Root() }
func (w authorizedWorkspace) Roots() []WorkspaceRoot { return w.base.Roots() }
func (w authorizedWorkspace) read(ctx context.Context, resolved ResolvedPath) error {
	return authorizeSystem(ctx, w.cfg, policy.ResourceRef{Kind: policy.ResourcePath, Path: workspaceResourcePath(resolved.Rel)}, policy.ActionWorkspaceRead)
}
func (w authorizedWorkspace) write(ctx context.Context, resolved ResolvedPath) error {
	return authorizeSystem(ctx, w.cfg, policy.ResourceRef{Kind: policy.ResourcePath, Path: workspaceResourcePath(resolved.Rel)}, policy.ActionWorkspaceWrite)
}

func (w authorizedWorkspace) ResolveExisting(ctx context.Context, raw string) (ResolvedPath, error) {
	resolved, err := w.base.ResolveExisting(ctx, raw)
	if err != nil {
		return ResolvedPath{}, err
	}
	if err := w.read(ctx, resolved); err != nil {
		return ResolvedPath{}, err
	}
	return resolved, nil
}

func (w authorizedWorkspace) ResolveCreate(ctx context.Context, raw string) (ResolvedPath, error) {
	resolved, err := w.base.ResolveCreate(ctx, raw)
	if err != nil {
		return ResolvedPath{}, err
	}
	if err := w.write(ctx, resolved); err != nil {
		return ResolvedPath{}, err
	}
	return resolved, nil
}

func (w authorizedWorkspace) ReadFile(ctx context.Context, raw string, maxBytes int64) ([]byte, bool, ResolvedPath, error) {
	resolved, err := w.base.ResolveExisting(ctx, raw)
	if err != nil {
		return nil, false, ResolvedPath{}, err
	}
	if err := w.read(ctx, resolved); err != nil {
		return nil, false, ResolvedPath{}, err
	}
	return w.base.ReadFile(ctx, raw, maxBytes)
}

func (w authorizedWorkspace) ReadFileLines(ctx context.Context, raw string, start, end int, maxBytes int64) ([]byte, int, bool, ResolvedPath, error) {
	resolved, err := w.base.ResolveExisting(ctx, raw)
	if err != nil {
		return nil, 0, false, ResolvedPath{}, err
	}
	if err := w.read(ctx, resolved); err != nil {
		return nil, 0, false, ResolvedPath{}, err
	}
	return w.base.ReadFileLines(ctx, raw, start, end, maxBytes)
}

func (w authorizedWorkspace) WriteFile(ctx context.Context, raw string, data []byte, mode os.FileMode, overwrite bool) (ResolvedPath, error) {
	resolved, err := w.base.ResolveCreate(ctx, raw)
	if err != nil {
		return ResolvedPath{}, err
	}
	if err := w.write(ctx, resolved); err != nil {
		return ResolvedPath{}, err
	}
	return w.base.WriteFile(ctx, raw, data, mode, overwrite)
}

func (w authorizedWorkspace) CopyFile(ctx context.Context, rawSrc, rawDst string, overwrite bool) (ResolvedPath, ResolvedPath, int64, error) {
	src, err := w.base.ResolveExisting(ctx, rawSrc)
	if err != nil {
		return ResolvedPath{}, ResolvedPath{}, 0, err
	}
	if err := w.read(ctx, src); err != nil {
		return ResolvedPath{}, ResolvedPath{}, 0, err
	}
	dst, err := w.base.ResolveCreate(ctx, rawDst)
	if err != nil {
		return ResolvedPath{}, ResolvedPath{}, 0, err
	}
	if err := w.write(ctx, dst); err != nil {
		return ResolvedPath{}, ResolvedPath{}, 0, err
	}
	return w.base.CopyFile(ctx, rawSrc, rawDst, overwrite)
}

func (w authorizedWorkspace) MoveFile(ctx context.Context, rawSrc, rawDst string, overwrite bool) (ResolvedPath, ResolvedPath, int64, error) {
	src, err := w.base.ResolveExisting(ctx, rawSrc)
	if err != nil {
		return ResolvedPath{}, ResolvedPath{}, 0, err
	}
	if err := w.write(ctx, src); err != nil {
		return ResolvedPath{}, ResolvedPath{}, 0, err
	}
	dst, err := w.base.ResolveCreate(ctx, rawDst)
	if err != nil {
		return ResolvedPath{}, ResolvedPath{}, 0, err
	}
	if err := w.write(ctx, dst); err != nil {
		return ResolvedPath{}, ResolvedPath{}, 0, err
	}
	return w.base.MoveFile(ctx, rawSrc, rawDst, overwrite)
}

func (w authorizedWorkspace) MkdirAll(ctx context.Context, raw string, mode os.FileMode) (ResolvedPath, error) {
	resolved, err := w.base.ResolveCreate(ctx, raw)
	if err != nil {
		return ResolvedPath{}, err
	}
	if err := w.write(ctx, resolved); err != nil {
		return ResolvedPath{}, err
	}
	return w.base.MkdirAll(ctx, raw, mode)
}

func (w authorizedWorkspace) Remove(ctx context.Context, raw string) (ResolvedPath, error) {
	resolved, err := w.base.ResolveExisting(ctx, raw)
	if err != nil {
		return ResolvedPath{}, err
	}
	if err := w.write(ctx, resolved); err != nil {
		return ResolvedPath{}, err
	}
	return w.base.Remove(ctx, raw)
}

func (w authorizedWorkspace) Stat(ctx context.Context, raw string) (fs.FileInfo, ResolvedPath, error) {
	resolved, err := w.base.ResolveExisting(ctx, raw)
	if err != nil {
		return nil, ResolvedPath{}, err
	}
	if err := w.read(ctx, resolved); err != nil {
		return nil, ResolvedPath{}, err
	}
	return w.base.Stat(ctx, raw)
}

func (w authorizedWorkspace) ReadDir(ctx context.Context, raw string) ([]fs.DirEntry, ResolvedPath, error) {
	resolved, err := w.base.ResolveExisting(ctx, raw)
	if err != nil {
		return nil, ResolvedPath{}, err
	}
	if err := w.read(ctx, resolved); err != nil {
		return nil, ResolvedPath{}, err
	}
	return w.base.ReadDir(ctx, raw)
}

func (w authorizedWorkspace) Walk(ctx context.Context, raw string, opts WalkOptions) ([]WalkEntry, ResolvedPath, bool, error) {
	resolved, err := w.base.ResolveExisting(ctx, raw)
	if err != nil {
		return nil, ResolvedPath{}, false, err
	}
	if err := w.read(ctx, resolved); err != nil {
		return nil, ResolvedPath{}, false, err
	}
	return w.base.Walk(ctx, raw, opts)
}

func (w authorizedWorkspace) Glob(ctx context.Context, pattern string, opts GlobOptions) ([]ResolvedPath, bool, error) {
	base := opts.Base
	if strings.TrimSpace(base) == "" {
		base = "."
	}
	resolved, err := w.base.ResolveExisting(ctx, base)
	if err != nil {
		return nil, false, err
	}
	if err := w.read(ctx, resolved); err != nil {
		return nil, false, err
	}
	return w.base.Glob(ctx, pattern, opts)
}

func (w authorizedWorkspace) CreateScratch(ctx context.Context, prefix string) (ScratchDir, error) {
	if err := authorizeSystem(ctx, w.cfg, policy.ResourceRef{Kind: policy.ResourceWorkspace, Name: "scratch"}, policy.ActionWorkspaceWrite); err != nil {
		return nil, err
	}
	scratch, err := w.base.CreateScratch(ctx, prefix)
	if err != nil || scratch == nil {
		return scratch, err
	}
	return authorizedScratchDir{base: scratch, cfg: w.cfg}, nil
}

type authorizedScratchDir struct {
	base ScratchDir
	cfg  AuthorizationConfig
}

func (s authorizedScratchDir) Root() string { return s.base.Root() }

func (s authorizedScratchDir) WriteFile(ctx context.Context, raw string, data []byte, mode os.FileMode) (ResolvedPath, error) {
	if err := authorizeSystem(ctx, s.cfg, policy.ResourceRef{Kind: policy.ResourcePath, Path: workspaceResourcePath("@scratch/" + strings.TrimLeft(raw, "/"))}, policy.ActionWorkspaceWrite); err != nil {
		return ResolvedPath{}, err
	}
	return s.base.WriteFile(ctx, raw, data, mode)
}

func (s authorizedScratchDir) RemoveAll(ctx context.Context) error {
	if err := authorizeSystem(ctx, s.cfg, policy.ResourceRef{Kind: policy.ResourceWorkspace, Name: "scratch"}, policy.ActionWorkspaceWrite); err != nil {
		return err
	}
	return s.base.RemoveAll(ctx)
}

type authorizedNetwork struct {
	base Network
	cfg  AuthorizationConfig
}

func (n authorizedNetwork) DoHTTP(ctx context.Context, req HTTPRequest) (HTTPResponse, error) {
	action := policy.ActionNetworkConnect
	method := strings.ToUpper(strings.TrimSpace(req.Method))
	if method == "" {
		method = http.MethodGet
	}
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		action = policy.ActionNetworkFetch
	}
	if err := authorizeSystem(ctx, n.cfg, policy.ResourceRef{Kind: policy.ResourceNetwork, Name: strings.TrimSpace(req.URL)}, action); err != nil {
		return HTTPResponse{}, err
	}
	return n.base.DoHTTP(ctx, req)
}

type authorizedProcessManager struct {
	base ProcessManager
	cfg  AuthorizationConfig
}

func (p authorizedProcessManager) Run(ctx context.Context, req ProcessRequest) (ProcessResult, error) {
	if err := p.exec(ctx, req.Command); err != nil {
		return ProcessResult{}, err
	}
	return p.base.Run(ctx, req)
}

func (p authorizedProcessManager) Start(ctx context.Context, req ProcessRequest) (ProcessHandle, error) {
	if err := p.exec(ctx, req.Command); err != nil {
		return nil, err
	}
	return p.base.Start(ctx, req)
}

func (p authorizedProcessManager) List(ctx context.Context) ([]ProcessInfo, error) {
	if err := authorizeSystem(ctx, p.cfg, policy.ResourceRef{Kind: policy.ResourceProcess, Name: "*"}, policy.ActionProcessExec); err != nil {
		return nil, err
	}
	return p.base.List(ctx)
}

func (p authorizedProcessManager) Status(ctx context.Context, id string) (ProcessInfo, error) {
	if err := authorizeSystem(ctx, p.cfg, policy.ResourceRef{Kind: policy.ResourceProcess, ID: strings.TrimSpace(id)}, policy.ActionProcessExec); err != nil {
		return ProcessInfo{}, err
	}
	return p.base.Status(ctx, id)
}

func (p authorizedProcessManager) Output(ctx context.Context, id string) (ProcessOutput, error) {
	if err := authorizeSystem(ctx, p.cfg, policy.ResourceRef{Kind: policy.ResourceProcess, ID: strings.TrimSpace(id)}, policy.ActionProcessExec); err != nil {
		return ProcessOutput{}, err
	}
	return p.base.Output(ctx, id)
}

func (p authorizedProcessManager) Kill(ctx context.Context, id string) error {
	if err := authorizeSystem(ctx, p.cfg, policy.ResourceRef{Kind: policy.ResourceProcess, ID: strings.TrimSpace(id)}, policy.ActionProcessAdmin); err != nil {
		return err
	}
	return p.base.Kill(ctx, id)
}

func (p authorizedProcessManager) exec(ctx context.Context, command string) error {
	return authorizeSystem(ctx, p.cfg, policy.ResourceRef{Kind: policy.ResourceProcess, Name: strings.TrimSpace(command)}, policy.ActionProcessExec)
}

type authorizedBrowserManager struct {
	base BrowserManager
	cfg  AuthorizationConfig
}

func (b authorizedBrowserManager) Open(ctx context.Context, req BrowserOpenRequest) (BrowserOpenResult, error) {
	if err := b.fetch(ctx, req.URL); err != nil {
		return BrowserOpenResult{}, err
	}
	return b.base.Open(ctx, req)
}

func (b authorizedBrowserManager) Navigate(ctx context.Context, req BrowserSessionRequest) (BrowserPageResult, error) {
	if err := b.fetch(ctx, req.URL); err != nil {
		return BrowserPageResult{}, err
	}
	return b.base.Navigate(ctx, req)
}

func (b authorizedBrowserManager) Click(ctx context.Context, req BrowserSelectorRequest) (BrowserPageResult, error) {
	if err := b.fetch(ctx, "*"); err != nil {
		return BrowserPageResult{}, err
	}
	return b.base.Click(ctx, req)
}

func (b authorizedBrowserManager) Type(ctx context.Context, req BrowserTypeRequest) (BrowserPageResult, error) {
	if err := b.fetch(ctx, "*"); err != nil {
		return BrowserPageResult{}, err
	}
	return b.base.Type(ctx, req)
}

func (b authorizedBrowserManager) Select(ctx context.Context, req BrowserSelectRequest) (BrowserPageResult, error) {
	if err := b.fetch(ctx, "*"); err != nil {
		return BrowserPageResult{}, err
	}
	return b.base.Select(ctx, req)
}

func (b authorizedBrowserManager) Read(ctx context.Context, req BrowserReadRequest) (BrowserReadResult, error) {
	if err := b.fetch(ctx, "*"); err != nil {
		return BrowserReadResult{}, err
	}
	return b.base.Read(ctx, req)
}

func (b authorizedBrowserManager) Screenshot(ctx context.Context, req BrowserSessionRequest) (BrowserArtifact, error) {
	if err := b.fetch(ctx, "*"); err != nil {
		return BrowserArtifact{}, err
	}
	return b.base.Screenshot(ctx, req)
}

func (b authorizedBrowserManager) Evaluate(ctx context.Context, req BrowserEvaluateRequest) (BrowserEvaluateResult, error) {
	if err := b.fetch(ctx, "*"); err != nil {
		return BrowserEvaluateResult{}, err
	}
	return b.base.Evaluate(ctx, req)
}

func (b authorizedBrowserManager) Wait(ctx context.Context, req BrowserWaitRequest) (BrowserPageResult, error) {
	if err := b.fetch(ctx, "*"); err != nil {
		return BrowserPageResult{}, err
	}
	return b.base.Wait(ctx, req)
}

func (b authorizedBrowserManager) Scroll(ctx context.Context, req BrowserScrollRequest) (BrowserPageResult, error) {
	if err := b.fetch(ctx, "*"); err != nil {
		return BrowserPageResult{}, err
	}
	return b.base.Scroll(ctx, req)
}

func (b authorizedBrowserManager) Hover(ctx context.Context, req BrowserSelectorRequest) (BrowserPageResult, error) {
	if err := b.fetch(ctx, "*"); err != nil {
		return BrowserPageResult{}, err
	}
	return b.base.Hover(ctx, req)
}

func (b authorizedBrowserManager) Back(ctx context.Context, req BrowserSessionRequest) (BrowserPageResult, error) {
	if err := b.fetch(ctx, "*"); err != nil {
		return BrowserPageResult{}, err
	}
	return b.base.Back(ctx, req)
}

func (b authorizedBrowserManager) Forward(ctx context.Context, req BrowserSessionRequest) (BrowserPageResult, error) {
	if err := b.fetch(ctx, "*"); err != nil {
		return BrowserPageResult{}, err
	}
	return b.base.Forward(ctx, req)
}

func (b authorizedBrowserManager) PDF(ctx context.Context, req BrowserSessionRequest) (BrowserArtifact, error) {
	if err := b.fetch(ctx, "*"); err != nil {
		return BrowserArtifact{}, err
	}
	return b.base.PDF(ctx, req)
}

func (b authorizedBrowserManager) Close(ctx context.Context, req BrowserSessionRequest) error {
	return b.base.Close(ctx, req)
}

func (b authorizedBrowserManager) fetch(ctx context.Context, target string) error {
	target = strings.TrimSpace(target)
	if target == "" {
		target = "*"
	}
	return authorizeSystem(ctx, b.cfg, policy.ResourceRef{Kind: policy.ResourceNetwork, Name: target}, policy.ActionNetworkFetch)
}

type authorizedEnvironment struct {
	base Environment
	cfg  AuthorizationConfig
}

func (e authorizedEnvironment) Lookup(ctx context.Context, key string) (string, bool, error) {
	key = strings.TrimSpace(key)
	if key == "" {
		return "", false, fmt.Errorf("environment key is empty")
	}
	if err := authorizeSystem(ctx, e.cfg, policy.ResourceRef{Kind: policy.ResourceSecret, Name: "env/" + key}, policy.ActionSecretRead); err != nil {
		return "", false, err
	}
	return e.base.Lookup(ctx, key)
}

func authorizeSystem(ctx context.Context, cfg AuthorizationConfig, resource policy.ResourceRef, action policy.Action) error {
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
	event.EmitAuthorizationDecision(ctx, auth, req, evaluation)
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
