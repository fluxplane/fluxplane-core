// Package project defines inert workspace project inventory models.
package project

import (
	"fmt"
	"strings"
)

// ID identifies one detected project within a workspace inventory.
type ID string

// FacetKind classifies one detected project capability or manifest.
type FacetKind string

const (
	FacetGoModule     FacetKind = "go_module"
	FacetGoWorkspace  FacetKind = "go_workspace"
	FacetNodePackage  FacetKind = "node_package"
	FacetTaskfile     FacetKind = "taskfile"
	FacetMakefile     FacetKind = "makefile"
	FacetMarkdownDocs FacetKind = "markdown_docs"
	FacetAgentsDir    FacetKind = "agents_dir"
	FacetClaudeDir    FacetKind = "claude_dir"
	FacetGitRepo      FacetKind = "git_repo"
	FacetCI           FacetKind = "ci"
)

// ParseStatus describes whether a manifest was parsed successfully.
type ParseStatus string

const (
	ParseStatusParsed      ParseStatus = "parsed"
	ParseStatusUnsupported ParseStatus = "unsupported"
	ParseStatusFailed      ParseStatus = "failed"
)

// Inventory is one bounded snapshot of detected workspace projects.
type Inventory struct {
	Root      string    `json:"root,omitempty"`
	Projects  []Project `json:"projects,omitempty"`
	Truncated bool      `json:"truncated,omitempty"`
	Summary   Summary   `json:"summary,omitempty"`
	Warnings  []Warning `json:"warnings,omitempty"`
}

// Summary contains coarse inventory counts.
type Summary struct {
	ProjectCount int            `json:"project_count,omitempty"`
	FacetCounts  map[string]int `json:"facet_counts,omitempty"`
}

// Project represents a detected workspace unit.
type Project struct {
	ID       ID        `json:"id"`
	Root     string    `json:"root"`
	Name     string    `json:"name,omitempty"`
	Kind     string    `json:"kind,omitempty"`
	ParentID ID        `json:"parent_id,omitempty"`
	Facets   []Facet   `json:"facets,omitempty"`
	Files    []FileRef `json:"files,omitempty"`
	Warnings []Warning `json:"warnings,omitempty"`
}

// Validate checks the project has stable identity and root fields.
func (p Project) Validate() error {
	if strings.TrimSpace(string(p.ID)) == "" {
		return fmt.Errorf("project: id is empty")
	}
	if strings.TrimSpace(p.Root) == "" {
		return fmt.Errorf("project: root is empty")
	}
	seen := map[string]bool{}
	for i, facet := range p.Facets {
		if strings.TrimSpace(string(facet.Kind)) == "" {
			return fmt.Errorf("project: facets[%d] kind is empty", i)
		}
		key := string(facet.Kind) + "\x00" + facet.Manifest.Path
		if seen[key] {
			return fmt.Errorf("project: duplicate facet %q for %q", facet.Kind, facet.Manifest.Path)
		}
		seen[key] = true
	}
	return nil
}

// Facet is one detected capability or manifest attached to a project.
type Facet struct {
	Kind        FacetKind         `json:"kind"`
	Name        string            `json:"name,omitempty"`
	Manifest    Manifest          `json:"manifest,omitempty"`
	Summary     map[string]string `json:"summary,omitempty"`
	RelatedDirs []string          `json:"related_dirs,omitempty"`
	Tasks       []Task            `json:"tasks,omitempty"`
	Documents   []DocumentOutline `json:"documents,omitempty"`
	Warnings    []Warning         `json:"warnings,omitempty"`
}

// Manifest records a detected manifest file and cheap parse metadata.
type Manifest struct {
	Path    string            `json:"path,omitempty"`
	Kind    string            `json:"kind,omitempty"`
	Status  ParseStatus       `json:"status,omitempty"`
	Summary map[string]string `json:"summary,omitempty"`
	Error   string            `json:"error,omitempty"`
}

// Task records a discovered project task entry point.
type Task struct {
	Name        string            `json:"name"`
	Kind        string            `json:"kind,omitempty"`
	Command     string            `json:"command,omitempty"`
	Path        string            `json:"path,omitempty"`
	Description string            `json:"description,omitempty"`
	Metadata    map[string]string `json:"metadata,omitempty"`
}

// DocumentOutline records a lightweight markdown document outline.
type DocumentOutline struct {
	Path      string    `json:"path"`
	Title     string    `json:"title,omitempty"`
	Headings  []Heading `json:"headings,omitempty"`
	Truncated bool      `json:"truncated,omitempty"`
}

// Heading is one markdown heading.
type Heading struct {
	Level    int       `json:"level"`
	Title    string    `json:"title"`
	Line     int       `json:"line,omitempty"`
	Children []Heading `json:"children,omitempty"`
}

// FileRef is a bounded project file reference.
type FileRef struct {
	Path string `json:"path"`
	Kind string `json:"kind,omitempty"`
	Size int64  `json:"size,omitempty"`
}

// Warning records a non-fatal inventory issue.
type Warning struct {
	Path    string `json:"path,omitempty"`
	Code    string `json:"code,omitempty"`
	Message string `json:"message"`
}

// InventoryQuery bounds project inventory discovery.
type InventoryQuery struct {
	Refresh    bool `json:"refresh,omitempty" jsonschema:"description=Force rebuilding in-memory project inventory for this request."`
	MaxResults int  `json:"max_results,omitempty" jsonschema:"description=Maximum number of projects or records returned."`
	MaxBytes   int  `json:"max_bytes,omitempty" jsonschema:"description=Maximum bytes read from any one manifest or document."`
}

// ProjectQuery selects one project.
type ProjectQuery struct {
	ProjectID ID     `json:"project_id,omitempty" jsonschema:"description=Project id returned by project_inventory."`
	Path      string `json:"path,omitempty" jsonschema:"description=Workspace-relative path used to find the nearest project."`
	Refresh   bool   `json:"refresh,omitempty" jsonschema:"description=Force rebuilding in-memory project inventory for this request."`
}

// FilesQuery bounds project file listing.
type FilesQuery struct {
	ProjectID  ID     `json:"project_id,omitempty" jsonschema:"description=Project id returned by project_inventory."`
	Path       string `json:"path,omitempty" jsonschema:"description=Workspace-relative project path."`
	FacetKind  string `json:"facet_kind,omitempty" jsonschema:"description=Optional facet kind filter."`
	Depth      int    `json:"depth,omitempty" jsonschema:"description=Maximum recursion depth."`
	MaxResults int    `json:"max_results,omitempty" jsonschema:"description=Maximum file entries returned."`
	Refresh    bool   `json:"refresh,omitempty" jsonschema:"description=Force rebuilding in-memory project inventory for this request."`
}

// TasksQuery selects project task entries.
type TasksQuery struct {
	ProjectID ID     `json:"project_id,omitempty" jsonschema:"description=Project id returned by project_inventory."`
	Path      string `json:"path,omitempty" jsonschema:"description=Workspace-relative path used to find the nearest project."`
	Kind      string `json:"kind,omitempty" jsonschema:"description=Optional task kind filter such as makefile, taskfile, or package_script."`
	Refresh   bool   `json:"refresh,omitempty" jsonschema:"description=Force rebuilding in-memory project inventory for this request."`
}

// DocsQuery selects markdown document outlines.
type DocsQuery struct {
	ProjectID  ID     `json:"project_id,omitempty" jsonschema:"description=Project id returned by project_inventory."`
	Path       string `json:"path,omitempty" jsonschema:"description=Workspace-relative project or markdown file path."`
	MaxResults int    `json:"max_results,omitempty" jsonschema:"description=Maximum document outlines returned."`
	MaxBytes   int    `json:"max_bytes,omitempty" jsonschema:"description=Maximum bytes read from each markdown file."`
	Refresh    bool   `json:"refresh,omitempty" jsonschema:"description=Force rebuilding in-memory project inventory for this request."`
}
