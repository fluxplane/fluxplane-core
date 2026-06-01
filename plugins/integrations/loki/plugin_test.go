package loki

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"

	coredatasource "github.com/fluxplane/fluxplane-core/core/datasource"
	"github.com/fluxplane/fluxplane-core/core/operation"
	"github.com/fluxplane/fluxplane-core/orchestration/pluginhost"
	operationruntime "github.com/fluxplane/fluxplane-core/runtime/operation"
	system "github.com/fluxplane/fluxplane-core/runtime/workspace"
	fpendpoint "github.com/fluxplane/fluxplane-endpoint"
	"github.com/fluxplane/fluxplane-event"
	fpsystem "github.com/fluxplane/fluxplane-system"
	"github.com/fluxplane/fluxplane-system/systemkit"
	fpsystemtest "github.com/fluxplane/fluxplane-system/systemtest"
)

func TestLokiQueryAddsNamespaceAndBoundsLimit(t *testing.T) {
	var rawQuery string
	var rawLimit string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/loki/api/v1/query_range":
			rawQuery = r.URL.Query().Get("query")
			rawLimit = r.URL.Query().Get("limit")
			_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"streams","result":[{"stream":{"namespace":"latest","app":"backend","pod":"backend-1","container":"app"},"values":[["1710000000000000000","level=error request_id=req-1 failed"]]}]}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	sys, err := system.NewHost(system.Config{Root: t.TempDir(), AllowPrivateNetwork: true})
	if err != nil {
		t.Fatalf("NewHost() error = %v", err)
	}
	plugin := NewWithBoundaries(Boundaries{Process: sys.Process(), Network: sys.Network(), Environment: sys.Environment()})
	plugin.cfg = Config{URL: server.URL, DefaultNamespace: "latest", DefaultSince: "15m", DefaultLimit: 10, MaxLimit: 20}
	out, err := plugin.runQuery(operation.NewContext(context.Background(), event.Discard()), QueryInput{Query: `{app="backend"}`, Limit: 100})
	if err != nil {
		t.Fatalf("runQuery() error = %v", err)
	}
	if !strings.Contains(rawQuery, `namespace="latest"`) {
		t.Fatalf("query = %q, want namespace selector", rawQuery)
	}
	if rawLimit != "20" {
		t.Fatalf("limit = %q, want max limit 20", rawLimit)
	}
	if len(out.Entries) != 1 || out.Entries[0].RequestID != "req-1" {
		t.Fatalf("entries = %#v", out.Entries)
	}
}

func TestLokiTestOperationReadsReadyAndBuildInfo(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/ready":
			_, _ = w.Write([]byte("ready"))
		case "/loki/api/v1/status/buildinfo":
			_, _ = w.Write([]byte(`{"status":"success","data":{"version":"3.5.7"}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	sys, err := system.NewHost(system.Config{Root: t.TempDir(), AllowPrivateNetwork: true})
	if err != nil {
		t.Fatalf("NewHost() error = %v", err)
	}
	plugin := NewWithBoundaries(Boundaries{Process: sys.Process(), Network: sys.Network(), Environment: sys.Environment()})
	plugin.cfg = Config{URL: server.URL}
	result := plugin.test()(operation.NewContext(context.Background(), event.Discard()), TestInput{})
	if result.Status != operation.StatusOK {
		t.Fatalf("status = %s error = %#v", result.Status, result.Error)
	}
	out := result.Output.(TestOutput)
	if !out.Ready || out.Version != "3.5.7" {
		t.Fatalf("output = %#v", out)
	}
}

func TestRecentLogsAppliesLevelFilter(t *testing.T) {
	query, err := recentLogsQuery(RecentLogsInput{App: "backend", Namespace: "latest", Levels: []string{"error", "warn"}}, "")
	if err != nil {
		t.Fatalf("recentLogsQuery() error = %v", err)
	}
	if !strings.Contains(query, `|~ "(?i)\\b(error|warn)\\b"`) {
		t.Fatalf("query = %q, want level filter", query)
	}
}

func TestAutoDiscoverySelectsOnlySuccessfulProbe(t *testing.T) {
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "not ready", http.StatusServiceUnavailable)
	}))
	defer bad.Close()
	good := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/ready":
			_, _ = w.Write([]byte("ready"))
		case "/loki/api/v1/status/buildinfo":
			_, _ = w.Write([]byte(`{"status":"success","data":{"version":"3.5.7"}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer good.Close()

	registry := fpendpoint.NewDiscoveryRegistry()
	if err := registry.Register(staticDiscoveryProvider{candidates: []fpendpoint.DiscoveryCandidate{
		{ID: "bad", URL: bad.URL, Score: 100},
		{ID: "good", URL: good.URL, Score: 90},
	}}); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	sys, err := system.NewHost(system.Config{Root: t.TempDir(), AllowPrivateNetwork: true})
	if err != nil {
		t.Fatalf("NewHost() error = %v", err)
	}
	plugin := NewWithBoundaries(Boundaries{Process: sys.Process(), Network: sys.Network(), Environment: sys.Environment()})
	plugin.discovery = registry
	plugin.cfg = Config{AutoDiscover: AutoDiscoverConfig{Enabled: true}}
	client, target, err := plugin.clientFor(operation.NewContext(context.Background(), event.Discard()), "", "", "", "")
	if err != nil {
		t.Fatalf("clientFor() error = %v", err)
	}
	if target != good.URL {
		t.Fatalf("target = %q, want %q", target, good.URL)
	}
	if client.baseURL != good.URL {
		t.Fatalf("client baseURL = %q, want %q", client.baseURL, good.URL)
	}
}

func TestAutoDiscoveryPortForwardsKubernetesServiceCandidate(t *testing.T) {
	registry := fpendpoint.NewDiscoveryRegistry()
	if err := registry.Register(staticDiscoveryProvider{candidates: []fpendpoint.DiscoveryCandidate{{
		ID:          "loki-service",
		URL:         "http://loki.monitoring.svc:3100",
		Host:        "loki.monitoring.svc",
		Port:        3100,
		ProductHint: "loki",
		Score:       100,
		Source: fpendpoint.SourceRef{
			Kind:      "kubernetes.service",
			Namespace: "monitoring",
			Name:      "loki",
			Attributes: map[string]string{
				"context": "dev",
			},
		},
	}}}); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	process := &recordingLokiProcess{}
	network := &lokiPortForwardNetwork{}
	plugin := NewWithBoundaries(Boundaries{Process: process, Network: network})
	plugin.discovery = registry
	plugin.cfg = Config{AutoDiscover: AutoDiscoverConfig{Enabled: true, Kubernetes: true}}

	client, target, err := plugin.clientFor(operation.NewContext(context.Background(), event.Discard()), "", "", "", "")
	if err != nil {
		t.Fatalf("clientFor() error = %v", err)
	}
	if !strings.HasPrefix(target, "http://127.0.0.1:") || client.baseURL != target {
		t.Fatalf("target = %q client = %#v, want local port-forward URL", target, client)
	}
	if len(process.ensureRequests) != 1 {
		t.Fatalf("ensure requests = %d, want 1", len(process.ensureRequests))
	}
	req := process.ensureRequests[0]
	if req.Command != "kubectl" || !strings.Contains(strings.Join(req.Args, " "), "--context dev -n monitoring port-forward --address 127.0.0.1 service/loki") {
		t.Fatalf("process request = %#v, want kubectl service/loki port-forward in dev monitoring", req)
	}
	if len(network.requests) < 2 {
		t.Fatalf("network requests = %#v, want original and forwarded probes", network.requests)
	}
}

func TestNormalizeNamespaceSelectorHandlesEmptySelector(t *testing.T) {
	if got := normalizeNamespaceSelector(`{} |~ "error"`, "latest", false); got != `{namespace="latest"} |~ "error"` {
		t.Fatalf("normalized query = %q", got)
	}
	if got := normalizeNamespaceSelector(`{app="backend"}`, "latest", false); got != `{namespace="latest",app="backend"}` {
		t.Fatalf("normalized query = %q", got)
	}
}

func TestLokiNetworkAccessUsesResolvedTarget(t *testing.T) {
	plugin := NewWithBoundaries(Boundaries{})
	plugin.cfg = Config{URL: "http://configured-loki:3100"}
	access, err := plugin.lokiNetworkAccess(operation.NewContext(context.Background(), event.Discard()), QueryInput{})
	if err != nil {
		t.Fatalf("lokiNetworkAccess() error = %v", err)
	}
	assertNetworkAccess(t, access, "http://configured-loki:3100")

	access, err = plugin.lokiNetworkAccess(operation.NewContext(context.Background(), event.Discard()), QueryInput{URL: "http://input-loki:3100"})
	if err != nil {
		t.Fatalf("lokiNetworkAccess(input url) error = %v", err)
	}
	assertNetworkAccess(t, access, "http://input-loki:3100")

	endpoints := fpendpoint.NewRegistry(0)
	ref, err := endpoints.Put(fpendpoint.RuntimeRecord{Spec: fpendpoint.Spec{Name: "loki-dev", URL: "http://endpoint-loki:3100", Product: "loki"}})
	if err != nil {
		t.Fatalf("endpoint Put() error = %v", err)
	}
	plugin.endpoints = endpoints
	plugin.cfg = Config{}
	access, err = plugin.lokiNetworkAccess(operation.NewContext(context.Background(), event.Discard()), QueryInput{EndpointRef: string(ref)})
	if err != nil {
		t.Fatalf("lokiNetworkAccess(endpoint ref) error = %v", err)
	}
	assertNetworkAccess(t, access, "http://endpoint-loki:3100")
}

func TestPluginContributionsOperationsAndDatasourceProvider(t *testing.T) {
	sys, err := system.NewHost(system.Config{Root: t.TempDir(), AllowPrivateNetwork: true})
	if err != nil {
		t.Fatalf("NewHost() error = %v", err)
	}
	plugin := NewWithBoundaries(Boundaries{Process: sys.Process(), Network: sys.Network(), Environment: sys.Environment()})
	if manifest := plugin.Manifest(); manifest.Name != Name || manifest.Description == "" {
		t.Fatalf("manifest = %#v, want Loki manifest", manifest)
	}
	cfg := normalizeConfig(Config{
		URL:              " http://loki:3100 ",
		DefaultNamespace: " latest ",
		AutoDiscover:     AutoDiscoverConfig{Enabled: true, Namespaces: []string{"prod, staging", "prod"}},
	})
	if cfg.URL != "http://loki:3100" || cfg.DefaultNamespace != "latest" || strings.Join(cfg.AutoDiscover.Namespaces, ",") != "prod,staging" || !cfg.AutoDiscover.Kubernetes {
		t.Fatalf("normalized config = %#v", cfg)
	}
	contrib, err := plugin.Contributions(context.Background(), pluginhost.Context{})
	if err != nil {
		t.Fatalf("Contributions: %v", err)
	}
	if len(contrib.DataSources) != 1 || len(contrib.Operations) != 4 || len(contrib.OperationSets) != 1 {
		t.Fatalf("contribution = %#v, want datasource, operations, and operation set", contrib)
	}
	ops, err := plugin.Operations(context.Background(), pluginhost.Context{})
	if err != nil {
		t.Fatalf("Operations: %v", err)
	}
	if len(ops) != 4 {
		t.Fatalf("operations len = %d, want 4", len(ops))
	}

	providers, err := plugin.DatasourceProviders(context.Background(), pluginhost.Context{})
	if err != nil {
		t.Fatalf("DatasourceProviders: %v", err)
	}
	if len(providers) != 1 || len(providers[0].Entities()) != 4 {
		t.Fatalf("providers = %#v, want one provider with four entities", providers)
	}
	accessor, err := providers[0].Open(context.Background(), coredatasource.Spec{
		Name:   "logs",
		Kind:   Name,
		Config: map[string]string{"url": "http://loki:3100", "default_namespace": "latest"},
	})
	if err != nil {
		t.Fatalf("Open datasource: %v", err)
	}
	if accessor.Spec().Name != "logs" || len(accessor.Entities()) != 4 {
		t.Fatalf("accessor spec/entities = %#v %#v", accessor.Spec(), accessor.Entities())
	}
	searcher, ok := accessor.(coredatasource.Searcher)
	if !ok {
		t.Fatalf("accessor = %T, want Searcher", accessor)
	}
	if _, err := searcher.Search(context.Background(), coredatasource.SearchRequest{Entity: LogEntryEntity}); err == nil || !strings.Contains(err.Error(), "requires LogQL query") {
		t.Fatalf("Search empty query error = %v, want LogQL query requirement", err)
	}
	if _, err := providers[0].Open(context.Background(), coredatasource.Spec{Kind: "other"}); err == nil || !strings.Contains(err.Error(), "unsupported datasource kind") {
		t.Fatalf("Open unsupported error = %v, want unsupported kind", err)
	}
}

func assertNetworkAccess(t *testing.T, access []operationruntime.AccessDescriptor, want string) {
	t.Helper()
	if len(access) != 1 {
		t.Fatalf("access len = %d, want 1", len(access))
	}
	if access[0].Resource.Name != want {
		t.Fatalf("network access = %q, want %q", access[0].Resource.Name, want)
	}
}

func TestLookupEnvDoesNotReadHostEnvironment(t *testing.T) {
	t.Setenv("LOKI_TEST_SECRET_URL", "http://should-not-leak")
	value, ok, err := lookupEnv(context.Background(), nil, "LOKI_TEST_SECRET_URL")
	if err != nil {
		t.Fatalf("lookupEnv() error = %v", err)
	}
	if ok || value != "" {
		t.Fatalf("lookupEnv() = %q, %v; want no host fallback", value, ok)
	}
	if _, exists := os.LookupEnv("LOKI_TEST_SECRET_URL"); !exists {
		t.Fatal("test env was not set")
	}
}

type staticDiscoveryProvider struct {
	candidates []fpendpoint.DiscoveryCandidate
}

func (p staticDiscoveryProvider) Spec() fpendpoint.ProviderSpec {
	return fpendpoint.ProviderSpec{Name: "static", Products: []string{"loki"}}
}

func (p staticDiscoveryProvider) Discover(context.Context, fpendpoint.DiscoveryRequest) (fpendpoint.DiscoveryResult, error) {
	return fpendpoint.DiscoveryResult{Candidates: p.candidates}, nil
}

type lokiFakeSystem struct {
	*system.MemorySystem
	process fpsystem.ProcessManager
	network fpsystem.Network
}

func (s lokiFakeSystem) Process() fpsystem.ProcessManager { return s.process }

func (s lokiFakeSystem) Network() fpsystem.Network {
	if s.network != nil {
		return s.network
	}
	return s.MemorySystem.Network()
}

type lokiPortForwardNetwork struct {
	fpsystemtest.UnsupportedNetwork
	requests []systemkit.HTTPRequest
}

func (n *lokiPortForwardNetwork) DoHTTP(_ context.Context, req systemkit.HTTPRequest) (systemkit.HTTPResponse, error) {
	n.requests = append(n.requests, req)
	parsed, _ := url.Parse(req.URL)
	if parsed.Hostname() != "127.0.0.1" {
		return systemkit.HTTPResponse{}, errors.New("cluster service DNS is not locally reachable")
	}
	switch parsed.Path {
	case "/ready":
		return systemkit.HTTPResponse{StatusCode: http.StatusOK, Status: "200 OK", Body: []byte("ready")}, nil
	case "/loki/api/v1/status/buildinfo":
		return systemkit.HTTPResponse{StatusCode: http.StatusOK, Status: "200 OK", Body: []byte(`{"status":"success","data":{"version":"3.5.7"}}`)}, nil
	default:
		return systemkit.HTTPResponse{StatusCode: http.StatusNotFound, Status: "404 Not Found"}, nil
	}
}

type recordingLokiProcess struct {
	ensureRequests []fpsystem.ProcessRequest
}

func (p *recordingLokiProcess) Run(context.Context, fpsystem.ProcessRequest) (fpsystem.ProcessResult, error) {
	return fpsystem.ProcessResult{}, errors.New("not implemented")
}

func (p *recordingLokiProcess) Start(context.Context, fpsystem.ProcessRequest) (fpsystem.ProcessHandle, error) {
	return nil, errors.New("not implemented")
}

func (p *recordingLokiProcess) Ensure(_ context.Context, req fpsystem.ProcessRequest) (fpsystem.ProcessHandle, bool, error) {
	p.ensureRequests = append(p.ensureRequests, req)
	return lokiProcessHandle{info: fpsystem.ProcessInfo{ID: "proc-1", Label: req.Label, Command: req.Command, Args: req.Args, Running: true}}, true, nil
}

func (p *recordingLokiProcess) Group(string) fpsystem.ProcessGroup { return nil }

func (p *recordingLokiProcess) List(context.Context) ([]fpsystem.ProcessInfo, error) {
	return nil, errors.New("not implemented")
}

type lokiProcessHandle struct {
	info fpsystem.ProcessInfo
}

func (h lokiProcessHandle) ID() string { return h.info.ID }

func (h lokiProcessHandle) Info() fpsystem.ProcessInfo { return h.info }

func (h lokiProcessHandle) Events() <-chan fpsystem.ProcessEvent {
	ch := make(chan fpsystem.ProcessEvent)
	close(ch)
	return ch
}

func (h lokiProcessHandle) Subscribe(context.Context) <-chan fpsystem.ProcessEvent { return h.Events() }

func (h lokiProcessHandle) Wait(context.Context) (fpsystem.ProcessResult, error) {
	return fpsystem.ProcessResult{Command: h.info.Command, Args: h.info.Args}, nil
}

func (h lokiProcessHandle) Stop(context.Context) error                              { return nil }
func (h lokiProcessHandle) Kill(context.Context) error                              { return nil }
func (h lokiProcessHandle) Signal(context.Context, fpsystem.ProcessSignal) error    { return nil }
func (h lokiProcessHandle) Interrupt(context.Context) error                         { return nil }
func (h lokiProcessHandle) Reload(context.Context) error                            { return nil }
func (h lokiProcessHandle) Pause(context.Context) error                             { return nil }
func (h lokiProcessHandle) Resume(context.Context) error                            { return nil }
func (h lokiProcessHandle) Write(context.Context, []byte) (int, error)              { return 0, nil }
func (h lokiProcessHandle) CloseInput(context.Context) error                        { return nil }
func (h lokiProcessHandle) Restart(context.Context) (fpsystem.ProcessHandle, error) { return h, nil }
func (h lokiProcessHandle) Detach(context.Context) error                            { return nil }
