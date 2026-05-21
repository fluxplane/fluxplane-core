package architecture

import (
	"bytes"
	"fmt"
	"sort"
	"strings"
)

func RenderText(report Report) string {
	var out bytes.Buffer
	fmt.Fprintf(&out, "Architecture score: %d/100\n", report.Summary.Score)
	fmt.Fprintf(&out, "Component scores: boundary=%d coupling=%d side_effect=%d coverage=%d",
		report.Scores.Boundary,
		report.Scores.Coupling,
		report.Scores.SideEffect,
		report.Scores.Coverage,
	)
	if report.Scores.TestBoundary > 0 {
		fmt.Fprintf(&out, " test_boundary=%d", report.Scores.TestBoundary)
	}
	fmt.Fprintln(&out)
	fmt.Fprintf(&out, "Packages: %d  Edges: %d  Violations: %d\n",
		report.Summary.PackageCount,
		report.Summary.InternalEdgeCount,
		report.Summary.ViolationCount,
	)
	fmt.Fprintf(&out, "Cross-layer: %d  Same-layer: %d  Runtime sibling: %d\n",
		report.Summary.CrossLayerEdges,
		report.Summary.SameLayerEdges,
		report.Summary.RuntimeSiblingEdges,
	)
	fmt.Fprintf(&out, "Max fan-in: %d  Max fan-out: %d\n\n", report.Summary.MaxFanIn, report.Summary.MaxFanOut)

	fmt.Fprintln(&out, "Layers:")
	for _, layer := range report.Layers {
		fmt.Fprintf(&out, "  %-13s packages=%-2d fan-in=%-2d fan-out=%-2d\n", layer.Layer, layer.Packages, layer.FanIn, layer.FanOut)
	}

	if len(report.Violations) > 0 {
		fmt.Fprintln(&out, "\nViolations:")
		for _, violation := range report.Violations {
			fmt.Fprintf(&out, "  %s -> %s (%s)\n", short(report.ModulePath, violation.From), short(report.ModulePath, violation.To), violation.Reason)
		}
	}

	if len(report.Summary.ScorePenalties) > 0 {
		fmt.Fprintln(&out, "\nReview penalties:")
		for _, penalty := range report.Summary.ScorePenalties {
			fmt.Fprintf(&out, "  -%-3d %-18s %s\n", penalty.Penalty, penalty.Kind, penaltyLabel(report.ModulePath, penalty))
		}
	}

	if len(report.Diagnostics) > 0 {
		fmt.Fprintln(&out, "\nDiagnostics:")
		for _, diagnostic := range report.Diagnostics {
			fmt.Fprintf(&out, "  %-24s %-7s %s\n", diagnostic.Kind, diagnostic.Severity, diagnosticLabel(report.ModulePath, diagnostic))
		}
	}

	fmt.Fprintln(&out, "\nHighest fan-out:")
	for _, pkg := range topFanOut(report.Packages, 8) {
		fmt.Fprintf(&out, "  %-55s %d\n", short(report.ModulePath, pkg.ImportPath), pkg.FanOut)
	}

	fmt.Fprintln(&out, "\nHighest fan-in:")
	for _, pkg := range topFanIn(report.Packages, 8) {
		fmt.Fprintf(&out, "  %-55s %d\n", short(report.ModulePath, pkg.ImportPath), pkg.FanIn)
	}

	return out.String()
}

func RenderDOT(report Report) string {
	var out bytes.Buffer
	fmt.Fprintln(&out, "digraph architecture {")
	fmt.Fprintln(&out, `  rankdir="LR";`)
	fmt.Fprintln(&out, `  node [shape=box, style="rounded"];`)
	for _, pkg := range report.Packages {
		fmt.Fprintf(&out, "  %q [label=%q, group=%q];\n", pkg.ImportPath, short(report.ModulePath, pkg.ImportPath), pkg.Layer)
	}
	for _, edge := range report.Edges {
		color := "gray55"
		if !edge.Allowed {
			color = "red"
		}
		style := "solid"
		if edge.TestOnly {
			style = "dashed"
		}
		fmt.Fprintf(&out, "  %q -> %q [color=%q, style=%q];\n", edge.From, edge.To, color, style)
	}
	fmt.Fprintln(&out, "}")
	return out.String()
}

func RenderMermaid(report Report) string {
	var out bytes.Buffer
	fmt.Fprintln(&out, "flowchart LR")
	ids := map[string]string{}
	for i, pkg := range report.Packages {
		id := fmt.Sprintf("p%d", i)
		ids[pkg.ImportPath] = id
		fmt.Fprintf(&out, "  %s[%q]\n", id, short(report.ModulePath, pkg.ImportPath))
	}
	for _, edge := range report.Edges {
		arrow := "-->"
		if edge.TestOnly {
			arrow = "-.->"
		}
		fmt.Fprintf(&out, "  %s %s %s\n", ids[edge.From], arrow, ids[edge.To])
	}
	for _, edge := range report.Edges {
		if !edge.Allowed {
			fmt.Fprintf(&out, "  style %s stroke:#d00,stroke-width:2px\n", ids[edge.From])
			fmt.Fprintf(&out, "  style %s stroke:#d00,stroke-width:2px\n", ids[edge.To])
		}
	}
	return out.String()
}

func penaltyLabel(modulePath string, penalty ScorePenalty) string {
	var parts []string
	if penalty.Package != "" {
		parts = append(parts, short(modulePath, penalty.Package))
	} else if penalty.Layer != "" {
		parts = append(parts, string(penalty.Layer))
	}
	if penalty.Threshold > 0 {
		parts = append(parts, fmt.Sprintf("count=%d threshold=%d", penalty.Count, penalty.Threshold))
	} else if penalty.Count > 0 {
		parts = append(parts, fmt.Sprintf("count=%d", penalty.Count))
	}
	if penalty.Reason != "" {
		parts = append(parts, penalty.Reason)
	}
	return strings.Join(parts, "; ")
}

func diagnosticLabel(modulePath string, diagnostic Diagnostic) string {
	var parts []string
	if diagnostic.Package != "" {
		parts = append(parts, short(modulePath, diagnostic.Package))
	}
	if diagnostic.Import != "" {
		parts = append(parts, "import="+short(modulePath, diagnostic.Import))
	}
	if diagnostic.Symbol != "" {
		parts = append(parts, "symbol="+diagnostic.Symbol)
	}
	if diagnostic.File != "" {
		parts = append(parts, "file="+diagnostic.File)
	}
	if diagnostic.TestOnly {
		parts = append(parts, "test")
	}
	if diagnostic.Allowed {
		parts = append(parts, "allowed")
	}
	if diagnostic.Reason != "" {
		parts = append(parts, diagnostic.Reason)
	}
	return strings.Join(parts, "; ")
}

func topFanOut(pkgs []PackageReport, limit int) []PackageReport {
	items := append([]PackageReport(nil), pkgs...)
	sort.Slice(items, func(i, j int) bool {
		if items[i].FanOut == items[j].FanOut {
			return items[i].ImportPath < items[j].ImportPath
		}
		return items[i].FanOut > items[j].FanOut
	})
	return clamp(items, limit)
}

func topFanIn(pkgs []PackageReport, limit int) []PackageReport {
	items := append([]PackageReport(nil), pkgs...)
	sort.Slice(items, func(i, j int) bool {
		if items[i].FanIn == items[j].FanIn {
			return items[i].ImportPath < items[j].ImportPath
		}
		return items[i].FanIn > items[j].FanIn
	})
	return clamp(items, limit)
}

func clamp(items []PackageReport, limit int) []PackageReport {
	if len(items) <= limit {
		return items
	}
	return items[:limit]
}

func short(modulePath, importPath string) string {
	if importPath == modulePath {
		return "."
	}
	return strings.TrimPrefix(importPath, modulePath+"/")
}
