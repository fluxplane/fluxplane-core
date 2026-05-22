package gitlab

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	gitlab "gitlab.com/gitlab-org/api/client-go/v2"
)

var hunkHeaderRE = regexp.MustCompile(`^@@ -([0-9]+)(?:,([0-9]+))? \+([0-9]+)(?:,([0-9]+))? @@(.*)$`)

type inlineCommentTarget struct {
	Valid            bool                   `json:"valid"`
	Reason           string                 `json:"reason,omitempty"`
	ProjectID        string                 `json:"project_id,omitempty"`
	MergeRequestIID  int64                  `json:"merge_request_iid,omitempty"`
	Path             string                 `json:"path,omitempty"`
	Line             int64                  `json:"line,omitempty"`
	LineSide         string                 `json:"line_side,omitempty"`
	LineType         string                 `json:"line_type,omitempty"`
	OldLine          int64                  `json:"old_line,omitempty"`
	NewLine          int64                  `json:"new_line,omitempty"`
	BaseSHA          string                 `json:"base_sha,omitempty"`
	StartSHA         string                 `json:"start_sha,omitempty"`
	HeadSHA          string                 `json:"head_sha,omitempty"`
	PositionType     string                 `json:"position_type,omitempty"`
	SurroundingLines []MergeRequestDiffLine `json:"surrounding_lines,omitempty"`
}

func parseMergeRequestDiffLines(project any, iid int64, diff *gitlab.MergeRequestDiff) []MergeRequestDiffLine {
	if diff == nil {
		return nil
	}
	oldPath := diff.OldPath
	newPath := firstNonEmpty(diff.NewPath, diff.OldPath)
	path := firstNonEmpty(newPath, oldPath)
	var (
		lines      []MergeRequestDiffLine
		oldLine    int64
		newLine    int64
		hunkHeader string
		inHunk     bool
	)
	for _, raw := range strings.Split(diff.Diff, "\n") {
		if match := hunkHeaderRE.FindStringSubmatch(raw); match != nil {
			oldLine, _ = strconv.ParseInt(match[1], 10, 64)
			newLine, _ = strconv.ParseInt(match[3], 10, 64)
			hunkHeader = raw
			inHunk = true
			continue
		}
		if !inHunk || strings.HasPrefix(raw, `\ No newline`) {
			continue
		}
		if raw == "" {
			raw = " "
		}
		prefix := raw[0]
		content := ""
		if len(raw) > 1 {
			content = raw[1:]
		}
		line := MergeRequestDiffLine{
			ProjectID:       projectIDLabel(project),
			MergeRequestIID: iid,
			OldPath:         oldPath,
			NewPath:         newPath,
			Content:         content,
			HunkHeader:      hunkHeader,
		}
		switch prefix {
		case '+':
			line.LineType = "add"
			line.NewLine = newLine
			newLine++
		case '-':
			line.LineType = "del"
			line.OldLine = oldLine
			oldLine++
		default:
			line.LineType = "ctx"
			line.OldLine = oldLine
			line.NewLine = newLine
			oldLine++
			newLine++
		}
		line.ID = diffLineID(line.ProjectID, iid, path, firstNonZero(line.NewLine, line.OldLine), line.LineType)
		lines = append(lines, line)
	}
	return lines
}

func diffLineID(project string, iid int64, path string, line int64, lineType string) string {
	if lineType == "" {
		return fmt.Sprintf("%s!%d!%s!%d", project, iid, path, line)
	}
	return fmt.Sprintf("%s!%d!%s!%d!%s", project, iid, path, line, lineType)
}

func selectDiffLines(lines []MergeRequestDiffLine, query string, targetLine int64, contextCount int) ([]MergeRequestDiffLine, error) {
	query = strings.ToLower(strings.TrimSpace(query))
	if contextCount < 0 {
		contextCount = 0
	}
	if contextCount > 20 {
		contextCount = 20
	}
	if targetLine != 0 {
		for i, line := range lines {
			if line.NewLine == targetLine || line.OldLine == targetLine {
				start := max(0, i-contextCount)
				end := min(len(lines), i+contextCount+1)
				return lines[start:end], nil
			}
		}
		return nil, fmt.Errorf("line %d is not present in the merge request diff; available new-line ranges: %s", targetLine, availableNewLineRanges(lines))
	}
	if query == "" {
		return lines, nil
	}
	out := make([]MergeRequestDiffLine, 0)
	for _, line := range lines {
		if strings.Contains(strings.ToLower(line.Content), query) || strings.Contains(strings.ToLower(line.NewPath), query) || strings.Contains(strings.ToLower(line.OldPath), query) {
			out = append(out, line)
		}
	}
	return out, nil
}

func validateInlineTarget(project any, iid int64, path string, requestedLine int64, lineSide string, diffs []*gitlab.MergeRequestDiff, versions []*gitlab.MergeRequestDiffVersion, contextCount int) (inlineCommentTarget, error) {
	lineSide = strings.ToLower(strings.TrimSpace(lineSide))
	if lineSide == "" {
		lineSide = "new"
	}
	if lineSide != "new" && lineSide != "old" {
		return inlineCommentTarget{}, fmt.Errorf("line_side must be new or old")
	}
	var lines []MergeRequestDiffLine
	for _, diff := range diffs {
		if diff == nil || (diff.NewPath != path && diff.OldPath != path) {
			continue
		}
		lines = append(lines, parseMergeRequestDiffLines(project, iid, diff)...)
	}
	if len(lines) == 0 {
		return inlineCommentTarget{}, fmt.Errorf("file %q is not present in the merge request diff", path)
	}
	selected, err := selectDiffLines(lines, "", requestedLine, contextCount)
	if err != nil {
		return inlineCommentTarget{}, err
	}
	var target MergeRequestDiffLine
	for _, line := range selected {
		if lineSide == "old" && line.OldLine == requestedLine {
			target = line
			break
		}
		if lineSide == "new" && line.NewLine == requestedLine {
			target = line
			break
		}
	}
	if target.ID == "" {
		return inlineCommentTarget{}, fmt.Errorf("line %d is not present on the %s side of the merge request diff; available new-line ranges: %s", requestedLine, lineSide, availableNewLineRanges(lines))
	}
	if lineSide == "new" && target.LineType == "del" {
		return inlineCommentTarget{}, fmt.Errorf("line %d is a deleted line; pass line_side=old to target old-file lines", requestedLine)
	}
	version := latestDiffVersion(versions)
	return inlineCommentTarget{
		Valid:            true,
		ProjectID:        projectIDLabel(project),
		MergeRequestIID:  iid,
		Path:             path,
		Line:             requestedLine,
		LineSide:         lineSide,
		LineType:         target.LineType,
		OldLine:          target.OldLine,
		NewLine:          target.NewLine,
		BaseSHA:          version.BaseCommitSHA,
		StartSHA:         version.StartCommitSHA,
		HeadSHA:          version.HeadCommitSHA,
		PositionType:     "text",
		SurroundingLines: selected,
	}, nil
}

func latestDiffVersion(versions []*gitlab.MergeRequestDiffVersion) *gitlab.MergeRequestDiffVersion {
	if len(versions) == 0 || versions[0] == nil {
		return &gitlab.MergeRequestDiffVersion{}
	}
	return versions[0]
}

func availableNewLineRanges(lines []MergeRequestDiffLine) string {
	var ranges []string
	var start, prev int64
	for _, line := range lines {
		if line.NewLine == 0 {
			continue
		}
		if start == 0 {
			start, prev = line.NewLine, line.NewLine
			continue
		}
		if line.NewLine == prev+1 {
			prev = line.NewLine
			continue
		}
		ranges = append(ranges, lineRange(start, prev))
		start, prev = line.NewLine, line.NewLine
	}
	if start != 0 {
		ranges = append(ranges, lineRange(start, prev))
	}
	return strings.Join(ranges, ", ")
}

func lineRange(start, end int64) string {
	if start == end {
		return strconv.FormatInt(start, 10)
	}
	return fmt.Sprintf("%d-%d", start, end)
}

func firstNonZero(values ...int64) int64 {
	for _, value := range values {
		if value != 0 {
			return value
		}
	}
	return 0
}
