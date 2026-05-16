package markdown

import "testing"

func TestMarkdownModels(t *testing.T) {
	outline := Outline{
		Path: "README.md",
		Headings: []Heading{{
			Level:  1,
			Title:  "Readme",
			Anchor: "readme",
			Children: []Heading{{
				Level: 2,
				Title: "Install",
			}},
		}},
	}
	if outline.Headings[0].Children[0].Title != "Install" {
		t.Fatalf("outline = %#v", outline)
	}
	link := Link{Path: "README.md", Target: "#install", Kind: LinkAnchor}
	if link.Kind != "anchor" || link.Target != "#install" {
		t.Fatalf("link = %#v", link)
	}
}
