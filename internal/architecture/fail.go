package architecture

import "strings"

// HasFailures reports whether the selected architecture gates have unallowed
// diagnostics in report.
func HasFailures(report Report, failOn string) bool {
	categories := parseFailCategories(failOn)
	for _, diagnostic := range report.Diagnostics {
		if diagnostic.Allowed {
			continue
		}
		for _, category := range categories {
			if diagnosticMatchesCategory(diagnostic, category) {
				return true
			}
		}
	}
	return false
}

func parseFailCategories(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	var out []string
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if part == "all" {
			return []string{"boundary", "test-boundary", "side-effects", "unknown"}
		}
		out = append(out, part)
	}
	return out
}

func diagnosticMatchesCategory(diagnostic Diagnostic, category string) bool {
	switch category {
	case "boundary":
		return diagnostic.Kind == DiagnosticBoundaryViolation
	case "test-boundary":
		return diagnostic.Kind == DiagnosticTestBoundaryViolation
	case "side-effects":
		switch diagnostic.Kind {
		case DiagnosticInnerHostIO, DiagnosticRuntimeHostIO, DiagnosticPluginHostEffect:
			return diagnostic.Severity == SeverityError
		default:
			return false
		}
	case "unknown":
		return diagnostic.Kind == DiagnosticUnknownPackage
	default:
		return false
	}
}
