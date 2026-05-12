package httpssechannel

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/fluxplane/agentruntime/core/command"
	corethread "github.com/fluxplane/agentruntime/core/thread"
	clientapi "github.com/fluxplane/agentruntime/orchestration/client"
)

var _ clientapi.ChannelClient = (*Client)(nil)

// Client is a remote ChannelClient backed by HTTP JSON and SSE.
type Client struct {
	baseURL    string
	httpClient *http.Client
}

// ClientConfig configures a remote HTTP/SSE channel client.
type ClientConfig struct {
	BaseURL    string
	HTTPClient *http.Client
}

// NewClient returns a remote HTTP/SSE channel client.
func NewClient(cfg ClientConfig) (*Client, error) {
	baseURL := strings.TrimRight(cfg.BaseURL, "/")
	if baseURL == "" {
		return nil, fmt.Errorf("httpssechannel: base URL is empty")
	}
	httpClient := cfg.HTTPClient
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	return &Client{baseURL: baseURL, httpClient: httpClient}, nil
}

func (c *Client) Open(ctx context.Context, req clientapi.OpenRequest) (clientapi.SessionHandle, error) {
	var info clientapi.SessionInfo
	if err := c.postJSON(ctx, "/sessions/open", req, &info); err != nil {
		return nil, err
	}
	return &Session{client: c, info: info}, nil
}

func (c *Client) Resume(ctx context.Context, req clientapi.ResumeRequest) (clientapi.SessionHandle, error) {
	var info clientapi.SessionInfo
	if err := c.postJSON(ctx, "/sessions/resume", req, &info); err != nil {
		return nil, err
	}
	return &Session{client: c, info: info}, nil
}

func (c *Client) ListSessions(ctx context.Context, req clientapi.ListSessionsRequest) ([]clientapi.SessionSummary, error) {
	values := url.Values{}
	if req.IncludeArchived {
		values.Set("include_archived", "true")
	}
	if req.Limit > 0 {
		values.Set("limit", strconv.Itoa(req.Limit))
	}
	path := "/sessions"
	if encoded := values.Encode(); encoded != "" {
		path += "?" + encoded
	}
	var summaries []clientapi.SessionSummary
	if err := c.getJSON(ctx, path, &summaries); err != nil {
		return nil, err
	}
	return summaries, nil
}

// Session is a remote session handle.
type Session struct {
	client *Client
	info   clientapi.SessionInfo
}

var _ clientapi.SessionHandle = (*Session)(nil)

func (s *Session) Info() clientapi.SessionInfo {
	if s == nil {
		return clientapi.SessionInfo{}
	}
	return s.info
}

func (s *Session) Submit(ctx context.Context, submission clientapi.Submission) (clientapi.RunHandle, error) {
	if s == nil || s.client == nil {
		return nil, fmt.Errorf("httpssechannel: session is nil")
	}
	if submission.ID == "" {
		submission.ID = clientapi.RunID(newID("run_"))
	}
	if submission.Kind == clientapi.SubmissionEvent {
		return nil, fmt.Errorf("httpssechannel: event submissions require a typed event codec")
	}
	if err := submission.Validate(); err != nil {
		return nil, err
	}
	run := newRunHandle(s.client, s.info, submission)
	go run.execute(ctx)
	return run, nil
}

func (s *Session) SendCommand(ctx context.Context, invocation command.Invocation) (clientapi.RunHandle, error) {
	return s.Submit(ctx, clientapi.Submission{
		Kind:    clientapi.SubmissionCommand,
		Command: &invocation,
	})
}

func (s *Session) SendInput(ctx context.Context, input clientapi.Input) (clientapi.RunHandle, error) {
	return s.Submit(ctx, clientapi.Submission{
		Kind:  clientapi.SubmissionInput,
		Input: &input,
	})
}

func (s *Session) Events(ctx context.Context, opts clientapi.EventOptions) (<-chan clientapi.Event, func(), error) {
	if s == nil || s.client == nil {
		ch := make(chan clientapi.Event)
		close(ch)
		return ch, func() {}, fmt.Errorf("httpssechannel: session is nil")
	}
	if s.info.Thread.ID == "" {
		ch := make(chan clientapi.Event)
		close(ch)
		return ch, func() {}, fmt.Errorf("httpssechannel: session thread id is empty")
	}
	events, cancel, _, err := s.client.openEventStream(ctx, s.info.Thread.ID, opts)
	return events, cancel, err
}

func (s *Session) OnEvent(ctx context.Context, fn func(clientapi.Event)) (func(), error) {
	if fn == nil {
		return func() {}, fmt.Errorf("httpssechannel: event callback is nil")
	}
	events, cancel, err := s.Events(ctx, clientapi.EventOptions{Buffer: 16})
	if err != nil {
		return cancel, err
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case <-ctx.Done():
				return
			case event, ok := <-events:
				if !ok {
					return
				}
				fn(event)
			}
		}
	}()
	return func() {
		cancel()
		<-done
	}, nil
}

func (s *Session) Close(context.Context) error { return nil }

type runHandle struct {
	client     *Client
	id         clientapi.RunID
	session    clientapi.SessionInfo
	submission clientapi.Submission
	events     chan clientapi.Event
	done       chan struct{}

	mu     sync.Mutex
	result clientapi.Result
	err    error
}

var _ clientapi.RunHandle = (*runHandle)(nil)

func newRunHandle(client *Client, session clientapi.SessionInfo, submission clientapi.Submission) *runHandle {
	return &runHandle{
		client:     client,
		id:         submission.ID,
		session:    session,
		submission: submission,
		events:     make(chan clientapi.Event, 16),
		done:       make(chan struct{}),
	}
}

func (r *runHandle) ID() clientapi.RunID { return r.id }

func (r *runHandle) Session() clientapi.SessionInfo {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.session
}

func (r *runHandle) Submission() clientapi.Submission {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.submission
}

func (r *runHandle) Events() <-chan clientapi.Event { return r.events }

func (r *runHandle) Done() <-chan struct{} { return r.done }

func (r *runHandle) Err() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.err
}

func (r *runHandle) Wait(ctx context.Context) (clientapi.Result, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	select {
	case <-ctx.Done():
		return clientapi.Result{}, ctx.Err()
	case <-r.done:
		r.mu.Lock()
		defer r.mu.Unlock()
		return r.result, r.err
	}
}

func (r *runHandle) execute(ctx context.Context) {
	defer close(r.done)

	session, submission := r.snapshot()
	events, cancel, streamErrs, err := r.client.openEventStream(ctx, session.Thread.ID, clientapi.EventOptions{Buffer: 16})
	if err != nil {
		r.setResult(clientapi.Result{RunID: r.id, Session: session, Submission: submission}, err)
		close(r.events)
		return
	}
	forwardDone := make(chan struct{})
	forwardErr := make(chan error, 1)
	go func() {
		defer close(forwardDone)
		defer close(forwardErr)
		forwardErr <- r.forwardRunEvents(events, streamErrs)
	}()

	var result clientapi.Result
	err = r.client.postJSON(ctx, "/sessions/"+url.PathEscape(string(session.Thread.ID))+"/submit", submitRequest{
		Session:    session,
		Submission: submission,
	}, &result)
	if err != nil && result.RunID == "" {
		result = clientapi.Result{RunID: r.id, Session: session, Submission: submission}
	}
	r.setResult(result, err)
	if err == nil {
		select {
		case <-forwardDone:
			if forwardErrValue := <-forwardErr; forwardErrValue != nil {
				r.setResult(result, forwardErrValue)
			}
		case <-time.After(time.Second):
			r.setResult(result, fmt.Errorf("httpssechannel: timed out waiting for run %s terminal event", r.id))
		}
	}
	cancel()
	select {
	case <-forwardDone:
	case <-time.After(time.Second):
	}
	close(r.events)
}

func (r *runHandle) snapshot() (clientapi.SessionInfo, clientapi.Submission) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.session, r.submission
}

func (r *runHandle) setResult(result clientapi.Result, err error) {
	r.mu.Lock()
	if result.Session.Thread.ID != "" {
		r.session = result.Session
	}
	if result.Submission.Kind != "" {
		r.submission = result.Submission
	}
	r.result = result
	r.err = err
	r.mu.Unlock()
}

func (r *runHandle) emit(event clientapi.Event) {
	select {
	case r.events <- event:
	case <-time.After(time.Second):
	}
}

func (r *runHandle) forwardRunEvents(events <-chan clientapi.Event, streamErrs <-chan error) error {
	for {
		select {
		case event, ok := <-events:
			if !ok {
				if err := pendingStreamError(streamErrs); err != nil {
					return err
				}
				return fmt.Errorf("httpssechannel: event stream closed before run %s completed", r.id)
			}
			if event.RunID != r.id {
				continue
			}
			r.emit(event)
			if event.Kind == clientapi.EventRunCompleted || event.Kind == clientapi.EventRunFailed {
				return nil
			}
		case err, ok := <-streamErrs:
			if !ok {
				streamErrs = nil
				continue
			}
			if err != nil {
				return err
			}
		}
	}
}

func pendingStreamError(streamErrs <-chan error) error {
	if streamErrs == nil {
		return nil
	}
	for {
		select {
		case err, ok := <-streamErrs:
			if !ok {
				return nil
			}
			if err != nil {
				return err
			}
		default:
			return nil
		}
	}
}

func (c *Client) openEventStream(ctx context.Context, threadID corethread.ID, opts clientapi.EventOptions) (<-chan clientapi.Event, func(), <-chan error, error) {
	if opts.Buffer < 0 {
		opts.Buffer = 0
	}
	values := url.Values{}
	if opts.Buffer > 0 {
		values.Set("buffer", strconv.Itoa(opts.Buffer))
	}
	if opts.Replay {
		values.Set("replay", "true")
	}
	if opts.After.Sequence != 0 {
		values.Set("after", strconv.FormatUint(uint64(opts.After.Sequence), 10))
	}
	path := "/sessions/" + url.PathEscape(string(threadID)) + "/events"
	if encoded := values.Encode(); encoded != "" {
		path += "?" + encoded
	}

	reqCtx, cancelCtx := context.WithCancel(ctx)
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		cancelCtx()
		return nil, nil, nil, err
	}
	req.Header.Set("Accept", "text/event-stream")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		cancelCtx()
		return nil, nil, nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		defer resp.Body.Close()
		cancelCtx()
		return nil, nil, nil, responseError(resp)
	}

	out := make(chan clientapi.Event, opts.Buffer)
	errs := make(chan error, 1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer close(out)
		defer close(errs)
		defer resp.Body.Close()
		scanner := bufio.NewScanner(resp.Body)
		scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		var data strings.Builder
		for scanner.Scan() {
			line := scanner.Text()
			if line == "" {
				if data.Len() > 0 {
					var event clientapi.Event
					if err := json.Unmarshal([]byte(data.String()), &event); err != nil {
						sendStreamError(reqCtx, errs, fmt.Errorf("httpssechannel: decode SSE event: %w", err))
						return
					}
					select {
					case out <- event:
					case <-reqCtx.Done():
						return
					}
					data.Reset()
				}
				continue
			}
			if strings.HasPrefix(line, "data:") {
				if data.Len() > 0 {
					data.WriteByte('\n')
				}
				data.WriteString(strings.TrimSpace(strings.TrimPrefix(line, "data:")))
			}
		}
		if err := scanner.Err(); err != nil && reqCtx.Err() == nil {
			sendStreamError(reqCtx, errs, fmt.Errorf("httpssechannel: read SSE stream: %w", err))
		}
	}()
	cancel := func() {
		cancelCtx()
		_ = resp.Body.Close()
		<-done
	}
	return out, cancel, errs, nil
}

func sendStreamError(ctx context.Context, errs chan<- error, err error) {
	select {
	case errs <- err:
	case <-ctx.Done():
	default:
	}
}

func (c *Client) postJSON(ctx context.Context, path string, in, out any) error {
	data, err := json.Marshal(in)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	return c.doJSON(req, out)
}

func (c *Client) getJSON(ctx context.Context, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	return c.doJSON(req, out)
}

func (c *Client) doJSON(req *http.Request, out any) error {
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return responseError(resp)
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func responseError(resp *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	var payload struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(body, &payload); err == nil && payload.Error != "" {
		return fmt.Errorf("httpssechannel: %s: %s", resp.Status, payload.Error)
	}
	return fmt.Errorf("httpssechannel: %s", resp.Status)
}

func newID(prefix string) string {
	var raw [8]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return fmt.Sprintf("%s%d", prefix, time.Now().UnixNano())
	}
	return prefix + hex.EncodeToString(raw[:])
}
