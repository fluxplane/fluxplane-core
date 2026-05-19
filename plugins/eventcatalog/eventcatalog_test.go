package eventcatalog

import (
	"context"
	"encoding/json"
	"testing"

	coreevent "github.com/fluxplane/agentruntime/core/event"
	"github.com/fluxplane/agentruntime/orchestration/pluginhost"
	"github.com/fluxplane/agentruntime/plugins/golangplugin"
	"github.com/fluxplane/agentruntime/plugins/humanplugin"
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

func TestHumanEventsDecodeFromRegisteredCatalog(t *testing.T) {
	registry := coreevent.NewRegistry()
	for _, sample := range All() {
		if err := registry.Register(sample); err != nil {
			t.Fatalf("Register %s: %v", sample.EventName(), err)
		}
	}
	raw, err := json.Marshal(humanplugin.ClarificationRequested{
		Prompt: "pick one",
	})
	if err != nil {
		t.Fatalf("Marshal clarification: %v", err)
	}
	decoded, err := registry.Decode(humanplugin.EventClarificationRequested, raw)
	if err != nil {
		t.Fatalf("Decode clarification: %v", err)
	}
	if got := decoded.(humanplugin.ClarificationRequested).Prompt; got != "pick one" {
		t.Fatalf("decoded prompt = %q, want pick one", got)
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
	samples = append(samples, projectBundle.EventTypes...)
	samples = append(samples, goBundle.EventTypes...)
	for _, sample := range samples {
		if !catalog[string(sample.EventName())] {
			t.Fatalf("catalog missing contributed event %s", sample.EventName())
		}
	}
}
