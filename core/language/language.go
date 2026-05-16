// Package language defines inert language-support models shared by plugins.
package language

import (
	"context"
	"fmt"
	"strings"
)

// LanguageID identifies a programming or document language.
type LanguageID string

const (
	LanguageGo       LanguageID = "go"
	LanguageMarkdown LanguageID = "markdown"
)

// Capability describes a language provider feature.
type Capability string

const (
	CapabilityProject         Capability = "project"
	CapabilityPackage         Capability = "package"
	CapabilityOutline         Capability = "outline"
	CapabilitySymbol          Capability = "symbol"
	CapabilityDefinition      Capability = "definition"
	CapabilitySymbolInfo      Capability = "symbol_info"
	CapabilityReferences      Capability = "references"
	CapabilityImplementations Capability = "implementations"
	CapabilityCalls           Capability = "calls"
	CapabilityImports         Capability = "imports"
	CapabilityDiagnostics     Capability = "diagnostics"
	CapabilityFormat          Capability = "format"
	CapabilityRename          Capability = "rename"
)

// ProviderName identifies a language-support provider.
type ProviderName string

// ProviderSpec describes a language provider without binding it to IO.
type ProviderSpec struct {
	Name         ProviderName `json:"name"`
	Language     LanguageID   `json:"language"`
	Description  string       `json:"description,omitempty"`
	Capabilities []Capability `json:"capabilities,omitempty"`
}

// Validate checks the provider spec has stable identity.
func (s ProviderSpec) Validate() error {
	if strings.TrimSpace(string(s.Name)) == "" {
		return fmt.Errorf("language: provider name is empty")
	}
	if strings.TrimSpace(string(s.Language)) == "" {
		return fmt.Errorf("language: provider language is empty")
	}
	return nil
}

// Position is a one-indexed source position.
type Position struct {
	Line   int `json:"line,omitempty"`
	Column int `json:"column,omitempty"`
}

// Range is a source span.
type Range struct {
	Start Position `json:"start,omitempty"`
	End   Position `json:"end,omitempty"`
}

// Location identifies a workspace-relative source range.
type Location struct {
	Path  string `json:"path"`
	Range Range  `json:"range,omitempty"`
}

// Package describes a language package/module unit.
type Package struct {
	ID         string     `json:"id"`
	Language   LanguageID `json:"language"`
	Name       string     `json:"name,omitempty"`
	ImportPath string     `json:"import_path,omitempty"`
	Dir        string     `json:"dir,omitempty"`
	Files      []string   `json:"files,omitempty"`
	Imports    []Import   `json:"imports,omitempty"`
	TestFor    string     `json:"test_for,omitempty"`
}

// Document describes one parsed source document.
type Document struct {
	Path      string     `json:"path"`
	Language  LanguageID `json:"language"`
	PackageID string     `json:"package_id,omitempty"`
	Outline   Outline    `json:"outline,omitempty"`
}

// Outline is an ordered symbol outline for one document or package.
type Outline struct {
	Path      string     `json:"path,omitempty"`
	PackageID string     `json:"package_id,omitempty"`
	Language  LanguageID `json:"language,omitempty"`
	Symbols   []Symbol   `json:"symbols,omitempty"`
	Truncated bool       `json:"truncated,omitempty"`
}

// Diagnostic describes a recoverable language analysis issue.
type Diagnostic struct {
	Path     string `json:"path,omitempty"`
	Severity string `json:"severity,omitempty"`
	Code     string `json:"code,omitempty"`
	Message  string `json:"message,omitempty"`
	Target   string `json:"target,omitempty"`
	Line     int    `json:"line,omitempty"`
}

// MarkdownHeading is one markdown heading in a nested document outline.
type MarkdownHeading struct {
	Level    int               `json:"level"`
	Title    string            `json:"title"`
	Line     int               `json:"line,omitempty"`
	Anchor   string            `json:"anchor,omitempty"`
	Children []MarkdownHeading `json:"children,omitempty"`
}

// MarkdownOutline is a parsed markdown document outline.
type MarkdownOutline struct {
	Path      string            `json:"path"`
	Title     string            `json:"title,omitempty"`
	Headings  []MarkdownHeading `json:"headings,omitempty"`
	Truncated bool              `json:"truncated,omitempty"`
}

// MarkdownLinkKind classifies a markdown link target.
type MarkdownLinkKind string

const (
	MarkdownLinkLocal    MarkdownLinkKind = "local"
	MarkdownLinkExternal MarkdownLinkKind = "external"
	MarkdownLinkAnchor   MarkdownLinkKind = "anchor"
	MarkdownLinkOther    MarkdownLinkKind = "other"
)

// MarkdownLink records one markdown link or image target.
type MarkdownLink struct {
	Path       string           `json:"path"`
	Line       int              `json:"line,omitempty"`
	Text       string           `json:"text,omitempty"`
	Target     string           `json:"target"`
	Kind       MarkdownLinkKind `json:"kind"`
	Image      bool             `json:"image,omitempty"`
	Heading    string           `json:"heading,omitempty"`
	TargetPath string           `json:"target_path,omitempty"`
	Fragment   string           `json:"fragment,omitempty"`
}

// MarkdownQuery selects markdown files or directories.
type MarkdownQuery struct {
	Language   LanguageID `json:"language,omitempty" jsonschema:"description=Language id. Defaults to markdown."`
	Path       string     `json:"path" jsonschema:"description=Workspace-relative markdown file or directory path.,required"`
	MaxResults int        `json:"max_results,omitempty" jsonschema:"description=Maximum records returned."`
	MaxBytes   int        `json:"max_bytes,omitempty" jsonschema:"description=Maximum bytes read from each markdown file."`
	Refresh    bool       `json:"refresh,omitempty" jsonschema:"description=Reserved for memory-backed language views."`
}

// MarkdownOutlineResult contains markdown outlines.
type MarkdownOutlineResult struct {
	Outlines []MarkdownOutline `json:"outlines,omitempty"`
	Indexed  bool              `json:"indexed,omitempty"`
	Fresh    bool              `json:"fresh,omitempty"`
}

// MarkdownLinksResult contains markdown links.
type MarkdownLinksResult struct {
	Links []MarkdownLink `json:"links,omitempty"`
	Fresh bool           `json:"fresh,omitempty"`
}

// MarkdownDiagnosticsResult contains markdown diagnostics.
type MarkdownDiagnosticsResult struct {
	Diagnostics []Diagnostic   `json:"diagnostics,omitempty"`
	Links       []MarkdownLink `json:"links,omitempty"`
	Fresh       bool           `json:"fresh,omitempty"`
}

// SymbolKind classifies one language symbol.
type SymbolKind string

const (
	SymbolModule    SymbolKind = "module"
	SymbolPackage   SymbolKind = "package"
	SymbolType      SymbolKind = "type"
	SymbolStruct    SymbolKind = "struct"
	SymbolInterface SymbolKind = "interface"
	SymbolFunction  SymbolKind = "function"
	SymbolMethod    SymbolKind = "method"
	SymbolField     SymbolKind = "field"
	SymbolConst     SymbolKind = "const"
	SymbolVar       SymbolKind = "var"
	SymbolImport    SymbolKind = "import"
	SymbolNamespace SymbolKind = "namespace"
)

// Symbol is a language-neutral source declaration.
type Symbol struct {
	ID             string     `json:"id,omitempty"`
	Language       LanguageID `json:"language,omitempty"`
	Kind           SymbolKind `json:"kind"`
	Name           string     `json:"name"`
	Container      string     `json:"container,omitempty"`
	PackageID      string     `json:"package_id,omitempty"`
	Location       Location   `json:"location,omitempty"`
	Range          Range      `json:"range,omitempty"`
	SelectionRange Range      `json:"selection_range,omitempty"`
	Signature      string     `json:"signature,omitempty"`
	Doc            string     `json:"doc,omitempty"`
	Children       []Symbol   `json:"children,omitempty"`
}

// NavigationScope bounds a position-based navigation lookup.
type NavigationScope string

const (
	NavigationScopeFile    NavigationScope = "file"
	NavigationScopePackage NavigationScope = "package"
)

// NavigationQuery selects a source position for language navigation.
type NavigationQuery struct {
	Language    LanguageID      `json:"language,omitempty" jsonschema:"description=Language id. Defaults to the provider language."`
	Path        string          `json:"path" jsonschema:"description=Workspace-relative Go source file path.,required"`
	Line        int             `json:"line,omitempty" jsonschema:"description=1-indexed source line. Required unless offset is set."`
	Column      int             `json:"column,omitempty" jsonschema:"description=1-indexed byte column. Required unless offset is set."`
	Offset      *int            `json:"offset,omitempty" jsonschema:"description=0-indexed byte offset. Takes precedence over line and column."`
	Scope       NavigationScope `json:"scope,omitempty" jsonschema:"description=Lookup scope. Defaults to package.,enum=file,enum=package"`
	IncludeDocs bool            `json:"include_docs,omitempty" jsonschema:"description=Include bounded documentation comments."`
	MaxResults  int             `json:"max_results,omitempty" jsonschema:"description=Maximum symbols or locations returned."`
	MaxBytes    int             `json:"max_bytes,omitempty" jsonschema:"description=Maximum bytes read from each source file."`
	Refresh     bool            `json:"refresh,omitempty" jsonschema:"description=Reserved for memory-backed language views."`
}

// NavigationTarget describes the source token selected by a navigation query.
type NavigationTarget struct {
	Text            string   `json:"text,omitempty"`
	Name            string   `json:"name,omitempty"`
	NodeKind        string   `json:"node_kind,omitempty"`
	PackageID       string   `json:"package_id,omitempty"`
	Location        Location `json:"location,omitempty"`
	EnclosingSymbol *Symbol  `json:"enclosing_symbol,omitempty"`
}

// NavigationResult is the structured result of a position-based language lookup.
type NavigationResult struct {
	Target         NavigationTarget `json:"target,omitempty"`
	Symbols        []Symbol         `json:"symbols,omitempty"`
	Locations      []Location       `json:"locations,omitempty"`
	Diagnostics    []Diagnostic     `json:"diagnostics,omitempty"`
	ResolutionMode string           `json:"resolution_mode,omitempty"`
	Complete       bool             `json:"complete,omitempty"`
	Warnings       []string         `json:"warnings,omitempty"`
	Indexed        bool             `json:"indexed,omitempty"`
	Fresh          bool             `json:"fresh,omitempty"`
}

// ReferenceQuery selects a source position for language reference lookup.
type ReferenceQuery struct {
	Language           LanguageID      `json:"language,omitempty" jsonschema:"description=Language id. Defaults to the provider language."`
	Path               string          `json:"path" jsonschema:"description=Workspace-relative source file path.,required"`
	Line               int             `json:"line,omitempty" jsonschema:"description=1-indexed source line. Required unless offset is set."`
	Column             int             `json:"column,omitempty" jsonschema:"description=1-indexed byte column. Required unless offset is set."`
	Offset             *int            `json:"offset,omitempty" jsonschema:"description=0-indexed byte offset. Takes precedence over line and column. Offset 0 is valid."`
	Scope              NavigationScope `json:"scope,omitempty" jsonschema:"description=Reference scan scope. Defaults to package.,enum=file,enum=package"`
	IncludeDeclaration bool            `json:"include_declaration,omitempty" jsonschema:"description=Include the selected symbol declaration as a reference result."`
	IncludeTests       *bool           `json:"include_tests,omitempty" jsonschema:"description=Include _test.go files in package-scope scans. Defaults to true."`
	MaxResults         int             `json:"max_results,omitempty" jsonschema:"description=Maximum references returned."`
	MaxBytes           int             `json:"max_bytes,omitempty" jsonschema:"description=Maximum bytes read from each source file."`
	Refresh            bool            `json:"refresh,omitempty" jsonschema:"description=Reserved for memory-backed language views."`
}

// ReferenceResult is the structured result of a position-based reference lookup.
type ReferenceResult struct {
	Target         NavigationTarget `json:"target,omitempty"`
	Symbol         Symbol           `json:"symbol,omitempty"`
	References     []Reference      `json:"references,omitempty"`
	Diagnostics    []Diagnostic     `json:"diagnostics,omitempty"`
	ResolutionMode string           `json:"resolution_mode,omitempty"`
	Complete       bool             `json:"complete,omitempty"`
	Warnings       []string         `json:"warnings,omitempty"`
	Indexed        bool             `json:"indexed,omitempty"`
	Fresh          bool             `json:"fresh,omitempty"`
}

// ImportDirection selects which import relationships to return.
type ImportDirection string

const (
	ImportDirectionDirect  ImportDirection = "direct"
	ImportDirectionReverse ImportDirection = "reverse"
	ImportDirectionBoth    ImportDirection = "both"
)

// ImportClass is a best-effort import target classification.
type ImportClass string

const (
	ImportClassStdlib      ImportClass = "stdlib"
	ImportClassModuleLocal ImportClass = "module_local"
	ImportClassExternal    ImportClass = "external"
	ImportClassUnknown     ImportClass = "unknown"
)

// ImportQuery selects direct and reverse language import edges.
type ImportQuery struct {
	Language     LanguageID      `json:"language,omitempty" jsonschema:"description=Language id. Defaults to the provider language."`
	Path         string          `json:"path,omitempty" jsonschema:"description=Workspace-relative Go file, package directory, module, or project path."`
	PackageID    string          `json:"package_id,omitempty" jsonschema:"description=Optional package id returned by go_packages."`
	ImportPath   string          `json:"import_path,omitempty" jsonschema:"description=Optional import path filter, primarily for reverse import lookup."`
	Direction    ImportDirection `json:"direction,omitempty" jsonschema:"description=Import relationship direction. Defaults to both.,enum=direct,enum=reverse,enum=both"`
	IncludeTests *bool           `json:"include_tests,omitempty" jsonschema:"description=Include _test.go files. Defaults to true."`
	MaxResults   int             `json:"max_results,omitempty" jsonschema:"description=Maximum import edges returned."`
	MaxBytes     int             `json:"max_bytes,omitempty" jsonschema:"description=Maximum bytes read from each source file."`
	Refresh      bool            `json:"refresh,omitempty" jsonschema:"description=Reserved for memory-backed language views."`
}

// ImportResult contains direct and reverse import edges.
type ImportResult struct {
	DirectImports    []Import     `json:"direct_imports,omitempty"`
	ReverseImporters []Import     `json:"reverse_importers,omitempty"`
	TargetImportPath string       `json:"target_import_path,omitempty"`
	Diagnostics      []Diagnostic `json:"diagnostics,omitempty"`
	ResolutionMode   string       `json:"resolution_mode,omitempty"`
	Complete         bool         `json:"complete,omitempty"`
	Warnings         []string     `json:"warnings,omitempty"`
	Indexed          bool         `json:"indexed,omitempty"`
	Fresh            bool         `json:"fresh,omitempty"`
}

// ImplementationScope bounds an implementation lookup.
type ImplementationScope string

const (
	ImplementationScopePackage ImplementationScope = "package"
	ImplementationScopeModule  ImplementationScope = "module"
)

// ImplementationRelation describes a best-effort implementation relationship.
type ImplementationRelation string

const (
	ImplementationRelationValue                ImplementationRelation = "value"
	ImplementationRelationPointer              ImplementationRelation = "pointer"
	ImplementationRelationMethodCorrespondence ImplementationRelation = "method_correspondence"
)

// ImplementationQuery selects a source position for implementation lookup.
type ImplementationQuery struct {
	Language     LanguageID          `json:"language,omitempty" jsonschema:"description=Language id. Defaults to the provider language."`
	Path         string              `json:"path" jsonschema:"description=Workspace-relative Go source file path.,required"`
	Line         int                 `json:"line,omitempty" jsonschema:"description=1-indexed source line. Required unless offset is set."`
	Column       int                 `json:"column,omitempty" jsonschema:"description=1-indexed byte column. Required unless offset is set."`
	Offset       *int                `json:"offset,omitempty" jsonschema:"description=0-indexed byte offset. Takes precedence over line and column. Offset 0 is valid."`
	Scope        ImplementationScope `json:"scope,omitempty" jsonschema:"description=Lookup scope. Defaults to package.,enum=package,enum=module"`
	IncludeTests *bool               `json:"include_tests,omitempty" jsonschema:"description=Include _test.go files. Defaults to true."`
	MaxResults   int                 `json:"max_results,omitempty" jsonschema:"description=Maximum implementation matches returned."`
	MaxBytes     int                 `json:"max_bytes,omitempty" jsonschema:"description=Maximum bytes read from each source file."`
	Refresh      bool                `json:"refresh,omitempty" jsonschema:"description=Reserved for memory-backed language views."`
}

// ImplementationMatch describes one best-effort implementation relationship.
type ImplementationMatch struct {
	Interface      Symbol                 `json:"interface,omitempty"`
	Concrete       Symbol                 `json:"concrete,omitempty"`
	Relation       ImplementationRelation `json:"relation,omitempty"`
	MatchedMethods []string               `json:"matched_methods,omitempty"`
	MissingMethods []string               `json:"missing_methods,omitempty"`
	Locations      []Location             `json:"locations,omitempty"`
}

// ImplementationResult contains implementation lookup matches.
type ImplementationResult struct {
	Target         NavigationTarget      `json:"target,omitempty"`
	Symbol         Symbol                `json:"symbol,omitempty"`
	Matches        []ImplementationMatch `json:"matches,omitempty"`
	Diagnostics    []Diagnostic          `json:"diagnostics,omitempty"`
	ResolutionMode string                `json:"resolution_mode,omitempty"`
	Complete       bool                  `json:"complete,omitempty"`
	Warnings       []string              `json:"warnings,omitempty"`
	Indexed        bool                  `json:"indexed,omitempty"`
	Fresh          bool                  `json:"fresh,omitempty"`
}

// Import describes one import edge from a document or package.
type Import struct {
	Path        string      `json:"path"`
	Name        string      `json:"name,omitempty"`
	SourcePath  string      `json:"source_path,omitempty"`
	PackageID   string      `json:"package_id,omitempty"`
	PackageName string      `json:"package_name,omitempty"`
	Class       ImportClass `json:"class,omitempty"`
	Test        bool        `json:"test,omitempty"`
	Location    Location    `json:"location,omitempty"`
}

// Reference describes a symbol usage site.
type Reference struct {
	SymbolID string   `json:"symbol_id,omitempty"`
	Kind     string   `json:"kind,omitempty"`
	Name     string   `json:"name,omitempty"`
	Location Location `json:"location,omitempty"`
	Preview  string   `json:"preview,omitempty"`
}

// CallScope bounds a call hierarchy lookup.
type CallScope string

const (
	CallScopeFile    CallScope = "file"
	CallScopePackage CallScope = "package"
	CallScopeModule  CallScope = "module"
)

// CallQuery selects a source position for language call hierarchy lookup.
type CallQuery struct {
	Language     LanguageID `json:"language,omitempty" jsonschema:"description=Language id. Defaults to the provider language."`
	Path         string     `json:"path" jsonschema:"description=Workspace-relative Go source file path.,required"`
	Line         int        `json:"line,omitempty" jsonschema:"description=1-indexed source line. Required unless offset is set."`
	Column       int        `json:"column,omitempty" jsonschema:"description=1-indexed byte column. Required unless offset is set."`
	Offset       *int       `json:"offset,omitempty" jsonschema:"description=0-indexed byte offset. Takes precedence over line and column. Offset 0 is valid."`
	Scope        CallScope  `json:"scope,omitempty" jsonschema:"description=Call scan scope. Defaults to package.,enum=file,enum=package,enum=module"`
	IncludeTests *bool      `json:"include_tests,omitempty" jsonschema:"description=Include _test.go files in package/module scans. Defaults to true."`
	MaxResults   int        `json:"max_results,omitempty" jsonschema:"description=Maximum call edges returned."`
	MaxBytes     int        `json:"max_bytes,omitempty" jsonschema:"description=Maximum bytes read from each source file."`
	Refresh      bool       `json:"refresh,omitempty" jsonschema:"description=Reserved for memory-backed language views."`
}

// CallEdge describes a caller/callee relationship.
type CallEdge struct {
	CallerID string   `json:"caller_id,omitempty"`
	CalleeID string   `json:"callee_id,omitempty"`
	Caller   Symbol   `json:"caller,omitempty"`
	Callee   Symbol   `json:"callee,omitempty"`
	Name     string   `json:"name,omitempty"`
	Kind     string   `json:"kind,omitempty"`
	Location Location `json:"location,omitempty"`
	Preview  string   `json:"preview,omitempty"`
}

// CallResult contains caller and callee hierarchy edges.
type CallResult struct {
	Target         NavigationTarget `json:"target,omitempty"`
	Symbol         Symbol           `json:"symbol,omitempty"`
	Callers        []CallEdge       `json:"callers,omitempty"`
	Callees        []CallEdge       `json:"callees,omitempty"`
	Diagnostics    []Diagnostic     `json:"diagnostics,omitempty"`
	ResolutionMode string           `json:"resolution_mode,omitempty"`
	Complete       bool             `json:"complete,omitempty"`
	Warnings       []string         `json:"warnings,omitempty"`
	Indexed        bool             `json:"indexed,omitempty"`
	Fresh          bool             `json:"fresh,omitempty"`
}

// ProjectQuery selects language projects.
type ProjectQuery struct {
	Language   LanguageID `json:"language,omitempty" jsonschema:"description=Language id. Defaults to the provider language."`
	Path       string     `json:"path,omitempty" jsonschema:"description=Workspace-relative path used to scope discovery."`
	Refresh    bool       `json:"refresh,omitempty" jsonschema:"description=Rebuild in-memory language view for this request."`
	MaxResults int        `json:"max_results,omitempty" jsonschema:"description=Maximum results returned."`
}

// PackageQuery selects language packages.
type PackageQuery struct {
	Language   LanguageID `json:"language,omitempty" jsonschema:"description=Language id. Defaults to the provider language."`
	ProjectID  string     `json:"project_id,omitempty" jsonschema:"description=Optional project id."`
	Path       string     `json:"path,omitempty" jsonschema:"description=Workspace-relative module, package, or file path."`
	Refresh    bool       `json:"refresh,omitempty" jsonschema:"description=Rebuild in-memory language view for this request."`
	MaxResults int        `json:"max_results,omitempty" jsonschema:"description=Maximum packages returned."`
}

// OutlineQuery selects a file or package outline.
type OutlineQuery struct {
	Language    LanguageID `json:"language,omitempty" jsonschema:"description=Language id. Defaults to the provider language."`
	Path        string     `json:"path" jsonschema:"description=Workspace-relative Go file or package directory path.,required"`
	PackageID   string     `json:"package_id,omitempty" jsonschema:"description=Optional package id returned by go_packages."`
	IncludeDocs bool       `json:"include_docs,omitempty" jsonschema:"description=Include bounded documentation comments."`
	MaxResults  int        `json:"max_results,omitempty" jsonschema:"description=Maximum symbols returned."`
	MaxBytes    int        `json:"max_bytes,omitempty" jsonschema:"description=Maximum bytes read from each source file."`
	Refresh     bool       `json:"refresh,omitempty" jsonschema:"description=Rebuild in-memory language view for this request."`
}

// SymbolQuery selects declaration symbols.
type SymbolQuery struct {
	Language    LanguageID `json:"language,omitempty" jsonschema:"description=Language id. Defaults to the provider language."`
	Query       string     `json:"query,omitempty" jsonschema:"description=Symbol name or substring to search for."`
	Name        string     `json:"name,omitempty" jsonschema:"description=Exact symbol name filter."`
	Kind        SymbolKind `json:"kind,omitempty" jsonschema:"description=Optional symbol kind filter."`
	Path        string     `json:"path,omitempty" jsonschema:"description=Workspace-relative path scope."`
	PackageID   string     `json:"package_id,omitempty" jsonschema:"description=Optional package id scope."`
	IncludeDocs bool       `json:"include_docs,omitempty" jsonschema:"description=Include bounded documentation comments."`
	MaxResults  int        `json:"max_results,omitempty" jsonschema:"description=Maximum symbols returned."`
	MaxBytes    int        `json:"max_bytes,omitempty" jsonschema:"description=Maximum bytes read from each source file."`
	Refresh     bool       `json:"refresh,omitempty" jsonschema:"description=Rebuild in-memory language view for this request."`
}

// Provider is an execution-neutral language support port.
type Provider interface {
	Spec() ProviderSpec
	Projects(context.Context, ProjectQuery) (ProjectResult, error)
	Packages(context.Context, PackageQuery) (PackageResult, error)
	Outline(context.Context, OutlineQuery) (OutlineResult, error)
	Symbols(context.Context, SymbolQuery) (SymbolResult, error)
	Imports(context.Context, ImportQuery) (ImportResult, error)
	Implementations(context.Context, ImplementationQuery) (ImplementationResult, error)
	Calls(context.Context, CallQuery) (CallResult, error)
}

// ProjectResult is a language project query result.
type ProjectResult struct {
	Projects []any `json:"projects,omitempty"`
	Indexed  bool  `json:"indexed,omitempty"`
	Fresh    bool  `json:"fresh,omitempty"`
}

// PackageResult is a language package query result.
type PackageResult struct {
	Packages []Package `json:"packages,omitempty"`
	Indexed  bool      `json:"indexed,omitempty"`
	Fresh    bool      `json:"fresh,omitempty"`
}

// OutlineResult is a language outline query result.
type OutlineResult struct {
	Outline     Outline      `json:"outline,omitempty"`
	Diagnostics []Diagnostic `json:"diagnostics,omitempty"`
	Indexed     bool         `json:"indexed,omitempty"`
	Fresh       bool         `json:"fresh,omitempty"`
}

// SymbolResult is a symbol query result.
type SymbolResult struct {
	Symbols     []Symbol     `json:"symbols,omitempty"`
	Diagnostics []Diagnostic `json:"diagnostics,omitempty"`
	Indexed     bool         `json:"indexed,omitempty"`
	Fresh       bool         `json:"fresh,omitempty"`
}
