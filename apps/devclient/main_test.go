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
