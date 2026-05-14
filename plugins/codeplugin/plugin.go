package codeplugin

import (
	"context"
	"fmt"
	"path"
	"strings"
	"time"

	"github.com/fluxplane/agentruntime/core/operation"
	"github.com/fluxplane/agentruntime/core/resource"
	"github.com/fluxplane/agentruntime/core/usage"
	"github.com/fluxplane/agentruntime/orchestration/pluginhost"
	operationruntime "github.com/fluxplane/agentruntime/runtime/operation"
	"github.com/fluxplane/agentruntime/runtime/system"
)

const (
	Name      = "code"
	ExecuteOp = "code_execute"
)

// Plugin contributes scratch code execution operations.
type Plugin struct {
	system system.System
}

var _ pluginhost.Plugin = Plugin{}
var _ pluginhost.OperationContributor = Plugin{}

// New returns a code execution plugin.
func New(sys system.System) Plugin { return Plugin{system: sys} }

// Manifest returns plugin metadata.
func (Plugin) Manifest() pluginhost.Manifest {
	return pluginhost.Manifest{Name: Name, Description: "Sandbox-oriented scratch code execution."}
}

// Contributions returns code execution specs.
func (Plugin) Contributions(context.Context, pluginhost.Context) (resource.ContributionBundle, error) {
	spec := executeSpec()
	return resource.ContributionBundle{
		OperationSets: []operation.Set{{Name: Name, Description: "Scratch code execution operations.", Operations: []operation.Ref{spec.Ref}}},
		Operations:    []operation.Spec{spec},
	}, nil
}

// Operations returns executable code operations.
func (p Plugin) Operations(context.Context, pluginhost.Context) ([]operation.Operation, error) {
	if p.system == nil {
		return nil, fmt.Errorf("codeplugin: system is nil")
	}
	return []operation.Operation{operationruntime.NewTypedResult[executeInput, map[string]any](executeSpec(), p.execute(), operationruntime.WithIntent(executeIntent))}, nil
}

func executeSpec() operation.Spec {
	return operationruntime.WithTypedContract[executeInput, map[string]any](operation.Spec{
		Ref:         operation.Ref{Name: ExecuteOp},
		Description: "Execute scratch code in a configured container preset. Files are written to an isolated /workspace, not the user repository.",
		Semantics: operation.Semantics{
			Determinism: operation.DeterminismNonDeterministic,
			Effects:     operation.EffectSet{operation.EffectProcess, operation.EffectFilesystem, operation.EffectReadExternal},
			Idempotency: operation.IdempotencyUnknown,
			Risk:        operation.RiskMedium,
		},
		Annotations: map[string]string{"sandbox": "container-scratchpad"},
	})
}

type executeInput struct {
	Preset    string     `json:"preset" jsonschema:"description=Execution preset.,enum=python,enum=go,enum=node,required"`
	Files     []fileSpec `json:"files" jsonschema:"description=Files written into scratch /workspace.,required"`
	Entry     string     `json:"entry,omitempty" jsonschema:"description=Entry file passed to the preset command."`
	Command   []string   `json:"command,omitempty" jsonschema:"description=Explicit command to run inside the preset container."`
	TimeoutMS int        `json:"timeout_ms,omitempty" jsonschema:"description=Timeout in milliseconds."`
}

type fileSpec struct {
	Path    string `json:"path" jsonschema:"description=Workspace-relative scratch file path.,required"`
	Content string `json:"content" jsonschema:"description=File content.,required"`
}

type preset struct {
	Image   string
	Command []string
}

var presets = map[string]preset{
	"python": {Image: "python:3.12-alpine", Command: []string{"python"}},
	"go":     {Image: "golang:1.26-alpine", Command: []string{"go", "run"}},
	"node":   {Image: "node:24-alpine", Command: []string{"node"}},
}

func executeIntent(_ operation.Context, req executeInput) (operation.IntentSet, error) {
	preset, ok := presets[req.Preset]
	if !ok {
		return operation.IntentSet{}, fmt.Errorf("unknown preset")
	}
	if len(req.Files) == 0 {
		return operation.IntentSet{}, fmt.Errorf("files are required")
	}
	command := append([]string(nil), req.Command...)
	if len(command) == 0 {
		command = append([]string(nil), preset.Command...)
		if strings.TrimSpace(req.Entry) != "" {
			command = append(command, req.Entry)
		}
	}
	if len(command) == 0 {
		return operation.IntentSet{}, fmt.Errorf("command or entry is required")
	}
	args := []string{"run", "--rm", "--network", "none", "-v", "<scratch>:/workspace", "-w", "/workspace", preset.Image}
	args = append(args, command...)
	ops := []operation.IntentOperation{
		processIntent("docker", args...),
	}
	for _, file := range req.Files {
		clean := path.Clean(strings.TrimSpace(file.Path))
		if clean == "" || strings.HasPrefix(clean, "/") || clean == ".." || strings.HasPrefix(clean, "../") {
			return operation.IntentSet{}, fmt.Errorf("file path must stay inside scratch workspace")
		}
		ops = append(ops, operation.IntentOperation{
			Behavior:  operation.IntentFilesystemWrite,
			Target:    operation.PathTarget{Path: operation.Path(path.Join("<scratch>", clean))},
			Role:      operation.IntentRoleWriteTarget,
			Certainty: operation.IntentCertain,
		})
	}
	return operation.IntentSet{Operations: ops}, nil
}

func processIntent(command string, args ...string) operation.IntentOperation {
	arguments := make([]operation.Argument, 0, len(args))
	for _, arg := range args {
		arguments = append(arguments, operation.Argument(arg))
	}
	return operation.IntentOperation{
		Behavior:  operation.IntentCommandExecution,
		Target:    operation.ProcessTarget{Command: operation.Command(command), Args: arguments},
		Role:      operation.IntentRoleProcessCommand,
		Certainty: operation.IntentCertain,
	}
}

func (p Plugin) execute() operationruntime.TypedResultHandler[executeInput, map[string]any] {
	return func(ctx operation.Context, req executeInput) operation.Result {
		if strings.TrimSpace(req.Preset) == "" {
			return operation.Failed("invalid_code_execute_input", "preset is required", nil)
		}
		preset, ok := presets[req.Preset]
		if !ok {
			return operation.Failed("invalid_code_execute_preset", "unknown preset", map[string]any{"preset": req.Preset})
		}
		if len(req.Files) == 0 {
			return operation.Failed("invalid_code_execute_input", "files are required", nil)
		}
		scratch, err := p.system.Workspace().CreateScratch(ctx, "agentruntime-code-*")
		if err != nil {
			return operation.Failed("code_execute_setup_failed", err.Error(), nil)
		}
		defer func() { _ = scratch.RemoveAll(ctx) }()
		var written []string
		var writtenBytes int
		for _, file := range req.Files {
			if strings.TrimSpace(file.Path) == "" {
				return operation.Failed("invalid_code_execute_input", "file path is required", nil)
			}
			clean := path.Clean(strings.TrimSpace(file.Path))
			if strings.HasPrefix(clean, "/") || clean == ".." || strings.HasPrefix(clean, "../") {
				return operation.Rejected("code_execute_path_denied", "file path must stay inside scratch workspace", map[string]any{"path": file.Path})
			}
			resolved, err := scratch.WriteFile(ctx, clean, []byte(file.Content), 0644)
			if err != nil {
				return operation.Failed("code_execute_setup_failed", err.Error(), nil)
			}
			written = append(written, resolved.Rel)
			writtenBytes += len(file.Content)
		}
		command := append([]string(nil), req.Command...)
		if len(command) == 0 {
			command = append([]string(nil), preset.Command...)
			if strings.TrimSpace(req.Entry) != "" {
				command = append(command, req.Entry)
			}
		}
		if len(command) == 0 {
			return operation.Failed("invalid_code_execute_input", "command or entry is required", nil)
		}
		timeout := time.Duration(req.TimeoutMS) * time.Millisecond
		if timeout <= 0 || timeout > 10*time.Minute {
			timeout = 30 * time.Second
		}
		args := []string{"run", "--rm", "--network", "none", "-v", scratch.Root() + ":/workspace", "-w", "/workspace", preset.Image}
		args = append(args, command...)
		result, err := p.system.Process().Run(ctx, system.ProcessRequest{
			Command:   "docker",
			Args:      args,
			Timeout:   timeout,
			MaxStdout: 128 * 1024,
			MaxStderr: 128 * 1024,
		})
		emitUsage(ctx, req.Preset, writtenBytes, result)
		data := map[string]any{
			"preset":           req.Preset,
			"image":            preset.Image,
			"files":            written,
			"command":          command,
			"stdout":           result.Stdout,
			"stderr":           result.Stderr,
			"exit_code":        result.ExitCode,
			"timed_out":        result.TimedOut,
			"stdout_truncated": result.StdoutTruncated,
			"stderr_truncated": result.StderrTruncated,
		}
		text := fmt.Sprintf("[code_execute preset=%s image=%s exit=%d duration=%.1fs]\n=== STDOUT ===\n%s\n=== STDERR ===\n%s", req.Preset, preset.Image, result.ExitCode, result.Duration.Seconds(), result.Stdout, result.Stderr)
		if err != nil {
			return operation.Failed("code_execute_failed", err.Error(), data)
		}
		return operation.OK(operation.Rendered{Text: strings.TrimSpace(text), Data: data})
	}
}

func emitUsage(ctx operation.Context, preset string, writtenBytes int, result system.ProcessResult) {
	ctx.Events().Emit(usage.Recorded{
		Source: ExecuteOp,
		Subject: usage.Subject{
			Kind: usage.SubjectProcess,
			Name: preset,
		},
		Measurements: []usage.Measurement{
			{Metric: usage.MetricFileBytes, Quantity: float64(writtenBytes), Unit: usage.UnitByte, Direction: usage.DirectionWrite},
			{Metric: usage.MetricWallTime, Quantity: float64(result.Duration.Milliseconds()), Unit: usage.UnitMillisecond},
			{Metric: usage.MetricFileBytes, Quantity: float64(len(result.Stdout) + len(result.Stderr)), Unit: usage.UnitByte, Direction: usage.DirectionOutput},
		},
	})
}
