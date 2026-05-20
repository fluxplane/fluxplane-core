package web

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

func TestWebRequestIntentUsesTypedURLTarget(t *testing.T) {
	sys, err := system.NewHost(system.Config{Root: t.TempDir(), AllowPrivateNetwork: true})
	if err != nil {
		t.Fatalf("NewHost: %v", err)
	}
	ops, err := New(sys).Operations(context.Background(), zeroPluginContext())
	if err != nil {
		t.Fatalf("Operations: %v", err)
	}
	provider, ok := ops[0].(operation.IntentProvider)
	if !ok {
		t.Fatalf("%s does not implement IntentProvider", ops[0].Spec().Ref.String())
	}

	intents, err := provider.Intent(operation.NewContext(context.Background(), nil), requestInput{URL: "https://example.com"})
	if err != nil {
		t.Fatalf("Intent: %v", err)
	}
	if len(intents.Operations) != 1 || intents.Operations[0].Behavior != operation.IntentNetworkFetch {
		t.Fatalf("intents = %#v, want one network intent", intents)
	}
	target, ok := intents.Operations[0].Target.(operation.URLTarget)
	if !ok || target.URL != "https://example.com" {
		t.Fatalf("target = %#v, want URL target", intents.Operations[0].Target)
	}
}

func TestWebDatasourceSearchesHTMLResults(t *testing.T) {
	network := &testNetwork{response: system.HTTPResponse{
		Status:     "200 OK",
		StatusCode: 200,
		Body: []byte(`
<html><body>
  <a class="result__a" href="https://duckduckgo.com/l/?uddg=https%3A%2F%2Fexample.com%2Fagent&amp;rut=abc">Agent &amp; Runtime</a>
  <div class="result__snippet">Useful <b>runtime</b> result.</div>
</body></html>`),
	}}
	oldTemplate := duckDuckGoSearchURLTemplate
	duckDuckGoSearchURLTemplate = "https://duckduckgo.test/search?q={query}"
	t.Cleanup(func() { duckDuckGoSearchURLTemplate = oldTemplate })

	sys := testSystem{network: network, env: testEnvironment{}}
	providers, err := New(sys).DatasourceProviders(context.Background(), zeroPluginContext())
	if err != nil {
		t.Fatalf("DatasourceProviders: %v", err)
	}
	accessor, err := providers[0].Open(context.Background(), coredatasource.Spec{
		Name:     "web",
		Kind:     "web_search",
		Entities: []coredatasource.EntityType{SearchResultEntity},
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
	if got := network.lastRequest().URL; got != "https://duckduckgo.test/search?q=agent+runtime" {
		t.Fatalf("request URL = %q, want duckduckgo fixture URL", got)
	}
	if len(result.Records) != 1 {
		t.Fatalf("records = %#v, want one record", result.Records)
	}
	record := result.Records[0]
	if record.URL != "https://example.com/agent" || record.Title != "Agent & Runtime" || !strings.Contains(record.Content, "Useful runtime result") {
		t.Fatalf("record = %#v", record)
	}
}

func TestWebSearchUsesTavilyProvider(t *testing.T) {
	sys := testSystem{
		network: &testNetwork{response: system.HTTPResponse{
			Status:     "200 OK",
			StatusCode: 200,
			Body: []byte(`{
				"query":"agent runtime",
				"results":[{"title":"Agent Runtime","url":"https://example.com/runtime","content":"Runtime search result.","score":0.9}],
				"response_time":0.1
			}`),
		}},
		env: testEnvironment{"TAVILY_API_KEY": "tvly-test"},
	}
	ops, err := New(sys).Operations(context.Background(), zeroPluginContext())
	if err != nil {
		t.Fatalf("Operations: %v", err)
	}
	var searchOp operation.Operation
	for _, op := range ops {
		if op.Spec().Ref.Name == SearchOp {
			searchOp = op
		}
	}
	if searchOp == nil {
		t.Fatal("web_search operation not found")
	}

	result := searchOp.Run(operation.NewContext(context.Background(), nil), map[string]any{
		"queries":   []string{"agent runtime"},
		"providers": []string{"tavily"},
		"max":       3,
	})
	if result.IsError() {
		t.Fatalf("result error = %#v", result.Error)
	}
	req := sys.network.lastRequest()
	if req.URL != tavilySearchURL || req.Method != "POST" {
		t.Fatalf("request = %#v, want Tavily POST", req)
	}
	if req.Headers["Authorization"] != "Bearer tvly-test" || req.Headers["Content-Type"] != "application/json" {
		t.Fatalf("headers = %#v, want Tavily bearer/json", req.Headers)
	}
	if !strings.Contains(req.Body, `"query":"agent runtime"`) || !strings.Contains(req.Body, `"max_results":3`) || !strings.Contains(req.Body, `"search_depth":"basic"`) {
		t.Fatalf("body = %s, want Tavily low-cost payload", req.Body)
	}
	rendered, ok := result.Output.(operation.Rendered)
	if !ok {
		t.Fatalf("output = %T, want operation.Rendered", result.Output)
	}
	if !strings.Contains(rendered.Text, "Agent Runtime") || !strings.Contains(rendered.Text, "https://example.com/runtime") {
		t.Fatalf("rendered text = %q", rendered.Text)
	}
}

func TestWebSearchExplicitTavilyRequiresAPIKey(t *testing.T) {
	sys := testSystem{network: &testNetwork{}, env: testEnvironment{}}
	ops, err := New(sys).Operations(context.Background(), zeroPluginContext())
	if err != nil {
		t.Fatalf("Operations: %v", err)
	}
	var searchOp operation.Operation
	for _, op := range ops {
		if op.Spec().Ref.Name == SearchOp {
			searchOp = op
		}
	}
	result := searchOp.Run(operation.NewContext(context.Background(), nil), map[string]any{
		"query":     "agent runtime",
		"providers": []string{"tavily"},
	})
	if !result.IsError() || !strings.Contains(result.Error.Message, "TAVILY_API_KEY") {
		t.Fatalf("result = %#v, want missing Tavily API key error", result)
	}
}

func TestWebSearchUsesDuckDuckGoByDefaultWithoutTavily(t *testing.T) {
	sys := testSystem{
		network: &testNetwork{response: system.HTTPResponse{
			Status:     "200 OK",
			StatusCode: 200,
			Body: []byte(`
<html><body>
  <a class="result__a" href="https://duckduckgo.com/l/?uddg=https%3A%2F%2Fexample.com%2Fagent&amp;rut=abc">Agent &amp; Runtime</a>
  <div class="result__snippet">Useful <b>runtime</b> result.</div>
</body></html>`),
		}},
		env: testEnvironment{},
	}
	ops, err := New(sys).Operations(context.Background(), zeroPluginContext())
	if err != nil {
		t.Fatalf("Operations: %v", err)
	}
	var searchOp operation.Operation
	for _, op := range ops {
		if op.Spec().Ref.Name == SearchOp {
			searchOp = op
		}
	}
	if searchOp == nil {
		t.Fatal("web_search operation not found")
	}

	result := searchOp.Run(operation.NewContext(context.Background(), nil), map[string]any{
		"query": "agent runtime",
		"max":   3,
	})
	if result.IsError() {
		t.Fatalf("result error = %#v", result.Error)
	}
	req := sys.network.lastRequest()
	if req.Method != "GET" || !strings.Contains(req.URL, "duckduckgo.com/html/") || !strings.Contains(req.URL, "q=agent+runtime") {
		t.Fatalf("request = %#v, want DuckDuckGo GET", req)
	}
	rendered, ok := result.Output.(operation.Rendered)
	if !ok {
		t.Fatalf("output = %T, want operation.Rendered", result.Output)
	}
	if !strings.Contains(rendered.Text, "Provider: duckduckgo") || !strings.Contains(rendered.Text, "https://example.com/agent") {
		t.Fatalf("rendered text = %q", rendered.Text)
	}
}

func TestWebSearchFiltersDuckDuckGoProvider(t *testing.T) {
	sys := testSystem{
		network: &testNetwork{response: system.HTTPResponse{
			Status:     "200 OK",
			StatusCode: 200,
			Body: []byte(`<html><body>
<a class="result__a" href="https://example.com/ddg">DuckDuckGo Result</a>
<div class="result__snippet">Snippet</div>
</body></html>`),
		}},
		env: testEnvironment{},
	}
	ops, err := New(sys).Operations(context.Background(), zeroPluginContext())
	if err != nil {
		t.Fatalf("Operations: %v", err)
	}
	var searchOp operation.Operation
	for _, op := range ops {
		if op.Spec().Ref.Name == SearchOp {
			searchOp = op
		}
	}
	result := searchOp.Run(operation.NewContext(context.Background(), nil), map[string]any{
		"query":     "agent runtime",
		"providers": []string{"duckduckgo"},
	})
	if result.IsError() {
		t.Fatalf("result error = %#v", result.Error)
	}
	if len(sys.network.requests) != 1 {
		t.Fatalf("requests = %d, want one DuckDuckGo request", len(sys.network.requests))
	}
}
