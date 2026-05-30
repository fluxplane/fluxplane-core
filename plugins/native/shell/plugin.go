package shell

import (
	"context"
	"fmt"
	fpsystem "github.com/fluxplane/fluxplane-system"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fluxplane/fluxplane-core/core/operation"
	"github.com/fluxplane/fluxplane-core/core/resource"
	"github.com/fluxplane/fluxplane-core/core/usage"
	"github.com/fluxplane/fluxplane-core/orchestration/pluginhost"
	operationruntime "github.com/fluxplane/fluxplane-core/runtime/operation"
	system "github.com/fluxplane/fluxplane-core/runtime/workspace"
	"github.com/fluxplane/fluxplane-policy"
)

const (
	Name            = "shell"
	ExecOp          = "shell_exec"
	ShellOp         = "shell"
	ShellInfoOp     = "shell_info"
	ProcessRunOp    = "process_run"
	ProcessStartOp  = "process_start"
	ProcessEnsureOp = "process_ensure"
	ProcessListOp   = "process_list"
	ProcessStatusOp = "process_status"
	ProcessOutputOp = "process_output"
	ProcessWaitOp   = "process_wait"
	ProcessStopOp   = "process_stop"
	ProcessKillOp   = "process_kill"
)

var supportedShells = []string{"sh", "bash", "zsh", "fish", "pwsh", "powershell", "cmd"}

// Plugin contributes shell and process execution operations.
type Plugin struct {
	process     fpsystem.ProcessManager
	environment fpsystem.Environment
	handles     *sync.Map
	captures    *sync.Map
}

var _ pluginhost.Plugin = Plugin{}
var _ pluginhost.OperationContributor = Plugin{}

// Config configures shell and process execution boundaries.
type Config struct {
	Process     fpsystem.ProcessManager
	Environment fpsystem.Environment
}

// New returns a shell plugin.
func New(cfg Config) Plugin {
	return Plugin{process: cfg.Process, environment: cfg.Environment, handles: &sync.Map{}, captures: &sync.Map{}}
}

// Manifest returns plugin metadata.
func (Plugin) Manifest() pluginhost.Manifest {
	return pluginhost.Manifest{Name: Name, Description: "Shell and process execution through the runtime system boundary."}
}

// Contributions returns shell specs.
func (Plugin) Contributions(context.Context, pluginhost.Context) (resource.ContributionBundle, error) {
	specs := specs()
	return resource.ContributionBundle{
		OperationSets: []operation.Set{{Name: Name, Description: "Shell and process execution operations.", Operations: refs(specs)}},
		Operations:    specs,
	}, nil
}

// Operations returns executable shell operations.
func (p Plugin) Operations(context.Context, pluginhost.Context) ([]operation.Operation, error) {
	if p.process == nil {
		return nil, fmt.Errorf("shellplugin: process manager is nil")
	}
	return []operation.Operation{
		operationruntime.NewTypedResult[shellInput, map[string]any](specByName(ShellOp), p.shell(), operationruntime.WithIntent(shellIntent), operationruntime.WithAccess(shellAccess)),
		operationruntime.NewTypedResult[shellInfoInput, map[string]any](specByName(ShellInfoOp), p.shellInfo(), operationruntime.WithIntent(shellInfoIntent)),
		operationruntime.NewTypedResult[execInput, map[string]any](specByName(ExecOp), p.shellExec(), operationruntime.WithIntent(execIntent), operationruntime.WithAccess(execAccess)),
		operationruntime.NewTypedResult[execInput, map[string]any](specByName(ProcessRunOp), p.run(), operationruntime.WithIntent(execIntent), operationruntime.WithAccess(execAccess)),
		operationruntime.NewTypedResult[execInput, map[string]any](specByName(ProcessStartOp), p.start(), operationruntime.WithIntent(execIntent), operationruntime.WithAccess(execAccess)),
		operationruntime.NewTypedResult[execInput, map[string]any](specByName(ProcessEnsureOp), p.ensure(), operationruntime.WithIntent(execIntent), operationruntime.WithAccess(execAccess)),
		operationruntime.NewTypedResult[processListInput, map[string]any](specByName(ProcessListOp), p.list(), operationruntime.WithIntent(processListIntent)),
		operationruntime.NewTypedResult[processIDInput, map[string]any](specByName(ProcessStatusOp), p.status(), operationruntime.WithIntent(processReadIntent)),
		operationruntime.NewTypedResult[processIDInput, map[string]any](specByName(ProcessOutputOp), p.output(), operationruntime.WithIntent(processReadIntent)),
		operationruntime.NewTypedResult[processIDInput, map[string]any](specByName(ProcessWaitOp), p.wait(), operationruntime.WithIntent(processReadIntent)),
		operationruntime.NewTypedResult[processIDInput, map[string]any](specByName(ProcessStopOp), p.stop(), operationruntime.WithIntent(processKillIntent)),
		operationruntime.NewTypedResult[processIDInput, map[string]any](specByName(ProcessKillOp), p.kill(), operationruntime.WithIntent(processKillIntent)),
	}, nil
}

func specs() []operation.Spec {
	return []operation.Spec{
		operationruntime.WithTypedContract[shellInput, map[string]any](processSpec(ShellOp, "Run shell commands using an available shell. op can be exec or start.", operation.RiskMedium)),
		operationruntime.WithTypedContract[shellInfoInput, map[string]any](processSpec(ShellInfoOp, "Return available shells and basic version information.", operation.RiskLow)),
		execLikeSpec[execInput](ExecOp, "Run shell script text through a shell and wait for completion."),
		execLikeSpec[execInput](ProcessRunOp, "Run one direct executable without a shell interpreter and wait for completion."),
		execLikeSpec[execInput](ProcessStartOp, "Start one direct executable in the background and return a process id."),
		execLikeSpec[execInput](ProcessEnsureOp, "Return an existing running labeled process or start one."),
		operationruntime.WithTypedContract[processListInput, map[string]any](processSpec(ProcessListOp, "List managed background processes.", operation.RiskLow)),
		operationruntime.WithTypedContract[processIDInput, map[string]any](processSpec(ProcessStatusOp, "Return status for one managed background process by id or label.", operation.RiskLow)),
		operationruntime.WithTypedContract[processIDInput, map[string]any](processSpec(ProcessOutputOp, "Return bounded stdout/stderr output for one managed background process by id or label.", operation.RiskLow)),
		operationruntime.WithTypedContract[processIDInput, map[string]any](processSpec(ProcessWaitOp, "Wait for one managed background process by id or label.", operation.RiskLow)),
		operationruntime.WithTypedContract[processIDInput, map[string]any](processSpec(ProcessStopOp, "Gracefully stop one managed background process by id or label.", operation.RiskMedium)),
		operationruntime.WithTypedContract[processIDInput, map[string]any](processSpec(ProcessKillOp, "Kill one managed background process by id or label.", operation.RiskMedium)),
	}
}

func execLikeSpec[I any](name, description string) operation.Spec {
	return operationruntime.WithTypedContract[I, map[string]any](processSpec(name, description, operation.RiskMedium))
}

func processSpec(name, description string, risk operation.RiskLevel) operation.Spec {
	return operation.Spec{
		Ref:         operation.Ref{Name: operation.Name(name)},
		Description: description,
		Semantics: operation.Semantics{
			Determinism: operation.DeterminismNonDeterministic,
			Effects:     operation.EffectSet{operation.EffectProcess, operation.EffectFilesystem, operation.EffectReadExternal},
			Idempotency: operation.IdempotencyUnknown,
			Risk:        risk,
		},
		Annotations: map[string]string{"sandbox.overlay": "bypasses-unless-process-sandboxed"},
	}
}

func specByName(name string) operation.Spec {
	for _, spec := range specs() {
		if string(spec.Ref.Name) == name {
			return spec
		}
	}
	return operation.Spec{Ref: operation.Ref{Name: operation.Name(name)}}
}

func refs(specs []operation.Spec) []operation.Ref {
	out := make([]operation.Ref, 0, len(specs))
	for _, spec := range specs {
		out = append(out, spec.Ref)
	}
	return out
}

type shellInput struct {
	Op        string            `json:"op,omitempty" jsonschema:"description=Shell operation: exec or start."`
	Shell     string            `json:"shell,omitempty" jsonschema:"enum=sh,enum=bash,enum=zsh,enum=fish,enum=pwsh,enum=powershell,enum=cmd,description=Shell executable to use. Defaults to bash when available, then sh."`
	Commands  []string          `json:"commands" jsonschema:"description=Shell command lines to run as one script.,required"`
	Workdir   string            `json:"workdir,omitempty" jsonschema:"description=Workspace-relative working directory."`
	TimeoutMS int               `json:"timeout_ms,omitempty" jsonschema:"description=Timeout in milliseconds."`
	Label     string            `json:"label,omitempty" jsonschema:"description=Optional managed process label for background shell commands."`
	Tags      []string          `json:"tags,omitempty" jsonschema:"description=Optional managed process tags."`
	Metadata  map[string]string `json:"metadata,omitempty" jsonschema:"description=Optional managed process metadata."`
}

type shellInfoInput struct{}

type execInput struct {
	Command   string            `json:"command" jsonschema:"description=Executable name or simple shell script for shell_exec.,required"`
	Args      []string          `json:"args,omitempty" jsonschema:"description=Arguments passed directly to the executable for process operations."`
	Workdir   string            `json:"workdir,omitempty" jsonschema:"description=Workspace-relative working directory."`
	TimeoutMS int               `json:"timeout_ms,omitempty" jsonschema:"description=Timeout in milliseconds."`
	Label     string            `json:"label,omitempty" jsonschema:"description=Optional managed process label."`
	Tags      []string          `json:"tags,omitempty" jsonschema:"description=Optional managed process tags."`
	Metadata  map[string]string `json:"metadata,omitempty" jsonschema:"description=Optional managed process metadata."`
}

func (p Plugin) shell() operationruntime.TypedResultHandler[shellInput, map[string]any] {
	return func(ctx operation.Context, req shellInput) operation.Result {
		request, invalid, ok := p.shellProcessRequest(ctx, req)
		if !ok {
			return invalid
		}
		switch strings.ToLower(strings.TrimSpace(firstNonEmpty(req.Op, "exec"))) {
		case "", "exec", "run":
			return p.runProcess(ctx, request)
		case "start", "background":
			return p.startProcess(ctx, request, false)
		default:
			return operation.Failed("invalid_shell_input", "op must be exec or start", nil)
		}
	}
}

func (p Plugin) shellInfo() operationruntime.TypedResultHandler[shellInfoInput, map[string]any] {
	return func(ctx operation.Context, _ shellInfoInput) operation.Result {
		infos := p.availableShells(ctx)
		lines := make([]string, 0, len(infos)+1)
		lines = append(lines, fmt.Sprintf("Shells: %d", len(infos)))
		for _, info := range infos {
			line := info["name"].(string)
			if path, _ := info["path"].(string); path != "" {
				line += " " + path
			}
			if version, _ := info["version"].(string); version != "" {
				line += " " + strings.Split(version, "\n")[0]
			}
			lines = append(lines, line)
		}
		return operation.OK(operation.Rendered{Text: strings.Join(lines, "\n"), Data: map[string]any{"shells": infos}})
	}
}

func (p Plugin) shellExec() operationruntime.TypedResultHandler[execInput, map[string]any] {
	return func(ctx operation.Context, req execInput) operation.Result {
		shellReq := shellInput{Shell: "bash", Commands: []string{req.Command}, Workdir: req.Workdir, TimeoutMS: req.TimeoutMS, Label: req.Label, Tags: req.Tags, Metadata: req.Metadata}
		request, invalid, ok := p.shellProcessRequest(ctx, shellReq)
		if !ok {
			return invalid
		}
		return p.runProcess(ctx, request)
	}
}

func (p Plugin) run() operationruntime.TypedResultHandler[execInput, map[string]any] {
	return func(ctx operation.Context, req execInput) operation.Result {
		request, invalid, ok := p.processRequest(req, 30*time.Second)
		if !ok {
			return invalid
		}
		return p.runProcess(ctx, request)
	}
}

func (p Plugin) runProcess(ctx operation.Context, request fpsystem.ProcessRequest) operation.Result {
	if strings.TrimSpace(request.Group) == "" {
		request.Group = fmt.Sprintf("shell-run-%d", time.Now().UnixNano())
	}
	eventCtx, cancelEvents := context.WithCancel(ctx)
	events := p.process.Group(request.Group).Subscribe(eventCtx)
	defer cancelEvents()
	handle, err := p.process.Start(ctx, request)
	if err != nil {
		return operation.Failed("process_run_failed", err.Error(), nil)
	}
	capture := &processCapture{}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for event := range events {
			if event.ProcessID != handle.ID() {
				continue
			}
			capture.append(event)
			ctx.Events().Emit(event)
		}
	}()
	result, waitErr := handle.Wait(ctx)
	cancelEvents()
	<-done
	capture.apply(&result)
	emitProcessUsage(ctx, result)
	data := processResultData(result)
	text := renderResult(result)
	if waitErr != nil {
		return operation.Failed("process_run_failed", waitErr.Error(), data)
	}
	return operation.OK(operation.Rendered{Text: text, Data: data})
}

func (p Plugin) start() operationruntime.TypedResultHandler[execInput, map[string]any] {
	return func(ctx operation.Context, req execInput) operation.Result {
		request, result, ok := p.processRequest(req, 0)
		if !ok {
			return result
		}
		return p.startProcess(ctx, request, false)
	}
}

func (p Plugin) ensure() operationruntime.TypedResultHandler[execInput, map[string]any] {
	return func(ctx operation.Context, req execInput) operation.Result {
		request, result, ok := p.processRequest(req, 0)
		if !ok {
			return result
		}
		return p.startProcess(ctx, request, true)
	}
}

func (p Plugin) startProcess(ctx operation.Context, request fpsystem.ProcessRequest, ensure bool) operation.Result {
	request.Timeout = 0
	request.Detached = true
	var (
		handle  fpsystem.ProcessHandle
		started bool
		err     error
	)
	if ensure {
		handle, started, err = p.process.Ensure(ctx, request)
	} else {
		handle, err = p.process.Start(ctx, request)
		started = true
	}
	if err != nil {
		return operation.Failed("process_start_failed", err.Error(), nil)
	}
	p.storeHandle(handle)
	p.captureHandle(context.Background(), handle, func(event fpsystem.ProcessEvent) { ctx.Events().Emit(event) })
	info := handle.Info()
	data := processInfoData(info)
	data["started"] = started
	verb := "Started"
	if !started {
		verb = "Using existing"
	}
	text := fmt.Sprintf("%s %s as %s", verb, info.Command, info.ID)
	if info.Label != "" {
		text += " (" + info.Label + ")"
	}
	return operation.OK(operation.Rendered{Text: text, Data: data})
}

type processListInput struct{}

func (p Plugin) list() operationruntime.TypedResultHandler[processListInput, map[string]any] {
	return func(ctx operation.Context, _ processListInput) operation.Result {
		processes, err := p.process.List(ctx)
		if err != nil {
			return operation.Failed("process_list_failed", err.Error(), nil)
		}
		items := make([]map[string]any, 0, len(processes))
		lines := []string{fmt.Sprintf("Processes: %d", len(processes))}
		for _, info := range processes {
			items = append(items, processInfoData(info))
			state := "exited"
			if info.Running {
				state = "running"
			}
			name := info.ID
			if info.Label != "" {
				name += " " + info.Label
			}
			lines = append(lines, fmt.Sprintf("%s %s %s", name, state, info.Command))
		}
		return operation.OK(operation.Rendered{Text: strings.Join(lines, "\n"), Data: map[string]any{"processes": items}})
	}
}

type processIDInput struct {
	ProcessID string `json:"process_id,omitempty" jsonschema:"description=Managed process id."`
	Label     string `json:"label,omitempty" jsonschema:"description=Managed process label."`
	TimeoutMS int    `json:"timeout_ms,omitempty" jsonschema:"description=Timeout in milliseconds for wait operations."`
}

func (p Plugin) status() operationruntime.TypedResultHandler[processIDInput, map[string]any] {
	return func(ctx operation.Context, req processIDInput) operation.Result {
		id, invalid := processSelector(req)
		if invalid != nil {
			return *invalid
		}
		handle, err := p.lookupHandle(id)
		if err != nil {
			return operation.Failed("process_status_failed", err.Error(), nil)
		}
		info := handle.Info()
		data := processInfoData(info)
		return operation.OK(operation.Rendered{Text: fmt.Sprintf("%s running=%v exit=%d", info.ID, info.Running, info.ExitCode), Data: data})
	}
}

func (p Plugin) output() operationruntime.TypedResultHandler[processIDInput, map[string]any] {
	return func(ctx operation.Context, req processIDInput) operation.Result {
		id, invalid := processSelector(req)
		if invalid != nil {
			return *invalid
		}
		if _, err := p.lookupHandle(id); err != nil {
			return operation.Failed("process_output_failed", err.Error(), nil)
		}
		capture := p.lookupCapture(id)
		stdout, stderr := "", ""
		if capture != nil {
			stdout, stderr = capture.output()
		}
		data := map[string]any{"process_id": id, "stdout": stdout, "stderr": stderr, "stdout_truncated": false, "stderr_truncated": false}
		text := renderOutput(stdout, stderr)
		return operation.OK(operation.Rendered{Text: text, Data: data})
	}
}

func (p Plugin) wait() operationruntime.TypedResultHandler[processIDInput, map[string]any] {
	return func(ctx operation.Context, req processIDInput) operation.Result {
		id, invalid := processSelector(req)
		if invalid != nil {
			return *invalid
		}
		handle, err := p.lookupHandle(id)
		if err != nil {
			return operation.Failed("process_wait_failed", err.Error(), nil)
		}
		waitCtx := context.Context(ctx)
		if req.TimeoutMS > 0 {
			var cancel context.CancelFunc
			waitCtx, cancel = context.WithTimeout(ctx, time.Duration(req.TimeoutMS)*time.Millisecond)
			defer cancel()
		}
		result, err := handle.Wait(waitCtx)
		if capture := p.lookupCapture(id); capture != nil {
			capture.apply(&result)
		}
		data := processResultData(result)
		if err != nil {
			return operation.Failed("process_wait_failed", err.Error(), data)
		}
		return operation.OK(operation.Rendered{Text: renderResult(result), Data: data})
	}
}

func (p Plugin) stop() operationruntime.TypedResultHandler[processIDInput, map[string]any] {
	return func(ctx operation.Context, req processIDInput) operation.Result {
		id, invalid := processSelector(req)
		if invalid != nil {
			return *invalid
		}
		handle, lookupErr := p.lookupHandle(id)
		if lookupErr != nil {
			return operation.Failed("process_stop_failed", lookupErr.Error(), nil)
		}
		if err := handle.Stop(ctx); err != nil {
			return operation.Failed("process_stop_failed", err.Error(), nil)
		}
		return operation.OK(operation.Rendered{Text: "Stopped " + id, Data: map[string]any{"process_id": id}})
	}
}

func (p Plugin) kill() operationruntime.TypedResultHandler[processIDInput, map[string]any] {
	return func(ctx operation.Context, req processIDInput) operation.Result {
		id, invalid := processSelector(req)
		if invalid != nil {
			return *invalid
		}
		handle, lookupErr := p.lookupHandle(id)
		if lookupErr != nil {
			return operation.Failed("process_kill_failed", lookupErr.Error(), nil)
		}
		if err := handle.Kill(ctx); err != nil {
			return operation.Failed("process_kill_failed", err.Error(), nil)
		}
		return operation.OK(operation.Rendered{Text: "Killed " + id, Data: map[string]any{"process_id": id}})
	}
}

type processCapture struct {
	mu     sync.Mutex
	stdout strings.Builder
	stderr strings.Builder
}

func (c *processCapture) append(event fpsystem.ProcessEvent) {
	if c == nil || event.Kind != fpsystem.ProcessEventOutput {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	switch event.Stream {
	case "stdout":
		c.stdout.WriteString(event.Data)
	case "stderr":
		c.stderr.WriteString(event.Data)
	}
}

func (c *processCapture) output() (string, string) {
	if c == nil {
		return "", ""
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.stdout.String(), c.stderr.String()
}

func (c *processCapture) apply(result *fpsystem.ProcessResult) {
	if c == nil || result == nil {
		return
	}
	result.Stdout, result.Stderr = c.output()
}

func (p Plugin) captureHandle(ctx context.Context, handle fpsystem.ProcessHandle, emit func(fpsystem.ProcessEvent)) *processCapture {
	capture := &processCapture{}
	if p.captures != nil && handle != nil {
		p.captures.Store(handle.ID(), capture)
		if label := strings.TrimSpace(handle.Info().Label); label != "" {
			p.captures.Store(label, capture)
		}
	}
	eventCtx, cancel := context.WithCancel(ctx)
	events := handle.Subscribe(eventCtx)
	go func() {
		for event := range events {
			capture.append(event)
			if emit != nil {
				emit(event)
			}
		}
	}()
	go func() {
		_, _ = handle.Wait(context.Background())
		cancel()
	}()
	return capture
}

func (p Plugin) lookupCapture(id string) *processCapture {
	if p.captures == nil {
		return nil
	}
	if value, ok := p.captures.Load(strings.TrimSpace(id)); ok {
		if capture, ok := value.(*processCapture); ok {
			return capture
		}
	}
	return nil
}

func (p Plugin) storeHandle(handle fpsystem.ProcessHandle) {
	if handle == nil || p.handles == nil {
		return
	}
	p.handles.Store(handle.ID(), handle)
	if label := strings.TrimSpace(handle.Info().Label); label != "" {
		p.handles.Store(label, handle)
	}
}

func (p Plugin) lookupHandle(id string) (fpsystem.ProcessHandle, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, fmt.Errorf("process id is empty")
	}
	if p.handles != nil {
		if value, ok := p.handles.Load(id); ok {
			if handle, ok := value.(fpsystem.ProcessHandle); ok && handle != nil {
				return handle, nil
			}
		}
	}
	return nil, fmt.Errorf("process %q not found in plugin handle registry", id)
}

func processSelector(req processIDInput) (string, *operation.Result) {
	id := strings.TrimSpace(req.ProcessID)
	if id == "" {
		id = strings.TrimSpace(req.Label)
	}
	if id == "" {
		result := operation.Failed("invalid_process_input", "process_id or label is required", nil)
		return "", &result
	}
	return id, nil
}

func (p Plugin) shellProcessRequest(ctx context.Context, req shellInput) (fpsystem.ProcessRequest, operation.Result, bool) {
	if len(req.Commands) == 0 {
		return fpsystem.ProcessRequest{}, operation.Failed("invalid_shell_input", "commands is required", nil), false
	}
	script := strings.Join(req.Commands, "\n")
	shell, err := p.resolveShell(ctx, req.Shell)
	if err != nil {
		return fpsystem.ProcessRequest{}, operation.Failed("shell_unavailable", err.Error(), nil), false
	}
	timeout := boundedTimeout(req.TimeoutMS, 30*time.Second)
	return fpsystem.ProcessRequest{
		Command: shell.Name, Args: shellScriptArgs(shell.Name, script), Workdir: req.Workdir, Env: system.DefaultProcessEnv(), Timeout: timeout,
		MaxStdout: 64 * 1024, MaxStderr: 64 * 1024, Label: req.Label, Tags: req.Tags, Metadata: req.Metadata,
	}, operation.Result{}, true
}

func (p Plugin) processRequest(req execInput, fallbackTimeout time.Duration) (fpsystem.ProcessRequest, operation.Result, bool) {
	if strings.TrimSpace(req.Command) == "" {
		return fpsystem.ProcessRequest{}, operation.Failed("invalid_process_input", "command is required", nil), false
	}
	command, args, err := commandAndArgs(req)
	if err != nil {
		return fpsystem.ProcessRequest{}, operation.Rejected("process_command_invalid", err.Error(), map[string]any{"command": req.Command}), false
	}
	timeout := boundedTimeout(req.TimeoutMS, fallbackTimeout)
	return fpsystem.ProcessRequest{
		Command: command, Args: args, Workdir: req.Workdir, Env: system.DefaultProcessEnv(), Timeout: timeout,
		MaxStdout: 64 * 1024, MaxStderr: 64 * 1024, Label: req.Label, Tags: req.Tags, Metadata: req.Metadata,
	}, operation.Result{}, true
}

func boundedTimeout(timeoutMS int, fallback time.Duration) time.Duration {
	timeout := time.Duration(timeoutMS) * time.Millisecond
	if timeout <= 0 {
		timeout = fallback
	}
	if timeout > 10*time.Minute {
		timeout = 10 * time.Minute
	}
	return timeout
}

func commandAndArgs(req execInput) (string, []string, error) {
	command := strings.TrimSpace(req.Command)
	args := append([]string(nil), req.Args...)
	if len(args) == 0 {
		parts := strings.Fields(command)
		if len(parts) == 0 {
			return "", nil, fmt.Errorf("command is empty")
		}
		command = parts[0]
		args = parts[1:]
	}
	return command, args, nil
}

type shellInfo struct {
	Name      string
	Path      string
	Available bool
	Version   string
}

func (p Plugin) resolveShell(ctx context.Context, requested string) (shellInfo, error) {
	choices := supportedShells
	if strings.TrimSpace(requested) != "" {
		choices = []string{strings.TrimSpace(requested)}
	} else if p.environment != nil {
		if shell, ok, _ := p.environment.Lookup(ctx, "SHELL"); ok && strings.TrimSpace(shell) != "" {
			choices = append([]string{filepath.Base(shell)}, choices...)
		}
	}
	for _, name := range choices {
		for _, info := range p.availableShells(ctx) {
			if info["name"] == name && info["available"] == true {
				path, _ := info["path"].(string)
				if path != "" {
					return shellInfo{Name: path, Path: path, Available: true}, nil
				}
				return shellInfo{Name: name, Available: true}, nil
			}
		}
	}
	return shellInfo{}, fmt.Errorf("shell %q is not available", firstNonEmpty(requested, strings.Join(supportedShells, ", ")))
}

func (p Plugin) availableShells(ctx context.Context) []map[string]any {
	out := make([]map[string]any, 0, len(supportedShells))
	seen := map[string]bool{}
	var resolver fpsystem.ExecutableResolver
	if p.environment != nil {
		resolver, _ = p.environment.(fpsystem.ExecutableResolver)
	}
	for _, name := range supportedShells {
		if seen[name] {
			continue
		}
		seen[name] = true
		var path string
		var ok bool
		if resolver != nil {
			path, ok, _ = resolver.ResolveExecutable(ctx, name)
		}
		info := map[string]any{"name": name, "available": ok}
		if ok {
			info["path"] = path
			info["version"] = p.shellVersion(ctx, path, name)
		}
		out = append(out, info)
	}
	return out
}

func (p Plugin) shellVersion(ctx context.Context, path, name string) string {
	args := []string{"--version"}
	if name == "cmd" {
		args = []string{"/C", "ver"}
	}
	result, err := p.process.Run(ctx, fpsystem.ProcessRequest{Command: path, Args: args, Timeout: 2 * time.Second, MaxStdout: 4096, MaxStderr: 4096})
	if err != nil && result.Stdout == "" && result.Stderr == "" {
		return ""
	}
	return strings.TrimSpace(firstNonEmpty(result.Stdout, result.Stderr))
}

func shellScriptArgs(shell, script string) []string {
	name := strings.ToLower(filepath.Base(shell))
	switch name {
	case "cmd":
		return []string{"/C", script}
	case "powershell", "pwsh":
		return []string{"-NoProfile", "-Command", script}
	default:
		return []string{"-c", script}
	}
}

func shellIntent(_ operation.Context, req shellInput) (operation.IntentSet, error) {
	shell := strings.TrimSpace(req.Shell)
	if shell == "" {
		shell = "shell"
	}
	return operation.IntentSet{Operations: []operation.IntentOperation{
		processIntent(shell, []string{"-c", strings.Join(req.Commands, "\n")}, req.Workdir),
	}}, nil
}

func shellInfoIntent(operation.Context, shellInfoInput) (operation.IntentSet, error) {
	return operation.IntentSet{Operations: []operation.IntentOperation{
		processIntent("shell_info", []string{"list"}, ""),
	}}, nil
}

func execIntent(_ operation.Context, req execInput) (operation.IntentSet, error) {
	command, args, err := commandAndArgs(req)
	if err != nil {
		return operation.IntentSet{}, err
	}
	return operation.IntentSet{Operations: []operation.IntentOperation{
		processIntent(command, args, req.Workdir),
	}}, nil
}

func shellAccess(_ operation.Context, req shellInput) ([]operationruntime.AccessDescriptor, error) {
	shell := strings.TrimSpace(req.Shell)
	if shell == "" {
		shell = "shell"
	}
	return []operationruntime.AccessDescriptor{operationruntime.ProcessDescriptor(shell, policy.ActionProcessExec)}, nil
}

func execAccess(_ operation.Context, req execInput) ([]operationruntime.AccessDescriptor, error) {
	command, _, err := commandAndArgs(req)
	if err != nil {
		return nil, err
	}
	return []operationruntime.AccessDescriptor{operationruntime.ProcessDescriptor(command, policy.ActionProcessExec)}, nil
}

func processListIntent(operation.Context, processListInput) (operation.IntentSet, error) {
	return operation.IntentSet{Operations: []operation.IntentOperation{
		processIntent("process_manager", []string{"list"}, ""),
	}}, nil
}

func processReadIntent(_ operation.Context, req processIDInput) (operation.IntentSet, error) {
	id, invalid := processSelector(req)
	if invalid != nil {
		return operation.IntentSet{}, fmt.Errorf("process_id or label is required")
	}
	return operation.IntentSet{Operations: []operation.IntentOperation{
		processIntent("process_manager", []string{"read", id}, ""),
	}}, nil
}

func processKillIntent(_ operation.Context, req processIDInput) (operation.IntentSet, error) {
	id, invalid := processSelector(req)
	if invalid != nil {
		return operation.IntentSet{}, fmt.Errorf("process_id or label is required")
	}
	return operation.IntentSet{Operations: []operation.IntentOperation{
		processIntent("process_manager", []string{"kill", id}, ""),
	}}, nil
}

func processIntent(command string, args []string, workdir string) operation.IntentOperation {
	arguments := make([]operation.Argument, 0, len(args))
	for _, arg := range args {
		arguments = append(arguments, operation.Argument(arg))
	}
	return operation.IntentOperation{
		Behavior:  operation.IntentCommandExecution,
		Target:    operation.ProcessTarget{Command: operation.Command(command), Args: arguments, Workdir: operation.Workdir(workdir)},
		Role:      operation.IntentRoleProcessCommand,
		Certainty: operation.IntentCertain,
	}
}

func processResultData(result fpsystem.ProcessResult) map[string]any {
	return map[string]any{
		"command": result.Command, "args": result.Args, "workdir": result.Workdir,
		"stdout": result.Stdout, "stderr": result.Stderr, "exit_code": result.ExitCode,
		"timed_out": result.TimedOut, "stdout_truncated": result.StdoutTruncated, "stderr_truncated": result.StderrTruncated,
	}
}

func renderResult(result fpsystem.ProcessResult) string {
	var b strings.Builder
	fmt.Fprintf(&b, "[exit: %d] [duration: %.1fs]", result.ExitCode, result.Duration.Seconds())
	if result.TimedOut {
		b.WriteString(" [timed out]")
	}
	if result.Workdir != "" {
		fmt.Fprintf(&b, " [dir: %s]", result.Workdir)
	}
	if result.Stdout != "" {
		b.WriteString("\n=== STDOUT ===\n")
		b.WriteString(result.Stdout)
	}
	if result.Stderr != "" {
		b.WriteString("\n=== STDERR ===\n")
		b.WriteString(result.Stderr)
	}
	return b.String()
}

func renderOutput(stdout, stderr string) string {
	var b strings.Builder
	if stdout != "" {
		b.WriteString("=== STDOUT ===\n")
		b.WriteString(stdout)
	}
	if stderr != "" {
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		b.WriteString("=== STDERR ===\n")
		b.WriteString(stderr)
	}
	return strings.TrimSpace(b.String())
}

func processInfoData(info fpsystem.ProcessInfo) map[string]any {
	return map[string]any{
		"id": info.ID, "label": info.Label, "tags": info.Tags, "metadata": info.Metadata,
		"command": info.Command, "args": info.Args, "workdir": info.Workdir,
		"started_at": info.StartedAt, "ended_at": info.EndedAt, "running": info.Running,
		"exit_code": info.ExitCode, "error": info.Error,
	}
}

func emitProcessUsage(ctx operation.Context, result fpsystem.ProcessResult) {
	ctx.Events().Emit(usage.Recorded{
		Source: ExecOp,
		Subject: usage.Subject{
			Kind: usage.SubjectProcess,
			Name: result.Command,
		},
		Measurements: []usage.Measurement{
			{Metric: usage.MetricWallTime, Quantity: float64(result.Duration.Milliseconds()), Unit: usage.UnitMillisecond},
			{Metric: usage.MetricFileBytes, Quantity: float64(len(result.Stdout)), Unit: usage.UnitByte, Direction: usage.DirectionOutput, Dimensions: map[string]string{"stream": "stdout"}},
			{Metric: usage.MetricFileBytes, Quantity: float64(len(result.Stderr)), Unit: usage.UnitByte, Direction: usage.DirectionOutput, Dimensions: map[string]string{"stream": "stderr"}},
		},
	})
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
