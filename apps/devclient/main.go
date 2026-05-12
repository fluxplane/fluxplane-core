package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	agentruntime "github.com/fluxplane/agentruntime"
	"github.com/fluxplane/agentruntime/core/agent"
	"github.com/fluxplane/agentruntime/core/channel"
	"github.com/fluxplane/agentruntime/core/command"
	"github.com/fluxplane/agentruntime/core/invocation"
	"github.com/fluxplane/agentruntime/core/operation"
	"github.com/fluxplane/agentruntime/core/policy"
	"github.com/fluxplane/agentruntime/orchestration/session"
)

func main() {
	if err := run(context.Background(), os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string) error {
	debug, mode, text, err := parseArgs(args)
	if err != nil {
		return err
	}

	service, err := newRuntime()
	if err != nil {
		return err
	}
	sessionHandle, err := service.Open(ctx, agentruntime.OpenRequest{
		Conversation: channel.ConversationRef{ID: "devclient"},
	})
	if err != nil {
		return err
	}

	submission := agentruntime.Submission{}
	switch mode {
	case "echo":
		submission.Kind = agentruntime.SubmissionCommand
		submission.Command = &command.Invocation{Path: command.Path{mode}, Input: text}
	case "input":
		submission.Kind = agentruntime.SubmissionInput
		submission.Input = &agentruntime.Input{Text: text}
	default:
		return fmt.Errorf("unknown mode %q (use echo or input)", mode)
	}
	if debug {
		debugPrint("input", text)
		debugPrint("submission", submission)
	}

	run, err := sessionHandle.Submit(ctx, submission)
	if err != nil {
		return err
	}
	eventsDone := debugRunEvents(debug, run.Events())
	result, err := run.Wait(ctx)
	if err != nil {
		return err
	}
	eventsDone()
	if debug {
		debugPrint("result", result)
	}
	if err := checkResult(result); err != nil {
		return err
	}
	if result.Outbound == nil || result.Outbound.Message == nil {
		return fmt.Errorf("%s produced no outbound message", mode)
	}
	if debug {
		debugPrint("output", result.Outbound.Message.Content)
	}
	fmt.Println(result.Outbound.Message.Content)
	return nil
}

func parseArgs(args []string) (bool, string, string, error) {
	debug := false
	positionals := make([]string, 0, len(args))
	for _, arg := range args {
		switch arg {
		case "-debug", "--debug":
			debug = true
		default:
			positionals = append(positionals, arg)
		}
	}
	if len(positionals) == 0 {
		return false, "", "", fmt.Errorf("usage: devclient [-debug] echo <text> | devclient [-debug] input <text>")
	}
	return debug, positionals[0], strings.Join(positionals[1:], " "), nil
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

func newRuntime() (*agentruntime.Service, error) {
	ops := operation.NewRegistry()
	if err := ops.Register(operation.New(operation.Spec{
		Ref:         operation.Ref{Name: "echo"},
		Description: "Return the provided input.",
	}, func(_ operation.Context, input operation.Value) operation.Result {
		return operation.OK(input)
	})); err != nil {
		return nil, err
	}

	commands := command.NewRegistry()
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

	return agentruntime.New(agentruntime.Config{
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
	})
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
