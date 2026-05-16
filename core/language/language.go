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

// GoInfoQuery selects curated Go toolchain orientation details.
type GoInfoQuery struct {
	Language       LanguageID `json:"language,omitempty" jsonschema:"description=Language id. Defaults to go."`
	Path           string     `json:"path,omitempty" jsonschema:"description=Workspace-relative working directory for go commands."`
	IncludePrivate *bool      `json:"include_private,omitempty" jsonschema:"description=Include parsed GOPRIVATE/GONOPROXY/GONOSUMDB and proxy/sumdb settings. Defaults to true."`
	IncludePaths   *bool      `json:"include_paths,omitempty" jsonschema:"description=Include GOROOT/GOPATH/GOMOD/GOWORK/cache/tool directories. Defaults to true."`
	IncludeRawEnv  bool       `json:"include_raw_env,omitempty" jsonschema:"description=Include selected raw go env values in the response."`
	MaxBytes       int        `json:"max_bytes,omitempty" jsonschema:"description=Maximum stdout/stderr bytes captured per go command."`
}

// GoEnvQuery selects read-only go env values.
type GoEnvQuery struct {
	Language LanguageID `json:"language,omitempty" jsonschema:"description=Language id. Defaults to go."`
	Path     string     `json:"path,omitempty" jsonschema:"description=Workspace-relative working directory for go env."`
	Vars     []string   `json:"vars,omitempty" jsonschema:"description=Specific go env variable names. Defaults to the curated go_info set."`
	All      bool       `json:"all,omitempty" jsonschema:"description=Return all go env -json values."`
	Changed  bool       `json:"changed,omitempty" jsonschema:"description=Return only values changed from defaults, equivalent to go env -changed -json."`
	Redact   *bool      `json:"redact,omitempty" jsonschema:"description=Redact sensitive-looking values. Defaults to true."`
	MaxBytes int        `json:"max_bytes,omitempty" jsonschema:"description=Maximum stdout/stderr bytes captured."`
}

// GoVersionQuery selects Go toolchain or binary build version details.
type GoVersionQuery struct {
	Language   LanguageID `json:"language,omitempty" jsonschema:"description=Language id. Defaults to go."`
	Path       string     `json:"path,omitempty" jsonschema:"description=Workspace-relative working directory for go version."`
	Files      []string   `json:"files,omitempty" jsonschema:"description=Optional workspace-relative binary files to inspect."`
	ModuleInfo bool       `json:"module_info,omitempty" jsonschema:"description=Include embedded module build info, equivalent to go version -m."`
	JSON       bool       `json:"json,omitempty" jsonschema:"description=Request JSON build info when module_info is true."`
	Verbose    bool       `json:"verbose,omitempty" jsonschema:"description=Report unrecognized files when inspecting explicit files or directories."`
	MaxResults int        `json:"max_results,omitempty" jsonschema:"description=Maximum inspected file records returned."`
	MaxBytes   int        `json:"max_bytes,omitempty" jsonschema:"description=Maximum stdout/stderr bytes captured."`
}

// GoDocQuery selects package or symbol documentation from go doc.
type GoDocQuery struct {
	Language          LanguageID `json:"language,omitempty" jsonschema:"description=Language id. Defaults to go."`
	Path              string     `json:"path,omitempty" jsonschema:"description=Workspace-relative Go file or directory used as the go doc working directory."`
	Line              int        `json:"line,omitempty" jsonschema:"description=1-indexed source line for position-based symbol docs."`
	Column            int        `json:"column,omitempty" jsonschema:"description=1-indexed byte column for position-based symbol docs."`
	Offset            *int       `json:"offset,omitempty" jsonschema:"description=0-indexed byte offset. Takes precedence over line and column."`
	Package           string     `json:"package,omitempty" jsonschema:"description=Optional package import path or suffix."`
	Symbol            string     `json:"symbol,omitempty" jsonschema:"description=Optional symbol, method, or field selector."`
	All               bool       `json:"all,omitempty" jsonschema:"description=Show all package documentation, equivalent to go doc -all."`
	Short             bool       `json:"short,omitempty" jsonschema:"description=Show one-line symbol summaries, equivalent to go doc -short."`
	Source            bool       `json:"source,omitempty" jsonschema:"description=Show source for the selected symbol, equivalent to go doc -src."`
	IncludeUnexported bool       `json:"include_unexported,omitempty" jsonschema:"description=Include unexported docs, equivalent to go doc -u."`
	IncludeCmd        bool       `json:"include_cmd,omitempty" jsonschema:"description=Treat package main like a regular package, equivalent to go doc -cmd."`
	MaxBytes          int        `json:"max_bytes,omitempty" jsonschema:"description=Maximum stdout/stderr bytes captured."`
}

// GoListQuery selects package or module metadata from go list.
type GoListQuery struct {
	Language      LanguageID `json:"language,omitempty" jsonschema:"description=Language id. Defaults to go."`
	Path          string     `json:"path,omitempty" jsonschema:"description=Workspace-relative working directory for go list."`
	Patterns      []string   `json:"patterns,omitempty" jsonschema:"description=Package or module patterns. Defaults to [\".\"]."`
	Modules       bool       `json:"modules,omitempty" jsonschema:"description=List modules instead of packages, equivalent to go list -m."`
	Deps          bool       `json:"deps,omitempty" jsonschema:"description=Include dependencies, equivalent to go list -deps."`
	Test          bool       `json:"test,omitempty" jsonschema:"description=Include test packages, equivalent to go list -test."`
	Compiled      bool       `json:"compiled,omitempty" jsonschema:"description=Include compiled go files, equivalent to go list -compiled."`
	Find          bool       `json:"find,omitempty" jsonschema:"description=Identify packages without resolving dependencies, equivalent to go list -find."`
	IncludeErrors bool       `json:"include_errors,omitempty" jsonschema:"description=Return erroneous packages in structured output, equivalent to go list -e."`
	MaxResults    int        `json:"max_results,omitempty" jsonschema:"description=Maximum package/module records returned."`
	MaxBytes      int        `json:"max_bytes,omitempty" jsonschema:"description=Maximum stdout/stderr bytes captured."`
}

// GoProxyConfig is a parsed GOPROXY-style value.
type GoProxyConfig struct {
	Raw    string         `json:"raw,omitempty"`
	Groups []GoProxyGroup `json:"groups,omitempty"`
}

// GoProxyGroup contains pipe-separated proxies within one comma fallback group.
type GoProxyGroup struct {
	Entries []string `json:"entries,omitempty"`
}

// GoInfoResult contains curated Go toolchain orientation.
type GoInfoResult struct {
	Version     map[string]string   `json:"version,omitempty"`
	Target      map[string]string   `json:"target,omitempty"`
	Workspace   map[string]string   `json:"workspace,omitempty"`
	Paths       map[string]string   `json:"paths,omitempty"`
	Modules     map[string]string   `json:"modules,omitempty"`
	Network     map[string]any      `json:"network,omitempty"`
	Private     map[string][]string `json:"private,omitempty"`
	RawEnv      map[string]string   `json:"raw_env,omitempty"`
	Diagnostics []Diagnostic        `json:"diagnostics,omitempty"`
}

// GoEnvResult contains read-only go env values.
type GoEnvResult struct {
	Values      map[string]string `json:"values,omitempty"`
	All         bool              `json:"all,omitempty"`
	Changed     bool              `json:"changed,omitempty"`
	Diagnostics []Diagnostic      `json:"diagnostics,omitempty"`
}

// GoVersionRecord contains one go version record.
type GoVersionRecord struct {
	Path      string         `json:"path,omitempty"`
	Version   string         `json:"version,omitempty"`
	Raw       string         `json:"raw,omitempty"`
	BuildInfo map[string]any `json:"build_info,omitempty"`
}

// GoVersionResult contains Go toolchain or binary build version details.
type GoVersionResult struct {
	Version     string            `json:"version,omitempty"`
	Records     []GoVersionRecord `json:"records,omitempty"`
	Diagnostics []Diagnostic      `json:"diagnostics,omitempty"`
}

// GoDocResult contains go doc output and selection metadata.
type GoDocResult struct {
	Text        string       `json:"text,omitempty"`
	Package     string       `json:"package,omitempty"`
	Symbol      string       `json:"symbol,omitempty"`
	Workdir     string       `json:"workdir,omitempty"`
	Diagnostics []Diagnostic `json:"diagnostics,omitempty"`
}

// GoListResult contains structured go list package or module records.
type GoListResult struct {
	Records     []map[string]any `json:"records,omitempty"`
	Modules     bool             `json:"modules,omitempty"`
	Diagnostics []Diagnostic     `json:"diagnostics,omitempty"`
	Complete    bool             `json:"complete,omitempty"`
}

// GoTestQuery selects structured go test execution.
type GoTestQuery struct {
	Language       LanguageID `json:"language,omitempty" jsonschema:"description=Language id. Defaults to go."`
	Path           string     `json:"path,omitempty" jsonschema:"description=Workspace-relative working directory for go test."`
	Patterns       []string   `json:"patterns,omitempty" jsonschema:"description=Package patterns. Defaults to [\".\"]."`
	Run            string     `json:"run,omitempty" jsonschema:"description=Run only tests matching this regular expression."`
	Skip           string     `json:"skip,omitempty" jsonschema:"description=Skip tests matching this regular expression."`
	Short          bool       `json:"short,omitempty" jsonschema:"description=Tell long-running tests to shorten execution."`
	Failfast       bool       `json:"failfast,omitempty" jsonschema:"description=Stop after the first package test failure."`
	Count          *int       `json:"count,omitempty" jsonschema:"description=Run each test and benchmark n times."`
	Timeout        string     `json:"timeout,omitempty" jsonschema:"description=Go duration timeout such as 30s or 2m."`
	Vet            string     `json:"vet,omitempty" jsonschema:"description=Vet mode: default, off, or all."`
	Race           bool       `json:"race,omitempty" jsonschema:"description=Enable the race detector."`
	Cover          bool       `json:"cover,omitempty" jsonschema:"description=Enable coverage analysis."`
	MaxOutputBytes int        `json:"max_output_bytes,omitempty" jsonschema:"description=Maximum stdout/stderr bytes captured."`
}

// GoTestPackageResult summarizes go test events for one package.
type GoTestPackageResult struct {
	Package string   `json:"package,omitempty"`
	Status  string   `json:"status,omitempty"`
	Elapsed float64  `json:"elapsed,omitempty"`
	Passed  int      `json:"passed,omitempty"`
	Failed  int      `json:"failed,omitempty"`
	Skipped int      `json:"skipped,omitempty"`
	Output  []string `json:"output,omitempty"`
}

// GoTestResult contains structured go test output.
type GoTestResult struct {
	Packages    []GoTestPackageResult `json:"packages,omitempty"`
	Events      []map[string]any      `json:"events,omitempty"`
	Diagnostics []Diagnostic          `json:"diagnostics,omitempty"`
	Passed      bool                  `json:"passed,omitempty"`
	Complete    bool                  `json:"complete,omitempty"`
}

// GoVetQuery selects go vet diagnostics.
type GoVetQuery struct {
	Language       LanguageID `json:"language,omitempty" jsonschema:"description=Language id. Defaults to go."`
	Path           string     `json:"path,omitempty" jsonschema:"description=Workspace-relative working directory for go vet."`
	Patterns       []string   `json:"patterns,omitempty" jsonschema:"description=Package patterns. Defaults to [\".\"]."`
	Tags           []string   `json:"tags,omitempty" jsonschema:"description=Optional build tags."`
	JSON           bool       `json:"json,omitempty" jsonschema:"description=Request go vet JSON diagnostics."`
	Diff           bool       `json:"diff,omitempty" jsonschema:"description=Unsupported in this milestone."`
	Fix            bool       `json:"fix,omitempty" jsonschema:"description=Unsupported in this milestone."`
	Vettool        string     `json:"vettool,omitempty" jsonschema:"description=Unsupported in this milestone."`
	MaxOutputBytes int        `json:"max_output_bytes,omitempty" jsonschema:"description=Maximum stdout/stderr bytes captured."`
}

// GoVetResult contains go vet diagnostics.
type GoVetResult struct {
	Diagnostics []Diagnostic `json:"diagnostics,omitempty"`
	Output      string       `json:"output,omitempty"`
	Passed      bool         `json:"passed,omitempty"`
}

// GoBuildQuery selects go build compile checks.
type GoBuildQuery struct {
	Language       LanguageID `json:"language,omitempty" jsonschema:"description=Language id. Defaults to go."`
	Path           string     `json:"path,omitempty" jsonschema:"description=Workspace-relative working directory for go build."`
	Patterns       []string   `json:"patterns,omitempty" jsonschema:"description=Package patterns. Defaults to [\".\"]."`
	Tags           []string   `json:"tags,omitempty" jsonschema:"description=Optional build tags."`
	Race           bool       `json:"race,omitempty" jsonschema:"description=Enable the race detector."`
	Cover          bool       `json:"cover,omitempty" jsonschema:"description=Enable coverage analysis."`
	Trimpath       bool       `json:"trimpath,omitempty" jsonschema:"description=Remove filesystem paths from resulting executable metadata."`
	Mod            string     `json:"mod,omitempty" jsonschema:"description=Module download mode: readonly, vendor, or mod."`
	Output         string     `json:"output,omitempty" jsonschema:"description=Unsupported in v1."`
	MaxOutputBytes int        `json:"max_output_bytes,omitempty" jsonschema:"description=Maximum stdout/stderr bytes captured."`
}

// GoBuildResult contains go build compile-check output.
type GoBuildResult struct {
	Diagnostics []Diagnostic `json:"diagnostics,omitempty"`
	Output      string       `json:"output,omitempty"`
	Passed      bool         `json:"passed,omitempty"`
}

// GoFmtQuery selects explicit go fmt formatting.
type GoFmtQuery struct {
	Language       LanguageID `json:"language,omitempty" jsonschema:"description=Language id. Defaults to go."`
	Path           string     `json:"path,omitempty" jsonschema:"description=Workspace-relative working directory for go fmt."`
	Patterns       []string   `json:"patterns,omitempty" jsonschema:"description=Package patterns. Defaults to [\".\"]."`
	DryRun         *bool      `json:"dry_run,omitempty" jsonschema:"description=Preview formatting with go fmt -n. Defaults to true."`
	Trace          bool       `json:"trace,omitempty" jsonschema:"description=Print commands as they are executed, equivalent to -x."`
	Mod            string     `json:"mod,omitempty" jsonschema:"description=Module download mode: readonly, vendor, or mod."`
	MaxOutputBytes int        `json:"max_output_bytes,omitempty" jsonschema:"description=Maximum stdout/stderr bytes captured."`
}

// GoFmtResult contains go fmt output.
type GoFmtResult struct {
	Files      []string `json:"files,omitempty"`
	Output     string   `json:"output,omitempty"`
	DryRun     bool     `json:"dry_run,omitempty"`
	WouldWrite bool     `json:"would_write,omitempty"`
	Changed    bool     `json:"changed,omitempty"`
}

// GoInstallQuery selects explicit go install execution.
type GoInstallQuery struct {
	Language       LanguageID        `json:"language,omitempty" jsonschema:"description=Language id. Defaults to go."`
	Path           string            `json:"path,omitempty" jsonschema:"description=Workspace-relative working directory for go install."`
	Packages       []string          `json:"packages" jsonschema:"description=Package paths or patterns to install.,required"`
	Version        string            `json:"version,omitempty" jsonschema:"description=Optional shared version suffix such as latest or v1.2.3."`
	DryRun         *bool             `json:"dry_run,omitempty" jsonschema:"description=Preview install with go install -n. Defaults to true."`
	Trace          bool              `json:"trace,omitempty" jsonschema:"description=Print commands as they are executed, equivalent to -x."`
	Tags           []string          `json:"tags,omitempty" jsonschema:"description=Optional build tags."`
	Race           bool              `json:"race,omitempty" jsonschema:"description=Enable the race detector."`
	Trimpath       bool              `json:"trimpath,omitempty" jsonschema:"description=Remove filesystem paths from resulting executable metadata."`
	Mod            string            `json:"mod,omitempty" jsonschema:"description=Module download mode when version is empty: readonly, vendor, or mod."`
	Env            map[string]string `json:"env,omitempty" jsonschema:"description=Restricted environment overrides. Allowed keys: GOBIN, GOPATH, GOOS, GOARCH, CGO_ENABLED."`
	MaxOutputBytes int               `json:"max_output_bytes,omitempty" jsonschema:"description=Maximum stdout/stderr bytes captured."`
}

// GoInstallResult contains go install output.
type GoInstallResult struct {
	Packages  []string `json:"packages,omitempty"`
	Output    string   `json:"output,omitempty"`
	DryRun    bool     `json:"dry_run,omitempty"`
	Installed bool     `json:"installed,omitempty"`
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
