package gitlab

import (
	"strings"
	"testing"

	gitlab "gitlab.com/gitlab-org/api/client-go/v2"
)

func TestParseMergeRequestDiffLinesClassifiesHunks(t *testing.T) {
	diff := &gitlab.MergeRequestDiff{
		OldPath: "runtime.go",
		NewPath: "runtime.go",
		Diff: strings.Join([]string{
			"@@ -10,3 +10,4 @@ func run() {",
			" keep",
			"-old",
			"+new",
			"+",
			" tail",
		}, "\n"),
	}
	lines := parseMergeRequestDiffLines("group/project", 7, diff)
	if len(lines) != 5 {
		t.Fatalf("lines = %#v, want 5 parsed lines", lines)
	}
	checks := []struct {
		index   int
		typ     string
		oldLine int64
		newLine int64
		content string
	}{
		{0, "ctx", 10, 10, "keep"},
		{1, "del", 11, 0, "old"},
		{2, "add", 0, 11, "new"},
		{3, "add", 0, 12, ""},
		{4, "ctx", 12, 13, "tail"},
	}
	for _, check := range checks {
		line := lines[check.index]
		if line.LineType != check.typ || line.OldLine != check.oldLine || line.NewLine != check.newLine || line.Content != check.content {
			t.Fatalf("line[%d] = %#v", check.index, line)
		}
	}
}

func TestValidateInlineTargetRejectsMissingLineWithRanges(t *testing.T) {
	diffs := []*gitlab.MergeRequestDiff{{
		OldPath: "runtime.go",
		NewPath: "runtime.go",
		Diff:    "@@ -1,2 +1,2 @@\n keep\n+new",
	}}
	_, err := validateInlineTarget("12", 7, "runtime.go", 99, "new", diffs, []*gitlab.MergeRequestDiffVersion{{
		BaseCommitSHA:  "base",
		StartCommitSHA: "start",
		HeadCommitSHA:  "head",
	}}, 3)
	if err == nil || !strings.Contains(err.Error(), "available new-line ranges") {
		t.Fatalf("err = %v, want available range error", err)
	}
}
