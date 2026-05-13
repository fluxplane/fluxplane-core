package agentsdk

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	agentruntime "github.com/fluxplane/agentruntime"
	"github.com/fluxplane/agentruntime/adapters/browsercdp"
	"github.com/fluxplane/agentruntime/adapters/distribution/localruntime"
	"github.com/fluxplane/agentruntime/adapters/terminalui"
	"github.com/fluxplane/agentruntime/apps/coder"
	"github.com/fluxplane/agentruntime/core/agent"
	"github.com/fluxplane/agentruntime/core/channel"
	coredatasource "github.com/fluxplane/agentruntime/core/datasource"
	"github.com/fluxplane/agentruntime/core/operation"
	"github.com/fluxplane/agentruntime/core/policy"
	"github.com/fluxplane/agentruntime/core/resource"
	"github.com/fluxplane/agentruntime/orchestration/app"
	clientapi "github.com/fluxplane/agentruntime/orchestration/client"
	"github.com/fluxplane/agentruntime/orchestration/distribution"
	"github.com/fluxplane/agentruntime/orchestration/pluginhost"
	"github.com/fluxplane/agentruntime/plugins/codingplugin"
	"github.com/fluxplane/agentruntime/plugins/datasourceplugin"
	"github.com/fluxplane/agentruntime/plugins/gitlabplugin"
	"github.com/fluxplane/agentruntime/plugins/jiraplugin"
	"github.com/fluxplane/agentruntime/plugins/planexecplugin"
	"github.com/fluxplane/agentruntime/plugins/skillplugin"
	"github.com/fluxplane/agentruntime/plugins/slackplugin"
	"github.com/fluxplane/agentruntime/plugins/textplugin"
	"github.com/fluxplane/agentruntime/plugins/webplugin"
	llmagent "github.com/fluxplane/agentruntime/runtime/agent/llmagent"
	operationruntime "github.com/fluxplane/agentruntime/runtime/operation"
	"github.com/fluxplane/agentruntime/runtime/system"
)

// AttachLocalRuntime gives a filesystem-loaded distribution the concrete
// local session opener used by the agentsdk CLI.
func AttachLocalRuntime(loaded distribution.Loaded) distribution.Loaded {
	if !needsLocalRuntimeOpener(loaded.Distribution.Runtime) {
		return loaded
	}
	loaded.Distribution.Runtime = localruntime.Runtime{
		DefaultSession:      loaded.Distribution.Spec.DefaultSession,
		DefaultConversation: loaded.Distribution.Spec.DefaultConversation,
		Open: func(ctx context.Context, req distribution.OpenRequest) (clientapi.SessionHandle, error) {
			return openLocalSession(ctx, loaded, req)
		},
	}
	return loaded
}

func needsLocalRuntimeOpener(runtime distribution.Runtime) bool {
	if runtime == nil {
		return true
	}
	if local, ok := runtime.(localruntime.Runtime); ok {
		return local.Open == nil
	}
	return false
}

func openLocalSession(ctx context.Context, loaded distribution.Loaded, req distribution.OpenRequest) (clientapi.SessionHandle, error) {
	root := loaded.Root
	if strings.TrimSpace(root) == "" {
		root = "."
	}
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	hostSystem, err := system.NewHost(system.Config{Root: root, AllowPrivateNetwork: true})
	if err != nil {
		return nil, err
	}
	hostSystem.SetClarifier(terminalui.Prompter{In: os.Stdin, Out: os.Stderr})
	browser, err := browsercdp.New(browsercdp.Config{Workspace: hostSystem.Workspace(), Headless: true})
	if err == nil {
		hostSystem.SetBrowser(browser)
	} else if req.Debug {
		_, _ = fmt.Fprintf(os.Stderr, "browser disabled: %v\n", err)
	}

	bundles := cloneBundles(loaded.Distribution.Bundles)
	ensureSkillDatasource(bundles)
	plugins := basePlugins(hostSystem)
	if hasAnyDatasource(bundles) {
		registry, err := datasourceRegistry(ctx, bundles, plugins, root)
		if err != nil {
			return nil, err
		}
		plugins = append(plugins, datasourceplugin.New(registry))
		ensurePluginRef(bundles, datasourceplugin.Name)
	}
	composition, err := app.Compose(app.Config{
		Context: ctx,
		Bundles: bundles,
		Plugins: plugins,
		OperationExecutor: operationruntime.NewExecutor(operationruntime.WithSafetyGate(operationruntime.SafetyEnvelope{
			Sandbox:        localSandbox{Root: root},
			ACL:            localACL{},
			CommandRisk:    coder.CommandRisk(root),
			MaxCommandRisk: operation.RiskMedium,
			AllowPure:      true,
		})),
	})
	if err != nil {
		return nil, err
	}
	service, err := agentruntime.NewFromComposition(composition, agentruntime.Config{
		LLMModelResolver: modelResolver{
			provider:     req.Provider,
			model:        req.Model,
			defaultModel: loaded.Distribution.Spec.DefaultModel.Model,
			debug:        req.Debug,
		},
		LLMStreamPolicy: coder.DebugStreamPolicy(req.Debug),
		ToolProjection: agentruntime.ToolProjectionConfig{
			AllowSideEffects:      true,
			MaxRisk:               operation.RiskMedium,
			IncludeBareOperations: true,
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
		Trust: policy.Trust{Kind: policy.TrustInvocation, Level: policy.TrustVerified},
	})
	if err != nil {
		return nil, err
	}
	return service.Open(ctx, agentruntime.OpenRequest{
		Session:      req.Session,
		Conversation: req.Conversation,
	})
}

type modelResolver struct {
	provider     string
	model        string
	defaultModel string
	debug        bool
}

func (r modelResolver) ResolveModel(_ context.Context, spec agent.Spec) (llmagent.Model, error) {
	selection := coder.ResolveModelSelection(firstNonEmptyString(r.provider, "openai"), firstNonEmptyString(r.model, spec.Inference.Model, r.defaultModel))
	return coder.NewModel(selection, r.debug)
}

func basePlugins(hostSystem system.System) []pluginhost.Plugin {
	dispatcher := slackplugin.NewDispatcher()
	return []pluginhost.Plugin{
		codingplugin.New(hostSystem),
		slackplugin.New(dispatcher),
		gitlabplugin.New(nil, nil),
		jiraplugin.New(nil, nil),
		planexecplugin.New(),
		skillplugin.New(),
		textplugin.New(),
		webplugin.New(hostSystem),
	}
}

func datasourceRegistry(ctx context.Context, bundles []resource.ContributionBundle, plugins []pluginhost.Plugin, root string) (*coredatasource.Registry, error) {
	host, err := pluginhost.New(plugins...)
	if err != nil {
		return nil, err
	}
	resolved, err := host.Resolve(ctx, pluginRefs(bundles)...)
	if err != nil {
		return nil, err
	}
	var providers []coredatasource.Provider
	for _, contribution := range resolved.DatasourceProviders {
		providers = append(providers, contribution.Provider)
	}
	providers = append(providers, datasourceplugin.NewFilesystemProvider(os.DirFS(root)))
	return datasourceplugin.BuildRegistry(ctx, datasourceSpecs(bundles), providers)
}

func ensureSkillDatasource(bundles []resource.ContributionBundle) {
	if !bundleHasPlugin(bundles, skillplugin.Name) || hasDatasource(bundles, skillplugin.DatasourceName) || len(bundles) == 0 {
		return
	}
	bundles[0].Datasources = append(bundles[0].Datasources, skillplugin.DatasourceSpec())
}

func ensurePluginRef(bundles []resource.ContributionBundle, name string) {
	if len(bundles) == 0 || bundleHasPlugin(bundles, name) {
		return
	}
	bundles[0].Plugins = append(bundles[0].Plugins, resource.PluginRef{Name: name})
}

func bundleHasPlugin(bundles []resource.ContributionBundle, name string) bool {
	for _, bundle := range bundles {
		for _, ref := range bundle.Plugins {
			if ref.Name == name {
				return true
			}
		}
	}
	return false
}

func hasAnyDatasource(bundles []resource.ContributionBundle) bool {
	return len(datasourceSpecs(bundles)) > 0
}

func hasDatasource(bundles []resource.ContributionBundle, name coredatasource.Name) bool {
	for _, spec := range datasourceSpecs(bundles) {
		if spec.Name == name {
			return true
		}
	}
	return false
}

func datasourceSpecs(bundles []resource.ContributionBundle) []coredatasource.Spec {
	var out []coredatasource.Spec
	for _, bundle := range bundles {
		out = append(out, bundle.Datasources...)
	}
	return out
}

func pluginRefs(bundles []resource.ContributionBundle) []resource.PluginRef {
	seen := map[string]bool{}
	var out []resource.PluginRef
	for _, bundle := range bundles {
		for _, ref := range bundle.Plugins {
			if ref.Name == datasourceplugin.Name || seen[ref.Name] {
				continue
			}
			seen[ref.Name] = true
			out = append(out, ref)
		}
	}
	return out
}

func cloneBundles(bundles []resource.ContributionBundle) []resource.ContributionBundle {
	out := make([]resource.ContributionBundle, len(bundles))
	copy(out, bundles)
	return out
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
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
