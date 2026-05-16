package eventcatalog

import (
	"context"
	"encoding/json"
	"testing"

	coreevent "github.com/fluxplane/agentruntime/core/event"
	"github.com/fluxplane/agentruntime/core/language"
	coreproject "github.com/fluxplane/agentruntime/core/project"
	"github.com/fluxplane/agentruntime/orchestration/pluginhost"
	"github.com/fluxplane/agentruntime/plugins/golangplugin"
	"github.com/fluxplane/agentruntime/plugins/humanplugin"
	"github.com/fluxplane/agentruntime/plugins/planexecplugin"
	"github.com/fluxplane/agentruntime/plugins/projectplugin"
)

func TestAllHasUniqueEventNames(t *testing.T) {
	seen := map[string]bool{}
	for _, sample := range All() {
		name := string(sample.EventName())
		if name == "" {
			t.Fatalf("event sample %T has empty name", sample)
		}
		if seen[name] {
			t.Fatalf("duplicate event name %q", name)
		}
		seen[name] = true
	}
}

func TestLanguageActivationEventsDecodeFromRegisteredCatalog(t *testing.T) {
	registry := coreevent.NewRegistry()
	for _, sample := range All() {
		if err := registry.Register(sample); err != nil {
			t.Fatalf("Register %s: %v", sample.EventName(), err)
		}
	}
	signalRaw, err := json.Marshal(coreproject.SignalsObserved{
		WorkspaceRoot: ".",
		Signals:       []coreproject.Signal{{Kind: "manifest", Path: "go.mod", Language: "go", Toolchain: "go"}},
	})
	if err != nil {
		t.Fatalf("Marshal signals: %v", err)
	}
	decoded, err := registry.Decode(coreproject.EventSignalsObserved, signalRaw)
	if err != nil {
		t.Fatalf("Decode signals: %v", err)
	}
	if got := decoded.(coreproject.SignalsObserved).Signals[0].Toolchain; got != "go" {
		t.Fatalf("decoded signal toolchain = %q, want go", got)
	}
	statusRaw, err := json.Marshal(language.ToolchainStatusObserved{
		Status: language.ToolchainStatus{ID: "go", Available: true},
	})
	if err != nil {
		t.Fatalf("Marshal status: %v", err)
	}
	decoded, err = registry.Decode(language.EventToolchainStatusObserved, statusRaw)
	if err != nil {
		t.Fatalf("Decode status: %v", err)
	}
	if got := decoded.(language.ToolchainStatusObserved).Status.ID; got != "go" {
		t.Fatalf("decoded status id = %q, want go", got)
	}
}

func TestAllCoversPluginContributedEventTypes(t *testing.T) {
	catalog := map[string]bool{}
	for _, sample := range All() {
		catalog[string(sample.EventName())] = true
	}
	humanBundle, err := humanplugin.New(nil).Contributions(context.Background(), pluginhost.Context{})
	if err != nil {
		t.Fatalf("human contributions: %v", err)
	}
	planBundle, err := planexecplugin.New().Contributions(context.Background(), pluginhost.Context{})
	if err != nil {
		t.Fatalf("plan contributions: %v", err)
	}
	projectBundle, err := projectplugin.New(nil).Contributions(context.Background(), pluginhost.Context{})
	if err != nil {
		t.Fatalf("project contributions: %v", err)
	}
	goBundle, err := golangplugin.New(nil).Contributions(context.Background(), pluginhost.Context{})
	if err != nil {
		t.Fatalf("go contributions: %v", err)
	}
	var samples []coreevent.Event
	samples = append(samples, humanBundle.EventTypes...)
	samples = append(samples, planBundle.EventTypes...)
	samples = append(samples, projectBundle.EventTypes...)
	samples = append(samples, goBundle.EventTypes...)
	for _, sample := range samples {
		if !catalog[string(sample.EventName())] {
			t.Fatalf("catalog missing contributed event %s", sample.EventName())
		}
	}
}
