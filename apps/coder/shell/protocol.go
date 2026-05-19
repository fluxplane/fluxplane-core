package codershell

// ClientMode selects the shell client implementation.
type ClientMode string

const (
	// ClientModeDirect uses an in-process shell client. It is the temporary default
	// until the local daemon transport is available.
	ClientModeDirect ClientMode = "direct"
	// ClientModeLocal uses the real shell transport against a local endpoint such
	// as unix://path/to/socket.
	ClientModeLocal ClientMode = "local"
	// ClientModeRemote uses the real shell transport against a URL/target.
	ClientModeRemote ClientMode = "remote"
	// ClientModeFake uses deterministic in-memory behavior for tests and demos.
	ClientModeFake ClientMode = "fake"
)

// SessionCreatePayload is the wire shape for creating a shell session.
type SessionCreatePayload struct {
	CWD string `json:"cwd,omitempty"`
}

// SessionCreateResponse is the wire shape returned after creating a shell session.
type SessionCreateResponse struct {
	ID  string `json:"id"`
	CWD string `json:"cwd,omitempty"`
}

// CommandSubmitPayload is the wire shape for command submission.
type CommandSubmitPayload struct {
	Line string `json:"line"`
	CWD  string `json:"cwd,omitempty"`
}

// AskSubmitPayload is the wire shape for ask submission.
type AskSubmitPayload struct {
	Text    string        `json:"text"`
	CWD     string        `json:"cwd,omitempty"`
	Context []ContextItem `json:"context,omitempty"`
}

// SlashSubmitPayload is the wire shape for slash command submission.
type SlashSubmitPayload struct {
	Line string `json:"line"`
	CWD  string `json:"cwd,omitempty"`
}

// CWDChangePayload is the wire shape for cwd changes.
type CWDChangePayload struct {
	Path string `json:"path"`
}

// ResourceSearchPayload is the wire shape for resource search.
type ResourceSearchPayload struct {
	Text       string         `json:"text,omitempty"`
	Kinds      []ResourceKind `json:"kinds,omitempty"`
	Limit      int            `json:"limit,omitempty"`
	Workspace  string         `json:"workspace,omitempty"`
	CWD        string         `json:"cwd,omitempty"`
	PrefixMode string         `json:"prefix_mode,omitempty"`
	Mention    bool           `json:"mention,omitempty"`
}

// EventsResponse is a simple synchronous event response shape.
type EventsResponse struct {
	Events []TranscriptEvent `json:"events,omitempty"`
}

// EventEnvelope is the async/streaming event envelope shape for future transports.
type EventEnvelope struct {
	SessionID string          `json:"session_id,omitempty"`
	Event     TranscriptEvent `json:"event"`
}
