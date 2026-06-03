package launch

import (
	"context"
	"strings"
	"testing"

	fluxplane "github.com/fluxplane/fluxplane-core"
	"github.com/fluxplane/fluxplane-core/adapters/distribution/localruntime"
	embedaxon "github.com/fluxplane/fluxplane-core/adapters/embeddings/axon"
	"github.com/fluxplane/fluxplane-core/contrib/datasource"
	"github.com/fluxplane/fluxplane-core/contrib/eventcatalog"
	"github.com/fluxplane/fluxplane-core/contrib/memory"
	"github.com/fluxplane/fluxplane-core/contrib/sessionhistory"
	"github.com/fluxplane/fluxplane-core/contrib/task"
	"github.com/fluxplane/fluxplane-core/contrib/text"
	usageplugin "github.com/fluxplane/fluxplane-core/contrib/usage"
	"github.com/fluxplane/fluxplane-core/contrib/workspace"
	coreagent "github.com/fluxplane/fluxplane-core/core/agent"
	coreapp "github.com/fluxplane/fluxplane-core/core/app"
	"github.com/fluxplane/fluxplane-core/core/channel"
	coredistribution "github.com/fluxplane/fluxplane-core/core/distribution"
	"github.com/fluxplane/fluxplane-core/core/resource"
	coresession "github.com/fluxplane/fluxplane-core/core/session"
	corethread "github.com/fluxplane/fluxplane-core/core/thread"
	clientapi "github.com/fluxplane/fluxplane-core/orchestration/client"
	"github.com/fluxplane/fluxplane-core/orchestration/contributions"
	"github.com/fluxplane/fluxplane-core/orchestration/datasourceindex"
	"github.com/fluxplane/fluxplane-core/orchestration/distribution"
	"github.com/fluxplane/fluxplane-core/orchestration/eventregistry"
	"github.com/fluxplane/fluxplane-core/orchestration/pluginbridge"
	"github.com/fluxplane/fluxplane-core/runtime/datasource/semantic"
	operationruntime "github.com/fluxplane/fluxplane-core/runtime/operation"
	runtimeworkspace "github.com/fluxplane/fluxplane-core/runtime/workspace"
	coredatasource "github.com/fluxplane/fluxplane-datasource"
	"github.com/fluxplane/fluxplane-event"
	"github.com/fluxplane/fluxplane-operation"
	"github.com/fluxplane/fluxplane-plugin/management"
	sdkmanifest "github.com/fluxplane/fluxplane-plugin/manifest"
	"github.com/fluxplane/fluxplane-plugin/pluginbinding"
	"github.com/fluxplane/fluxplane-plugin/pluginruntime"
	sharedsecret "github.com/fluxplane/fluxplane-secret"
	fpsystem "github.com/fluxplane/fluxplane-system"
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

func TestMergeToolProjectionMaxRiskPreservesDefaults(t *testing.T) {
	cfg := mergeToolProjectionMaxRisk(fluxplane.ToolProjectionConfig{}, operation.RiskHigh)
	if cfg.MaxRisk != operation.RiskHigh {
		t.Fatalf("max risk = %q, want high", cfg.MaxRisk)
	}
	if !cfg.AllowSideEffects || !cfg.AllowApprovalRequired || !cfg.IncludeBareOperations || !cfg.PreferCommandProjection {
		t.Fatalf("projection defaults not preserved: %#v", cfg)
	}
}

func TestFirstToolProjectionTreatsOnlyMaxRiskAsDefaultCap(t *testing.T) {
	cfg := firstToolProjection(fluxplane.ToolProjectionConfig{MaxRisk: operation.RiskMedium}, defaultToolProjection())
	if cfg.MaxRisk != operation.RiskMedium {
		t.Fatalf("max risk = %q, want medium", cfg.MaxRisk)
	}
	if !cfg.AllowSideEffects || !cfg.AllowApprovalRequired || !cfg.IncludeBareOperations || !cfg.PreferCommandProjection {
		t.Fatalf("projection defaults not preserved: %#v", cfg)
	}
}

func TestMergeNamedPluginInstancesIntersectsCallerRestrictions(t *testing.T) {
	got := mergeNamedPluginInstances(
		map[string]map[string]bool{"gitlab": {"staging": true, "prod": false}},
		map[string]map[string]bool{"gitlab": {"staging": true, "prod": true, "dev": true}},
	)
	if !got["gitlab"]["staging"] {
		t.Fatalf("staging = false, want true: %#v", got)
	}
	if got["gitlab"]["prod"] {
		t.Fatalf("prod = true, want false: %#v", got)
	}
	if got["gitlab"]["dev"] {
		t.Fatalf("dev = true, want absent/false: %#v", got)
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

func TestMergeInstalledPluginsAddsNonCollidingPluginRefs(t *testing.T) {
	existing := []contributions.Provider{staticContributionPlugin{name: "builtin"}}
	bundles := []resource.ContributionBundle{{Plugins: []resource.PluginRef{{Name: "builtin"}}}}
	installed := pluginbridge.InstalledLoadResult{
		Plugins: []contributions.Provider{
			staticContributionPlugin{name: "installed"},
			staticContributionPlugin{name: "builtin"},
		},
		Refs: []resource.PluginRef{
			{Name: "installed", Instance: "default"},
			{Name: "builtin", Instance: "default"},
		},
	}

	available, mergedBundles := mergeInstalledPlugins(existing, bundles, installed)

	if len(available) != 2 {
		t.Fatalf("available len = %d, want 2", len(available))
	}
	if available[1].Manifest().Name != "installed" {
		t.Fatalf("installed plugin = %#v", available[1].Manifest())
	}
	if len(mergedBundles) != 2 {
		t.Fatalf("bundles len = %d, want 2", len(mergedBundles))
	}
	if refs := mergedBundles[1].Plugins; len(refs) != 1 || refs[0].Key() != "installed/default" {
		t.Fatalf("installed refs = %#v", refs)
	}
}

func TestLaunchUsesOnlyDeclaredPlugins(t *testing.T) {
	withStateDir(t)
	runtime, err := Launch(context.Background(), RuntimeOptions{
		Root: t.TempDir(),
		Bundles: []resource.ContributionBundle{{
			Plugins: []resource.PluginRef{{Name: text.Name}},
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

func TestLaunchCanLoadInstalledPluginsFromInjectedState(t *testing.T) {
	withStateDir(t)
	runtime, err := Launch(context.Background(), RuntimeOptions{
		Root:                   t.TempDir(),
		AllowPrivateNetwork:    true,
		EnableInstalledPlugins: true,
		InstalledPluginStore: fakeInstalledPluginStore{
			plugins: []management.Plugin{{
				Ref:       management.Ref{Name: "installed_echo"},
				Installed: true,
				Enabled:   true,
				Runtime:   management.RuntimeSpec{Kind: "direct"},
			}},
			instances: []management.Instance{{
				Plugin:  management.Ref{Name: "installed_echo"},
				Name:    management.DefaultInstance,
				Enabled: true,
			}},
		},
		InstalledRuntime: func(plugin management.Plugin) (pluginruntime.Plugin, error) {
			return pluginruntime.Direct(installedEchoPlugin()), nil
		},
	})
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	defer runtime.Close()

	if !hasOperationSpec(runtime, "installed_echo.say") {
		t.Fatal("expected installed plugin operation installed_echo.say")
	}
	op, ok := runtime.Composition.Operations.Resolve(operation.Ref{Name: "installed_echo.say"})
	if !ok {
		t.Fatal("installed_echo.say operation not executable")
	}
	result := op.Run(operation.NewContext(context.Background(), nil), map[string]any{"text": "core"})
	if result.Status != operation.StatusOK {
		t.Fatalf("operation result = %#v", result)
	}
	output, ok := result.Output.(map[string]any)
	if !ok || output["text"] != "core installed" {
		t.Fatalf("operation output = %#v", result.Output)
	}
}

func TestLaunchMemoryOnlyPluginGetsStores(t *testing.T) {
	withStateDir(t)
	runtime, err := Launch(context.Background(), RuntimeOptions{
		Root: t.TempDir(),
		Bundles: []resource.ContributionBundle{{
			Plugins: []resource.PluginRef{{Name: memory.Name}},
		}},
		AllowPrivateNetwork: true,
	})
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	defer runtime.Close()

	if !hasOperationSpec(runtime, memory.MemorizeOp) {
		t.Fatalf("expected memory operation %s", memory.MemorizeOp)
	}
	if !hasOperationSpec(runtime, datasource.SearchOperation) {
		t.Fatal("expected datasource plugin for memory-only launch")
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

func TestLaunchRejectsUsagePluginOutsideDev(t *testing.T) {
	withStateDir(t)
	_, err := Launch(context.Background(), RuntimeOptions{
		Root: t.TempDir(),
		Bundles: []resource.ContributionBundle{{
			Plugins: []resource.PluginRef{{Name: usageplugin.Name}},
		}},
		AllowPrivateNetwork: true,
	})
	if err == nil || !strings.Contains(err.Error(), `plugin "usage" is not available`) {
		t.Fatalf("Launch error = %v, want usage plugin unavailable outside dev", err)
	}
}

func TestSelectDeclaredPluginsAllowsMultipleInstances(t *testing.T) {
	plugins, err := selectDeclaredPlugins([]resource.ContributionBundle{{
		Plugins: []resource.PluginRef{
			{Name: text.Name, Instance: "company-a"},
			{Name: text.Name, Instance: "company-b"},
		},
	}}, []contributions.Provider{workspace.New(nil), text.New()})
	if err != nil {
		t.Fatalf("selectDeclaredPlugins: %v", err)
	}
	if len(plugins) != 2 {
		t.Fatalf("plugins len = %d, want workspace plus one text implementation", len(plugins))
	}
	if got := plugins[1].Manifest().Name; got != text.Name {
		t.Fatalf("selected plugin = %q, want %q", got, text.Name)
	}
}

func TestLaunchUsesInjectedProductPluginWhenDeclared(t *testing.T) {
	withStateDir(t)
	const (
		pluginName = "product_coding"
		opName     = "product_coding_operation"
	)
	runtime, err := Launch(context.Background(), RuntimeOptions{
		Root: t.TempDir(),
		Bundles: []resource.ContributionBundle{{
			Plugins: []resource.PluginRef{{Name: pluginName}},
		}},
		PluginFactory: func(PluginFactoryContext) []contributions.Provider {
			return []contributions.Provider{
				workspace.New(nil),
				staticContributionPlugin{
					name: pluginName,
					bundle: resource.ContributionBundle{
						Operations: []operation.Spec{{
							Ref:         operation.Ref{Name: opName},
							Description: "Injected product operation.",
						}},
					},
				},
			}
		},
		AllowPrivateNetwork: true,
	})
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	defer runtime.Close()

	if !hasOperationSpec(runtime, opName) {
		t.Fatalf("expected injected product operation %q", opName)
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

	resolved, err := runtime.Workspace.ResolveCreate(context.Background(), "@tmp/out.txt")
	if err != nil {
		t.Fatalf("ResolveCreate: %v", err)
	}
	fsys, err := runtimeworkspace.FileSystem(runtime.Workspace)
	if err != nil {
		t.Fatalf("FileSystem: %v", err)
	}
	if err := fsys.WriteFile(context.Background(), runtimeworkspace.PathName(resolved), []byte("x"), fpsystem.WriteFileOptions{Perm: 0644}); err != nil {
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

	if !hasDatasourceSpec(runtime, string(sessionhistory.DatasourceName)) {
		t.Fatal("expected session history datasource")
	}
	if !hasDatasourceSpec(runtime, string(usageplugin.DatasourceName)) {
		t.Fatal("expected usage datasource")
	}
	if !hasOperationSpec(runtime, datasource.SearchOperation) {
		t.Fatal("expected datasource search operation")
	}
}

func TestLaunchOpensDatasourceThroughInjectedProductPlugin(t *testing.T) {
	withStateDir(t)
	ctx := context.Background()
	root := t.TempDir()
	const (
		pluginName = "product_search"
		sourceName = "product_search"
		entityType = coredatasource.EntityType("product.search_result")
	)
	bundles := []resource.ContributionBundle{{
		Plugins: []resource.PluginRef{{Name: pluginName}},
		Datasources: []coredatasource.Spec{{
			Name:        sourceName,
			Description: "Injected product search datasource.",
			Kind:        sourceName,
			Entities:    []coredatasource.EntityType{entityType},
		}},
	}}
	plugins := []contributions.Provider{staticDatasourcePlugin{
		name: pluginName,
		provider: staticDatasourceProvider{entity: coredatasource.EntitySpec{
			Type:         entityType,
			Capabilities: []coredatasource.EntityCapability{coredatasource.EntityCapabilitySearch},
		}},
	}}

	registry, err := datasourceRegistry(ctx, bundles, plugins, root)
	if err != nil {
		t.Fatalf("datasourceRegistry: %v", err)
	}
	accessor, ok := registry.Get(coredatasource.Name(sourceName))
	if !ok {
		t.Fatal("expected product_search datasource accessor")
	}
	if got := accessor.Spec().Kind; got != sourceName {
		t.Fatalf("kind = %q, want %q", got, sourceName)
	}
	entities := accessor.Entities()
	if len(entities) != 1 || entities[0].Type != entityType {
		t.Fatalf("entities = %#v, want %s", entities, entityType)
	}
	if _, ok := accessor.(coredatasource.Searcher); !ok {
		t.Fatalf("accessor = %T, want datasource searcher", accessor)
	}
}

func TestDatasourceIndexRuntimePassesProcessAuthEnvToPluginFactory(t *testing.T) {
	withStateDir(t)
	ctx := context.Background()
	t.Setenv("DATASOURCE_INDEX_TEST_SECRET", "from-process")
	var resolved bool

	runtime, err := NewDatasourceIndexRuntime(ctx, DatasourceIndexOptions{
		Root:               t.TempDir(),
		Provider:           "hash",
		AllowPluginAuthEnv: true,
		PluginFactory: func(factoryCtx PluginFactoryContext) []contributions.Provider {
			material, ok, err := factoryCtx.NativeAuthResolver.ResolveSecret(ctx, sharedsecret.Ref{
				Scheme: sharedsecret.SchemeEnv,
				Slot:   "DATASOURCE_INDEX_TEST_SECRET",
			})
			if err != nil {
				t.Fatalf("ResolveSecret: %v", err)
			}
			resolved = ok && material.String() == "from-process"
			return nil
		},
	})
	if err != nil {
		t.Fatalf("NewDatasourceIndexRuntime: %v", err)
	}
	defer func() { _ = runtime.Close() }()

	if !resolved {
		t.Fatal("expected datasource index plugin factory to receive process auth env resolver")
	}
}

func TestLaunchDevWiresSessionHistoryIntoPluginAgents(t *testing.T) {
	withStateDir(t)
	runtime, err := Launch(context.Background(), RuntimeOptions{
		Root: t.TempDir(),
		Bundles: []resource.ContributionBundle{{
			Agents:  []coreagent.Spec{{Name: "main"}},
			Plugins: []resource.PluginRef{{Name: task.Name}},
		}},
		Dev:                 true,
		AllowPrivateNetwork: true,
	})
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	defer runtime.Close()

	for _, name := range []string{task.WorkerAgent, task.ExplorerAgent, task.ReviewerAgent} {
		spec, ok := agentSpec(runtime, name)
		if !ok {
			t.Fatalf("expected plugin agent %q", name)
		}
		if !agentHasOperation(spec, datasource.SearchOperation) ||
			!agentHasOperation(spec, datasource.GetOperation) ||
			!agentHasOperation(spec, datasource.BatchGetOperation) {
			t.Fatalf("expected datasource operations on agent %q: %#v", name, spec.Operations)
		}
		if !agentHasDatasource(spec, string(sessionhistory.DatasourceName)) {
			t.Fatalf("expected session history datasource on agent %q: %#v", name, spec.Datasources)
		}
		if !agentHasDatasource(spec, string(usageplugin.DatasourceName)) {
			t.Fatalf("expected usage datasource on agent %q: %#v", name, spec.Datasources)
		}
		if !agentHasContext(spec, datasource.ContextProvider) {
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
	done := startDatasourceIndexWarmup(ctx, registry, index, nil, nil, coreapp.DatasourceIndexSpec{}, false)
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

func TestClearSemanticIndexStatusDeletesFieldRecordsAndQueue(t *testing.T) {
	ctx := context.Background()
	index, err := semantic.New(semantic.HashEmbedder{}, semantic.NewJSONStore(""), semantic.Config{})
	if err != nil {
		t.Fatalf("semantic.New: %v", err)
	}
	entity := coredatasource.EntitySpec{
		Type:         "file.document",
		Capabilities: []coredatasource.EntityCapability{coredatasource.EntityCapabilityIndex, coredatasource.EntityCapabilitySemanticSearch},
	}
	fieldDoc := coredatasource.CorpusDocument{
		Ref:   coredatasource.RecordRef{Datasource: "docs", Entity: "file.document", ID: "field-only.md"},
		Title: "Field only",
	}
	queueDoc := coredatasource.CorpusDocument{
		Ref:   coredatasource.RecordRef{Datasource: "docs", Entity: "file.document", ID: "queued-only.md"},
		Title: "Queued only",
		Body:  "queued document",
	}
	if _, err := index.UpdateRecord(ctx, fieldDoc, entity); err != nil {
		t.Fatalf("UpdateRecord: %v", err)
	}
	if _, err := index.Enqueue(ctx, queueDoc); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	status, err := index.Status(ctx, semantic.StatusRequest{Datasource: "docs", Entity: "file.document"})
	if err != nil {
		t.Fatalf("Status before clear: %v", err)
	}
	if len(status.Records) != 1 || len(status.Queue) != 1 || len(status.Documents) != 0 {
		t.Fatalf("status before clear = %#v, want field record and queue only", status)
	}
	deleted, err := clearSemanticIndexStatus(ctx, index, status)
	if err != nil {
		t.Fatalf("clearSemanticIndexStatus: %v", err)
	}
	if deleted != 2 {
		t.Fatalf("deleted = %d, want 2", deleted)
	}
	status, err = index.Status(ctx, semantic.StatusRequest{Datasource: "docs", Entity: "file.document"})
	if err != nil {
		t.Fatalf("Status after clear: %v", err)
	}
	if len(status.Records) != 0 || len(status.Queue) != 0 || len(status.Documents) != 0 {
		t.Fatalf("status after clear = %#v, want empty", status)
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

type fakeInstalledPluginStore struct {
	plugins   []management.Plugin
	instances []management.Instance
}

func (s fakeInstalledPluginStore) ListPlugins(context.Context, management.ListRequest) ([]management.Plugin, error) {
	return append([]management.Plugin(nil), s.plugins...), nil
}

func (s fakeInstalledPluginStore) PluginStatus(context.Context, management.StatusRequest) (management.StatusResult, error) {
	return management.StatusResult{
		Plugins:   append([]management.Plugin(nil), s.plugins...),
		Instances: append([]management.Instance(nil), s.instances...),
	}, nil
}

func (s fakeInstalledPluginStore) SetPluginEnabled(context.Context, management.SetEnabledRequest) (management.SetEnabledResult, error) {
	return management.SetEnabledResult{}, nil
}

func (s fakeInstalledPluginStore) RemovePlugin(context.Context, management.RemoveRequest) (management.RemoveResult, error) {
	return management.RemoveResult{}, nil
}

type installedEchoInput struct {
	Text string `json:"text"`
}

type installedEchoOutput struct {
	Text string `json:"text"`
}

func installedEchoPlugin() *pluginbinding.Plugin {
	return pluginbinding.Define(
		pluginbinding.ManifestSpec{Name: "installed_echo", Description: "Installed echo test plugin."},
		pluginbinding.RegisterOperation(
			sdkmanifest.OperationSpec{
				Name:        "installed_echo.say",
				Description: "Echo installed plugin input.",
				ReadOnly:    true,
			},
			func(_ pluginbinding.Context, input installedEchoInput) (installedEchoOutput, error) {
				return installedEchoOutput{Text: input.Text + " installed"}, nil
			},
		),
	)
}

type staticContributionPlugin struct {
	name   string
	bundle resource.ContributionBundle
}

func (p staticContributionPlugin) Manifest() contributions.Manifest {
	return contributions.Manifest{Name: p.name}
}

func (p staticContributionPlugin) Contributions(context.Context, contributions.Context) (resource.ContributionBundle, error) {
	return p.bundle, nil
}

type staticDatasourcePlugin struct {
	name     string
	provider coredatasource.Provider
}

func (p staticDatasourcePlugin) Manifest() contributions.Manifest {
	return contributions.Manifest{Name: p.name}
}

func (p staticDatasourcePlugin) Contributions(context.Context, contributions.Context) (resource.ContributionBundle, error) {
	return resource.ContributionBundle{}, nil
}

func (p staticDatasourcePlugin) DatasourceProviders(context.Context, contributions.Context) ([]coredatasource.Provider, error) {
	return []coredatasource.Provider{p.provider}, nil
}

type staticDatasourceProvider struct {
	entity coredatasource.EntitySpec
}

func (p staticDatasourceProvider) Entities() []coredatasource.EntitySpec {
	return []coredatasource.EntitySpec{p.entity}
}

func (p staticDatasourceProvider) Open(_ context.Context, spec coredatasource.Spec) (coredatasource.Accessor, error) {
	return staticDatasourceAccessor{spec: spec, entity: p.entity}, nil
}

type staticDatasourceAccessor struct {
	spec   coredatasource.Spec
	entity coredatasource.EntitySpec
}

func (a staticDatasourceAccessor) Spec() coredatasource.Spec { return a.spec }

func (a staticDatasourceAccessor) Entities() []coredatasource.EntitySpec {
	return []coredatasource.EntitySpec{a.entity}
}

func (a staticDatasourceAccessor) Search(context.Context, coredatasource.SearchRequest) (coredatasource.SearchResult, error) {
	return coredatasource.SearchResult{Datasource: a.spec.Name, Entity: a.entity.Type}, nil
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
