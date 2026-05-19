// Package localruntime adapts local launch functions to distribution runtimes.
package localruntime

import (
	"context"
	"fmt"

	"github.com/fluxplane/agentruntime/core/channel"
	coresession "github.com/fluxplane/agentruntime/core/session"
	clientapi "github.com/fluxplane/agentruntime/orchestration/client"
	"github.com/fluxplane/agentruntime/orchestration/distribution"
)

// OpenFunc opens one local distribution session.
type OpenFunc func(context.Context, distribution.OpenRequest) (clientapi.SessionHandle, error)

// Runtime applies local launch defaults before delegating to Open.
type Runtime struct {
	DefaultSession      coresession.Ref
	DefaultConversation channel.ConversationRef
	Open                OpenFunc
}

// OpenSession opens the requested or default local distribution session.
func (r Runtime) OpenSession(ctx context.Context, req distribution.OpenRequest) (clientapi.SessionHandle, error) {
	if req.Session.Name == "" {
		req.Session = r.DefaultSession
	}
	if req.Session.Name == "" {
		return nil, fmt.Errorf("distribution run: no session selected")
	}
	if req.Conversation.ID == "" {
		req.Conversation = r.DefaultConversation
	}
	if req.Conversation.ID == "" {
		req.Conversation = channel.ConversationRef{ID: "coder-app-run"}
	}
	if r.Open == nil {
		return nil, fmt.Errorf("distribution run: local runtime opener is not configured")
	}
	return r.Open(ctx, req)
}
