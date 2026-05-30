package loki

import (
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	fpsystem "github.com/fluxplane/fluxplane-system"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	corediscovery "github.com/fluxplane/fluxplane-core/core/discovery"
	coreendpoint "github.com/fluxplane/fluxplane-core/core/endpoint"
	"github.com/fluxplane/fluxplane-core/core/operation"
	runtimeendpoint "github.com/fluxplane/fluxplane-core/runtime/endpoint"
	operationruntime "github.com/fluxplane/fluxplane-core/runtime/operation"
	"github.com/fluxplane/fluxplane-policy"
)

type TestInput struct {
	URL         string `json:"url,omitempty"`
	EndpointRef string `json:"endpoint_ref,omitempty"`
	TenantID    string `json:"tenant_id,omitempty"`
	Timeout     string `json:"timeout,omitempty"`
}

type TestOutput struct {
	URL       string `json:"url"`
	Reachable bool   `json:"reachable"`
	Ready     bool   `json:"ready"`
	Version   string `json:"version,omitempty"`
	LatencyMS int64  `json:"latency_ms,omitempty"`
	Error     string `json:"error,omitempty"`
}

type LabelsInput struct {
	URL           string `json:"url,omitempty"`
	EndpointRef   string `json:"endpoint_ref,omitempty"`
	Label         string `json:"label,omitempty"`
	Namespace     string `json:"namespace,omitempty"`
	AllNamespaces bool   `json:"all_namespaces,omitempty"`
	Since         string `json:"since,omitempty"`
	Until         string `json:"until,omitempty"`
	Limit         int    `json:"limit,omitempty"`
}

type LabelsOutput struct {
	URL    string   `json:"url"`
	Label  string   `json:"label,omitempty"`
	Values []string `json:"values,omitempty"`
}

type QueryInput struct {
	URL           string   `json:"url,omitempty"`
	EndpointRef   string   `json:"endpoint_ref,omitempty"`
	Query         string   `json:"query"`
	Namespace     string   `json:"namespace,omitempty"`
	AllNamespaces bool     `json:"all_namespaces,omitempty"`
	Since         string   `json:"since,omitempty"`
	Until         string   `json:"until,omitempty"`
	Limit         int      `json:"limit,omitempty"`
	Direction     string   `json:"direction,omitempty"`
	Labels        []string `json:"labels,omitempty"`
	Parse         string   `json:"parse,omitempty"`
}

type RecentLogsInput struct {
	URL           string   `json:"url,omitempty"`
	EndpointRef   string   `json:"endpoint_ref,omitempty"`
	App           string   `json:"app,omitempty"`
	Namespace     string   `json:"namespace,omitempty"`
	AllNamespaces bool     `json:"all_namespaces,omitempty"`
	Pod           string   `json:"pod,omitempty"`
	Container     string   `json:"container,omitempty"`
	Contains      string   `json:"contains,omitempty"`
	Regex         string   `json:"regex,omitempty"`
	Since         string   `json:"since,omitempty"`
	Limit         int      `json:"limit,omitempty"`
	Levels        []string `json:"levels,omitempty"`
	RequestID     string   `json:"request_id,omitempty"`
	TraceID       string   `json:"trace_id,omitempty"`
}

type Stream struct {
	ID             string            `json:"id"`
	Labels         map[string]string `json:"labels,omitempty"`
	Namespace      string            `json:"namespace,omitempty"`
	App            string            `json:"app,omitempty"`
	Pod            string            `json:"pod,omitempty"`
	Container      string            `json:"container,omitempty"`
	FirstTimestamp string            `json:"first_timestamp,omitempty"`
	LastTimestamp  string            `json:"last_timestamp,omitempty"`
	EntryCount     int               `json:"entry_count,omitempty"`
}

type LogEntry struct {
	ID        string            `json:"id"`
	Timestamp string            `json:"timestamp"`
	Labels    map[string]string `json:"labels,omitempty"`
	Namespace string            `json:"namespace,omitempty"`
	App       string            `json:"app,omitempty"`
	Pod       string            `json:"pod,omitempty"`
	Container string            `json:"container,omitempty"`
	Line      string            `json:"line"`
	Level     string            `json:"level,omitempty"`
	Message   string            `json:"message,omitempty"`
	RequestID string            `json:"request_id,omitempty"`
	TraceID   string            `json:"trace_id,omitempty"`
}

type QueryOutput struct {
	URL             string     `json:"url"`
	NormalizedQuery string     `json:"normalized_query"`
	Streams         []Stream   `json:"streams,omitempty"`
	Entries         []LogEntry `json:"entries,omitempty"`
	Stats           any        `json:"stats,omitempty"`
	Truncated       bool       `json:"truncated,omitempty"`
	Limit           int        `json:"limit"`
}

func (p Plugin) test() operationruntime.TypedResultHandler[TestInput, TestOutput] {
	return func(ctx operation.Context, in TestInput) operation.Result {
		client, target, err := p.clientFor(ctx, in.URL, in.EndpointRef, in.TenantID, in.Timeout)
		if err != nil {
			return operation.Failed("loki_test_failed", err.Error(), nil)
		}
		result := client.test(ctx)
		return operation.OK(TestOutput{URL: target, Reachable: result.Reachable, Ready: result.Ready, Version: result.Version, LatencyMS: result.LatencyMS, Error: result.Error})
	}
}

func (p Plugin) labels() operationruntime.TypedResultHandler[LabelsInput, LabelsOutput] {
	return func(ctx operation.Context, in LabelsInput) operation.Result {
		client, target, err := p.clientFor(ctx, in.URL, in.EndpointRef, "", "")
		if err != nil {
			return operation.Failed("loki_labels_failed", err.Error(), nil)
		}
		start, end, err := p.window(in.Since, in.Until)
		if err != nil {
			return operation.Failed("loki_labels_failed", err.Error(), nil)
		}
		values, err := client.labels(ctx, in.Label, start, end, p.limit(in.Limit))
		if err != nil {
			return operation.Failed("loki_labels_failed", err.Error(), nil)
		}
		sort.Strings(values)
		return operation.OK(LabelsOutput{URL: target, Label: in.Label, Values: values})
	}
}

func (p Plugin) query() operationruntime.TypedResultHandler[QueryInput, QueryOutput] {
	return func(ctx operation.Context, in QueryInput) operation.Result {
		out, err := p.runQuery(ctx, in)
		if err != nil {
			return operation.Failed("loki_query_failed", err.Error(), nil)
		}
		return operation.OK(out)
	}
}

func (p Plugin) recentLogs() operationruntime.TypedResultHandler[RecentLogsInput, QueryOutput] {
	return func(ctx operation.Context, in RecentLogsInput) operation.Result {
		query, err := recentLogsQuery(in, p.cfg.DefaultNamespace)
		if err != nil {
			return operation.Failed("loki_recent_logs_failed", err.Error(), nil)
		}
		out, err := p.runQuery(ctx, QueryInput{
			URL: in.URL, EndpointRef: in.EndpointRef, Query: query, Namespace: in.Namespace,
			AllNamespaces: in.AllNamespaces, Since: in.Since, Limit: in.Limit, Direction: "backward",
		})
		if err != nil {
			return operation.Failed("loki_recent_logs_failed", err.Error(), nil)
		}
		return operation.OK(out)
	}
}

type discoveryRequest struct {
	Namespaces []string
	Limit      int
	Probe      bool
}

type discoveryResult struct {
	EndpointRefs []coreendpoint.Ref
	Candidates   []corediscovery.Candidate
	Probes       []corediscovery.ProbeResult
	SelectedURL  string
}

func (p Plugin) discoverEndpoints(ctx operation.Context, in discoveryRequest) (discoveryResult, error) {
	cfg := normalizeConfig(p.cfg)
	if !cfg.AutoDiscover.Enabled {
		return discoveryResult{}, fmt.Errorf("loki auto-discovery is disabled")
	}
	if p.discovery == nil {
		return discoveryResult{}, fmt.Errorf("loki discovery registry is nil")
	}
	limit := in.Limit
	if limit <= 0 {
		limit = 10
	}
	namespaces := in.Namespaces
	if len(namespaces) == 0 {
		namespaces = cfg.AutoDiscover.Namespaces
	}
	query := map[string]string{
		"allow_pod_ip": fmt.Sprintf("%t", cfg.AutoDiscover.AllowPodIP),
	}
	if len(namespaces) > 0 {
		query["namespaces"] = strings.Join(namespaces, ",")
	}
	discovered, err := p.discovery.Discover(ctx, corediscovery.Request{Product: "loki", Query: query, Limit: limit})
	if err != nil {
		return discoveryResult{}, err
	}
	var probes []corediscovery.ProbeResult
	var refs []coreendpoint.Ref
	selected := ""
	for _, candidate := range discovered.Candidates {
		probeCandidate := candidate
		if in.Probe {
			probe := p.probeCandidate(ctx, probeCandidate)
			if !usableProbe(probe) && cfg.AutoDiscover.Kubernetes && cfg.AutoDiscover.PortForward {
				if forwarded, err := p.portForwardCandidate(ctx, candidate); err == nil {
					forwardedProbe := p.probeCandidateWithRetry(ctx, forwarded, 5*time.Second)
					probes = append(probes, probe)
					probe = forwardedProbe
					probeCandidate = forwarded
				}
			}
			probes = append(probes, probe)
			if !usableProbe(probe) {
				continue
			}
		}
		ref, err := p.endpointRegistry().Put(runtimeendpoint.Record{
			Spec:     coreendpoint.Spec{Name: "loki-" + probeCandidate.ID, URL: probeCandidate.URL, Product: "loki", Protocol: "http", Labels: probeCandidate.Labels, Annotations: probeCandidate.Annotations},
			Source:   probeCandidate.Source,
			Metadata: endpointMetadata(candidate, probeCandidate),
		})
		if err == nil {
			refs = append(refs, ref)
		}
		if selected == "" {
			selected = probeCandidate.URL
		}
	}
	if in.Probe && selected == "" && len(discovered.Candidates) > 0 {
		return discoveryResult{Candidates: discovered.Candidates, Probes: probes}, fmt.Errorf("no usable Loki endpoint discovered")
	}
	return discoveryResult{EndpointRefs: refs, Candidates: discovered.Candidates, Probes: probes, SelectedURL: selected}, nil
}

func usableProbe(probe corediscovery.ProbeResult) bool {
	return probe.Status == "ready" || probe.Status == "reachable"
}

func (p Plugin) probeCandidate(ctx operation.Context, candidate corediscovery.Candidate) corediscovery.ProbeResult {
	timeout := p.cfg.AutoDiscover.ProbeTimeout
	client := lokiClient{network: p.network, baseURL: candidate.URL, timeout: durationOrDefault(timeout, 5*time.Second)}
	test := client.test(ctx)
	status := "failed"
	if test.Ready {
		status = "ready"
	} else if test.Reachable && test.Error == "" {
		status = "reachable"
	}
	return corediscovery.ProbeResult{
		CandidateID: candidate.ID,
		Probe:       corediscovery.ProbeSpec{Product: "loki", Method: "GET", Path: "/ready", ExpectedCodes: []int{200}, Timeout: timeout},
		Status:      status,
		LatencyMS:   test.LatencyMS,
		Product:     "loki",
		Version:     test.Version,
		Error:       test.Error,
	}
}

func (p Plugin) probeCandidateWithRetry(ctx operation.Context, candidate corediscovery.Candidate, timeout time.Duration) corediscovery.ProbeResult {
	deadline := time.Now().Add(timeout)
	var last corediscovery.ProbeResult
	for {
		last = p.probeCandidate(ctx, candidate)
		if usableProbe(last) || time.Now().After(deadline) {
			return last
		}
		select {
		case <-ctx.Done():
			last.Error = ctx.Err().Error()
			return last
		case <-time.After(250 * time.Millisecond):
		}
	}
}

func (p Plugin) portForwardCandidate(ctx operation.Context, candidate corediscovery.Candidate) (corediscovery.Candidate, error) {
	if p.process == nil {
		return corediscovery.Candidate{}, fmt.Errorf("loki kubernetes port-forward requires process manager")
	}
	namespace := strings.TrimSpace(candidate.Source.Namespace)
	name := strings.TrimSpace(candidate.Source.Name)
	if namespace == "" || name == "" {
		return corediscovery.Candidate{}, fmt.Errorf("loki kubernetes candidate is missing namespace or name")
	}
	kind, ok := portForwardKind(candidate.Source.Kind)
	if !ok {
		return corediscovery.Candidate{}, fmt.Errorf("loki kubernetes candidate kind %q cannot be port-forwarded", candidate.Source.Kind)
	}
	remotePort := candidate.Port
	if remotePort <= 0 {
		remotePort = portFromURL(candidate.URL)
	}
	if remotePort <= 0 {
		remotePort = 3100
	}
	localPort := localPortForCandidate(candidate, remotePort)
	args := []string{}
	if contextName := strings.TrimSpace(candidate.Source.Attributes["context"]); contextName != "" {
		args = append(args, "--context", contextName)
	}
	kubeconfig := strings.TrimSpace(candidate.Source.Attributes["kubeconfig"])
	if kubeconfig == "" {
		kubeconfig, _, _ = lookupEnv(ctx, p.environment, candidate.Source.Attributes["kubeconfig_env"])
	}
	if kubeconfig != "" {
		args = append(args, "--kubeconfig", kubeconfig)
	}
	resource := kind + "/" + name
	args = append(args, "-n", namespace, "port-forward", "--address", "127.0.0.1", resource, fmt.Sprintf("%d:%d", localPort, remotePort))
	label := fmt.Sprintf("loki-%s-%s-%d", namespace, name, remotePort)
	_, _, err := p.process.Ensure(ctx, fpsystem.ProcessRequest{
		Command:   "kubectl",
		Args:      args,
		Label:     label,
		Tags:      []string{"kubernetes", "loki", "port-forward"},
		Metadata:  map[string]string{"namespace": namespace, "kind": kind, "name": name, "remote_port": strconv.Itoa(remotePort), "local_port": strconv.Itoa(localPort)},
		MaxStdout: 16 * 1024,
		MaxStderr: 16 * 1024,
	})
	if err != nil {
		return corediscovery.Candidate{}, err
	}
	forwarded := candidate
	forwarded.URL = fmt.Sprintf("http://127.0.0.1:%d", localPort)
	forwarded.Host = "127.0.0.1"
	forwarded.Port = localPort
	forwarded.Reasons = append(append([]string(nil), candidate.Reasons...), "local_port_forward")
	forwarded.Annotations = cloneStringMap(candidate.Annotations)
	forwarded.Annotations["original_url"] = candidate.URL
	forwarded.Annotations["port_forward"] = "true"
	return forwarded, nil
}

func portForwardKind(kind string) (string, bool) {
	switch strings.TrimSpace(kind) {
	case "kubernetes.service":
		return "service", true
	case "kubernetes.pod":
		return "pod", true
	default:
		return "", false
	}
}

func portFromURL(value string) int {
	parsed, err := url.Parse(value)
	if err != nil {
		return 0
	}
	port, _ := strconv.Atoi(parsed.Port())
	return port
}

func localPortForCandidate(candidate corediscovery.Candidate, remotePort int) int {
	sum := sha1.Sum([]byte(candidate.ID + "|" + candidate.Source.Namespace + "|" + candidate.Source.Name + "|" + strconv.Itoa(remotePort)))
	offset := int(sum[0])<<8 | int(sum[1])
	return 20000 + offset%20000
}

func endpointMetadata(original, selected corediscovery.Candidate) map[string]string {
	metadata := map[string]string{"product": "loki", "score": fmt.Sprintf("%.1f", original.Score), "provenance": strings.Join(selected.Reasons, ",")}
	if selected.URL != original.URL {
		metadata["original_url"] = original.URL
		metadata["port_forward"] = "true"
	}
	return metadata
}

func cloneStringMap(values map[string]string) map[string]string {
	out := map[string]string{}
	for key, value := range values {
		out[key] = value
	}
	return out
}

func (p Plugin) clientFor(ctx operation.Context, inputURL, endpointRef, tenantID, timeout string) (lokiClient, string, error) {
	cfg := normalizeConfig(p.cfg)
	target := firstNonEmpty(inputURL, cfg.URL)
	ref := firstNonEmpty(endpointRef, cfg.EndpointRef)
	if endpointLike(target) {
		ref = target
		target = ""
	}
	if target == "" && ref != "" {
		resolved, ok := p.endpointRegistry().Resolve(coreendpoint.Ref(ref))
		if !ok {
			return lokiClient{}, "", fmt.Errorf("endpoint %q is not resolved", ref)
		}
		target = resolved.URL
	}
	if target == "" && cfg.URLEnv != "" {
		target, _, _ = lookupEnv(ctx, p.environment, cfg.URLEnv)
	}
	if target == "" && cfg.AutoDiscover.Enabled {
		if record, ok := p.selectDiscoveredEndpoint(); ok {
			target = record.Resolved.URL
		} else {
			discovered, err := p.discoverEndpoints(ctx, discoveryRequest{Probe: true, Limit: 3})
			if err != nil {
				return lokiClient{}, "", err
			}
			target = discovered.SelectedURL
		}
	}
	if target == "" {
		return lokiClient{}, "", fmt.Errorf("loki url is not configured")
	}
	if _, err := url.ParseRequestURI(target); err != nil {
		return lokiClient{}, "", fmt.Errorf("invalid loki url %q: %w", target, err)
	}
	tenant := firstNonEmpty(tenantID, cfg.TenantID)
	if tenant == "" && cfg.TenantIDEnv != "" {
		tenant, _, _ = lookupEnv(ctx, p.environment, cfg.TenantIDEnv)
	}
	return lokiClient{network: p.network, baseURL: target, tenantID: tenant, timeout: durationOrDefault(timeout, 10*time.Second)}, target, nil
}

func (p Plugin) selectDiscoveredEndpoint() (runtimeendpoint.Record, bool) {
	records := p.endpointRegistry().List("loki")
	if len(records) == 0 {
		return runtimeendpoint.Record{}, false
	}
	sort.SliceStable(records, func(i, j int) bool {
		leftMonitoring := records[i].Source.Namespace == "monitoring"
		rightMonitoring := records[j].Source.Namespace == "monitoring"
		if leftMonitoring != rightMonitoring {
			return leftMonitoring
		}
		leftScore := scoreMetadata(records[i].Metadata)
		rightScore := scoreMetadata(records[j].Metadata)
		if leftScore != rightScore {
			return leftScore > rightScore
		}
		return records[i].Resolved.Ref < records[j].Resolved.Ref
	})
	for _, record := range records {
		if strings.TrimSpace(record.Resolved.URL) != "" {
			return record, true
		}
	}
	return runtimeendpoint.Record{}, false
}

func scoreMetadata(metadata map[string]string) float64 {
	if len(metadata) == 0 {
		return 0
	}
	score, _ := strconv.ParseFloat(metadata["score"], 64)
	return score
}

func (p Plugin) runQuery(ctx operation.Context, in QueryInput) (QueryOutput, error) {
	query := strings.TrimSpace(in.Query)
	if query == "" {
		return QueryOutput{}, fmt.Errorf("query is required")
	}
	query = normalizeNamespaceSelector(query, firstNonEmpty(in.Namespace, p.cfg.DefaultNamespace), in.AllNamespaces)
	client, target, err := p.clientFor(ctx, in.URL, in.EndpointRef, "", "")
	if err != nil {
		return QueryOutput{}, err
	}
	start, end, err := p.window(in.Since, in.Until)
	if err != nil {
		return QueryOutput{}, err
	}
	limit := p.limit(in.Limit)
	direction := firstNonEmpty(in.Direction, "backward")
	resp, truncated, err := client.queryRange(ctx, query, start, end, limit, direction)
	if err != nil {
		return QueryOutput{}, err
	}
	streams, entries := normalizeQueryResult(resp.Data.Result, limit)
	if len(entries) >= limit {
		truncated = true
	}
	return QueryOutput{URL: target, NormalizedQuery: query, Streams: streams, Entries: entries, Stats: resp.Data.Stats, Truncated: truncated, Limit: limit}, nil
}

func (p Plugin) window(since, until string) (time.Time, time.Time, error) {
	end := time.Now()
	if strings.TrimSpace(until) != "" {
		parsed, err := time.Parse(time.RFC3339, strings.TrimSpace(until))
		if err != nil {
			return time.Time{}, time.Time{}, err
		}
		end = parsed
	}
	dur := durationOrDefault(firstNonEmpty(since, p.cfg.DefaultSince), time.Hour)
	return end.Add(-dur), end, nil
}

func (p Plugin) limit(limit int) int {
	cfg := normalizeConfig(p.cfg)
	if limit <= 0 {
		limit = cfg.DefaultLimit
	}
	if limit > cfg.MaxLimit {
		limit = cfg.MaxLimit
	}
	return limit
}

func (p Plugin) endpointRegistry() *runtimeendpoint.Registry {
	if p.endpoints != nil {
		return p.endpoints
	}
	return runtimeendpoint.NewRegistry(15 * time.Minute)
}

func recentLogsQuery(in RecentLogsInput, defaultNamespace string) (string, error) {
	labels := map[string]string{}
	if ns := firstNonEmpty(in.Namespace, defaultNamespace); ns != "" && !in.AllNamespaces {
		labels["namespace"] = ns
	}
	if in.App != "" {
		labels["app"] = in.App
	}
	if in.Pod != "" {
		labels["pod"] = in.Pod
	}
	if in.Container != "" {
		labels["container"] = in.Container
	}
	selector := selectorString(labels)
	if selector == "{}" {
		return "", fmt.Errorf("at least one selector field is required")
	}
	query := selector
	for _, value := range []string{in.Contains, in.RequestID, in.TraceID} {
		if strings.TrimSpace(value) != "" {
			query += " |= " + quoteLogQL(value)
		}
	}
	if pattern := levelPattern(in.Levels); pattern != "" {
		query += " |~ " + quoteLogQL(pattern)
	}
	if strings.TrimSpace(in.Regex) != "" {
		query += " |~ " + quoteLogQL(in.Regex)
	}
	return query, nil
}

func normalizeNamespaceSelector(query, namespace string, all bool) string {
	if all || strings.TrimSpace(namespace) == "" || strings.Contains(query, "namespace=") {
		return query
	}
	trimmed := strings.TrimSpace(query)
	if strings.HasPrefix(trimmed, "{") {
		end := strings.Index(trimmed, "}")
		if end > 0 && strings.TrimSpace(trimmed[1:end]) == "" {
			return "{namespace=" + quoteLogQL(namespace) + "}" + trimmed[end+1:]
		}
		return strings.Replace(trimmed, "{", "{namespace="+quoteLogQL(namespace)+",", 1)
	}
	return `{namespace=` + quoteLogQL(namespace) + `} ` + trimmed
}

func selectorString(labels map[string]string) string {
	if len(labels) == 0 {
		return "{}"
	}
	keys := make([]string, 0, len(labels))
	for key := range labels {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, key+"="+quoteLogQL(labels[key]))
	}
	return "{" + strings.Join(parts, ",") + "}"
}

func quoteLogQL(value string) string {
	return strconv.Quote(value)
}

func levelPattern(levels []string) string {
	var parts []string
	for _, level := range levels {
		level = strings.ToLower(strings.TrimSpace(level))
		if level == "" {
			continue
		}
		parts = append(parts, regexp.QuoteMeta(level))
	}
	if len(parts) == 0 {
		return ""
	}
	return `(?i)\b(` + strings.Join(parts, "|") + `)\b`
}

func normalizeQueryResult(in []lokiStream, limit int) ([]Stream, []LogEntry) {
	var streams []Stream
	var entries []LogEntry
	for _, stream := range in {
		summary := Stream{ID: labelsID(stream.Stream), Labels: cloneMap(stream.Stream), Namespace: stream.Stream["namespace"], App: firstNonEmpty(stream.Stream["app"], stream.Stream["app_kubernetes_io_name"]), Pod: stream.Stream["pod"], Container: stream.Stream["container"], EntryCount: len(stream.Values)}
		for i, value := range stream.Values {
			if len(value) < 2 {
				continue
			}
			ts := parseLokiTimestamp(value[0])
			if i == 0 {
				summary.FirstTimestamp = ts
			}
			summary.LastTimestamp = ts
			if len(entries) >= limit {
				continue
			}
			line := value[1]
			entries = append(entries, LogEntry{ID: labelsID(stream.Stream) + ":" + value[0] + ":" + shortHash(line), Timestamp: ts, Labels: cloneMap(stream.Stream), Namespace: summary.Namespace, App: summary.App, Pod: summary.Pod, Container: summary.Container, Line: line, Level: extractLevel(line), Message: line, RequestID: extractField(line, "request_id"), TraceID: extractField(line, "trace_id")})
		}
		streams = append(streams, summary)
	}
	return streams, entries
}

func parseLokiTimestamp(value string) string {
	ns, err := time.ParseDuration(value + "ns")
	if err != nil {
		return value
	}
	return time.Unix(0, int64(ns)).UTC().Format(time.RFC3339Nano)
}

var levelRE = regexp.MustCompile(`(?i)\b(trace|debug|info|warn|warning|error|fatal)\b`)

func extractLevel(line string) string {
	match := levelRE.FindString(line)
	return strings.ToLower(match)
}

func extractField(line, key string) string {
	for _, sep := range []string{"=", ":"} {
		prefix := key + sep
		idx := strings.Index(line, prefix)
		if idx < 0 {
			continue
		}
		value := strings.Trim(line[idx+len(prefix):], ` "'`)
		if cut := strings.IndexAny(value, " ,}"); cut >= 0 {
			value = value[:cut]
		}
		return value
	}
	return ""
}

func (p Plugin) lokiNetworkAccess(ctx operation.Context, input any) ([]operationruntime.AccessDescriptor, error) {
	target := p.accessTarget(ctx, input)
	return []operationruntime.AccessDescriptor{operationruntime.NetworkDescriptor(target, policy.ActionNetworkFetch)}, nil
}

func (p Plugin) accessTarget(ctx operation.Context, input any) string {
	cfg := normalizeConfig(p.cfg)
	urlValue := ""
	endpointRef := ""
	switch in := input.(type) {
	case TestInput:
		urlValue = in.URL
		endpointRef = in.EndpointRef
	case LabelsInput:
		urlValue = in.URL
		endpointRef = in.EndpointRef
	case QueryInput:
		urlValue = in.URL
		endpointRef = in.EndpointRef
	case RecentLogsInput:
		urlValue = in.URL
		endpointRef = in.EndpointRef
	}
	target := firstNonEmpty(urlValue, cfg.URL)
	ref := firstNonEmpty(endpointRef, cfg.EndpointRef)
	if endpointLike(target) {
		ref = target
		target = ""
	}
	if target == "" && ref != "" {
		if resolved, ok := p.endpointRegistry().Resolve(coreendpoint.Ref(ref)); ok {
			target = resolved.URL
		}
	}
	if target == "" && cfg.URLEnv != "" {
		target, _, _ = lookupEnv(ctx, p.environment, cfg.URLEnv)
	}
	if target == "" {
		target = "*"
	}
	return target
}

func endpointLike(value string) bool {
	return strings.HasPrefix(strings.TrimSpace(value), "@endpoint/")
}

func durationOrDefault(value string, fallback time.Duration) time.Duration {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	dur, err := time.ParseDuration(strings.TrimSpace(value))
	if err != nil || dur <= 0 {
		return fallback
	}
	return dur
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func labelsID(labels map[string]string) string {
	if len(labels) == 0 {
		return "empty"
	}
	keys := make([]string, 0, len(labels))
	for key := range labels {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, key := range keys {
		b.WriteString(key)
		b.WriteByte('=')
		b.WriteString(labels[key])
		b.WriteByte('\n')
	}
	return shortHash(b.String())
}

func shortHash(value string) string {
	sum := sha1.Sum([]byte(value))
	return hex.EncodeToString(sum[:8])
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
