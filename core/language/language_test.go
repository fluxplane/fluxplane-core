package language

import "testing"

func TestProviderSpecValidate(t *testing.T) {
	if err := (ProviderSpec{}).Validate(); err == nil {
		t.Fatal("Validate: want error for empty provider")
	}
	if err := (ProviderSpec{Name: "go", Language: LanguageGo}).Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestSharedSymbolKinds(t *testing.T) {
	s := Symbol{Kind: SymbolFunction, Name: "Run", Language: LanguageGo}
	if s.Kind != "function" || s.Language != "go" {
		t.Fatalf("symbol = %#v", s)
	}
}

func TestMarkdownModels(t *testing.T) {
	outline := MarkdownOutline{
		Path: "README.md",
		Headings: []MarkdownHeading{{
			Level:  1,
			Title:  "Readme",
			Anchor: "readme",
			Children: []MarkdownHeading{{
				Level: 2,
				Title: "Install",
			}},
		}},
	}
	if outline.Headings[0].Children[0].Title != "Install" {
		t.Fatalf("outline = %#v", outline)
	}
	link := MarkdownLink{Path: "README.md", Target: "#install", Kind: MarkdownLinkAnchor}
	if link.Kind != "anchor" || link.Target != "#install" {
		t.Fatalf("link = %#v", link)
	}
}
