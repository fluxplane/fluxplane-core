package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"

	fluxplane "github.com/fluxplane/fluxplane-core"
	"github.com/fluxplane/fluxplane-core/adapters/channels/httpsse"
	adapterllm "github.com/fluxplane/fluxplane-core/adapters/llm"
	"github.com/fluxplane/fluxplane-core/adapters/llm/openai"
	"github.com/fluxplane/fluxplane-core/adapters/resources/appconfig"
	"github.com/fluxplane/fluxplane-core/core/agent"
	"github.com/fluxplane/fluxplane-core/core/channel"
	"github.com/fluxplane/fluxplane-core/core/command"
	"github.com/fluxplane/fluxplane-core/core/invocation"
	coresession "github.com/fluxplane/fluxplane-core/core/session"
	"github.com/fluxplane/fluxplane-core/core/tool"
	appcomposition "github.com/fluxplane/fluxplane-core/orchestration/app"
	"github.com/fluxplane/fluxplane-core/orchestration/pluginhost"
	"github.com/fluxplane/fluxplane-core/orchestration/session"
	"github.com/fluxplane/fluxplane-core/plugins/examples/echo"
	"github.com/fluxplane/fluxplane-core/plugins/native/text"
	llmagent "github.com/fluxplane/fluxplane-core/runtime/agent/llmagent"
	"github.com/fluxplane/fluxplane-operation"
	"github.com/fluxplane/fluxplane-policy"
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
		return serve(ctx, cfg)
	}

	client, err := newClient(ctx, cfg)
	if err != nil {
		return err
	}
	sessionHandle, err := client.Open(ctx, fluxplane.OpenRequest{
		Session:      coresession.Ref{Name: coresession.Name(cfg.sessionName)},
		Conversation: channel.ConversationRef{ID: "devclient"},
	})
	if err != nil {
		return err
	}

	submission := fluxplane.NewSubmission()
	switch cfg.mode {
	case "command":
		submission = submission.WithCommand(command.Invocation{Path: cfg.commandPath, Input: cfg.text})
	case "input":
		submission = submission.WithText(cfg.text)
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
	debug         bool
	remoteURL     string
	appDir        string
	sessionName   string
	addr          string
	useOpenAI     bool
	openAIModel   string
	syntheticTool bool
	mode          string
	commandPath   command.Path
	text          string
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
		case "-openai", "--openai":
			cfg.useOpenAI = true
		case "-openai-model", "--openai-model":
			i++
			if i >= len(args) {
				return config{}, fmt.Errorf("%s", usage())
			}
			cfg.openAIModel = args[i]
		case "-synthetic-tool", "--synthetic-tool":
			cfg.syntheticTool = true
		case "-session", "--session":
			i++
			if i >= len(args) {
				return config{}, fmt.Errorf("%s", usage())
			}
			cfg.sessionName = args[i]
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
	if cfg.openAIModel == "" {
		cfg.openAIModel = os.Getenv("OPENAI_MODEL")
	}
	if cfg.useOpenAI && cfg.openAIModel == "" {
		cfg.openAIModel = "gpt-4.1-mini"
	}
	return cfg, nil
}

func usage() string {
	return "usage: devclient [-debug] [-openai] [-openai-model <model>] [-synthetic-tool] [-app <dir>] [-session <name>] [-url <base-url>] <command-path> <text> | devclient [-debug] [-openai] [-openai-model <model>] [-synthetic-tool] [-app <dir>] [-session <name>] [-url <base-url>] input <text> | devclient serve [-addr <addr>] [-openai] [-openai-model <model>] [-synthetic-tool] [-app <dir>]"
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

func debugRunEvents(debug bool, events <-chan fluxplane.Event) func() {
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

func checkResult(result fluxplane.Result) error {
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

func newClient(ctx context.Context, cfg config) (fluxplane.ChannelClient, error) {
	if cfg.remoteURL != "" {
		return httpsse.NewClient(httpsse.ClientConfig{BaseURL: cfg.remoteURL})
	}
	return newRuntime(ctx, cfg)
}

func serve(ctx context.Context, cfg config) error {
	service, err := newRuntime(ctx, cfg)
	if err != nil {
		return err
	}
	server, err := httpsse.NewServer(httpsse.ServerConfig{Client: service})
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "devclient listening on http://%s\n", cfg.addr)
	return http.ListenAndServe(cfg.addr, server)
}

func newRuntime(ctx context.Context, dev config) (*fluxplane.Service, error) {
	ops := operation.NewRegistry()
	echoOp := operation.New(operation.Spec{
		Ref:         operation.Ref{Name: "echo"},
		Description: "Return the provided input.",
	}, func(_ operation.Context, input operation.Value) operation.Result {
		return operation.OK(input)
	})
	if err := ops.Register(echoOp); err != nil {
		return nil, err
	}
	synthetic := syntheticLookupOperation()
	if dev.syntheticTool {
		if err := ops.Register(synthetic); err != nil {
			return nil, err
		}
	}

	commands := command.NewRegistry()
	if dev.appDir == "" {
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

	runtimeAgent := agent.Agent(echoAgent{})
	var model llmagent.Model
	if dev.useOpenAI {
		system := "You are a concise development assistant running inside Fluxplane Engine."
		var tools []tool.Spec
		if dev.syntheticTool {
			system += " When a user asks for synthetic runtime data, call the synthetic_lookup tool first. After the tool result arrives, answer from that result and do not call the tool again."
			tools = append(tools, syntheticLookupTool(synthetic.Spec()))
		}
		openAIModel, err := openai.New(openai.Config{
			Model:             dev.openAIModel,
			ParallelToolCalls: true,
			Redactor:          debugRedactor(dev.debug),
		})
		if err != nil {
			return nil, err
		}
		model = openAIModel
		runtimeAgent, err = llmagent.New(agent.Spec{
			Name:   "dev-openai-agent",
			System: system,
			Driver: agent.DriverSpec{
				Kind: llmagent.DriverKind,
			},
			Inference: agent.InferenceSpec{
				Model: dev.openAIModel,
			},
			Turns: agent.TurnPolicy{MaxSteps: 50},
		}, model, llmagent.WithTools(tools...), llmagent.WithStreamPolicy(debugStreamPolicy(dev.debug)))
		if err != nil {
			return nil, err
		}
	}

	cfg := fluxplane.Config{
		Agent:      runtimeAgent,
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
	if dev.useOpenAI {
		cfg.LLMModel = model
		cfg.LLMStreamPolicy = debugStreamPolicy(dev.debug)
	}
	if dev.appDir == "" {
		return fluxplane.New(cfg)
	}
	bundle, err := appconfig.LoadDir(ctx, dev.appDir)
	if err != nil {
		return nil, err
	}
	composeAgent := cfg.Agent
	if dev.useOpenAI {
		composeAgent = nil
	}
	composition, err := appcomposition.Compose(appcomposition.Config{
		Agent:      composeAgent,
		Operations: appOperations(bundle, echoOp),
		Plugins:    []pluginhost.Plugin{echo.New(), text.New()},
		Bundles:    []fluxplane.ResourceBundle{bundle},
	})
	if err != nil {
		return nil, err
	}
	cfg.Commands = nil
	cfg.Operations = nil
	if dev.useOpenAI {
		cfg.Agent = nil
	}
	return fluxplane.NewFromComposition(composition, cfg)
}

func syntheticLookupOperation() operation.Operation {
	spec := operation.Spec{
		Ref:         operation.Ref{Name: "synthetic_lookup"},
		Description: "Return deterministic synthetic runtime facts for a requested key.",
		Input: operation.Type{
			Name:        "SyntheticLookupInput",
			Description: "Lookup request. Provide a key such as alpha, beta, or gamma.",
			Schema: operation.Schema{
				Format: "json-schema",
				Data:   json.RawMessage(`{"type":"object","properties":{"key":{"type":"string","description":"Synthetic key to lookup."}},"required":["key"],"additionalProperties":false}`),
			},
		},
		Output: operation.Type{
			Name:        "SyntheticLookupOutput",
			Description: "Synthetic fact returned by the app-injected tool.",
		},
		Semantics: operation.Semantics{
			Determinism: operation.DeterminismDeterministic,
			Effects:     operation.EffectSet{operation.EffectNone},
			Idempotency: operation.IdempotencyIdempotent,
			Risk:        operation.RiskLow,
		},
	}
	return operation.New(spec, func(_ operation.Context, input operation.Value) operation.Result {
		key := syntheticLookupKey(input)
		if key == "" {
			key = "alpha"
		}
		return operation.OK(map[string]any{
			"key":     key,
			"value":   strings.ToUpper(key) + "-42",
			"source":  "apps/devclient synthetic_lookup",
			"message": "synthetic runtime data resolved successfully",
		})
	})
}

func syntheticLookupTool(spec operation.Spec) tool.Spec {
	return tool.Spec{
		Name:        "synthetic_lookup",
		Description: spec.Description,
		Target: invocation.Target{
			Kind:      invocation.TargetOperation,
			Operation: spec.Ref,
		},
		Input:     spec.Input,
		Output:    spec.Output,
		Semantics: spec.Semantics,
	}
}

func syntheticLookupKey(input any) string {
	switch value := input.(type) {
	case map[string]any:
		if key, ok := value["key"].(string); ok {
			return strings.TrimSpace(key)
		}
	case map[string]string:
		return strings.TrimSpace(value["key"])
	case string:
		return strings.TrimSpace(value)
	}
	return ""
}

func debugStreamPolicy(debug bool) llmagent.StreamPolicy {
	if !debug {
		return llmagent.StreamPolicy{}
	}
	return llmagent.StreamPolicy{EmitContent: true, EmitThinking: true, EmitToolCall: true}
}

func debugRedactor(debug bool) adapterllm.Redactor {
	if !debug {
		return adapterllm.Redactor{}
	}
	return adapterllm.Redactor{ExposeThinking: true, ExposeToolArgs: true}
}

func appOperations(bundle fluxplane.ResourceBundle, echoOp operation.Operation) []operation.Operation {
	for _, ref := range bundle.Plugins {
		if ref.Name == echo.Name {
			return nil
		}
	}
	return []operation.Operation{echoOp}
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
