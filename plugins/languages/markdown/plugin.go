package markdown

import (
	"context"
	"fmt"
	"io/fs"
	"net/url"
	"path"
	"sort"
	"strings"
	"unicode"

	"github.com/fluxplane/fluxplane-core/core/language"
	"github.com/fluxplane/fluxplane-core/core/language/markdown"
	"github.com/fluxplane/fluxplane-core/core/operation"
	"github.com/fluxplane/fluxplane-core/core/resource"
	"github.com/fluxplane/fluxplane-core/orchestration/pluginhost"
	runtimelanguage "github.com/fluxplane/fluxplane-core/runtime/language"
	operationruntime "github.com/fluxplane/fluxplane-core/runtime/operation"
	runtimeworkspace "github.com/fluxplane/fluxplane-core/runtime/workspace"
	fpsystem "github.com/fluxplane/fluxplane-system"
	"github.com/yuin/goldmark"
	goldast "github.com/yuin/goldmark/ast"
	goldtext "github.com/yuin/goldmark/text"
)

const (
	Name               = "markdown"
	OutlineOp          = markdown.OutlineOp
	LinksOp            = markdown.LinksOp
	DiagnosticsOp      = markdown.DiagnosticsOp
	defaultMaxResults  = 200
	defaultSourceBytes = 128 * 1024
)

// Plugin contributes markdown language-support operations.
type Plugin struct {
	workspace runtimeworkspace.Workspace
}

var _ pluginhost.Plugin = Plugin{}
var _ pluginhost.OperationContributor = Plugin{}

// New returns a markdown language plugin.
func New(workspace runtimeworkspace.Workspace) Plugin {
	return Plugin{workspace: workspace}
}

// Manifest returns plugin metadata.
func (Plugin) Manifest() pluginhost.Manifest {
	return pluginhost.Manifest{Name: Name, Description: "Markdown outline and local link diagnostics."}
}

// LanguageSupport returns the reusable Markdown language activation descriptor.
func LanguageSupport() runtimelanguage.Support {
	specs := specs()
	return runtimelanguage.StaticSupport{Spec: runtimelanguage.SupportSpec{
		Provider: language.ProviderSpec{
			Name:        language.ProviderName(Name),
			Language:    language.LanguageMarkdown,
			Description: "Markdown outline and link support.",
			Capabilities: []language.Capability{
				language.CapabilityOutline,
				language.CapabilityDiagnostics,
			},
		},
		OperationSets: []operation.Set{{
			Name:        Name,
			Description: "Markdown outline, link listing, and local diagnostics.",
			Operations:  refs(specs),
		}},
	}}
}

// Contributions returns markdown operation specs.
func (Plugin) Contributions(context.Context, pluginhost.Context) (resource.ContributionBundle, error) {
	specs := specs()
	support := LanguageSupport().SupportSpec()
	return resource.ContributionBundle{
		OperationSets: support.OperationSets,
		Operations:    specs,
	}, nil
}

// Operations returns executable markdown operations.
func (p Plugin) Operations(context.Context, pluginhost.Context) ([]operation.Operation, error) {
	if p.workspace == nil {
		return nil, fmt.Errorf("markdownplugin: system workspace is nil")
	}
	return []operation.Operation{
		operationruntime.NewTypedResult[markdown.Query, operation.Rendered](specByName(OutlineOp), p.outline),
		operationruntime.NewTypedResult[markdown.Query, operation.Rendered](specByName(LinksOp), p.links),
		operationruntime.NewTypedResult[markdown.Query, operation.Rendered](specByName(DiagnosticsOp), p.diagnostics),
	}, nil
}

func specs() []operation.Spec {
	return []operation.Spec{
		spec(OutlineOp, "Parse markdown files with goldmark and return nested heading outlines."),
		spec(LinksOp, "List markdown links and images with source path, line, heading, and normalized local target information."),
		spec(DiagnosticsOp, "Run local-only markdown diagnostics for workspace-relative file links and markdown heading anchors. External URLs are reported as unchecked info; no network requests are made."),
	}
}

func spec(name, description string) operation.Spec {
	return operationruntime.WithTypedContract[markdown.Query, operation.Rendered](operation.Spec{
		Ref:         operation.Ref{Name: operation.Name(name)},
		Description: description,
		Semantics: operation.Semantics{
			Determinism: operation.DeterminismNonDeterministic,
			Effects:     operation.EffectSet{operation.EffectFilesystem, operation.EffectReadExternal},
			Idempotency: operation.IdempotencyIdempotent,
			Risk:        operation.RiskLow,
		},
	})
}

func specByName(name string) operation.Spec {
	for _, spec := range specs() {
		if string(spec.Ref.Name) == name {
			return spec
		}
	}
	return operation.Spec{Ref: operation.Ref{Name: operation.Name(name)}}
}

func refs(specs []operation.Spec) []operation.Ref {
	out := make([]operation.Ref, 0, len(specs))
	for _, spec := range specs {
		out = append(out, spec.Ref)
	}
	return out
}

func (p Plugin) outline(ctx operation.Context, req markdown.Query) operation.Result {
	if err := validateMarkdownQuery(req); err != nil {
		return operation.Failed("invalid_markdown_outline_input", err.Error(), nil)
	}
	files, err := p.markdownFiles(ctx, req)
	if err != nil {
		return operation.Failed("markdown_outline_failed", err.Error(), nil)
	}
	var outlines []markdown.Outline
	for _, file := range files {
		parsed, err := p.parse(ctx, file, maxBytes(req.MaxBytes))
		if err != nil {
			return operation.Failed("markdown_outline_failed", err.Error(), map[string]any{"path": file})
		}
		outlines = append(outlines, parsed.Outline)
	}
	lines := []string{fmt.Sprintf("Markdown outlines: %d", len(outlines))}
	for _, outline := range outlines {
		lines = append(lines, "- "+outline.Path)
		lines = append(lines, renderMarkdownHeadings(outline.Headings, 1, 20)...)
	}
	return operation.OK(operation.Rendered{Text: strings.Join(lines, "\n"), Data: map[string]any{"outlines": outlines}})
}

func (p Plugin) links(ctx operation.Context, req markdown.Query) operation.Result {
	if err := validateMarkdownQuery(req); err != nil {
		return operation.Failed("invalid_markdown_links_input", err.Error(), nil)
	}
	links, err := p.collectLinks(ctx, req)
	if err != nil {
		return operation.Failed("markdown_links_failed", err.Error(), nil)
	}
	lines := []string{fmt.Sprintf("Markdown links: %d", len(links))}
	for _, link := range links {
		lines = append(lines, fmt.Sprintf("- %s:%d %s -> %s", link.Path, link.Line, linkKindLabel(link), link.Target))
	}
	return operation.OK(operation.Rendered{Text: strings.Join(lines, "\n"), Data: map[string]any{"links": links}})
}

func (p Plugin) diagnostics(ctx operation.Context, req markdown.Query) operation.Result {
	if err := validateMarkdownQuery(req); err != nil {
		return operation.Failed("invalid_markdown_diagnostics_input", err.Error(), nil)
	}
	links, err := p.collectLinks(ctx, req)
	if err != nil {
		return operation.Failed("markdown_diagnostics_failed", err.Error(), nil)
	}
	diagnostics := p.checkLinks(ctx, links, maxBytes(req.MaxBytes))
	lines := []string{fmt.Sprintf("Markdown diagnostics: %d", len(diagnostics))}
	for _, diag := range diagnostics {
		lines = append(lines, fmt.Sprintf("- %s %s %s:%d %s", diag.Severity, diag.Code, diag.Path, diag.Line, diag.Message))
	}
	return operation.OK(operation.Rendered{Text: strings.Join(lines, "\n"), Data: map[string]any{"diagnostics": diagnostics, "links": links}})
}

func (p Plugin) collectLinks(ctx context.Context, req markdown.Query) ([]markdown.Link, error) {
	files, err := p.markdownFiles(ctx, req)
	if err != nil {
		return nil, err
	}
	var out []markdown.Link
	for _, file := range files {
		parsed, err := p.parse(ctx, file, maxBytes(req.MaxBytes))
		if err != nil {
			return nil, err
		}
		out = append(out, parsed.Links...)
	}
	return out, nil
}

func (p Plugin) markdownFiles(ctx context.Context, req markdown.Query) ([]string, error) {
	rel := cleanRel(req.Path)
	if strings.TrimSpace(req.Path) == "." {
		rel = "."
	}
	if rel == "" {
		return nil, fmt.Errorf("path is required")
	}
	max := maxResults(req.MaxResults)
	if info, _, err := statWorkspacePath(ctx, p.workspace, rel); err == nil && !info.IsDir() {
		if !isMarkdown(rel) {
			return nil, fmt.Errorf("path is not a markdown file")
		}
		return []string{rel}, nil
	}
	entries, err := walkWorkspace(ctx, p.workspace, rel, fpsystem.WalkOptions{Depth: 20, ShowHidden: true, MaxEntries: 10000, FilesOnly: true, SkipDirs: noisyDirs()})
	if err != nil {
		return nil, err
	}
	var files []string
	for _, entry := range entries {
		if isMarkdown(entry.Path) {
			files = append(files, entry.Path)
			if len(files) >= max {
				break
			}
		}
	}
	sort.Strings(files)
	return files, nil
}

type parsedMarkdown struct {
	Outline markdown.Outline
	Links   []markdown.Link
}

func (p Plugin) parse(ctx context.Context, rel string, maxBytes int) (parsedMarkdown, error) {
	data, truncated, _, err := readWorkspaceFile(ctx, p.workspace, rel, int64(maxBytes))
	if err != nil {
		return parsedMarkdown{}, err
	}
	doc := goldmark.New().Parser().Parse(goldtext.NewReader(data))
	headings := markdownHeadings(doc, data)
	outline := markdown.Outline{Path: rel, Headings: headings, Truncated: truncated}
	if title, ok := markdownTitle(headings); ok {
		outline.Title = title.Title
	}
	return parsedMarkdown{Outline: outline, Links: markdownLinks(doc, data, rel)}, nil
}

func statWorkspacePath(ctx context.Context, ws runtimeworkspace.Workspace, rel string) (fs.FileInfo, runtimeworkspace.ResolvedPath, error) {
	resolved, err := ws.ResolveExisting(ctx, rel)
	if err != nil {
		return nil, runtimeworkspace.ResolvedPath{}, err
	}
	fsys, err := runtimeworkspace.FileSystem(ws)
	if err != nil {
		return nil, runtimeworkspace.ResolvedPath{}, err
	}
	info, err := fsys.Stat(runtimeworkspace.PathName(resolved))
	return info, resolved, err
}

func readWorkspaceFile(ctx context.Context, ws runtimeworkspace.Workspace, rel string, maxBytes int64) ([]byte, bool, runtimeworkspace.ResolvedPath, error) {
	resolved, err := ws.ResolveExisting(ctx, rel)
	if err != nil {
		return nil, false, runtimeworkspace.ResolvedPath{}, err
	}
	fsys, err := runtimeworkspace.FileSystem(ws)
	if err != nil {
		return nil, false, runtimeworkspace.ResolvedPath{}, err
	}
	data, truncated, err := fpsystem.ReadFileLimit(ctx, fsys, runtimeworkspace.PathName(resolved), maxBytes)
	return data, truncated, resolved, err
}

func walkWorkspace(ctx context.Context, ws runtimeworkspace.Workspace, rel string, opts fpsystem.WalkOptions) ([]fpsystem.WalkEntry, error) {
	resolved, err := ws.ResolveExisting(ctx, rel)
	if err != nil {
		return nil, err
	}
	fsys, err := runtimeworkspace.FileSystem(ws)
	if err != nil {
		return nil, err
	}
	entries, _, err := fpsystem.Walk(ctx, fsys, runtimeworkspace.PathName(resolved), opts)
	return entries, err
}

func markdownHeadings(doc goldast.Node, source []byte) []markdown.Heading {
	var flat []markdown.Heading
	seen := map[string]int{}
	_ = goldast.Walk(doc, func(node goldast.Node, entering bool) (goldast.WalkStatus, error) {
		if !entering {
			return goldast.WalkContinue, nil
		}
		heading, ok := node.(*goldast.Heading)
		if !ok {
			return goldast.WalkContinue, nil
		}
		title := markdownNodeText(heading, source)
		if title == "" {
			return goldast.WalkContinue, nil
		}
		anchor := uniqueAnchor(title, seen)
		flat = append(flat, markdown.Heading{Level: heading.Level, Title: title, Line: nodeLine(heading, source), Anchor: anchor})
		return goldast.WalkSkipChildren, nil
	})
	return nestHeadings(flat)
}

func markdownLinks(doc goldast.Node, source []byte, rel string) []markdown.Link {
	var out []markdown.Link
	currentHeading := ""
	_ = goldast.Walk(doc, func(node goldast.Node, entering bool) (goldast.WalkStatus, error) {
		if !entering {
			return goldast.WalkContinue, nil
		}
		if heading, ok := node.(*goldast.Heading); ok {
			currentHeading = markdownNodeText(heading, source)
			return goldast.WalkContinue, nil
		}
		switch n := node.(type) {
		case *goldast.Link:
			target := string(n.Destination)
			out = append(out, markdownLink(rel, nodeLine(n, source), markdownNodeText(n, source), target, false, currentHeading))
			return goldast.WalkSkipChildren, nil
		case *goldast.Image:
			target := string(n.Destination)
			out = append(out, markdownLink(rel, nodeLine(n, source), markdownNodeText(n, source), target, true, currentHeading))
			return goldast.WalkSkipChildren, nil
		default:
			return goldast.WalkContinue, nil
		}
	})
	return out
}

func markdownLink(sourcePath string, line int, text, target string, image bool, heading string) markdown.Link {
	link := markdown.Link{Path: sourcePath, Line: line, Text: text, Target: target, Image: image, Heading: heading}
	kind, targetPath, fragment := classifyTarget(sourcePath, target)
	link.Kind = kind
	link.TargetPath = targetPath
	link.Fragment = fragment
	return link
}

func classifyTarget(sourcePath, raw string) (markdown.LinkKind, string, string) {
	if strings.TrimSpace(raw) == "" {
		return markdown.LinkOther, "", ""
	}
	parsed, err := url.Parse(raw)
	if err == nil && parsed.Host != "" && parsed.Scheme == "" {
		return markdown.LinkExternal, "", parsed.Fragment
	}
	if err == nil && parsed.Scheme != "" {
		if parsed.Scheme == "http" || parsed.Scheme == "https" {
			return markdown.LinkExternal, "", parsed.Fragment
		}
		return markdown.LinkOther, "", parsed.Fragment
	}
	if strings.HasPrefix(raw, "#") {
		return markdown.LinkAnchor, sourcePath, strings.TrimPrefix(raw, "#")
	}
	pathPart := raw
	fragment := ""
	if parsed != nil {
		pathPart = parsed.Path
		fragment = parsed.Fragment
	}
	if decoded, err := url.PathUnescape(pathPart); err == nil {
		pathPart = decoded
	}
	targetPath := cleanRel(pathPart)
	if targetPath == "" {
		targetPath = sourcePath
	} else if !strings.HasPrefix(pathPart, "/") {
		targetPath = cleanRel(path.Join(path.Dir(sourcePath), targetPath))
	}
	if fragment != "" && targetPath == sourcePath {
		return markdown.LinkAnchor, targetPath, fragment
	}
	return markdown.LinkLocal, targetPath, fragment
}

func (p Plugin) checkLinks(ctx context.Context, links []markdown.Link, maxBytes int) []language.Diagnostic {
	anchorCache := map[string]map[string]bool{}
	var out []language.Diagnostic
	for _, link := range links {
		switch link.Kind {
		case markdown.LinkExternal, markdown.LinkOther:
			out = append(out, language.Diagnostic{Path: link.Path, Line: link.Line, Severity: "info", Code: "unchecked_link", Message: "external or non-file link was not checked", Target: link.Target})
		case markdown.LinkAnchor, markdown.LinkLocal:
			if link.TargetPath == "" {
				out = append(out, language.Diagnostic{Path: link.Path, Line: link.Line, Severity: "error", Code: "missing_target", Message: "link target is empty", Target: link.Target})
				continue
			}
			if _, _, err := statWorkspacePath(ctx, p.workspace, link.TargetPath); err != nil {
				out = append(out, language.Diagnostic{Path: link.Path, Line: link.Line, Severity: "error", Code: "missing_target", Message: "link target file does not exist", Target: link.Target})
				continue
			}
			if link.Fragment == "" {
				continue
			}
			if !isMarkdown(link.TargetPath) {
				out = append(out, language.Diagnostic{Path: link.Path, Line: link.Line, Severity: "warning", Code: "unchecked_anchor", Message: "anchor on non-markdown target was not checked", Target: link.Target})
				continue
			}
			anchors, ok := anchorCache[link.TargetPath]
			if !ok {
				parsed, err := p.parse(ctx, link.TargetPath, maxBytes)
				if err != nil {
					out = append(out, language.Diagnostic{Path: link.Path, Line: link.Line, Severity: "error", Code: "target_parse_failed", Message: err.Error(), Target: link.Target})
					continue
				}
				anchors = headingAnchors(parsed.Outline.Headings)
				anchorCache[link.TargetPath] = anchors
			}
			want := normalizeAnchor(link.Fragment)
			if !anchors[want] {
				out = append(out, language.Diagnostic{Path: link.Path, Line: link.Line, Severity: "error", Code: "missing_anchor", Message: "markdown heading anchor does not exist", Target: link.Target})
			}
		}
	}
	return out
}

func validateMarkdownQuery(req markdown.Query) error {
	if req.Language != "" && req.Language != language.LanguageMarkdown {
		return fmt.Errorf("unsupported language %q; this operation only supports %q", req.Language, language.LanguageMarkdown)
	}
	if strings.TrimSpace(req.Path) == "" {
		return fmt.Errorf("path is required")
	}
	return nil
}

func markdownNodeText(node goldast.Node, source []byte) string {
	var parts []string
	_ = goldast.Walk(node, func(child goldast.Node, entering bool) (goldast.WalkStatus, error) {
		if !entering || child == node {
			return goldast.WalkContinue, nil
		}
		switch n := child.(type) {
		case *goldast.Text:
			parts = append(parts, string(n.Value(source)))
		case *goldast.String:
			parts = append(parts, string(n.Value))
		}
		return goldast.WalkContinue, nil
	})
	return strings.Join(strings.Fields(strings.Join(parts, "")), " ")
}

func nodeLine(node goldast.Node, source []byte) int {
	for current := node; current != nil; current = current.Parent() {
		if current.Type() != goldast.TypeBlock {
			continue
		}
		lines := current.Lines()
		if lines == nil || lines.Len() == 0 {
			continue
		}
		segment := lines.At(0)
		if segment.Start >= 0 && segment.Start <= len(source) {
			return 1 + strings.Count(string(source[:segment.Start]), "\n")
		}
	}
	return 0
}

func nestHeadings(flat []markdown.Heading) []markdown.Heading {
	var roots []markdown.Heading
	type stackEntry struct {
		level    int
		children *[]markdown.Heading
	}
	stack := []stackEntry{{level: 0, children: &roots}}
	for _, heading := range flat {
		heading.Children = nil
		for len(stack) > 1 && stack[len(stack)-1].level >= heading.Level {
			stack = stack[:len(stack)-1]
		}
		parent := stack[len(stack)-1]
		*parent.children = append(*parent.children, heading)
		idx := len(*parent.children) - 1
		stack = append(stack, stackEntry{level: heading.Level, children: &(*parent.children)[idx].Children})
	}
	return roots
}

func markdownTitle(headings []markdown.Heading) (markdown.Heading, bool) {
	var first *markdown.Heading
	var walk func([]markdown.Heading) (markdown.Heading, bool)
	walk = func(items []markdown.Heading) (markdown.Heading, bool) {
		for i := range items {
			if first == nil {
				first = &items[i]
			}
			if items[i].Level == 1 {
				return items[i], true
			}
			if heading, ok := walk(items[i].Children); ok {
				return heading, true
			}
		}
		return markdown.Heading{}, false
	}
	if heading, ok := walk(headings); ok {
		return heading, true
	}
	if first != nil {
		return *first, true
	}
	return markdown.Heading{}, false
}

func renderMarkdownHeadings(headings []markdown.Heading, depth, limit int) []string {
	var lines []string
	var walk func([]markdown.Heading, int)
	walk = func(items []markdown.Heading, currentDepth int) {
		for _, heading := range items {
			if limit > 0 && len(lines) >= limit {
				return
			}
			lines = append(lines, fmt.Sprintf("%s%s %s", strings.Repeat("  ", currentDepth), strings.Repeat("#", heading.Level), heading.Title))
			walk(heading.Children, currentDepth+1)
		}
	}
	walk(headings, depth)
	return lines
}

func headingAnchors(headings []markdown.Heading) map[string]bool {
	out := map[string]bool{}
	var walk func([]markdown.Heading)
	walk = func(items []markdown.Heading) {
		for _, heading := range items {
			out[normalizeAnchor(heading.Anchor)] = true
			out[normalizeAnchor(heading.Title)] = true
			walk(heading.Children)
		}
	}
	walk(headings)
	return out
}

func uniqueAnchor(title string, seen map[string]int) string {
	base := normalizeAnchor(title)
	if base == "" {
		base = "section"
	}
	count := seen[base]
	seen[base] = count + 1
	if count == 0 {
		return base
	}
	return fmt.Sprintf("%s-%d", base, count)
}

func normalizeAnchor(raw string) string {
	if decoded, err := url.PathUnescape(raw); err == nil {
		raw = decoded
	}
	raw = strings.TrimSpace(strings.ToLower(raw))
	var b strings.Builder
	lastDash := false
	for _, r := range raw {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteRune(r)
			lastDash = false
		case unicode.IsSpace(r) || r == '-':
			if !lastDash && b.Len() > 0 {
				b.WriteByte('-')
				lastDash = true
			}
		}
	}
	return strings.Trim(b.String(), "-")
}

func linkKindLabel(link markdown.Link) string {
	if link.Image {
		return string(link.Kind) + " image"
	}
	return string(link.Kind) + " link"
}

func isMarkdown(rel string) bool {
	ext := strings.ToLower(path.Ext(rel))
	return ext == ".md" || ext == ".markdown"
}

func maxResults(value int) int {
	if value <= 0 || value > defaultMaxResults {
		return defaultMaxResults
	}
	return value
}

func maxBytes(value int) int {
	if value <= 0 || value > defaultSourceBytes {
		return defaultSourceBytes
	}
	return value
}

func cleanRel(raw string) string {
	raw = strings.TrimSpace(strings.ReplaceAll(raw, "\\", "/"))
	if raw == "" || raw == "." {
		return ""
	}
	clean := path.Clean(raw)
	if clean == "." {
		return ""
	}
	return strings.TrimPrefix(strings.TrimPrefix(clean, "./"), "/")
}

func noisyDirs() []string {
	return []string{".git", ".cache", "node_modules", "vendor", "dist", "build", "target", "tmp"}
}
