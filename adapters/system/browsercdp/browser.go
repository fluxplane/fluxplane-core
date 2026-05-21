// Package browsercdp implements BrowserManager with Chrome DevTools Protocol.
package browsercdp

import (
	"context"
	"fmt"
	"net/url"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"
	"github.com/fluxplane/engine/runtime/system"
)

// Config configures a CDP-backed browser manager.
type Config struct {
	Workspace system.Workspace
	Headless  bool
}

// Manager owns local browser sessions.
type Manager struct {
	workspace system.Workspace
	allocCtx  context.Context
	cancel    context.CancelFunc
	mu        sync.Mutex
	next      int
	sessions  map[string]*session
}

type session struct {
	ctx    context.Context
	cancel context.CancelFunc
}

// New returns a browser manager using a local Chrome/Chromium executable.
func New(cfg Config) (*Manager, error) {
	if cfg.Workspace == nil {
		return nil, fmt.Errorf("browsercdp: workspace is nil")
	}
	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.Flag("headless", cfg.Headless),
		chromedp.Flag("disable-gpu", true),
		chromedp.Flag("no-first-run", true),
		chromedp.Flag("no-default-browser-check", true),
	)
	allocCtx, cancel := chromedp.NewExecAllocator(context.Background(), opts...)
	return &Manager{workspace: cfg.Workspace, allocCtx: allocCtx, cancel: cancel, sessions: map[string]*session{}}, nil
}

// Shutdown releases all browser resources.
func (m *Manager) Shutdown() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, sess := range m.sessions {
		sess.cancel()
		delete(m.sessions, id)
	}
	if m.cancel != nil {
		m.cancel()
	}
}

func (m *Manager) Open(ctx context.Context, req system.BrowserOpenRequest) (system.BrowserOpenResult, error) {
	_ = ctx
	target := strings.TrimSpace(req.URL)
	if target == "" {
		target = "about:blank"
	}
	if err := validateBrowserURL(target); err != nil {
		return system.BrowserOpenResult{}, err
	}
	width := req.Width
	if width <= 0 {
		width = 1280
	}
	height := req.Height
	if height <= 0 {
		height = 900
	}
	sessionCtx, cancel := chromedp.NewContext(m.allocCtx)
	if err := chromedp.Run(sessionCtx); err != nil {
		cancel()
		return system.BrowserOpenResult{}, err
	}
	m.mu.Lock()
	m.next++
	id := fmt.Sprintf("browser-%d", m.next)
	m.sessions[id] = &session{ctx: sessionCtx, cancel: cancel}
	m.mu.Unlock()
	runCtx, runCancel := timeout(sessionCtx, req.Timeout)
	defer runCancel()
	var title, location string
	if err := chromedp.Run(runCtx,
		chromedp.EmulateViewport(int64(width), int64(height)),
		chromedp.Navigate(target),
		chromedp.Title(&title),
		chromedp.Location(&location),
	); err != nil {
		cancel()
		return system.BrowserOpenResult{}, err
	}
	return system.BrowserOpenResult{SessionID: id, URL: location, Title: title}, nil
}

func (m *Manager) Navigate(ctx context.Context, req system.BrowserSessionRequest) (system.BrowserPageResult, error) {
	if strings.TrimSpace(req.URL) == "" {
		return system.BrowserPageResult{}, fmt.Errorf("url is required")
	}
	if err := validateBrowserURL(req.URL); err != nil {
		return system.BrowserPageResult{}, err
	}
	return m.runPage(ctx, req.SessionID, req.Timeout, chromedp.Navigate(req.URL))
}

func (m *Manager) Click(ctx context.Context, req system.BrowserSelectorRequest) (system.BrowserPageResult, error) {
	return m.runPage(ctx, req.SessionID, req.Timeout, chromedp.Click(req.Selector, chromedp.ByQuery))
}

func (m *Manager) Type(ctx context.Context, req system.BrowserTypeRequest) (system.BrowserPageResult, error) {
	text := req.Text
	if req.Submit {
		text += "\n"
	}
	return m.runPage(ctx, req.SessionID, req.Timeout, chromedp.SendKeys(req.Selector, text, chromedp.ByQuery))
}

func (m *Manager) Select(ctx context.Context, req system.BrowserSelectRequest) (system.BrowserPageResult, error) {
	quoted := make([]string, 0, len(req.Values))
	for _, value := range req.Values {
		quoted = append(quoted, fmt.Sprintf("%q", value))
	}
	script := fmt.Sprintf(`(() => {
const el = document.querySelector(%q);
if (!el) throw new Error("selector not found");
const values = new Set([%s]);
for (const opt of el.options || []) opt.selected = values.has(opt.value);
el.dispatchEvent(new Event("change", {bubbles:true}));
})()`, req.Selector, strings.Join(quoted, ","))
	return m.runPage(ctx, req.SessionID, req.Timeout, chromedp.Evaluate(script, nil))
}

func (m *Manager) Read(ctx context.Context, req system.BrowserReadRequest) (system.BrowserReadResult, error) {
	sess, err := m.session(req.SessionID)
	if err != nil {
		return system.BrowserReadResult{}, err
	}
	selector := req.Selector
	if strings.TrimSpace(selector) == "" {
		selector = "body"
	}
	runCtx, cancel := timeout(sess.ctx, req.Timeout)
	defer cancel()
	var title, location, text, html string
	if err := chromedp.Run(runCtx,
		chromedp.Text(selector, &text, chromedp.ByQuery),
		chromedp.OuterHTML(selector, &html, chromedp.ByQuery),
		chromedp.Title(&title),
		chromedp.Location(&location),
	); err != nil {
		_ = sess
		return system.BrowserReadResult{}, err
	}
	return system.BrowserReadResult{SessionID: req.SessionID, URL: location, Title: title, Text: text, HTML: html}, nil
}

func (m *Manager) Screenshot(ctx context.Context, req system.BrowserSessionRequest) (system.BrowserArtifact, error) {
	var data []byte
	if _, err := m.run(ctx, req.SessionID, req.Timeout, chromedp.FullScreenshot(&data, 90)); err != nil {
		return system.BrowserArtifact{}, err
	}
	return m.writeArtifact(ctx, req.SessionID, "screenshot", ".png", "image/png", data)
}

func (m *Manager) Evaluate(ctx context.Context, req system.BrowserEvaluateRequest) (system.BrowserEvaluateResult, error) {
	var value any
	if _, err := m.run(ctx, req.SessionID, req.Timeout, chromedp.Evaluate(req.Script, &value)); err != nil {
		return system.BrowserEvaluateResult{}, err
	}
	return system.BrowserEvaluateResult{SessionID: req.SessionID, Value: value}, nil
}

func (m *Manager) Wait(ctx context.Context, req system.BrowserWaitRequest) (system.BrowserPageResult, error) {
	var action chromedp.Action
	if strings.TrimSpace(req.Selector) != "" {
		action = chromedp.WaitVisible(req.Selector, chromedp.ByQuery)
	} else if req.Duration > 0 {
		action = chromedp.Sleep(req.Duration)
	} else {
		return system.BrowserPageResult{}, fmt.Errorf("selector or duration is required")
	}
	return m.runPage(ctx, req.SessionID, req.Timeout, action)
}

func (m *Manager) Scroll(ctx context.Context, req system.BrowserScrollRequest) (system.BrowserPageResult, error) {
	script := fmt.Sprintf("window.scrollBy(%d,%d)", req.X, req.Y)
	return m.runPage(ctx, req.SessionID, req.Timeout, chromedp.Evaluate(script, nil))
}

func (m *Manager) Hover(ctx context.Context, req system.BrowserSelectorRequest) (system.BrowserPageResult, error) {
	script := fmt.Sprintf(`(() => {
const el = document.querySelector(%q);
if (!el) throw new Error("selector not found");
el.dispatchEvent(new MouseEvent("mouseover", {bubbles:true}));
})()`, req.Selector)
	return m.runPage(ctx, req.SessionID, req.Timeout, chromedp.Evaluate(script, nil))
}

func (m *Manager) Back(ctx context.Context, req system.BrowserSessionRequest) (system.BrowserPageResult, error) {
	return m.runPage(ctx, req.SessionID, req.Timeout, chromedp.Evaluate("history.back()", nil), chromedp.Sleep(500*time.Millisecond))
}

func (m *Manager) Forward(ctx context.Context, req system.BrowserSessionRequest) (system.BrowserPageResult, error) {
	return m.runPage(ctx, req.SessionID, req.Timeout, chromedp.Evaluate("history.forward()", nil), chromedp.Sleep(500*time.Millisecond))
}

func (m *Manager) PDF(ctx context.Context, req system.BrowserSessionRequest) (system.BrowserArtifact, error) {
	sess, err := m.session(req.SessionID)
	if err != nil {
		return system.BrowserArtifact{}, err
	}
	runCtx, cancel := timeout(sess.ctx, req.Timeout)
	defer cancel()
	var data []byte
	if err := chromedp.Run(runCtx, chromedp.ActionFunc(func(ctx context.Context) error {
		var err error
		data, _, err = page.PrintToPDF().WithPrintBackground(true).Do(ctx)
		return err
	})); err != nil {
		_ = sess
		return system.BrowserArtifact{}, err
	}
	return m.writeArtifact(ctx, req.SessionID, "page", ".pdf", "application/pdf", data)
}

func (m *Manager) Close(_ context.Context, req system.BrowserSessionRequest) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	sess, ok := m.sessions[req.SessionID]
	if !ok {
		return fmt.Errorf("browser session %q not found", req.SessionID)
	}
	sess.cancel()
	delete(m.sessions, req.SessionID)
	return nil
}

func (m *Manager) runPage(ctx context.Context, id string, timeoutValue time.Duration, actions ...chromedp.Action) (system.BrowserPageResult, error) {
	location, err := m.run(ctx, id, timeoutValue, actions...)
	if err != nil {
		return system.BrowserPageResult{}, err
	}
	return system.BrowserPageResult{SessionID: id, URL: location.URL, Title: location.Title}, nil
}

type pageLocation struct {
	URL   string
	Title string
}

func (m *Manager) run(ctx context.Context, id string, timeoutValue time.Duration, actions ...chromedp.Action) (pageLocation, error) {
	_ = ctx
	sess, err := m.session(id)
	if err != nil {
		return pageLocation{}, err
	}
	runCtx, cancel := timeout(sess.ctx, timeoutValue)
	defer cancel()
	var title, location string
	all := append([]chromedp.Action{}, actions...)
	all = append(all, chromedp.Title(&title), chromedp.Location(&location))
	if err := chromedp.Run(runCtx, all...); err != nil {
		_ = sess
		return pageLocation{}, err
	}
	return pageLocation{URL: location, Title: title}, nil
}

func (m *Manager) session(id string) (*session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	sess, ok := m.sessions[id]
	if !ok {
		return nil, fmt.Errorf("browser session %q not found", id)
	}
	return sess, nil
}

func (m *Manager) writeArtifact(ctx context.Context, sessionID, prefix, ext, mediaType string, data []byte) (system.BrowserArtifact, error) {
	name := path.Join(".agents", "artifacts", "browser", sessionID, fmt.Sprintf("%s-%d%s", prefix, time.Now().UnixNano(), ext))
	resolved, err := m.workspace.WriteFile(ctx, name, data, 0644, true)
	if err != nil {
		return system.BrowserArtifact{}, err
	}
	return system.BrowserArtifact{SessionID: sessionID, Path: resolved.Rel, MediaType: mediaType, Bytes: len(data), Description: prefix}, nil
}

func timeout(ctx context.Context, value time.Duration) (context.Context, context.CancelFunc) {
	if ctx == nil {
		ctx = context.Background()
	}
	if value <= 0 {
		value = 30 * time.Second
	}
	return context.WithTimeout(ctx, value)
}

func validateBrowserURL(raw string) error {
	if raw == "about:blank" {
		return nil
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return err
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return fmt.Errorf("browser url must be http or https")
	}
	if parsed.Host == "" {
		return fmt.Errorf("browser url host is empty")
	}
	return nil
}
