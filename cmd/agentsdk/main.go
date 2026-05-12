package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	agentruntime "github.com/fluxplane/agentruntime"
	"github.com/fluxplane/agentruntime/adapters/browsercdp"
	cmdriskadapter "github.com/fluxplane/agentruntime/adapters/cmdrisk"
	codexadapter "github.com/fluxplane/agentruntime/adapters/codex"
	adapterllm "github.com/fluxplane/agentruntime/adapters/llm"
	"github.com/fluxplane/agentruntime/adapters/modelcatalog"
	openaiadapter "github.com/fluxplane/agentruntime/adapters/openai"
	"github.com/fluxplane/agentruntime/adapters/terminalui"
	"github.com/fluxplane/agentruntime/apps/coder"
	"github.com/fluxplane/agentruntime/core/channel"
	"github.com/fluxplane/agentruntime/core/operation"
	"github.com/fluxplane/agentruntime/core/policy"
	"github.com/fluxplane/agentruntime/orchestration/app"
	"github.com/fluxplane/agentruntime/orchestration/pluginhost"
	sessionruntime "github.com/fluxplane/agentruntime/orchestration/session"
	"github.com/fluxplane/agentruntime/plugins/codingplugin"
	llmagent "github.com/fluxplane/agentruntime/runtime/agent/llmagent"
	operationruntime "github.com/fluxplane/agentruntime/runtime/operation"
	"github.com/fluxplane/agentruntime/runtime/system"
	"github.com/spf13/cobra"
)

func main() {
	if err := newRootCommand().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func newRootCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:           "agentsdk",
		Short:         "Run agentsdk tools",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	cmd.AddCommand(newCoderCommand())
	return cmd
}

type coderOptions struct {
	provider string
	model    string
	debug    bool
	usage    bool
}

func newCoderCommand() *cobra.Command {
	var opts coderOptions
	cmd := &cobra.Command{
		Use:   "coder [prompt]",
		Short: "Run the first-party coding agent",
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			prompt := strings.TrimSpace(strings.Join(args, " "))
			if prompt == "" {
				return errors.New("prompt is empty")
			}
			return runCoder(cmd.Context(), opts, prompt)
		},
	}
	cmd.PersistentFlags().StringVar(&opts.provider, "provider", "openai", "model provider")
	cmd.PersistentFlags().StringVar(&opts.model, "model", coder.DefaultModel, "model name or provider/model")
	cmd.PersistentFlags().BoolVar(&opts.debug, "debug", false, "print run events as highlighted JSON markdown")
	cmd.PersistentFlags().BoolVar(&opts.usage, "usage", false, "print usage events after each response")
	cmd.AddCommand(newCoderReplCommand(&opts))
	return cmd
}

func newCoderReplCommand(opts *coderOptions) *cobra.Command {
	return &cobra.Command{
		Use:   "repl",
		Short: "Run coder in an interactive session",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runCoderREPL(cmd.Context(), *opts)
		},
	}
}

func runCoder(ctx context.Context, opts coderOptions, prompt string) error {
	session, err := openCoderSession(ctx, opts)
	if err != nil {
		return err
	}
	return sendCoderPrompt(ctx, session, opts, prompt)
}

func runCoderREPL(ctx context.Context, opts coderOptions) error {
	session, err := openCoderSession(ctx, opts)
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintln(os.Stderr, "agentsdk coder repl. Type /exit or /quit to stop.")
	scanner := bufio.NewScanner(os.Stdin)
	for {
		_, _ = fmt.Fprint(os.Stdout, "coder> ")
		if !scanner.Scan() {
			break
		}
		prompt := strings.TrimSpace(scanner.Text())
		switch prompt {
		case "":
			continue
		case "/exit", "/quit":
			return nil
		}
		if err := sendCoderPrompt(ctx, session, opts, prompt); err != nil {
			_, _ = fmt.Fprintf(os.Stderr, "error: %v\n", err)
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	return nil
}

func openCoderSession(ctx context.Context, opts coderOptions) (agentruntime.Session, error) {
	root, err := workspaceRoot()
	if err != nil {
		return nil, err
	}
	selection := resolveModelSelection(opts)
	model, err := newCoderModel(selection, opts)
	if err != nil {
		return nil, err
	}
	hostSystem, err := system.NewHost(system.Config{Root: root})
	if err != nil {
		return nil, err
	}
	hostSystem.SetClarifier(terminalui.Prompter{In: os.Stdin, Out: os.Stderr})
	browser, err := browsercdp.New(browsercdp.Config{Workspace: hostSystem.Workspace(), Headless: true})
	if err == nil {
		hostSystem.SetBrowser(browser)
	} else if opts.debug {
		_, _ = fmt.Fprintf(os.Stderr, "browser disabled: %v\n", err)
	}
	bundle := coderBundle(selection.Provider, selection.Model)
	composition, err := app.Compose(app.Config{
		Bundles: []agentruntime.ResourceBundle{bundle},
		Plugins: []pluginhost.Plugin{
			codingplugin.New(hostSystem),
		},
		OperationExecutor: operationruntime.NewExecutor(operationruntime.WithSafetyGate(operationruntime.SafetyEnvelope{
			Sandbox:        localSandbox{Root: root},
			ACL:            localACL{},
			CommandRisk:    coderCommandRisk(root),
			MaxCommandRisk: operation.RiskMedium,
			AllowPure:      true,
		})),
	})
	if err != nil {
		return nil, err
	}
	service, err := agentruntime.NewFromComposition(composition, agentruntime.Config{
		LLMModel:        model,
		LLMStreamPolicy: debugStreamPolicy(opts.debug),
		ToolProjection: agentruntime.ToolProjectionConfig{
			AllowSideEffects: true,
			MaxRisk:          operation.RiskMedium,
		},
		Channel: channel.Ref{Name: "local"},
		Caller: policy.Caller{
			Kind: policy.CallerUser,
			Principal: policy.Principal{
				Kind: "user",
				ID:   "agentsdk",
				Name: "agentsdk",
			},
		},
		Trust: policy.Trust{
			Kind:  policy.TrustInvocation,
			Level: policy.TrustVerified,
		},
	})
	if err != nil {
		return nil, err
	}
	session, err := service.Open(ctx, agentruntime.OpenRequest{
		Session:      agentruntime.SessionRef{Name: coder.SessionName},
		Conversation: channel.ConversationRef{ID: "agentsdk-coder"},
	})
	if err != nil {
		return nil, err
	}
	return session, nil
}

func coderBundle(provider, model string) agentruntime.ResourceBundle {
	bundle := coder.Bundle()
	if model == "" {
		return bundle
	}
	for i := range bundle.Agents {
		if bundle.Agents[i].Name == coder.AgentName {
			bundle.Agents[i].Inference.Model = model
		}
	}
	for i := range bundle.Apps {
		if bundle.Apps[i].Name == coder.AppName {
			bundle.Apps[i].Model.Provider = provider
			bundle.Apps[i].Model.Model = model
		}
	}
	return bundle
}

func sendCoderPrompt(ctx context.Context, session agentruntime.Session, opts coderOptions, prompt string) error {
	run, err := session.SendInput(ctx, agentruntime.Input{Text: prompt})
	if err != nil {
		return err
	}
	var eventsDone <-chan bool
	if opts.debug {
		eventsDone = printEvents(run.Events())
	} else {
		eventsDone = renderEvents(run.Events(), opts.usage)
	}
	result, err := run.Wait(ctx)
	streamed := false
	if eventsDone != nil {
		streamed = <-eventsDone
	}
	if !streamed && result.Outbound != nil && result.Outbound.Message != nil {
		_, _ = fmt.Fprintln(os.Stdout, result.Outbound.Message.Content)
	}
	if err != nil {
		return err
	}
	return resultError(result)
}

func resultError(result agentruntime.Result) error {
	if result.Input != nil && result.Input.Status != sessionruntime.InputStatusOK {
		if result.Input.Error != nil {
			return fmt.Errorf("%s: %s", result.Input.Error.Code, result.Input.Error.Message)
		}
		return fmt.Errorf("input failed: %s", result.Input.Status)
	}
	if result.Command != nil && result.Command.Status != sessionruntime.CommandStatusOK {
		if result.Command.Error != nil {
			return fmt.Errorf("%s: %s", result.Command.Error.Code, result.Command.Error.Message)
		}
		return fmt.Errorf("command failed: %s", result.Command.Status)
	}
	return nil
}

func printEvents(events <-chan agentruntime.Event) <-chan bool {
	done := make(chan bool, 1)
	go func() {
		renderer := terminalui.NewRenderer(os.Stdout, os.Stderr, true)
		for event := range events {
			renderer.RenderDebug(event)
			renderer.Render(event)
		}
		renderer.Finish()
		done <- renderer.HasStreamedContent()
		close(done)
	}()
	return done
}

func renderEvents(events <-chan agentruntime.Event, showUsage bool) <-chan bool {
	done := make(chan bool, 1)
	go func() {
		renderer := terminalui.NewRenderer(os.Stdout, os.Stderr, showUsage)
		for event := range events {
			renderer.Render(event)
		}
		renderer.Finish()
		done <- renderer.HasStreamedContent()
		close(done)
	}()
	return done
}

func workspaceRoot() (string, error) {
	wd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	return filepath.Abs(wd)
}

func debugStreamPolicy(debug bool) llmagent.StreamPolicy {
	return llmagent.StreamPolicy{EmitContent: true, EmitThinking: true, EmitToolCall: debug}
}

func debugRedactor(debug bool) adapterllm.Redactor {
	if !debug {
		return adapterllm.Redactor{ExposeThinkingSummary: true}
	}
	return adapterllm.Redactor{ExposeThinking: true, ExposeThinkingSummary: true, ExposeToolArgs: true}
}

type modelSelection struct {
	Provider string
	Model    string
}

func resolveModelSelection(opts coderOptions) modelSelection {
	provider := strings.TrimSpace(opts.provider)
	if provider == "" {
		provider = "openai"
	}
	model := strings.TrimSpace(opts.model)
	if before, after, ok := strings.Cut(model, "/"); ok && before != "" && after != "" {
		provider = before
		model = after
	}
	if model == "" {
		model = coder.DefaultModel
	}
	return modelSelection{Provider: provider, Model: model}
}

func newCoderModel(selection modelSelection, opts coderOptions) (llmagent.Model, error) {
	_, _, _ = modelcatalog.Find(selection.Provider, selection.Model)
	runtime := openaiadapter.DefaultResponsesRuntimeConfig()
	switch selection.Provider {
	case "openai":
		return openaiadapter.New(openaiadapter.Config{
			Model:             selection.Model,
			Runtime:           runtime,
			ParallelToolCalls: true,
			Redactor:          debugRedactor(opts.debug),
		})
	case "codex":
		return codexadapter.New(codexadapter.Config{
			Model:             selection.Model,
			Runtime:           runtime,
			ParallelToolCalls: true,
			Redactor:          debugRedactor(opts.debug),
		})
	default:
		return nil, fmt.Errorf("unknown provider %q", selection.Provider)
	}
}

type localSandbox struct {
	Root string
}

func (s localSandbox) Check(_ operation.Context, spec operation.Spec, input operation.Value) error {
	if spec.Semantics.Effects.Has(operation.EffectProcess) {
		_ = input
		_ = s.Root
	}
	return nil
}

type localACL struct{}

func (localACL) Authorize(operation.Context, operation.Spec, operation.Value) error {
	return nil
}

func coderCommandRisk(root string) operationruntime.CommandRiskClassifier {
	secretPrefixes := []string{
		filepath.Join(root, ".env"),
		filepath.Join(root, ".git", "config"),
		filepath.Join(root, ".git", "credentials"),
	}
	if home, err := os.UserHomeDir(); err == nil {
		secretPrefixes = append(secretPrefixes,
			filepath.Join(home, ".ssh"),
			filepath.Join(home, ".aws"),
			filepath.Join(home, ".config", "gh"),
		)
	}
	return cmdriskadapter.New(cmdriskadapter.Config{
		WorkingDirectory:        root,
		WorkspacePathPrefixes:   []string{root},
		SecretPathPrefixes:      secretPrefixes,
		SensitivePathPrefixes:   []string{filepath.Join(root, ".git")},
		Sandboxed:               false,
		Disposable:              false,
		Interactive:             false,
		NetworkApprovalAsMedium: true,
	})
}
