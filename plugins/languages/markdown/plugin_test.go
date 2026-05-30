package markdown

import (
	"context"
	"strings"
	"testing"

	"github.com/fluxplane/fluxplane-core/core/language"
	"github.com/fluxplane/fluxplane-core/core/operation"
	"github.com/fluxplane/fluxplane-core/orchestration/pluginhost"
	"github.com/fluxplane/fluxplane-core/runtime/system"
	"github.com/fluxplane/fluxplane-core/runtime/systemtest"
	runtimeworkspace "github.com/fluxplane/fluxplane-core/runtime/workspace"
	fpsystem "github.com/fluxplane/fluxplane-system"
)

func TestMarkdownOperationsWithMemoryAndHostWorkspaces(t *testing.T) {
	runMarkdownBackends(t, func(t *testing.T, sys system.System) {
		writeMarkdownFile(t, sys.Workspace(), "README.md", `# Root *Title*

[Good](docs/guide.md#install)
[Missing](docs/missing.md)
[Bad Anchor](#missing)
[External](https://example.com)
[Protocol Relative](//cdn.example.com/image.png)
![Image](assets/logo.png)

`+"```md\n# Not Heading\n```\n"+`
## Usage
`)
		writeMarkdownFile(t, sys.Workspace(), "docs/guide.md", `Guide
=====

## Install
`)
		writeMarkdownFile(t, sys.Workspace(), "assets/logo.png", "png")

		outline := runMarkdownOp(t, sys, OutlineOp, map[string]any{"path": "README.md"})
		if !strings.Contains(outline.Text, "# Root Title") || !strings.Contains(outline.Text, "## Usage") || strings.Contains(outline.Text, "Not Heading") {
			t.Fatalf("outline text = %q", outline.Text)
		}

		links := runMarkdownOp(t, sys, LinksOp, map[string]any{"path": "README.md"})
		if !strings.Contains(links.Text, "docs/guide.md#install") || !strings.Contains(links.Text, "assets/logo.png") {
			t.Fatalf("links text = %q", links.Text)
		}

		rootOutline := runMarkdownOp(t, sys, OutlineOp, map[string]any{"path": ".", "max_results": 10})
		if !strings.Contains(rootOutline.Text, "README.md") || !strings.Contains(rootOutline.Text, "docs/guide.md") {
			t.Fatalf("root outline text = %q", rootOutline.Text)
		}
		rootLinks := runMarkdownOp(t, sys, LinksOp, map[string]any{"path": ".", "max_results": 10})
		if !strings.Contains(rootLinks.Text, "//cdn.example.com/image.png") {
			t.Fatalf("root links text = %q", rootLinks.Text)
		}

		diagnostics := runMarkdownOp(t, sys, DiagnosticsOp, map[string]any{"path": "README.md"})
		for _, want := range []string{"missing_target", "missing_anchor", "unchecked_link"} {
			if !strings.Contains(diagnostics.Text, want) {
				t.Fatalf("diagnostics text = %q, want %q", diagnostics.Text, want)
			}
		}
		if strings.Contains(diagnostics.Text, "docs/guide.md#install") {
			t.Fatalf("diagnostics text = %q, valid cross-file anchor should pass", diagnostics.Text)
		}
		foundProtocolRelative := false
		for _, diag := range diagnosticsFor(t, diagnostics) {
			if diag.Target == "//cdn.example.com/image.png" && diag.Code != "unchecked_link" {
				t.Fatalf("protocol-relative diagnostic = %#v, want unchecked_link", diag)
			}
			if diag.Target == "//cdn.example.com/image.png" && diag.Code == "unchecked_link" {
				foundProtocolRelative = true
			}
		}
		if !foundProtocolRelative {
			t.Fatalf("diagnostics = %#v, want unchecked protocol-relative link", diagnosticsFor(t, diagnostics))
		}
	})
}

func runMarkdownBackends(t *testing.T, fn func(*testing.T, system.System)) {
	t.Helper()
	t.Run("memory", func(t *testing.T) {
		fn(t, systemtest.NewMemory())
	})
	t.Run("host", func(t *testing.T) {
		sys, err := system.NewHost(system.Config{Root: t.TempDir()})
		if err != nil {
			t.Fatalf("NewHost: %v", err)
		}
		fn(t, sys)
	})
}

func runMarkdownOp(t *testing.T, sys system.System, name string, input map[string]any) operation.Rendered {
	t.Helper()
	ops, err := New(sys.Workspace()).Operations(context.Background(), pluginhost.Context{})
	if err != nil {
		t.Fatalf("Operations: %v", err)
	}
	for _, op := range ops {
		if string(op.Spec().Ref.Name) == name {
			result := op.Run(operation.NewContext(context.Background(), nil), input)
			if result.Status != operation.StatusOK {
				t.Fatalf("%s status = %s error = %#v", name, result.Status, result.Error)
			}
			rendered, ok := result.Output.(operation.Rendered)
			if !ok {
				t.Fatalf("%s output = %#v, want Rendered", name, result.Output)
			}
			return rendered
		}
	}
	t.Fatalf("operation %s not found", name)
	return operation.Rendered{}
}

func writeMarkdownFile(t *testing.T, ws runtimeworkspace.Workspace, rel, content string) {
	t.Helper()
	resolved, err := ws.ResolveCreate(context.Background(), rel)
	if err != nil {
		t.Fatalf("ResolveCreate(%s): %v", rel, err)
	}
	fsys, err := runtimeworkspace.FileSystem(ws)
	if err != nil {
		t.Fatalf("FileSystem(%s): %v", rel, err)
	}
	if err := fsys.WriteFile(context.Background(), runtimeworkspace.PathName(resolved), []byte(content), fpsystem.WriteFileOptions{Perm: 0644, Overwrite: true}); err != nil {
		t.Fatalf("WriteFile(%s): %v", rel, err)
	}
}

func diagnosticsFor(t *testing.T, rendered operation.Rendered) []language.Diagnostic {
	t.Helper()
	data, ok := rendered.Data.(map[string]any)
	if !ok {
		t.Fatalf("data = %#v, want map", rendered.Data)
	}
	diagnostics, ok := data["diagnostics"].([]language.Diagnostic)
	if !ok {
		t.Fatalf("diagnostics data = %#v, want []language.Diagnostic", data["diagnostics"])
	}
	return diagnostics
}
