package pathpattern

import "testing"

func TestPatternMatch(t *testing.T) {
	tests := []struct {
		name    string
		pattern string
		rel     string
		want    bool
	}{
		{name: "brace alternation matches", pattern: ".agents/{designs,plans,reviews}/**/*", rel: ".agents/plans/coder.md", want: true},
		{name: "brace alternation other branch", pattern: ".agents/{designs,plans,reviews}/**/*", rel: ".agents/reviews/2026/review.md", want: true},
		{name: "brace alternation rejects missing branch", pattern: ".agents/{designs,plans,reviews}/**/*", rel: ".agents/notes/review.md", want: false},
		{name: "recursive globstar matches nested", pattern: "docs/**/*.md", rel: "docs/architecture/runtime.md", want: true},
		{name: "recursive globstar matches zero segments", pattern: "docs/**/*.md", rel: "docs/readme.md", want: true},
		{name: "root globstar matches root file", pattern: "**/*.md", rel: "readme.md", want: true},
		{name: "root globstar matches nested file", pattern: "**/*.md", rel: "docs/readme.md", want: true},
		{name: "single star does not cross slash", pattern: "*.md", rel: "docs/readme.md", want: false},
		{name: "question and class", pattern: "docs/file-?.[mt][dx]", rel: "docs/file-a.md", want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			compiled, err := Compile(tt.pattern)
			if err != nil {
				t.Fatalf("Compile() error = %v", err)
			}
			if got := compiled.Match(tt.rel); got != tt.want {
				t.Fatalf("Match() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestCompileRejectsMalformedBracePatterns(t *testing.T) {
	tests := []string{
		".agents/{designs,plans/**/*.md",
		".agents/designs,plans}/**/*.md",
		".agents/{designs,,plans}/**/*.md",
		".agents/{designs,{plans}}/**/*.md",
		".agents/{designs,plans}/{reviews}/**/*.md",
	}
	for _, pattern := range tests {
		t.Run(pattern, func(t *testing.T) {
			if _, err := Compile(pattern); err == nil {
				t.Fatalf("Compile() error = nil, want error")
			}
		})
	}
}
