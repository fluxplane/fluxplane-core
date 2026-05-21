package coder

import (
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"

	distcli "github.com/fluxplane/engine/adapters/distribution/cli"
	"github.com/fluxplane/engine/apps/launch"
	"github.com/fluxplane/engine/core/command"
	clientapi "github.com/fluxplane/engine/orchestration/client"
)

func newRunPromptHandler(loader launch.Loader) distcli.PromptHandler {
	return func(ctx context.Context, prompt string, _ clientapi.SessionHandle, runOpts distcli.RunOptions) (bool, error) {
		inv, ok, err := command.ParseSlash(prompt)
		if err != nil || !ok {
			return ok, err
		}
		if len(inv.Path) == 0 || inv.Path[0] != "run" {
			return false, nil
		}
		if len(inv.Path) == 1 {
			return true, runPromptApp(ctx, loader, ".", inv, runOpts)
		}
		switch inv.Path[1] {
		case "app":
			path := "."
			if len(inv.Path) > 2 {
				path = inv.Path[2]
			} else if len(inv.Args) > 0 {
				path = inv.Args[0]
				inv.Args = inv.Args[1:]
			}
			return true, runPromptApp(ctx, loader, path, inv, runOpts)
		case "op", "workflow":
			if len(inv.Path) < 3 {
				return true, fmt.Errorf("/run %s requires an id", inv.Path[1])
			}
			opts := resourceRunOptionsFromInvocation(inv, runOpts, 3)
			return true, runCommandResource(ctx, loader, inv.Path[1], inv.Path[2], opts, promptWriterOr(runOpts.Out, io.Discard))
		case "agent":
			if len(inv.Path) < 3 {
				return true, fmt.Errorf("/run agent requires a name")
			}
			opts := resourceRunOptionsFromInvocation(inv, runOpts, 3)
			return true, runAgentResource(ctx, loader, inv.Path[2], opts, promptWriterOr(runOpts.Out, io.Discard))
		default:
			return true, fmt.Errorf("/run target %q is not supported; use /run app, /run op, /run workflow, or /run agent", inv.Path[1])
		}
	}
}

func runPromptApp(ctx context.Context, loader launch.Loader, path string, inv command.Invocation, runOpts distcli.RunOptions) error {
	opts := appRunOptionsFromInvocation(inv, runOpts)
	return launch.RunPathWithLoader(ctx, loader, path, opts)
}

func appRunOptionsFromInvocation(inv command.Invocation, inherited distcli.RunOptions) launch.RunPathOptions {
	values := invocationInputMap(inv)
	return launch.RunPathOptions{
		Session:             inputString(values, "session", ""),
		Conversation:        inputString(values, "conversation", ""),
		Provider:            inputString(values, "provider", ""),
		Model:               inputString(values, "model", ""),
		Thinking:            inputString(values, "thinking", "auto"),
		ThinkingSet:         hasInput(values, "thinking"),
		Effort:              inputString(values, "effort", ""),
		EffortSet:           hasInput(values, "effort"),
		Input:               inputString(values, "input", strings.TrimSpace(strings.Join(inv.Args, " "))),
		Goal:                inputString(values, "goal", ""),
		GoalSet:             hasInput(values, "goal"),
		MaxContinuations:    inputInt(values, "max-continuations", inherited.MaxContinuations),
		MaxContinuationsSet: hasInput(values, "max-continuations"),
		Debug:               inputBool(values, "debug", inherited.Debug),
		Usage:               inputBool(values, "usage", inherited.Usage),
		Yolo:                inputBool(values, "yolo", inherited.Yolo),
		Dev:                 inputBool(values, "dev", inherited.Dev),
		AllowPluginAuthEnv:  inherited.AllowPluginAuthEnv,
		WorkspaceRoots:      append([]string(nil), inherited.WorkspaceRoots...),
		EnvFiles:            append([]string(nil), inherited.EnvFiles...),
		Workspace:           inherited.Workspace,
		In:                  inherited.In,
		Out:                 inherited.Out,
		Err:                 inherited.Err,
	}
}

func resourceRunOptionsFromInvocation(inv command.Invocation, inherited distcli.RunOptions, positionalFrom int) resourceRunOptions {
	values := invocationInputMap(inv)
	opts := resourceRunOptions{
		appPath:      inputString(values, "app", "."),
		provider:     inputString(values, "provider", ""),
		model:        inputString(values, "model", ""),
		conversation: inputString(values, "conversation", ""),
		inputText:    inputString(values, "input", ""),
		inputJSON:    inputString(values, "json", ""),
		args:         map[string]any{},
		yolo:         inputBool(values, "yolo", inherited.Yolo),
		debug:        inputBool(values, "debug", inherited.Debug),
		dev:          inputBool(values, "dev", inherited.Dev),
	}
	if opts.args == nil {
		opts.args = map[string]any{}
	}
	if arg, ok := values["arg"]; ok {
		for _, part := range splitArgValues(arg) {
			key, value, found := strings.Cut(part, "=")
			if found && strings.TrimSpace(key) != "" {
				opts.args[strings.TrimSpace(key)] = value
			}
		}
	}
	for key, value := range values {
		switch key {
		case "app", "provider", "model", "conversation", "input", "json", "arg", "debug", "yolo", "dev":
			continue
		default:
			opts.args[key] = value
		}
	}
	if len(inv.Path) > positionalFrom {
		opts.positional = append(opts.positional, inv.Path[positionalFrom:]...)
	}
	opts.positional = append(opts.positional, inv.Args...)
	return opts
}

func invocationInputMap(inv command.Invocation) map[string]any {
	values, ok := inv.Input.(map[string]any)
	if !ok || len(values) == 0 {
		return nil
	}
	return values
}

func hasInput(values map[string]any, key string) bool {
	_, ok := values[key]
	return ok
}

func inputString(values map[string]any, key, fallback string) string {
	value, ok := values[key]
	if !ok {
		return fallback
	}
	switch typed := value.(type) {
	case string:
		if strings.TrimSpace(typed) == "" {
			return fallback
		}
		return typed
	case fmt.Stringer:
		text := strings.TrimSpace(typed.String())
		if text == "" {
			return fallback
		}
		return text
	default:
		text := strings.TrimSpace(fmt.Sprint(value))
		if text == "" {
			return fallback
		}
		return text
	}
}

func inputBool(values map[string]any, key string, fallback bool) bool {
	value, ok := values[key]
	if !ok {
		return fallback
	}
	switch typed := value.(type) {
	case bool:
		return typed
	case string:
		parsed, err := strconv.ParseBool(typed)
		if err == nil {
			return parsed
		}
	}
	return fallback
}

func inputInt(values map[string]any, key string, fallback int) int {
	value, ok := values[key]
	if !ok {
		return fallback
	}
	switch typed := value.(type) {
	case int:
		return typed
	case string:
		parsed, err := strconv.Atoi(typed)
		if err == nil {
			return parsed
		}
	}
	return fallback
}

func splitArgValues(value any) []string {
	switch typed := value.(type) {
	case []string:
		return typed
	case []any:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			out = append(out, fmt.Sprint(item))
		}
		return out
	default:
		return []string{fmt.Sprint(value)}
	}
}

func promptWriterOr(value io.Writer, fallback io.Writer) io.Writer {
	if value != nil {
		return value
	}
	return fallback
}
