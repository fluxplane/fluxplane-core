package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/fluxplane/agentruntime/adapters/directchannel"
	"github.com/fluxplane/agentruntime/core/channel"
	"github.com/fluxplane/agentruntime/core/command"
	"github.com/fluxplane/agentruntime/core/invocation"
	"github.com/fluxplane/agentruntime/core/operation"
	"github.com/fluxplane/agentruntime/core/policy"
	clientapi "github.com/fluxplane/agentruntime/orchestration/client"
	"github.com/fluxplane/agentruntime/orchestration/harness"
	"github.com/fluxplane/agentruntime/orchestration/session"
	"github.com/fluxplane/agentruntime/runtime/eventstore"
	operationruntime "github.com/fluxplane/agentruntime/runtime/operation"
	runtimethread "github.com/fluxplane/agentruntime/runtime/thread"
)

func main() {
	if err := run(context.Background(), os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: devclient echo <text>")
	}
	commandName := args[0]
	input := strings.Join(args[1:], " ")

	service, err := newHarness()
	if err != nil {
		return err
	}
	client, err := directchannel.New(directchannel.Config{
		Service: service,
		Channel: channel.Ref{Name: "local"},
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
	if err != nil {
		return err
	}
	sessionHandle, err := client.Open(ctx, clientapi.OpenRequest{
		Conversation: channel.ConversationRef{ID: "devclient"},
	})
	if err != nil {
		return err
	}

	run, err := sessionHandle.SendCommand(ctx, command.Invocation{
		Path:  command.Path{commandName},
		Input: input,
	})
	if err != nil {
		return err
	}
	result, err := run.Wait(ctx)
	if err != nil {
		return err
	}
	if result.Command == nil {
		return fmt.Errorf("command produced no command result")
	}
	if result.Command.Status != session.CommandStatusOK {
		if result.Command.Error != nil {
			return fmt.Errorf("%s: %s", result.Command.Error.Code, result.Command.Error.Message)
		}
		return fmt.Errorf("command failed: %s", result.Command.Status)
	}
	if result.Outbound == nil || result.Outbound.Message == nil {
		return fmt.Errorf("command produced no outbound message")
	}
	fmt.Println(result.Outbound.Message.Content)
	return nil
}

func newHarness() (*harness.Service, error) {
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

	threadStore, err := runtimethread.NewStore(eventstore.NewMemoryStore())
	if err != nil {
		return nil, err
	}
	return harness.New(harness.Config{
		Commands:          commands,
		Operations:        ops,
		OperationExecutor: operationruntime.NewExecutor(),
		ThreadStore:       threadStore,
	}), nil
}
