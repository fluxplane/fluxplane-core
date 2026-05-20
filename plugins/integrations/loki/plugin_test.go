package loki

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	corediscovery "github.com/fluxplane/agentruntime/core/discovery"
	coreendpoint "github.com/fluxplane/agentruntime/core/endpoint"
	"github.com/fluxplane/agentruntime/core/event"
	"github.com/fluxplane/agentruntime/core/operation"
	runtimediscovery "github.com/fluxplane/agentruntime/runtime/discovery"
	runtimeendpoint "github.com/fluxplane/agentruntime/runtime/endpoint"
	operationruntime "github.com/fluxplane/agentruntime/runtime/operation"
	"github.com/fluxplane/agentruntime/runtime/system"
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
	plugin := New(sys)
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
	plugin := New(sys)
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

	registry := runtimediscovery.NewRegistry()
	if err := registry.Register(staticDiscoveryProvider{candidates: []corediscovery.Candidate{
		{ID: "bad", URL: bad.URL, Score: 100},
		{ID: "good", URL: good.URL, Score: 90},
	}}); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	sys, err := system.NewHost(system.Config{Root: t.TempDir(), AllowPrivateNetwork: true})
	if err != nil {
		t.Fatalf("NewHost() error = %v", err)
	}
	plugin := New(sys)
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

func TestNormalizeNamespaceSelectorHandlesEmptySelector(t *testing.T) {
	if got := normalizeNamespaceSelector(`{} |~ "error"`, "latest", false); got != `{namespace="latest"} |~ "error"` {
		t.Fatalf("normalized query = %q", got)
	}
	if got := normalizeNamespaceSelector(`{app="backend"}`, "latest", false); got != `{namespace="latest",app="backend"}` {
		t.Fatalf("normalized query = %q", got)
	}
}

func TestLokiNetworkAccessUsesResolvedTarget(t *testing.T) {
	plugin := New(nil)
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

	endpoints := runtimeendpoint.NewRegistry(0)
	ref, err := endpoints.Put(runtimeendpoint.Record{Spec: coreendpoint.Spec{Name: "loki-dev", URL: "http://endpoint-loki:3100", Product: "loki"}})
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
	candidates []corediscovery.Candidate
}

func (p staticDiscoveryProvider) Spec() runtimediscovery.ProviderSpec {
	return runtimediscovery.ProviderSpec{Name: "static", Products: []string{"loki"}}
}

func (p staticDiscoveryProvider) Discover(context.Context, corediscovery.Request) (corediscovery.Result, error) {
	return corediscovery.Result{Candidates: p.candidates}, nil
}
