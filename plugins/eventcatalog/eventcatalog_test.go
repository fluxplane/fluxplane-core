package eventcatalog

import (
	"context"
	"testing"

	"github.com/fluxplane/agentruntime/orchestration/pluginhost"
	"github.com/fluxplane/agentruntime/plugins/humanplugin"
	"github.com/fluxplane/agentruntime/plugins/planexecplugin"
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
	for _, sample := range append(humanBundle.EventTypes, planBundle.EventTypes...) {
		if !catalog[string(sample.EventName())] {
			t.Fatalf("catalog missing contributed event %s", sample.EventName())
		}
	}
}
