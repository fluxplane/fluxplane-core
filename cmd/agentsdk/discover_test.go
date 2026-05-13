package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDiscoverCommandRendersSkillReferences(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, ".agents/agents/main.md", "---\nname: main\nskills: [architecture]\n---\nMain.\n")
	writeTestFile(t, root, ".agents/skills/architecture/SKILL.md", "---\nname: architecture\ndescription: Architecture\n---\nBody.\n")
	writeTestFile(t, root, ".agents/skills/architecture/references/tradeoffs.md", "---\ntrigger: tradeoffs\n---\nRefs.\n")

	cmd := newRootCommand()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"discover", root})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v\n%s", err, out.String())
	}
	text := out.String()
	for _, want := range []string{"Sources:", "agents", "skills", "architecture", "references/tradeoffs.md", "Resolution:"} {
		if !strings.Contains(text, want) {
			t.Fatalf("discover output missing %q:\n%s", want, text)
		}
	}
}

func writeTestFile(t *testing.T, root, rel, data string) {
	t.Helper()
	path := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatalf("write %s: %v", rel, err)
	}
}
