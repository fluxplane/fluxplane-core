package subagent

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/fluxplane/agentruntime/core/agent"
	"github.com/fluxplane/agentruntime/core/channel"
	"github.com/fluxplane/agentruntime/core/command"
	corecontext "github.com/fluxplane/agentruntime/core/context"
	"github.com/fluxplane/agentruntime/core/event"
	"github.com/fluxplane/agentruntime/core/operation"
	coresession "github.com/fluxplane/agentruntime/core/session"
	corethread "github.com/fluxplane/agentruntime/core/thread"
)

// ID identifies one supervised child run.
type ID string

// Status is the child lifecycle state tracked by Supervisor.
type Status string

const (
	StatusPrepared  Status = "prepared"
	StatusRunning   Status = "running"
	StatusCompleted Status = "completed"
	StatusFailed    Status = "failed"
	StatusCancelled Status = "cancelled"
)

// Config wires the supervisor to the channel/session client.
type Config struct {
	Client         Client
	MaxParallel    int
	ResolveProfile ProfileResolver
}

// Client is the small child-session port the supervisor needs.
type Client interface {
	Open(context.Context, OpenRequest) (Session, error)
}

// ProfileResolver resolves a child session profile before it is opened.
type ProfileResolver interface {
	ResolveProfile(context.Context, coresession.Ref) (coresession.Spec, error)
}

// ProfileResolverFunc adapts a function into ProfileResolver.
type ProfileResolverFunc func(context.Context, coresession.Ref) (coresession.Spec, error)

func (f ProfileResolverFunc) ResolveProfile(ctx context.Context, ref coresession.Ref) (coresession.Spec, error) {
	if f == nil {
		return coresession.Spec{}, fmt.Errorf("subagent: profile resolver is nil")
	}
	return f(ctx, ref)
}

type OpenRequest struct {
	Session      coresession.Ref
	Profile      coresession.Spec
	Conversation channel.ConversationRef
	Metadata     map[string]string
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

// SpawnRequest describes one child task.
type SpawnRequest struct {
	ID             ID
	Profile        coresession.Ref
	Task           string
	TaskID         string
	Timeout        time.Duration
	Policy         coresession.DelegationPolicy
	ParentThreadID corethread.ID
	ParentRunID    string
	ParentCallID   operation.CallID
	Metadata       map[string]string
	Events         event.Sink
}

// Handle is a snapshot of one child task.
type Handle struct {
	ID            ID                `json:"id"`
	Status        Status            `json:"status"`
	Profile       coresession.Ref   `json:"profile,omitempty"`
	Agent         agent.Ref         `json:"agent,omitempty"`
	Task          string            `json:"task,omitempty"`
	TaskID        string            `json:"task_id,omitempty"`
	ParentThread  corethread.ID     `json:"parent_thread_id,omitempty"`
	ParentRunID   string            `json:"parent_run_id,omitempty"`
	ParentCallID  operation.CallID  `json:"parent_call_id,omitempty"`
	ChildThreadID corethread.ID     `json:"child_thread_id,omitempty"`
	ChildRunID    string            `json:"child_run_id,omitempty"`
	StartedAt     time.Time         `json:"started_at,omitempty"`
	DoneAt        time.Time         `json:"done_at,omitempty"`
	Output        string            `json:"output,omitempty"`
	Error         string            `json:"error,omitempty"`
	Progress      string            `json:"progress,omitempty"`
	Metadata      map[string]string `json:"metadata,omitempty"`
}

// PreparedSpawn reserves capacity and opens the child session. Start begins
// the actual child run after the caller has persisted its own dispatch state.
type PreparedSpawn struct {
	Handle Handle
	Start  func()
}

// Result is the terminal output of a child task.
type Result struct {
	Handle Handle `json:"handle"`
	Output string `json:"output,omitempty"`
	Error  string `json:"error,omitempty"`
}

// Supervisor owns child session lifecycle.
type Supervisor struct {
	client         Client
	limit          int
	resolveProfile ProfileResolver

	mu      sync.Mutex
	handles map[ID]*entry
}

type entry struct {
	handle Handle
	cancel context.CancelFunc
	done   chan struct{}
}

// New returns a child-session supervisor.
func New(cfg Config) *Supervisor {
	limit := cfg.MaxParallel
	if limit <= 0 {
		limit = 4
	}
	return &Supervisor{client: cfg.Client, limit: limit, resolveProfile: cfg.ResolveProfile, handles: map[ID]*entry{}}
}

// Prepare reserves capacity and opens a child session without starting the run.
func (s *Supervisor) Prepare(ctx context.Context, req SpawnRequest) (PreparedSpawn, error) {
	if s == nil || s.client == nil {
		return PreparedSpawn{}, fmt.Errorf("subagent: supervisor is not configured")
	}
	if strings.TrimSpace(req.Task) == "" {
		return PreparedSpawn{}, fmt.Errorf("subagent: task is required")
	}
	profile, err := resolveProfile(req)
	if err != nil {
		return PreparedSpawn{}, err
	}
	if err := authorizeProfile(req.Policy, profile); err != nil {
		return PreparedSpawn{}, err
	}
	profileSpec, profileResolved, err := s.resolveProfileSpec(ctx, req.Policy, profile)
	if err != nil {
		return PreparedSpawn{}, err
	}
	agentRef, err := authorizeAgent(req.Policy, profile, profileSpec, profileResolved)
	if err != nil {
		return PreparedSpawn{}, err
	}
	effectiveProfile := narrowProfile(profileSpec, profile, req.Policy, profileResolved)
	if req.ID == "" {
		req.ID = ID(newID("subagent_"))
	}
	timeout := req.Timeout
	if timeout <= 0 {
		timeout = parsePolicyTimeout(req.Policy.DefaultTimeout)
	}
	if timeout <= 0 {
		timeout = 10 * time.Minute
	}
	runCtx, cancel := context.WithTimeout(ensureContext(ctx), timeout)
	handle := Handle{
		ID:           req.ID,
		Status:       StatusPrepared,
		Profile:      profile,
		Agent:        agentRef,
		Task:         req.Task,
		TaskID:       req.TaskID,
		ParentThread: req.ParentThreadID,
		ParentRunID:  req.ParentRunID,
		ParentCallID: req.ParentCallID,
		Metadata:     cloneStringMap(req.Metadata),
	}
	ent := &entry{handle: handle, cancel: cancel, done: make(chan struct{})}

	s.mu.Lock()
	if running := s.inFlightLocked(); running >= s.effectiveLimit(req.Policy) {
		s.mu.Unlock()
		cancel()
		return PreparedSpawn{}, fmt.Errorf("subagent: at capacity (%d/%d)", running, s.effectiveLimit(req.Policy))
	}
	if _, exists := s.handles[req.ID]; exists {
		s.mu.Unlock()
		cancel()
		return PreparedSpawn{}, fmt.Errorf("subagent: worker %q already exists", req.ID)
	}
	s.handles[req.ID] = ent
	s.mu.Unlock()

	emit(req.Events, SpawnRequested{Causation: causation(ent.handle), Task: req.Task})
	session, err := s.client.Open(runCtx, OpenRequest{
		Session:      profile,
		Profile:      effectiveProfile,
		Conversation: channel.ConversationRef{ID: string(req.ID)},
		Metadata: map[string]string{
			"parent_thread_id": string(req.ParentThreadID),
			"parent_run_id":    req.ParentRunID,
			"parent_call_id":   string(req.ParentCallID),
			"task_id":          req.TaskID,
		},
	})
	if err != nil {
		s.remove(req.ID)
		cancel()
		return PreparedSpawn{}, fmt.Errorf("subagent: open child session: %w", err)
	}
	info := session.Info()
	s.update(req.ID, func(h *Handle) {
		h.ChildThreadID = info.Thread.ID
	})
	prepared := PreparedSpawn{Handle: s.snapshot(req.ID)}
	prepared.Start = func() { go s.run(runCtx, req, session) }
	return prepared, nil
}

// Spawn prepares and starts a child task.
func (s *Supervisor) Spawn(ctx context.Context, req SpawnRequest) (Handle, error) {
	prepared, err := s.Prepare(ctx, req)
	if err != nil {
		return Handle{}, err
	}
	prepared.Start()
	return prepared.Handle, nil
}

// Status returns known child task handles.
func (s *Supervisor) Status() []Handle {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Handle, 0, len(s.handles))
	for _, ent := range s.handles {
		out = append(out, cloneHandle(ent.handle))
	}
	return out
}

// Result returns the terminal output for one child task.
func (s *Supervisor) Result(id ID) (Result, error) {
	h := s.snapshot(id)
	if h.ID == "" {
		return Result{}, fmt.Errorf("subagent: worker %q not found", id)
	}
	if h.Status == StatusPrepared || h.Status == StatusRunning {
		return Result{}, fmt.Errorf("subagent: worker %q is %s", id, h.Status)
	}
	return Result{Handle: h, Output: h.Output, Error: h.Error}, nil
}

// Wait waits for a child task to finish.
func (s *Supervisor) Wait(ctx context.Context, id ID) (Result, error) {
	ent := s.entry(id)
	if ent == nil {
		return Result{}, fmt.Errorf("subagent: worker %q not found", id)
	}
	select {
	case <-ensureContext(ctx).Done():
		return Result{}, ensureContext(ctx).Err()
	case <-ent.done:
		return s.Result(id)
	}
}

// Cancel cancels a running child task.
func (s *Supervisor) Cancel(id ID, reason string) error {
	ent := s.entry(id)
	if ent == nil {
		return fmt.Errorf("subagent: worker %q not found", id)
	}
	if ent.cancel != nil {
		ent.cancel()
	}
	s.update(id, func(h *Handle) {
		if h.Status == StatusPrepared || h.Status == StatusRunning {
			h.Status = StatusCancelled
			h.Error = reason
			h.DoneAt = time.Now().UTC()
		}
	})
	return nil
}

func (s *Supervisor) run(ctx context.Context, req SpawnRequest, session Session) {
	id := req.ID
	s.update(id, func(h *Handle) {
		h.Status = StatusRunning
		h.StartedAt = time.Now().UTC()
	})
	emit(req.Events, Started{Causation: causation(s.snapshot(id)), Task: req.Task})
	run, err := session.SendInput(ctx, Input{Text: req.Task, Metadata: map[string]any{
		"subagent_worker_id": string(id),
		"subagent_task_id":   req.TaskID,
	}})
	if err != nil {
		s.finishFailed(id, req.Events, err.Error())
		return
	}
	s.update(id, func(h *Handle) { h.ChildRunID = string(run.ID()) })
	for {
		select {
		case event, ok := <-run.Events():
			if !ok {
				if ctx.Err() != nil {
					s.finishCancelled(id, req.Events, ctx.Err().Error())
					return
				}
				result, waitErr := run.Wait(ctx)
				if waitErr != nil {
					if ctx.Err() != nil {
						s.finishCancelled(id, req.Events, ctx.Err().Error())
						return
					}
					s.finishFailed(id, req.Events, waitErr.Error())
					return
				}
				if ctx.Err() != nil {
					s.finishCancelled(id, req.Events, ctx.Err().Error())
					return
				}
				s.finishCompleted(id, req.Events, result.Text)
				return
			}
			if msg := progressMessage(event); msg != "" {
				s.update(id, func(h *Handle) { h.Progress = msg })
				emit(req.Events, Progressed{Causation: causation(s.snapshot(id)), Message: msg, Percent: -1})
			}
		case <-ctx.Done():
			s.finishCancelled(id, req.Events, ctx.Err().Error())
			return
		}
	}
}

func (s *Supervisor) finishCompleted(id ID, sink event.Sink, output string) {
	s.update(id, func(h *Handle) {
		h.Status = StatusCompleted
		h.Output = output
		h.Progress = ""
		h.DoneAt = time.Now().UTC()
	})
	emit(sink, Completed{Causation: causation(s.snapshot(id)), Output: output})
	s.closeDone(id)
}

func (s *Supervisor) finishFailed(id ID, sink event.Sink, err string) {
	s.update(id, func(h *Handle) {
		h.Status = StatusFailed
		h.Error = err
		h.Progress = ""
		h.DoneAt = time.Now().UTC()
	})
	emit(sink, Failed{Causation: causation(s.snapshot(id)), Error: err})
	s.closeDone(id)
}

func (s *Supervisor) finishCancelled(id ID, sink event.Sink, reason string) {
	s.update(id, func(h *Handle) {
		h.Status = StatusCancelled
		h.Error = reason
		h.Progress = ""
		h.DoneAt = time.Now().UTC()
	})
	emit(sink, Cancelled{Causation: causation(s.snapshot(id)), Reason: reason})
	s.closeDone(id)
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

func resolveProfile(req SpawnRequest) (coresession.Ref, error) {
	if req.Profile.Name != "" {
		return req.Profile, nil
	}
	if len(req.Policy.AllowedProfiles) == 1 {
		return req.Policy.AllowedProfiles[0], nil
	}
	return coresession.Ref{}, fmt.Errorf("subagent: profile is required; allowed profiles: %s", allowedProfileList(req.Policy.AllowedProfiles))
}

func authorizeProfile(policy coresession.DelegationPolicy, profile coresession.Ref) error {
	if profile.Name == "" {
		return fmt.Errorf("subagent: profile is required; allowed profiles: %s", allowedProfileList(policy.AllowedProfiles))
	}
	if len(policy.AllowedProfiles) == 0 {
		return fmt.Errorf("subagent: delegation policy has no allowed profiles")
	}
	for _, allowed := range policy.AllowedProfiles {
		if allowed.Name == profile.Name {
			return nil
		}
	}
	return fmt.Errorf("subagent: profile %q is not allowed; allowed profiles: %s", profile.Name, allowedProfileList(policy.AllowedProfiles))
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

func (s *Supervisor) resolveProfileSpec(ctx context.Context, policy coresession.DelegationPolicy, profile coresession.Ref) (coresession.Spec, bool, error) {
	if s.resolveProfile == nil {
		if len(policy.AllowedAgents) > 0 || len(policy.Context) > 0 || len(policy.Commands) > 0 || len(policy.Operations) > 0 {
			return coresession.Spec{}, false, fmt.Errorf("subagent: profile resolver is required for delegated profile policy")
		}
		return coresession.Spec{}, false, nil
	}
	spec, err := s.resolveProfile.ResolveProfile(ctx, profile)
	if err != nil {
		return coresession.Spec{}, false, fmt.Errorf("subagent: resolve profile %q: %w", profile.Name, err)
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
		return agent.Ref{}, fmt.Errorf("subagent: profile %q has no agent", profile.Name)
	}
	for _, allowed := range policy.AllowedAgents {
		if allowed.Name == spec.Agent.Name {
			return spec.Agent, nil
		}
	}
	return agent.Ref{}, fmt.Errorf("subagent: agent %q for profile %q is not allowed", spec.Agent.Name, profile.Name)
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
		return cloneCommandPaths(base)
	}
	if len(base) == 0 {
		return cloneCommandPaths(policy)
	}
	allowed := map[string]struct{}{}
	for _, path := range policy {
		if key := path.String(); key != "" {
			allowed[key] = struct{}{}
		}
	}
	out := make([]command.Path, 0, len(base))
	for _, path := range base {
		if _, ok := allowed[path.String()]; ok {
			out = append(out, cloneCommandPath(path))
		}
	}
	return out
}

func cloneCommandPaths(paths []command.Path) []command.Path {
	if paths == nil {
		return nil
	}
	out := make([]command.Path, len(paths))
	for i, path := range paths {
		out[i] = cloneCommandPath(path)
	}
	return out
}

func cloneCommandPath(path command.Path) command.Path {
	if path == nil {
		return nil
	}
	return append(command.Path(nil), path...)
}

func narrowOperationRefs(base, policy []operation.Ref) []operation.Ref {
	if len(policy) == 0 {
		return cloneOperationRefs(base)
	}
	if len(base) == 0 {
		return cloneOperationRefs(policy)
	}
	allowed := map[operation.Name]struct{}{}
	for _, ref := range policy {
		if ref.Name != "" {
			allowed[ref.Name] = struct{}{}
		}
	}
	out := make([]operation.Ref, 0, len(base))
	for _, ref := range base {
		if _, ok := allowed[ref.Name]; ok {
			out = append(out, ref)
		}
	}
	return out
}

func cloneOperationRefs(refs []operation.Ref) []operation.Ref {
	if refs == nil {
		return nil
	}
	return append([]operation.Ref(nil), refs...)
}

func (s *Supervisor) effectiveLimit(policy coresession.DelegationPolicy) int {
	limit := s.limit
	if policy.MaxParallel > 0 && policy.MaxParallel < limit {
		limit = policy.MaxParallel
	}
	if limit <= 0 {
		return 1
	}
	return limit
}

func (s *Supervisor) inFlightLocked() int {
	running := 0
	for _, ent := range s.handles {
		switch ent.handle.Status {
		case StatusPrepared, StatusRunning:
			running++
		}
	}
	return running
}

func (s *Supervisor) entry(id ID) *entry {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.handles[id]
}

func (s *Supervisor) snapshot(id ID) Handle {
	ent := s.entry(id)
	if ent == nil {
		return Handle{}
	}
	return cloneHandle(ent.handle)
}

func (s *Supervisor) update(id ID, fn func(*Handle)) {
	if s == nil || fn == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if ent := s.handles[id]; ent != nil {
		fn(&ent.handle)
	}
}

func (s *Supervisor) remove(id ID) {
	if s == nil {
		return
	}
	s.mu.Lock()
	delete(s.handles, id)
	s.mu.Unlock()
}

func (s *Supervisor) closeDone(id ID) {
	ent := s.entry(id)
	if ent == nil {
		return
	}
	select {
	case <-ent.done:
	default:
		close(ent.done)
	}
}

func causation(h Handle) Causation {
	return Causation{
		ParentThreadID: h.ParentThread,
		ParentRunID:    h.ParentRunID,
		ParentCallID:   h.ParentCallID,
		ChildThreadID:  h.ChildThreadID,
		ChildRunID:     h.ChildRunID,
		Profile:        h.Profile,
		Agent:          h.Agent,
		WorkerID:       h.ID,
		TaskID:         h.TaskID,
		Metadata:       cloneStringMap(h.Metadata),
	}
}

// CausationFromHandle returns the parent/child cause metadata for h.
func CausationFromHandle(h Handle) Causation {
	return causation(h)
}

func emit(sink event.Sink, payload event.Event) {
	if sink == nil || payload == nil {
		return
	}
	sink.Emit(payload)
}

func parsePolicyTimeout(value string) time.Duration {
	if strings.TrimSpace(value) == "" {
		return 0
	}
	d, _ := time.ParseDuration(value)
	return d
}

func cloneHandle(h Handle) Handle {
	h.Metadata = cloneStringMap(h.Metadata)
	return h
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
	var raw [8]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return fmt.Sprintf("%s%d", prefix, time.Now().UnixNano())
	}
	return prefix + hex.EncodeToString(raw[:])
}
