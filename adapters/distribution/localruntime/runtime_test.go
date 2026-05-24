package localruntime

import (
	"context"
	"testing"

	"github.com/fluxplane/fluxplane-core/core/channel"
	coresession "github.com/fluxplane/fluxplane-core/core/session"
	clientapi "github.com/fluxplane/fluxplane-core/orchestration/client"
	"github.com/fluxplane/fluxplane-core/orchestration/distribution"
)

func TestRuntimeAppliesDefaultsBeforeOpening(t *testing.T) {
	var got distribution.OpenRequest
	runtime := Runtime{
		DefaultSession:      coresession.Ref{Name: "main"},
		DefaultConversation: channel.ConversationRef{ID: "conv"},
		Open: func(_ context.Context, req distribution.OpenRequest) (clientapi.SessionHandle, error) {
			got = req
			return nil, nil
		},
	}

	if _, err := runtime.OpenSession(context.Background(), distribution.OpenRequest{}); err != nil {
		t.Fatalf("OpenSession: %v", err)
	}
	if got.Session.Name != "main" {
		t.Fatalf("session = %q, want main", got.Session.Name)
	}
	if got.Conversation.ID != "conv" {
		t.Fatalf("conversation = %q, want conv", got.Conversation.ID)
	}
}

func TestRuntimeRequiresSessionAndOpener(t *testing.T) {
	_, err := (Runtime{}).OpenSession(context.Background(), distribution.OpenRequest{})
	if err == nil {
		t.Fatal("OpenSession error is nil, want missing session")
	}
	_, err = (Runtime{DefaultSession: coresession.Ref{Name: "main"}}).OpenSession(context.Background(), distribution.OpenRequest{})
	if err == nil {
		t.Fatal("OpenSession error is nil, want missing opener")
	}
}
