package operation

import "testing"

func TestParseRiskLevel(t *testing.T) {
	for input, want := range map[string]RiskLevel{
		"":         RiskUnknown,
		" low ":    RiskLow,
		"MEDIUM":   RiskMedium,
		"high":     RiskHigh,
		"critical": RiskCritical,
	} {
		got, err := ParseRiskLevel(input)
		if err != nil {
			t.Fatalf("ParseRiskLevel(%q): %v", input, err)
		}
		if got != want {
			t.Fatalf("ParseRiskLevel(%q) = %q, want %q", input, got, want)
		}
	}
	if _, err := ParseRiskLevel("dangerous"); err == nil {
		t.Fatal("ParseRiskLevel(dangerous) error = nil, want error")
	}
}

func TestRiskLevelTextMarshaling(t *testing.T) {
	var risk RiskLevel
	if err := risk.UnmarshalText([]byte("HIGH")); err != nil {
		t.Fatalf("UnmarshalText: %v", err)
	}
	if risk != RiskHigh {
		t.Fatalf("risk = %q, want high", risk)
	}
	text, err := risk.MarshalText()
	if err != nil {
		t.Fatalf("MarshalText: %v", err)
	}
	if string(text) != "high" {
		t.Fatalf("MarshalText = %q, want high", text)
	}
}
