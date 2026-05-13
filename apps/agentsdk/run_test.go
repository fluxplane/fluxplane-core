package agentsdk

import (
	"context"
	"testing"

	"github.com/fluxplane/agentruntime/adapters/distribution/localruntime"
	"github.com/fluxplane/agentruntime/core/channel"
	coredistribution "github.com/fluxplane/agentruntime/core/distribution"
	coresession "github.com/fluxplane/agentruntime/core/session"
	clientapi "github.com/fluxplane/agentruntime/orchestration/client"
	"github.com/fluxplane/agentruntime/orchestration/distribution"
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

type fakeRuntime struct{}

func (fakeRuntime) OpenSession(context.Context, distribution.OpenRequest) (clientapi.SessionHandle, error) {
	return nil, nil
}
