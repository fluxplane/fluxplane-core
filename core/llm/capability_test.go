package llm

import "testing"

func TestCapabilitySetHas(t *testing.T) {
	tests := []struct {
		name       string
		set        CapabilitySet
		capability Capability
		want       bool
	}{
		{
			name:       "has tool calling",
			set:        CapabilitySet{CapabilityToolCalling, CapabilityStreaming},
			capability: CapabilityToolCalling,
			want:       true,
		},
		{
			name:       "missing capability",
			set:        CapabilitySet{CapabilityToolCalling},
			capability: CapabilityVision,
			want:       false,
		},
		{
			name:       "empty set",
			set:        CapabilitySet{},
			capability: CapabilityToolCalling,
			want:       false,
		},
		{
			name:       "has reasoning",
			set:        CapabilitySet{CapabilityReasoning, CapabilityThinking},
			capability: CapabilityReasoning,
			want:       true,
		},
		{
			name:       "has vision",
			set:        CapabilitySet{CapabilityVision},
			capability: CapabilityVision,
			want:       true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.set.Has(tt.capability)
			if got != tt.want {
				t.Errorf("Has(%q) = %v, want %v", tt.capability, got, tt.want)
			}
		})
	}
}
