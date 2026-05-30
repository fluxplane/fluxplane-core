package golang

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/fluxplane/codegate"
	codegategolang "github.com/fluxplane/codegate/language/golang"
	corelanguage "github.com/fluxplane/fluxplane-core/core/language"
	"github.com/fluxplane/fluxplane-core/core/language/golang"
	"github.com/fluxplane/fluxplane-core/core/operation"
	operationruntime "github.com/fluxplane/fluxplane-core/runtime/operation"
	runtimeworkspace "github.com/fluxplane/fluxplane-core/runtime/workspace"
	fpsystem "github.com/fluxplane/fluxplane-system"
)

const defaultAssessmentRulesPath = "engine-architecture.rules.json"

func (p Plugin) goAssess(review bool) operationruntime.TypedResultHandler[golang.AssessmentQuery, operation.Rendered] {
	return func(ctx operation.Context, req golang.AssessmentQuery) operation.Result {
		if err := validateGoLanguage(req.Language); err != nil {
			return operation.Failed("invalid_go_assessment_input", err.Error(), nil)
		}
		report, rules, err := p.runCodegateAssessment(ctx, req, review)
		if err != nil {
			return operation.Failed("go_assessment_failed", err.Error(), nil)
		}
		failOn, err := assessmentFailureCategories(req.FailOn)
		if err != nil {
			return operation.Failed("invalid_go_assessment_input", err.Error(), nil)
		}
		view, err := assessmentView(req.View, review)
		if err != nil {
			return operation.Failed("invalid_go_assessment_input", err.Error(), nil)
		}
		result := assessmentResult(report, view, req.SuggestionLimit)
		data := map[string]any{
			"assessment":       result,
			"finding_counts":   result.FindingCounts,
			"violation_counts": result.ViolationCounts,
		}
		if view == golang.AssessmentViewFull {
			data["report"] = report
		}
		text := renderAssessmentText(result)
		if assessmentHasFailures(report, failOn, rules) {
			return operation.Result{
				Status: operation.StatusFailed,
				Output: operation.Rendered{Text: text, Data: data},
				Error: &operation.Error{
					Code:    "go_assessment_gate_failed",
					Message: "codegate assessment failed selected categories\n\n" + text,
					Details: map[string]any{
						"assessment": result,
						"fail_on":    failOn,
					},
				},
			}
		}
		return operation.OK(operation.Rendered{Text: text, Data: data})
	}
}

func (p Plugin) runCodegateAssessment(ctx context.Context, req golang.AssessmentQuery, review bool) (codegate.AssessmentReport, *codegate.ArchitectureRules, error) {
	if p.system == nil || p.system.Workspace() == nil {
		return codegate.AssessmentReport{}, nil, fmt.Errorf("golang: system workspace is nil")
	}
	source := workspaceAssessmentSource{workspace: p.system.Workspace()}
	engine, err := codegate.New().
		Roots(".").
		WithSource(source).
		WithLanguage(codegategolang.New(codegategolang.Config{})).
		Build(ctx)
	if err != nil {
		return codegate.AssessmentReport{}, nil, err
	}
	rules, err := p.assessmentRules(ctx, req.RulesPath)
	if err != nil {
		return codegate.AssessmentReport{}, nil, err
	}
	gates, err := assessmentGates(req.Gates)
	if err != nil {
		return codegate.AssessmentReport{}, nil, err
	}
	limit := req.SuggestionLimit
	if review && limit == 0 {
		limit = 10
	}
	report, err := engine.Assess(ctx, codegate.AssessmentOptions{
		Scope: codegate.Scope{
			Language:         codegate.Go,
			Path:             cleanAssessmentPath(req.Path),
			IncludeTests:     req.IncludeTests,
			IncludeGenerated: req.IncludeGenerated,
		},
		SuggestionLimit: limit,
		Gates:           gates,
		Architecture:    rules,
	})
	if err != nil {
		return codegate.AssessmentReport{}, nil, err
	}
	return report, rules, nil
}

func (p Plugin) assessmentRules(ctx context.Context, rulesPath string) (*codegate.ArchitectureRules, error) {
	rel := cleanAssessmentPath(rulesPath)
	if rel == "" {
		rel = defaultAssessmentRulesPath
	}
	data, truncated, _, err := readWorkspaceFile(ctx, p.system.Workspace(), rel, 0)
	if err != nil {
		if rulesPath == "" {
			return nil, nil
		}
		return nil, fmt.Errorf("read architecture rules %q: %w", rel, err)
	}
	if truncated {
		return nil, fmt.Errorf("architecture rules %q were truncated", rel)
	}
	var rules codegate.ArchitectureRules
	if err := json.Unmarshal(data, &rules); err != nil {
		return nil, fmt.Errorf("parse architecture rules %q: %w", rel, err)
	}
	return &rules, nil
}

func assessmentGates(values []golang.AssessmentGate) ([]codegate.AssessmentGate, error) {
	if len(values) == 0 {
		return []codegate.AssessmentGate{codegate.AssessmentGateAll}, nil
	}
	seen := map[codegate.AssessmentGate]bool{}
	out := make([]codegate.AssessmentGate, 0, len(values))
	for _, value := range values {
		gate := codegate.AssessmentGate(strings.TrimSpace(string(value)))
		if gate == "" {
			continue
		}
		switch gate {
		case codegate.AssessmentGateAll, codegate.AssessmentGateArchitecture, codegate.AssessmentGateMaintainability, codegate.AssessmentGateSafety, codegate.AssessmentGateCoverage:
			if !seen[gate] {
				seen[gate] = true
				out = append(out, gate)
			}
		default:
			return nil, fmt.Errorf("unsupported assessment gate %q", value)
		}
	}
	if len(out) == 0 {
		return []codegate.AssessmentGate{codegate.AssessmentGateAll}, nil
	}
	return out, nil
}

func assessmentFailureCategories(values []golang.AssessmentFailureCategory) ([]string, error) {
	seen := map[string]bool{}
	var out []string
	for _, value := range values {
		part := strings.TrimSpace(string(value))
		if part == "" {
			continue
		}
		if part == string(golang.AssessmentFailureAll) {
			for _, category := range []string{"boundary", "test-boundary", "effects", "unknown"} {
				if !seen[category] {
					seen[category] = true
					out = append(out, category)
				}
			}
			continue
		}
		switch part {
		case "boundary", "test-boundary", "effects", "unknown":
			if !seen[part] {
				seen[part] = true
				out = append(out, part)
			}
		default:
			return nil, fmt.Errorf("unsupported failure category %q", value)
		}
	}
	return out, nil
}

func assessmentHasFailures(report codegate.AssessmentReport, categories []string, rules *codegate.ArchitectureRules) bool {
	if len(categories) == 0 {
		return false
	}
	for _, violation := range report.Violations {
		if violation.Severity != "error" {
			continue
		}
		for _, category := range categories {
			if assessmentViolationMatchesCategory(violation, category, rules) {
				return true
			}
		}
	}
	return false
}

func assessmentViolationMatchesCategory(violation codegate.Violation, category string, rules *codegate.ArchitectureRules) bool {
	switch category {
	case "boundary":
		return violation.Kind == "architecture_boundary_violation" || violation.Kind == "architecture_denied_import"
	case "test-boundary":
		return violation.Kind == "architecture_test_boundary_violation" || violation.Kind == "architecture_test_boundary_import"
	case "effects":
		return assessmentEffectViolation(violation.Kind, rules)
	case "unknown":
		return violation.Kind == "architecture_unknown_package"
	default:
		return false
	}
}

func assessmentEffectViolation(kind string, rules *codegate.ArchitectureRules) bool {
	if strings.HasPrefix(kind, "architecture_effect_") {
		return true
	}
	if rules == nil {
		return false
	}
	for _, rule := range rules.Effects {
		if kind == assessmentEffectKind(rule.Name, "architecture_effect_import") || kind == assessmentEffectKind(rule.Name, "architecture_effect_call") {
			return true
		}
	}
	return false
}

func assessmentEffectKind(name, fallback string) string {
	if name == "" {
		return fallback
	}
	if strings.HasPrefix(name, "architecture_") {
		return name
	}
	return "architecture_" + name
}

func assessmentView(view golang.AssessmentView, review bool) (golang.AssessmentView, error) {
	if view != "" {
		switch view {
		case golang.AssessmentViewSummary, golang.AssessmentViewCompact, golang.AssessmentViewFull:
			return view, nil
		default:
			return "", fmt.Errorf("unsupported assessment view %q", view)
		}
	}
	if review {
		return golang.AssessmentViewCompact, nil
	}
	return golang.AssessmentViewSummary, nil
}

func assessmentResult(report codegate.AssessmentReport, view golang.AssessmentView, suggestionLimit int) golang.AssessmentResult {
	limit := 5
	if view == golang.AssessmentViewFull {
		limit = 0
	}
	if suggestionLimit > 0 && (limit == 0 || suggestionLimit < limit) {
		limit = suggestionLimit
	}
	result := golang.AssessmentResult{
		Root:       report.Root,
		Language:   report.Language,
		View:       view,
		Rating:     assessmentRating(report.Scores.Overall),
		ScoreMax:   100,
		Summary:    assessmentSummary(report.Summary),
		Scores:     assessmentScores(report.Scores),
		Validation: assessmentValidation(report.Validation),
		Suggestions: golang.AssessmentSuggestionSummary{
			Total:      len(report.Suggestions),
			Executable: executableSuggestionCount(report.Suggestions),
		},
		FindingCounts:         assessmentFindingCounts(report.Findings),
		FindingCategoryCounts: assessmentFindingCategoryCounts(report.Findings),
		ViolationCounts:       assessmentViolationCounts(report.Violations),
		TopFindings:           assessmentFindings(report.Findings, limit),
		TopViolations:         assessmentViolations(report.Violations, limit),
		TopUnits:              assessmentUnits(report.TopUnits, limit),
		TopSuggestions:        assessmentSuggestions(report.Suggestions, limit),
	}
	if view != golang.AssessmentViewSummary {
		result.Metrics = compactAssessmentMetrics(report.Metrics)
	}
	if view == golang.AssessmentViewFull {
		result.Metrics = report.Metrics
	}
	return result
}

func assessmentSummary(summary codegate.AssessmentSummary) golang.AssessmentSummary {
	return golang.AssessmentSummary{
		Score:           summary.Score,
		Packages:        summary.Packages,
		Symbols:         summary.Symbols,
		Imports:         summary.Imports,
		Suggestions:     summary.Suggestions,
		ExecutableFixes: summary.ExecutableFixes,
		Findings:        summary.Findings,
		Violations:      summary.Violations,
		Diagnostics:     summary.Diagnostics,
	}
}

func assessmentScores(scores codegate.ScoreSet) golang.AssessmentScores {
	return golang.AssessmentScores{
		Overall:         scores.Overall,
		Boundary:        scores.Boundary,
		TestBoundary:    scores.TestBoundary,
		Coupling:        scores.Coupling,
		SideEffect:      scores.SideEffect,
		Coverage:        scores.Coverage,
		Maintainability: scores.Maintainability,
		Pressure:        scores.Pressure,
	}
}

func assessmentValidation(validation codegate.ValidationSummary) golang.AssessmentValidation {
	return golang.AssessmentValidation{
		Passed:         validation.Passed,
		ResolutionMode: validation.ResolutionMode,
		Diagnostics:    validation.Diagnostics,
		Files:          validation.Files,
		Complete:       validation.Complete,
	}
}

func assessmentFindings(findings []codegate.Finding, limit int) []golang.AssessmentIssue {
	out := make([]golang.AssessmentIssue, 0, len(findings))
	for _, finding := range findings {
		out = append(out, golang.AssessmentIssue{
			Kind:     finding.Kind,
			Severity: finding.Severity,
			Location: assessmentLocation(finding.Location),
			Package:  finding.Package,
			Symbol:   finding.Symbol,
			Allowed:  finding.Allowed,
			Reason:   finding.Reason,
		})
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out
}

func assessmentViolations(violations []codegate.Violation, limit int) []golang.AssessmentIssue {
	out := make([]golang.AssessmentIssue, 0, len(violations))
	for _, violation := range violations {
		out = append(out, golang.AssessmentIssue{
			Kind:     violation.Kind,
			Severity: violation.Severity,
			Location: assessmentLocation(violation.Location),
			Package:  violation.Package,
			Symbol:   violation.Symbol,
			Reason:   violation.Reason,
		})
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out
}

func assessmentUnits(units []codegate.UnitMetrics, limit int) []golang.AssessmentUnit {
	out := make([]golang.AssessmentUnit, 0, len(units))
	for _, unit := range units {
		out = append(out, golang.AssessmentUnit{
			UnitID:        unit.UnitID,
			DirectFanIn:   unit.DirectFanIn,
			DirectFanOut:  unit.DirectFanOut,
			CallFanIn:     unit.CallFanIn,
			CallFanOut:    unit.CallFanOut,
			FileCount:     unit.FileCount,
			LOC:           unit.LOC,
			PressureScore: unit.PressureScore,
		})
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out
}

func assessmentSuggestions(suggestions []codegate.AssessmentSuggestion, limit int) []golang.AssessmentSuggestion {
	out := make([]golang.AssessmentSuggestion, 0, len(suggestions))
	for _, suggestion := range suggestions {
		out = append(out, golang.AssessmentSuggestion{
			ID:         suggestion.ID,
			Kind:       string(suggestion.Kind),
			Title:      suggestion.Title,
			Summary:    suggestion.Summary,
			Confidence: string(suggestion.Confidence),
			Risk:       string(suggestion.Risk),
			Operations: suggestion.Operations,
			Metrics:    suggestion.Metrics,
		})
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out
}

func assessmentLocation(location codegate.Location) corelanguage.Location {
	return corelanguage.Location{
		Path: location.URI,
		Range: corelanguage.Range{
			Start: corelanguage.Position{Line: location.Range.Start.Line, Column: location.Range.Start.Column},
			End:   corelanguage.Position{Line: location.Range.End.Line, Column: location.Range.End.Column},
		},
	}
}

func executableSuggestionCount(suggestions []codegate.AssessmentSuggestion) int {
	count := 0
	for _, suggestion := range suggestions {
		if suggestion.Operations > 0 {
			count++
		}
	}
	return count
}

func assessmentFindingCounts(findings []codegate.Finding) map[string]int {
	out := map[string]int{}
	for _, finding := range findings {
		out[finding.Kind]++
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func assessmentFindingCategoryCounts(findings []codegate.Finding) map[string]int {
	out := map[string]int{}
	for _, finding := range findings {
		out[assessmentFindingCategory(finding.Kind)]++
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func assessmentFindingCategory(kind string) string {
	switch {
	case strings.HasPrefix(kind, "architecture_"):
		return "architecture"
	case strings.HasPrefix(kind, "coverage_"):
		return "coverage"
	case strings.HasPrefix(kind, "performance_"):
		return "performance"
	case strings.HasPrefix(kind, "quality_"), strings.HasPrefix(kind, "maintainability_"):
		return "maintainability"
	case strings.HasPrefix(kind, "safety_"):
		return "safety"
	case strings.HasPrefix(kind, "security_"):
		return "security"
	default:
		return "other"
	}
}

func assessmentViolationCounts(violations []codegate.Violation) map[string]int {
	out := map[string]int{}
	for _, violation := range violations {
		out[violation.Kind]++
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func compactAssessmentMetrics(metrics map[string]any) map[string]any {
	out := map[string]any{}
	for key, value := range metrics {
		switch value.(type) {
		case map[string]int, map[string]any:
			continue
		default:
			out[key] = value
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func assessmentRating(score int) string {
	switch {
	case score >= 98:
		return "A++"
	case score >= 95:
		return "A+"
	case score >= 90:
		return "A"
	case score >= 85:
		return "B++"
	case score >= 80:
		return "B+"
	case score >= 70:
		return "B"
	case score >= 60:
		return "C+"
	case score >= 50:
		return "C"
	case score >= 40:
		return "D+"
	case score >= 30:
		return "D"
	case score >= 20:
		return "F+"
	case score >= 10:
		return "F"
	default:
		return "F--"
	}
}

func renderAssessmentText(result golang.AssessmentResult) string {
	lines := []string{
		fmt.Sprintf("Go assessment: score=%d/100 rating=%s", result.Summary.Score, result.Rating),
		fmt.Sprintf("validation: passed=%t mode=%s diagnostics=%d files=%d", result.Validation.Passed, result.Validation.ResolutionMode, result.Validation.Diagnostics, result.Validation.Files),
		fmt.Sprintf("scores: boundary=%d coupling=%d side_effect=%d coverage=%d maintainability=%d", result.Scores.Boundary, result.Scores.Coupling, result.Scores.SideEffect, result.Scores.Coverage, result.Scores.Maintainability),
		fmt.Sprintf("findings=%d violations=%d suggestions=%d", result.Summary.Findings, result.Summary.Violations, result.Summary.Suggestions),
	}
	for _, issue := range result.TopViolations {
		lines = append(lines, "- violation "+assessmentIssueLine(issue))
	}
	for _, issue := range result.TopFindings {
		lines = append(lines, "- finding "+assessmentIssueLine(issue))
	}
	return strings.Join(lines, "\n")
}

func assessmentIssueLine(issue golang.AssessmentIssue) string {
	parts := []string{issue.Kind}
	if issue.Severity != "" {
		parts = append(parts, "severity="+issue.Severity)
	}
	if issue.Package != "" {
		parts = append(parts, "package="+issue.Package)
	}
	if issue.Location.Path != "" {
		parts = append(parts, fmt.Sprintf("path=%s:%d", issue.Location.Path, issue.Location.Range.Start.Line))
	}
	if issue.Reason != "" {
		parts = append(parts, "reason="+issue.Reason)
	}
	return strings.Join(parts, " ")
}

func cleanAssessmentPath(raw string) string {
	rel := cleanRel(raw)
	if rel == "." {
		return ""
	}
	return rel
}

type workspaceAssessmentSource struct {
	workspace runtimeworkspace.Workspace
}

func (s workspaceAssessmentSource) WorkspaceRoot() string {
	if s.workspace == nil {
		return "."
	}
	return s.workspace.Root()
}

func (s workspaceAssessmentSource) ListFiles(ctx context.Context, scope codegate.Scope) ([]string, error) {
	if s.workspace == nil {
		return nil, fmt.Errorf("workspace is nil")
	}
	root := cleanAssessmentPath(firstNonEmptyAssessment(scope.Path, scope.Root, "."))
	if root == "" {
		root = "."
	}
	entries, _, truncated, err := walkWorkspace(ctx, s.workspace, root, fpsystem.WalkOptions{
		Depth:      50,
		MaxEntries: 20000,
		FilesOnly:  true,
		SkipDirs:   []string{".git", ".agents", "vendor"},
	})
	if err != nil {
		return nil, err
	}
	if truncated {
		return nil, fmt.Errorf("workspace walk truncated at 20000 entries")
	}
	files := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.Kind != "" && entry.Kind != "file" {
			continue
		}
		rel := cleanAssessmentPath(entry.Path.Rel)
		if rel == "" {
			continue
		}
		files = append(files, rel)
	}
	sort.Strings(files)
	return files, nil
}

func (s workspaceAssessmentSource) ReadFile(ctx context.Context, filePath string) ([]byte, error) {
	if s.workspace == nil {
		return nil, fmt.Errorf("workspace is nil")
	}
	rel := cleanAssessmentPath(filePath)
	if rel == "" {
		return nil, fmt.Errorf("file path is empty")
	}
	data, truncated, _, err := readWorkspaceFile(ctx, s.workspace, rel, 0)
	if err != nil {
		return nil, err
	}
	if truncated {
		return nil, fmt.Errorf("%s was truncated", rel)
	}
	return data, nil
}

func firstNonEmptyAssessment(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return "."
}
