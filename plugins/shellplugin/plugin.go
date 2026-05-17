package shellplugin

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/fluxplane/agentruntime/core/operation"
	"github.com/fluxplane/agentruntime/core/policy"
	"github.com/fluxplane/agentruntime/core/resource"
	"github.com/fluxplane/agentruntime/core/usage"
	"github.com/fluxplane/agentruntime/orchestration/pluginhost"
	operationruntime "github.com/fluxplane/agentruntime/runtime/operation"
	"github.com/fluxplane/agentruntime/runtime/system"
)

const (
	Name            = "shell"
	ExecOp          = "shell_exec"
	ProcessStartOp  = "process_start"
	ProcessListOp   = "process_list"
	ProcessStatusOp = "process_status"
	ProcessOutputOp = "process_output"
	ProcessKillOp   = "process_kill"
)

// Plugin contributes direct process execution operations.
type Plugin struct {
	system system.System
}

var _ pluginhost.Plugin = Plugin{}
var _ pluginhost.OperationContributor = Plugin{}

// New returns a shell plugin.
func New(sys system.System) Plugin { return Plugin{system: sys} }

// Manifest returns plugin metadata.
func (Plugin) Manifest() pluginhost.Manifest {
	return pluginhost.Manifest{Name: Name, Description: "Direct process execution through the runtime system boundary."}
}

// Contributions returns shell specs.
func (Plugin) Contributions(context.Context, pluginhost.Context) (resource.ContributionBundle, error) {
	specs := specs()
	return resource.ContributionBundle{
		OperationSets: []operation.Set{{Name: Name, Description: "Process execution operations.", Operations: refs(specs)}},
		Operations:    specs,
	}, nil
}

// Operations returns executable shell operations.
func (p Plugin) Operations(context.Context, pluginhost.Context) ([]operation.Operation, error) {
	if p.system == nil {
		return nil, fmt.Errorf("shellplugin: system is nil")
	}
	return []operation.Operation{
		operationruntime.NewTypedResult[execInput, map[string]any](specByName(ExecOp), p.exec(), operationruntime.WithIntent(execIntent), operationruntime.WithAccess(execAccess)),
		operationruntime.NewTypedResult[execInput, map[string]any](specByName(ProcessStartOp), p.start(), operationruntime.WithIntent(execIntent), operationruntime.WithAccess(execAccess)),
		operationruntime.NewTypedResult[processListInput, map[string]any](specByName(ProcessListOp), p.list(), operationruntime.WithIntent(processListIntent)),
		operationruntime.NewTypedResult[processIDInput, map[string]any](specByName(ProcessStatusOp), p.status(), operationruntime.WithIntent(processReadIntent)),
		operationruntime.NewTypedResult[processIDInput, map[string]any](specByName(ProcessOutputOp), p.output(), operationruntime.WithIntent(processReadIntent)),
		operationruntime.NewTypedResult[processIDInput, map[string]any](specByName(ProcessKillOp), p.kill(), operationruntime.WithIntent(processKillIntent)),
	}, nil
}

func specs() []operation.Spec {
	return []operation.Spec{
		execLikeSpec[execInput](ExecOp, "Run one direct executable without invoking a shell interpreter and wait for completion."),
		execLikeSpec[execInput](ProcessStartOp, "Start one direct executable in the background and return a process id."),
		operationruntime.WithTypedContract[processListInput, map[string]any](processSpec(ProcessListOp, "List managed background processes.", operation.RiskLow)),
		operationruntime.WithTypedContract[processIDInput, map[string]any](processSpec(ProcessStatusOp, "Return status for one managed background process.", operation.RiskLow)),
		operationruntime.WithTypedContract[processIDInput, map[string]any](processSpec(ProcessOutputOp, "Return bounded stdout/stderr output for one managed background process.", operation.RiskLow)),
		operationruntime.WithTypedContract[processIDInput, map[string]any](processSpec(ProcessKillOp, "Kill one managed background process.", operation.RiskMedium)),
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

type execInput struct {
	Command   string   `json:"command" jsonschema:"description=Executable name or simple command line without shell syntax.,required"`
	Args      []string `json:"args,omitempty" jsonschema:"description=Arguments passed directly to the executable."`
	Workdir   string   `json:"workdir,omitempty" jsonschema:"description=Workspace-relative working directory."`
	TimeoutMS int      `json:"timeout_ms,omitempty" jsonschema:"description=Timeout in milliseconds."`
}

func (p Plugin) exec() operationruntime.TypedResultHandler[execInput, map[string]any] {
	return func(ctx operation.Context, req execInput) operation.Result {
		request, invalid, ok := p.processRequest(req, 30*time.Second)
		if !ok {
			return invalid
		}
		handle, err := p.system.Process().Start(ctx, request)
		if err != nil {
			return operation.Failed("shell_exec_failed", err.Error(), nil)
		}
		done := make(chan struct{})
		go func() {
			defer close(done)
			for event := range handle.Events() {
				ctx.Events().Emit(event)
			}
		}()
		result, waitErr := handle.Wait(ctx)
		<-done
		emitProcessUsage(ctx, result)
		data := map[string]any{
			"command":          result.Command,
			"args":             result.Args,
			"workdir":          result.Workdir,
			"stdout":           result.Stdout,
			"stderr":           result.Stderr,
			"exit_code":        result.ExitCode,
			"timed_out":        result.TimedOut,
			"stdout_truncated": result.StdoutTruncated,
			"stderr_truncated": result.StderrTruncated,
		}
		text := renderResult(result)
		if waitErr != nil {
			return operation.Failed("shell_exec_failed", waitErr.Error(), data)
		}
		return operation.OK(operation.Rendered{Text: text, Data: data})
	}
}

func (p Plugin) start() operationruntime.TypedResultHandler[execInput, map[string]any] {
	return func(ctx operation.Context, req execInput) operation.Result {
		request, result, ok := p.processRequest(req, 0)
		if !ok {
			return result
		}
		handle, err := p.system.Process().Start(ctx, request)
		if err != nil {
			return operation.Failed("process_start_failed", err.Error(), nil)
		}
		info := handle.Info()
		data := processInfoData(info)
		text := fmt.Sprintf("Started %s as %s", info.Command, info.ID)
		return operation.OK(operation.Rendered{Text: text, Data: data})
	}
}

type processListInput struct{}

func (p Plugin) list() operationruntime.TypedResultHandler[processListInput, map[string]any] {
	return func(ctx operation.Context, _ processListInput) operation.Result {
		processes, err := p.system.Process().List(ctx)
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
			lines = append(lines, fmt.Sprintf("%s %s %s", info.ID, state, info.Command))
		}
		return operation.OK(operation.Rendered{Text: strings.Join(lines, "\n"), Data: map[string]any{"processes": items}})
	}
}

type processIDInput struct {
	ProcessID string `json:"process_id" jsonschema:"description=Managed process id.,required"`
}

func execIntent(_ operation.Context, req execInput) (operation.IntentSet, error) {
	command, args, err := commandAndArgs(req)
	if err != nil {
		return operation.IntentSet{}, err
	}
	if deniedCommand(command) {
		return operation.IntentSet{}, fmt.Errorf("command is blocked by default policy")
	}
	return operation.IntentSet{Operations: []operation.IntentOperation{
		processIntent(command, args, req.Workdir),
	}}, nil
}

func execAccess(_ operation.Context, req execInput) ([]operationruntime.AccessDescriptor, error) {
	command, _, err := commandAndArgs(req)
	if err != nil {
		return nil, err
	}
	if deniedCommand(command) {
		return nil, fmt.Errorf("command is blocked by default policy")
	}
	return []operationruntime.AccessDescriptor{operationruntime.ProcessDescriptor(command, policy.ActionProcessExec)}, nil
}

func processListIntent(operation.Context, processListInput) (operation.IntentSet, error) {
	return operation.IntentSet{Operations: []operation.IntentOperation{
		processIntent("process_manager", []string{"list"}, ""),
	}}, nil
}

func processReadIntent(_ operation.Context, req processIDInput) (operation.IntentSet, error) {
	if strings.TrimSpace(req.ProcessID) == "" {
		return operation.IntentSet{}, fmt.Errorf("process_id is required")
	}
	return operation.IntentSet{Operations: []operation.IntentOperation{
		processIntent("process_manager", []string{"read", req.ProcessID}, ""),
	}}, nil
}

func processKillIntent(_ operation.Context, req processIDInput) (operation.IntentSet, error) {
	if strings.TrimSpace(req.ProcessID) == "" {
		return operation.IntentSet{}, fmt.Errorf("process_id is required")
	}
	return operation.IntentSet{Operations: []operation.IntentOperation{
		processIntent("process_manager", []string{"kill", req.ProcessID}, ""),
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

func (p Plugin) status() operationruntime.TypedResultHandler[processIDInput, map[string]any] {
	return func(ctx operation.Context, req processIDInput) operation.Result {
		if strings.TrimSpace(req.ProcessID) == "" {
			return operation.Failed("invalid_process_input", "process_id is required", nil)
		}
		info, err := p.system.Process().Status(ctx, req.ProcessID)
		if err != nil {
			return operation.Failed("process_status_failed", err.Error(), nil)
		}
		data := processInfoData(info)
		return operation.OK(operation.Rendered{Text: fmt.Sprintf("%s running=%v exit=%d", info.ID, info.Running, info.ExitCode), Data: data})
	}
}

func (p Plugin) output() operationruntime.TypedResultHandler[processIDInput, map[string]any] {
	return func(ctx operation.Context, req processIDInput) operation.Result {
		if strings.TrimSpace(req.ProcessID) == "" {
			return operation.Failed("invalid_process_input", "process_id is required", nil)
		}
		output, err := p.system.Process().Output(ctx, req.ProcessID)
		if err != nil {
			return operation.Failed("process_output_failed", err.Error(), nil)
		}
		data := map[string]any{"process_id": output.ProcessID, "stdout": output.Stdout, "stderr": output.Stderr, "stdout_truncated": output.StdoutTruncated, "stderr_truncated": output.StderrTruncated}
		text := renderOutput(output.Stdout, output.Stderr)
		return operation.OK(operation.Rendered{Text: text, Data: data})
	}
}

func (p Plugin) kill() operationruntime.TypedResultHandler[processIDInput, map[string]any] {
	return func(ctx operation.Context, req processIDInput) operation.Result {
		if strings.TrimSpace(req.ProcessID) == "" {
			return operation.Failed("invalid_process_input", "process_id is required", nil)
		}
		if err := p.system.Process().Kill(ctx, req.ProcessID); err != nil {
			return operation.Failed("process_kill_failed", err.Error(), nil)
		}
		return operation.OK(operation.Rendered{Text: "Killed " + req.ProcessID, Data: map[string]any{"process_id": req.ProcessID}})
	}
}

func (p Plugin) processRequest(req execInput, fallbackTimeout time.Duration) (system.ProcessRequest, operation.Result, bool) {
	if strings.TrimSpace(req.Command) == "" {
		return system.ProcessRequest{}, operation.Failed("invalid_shell_exec_input", "command is required", nil), false
	}
	command, args, err := commandAndArgs(req)
	if err != nil {
		return system.ProcessRequest{}, operation.Rejected("shell_syntax_denied", err.Error(), map[string]any{"command": req.Command}), false
	}
	if deniedCommand(command) {
		return system.ProcessRequest{}, operation.Rejected("shell_command_denied", "command is blocked by default policy", map[string]any{"command": command}), false
	}
	timeout := time.Duration(req.TimeoutMS) * time.Millisecond
	if timeout <= 0 {
		timeout = fallbackTimeout
	}
	if timeout > 10*time.Minute {
		timeout = 10 * time.Minute
	}
	return system.ProcessRequest{
		Command: command, Args: args, Workdir: req.Workdir, Env: system.DefaultProcessEnv(), Timeout: timeout,
		MaxStdout: 64 * 1024, MaxStderr: 64 * 1024,
	}, operation.Result{}, true
}

func commandAndArgs(req execInput) (string, []string, error) {
	command := strings.TrimSpace(req.Command)
	args := append([]string(nil), req.Args...)
	if len(args) == 0 {
		if containsShellSyntax(command) {
			return "", nil, fmt.Errorf("shell syntax is not supported; pass command plus args")
		}
		parts := strings.Fields(command)
		if len(parts) == 0 {
			return "", nil, fmt.Errorf("command is empty")
		}
		command = parts[0]
		args = parts[1:]
	}
	if containsShellSyntax(command) {
		return "", nil, fmt.Errorf("command must be an executable path, not shell syntax")
	}
	return command, args, nil
}

func containsShellSyntax(command string) bool {
	return strings.ContainsAny(command, "\n\r;&|<>$`\\")
}

func deniedCommand(command string) bool {
	switch strings.ToLower(strings.TrimSpace(filepath.Base(command))) {
	case "rm", "sudo", "su", "chmod", "chown", "mkfs", "dd", "shutdown", "reboot", "kill", "pkill":
		return true
	default:
		return false
	}
}

func renderResult(result system.ProcessResult) string {
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

func processInfoData(info system.ProcessInfo) map[string]any {
	return map[string]any{
		"id": info.ID, "command": info.Command, "args": info.Args, "workdir": info.Workdir,
		"started_at": info.StartedAt, "ended_at": info.EndedAt, "running": info.Running,
		"exit_code": info.ExitCode, "error": info.Error,
	}
}

func emitProcessUsage(ctx operation.Context, result system.ProcessResult) {
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
