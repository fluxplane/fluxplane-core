package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	adapterllm "github.com/fluxplane/fluxplane-core/adapters/llm"
	coreconversation "github.com/fluxplane/fluxplane-core/core/conversation"
	llmagent "github.com/fluxplane/fluxplane-core/runtime/agent/llmagent"
	"github.com/gorilla/websocket"
	"github.com/openai/openai-go/v3/responses"
)

const defaultResponsesBaseURL = "https://api.openai.com/v1"

var errWebSocketFallback = errors.New("openai: websocket fallback")

type responsesWebSocketSession struct {
	mu             sync.Mutex
	conn           *websocket.Conn
	lastRequest    *responsesLogicalRequest
	lastResponseID string
	lastOutput     []json.RawMessage
}

func (s *responsesWebSocketSession) reset() {
	if s == nil {
		return
	}
	if s.conn != nil {
		_ = s.conn.Close()
	}
	s.conn = nil
	s.lastRequest = nil
	s.lastResponseID = ""
	s.lastOutput = nil
}

type responsesLogicalRequest struct {
	payload map[string]json.RawMessage
	input   []json.RawMessage
}

func (m *Model) streamWebSocketWithParams(ctx context.Context, req llmagent.Request, emit llmagent.StreamFunc, params responses.ResponseNewParams, tools []adapterllm.ToolSpec, sentItems []coreconversation.Item, httpUsage *httpUsageCollector) (llmagent.Response, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	provider := m.providerIdentity(m.modelName(req))
	session, cacheable := m.webSocketSession(req, provider)
	session.mu.Lock()
	defer session.mu.Unlock()
	if !cacheable {
		defer session.reset()
	}
	logical, err := m.responsesLogicalRequest(params)
	if err != nil {
		return llmagent.Response{}, err
	}
	if cacheable && m.webSocketWarmupEnabled() && session.lastRequest == nil {
		if err := m.prewarmWebSocketLocked(ctx, session, logical); err != nil {
			session.reset()
		}
	}
	if err := m.ensureWebSocketConnectionLocked(ctx, session); err != nil {
		session.reset()
		return llmagent.Response{}, err
	}
	payload, err := session.requestPayload(logical)
	if err != nil {
		return llmagent.Response{}, err
	}
	stream, err := m.sendWebSocketRequest(ctx, session.conn, payload)
	if err != nil {
		session.reset()
		return llmagent.Response{}, err
	}
	defer stream.stop()
	watchdog := newStreamIdleWatchdog(0, nil)
	var completed responses.Response
	out, err := m.consumeResponseEventStream(req, emit, stream, tools, sentItems, httpUsage, watchdog, func(source responses.Response) {
		completed = source
	})
	if err != nil {
		session.reset()
		return out, err
	}
	markWebSocketContinuation(&out.Transcript)
	if cacheable && completed.ID != "" {
		session.lastRequest = logical.clone()
		session.lastResponseID = completed.ID
		session.lastOutput = responseOutputRaw(completed.Output)
	}
	return out, nil
}

func (m *Model) webSocketSession(req llmagent.Request, provider coreconversation.ProviderIdentity) (*responsesWebSocketSession, bool) {
	key := strings.TrimSpace(req.ConversationKey)
	if key == "" {
		return &responsesWebSocketSession{}, false
	}
	key = strings.Join([]string{key, provider.Provider, provider.API, provider.Family, provider.Model}, "\x00")
	m.webSocketMu.Lock()
	defer m.webSocketMu.Unlock()
	if m.webSocketSessions == nil {
		m.webSocketSessions = map[string]*responsesWebSocketSession{}
	}
	session := m.webSocketSessions[key]
	if session == nil {
		session = &responsesWebSocketSession{}
		m.webSocketSessions[key] = session
	}
	return session, true
}

func (m *Model) webSocketWarmupEnabled() bool {
	switch m.runtime.WebSocketWarmup {
	case ResponsesWebSocketWarmupOff:
		return false
	case ResponsesWebSocketWarmupOn:
		return true
	default:
		return m.runtime.Transport == ResponsesTransportWebSocket
	}
}

func (m *Model) prewarmWebSocketLocked(ctx context.Context, session *responsesWebSocketSession, logical *responsesLogicalRequest) error {
	if err := m.ensureWebSocketConnectionLocked(ctx, session); err != nil {
		return err
	}
	payload := logical.payloadCopy()
	payload["type"] = mustJSONRaw("response.create")
	payload["generate"] = mustJSONRaw(false)
	stream, err := m.sendWebSocketRequest(ctx, session.conn, payload)
	if err != nil {
		return err
	}
	defer stream.stop()
	for stream.Next() {
		evt := stream.Current()
		switch evt.Type {
		case "response.completed":
			resp := evt.AsResponseCompleted().Response
			if resp.ID == "" {
				return errors.New("openai: websocket warmup completed without response id")
			}
			session.lastRequest = logical.clone()
			session.lastResponseID = resp.ID
			session.lastOutput = responseOutputRaw(resp.Output)
			return nil
		case "response.failed":
			resp := evt.AsResponseFailed().Response
			if resp.Error.Message != "" {
				return fmt.Errorf("openai: websocket warmup failed: %s: %s", resp.Error.Code, resp.Error.Message)
			}
			return errors.New("openai: websocket warmup failed")
		case "response.incomplete":
			return errors.New("openai: websocket warmup incomplete")
		case "error":
			eventErr := evt.AsError()
			return fmt.Errorf("openai: websocket warmup error: %s: %s", eventErr.Code, eventErr.Message)
		}
	}
	if err := stream.Err(); err != nil {
		return err
	}
	return errors.New("openai: websocket warmup ended without completion")
}

func (m *Model) ensureWebSocketConnectionLocked(ctx context.Context, session *responsesWebSocketSession) error {
	if session.conn != nil {
		return nil
	}
	wsURL, err := responsesWebSocketURL(m.baseURL)
	if err != nil {
		return err
	}
	headers, err := m.webSocketHandshakeHeaders(ctx)
	if err != nil {
		return err
	}
	dialer := websocket.Dialer{
		Proxy:            http.ProxyFromEnvironment,
		HandshakeTimeout: 45 * time.Second,
	}
	conn, resp, err := dialer.DialContext(ctx, wsURL, headers)
	if err != nil {
		if resp != nil && resp.StatusCode == http.StatusUpgradeRequired {
			return fmt.Errorf("%w: websocket upgrade required", errWebSocketFallback)
		}
		return fmt.Errorf("%w: %v", errWebSocketFallback, err)
	}
	session.conn = conn
	return nil
}

func (m *Model) webSocketHandshakeHeaders(ctx context.Context) (http.Header, error) {
	headers := make(http.Header)
	if m.apiKey != "" {
		headers.Set("Authorization", "Bearer "+m.apiKey)
	}
	for key, values := range m.webSocketHeaders {
		for _, value := range values {
			headers.Add(key, value)
		}
	}
	if m.webSocketHeaderFunc != nil {
		if err := m.webSocketHeaderFunc(ctx, headers); err != nil {
			return nil, err
		}
	}
	return headers, nil
}

func (m *Model) sendWebSocketRequest(ctx context.Context, conn *websocket.Conn, payload map[string]json.RawMessage) (*webSocketEventStream, error) {
	if conn == nil {
		return nil, fmt.Errorf("%w: websocket connection is unavailable", errWebSocketFallback)
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("openai: encode websocket request: %w", err)
	}
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetWriteDeadline(deadline)
	} else {
		_ = conn.SetWriteDeadline(time.Now().Add(45 * time.Second))
	}
	if err := conn.WriteMessage(websocket.TextMessage, data); err != nil {
		return nil, fmt.Errorf("%w: write websocket request: %v", errWebSocketFallback, err)
	}
	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = conn.Close()
		case <-done:
		}
	}()
	return &webSocketEventStream{
		ctx:     ctx,
		conn:    conn,
		timeout: m.runtime.StreamIdleTimeout,
		done:    done,
	}, nil
}

func (m *Model) responsesLogicalRequest(params responses.ResponseNewParams) (*responsesLogicalRequest, error) {
	data, err := json.Marshal(params)
	if err != nil {
		return nil, fmt.Errorf("openai: encode responses request: %w", err)
	}
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, fmt.Errorf("openai: decode responses request: %w", err)
	}
	if m.payloadMutator != nil {
		m.payloadMutator(payload)
	}
	input, err := inputRawItems(payload["input"])
	if err != nil {
		return nil, err
	}
	delete(payload, "previous_response_id")
	return &responsesLogicalRequest{payload: payload, input: input}, nil
}

func (s *responsesWebSocketSession) requestPayload(current *responsesLogicalRequest) (map[string]json.RawMessage, error) {
	payload := current.payloadCopy()
	payload["type"] = mustJSONRaw("response.create")
	last := s.lastRequest
	if last == nil || s.lastResponseID == "" || !sameNonInputPayload(last.payload, current.payload) {
		return payload, nil
	}
	prefix := append(append([]json.RawMessage(nil), last.input...), s.lastOutput...)
	if !hasRawPrefix(current.input, prefix) {
		return payload, nil
	}
	delta := current.input[len(prefix):]
	rawDelta, err := json.Marshal(delta)
	if err != nil {
		return nil, fmt.Errorf("openai: encode websocket input delta: %w", err)
	}
	payload["input"] = rawDelta
	payload["previous_response_id"] = mustJSONRaw(s.lastResponseID)
	return payload, nil
}

func (r *responsesLogicalRequest) payloadCopy() map[string]json.RawMessage {
	out := make(map[string]json.RawMessage, len(r.payload))
	for key, value := range r.payload {
		out[key] = append(json.RawMessage(nil), value...)
	}
	return out
}

func (r *responsesLogicalRequest) clone() *responsesLogicalRequest {
	if r == nil {
		return nil
	}
	return &responsesLogicalRequest{
		payload: r.payloadCopy(),
		input:   cloneRawSlice(r.input),
	}
}

type webSocketEventStream struct {
	ctx     context.Context
	conn    *websocket.Conn
	timeout time.Duration
	current responses.ResponseStreamEventUnion
	err     error
	done    chan struct{}
	once    sync.Once
}

func (s *webSocketEventStream) stop() {
	if s == nil {
		return
	}
	s.once.Do(func() {
		if s.done != nil {
			close(s.done)
		}
	})
}

func (s *webSocketEventStream) Next() bool {
	if s == nil || s.err != nil {
		return false
	}
	if err := s.ctx.Err(); err != nil {
		s.err = err
		return false
	}
	deadline := time.Time{}
	if s.timeout > 0 {
		deadline = time.Now().Add(s.timeout)
	}
	if ctxDeadline, ok := s.ctx.Deadline(); ok && (deadline.IsZero() || ctxDeadline.Before(deadline)) {
		deadline = ctxDeadline
	}
	_ = s.conn.SetReadDeadline(deadline)
	messageType, data, err := s.conn.ReadMessage()
	if err != nil {
		if ctxErr := s.ctx.Err(); ctxErr != nil {
			s.err = ctxErr
			return false
		}
		var netErr net.Error
		if errors.As(err, &netErr) && netErr.Timeout() && s.timeout > 0 {
			s.err = fmt.Errorf("%w after %s", ErrStreamIdleTimeout, s.timeout)
			return false
		}
		s.err = err
		return false
	}
	if messageType != websocket.TextMessage {
		s.err = errors.New("openai: unexpected binary websocket event")
		return false
	}
	var evt responses.ResponseStreamEventUnion
	if err := json.Unmarshal(data, &evt); err != nil {
		s.err = fmt.Errorf("openai: decode websocket event: %w", err)
		return false
	}
	s.current = evt
	return true
}

func (s *webSocketEventStream) Current() responses.ResponseStreamEventUnion {
	if s == nil {
		return responses.ResponseStreamEventUnion{}
	}
	return s.current
}

func (s *webSocketEventStream) Err() error {
	if s == nil {
		return nil
	}
	return s.err
}

func responsesWebSocketURL(baseURL string) (string, error) {
	if strings.TrimSpace(baseURL) == "" {
		baseURL = defaultResponsesBaseURL
	}
	u, err := url.Parse(baseURL)
	if err != nil {
		return "", fmt.Errorf("openai: parse websocket base url: %w", err)
	}
	switch u.Scheme {
	case "http":
		u.Scheme = "ws"
	case "https":
		u.Scheme = "wss"
	case "ws", "wss":
	default:
		return "", fmt.Errorf("openai: unsupported websocket base url scheme %q", u.Scheme)
	}
	path := strings.TrimRight(u.Path, "/")
	if !strings.HasSuffix(path, "/responses") {
		path += "/responses"
	}
	u.Path = path
	return u.String(), nil
}

func inputRawItems(raw json.RawMessage) ([]json.RawMessage, error) {
	if len(bytes.TrimSpace(raw)) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil, nil
	}
	var items []json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil, fmt.Errorf("openai: responses websocket input must be a list: %w", err)
	}
	return cloneRawSlice(items), nil
}

func sameNonInputPayload(a, b map[string]json.RawMessage) bool {
	if len(a) != len(b) {
		return sameNonInputPayloadSlow(a, b)
	}
	for key, av := range a {
		if key == "input" || key == "previous_response_id" {
			continue
		}
		bv, ok := b[key]
		if !ok || !rawJSONEqual(av, bv) {
			return false
		}
	}
	for key := range b {
		if key == "input" || key == "previous_response_id" {
			continue
		}
		if _, ok := a[key]; !ok {
			return false
		}
	}
	return true
}

func sameNonInputPayloadSlow(a, b map[string]json.RawMessage) bool {
	for key, av := range a {
		if key == "input" || key == "previous_response_id" {
			continue
		}
		bv, ok := b[key]
		if !ok || !rawJSONEqual(av, bv) {
			return false
		}
	}
	for key := range b {
		if key == "input" || key == "previous_response_id" {
			continue
		}
		if _, ok := a[key]; !ok {
			return false
		}
	}
	return true
}

func hasRawPrefix(items, prefix []json.RawMessage) bool {
	if len(prefix) > len(items) {
		return false
	}
	for i := range prefix {
		if !rawJSONEqual(items[i], prefix[i]) {
			return false
		}
	}
	return true
}

func rawJSONEqual(a, b json.RawMessage) bool {
	var ac, bc bytes.Buffer
	if json.Compact(&ac, a) != nil || json.Compact(&bc, b) != nil {
		return bytes.Equal(bytes.TrimSpace(a), bytes.TrimSpace(b))
	}
	return bytes.Equal(ac.Bytes(), bc.Bytes())
}

func responseOutputRaw(output []responses.ResponseOutputItemUnion) []json.RawMessage {
	out := make([]json.RawMessage, 0, len(output))
	for _, item := range output {
		raw := item.RawJSON()
		if strings.TrimSpace(raw) == "" {
			data, err := json.Marshal(item)
			if err != nil {
				continue
			}
			raw = string(data)
		}
		out = append(out, json.RawMessage(raw))
	}
	return out
}

func markWebSocketContinuation(transcript *coreconversation.Transcript) {
	if transcript == nil || transcript.Continuation == nil {
		return
	}
	transcript.Continuation.Transport = coreconversation.TransportWebSocket
}

func cloneRawSlice(in []json.RawMessage) []json.RawMessage {
	if len(in) == 0 {
		return nil
	}
	out := make([]json.RawMessage, len(in))
	for i, item := range in {
		out[i] = append(json.RawMessage(nil), item...)
	}
	return out
}

func cloneHeader(in http.Header) http.Header {
	if in == nil {
		return nil
	}
	out := make(http.Header, len(in))
	for key, values := range in {
		out[key] = append([]string(nil), values...)
	}
	return out
}

func mustJSONRaw(value any) json.RawMessage {
	data, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return data
}
