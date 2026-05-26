package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"reflect"
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
var errWebSocketReadIdleTimeout = errors.New("openai: websocket read idle timeout")

const webSocketWriteTimeout = 45 * time.Second

type responsesWebSocketSession struct {
	mu             sync.Mutex
	providerKey    string
	conn           *responsesWebSocketConn
	lastRequest    *responsesLogicalRequest
	lastResponseID string
	lastOutput     []json.RawMessage
	stickyHeader   string
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
	s.stickyHeader = ""
}

func (s *responsesWebSocketSession) resetRequestState() {
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
	logical, err := m.responsesLogicalRequest(req, params)
	if err != nil {
		return llmagent.Response{}, err
	}
	if cacheable && m.webSocketWarmupEnabled() && session.lastRequest == nil {
		if err := m.prewarmWebSocketLocked(ctx, req, session, logical); err != nil {
			session.reset()
			if errors.Is(err, errWebSocketFallback) {
				return llmagent.Response{}, err
			}
		}
	}
	var out llmagent.Response
	var completed responses.Response
	for attempt := 0; attempt < 2; attempt++ {
		if err := m.ensureWebSocketConnectionLocked(ctx, req, session); err != nil {
			session.reset()
			return llmagent.Response{}, err
		}
		payload, err := session.requestPayload(logical)
		if err != nil {
			return llmagent.Response{}, err
		}
		stream, err := m.sendWebSocketRequest(ctx, session.conn, payload)
		if err != nil {
			if attempt == 0 && websocketCanRetryBeforeProviderEvent(err) {
				session.resetRequestState()
				continue
			}
			session.reset()
			return llmagent.Response{}, err
		}
		watchdog := newStreamIdleWatchdog(0, nil)
		out, err = m.consumeResponseEventStream(req, emit, stream, tools, sentItems, httpUsage, watchdog, func(source responses.Response) {
			completed = source
		})
		stream.stop()
		if err == nil {
			break
		}
		if attempt == 0 && stream.EventsRead() == 0 && websocketCanRetryBeforeProviderEvent(err) {
			session.resetRequestState()
			continue
		}
		session.reset()
		return out, err
	}
	markWebSocketContinuation(&out.Transcript)
	cacheCompleted := cacheable && completed.ID != ""
	if completed.ID != "" && m.webSocketResponseProcessed {
		if err := m.sendWebSocketResponseProcessed(ctx, session.conn, completed.ID); err != nil && websocketCanRetryBeforeProviderEvent(err) {
			session.resetRequestState()
			cacheCompleted = false
		}
	}
	if cacheCompleted {
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
	providerKey := responsesWebSocketProviderKey(provider)
	key = strings.Join([]string{key, providerKey}, "\x00")
	m.webSocketMu.Lock()
	defer m.webSocketMu.Unlock()
	if m.webSocketSessions == nil {
		m.webSocketSessions = map[string]*responsesWebSocketSession{}
	}
	session := m.webSocketSessions[key]
	if session == nil {
		session = &responsesWebSocketSession{providerKey: providerKey}
		m.webSocketSessions[key] = session
	}
	return session, true
}

func responsesWebSocketProviderKey(provider coreconversation.ProviderIdentity) string {
	return strings.Join([]string{provider.Provider, provider.API, provider.Family, provider.Model}, "\x00")
}

func (m *Model) webSocketFallbackDisabledFor(provider coreconversation.ProviderIdentity) bool {
	if !m.webSocketSessionFallback {
		return false
	}
	key := responsesWebSocketProviderKey(provider)
	m.webSocketMu.Lock()
	defer m.webSocketMu.Unlock()
	return m.webSocketFallbackDisabled[key]
}

func (m *Model) disableWebSocketFallback(provider coreconversation.ProviderIdentity) {
	if !m.webSocketSessionFallback {
		return
	}
	key := responsesWebSocketProviderKey(provider)
	m.webSocketMu.Lock()
	defer m.webSocketMu.Unlock()
	if m.webSocketFallbackDisabled == nil {
		m.webSocketFallbackDisabled = map[string]bool{}
	}
	m.webSocketFallbackDisabled[key] = true
	for sessionKey, session := range m.webSocketSessions {
		if session != nil && session.providerKey == key {
			session.mu.Lock()
			session.reset()
			session.mu.Unlock()
			delete(m.webSocketSessions, sessionKey)
		}
	}
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

func (m *Model) prewarmWebSocketLocked(ctx context.Context, req llmagent.Request, session *responsesWebSocketSession, logical *responsesLogicalRequest) error {
	if err := m.ensureWebSocketConnectionLocked(ctx, req, session); err != nil {
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
			return openAIProviderErrorFromResponse("openai: websocket warmup failed", resp)
		case "response.incomplete":
			return errors.New("openai: websocket warmup incomplete")
		case "error":
			return openAIProviderErrorFromEvent("openai: websocket warmup error", evt)
		}
	}
	if err := stream.Err(); err != nil {
		return err
	}
	return errors.New("openai: websocket warmup ended without completion")
}

func (m *Model) ensureWebSocketConnectionLocked(ctx context.Context, req llmagent.Request, session *responsesWebSocketSession) error {
	if session.conn != nil {
		if !session.conn.IsClosed() {
			return nil
		}
		session.resetRequestState()
	}
	wsURL, err := responsesWebSocketURL(m.baseURL)
	if err != nil {
		return err
	}
	headers, err := m.webSocketHandshakeHeaders(ctx, req)
	if err != nil {
		return err
	}
	if m.webSocketStickyHeader != "" && strings.TrimSpace(session.stickyHeader) != "" {
		headers.Set(m.webSocketStickyHeader, session.stickyHeader)
	}
	dialer := websocket.Dialer{
		Proxy:             http.ProxyFromEnvironment,
		HandshakeTimeout:  45 * time.Second,
		EnableCompression: true,
	}
	conn, resp, err := dialer.DialContext(ctx, wsURL, headers)
	if err != nil {
		if resp != nil && resp.StatusCode == http.StatusUpgradeRequired {
			return fmt.Errorf("%w: websocket upgrade required", errWebSocketFallback)
		}
		return fmt.Errorf("%w: %v", errWebSocketFallback, err)
	}
	if m.webSocketStickyHeader != "" && resp != nil {
		if value := strings.TrimSpace(resp.Header.Get(m.webSocketStickyHeader)); value != "" {
			session.stickyHeader = value
		}
	}
	session.conn = newResponsesWebSocketConn(conn)
	return nil
}

func (m *Model) webSocketHandshakeHeaders(ctx context.Context, req llmagent.Request) (http.Header, error) {
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
	if m.webSocketRequestHeaderFunc != nil {
		if err := m.webSocketRequestHeaderFunc(ctx, req, headers); err != nil {
			return nil, err
		}
	}
	return headers, nil
}

func (m *Model) sendWebSocketRequest(ctx context.Context, conn *responsesWebSocketConn, payload map[string]json.RawMessage) (*webSocketEventStream, error) {
	if conn == nil {
		return nil, fmt.Errorf("%w: websocket connection is unavailable", errWebSocketFallback)
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("openai: encode websocket request: %w", err)
	}
	if err := conn.WriteMessage(ctx, websocket.TextMessage, data); err != nil {
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
		ctx:           ctx,
		conn:          conn,
		timeout:       m.runtime.StreamIdleTimeout,
		done:          done,
		wrappedError:  m.webSocketWrappedErrorFunc,
		skipMalformed: m.webSocketSkipMalformed,
	}, nil
}

func (m *Model) sendWebSocketResponseProcessed(ctx context.Context, conn *responsesWebSocketConn, responseID string) error {
	if conn == nil || strings.TrimSpace(responseID) == "" {
		return nil
	}
	payload := map[string]json.RawMessage{
		"type":        mustJSONRaw("response.processed"),
		"response_id": mustJSONRaw(responseID),
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("openai: encode websocket response.processed: %w", err)
	}
	if err := conn.WriteMessage(ctx, websocket.TextMessage, data); err != nil {
		return fmt.Errorf("openai: write websocket response.processed: %w", err)
	}
	return nil
}

type responsesWebSocketConn struct {
	raw       *websocket.Conn
	writeMu   sync.Mutex
	closeOnce sync.Once
	reads     *webSocketReadQueue
	closed    chan struct{}
}

type webSocketRead struct {
	messageType int
	data        []byte
	err         error
}

func newResponsesWebSocketConn(raw *websocket.Conn) *responsesWebSocketConn {
	conn := &responsesWebSocketConn{
		raw:    raw,
		reads:  newWebSocketReadQueue(),
		closed: make(chan struct{}),
	}
	go conn.readLoop()
	return conn
}

func (c *responsesWebSocketConn) readLoop() {
	defer c.reads.close()
	defer c.close()
	for {
		messageType, data, err := c.raw.ReadMessage()
		if err != nil {
			c.deliver(webSocketRead{err: err})
			return
		}
		c.deliver(webSocketRead{messageType: messageType, data: data})
	}
}

func (c *responsesWebSocketConn) deliver(read webSocketRead) {
	if c.reads == nil || !c.reads.push(read) {
		return
	}
}

func (c *responsesWebSocketConn) Close() error {
	if c == nil {
		return nil
	}
	c.close()
	return nil
}

func (c *responsesWebSocketConn) close() {
	c.closeOnce.Do(func() {
		_ = c.raw.Close()
		close(c.closed)
	})
}

func (c *responsesWebSocketConn) IsClosed() bool {
	if c == nil {
		return true
	}
	select {
	case <-c.closed:
		return true
	default:
		return false
	}
}

func (c *responsesWebSocketConn) WriteMessage(ctx context.Context, messageType int, data []byte) error {
	if c == nil {
		return errors.New("websocket connection is unavailable")
	}
	select {
	case <-c.closed:
		return errors.New("websocket connection is closed")
	default:
	}
	deadline := time.Now().Add(webSocketWriteTimeout)
	if ctx != nil {
		if ctxDeadline, ok := ctx.Deadline(); ok && ctxDeadline.Before(deadline) {
			deadline = ctxDeadline
		}
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	_ = c.raw.SetWriteDeadline(deadline)
	return c.raw.WriteMessage(messageType, data)
}

func (c *responsesWebSocketConn) ReadMessage(ctx context.Context, deadline time.Time) (int, []byte, error) {
	if c == nil {
		return 0, nil, errors.New("websocket connection is unavailable")
	}
	var timer *time.Timer
	var timeout <-chan time.Time
	if !deadline.IsZero() {
		wait := time.Until(deadline)
		if wait <= 0 {
			return 0, nil, errWebSocketReadIdleTimeout
		}
		timer = time.NewTimer(wait)
		timeout = timer.C
		defer timer.Stop()
	}
	var done <-chan struct{}
	if ctx != nil {
		done = ctx.Done()
	}
	read, ok, err := c.reads.pop(done, timeout)
	if err != nil {
		return 0, nil, err
	}
	if !ok {
		return 0, nil, errors.New("websocket connection is closed")
	}
	if read.err != nil {
		return 0, nil, read.err
	}
	return read.messageType, read.data, nil
}

type webSocketReadQueue struct {
	mu     sync.Mutex
	items  []webSocketRead
	closed bool
	notify chan struct{}
}

func newWebSocketReadQueue() *webSocketReadQueue {
	return &webSocketReadQueue{notify: make(chan struct{})}
}

func (q *webSocketReadQueue) push(read webSocketRead) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed {
		return false
	}
	q.items = append(q.items, read)
	close(q.notify)
	q.notify = make(chan struct{})
	return true
}

func (q *webSocketReadQueue) close() {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed {
		return
	}
	q.closed = true
	close(q.notify)
}

func (q *webSocketReadQueue) pop(done <-chan struct{}, timeout <-chan time.Time) (webSocketRead, bool, error) {
	for {
		q.mu.Lock()
		if len(q.items) > 0 {
			read := q.items[0]
			copy(q.items, q.items[1:])
			q.items[len(q.items)-1] = webSocketRead{}
			q.items = q.items[:len(q.items)-1]
			q.mu.Unlock()
			return read, true, nil
		}
		if q.closed {
			q.mu.Unlock()
			return webSocketRead{}, false, nil
		}
		notify := q.notify
		q.mu.Unlock()
		select {
		case <-done:
			return webSocketRead{}, false, context.Canceled
		case <-timeout:
			return webSocketRead{}, false, errWebSocketReadIdleTimeout
		case <-notify:
		}
	}
}

func (m *Model) responsesLogicalRequest(req llmagent.Request, params responses.ResponseNewParams) (*responsesLogicalRequest, error) {
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
	if m.webSocketPayloadFunc != nil {
		if err := m.webSocketPayloadFunc(req, payload); err != nil {
			return nil, err
		}
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
	if last == nil || s.lastResponseID == "" {
		return payload, nil
	}
	if !sameNonInputPayload(last.payload, current.payload) {
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
	ctx           context.Context
	conn          *responsesWebSocketConn
	timeout       time.Duration
	current       responses.ResponseStreamEventUnion
	err           error
	seen          int
	done          chan struct{}
	once          sync.Once
	wrappedError  func([]byte) (error, bool)
	skipMalformed bool
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
	for {
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
		messageType, data, err := s.conn.ReadMessage(s.ctx, deadline)
		if err != nil {
			if ctxErr := s.ctx.Err(); ctxErr != nil {
				s.err = ctxErr
				return false
			}
			if errors.Is(err, errWebSocketReadIdleTimeout) && s.timeout > 0 {
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
		if s.wrappedError != nil {
			if err, ok := s.wrappedError(data); ok {
				s.err = err
				return false
			}
		}
		var evt responses.ResponseStreamEventUnion
		if err := json.Unmarshal(data, &evt); err != nil {
			if s.skipMalformed {
				continue
			}
			s.err = fmt.Errorf("openai: decode websocket event: %w", err)
			return false
		}
		if strings.TrimSpace(evt.Type) == "" && s.skipMalformed {
			continue
		}
		s.seen++
		s.current = evt
		return true
	}
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

func (s *webSocketEventStream) EventsRead() int {
	if s == nil {
		return 0
	}
	return s.seen
}

func websocketCanRetryBeforeProviderEvent(err error) bool {
	if err == nil || errors.Is(err, ErrStreamIdleTimeout) {
		return false
	}
	if errors.Is(err, ErrProviderRetryable) {
		return true
	}
	if errors.Is(err, errWebSocketFallback) {
		return true
	}
	var closeErr *websocket.CloseError
	if errors.As(err, &closeErr) {
		return true
	}
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return true
	}
	errText := strings.ToLower(err.Error())
	for _, fragment := range []string{
		"unexpected eof",
		"connection reset by peer",
		"broken pipe",
		"use of closed network connection",
		"websocket connection is unavailable",
	} {
		if strings.Contains(errText, fragment) {
			return true
		}
	}
	return false
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
	av, aok := decodeJSONValue(a)
	bv, bok := decodeJSONValue(b)
	if aok && bok {
		return reflect.DeepEqual(av, bv)
	}
	return bytes.Equal(bytes.TrimSpace(a), bytes.TrimSpace(b))
}

func decodeJSONValue(raw json.RawMessage) (any, bool) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return nil, false
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, false
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return nil, false
	}
	return value, true
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
