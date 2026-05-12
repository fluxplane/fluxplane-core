package llmagent

import (
	"context"

	coreconversation "github.com/fluxplane/agentruntime/core/conversation"
)

type transcriptContextKey struct{}

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
