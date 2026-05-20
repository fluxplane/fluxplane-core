package discovery

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	corediscovery "github.com/fluxplane/agentruntime/core/discovery"
	coreendpoint "github.com/fluxplane/agentruntime/core/endpoint"
	runtimeendpoint "github.com/fluxplane/agentruntime/runtime/endpoint"
)

const defaultRefreshInterval = 5 * time.Minute

// RunRequest asks the discovery runner to refresh providers.
type RunRequest struct {
	Providers  []string `json:"providers,omitempty"`
	Products   []string `json:"products,omitempty"`
	Namespaces []string `json:"namespaces,omitempty"`
	Force      bool     `json:"force,omitempty"`
	Reason     string   `json:"reason,omitempty"`
}

// EndpointChange is a compact model-safe endpoint change summary.
type EndpointChange struct {
	Ref       coreendpoint.Ref       `json:"ref"`
	URL       string                 `json:"url,omitempty"`
	Product   string                 `json:"product,omitempty"`
	Source    coreendpoint.SourceRef `json:"source,omitempty"`
	Metadata  map[string]string      `json:"metadata,omitempty"`
	ExpiresAt string                 `json:"expires_at,omitempty"`
}

// ProviderRunSummary summarizes one provider's latest refresh.
type ProviderRunSummary struct {
	Name       string `json:"name"`
	Product    string `json:"product,omitempty"`
	Error      string `json:"error,omitempty"`
	Candidates int    `json:"candidates"`
	Stored     int    `json:"stored"`
	Added      int    `json:"added"`
	Updated    int    `json:"updated"`
	Removed    int    `json:"removed"`
	StartedAt  string `json:"started_at,omitempty"`
	FinishedAt string `json:"finished_at,omitempty"`
	ElapsedMS  int64  `json:"elapsed_ms,omitempty"`
}

// RunSummary is the public status for one discovery refresh run.
type RunSummary struct {
	ID         string               `json:"id,omitempty"`
	Status     string               `json:"status"`
	Reason     string               `json:"reason,omitempty"`
	Providers  []ProviderRunSummary `json:"providers,omitempty"`
	Added      []EndpointChange     `json:"added,omitempty"`
	Updated    []EndpointChange     `json:"updated,omitempty"`
	Removed    []EndpointChange     `json:"removed,omitempty"`
	StartedAt  string               `json:"started_at,omitempty"`
	FinishedAt string               `json:"finished_at,omitempty"`
	Error      string               `json:"error,omitempty"`
}

// Runner refreshes discovery providers into an endpoint registry.
type Runner struct {
	discovery *Registry
	endpoints *runtimeendpoint.Registry
	interval  time.Duration

	mu      sync.RWMutex
	nextID  int64
	current *RunSummary
	latest  RunSummary
}

// NewRunner returns a discovery runner.
func NewRunner(discovery *Registry, endpoints *runtimeendpoint.Registry) *Runner {
	return &Runner{discovery: discovery, endpoints: endpoints, interval: defaultRefreshInterval}
}

// Start launches startup discovery and periodic refresh until ctx is canceled.
func (r *Runner) Start(ctx context.Context) {
	if r == nil {
		return
	}
	_ = r.Trigger(ctx, RunRequest{Reason: "startup"})
	interval := r.interval
	if interval <= 0 {
		interval = defaultRefreshInterval
	}
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				_ = r.Trigger(ctx, RunRequest{Reason: "refresh"})
			}
		}
	}()
}

// Trigger enqueues an async discovery refresh.
func (r *Runner) Trigger(ctx context.Context, req RunRequest) RunSummary {
	if r == nil {
		return RunSummary{Status: "failed", Error: "discovery runner is nil"}
	}
	if ctx == nil {
		ctx = context.Background()
	}
	r.mu.Lock()
	if r.current != nil && r.current.Status == "running" && !req.Force {
		current := cloneRunSummary(*r.current)
		r.mu.Unlock()
		return current
	}
	r.nextID++
	run := RunSummary{
		ID:        fmt.Sprintf("discovery-run-%d", r.nextID),
		Status:    "running",
		Reason:    firstNonEmpty(req.Reason, "manual"),
		StartedAt: time.Now().UTC().Format(time.RFC3339),
	}
	r.current = &run
	r.mu.Unlock()

	go r.run(ctx, req, run)
	return run
}

// DiscoverNow executes a refresh synchronously. It is primarily useful in
// tests and controlled callers; normal operations should use Trigger.
func (r *Runner) DiscoverNow(ctx context.Context, req RunRequest) RunSummary {
	if r == nil {
		return RunSummary{Status: "failed", Error: "discovery runner is nil"}
	}
	r.mu.Lock()
	r.nextID++
	run := RunSummary{
		ID:        fmt.Sprintf("discovery-run-%d", r.nextID),
		Status:    "running",
		Reason:    firstNonEmpty(req.Reason, "manual"),
		StartedAt: time.Now().UTC().Format(time.RFC3339),
	}
	r.current = &run
	r.mu.Unlock()
	r.run(ctx, req, run)
	return r.Status().Latest
}

// Status returns current and latest discovery run status.
func (r *Runner) Status() RunnerStatus {
	if r == nil {
		return RunnerStatus{}
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	var current *RunSummary
	if r.current != nil {
		cloned := cloneRunSummary(*r.current)
		current = &cloned
	}
	return RunnerStatus{Current: current, Latest: cloneRunSummary(r.latest)}
}

// RunnerStatus is the current discovery runner state.
type RunnerStatus struct {
	Current *RunSummary `json:"current,omitempty"`
	Latest  RunSummary  `json:"latest,omitempty"`
}

func (r *Runner) run(ctx context.Context, req RunRequest, run RunSummary) {
	if ctx == nil {
		ctx = context.Background()
	}
	if r.discovery == nil {
		run.Status = "failed"
		run.Error = "discovery registry is nil"
		r.finish(run)
		return
	}
	if r.endpoints == nil {
		run.Status = "failed"
		run.Error = "endpoint registry is nil"
		r.finish(run)
		return
	}
	products := normalizeList(req.Products)
	if len(products) == 0 {
		products = []string{""}
	}
	providerFilter := stringSet(req.Providers)
	var firstErr error
	for _, product := range products {
		providers := r.discovery.matchingProviders(product)
		for _, provider := range providers {
			spec := provider.Spec()
			if len(providerFilter) > 0 && !providerFilter[spec.Name] {
				continue
			}
			providerRun := r.runProvider(ctx, provider, spec, product, req)
			run.Providers = append(run.Providers, providerRun.summary)
			run.Added = append(run.Added, providerRun.added...)
			run.Updated = append(run.Updated, providerRun.updated...)
			run.Removed = append(run.Removed, providerRun.removed...)
			if providerRun.summary.Error != "" && firstErr == nil {
				firstErr = fmt.Errorf("%s: %s", providerRun.summary.Name, providerRun.summary.Error)
			}
		}
	}
	sortProviderRuns(run.Providers)
	sortEndpointChanges(run.Added)
	sortEndpointChanges(run.Updated)
	sortEndpointChanges(run.Removed)
	if firstErr != nil && len(run.Providers) == 0 {
		run.Status = "failed"
		run.Error = firstErr.Error()
	} else {
		run.Status = "completed"
		if firstErr != nil {
			run.Error = firstErr.Error()
		}
	}
	r.finish(run)
}

type providerRunResult struct {
	summary ProviderRunSummary
	added   []EndpointChange
	updated []EndpointChange
	removed []EndpointChange
}

func (r *Runner) runProvider(ctx context.Context, provider Provider, spec ProviderSpec, product string, req RunRequest) providerRunResult {
	started := time.Now()
	query := map[string]string{}
	if len(req.Namespaces) > 0 {
		query["namespaces"] = strings.Join(normalizeList(req.Namespaces), ",")
	}
	discoveryReq := corediscovery.Request{
		Op:        "refresh",
		Providers: []string{spec.Name},
		Product:   product,
		Query:     query,
	}
	result, err := provider.Discover(ctx, discoveryReq)
	r.discovery.recordRun(spec, len(result.Candidates), err)
	summary := ProviderRunSummary{
		Name:       spec.Name,
		Product:    product,
		Candidates: len(result.Candidates),
		StartedAt:  started.UTC().Format(time.RFC3339),
		FinishedAt: time.Now().UTC().Format(time.RFC3339),
		ElapsedMS:  time.Since(started).Milliseconds(),
	}
	if err != nil {
		summary.Error = err.Error()
		return providerRunResult{summary: summary}
	}
	records := make([]runtimeendpoint.Record, 0, len(result.Candidates))
	for _, candidate := range result.Candidates {
		record, ok := endpointRecordFromCandidate(spec.Name, product, candidate)
		if ok {
			records = append(records, record)
		}
	}
	changes, err := r.endpoints.ReplaceOwned(spec.Name, records, product == "")
	if err != nil {
		summary.Error = err.Error()
		return providerRunResult{summary: summary}
	}
	summary.Stored = len(records)
	summary.Added = len(changes.Added)
	summary.Updated = len(changes.Updated)
	summary.Removed = len(changes.Removed)
	return providerRunResult{
		summary: summary,
		added:   r.endpointChanges(changes.Added),
		updated: r.endpointChanges(changes.Updated),
		removed: endpointRefChanges(changes.Removed),
	}
}

func endpointRecordFromCandidate(provider, product string, candidate corediscovery.Candidate) (runtimeendpoint.Record, bool) {
	name := strings.TrimSpace(candidate.ID)
	if name == "" {
		name = candidate.Source.Kind + "-" + candidate.Source.Namespace + "-" + candidate.Source.Name
	}
	name = safeEndpointID(provider + "-" + name)
	if name == "" {
		return runtimeendpoint.Record{}, false
	}
	resolvedProduct := firstNonEmpty(candidate.ProductHint, product)
	protocol := firstNonEmpty(candidate.Protocol, candidate.Scheme)
	metadata := map[string]string{
		"provider": provider,
	}
	if resolvedProduct != "" {
		metadata["product"] = resolvedProduct
	}
	if candidate.Score != 0 {
		metadata["score"] = strconv.FormatFloat(candidate.Score, 'f', 1, 64)
	}
	if len(candidate.Reasons) > 0 {
		metadata["provenance"] = strings.Join(candidate.Reasons, ",")
	}
	if candidate.Host != "" {
		metadata["host"] = candidate.Host
	}
	if candidate.Port > 0 {
		metadata["port"] = strconv.Itoa(candidate.Port)
	}
	if candidate.PortName != "" {
		metadata["port_name"] = candidate.PortName
	}
	return runtimeendpoint.Record{
		Spec: coreendpoint.Spec{
			Name:        name,
			URL:         candidate.URL,
			Product:     resolvedProduct,
			Protocol:    protocol,
			AuthRef:     candidate.AuthRef,
			Labels:      candidate.Labels,
			Annotations: candidate.Annotations,
		},
		Source:   candidate.Source,
		Metadata: metadata,
		Owner:    provider,
	}, true
}

func (r *Runner) endpointChanges(refs []coreendpoint.Ref) []EndpointChange {
	out := make([]EndpointChange, 0, len(refs))
	for _, ref := range refs {
		if resolved, ok := r.endpoints.Resolve(ref); ok {
			out = append(out, EndpointChange{
				Ref:       resolved.Ref,
				URL:       resolved.URL,
				Product:   resolved.Metadata["product"],
				Source:    resolved.Source,
				Metadata:  cloneMap(resolved.Metadata),
				ExpiresAt: resolved.ExpiresAt,
			})
			continue
		}
		out = append(out, EndpointChange{Ref: ref})
	}
	return out
}

func endpointRefChanges(refs []coreendpoint.Ref) []EndpointChange {
	out := make([]EndpointChange, 0, len(refs))
	for _, ref := range refs {
		out = append(out, EndpointChange{Ref: ref})
	}
	return out
}

func (r *Runner) finish(run RunSummary) {
	run.FinishedAt = time.Now().UTC().Format(time.RFC3339)
	r.mu.Lock()
	defer r.mu.Unlock()
	r.latest = cloneRunSummary(run)
	if r.current != nil && r.current.ID == run.ID {
		r.current = nil
	}
}

func normalizeList(values []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func stringSet(values []string) map[string]bool {
	out := map[string]bool{}
	for _, value := range normalizeList(values) {
		out[value] = true
	}
	return out
}

func safeEndpointID(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var b strings.Builder
	dash := false
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			dash = false
		default:
			if !dash && b.Len() > 0 {
				b.WriteByte('-')
				dash = true
			}
		}
	}
	return strings.Trim(b.String(), "-")
}

func cloneRunSummary(in RunSummary) RunSummary {
	in.Providers = append([]ProviderRunSummary(nil), in.Providers...)
	in.Added = cloneEndpointChanges(in.Added)
	in.Updated = cloneEndpointChanges(in.Updated)
	in.Removed = cloneEndpointChanges(in.Removed)
	return in
}

func cloneEndpointChanges(in []EndpointChange) []EndpointChange {
	out := make([]EndpointChange, 0, len(in))
	for _, change := range in {
		change.Metadata = cloneMap(change.Metadata)
		change.Source.Attributes = cloneMap(change.Source.Attributes)
		out = append(out, change)
	}
	return out
}

func sortProviderRuns(runs []ProviderRunSummary) {
	sort.Slice(runs, func(i, j int) bool {
		if runs[i].Name == runs[j].Name {
			return runs[i].Product < runs[j].Product
		}
		return runs[i].Name < runs[j].Name
	})
}

func sortEndpointChanges(changes []EndpointChange) {
	sort.Slice(changes, func(i, j int) bool { return changes[i].Ref < changes[j].Ref })
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func cloneMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}
