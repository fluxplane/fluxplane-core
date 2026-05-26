package language

import "testing"

func TestLanguageIDs(t *testing.T) {
	if LanguageGo != "go" {
		t.Errorf("LanguageGo = %q, want %q", LanguageGo, "go")
	}
	if LanguageMarkdown != "markdown" {
		t.Errorf("LanguageMarkdown = %q, want %q", LanguageMarkdown, "markdown")
	}
}

func TestCapabilityConstants(t *testing.T) {
	caps := []struct {
		got  string
		want string
	}{
		{string(CapabilityProject), "project"},
		{string(CapabilityPackage), "package"},
		{string(CapabilityOutline), "outline"},
		{string(CapabilitySymbol), "symbol"},
		{string(CapabilityDefinition), "definition"},
		{string(CapabilitySymbolInfo), "symbol_info"},
		{string(CapabilityReferences), "references"},
		{string(CapabilityImplementations), "implementations"},
		{string(CapabilityCalls), "calls"},
		{string(CapabilityImports), "imports"},
		{string(CapabilityDiagnostics), "diagnostics"},
		{string(CapabilityFormat), "format"},
		{string(CapabilityRename), "rename"},
	}
	for _, c := range caps {
		if c.got != c.want {
			t.Errorf("Capability %q = %q, want %q", c.want, c.got, c.want)
		}
	}
}

func TestSymbolKindConstants(t *testing.T) {
	kinds := []struct {
		got  string
		want string
	}{
		{string(SymbolModule), "module"},
		{string(SymbolPackage), "package"},
		{string(SymbolType), "type"},
		{string(SymbolStruct), "struct"},
		{string(SymbolInterface), "interface"},
		{string(SymbolFunction), "function"},
		{string(SymbolMethod), "method"},
		{string(SymbolField), "field"},
		{string(SymbolConst), "const"},
		{string(SymbolVar), "var"},
		{string(SymbolImport), "import"},
		{string(SymbolNamespace), "namespace"},
	}
	for _, k := range kinds {
		if k.got != k.want {
			t.Errorf("SymbolKind %q = %q, want %q", k.want, k.got, k.want)
		}
	}
}

func TestImportClassConstants(t *testing.T) {
	classes := []struct {
		got  string
		want string
	}{
		{string(ImportClassStdlib), "stdlib"},
		{string(ImportClassModuleLocal), "module_local"},
		{string(ImportClassExternal), "external"},
		{string(ImportClassUnknown), "unknown"},
	}
	for _, c := range classes {
		if c.got != c.want {
			t.Errorf("ImportClass %q = %q, want %q", c.want, c.got, c.want)
		}
	}
}

func TestToolchainCapabilityConstants(t *testing.T) {
	caps := []struct {
		got  string
		want string
	}{
		{string(ToolchainCapabilityTest), "test"},
		{string(ToolchainCapabilityBuild), "build"},
		{string(ToolchainCapabilityFormat), "format"},
		{string(ToolchainCapabilityLint), "lint"},
		{string(ToolchainCapabilityDoc), "doc"},
		{string(ToolchainCapabilityList), "list"},
		{string(ToolchainCapabilityPackageInfo), "package_info"},
		{string(ToolchainCapabilityInstall), "install"},
	}
	for _, c := range caps {
		if c.got != c.want {
			t.Errorf("ToolchainCapability %q = %q, want %q", c.want, c.got, c.want)
		}
	}
}

func TestToolchainSpecValidate(t *testing.T) {
	tests := []struct {
		name    string
		spec    ToolchainSpec
		wantErr bool
		errMsg  string
	}{
		{
			name:    "empty id",
			spec:    ToolchainSpec{ID: ""},
			wantErr: true,
			errMsg:  "language: toolchain id is empty",
		},
		{
			name:    "whitespace id",
			spec:    ToolchainSpec{ID: "   "},
			wantErr: true,
			errMsg:  "language: toolchain id is empty",
		},
		{
			name:    "valid spec",
			spec:    ToolchainSpec{ID: "go", DisplayName: "Go Toolchain"},
			wantErr: false,
		},
		{
			name: "binary with empty name",
			spec: ToolchainSpec{
				ID:               "go",
				RequiredBinaries: []ToolchainBinarySpec{{Name: ""}},
			},
			wantErr: true,
			errMsg:  `language: toolchain "go" binary[0] name is empty`,
		},
		{
			name: "valid with binaries",
			spec: ToolchainSpec{
				ID:               "go",
				RequiredBinaries: []ToolchainBinarySpec{{Name: "go", MinVersion: "1.21"}},
			},
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.spec.Validate()
			if tt.wantErr {
				if err == nil {
					t.Fatalf("Validate() error = nil, want %q", tt.errMsg)
				}
				if err.Error() != tt.errMsg {
					t.Errorf("Validate() error = %q, want %q", err.Error(), tt.errMsg)
				}
			} else {
				if err != nil {
					t.Fatalf("Validate() error = %v, want nil", err)
				}
			}
		})
	}
}

func TestPositionRange(t *testing.T) {
	p := Position{Line: 10, Column: 5}
	if p.Line != 10 || p.Column != 5 {
		t.Errorf("Position = %#v, want {Line:10, Column:5}", p)
	}
	r := Range{Start: Position{Line: 1, Column: 1}, End: Position{Line: 10, Column: 5}}
	if r.Start.Line != 1 || r.End.Line != 10 {
		t.Errorf("Range = %#v", r)
	}
}

func TestLocation(t *testing.T) {
	loc := Location{Path: "foo.go", Range: Range{Start: Position{Line: 1, Column: 1}}}
	if loc.Path != "foo.go" {
		t.Errorf("Location.Path = %q, want %q", loc.Path, "foo.go")
	}
}

func TestPackage(t *testing.T) {
	pkg := Package{
		ID:         "pkg-1",
		Language:   LanguageGo,
		Name:       "mypackage",
		ImportPath: "github.com/example/mypackage",
		Dir:        "/src/mypackage",
		Files:      []string{"foo.go", "bar.go"},
	}
	if pkg.ID != "pkg-1" || pkg.Language != LanguageGo {
		t.Errorf("Package = %#v", pkg)
	}
}

func TestDocument(t *testing.T) {
	doc := Document{Path: "foo.go", Language: LanguageGo, PackageID: "pkg-1"}
	if doc.Path != "foo.go" || doc.Language != LanguageGo {
		t.Errorf("Document = %#v", doc)
	}
}

func TestOutline(t *testing.T) {
	outline := Outline{
		Path:      "foo.go",
		PackageID: "pkg-1",
		Language:  LanguageGo,
		Symbols:   []Symbol{{Kind: SymbolFunction, Name: "main"}},
		Truncated: false,
	}
	if len(outline.Symbols) != 1 || outline.Symbols[0].Name != "main" {
		t.Errorf("Outline = %#v", outline)
	}
}

func TestDiagnostic(t *testing.T) {
	diag := Diagnostic{Path: "foo.go", Severity: "error", Code: "E001", Message: "syntax error", Line: 10}
	if diag.Severity != "error" || diag.Message != "syntax error" {
		t.Errorf("Diagnostic = %#v", diag)
	}
}

func TestSymbol(t *testing.T) {
	sym := Symbol{
		ID:        "sym-1",
		Language:  LanguageGo,
		Kind:      SymbolFunction,
		Name:      "main",
		Container: "main",
		Location:  Location{Path: "foo.go"},
	}
	if sym.Name != "main" || sym.Kind != SymbolFunction {
		t.Errorf("Symbol = %#v", sym)
	}
}

func TestSymbolWithChildren(t *testing.T) {
	sym := Symbol{
		ID:       "type-1",
		Kind:     SymbolStruct,
		Name:     "MyStruct",
		Children: []Symbol{{ID: "field-1", Kind: SymbolField, Name: "Field1"}},
	}
	if len(sym.Children) != 1 {
		t.Errorf("Symbol.Children length = %d, want 1", len(sym.Children))
	}
}

func TestImport(t *testing.T) {
	imp := Import{
		Path:       "context",
		Name:       "context",
		SourcePath: "foo.go",
		Class:      ImportClassStdlib,
		Test:       false,
	}
	if imp.Path != "context" || imp.Class != ImportClassStdlib {
		t.Errorf("Import = %#v", imp)
	}
}

func TestReference(t *testing.T) {
	ref := Reference{
		SymbolID: "sym-1",
		Kind:     "definition",
		Name:     "main",
		Location: Location{Path: "foo.go"},
		Preview:  "func main()",
	}
	if ref.SymbolID != "sym-1" {
		t.Errorf("Reference = %#v", ref)
	}
}

func TestToolchainBinarySpec(t *testing.T) {
	binary := ToolchainBinarySpec{
		Name:        "go",
		MinVersion:  "1.21",
		VersionArgs: []string{"version"},
	}
	if binary.Name != "go" || binary.MinVersion != "1.21" {
		t.Errorf("ToolchainBinarySpec = %#v", binary)
	}
}

func TestToolchainStatus(t *testing.T) {
	status := ToolchainStatus{
		ID:        "go",
		Available: true,
		Binaries: []ToolchainBinaryStatus{
			{Name: "go", Available: true, Path: "/usr/bin/go", Version: "1.21.0"},
		},
		Version: "1.21.0",
	}
	if !status.Available || len(status.Binaries) != 1 {
		t.Errorf("ToolchainStatus = %#v", status)
	}
}

func TestToolchainStatusWithDiagnostics(t *testing.T) {
	status := ToolchainStatus{
		ID:          "go",
		Available:   false,
		Binaries:    []ToolchainBinaryStatus{{Name: "go", Available: false, Error: "not found"}},
		Diagnostics: []Diagnostic{{Message: "toolchain unavailable"}},
	}
	if status.Available || len(status.Diagnostics) != 1 {
		t.Errorf("ToolchainStatus = %#v", status)
	}
}

func TestProviderSpecValidate(t *testing.T) {
	tests := []struct {
		name    string
		spec    ProviderSpec
		wantErr string
	}{
		{name: "empty name", spec: ProviderSpec{Language: LanguageGo}, wantErr: "language: provider name is empty"},
		{name: "empty language", spec: ProviderSpec{Name: "gopls"}, wantErr: "language: provider language is empty"},
		{name: "valid", spec: ProviderSpec{Name: "gopls", Language: LanguageGo}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.spec.Validate()
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("Validate() error = %v, want nil", err)
				}
				return
			}
			if err == nil || err.Error() != tt.wantErr {
				t.Fatalf("Validate() error = %v, want %q", err, tt.wantErr)
			}
		})
	}
}

func TestToolchainSpecValidateWhitespaceBinaryName(t *testing.T) {
	err := (ToolchainSpec{
		ID:               "go",
		RequiredBinaries: []ToolchainBinarySpec{{Name: "go"}, {Name: "  "}},
	}).Validate()
	if err == nil || err.Error() != `language: toolchain "go" binary[1] name is empty` {
		t.Fatalf("Validate() error = %v, want binary[1] name error", err)
	}
}
