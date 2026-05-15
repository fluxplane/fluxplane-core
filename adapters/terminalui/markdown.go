package terminalui

import (
	"io"
	"strings"

	"github.com/codewandler/markdown/stream"
	mdterminal "github.com/codewandler/markdown/terminal"
)

type markdownLiveRenderer = mdterminal.LiveRenderer

func newMarkdownRenderer(w io.Writer) *markdownLiveRenderer {
	if w == nil {
		w = io.Discard
	}
	return mdterminal.NewLiveRenderer(w, markdownRendererOptions()...)
}

func markdownRendererOptions() []mdterminal.RendererOption {
	return []mdterminal.RendererOption{
		mdterminal.WithAnsi(mdterminal.AnsiOn),
		mdterminal.WithParserOptions(stream.WithGFMAutolinks()),
	}
}

// RenderMarkdown renders one complete Markdown document to w.
func RenderMarkdown(w io.Writer, text string) error {
	renderer := newMarkdownRenderer(w)
	if _, err := renderer.Write([]byte(text)); err != nil {
		return err
	}
	return renderer.Flush()
}

func fencedCodeBlock(language, body string) string {
	body = strings.TrimRight(body, "\n")
	if strings.TrimSpace(body) == "" {
		return ""
	}
	return "```" + language + "\n" + body + "\n```\n"
}
