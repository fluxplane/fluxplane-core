package webplugin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	coredatasource "github.com/fluxplane/agentruntime/core/datasource"
	"github.com/fluxplane/agentruntime/core/operation"
	"github.com/fluxplane/agentruntime/runtime/system"
)

func TestWebRequestConvertsHTMLToMarkdown(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<html><body><h1>Hello</h1><a href="https://example.com">link</a></body></html>`))
	}))
	t.Cleanup(server.Close)

	sys, err := system.NewHost(system.Config{Root: t.TempDir(), AllowPrivateNetwork: true})
	if err != nil {
		t.Fatalf("NewHost: %v", err)
	}
	ops, err := New(sys).Operations(context.Background(), zeroPluginContext())
	if err != nil {
		t.Fatalf("Operations: %v", err)
	}
	result := ops[0].Run(operation.NewContext(context.Background(), nil), map[string]any{"url": server.URL})
	if result.IsError() {
		t.Fatalf("result error = %#v", result.Error)
	}
	rendered, ok := result.Output.(operation.Rendered)
	if !ok {
		t.Fatalf("output = %T, want operation.Rendered", result.Output)
	}
	if !strings.Contains(rendered.Text, "# Hello") || strings.Contains(rendered.Text, "<h1>") {
		t.Fatalf("rendered text = %q, want markdown conversion", rendered.Text)
	}
}

func TestWebDatasourceSearchesHTMLResults(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("q"); got != "agent runtime" {
			t.Fatalf("query = %q, want agent runtime", got)
		}
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`
<html><body>
  <a class="result__a" href="https://duckduckgo.com/l/?uddg=https%3A%2F%2Fexample.com%2Fagent&amp;rut=abc">Agent &amp; Runtime</a>
  <div class="result__snippet">Useful <b>runtime</b> result.</div>
</body></html>`))
	}))
	t.Cleanup(server.Close)

	sys, err := system.NewHost(system.Config{Root: t.TempDir(), AllowPrivateNetwork: true})
	if err != nil {
		t.Fatalf("NewHost: %v", err)
	}
	providers, err := New(sys).DatasourceProviders(context.Background(), zeroPluginContext())
	if err != nil {
		t.Fatalf("DatasourceProviders: %v", err)
	}
	accessor, err := providers[0].Open(context.Background(), coredatasource.Spec{
		Name:     "web",
		Kind:     "web",
		Entities: []coredatasource.EntityType{SearchResultEntity},
		Config:   map[string]string{"search_url": server.URL + "/search?q={query}"},
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	searcher, ok := accessor.(coredatasource.Searcher)
	if !ok {
		t.Fatalf("accessor = %T, want Searcher", accessor)
	}
	result, err := searcher.Search(context.Background(), coredatasource.SearchRequest{
		Entity: SearchResultEntity,
		Query:  "agent runtime",
		Limit:  5,
	})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(result.Records) != 1 {
		t.Fatalf("records = %#v, want one record", result.Records)
	}
	record := result.Records[0]
	if record.URL != "https://example.com/agent" || record.Title != "Agent & Runtime" || !strings.Contains(record.Content, "Useful runtime result") {
		t.Fatalf("record = %#v", record)
	}
}
