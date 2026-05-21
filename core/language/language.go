// Package language defines inert language-support models shared by plugins.
package language

import (
	"fmt"
	"strings"

	"github.com/fluxplane/engine/core/operation"
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

// ImportClass is a best-effort import target classification.
type ImportClass string

const (
	ImportClassStdlib      ImportClass = "stdlib"
	ImportClassModuleLocal ImportClass = "module_local"
	ImportClassExternal    ImportClass = "external"
	ImportClassUnknown     ImportClass = "unknown"
)

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

// ToolchainCapability classifies a toolchain capability surface.
type ToolchainCapability string

const (
	ToolchainCapabilityTest        ToolchainCapability = "test"
	ToolchainCapabilityBuild       ToolchainCapability = "build"
	ToolchainCapabilityFormat      ToolchainCapability = "format"
	ToolchainCapabilityLint        ToolchainCapability = "lint"
	ToolchainCapabilityDoc         ToolchainCapability = "doc"
	ToolchainCapabilityList        ToolchainCapability = "list"
	ToolchainCapabilityPackageInfo ToolchainCapability = "package_info"
	ToolchainCapabilityInstall     ToolchainCapability = "install"
)

// ToolchainBinarySpec describes one required executable without probing it.
type ToolchainBinarySpec struct {
	Name        string   `json:"name"`
	MinVersion  string   `json:"min_version,omitempty"`
	VersionArgs []string `json:"version_args,omitempty"`
}

// ToolchainSpec describes an inert language/toolchain capability surface.
type ToolchainSpec struct {
	ID               string                `json:"id"`
	DisplayName      string                `json:"display_name,omitempty"`
	Languages        []LanguageID          `json:"languages,omitempty"`
	RequiredBinaries []ToolchainBinarySpec `json:"required_binaries,omitempty"`
	Capabilities     []ToolchainCapability `json:"capabilities,omitempty"`
	OperationSets    []string              `json:"operation_sets,omitempty"`
	Operations       []operation.Ref       `json:"operations,omitempty"`
	ActivationHints  []string              `json:"activation_hints,omitempty"`
	Annotations      map[string]string     `json:"annotations,omitempty"`
}

// Validate checks the toolchain spec has a stable identity and binary names.
func (s ToolchainSpec) Validate() error {
	if strings.TrimSpace(s.ID) == "" {
		return fmt.Errorf("language: toolchain id is empty")
	}
	for i, binary := range s.RequiredBinaries {
		if strings.TrimSpace(binary.Name) == "" {
			return fmt.Errorf("language: toolchain %q binary[%d] name is empty", s.ID, i)
		}
	}
	return nil
}

// ToolchainBinaryStatus records observed availability for one binary.
type ToolchainBinaryStatus struct {
	Name      string `json:"name"`
	Available bool   `json:"available"`
	Path      string `json:"path,omitempty"`
	Version   string `json:"version,omitempty"`
	Error     string `json:"error,omitempty"`
}

// ToolchainStatus records runtime availability for a toolchain.
type ToolchainStatus struct {
	ID          string                  `json:"id"`
	Available   bool                    `json:"available"`
	Binaries    []ToolchainBinaryStatus `json:"binaries,omitempty"`
	Version     string                  `json:"version,omitempty"`
	Versions    map[string]string       `json:"versions,omitempty"`
	Diagnostics []Diagnostic            `json:"diagnostics,omitempty"`
}
