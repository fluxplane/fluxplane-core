package coding

import (
	"context"
	"strings"
	"testing"

	corecontext "github.com/fluxplane/engine/core/context"
	"github.com/fluxplane/engine/orchestration/pluginhost"
	"github.com/fluxplane/engine/plugins/languages/golang"
	"github.com/fluxplane/engine/plugins/native/project"
	"github.com/fluxplane/engine/runtime/system"
	"github.com/fluxplane/engine/runtime/systemtest"
)

func TestContextProvidersAggregateCodingSummaries(t *testing.T) {
	sys := systemtest.NewMemory()
	writeCodingFile(t, sys.Workspace(), "AGENTS.md", "# Agent Notes\n")
	writeCodingFile(t, sys.Workspace(), "go.mod", "module example.com/app\n\ngo 1.26\n")
	writeCodingFile(t, sys.Workspace(), "main.go", "package main\n\nfunc main() {}\n")
	writeCodingFile(t, sys.Workspace(), "README.md", "# App\n")

	providers, err := New(sys).ContextProviders(context.Background(), pluginhost.Context{})
	if err != nil {
		t.Fatalf("ContextProviders: %v", err)
	}
	byName := map[corecontext.ProviderName]corecontext.Provider{}
	for _, provider := range providers {
		byName[provider.Spec().Name] = provider
	}
	for _, name := range []corecontext.ProviderName{AgentsContextProvider, project.SummaryProvider, golang.SummaryProvider} {
		if byName[name] == nil {
			t.Fatalf("provider %s missing from %#v", name, byName)
		}
	}
	for _, name := range []corecontext.ProviderName{project.SummaryProvider, golang.SummaryProvider} {
		if byName[name].Spec().Annotations[corecontext.AnnotationAutoContext] != "true" {
			t.Fatalf("provider %s spec = %#v, want auto context", name, byName[name].Spec())
		}
	}

	projectBlocks, err := byName[project.SummaryProvider].Build(context.Background(), corecontext.Request{})
	if err != nil {
		t.Fatalf("project summary Build: %v", err)
	}
	if len(projectBlocks) != 1 || !strings.Contains(projectBlocks[0].Content, "Workspace project summary:") {
		t.Fatalf("project blocks = %#v", projectBlocks)
	}
	goBlocks, err := byName[golang.SummaryProvider].Build(context.Background(), corecontext.Request{})
	if err != nil {
		t.Fatalf("go summary Build: %v", err)
	}
	if len(goBlocks) != 1 || !strings.Contains(goBlocks[0].Content, "Go workspace summary:") {
		t.Fatalf("go blocks = %#v", goBlocks)
	}
}

func writeCodingFile(t *testing.T, ws system.Workspace, rel, content string) {
	t.Helper()
	if _, err := ws.WriteFile(context.Background(), rel, []byte(content), 0644, true); err != nil {
		t.Fatalf("WriteFile(%s): %v", rel, err)
	}
}
