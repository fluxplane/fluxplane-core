package launch

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fluxplane/agentruntime/adapters/appconfig"
	"github.com/fluxplane/agentruntime/adapters/distribution/localruntime"
	embedaxon "github.com/fluxplane/agentruntime/adapters/embed/axon"
	coreagent "github.com/fluxplane/agentruntime/core/agent"
	coreapp "github.com/fluxplane/agentruntime/core/app"
	"github.com/fluxplane/agentruntime/core/channel"
	coredatasource "github.com/fluxplane/agentruntime/core/datasource"
	coredistribution "github.com/fluxplane/agentruntime/core/distribution"
	"github.com/fluxplane/agentruntime/core/event"
	"github.com/fluxplane/agentruntime/core/resource"
	coresession "github.com/fluxplane/agentruntime/core/session"
	corethread "github.com/fluxplane/agentruntime/core/thread"
	clientapi "github.com/fluxplane/agentruntime/orchestration/client"
	"github.com/fluxplane/agentruntime/orchestration/datasourceindex"
	"github.com/fluxplane/agentruntime/orchestration/distribution"
	"github.com/fluxplane/agentruntime/orchestration/eventregistry"
	"github.com/fluxplane/agentruntime/orchestration/pluginhost"
	"github.com/fluxplane/agentruntime/plugins/codingplugin"
	"github.com/fluxplane/agentruntime/plugins/datasourceplugin"
	"github.com/fluxplane/agentruntime/plugins/eventcatalog"
	"github.com/fluxplane/agentruntime/plugins/sessionhistoryplugin"
	"github.com/fluxplane/agentruntime/plugins/slackplugin"
	"github.com/fluxplane/agentruntime/plugins/taskplugin"
	"github.com/fluxplane/agentruntime/plugins/textplugin"
	"github.com/fluxplane/agentruntime/plugins/webplugin"
	"github.com/fluxplane/agentruntime/plugins/workspaceplugin"
	"github.com/fluxplane/agentruntime/runtime/datasource/semantic"
	operationruntime "github.com/fluxplane/agentruntime/runtime/operation"
	runtimesecret "github.com/fluxplane/agentruntime/runtime/secret"
	"github.com/fluxplane/agentruntime/runtime/system"
)

func TestAttachLocalRuntimeConfiguresLocalOpener(t *testing.T) {
	loaded := distribution.Loaded{
		Distribution: distribution.Distribution{
			Spec: coredistribution.Spec{
				DefaultSession:      coresession.Ref{Name: "main"},
				DefaultConversation: channel.ConversationRef{ID: "thread"},
			},
			Runtime: localruntime.Runtime{},
		},
	}

	got := AttachLocalRuntime(loaded)

	runtime, ok := got.Distribution.Runtime.(localruntime.Runtime)
	if !ok {
		t.Fatalf("runtime type = %T, want localruntime.Runtime", got.Distribution.Runtime)
	}
	if runtime.DefaultSession.Name != "main" {
		t.Fatalf("default session = %q, want main", runtime.DefaultSession.Name)
	}
	if runtime.DefaultConversation.ID != "thread" {
		t.Fatalf("default conversation = %q, want thread", runtime.DefaultConversation.ID)
	}
	if runtime.Open == nil {
		t.Fatal("expected local opener")
	}
}

func TestAttachLocalRuntimePreservesConcreteRuntime(t *testing.T) {
	existing := fakeRuntime{}
	loaded := distribution.Loaded{
		Distribution: distribution.Distribution{
			Runtime: existing,
		},
	}

	got := AttachLocalRuntime(loaded)

	if got.Distribution.Runtime != existing {
		t.Fatalf("runtime = %T, want existing fakeRuntime", got.Distribution.Runtime)
	}
}

func TestRunConnectorEngineRejectsUnknownProvider(t *testing.T) {
	_, _, err := launchConnectorEngine(context.Background(), t.TempDir(), map[string]distribution.Connector{
		"unknown": {Kind: "does-not-exist"},
	})
	if err == nil || !strings.Contains(err.Error(), "unknown provider") {
		t.Fatalf("launchConnectorEngine error = %v, want unknown provider", err)
	}
}

func TestLaunchUsesOnlyDeclaredPlugins(t *testing.T) {
	withStateDir(t)
	runtime, err := Launch(context.Background(), RuntimeOptions{
		Root: t.TempDir(),
		Bundles: []resource.ContributionBundle{{
			Plugins: []resource.PluginRef{{Name: textplugin.Name}},
		}},
		AllowPrivateNetwork: true,
	})
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	defer runtime.Close()

	if !hasOperationSpec(runtime, "upper") {
		t.Fatal("expected text plugin operation upper")
	}
	if hasOperationSpec(runtime, "shell_exec") {
		t.Fatal("did not expect coding shell operation without coding plugin ref")
	}
}

func TestLaunchRejectsUndeclaredPluginImplementation(t *testing.T) {
	withStateDir(t)
	_, err := Launch(context.Background(), RuntimeOptions{
		Root: t.TempDir(),
		Bundles: []resource.ContributionBundle{{
			Plugins: []resource.PluginRef{{Name: "missing"}},
		}},
		AllowPrivateNetwork: true,
	})
	if err == nil || !strings.Contains(err.Error(), `plugin "missing" is not available`) {
		t.Fatalf("Launch error = %v, want missing plugin", err)
	}
}

func TestSelectDeclaredPluginsAllowsMultipleInstances(t *testing.T) {
	plugins, err := selectDeclaredPlugins([]resource.ContributionBundle{{
		Plugins: []resource.PluginRef{
			{Name: textplugin.Name, Instance: "company-a"},
			{Name: textplugin.Name, Instance: "company-b"},
		},
	}}, []pluginhost.Plugin{workspaceplugin.New(nil), textplugin.New()})
	if err != nil {
		t.Fatalf("selectDeclaredPlugins: %v", err)
	}
	if len(plugins) != 2 {
		t.Fatalf("plugins len = %d, want workspace plus one text implementation", len(plugins))
	}
	if got := plugins[1].Manifest().Name; got != textplugin.Name {
		t.Fatalf("selected plugin = %q, want %q", got, textplugin.Name)
	}
}

func TestLaunchProvidesCodingOnlyWhenDeclared(t *testing.T) {
	withStateDir(t)
	runtime, err := Launch(context.Background(), RuntimeOptions{
		Root: t.TempDir(),
		Bundles: []resource.ContributionBundle{{
			Plugins: []resource.PluginRef{{Name: codingplugin.Name}},
		}},
		AllowPrivateNetwork: true,
	})
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	defer runtime.Close()

	if !hasOperationSpec(runtime, "shell_exec") {
		t.Fatal("expected coding plugin shell operation")
	}
	if !hasOperationSpec(runtime, "file_read") {
		t.Fatal("expected coding plugin filesystem operation")
	}
}

func TestLaunchYoloEnablesOverMaxCommandRiskApproval(t *testing.T) {
	for _, tt := range []struct {
		name string
		yolo bool
		want bool
	}{
		{name: "default", yolo: false, want: false},
		{name: "yolo", yolo: true, want: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			withStateDir(t)
			runtime, err := Launch(context.Background(), RuntimeOptions{
				Root:                t.TempDir(),
				Yolo:                tt.yolo,
				AllowPrivateNetwork: true,
			})
			if err != nil {
				t.Fatalf("Launch: %v", err)
			}
			defer runtime.Close()

			envelope, ok := runtime.Composition.OperationExecutor.Safety.(operationruntime.SafetyEnvelope)
			if !ok {
				t.Fatalf("safety = %T, want SafetyEnvelope", runtime.Composition.OperationExecutor.Safety)
			}
			if envelope.ApproveOverMaxCommandRisk != tt.want {
				t.Fatalf("ApproveOverMaxCommandRisk = %v, want %v", envelope.ApproveOverMaxCommandRisk, tt.want)
			}
		})
	}
}

func TestLocalUsernameNormalizesHostPrefixes(t *testing.T) {
	cases := map[string]string{
		"DOMAIN\\timo": "timo",
		"host/timo":    "timo",
		"timo@host":    "timo",
		"timo":         "timo",
	}
	for input, want := range cases {
		if got := localUsername(input); got != want {
			t.Fatalf("localUsername(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestLaunchPassesWorkspaceConfigToSystem(t *testing.T) {
	withStateDir(t)
	tmp := t.TempDir()
	runtime, err := Launch(context.Background(), RuntimeOptions{
		Root: t.TempDir(),
		Launch: distribution.LaunchConfig{
			Workspace: distribution.WorkspaceConfig{
				Roots: []distribution.WorkspaceRoot{{
					Name:   "tmp",
					Path:   tmp,
					Access: "read_write",
				}},
				ScratchRoot: "tmp",
			},
		},
		AllowPrivateNetwork: true,
	})
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	defer runtime.Close()

	resolved, err := runtime.System.Workspace().WriteFile(context.Background(), "@tmp/out.txt", []byte("x"), 0644, false)
	if err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if resolved.Rel != "@tmp/out.txt" {
		t.Fatalf("resolved = %#v, want @tmp/out.txt", resolved)
	}
}

func TestLaunchDevWiresSessionHistoryDatasource(t *testing.T) {
	withStateDir(t)
	runtime, err := Launch(context.Background(), RuntimeOptions{
		Root: t.TempDir(),
		Bundles: []resource.ContributionBundle{{
			Agents: []coreagent.Spec{{Name: "main"}},
		}},
		Dev:                 true,
		AllowPrivateNetwork: true,
	})
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	defer runtime.Close()

	if !hasDatasourceSpec(runtime, string(sessionhistoryplugin.DatasourceName)) {
		t.Fatal("expected session history datasource")
	}
	if !hasOperationSpec(runtime, datasourceplugin.SearchOperation) {
		t.Fatal("expected datasource search operation")
	}
}

func TestLaunchOpensCoderWebSearchDatasourceThroughCodingPlugin(t *testing.T) {
	withStateDir(t)
	ctx := context.Background()
	root := t.TempDir()
	bundles := []resource.ContributionBundle{{
		Plugins: []resource.PluginRef{{Name: codingplugin.Name}},
		Datasources: []coredatasource.Spec{{
			Name:        "web_search",
			Description: "Default public web search datasource.",
			Kind:        "web_search",
			Entities:    []coredatasource.EntityType{webplugin.SearchResultEntity},
		}},
	}}
	sys, err := system.NewHost(system.Config{Root: root, AllowPrivateNetwork: true})
	if err != nil {
		t.Fatalf("NewHost: %v", err)
	}
	plugins := []pluginhost.Plugin{codingplugin.New(sys)}

	registry, err := datasourceRegistry(ctx, bundles, plugins, root)
	if err != nil {
		t.Fatalf("datasourceRegistry: %v", err)
	}
	accessor, ok := registry.Get(coredatasource.Name("web_search"))
	if !ok {
		t.Fatal("expected web_search datasource accessor")
	}
	if got := accessor.Spec().Kind; got != "web_search" {
		t.Fatalf("kind = %q, want web_search", got)
	}
	entities := accessor.Entities()
	if len(entities) != 1 || entities[0].Type != webplugin.SearchResultEntity {
		t.Fatalf("entities = %#v, want %s", entities, webplugin.SearchResultEntity)
	}
	if _, ok := accessor.(coredatasource.Searcher); !ok {
		t.Fatalf("accessor = %T, want datasource searcher", accessor)
	}
}

func TestDatasourceRegistryOpensNativeGitLabDatasource(t *testing.T) {
	withStateDir(t)
	ctx := context.Background()
	root := filepath.Join("..", "..", "examples", "slack-bot")
	file, err := appconfig.LoadDirFile(ctx, root)
	if err != nil {
		t.Fatalf("LoadDirFile: %v", err)
	}
	sys, err := system.NewHost(system.Config{Root: root, AllowPrivateNetwork: true})
	if err != nil {
		t.Fatalf("NewHost: %v", err)
	}
	if !bundleHasPlugin([]resource.ContributionBundle{file.Bundle}, "gitlab") {
		t.Fatalf("decoded plugins = %#v, want gitlab", file.Bundle.Plugins)
	}
	plugins := availablePlugins(sys, nil, nil, "")
	host, err := pluginhost.New(plugins...)
	if err != nil {
		t.Fatalf("pluginhost.New: %v", err)
	}
	resolved, err := host.Resolve(ctx, file.Bundle.Plugins...)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	var hasGitLabProvider bool
	for _, contribution := range resolved.DatasourceProviders {
		for _, entity := range contribution.Provider.Entities() {
			if entity.Type == "gitlab.project" {
				hasGitLabProvider = true
			}
		}
	}
	if !hasGitLabProvider {
		t.Fatalf("datasource providers = %#v, want gitlab.project provider", resolved.DatasourceProviders)
	}
	bundle := file.Bundle
	bundle.Datasources = []coredatasource.Spec{{
		Name:     "gitlab",
		Kind:     "gitlab",
		Entities: []coredatasource.EntityType{"gitlab.project"},
		Config:   map[string]string{"instance": "main"},
	}}
	registry, err := datasourceRegistry(ctx, []resource.ContributionBundle{bundle}, plugins, root)
	if err != nil {
		t.Fatalf("datasourceRegistry: %v", err)
	}
	accessor, ok := registry.Get(coredatasource.Name("gitlab"))
	if !ok {
		t.Fatal("expected gitlab datasource accessor")
	}
	entities := accessor.Entities()
	if len(entities) != 1 || entities[0].Type != "gitlab.project" {
		t.Fatalf("entities = %#v, want gitlab.project", entities)
	}
}

func TestLaunchSlackDatasourceUsesRuntimeAuthPath(t *testing.T) {
	withStateDir(t)
	ctx := context.Background()
	authPath := t.TempDir()
	ref := resource.PluginRef{Name: slackplugin.Name, Instance: "slack-bot"}
	saveSlackBotToken(t, authPath, ref)
	bundle := slackDatasourceBundle(ref)

	runtime, err := Launch(ctx, RuntimeOptions{
		Root:                t.TempDir(),
		Bundles:             []resource.ContributionBundle{bundle},
		AuthPath:            authPath,
		AllowPrivateNetwork: true,
	})
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	defer runtime.Close()

	registry, err := datasourceplugin.BuildRegistry(ctx, bundle.Datasources, runtime.Composition.DatasourceProviders)
	if err != nil {
		t.Fatalf("BuildRegistry: %v", err)
	}
	assertSlackDatasourceLoadedToken(t, registry)
}

func TestDatasourceIndexRuntimeSlackDatasourceUsesAuthPath(t *testing.T) {
	withStateDir(t)
	ctx := context.Background()
	authPath := t.TempDir()
	ref := resource.PluginRef{Name: slackplugin.Name, Instance: "slack-bot"}
	saveSlackBotToken(t, authPath, ref)

	runtime, err := NewDatasourceIndexRuntime(ctx, DatasourceIndexOptions{
		Root:     t.TempDir(),
		Bundles:  []resource.ContributionBundle{slackDatasourceBundle(ref)},
		AuthPath: authPath,
		Provider: "hash",
	})
	if err != nil {
		t.Fatalf("NewDatasourceIndexRuntime: %v", err)
	}
	defer func() { _ = runtime.Close() }()

	assertSlackDatasourceLoadedToken(t, runtime.Registry)
}

func TestLaunchDevWiresSessionHistoryIntoPluginAgents(t *testing.T) {
	withStateDir(t)
	runtime, err := Launch(context.Background(), RuntimeOptions{
		Root: t.TempDir(),
		Bundles: []resource.ContributionBundle{{
			Agents:  []coreagent.Spec{{Name: "main"}},
			Plugins: []resource.PluginRef{{Name: taskplugin.Name}},
		}},
		Dev:                 true,
		AllowPrivateNetwork: true,
	})
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	defer runtime.Close()

	for _, name := range []string{taskplugin.WorkerAgent, taskplugin.ExplorerAgent, taskplugin.ReviewerAgent} {
		spec, ok := agentSpec(runtime, name)
		if !ok {
			t.Fatalf("expected plugin agent %q", name)
		}
		if !agentHasOperation(spec, datasourceplugin.SearchOperation) ||
			!agentHasOperation(spec, datasourceplugin.GetOperation) ||
			!agentHasOperation(spec, datasourceplugin.BatchGetOperation) {
			t.Fatalf("expected datasource operations on agent %q: %#v", name, spec.Operations)
		}
		if !agentHasDatasource(spec, string(sessionhistoryplugin.DatasourceName)) {
			t.Fatalf("expected session history datasource on agent %q: %#v", name, spec.Datasources)
		}
		if !agentHasContext(spec, datasourceplugin.ContextProvider) {
			t.Fatalf("expected datasource catalog context on agent %q: %#v", name, spec.Context)
		}
	}
}

func TestDatasourceIndexDevIncludesSessionHistoryCorpus(t *testing.T) {
	withStateDir(t)
	ctx := context.Background()
	registry, err := eventregistry.New(eventregistry.Config{EventTypes: eventcatalog.All()})
	if err != nil {
		t.Fatalf("NewEventRegistry: %v", err)
	}
	store, _, closeStore, err := openLocalThreadStore(registry)
	if err != nil {
		t.Fatalf("openLocalThreadStore: %v", err)
	}
	snapshot, err := store.Create(ctx, corethread.CreateParams{ID: "thread_index_dev"})
	if err != nil {
		closeStore()
		t.Fatalf("Create: %v", err)
	}
	_, err = store.Append(ctx, corethread.Ref{ID: snapshot.ID, BranchID: snapshot.BranchID}, corethread.AppendRecord{Event: event.Record{
		Name: coresession.EventInputReceived,
		Payload: coresession.InputReceived{
			Message: channel.Message{Content: "index dev session history marker"},
		},
	}})
	closeStore()
	if err != nil {
		t.Fatalf("Append: %v", err)
	}

	runtime, err := NewDatasourceIndexRuntime(ctx, DatasourceIndexOptions{
		Root:     t.TempDir(),
		Provider: "hash",
		Dev:      true,
	})
	if err != nil {
		t.Fatalf("NewDatasourceIndexRuntime: %v", err)
	}
	defer func() { _ = runtime.Close() }()

	result, err := datasourceindex.Build(ctx, datasourceindex.Request{
		Registry:   runtime.Registry,
		Index:      runtime.Index,
		Datasource: coredatasource.Name("session_history"),
		Entity:     coredatasource.EntityType("session.message"),
		DryRun:     true,
	})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if result.Documents == 0 {
		t.Fatalf("documents = 0, want session history corpus documents")
	}
}

func TestDatasourceIndexWarmupBuildsIndexedDatasources(t *testing.T) {
	ctx := context.Background()
	accessor := warmupCorpusAccessor{
		spec: coredatasource.Spec{
			Name:     "docs",
			Kind:     "memory",
			Entities: []coredatasource.EntityType{"file.document"},
			Index:    coredatasource.IndexSpec{Enabled: true},
		},
		entity: coredatasource.EntitySpec{
			Type:         "file.document",
			Capabilities: []coredatasource.EntityCapability{coredatasource.EntityCapabilitySemanticSearch},
		},
		docs: []coredatasource.CorpusDocument{{
			Ref:   coredatasource.RecordRef{Datasource: "docs", Entity: "file.document", ID: "runbook.md"},
			Title: "Runbook",
			Body:  "warmup indexed document",
		}},
	}
	registry, err := coredatasource.NewRegistry([]coredatasource.Accessor{accessor}, nil)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	index, err := semantic.New(semantic.HashEmbedder{}, semantic.NewJSONStore(""), semantic.Config{})
	if err != nil {
		t.Fatalf("semantic.New: %v", err)
	}
	done := startDatasourceIndexWarmup(ctx, registry, index, coreapp.DatasourceIndexSpec{})
	warmup := <-done
	if warmup.Err != nil {
		t.Fatalf("warmup error: %v", warmup.Err)
	}
	if warmup.Result.Queued != 1 {
		t.Fatalf("warmup result = %#v, want one queued", warmup.Result)
	}
	status, err := index.Status(ctx, semantic.StatusRequest{Datasource: "docs", Entity: "file.document"})
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if len(status.Queue) != 1 {
		t.Fatalf("queue = %#v, want one queued semantic job", status.Queue)
	}
	processed, err := index.ProcessQueue(ctx, semantic.ProcessQueueRequest{Datasource: "docs", Entity: "file.document"})
	if err != nil {
		t.Fatalf("ProcessQueue: %v", err)
	}
	if processed.Embedded != 1 {
		t.Fatalf("processed = %#v, want one embedded document", processed)
	}
}

func hasOperationSpec(runtime Runtime, name string) bool {
	for _, spec := range runtime.Composition.OperationSpecs {
		if string(spec.Ref.Name) == name {
			return true
		}
	}
	return false
}

func hasDatasourceSpec(runtime Runtime, name string) bool {
	for _, spec := range runtime.Composition.DatasourceSpecs {
		if string(spec.Name) == name {
			return true
		}
	}
	return false
}

func saveSlackBotToken(t *testing.T, authPath string, ref resource.PluginRef) {
	t.Helper()
	store := runtimesecret.NewFileStore(authPath)
	if err := store.SaveSecret(context.Background(), runtimesecret.StoredSecret{
		Ref:   slackplugin.BotTokenSecretRef(ref),
		Value: "xoxb-test",
	}); err != nil {
		t.Fatalf("SaveSecret: %v", err)
	}
}

func slackDatasourceBundle(ref resource.PluginRef) resource.ContributionBundle {
	return resource.ContributionBundle{
		Plugins: []resource.PluginRef{ref},
		Datasources: []coredatasource.Spec{{
			Name:     "slack-bot",
			Kind:     slackplugin.Name,
			Entities: []coredatasource.EntityType{slackplugin.ChannelEntity},
			Config:   map[string]string{"instance": ref.InstanceName()},
		}},
	}
}

func assertSlackDatasourceLoadedToken(t *testing.T, registry *coredatasource.Registry) {
	t.Helper()
	accessor, ok := registry.Get(coredatasource.Name("slack-bot"))
	if !ok {
		t.Fatal("expected slack datasource accessor")
	}
	lister, ok := accessor.(coredatasource.Lister)
	if !ok {
		t.Fatalf("accessor = %T, want lister", accessor)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := lister.List(canceled, coredatasource.ListRequest{Entity: slackplugin.ChannelEntity, Limit: 1})
	if err == nil {
		t.Fatal("List error is nil, want canceled request")
	}
	if strings.Contains(err.Error(), "bot token is not configured") {
		t.Fatalf("List error = %v, want token loaded from configured auth path", err)
	}
}

func agentSpec(runtime Runtime, name string) (coreagent.Spec, bool) {
	for _, spec := range runtime.Composition.AgentSpecs {
		if string(spec.Name) == name {
			return spec, true
		}
	}
	return coreagent.Spec{}, false
}

func agentHasOperation(spec coreagent.Spec, name string) bool {
	for _, ref := range spec.Operations {
		if string(ref.Name) == name {
			return true
		}
	}
	return false
}

func agentHasDatasource(spec coreagent.Spec, name string) bool {
	for _, ref := range spec.Datasources {
		if string(ref.Name) == name {
			return true
		}
	}
	return false
}

func agentHasContext(spec coreagent.Spec, name string) bool {
	for _, ref := range spec.Context {
		if string(ref.Name) == name {
			return true
		}
	}
	return false
}

func withStateDir(t *testing.T) {
	t.Helper()
	t.Setenv("XDG_STATE_HOME", t.TempDir())
}

func TestSemanticEmbedderDefaultsToAxon(t *testing.T) {
	embedder, model, err := semanticEmbedder("", "")
	if err != nil {
		t.Fatalf("semanticEmbedder: %v", err)
	}
	defer func() {
		if closer, ok := embedder.(interface{ Close() error }); ok {
			_ = closer.Close()
		}
	}()
	if !strings.HasPrefix(model, embedaxon.ProviderName+"/") {
		t.Fatalf("model = %q, want axon provider prefix", model)
	}
}

func TestSemanticEmbedderSupportsExplicitHashProvider(t *testing.T) {
	_, model, err := semanticEmbedder("hash", "")
	if err != nil {
		t.Fatalf("semanticEmbedder hash: %v", err)
	}
	if model != "local/hash-embedding" {
		t.Fatalf("model = %q, want local/hash-embedding", model)
	}
}

type fakeRuntime struct{}

func (fakeRuntime) OpenSession(context.Context, distribution.OpenRequest) (clientapi.SessionHandle, error) {
	return nil, nil
}

type warmupCorpusAccessor struct {
	spec   coredatasource.Spec
	entity coredatasource.EntitySpec
	docs   []coredatasource.CorpusDocument
}

func (a warmupCorpusAccessor) Spec() coredatasource.Spec { return a.spec }
func (a warmupCorpusAccessor) Entities() []coredatasource.EntitySpec {
	return []coredatasource.EntitySpec{a.entity}
}
func (a warmupCorpusAccessor) Corpus(context.Context, coredatasource.CorpusRequest) (coredatasource.CorpusPage, error) {
	return coredatasource.CorpusPage{Documents: a.docs, Complete: true}, nil
}
