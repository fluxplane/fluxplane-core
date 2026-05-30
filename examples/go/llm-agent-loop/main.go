package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	fluxplane "github.com/fluxplane/fluxplane-core"
	"github.com/fluxplane/fluxplane-core/adapters/llm/openrouter"
	"github.com/fluxplane/fluxplane-core/core/agent"
	"github.com/fluxplane/fluxplane-core/core/channel"
	llmagent "github.com/fluxplane/fluxplane-core/runtime/agent/llmagent"
	"github.com/fluxplane/fluxplane-policy"
)

func main() {
	if err := run(context.Background(), os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string) error {
	prompt := strings.TrimSpace(strings.Join(args, " "))
	if prompt == "" {
		prompt = "Reply with one sentence explaining what Fluxplane is doing in this example and how you know that."
	}
	if strings.TrimSpace(os.Getenv("OPENROUTER_API_KEY")) == "" {
		return fmt.Errorf("OPENROUTER_API_KEY is required")
	}
	modelName := strings.TrimSpace(os.Getenv("OPENROUTER_MODEL"))
	if modelName == "" {
		modelName = "openai/gpt-5.5"
	}
	model, err := openrouter.New(openrouter.Config{
		Model:             modelName,
		Title:             "fluxplane-go-example",
		ParallelToolCalls: true,
	})
	if err != nil {
		return err
	}
	runtimeAgent, err := llmagent.New(agent.Spec{
		Name:   "assistant",
		System: "You are a concise assistant embedded in a small Go program.",
		Driver: agent.DriverSpec{
			Kind: llmagent.DriverKind,
		},
		Inference: agent.InferenceSpec{
			Model: modelName,
		},
		Turns: agent.TurnPolicy{
			MaxSteps: 4,
		},
	}, model)
	if err != nil {
		return err
	}
	service, err := fluxplane.New(fluxplane.Config{
		Agent:    runtimeAgent,
		LLMModel: model,
		Channel:  channel.Ref{Name: "local"},
		Caller: policy.Caller{
			Kind: policy.CallerUser,
			Principal: policy.Principal{
				Kind: "user",
				ID:   "go-example",
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
	session, err := service.Open(ctx, fluxplane.OpenRequest{
		Conversation: channel.ConversationRef{ID: "go-llm-agent-loop"},
	})
	if err != nil {
		return err
	}
	defer func() { _ = session.Close(ctx) }()

	run, err := session.Submit(ctx, fluxplane.NewSubmission().WithText(prompt))
	if err != nil {
		return err
	}
	result, err := run.Wait(ctx)
	if err != nil {
		return err
	}
	if result.Outbound == nil || result.Outbound.Message == nil {
		return fmt.Errorf("agent produced no outbound message")
	}
	fmt.Println(result.Outbound.Message.Content)
	return nil
}
