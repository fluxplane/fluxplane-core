package webplugin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

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
