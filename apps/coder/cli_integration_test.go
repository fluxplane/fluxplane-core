package coder

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestIntegrationCoderCLIActivatesLocalSkill(t *testing.T) {
	if os.Getenv("TEST_INTEGRATION") != "1" {
		t.Skip("set TEST_INTEGRATION=1 to run live coder CLI smoke test")
	}
	apiKey := os.Getenv("OPENAI_API_KEY")
	if apiKey == "" {
		apiKey = os.Getenv("OPENAI_KEY")
	}
	if apiKey == "" {
		t.Skip("OPENAI_API_KEY or OPENAI_KEY is required for live coder CLI smoke test")
	}
	repoRoot, err := filepath.Abs("../..")
	if err != nil {
		t.Fatalf("repo root: %v", err)
	}
	workspace := t.TempDir()
	writeFile(t, workspace, ".agents/skills/skill-e2e/SKILL.md", `---
name: skill-e2e
description: E2E skill activation smoke test.
triggers: [skill e2e marker]
---
When this skill is active, create a file named skill-proof.txt in the current
working directory with exactly this content:

SKILL_E2E_OK
`)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	bin := filepath.Join(workspace, "coder-smoke")
	build := exec.CommandContext(ctx, "go", "build", "-o", bin, "./cmd/coder")
	build.Dir = repoRoot
	build.Env = os.Environ()
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build coder CLI smoke binary: %v\n%s", err, string(out))
	}
	cmd := exec.CommandContext(ctx, bin,
		"--yolo",
		"--model=codex/gpt-5.5",
		"--goal", "skill e2e marker: create the proof file exactly as the active skill instructs, then stop",
		"--max-continuations", "10",
		"--debug",
	)
	cmd.Dir = workspace
	cmd.Env = append(os.Environ(), "OPENAI_API_KEY="+apiKey)
	out, err := cmd.CombinedOutput()
	text := string(out)
	if err != nil {
		t.Fatalf("coder CLI smoke failed: %v\n%s", err, text)
	}
	for _, unwanted := range []string{"skill_state_missing", "skill not found"} {
		if strings.Contains(text, unwanted) {
			t.Fatalf("coder CLI smoke output contains %q:\n%s", unwanted, text)
		}
	}
	data, err := os.ReadFile(filepath.Join(workspace, "skill-proof.txt"))
	if err != nil {
		t.Fatalf("read proof file: %v\n%s", err, text)
	}
	if strings.TrimSpace(string(data)) != "SKILL_E2E_OK" {
		t.Fatalf("proof file = %q, want SKILL_E2E_OK\n%s", string(data), text)
	}
}
