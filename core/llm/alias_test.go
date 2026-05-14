package llm

import (
	"strings"
	"testing"
)

func TestNewModelAliasSpecParsesProviderQualifiedTarget(t *testing.T) {
	spec, err := NewModelAliasSpec("claude/sonnet", "anthropic/claude-sonnet-4-6")
	if err != nil {
		t.Fatalf("NewModelAliasSpec: %v", err)
	}
	if spec.Name != "claude/sonnet" || spec.Target.Provider != "anthropic" || spec.Target.Name != "claude-sonnet-4-6" {
		t.Fatalf("spec = %#v, want claude/sonnet -> anthropic/claude-sonnet-4-6", spec)
	}
}

func TestParseModelRefKeepsSlashModelIDs(t *testing.T) {
	ref, err := ParseModelRef("openrouter/anthropic/claude-sonnet-4.6")
	if err != nil {
		t.Fatalf("ParseModelRef: %v", err)
	}
	if ref.Provider != "openrouter" || ref.Name != "anthropic/claude-sonnet-4.6" {
		t.Fatalf("ref = %#v, want openrouter provider with slash model", ref)
	}
}

func TestNewModelAliasSpecRejectsBareTarget(t *testing.T) {
	_, err := NewModelAliasSpec("codex", "gpt-5.5")
	if err == nil || !strings.Contains(err.Error(), "<provider>/<model>") {
		t.Fatalf("error = %v, want provider/model target error", err)
	}
}
