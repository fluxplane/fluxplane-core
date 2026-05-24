// Package markdown defines Markdown language operation contracts.
package markdown

import "github.com/fluxplane/fluxplane-core/core/language"

const (
	Language      = language.LanguageMarkdown
	OutlineOp     = "markdown_outline"
	LinksOp       = "markdown_links"
	DiagnosticsOp = "markdown_diagnostics"
)

// Heading is one markdown heading in a nested document outline.
type Heading struct {
	Level    int       `json:"level"`
	Title    string    `json:"title"`
	Line     int       `json:"line,omitempty"`
	Anchor   string    `json:"anchor,omitempty"`
	Children []Heading `json:"children,omitempty"`
}

// Outline is a parsed markdown document outline.
type Outline struct {
	Path      string    `json:"path"`
	Title     string    `json:"title,omitempty"`
	Headings  []Heading `json:"headings,omitempty"`
	Truncated bool      `json:"truncated,omitempty"`
}

// LinkKind classifies a markdown link target.
type LinkKind string

const (
	LinkLocal    LinkKind = "local"
	LinkExternal LinkKind = "external"
	LinkAnchor   LinkKind = "anchor"
	LinkOther    LinkKind = "other"
)

// Link records one markdown link or image target.
type Link struct {
	Path       string   `json:"path"`
	Line       int      `json:"line,omitempty"`
	Text       string   `json:"text,omitempty"`
	Target     string   `json:"target"`
	Kind       LinkKind `json:"kind"`
	Image      bool     `json:"image,omitempty"`
	Heading    string   `json:"heading,omitempty"`
	TargetPath string   `json:"target_path,omitempty"`
	Fragment   string   `json:"fragment,omitempty"`
}

// Query selects markdown files or directories.
type Query struct {
	Language   language.LanguageID `json:"language,omitempty" jsonschema:"description=Language id. Defaults to markdown."`
	Path       string              `json:"path" jsonschema:"description=Workspace-relative markdown file or directory path.,required"`
	MaxResults int                 `json:"max_results,omitempty" jsonschema:"description=Maximum records returned."`
	MaxBytes   int                 `json:"max_bytes,omitempty" jsonschema:"description=Maximum bytes read from each markdown file."`
	Refresh    bool                `json:"refresh,omitempty" jsonschema:"description=Reserved for memory-backed language views."`
}

// OutlineResult contains markdown outlines.
type OutlineResult struct {
	Outlines []Outline `json:"outlines,omitempty"`
	Indexed  bool      `json:"indexed,omitempty"`
	Fresh    bool      `json:"fresh,omitempty"`
}

// LinksResult contains markdown links.
type LinksResult struct {
	Links []Link `json:"links,omitempty"`
	Fresh bool   `json:"fresh,omitempty"`
}

// DiagnosticsResult contains markdown diagnostics.
type DiagnosticsResult struct {
	Diagnostics []language.Diagnostic `json:"diagnostics,omitempty"`
	Links       []Link                `json:"links,omitempty"`
	Fresh       bool                  `json:"fresh,omitempty"`
}
