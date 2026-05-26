package openai

import "context"

type responsesProtocolMetadataKey struct{}

// ResponsesProtocolMetadata carries provider transport metadata through
// contexts used by provider HTTP middleware.
type ResponsesProtocolMetadata struct {
	ConversationKey string
	WindowID        string
	Subagent        string
	ParentThreadID  string
	TurnMetadata    string
	TraceParent     string
	TraceState      string
	BetaFeatures    string
	Attestation     string
	ClientMetadata  map[string]string
}

// ContextWithResponsesProtocolMetadata returns a context carrying provider
// transport metadata for middleware.
func ContextWithResponsesProtocolMetadata(ctx context.Context, meta ResponsesProtocolMetadata) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, responsesProtocolMetadataKey{}, meta)
}

// ResponsesProtocolMetadataFromContext extracts provider transport metadata.
func ResponsesProtocolMetadataFromContext(ctx context.Context) (ResponsesProtocolMetadata, bool) {
	if ctx == nil {
		return ResponsesProtocolMetadata{}, false
	}
	meta, ok := ctx.Value(responsesProtocolMetadataKey{}).(ResponsesProtocolMetadata)
	return meta, ok
}
