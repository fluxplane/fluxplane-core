package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"

	agentruntime "github.com/fluxplane/agentruntime"
	"github.com/fluxplane/agentruntime/adapters/httpssechannel"
	"github.com/fluxplane/agentruntime/adapters/resourcefs"
	"github.com/fluxplane/agentruntime/core/agent"
	"github.com/fluxplane/agentruntime/core/channel"
	"github.com/fluxplane/agentruntime/core/command"
	"github.com/fluxplane/agentruntime/core/invocation"
	"github.com/fluxplane/agentruntime/core/operation"
	"github.com/fluxplane/agentruntime/core/policy"
	appcomposition "github.com/fluxplane/agentruntime/orchestration/app"
	"github.com/fluxplane/agentruntime/orchestration/pluginhost"
	"github.com/fluxplane/agentruntime/orchestration/session"
	"github.com/fluxplane/agentruntime/plugins/echoplugin"
	"github.com/fluxplane/agentruntime/plugins/textplugin"
)

func main() {
	if err := run(context.Background(), os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string) error {
	cfg, err := parseArgs(args)
	if err != nil {
		return err
	}
	if cfg.mode == "serve" {
		return serve(ctx, cfg.addr, cfg.appDir)
	}

	client, err := newClient(ctx, cfg.remoteURL, cfg.appDir)
	if err != nil {
		return err
	}
	sessionHandle, err := client.Open(ctx, agentruntime.OpenRequest{
		Conversation: channel.ConversationRef{ID: "devclient"},
	})
	if err != nil {
		return err
	}

	submission := agentruntime.Submission{}
	switch cfg.mode {
	case "command":
		submission.Kind = agentruntime.SubmissionCommand
		submission.Command = &command.Invocation{Path: cfg.commandPath, Input: cfg.text}
	case "input":
		submission.Kind = agentruntime.SubmissionInput
		submission.Input = &agentruntime.Input{Text: cfg.text}
	default:
		return fmt.Errorf("unknown mode %q", cfg.mode)
	}
	if cfg.debug {
		debugPrint("input", cfg.text)
		debugPrint("submission", submission)
	}

	run, err := sessionHandle.Submit(ctx, submission)
	if err != nil {
		return err
	}
	eventsDone := debugRunEvents(cfg.debug, run.Events())
	result, err := run.Wait(ctx)
	if err != nil {
		return err
	}
	eventsDone()
	if cfg.debug {
		debugPrint("result", result)
	}
	if err := checkResult(result); err != nil {
		return err
	}
	if result.Outbound == nil || result.Outbound.Message == nil {
		return fmt.Errorf("%s produced no outbound message", cfg.mode)
	}
	if cfg.debug {
		debugPrint("output", result.Outbound.Message.Content)
	}
	fmt.Println(result.Outbound.Message.Content)
	return nil
}

type config struct {
	debug       bool
	remoteURL   string
	appDir      string
	addr        string
	mode        string
	commandPath command.Path
	text        string
}

func parseArgs(args []string) (config, error) {
	cfg := config{addr: "127.0.0.1:8080"}
	positionals := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch arg {
		case "-debug", "--debug":
			cfg.debug = true
		case "-url", "--url":
			i++
			if i >= len(args) {
				return config{}, fmt.Errorf("%s", usage())
			}
			cfg.remoteURL = args[i]
		case "-addr", "--addr":
			i++
			if i >= len(args) {
				return config{}, fmt.Errorf("%s", usage())
			}
			cfg.addr = args[i]
		case "-app", "--app":
			i++
			if i >= len(args) {
				return config{}, fmt.Errorf("%s", usage())
			}
			cfg.appDir = args[i]
		default:
			positionals = append(positionals, arg)
		}
	}
	if len(positionals) == 0 {
		return config{}, fmt.Errorf("%s", usage())
	}
	switch positionals[0] {
	case "input":
		cfg.mode = "input"
		cfg.text = strings.Join(positionals[1:], " ")
	case "serve":
		cfg.mode = "serve"
		cfg.text = strings.Join(positionals[1:], " ")
	default:
		path := parseCommandPath(positionals[0])
		if len(path) == 0 {
			return config{}, fmt.Errorf("%s", usage())
		}
		cfg.mode = "command"
		cfg.commandPath = path
		cfg.text = strings.Join(positionals[1:], " ")
	}
	return cfg, nil
}

func usage() string {
	return "usage: devclient [-debug] [-app <dir>] [-url <base-url>] <command-path> <text> | devclient [-debug] [-app <dir>] [-url <base-url>] input <text> | devclient serve [-addr <addr>] [-app <dir>]"
}

func parseCommandPath(raw string) command.Path {
	parts := strings.FieldsFunc(raw, func(r rune) bool {
		return r == '/'
	})
	path := make(command.Path, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			path = append(path, part)
		}
	}
	return path
}

func debugRunEvents(debug bool, events <-chan agentruntime.Event) func() {
	if !debug {
		return func() {}
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for event := range events {
			debugPrint("event", event)
		}
	}()
	return func() {
		<-done
	}
}

func debugPrint(label string, value any) {
	fmt.Fprintf(os.Stderr, "\n[%s]\n", label)
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "%#v\n", value)
		return
	}
	fmt.Fprintln(os.Stderr, string(data))
}

func checkResult(result agentruntime.Result) error {
	if result.Command != nil {
		if result.Command.Status != session.CommandStatusOK {
			if result.Command.Error != nil {
				return fmt.Errorf("%s: %s", result.Command.Error.Code, result.Command.Error.Message)
			}
			return fmt.Errorf("command failed: %s", result.Command.Status)
		}
		return nil
	}
	if result.Input != nil {
		if result.Input.Status != session.InputStatusOK {
			if result.Input.Error != nil {
				return fmt.Errorf("%s: %s", result.Input.Error.Code, result.Input.Error.Message)
			}
			return fmt.Errorf("input failed: %s", result.Input.Status)
		}
		return nil
	}
	return fmt.Errorf("run produced no command or input result")
}

func newClient(ctx context.Context, remoteURL, appDir string) (agentruntime.ChannelClient, error) {
	if remoteURL != "" {
		return httpssechannel.NewClient(httpssechannel.ClientConfig{BaseURL: remoteURL})
	}
	return newRuntime(ctx, appDir)
}

func serve(ctx context.Context, addr, appDir string) error {
	service, err := newRuntime(ctx, appDir)
	if err != nil {
		return err
	}
	server, err := httpssechannel.NewServer(httpssechannel.ServerConfig{Client: service})
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "devclient listening on http://%s\n", addr)
	return http.ListenAndServe(addr, server)
}

func newRuntime(ctx context.Context, appDir string) (*agentruntime.Service, error) {
	ops := operation.NewRegistry()
	echo := operation.New(operation.Spec{
		Ref:         operation.Ref{Name: "echo"},
		Description: "Return the provided input.",
	}, func(_ operation.Context, input operation.Value) operation.Result {
		return operation.OK(input)
	})
	if err := ops.Register(echo); err != nil {
		return nil, err
	}

	commands := command.NewRegistry()
	if appDir == "" {
		if err := commands.Register(command.Spec{
			Path: command.Path{"echo"},
			Target: invocation.Target{
				Kind:      invocation.TargetOperation,
				Operation: operation.Ref{Name: "echo"},
			},
			Policy: policy.InvocationPolicy{
				AllowedCallers: []policy.CallerKind{policy.CallerUser},
				RequiredTrust:  policy.TrustVerified,
			},
		}); err != nil {
			return nil, err
		}
	}

	cfg := agentruntime.Config{
		Agent:      echoAgent{},
		Commands:   commands,
		Operations: ops,
		Channel:    channel.Ref{Name: "local"},
		Caller: policy.Caller{
			Kind: policy.CallerUser,
			Principal: policy.Principal{
				Kind: "user",
				ID:   "devclient",
				Name: "devclient",
			},
		},
		Trust: policy.Trust{
			Kind:  policy.TrustInvocation,
			Level: policy.TrustVerified,
		},
	}
	if appDir == "" {
		return agentruntime.New(cfg)
	}
	bundle, err := resourcefs.LoadDir(ctx, appDir)
	if err != nil {
		return nil, err
	}
	composition, err := appcomposition.Compose(appcomposition.Config{
		Agent:      cfg.Agent,
		Operations: appOperations(bundle, echo),
		Plugins:    []pluginhost.Plugin{echoplugin.New(), textplugin.New()},
		Bundles:    []agentruntime.ResourceBundle{bundle},
	})
	if err != nil {
		return nil, err
	}
	cfg.Commands = nil
	cfg.Operations = nil
	return agentruntime.NewFromComposition(composition, cfg)
}

func appOperations(bundle agentruntime.ResourceBundle, echo operation.Operation) []operation.Operation {
	for _, ref := range bundle.Plugins {
		if ref.Name == echoplugin.Name {
			return nil
		}
	}
	return []operation.Operation{echo}
}

type echoAgent struct{}

func (echoAgent) Spec() agent.Spec {
	return agent.Spec{Name: "dev-echo-agent"}
}

func (echoAgent) Step(_ agent.Context, input agent.StepInput) agent.StepResult {
	var content any
	if len(input.Observations) > 0 {
		content = input.Observations[0].Content
	}
	return agent.StepResult{
		Status: agent.StatusOK,
		Decision: agent.Decision{
			Kind:    agent.DecisionMessage,
			Message: &agent.Message{Content: content},
		},
	}
}
