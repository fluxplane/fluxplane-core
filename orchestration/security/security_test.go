package security

import (
	"context"
	"testing"

	"github.com/fluxplane/engine/core/agent"
	"github.com/fluxplane/engine/core/channel"
	"github.com/fluxplane/engine/core/policy"
)

func TestContextForInboundCarriesSubjectsWithoutConfiguredPolicy(t *testing.T) {
	actor := LocalActor("timo", "1000", "devhost", "1000")
	ctx := ContextForInbound(context.Background(), policy.AuthorizationPolicy{}, channel.Inbound{
		Actor: &actor,
		Kind:  channel.InboundMessage,
	}, agent.Spec{Name: "coder"}, false)

	auth, ok := policy.AuthorizationFromContext(ctx)
	if !ok {
		t.Fatalf("AuthorizationFromContext = false, want true")
	}
	if !auth.Policy.IsZero() {
		t.Fatalf("policy = %#v, want zero policy", auth.Policy)
	}
	if !hasSubject(auth.Subjects, policy.SubjectUser, "timo@localhost") {
		t.Fatalf("subjects = %#v, want local user", auth.Subjects)
	}
	if !hasSubject(auth.Subjects, policy.SubjectAgent, "coder") {
		t.Fatalf("subjects = %#v, want agent", auth.Subjects)
	}
}

func hasSubject(subjects []policy.SubjectRef, kind policy.SubjectKind, id string) bool {
	for _, subject := range subjects {
		if subject.Kind == kind && subject.ID == id {
			return true
		}
	}
	return false
}
