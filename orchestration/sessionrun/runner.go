package sessionrun

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/fluxplane/fluxplane-core/core/agent"
	"github.com/fluxplane/fluxplane-core/core/channel"
	"github.com/fluxplane/fluxplane-core/core/command"
	corecontext "github.com/fluxplane/fluxplane-core/core/context"
	"github.com/fluxplane/fluxplane-core/core/event"
	"github.com/fluxplane/fluxplane-core/core/operation"
	coresession "github.com/fluxplane/fluxplane-core/core/session"
	corethread "github.com/fluxplane/fluxplane-core/core/thread"
	operationruntime "github.com/fluxplane/fluxplane-core/runtime/operation"
)

const defaultMaxParallel = 4

// Client opens helper sessions.
type Client interface {
	Open(context.Context, OpenRequest) (Session, error)
}

// ProfileResolver resolves configured session profiles before opening them.
type ProfileResolver interface {
	ResolveProfile(context.Context, coresession.Ref) (coresession.Spec, error)
}

// ProfileResolverFunc adapts a function into ProfileResolver.
type ProfileResolverFunc func(context.Context, coresession.Ref) (coresession.Spec, error)

func (f ProfileResolverFunc) ResolveProfile(ctx context.Context, ref coresession.Ref) (coresession.Spec, error) {
	if f == nil {
		return coresession.Spec{}, fmt.Errorf("sessionrun: profile resolver is nil")
	}
	return f(ctx, ref)
}

// OpenRequest describes one helper session open.
type OpenRequest struct {
	Session      coresession.Ref
	Profile      coresession.Spec
	Conversation channel.ConversationRef
	Metadata     map[string]string
	Approver     operationruntime.ApprovalGate
}

// Session is the small port needed to submit one input to an opened session.
type Session interface {
	Info() SessionInfo
	SendInput(context.Context, Input) (Run, error)
}

type closeableSession interface {
	Close(context.Context) error
}

// SessionInfo reports helper session identity.
type SessionInfo struct {
	Thread corethread.Ref
}

// Input is the text submitted to a helper session.
type Input struct {
	Text     string
	Metadata map[string]any
}

// Run tracks one submitted helper session input.
type Run interface {
	ID() string
	Events() <-chan RunEvent
	Wait(context.Context) (RunResult, error)
}

// RunEvent is a normalized progress signal from the child run.
type RunEvent struct {
	Kind      string
	Operation string
	Runtime   string
}

// RunResult is the terminal child run output.
type RunResult struct {
	Text string
}

// Config wires a Runner.
type Config struct {
	Client         Client
	MaxParallel    int
	ResolveProfile ProfileResolver
}

// Request describes one helper session run.
type Request struct {
	ID             ID
	Session        coresession.Ref
	Profile        coresession.Spec
	Agent          agent.Ref
	Input          string
	InputMetadata  map[string]any
	Conversation   channel.ConversationRef
	Timeout        time.Duration
	Policy         coresession.DelegationPolicy
	EnforcePolicy  bool
	ParentThreadID corethread.ID
	ParentRunID    string
	ParentCallID   operation.CallID
	TaskID         string
	Metadata       map[string]string
	Events         event.Sink
	Approver       operationruntime.ApprovalGate
}

// Result is the terminal output from one helper session run.
type Result struct {
	ID            ID                `json:"id"`
	Profile       coresession.Ref   `json:"profile,omitempty"`
	Agent         agent.Ref         `json:"agent,omitempty"`
	ChildThreadID corethread.ID     `json:"child_thread_id,omitempty"`
	ChildRunID    string            `json:"child_run_id,omitempty"`
	Output        string            `json:"output,omitempty"`
	Metadata      map[string]string `json:"metadata,omitempty"`
}

// Runner runs helper sessions with optional delegation-policy narrowing.
type Runner struct {
	client         Client
	limit          int
	resolveProfile ProfileResolver

	mu      sync.Mutex
	running int
}

// New returns a session run helper.
func New(cfg Config) *Runner {
	limit := cfg.MaxParallel
	if limit <= 0 {
		limit = defaultMaxParallel
	}
	return &Runner{client: cfg.Client, limit: limit, resolveProfile: cfg.ResolveProfile}
}

// Run opens the requested profile, submits the input text, and waits for the
// helper session's final response.
func (r *Runner) Run(ctx context.Context, req Request) (Result, error) {
	if r == nil || r.client == nil {
		return Result{}, fmt.Errorf("sessionrun: runner is not configured")
	}
	if strings.TrimSpace(req.Input) == "" {
		return Result{}, fmt.Errorf("sessionrun: input is required")
	}
	if req.ID == "" {
		req.ID = ID(newID("session_run_"))
	}
	profile, profileSpec, profileResolved, err := resolveProfile(ctx, r, req)
	if err != nil {
		return Result{}, err
	}
	if req.EnforcePolicy {
		if err := authorizeProfile(req.Policy, profile, req.Agent); err != nil {
			return Result{}, err
		}
	}
	agentRef, err := authorizeAgent(req.Policy, profile, profileSpec, profileResolved, req.EnforcePolicy)
	if err != nil {
		return Result{}, err
	}
	effectiveProfile := profileSpec
	if req.EnforcePolicy {
		effectiveProfile = narrowProfile(profileSpec, profile, req.Policy, profileResolved)
	}
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
	emit(req.Events, Requested{Causation: cause, Input: req.Input})
	session, err := r.client.Open(runCtx, OpenRequest{
		Session:      profile,
		Profile:      effectiveProfile,
		Conversation: conversationForRequest(req),
		Approver:     req.Approver,
		Metadata:     metadataForRequest(req),
	})
	if err != nil {
		emit(req.Events, Failed{Causation: cause, Error: err.Error()})
		return Result{}, fmt.Errorf("sessionrun: open session: %w", err)
	}
	if closeable, ok := session.(closeableSession); ok {
		defer func() { _ = closeable.Close(context.WithoutCancel(runCtx)) }()
	}
	info := session.Info()
	cause.ChildThreadID = info.Thread.ID
	emit(req.Events, Started{Causation: cause, Input: req.Input})
	run, err := session.SendInput(runCtx, Input{Text: req.Input, Metadata: req.InputMetadata})
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

func conversationForRequest(req Request) channel.ConversationRef {
	if req.Conversation.ID != "" {
		return req.Conversation
	}
	return channel.ConversationRef{ID: string(req.ID)}
}

func metadataForRequest(req Request) map[string]string {
	metadata := cloneStringMap(req.Metadata)
	if metadata == nil {
		metadata = map[string]string{}
	}
	if req.ParentThreadID != "" {
		metadata["parent_thread_id"] = string(req.ParentThreadID)
	}
	if req.ParentRunID != "" {
		metadata["parent_run_id"] = req.ParentRunID
	}
	if req.ParentCallID != "" {
		metadata["parent_call_id"] = string(req.ParentCallID)
	}
	if req.TaskID != "" {
		metadata["task_id"] = req.TaskID
	}
	if len(metadata) == 0 {
		return nil
	}
	return metadata
}

func (r *Runner) acquire(policy coresession.DelegationPolicy) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	limit := r.limit
	if policy.MaxParallel > 0 && policy.MaxParallel < limit {
		limit = policy.MaxParallel
	}
	if r.running >= limit {
		return fmt.Errorf("sessionrun: at capacity (%d/%d)", r.running, limit)
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
		profile := req.Session
		if profile.Name == "" && req.Profile.Name != "" {
			profile = coresession.Ref{Name: req.Profile.Name}
		}
		if profile.Name == "" {
			profile = coresession.Ref{Name: coresession.Name(req.Agent.Name)}
		}
		spec := req.Profile
		if spec.Name == "" {
			spec.Name = profile.Name
		}
		spec.Agent = req.Agent
		return profile, spec, true, nil
	}
	if req.Profile.Name != "" {
		profile := req.Session
		if profile.Name == "" {
			profile = coresession.Ref{Name: req.Profile.Name}
		}
		return profile, req.Profile, true, nil
	}
	if req.Session.Name != "" {
		spec, resolved, err := r.resolveProfileSpec(ctx, req.Policy, req.Session, req.EnforcePolicy)
		return req.Session, spec, resolved, err
	}
	if req.EnforcePolicy && len(req.Policy.AllowedProfiles) == 1 {
		profile := req.Policy.AllowedProfiles[0]
		spec, resolved, err := r.resolveProfileSpec(ctx, req.Policy, profile, true)
		return profile, spec, resolved, err
	}
	return coresession.Ref{}, coresession.Spec{}, false, fmt.Errorf("sessionrun: profile is required; allowed profiles: %s", allowedProfileList(req.Policy.AllowedProfiles))
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
		return fmt.Errorf("sessionrun: agent %q is not allowed", agentRef.Name)
	}
	if profile.Name == "" {
		return fmt.Errorf("sessionrun: profile is required; allowed profiles: %s", allowedProfileList(policy.AllowedProfiles))
	}
	if len(policy.AllowedProfiles) == 0 {
		return fmt.Errorf("sessionrun: delegation policy has no allowed profiles")
	}
	for _, allowed := range policy.AllowedProfiles {
		if allowed.Name == profile.Name {
			return nil
		}
	}
	return fmt.Errorf("sessionrun: profile %q is not allowed; allowed profiles: %s", profile.Name, allowedProfileList(policy.AllowedProfiles))
}

func (r *Runner) resolveProfileSpec(ctx context.Context, policy coresession.DelegationPolicy, profile coresession.Ref, required bool) (coresession.Spec, bool, error) {
	if r.resolveProfile == nil {
		if required && (len(policy.AllowedAgents) > 0 || len(policy.Context) > 0 || len(policy.Commands) > 0 || len(policy.Operations) > 0) {
			return coresession.Spec{}, false, fmt.Errorf("sessionrun: profile resolver is required for delegated profile policy")
		}
		return coresession.Spec{}, false, nil
	}
	spec, err := r.resolveProfile.ResolveProfile(ctx, profile)
	if err != nil {
		if !required {
			return coresession.Spec{}, false, nil
		}
		return coresession.Spec{}, false, fmt.Errorf("sessionrun: resolve profile %q: %w", profile.Name, err)
	}
	return spec, true, nil
}

func authorizeAgent(policy coresession.DelegationPolicy, profile coresession.Ref, spec coresession.Spec, resolved bool, enforce bool) (agent.Ref, error) {
	if !enforce {
		return spec.Agent, nil
	}
	if len(policy.AllowedAgents) == 0 && !resolved {
		return agent.Ref{}, nil
	}
	if len(policy.AllowedAgents) == 0 {
		return spec.Agent, nil
	}
	if spec.Agent.Name == "" {
		return agent.Ref{}, fmt.Errorf("sessionrun: profile %q has no agent", profile.Name)
	}
	for _, allowed := range policy.AllowedAgents {
		if allowed.Name == spec.Agent.Name {
			return spec.Agent, nil
		}
	}
	return agent.Ref{}, fmt.Errorf("sessionrun: agent %q for profile %q is not allowed", spec.Agent.Name, profile.Name)
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
