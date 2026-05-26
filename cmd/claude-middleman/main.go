package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

const (
	defaultUpstream = "https://api.anthropic.com"
	defaultPrompt   = "Reply with exactly ok."
)

type options struct {
	listen        string
	outRoot       string
	runID         string
	upstream      string
	forward       bool
	redact        bool
	runClaude     bool
	claudeBin     string
	prompt        string
	model         string
	outputFormat  string
	timeout       time.Duration
	bare          bool
	disableTools  bool
	dummyAPIKey   bool
	debugAPI      bool
	responseLimit int
	extraArgs     stringList
}

type stringList []string

func (s *stringList) String() string {
	return strings.Join(*s, " ")
}

func (s *stringList) Set(value string) error {
	*s = append(*s, value)
	return nil
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "claude-middleman: %v\n", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	opts := options{
		listen:        "127.0.0.1:0",
		outRoot:       ".tmp/claude-middleman",
		upstream:      defaultUpstream,
		redact:        true,
		runClaude:     true,
		claudeBin:     "claude",
		prompt:        defaultPrompt,
		model:         "sonnet",
		outputFormat:  "stream-json",
		timeout:       90 * time.Second,
		bare:          true,
		disableTools:  true,
		dummyAPIKey:   true,
		responseLimit: 64 * 1024,
	}

	fs := flag.NewFlagSet("claude-middleman", flag.ContinueOnError)
	fs.StringVar(&opts.listen, "listen", opts.listen, "address for the local capture proxy")
	fs.StringVar(&opts.outRoot, "out", opts.outRoot, "directory for capture artifacts")
	fs.StringVar(&opts.runID, "run-id", "", "capture run directory name; defaults to a UTC timestamp")
	fs.StringVar(&opts.upstream, "upstream", opts.upstream, "upstream Anthropic base URL used with --forward")
	fs.BoolVar(&opts.forward, "forward", opts.forward, "forward captured requests to the upstream instead of returning a stub response")
	fs.BoolVar(&opts.redact, "redact", opts.redact, "redact credential-like headers and body fields in capture files")
	fs.BoolVar(&opts.runClaude, "run-claude", opts.runClaude, "start claude against the local proxy after the proxy is ready")
	fs.StringVar(&opts.claudeBin, "claude-bin", opts.claudeBin, "claude executable to run")
	fs.StringVar(&opts.prompt, "prompt", opts.prompt, "prompt passed to claude when --run-claude is true")
	fs.StringVar(&opts.model, "model", opts.model, "model passed to claude when --run-claude is true")
	fs.StringVar(&opts.outputFormat, "output-format", opts.outputFormat, "claude --output-format value")
	fs.DurationVar(&opts.timeout, "timeout", opts.timeout, "overall timeout when --run-claude is true")
	fs.BoolVar(&opts.bare, "bare", opts.bare, "pass --bare to claude")
	fs.BoolVar(&opts.disableTools, "disable-tools", opts.disableTools, "pass --tools with an empty value to claude")
	fs.BoolVar(&opts.dummyAPIKey, "dummy-api-key", opts.dummyAPIKey, "set a dummy ANTHROPIC_API_KEY when stubbing")
	fs.BoolVar(&opts.debugAPI, "debug-api", opts.debugAPI, "pass --debug api to claude")
	fs.IntVar(&opts.responseLimit, "response-log-limit", opts.responseLimit, "maximum response body bytes retained in capture records")
	fs.Var(&opts.extraArgs, "extra-claude-arg", "additional claude argument; repeat for multiple arguments")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if opts.outputFormat == "" {
		return errors.New("output-format is empty")
	}
	if opts.outRoot == "" {
		return errors.New("out is empty")
	}
	if opts.responseLimit < 0 {
		return errors.New("response-log-limit must be non-negative")
	}
	captureDir := opts.outRoot
	if opts.runID == "" {
		opts.runID = time.Now().UTC().Format("20060102T150405Z")
	}
	if opts.runID != "." {
		captureDir = filepath.Join(opts.outRoot, opts.runID)
	}
	if err := os.MkdirAll(captureDir, 0o700); err != nil {
		return err
	}

	proxy, err := newCaptureProxy(opts, captureDir)
	if err != nil {
		return err
	}
	listener, err := net.Listen("tcp", opts.listen)
	if err != nil {
		return err
	}
	defer func() { _ = listener.Close() }()

	server := &http.Server{Handler: proxy}
	serverErr := make(chan error, 1)
	go func() {
		if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErr <- err
			return
		}
		serverErr <- nil
	}()

	baseURL := baseURL(listener.Addr())
	log.Printf("capture proxy listening at %s", baseURL)
	log.Printf("capture directory: %s", captureDir)

	if err := writeJSON(filepath.Join(captureDir, "run.json"), runRecord(opts, baseURL)); err != nil {
		return err
	}

	if opts.runClaude {
		if err := runClaude(opts, baseURL, captureDir); err != nil {
			_ = shutdownServer(server)
			_ = proxy.writeSummary()
			return err
		}
		if err := shutdownServer(server); err != nil {
			return err
		}
		if err := <-serverErr; err != nil {
			return err
		}
		return proxy.writeSummary()
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	select {
	case <-ctx.Done():
	case err := <-serverErr:
		if err != nil {
			return err
		}
	}
	if err := shutdownServer(server); err != nil {
		return err
	}
	return proxy.writeSummary()
}

func shutdownServer(server *http.Server) error {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	return server.Shutdown(ctx)
}

func baseURL(addr net.Addr) string {
	host, port, err := net.SplitHostPort(addr.String())
	if err != nil {
		return "http://" + addr.String()
	}
	if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	return "http://" + host + ":" + port
}

func runRecord(opts options, base string) map[string]any {
	env := map[string]string{
		"ANTHROPIC_BASE_URL":                            base,
		"CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC":      "1",
		"CLAUDE_CODE_SIMPLE":                            "1",
		"CLAUDE_CODE_DISABLE_AUTO_MEMORY":               "1",
		"CLAUDE_CODE_DISABLE_BACKGROUND_TASKS":          "1",
		"CLAUDE_CODE_DISABLE_BACKGROUND_PLUGIN_REFRESH": "1",
	}
	if opts.runClaude && opts.dummyAPIKey && !opts.forward {
		env["ANTHROPIC_API_KEY"] = redactedValue("middleman-dummy-key")
	}
	return map[string]any{
		"created_at":    time.Now().UTC().Format(time.RFC3339Nano),
		"base_url":      base,
		"forward":       opts.forward,
		"upstream":      opts.upstream,
		"redact":        opts.redact,
		"run_claude":    opts.runClaude,
		"claude_bin":    opts.claudeBin,
		"claude_args":   claudeArgs(opts),
		"env_overrides": env,
	}
}

func runClaude(opts options, base, captureDir string) error {
	ctx, cancel := context.WithTimeout(context.Background(), opts.timeout)
	defer cancel()

	args := claudeArgs(opts)
	cmd := exec.CommandContext(ctx, opts.claudeBin, args...)
	cmd.Env = claudeEnv(os.Environ(), opts, base)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	log.Printf("running: %s %s", opts.claudeBin, quoteArgs(args))
	err := cmd.Run()
	result := map[string]any{
		"args":       args,
		"stdout":     stdout.String(),
		"stderr":     stderr.String(),
		"exit_error": "",
	}
	if err != nil {
		result["exit_error"] = err.Error()
	}
	if writeErr := writeJSON(filepath.Join(captureDir, "claude-result.json"), result); writeErr != nil {
		return writeErr
	}
	if ctx.Err() != nil {
		return fmt.Errorf("claude timed out after %s", opts.timeout)
	}
	if err != nil {
		return fmt.Errorf("claude failed: %w; see %s", err, filepath.Join(captureDir, "claude-result.json"))
	}
	return nil
}

func claudeArgs(opts options) []string {
	var args []string
	if opts.bare {
		args = append(args, "--bare")
	}
	if opts.debugAPI {
		args = append(args, "--debug", "api")
	}
	if opts.outputFormat == "stream-json" {
		args = append(args, "--verbose")
	}
	args = append(args,
		"--print",
		"--input-format", "text",
		"--output-format", opts.outputFormat,
		"--model", opts.model,
		"--no-session-persistence",
		"--strict-mcp-config",
		"--mcp-config", `{"mcpServers":{}}`,
		"--disable-slash-commands",
		"--no-chrome",
	)
	if opts.disableTools {
		args = append(args, "--tools=")
	}
	args = append(args, opts.extraArgs...)
	args = append(args, opts.prompt)
	return args
}

func claudeEnv(base []string, opts options, endpoint string) []string {
	env := setEnv(base, "ANTHROPIC_BASE_URL", endpoint)
	env = setEnv(env, "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC", "1")
	env = setEnv(env, "CLAUDE_CODE_SIMPLE", "1")
	env = setEnv(env, "CLAUDE_CODE_DISABLE_AUTO_MEMORY", "1")
	env = setEnv(env, "CLAUDE_CODE_DISABLE_BACKGROUND_TASKS", "1")
	env = setEnv(env, "CLAUDE_CODE_DISABLE_BACKGROUND_PLUGIN_REFRESH", "1")
	if opts.dummyAPIKey && !opts.forward {
		env = setEnv(env, "ANTHROPIC_API_KEY", "middleman-dummy-key")
	}
	return env
}

func setEnv(env []string, key, value string) []string {
	prefix := key + "="
	for i, current := range env {
		if strings.HasPrefix(current, prefix) {
			env[i] = prefix + value
			return env
		}
	}
	return append(env, prefix+value)
}

func quoteArgs(args []string) string {
	quoted := make([]string, 0, len(args))
	for _, arg := range args {
		if arg == "" {
			quoted = append(quoted, `""`)
			continue
		}
		if strings.ContainsAny(arg, " \t\n\"'") {
			quoted = append(quoted, strconv.Quote(arg))
			continue
		}
		quoted = append(quoted, arg)
	}
	return strings.Join(quoted, " ")
}

type captureProxy struct {
	opts      options
	dir       string
	upstream  *url.URL
	client    *http.Client
	jsonl     *os.File
	mu        sync.Mutex
	nextID    int
	summaries []requestSummary
}

func newCaptureProxy(opts options, dir string) (*captureProxy, error) {
	var upstream *url.URL
	var err error
	if opts.forward {
		upstream, err = url.Parse(opts.upstream)
		if err != nil {
			return nil, err
		}
		if upstream.Scheme == "" || upstream.Host == "" {
			return nil, fmt.Errorf("upstream must be an absolute URL: %q", opts.upstream)
		}
	}
	jsonl, err := os.OpenFile(filepath.Join(dir, "captures.jsonl"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, err
	}
	return &captureProxy{
		opts:     opts,
		dir:      dir,
		upstream: upstream,
		client:   &http.Client{},
		jsonl:    jsonl,
	}, nil
}

func (p *captureProxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	started := time.Now()
	id := p.allocateID()
	body, readErr := io.ReadAll(r.Body)
	_ = r.Body.Close()
	if readErr != nil {
		http.Error(w, readErr.Error(), http.StatusBadRequest)
		return
	}

	record := captureRecord{
		ID:        id,
		StartedAt: started.UTC().Format(time.RFC3339Nano),
		Request: requestCapture{
			Method:        r.Method,
			Path:          r.URL.Path,
			RawQuery:      r.URL.RawQuery,
			Host:          r.Host,
			RemoteAddr:    r.RemoteAddr,
			ContentLength: len(body),
			Headers:       headerList(r.Header, p.opts.redact),
			Body:          bodyValue(body, p.opts.redact),
		},
	}

	if p.opts.forward {
		p.forward(w, r, body, &record)
	} else {
		p.stub(w, r, body, &record)
	}
	record.DurationMS = time.Since(started).Milliseconds()
	if err := p.persist(record); err != nil {
		log.Printf("persist capture %d: %v", id, err)
	}
}

func (p *captureProxy) allocateID() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.nextID++
	return p.nextID
}

func (p *captureProxy) forward(w http.ResponseWriter, original *http.Request, body []byte, record *captureRecord) {
	target := *p.upstream
	target.Path = joinURLPath(p.upstream.Path, original.URL.Path)
	target.RawQuery = original.URL.RawQuery

	req, err := http.NewRequestWithContext(original.Context(), original.Method, target.String(), bytes.NewReader(body))
	if err != nil {
		record.Response.Error = err.Error()
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	copyHeader(req.Header, original.Header)
	req.Host = p.upstream.Host

	resp, err := p.client.Do(req)
	if err != nil {
		record.Response.Error = err.Error()
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	defer func() { _ = resp.Body.Close() }()

	copyHeader(w.Header(), resp.Header)
	w.WriteHeader(resp.StatusCode)
	limited := &limitBuffer{limit: p.opts.responseLimit}
	_, copyErr := io.Copy(w, io.TeeReader(resp.Body, limited))
	record.Response = responseCapture{
		Status:        resp.Status,
		StatusCode:    resp.StatusCode,
		Headers:       headerList(resp.Header, p.opts.redact),
		BodySample:    limited.String(),
		BodyTruncated: limited.truncated,
		Error:         errorString(copyErr),
	}
}

func (p *captureProxy) stub(w http.ResponseWriter, r *http.Request, body []byte, record *captureRecord) {
	switch r.URL.Path {
	case "/":
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		record.Response = responseCapture{
			Status:     "200 OK",
			StatusCode: http.StatusOK,
			Headers:    headerList(w.Header(), p.opts.redact),
		}
	case "/v1/messages":
		model, stream := messageRequestMetadata(body)
		if stream {
			data := stubSSE(model)
			w.Header().Set("Content-Type", "text/event-stream")
			w.Header().Set("Cache-Control", "no-cache")
			w.WriteHeader(http.StatusOK)
			_, err := io.WriteString(w, data)
			if flusher, ok := w.(http.Flusher); ok {
				flusher.Flush()
			}
			record.Response = responseCapture{
				Status:     "200 OK",
				StatusCode: http.StatusOK,
				Headers:    headerList(w.Header(), p.opts.redact),
				BodySample: data,
				Error:      errorString(err),
			}
			return
		}
		data := stubJSON(model)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, err := io.WriteString(w, data)
		record.Response = responseCapture{
			Status:     "200 OK",
			StatusCode: http.StatusOK,
			Headers:    headerList(w.Header(), p.opts.redact),
			BodySample: data,
			Error:      errorString(err),
		}
	case "/v1/messages/count_tokens":
		data := `{"input_tokens":1}`
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, err := io.WriteString(w, data)
		record.Response = responseCapture{
			Status:     "200 OK",
			StatusCode: http.StatusOK,
			Headers:    headerList(w.Header(), p.opts.redact),
			BodySample: data,
			Error:      errorString(err),
		}
	default:
		data := fmt.Sprintf(`{"type":"error","error":{"type":"not_found_error","message":"middleman stub has no route for %s"}}`, r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, err := io.WriteString(w, data)
		record.Response = responseCapture{
			Status:     "404 Not Found",
			StatusCode: http.StatusNotFound,
			Headers:    headerList(w.Header(), p.opts.redact),
			BodySample: data,
			Error:      errorString(err),
		}
	}
}

func (p *captureProxy) persist(record captureRecord) error {
	name := filepath.Join(p.dir, fmt.Sprintf("%04d_%s.json", record.ID, sanitizePath(record.Request.Path)))
	if err := writeJSON(name, record); err != nil {
		return err
	}
	summary := requestSummary{
		ID:          record.ID,
		Method:      record.Request.Method,
		Path:        record.Request.Path,
		RawQuery:    record.Request.RawQuery,
		StatusCode:  record.Response.StatusCode,
		DurationMS:  record.DurationMS,
		BodyKeys:    bodyKeys(record.Request.Body),
		HeaderNames: headerNames(record.Request.Headers),
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.summaries = append(p.summaries, summary)
	line, err := json.Marshal(record)
	if err != nil {
		return err
	}
	if _, err := p.jsonl.Write(append(line, '\n')); err != nil {
		return err
	}
	return nil
}

func (p *captureProxy) writeSummary() error {
	p.mu.Lock()
	summaries := append([]requestSummary(nil), p.summaries...)
	p.mu.Unlock()
	if p.jsonl != nil {
		if err := p.jsonl.Close(); err != nil {
			return err
		}
	}
	return writeJSON(filepath.Join(p.dir, "summary.json"), map[string]any{
		"captured_at": time.Now().UTC().Format(time.RFC3339Nano),
		"requests":    summaries,
	})
}

type captureRecord struct {
	ID         int             `json:"id"`
	StartedAt  string          `json:"started_at"`
	DurationMS int64           `json:"duration_ms"`
	Request    requestCapture  `json:"request"`
	Response   responseCapture `json:"response"`
}

type requestCapture struct {
	Method        string       `json:"method"`
	Path          string       `json:"path"`
	RawQuery      string       `json:"raw_query,omitempty"`
	Host          string       `json:"host"`
	RemoteAddr    string       `json:"remote_addr"`
	ContentLength int          `json:"content_length"`
	Headers       []headerItem `json:"headers"`
	Body          any          `json:"body"`
}

type responseCapture struct {
	Status        string       `json:"status,omitempty"`
	StatusCode    int          `json:"status_code,omitempty"`
	Headers       []headerItem `json:"headers,omitempty"`
	BodySample    string       `json:"body_sample,omitempty"`
	BodyTruncated bool         `json:"body_truncated,omitempty"`
	Error         string       `json:"error,omitempty"`
}

type requestSummary struct {
	ID          int      `json:"id"`
	Method      string   `json:"method"`
	Path        string   `json:"path"`
	RawQuery    string   `json:"raw_query,omitempty"`
	StatusCode  int      `json:"status_code"`
	DurationMS  int64    `json:"duration_ms"`
	BodyKeys    []string `json:"body_keys,omitempty"`
	HeaderNames []string `json:"header_names,omitempty"`
}

type headerItem struct {
	Name   string   `json:"name"`
	Values []string `json:"values"`
}

func headerList(headers http.Header, redact bool) []headerItem {
	names := make([]string, 0, len(headers))
	for name := range headers {
		names = append(names, name)
	}
	sort.Strings(names)
	items := make([]headerItem, 0, len(names))
	for _, name := range names {
		values := append([]string(nil), headers.Values(name)...)
		if redact && sensitiveKey(name) {
			for i, value := range values {
				values[i] = redactedValue(value)
			}
		}
		items = append(items, headerItem{Name: name, Values: values})
	}
	return items
}

func headerNames(headers []headerItem) []string {
	out := make([]string, 0, len(headers))
	for _, header := range headers {
		out = append(out, header.Name)
	}
	sort.Strings(out)
	return out
}

func bodyValue(body []byte, redact bool) any {
	if len(body) == 0 {
		return nil
	}
	var value any
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return string(body)
	}
	if redact {
		return redactAny(value)
	}
	return value
}

func bodyKeys(value any) []string {
	obj, ok := value.(map[string]any)
	if !ok {
		return nil
	}
	keys := make([]string, 0, len(obj))
	for key := range obj {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func redactAny(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(typed))
		for key, child := range typed {
			if sensitiveKey(key) {
				if text, ok := child.(string); ok {
					out[key] = redactedValue(text)
				} else {
					out[key] = "<redacted>"
				}
				continue
			}
			out[key] = redactAny(child)
		}
		return out
	case []any:
		out := make([]any, len(typed))
		for i, child := range typed {
			out[i] = redactAny(child)
		}
		return out
	case string:
		if strings.HasPrefix(strings.ToLower(typed), "bearer ") {
			return redactedValue(typed)
		}
		return typed
	default:
		return value
	}
}

func sensitiveKey(key string) bool {
	normalized := strings.ToLower(strings.ReplaceAll(strings.ReplaceAll(key, "-", "_"), " ", "_"))
	switch normalized {
	case "authorization", "proxy_authorization", "x_api_key", "api_key", "apikey", "api_key_helper",
		"access_token", "accesstoken", "refresh_token", "refreshtoken", "cookie", "set_cookie",
		"auth_token", "authtoken", "oauth_token", "oauthtoken", "session_access_token",
		"user_id", "device_id", "account_uuid":
		return true
	default:
		return strings.Contains(normalized, "secret")
	}
}

func redactedValue(value string) string {
	sum := sha256.Sum256([]byte(value))
	return fmt.Sprintf("<redacted sha256:%s len:%d>", hex.EncodeToString(sum[:8]), len(value))
}

func messageRequestMetadata(body []byte) (string, bool) {
	var req struct {
		Model  string `json:"model"`
		Stream bool   `json:"stream"`
	}
	_ = json.Unmarshal(body, &req)
	model := strings.TrimSpace(req.Model)
	if model == "" {
		model = "claude-middleman-stub"
	}
	return model, req.Stream
}

func stubSSE(model string) string {
	return strings.Join([]string{
		`event: message_start`,
		fmt.Sprintf(`data: {"type":"message_start","message":{"id":"msg_middleman","type":"message","role":"assistant","model":%q,"content":[],"usage":{"input_tokens":1,"output_tokens":0}}}`, model),
		``,
		`event: content_block_start`,
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
		``,
		`event: content_block_delta`,
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"ok"}}`,
		``,
		`event: content_block_stop`,
		`data: {"type":"content_block_stop","index":0}`,
		``,
		`event: message_delta`,
		`data: {"type":"message_delta","delta":{"stop_reason":"end_turn","stop_sequence":null},"usage":{"output_tokens":1}}`,
		``,
		`event: message_stop`,
		`data: {"type":"message_stop"}`,
		``,
	}, "\n")
}

func stubJSON(model string) string {
	return fmt.Sprintf(`{"id":"msg_middleman","type":"message","role":"assistant","model":%q,"content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`, model)
}

func copyHeader(dst, src http.Header) {
	for key, values := range src {
		for _, value := range values {
			dst.Add(key, value)
		}
	}
}

func joinURLPath(basePath, requestPath string) string {
	if basePath == "" || basePath == "/" {
		return requestPath
	}
	return strings.TrimRight(basePath, "/") + "/" + strings.TrimLeft(requestPath, "/")
}

func sanitizePath(path string) string {
	path = strings.Trim(path, "/")
	if path == "" {
		return "root"
	}
	replacer := strings.NewReplacer("/", "_", "\\", "_", ":", "_")
	return replacer.Replace(path)
}

func writeJSON(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(path, data, 0o600)
}

func errorString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

type limitBuffer struct {
	limit     int
	buf       bytes.Buffer
	truncated bool
}

func (b *limitBuffer) Write(p []byte) (int, error) {
	if b.limit <= 0 {
		if len(p) > 0 {
			b.truncated = true
		}
		return len(p), nil
	}
	remaining := b.limit - b.buf.Len()
	if remaining <= 0 {
		if len(p) > 0 {
			b.truncated = true
		}
		return len(p), nil
	}
	if len(p) > remaining {
		b.truncated = true
		_, _ = b.buf.Write(p[:remaining])
		return len(p), nil
	}
	_, _ = b.buf.Write(p)
	return len(p), nil
}

func (b *limitBuffer) String() string {
	return b.buf.String()
}
