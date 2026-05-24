package datasource

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"

	coredata "github.com/fluxplane/fluxplane-core/core/data"
	coredatasource "github.com/fluxplane/fluxplane-core/core/datasource"
	runtimedata "github.com/fluxplane/fluxplane-core/runtime/data"
	runtimedatasource "github.com/fluxplane/fluxplane-core/runtime/datasource"
	operationruntime "github.com/fluxplane/fluxplane-core/runtime/operation"
	"gopkg.in/yaml.v3"
)

const FileDocumentEntity coredatasource.EntityType = "file.document"

type FileDocument struct {
	Path    string            `json:"path" datasource:"id,searchable,filterable" jsonschema:"description=Workspace-relative file path.,required"`
	Title   string            `json:"title,omitempty" datasource:"searchable" jsonschema:"description=Document title from frontmatter or first markdown heading."`
	Content string            `json:"content,omitempty" datasource:"searchable" jsonschema:"description=Document body text."`
	Meta    map[string]string `json:"meta,omitempty" datasource:"filterable" jsonschema:"description=String metadata parsed from frontmatter."`
}

// FilesystemDataSourceSpec describes workspace file datasource configuration
// and entities.
func FilesystemDataSourceSpec() coredata.SourceSpec {
	spec := runtimedata.SourceFromDatasource("filesystem", "filesystem", filesystemProvider{}.Entities())
	spec.ConfigSchema = operationruntime.SchemaFor[filesystemDatasourceConfig]()
	return spec
}

type filesystemDatasourceConfig struct {
	Path    string `json:"path,omitempty" jsonschema:"description=Workspace-relative directory or file path to index."`
	Include string `json:"include,omitempty" jsonschema:"description=Comma-separated glob patterns included from the filesystem datasource."`
}

// NewFilesystemProvider returns a datasource provider backed by an fs.FS.
func NewFilesystemProvider(root fs.FS) coredatasource.Provider {
	return filesystemProvider{root: root}
}

type filesystemProvider struct {
	root fs.FS
}

func (p filesystemProvider) Entities() []coredatasource.EntitySpec {
	entity := runtimedatasource.EntityOf[FileDocument](FileDocumentEntity, "Local markdown and text documents.")
	entity.Capabilities = []coredatasource.EntityCapability{coredatasource.EntityCapabilitySearch, coredatasource.EntityCapabilityGet, coredatasource.EntityCapabilitySemanticSearch}
	entity.Detectors = []coredatasource.DetectorSpec{{
		Name:          "file_document_path",
		Kind:          coredatasource.DetectorRegex,
		Pattern:       `(?:^|\s)([A-Za-z0-9_./-]+\.(?:md|markdown|txt))(?:\s|$)`,
		IDTemplate:    "$1",
		QueryTemplate: "$1",
		Confidence:    0.75,
	}}
	return []coredatasource.EntitySpec{entity}
}

func (p filesystemProvider) Open(_ context.Context, spec coredatasource.Spec) (coredatasource.Accessor, error) {
	if !specHasEntity(spec, FileDocumentEntity) {
		return nil, fmt.Errorf("unsupported entities %q", spec.Entities)
	}
	if spec.Kind != "filesystem" && spec.Kind != "file" && spec.Kind != "fs" {
		return nil, fmt.Errorf("unsupported datasource kind %q", spec.Kind)
	}
	if p.root == nil {
		return nil, fmt.Errorf("filesystem root is nil")
	}
	base := cleanRelative(spec.Config["path"])
	if base == "" {
		base = "."
	}
	include := splitCSV(spec.Config["include"])
	if len(include) == 0 {
		include = []string{"*.md", "*.markdown", "*.txt"}
	}
	return &filesystemAccessor{
		root:    p.root,
		spec:    spec,
		base:    base,
		include: include,
		entity:  p.Entities()[0],
	}, nil
}

type filesystemAccessor struct {
	root    fs.FS
	spec    coredatasource.Spec
	base    string
	include []string
	entity  coredatasource.EntitySpec
}

func (a *filesystemAccessor) Spec() coredatasource.Spec { return a.spec }
func (a *filesystemAccessor) Entities() []coredatasource.EntitySpec {
	return []coredatasource.EntitySpec{a.entity}
}

func (a *filesystemAccessor) Search(ctx context.Context, req coredatasource.SearchRequest) (coredatasource.SearchResult, error) {
	if req.Entity != FileDocumentEntity {
		return coredatasource.SearchResult{}, fmt.Errorf("datasource %q does not expose entity %q", a.spec.Name, req.Entity)
	}
	query := strings.ToLower(strings.TrimSpace(req.Query))
	limit := req.Limit
	var records []coredatasource.Record
	err := fs.WalkDir(a.root, a.base, func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if entry.IsDir() {
			if a.skippedDir(name) {
				return fs.SkipDir
			}
			return nil
		}
		if !a.included(name) {
			return nil
		}
		record, err := a.readRecord(name)
		if err != nil {
			return nil
		}
		if query == "" || recordMatches(record, query) {
			records = append(records, record)
			if limit > 0 && len(records) >= limit {
				return fs.SkipAll
			}
		}
		return nil
	})
	if err != nil {
		return coredatasource.SearchResult{}, err
	}
	return coredatasource.SearchResult{Datasource: a.spec.Name, Entity: req.Entity, Records: records, Total: len(records)}, nil
}

func (a *filesystemAccessor) Get(_ context.Context, req coredatasource.GetRequest) (coredatasource.Record, error) {
	if req.Entity != FileDocumentEntity {
		return coredatasource.Record{}, coredatasource.ErrNotFound
	}
	name := cleanRelative(req.ID)
	if name == "" {
		return coredatasource.Record{}, coredatasource.ErrNotFound
	}
	if a.base != "." && !strings.HasPrefix(name, strings.TrimSuffix(a.base, "/")+"/") && name != a.base {
		return coredatasource.Record{}, coredatasource.ErrNotFound
	}
	if !a.included(name) {
		return coredatasource.Record{}, coredatasource.ErrNotFound
	}
	record, err := a.readRecord(name)
	if err != nil {
		return coredatasource.Record{}, coredatasource.ErrNotFound
	}
	return record, nil
}

func (a *filesystemAccessor) Corpus(ctx context.Context, req coredatasource.CorpusRequest) (coredatasource.CorpusPage, error) {
	if req.Entity != FileDocumentEntity {
		return coredatasource.CorpusPage{}, fmt.Errorf("datasource %q does not expose entity %q", a.spec.Name, req.Entity)
	}
	limit := req.Limit
	var docs []coredatasource.CorpusDocument
	err := fs.WalkDir(a.root, a.base, func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if entry.IsDir() {
			if a.skippedDir(name) {
				return fs.SkipDir
			}
			return nil
		}
		if !a.included(name) {
			return nil
		}
		doc, err := a.readCorpusDocument(name)
		if err != nil {
			return nil
		}
		docs = append(docs, doc)
		if limit > 0 && len(docs) >= limit {
			return fs.SkipAll
		}
		return nil
	})
	if err != nil {
		return coredatasource.CorpusPage{}, err
	}
	return coredatasource.CorpusPage{Documents: docs, Complete: true}, nil
}

func (a *filesystemAccessor) skippedDir(name string) bool {
	if name == "." || name == a.base {
		return false
	}
	switch path.Base(name) {
	case ".git", ".agents", ".codex", "node_modules", "vendor":
		return true
	default:
		return false
	}
}

func (a *filesystemAccessor) included(name string) bool {
	base := path.Base(name)
	for _, pattern := range a.include {
		if ok, _ := path.Match(pattern, name); ok {
			return true
		}
		if ok, _ := path.Match(pattern, base); ok {
			return true
		}
	}
	return false
}

func (a *filesystemAccessor) readRecord(name string) (coredatasource.Record, error) {
	data, err := fs.ReadFile(a.root, name)
	if err != nil {
		return coredatasource.Record{}, err
	}
	meta, body := splitFrontmatter(string(data))
	title := meta["title"]
	if title == "" {
		title = firstMarkdownHeading(body)
	}
	if title == "" {
		title = path.Base(name)
	}
	return coredatasource.Record{
		ID:         name,
		Datasource: a.spec.Name,
		Entity:     FileDocumentEntity,
		Title:      title,
		Content:    truncate(body, 1200),
		Metadata:   meta,
	}, nil
}

func (a *filesystemAccessor) readCorpusDocument(name string) (coredatasource.CorpusDocument, error) {
	data, err := fs.ReadFile(a.root, name)
	if err != nil {
		return coredatasource.CorpusDocument{}, err
	}
	meta, body := splitFrontmatter(string(data))
	title := meta["title"]
	if title == "" {
		title = firstMarkdownHeading(body)
	}
	if title == "" {
		title = path.Base(name)
	}
	sum := sha256.Sum256(data)
	return coredatasource.CorpusDocument{
		Ref: coredatasource.RecordRef{
			Datasource: a.spec.Name,
			Entity:     FileDocumentEntity,
			ID:         name,
		},
		Title:       title,
		Body:        body,
		Metadata:    meta,
		Fingerprint: hex.EncodeToString(sum[:]),
	}, nil
}

func specHasEntity(spec coredatasource.Spec, target coredatasource.EntityType) bool {
	for _, entity := range spec.Entities {
		if entity == target {
			return true
		}
	}
	return false
}

func recordMatches(record coredatasource.Record, query string) bool {
	values := []string{record.ID, record.Title, record.Content}
	for key, value := range record.Metadata {
		values = append(values, key, value)
	}
	for _, value := range values {
		if strings.Contains(strings.ToLower(value), query) {
			return true
		}
	}
	return false
}

func splitFrontmatter(content string) (map[string]string, string) {
	if !strings.HasPrefix(content, "---\n") && !strings.HasPrefix(content, "---\r\n") {
		return nil, content
	}
	normalized := strings.ReplaceAll(content, "\r\n", "\n")
	end := strings.Index(normalized[4:], "\n---\n")
	if end < 0 {
		return nil, content
	}
	raw := normalized[4 : 4+end]
	body := normalized[4+end+5:]
	values := map[string]any{}
	if err := yaml.Unmarshal([]byte(raw), &values); err != nil {
		return nil, body
	}
	meta := map[string]string{}
	for key, value := range values {
		meta[key] = fmt.Sprint(value)
	}
	if len(meta) == 0 {
		return nil, body
	}
	return meta, body
}

func firstMarkdownHeading(content string) string {
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "#") {
			return strings.TrimSpace(strings.TrimLeft(line, "#"))
		}
	}
	return ""
}

func cleanRelative(value string) string {
	value = strings.TrimSpace(strings.ReplaceAll(value, "\\", "/"))
	value = strings.TrimPrefix(value, "/")
	cleaned := path.Clean(value)
	if cleaned == "." || cleaned == "" {
		return "."
	}
	if strings.HasPrefix(cleaned, "../") || cleaned == ".." {
		return ""
	}
	return cleaned
}

func splitCSV(value string) []string {
	var out []string
	for _, item := range strings.Split(value, ",") {
		if item = strings.TrimSpace(item); item != "" {
			out = append(out, item)
		}
	}
	sort.Strings(out)
	return out
}

func truncate(value string, max int) string {
	if max <= 0 || len(value) <= max {
		return value
	}
	return value[:max]
}
