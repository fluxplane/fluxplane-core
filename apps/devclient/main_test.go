package main

import "testing"

func TestParseArgsTreatsUnknownFirstPositionalAsCommandPath(t *testing.T) {
	cfg, err := parseArgs([]string{"text/upper", "hello", "world"})
	if err != nil {
		t.Fatalf("parseArgs: %v", err)
	}
	if cfg.mode != "command" {
		t.Fatalf("mode = %q, want command", cfg.mode)
	}
	if cfg.commandPath.String() != "/text/upper" {
		t.Fatalf("command path = %s, want /text/upper", cfg.commandPath.String())
	}
	if cfg.text != "hello world" {
		t.Fatalf("text = %q, want hello world", cfg.text)
	}
}

func TestParseArgsKeepsInputMode(t *testing.T) {
	cfg, err := parseArgs([]string{"input", "hello"})
	if err != nil {
		t.Fatalf("parseArgs: %v", err)
	}
	if cfg.mode != "input" {
		t.Fatalf("mode = %q, want input", cfg.mode)
	}
}

func TestParseArgsEnablesSyntheticTool(t *testing.T) {
	cfg, err := parseArgs([]string{"-openai", "-synthetic-tool", "input", "lookup alpha"})
	if err != nil {
		t.Fatalf("parseArgs: %v", err)
	}
	if !cfg.useOpenAI {
		t.Fatal("useOpenAI = false, want true")
	}
	if !cfg.syntheticTool {
		t.Fatal("syntheticTool = false, want true")
	}
}

func TestSyntheticLookupOperationReturnsDeterministicValue(t *testing.T) {
	result := syntheticLookupOperation().Run(nil, map[string]any{"key": "alpha"})
	if result.IsError() {
		t.Fatalf("result = %#v, want ok", result)
	}
	output, ok := result.Output.(map[string]any)
	if !ok {
		t.Fatalf("output = %T, want map", result.Output)
	}
	if output["value"] != "ALPHA-42" {
		t.Fatalf("value = %#v, want ALPHA-42", output["value"])
	}
}
