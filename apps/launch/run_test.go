package launch

import (
	"context"
	"strings"
	"testing"

	"github.com/fluxplane/agentruntime/adapters/distribution/localruntime"
	embedaxon "github.com/fluxplane/agentruntime/adapters/embed/axon"
	coreagent "github.com/fluxplane/agentruntime/core/agent"
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
	"github.com/fluxplane/agentruntime/plugins/codingplugin"
	"github.com/fluxplane/agentruntime/plugins/datasourceplugin"
	"github.com/fluxplane/agentruntime/plugins/eventcatalog"
	"github.com/fluxplane/agentruntime/plugins/planexecplugin"
	"github.com/fluxplane/agentruntime/plugins/sessionhistoryplugin"
	"github.com/fluxplane/agentruntime/plugins/textplugin"
	operationruntime "github.com/fluxplane/agentruntime/runtime/operation"
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

func TestLaunchDevWiresSessionHistoryIntoPluginAgents(t *testing.T) {
	withStateDir(t)
	runtime, err := Launch(context.Background(), RuntimeOptions{
		Root: t.TempDir(),
		Bundles: []resource.ContributionBundle{{
			Agents:  []coreagent.Spec{{Name: "main"}},
			Plugins: []resource.PluginRef{{Name: planexecplugin.Name}},
		}},
		Dev:                 true,
		AllowPrivateNetwork: true,
	})
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	defer runtime.Close()

	for _, name := range []string{string(planexecplugin.WorkerAgent), string(planexecplugin.ExplorerAgent)} {
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
	store, closeStore, err := openLocalThreadStore(registry)
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
