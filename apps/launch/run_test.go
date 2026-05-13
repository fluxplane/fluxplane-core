package launch

import (
	"context"
	"strings"
	"testing"

	"github.com/fluxplane/agentruntime/adapters/distribution/localruntime"
	embedaxon "github.com/fluxplane/agentruntime/adapters/embed/axon"
	"github.com/fluxplane/agentruntime/core/channel"
	coredistribution "github.com/fluxplane/agentruntime/core/distribution"
	"github.com/fluxplane/agentruntime/core/resource"
	coresession "github.com/fluxplane/agentruntime/core/session"
	clientapi "github.com/fluxplane/agentruntime/orchestration/client"
	"github.com/fluxplane/agentruntime/orchestration/distribution"
	"github.com/fluxplane/agentruntime/plugins/codingplugin"
	"github.com/fluxplane/agentruntime/plugins/textplugin"
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

func hasOperationSpec(runtime Runtime, name string) bool {
	for _, spec := range runtime.Composition.OperationSpecs {
		if string(spec.Ref.Name) == name {
			return true
		}
	}
	return false
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
