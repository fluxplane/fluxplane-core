package context

import (
	"testing"

	"github.com/fluxplane/fluxplane-core/core/event"
	"github.com/fluxplane/fluxplane-core/core/policy"
)

func TestProviderSpecValidateRequiresName(t *testing.T) {
	spec := ProviderSpec{}
	if err := spec.Validate(); err == nil {
		t.Fatal("Validate succeeded, want empty name error")
	}
}

func TestProviderSpecValidateAllowsEmptyFields(t *testing.T) {
	spec := ProviderSpec{Name: "my-provider"}
	if err := spec.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestNormalizePlacementDefaultsToUser(t *testing.T) {
	tests := []struct {
		input    Placement
		expected Placement
	}{
		{"", PlacementUser},
		{PlacementUser, PlacementUser},
		{PlacementSystem, PlacementSystem},
		{PlacementDeveloper, PlacementDeveloper},
		{Placement("unknown"), PlacementUser},
	}
	for _, tt := range tests {
		got := NormalizePlacement(tt.input)
		if got != tt.expected {
			t.Errorf("NormalizePlacement(%q) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}

func TestBlockRecordedEventName(t *testing.T) {
	event := BlockRecorded{
		Provider: "test-provider",
		Block:    Block{Content: "test"},
	}
	if got := event.EventName(); got != EventBlockRecorded {
		t.Errorf("EventName = %q, want %q", got, EventBlockRecorded)
	}
}

func TestBlockRemovedRecordedEventName(t *testing.T) {
	event := BlockRemovedRecorded{
		Removed: BlockRemoved{ID: "block-1"},
	}
	if got := event.EventName(); got != EventBlockRemoved {
		t.Errorf("EventName = %q, want %q", got, EventBlockRemoved)
	}
}

func TestRenderCommittedEventName(t *testing.T) {
	event := RenderCommitted{
		Records: map[ProviderName]ProviderRenderRecord{},
	}
	if got := event.EventName(); got != EventRenderCommitted {
		t.Errorf("EventName = %q, want %q", got, EventRenderCommitted)
	}
}

func TestBuildResultEmptyDiff(t *testing.T) {
	tests := []struct {
		name     string
		result   BuildResult
		expected bool
	}{
		{
			name:     "empty result",
			result:   BuildResult{},
			expected: true,
		},
		{
			name: "with added blocks",
			result: BuildResult{
				Added: []Block{{Content: "test"}},
			},
			expected: false,
		},
		{
			name: "with updated blocks",
			result: BuildResult{
				Updated: []Block{{Content: "test"}},
			},
			expected: false,
		},
		{
			name: "with removed blocks",
			result: BuildResult{
				Removed: []BlockRemoved{{ID: "block-1"}},
			},
			expected: false,
		},
	}
	for _, tt := range tests {
		got := tt.result.EmptyDiff()
		if got != tt.expected {
			t.Errorf("%s: EmptyDiff = %v, want %v", tt.name, got, tt.expected)
		}
	}
}

func TestBlockWithSensitivity(t *testing.T) {
	block := Block{
		ID:          "block-1",
		Provider:    "test-provider",
		Kind:        BlockText,
		Placement:   PlacementSystem,
		Content:     "sensitive data",
		Sensitivity: policy.SensitivityConfidential,
		Freshness:   FreshnessStatic,
	}
	if block.Provider != "test-provider" {
		t.Errorf("Provider = %q, want test-provider", block.Provider)
	}
	if block.Sensitivity != policy.SensitivityConfidential {
		t.Errorf("Sensitivity = %v, want confidential", block.Sensitivity)
	}
}

func TestRegisterEventsWithNilRegistry(t *testing.T) {
	err := RegisterEvents(nil)
	if err == nil {
		t.Fatal("RegisterEvents succeeded with nil registry, want error")
	}
}

func TestRegisterEventsSuccess(t *testing.T) {
	registry := &event.Registry{}
	err := RegisterEvents(registry)
	if err != nil {
		t.Fatalf("RegisterEvents: %v", err)
	}
}
