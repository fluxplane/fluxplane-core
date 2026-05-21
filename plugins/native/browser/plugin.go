package browser

import (
	"context"
	"fmt"
	"strings"
	"time"

	coreevidence "github.com/fluxplane/engine/core/evidence"
	"github.com/fluxplane/engine/core/operation"
	"github.com/fluxplane/engine/core/resource"
	"github.com/fluxplane/engine/core/usage"
	"github.com/fluxplane/engine/orchestration/pluginhost"
	runtimeevidence "github.com/fluxplane/engine/runtime/evidence"
	operationruntime "github.com/fluxplane/engine/runtime/operation"
	"github.com/fluxplane/engine/runtime/system"
)

const (
	Name         = "browser"
	OpenOp       = "browser_open"
	NavigateOp   = "browser_navigate"
	ClickOp      = "browser_click"
	TypeOp       = "browser_type"
	SelectOp     = "browser_select"
	ReadOp       = "browser_read"
	ScreenshotOp = "browser_screenshot"
	EvaluateOp   = "browser_evaluate"
	WaitOp       = "browser_wait"
	ScrollOp     = "browser_scroll"
	HoverOp      = "browser_hover"
	BackOp       = "browser_back"
	ForwardOp    = "browser_forward"
	PDFOp        = "browser_pdf"
	CloseOp      = "browser_close"

	ObservationBrowserRuntime   = "browser.runtime"
	AssertionBrowserAvailable   = "capability.available"
	browserRuntimeObserverName  = "browser.runtime"
	browserAssertionDeriverName = "browser.availability"
)

// Plugin contributes browser automation operations.
type Plugin struct {
	system system.System
}

var _ pluginhost.Plugin = Plugin{}
var _ pluginhost.OperationContributor = Plugin{}
var _ pluginhost.ObserverContributor = Plugin{}
var _ pluginhost.AssertionDeriverContributor = Plugin{}

// New returns a browser plugin.
func New(sys system.System) Plugin { return Plugin{system: sys} }

// Manifest returns plugin metadata.
func (Plugin) Manifest() pluginhost.Manifest {
	return pluginhost.Manifest{Name: Name, Description: "Browser automation operations."}
}

// Contributions returns browser operation specs.
func (Plugin) Contributions(context.Context, pluginhost.Context) (resource.ContributionBundle, error) {
	specs := specs()
	return resource.ContributionBundle{
		OperationSets: []operation.Set{{Name: Name, Description: "Browser automation operations.", Operations: refs(specs)}},
		Operations:    specs,
		Observers: []coreevidence.ObserverSpec{{
			Name:            browserRuntimeObserverName,
			Description:     "Observes whether browser automation is configured for this runtime.",
			Environment:     coreevidence.Ref{Name: Name},
			Phase:           coreevidence.PhaseTurn,
			ObservableKinds: []string{ObservationBrowserRuntime},
			Dynamic:         true,
		}},
		AssertionDerivers: []coreevidence.AssertionDeriverSpec{{
			Name:             browserAssertionDeriverName,
			Description:      "Derives browser activation from stable runtime availability.",
			ObservationKinds: []string{ObservationBrowserRuntime},
			Assertions: []coreevidence.AssertionTemplate{
				{Kind: AssertionBrowserAvailable, Target: Name, Subject: coreevidence.Subject{Kind: coreevidence.SubjectCapability, Name: Name}},
			},
		}},
	}, nil
}

// EnvironmentObservers returns browser availability observers.
func (p Plugin) EnvironmentObservers(context.Context, pluginhost.Context) ([]runtimeevidence.Observer, error) {
	return []runtimeevidence.Observer{browserRuntimeObserver(p)}, nil
}

// AssertionDerivers returns browser availability derivers.
func (Plugin) AssertionDerivers(context.Context, pluginhost.Context) ([]runtimeevidence.AssertionDeriver, error) {
	return []runtimeevidence.AssertionDeriver{browserAvailabilityDeriver{}}, nil
}

// Operations returns executable browser operations.
func (p Plugin) Operations(context.Context, pluginhost.Context) ([]operation.Operation, error) {
	if p.system == nil {
		return nil, fmt.Errorf("browserplugin: system is nil")
	}
	return []operation.Operation{
		operationruntime.NewTypedResult[openInput, map[string]any](specByName(OpenOp), p.open()),
		operationruntime.NewTypedResult[sessionInput, map[string]any](specByName(NavigateOp), p.navigate()),
		operationruntime.NewTypedResult[selectorInput, map[string]any](specByName(ClickOp), p.click()),
		operationruntime.NewTypedResult[typeInput, map[string]any](specByName(TypeOp), p.typ()),
		operationruntime.NewTypedResult[selectInput, map[string]any](specByName(SelectOp), p.selectOption()),
		operationruntime.NewTypedResult[readInput, map[string]any](specByName(ReadOp), p.read()),
		operationruntime.NewTypedResult[sessionInput, map[string]any](specByName(ScreenshotOp), p.screenshot()),
		operationruntime.NewTypedResult[evaluateInput, map[string]any](specByName(EvaluateOp), p.evaluate()),
		operationruntime.NewTypedResult[waitInput, map[string]any](specByName(WaitOp), p.wait()),
		operationruntime.NewTypedResult[scrollInput, map[string]any](specByName(ScrollOp), p.scroll()),
		operationruntime.NewTypedResult[selectorInput, map[string]any](specByName(HoverOp), p.hover()),
		operationruntime.NewTypedResult[sessionInput, map[string]any](specByName(BackOp), p.back()),
		operationruntime.NewTypedResult[sessionInput, map[string]any](specByName(ForwardOp), p.forward()),
		operationruntime.NewTypedResult[sessionInput, map[string]any](specByName(PDFOp), p.pdf()),
		operationruntime.NewTypedResult[sessionInput, map[string]any](specByName(CloseOp), p.close()),
	}, nil
}

func specs() []operation.Spec {
	return []operation.Spec{
		spec[openInput](OpenOp, "Open a new browser session."),
		spec[sessionInput](NavigateOp, "Navigate a browser session to a URL."),
		spec[selectorInput](ClickOp, "Click an element by selector."),
		spec[typeInput](TypeOp, "Type text into an element by selector."),
		spec[selectInput](SelectOp, "Select option values in a select element."),
		spec[readInput](ReadOp, "Read text and HTML from the current page or selector."),
		spec[sessionInput](ScreenshotOp, "Capture a screenshot artifact."),
		spec[evaluateInput](EvaluateOp, "Evaluate JavaScript in the page."),
		spec[waitInput](WaitOp, "Wait for a selector or duration."),
		spec[scrollInput](ScrollOp, "Scroll the page."),
		spec[selectorInput](HoverOp, "Hover an element by selector."),
		spec[sessionInput](BackOp, "Navigate browser history back."),
		spec[sessionInput](ForwardOp, "Navigate browser history forward."),
		spec[sessionInput](PDFOp, "Capture a PDF artifact."),
		spec[sessionInput](CloseOp, "Close a browser session."),
	}
}

func spec[I any](name, description string) operation.Spec {
	return operationruntime.WithTypedContract[I, map[string]any](operation.Spec{
		Ref:         operation.Ref{Name: operation.Name(name)},
		Description: description,
		Semantics: operation.Semantics{
			Determinism: operation.DeterminismNonDeterministic,
			Effects:     operation.EffectSet{operation.EffectNetwork, operation.EffectReadExternal},
			Idempotency: operation.IdempotencyUnknown,
			Risk:        operation.RiskMedium,
		},
	})
}

func specByName(name string) operation.Spec {
	for _, spec := range specs() {
		if string(spec.Ref.Name) == name {
			return spec
		}
	}
	return operation.Spec{Ref: operation.Ref{Name: operation.Name(name)}}
}

func refs(specs []operation.Spec) []operation.Ref {
	out := make([]operation.Ref, 0, len(specs))
	for _, spec := range specs {
		out = append(out, spec.Ref)
	}
	return out
}

type BrowserRuntimeEvidence struct {
	Available bool   `json:"available"`
	Reason    string `json:"reason,omitempty"`
}

type browserRuntimeObserver struct {
	system system.System
}

func (o browserRuntimeObserver) Spec() coreevidence.ObserverSpec {
	return coreevidence.ObserverSpec{
		Name:            browserRuntimeObserverName,
		Description:     "Observes whether browser automation is configured for this runtime.",
		Environment:     coreevidence.Ref{Name: Name},
		Phase:           coreevidence.PhaseTurn,
		ObservableKinds: []string{ObservationBrowserRuntime},
		Dynamic:         true,
	}
}

func (o browserRuntimeObserver) Observe(_ context.Context, _ runtimeevidence.ObservationRequest) ([]coreevidence.Observation, error) {
	evidence := BrowserRuntimeEvidence{Available: o.system != nil && o.system.Browser() != nil}
	if !evidence.Available {
		evidence.Reason = "browser manager is not configured"
	}
	return []coreevidence.Observation{{
		ID:          "browser:runtime",
		Environment: coreevidence.Ref{Name: Name},
		Kind:        ObservationBrowserRuntime,
		Scope:       "runtime",
		Content:     evidence,
		At:          time.Now().UTC(),
	}}, nil
}

type browserAvailabilityDeriver struct{}

func (browserAvailabilityDeriver) Spec() coreevidence.AssertionDeriverSpec {
	return coreevidence.AssertionDeriverSpec{
		Name:             browserAssertionDeriverName,
		Description:      "Derives browser activation from stable runtime availability.",
		ObservationKinds: []string{ObservationBrowserRuntime},
	}
}

func (browserAvailabilityDeriver) Derive(_ context.Context, req runtimeevidence.AssertionDeriveRequest) ([]coreevidence.Assertion, error) {
	var available bool
	var ids []string
	var scope string
	for _, observation := range req.Observations {
		if observation.Kind != ObservationBrowserRuntime {
			continue
		}
		if browserRuntimeAvailable(observation.Content) {
			available = true
			ids = appendObservationID(ids, observation.ID)
			if scope == "" {
				scope = observation.Scope
			}
		}
	}
	if !available {
		return nil, nil
	}
	return []coreevidence.Assertion{{
		Kind:           AssertionBrowserAvailable,
		Target:         Name,
		Subject:        coreevidence.Subject{Kind: coreevidence.SubjectCapability, Name: Name},
		Scope:          scope,
		Environment:    coreevidence.Ref{Name: Name},
		Confidence:     1,
		ObservationIDs: ids,
		Metadata:       map[string]string{"capability": Name},
	}}, nil
}

func browserRuntimeAvailable(content any) bool {
	switch typed := content.(type) {
	case BrowserRuntimeEvidence:
		return typed.Available
	case *BrowserRuntimeEvidence:
		return typed != nil && typed.Available
	case map[string]any:
		available, _ := typed["available"].(bool)
		return available
	default:
		return false
	}
}

func appendObservationID(ids []string, id string) []string {
	if strings.TrimSpace(id) == "" {
		return ids
	}
	return append(ids, id)
}

type openInput struct {
	URL       string `json:"url,omitempty" jsonschema:"description=URL to open."`
	Width     int    `json:"width,omitempty" jsonschema:"description=Viewport width."`
	Height    int    `json:"height,omitempty" jsonschema:"description=Viewport height."`
	TimeoutMS int    `json:"timeout_ms,omitempty" jsonschema:"description=Timeout in milliseconds."`
}

type sessionInput struct {
	SessionID string `json:"session_id" jsonschema:"description=Browser session id.,required"`
	URL       string `json:"url,omitempty" jsonschema:"description=URL for navigation operations."`
	TimeoutMS int    `json:"timeout_ms,omitempty" jsonschema:"description=Timeout in milliseconds."`
}

type selectorInput struct {
	SessionID string `json:"session_id" jsonschema:"description=Browser session id.,required"`
	Selector  string `json:"selector" jsonschema:"description=CSS selector.,required"`
	TimeoutMS int    `json:"timeout_ms,omitempty" jsonschema:"description=Timeout in milliseconds."`
}

type typeInput struct {
	SessionID string `json:"session_id" jsonschema:"description=Browser session id.,required"`
	Selector  string `json:"selector" jsonschema:"description=CSS selector.,required"`
	Text      string `json:"text" jsonschema:"description=Text to type.,required"`
	Submit    bool   `json:"submit,omitempty" jsonschema:"description=Press Enter after typing."`
	TimeoutMS int    `json:"timeout_ms,omitempty" jsonschema:"description=Timeout in milliseconds."`
}

type selectInput struct {
	SessionID string   `json:"session_id" jsonschema:"description=Browser session id.,required"`
	Selector  string   `json:"selector" jsonschema:"description=CSS selector.,required"`
	Values    []string `json:"values" jsonschema:"description=Option values to select.,required"`
	TimeoutMS int      `json:"timeout_ms,omitempty" jsonschema:"description=Timeout in milliseconds."`
}

type readInput struct {
	SessionID string `json:"session_id" jsonschema:"description=Browser session id.,required"`
	Selector  string `json:"selector,omitempty" jsonschema:"description=Optional CSS selector to read."`
	TimeoutMS int    `json:"timeout_ms,omitempty" jsonschema:"description=Timeout in milliseconds."`
}

type evaluateInput struct {
	SessionID string `json:"session_id" jsonschema:"description=Browser session id.,required"`
	Script    string `json:"script" jsonschema:"description=JavaScript expression or function body.,required"`
	TimeoutMS int    `json:"timeout_ms,omitempty" jsonschema:"description=Timeout in milliseconds."`
}

type waitInput struct {
	SessionID  string `json:"session_id" jsonschema:"description=Browser session id.,required"`
	Selector   string `json:"selector,omitempty" jsonschema:"description=Optional selector to wait for."`
	DurationMS int    `json:"duration_ms,omitempty" jsonschema:"description=Optional duration to wait."`
	TimeoutMS  int    `json:"timeout_ms,omitempty" jsonschema:"description=Timeout in milliseconds."`
}

type scrollInput struct {
	SessionID string `json:"session_id" jsonschema:"description=Browser session id.,required"`
	X         int    `json:"x,omitempty" jsonschema:"description=Horizontal pixels."`
	Y         int    `json:"y,omitempty" jsonschema:"description=Vertical pixels."`
	TimeoutMS int    `json:"timeout_ms,omitempty" jsonschema:"description=Timeout in milliseconds."`
}

func (p Plugin) browser() (system.BrowserManager, operation.Result) {
	browser := p.system.Browser()
	if browser == nil {
		return nil, operation.Failed("browser_not_configured", "browser manager is not configured", nil)
	}
	return browser, operation.Result{}
}

func (p Plugin) open() operationruntime.TypedResultHandler[openInput, map[string]any] {
	return func(ctx operation.Context, req openInput) operation.Result {
		browser, failed := p.browser()
		if browser == nil {
			return failed
		}
		out, err := browser.Open(ctx, system.BrowserOpenRequest{URL: req.URL, Width: req.Width, Height: req.Height, Timeout: duration(req.TimeoutMS, 30*time.Second)})
		if err != nil {
			return operation.Failed("browser_open_failed", err.Error(), nil)
		}
		emitBrowserUsage(ctx, OpenOp, out.URL, 1)
		return pageResult(OpenOp, out.SessionID, out.URL, out.Title, nil)
	}
}

func (p Plugin) navigate() operationruntime.TypedResultHandler[sessionInput, map[string]any] {
	return func(ctx operation.Context, req sessionInput) operation.Result {
		browser, failed := p.browser()
		if browser == nil {
			return failed
		}
		if strings.TrimSpace(req.URL) == "" {
			return operation.Failed("invalid_browser_input", "url is required", nil)
		}
		out, err := browser.Navigate(ctx, sessionReq(req))
		if err != nil {
			return operation.Failed("browser_navigate_failed", err.Error(), nil)
		}
		emitBrowserUsage(ctx, NavigateOp, out.URL, 1)
		return pageResult(NavigateOp, out.SessionID, out.URL, out.Title, nil)
	}
}

func (p Plugin) click() operationruntime.TypedResultHandler[selectorInput, map[string]any] {
	return func(ctx operation.Context, req selectorInput) operation.Result {
		return p.selectorAction(ctx, ClickOp, req, func(browser system.BrowserManager, ctx context.Context, req system.BrowserSelectorRequest) (system.BrowserPageResult, error) {
			return browser.Click(ctx, req)
		})
	}
}

func (p Plugin) hover() operationruntime.TypedResultHandler[selectorInput, map[string]any] {
	return func(ctx operation.Context, req selectorInput) operation.Result {
		return p.selectorAction(ctx, HoverOp, req, func(browser system.BrowserManager, ctx context.Context, req system.BrowserSelectorRequest) (system.BrowserPageResult, error) {
			return browser.Hover(ctx, req)
		})
	}
}

func (p Plugin) selectorAction(ctx operation.Context, name string, req selectorInput, fn func(system.BrowserManager, context.Context, system.BrowserSelectorRequest) (system.BrowserPageResult, error)) operation.Result {
	if strings.TrimSpace(req.Selector) == "" || strings.TrimSpace(req.SessionID) == "" {
		return operation.Failed("invalid_browser_input", "session_id and selector are required", nil)
	}
	browser, failed := p.browser()
	if browser == nil {
		return failed
	}
	out, err := fn(browser, ctx, system.BrowserSelectorRequest{SessionID: req.SessionID, Selector: req.Selector, Timeout: duration(req.TimeoutMS, 30*time.Second)})
	if err != nil {
		return operation.Failed(name+"_failed", err.Error(), nil)
	}
	emitBrowserUsage(ctx, name, out.URL, 1)
	return pageResult(name, out.SessionID, out.URL, out.Title, map[string]any{"selector": req.Selector})
}

func (p Plugin) typ() operationruntime.TypedResultHandler[typeInput, map[string]any] {
	return func(ctx operation.Context, req typeInput) operation.Result {
		browser, failed := p.browser()
		if browser == nil {
			return failed
		}
		out, err := browser.Type(ctx, system.BrowserTypeRequest{SessionID: req.SessionID, Selector: req.Selector, Text: req.Text, Submit: req.Submit, Timeout: duration(req.TimeoutMS, 30*time.Second)})
		if err != nil {
			return operation.Failed("browser_type_failed", err.Error(), nil)
		}
		return pageResult(TypeOp, out.SessionID, out.URL, out.Title, map[string]any{"selector": req.Selector})
	}
}

func (p Plugin) selectOption() operationruntime.TypedResultHandler[selectInput, map[string]any] {
	return func(ctx operation.Context, req selectInput) operation.Result {
		browser, failed := p.browser()
		if browser == nil {
			return failed
		}
		out, err := browser.Select(ctx, system.BrowserSelectRequest{SessionID: req.SessionID, Selector: req.Selector, Values: req.Values, Timeout: duration(req.TimeoutMS, 30*time.Second)})
		if err != nil {
			return operation.Failed("browser_select_failed", err.Error(), nil)
		}
		return pageResult(SelectOp, out.SessionID, out.URL, out.Title, map[string]any{"selector": req.Selector, "values": req.Values})
	}
}

func (p Plugin) read() operationruntime.TypedResultHandler[readInput, map[string]any] {
	return func(ctx operation.Context, req readInput) operation.Result {
		browser, failed := p.browser()
		if browser == nil {
			return failed
		}
		out, err := browser.Read(ctx, system.BrowserReadRequest{SessionID: req.SessionID, Selector: req.Selector, Timeout: duration(req.TimeoutMS, 30*time.Second)})
		if err != nil {
			return operation.Failed("browser_read_failed", err.Error(), nil)
		}
		data := map[string]any{"session_id": out.SessionID, "url": out.URL, "title": out.Title, "text": out.Text, "html": out.HTML}
		text := fmt.Sprintf("[browser_read session=%s title=%s url=%s]\n%s", out.SessionID, out.Title, out.URL, out.Text)
		return operation.OK(operation.Rendered{Text: strings.TrimSpace(text), Data: data})
	}
}

func (p Plugin) screenshot() operationruntime.TypedResultHandler[sessionInput, map[string]any] {
	return func(ctx operation.Context, req sessionInput) operation.Result {
		return p.artifact(ctx, ScreenshotOp, req, func(browser system.BrowserManager, ctx context.Context, req system.BrowserSessionRequest) (system.BrowserArtifact, error) {
			return browser.Screenshot(ctx, req)
		})
	}
}

func (p Plugin) pdf() operationruntime.TypedResultHandler[sessionInput, map[string]any] {
	return func(ctx operation.Context, req sessionInput) operation.Result {
		return p.artifact(ctx, PDFOp, req, func(browser system.BrowserManager, ctx context.Context, req system.BrowserSessionRequest) (system.BrowserArtifact, error) {
			return browser.PDF(ctx, req)
		})
	}
}

func (p Plugin) artifact(ctx operation.Context, name string, req sessionInput, fn func(system.BrowserManager, context.Context, system.BrowserSessionRequest) (system.BrowserArtifact, error)) operation.Result {
	browser, failed := p.browser()
	if browser == nil {
		return failed
	}
	out, err := fn(browser, ctx, sessionReq(req))
	if err != nil {
		return operation.Failed(name+"_failed", err.Error(), nil)
	}
	data := map[string]any{"session_id": out.SessionID, "path": out.Path, "media_type": out.MediaType, "bytes": out.Bytes, "description": out.Description}
	text := fmt.Sprintf("%s wrote %s (%s, %d bytes)", name, out.Path, out.MediaType, out.Bytes)
	return operation.OK(operation.Rendered{Text: text, Data: data})
}

func (p Plugin) evaluate() operationruntime.TypedResultHandler[evaluateInput, map[string]any] {
	return func(ctx operation.Context, req evaluateInput) operation.Result {
		browser, failed := p.browser()
		if browser == nil {
			return failed
		}
		out, err := browser.Evaluate(ctx, system.BrowserEvaluateRequest{SessionID: req.SessionID, Script: req.Script, Timeout: duration(req.TimeoutMS, 30*time.Second)})
		if err != nil {
			return operation.Failed("browser_evaluate_failed", err.Error(), nil)
		}
		return operation.OK(operation.Rendered{Text: fmt.Sprintf("Evaluation result: %v", out.Value), Data: map[string]any{"session_id": out.SessionID, "value": out.Value}})
	}
}

func (p Plugin) wait() operationruntime.TypedResultHandler[waitInput, map[string]any] {
	return func(ctx operation.Context, req waitInput) operation.Result {
		browser, failed := p.browser()
		if browser == nil {
			return failed
		}
		out, err := browser.Wait(ctx, system.BrowserWaitRequest{SessionID: req.SessionID, Selector: req.Selector, Duration: duration(req.DurationMS, 0), Timeout: duration(req.TimeoutMS, 30*time.Second)})
		if err != nil {
			return operation.Failed("browser_wait_failed", err.Error(), nil)
		}
		return pageResult(WaitOp, out.SessionID, out.URL, out.Title, nil)
	}
}

func (p Plugin) scroll() operationruntime.TypedResultHandler[scrollInput, map[string]any] {
	return func(ctx operation.Context, req scrollInput) operation.Result {
		browser, failed := p.browser()
		if browser == nil {
			return failed
		}
		out, err := browser.Scroll(ctx, system.BrowserScrollRequest{SessionID: req.SessionID, X: req.X, Y: req.Y, Timeout: duration(req.TimeoutMS, 30*time.Second)})
		if err != nil {
			return operation.Failed("browser_scroll_failed", err.Error(), nil)
		}
		return pageResult(ScrollOp, out.SessionID, out.URL, out.Title, map[string]any{"x": req.X, "y": req.Y})
	}
}

func (p Plugin) back() operationruntime.TypedResultHandler[sessionInput, map[string]any] {
	return func(ctx operation.Context, req sessionInput) operation.Result {
		return p.sessionAction(ctx, BackOp, req, func(browser system.BrowserManager, ctx context.Context, req system.BrowserSessionRequest) (system.BrowserPageResult, error) {
			return browser.Back(ctx, req)
		})
	}
}

func (p Plugin) forward() operationruntime.TypedResultHandler[sessionInput, map[string]any] {
	return func(ctx operation.Context, req sessionInput) operation.Result {
		return p.sessionAction(ctx, ForwardOp, req, func(browser system.BrowserManager, ctx context.Context, req system.BrowserSessionRequest) (system.BrowserPageResult, error) {
			return browser.Forward(ctx, req)
		})
	}
}

func (p Plugin) sessionAction(ctx operation.Context, name string, req sessionInput, fn func(system.BrowserManager, context.Context, system.BrowserSessionRequest) (system.BrowserPageResult, error)) operation.Result {
	browser, failed := p.browser()
	if browser == nil {
		return failed
	}
	out, err := fn(browser, ctx, sessionReq(req))
	if err != nil {
		return operation.Failed(name+"_failed", err.Error(), nil)
	}
	return pageResult(name, out.SessionID, out.URL, out.Title, nil)
}

func (p Plugin) close() operationruntime.TypedResultHandler[sessionInput, map[string]any] {
	return func(ctx operation.Context, req sessionInput) operation.Result {
		browser, failed := p.browser()
		if browser == nil {
			return failed
		}
		if err := browser.Close(ctx, sessionReq(req)); err != nil {
			return operation.Failed("browser_close_failed", err.Error(), nil)
		}
		return operation.OK(operation.Rendered{Text: "Closed browser session " + req.SessionID, Data: map[string]any{"session_id": req.SessionID}})
	}
}

func sessionReq(req sessionInput) system.BrowserSessionRequest {
	return system.BrowserSessionRequest{SessionID: req.SessionID, URL: req.URL, Timeout: duration(req.TimeoutMS, 30*time.Second)}
}

func duration(ms int, fallback time.Duration) time.Duration {
	if ms <= 0 {
		return fallback
	}
	d := time.Duration(ms) * time.Millisecond
	if d > 2*time.Minute {
		return 2 * time.Minute
	}
	return d
}

func pageResult(source, sessionID, url, title string, extra map[string]any) operation.Result {
	data := map[string]any{"session_id": sessionID, "url": url, "title": title}
	for key, value := range extra {
		data[key] = value
	}
	text := fmt.Sprintf("%s session=%s title=%s url=%s", source, sessionID, title, url)
	return operation.OK(operation.Rendered{Text: strings.TrimSpace(text), Data: data})
}

func emitBrowserUsage(ctx operation.Context, source, target string, count float64) {
	ctx.Events().Emit(usage.Recorded{
		Source:       source,
		Subject:      usage.Subject{Kind: usage.SubjectNetwork, Name: target},
		Measurements: []usage.Measurement{{Metric: usage.MetricRequests, Quantity: count, Unit: usage.UnitRequest}},
	})
}
