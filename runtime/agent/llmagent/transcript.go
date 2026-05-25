package llmagent

import (
	"context"

	coreconversation "github.com/fluxplane/fluxplane-core/core/conversation"
)

type transcriptContextKey struct{}
type conversationKeyContextKey struct{}
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

// ContextWithConversationKey attaches a runtime-local conversation cache key to
// ctx. Model adapters may use it for process-local transport caches.
func ContextWithConversationKey(ctx context.Context, key string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if key == "" {
		return ctx
	}
	return context.WithValue(ctx, conversationKeyContextKey{}, key)
}

func conversationKeyFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	key, _ := ctx.Value(conversationKeyContextKey{}).(string)
	return key
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
