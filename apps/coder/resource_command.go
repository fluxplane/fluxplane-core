package coder

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	agentruntime "github.com/fluxplane/agentruntime"
	distlocal "github.com/fluxplane/agentruntime/adapters/distribution/local"
	"github.com/fluxplane/agentruntime/apps/launch"
	"github.com/fluxplane/agentruntime/core/agent"
	"github.com/fluxplane/agentruntime/core/channel"
	"github.com/fluxplane/agentruntime/core/command"
	"github.com/fluxplane/agentruntime/core/invocation"
	"github.com/fluxplane/agentruntime/core/operation"
	"github.com/fluxplane/agentruntime/core/resource"
	coresession "github.com/fluxplane/agentruntime/core/session"
	"github.com/fluxplane/agentruntime/core/workflow"
	clientapi "github.com/fluxplane/agentruntime/orchestration/client"
	"github.com/fluxplane/agentruntime/orchestration/distribution"
	"github.com/fluxplane/agentruntime/orchestration/session"
	"github.com/spf13/cobra"
)

const resourceRunSession = "__coder_resource_run"

type resourceRunOptions struct {
	appPath      string
	provider     string
	model        string
	conversation string
	inputText    string
	inputJSON    string
	args         map[string]any
	positional   []string
	yolo         bool
	debug        bool
	dev          bool
}

func newOpCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "op",
		Short: "Run app operations",
	}
	cmd.AddCommand(newResourceRunCommand("op", distlocal.Load))
	return cmd
}

func newWorkflowCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "workflow",
		Short: "Run app workflows",
	}
	cmd.AddCommand(newResourceRunCommand("workflow", distlocal.Load))
	return cmd
}

func newAgentCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "agent",
		Short: "Run app agents",
	}
	cmd.AddCommand(newAgentRunCommand(distlocal.Load))
	return cmd
}

func newResourceRunCommand(kind string, loader launch.Loader) *cobra.Command {
	cmd := &cobra.Command{
		Use:                "run <id> [flags]",
		Short:              "Run an app " + kind,
		Args:               cobra.ArbitraryArgs,
		DisableFlagParsing: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if wantsHelp(args) {
				return resourceRunHelp(cmd.OutOrStdout(), kind)
			}
			name, opts, err := parseResourceRunArgs(args)
			if err != nil {
				return err
			}
			return runCommandResource(cmd.Context(), loader, kind, name, opts, cmd.OutOrStdout())
		},
	}
	return cmd
}

func newAgentRunCommand(loader launch.Loader) *cobra.Command {
	cmd := &cobra.Command{
		Use:                "run <name> [flags] [input]",
		Short:              "Run an app agent",
		Args:               cobra.ArbitraryArgs,
		DisableFlagParsing: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if wantsHelp(args) {
				return agentRunHelp(cmd.OutOrStdout())
			}
			name, opts, err := parseResourceRunArgs(args)
			if err != nil {
				return err
			}
			return runAgentResource(cmd.Context(), loader, name, opts, cmd.OutOrStdout())
		},
	}
	return cmd
}

func wantsHelp(args []string) bool {
	if len(args) == 0 {
		return true
	}
	for _, arg := range args {
		if arg == "-h" || arg == "--help" {
			return true
		}
	}
	return false
}

func resourceRunHelp(out io.Writer, kind string) error {
	_, _ = fmt.Fprintf(out, `Run an app %[1]s.

Usage:
  coder %[1]s run <id> [--app path] [--json JSON | --arg key=value | --key=value]

Flags:
      --app path       app directory or manifest scope (default ".")
      --json JSON      JSON input payload
      --arg key=value  add one object input field
      --provider name  model provider override
      --model name     model override
      --conversation id
      --debug
      --yolo
`, kind)
	return nil
}

func agentRunHelp(out io.Writer) error {
	_, _ = fmt.Fprint(out, `Run an app agent.

Usage:
  coder agent run <name> [--app path] [--input text | text...]

Flags:
      --app path       app directory or manifest scope (default ".")
      --input text     input text
      --provider name  model provider override
      --model name     model override
      --conversation id
      --debug
      --yolo
`)
	return nil
}

func parseResourceRunArgs(args []string) (string, resourceRunOptions, error) {
	if len(args) == 0 {
		return "", resourceRunOptions{}, fmt.Errorf("run: target id is required")
	}
	name := strings.TrimSpace(args[0])
	if name == "" {
		return "", resourceRunOptions{}, fmt.Errorf("run: target id is empty")
	}
	opts := resourceRunOptions{appPath: ".", args: map[string]any{}}
	rest := args[1:]
	for i := 0; i < len(rest); i++ {
		arg := rest[i]
		if arg == "--" {
			opts.positional = append(opts.positional, rest[i+1:]...)
			break
		}
		if !strings.HasPrefix(arg, "-") {
			opts.positional = append(opts.positional, arg)
			continue
		}
		key, value, hasValue := strings.Cut(strings.TrimLeft(arg, "-"), "=")
		key = strings.TrimSpace(key)
		if key == "" {
			return "", opts, fmt.Errorf("run: invalid flag %q", arg)
		}
		switch key {
		case "app":
			value, i = flagValue(rest, i, value, hasValue, key)
			opts.appPath = value
		case "provider":
			value, i = flagValue(rest, i, value, hasValue, key)
			opts.provider = value
		case "model":
			value, i = flagValue(rest, i, value, hasValue, key)
			opts.model = value
		case "conversation":
			value, i = flagValue(rest, i, value, hasValue, key)
			opts.conversation = value
		case "input":
			value, i = flagValue(rest, i, value, hasValue, key)
			opts.inputText = value
		case "json":
			value, i = flagValue(rest, i, value, hasValue, key)
			opts.inputJSON = value
		case "arg":
			value, i = flagValue(rest, i, value, hasValue, key)
			field, fieldValue, ok := strings.Cut(value, "=")
			if !ok || strings.TrimSpace(field) == "" {
				return "", opts, fmt.Errorf("run: --arg expects key=value")
			}
			opts.args[strings.TrimSpace(field)] = fieldValue
		case "debug":
			opts.debug = true
		case "yolo":
			opts.yolo = true
		case "dev":
			opts.dev = true
		default:
			if !hasValue && i+1 < len(rest) && !strings.HasPrefix(rest[i+1], "-") {
				value = rest[i+1]
				i++
			} else if !hasValue {
				opts.args[key] = true
				continue
			}
			opts.args[key] = value
		}
	}
	return name, opts, nil
}

func flagValue(args []string, index int, current string, hasValue bool, key string) (string, int) {
	if hasValue {
		return current, index
	}
	if index+1 >= len(args) {
		return "", index
	}
	return args[index+1], index + 1
}

func commandInput(opts resourceRunOptions) (any, error) {
	if strings.TrimSpace(opts.inputJSON) != "" {
		var input any
		if err := json.Unmarshal([]byte(opts.inputJSON), &input); err != nil {
			return nil, fmt.Errorf("run: decode --json: %w", err)
		}
		return input, nil
	}
	if strings.TrimSpace(opts.inputText) != "" {
		return opts.inputText, nil
	}
	if len(opts.args) > 0 {
		input := map[string]any{}
		for key, value := range opts.args {
			input[key] = value
		}
		if len(opts.positional) > 0 {
			input["args"] = append([]string(nil), opts.positional...)
		}
		return input, nil
	}
	if len(opts.positional) > 0 {
		return append([]string(nil), opts.positional...), nil
	}
	return nil, nil
}

func textInput(opts resourceRunOptions) string {
	if strings.TrimSpace(opts.inputText) != "" {
		return opts.inputText
	}
	return strings.TrimSpace(strings.Join(opts.positional, " "))
}

func runCommandResource(ctx context.Context, loader launch.Loader, kind, name string, opts resourceRunOptions, out io.Writer) error {
	input, err := commandInput(opts)
	if err != nil {
		return err
	}
	loaded, err := loadResourceRunDistribution(ctx, loader, opts, commandResourceBundle(kind, name))
	if err != nil {
		return err
	}
	sessionHandle, err := openResourceRunSession(ctx, loaded, opts)
	if err != nil {
		return err
	}
	defer func() { _ = sessionHandle.Close(ctx) }()
	run, err := sessionHandle.Submit(ctx, agentruntime.NewSubmission().WithCommand(command.Invocation{
		Path:  command.Path{"coder", kind, "run"},
		Input: input,
	}))
	if err != nil {
		return err
	}
	result, err := run.Wait(ctx)
	if err != nil {
		return err
	}
	return renderRunResult(out, result)
}

func runAgentResource(ctx context.Context, loader launch.Loader, name string, opts resourceRunOptions, out io.Writer) error {
	input := textInput(opts)
	if input == "" {
		return fmt.Errorf("agent run: input text is required")
	}
	sessionSpec := coresession.Spec{
		Name:  coresession.Name(resourceRunSession),
		Agent: agent.Ref{Name: agent.Name(name)},
	}
	loaded, err := loadResourceRunDistribution(ctx, loader, opts, resource.ContributionBundle{Sessions: []coresession.Spec{sessionSpec}})
	if err != nil {
		return err
	}
	loaded.Distribution.Spec.DefaultSession = coresession.Ref{Name: sessionSpec.Name}
	sessionHandle, err := openResourceRunSession(ctx, loaded, opts)
	if err != nil {
		return err
	}
	defer func() { _ = sessionHandle.Close(ctx) }()
	run, err := sessionHandle.Submit(ctx, agentruntime.NewSubmission().WithText(input))
	if err != nil {
		return err
	}
	result, err := run.Wait(ctx)
	if err != nil {
		return err
	}
	return renderRunResult(out, result)
}

func commandResourceBundle(kind, name string) resource.ContributionBundle {
	target := invocation.Target{}
	switch kind {
	case "op":
		target = invocation.Target{Kind: invocation.TargetOperation, Operation: operation.Ref{Name: operation.Name(name)}}
	case "workflow":
		target = invocation.Target{Kind: invocation.TargetWorkflow, Workflow: workflow.Name(name)}
	}
	return resource.ContributionBundle{
		Source: resource.SourceRef{
			ID:       "coder-resource-run:" + kind,
			Scope:    resource.ScopeEmbedded,
			Location: "apps/coder/resource-run",
		},
		Commands: []command.Spec{{
			Path:        command.Path{"coder", kind, "run"},
			Description: "Run " + kind + " " + name,
			Target:      target,
			Annotations: map[string]string{
				"name": "coder:" + kind + ":run",
			},
		}},
		Sessions: []coresession.Spec{{
			Name:        resourceRunSession,
			Description: "Temporary coder resource run session.",
		}},
	}
}

func loadResourceRunDistribution(ctx context.Context, loader launch.Loader, opts resourceRunOptions, extra resource.ContributionBundle) (distribution.Loaded, error) {
	if loader == nil {
		loader = distlocal.Load
	}
	loaded, err := loader(ctx, opts.appPath)
	if err != nil {
		return distribution.Loaded{}, err
	}
	loaded.Distribution.Bundles = append(append([]resource.ContributionBundle(nil), loaded.Distribution.Bundles...), extra)
	if loaded.Distribution.Spec.DefaultSession.Name == "" {
		loaded.Distribution.Spec.DefaultSession = coresession.Ref{Name: resourceRunSession}
	}
	loaded = launch.AttachLocalRuntime(loaded)
	if loaded.Distribution.Runtime == nil {
		return distribution.Loaded{}, fmt.Errorf("run: distribution %q has no runtime", loaded.Distribution.Spec.Name)
	}
	return loaded, nil
}

func openResourceRunSession(ctx context.Context, loaded distribution.Loaded, opts resourceRunOptions) (clientapi.SessionHandle, error) {
	conversation := opts.conversation
	if conversation == "" {
		conversation = "coder-resource-run"
	}
	return loaded.Distribution.Runtime.OpenSession(ctx, distribution.OpenRequest{
		Session:      loaded.Distribution.Spec.DefaultSession,
		Conversation: channel.ConversationRef{ID: conversation},
		Provider:     opts.provider,
		Model:        opts.model,
		Debug:        opts.debug,
		Yolo:         opts.yolo,
		Dev:          opts.dev,
	})
}

func renderRunResult(out io.Writer, result clientapi.Result) error {
	if result.Outbound != nil && result.Outbound.Message != nil {
		_, _ = fmt.Fprintln(out, result.Outbound.Message.Content)
	}
	if result.Command != nil && result.Command.Status != session.CommandStatusOK {
		if result.Command.Error != nil {
			return fmt.Errorf("%s: %s", result.Command.Error.Code, result.Command.Error.Message)
		}
		return fmt.Errorf("command status: %s", result.Command.Status)
	}
	if result.Input != nil && result.Input.Status != session.InputStatusOK {
		if result.Input.Error != nil {
			return fmt.Errorf("%s: %s", result.Input.Error.Code, result.Input.Error.Message)
		}
		return fmt.Errorf("input status: %s", result.Input.Status)
	}
	return nil
}
