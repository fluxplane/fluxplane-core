package sessionagent

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/fluxplane/engine/core/agent"
	"github.com/fluxplane/engine/core/channel"
	"github.com/fluxplane/engine/core/command"
	corecontext "github.com/fluxplane/engine/core/context"
	"github.com/fluxplane/engine/core/event"
	"github.com/fluxplane/engine/core/operation"
	coresession "github.com/fluxplane/engine/core/session"
	corethread "github.com/fluxplane/engine/core/thread"
	operationruntime "github.com/fluxplane/engine/runtime/operation"
)

const defaultMaxParallel = 4

// Config wires a Runner.
type Config struct {
	Client         Client
	MaxParallel    int
	ResolveProfile ProfileResolver
}

// Client is the small child-session port a command session-agent needs.
type Client interface {
	Open(context.Context, OpenRequest) (Session, error)
}

// ProfileResolver resolves a session profile before it is opened.
type ProfileResolver interface {
	ResolveProfile(context.Context, coresession.Ref) (coresession.Spec, error)
}

// ProfileResolverFunc adapts a function into ProfileResolver.
type ProfileResolverFunc func(context.Context, coresession.Ref) (coresession.Spec, error)

func (f ProfileResolverFunc) ResolveProfile(ctx context.Context, ref coresession.Ref) (coresession.Spec, error) {
	if f == nil {
		return coresession.Spec{}, fmt.Errorf("sessionagent: profile resolver is nil")
	}
	return f(ctx, ref)
}

type OpenRequest struct {
	Session      coresession.Ref
	Profile      coresession.Spec
	Conversation channel.ConversationRef
	Metadata     map[string]string
	Approver     operationruntime.ApprovalGate
}

type Session interface {
	Info() SessionInfo
	SendInput(context.Context, Input) (Run, error)
}

type SessionInfo struct {
	Thread corethread.Ref
}

type Input struct {
	Text     string
	Metadata map[string]any
}

type Run interface {
	ID() string
	Events() <-chan RunEvent
	Wait(context.Context) (RunResult, error)
}

type RunEvent struct {
	Kind      string
	Operation string
	Runtime   string
}

type RunResult struct {
	Text string
}

// Request describes one command helper session invocation.
type Request struct {
	ID             ID
	Profile        coresession.Ref
	Agent          agent.Ref
	Task           string
	TaskID         string
	Timeout        time.Duration
	Policy         coresession.DelegationPolicy
	ParentThreadID corethread.ID
	ParentRunID    string
	ParentCallID   operation.CallID
	Metadata       map[string]string
	Events         event.Sink
	Approver       operationruntime.ApprovalGate
}

// Result is the terminal output from a command helper session.
type Result struct {
	ID            ID                `json:"id"`
	Profile       coresession.Ref   `json:"profile,omitempty"`
	Agent         agent.Ref         `json:"agent,omitempty"`
	ChildThreadID corethread.ID     `json:"child_thread_id,omitempty"`
	ChildRunID    string            `json:"child_run_id,omitempty"`
	Output        string            `json:"output,omitempty"`
	Metadata      map[string]string `json:"metadata,omitempty"`
}

// Runner runs command helper sessions with delegation-policy narrowing.
type Runner struct {
	client         Client
	limit          int
	resolveProfile ProfileResolver

	mu      sync.Mutex
	running int
}

// New returns a command session-agent runner.
func New(cfg Config) *Runner {
	limit := cfg.MaxParallel
	if limit <= 0 {
		limit = defaultMaxParallel
	}
	return &Runner{client: cfg.Client, limit: limit, resolveProfile: cfg.ResolveProfile}
}

// Run opens the requested profile, submits the task text, and waits for the
// helper session's final response.
func (r *Runner) Run(ctx context.Context, req Request) (Result, error) {
	if r == nil || r.client == nil {
		return Result{}, fmt.Errorf("sessionagent: runner is not configured")
	}
	if strings.TrimSpace(req.Task) == "" {
		return Result{}, fmt.Errorf("sessionagent: task is required")
	}
	if req.ID == "" {
		req.ID = ID(newID("session_agent_"))
	}
	profile, profileSpec, profileResolved, err := resolveProfile(ctx, r, req)
	if err != nil {
		return Result{}, err
	}
	if err := authorizeProfile(req.Policy, profile, req.Agent); err != nil {
		return Result{}, err
	}
	agentRef, err := authorizeAgent(req.Policy, profile, profileSpec, profileResolved)
	if err != nil {
		return Result{}, err
	}
	effectiveProfile := narrowProfile(profileSpec, profile, req.Policy, profileResolved)
	if err := r.acquire(req.Policy); err != nil {
		return Result{}, err
	}
	defer r.release()

	runCtx := ensureContext(ctx)
	if req.Timeout > 0 {
		var cancel context.CancelFunc
		runCtx, cancel = context.WithTimeout(runCtx, req.Timeout)
		defer cancel()
	}
	cause := Causation{
		ID:             req.ID,
		ParentThreadID: req.ParentThreadID,
		ParentRunID:    req.ParentRunID,
		ParentCallID:   req.ParentCallID,
		Profile:        profile,
		Agent:          agentRef,
		TaskID:         req.TaskID,
		Metadata:       cloneStringMap(req.Metadata),
	}
	emit(req.Events, Requested{Causation: cause, Task: req.Task})
	session, err := r.client.Open(runCtx, OpenRequest{
		Session:      profile,
		Profile:      effectiveProfile,
		Conversation: channel.ConversationRef{ID: string(req.ID)},
		Approver:     req.Approver,
		Metadata: map[string]string{
			"parent_thread_id": string(req.ParentThreadID),
			"parent_run_id":    req.ParentRunID,
			"parent_call_id":   string(req.ParentCallID),
			"task_id":          req.TaskID,
		},
	})
	if err != nil {
		emit(req.Events, Failed{Causation: cause, Error: err.Error()})
		return Result{}, fmt.Errorf("sessionagent: open session: %w", err)
	}
	info := session.Info()
	cause.ChildThreadID = info.Thread.ID
	emit(req.Events, Started{Causation: cause, Task: req.Task})
	run, err := session.SendInput(runCtx, Input{Text: req.Task, Metadata: map[string]any{
		"session_agent_id": string(req.ID),
		"task_id":          req.TaskID,
	}})
	if err != nil {
		emit(req.Events, Failed{Causation: cause, Error: err.Error()})
		return Result{}, err
	}
	cause.ChildRunID = run.ID()
	for {
		select {
		case event, ok := <-run.Events():
			if !ok {
				result, waitErr := run.Wait(runCtx)
				if waitErr != nil {
					if runCtx.Err() != nil {
						emit(req.Events, Cancelled{Causation: cause, Reason: runCtx.Err().Error()})
						return Result{}, runCtx.Err()
					}
					emit(req.Events, Failed{Causation: cause, Error: waitErr.Error()})
					return Result{}, waitErr
				}
				if runCtx.Err() != nil {
					emit(req.Events, Cancelled{Causation: cause, Reason: runCtx.Err().Error()})
					return Result{}, runCtx.Err()
				}
				emit(req.Events, Completed{Causation: cause, Output: result.Text})
				return Result{
					ID:            req.ID,
					Profile:       profile,
					Agent:         agentRef,
					ChildThreadID: cause.ChildThreadID,
					ChildRunID:    cause.ChildRunID,
					Output:        result.Text,
					Metadata:      cloneStringMap(req.Metadata),
				}, nil
			}
			if msg := progressMessage(event); msg != "" {
				emit(req.Events, Progressed{Causation: cause, Message: msg, Percent: -1})
			}
		case <-runCtx.Done():
			emit(req.Events, Cancelled{Causation: cause, Reason: runCtx.Err().Error()})
			return Result{}, runCtx.Err()
		}
	}
}

func (r *Runner) acquire(policy coresession.DelegationPolicy) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	limit := r.limit
	if policy.MaxParallel > 0 && policy.MaxParallel < limit {
		limit = policy.MaxParallel
	}
	if r.running >= limit {
		return fmt.Errorf("sessionagent: at capacity (%d/%d)", r.running, limit)
	}
	r.running++
	return nil
}

func (r *Runner) release() {
	r.mu.Lock()
	if r.running > 0 {
		r.running--
	}
	r.mu.Unlock()
}

func progressMessage(ev RunEvent) string {
	switch ev.Kind {
	case "operation.requested":
		if ev.Operation != "" {
			return "calling " + ev.Operation
		}
	case "operation.completed":
		if ev.Operation != "" {
			return "completed " + ev.Operation
		}
	case "runtime.emitted":
		if ev.Runtime != "" {
			return ev.Runtime
		}
	}
	return ""
}

func resolveProfile(ctx context.Context, r *Runner, req Request) (coresession.Ref, coresession.Spec, bool, error) {
	if req.Agent.Name != "" {
		profile := req.Profile
		if profile.Name == "" {
			profile = coresession.Ref{Name: coresession.Name(req.Agent.Name)}
		}
		return profile, coresession.Spec{Name: profile.Name, Agent: req.Agent}, true, nil
	}
	if req.Profile.Name != "" {
		spec, resolved, err := r.resolveProfileSpec(ctx, req.Policy, req.Profile)
		return req.Profile, spec, resolved, err
	}
	if len(req.Policy.AllowedProfiles) == 1 {
		profile := req.Policy.AllowedProfiles[0]
		spec, resolved, err := r.resolveProfileSpec(ctx, req.Policy, profile)
		return profile, spec, resolved, err
	}
	return coresession.Ref{}, coresession.Spec{}, false, fmt.Errorf("sessionagent: profile is required; allowed profiles: %s", allowedProfileList(req.Policy.AllowedProfiles))
}

func authorizeProfile(policy coresession.DelegationPolicy, profile coresession.Ref, agentRef agent.Ref) error {
	if agentRef.Name != "" {
		if len(policy.AllowedAgents) == 0 {
			return nil
		}
		for _, allowed := range policy.AllowedAgents {
			if allowed.Name == agentRef.Name {
				return nil
			}
		}
		return fmt.Errorf("sessionagent: agent %q is not allowed", agentRef.Name)
	}
	if profile.Name == "" {
		return fmt.Errorf("sessionagent: profile is required; allowed profiles: %s", allowedProfileList(policy.AllowedProfiles))
	}
	if len(policy.AllowedProfiles) == 0 {
		return fmt.Errorf("sessionagent: delegation policy has no allowed profiles")
	}
	for _, allowed := range policy.AllowedProfiles {
		if allowed.Name == profile.Name {
			return nil
		}
	}
	return fmt.Errorf("sessionagent: profile %q is not allowed; allowed profiles: %s", profile.Name, allowedProfileList(policy.AllowedProfiles))
}

func (r *Runner) resolveProfileSpec(ctx context.Context, policy coresession.DelegationPolicy, profile coresession.Ref) (coresession.Spec, bool, error) {
	if r.resolveProfile == nil {
		if len(policy.AllowedAgents) > 0 || len(policy.Context) > 0 || len(policy.Commands) > 0 || len(policy.Operations) > 0 {
			return coresession.Spec{}, false, fmt.Errorf("sessionagent: profile resolver is required for delegated profile policy")
		}
		return coresession.Spec{}, false, nil
	}
	spec, err := r.resolveProfile.ResolveProfile(ctx, profile)
	if err != nil {
		return coresession.Spec{}, false, fmt.Errorf("sessionagent: resolve profile %q: %w", profile.Name, err)
	}
	return spec, true, nil
}

func authorizeAgent(policy coresession.DelegationPolicy, profile coresession.Ref, spec coresession.Spec, resolved bool) (agent.Ref, error) {
	if len(policy.AllowedAgents) == 0 && !resolved {
		return agent.Ref{}, nil
	}
	if len(policy.AllowedAgents) == 0 {
		return spec.Agent, nil
	}
	if spec.Agent.Name == "" {
		return agent.Ref{}, fmt.Errorf("sessionagent: profile %q has no agent", profile.Name)
	}
	for _, allowed := range policy.AllowedAgents {
		if allowed.Name == spec.Agent.Name {
			return spec.Agent, nil
		}
	}
	return agent.Ref{}, fmt.Errorf("sessionagent: agent %q for profile %q is not allowed", spec.Agent.Name, profile.Name)
}

func narrowProfile(spec coresession.Spec, profile coresession.Ref, policy coresession.DelegationPolicy, resolved bool) coresession.Spec {
	if !resolved {
		spec = coresession.Spec{Name: profile.Name}
	}
	if spec.Name == "" {
		spec.Name = profile.Name
	}
	spec.Context = narrowContextRefs(spec.Context, policy.Context)
	spec.Commands = narrowCommandPaths(spec.Commands, policy.Commands)
	spec.Operations = narrowOperationRefs(spec.Operations, policy.Operations)
	return spec
}

func narrowContextRefs(base, policy []corecontext.ProviderRef) []corecontext.ProviderRef {
	if len(policy) == 0 {
		return append([]corecontext.ProviderRef(nil), base...)
	}
	if len(base) == 0 {
		return append([]corecontext.ProviderRef(nil), policy...)
	}
	allowed := map[corecontext.ProviderName]struct{}{}
	for _, ref := range policy {
		if ref.Name != "" {
			allowed[ref.Name] = struct{}{}
		}
	}
	out := make([]corecontext.ProviderRef, 0, len(base))
	for _, ref := range base {
		if _, ok := allowed[ref.Name]; ok {
			out = append(out, ref)
		}
	}
	return out
}

func narrowCommandPaths(base, policy []command.Path) []command.Path {
	if len(policy) == 0 {
		return append([]command.Path(nil), base...)
	}
	if len(base) == 0 {
		return append([]command.Path(nil), policy...)
	}
	allowed := map[string]struct{}{}
	for _, path := range policy {
		allowed[path.String()] = struct{}{}
	}
	out := make([]command.Path, 0, len(base))
	for _, path := range base {
		if _, ok := allowed[path.String()]; ok {
			out = append(out, path)
		}
	}
	return out
}

func narrowOperationRefs(base, policy []operation.Ref) []operation.Ref {
	if len(policy) == 0 {
		return append([]operation.Ref(nil), base...)
	}
	if len(base) == 0 {
		return append([]operation.Ref(nil), policy...)
	}
	allowed := map[operation.Name]struct{}{}
	for _, ref := range policy {
		allowed[ref.Name] = struct{}{}
	}
	out := make([]operation.Ref, 0, len(base))
	for _, ref := range base {
		if _, ok := allowed[ref.Name]; ok {
			out = append(out, ref)
		}
	}
	return out
}

func allowedProfileList(profiles []coresession.Ref) string {
	names := make([]string, 0, len(profiles))
	for _, profile := range profiles {
		if profile.Name != "" {
			names = append(names, string(profile.Name))
		}
	}
	if len(names) == 0 {
		return "(none)"
	}
	return strings.Join(names, ", ")
}

func emit(sink event.Sink, payload event.Event) {
	if sink != nil {
		sink.Emit(payload)
	}
}

func cloneStringMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func ensureContext(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	return ctx
}

func newID(prefix string) string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return prefix + fmt.Sprint(time.Now().UnixNano())
	}
	return prefix + hex.EncodeToString(b[:])
}
