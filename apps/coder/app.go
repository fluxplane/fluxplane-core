package coder

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	agentruntime "github.com/fluxplane/agentruntime"
	"github.com/fluxplane/agentruntime/adapters/anthropic"
	"github.com/fluxplane/agentruntime/adapters/browsercdp"
	"github.com/fluxplane/agentruntime/adapters/cmdrisk"
	"github.com/fluxplane/agentruntime/adapters/codex"
	distcli "github.com/fluxplane/agentruntime/adapters/distribution/cli"
	adapterllm "github.com/fluxplane/agentruntime/adapters/llm"
	"github.com/fluxplane/agentruntime/adapters/minimax"
	"github.com/fluxplane/agentruntime/adapters/modelcatalog"
	"github.com/fluxplane/agentruntime/adapters/openai"
	"github.com/fluxplane/agentruntime/adapters/openrouter"
	"github.com/fluxplane/agentruntime/adapters/terminalui"
	"github.com/fluxplane/agentruntime/core/channel"
	coredistribution "github.com/fluxplane/agentruntime/core/distribution"
	corellm "github.com/fluxplane/agentruntime/core/llm"
	"github.com/fluxplane/agentruntime/core/operation"
	"github.com/fluxplane/agentruntime/core/policy"
	"github.com/fluxplane/agentruntime/orchestration/app"
	clientapi "github.com/fluxplane/agentruntime/orchestration/client"
	"github.com/fluxplane/agentruntime/orchestration/distribution"
	"github.com/fluxplane/agentruntime/orchestration/pluginhost"
	"github.com/fluxplane/agentruntime/plugins/codingplugin"
	"github.com/fluxplane/agentruntime/plugins/planexecplugin"
	"github.com/fluxplane/agentruntime/plugins/skillplugin"
	llmagent "github.com/fluxplane/agentruntime/runtime/agent/llmagent"
	operationruntime "github.com/fluxplane/agentruntime/runtime/operation"
	"github.com/fluxplane/agentruntime/runtime/system"
	"github.com/spf13/cobra"
)

const defaultConversation = "agentsdk-coder"

// Options configures local coder execution.
type Options struct {
	Provider string
	Model    string
	Debug    bool
}

// Runtime opens local sessions for the coder distribution.
type Runtime struct{}

// NewCommand returns the CLI command for the coder distribution.
func NewCommand() *cobra.Command {
	return distcli.NewCommand(Distribution())
}

// Distribution returns the runnable/deployable coder distribution declaration.
func Distribution() distribution.Distribution {
	return distribution.Distribution{
		Spec: coredistribution.Spec{
			Name:                AppName,
			Title:               "Coder",
			Description:         "Run coder in an interactive session",
			DefaultSession:      agentruntime.SessionRef{Name: SessionName},
			DefaultConversation: channel.ConversationRef{ID: defaultConversation},
			DefaultModel: coredistribution.ModelDefault{
				Provider: "openai",
				Model:    DefaultModel,
				UseCase:  "coding",
			},
			Surfaces: coredistribution.Surfaces{
				CLI:     true,
				REPL:    true,
				OneShot: true,
				Serve:   true,
			},
		},
		Bundles: []agentruntime.ResourceBundle{Bundle()},
		Runtime: Runtime{},
	}
}

// OpenSession opens a local coder session.
func OpenSession(ctx context.Context, opts Options) (agentruntime.Session, error) {
	root, err := workspaceRoot()
	if err != nil {
		return nil, err
	}
	selection := ResolveModelSelection(opts.Provider, opts.Model)
	model, err := NewModel(selection, opts.Debug)
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
	} else if opts.Debug {
		_, _ = fmt.Fprintf(os.Stderr, "browser disabled: %v\n", err)
	}
	composition, err := app.Compose(app.Config{
		Bundles: []agentruntime.ResourceBundle{BundleWithModel(selection.Provider, selection.Model)},
		Plugins: []pluginhost.Plugin{
			codingplugin.New(hostSystem),
			planexecplugin.New(),
			skillplugin.New(),
		},
		OperationExecutor: operationruntime.NewExecutor(operationruntime.WithSafetyGate(operationruntime.SafetyEnvelope{
			Sandbox:        localSandbox{Root: root},
			ACL:            localACL{},
			CommandRisk:    CommandRisk(root),
			MaxCommandRisk: operation.RiskMedium,
			AllowPure:      true,
		})),
	})
	if err != nil {
		return nil, err
	}
	service, err := agentruntime.NewFromComposition(composition, agentruntime.Config{
		LLMModel:        model,
		LLMStreamPolicy: DebugStreamPolicy(opts.Debug),
		ToolProjection:  ToolProjectionConfig(),
		Channel:         channel.Ref{Name: "local"},
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
	return service.Open(ctx, agentruntime.OpenRequest{
		Session:      agentruntime.SessionRef{Name: SessionName},
		Conversation: channel.ConversationRef{ID: defaultConversation},
	})
}

// OpenSession opens a local coder session for distribution launchers.
func (Runtime) OpenSession(ctx context.Context, req distribution.OpenRequest) (clientapi.SessionHandle, error) {
	return OpenSession(ctx, Options{
		Provider: req.Provider,
		Model:    req.Model,
		Debug:    req.Debug,
	})
}

// ToolProjectionConfig returns coder's local tool projection policy.
func ToolProjectionConfig() agentruntime.ToolProjectionConfig {
	return agentruntime.ToolProjectionConfig{
		AllowSideEffects:        true,
		MaxRisk:                 operation.RiskMedium,
		IncludeBareOperations:   true,
		PreferCommandProjection: true,
	}
}

// BundleWithModel returns Bundle with a provider/model override applied.
func BundleWithModel(provider, model string) agentruntime.ResourceBundle {
	bundle := Bundle()
	if model == "" {
		return bundle
	}
	for i := range bundle.Agents {
		if bundle.Agents[i].Name == AgentName {
			bundle.Agents[i].Inference.Model = model
		}
	}
	for i := range bundle.Apps {
		if bundle.Apps[i].Name == AppName {
			bundle.Apps[i].Model.Provider = provider
			bundle.Apps[i].Model.Model = model
		}
	}
	return bundle
}

// ModelSelection is the provider/model pair selected for a coder run.
type ModelSelection struct {
	Provider string
	Model    string
}

// ResolveModelSelection applies coder CLI model/provider defaults.
func ResolveModelSelection(provider, model string) ModelSelection {
	provider = strings.TrimSpace(provider)
	if provider == "" {
		provider = "openai"
	}
	model = strings.TrimSpace(model)
	if before, after, ok := strings.Cut(model, "/"); ok && before != "" && after != "" {
		if knownCLIProvider(before) && provider == "openai" {
			provider = before
			model = after
		}
	}
	if model == "" {
		model = DefaultModel
	}
	return ModelSelection{Provider: provider, Model: model}
}

// NewModel constructs the selected coder model adapter.
func NewModel(selection ModelSelection, debug bool) (llmagent.Model, error) {
	_, modelSpec, found := modelcatalog.Find(selection.Provider, selection.Model)
	pricing := modelSpec.Pricing
	runtime := openaiadapter.DefaultResponsesRuntimeConfig()
	switch selection.Provider {
	case "openai":
		return openaiadapter.New(openaiadapter.Config{
			Model:             selection.Model,
			Runtime:           runtime,
			Pricing:           pricing,
			ParallelToolCalls: true,
			Redactor:          debugRedactor(debug),
		})
	case "codex":
		return codex.New(codex.Config{
			Model:             selection.Model,
			Runtime:           runtime,
			Pricing:           pricing,
			ParallelToolCalls: true,
			Redactor:          debugRedactor(debug),
		})
	case "openrouter":
		if !found {
			return nil, fmt.Errorf("openrouter model %q was not found in modeldb; use an exact OpenRouter model id, for example --model openrouter/anthropic/claude-sonnet-4.6", selection.Model)
		}
		if !modelcatalog.SupportsAPI(modelSpec, "openai-responses") {
			return nil, fmt.Errorf("openrouter model %q does not expose OpenAI Responses in modeldb", selection.Model)
		}
		reasoningEffort, reasoningSummary := OpenRouterReasoningDefaults(modelSpec)
		return openrouter.New(openrouter.Config{
			Model:             selection.Model,
			Pricing:           pricing,
			ReasoningEffort:   reasoningEffort,
			ReasoningSummary:  reasoningSummary,
			ParallelToolCalls: true,
			Redactor:          debugRedactor(debug),
		})
	case "anthropic":
		if err := requireMessagesModel(selection.Provider, selection.Model, modelSpec, found); err != nil {
			return nil, err
		}
		return anthropic.New(anthropic.Config{
			Model:           selection.Model,
			Pricing:         pricing,
			MaxOutputTokens: maxOutputTokens(modelSpec),
			PromptCache:     modelSpec.Capabilities.Has(corellm.CapabilityPromptCaching),
			Redactor:        debugRedactor(debug),
		})
	case "minimax":
		if err := requireMessagesModel(selection.Provider, selection.Model, modelSpec, found); err != nil {
			return nil, err
		}
		return minimax.New(minimax.Config{
			Model:           selection.Model,
			Pricing:         pricing,
			MaxOutputTokens: maxOutputTokens(modelSpec),
			PromptCache:     modelSpec.Capabilities.Has(corellm.CapabilityPromptCaching),
			Redactor:        debugRedactor(debug),
		})
	default:
		return nil, fmt.Errorf("unknown provider %q", selection.Provider)
	}
}

// DebugStreamPolicy returns coder's model stream event policy.
func DebugStreamPolicy(debug bool) llmagent.StreamPolicy {
	return llmagent.StreamPolicy{EmitContent: true, EmitThinking: true, EmitToolCall: debug}
}

// OpenRouterReasoningDefaults selects supported reasoning defaults.
func OpenRouterReasoningDefaults(modelSpec corellm.ModelSpec) (string, string) {
	effort := firstSupportedCSV(modelSpec.Annotations["modeldb.openai_responses.reasoning_efforts"], "minimal", "low", "medium", "high")
	summary := firstSupportedCSV(modelSpec.Annotations["modeldb.openai_responses.reasoning_summaries"], "auto", "concise", "detailed")
	return effort, summary
}

// CommandRisk returns coder's local command-risk classifier.
func CommandRisk(root string) operationruntime.CommandRiskClassifier {
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
	return cmdrisk.New(cmdrisk.Config{
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

func workspaceRoot() (string, error) {
	wd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	return filepath.Abs(wd)
}

func knownCLIProvider(provider string) bool {
	switch provider {
	case "openai", "codex", "openrouter", "anthropic", "minimax":
		return true
	default:
		return false
	}
}

func debugRedactor(debug bool) adapterllm.Redactor {
	if !debug {
		return adapterllm.Redactor{ExposeThinkingSummary: true}
	}
	return adapterllm.Redactor{ExposeThinking: true, ExposeThinkingSummary: true, ExposeToolArgs: true}
}

func requireMessagesModel(provider, model string, modelSpec corellm.ModelSpec, found bool) error {
	if !found {
		return fmt.Errorf("%s model %q was not found in modeldb", provider, model)
	}
	if !modelcatalog.SupportsAPI(modelSpec, "anthropic-messages") {
		return fmt.Errorf("%s model %q does not expose Anthropic Messages in modeldb", provider, model)
	}
	return nil
}

func maxOutputTokens(modelSpec corellm.ModelSpec) int {
	if modelSpec.MaxOutputTokens > 0 && modelSpec.MaxOutputTokens < int64(^uint(0)>>1) {
		return int(modelSpec.MaxOutputTokens)
	}
	return 0
}

func firstSupportedCSV(csv string, preferred ...string) string {
	values := map[string]bool{}
	for _, value := range strings.Split(csv, ",") {
		value = strings.TrimSpace(value)
		if value != "" {
			values[value] = true
		}
	}
	for _, value := range preferred {
		if values[value] {
			return value
		}
	}
	return ""
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
