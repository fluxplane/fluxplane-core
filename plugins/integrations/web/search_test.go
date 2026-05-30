package web

import (
	"context"
	runtimeworkspace "github.com/fluxplane/fluxplane-core/runtime/workspace"
	"sync"
	"testing"

	fpsystem "github.com/fluxplane/fluxplane-system"
	"github.com/fluxplane/fluxplane-system/systemkit"
	fpsystemtest "github.com/fluxplane/fluxplane-system/systemtest"
)

type testSystem struct {
	workspace runtimeworkspace.Workspace
	network   *testNetwork
	env       fpsystem.Environment
}

func (s testSystem) Workspace() runtimeworkspace.Workspace { return s.workspace }
func (s testSystem) Network() fpsystem.Network             { return s.network }
func (s testSystem) Process() fpsystem.ProcessManager      { return nil }
func (s testSystem) Environment() fpsystem.Environment     { return s.env }

type testNetwork struct {
	fpsystemtest.UnsupportedNetwork
	requests []systemkit.HTTPRequest
	response systemkit.HTTPResponse
	err      error
}

func (n *testNetwork) DoHTTP(_ context.Context, req systemkit.HTTPRequest) (systemkit.HTTPResponse, error) {
	n.requests = append(n.requests, req)
	return n.response, n.err
}

func (n *testNetwork) lastRequest() systemkit.HTTPRequest {
	if len(n.requests) == 0 {
		return systemkit.HTTPRequest{}
	}
	return n.requests[len(n.requests)-1]
}

type testEnvironment map[string]string

func (e testEnvironment) Lookup(_ context.Context, key string) (string, bool, error) {
	value, ok := e[key]
	return value, ok, nil
}

type testSearchProvider struct {
	name    string
	gate    chan struct{}
	started chan string
	done    chan string
}

func (p testSearchProvider) Name() string { return p.name }

func (p testSearchProvider) Available(context.Context) bool { return true }

func (p testSearchProvider) Search(_ context.Context, req SearchProviderRequest) (SearchProviderResult, error) {
	p.started <- req.Query
	<-p.gate
	p.done <- req.Query
	return SearchProviderResult{
		Provider: p.name,
		Query:    req.Query,
		Results:  []SearchResult{{Title: req.Query, URL: "https://example.com/" + req.Query, Source: p.name}},
	}, nil
}

func TestRunProviderSearchesLimitsConcurrencyAndPreservesOrder(t *testing.T) {
	gate := make(chan struct{})
	started := make(chan string, 8)
	done := make(chan string, 8)
	providers := []SearchProvider{testSearchProvider{name: "test", gate: gate, started: started, done: done}}
	queries := []string{"one", "two", "three", "four", "five", "six"}

	var out searchOutput
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		out = runProviderSearches(context.Background(), queries, providers, 10, nil)
	}()

	for range searchConcurrency {
		<-started
	}
	select {
	case query := <-started:
		t.Fatalf("started query %q beyond concurrency limit %d", query, searchConcurrency)
	default:
	}

	for range searchConcurrency {
		gate <- struct{}{}
	}
	for range searchConcurrency {
		<-done
	}
	for range len(queries) - searchConcurrency {
		<-started
	}
	for range len(queries) - searchConcurrency {
		gate <- struct{}{}
	}
	wg.Wait()

	if len(out.Errors) != 0 {
		t.Fatalf("errors = %#v, want none", out.Errors)
	}
	if len(out.Results) != len(queries) {
		t.Fatalf("results = %#v, want %d", out.Results, len(queries))
	}
	for i, query := range queries {
		if out.Results[i].Query != query {
			t.Fatalf("result %d query = %q, want %q", i, out.Results[i].Query, query)
		}
	}
}
