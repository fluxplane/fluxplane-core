package llmagent

import (
	"context"

	coreconversation "github.com/fluxplane/engine/core/conversation"
)

type transcriptContextKey struct{}
type contextMaterializedKey struct{}

// ContextWithTranscript attaches a provider transcript projection to ctx.
func ContextWithTranscript(ctx context.Context, transcript *coreconversation.Transcript) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if transcript == nil || transcript.Empty() {
		return ctx
	}
	return context.WithValue(ctx, transcriptContextKey{}, transcript)
}

func transcriptFromContext(ctx context.Context) *coreconversation.Transcript {
	if ctx == nil {
		return nil
	}
	transcript, _ := ctx.Value(transcriptContextKey{}).(*coreconversation.Transcript)
	return transcript
}

// ContextWithMaterializedContext marks ctx as already carrying session-rendered
// provider context.
func ContextWithMaterializedContext(ctx context.Context) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, contextMaterializedKey{}, true)
}

func contextMaterializedFromContext(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	value, _ := ctx.Value(contextMaterializedKey{}).(bool)
	return value
}
